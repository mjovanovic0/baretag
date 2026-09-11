package render

import (
	"strings"
	"testing"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
	skip2 "github.com/skip2/go-qrcode"
)

// bitMatrix converts a rendered symbol, quiet zone included, into the form an
// independent decoder expects. It reads the same bitmap the console drawing
// reads, so a pass here means the drawing itself is scannable.
func bitMatrix(t *testing.T, s *Symbol, st Style) *gozxing.BitMatrix {
	t.Helper()

	side := s.Cols(st)
	m, err := gozxing.NewBitMatrix(side, side)
	if err != nil {
		t.Fatalf("NewBitMatrix: %v", err)
	}
	for y := 0; y < s.Modules; y++ {
		for x := 0; x < s.Modules; x++ {
			if s.bitmap[y][x] {
				m.Set(x+st.Quiet, y+st.Quiet)
			}
		}
	}
	return m
}

// TestSymbolDecodes runs the full loop: encode a payload, take the exact
// modules the console renderer draws, and read them back with a decoder from a
// different project. This is the check that a phone will actually scan what is
// on the screen.
func TestSymbolDecodes(t *testing.T) {
	payloads := []struct {
		name    string
		content string
	}{
		{"plain json", `{"h":"worker-01","s":"JHK3M92","n":[["eno1","b0:7b:25:1a:2c:3d",10000,"up","10.10.4.21/24"]],"d":[["/dev/sda",960197124096,"PHYS7412000M960CGN"]]}`},
		{"packed base32", "QRINV1:H4SIAAAAAAACA23MQQ6AIAxE0bt0LYFCKeVAXsAYo0Zdefe6cGOc7ftnbtizNzrpIKmMIpVRWYoGjKgxNHFtHpqEMr7XnzaTkrV5jJJASnbkkEGRKoZAxbqB9r73AR8H4b1zAAAA"},
		{"chunk header", "QRINVC1:2/3:ORSXG5BOEBQXA5DJNZXXGZLSMVSA"},
	}

	for _, st := range []Style{{Quiet: 2, Color: true}, {Quiet: 4, Color: false}} {
		for _, p := range payloads {
			sym, err := Encode(p.content, skip2.Low)
			if err != nil {
				t.Fatalf("%s: Encode: %v", p.name, err)
			}

			result, err := qrcode.NewQRCodeReader().Decode(
				newSource(bitMatrix(t, sym, st)), nil)
			if err != nil {
				t.Fatalf("%s (quiet=%d): independent decoder rejected the symbol: %v",
					p.name, st.Quiet, err)
			}
			if got := result.GetText(); got != p.content {
				t.Errorf("%s (quiet=%d): decoded %q, want %q", p.name, st.Quiet, got, p.content)
			}
		}
	}
}

// TestRenderedRowsMatchModules checks the drawing itself, not just the bitmap:
// every rendered row must be exactly as wide as the symbol claims, so the
// console layout arithmetic in the caller holds.
func TestRenderedRowsMatchModules(t *testing.T) {
	sym, err := Encode("QRINV1:WIDTH PROBE", skip2.Low)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	st := Style{Quiet: 2, Color: false}

	for i, line := range strings.Split(strings.TrimSuffix(sym.String(st), "\n"), "\n") {
		if got := len([]rune(line)); got != sym.Cols(st) {
			t.Fatalf("row %d is %d cells wide, want %d", i, got, sym.Cols(st))
		}
	}
}

// newSource adapts a BitMatrix to the reader interface, which normally expects
// a photograph rather than an exact grid.
func newSource(m *gozxing.BitMatrix) *gozxing.BinaryBitmap {
	bb, err := gozxing.NewBinaryBitmap(&exactBinarizer{m: m})
	if err != nil {
		panic(err)
	}
	return bb
}

type exactBinarizer struct{ m *gozxing.BitMatrix }

func (b *exactBinarizer) GetLuminanceSource() gozxing.LuminanceSource { return nil }
func (b *exactBinarizer) GetWidth() int                               { return b.m.GetWidth() }
func (b *exactBinarizer) GetHeight() int                              { return b.m.GetHeight() }
func (b *exactBinarizer) GetBlackMatrix() (*gozxing.BitMatrix, error) { return b.m, nil }
func (b *exactBinarizer) GetBlackRow(y int, row *gozxing.BitArray) (*gozxing.BitArray, error) {
	return b.m.GetRow(y, row), nil
}
func (b *exactBinarizer) CreateBinarizer(gozxing.LuminanceSource) gozxing.Binarizer { return b }
