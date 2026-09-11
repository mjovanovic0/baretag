// Command baretag prints a machine's network, disk and identity facts on
// the console and encodes the same facts into a QR code, so that a server with
// no network connectivity can still be inventoried by scanning its screen.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	qrcode "github.com/skip2/go-qrcode"
	"golang.org/x/term"

	"github.com/mjovanovic/baretag/internal/inventory"
	"github.com/mjovanovic/baretag/internal/payload"
	"github.com/mjovanovic/baretag/internal/render"
)

// Console geometry assumed when the output is not a terminal, which is the
// case when writing the login banner at boot. A 1024x768 EFI framebuffer with
// the default 8x16 console font gives 128x48; staying under that leaves room
// for the login prompt and for machines that come up at 800x600.
const (
	defaultCols = 98
	defaultRows = 34
)

// version is stamped at build time with -ldflags "-X main.version=v0.1.0".
// A released binary that cannot say what it is makes a bug report guesswork.
var version = "dev"

type config struct {
	format   string
	layout   string
	level    string
	quiet    int
	cols     int
	rows     int
	maxParts int
	noColor  bool
	jsonOnly bool
	payload  bool
	out      string
	issue    string
	allNICs  bool
	allDisks bool
	noUdev   bool
	minimal  bool
	tty      string
	reserve  int
	root     string
	lldp     time.Duration
	clear    bool
	noFooter bool
	prefer   string
}

func main() {
	if len(os.Args) > 1 {
		if run, ok := subcommands[os.Args[1]]; ok {
			if err := run(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "baretag %s: %v\n", os.Args[1], err)
				os.Exit(1)
			}
			return
		}
	}

	var cfg config
	flag.StringVar(&cfg.format, "format", "auto", "payload encoding: auto, plain (readable JSON) or packed (gzip+base32)")
	flag.StringVar(&cfg.layout, "layout", "auto", "console layout: auto, full, compact or qr")
	flag.StringVar(&cfg.level, "level", "L", "QR error correction: L, M, Q or H")
	flag.IntVar(&cfg.quiet, "quiet-zone", 2, "width in modules of the light margin around the symbol")
	flag.IntVar(&cfg.cols, "cols", 0, "console width, 0 to detect")
	flag.IntVar(&cfg.rows, "rows", 0, "console height, 0 to detect")
	flag.IntVar(&cfg.maxParts, "max-parts", 4, "most QR symbols to split a payload across")
	flag.BoolVar(&cfg.noColor, "no-color", false, "draw without SGR colour, assuming a dark console background")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.BoolVar(&cfg.jsonOnly, "json", false, "print the inventory as JSON and exit")
	flag.BoolVar(&cfg.payload, "payload", false, "print the exact string the QR code would carry and exit")
	flag.StringVar(&cfg.out, "out", "", "also write the rendered output to this file")
	flag.StringVar(&cfg.issue, "issue", "", "write the rendered output as a login banner at this path")
	flag.BoolVar(&cfg.allNICs, "all-nics", false, "include virtual interfaces that carry no address")
	flag.BoolVar(&cfg.allDisks, "all-disks", false, "include loop, ram and device-mapper nodes")
	flag.BoolVar(&cfg.noUdev, "no-udev", false, "do not fall back to udevadm for disk serials")
	flag.DurationVar(&cfg.lldp, "lldp", 0, "listen this long on each live link for the switch on the other end; switches advertise every 30s, so 35s is the useful minimum")
	flag.BoolVar(&cfg.minimal, "minimal", false, "leave model, vendor and UUID out of the QR payload to shrink the symbol")
	flag.StringVar(&cfg.tty, "tty", "", "read the console size from this device, for example /dev/tty1")
	flag.IntVar(&cfg.reserve, "reserve", 0, "console rows to leave free for the login prompt and other banners")
	flag.StringVar(&cfg.root, "root", "", "prefix every sysfs path with this directory, for testing against a captured tree")
	flag.BoolVar(&cfg.clear, "clear", false, "clear the console before drawing, so the banner does not share it with other login snippets")
	flag.BoolVar(&cfg.noFooter, "no-footer", false, "leave out the line describing the payload and symbol")
	flag.StringVar(&cfg.prefer, "prefer", "payload", "what to keep when the console is too small: payload (every field in the QR) or table (the readable list on screen)")
	flag.Usage = usage
	flag.Parse()

	if *showVersion {
		fmt.Println("baretag", version)
		return
	}

	if err := run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "baretag:", err)
		os.Exit(1)
	}
}

// subcommands are the modes that do something other than draw the inventory.
var subcommands = map[string]func([]string) error{
	"decode":     decodeMain,
	"bake-into":  bakeMain,
	"verify-iso": verifyMain,
}

func usage() {
	fmt.Fprint(os.Stderr, `baretag collects machine facts and shows them as text and as a QR code.

usage:
  baretag [flags]                       draw the inventory of this machine
  baretag decode [string ...]           turn a scanned payload back into JSON
  baretag bake-into <coreos.iso>        write an ISO that runs this at boot
  baretag verify-iso <iso>              check a baked ISO

decode takes the scanned strings as arguments, or on stdin one per line. A
payload split across several symbols may be given in any order.

bake-into copies a CoreOS live ISO and adds this tool to it, so that booting
the result shows the inventory and its QR code above the login prompt. It needs
nothing else installed.

flags:
`)
	flag.PrintDefaults()
}

func run(cfg config) error {
	inv := inventory.Collect(inventory.Options{
		AllNICs:  cfg.allNICs,
		AllDisks: cfg.allDisks,
		// Shelling out to udevadm would read the machine running the tool, so
		// it is off whenever the collectors are pointed at a captured tree.
		UseUdev: !cfg.noUdev && cfg.root == "",
		Root:    cfg.root,
	})

	if cfg.jsonOnly {
		b, err := json.MarshalIndent(inv, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal inventory: %w", err)
		}
		fmt.Println(string(b))
		return nil
	}

	if cfg.payload {
		parts, err := choosePayload(inv, cfg)
		if err != nil {
			return err
		}
		for _, part := range parts {
			fmt.Println(part)
		}
		return nil
	}

	level, err := parseLevel(cfg.level)
	if err != nil {
		return err
	}
	style := render.Style{Quiet: cfg.quiet, Color: !cfg.noColor}
	cols, rows := geometry(cfg)

	out, err := compose(inv, cfg, style, level, cols, rows)
	if err != nil {
		return err
	}
	if cfg.clear {
		// agetty prints /etc/issue and every snippet in turn before the login
		// prompt. Wiping the screen first means the drawing does not have to
		// compete with the OS banner and the SSH host key fingerprints for the
		// rows it needs.
		out = "\x1b[2J\x1b[H" + out
	}

	os.Stdout.WriteString(out)

	if cfg.out != "" {
		if err := os.WriteFile(cfg.out, []byte(out), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", cfg.out, err)
		}
	}
	if cfg.issue != "" {
		if err := writeIssue(cfg.issue, out); err != nil {
			return err
		}
	}
	return nil
}

// choosePayload returns the payload strings without drawing anything, which is
// what the -payload flag prints and what the tests feed back through decode.
func choosePayload(inv *inventory.Inventory, cfg config) ([]string, error) {
	build := payload.Packed
	if cfg.format == "plain" {
		build = payload.Plain
	}
	s, err := build(inv, cfg.minimal)
	if err != nil {
		return nil, err
	}
	return payload.Split(s, 1), nil
}

// compose lays out the report and the QR symbols together. On a console that
// is too small for both it gives ground in a deliberate order: first the
// whitespace and detail in the report, then the descriptive fields in the
// payload. The machine identity and the device facts are never what gets cut.
func compose(inv *inventory.Inventory, cfg config, style render.Style, level qrcode.RecoveryLevel, cols, rows int) (string, error) {
	reports := map[string]func() string{
		"full":    func() string { return render.Report(inv, cols) },
		"compact": func() string { return render.ReportCompact(inv, cols) },
		"qr":      func() string { return render.ReportIdentity(inv, cols) },
	}

	layouts := []string{"full", "compact", "qr"}
	if cfg.layout != "auto" {
		if _, ok := reports[cfg.layout]; !ok {
			return "", fmt.Errorf("unknown layout %q", cfg.layout)
		}
		layouts = []string{cfg.layout}
	}

	// Dropping the model, vendor and UUID shrinks the symbol by about two
	// versions, which is often the difference between fitting and not. It is
	// only tried once the report has already been reduced as far as it goes.
	payloads := []bool{cfg.minimal}
	if !cfg.minimal {
		payloads = append(payloads, true)
	}

	// This ordering is the whole policy for a console that cannot hold
	// everything. Preferring the payload keeps every field in the QR code and
	// gives up the readable table, which is the safer default: the table can
	// be recovered from the QR code and from the journal, while a field left
	// out of the payload is simply gone. Preferring the table does the
	// opposite, for operators who read the screen more often than they scan.
	type plan struct {
		minimal bool
		layout  string
	}
	var plans []plan
	switch cfg.prefer {
	case "payload":
		for _, m := range payloads {
			for _, name := range layouts {
				plans = append(plans, plan{m, name})
			}
		}
	case "table":
		for _, name := range layouts {
			for _, m := range payloads {
				plans = append(plans, plan{m, name})
			}
		}
	default:
		return "", fmt.Errorf("unknown value for -prefer: %q", cfg.prefer)
	}

	var best *layout
	for _, pl := range plans {
		l, err := tryLayout(inv, cfg, style, level, cols, rows, reports[pl.layout], pl.minimal)
		if err != nil {
			return "", err
		}
		best = l
		if l.fit.ok {
			return l.render(style), nil
		}
	}

	// Nothing fits. Draw the densest arrangement anyway and say so: a symbol
	// that overflows still scans if the console turns out to be bigger than
	// this one thinks, and it is still in the scrollback either way.
	return best.render(style), nil
}

// layout is one candidate arrangement of the report and its symbols.
type layout struct {
	report    string
	separator bool
	footer    bool
	fit       fit
}

func tryLayout(inv *inventory.Inventory, cfg config, style render.Style, level qrcode.RecoveryLevel, cols, rows int, build func() string, minimal bool) (*layout, error) {
	report := build()

	// One row goes to the footer, and one to a blank separator, which is only
	// worth its row when there is a table above it to separate.
	separator := render.Lines(report) > 1
	budget := rows - render.Lines(report)
	if !cfg.noFooter {
		budget--
	}
	if separator {
		budget--
	}

	f, err := fitPayload(inv, cfg, style, level, cols, budget, minimal)
	if err != nil {
		return nil, err
	}
	// A budget that has already gone negative means the report alone overflows
	// the console, so no symbol fits underneath it however small. Without this
	// the zero-means-unconstrained convention inside fitPayload reports a fit.
	if budget <= 0 {
		f.ok = false
	}
	f.need = budget

	return &layout{report: report, separator: separator, fit: f, footer: !cfg.noFooter}, nil
}

func (l *layout) render(style render.Style) string {
	var sb strings.Builder
	sb.WriteString(l.report)
	if l.separator {
		sb.WriteString("\n")
	}
	for i, sym := range l.fit.symbols {
		if len(l.fit.symbols) > 1 {
			fmt.Fprintf(&sb, "  part %d of %d\n", i+1, len(l.fit.symbols))
		}
		sb.WriteString(sym.String(style))
	}
	// The overflow warning is printed even when the footer is off, because an
	// operator who cannot see the whole symbol needs to be told.
	if l.footer {
		sb.WriteString(l.fit.footer(style))
	} else if !l.fit.ok {
		sb.WriteString(l.fit.overflowNote(style))
	}
	return sb.String()
}

// fit is the outcome of trying to squeeze a payload into the space left over
// after the report was drawn.
type fit struct {
	symbols []*render.Symbol
	kind    string
	bytes   int
	// need is the number of console rows left for the symbols after the report
	// was drawn. It is reported when nothing fits, because the useful thing to
	// know then is how far short the console fell.
	need int
	ok   bool
}

func (f fit) footer(st render.Style) string {
	note := fmt.Sprintf("  %s payload, %d bytes, QR version %d",
		f.kind, f.bytes, f.symbols[0].Version)
	if len(f.symbols) > 1 {
		note += fmt.Sprintf(", %d symbols", len(f.symbols))
	}
	if !f.ok {
		note += "\n" + strings.TrimSuffix(f.overflowNote(st), "\n")
	}
	return note + "\n"
}

// overflowNote says how far short the console fell, so the shortfall is not
// left for the operator to work out from a half drawn symbol.
func (f fit) overflowNote(st render.Style) string {
	sym := f.symbols[0]
	return fmt.Sprintf("  (needs %dx%d, only %d rows left; full report in journalctl -u baretag)\n",
		sym.Cols(st), sym.Rows(st), max(f.need, 0))
}

// fitPayload picks the smallest encoding that fits, preferring a payload a
// phone can read directly over one that needs the decode subcommand, and
// preferring a single symbol over several.
func fitPayload(inv *inventory.Inventory, cfg config, style render.Style, level qrcode.RecoveryLevel, cols, rows int, minimal bool) (fit, error) {
	plain, err := payload.Plain(inv, minimal)
	if err != nil {
		return fit{}, err
	}
	packed, err := payload.Packed(inv, minimal)
	if err != nil {
		return fit{}, err
	}

	type candidate struct {
		kind string
		data string
	}
	var candidates []candidate
	switch cfg.format {
	case "auto":
		candidates = []candidate{{"plain", plain}, {"packed", packed}}
	case "plain":
		candidates = []candidate{{"plain", plain}}
	case "packed":
		candidates = []candidate{{"packed", packed}}
	default:
		return fit{}, fmt.Errorf("unknown format %q", cfg.format)
	}
	if minimal {
		for i := range candidates {
			candidates[i].kind += " minimal"
		}
	}

	for _, c := range candidates {
		for parts := 1; parts <= cfg.maxParts; parts++ {
			syms, err := encodeParts(c.data, parts, level)
			if err != nil {
				continue // too long for this many symbols; try more
			}
			if allFit(syms, style, cols, rows, parts) {
				return fit{symbols: syms, kind: c.kind, bytes: len(c.data), ok: true}, nil
			}
		}
	}

	last := candidates[len(candidates)-1]
	syms, err := encodeParts(last.data, 1, level)
	if err != nil {
		return fit{}, fmt.Errorf("payload of %d bytes does not fit any QR symbol: %w", len(last.data), err)
	}
	return fit{symbols: syms, kind: last.kind, bytes: len(last.data)}, nil
}

func encodeParts(data string, parts int, level qrcode.RecoveryLevel) ([]*render.Symbol, error) {
	chunks := payload.Split(data, parts)
	syms := make([]*render.Symbol, 0, len(chunks))
	for _, c := range chunks {
		s, err := render.Encode(c, level)
		if err != nil {
			return nil, err
		}
		syms = append(syms, s)
	}
	return syms, nil
}

// allFit checks the whole set against the console. Several symbols are stacked
// vertically, each with a caption row, so they share the row budget.
func allFit(syms []*render.Symbol, style render.Style, cols, rows, parts int) bool {
	total := 0
	for _, s := range syms {
		if !s.Fits(style, cols, 0) {
			return false
		}
		total += s.Rows(style)
		if parts > 1 {
			total++ // caption
		}
	}
	return rows <= 0 || total <= rows
}

func parseLevel(s string) (qrcode.RecoveryLevel, error) {
	switch strings.ToUpper(s) {
	case "L":
		return qrcode.Low, nil
	case "M":
		return qrcode.Medium, nil
	case "Q":
		return qrcode.High, nil
	case "H":
		return qrcode.Highest, nil
	default:
		return 0, fmt.Errorf("unknown error correction level %q", s)
	}
}

// geometry resolves the console size, preferring explicit flags, then the real
// terminal, then the assumption baked in for boot time use.
func geometry(cfg config) (cols, rows int) {
	cols, rows = cfg.cols, cfg.rows

	dc, dr := defaultCols, defaultRows
	if cols > 0 && rows > 0 {
		dc, dr = cols, rows
	} else if w, h, ok := consoleSize(cfg.tty); ok {
		dc, dr = w, h
	} else if w, h, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 && h > 0 {
		dc, dr = w, h
	}
	if cols <= 0 {
		cols = dc
	}
	if rows <= 0 {
		rows = dr
	}
	if rows -= cfg.reserve; rows < 1 {
		rows = 1
	}
	return cols, rows
}

// consoleSize asks a console device how big it is. At boot the output is a
// file or a pipe, so the only way to learn the real geometry of the screen the
// technician is looking at is to ask that screen directly. This matters: a
// UEFI framebuffer console is typically 128x48 while a BIOS text console is
// 80x25, and the largest QR code that scans differs accordingly.
func consoleSize(path string) (cols, rows int, ok bool) {
	if path == "" {
		return 0, 0, false
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOCTTY, 0)
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()

	w, h, err := term.GetSize(int(f.Fd()))
	if err != nil || w <= 0 || h <= 0 {
		return 0, 0, false
	}
	return w, h, true
}

// writeIssue installs the rendered output as a login banner. agetty expands
// backslash escapes in the banner, so literal backslashes are doubled.
func writeIssue(path, content string) error {
	escaped := strings.ReplaceAll(content, `\`, `\\`)
	if err := os.MkdirAll(dir(path), 0o755); err != nil {
		return fmt.Errorf("create banner directory: %w", err)
	}
	if err := os.WriteFile(path, []byte("\n"+escaped+"\n"), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func dir(path string) string {
	if i := strings.LastIndex(path, "/"); i > 0 {
		return path[:i]
	}
	return "."
}
