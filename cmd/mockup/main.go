// Command mockup draws the illustration used in the README, so the picture can
// be regenerated when the report layout changes rather than going stale. It is
// documentation tooling and is not part of the released binary.
package main

import (
	"fmt"
	"os"
	"strings"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/mjovanovic/baretag/internal/inventory"
	"github.com/mjovanovic/baretag/internal/payload"
	"github.com/mjovanovic/baretag/internal/render"
)

func demo() *inventory.Inventory {
	return &inventory.Inventory{
		Hostname:  "node-07",
		Serial:    "7XKZQ83",
		SerialSrc: "chassis_serial",
		Vendor:    "Dell Inc.",
		Product:   "PowerEdge R750",
		UUID:      "4c4c4544-0058-4b10-8051-b4c04f513833",
		Collected: "2026-09-11T20:31:00Z",
		NICs: []inventory.NIC{
			{Name: "eno1", MAC: "b0:7b:25:1a:2c:3d", Speed: 10000, State: "up", IPs: []string{"10.20.4.71/24"}},
			{Name: "eno2", MAC: "b0:7b:25:1a:2c:3e", State: "down"},
			{Name: "ens3f0", MAC: "b0:7b:25:1a:2c:4a", Speed: 25000, State: "up", IPs: []string{"192.168.40.71/24"}},
			{Name: "ens3f1", MAC: "b0:7b:25:1a:2c:4b", State: "down"},
		},
		Disks: []inventory.Disk{
			{Path: "/dev/nvme0n1", Size: 1920383410176, Serial: "S6EWNG0T801234", Model: "SAMSUNG MZQL21T9HCJR-00A07"},
			{Path: "/dev/nvme1n1", Size: 1920383410176, Serial: "S6EWNG0T801235", Model: "SAMSUNG MZQL21T9HCJR-00A07"},
			{Path: "/dev/sda", Size: 960197124096, Serial: "PHYS7412000M960CGN", Model: "DELL PERC H755 Front"},
			{Path: "/dev/sdb", Size: 960197124096, Serial: "S6KHNA0T123456", Model: "SAMSUNG MZ7L3960HCJR"},
		},
	}
}

// modules turns the half block drawing back into a grid, so the same symbol
// that goes to a console can be drawn as vector squares.
func modules(sym *render.Symbol, st render.Style) [][]bool {
	text := sym.String(render.Style{Quiet: st.Quiet, Color: true})
	text = strings.ReplaceAll(text, "\x1b[30;107m", "")
	text = strings.ReplaceAll(text, "\x1b[0m", "")

	side := sym.Cols(st)
	grid := make([][]bool, side)
	for i := range grid {
		grid[i] = make([]bool, side)
	}
	for row, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		for col, r := range []rune(line) {
			top, bottom := false, false
			switch r {
			case '█':
				top, bottom = true, true
			case '▀':
				top = true
			case '▄':
				bottom = true
			}
			if y := row * 2; y < side {
				grid[y][col] = top
			}
			if y := row*2 + 1; y < side {
				grid[y][col] = bottom
			}
		}
	}
	return grid
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "ref" {
		if err := writeReference(os.Args[2]); err != nil {
			panic(err)
		}
		return
	}

	inv := demo()
	st := render.Style{Quiet: 2}

	data, err := payload.Packed(inv, false)
	if err != nil {
		panic(err)
	}
	sym, err := render.Encode(data, qrcode.Low)
	if err != nil {
		panic(err)
	}
	grid := modules(sym, st)

	lines := strings.Split(strings.TrimSuffix(render.ReportCompact(inv, 100), "\n"), "\n")

	diff, total := checkGrid(grid, data, st.Quiet)
	fmt.Fprintf(os.Stderr, "payload %d bytes, QR version %d, %d modules; grid differs in %d of %d modules\n",
		len(data), sym.Version, sym.Modules, diff, total)
	os.Stdout.WriteString(svg(lines, grid, inv.Hostname))
}
