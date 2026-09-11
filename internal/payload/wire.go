package payload

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mjovanovic/baretag/internal/inventory"
)

// wire is the shape actually carried by the QR code. Keys are one or two
// characters and each device is an array rather than an object, because the
// field names would otherwise be repeated once per disk. Measured on a four
// NIC, four disk server this is the difference between a 77 module symbol and
// a 69 module one, which is the difference between fitting on a console and
// not.
//
// Decode turns this back into the long form, so nothing downstream has to know
// about the abbreviations.
type wire struct {
	H  string `json:"h,omitempty"`  // hostname
	S  string `json:"s,omitempty"`  // serial
	SS string `json:"ss,omitempty"` // which DMI field the serial came from
	AT string `json:"at,omitempty"` // asset tag
	VN string `json:"vn,omitempty"` // vendor
	P  string `json:"p,omitempty"`  // product
	U  string `json:"u,omitempty"`  // product uuid
	VT string `json:"vt,omitempty"` // hypervisor, empty on bare metal
	FW string `json:"fw,omitempty"` // uefi or bios
	BV string `json:"bv,omitempty"` // bios version
	A  string `json:"a,omitempty"`  // architecture
	C  int    `json:"c,omitempty"`  // logical cpus
	CM string `json:"cm,omitempty"` // cpu model
	M  int64  `json:"m,omitempty"`  // memory in bytes
	TP string `json:"tp,omitempty"` // tpm version
	T  string `json:"t,omitempty"`  // collection time

	// Device rows are arrays so the field names are not repeated once per
	// device. Positions are fixed: a field that is not wanted is blanked
	// rather than removed, because the reader works by index.
	N [][]any `json:"n,omitempty"` // name, mac, speed, state, ips, pci, switch, port
	D [][]any `json:"d,omitempty"` // path, size, serial, model, type, wwn
}

// trimTrailing drops empty values from the end of a device row. A disk with no
// model then costs three elements instead of four.
func trimTrailing(row []any) []any {
	for len(row) > 1 {
		last := row[len(row)-1]
		if s, ok := last.(string); ok && s == "" {
			row = row[:len(row)-1]
			continue
		}
		if n, ok := last.(int); ok && n == 0 {
			row = row[:len(row)-1]
			continue
		}
		break
	}
	return row
}

// toWire converts an inventory to its compact form. When minimal is set the
// descriptive extras are dropped, keeping only the facts needed to identify the
// machine and its parts.
func toWire(inv *inventory.Inventory, minimal bool) *wire {
	w := &wire{
		H:  inv.Hostname,
		S:  inv.Serial,
		AT: inv.AssetTag,
		VT: inv.Virtual,
		FW: inv.Firmware,
		A:  inv.Arch,
		C:  inv.CPUs,
		M:  inv.MemoryBytes,
		T:  inv.Collected,
	}
	// What minimal gives up is description. How much machine there is, how it
	// boots and what it is cabled to are what the inventory is for, so they
	// stay whatever the console can hold.
	if !minimal {
		w.SS, w.VN, w.P, w.U = inv.SerialSrc, inv.Vendor, inv.Product, inv.UUID
		w.BV, w.CM, w.TP = inv.BIOSVersion, inv.CPUModel, inv.TPM
	}

	for _, n := range inv.NICs {
		pci := n.PCI
		if minimal {
			pci = ""
		}
		w.N = append(w.N, trimTrailing([]any{
			n.Name, n.MAC, n.Speed, n.State, strings.Join(n.IPs, ","),
			pci, n.Switch, n.Port,
		}))
	}
	for _, d := range inv.Disks {
		model, wwn := d.Model, d.WWN
		if minimal {
			model, wwn = "", ""
		}
		w.D = append(w.D, trimTrailing([]any{
			d.Path, d.Size, d.Serial, model, d.Type, wwn,
		}))
	}
	return w
}

// toInventory expands the compact form. Rows are read defensively by index
// because trailing empty fields were trimmed away on the way out.
func (w *wire) toInventory() *inventory.Inventory {
	inv := &inventory.Inventory{
		Hostname:    w.H,
		Serial:      w.S,
		SerialSrc:   w.SS,
		AssetTag:    w.AT,
		Vendor:      w.VN,
		Product:     w.P,
		UUID:        w.U,
		Virtual:     w.VT,
		Firmware:    w.FW,
		BIOSVersion: w.BV,
		Arch:        w.A,
		CPUs:        w.C,
		CPUModel:    w.CM,
		MemoryBytes: w.M,
		TPM:         w.TP,
		Collected:   w.T,
	}

	for _, row := range w.N {
		nic := inventory.NIC{
			Name:   str(row, 0),
			MAC:    str(row, 1),
			Speed:  num(row, 2),
			State:  str(row, 3),
			PCI:    str(row, 5),
			Switch: str(row, 6),
			Port:   str(row, 7),
		}
		if ips := str(row, 4); ips != "" {
			nic.IPs = strings.Split(ips, ",")
		}
		if nic.State == "" {
			nic.State = "unknown"
		}
		inv.NICs = append(inv.NICs, nic)
	}
	for _, row := range w.D {
		inv.Disks = append(inv.Disks, inventory.Disk{
			Path:   str(row, 0),
			Size:   int64(num(row, 1)),
			Serial: str(row, 2),
			Model:  str(row, 3),
			Type:   str(row, 4),
			WWN:    str(row, 5),
		})
	}
	return inv
}

func str(row []any, i int) string {
	if i >= len(row) {
		return ""
	}
	s, _ := row[i].(string)
	return s
}

// num reads a numeric cell. Values arrive as float64 after a JSON round trip.
func num(row []any, i int) int {
	if i >= len(row) {
		return 0
	}
	switch v := row[i].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}

// expand turns a compact JSON payload into the readable long form.
func expand(compact string) (string, error) {
	var w wire
	if err := json.Unmarshal([]byte(compact), &w); err != nil {
		return "", fmt.Errorf("parse payload: %w", err)
	}
	b, err := json.MarshalIndent(w.toInventory(), "", "  ")
	if err != nil {
		return "", fmt.Errorf("render inventory: %w", err)
	}
	return string(b), nil
}
