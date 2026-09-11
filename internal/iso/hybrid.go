package iso

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
)

// A CoreOS live ISO is an isohybrid image: as well as the ISO9660 filesystem it
// carries an MBR and a GPT so the same file works when written to a USB stick.
// Both describe how far the image extends, so both have to be told when it
// grows. Getting this wrong is invisible on a virtual CD, where firmware boots
// through El Torito and never reads a partition table, and shows up only on a
// USB stick.

const (
	mbrPartitionTable = 446
	mbrPartitionCount = 4
	mbrEntrySize      = 16

	gptSignature   = "EFI PART"
	gptHeaderLBA   = 1
	gptHeaderBytes = 92
)

// gptHeader is the subset of the GPT header this code reads or writes.
type gptHeader struct {
	raw            []byte
	headerSize     uint32
	myLBA          uint64
	alternateLBA   uint64
	firstUsableLBA uint64
	lastUsableLBA  uint64
	entriesLBA     uint64
	entryCount     uint32
	entrySize      uint32
}

func parseGPTHeader(buf []byte) (*gptHeader, bool) {
	if len(buf) < gptHeaderBytes || string(buf[0:8]) != gptSignature {
		return nil, false
	}
	return &gptHeader{
		raw:            buf,
		headerSize:     binary.LittleEndian.Uint32(buf[12:16]),
		myLBA:          binary.LittleEndian.Uint64(buf[24:32]),
		alternateLBA:   binary.LittleEndian.Uint64(buf[32:40]),
		firstUsableLBA: binary.LittleEndian.Uint64(buf[40:48]),
		lastUsableLBA:  binary.LittleEndian.Uint64(buf[48:56]),
		entriesLBA:     binary.LittleEndian.Uint64(buf[72:80]),
		entryCount:     binary.LittleEndian.Uint32(buf[80:84]),
		entrySize:      binary.LittleEndian.Uint32(buf[84:88]),
	}, true
}

// seal writes the derived fields back and recomputes the header checksum,
// which must be calculated with the checksum field itself zeroed.
func (h *gptHeader) seal(entriesCRC uint32) []byte {
	buf := make([]byte, len(h.raw))
	copy(buf, h.raw)

	binary.LittleEndian.PutUint64(buf[24:32], h.myLBA)
	binary.LittleEndian.PutUint64(buf[32:40], h.alternateLBA)
	binary.LittleEndian.PutUint64(buf[40:48], h.firstUsableLBA)
	binary.LittleEndian.PutUint64(buf[48:56], h.lastUsableLBA)
	binary.LittleEndian.PutUint64(buf[72:80], h.entriesLBA)
	binary.LittleEndian.PutUint32(buf[88:92], entriesCRC)

	size := int(h.headerSize)
	if size < gptHeaderBytes || size > len(buf) {
		size = gptHeaderBytes
	}
	binary.LittleEndian.PutUint32(buf[16:20], 0)
	binary.LittleEndian.PutUint32(buf[16:20], crc32.ChecksumIEEE(buf[:size]))
	return buf
}

// growPartitions tells the MBR and the GPT that the image now runs to
// totalBlocks, and that the ISO9660 volume inside it now ends at isoLastBlock.
// Both are counts of 512 byte blocks.
func (im *Image) growPartitions(totalBlocks, isoLastBlock uint64) error {
	if err := im.growMBR(totalBlocks, isoLastBlock); err != nil {
		return err
	}
	return im.growGPT(totalBlocks, isoLastBlock)
}

func (im *Image) growMBR(totalBlocks, isoLastBlock uint64) error {
	mbr := make([]byte, blockSize)
	if _, err := im.f.ReadAt(mbr, 0); err != nil {
		return fmt.Errorf("read MBR: %w", err)
	}
	if mbr[510] != 0x55 || mbr[511] != 0xAA {
		return nil // not an isohybrid image, nothing to keep in step
	}

	for i := 0; i < mbrPartitionCount; i++ {
		off := mbrPartitionTable + i*mbrEntrySize
		entry := mbr[off : off+mbrEntrySize]
		start := binary.LittleEndian.Uint32(entry[8:12])
		count := binary.LittleEndian.Uint32(entry[12:16])
		if count == 0 {
			continue
		}
		// The partition that starts at block zero is the image itself. The
		// others describe things inside it, such as the EFI system partition,
		// and their extents have not moved.
		if start == 0 {
			if uint64(uint32(totalBlocks)) != totalBlocks {
				return fmt.Errorf("image is too large for an MBR partition entry")
			}
			binary.LittleEndian.PutUint32(entry[12:16], uint32(totalBlocks))
		}
	}

	if _, err := im.f.WriteAt(mbr, 0); err != nil {
		return fmt.Errorf("write MBR: %w", err)
	}
	return nil
}

func (im *Image) growGPT(totalBlocks, isoLastBlock uint64) error {
	buf := make([]byte, blockSize)
	if _, err := im.f.ReadAt(buf, gptHeaderLBA*blockSize); err != nil {
		return fmt.Errorf("read GPT header: %w", err)
	}
	head, ok := parseGPTHeader(buf)
	if !ok {
		return nil // no GPT on this image
	}
	if head.entryCount == 0 || head.entrySize < 128 {
		return fmt.Errorf("GPT header declares %d entries of %d bytes", head.entryCount, head.entrySize)
	}

	arrayBytes := int(head.entryCount) * int(head.entrySize)
	entries := make([]byte, arrayBytes)
	if _, err := im.f.ReadAt(entries, int64(head.entriesLBA)*blockSize); err != nil {
		return fmt.Errorf("read GPT entries: %w", err)
	}

	// Extend the entry that covers the ISO9660 volume. It is the one starting
	// at block zero; the EFI system partition sits inside it and is unchanged.
	for i := 0; i < int(head.entryCount); i++ {
		e := entries[i*int(head.entrySize) : (i+1)*int(head.entrySize)]
		if isZero(e[0:16]) {
			continue
		}
		if binary.LittleEndian.Uint64(e[32:40]) == 0 {
			binary.LittleEndian.PutUint64(e[40:48], isoLastBlock)
		}
	}
	entriesCRC := crc32.ChecksumIEEE(entries)

	arrayBlocks := uint64((arrayBytes + blockSize - 1) / blockSize)
	backupEntriesLBA := totalBlocks - 1 - arrayBlocks
	lastUsable := backupEntriesLBA - 1

	// Primary header and its copy of the entry array stay where they are.
	head.alternateLBA = totalBlocks - 1
	head.lastUsableLBA = lastUsable
	if _, err := im.f.WriteAt(entries, int64(head.entriesLBA)*blockSize); err != nil {
		return fmt.Errorf("write GPT entries: %w", err)
	}
	if _, err := im.f.WriteAt(head.seal(entriesCRC), gptHeaderLBA*blockSize); err != nil {
		return fmt.Errorf("write GPT header: %w", err)
	}

	// The backup lives at the very end of the image, so growing the image
	// means moving it. A stale backup left in the middle is the kind of thing
	// that boots fine until the day firmware decides to prefer it.
	backup := *head
	backup.myLBA = totalBlocks - 1
	backup.alternateLBA = gptHeaderLBA
	backup.entriesLBA = backupEntriesLBA
	if _, err := im.f.WriteAt(entries, int64(backupEntriesLBA)*blockSize); err != nil {
		return fmt.Errorf("write backup GPT entries: %w", err)
	}
	if _, err := im.f.WriteAt(backup.seal(entriesCRC), int64(totalBlocks-1)*blockSize); err != nil {
		return fmt.Errorf("write backup GPT header: %w", err)
	}
	return nil
}

func isZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}

// ValidatePartitions re-reads the MBR and both GPT copies and recomputes every
// checksum, returning a description of anything wrong.
//
// The tables are checked against the extent they declare themselves, not
// against the size of the file. That distinction matters here: a payload
// appended by AppendTrailer sits deliberately outside them, so a file longer
// than the declared image is expected and is not a fault.
func (im *Image) ValidatePartitions() []string {
	var problems []string

	fileBlocks := im.size / blockSize
	mbr := make([]byte, blockSize)
	if _, err := im.f.ReadAt(mbr, 0); err != nil {
		return []string{fmt.Sprintf("cannot read the MBR: %v", err)}
	}
	if mbr[510] != 0x55 || mbr[511] != 0xAA {
		return nil // not an isohybrid image
	}

	primary, err := im.readGPT(gptHeaderLBA)
	if err != nil {
		return append(problems, err.Error())
	}
	if primary == nil {
		return problems // an MBR only image; there is nothing else to cross check
	}

	// The image the tables describe ends where the primary header says its
	// backup lives.
	declaredEnd := primary.alternateLBA
	if declaredEnd >= uint64(fileBlocks) {
		problems = append(problems, fmt.Sprintf(
			"the GPT places its backup at block %d, past the end of the file at %d",
			declaredEnd, fileBlocks-1))
		return problems
	}

	backup, err := im.readGPT(int64(declaredEnd))
	if err != nil {
		return append(problems, fmt.Sprintf("backup GPT: %v", err))
	}
	if backup == nil {
		return append(problems, fmt.Sprintf("no backup GPT at block %d", declaredEnd))
	}
	if backup.alternateLBA != gptHeaderLBA {
		problems = append(problems, "the backup GPT does not point back at the primary")
	}
	if primary.lastUsableLBA >= backup.entriesLBA {
		problems = append(problems, fmt.Sprintf(
			"the last usable block %d overlaps the backup entry array at %d",
			primary.lastUsableLBA, backup.entriesLBA))
	}

	for i := 0; i < mbrPartitionCount; i++ {
		off := mbrPartitionTable + i*mbrEntrySize
		start := binary.LittleEndian.Uint32(mbr[off+8 : off+12])
		count := binary.LittleEndian.Uint32(mbr[off+12 : off+16])
		if count == 0 || start != 0 {
			continue
		}
		if uint64(count) != declaredEnd+1 {
			problems = append(problems, fmt.Sprintf(
				"the MBR partition covers %d blocks but the GPT describes %d",
				count, declaredEnd+1))
		}
	}
	return problems
}

// readGPT loads and checksums one GPT copy. It returns nil without an error
// when there is simply no GPT there.
func (im *Image) readGPT(lba int64) (*gptHeader, error) {
	buf := make([]byte, blockSize)
	if _, err := im.f.ReadAt(buf, lba*blockSize); err != nil {
		return nil, fmt.Errorf("read block %d: %w", lba, err)
	}
	head, ok := parseGPTHeader(buf)
	if !ok {
		return nil, nil
	}

	size := int(head.headerSize)
	if size < gptHeaderBytes || size > len(buf) {
		return nil, fmt.Errorf("GPT at block %d declares a header of %d bytes", lba, size)
	}
	hdr := make([]byte, size)
	copy(hdr, buf[:size])
	want := binary.LittleEndian.Uint32(hdr[16:20])
	binary.LittleEndian.PutUint32(hdr[16:20], 0)
	if got := crc32.ChecksumIEEE(hdr); got != want {
		return nil, fmt.Errorf("GPT at block %d has header checksum %#x but recomputes to %#x", lba, want, got)
	}

	entries := make([]byte, int(head.entryCount)*int(head.entrySize))
	if _, err := im.f.ReadAt(entries, int64(head.entriesLBA)*blockSize); err != nil {
		return nil, fmt.Errorf("read the entry array of the GPT at block %d: %w", lba, err)
	}
	if got := crc32.ChecksumIEEE(entries); got != binary.LittleEndian.Uint32(buf[88:92]) {
		return nil, fmt.Errorf("the entry array of the GPT at block %d does not match its checksum", lba)
	}
	return head, nil
}
