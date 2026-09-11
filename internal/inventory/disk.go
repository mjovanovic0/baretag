package inventory

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	blockDir = "/sys/block"
	byIDDir  = "/dev/disk/by-id"
)

// Disk is one whole block device. Partitions are deliberately not reported.
type Disk struct {
	Path   string `json:"path"`
	Size   int64  `json:"size_bytes"`
	Serial string `json:"serial,omitempty"`
	Model  string `json:"model,omitempty"`
	// Type is nvme, ssd or hdd. Installers pick a root device by it, and
	// nobody wants an operating system on the spinning disk.
	Type string `json:"type,omitempty"`
	// WWN is the world wide name, the most stable way to name a disk across
	// reboots and controller reorderings.
	WWN string `json:"wwn,omitempty"`
}

// SizeString renders capacity the way a disk is sold, in powers of ten, which
// is what is printed on the label a technician is holding.
func (d Disk) SizeString() string {
	const unit = 1000
	if d.Size < unit {
		return fmt.Sprintf("%dB", d.Size)
	}
	div, exp := int64(unit), 0
	for n := d.Size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(d.Size)/float64(div), "kMGTPE"[exp])
}

// skipPrefixes are block device kinds that are not disks in the chassis.
var skipPrefixes = []string{"loop", "ram", "zram", "dm-", "md", "sr", "fd", "nbd"}

func skipBlock(name string) bool {
	for _, p := range skipPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

func collectDisks(opts Options) []Disk {
	entries, err := os.ReadDir(opts.path(blockDir))
	if err != nil {
		return nil
	}

	byID := linksByID(opts, byIDPrefixes)
	wwnByID := linksByID(opts, []string{"nvme-eui.", "wwn-"})
	var disks []Disk

	for _, e := range entries {
		name := e.Name()
		if !opts.AllDisks && skipBlock(name) {
			continue
		}

		dir := opts.path(blockDir, name)
		disk := Disk{Path: "/dev/" + name}

		// The kernel always reports size in 512 byte sectors here, whatever the
		// device's real logical block size.
		if sectors, err := strconv.ParseInt(readSysFile(filepath.Join(dir, "size")), 10, 64); err == nil {
			disk.Size = sectors * 512
		}
		// A size of zero means an empty slot, such as a card reader with no
		// card in it. Reporting it would only add noise to the QR code.
		if disk.Size == 0 && !opts.AllDisks {
			continue
		}

		disk.Model = diskModel(dir)
		disk.Serial = diskSerial(dir, name, byID, opts)
		disk.Type = driveType(dir, name)
		disk.WWN = diskWWN(dir, name, wwnByID)

		disks = append(disks, disk)
	}

	sort.Slice(disks, func(i, j int) bool { return disks[i].Path < disks[j].Path })
	return disks
}

// driveType tells a spinning disk from a solid state one. NVMe is called out
// separately because it is the thing an installer usually wants.
func driveType(dir, name string) string {
	if strings.HasPrefix(name, "nvme") {
		return "nvme"
	}
	switch readSysFile(filepath.Join(dir, "queue", "rotational")) {
	case "0":
		return "ssd"
	case "1":
		return "hdd"
	default:
		return ""
	}
}

// diskWWN returns the world wide name. It is not a serial number, but it is
// unique and stable, which is what a root device hint needs.
func diskWWN(dir, name string, byID map[string]string) string {
	for _, attr := range []string{"wwid", "device/wwid"} {
		if v := clean(readSysFile(filepath.Join(dir, attr))); meaningful(v) {
			return firstField(v)
		}
	}
	if v, ok := byID[name]; ok && meaningful(v) {
		return v
	}
	return ""
}

// firstField drops the vendor prefix the kernel puts in front of some world
// wide names, such as "naa.6000..." or "t10.ATA     ...".
func firstField(s string) string {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return s
	}
	return fields[len(fields)-1]
}

// diskModel prefers the vendor and model pair that SCSI exposes, falling back
// to the single model string NVMe uses.
func diskModel(dir string) string {
	model := clean(readSysFile(filepath.Join(dir, "device", "model")))
	vendor := clean(readSysFile(filepath.Join(dir, "device", "vendor")))
	switch {
	case vendor != "" && model != "" && !strings.EqualFold(vendor, "ATA"):
		return clean(vendor + " " + model)
	case model != "":
		return model
	default:
		return clean(readSysFile(filepath.Join(dir, "device", "name")))
	}
}

// diskSerial resolves a disk serial from the cheapest source that has one.
// No single source covers NVMe, SATA behind AHCI and SAS behind a RAID HBA, so
// the lookups are tried in order of how directly they name the drive.
func diskSerial(dir, name string, byID map[string]string, opts Options) string {
	// NVMe controllers and many SCSI disks publish the serial directly.
	if s := clean(readSysFile(filepath.Join(dir, "device", "serial"))); meaningful(s) {
		return s
	}
	// virtio-blk devices, and NVMe namespaces on newer kernels, publish it on
	// the block device itself rather than under the backing device.
	if s := clean(readSysFile(filepath.Join(dir, "serial"))); meaningful(s) {
		return s
	}
	// SCSI disks expose VPD page 0x80, the unit serial number page.
	if s := vpdSerial(filepath.Join(dir, "device", "vpd_pg80")); meaningful(s) {
		return s
	}
	// udev has already done this work for every disk it enumerated, and its
	// by-id links encode the serial in the file name.
	if s, ok := byID[name]; ok && meaningful(s) {
		return s
	}
	// A world wide name is not a serial, but it is a stable unique identifier
	// and is better than showing nothing.
	if s := clean(readSysFile(filepath.Join(dir, "device", "wwid"))); meaningful(s) {
		return s
	}
	if s := clean(readSysFile(filepath.Join(dir, "wwid"))); meaningful(s) {
		return s
	}
	if opts.UseUdev {
		if s := udevSerial(name); meaningful(s) {
			return s
		}
	}
	return ""
}

// vpdSerial parses SCSI VPD page 0x80. The page is a four byte header, whose
// last two bytes are the big endian length, followed by the serial itself.
func vpdSerial(path string) string {
	b, err := os.ReadFile(path)
	if err != nil || len(b) < 4 || b[1] != 0x80 {
		return ""
	}
	n := int(b[2])<<8 | int(b[3])
	if n <= 0 || 4+n > len(b) {
		return ""
	}
	return clean(strings.Trim(string(b[4:4+n]), "\x00 "))
}

// byIDPrefixes are the udev link kinds that embed a serial, most specific
// first. wwn links are omitted because they carry a world wide name instead.
var byIDPrefixes = []string{"nvme-eui.", "nvme-", "ata-", "scsi-", "usb-", "mmc-", "virtio-"}

// linksByID resolves every /dev/disk/by-id link once and indexes what it
// encodes by the kernel device name it points at. The prefixes decide which
// kind of identifier is wanted: a serial, or a world wide name.
func linksByID(opts Options, prefixes []string) map[string]string {
	out := map[string]string{}

	entries, err := os.ReadDir(opts.path(byIDDir))
	if err != nil {
		return out
	}
	for _, e := range entries {
		link := opts.path(byIDDir, e.Name())
		target, err := filepath.EvalSymlinks(link)
		if err != nil {
			continue
		}
		dev := filepath.Base(target)
		// Partition links point at sdb1 and the like; only whole disks count.
		if _, err := os.Stat(opts.path(blockDir, dev)); err != nil {
			continue
		}

		for _, prefix := range prefixes {
			if !strings.HasPrefix(e.Name(), prefix) {
				continue
			}
			// udev builds these names as model_serial, so the serial is the
			// tail. Some links carry no underscore and are serial only.
			s := strings.TrimPrefix(e.Name(), prefix)
			if idx := strings.LastIndex(s, "_"); idx >= 0 {
				s = s[idx+1:]
			}
			existing, seen := out[dev]
			// byIDPrefixes is ordered most specific first, so the first
			// prefix that matches is the one to trust, unless it yielded
			// nothing and a later link did better.
			if !seen || (meaningful(s) && !meaningful(existing)) {
				out[dev] = s
			}
			break
		}
	}
	return out
}

// udevSerial is the last resort for disks behind a RAID HBA, where the serial
// only appears in udev's database and not in sysfs.
func udevSerial(name string) string {
	out, err := exec.Command("udevadm", "info", "--query=property", "--name=/dev/"+name).Output()
	if err != nil {
		return ""
	}
	props := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			props[k] = v
		}
	}
	for _, key := range []string{"ID_SERIAL_SHORT", "SCSI_IDENT_SERIAL", "ID_SCSI_SERIAL", "ID_SERIAL"} {
		if v := clean(props[key]); meaningful(v) {
			return v
		}
	}
	return ""
}
