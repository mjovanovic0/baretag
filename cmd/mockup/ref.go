package main

import (
	qrcode "github.com/skip2/go-qrcode"

	"github.com/mjovanovic/baretag/internal/payload"
)

// writeReference renders the same payload with the library's own PNG writer,
// as a control when a scanner refuses the drawing built from modules.
func writeReference(path string) error {
	data, err := payload.Packed(demo(), false)
	if err != nil {
		return err
	}
	return qrcode.WriteFile(data, qrcode.Low, 700, path)
}

// checkGrid compares the grid rebuilt from the console drawing with the
// library's own bitmap, so a fault in the reconstruction is not mistaken for
// a fault in the scanner.
func checkGrid(grid [][]bool, data string, quiet int) (int, int) {
	q, err := qrcode.New(data, qrcode.Low)
	if err != nil {
		panic(err)
	}
	q.DisableBorder = true
	bm := q.Bitmap()

	diff, total := 0, 0
	for y := range bm {
		for x := range bm[y] {
			total++
			if grid[y+quiet][x+quiet] != bm[y][x] {
				diff++
			}
		}
	}
	return diff, total
}
