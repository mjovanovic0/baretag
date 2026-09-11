package render

import (
	"fmt"
	"strings"

	"github.com/mjovanovic/baretag/internal/inventory"
)

// Report draws the human readable half of the console output: the same facts
// that are in the QR code, for the technician standing at the machine who only
// needs to read one of them.
func Report(inv *inventory.Inventory, width int) string {
	if width < 40 {
		width = 40
	}

	var sb strings.Builder
	rule := strings.Repeat("=", width)

	sb.WriteString(rule + "\n")
	sb.WriteString(center("MACHINE INVENTORY", width) + "\n")
	sb.WriteString(rule + "\n")

	writeField(&sb, "Hostname", orDash(inv.Hostname))
	serial := orDash(inv.Serial)
	if inv.Serial != "" && inv.SerialSrc != "" {
		serial = fmt.Sprintf("%s  (%s)", inv.Serial, inv.SerialSrc)
	}
	writeField(&sb, "Serial", serial)
	if inv.AssetTag != "" {
		writeField(&sb, "Asset tag", inv.AssetTag)
	}
	if model := strings.TrimSpace(inv.Vendor + " " + inv.Product); model != "" {
		if inv.Virtual != "" {
			model += "  (" + inv.Virtual + ")"
		}
		writeField(&sb, "Model", model)
	}
	if hw := hardwareLine(inv); hw != "" {
		writeField(&sb, "Hardware", hw)
	}
	if fw := firmwareLine(inv); fw != "" {
		writeField(&sb, "Firmware", fw)
	}
	if inv.UUID != "" {
		writeField(&sb, "UUID", inv.UUID)
	}
	writeField(&sb, "Collected", inv.Collected)

	sb.WriteString("\n" + section("NETWORK INTERFACES", len(inv.NICs), width) + "\n")
	nicRows := [][]string{{"NAME", "MAC", "SPEED", "STATE", "ADDRESSES", "NEIGHBOUR"}}
	for _, n := range inv.NICs {
		nicRows = append(nicRows, []string{
			n.Name,
			orDash(n.MAC),
			n.SpeedString(),
			n.State,
			orDash(strings.Join(n.IPs, " ")),
			orDash(neighbour(n)),
		})
	}
	writeTable(&sb, nicRows, width)

	sb.WriteString("\n" + section("DISKS", len(inv.Disks), width) + "\n")
	diskRows := [][]string{{"DEVICE", "SIZE", "TYPE", "SERIAL", "MODEL"}}
	for _, d := range inv.Disks {
		diskRows = append(diskRows, []string{
			d.Path,
			d.SizeString(),
			orDash(d.Type),
			orDash(d.Serial),
			orDash(d.Model),
		})
	}
	writeTable(&sb, diskRows, width)

	return sb.String()
}

// neighbour renders what LLDP heard on a link as "switch port".
func neighbour(n inventory.NIC) string {
	return strings.TrimSpace(n.Switch + " " + n.Port)
}

// hardwareLine summarises how much machine this is.
func hardwareLine(inv *inventory.Inventory) string {
	var parts []string
	if inv.CPUs > 0 {
		cpu := fmt.Sprintf("%d CPU", inv.CPUs)
		if inv.CPUModel != "" {
			cpu += " " + inv.CPUModel
		}
		parts = append(parts, cpu)
	}
	if inv.MemoryBytes > 0 {
		parts = append(parts, memoryString(inv.MemoryBytes)+" RAM")
	}
	if inv.Arch != "" {
		parts = append(parts, inv.Arch)
	}
	return strings.Join(parts, ", ")
}

// firmwareLine gathers the facts that decide how a machine is installed.
func firmwareLine(inv *inventory.Inventory) string {
	var parts []string
	if inv.Firmware != "" {
		parts = append(parts, inv.Firmware)
	}
	if inv.BIOSVersion != "" {
		parts = append(parts, "BIOS "+inv.BIOSVersion)
	}
	if inv.TPM != "" {
		parts = append(parts, "TPM "+inv.TPM)
	}
	return strings.Join(parts, ", ")
}

// memoryString renders memory in powers of two, which is how it is fitted and
// sold, unlike a disk.
func memoryString(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.0f%ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func section(title string, count, width int) string {
	head := fmt.Sprintf("-- %s (%d) ", title, count)
	if len(head) < width {
		head += strings.Repeat("-", width-len(head))
	}
	return head
}

func writeField(sb *strings.Builder, label, value string) {
	fmt.Fprintf(sb, "  %-11s %s\n", label+":", value)
}

// writeTable prints rows in aligned columns, trimming the last column rather
// than letting a long model name wrap and ruin the layout.
func writeTable(sb *strings.Builder, rows [][]string, width int) {
	if len(rows) <= 1 {
		sb.WriteString("  (none found)\n")
		return
	}

	cols := len(rows[0])
	widths := make([]int, cols)
	for _, r := range rows {
		for i, cell := range r {
			if len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}

	for _, r := range rows {
		var line strings.Builder
		line.WriteString("  ")
		for i, cell := range r {
			if i == cols-1 {
				line.WriteString(cell)
				continue
			}
			fmt.Fprintf(&line, "%-*s  ", widths[i], cell)
		}
		sb.WriteString(truncate(strings.TrimRight(line.String(), " "), width) + "\n")
	}
}

func truncate(s string, width int) string {
	if len(s) <= width {
		return s
	}
	if width <= 3 {
		return s[:width]
	}
	return s[:width-3] + "..."
}

func center(s string, width int) string {
	if len(s) >= width {
		return s
	}
	pad := (width - len(s)) / 2
	return strings.Repeat(" ", pad) + s
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// ReportCompact draws one line per device. A full report plus a scannable QR
// code rarely fit together on a 1024x768 console, and when something has to
// give it should be the whitespace, not the facts.
func ReportCompact(inv *inventory.Inventory, width int) string {
	var sb strings.Builder

	head := "HOST " + orDash(inv.Hostname) + "   SERIAL " + orDash(inv.Serial)
	if inv.AssetTag != "" {
		head += "   ASSET " + inv.AssetTag
	}
	if model := strings.TrimSpace(inv.Vendor + " " + inv.Product); model != "" {
		head += "   " + model
	}
	sb.WriteString(truncate(head, width) + "\n")

	if sys := compactSystem(inv); sys != "" {
		sb.WriteString(truncate(sys, width) + "\n")
	}

	for _, n := range inv.NICs {
		line := fmt.Sprintf("NIC %s %s %s %s %s %s",
			n.Name, orDash(n.MAC), n.SpeedString(), n.State,
			strings.Join(n.IPs, ","), neighbourCompact(n))
		sb.WriteString(truncate(strings.TrimRight(line, " "), width) + "\n")
	}
	for _, d := range inv.Disks {
		line := fmt.Sprintf("DSK %s %s %s %s",
			d.Path, d.SizeString(), orDash(d.Type), orDash(d.Serial))
		sb.WriteString(truncate(strings.TrimRight(line, " "), width) + "\n")
	}

	return sb.String()
}

// compactSystem is the one line worth spending on the machine as a whole.
func compactSystem(inv *inventory.Inventory) string {
	var parts []string
	if inv.CPUs > 0 {
		parts = append(parts, fmt.Sprintf("%dcpu", inv.CPUs))
	}
	if inv.MemoryBytes > 0 {
		parts = append(parts, memoryString(inv.MemoryBytes))
	}
	if inv.Arch != "" {
		parts = append(parts, inv.Arch)
	}
	if inv.Firmware != "" {
		parts = append(parts, inv.Firmware)
	}
	if inv.Virtual != "" {
		parts = append(parts, inv.Virtual)
	}
	if len(parts) == 0 {
		return ""
	}
	return "SYS " + strings.Join(parts, " ")
}

// neighbourCompact renders a neighbour as switch/port, which reads as one
// token on a crowded line.
func neighbourCompact(n inventory.NIC) string {
	switch {
	case n.Switch != "" && n.Port != "":
		return n.Switch + "/" + n.Port
	case n.Switch != "":
		return n.Switch
	default:
		return n.Port
	}
}

// ReportIdentity is the last fallback: just enough to tell two racked machines
// apart, leaving every remaining console row to the QR code.
func ReportIdentity(inv *inventory.Inventory, width int) string {
	line := fmt.Sprintf("HOST %s   SERIAL %s", orDash(inv.Hostname), orDash(inv.Serial))
	if sys := compactSystem(inv); sys != "" {
		line += "   " + strings.TrimPrefix(sys, "SYS ")
	}
	line += fmt.Sprintf("   %d NIC / %d disk", len(inv.NICs), len(inv.Disks))
	return truncate(line, width) + "\n"
}

// Lines counts the console rows a rendered block occupies.
func Lines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(strings.TrimSuffix(s, "\n"), "\n") + 1
}
