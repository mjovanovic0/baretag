package iso

import (
	"encoding/binary"
	"fmt"
	"time"
)

// gptReservedBlocks is the space a GPT needs at the end of the image: one
// block for the backup header and thirty two for its copy of the entry array.
const gptReservedBlocks = 33

// AddFile appends data to the image as a new file in the root directory,
// growing the declared volume and the partition tables to cover it.
//
// Be careful with this. It produces a correct image that boots under OVMF, and
// it has been seen to fail on real server firmware with "no bootable device",
// which is why bake uses AppendTrailer instead. Anything that rewrites a
// partition table needs testing on the hardware it is aimed at, not only in a
// virtual machine. AddFile is kept because it is the right tool when the
// payload has to be visible as a file in the mounted filesystem.
//
// isoName is the ISO9660 name and must follow the 8.3 rules and sort after
// every name already in the root, because records in a directory are required
// to be in order and this one is appended. rrName is the name the file is
// actually opened by, recorded as a Rock Ridge NM entry and in the Joliet tree.
func (im *Image) AddFile(isoName, rrName string, data []byte) error {
	if im.f == nil {
		return fmt.Errorf("image is not open for writing")
	}

	// The data goes in the first sector past the end of the declared volume.
	// What is there now is the padding every ISO carries, plus the backup GPT,
	// which is rewritten at the new end of the image afterwards.
	dataLBA := im.pvd.spaceSize
	sectors := uint32((len(data) + SectorSize - 1) / SectorSize)

	padded := make([]byte, int(sectors)*SectorSize)
	copy(padded, data)
	if _, err := im.f.WriteAt(padded, int64(dataLBA)*SectorSize); err != nil {
		return fmt.Errorf("write file data: %w", err)
	}

	if err := im.insertRecord(im.pvd, isoName, rrName, dataLBA, uint32(len(data))); err != nil {
		return fmt.Errorf("add record to the ISO9660 root directory: %w", err)
	}
	if im.svd != nil {
		if err := im.insertRecord(*im.svd, rrName, rrName, dataLBA, uint32(len(data))); err != nil {
			return fmt.Errorf("add record to the Joliet root directory: %w", err)
		}
	}

	newSpace := dataLBA + sectors
	if err := im.setVolumeSpaceSize(im.pvd.sector, newSpace); err != nil {
		return err
	}
	im.pvd.spaceSize = newSpace
	if im.svd != nil {
		if err := im.setVolumeSpaceSize(im.svd.sector, newSpace); err != nil {
			return err
		}
		im.svd.spaceSize = newSpace
	}

	return im.resize(newSpace)
}

// resize grows the image file to hold the enlarged volume plus whatever the
// partition tables need after it, and brings those tables up to date.
func (im *Image) resize(volumeSectors uint32) error {
	isoBlocks := uint64(volumeSectors) * (SectorSize / blockSize)

	reserved := uint64(0)
	if im.hasGPT() {
		reserved = gptReservedBlocks
	}
	totalBlocks := isoBlocks + reserved
	// Keep the image a whole number of ISO sectors, as it was before.
	if r := totalBlocks % (SectorSize / blockSize); r != 0 {
		totalBlocks += (SectorSize / blockSize) - r
	}

	size := int64(totalBlocks) * blockSize
	if err := im.f.Truncate(size); err != nil {
		return fmt.Errorf("resize image: %w", err)
	}
	im.size = size

	return im.growPartitions(totalBlocks, isoBlocks-1)
}

func (im *Image) hasGPT() bool {
	buf := make([]byte, blockSize)
	if _, err := im.f.ReadAt(buf, gptHeaderLBA*blockSize); err != nil {
		return false
	}
	_, ok := parseGPTHeader(buf)
	return ok
}

// setVolumeSpaceSize writes the volume size, which ISO9660 records twice, once
// in each byte order.
func (im *Image) setVolumeSpaceSize(sector int64, sectors uint32) error {
	var buf [8]byte
	binary.LittleEndian.PutUint32(buf[0:4], sectors)
	binary.BigEndian.PutUint32(buf[4:8], sectors)
	if _, err := im.f.WriteAt(buf[:], sector*SectorSize+80); err != nil {
		return fmt.Errorf("write volume space size: %w", err)
	}
	return nil
}

// insertRecord adds one directory record to a root directory extent, in the
// free space left at the end of a sector.
func (im *Image) insertRecord(d descriptor, isoName, rrName string, lba, size uint32) error {
	rec := buildRecord(d.joliet, isoName, rrName, lba, size)

	dir := make([]byte, d.rootLen)
	if _, err := im.f.ReadAt(dir, int64(d.rootLBA)*SectorSize); err != nil {
		return fmt.Errorf("read root directory: %w", err)
	}

	// Records may not straddle a sector, so each sector is considered on its
	// own and the record goes in the first one with room after its records.
	for base := 0; base < len(dir); base += SectorSize {
		end := base
		for end < base+SectorSize {
			n := int(dir[end])
			if n == 0 {
				break
			}
			end += n
		}
		if base+SectorSize-end < len(rec) {
			continue
		}
		copy(dir[end:], rec)
		if _, err := im.f.WriteAt(dir[base:base+SectorSize], int64(d.rootLBA)*SectorSize+int64(base)); err != nil {
			return fmt.Errorf("write root directory: %w", err)
		}
		return nil
	}
	return fmt.Errorf("no room for another record in the root directory")
}

// buildRecord assembles one ISO9660 directory record for a file.
func buildRecord(joliet bool, isoName, rrName string, lba, size uint32) []byte {
	var name []byte
	if joliet {
		name = encodeUCS2(isoName)
	} else {
		name = []byte(isoName)
	}

	body := make([]byte, 33)
	binary.LittleEndian.PutUint32(body[2:6], lba)
	binary.BigEndian.PutUint32(body[6:10], lba)
	binary.LittleEndian.PutUint32(body[10:14], size)
	binary.BigEndian.PutUint32(body[14:18], size)
	copy(body[18:25], recordingTime(time.Now()))
	body[25] = 0 // a plain file: not a directory, not hidden
	binary.LittleEndian.PutUint16(body[28:30], 1)
	binary.BigEndian.PutUint16(body[30:32], 1)
	body[32] = byte(len(name))

	rec := append(body, name...)
	// The system use area has to start on an even offset.
	if len(name)%2 == 0 {
		rec = append(rec, 0)
	}
	if !joliet {
		rec = append(rec, rockRidgeNM(rrName)...)
	}
	// Every directory record has an even length.
	if len(rec)%2 == 1 {
		rec = append(rec, 0)
	}
	rec[0] = byte(len(rec))
	return rec
}

// rockRidgeNM builds the NM entry that gives a file its real name, which is
// how baretag survives ISO9660's rule that names are eight characters and
// a three character extension.
func rockRidgeNM(name string) []byte {
	out := []byte{'N', 'M', byte(5 + len(name)), 1, 0}
	return append(out, name...)
}

// recordingTime is the seven byte date format directory records use.
func recordingTime(t time.Time) []byte {
	t = t.UTC()
	return []byte{
		byte(t.Year() - 1900),
		byte(t.Month()),
		byte(t.Day()),
		byte(t.Hour()),
		byte(t.Minute()),
		byte(t.Second()),
		0, // offset from GMT in fifteen minute intervals
	}
}
