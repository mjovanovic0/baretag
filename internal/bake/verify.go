package bake

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/mjovanovic/baretag/internal/iso"
)

// Check is one thing that was looked at.
type Check struct {
	Name   string
	Detail string
	OK     bool
}

// Report is the outcome of verifying an ISO.
type Report struct{ Checks []Check }

func (r *Report) add(name string, ok bool, detail string) {
	r.Checks = append(r.Checks, Check{Name: name, OK: ok, Detail: detail})
}

func (r *Report) Failures() int {
	n := 0
	for _, c := range r.Checks {
		if !c.OK {
			n++
		}
	}
	return n
}

func (r *Report) OK() bool { return r.Failures() == 0 }

// Verify checks that a baked ISO is still a bootable CoreOS image and that
// everything the boot needs is really in it.
//
// The failure this guards against is a quiet one. A damaged ISO looks fine on
// the workstation and only misbehaves once it is attached to a server, where
// finding out costs a reboot.
func Verify(path string) (*Report, error) {
	im, err := iso.Open(path)
	if err != nil {
		return nil, err
	}
	defer im.Close()

	r := &Report{}

	label, err := im.VolumeLabel()
	r.add("volume label is readable", err == nil && label != "", label)

	// coreos.liveiso= names the label, so the initramfs finds its own root
	// filesystem by it. A boot argument naming a different one never mounts.
	kargs, err := im.KernelArgs()
	switch {
	case err != nil:
		r.add("kernel command line is intact", false, err.Error())
	case !strings.Contains(kargs, "coreos.liveiso="):
		r.add("kernel command line is intact", false, kargs)
	default:
		want := "coreos.liveiso=" + label
		r.add("kernel command line is intact", strings.Contains(kargs, want), kargs)
	}

	// A repack that drops the Joliet tree still boots, so nothing notices
	// until a tool that reads it does.
	r.add("Joliet tree is present", im.HasJoliet(), "")

	for _, p := range im.ValidatePartitions() {
		r.add("partition tables are consistent", false, p)
	}
	if len(im.ValidatePartitions()) == 0 {
		r.add("partition tables are consistent",
			true, fmt.Sprintf("MBR and GPT cover %s", humanSize(im.Size())))
	}

	trailer, payload, err := im.ReadTrailer()
	if err != nil {
		r.add("the payload is present and checksums", false, err.Error())
	} else {
		r.add("the payload is present and checksums", true,
			fmt.Sprintf("%s at sector %d, %s unpacked",
				humanSize(int64(trailer.PayloadBytes)), trailer.PayloadSector,
				humanSize(int64(trailer.PlainBytes))))

		// A payload that unpacks to something the machine cannot execute is
		// the failure that would otherwise only appear at boot.
		binary, err := gunzip(payload)
		if err != nil {
			r.add("the payload unpacks", false, err.Error())
		} else {
			r.add("the payload unpacks", true, fmt.Sprintf("%d bytes", len(binary)))
			r.add("it unpacks to a linux/amd64 binary", CheckELF(binary) == nil, "")
		}

		// The launcher reads the payload by offset, so the number baked into
		// it has to be the one the payload actually sits at.
		want := fmt.Sprintf("payload_skip=%d", trailer.PayloadSector)
		cfg, _ := im.ReadIgnition()
		r.add("the launcher points at the payload", bytes.Contains(cfg, []byte(want)) ||
			configMentions(cfg, want), want)
	}

	config, err := im.ReadIgnition()
	switch {
	case err != nil:
		r.add("the Ignition config reads back", false, err.Error())
	case config == nil:
		r.add("the Ignition config reads back", false, "the embed area is blank")
	default:
		r.add("the Ignition config reads back", true, fmt.Sprintf("%d bytes", len(config)))
		var parsed map[string]any
		r.add("the Ignition config is valid JSON", json.Unmarshal(config, &parsed) == nil, "")
		r.add("it installs the launcher", bytes.Contains(config, []byte(launcherPath)), launcherPath)
		r.add("it enables the unit", bytes.Contains(config, []byte(unitName)), unitName)
	}

	return r, nil
}

// VerifyAgainst adds the check that matters most: that baking left every byte
// of the original image alone apart from the Ignition region. Firmware is
// unforgiving about partition tables and volume descriptors, so the strongest
// thing that can be said about an image is that they were never touched.
func VerifyAgainst(output, original string) (*Report, error) {
	r, err := Verify(output)
	if err != nil {
		return nil, err
	}

	changed, err := changedRegions(original, output)
	if err != nil {
		r.add("the original image is untouched", false, err.Error())
		return r, nil
	}

	im, err := iso.Open(output)
	if err != nil {
		return nil, err
	}
	defer im.Close()
	// The regions a bake is allowed to write: the Ignition config, and the
	// kernel command line when --karg was used. Everything else, above all the
	// partition tables and volume descriptors, has to be untouched.
	area, err := im.IgnitionArea()
	if err != nil {
		return nil, err
	}
	allowed := []iso.Region{{Path: area.Path, Offset: area.Offset, Length: area.Length}}
	if kargs, err := im.KargAreas(); err == nil {
		allowed = append(allowed, kargs...)
	}

	var outside, touched []string
	for _, c := range changed {
		where := within(c, allowed)
		if where == "" {
			outside = append(outside, fmt.Sprintf("%d..%d", c.from, c.to))
			continue
		}
		if !slices.Contains(touched, where) {
			touched = append(touched, where)
		}
	}
	if len(outside) == 0 {
		detail := "nothing in it changed at all, and the payload was appended"
		if len(touched) > 0 {
			detail = "only " + strings.Join(touched, " and ") + " changed, plus the appended payload"
		}
		r.add("the original image is untouched", true, detail)
	} else {
		r.add("the original image is untouched", false,
			"changed outside the regions a bake may write: "+strings.Join(outside, " "))
	}
	return r, nil
}

// within reports which writable region a change falls in, or "" when it falls
// outside all of them.
func within(c region, allowed []iso.Region) string {
	for _, a := range allowed {
		if c.from >= a.Offset && c.to <= a.Offset+a.Length {
			return a.Path
		}
	}
	return ""
}

type region struct{ from, to int64 }

// changedRegions compares the two images over the length of the original.
func changedRegions(original, output string) ([]region, error) {
	a, err := os.Open(original)
	if err != nil {
		return nil, err
	}
	defer a.Close()
	b, err := os.Open(output)
	if err != nil {
		return nil, err
	}
	defer b.Close()

	st, err := a.Stat()
	if err != nil {
		return nil, err
	}

	const chunk = 1 << 20
	bufA := make([]byte, chunk)
	bufB := make([]byte, chunk)
	var out []region
	for off := int64(0); off < st.Size(); off += chunk {
		n, err := io.ReadFull(a, bufA)
		if n == 0 {
			break
		}
		if err != nil && err != io.ErrUnexpectedEOF {
			return nil, err
		}
		if _, err := io.ReadFull(b, bufB[:n]); err != nil && err != io.ErrUnexpectedEOF {
			return nil, err
		}
		for i := 0; i < n; {
			if bufA[i] == bufB[i] {
				i++
				continue
			}
			j := i
			for j < n && bufA[j] != bufB[j] {
				j++
			}
			from, to := off+int64(i), off+int64(j)
			if len(out) > 0 && from-out[len(out)-1].to < 4096 {
				out[len(out)-1].to = to
			} else {
				out = append(out, region{from, to})
			}
			i = j
		}
	}
	return out, nil
}

func gunzip(b []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	var out bytes.Buffer
	if _, err := out.ReadFrom(zr); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// configMentions looks for a value inside the base64 launcher in the config.
func configMentions(cfg []byte, want string) bool {
	const marker = "data:;base64,"
	i := bytes.Index(cfg, []byte(marker))
	if i < 0 {
		return false
	}
	rest := cfg[i+len(marker):]
	j := bytes.IndexAny(rest, `"`)
	if j < 0 {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(string(rest[:j]))
	if err != nil {
		return false
	}
	return bytes.Contains(decoded, []byte(want))
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
