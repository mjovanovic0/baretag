// Package render draws the inventory report and its QR code for a text console.
package render

import (
	"fmt"
	"strings"

	qrcode "github.com/skip2/go-qrcode"
)

// Style controls how a QR symbol is drawn.
type Style struct {
	// Quiet is the width in modules of the mandatory light margin. The
	// standard asks for 4; 2 still scans reliably and buys two console rows.
	Quiet int
	// Color emits an explicit black on white SGR pair. Without it the drawing
	// relies on the console already having a dark background, because a QR
	// code read in the wrong polarity does not scan.
	Color bool
}

// DefaultStyle is what the boot time unit uses.
func DefaultStyle() Style {
	return Style{Quiet: 2, Color: true}
}

const (
	blockFull  = '█' // full block
	blockUpper = '▀' // upper half block
	blockLower = '▄' // lower half block
	blockNone  = ' '
)

// Symbol is an encoded QR code together with the geometry needed to decide
// whether it will fit on a given console.
type Symbol struct {
	Content string
	Version int
	Modules int
	bitmap  [][]bool
}

// Encode builds a QR symbol without the library's own border, so that the
// quiet zone is under this package's control.
func Encode(content string, level qrcode.RecoveryLevel) (*Symbol, error) {
	q, err := qrcode.New(content, level)
	if err != nil {
		return nil, fmt.Errorf("encode %d bytes: %w", len(content), err)
	}
	q.DisableBorder = true

	bm := q.Bitmap()
	return &Symbol{
		Content: content,
		Version: q.VersionNumber,
		Modules: len(bm),
		bitmap:  bm,
	}, nil
}

// Cols is the console width the drawing needs.
func (s *Symbol) Cols(st Style) int { return s.Modules + 2*st.Quiet }

// Rows is the console height the drawing needs. Two module rows share one text
// row, which is what makes the symbol come out square on a console whose cells
// are about twice as tall as they are wide.
func (s *Symbol) Rows(st Style) int { return (s.Cols(st) + 1) / 2 }

// Fits reports whether the drawing is within the given console geometry.
// A non positive bound means that dimension is unconstrained.
func (s *Symbol) Fits(st Style, cols, rows int) bool {
	if cols > 0 && s.Cols(st) > cols {
		return false
	}
	if rows > 0 && s.Rows(st) > rows {
		return false
	}
	return true
}

// String draws the symbol with half block characters.
func (s *Symbol) String(st Style) string {
	side := s.Cols(st)

	// dark reports the module colour at a point in the padded grid. Everything
	// outside the symbol is quiet zone, which is light.
	dark := func(x, y int) bool {
		mx, my := x-st.Quiet, y-st.Quiet
		if mx < 0 || my < 0 || mx >= s.Modules || my >= s.Modules {
			return false
		}
		return s.bitmap[my][mx]
	}

	var sb strings.Builder
	for y := 0; y < side; y += 2 {
		if st.Color {
			// Black foreground on bright white background, so the polarity is
			// correct whatever theme the console is using.
			sb.WriteString("\x1b[30;107m")
		}
		for x := 0; x < side; x++ {
			top, bottom := dark(x, y), dark(x, y+1)
			if !st.Color {
				// Without colour the console background stands in for the dark
				// modules, so the drawing has to be inverted.
				top, bottom = !top, !bottom
			}
			switch {
			case top && bottom:
				sb.WriteRune(blockFull)
			case top:
				sb.WriteRune(blockUpper)
			case bottom:
				sb.WriteRune(blockLower)
			default:
				sb.WriteRune(blockNone)
			}
		}
		if st.Color {
			sb.WriteString("\x1b[0m")
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}
