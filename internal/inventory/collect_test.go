package inventory

import (
	"strings"
	"testing"
	"time"
)

// opts points the collectors at the captured sysfs tree in testdata, which
// stands in for a Dell R750 with four NICs and four disks behind a mix of
// NVMe, a RAID controller and plain SATA.
func opts() Options {
	// udev is disabled: the fixture provides the by-id links itself, and
	// shelling out to udevadm would read the machine running the test.
	return Options{Root: "testdata/r750"}
}

func TestSerialSkipsFirmwarePlaceholders(t *testing.T) {
	inv := Collect(opts())

	// chassis_serial in the fixture is "To Be Filled By O.E.M.", which looks
	// like data but is not, so the next source has to win.
	if inv.Serial != "JHK3M92" {
		t.Errorf("Serial = %q, want JHK3M92", inv.Serial)
	}
	if inv.SerialSrc != "product_serial" {
		t.Errorf("SerialSrc = %q, want product_serial", inv.SerialSrc)
	}
	if inv.Vendor != "Dell Inc." || inv.Product != "PowerEdge R750" {
		t.Errorf("Vendor/Product = %q / %q", inv.Vendor, inv.Product)
	}
}

func TestNICsReportPhysicalPortsOnly(t *testing.T) {
	inv := Collect(opts())

	var names []string
	for _, n := range inv.NICs {
		names = append(names, n.Name)
	}
	want := []string{"eno1", "eno2", "ens3f0", "ens3f1"}
	if len(names) != len(want) {
		t.Fatalf("NICs = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("NICs = %v, want %v", names, want)
		}
	}

	byName := map[string]NIC{}
	for _, n := range inv.NICs {
		byName[n.Name] = n
	}

	if got := byName["eno1"]; got.Speed != 10000 || got.State != "up" {
		t.Errorf("eno1 = %+v, want 10000 Mbps up", got)
	}
	// A down link reports a speed of -1, which must not surface as a number.
	if got := byName["eno2"]; got.Speed != 0 || got.State != "down" {
		t.Errorf("eno2 = %+v, want speed 0 and state down", got)
	}
	if got := byName["ens3f0"].SpeedString(); got != "25G" {
		t.Errorf("ens3f0 SpeedString() = %q, want 25G", got)
	}
	if got := byName["eno2"].SpeedString(); got != "-" {
		t.Errorf("eno2 SpeedString() = %q, want -", got)
	}
}

func TestDiskSerialsComeFromEverySource(t *testing.T) {
	inv := Collect(opts())

	byPath := map[string]Disk{}
	for _, d := range inv.Disks {
		byPath[d.Path] = d
	}

	cases := []struct {
		path, serial, why string
	}{
		{"/dev/nvme0n1", "S6EWNG0T801234", "device/serial attribute"},
		{"/dev/sda", "PHYS7412000M960CGN", "SCSI VPD page 0x80"},
		{"/dev/sdb", "S6KHNA0T123456", "udev by-id link"},
		{"/dev/vda", "QRTESTVIRTIO01", "serial attribute on the block device, as virtio-blk uses"},
	}
	for _, c := range cases {
		got, ok := byPath[c.path]
		if !ok {
			t.Errorf("%s missing from the inventory", c.path)
			continue
		}
		if got.Serial != c.serial {
			t.Errorf("%s serial = %q, want %q (from the %s)", c.path, got.Serial, c.serial, c.why)
		}
	}

	if got := byPath["/dev/sda"]; got.Size != 960197124096 {
		t.Errorf("/dev/sda size = %d bytes, want 960197124096", got.Size)
	}
	if got := byPath["/dev/sda"].SizeString(); got != "960.2GB" {
		t.Errorf("/dev/sda SizeString() = %q, want 960.2GB", got)
	}
	// The vendor field reads "ATA" for a plain SATA disk, which is noise
	// rather than a manufacturer.
	if got := byPath["/dev/sdb"].Model; got != "SAMSUNG MZ7L3960" {
		t.Errorf("/dev/sdb model = %q, want the model without the ATA vendor", got)
	}
}

func TestNonDisksAreLeftOut(t *testing.T) {
	inv := Collect(opts())

	for _, d := range inv.Disks {
		switch d.Path {
		case "/dev/loop0", "/dev/ram0", "/dev/zram0", "/dev/dm-0", "/dev/md0", "/dev/sr0":
			t.Errorf("%s is not a disk in the chassis but was reported", d.Path)
		case "/dev/sdc":
			t.Errorf("/dev/sdc is an empty slot of zero size but was reported")
		}
	}
	if len(inv.Disks) != 5 {
		t.Errorf("reported %d disks, want 5", len(inv.Disks))
	}
}

func TestAllDisksIncludesTheRest(t *testing.T) {
	o := opts()
	o.AllDisks = true
	inv := Collect(o)

	if len(inv.Disks) <= 5 {
		t.Errorf("--all-disks reported %d disks, want more than the 5 real ones", len(inv.Disks))
	}
}

func TestAllNICsIncludesVirtualInterfaces(t *testing.T) {
	o := opts()
	o.AllNICs = true
	inv := Collect(o)

	var found bool
	for _, n := range inv.NICs {
		if n.Name == "bond0" {
			found = true
		}
		if n.Name == "lo" {
			t.Error("loopback should never be reported")
		}
	}
	if !found {
		t.Error("--all-nics did not report bond0")
	}
}

func TestSystemFacts(t *testing.T) {
	inv := Collect(opts())

	if inv.CPUs != 64 {
		t.Errorf("CPUs = %d, want 64 from the 0-63 range the kernel publishes", inv.CPUs)
	}
	if !strings.Contains(inv.CPUModel, "Xeon") {
		t.Errorf("CPUModel = %q", inv.CPUModel)
	}
	// 536608768 kB as the kernel reports it, in bytes.
	if want := int64(536608768) * 1024; inv.MemoryBytes != want {
		t.Errorf("MemoryBytes = %d, want %d", inv.MemoryBytes, want)
	}
	if inv.Firmware != "uefi" {
		t.Errorf("Firmware = %q, want uefi; the fixture has /sys/firmware/efi", inv.Firmware)
	}
	if inv.TPM != "2.0" {
		t.Errorf("TPM = %q, want 2.0", inv.TPM)
	}
	if inv.AssetTag != "RACK14-U07" {
		t.Errorf("AssetTag = %q", inv.AssetTag)
	}
	if inv.BIOSVersion != "2.15.1" {
		t.Errorf("BIOSVersion = %q", inv.BIOSVersion)
	}
	// The fixture is a Dell, so nothing should claim it is a virtual machine.
	if inv.Virtual != "" {
		t.Errorf("Virtual = %q, want empty on a PowerEdge", inv.Virtual)
	}
	// Architecture describes the kernel this binary is running on rather than
	// the captured tree, so only its presence can be asserted here.
	if inv.Arch == "" {
		t.Error("Arch is empty")
	}
}

func TestDiskTypeAndWWN(t *testing.T) {
	byPath := map[string]Disk{}
	for _, d := range Collect(opts()).Disks {
		byPath[d.Path] = d
	}

	cases := []struct{ path, typ, wwn, why string }{
		{"/dev/nvme0n1", "nvme", "eui.3634473052801234", "wwid on the block device"},
		{"/dev/sda", "ssd", "naa.6d0946606b1b2c002b9f0e1a3c4d5e6f", "wwid under the SCSI device"},
		{"/dev/sdb", "hdd", "0x5002538f31a1b2c3", "the udev wwn- link"},
	}
	for _, c := range cases {
		got, ok := byPath[c.path]
		if !ok {
			t.Errorf("%s missing", c.path)
			continue
		}
		if got.Type != c.typ {
			t.Errorf("%s type = %q, want %q", c.path, got.Type, c.typ)
		}
		if got.WWN != c.wwn {
			t.Errorf("%s wwn = %q, want %q (from %s)", c.path, got.WWN, c.wwn, c.why)
		}
	}
}

func TestNICPCIAddress(t *testing.T) {
	byName := map[string]NIC{}
	for _, n := range Collect(opts()).NICs {
		byName[n.Name] = n
	}

	// The slot survives a rename, which the interface name does not.
	for name, want := range map[string]string{
		"eno1":   "0000:19:00.0",
		"ens3f1": "0000:5e:00.1",
	} {
		if got := byName[name].PCI; got != want {
			t.Errorf("%s PCI = %q, want %q", name, got, want)
		}
	}
}

// TestNeighboursAreNotSoughtAgainstAFixture makes sure pointing the collectors
// at a captured tree never puts traffic on the machine running the tests.
func TestNeighboursAreNotSoughtAgainstAFixture(t *testing.T) {
	o := opts()
	o.LLDPWait = time.Hour // would hang for an hour if it were honoured

	done := make(chan struct{})
	go func() {
		Collect(o)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("collection listened for neighbours despite reading a captured tree")
	}
}
