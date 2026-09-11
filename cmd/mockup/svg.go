package main

import (
	"fmt"
	"strings"
)

const (
	canvasW = 1220
	canvasH = 980

	winX, winY = 16, 16
	winW, winH = canvasW - 2*winX, canvasH - 2*winY

	headerH  = 46
	toolbarH = 38
	statusH  = 26

	consoleX = winX + 10
	consoleY = winY + headerH + toolbarH + 10
	consoleW = winW - 20
	consoleH = winH - headerH - toolbarH - statusH - 20

	textX      = consoleX + 18
	textTop    = consoleY + 30
	lineHeight = 19
	fontSize   = 14

	moduleSize = 6
)

const monospace = "ui-monospace, SFMono-Regular, Menlo, Consolas, 'DejaVu Sans Mono', monospace"
const uiFont = "-apple-system, 'Segoe UI', 'DejaVu Sans', Helvetica, Arial, sans-serif"

func esc(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

// svg draws a BMC remote console window around the report and its QR code.
// The chrome is a generic virtual console rather than any vendor's product, so
// the picture illustrates the workflow without pretending to be a capture of
// somebody else's software.
func svg(lines []string, grid [][]bool, hostname string) string {
	var b strings.Builder

	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d">`,
		canvasW, canvasH, canvasW, canvasH)
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="#dfe4ea"/>`, canvasW, canvasH)

	// Window body.
	fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="%d" rx="8" fill="#ffffff" stroke="#c3cbd4"/>`,
		winX, winY, winW, winH)

	// Title bar.
	fmt.Fprintf(&b, `<path d="M%d %d h%d a8 8 0 0 1 8 8 v%d h-%d v-%d a8 8 0 0 1 8 -8 z" fill="#12395e"/>`,
		winX+8, winY, winW-16, headerH-8, winW, headerH-8)
	fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="%d" fill="#12395e"/>`,
		winX, winY+8, winW, headerH-8)
	fmt.Fprintf(&b, `<text x="%d" y="%d" font-family="%s" font-size="14" font-weight="600" fill="#ffffff">Virtual Console</text>`,
		winX+18, winY+29, uiFont)
	fmt.Fprintf(&b, `<text x="%d" y="%d" font-family="%s" font-size="13" fill="#9fc4e4">%s  ·  %s</text>`,
		winX+150, winY+29, uiFont, esc(hostname+".rack14.example.net"), "192.0.2.40")
	for i, c := range []string{"#f7c948", "#3ec97a", "#e8556d"} {
		fmt.Fprintf(&b, `<circle cx="%d" cy="%d" r="5" fill="%s"/>`, winX+winW-24-i*20, winY+23, c)
	}

	// Toolbar.
	fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="%d" fill="#f5f7f9"/>`,
		winX, winY+headerH, winW, toolbarH)
	fmt.Fprintf(&b, `<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#dde3e9"/>`,
		winX, winY+headerH+toolbarH, winX+winW, winY+headerH+toolbarH)
	x := winX + 14
	for _, label := range []string{"Boot", "Power", "Keyboard", "Screen Capture", "Refresh", "Full Screen"} {
		w := 16 + 7*len(label)
		fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="22" rx="4" fill="#ffffff" stroke="#d5dbe1"/>`,
			x, winY+headerH+8, w)
		fmt.Fprintf(&b, `<text x="%d" y="%d" font-family="%s" font-size="12" fill="#41505f">%s</text>`,
			x+8, winY+headerH+23, uiFont, esc(label))
		x += w + 8
	}
	fmt.Fprintf(&b, `<rect x="%d" y="%d" width="128" height="22" rx="4" fill="#e8f2fb" stroke="#9cc4e6"/>`,
		winX+winW-142, winY+headerH+8)
	fmt.Fprintf(&b, `<text x="%d" y="%d" font-family="%s" font-size="12" fill="#1d5c92">Virtual Media ▾</text>`,
		winX+winW-134, winY+headerH+23, uiFont)

	// The console itself.
	fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="%d" fill="#000000"/>`,
		consoleX, consoleY, consoleW, consoleH)

	y := textTop
	for _, line := range lines {
		fmt.Fprintf(&b, `<text x="%d" y="%d" font-family="%s" font-size="%d" fill="#d6d6d6" xml:space="preserve">%s</text>`,
			textX, y, monospace, fontSize, esc(line))
		y += lineHeight
	}

	// QR code, drawn from the same modules the console renderer produces. The
	// grid already carries the renderer's two module quiet zone; the white
	// background extends it to the four the specification asks for, which is
	// what a strict scanner wants to see.
	qrTop := y + 14
	side := len(grid)
	pad := 2 * moduleSize
	// crispEdges turns off antialiasing. Without it the rasteriser softens
	// every module edge and a scanner will not read the result, however
	// correct the modules underneath are.
	fmt.Fprintf(&b, `<g shape-rendering="crispEdges">`)
	fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="%d" fill="#ffffff"/>`,
		textX-pad, qrTop-pad, side*moduleSize+2*pad, side*moduleSize+2*pad)
	for row := 0; row < side; row++ {
		for col := 0; col < side; col++ {
			if !grid[row][col] {
				continue
			}
			fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="%d" fill="#000000"/>`,
				textX+col*moduleSize, qrTop+row*moduleSize, moduleSize, moduleSize)
		}
	}

	b.WriteString(`</g>`)

	prompt := qrTop + side*moduleSize + 26
	fmt.Fprintf(&b, `<text x="%d" y="%d" font-family="%s" font-size="%d" fill="#d6d6d6">%s login:</text>`,
		textX, prompt, monospace, fontSize, esc(hostname))
	fmt.Fprintf(&b, `<rect x="%d" y="%d" width="8" height="15" fill="#d6d6d6"/>`,
		textX+8*(len(hostname)+8), prompt-12)

	// Status bar.
	statusY := winY + winH - statusH
	fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="%d" fill="#f5f7f9"/>`,
		winX, statusY, winW, statusH-8)
	fmt.Fprintf(&b, `<text x="%d" y="%d" font-family="%s" font-size="11" fill="#5a6775">Virtual Media: baretag.iso attached  ·  Connected  ·  1024 x 768</text>`,
		winX+18, statusY+13, uiFont)
	fmt.Fprintf(&b, `<text x="%d" y="%d" text-anchor="end" font-family="%s" font-size="11" fill="#9aa7b4">illustration</text>`,
		winX+winW-18, statusY+13, uiFont)

	b.WriteString(`</svg>`)
	return b.String()
}
