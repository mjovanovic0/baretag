// Package payload turns an inventory into the string carried by a QR code, and
// back again.
//
// Two encodings exist. Plain is compact JSON, which a phone shows as readable
// text straight out of the scanner. Packed is gzipped JSON in base32, which is
// three to four times smaller and is what makes a machine with many disks fit
// in a symbol small enough to draw on an 80 column console.
//
// Packed payloads deliberately use only characters from the QR alphanumeric
// set, so the encoder spends 5.5 bits per character instead of the 8 bits that
// byte mode would cost.
package payload

import (
	"bytes"
	"compress/gzip"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mjovanovic/baretag/internal/inventory"
)

const (
	// PackedPrefix marks a gzipped base32 payload.
	PackedPrefix = "QRINV1:"
	// ChunkPrefix marks one part of a payload split across several symbols.
	ChunkPrefix = "QRINVC1:"
)

// b32 is unpadded so that "=" never appears, which would force the encoder out
// of alphanumeric mode.
var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// Plain renders the inventory as single line compact JSON. A phone shows this
// straight out of the scanner, so it is preferred whenever it fits.
func Plain(inv *inventory.Inventory, minimal bool) (string, error) {
	b, err := json.Marshal(toWire(inv, minimal))
	if err != nil {
		return "", fmt.Errorf("marshal inventory: %w", err)
	}
	return string(b), nil
}

// Packed renders the inventory as gzipped compact JSON in base32.
func Packed(inv *inventory.Inventory, minimal bool) (string, error) {
	plain, err := Plain(inv, minimal)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return "", fmt.Errorf("init gzip: %w", err)
	}
	if _, err := zw.Write([]byte(plain)); err != nil {
		return "", fmt.Errorf("compress payload: %w", err)
	}
	if err := zw.Close(); err != nil {
		return "", fmt.Errorf("finish gzip: %w", err)
	}

	return PackedPrefix + b32.EncodeToString(buf.Bytes()), nil
}

// Split breaks a payload into n chunks, each tagged with its position so the
// parts can be scanned in any order and reassembled.
func Split(s string, n int) []string {
	if n <= 1 {
		return []string{s}
	}

	size := (len(s) + n - 1) / n
	chunks := make([]string, 0, n)
	for i := 0; i < n; i++ {
		start := i * size
		if start >= len(s) {
			break
		}
		end := start + size
		if end > len(s) {
			end = len(s)
		}
		header := ChunkPrefix + strconv.Itoa(i+1) + "/" + strconv.Itoa(n) + ":"
		chunks = append(chunks, header+s[start:end])
	}
	return chunks
}

// Decode accepts whatever a scanner produced, in any order, and returns the
// inventory as readable JSON with full field names. It understands plain JSON, a packed payload, and the chunks of
// a split payload.
func Decode(parts []string) (string, error) {
	joined, err := join(parts)
	if err != nil {
		return "", err
	}

	if !strings.HasPrefix(joined, PackedPrefix) {
		return expand(joined)
	}

	raw, err := b32.DecodeString(strings.TrimPrefix(joined, PackedPrefix))
	if err != nil {
		return "", fmt.Errorf("base32 decode: %w", err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return "", fmt.Errorf("open gzip stream: %w", err)
	}
	defer zr.Close()

	var out bytes.Buffer
	if _, err := out.ReadFrom(zr); err != nil {
		return "", fmt.Errorf("decompress payload: %w", err)
	}
	return expand(out.String())
}

// join reassembles chunked scans, verifying that every part is present exactly
// once before returning anything.
func join(parts []string) (string, error) {
	type chunk struct {
		index int
		body  string
	}

	var (
		chunks []chunk
		total  int
		plain  []string
	)

	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !strings.HasPrefix(p, ChunkPrefix) {
			plain = append(plain, p)
			continue
		}

		rest := strings.TrimPrefix(p, ChunkPrefix)
		head, body, ok := strings.Cut(rest, ":")
		if !ok {
			return "", fmt.Errorf("malformed chunk header %q", p[:min(len(p), 24)])
		}
		iStr, nStr, ok := strings.Cut(head, "/")
		if !ok {
			return "", fmt.Errorf("malformed chunk index %q", head)
		}
		i, err := strconv.Atoi(iStr)
		if err != nil {
			return "", fmt.Errorf("bad chunk index %q: %w", iStr, err)
		}
		n, err := strconv.Atoi(nStr)
		if err != nil {
			return "", fmt.Errorf("bad chunk count %q: %w", nStr, err)
		}
		if total != 0 && total != n {
			return "", fmt.Errorf("chunks disagree on total: %d and %d", total, n)
		}
		total = n
		chunks = append(chunks, chunk{index: i, body: body})
	}

	if total == 0 {
		return strings.Join(plain, ""), nil
	}
	if len(plain) > 0 {
		return "", fmt.Errorf("mixed chunked and unchunked input")
	}

	sort.Slice(chunks, func(i, j int) bool { return chunks[i].index < chunks[j].index })

	var sb strings.Builder
	for want := 1; want <= total; want++ {
		if len(chunks) < want || chunks[want-1].index != want {
			return "", fmt.Errorf("missing chunk %d of %d", want, total)
		}
		sb.WriteString(chunks[want-1].body)
	}
	return sb.String(), nil
}
