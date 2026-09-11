package iso

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"hash/crc32"
	"os"
	"testing"
)

func TestReadSyntheticImage(t *testing.T) {
	im, err := Open(buildTestISO(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer im.Close()

	if label, err := im.VolumeLabel(); err != nil || label != "qr-test-label" {
		t.Errorf("VolumeLabel() = %q, %v; want qr-test-label", label, err)
	}
	if !im.HasJoliet() {
		t.Error("HasJoliet() = false, want true")
	}

	// Rock Ridge names are how a lookup of a lowercase path succeeds against
	// an 8.3 ISO9660 name.
	raw, err := im.ReadFile("coreos/igninfo.json")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Contains(raw, []byte("images/ignition.img")) {
		t.Errorf("igninfo.json = %q", raw)
	}

	area, err := im.IgnitionArea()
	if err != nil {
		t.Fatalf("IgnitionArea: %v", err)
	}
	if area.Length != 4*SectorSize {
		t.Errorf("embed area is %d bytes, want %d", area.Length, 4*SectorSize)
	}
}

func TestEmbedIgnitionRoundTrip(t *testing.T) {
	path := buildTestISO(t)
	im, err := OpenForWrite(path)
	if err != nil {
		t.Fatalf("OpenForWrite: %v", err)
	}
	defer im.Close()

	if got, err := im.ReadIgnition(); err != nil || got != nil {
		t.Errorf("a fresh image should have no config; got %q, %v", got, err)
	}

	config := []byte(`{"ignition":{"version":"3.4.0"},"storage":{"files":[]}}`)
	if err := im.EmbedIgnition(config); err != nil {
		t.Fatalf("EmbedIgnition: %v", err)
	}
	got, err := im.ReadIgnition()
	if err != nil {
		t.Fatalf("ReadIgnition: %v", err)
	}
	if !bytes.Equal(got, config) {
		t.Errorf("round trip returned %q, want %q", got, config)
	}
}

func TestEmbedIgnitionRejectsOversizedConfig(t *testing.T) {
	im, err := OpenForWrite(buildTestISO(t))
	if err != nil {
		t.Fatalf("OpenForWrite: %v", err)
	}
	defer im.Close()

	// The data has to be incompressible, or gzip shrinks it back into the
	// area and the check under test never fires.
	big := make([]byte, 64*1024)
	if _, err := rand.Read(big); err != nil {
		t.Fatal(err)
	}
	if err := im.EmbedIgnition(big); err == nil {
		t.Error("EmbedIgnition accepted a config larger than the embed area")
	}
}

func TestAddFile(t *testing.T) {
	path := buildTestISO(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	payload := bytes.Repeat([]byte("baretag payload "), 500) // ~10 KB
	im, err := OpenForWrite(path)
	if err != nil {
		t.Fatalf("OpenForWrite: %v", err)
	}
	oldVolumeEnd := int64(im.pvd.spaceSize) * SectorSize
	if err := im.AddFile("QRINV.BIN;1", "baretag", payload); err != nil {
		t.Fatalf("AddFile: %v", err)
	}
	im.Close()

	// Reopen and read the file back through the normal lookup path.
	im, err = Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer im.Close()

	got, err := im.ReadFile("baretag")
	if err != nil {
		t.Fatalf("ReadFile after AddFile: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("read back %d bytes, want %d", len(got), len(payload))
	}

	// The file has to be inside the declared volume, or a reader that trusts
	// the volume size will not see it.
	entry, err := im.Find("baretag")
	if err != nil {
		t.Fatal(err)
	}
	end := entry.Offset() + int64(entry.Size)
	if volEnd := int64(im.pvd.spaceSize) * SectorSize; end > volEnd {
		t.Errorf("file ends at %d, past the declared volume end %d", end, volEnd)
	}
	if im.svd != nil && im.svd.spaceSize != im.pvd.spaceSize {
		t.Errorf("Joliet volume size %d does not match the primary %d", im.svd.spaceSize, im.pvd.spaceSize)
	}

	// Everything that was already in the image has to be byte for byte intact,
	// because that is the whole reason for appending rather than repacking.
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	assertUntouched(t, before, after, oldVolumeEnd)
	checkGPT(t, path)
}

// assertUntouched compares the parts of the image that carry boot code and
// file data. Only the volume descriptors, the two root directories and the
// partition tables are allowed to differ.
func assertUntouched(t *testing.T, before, after []byte, oldVolumeEnd int64) {
	t.Helper()

	allowed := []struct {
		name     string
		from, to int64
	}{
		{"MBR partition table", mbrPartitionTable, mbrPartitionTable + 4*mbrEntrySize},
		{"GPT header and entries", blockSize, blockSize + 33*blockSize},
		{"primary volume descriptor", 16 * SectorSize, 17 * SectorSize},
		{"Joliet volume descriptor", 17 * SectorSize, 18 * SectorSize},
		{"primary root directory", 19 * SectorSize, 20 * SectorSize},
		{"Joliet root directory", 20 * SectorSize, 21 * SectorSize},
	}
	isAllowed := func(i int64) bool {
		for _, a := range allowed {
			if i >= a.from && i < a.to {
				return true
			}
		}
		return false
	}

	for i := int64(0); i < oldVolumeEnd && i < int64(len(before)); i++ {
		if before[i] != after[i] && !isAllowed(i) {
			t.Fatalf("byte %d changed (sector %d, offset %d in it) but is not in a region this may touch",
				i, i/SectorSize, i%SectorSize)
		}
	}
}

// checkGPT re-reads both GPT copies and recomputes every checksum, which is
// the part of the image most easily left subtly wrong.
func checkGPT(t *testing.T, path string) {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	totalBlocks := int64(len(raw) / blockSize)

	verify := func(what string, at int64) *gptHeader {
		h, ok := parseGPTHeader(raw[at : at+blockSize])
		if !ok {
			t.Fatalf("%s: no GPT signature at block %d", what, at/blockSize)
		}
		hdr := make([]byte, h.headerSize)
		copy(hdr, raw[at:at+int64(h.headerSize)])
		want := binary.LittleEndian.Uint32(hdr[16:20])
		binary.LittleEndian.PutUint32(hdr[16:20], 0)
		if got := crc32.ChecksumIEEE(hdr); got != want {
			t.Errorf("%s: header CRC is %#x, recomputes to %#x", what, want, got)
		}

		start := int64(h.entriesLBA) * blockSize
		size := int64(h.entryCount) * int64(h.entrySize)
		if start+size > int64(len(raw)) {
			t.Fatalf("%s: entry array at block %d runs past the end of the image", what, h.entriesLBA)
		}
		if got := crc32.ChecksumIEEE(raw[start : start+size]); got != binary.LittleEndian.Uint32(h.raw[88:92]) {
			t.Errorf("%s: entry array CRC mismatch", what)
		}
		return h
	}

	primary := verify("primary GPT", gptHeaderLBA*blockSize)
	backup := verify("backup GPT", (totalBlocks-1)*blockSize)

	if primary.alternateLBA != uint64(totalBlocks-1) {
		t.Errorf("primary alternate_lba is %d, want the last block %d", primary.alternateLBA, totalBlocks-1)
	}
	if backup.myLBA != uint64(totalBlocks-1) || backup.alternateLBA != gptHeaderLBA {
		t.Errorf("backup header points the wrong way: my=%d alternate=%d", backup.myLBA, backup.alternateLBA)
	}
	if primary.lastUsableLBA >= backup.entriesLBA {
		t.Errorf("last usable block %d overlaps the backup entry array at %d",
			primary.lastUsableLBA, backup.entriesLBA)
	}

	// The MBR partition that starts at block zero has to span the image.
	count := binary.LittleEndian.Uint32(raw[mbrPartitionTable+12 : mbrPartitionTable+16])
	if int64(count) != totalBlocks {
		t.Errorf("MBR partition covers %d blocks, image is %d", count, totalBlocks)
	}

	// And the entry covering the ISO must now reach the end of the volume.
	entries := raw[int64(primary.entriesLBA)*blockSize:]
	if last := binary.LittleEndian.Uint64(entries[40:48]); last < 100 {
		t.Errorf("ISO partition entry still ends at block %d", last)
	}
}
