package payload

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mjovanovic/baretag/internal/inventory"
)

func sample() *inventory.Inventory {
	return &inventory.Inventory{
		Hostname:  "worker-01",
		Serial:    "JHK3M92",
		SerialSrc: "chassis_serial",
		Vendor:    "Dell Inc.",
		Product:   "PowerEdge R750",
		UUID:      "4c4c4544-0048-4b10-8033-b4c04f4d3932",
		Collected: "2026-09-11T15:04:05Z",
		NICs: []inventory.NIC{
			{Name: "eno1", MAC: "b0:7b:25:1a:2c:3d", Speed: 10000, State: "up", IPs: []string{"10.10.4.21/24", "fd00::21/64"}},
			{Name: "eno2", MAC: "b0:7b:25:1a:2c:3e", State: "down"},
		},
		Disks: []inventory.Disk{
			{Path: "/dev/nvme0n1", Size: 1920383410176, Serial: "S6EWNG0T801234", Model: "SAMSUNG MZQL21T9HCJR"},
			{Path: "/dev/sda", Size: 960197124096},
		},
	}
}

// decodeTo runs a payload back through Decode and parses the readable JSON.
func decodeTo(t *testing.T, parts []string) *inventory.Inventory {
	t.Helper()
	out, err := Decode(parts)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	var got inventory.Inventory
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decoded output is not valid JSON: %v\n%s", err, out)
	}
	return &got
}

func assertSame(t *testing.T, got, want *inventory.Inventory) {
	t.Helper()
	gb, _ := json.Marshal(got)
	wb, _ := json.Marshal(want)
	if string(gb) != string(wb) {
		t.Errorf("round trip lost data:\n got %s\nwant %s", gb, wb)
	}
}

func TestPlainRoundTrip(t *testing.T) {
	want := sample()
	s, err := Plain(want, false)
	if err != nil {
		t.Fatalf("Plain: %v", err)
	}
	assertSame(t, decodeTo(t, []string{s}), want)
}

func TestPackedRoundTrip(t *testing.T) {
	want := sample()
	s, err := Packed(want, false)
	if err != nil {
		t.Fatalf("Packed: %v", err)
	}
	if !strings.HasPrefix(s, PackedPrefix) {
		t.Fatalf("packed payload lacks its prefix: %q", s[:20])
	}
	assertSame(t, decodeTo(t, []string{s}), want)
}

// TestPackedIsAlphanumeric locks in the property that makes the packed payload
// worth using: every character must be in the QR alphanumeric set, or the
// encoder silently falls back to byte mode and the symbol grows.
func TestPackedIsAlphanumeric(t *testing.T) {
	const alphanumeric = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ $%*+-./:"

	s, err := Packed(sample(), false)
	if err != nil {
		t.Fatalf("Packed: %v", err)
	}
	for i, r := range s {
		if !strings.ContainsRune(alphanumeric, r) {
			t.Fatalf("packed payload has non-alphanumeric %q at offset %d", r, i)
		}
	}

	for _, chunk := range Split(s, 3) {
		for i, r := range chunk {
			if !strings.ContainsRune(alphanumeric, r) {
				t.Fatalf("chunk has non-alphanumeric %q at offset %d", r, i)
			}
		}
	}
}

func TestSplitRoundTripInAnyOrder(t *testing.T) {
	want := sample()
	s, err := Packed(want, false)
	if err != nil {
		t.Fatalf("Packed: %v", err)
	}

	parts := Split(s, 3)
	if len(parts) != 3 {
		t.Fatalf("Split returned %d parts, want 3", len(parts))
	}
	// A technician scans whichever symbol is closest first.
	assertSame(t, decodeTo(t, []string{parts[2], parts[0], parts[1]}), want)
}

func TestSplitRejectsMissingChunk(t *testing.T) {
	s, err := Packed(sample(), false)
	if err != nil {
		t.Fatalf("Packed: %v", err)
	}
	parts := Split(s, 3)
	if _, err := Decode(parts[:2]); err == nil {
		t.Fatal("Decode accepted an incomplete set of chunks")
	}
}

func TestMinimalDropsDescriptiveFields(t *testing.T) {
	full, err := Plain(sample(), false)
	if err != nil {
		t.Fatalf("Plain: %v", err)
	}
	minimal, err := Plain(sample(), true)
	if err != nil {
		t.Fatalf("Plain minimal: %v", err)
	}
	if len(minimal) >= len(full) {
		t.Errorf("minimal payload is %d bytes, not smaller than full at %d", len(minimal), len(full))
	}

	got := decodeTo(t, []string{minimal})
	if got.Serial != "JHK3M92" || got.Hostname != "worker-01" {
		t.Errorf("minimal payload lost machine identity: %+v", got)
	}
	if got.Disks[0].Serial != "S6EWNG0T801234" {
		t.Errorf("minimal payload lost a disk serial: %+v", got.Disks[0])
	}
	if got.UUID != "" || got.Disks[0].Model != "" {
		t.Errorf("minimal payload kept descriptive fields: %+v", got)
	}
}
