package iso

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"strings"
)

// The live ISO reserves a fixed region for an Ignition config, described by
// coreos/igninfo.json. The region is listed as an extra initrd on the kernel
// command line, so what goes in it is a compressed cpio archive holding one
// file called config.ign; the initramfs copies that to
// /usr/lib/ignition/user.ign before Ignition runs.
//
// On the ISO this was written against the region is 256 KiB, which is why the
// baretag binary cannot travel in the Ignition config and is appended to
// the image as a file instead.

const ignInfoPath = "coreos/igninfo.json"

type ignInfo struct {
	File   string `json:"file"`
	Offset int64  `json:"offset"`
	Length int64  `json:"length"`
}

// IgnitionArea is where an embedded Ignition config lives in the image.
type IgnitionArea struct {
	Path   string
	Offset int64
	Length int64
}

// IgnitionArea locates the embed region.
func (im *Image) IgnitionArea() (*IgnitionArea, error) {
	raw, err := im.ReadFile(ignInfoPath)
	if err != nil {
		return nil, fmt.Errorf("this does not look like a CoreOS live ISO: %w", err)
	}
	var info ignInfo
	if err := json.Unmarshal(bytes.TrimRight(raw, "\x00"), &info); err != nil {
		return nil, fmt.Errorf("parse %s: %w", ignInfoPath, err)
	}
	if info.File == "" {
		return nil, fmt.Errorf("%s names no file", ignInfoPath)
	}

	entry, err := im.Find(strings.TrimPrefix(info.File, "/"))
	if err != nil {
		return nil, fmt.Errorf("locate %s: %w", info.File, err)
	}

	area := &IgnitionArea{
		Path:   info.File,
		Offset: entry.Offset() + info.Offset,
		Length: int64(entry.Size) - info.Offset,
	}
	if info.Length > 0 {
		area.Length = info.Length
	}
	return area, nil
}

// EmbedIgnition writes an Ignition config into the reserved region, replacing
// anything already there.
func (im *Image) EmbedIgnition(config []byte) error {
	area, err := im.IgnitionArea()
	if err != nil {
		return err
	}

	payload, err := gzipCpio("config.ign", config)
	if err != nil {
		return err
	}
	if int64(len(payload)) > area.Length {
		return fmt.Errorf("Ignition config needs %d bytes but the embed area holds %d",
			len(payload), area.Length)
	}

	// The rest of the region is zeroed. The kernel stops at the end of the
	// compressed stream and ignores the zeros that follow.
	buf := make([]byte, area.Length)
	copy(buf, payload)
	if _, err := im.f.WriteAt(buf, area.Offset); err != nil {
		return fmt.Errorf("write Ignition config: %w", err)
	}
	return nil
}

// ReadIgnition returns the config currently embedded, or nil when the region
// is still blank.
func (im *Image) ReadIgnition() ([]byte, error) {
	area, err := im.IgnitionArea()
	if err != nil {
		return nil, err
	}
	buf := make([]byte, area.Length)
	if _, err := im.f.ReadAt(buf, area.Offset); err != nil {
		return nil, err
	}
	if len(buf) < 2 || buf[0] != 0x1f || buf[1] != 0x8b {
		return nil, nil
	}

	zr, err := gzip.NewReader(bytes.NewReader(buf))
	if err != nil {
		return nil, fmt.Errorf("open embedded archive: %w", err)
	}
	defer zr.Close()
	// The region is zero filled after the archive. Without this the reader
	// treats those zeros as the start of a second gzip member and fails.
	zr.Multistream(false)
	var plain bytes.Buffer
	if _, err := plain.ReadFrom(zr); err != nil {
		return nil, fmt.Errorf("decompress embedded archive: %w", err)
	}
	return cpioFile(plain.Bytes(), "config.ign")
}

// gzipCpio wraps one file in a newc cpio archive and compresses it, which is
// the form the kernel accepts as an appended initrd.
func gzipCpio(name string, data []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, fmt.Errorf("init gzip: %w", err)
	}
	if _, err := zw.Write(cpioArchive(name, data)); err != nil {
		return nil, fmt.Errorf("compress archive: %w", err)
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("finish gzip: %w", err)
	}
	return buf.Bytes(), nil
}

const cpioMagic = "070701"

// cpioArchive builds a newc archive holding a single regular file.
func cpioArchive(name string, data []byte) []byte {
	var out bytes.Buffer
	out.Write(cpioHeader(name, len(data), 0o100644))
	out.Write(data)
	pad(&out, 4)
	out.Write(cpioHeader("TRAILER!!!", 0, 0))
	pad(&out, 4)
	return out.Bytes()
}

func cpioHeader(name string, size int, mode int) []byte {
	fields := []int{
		1,    // inode
		mode, // mode
		0, 0, // uid, gid
		1,          // nlink
		0,          // mtime
		size,       // filesize
		0, 0, 0, 0, // devmajor, devminor, rdevmajor, rdevminor
		len(name) + 1, // namesize, counting the terminator
		0,             // check
	}

	var out bytes.Buffer
	out.WriteString(cpioMagic)
	for _, v := range fields {
		fmt.Fprintf(&out, "%08X", v)
	}
	out.WriteString(name)
	out.WriteByte(0)
	pad(&out, 4)
	return out.Bytes()
}

func pad(buf *bytes.Buffer, to int) {
	for buf.Len()%to != 0 {
		buf.WriteByte(0)
	}
}

// cpioFile pulls one file back out of a newc archive.
func cpioFile(archive []byte, want string) ([]byte, error) {
	off := 0
	for off+110 <= len(archive) {
		if string(archive[off:off+6]) != cpioMagic {
			return nil, fmt.Errorf("not a newc cpio archive at offset %d", off)
		}
		var size, nameSize int
		if _, err := fmt.Sscanf(string(archive[off+54:off+62]), "%08X", &size); err != nil {
			return nil, fmt.Errorf("parse cpio file size: %w", err)
		}
		if _, err := fmt.Sscanf(string(archive[off+94:off+102]), "%08X", &nameSize); err != nil {
			return nil, fmt.Errorf("parse cpio name size: %w", err)
		}

		nameStart := off + 110
		if nameStart+nameSize > len(archive) {
			return nil, fmt.Errorf("truncated cpio archive")
		}
		name := string(archive[nameStart : nameStart+nameSize-1])

		body := roundUp(nameStart+nameSize, 4)
		if name == "TRAILER!!!" {
			return nil, fmt.Errorf("%s is not in the archive", want)
		}
		if name == want {
			if body+size > len(archive) {
				return nil, fmt.Errorf("truncated cpio archive")
			}
			return archive[body : body+size], nil
		}
		off = roundUp(body+size, 4)
	}
	return nil, fmt.Errorf("%s is not in the archive", want)
}

func roundUp(n, to int) int {
	if r := n % to; r != 0 {
		return n + to - r
	}
	return n
}
