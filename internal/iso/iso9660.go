// Package iso reads and patches a CoreOS live ISO in place.
//
// Nothing here rewrites the filesystem. A new file is appended after the
// existing volume and the handful of fields that describe where the volume
// ends are adjusted to cover it. Everything the machine boots from, the El
// Torito catalogue, the isohybrid boot code, the Rock Ridge and Joliet trees
// and the kernel argument areas, keeps the bytes it already had.
//
// That is a deliberate choice. Repacking the image with a general purpose ISO
// tool is the obvious approach and it is how this started, but a repack
// silently dropped the Joliet tree and left coreos-installer unable to find
// the kernel argument embed areas. Appending cannot do that.
package iso

import (
	"encoding/binary"
	"fmt"
	"os"
	"strings"
)

const (
	// SectorSize is the ISO9660 logical block size. Every CoreOS ISO uses it.
	SectorSize = 2048
	// blockSize is the unit the MBR and GPT count in.
	blockSize = 512

	firstDescriptor = 16 // sector of the primary volume descriptor

	typePrimary       = 1
	typeSupplementary = 2
	typeTerminator    = 255
)

// Image is an opened ISO. Use Open for read only inspection and OpenForWrite
// when the image is going to be patched.
type Image struct {
	f    *os.File
	size int64

	pvd descriptor
	svd *descriptor // the Joliet tree, absent on images that have none
}

// descriptor is one volume descriptor and the root directory it points at.
type descriptor struct {
	sector  int64
	joliet  bool
	rootLBA uint32
	rootLen uint32
	// spaceSize is the number of logical blocks the volume declares.
	spaceSize uint32
}

// Entry is a file or directory found in the image.
type Entry struct {
	Name string
	LBA  uint32
	Size uint32
	Dir  bool
}

// Offset is where the entry's data starts in the image.
func (e Entry) Offset() int64 { return int64(e.LBA) * SectorSize }

func Open(path string) (*Image, error) { return open(path, os.O_RDONLY) }

func OpenForWrite(path string) (*Image, error) { return open(path, os.O_RDWR) }

func open(path string, flag int) (*Image, error) {
	f, err := os.OpenFile(path, flag, 0)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}

	im := &Image{f: f, size: st.Size()}
	if err := im.readDescriptors(); err != nil {
		f.Close()
		return nil, err
	}
	return im, nil
}

func (im *Image) Close() error { return im.f.Close() }

// Size is the length of the image file.
func (im *Image) Size() int64 { return im.size }

// readDescriptors walks the volume descriptor set, keeping the primary
// descriptor and the Joliet supplementary descriptor if there is one.
func (im *Image) readDescriptors() error {
	found := false
	for sector := int64(firstDescriptor); sector < firstDescriptor+32; sector++ {
		buf := make([]byte, SectorSize)
		if _, err := im.f.ReadAt(buf, sector*SectorSize); err != nil {
			return fmt.Errorf("read volume descriptor at sector %d: %w", sector, err)
		}
		if string(buf[1:6]) != "CD001" {
			return fmt.Errorf("sector %d is not a volume descriptor; this is not an ISO9660 image", sector)
		}

		switch buf[0] {
		case typePrimary:
			im.pvd = parseDescriptor(sector, buf, false)
			found = true
		case typeSupplementary:
			// A Joliet descriptor is identified by its escape sequence.
			esc := strings.TrimRight(string(buf[88:120]), "\x00")
			if esc == "%/@" || esc == "%/C" || esc == "%/E" {
				d := parseDescriptor(sector, buf, true)
				im.svd = &d
			}
		case typeTerminator:
			if !found {
				return fmt.Errorf("no primary volume descriptor")
			}
			return nil
		}
	}
	return fmt.Errorf("volume descriptor set has no terminator")
}

func parseDescriptor(sector int64, buf []byte, joliet bool) descriptor {
	root := buf[156 : 156+34]
	return descriptor{
		sector:    sector,
		joliet:    joliet,
		rootLBA:   binary.LittleEndian.Uint32(root[2:6]),
		rootLen:   binary.LittleEndian.Uint32(root[10:14]),
		spaceSize: binary.LittleEndian.Uint32(buf[80:84]),
	}
}

// VolumeLabel is the primary descriptor's volume identifier, which is what a
// coreos.liveiso= kernel argument names and what udev turns into a
// /dev/disk/by-label entry.
func (im *Image) VolumeLabel() (string, error) {
	buf := make([]byte, SectorSize)
	if _, err := im.f.ReadAt(buf, im.pvd.sector*SectorSize); err != nil {
		return "", err
	}
	return strings.TrimRight(string(buf[40:72]), " \x00"), nil
}

// HasJoliet reports whether the image carries a Joliet tree.
func (im *Image) HasJoliet() bool { return im.svd != nil }

// Find resolves a slash separated path against the primary tree, matching the
// Rock Ridge name when a record has one and the plain ISO9660 name otherwise.
func (im *Image) Find(path string) (*Entry, error) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	lba, length := im.pvd.rootLBA, im.pvd.rootLen

	for i, want := range parts {
		entries, err := im.readDir(lba, length, false)
		if err != nil {
			return nil, err
		}
		var hit *Entry
		for j := range entries {
			if strings.EqualFold(entries[j].Name, want) {
				hit = &entries[j]
				break
			}
		}
		if hit == nil {
			return nil, fmt.Errorf("%s: not found in the image", path)
		}
		if i == len(parts)-1 {
			return hit, nil
		}
		if !hit.Dir {
			return nil, fmt.Errorf("%s: %s is not a directory", path, want)
		}
		lba, length = hit.LBA, hit.Size
	}
	return nil, fmt.Errorf("%s: empty path", path)
}

// ReadFile returns the contents of a file in the image.
func (im *Image) ReadFile(path string) ([]byte, error) {
	e, err := im.Find(path)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, e.Size)
	if _, err := im.f.ReadAt(buf, e.Offset()); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return buf, nil
}

// readDir parses the directory records in one directory extent.
func (im *Image) readDir(lba, length uint32, joliet bool) ([]Entry, error) {
	data := make([]byte, length)
	if _, err := im.f.ReadAt(data, int64(lba)*SectorSize); err != nil {
		return nil, fmt.Errorf("read directory at lba %d: %w", lba, err)
	}

	var out []Entry
	for off := 0; off < len(data); {
		recLen := int(data[off])
		if recLen == 0 {
			// Records never straddle a sector, so the rest of this one is
			// padding and the next record starts at the following sector.
			next := (off/SectorSize + 1) * SectorSize
			if next <= off || next >= len(data) {
				break
			}
			off = next
			continue
		}
		if off+recLen > len(data) {
			break
		}
		rec := data[off : off+recLen]
		e, ok := parseRecord(rec, joliet)
		if ok {
			out = append(out, e)
		}
		off += recLen
	}
	return out, nil
}

// parseRecord decodes one directory record. The two records named "\x00" and
// "\x01" are the directory itself and its parent, which are never interesting.
func parseRecord(rec []byte, joliet bool) (Entry, bool) {
	if len(rec) < 33 {
		return Entry{}, false
	}
	nameLen := int(rec[32])
	if 33+nameLen > len(rec) {
		return Entry{}, false
	}
	raw := rec[33 : 33+nameLen]
	if nameLen == 1 && (raw[0] == 0 || raw[0] == 1) {
		return Entry{}, false
	}

	e := Entry{
		LBA:  binary.LittleEndian.Uint32(rec[2:6]),
		Size: binary.LittleEndian.Uint32(rec[10:14]),
		Dir:  rec[25]&0x02 != 0,
	}

	if joliet {
		e.Name = decodeUCS2(raw)
	} else {
		e.Name = strings.TrimSuffix(string(raw), ";1")
		// A Rock Ridge NM entry carries the real name, which is how a file
		// called baretag survives ISO9660's 8.3 name rules.
		if nm := rockRidgeName(rec, nameLen); nm != "" {
			e.Name = nm
		}
	}
	return e, true
}

// rockRidgeName reads the NM entry from a record's system use area.
func rockRidgeName(rec []byte, nameLen int) string {
	start := 33 + nameLen
	if nameLen%2 == 0 {
		start++ // padding byte keeps the system use area even aligned
	}
	if start >= len(rec) {
		return ""
	}

	var name strings.Builder
	su := rec[start:]
	for i := 0; i+4 <= len(su); {
		length := int(su[i+2])
		if length < 4 || i+length > len(su) {
			break
		}
		if su[i] == 'N' && su[i+1] == 'M' && length > 5 {
			name.Write(su[i+5 : i+length])
		}
		i += length
	}
	return name.String()
}

func decodeUCS2(b []byte) string {
	var sb strings.Builder
	for i := 0; i+1 < len(b); i += 2 {
		sb.WriteRune(rune(binary.BigEndian.Uint16(b[i : i+2])))
	}
	return sb.String()
}

func encodeUCS2(s string) []byte {
	out := make([]byte, 0, len(s)*2)
	for _, r := range s {
		out = binary.BigEndian.AppendUint16(out, uint16(r))
	}
	return out
}
