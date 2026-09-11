package main

import (
	"fmt"
	"strings"
	"testing"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/mjovanovic/baretag/internal/inventory"
	"github.com/mjovanovic/baretag/internal/render"
)

// machine builds an inventory of a given shape, with addresses, long model
// strings and full length serials, so the payload is as large as a real
// machine of that size would produce.
func machine(nics, disks int) *inventory.Inventory {
	inv := &inventory.Inventory{
		Hostname:  "worker-01.rack14.example.net",
		Serial:    "JHK3M92",
		SerialSrc: "chassis_serial",
		Vendor:    "Dell Inc.",
		Product:   "PowerEdge R750",
		UUID:      "4c4c4544-0048-4b10-8033-b4c04f4d3932",
		Collected: "2026-09-11T15:04:05Z",
	}
	for i := 0; i < nics; i++ {
		n := inventory.NIC{
			Name:  fmt.Sprintf("ens%df%d", i/2+3, i%2),
			MAC:   fmt.Sprintf("b0:7b:25:1a:2c:%02x", i),
			State: "down",
		}
		if i%2 == 0 {
			n.Speed, n.State = 25000, "up"
			n.IPs = []string{fmt.Sprintf("10.10.%d.21/24", i)}
		}
		inv.NICs = append(inv.NICs, n)
	}
	for i := 0; i < disks; i++ {
		inv.Disks = append(inv.Disks, inventory.Disk{
			Path:   fmt.Sprintf("/dev/sd%c", 'a'+i%26),
			Size:   1920383410176,
			Serial: fmt.Sprintf("S6EWNG0T80%04d", i),
			Model:  "SAMSUNG MZQL21T9HCJR-00A07",
		})
	}
	return inv
}

func baseConfig() config {
	return config{format: "auto", layout: "auto", level: "L", maxParts: 4, prefer: "payload"}
}

// measure returns the rendered size and whether the drawing reported that it
// overflowed.
func measure(t *testing.T, out string) (cols, rows int, overflowed bool) {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		rows++
		if n := len([]rune(line)); n > cols {
			cols = n
		}
		if strings.Contains(line, "only") && strings.Contains(line, "rows left") {
			overflowed = true
		}
	}
	return cols, rows, overflowed
}

// TestLayoutNeverOverflowsSilently is the property that matters on a console
// nobody can scroll: whenever the drawing does not warn, it really does fit.
func TestLayoutNeverOverflowsSilently(t *testing.T) {
	style := render.Style{Quiet: 2, Color: false}

	for _, shape := range []struct{ nics, disks int }{{1, 1}, {2, 2}, {4, 4}, {4, 8}, {8, 24}} {
		for _, screen := range []struct{ cols, rows int }{{80, 25}, {96, 30}, {100, 37}, {128, 48}, {240, 67}} {
			inv := machine(shape.nics, shape.disks)
			out, err := compose(inv, baseConfig(), style, qrcode.Low, screen.cols, screen.rows)
			if err != nil {
				t.Fatalf("%d NIC/%d disk at %dx%d: %v", shape.nics, shape.disks, screen.cols, screen.rows, err)
			}

			gotCols, gotRows, overflowed := measure(t, out)
			if overflowed {
				continue // an honest warning is the acceptable outcome
			}
			if gotCols > screen.cols || gotRows > screen.rows {
				t.Errorf("%d NIC/%d disk at %dx%d: drew %dx%d without warning",
					shape.nics, shape.disks, screen.cols, screen.rows, gotCols, gotRows)
			}
		}
	}
}

// TestLayoutGivesGroundInOrder checks that detail is surrendered in the
// documented order as the console shrinks: the full table first, then the
// compact one, then the descriptive fields in the payload.
func TestLayoutGivesGroundInOrder(t *testing.T) {
	style := render.Style{Quiet: 2, Color: false}
	inv := machine(4, 4)

	wide, err := compose(inv, baseConfig(), style, qrcode.Low, 240, 67)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(wide, "MACHINE INVENTORY") {
		t.Error("a 240x67 console should still get the full table")
	}
	if strings.Contains(wide, "minimal") {
		t.Error("a 240x67 console should not need the minimal payload")
	}

	narrow, err := compose(inv, baseConfig(), style, qrcode.Low, 100, 37)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(narrow, "MACHINE INVENTORY") {
		t.Error("a 100x37 console cannot fit the full table and a symbol")
	}
	// Whatever else is given up, the identity has to survive.
	if !strings.Contains(narrow, "JHK3M92") {
		t.Error("the machine serial was dropped from the drawing")
	}
}

// TestExplicitLayoutIsHonoured makes sure the automatic degradation does not
// override an operator who asked for something specific.
func TestExplicitLayoutIsHonoured(t *testing.T) {
	style := render.Style{Quiet: 2, Color: false}
	cfg := baseConfig()
	cfg.layout = "full"

	out, err := compose(machine(4, 4), cfg, style, qrcode.Low, 80, 25)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "MACHINE INVENTORY") {
		t.Error("--layout full was not honoured")
	}
	if _, _, overflowed := measure(t, out); !overflowed {
		t.Error("--layout full on an 80x25 console should say that it overflowed")
	}
}

func TestUnknownLayoutIsAnError(t *testing.T) {
	cfg := baseConfig()
	cfg.layout = "enormous"
	if _, err := compose(machine(1, 1), cfg, render.DefaultStyle(), qrcode.Low, 100, 40); err == nil {
		t.Error("compose accepted an unknown layout")
	}
}

// TestPreferTableKeepsTheListOnScreen checks the other side of the tradeoff:
// with --prefer table the readable list survives and the payload is what gives
// way, on a console where both cannot fit.
func TestPreferTableKeepsTheListOnScreen(t *testing.T) {
	style := render.Style{Quiet: 2, Color: false}
	inv := machine(4, 4)

	cfg := baseConfig()
	cfg.prefer = "payload"
	byPayload, err := compose(inv, cfg, style, qrcode.Low, 128, 48)
	if err != nil {
		t.Fatal(err)
	}

	cfg.prefer = "table"
	byTable, err := compose(inv, cfg, style, qrcode.Low, 128, 48)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(byPayload, "minimal") {
		t.Error("--prefer payload gave up payload fields it did not have to")
	}
	if !strings.Contains(byTable, "\nDSK /dev/sda") {
		t.Error("--prefer table did not keep the device list on screen")
	}
	if _, _, overflowed := measure(t, byTable); overflowed {
		t.Error("--prefer table overflowed a 128x48 console")
	}
}

func TestUnknownPreferIsAnError(t *testing.T) {
	cfg := baseConfig()
	cfg.prefer = "everything"
	if _, err := compose(machine(1, 1), cfg, render.DefaultStyle(), qrcode.Low, 100, 40); err == nil {
		t.Error("compose accepted an unknown -prefer value")
	}
}
