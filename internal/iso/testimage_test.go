package iso

import (
	"encoding/binary"
	"hash/crc32"
	"os"
	"path/filepath"
	"testing"
)

// buildTestISO writes a small but structurally faithful CoreOS style live ISO:
// a primary tree, a Joliet tree, coreos/igninfo.json pointing at an
// images/ignition.img embed area, an isohybrid MBR, and a GPT with a backup at
// the end of the image. It exists so the patching code can be exercised
// without a 1.4 GB file.
func buildTestISO(t *testing.T) string {
	t.Helper()

	const (
		pvdSector     = 16
		svdSector     = 17
		termSector    = 18
		rootSector    = 19
		jrootSector   = 20
		coreosSector  = 21
		imagesSector  = 22
		ignInfoSector = 23
		ignImgSector  = 24
		ignImgSectors = 4
		volumeSectors = ignImgSector + ignImgSectors // 28
	)

	ignInfo := []byte(`{"file": "images/ignition.img"}`)

	// The image is the volume, then whatever the GPT needs after it.
	isoBlocks := volumeSectors * (SectorSize / blockSize)
	totalBlocks := isoBlocks + gptReservedBlocks
	if r := totalBlocks % (SectorSize / blockSize); r != 0 {
		totalBlocks += (SectorSize / blockSize) - r
	}
	img := make([]byte, totalBlocks*blockSize)

	put := func(sector int, b []byte) { copy(img[sector*SectorSize:], b) }

	put(pvdSector, volumeDescriptor(typePrimary, false, rootSector, volumeSectors))
	put(svdSector, volumeDescriptor(typeSupplementary, true, jrootSector, volumeSectors))
	term := make([]byte, SectorSize)
	term[0] = typeTerminator
	copy(term[1:6], "CD001")
	term[6] = 1
	put(termSector, term)

	root := make([]byte, SectorSize)
	n := copy(root, dotRecords(rootSector, SectorSize))
	n += copy(root[n:], dirRecord("COREOS", coreosSector, SectorSize, false))
	copy(root[n:], dirRecord("IMAGES", imagesSector, SectorSize, false))
	put(rootSector, root)

	// The Joliet root has no children; AddFile putting one there is the point.
	jroot := make([]byte, SectorSize)
	copy(jroot, dotRecords(jrootSector, SectorSize))
	put(jrootSector, jroot)

	coreos := make([]byte, SectorSize)
	n = copy(coreos, dotRecords(coreosSector, SectorSize))
	copy(coreos[n:], buildRecord(false, "IGNINFO.JSON;1", "igninfo.json", ignInfoSector, uint32(len(ignInfo))))
	put(coreosSector, coreos)

	images := make([]byte, SectorSize)
	n = copy(images, dotRecords(imagesSector, SectorSize))
	copy(images[n:], buildRecord(false, "IGNITION.IMG;1", "ignition.img", ignImgSector, ignImgSectors*SectorSize))
	put(imagesSector, images)

	put(ignInfoSector, ignInfo)

	writeMBR(img, totalBlocks)
	writeGPT(img, totalBlocks, isoBlocks-1)

	path := filepath.Join(t.TempDir(), "test.iso")
	if err := os.WriteFile(path, img, 0o644); err != nil {
		t.Fatalf("write test image: %v", err)
	}
	return path
}

func volumeDescriptor(kind byte, joliet bool, rootSector, volumeSectors int) []byte {
	b := make([]byte, SectorSize)
	b[0] = kind
	copy(b[1:6], "CD001")
	b[6] = 1
	copy(b[8:40], padTo("QR-TEST", 32))
	copy(b[40:72], padTo("qr-test-label", 32))
	binary.LittleEndian.PutUint32(b[80:84], uint32(volumeSectors))
	binary.BigEndian.PutUint32(b[84:88], uint32(volumeSectors))
	if joliet {
		copy(b[88:120], "%/E")
	}
	binary.LittleEndian.PutUint16(b[120:122], 1)
	binary.BigEndian.PutUint16(b[122:124], 1)
	binary.LittleEndian.PutUint16(b[128:130], SectorSize)
	binary.BigEndian.PutUint16(b[130:132], SectorSize)
	copy(b[156:190], dirRecord("\x00", rootSector, SectorSize, true))
	return b
}

func padTo(s string, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = ' '
	}
	copy(out, s)
	return out
}

// dirRecord builds a directory record for a directory. name "\x00" and "\x01"
// are the self and parent entries every directory begins with.
func dirRecord(name string, sector, size int, self bool) []byte {
	body := make([]byte, 33)
	binary.LittleEndian.PutUint32(body[2:6], uint32(sector))
	binary.BigEndian.PutUint32(body[6:10], uint32(sector))
	binary.LittleEndian.PutUint32(body[10:14], uint32(size))
	binary.BigEndian.PutUint32(body[14:18], uint32(size))
	body[25] = 0x02 // directory
	binary.LittleEndian.PutUint16(body[28:30], 1)
	binary.BigEndian.PutUint16(body[30:32], 1)
	body[32] = byte(len(name))

	rec := append(body, name...)
	if len(rec)%2 == 1 {
		rec = append(rec, 0)
	}
	rec[0] = byte(len(rec))
	return rec
}

func dotRecords(sector, size int) []byte {
	out := dirRecord("\x00", sector, size, true)
	return append(out, dirRecord("\x01", sector, size, true)...)
}

func writeMBR(img []byte, totalBlocks int) {
	img[510], img[511] = 0x55, 0xAA
	e := img[mbrPartitionTable : mbrPartitionTable+mbrEntrySize]
	e[0] = 0x80
	e[4] = 0x00
	binary.LittleEndian.PutUint32(e[8:12], 0)
	binary.LittleEndian.PutUint32(e[12:16], uint32(totalBlocks))
}

func writeGPT(img []byte, totalBlocks, isoLastBlock int) {
	const entryCount, entrySize, entriesLBA = 128, 128, 2
	arrayBytes := entryCount * entrySize
	arrayBlocks := arrayBytes / blockSize

	entries := make([]byte, arrayBytes)
	copy(entries[0:16], []byte{0xeb, 0xd0, 0xa0, 0xa2}) // basic data partition
	copy(entries[16:32], []byte("qr-test-uniqueid"))
	binary.LittleEndian.PutUint64(entries[32:40], 0)
	binary.LittleEndian.PutUint64(entries[40:48], uint64(isoLastBlock))
	copy(entries[56:128], encodeUTF16LE("ISOHybrid ISO"))
	entriesCRC := crc32.ChecksumIEEE(entries)

	header := func(my, alt, entLBA uint64) []byte {
		h := make([]byte, blockSize)
		copy(h[0:8], gptSignature)
		binary.LittleEndian.PutUint32(h[8:12], 0x00010000)
		binary.LittleEndian.PutUint32(h[12:16], gptHeaderBytes)
		binary.LittleEndian.PutUint64(h[24:32], my)
		binary.LittleEndian.PutUint64(h[32:40], alt)
		binary.LittleEndian.PutUint64(h[40:48], uint64(entriesLBA+arrayBlocks))
		binary.LittleEndian.PutUint64(h[48:56], uint64(totalBlocks-1-arrayBlocks-1))
		copy(h[56:72], []byte("qr-test-diskguid"))
		binary.LittleEndian.PutUint64(h[72:80], entLBA)
		binary.LittleEndian.PutUint32(h[80:84], entryCount)
		binary.LittleEndian.PutUint32(h[84:88], entrySize)
		binary.LittleEndian.PutUint32(h[88:92], entriesCRC)
		binary.LittleEndian.PutUint32(h[16:20], crc32.ChecksumIEEE(h[:gptHeaderBytes]))
		return h
	}

	copy(img[gptHeaderLBA*blockSize:], header(gptHeaderLBA, uint64(totalBlocks-1), entriesLBA))
	copy(img[entriesLBA*blockSize:], entries)

	backupEntries := totalBlocks - 1 - arrayBlocks
	copy(img[backupEntries*blockSize:], entries)
	copy(img[(totalBlocks-1)*blockSize:], header(uint64(totalBlocks-1), gptHeaderLBA, uint64(backupEntries)))
}

func encodeUTF16LE(s string) []byte {
	out := make([]byte, 0, len(s)*2)
	for _, r := range s {
		out = binary.LittleEndian.AppendUint16(out, uint16(r))
	}
	return out
}
