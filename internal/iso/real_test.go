package iso

import (
	"os"
	"strings"
	"testing"
)

// realISO is a CoreOS live ISO to read. Nothing here writes to it, so the test
// is safe to point at the image a build actually uses.
func realISO(t *testing.T) string {
	t.Helper()
	path := os.Getenv("BARETAG_TEST_ISO")
	if path == "" {
		t.Skip("set BARETAG_TEST_ISO to a CoreOS live ISO to run this")
	}
	if _, err := os.Stat(path); err != nil {
		t.Skipf("%s is not readable: %v", path, err)
	}
	return path
}

// TestReadRealImage checks the reader against a genuine image. The synthetic
// image the other tests use was written to match this code's understanding of
// the format, so on its own it cannot catch a misunderstanding.
func TestReadRealImage(t *testing.T) {
	im, err := Open(realISO(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer im.Close()

	label, err := im.VolumeLabel()
	if err != nil || label == "" {
		t.Fatalf("VolumeLabel() = %q, %v", label, err)
	}
	t.Logf("volume label: %s", label)

	if !im.HasJoliet() {
		t.Error("a CoreOS ISO carries a Joliet tree, but none was found")
	}

	// Lowercase paths only resolve through Rock Ridge, since ISO9660 stores
	// these names uppercase and truncated.
	for _, path := range []string{
		"coreos/igninfo.json",
		"coreos/kargs.json",
		"EFI/redhat/grub.cfg",
		"images/ignition.img",
		"isolinux/isolinux.cfg",
	} {
		if _, err := im.Find(path); err != nil {
			t.Errorf("Find(%q): %v", path, err)
		}
	}

	area, err := im.IgnitionArea()
	if err != nil {
		t.Fatalf("IgnitionArea: %v", err)
	}
	t.Logf("ignition embed area: %d bytes at offset %d", area.Length, area.Offset)
	if area.Length <= 0 {
		t.Error("the embed area has no size")
	}

	kargs, err := im.KernelArgs()
	if err != nil {
		t.Fatalf("KernelArgs: %v", err)
	}
	t.Logf("kernel arguments: %s", kargs)
	if !strings.Contains(kargs, "coreos.liveiso="+label) {
		t.Errorf("kernel arguments %q do not name the volume label %q", kargs, label)
	}

	// This is the real check on the GPT code: every checksum in an untouched
	// image has to recompute exactly.
	if problems := im.ValidatePartitions(); len(problems) != 0 {
		for _, p := range problems {
			t.Errorf("untouched image reported a partition problem: %s", p)
		}
	}
}
