// Package bake writes a baretag build into a CoreOS live ISO.
//
// Two things have to reach the booted machine and they travel differently. The
// binary is a few megabytes, far more than the 256 KiB the ISO reserves for an
// Ignition config, so it is appended to the image as an ordinary file. The
// launcher and the systemd unit are small and go into that reserved region,
// where Ignition picks them up on the first boot.
package bake

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mjovanovic/baretag/internal/iso"
)

//go:embed files/baretag-boot
var launcher []byte

//go:embed files/baretag.service
var unit []byte

const (
	launcherPath = "/usr/local/bin/baretag-boot"
	unitName     = "baretag.service"
)

// Options controls one bake.
type Options struct {
	Input  string // the CoreOS live ISO to start from
	Output string // where to write the result
	Binary []byte // a linux/amd64 baretag build
	Kargs  []string
	Force  bool // overwrite an existing output file
}

// Result describes what was written.
type Result struct {
	Output       string
	Size         int64
	Grew         int64
	BinaryBytes  int
	PayloadBytes int
	ConfigBytes  int
	AreaBytes    int64
	Label        string
	KernelArgs   string
}

// Run produces the customised ISO.
func Run(opts Options) (*Result, error) {
	if err := checkInput(opts.Input); err != nil {
		return nil, err
	}
	if len(opts.Binary) == 0 {
		return nil, fmt.Errorf("no binary to bake in")
	}
	if err := checkOutput(opts.Output, opts.Force); err != nil {
		return nil, err
	}

	// The binary is compressed because it is read back with dd at boot, and a
	// smaller read is a faster one on a virtual CD served over the network.
	payload, err := gzipBytes(opts.Binary)
	if err != nil {
		return nil, err
	}

	if err := copyFile(opts.Input, opts.Output); err != nil {
		return nil, err
	}
	// A half written ISO is worse than none, because it looks like a build
	// that worked.
	success := false
	defer func() {
		if !success {
			os.Remove(opts.Output)
		}
	}()

	im, err := iso.OpenForWrite(opts.Output)
	if err != nil {
		return nil, err
	}
	defer im.Close()

	if _, err := im.IgnitionArea(); err != nil {
		return nil, fmt.Errorf("%s is not a CoreOS live ISO: %w", opts.Input, err)
	}

	original := im.Size()
	trailer, err := im.AppendTrailer(payload, uint64(len(opts.Binary)))
	if err != nil {
		return nil, fmt.Errorf("append the binary: %w", err)
	}

	config, err := ignitionConfig(trailer)
	if err != nil {
		return nil, err
	}
	if err := im.EmbedIgnition(config); err != nil {
		return nil, fmt.Errorf("embed the Ignition config: %w", err)
	}
	if err := im.AppendKernelArgs(opts.Kargs); err != nil {
		return nil, fmt.Errorf("set kernel arguments: %w", err)
	}

	area, err := im.IgnitionArea()
	if err != nil {
		return nil, err
	}
	label, _ := im.VolumeLabel()
	kargs, _ := im.KernelArgs()

	res := &Result{
		Output:       opts.Output,
		Size:         im.Size(),
		Grew:         im.Size() - original,
		BinaryBytes:  len(opts.Binary),
		PayloadBytes: len(payload),
		ConfigBytes:  len(config),
		AreaBytes:    area.Length,
		Label:        label,
		KernelArgs:   kargs,
	}
	success = true
	return res, nil
}

func checkInput(path string) error {
	if path == "" {
		return fmt.Errorf("no input ISO given")
	}
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if st.IsDir() {
		return fmt.Errorf("%s is a directory", path)
	}
	return nil
}

func checkOutput(path string, force bool) error {
	if path == "" {
		return fmt.Errorf("no output path given")
	}
	if _, err := os.Stat(path); err == nil && !force {
		return fmt.Errorf("%s already exists; pass --force to overwrite it", path)
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// copyFile duplicates the input ISO, because every change is made in place and
// the original must not be touched. A filesystem that can clone does so, which
// on a gigabyte sized ISO is instant and costs almost no space.
func copyFile(src, dst string) error {
	os.Remove(dst)
	if err := cloneFile(src, dst); err == nil {
		return nil
	}
	return copyBytes(src, dst)
}

func copyBytes(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("copy %s to %s: %w", src, dst, err)
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// Ignition spec 3.4.0, which every CoreOS release that carries this ISO layout
// understands. The structures are written out by hand rather than rendered
// from a Butane file so that baking needs nothing but this binary.
type ignition struct {
	Ignition struct {
		Version string `json:"version"`
	} `json:"ignition"`
	Storage struct {
		Files []ignFile `json:"files,omitempty"`
	} `json:"storage,omitempty"`
	Systemd struct {
		Units []ignUnit `json:"units,omitempty"`
	} `json:"systemd,omitempty"`
}

type ignFile struct {
	Path      string `json:"path"`
	Mode      int    `json:"mode"`
	Overwrite bool   `json:"overwrite"`
	Contents  struct {
		Source string `json:"source"`
	} `json:"contents"`
}

type ignUnit struct {
	Name     string `json:"name"`
	Enabled  bool   `json:"enabled"`
	Contents string `json:"contents"`
}

func ignitionConfig(t *iso.Trailer) ([]byte, error) {
	var cfg ignition
	cfg.Ignition.Version = "3.4.0"

	f := ignFile{Path: launcherPath, Mode: 0o755, Overwrite: true}
	f.Contents.Source = dataURL(launcherFor(t))
	cfg.Storage.Files = append(cfg.Storage.Files, f)

	cfg.Systemd.Units = append(cfg.Systemd.Units, ignUnit{
		Name:     unitName,
		Enabled:  true,
		Contents: string(unit),
	})

	out, err := json.Marshal(&cfg)
	if err != nil {
		return nil, fmt.Errorf("render the Ignition config: %w", err)
	}
	return out, nil
}

// launcherFor fills in where the payload ended up. The launcher is a plain
// file in the repository so it stays readable and testable; only these four
// values depend on the bake.
func launcherFor(t *iso.Trailer) []byte {
	r := strings.NewReplacer(
		"@@PAYLOAD_SKIP@@", strconv.FormatUint(uint64(t.PayloadSector), 10),
		"@@PAYLOAD_COUNT@@", strconv.FormatUint(uint64(t.PayloadSectors), 10),
		"@@PAYLOAD_BYTES@@", strconv.FormatUint(t.PayloadBytes, 10),
		"@@PAYLOAD_SHA256@@", hex.EncodeToString(t.SHA256[:]),
	)
	return []byte(r.Replace(string(launcher)))
}

func gzipBytes(b []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, fmt.Errorf("init gzip: %w", err)
	}
	if _, err := zw.Write(b); err != nil {
		return nil, fmt.Errorf("compress the binary: %w", err)
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("finish gzip: %w", err)
	}
	return buf.Bytes(), nil
}

// dataURL encodes file contents the way Ignition expects them inline. The
// base64 alphabet is left alone: percent escaping it here would leave Ignition
// unable to decode it.
func dataURL(b []byte) string {
	return "data:;base64," + base64.StdEncoding.EncodeToString(b)
}
