// Package inventory collects hardware facts about the running machine from
// sysfs, procfs and the kernel's network interface list.
package inventory

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Inventory is the complete set of facts encoded into the QR code.
type Inventory struct {
	Hostname  string `json:"hostname"`
	Serial    string `json:"serial,omitempty"`
	SerialSrc string `json:"serial_source,omitempty"`
	Vendor    string `json:"vendor,omitempty"`
	Product   string `json:"product,omitempty"`
	UUID      string `json:"uuid,omitempty"`
	Collected string `json:"collected"`
	NICs      []NIC  `json:"nics"`
	Disks     []Disk `json:"disks"`
}

// Options controls which devices are reported.
type Options struct {
	// AllNICs reports virtual interfaces that carry no address, such as
	// bridges and veth pairs, which are otherwise skipped.
	AllNICs bool
	// AllDisks reports loop, ram and device-mapper nodes as well.
	AllDisks bool
	// UseUdev allows falling back to udevadm when sysfs has no serial.
	UseUdev bool
	// Root prefixes every sysfs and device path. It is empty in production and
	// set in tests, so that the collectors can be pointed at a sysfs tree
	// captured from a real server instead of the machine running the tests.
	Root string
}

// path joins a system path onto the configured root.
func (o Options) path(parts ...string) string {
	return filepath.Join(append([]string{o.Root}, parts...)...)
}

// DefaultOptions are the settings used when the tool runs unattended on boot.
func DefaultOptions() Options {
	return Options{UseUdev: true}
}

// Collect gathers every fact the tool knows how to read. It never fails as a
// whole: a device that cannot be read contributes whatever fields did resolve,
// because a partial inventory is still worth showing on the console.
func Collect(opts Options) *Inventory {
	inv := &Inventory{
		Collected: time.Now().UTC().Format(time.RFC3339),
		NICs:      collectNICs(opts),
		Disks:     collectDisks(opts),
	}

	if h, err := os.Hostname(); err == nil {
		inv.Hostname = h
	}
	inv.applyDMI(opts)

	return inv
}

// readSysFile returns the trimmed contents of a sysfs attribute, or "" when the
// attribute is missing or unreadable. Most sysfs reads are best effort.
func readSysFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// placeholders are the strings firmware vendors ship when a field was never
// programmed. They are worse than an empty value because they look like data.
var placeholders = map[string]bool{
	"":                                     true,
	"none":                                 true,
	"unknown":                              true,
	"n/a":                                  true,
	"na":                                   true,
	"null":                                 true,
	"0":                                    true,
	"default string":                       true,
	"not specified":                        true,
	"not applicable":                       true,
	"not available":                        true,
	"to be filled by o.e.m.":               true,
	"to be filled by o.e.m":                true,
	"system serial number":                 true,
	"chassis serial number":                true,
	"base board serial number":             true,
	"filled by oem":                        true,
	"empty":                                true,
	"xxxxxxx":                              true,
	"00000000-0000-0000-0000-000000000000": true,
}

// meaningful reports whether a firmware string carries real information.
func meaningful(s string) bool {
	return !placeholders[strings.ToLower(strings.TrimSpace(s))]
}

// clean normalises a firmware or sysfs string, collapsing the inner whitespace
// that SCSI vendor fields are padded with.
func clean(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
