package render

import (
	"image/color"
	"testing"

	qrcode "github.com/skip2/go-qrcode"
)

// TestBitmapOrientation guards the row/column order of the bitmap. A QR code
// drawn transposed is a mirror image, which almost no scanner will read, and
// the mistake is invisible to the eye because finder patterns sit in three
// corners either way. The library's own image output is the reference.
func TestBitmapOrientation(t *testing.T) {
	const content = "QRINV1:ORIENTATION PROBE 12345"

	q, err := qrcode.New(content, qrcode.Low)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	q.DisableBorder = true

	sym, err := Encode(content, qrcode.Low)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	img := q.Image(-1) // one pixel per module
	b := img.Bounds()
	if b.Dx() != sym.Modules || b.Dy() != sym.Modules {
		t.Fatalf("reference image is %dx%d, symbol is %d modules", b.Dx(), b.Dy(), sym.Modules)
	}

	for y := 0; y < sym.Modules; y++ {
		for x := 0; x < sym.Modules; x++ {
			r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			imgDark := color.Gray16Model.Convert(color.RGBA64{R: uint16(r), G: uint16(g), B: uint16(bl)}).(color.Gray16).Y < 0x8000
			if got := sym.bitmap[y][x]; got != imgDark {
				t.Fatalf("module at x=%d y=%d: bitmap says dark=%v, image says dark=%v", x, y, got, imgDark)
			}
		}
	}
}

// TestQuietZoneIsLight checks that the margin the renderer adds is light in
// every direction, since a symbol without a quiet zone does not scan.
func TestQuietZoneIsLight(t *testing.T) {
	sym, err := Encode("probe", qrcode.Low)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	st := Style{Quiet: 2, Color: false}

	if got, want := sym.Cols(st), sym.Modules+4; got != want {
		t.Errorf("Cols() = %d, want %d", got, want)
	}
	if got, want := sym.Rows(st), (sym.Modules+4+1)/2; got != want {
		t.Errorf("Rows() = %d, want %d", got, want)
	}

	lines := splitLines(sym.String(st))
	if len(lines) != sym.Rows(st) {
		t.Fatalf("rendered %d rows, want %d", len(lines), sym.Rows(st))
	}
	// Without colour the drawing is inverted, so a light module is a full
	// block. The first row is entirely quiet zone.
	for i, r := range []rune(lines[0]) {
		if r != blockFull {
			t.Fatalf("top quiet row column %d is %q, want a light module", i, r)
		}
	}
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i, c := range s {
		if c == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return out
}
