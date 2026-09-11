package bake

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mjovanovic/baretag/internal/iso"
)

func testTrailer() *iso.Trailer {
	t := &iso.Trailer{PayloadSector: 690577, PayloadSectors: 621, PayloadBytes: 1270000, PlainBytes: 3125410}
	for i := range t.SHA256 {
		t.SHA256[i] = byte(i)
	}
	return t
}

func TestIgnitionConfig(t *testing.T) {
	trailer := testTrailer()
	raw, err := ignitionConfig(trailer)
	if err != nil {
		t.Fatalf("ignitionConfig: %v", err)
	}

	var cfg struct {
		Ignition struct {
			Version string `json:"version"`
		} `json:"ignition"`
		Storage struct {
			Files []struct {
				Path      string `json:"path"`
				Mode      int    `json:"mode"`
				Overwrite bool   `json:"overwrite"`
				Contents  struct {
					Source string `json:"source"`
				} `json:"contents"`
			} `json:"files"`
		} `json:"storage"`
		Systemd struct {
			Units []struct {
				Name     string `json:"name"`
				Enabled  bool   `json:"enabled"`
				Contents string `json:"contents"`
			} `json:"units"`
		} `json:"systemd"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("the config is not valid JSON: %v\n%s", err, raw)
	}

	if cfg.Ignition.Version != "3.4.0" {
		t.Errorf("spec version is %q, want 3.4.0", cfg.Ignition.Version)
	}
	if len(cfg.Storage.Files) != 1 {
		t.Fatalf("config writes %d files, want 1", len(cfg.Storage.Files))
	}

	f := cfg.Storage.Files[0]
	if f.Path != launcherPath {
		t.Errorf("launcher path is %q, want %q", f.Path, launcherPath)
	}
	// 0755 as a decimal, which is how Ignition reads it. A mode of 0644 here
	// produces an ISO that boots and then cannot run its own launcher.
	if f.Mode != 0o755 {
		t.Errorf("launcher mode is %o, want 755", f.Mode)
	}
	if !f.Overwrite {
		t.Error("launcher is not marked overwrite")
	}

	// The contents have to survive the data URL round trip exactly, or the
	// launcher arrives corrupt and the failure only shows up at boot.
	encoded, ok := strings.CutPrefix(f.Contents.Source, "data:;base64,")
	if !ok {
		t.Fatalf("contents source is %q, want a base64 data URL", truncate(f.Contents.Source))
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode the launcher: %v", err)
	}
	if string(decoded) != string(launcherFor(trailer)) {
		t.Error("the launcher does not survive the data URL round trip")
	}
	if !strings.HasPrefix(string(decoded), "#!/usr/bin/bash") {
		t.Errorf("the launcher has no interpreter line: %q", truncate(string(decoded)))
	}

	// Every placeholder has to be filled in. One left behind would produce a
	// launcher that fails at boot with a shell syntax error.
	if strings.Contains(string(decoded), "@@") {
		t.Errorf("the launcher still has an unfilled placeholder")
	}
	for _, want := range []string{
		"payload_skip=690577",
		"payload_count=621",
		"payload_bytes=1270000",
		"payload_sha=000102030405",
	} {
		if !strings.Contains(string(decoded), want) {
			t.Errorf("the launcher does not carry %q", want)
		}
	}

	if len(cfg.Systemd.Units) != 1 {
		t.Fatalf("config defines %d units, want 1", len(cfg.Systemd.Units))
	}
	u := cfg.Systemd.Units[0]
	if u.Name != unitName || !u.Enabled {
		t.Errorf("unit is %q enabled=%v, want %q enabled", u.Name, u.Enabled, unitName)
	}
	// An enabled unit with no install section is never started.
	if !strings.Contains(u.Contents, "[Install]") {
		t.Error("the unit has no [Install] section, so enabling it does nothing")
	}
	if !strings.Contains(u.Contents, launcherPath) {
		t.Errorf("the unit does not run %s", launcherPath)
	}
}

func TestCheckELF(t *testing.T) {
	amd64 := make([]byte, 64)
	copy(amd64, "\x7fELF")
	amd64[4] = 2
	amd64[18], amd64[19] = 0x3e, 0x00
	if err := CheckELF(amd64); err != nil {
		t.Errorf("a linux/amd64 header was rejected: %v", err)
	}

	arm64 := make([]byte, 64)
	copy(arm64, "\x7fELF")
	arm64[4] = 2
	arm64[18], arm64[19] = 0xb7, 0x00
	if err := CheckELF(arm64); err == nil {
		t.Error("an arm64 binary was accepted; it cannot run on the server")
	}

	// A Mach-O build is the easy mistake to make from a Mac.
	macho := []byte{0xcf, 0xfa, 0xed, 0xfe, 0x0c, 0, 0, 1}
	if err := CheckELF(append(macho, make([]byte, 32)...)); err == nil {
		t.Error("a Mach-O binary was accepted")
	}
}

// TestBakeEndToEnd runs a real bake. It needs a CoreOS live ISO and about
// twice its size in free space, so it only runs when one is pointed at:
//
//	BARETAG_TEST_ISO=/path/to/rhcos-live.iso go test ./internal/bake/
func TestBakeEndToEnd(t *testing.T) {
	input := os.Getenv("BARETAG_TEST_ISO")
	if input == "" {
		t.Skip("set BARETAG_TEST_ISO to a CoreOS live ISO to run this")
	}

	binary := make([]byte, 3<<20)
	copy(binary, "\x7fELF")
	binary[4] = 2
	binary[18] = 0x3e
	for i := 64; i < len(binary); i++ {
		binary[i] = byte(i)
	}

	out := filepath.Join(t.TempDir(), "inventory.iso")
	res, err := Run(Options{Input: input, Output: out, Binary: binary, Kargs: []string{"video=1024x768"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Label == "" {
		t.Error("the baked ISO has no volume label")
	}
	if !strings.Contains(res.KernelArgs, "video=1024x768") {
		t.Errorf("kernel arguments are %q, want the added one", res.KernelArgs)
	}

	report, err := VerifyAgainst(out, input)
	if err != nil {
		t.Fatalf("VerifyAgainst: %v", err)
	}
	for _, c := range report.Checks {
		if !c.OK {
			t.Errorf("check failed: %s (%s)", c.Name, c.Detail)
		}
	}
}

func truncate(s string) string {
	if len(s) > 60 {
		return s[:60] + "..."
	}
	return s
}
