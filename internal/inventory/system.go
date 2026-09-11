package inventory

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// applySystem fills in the facts about the machine as a whole: how much of it
// there is, how it boots, and whether it is a machine at all.
func (inv *Inventory) applySystem(opts Options) {
	inv.Arch = architecture()
	inv.CPUs = cpuCount(opts)
	inv.CPUModel = cpuModel(opts)
	inv.MemoryBytes = memoryBytes(opts)
	inv.Firmware = firmwareMode(opts)
	inv.Virtual = hypervisor(opts)
	inv.TPM = tpmVersion(opts)
}

// architecture reports the name the wider ecosystem uses rather than Go's, so
// that a scanned inventory lines up with what an installer expects to see.
func architecture() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	default:
		return runtime.GOARCH
	}
}

// cpuCount counts logical processors. The kernel publishes them as a range
// list such as "0-63" or "0-7,16-23".
func cpuCount(opts Options) int {
	present := readSysFile(opts.path("/sys/devices/system/cpu/present"))
	if present == "" {
		return cpuCountFromProc(opts)
	}

	total := 0
	for _, part := range strings.Split(present, ",") {
		lo, hi, ok := strings.Cut(part, "-")
		first, err := strconv.Atoi(strings.TrimSpace(lo))
		if err != nil {
			continue
		}
		if !ok {
			total++
			continue
		}
		last, err := strconv.Atoi(strings.TrimSpace(hi))
		if err != nil {
			continue
		}
		total += last - first + 1
	}
	if total == 0 {
		return cpuCountFromProc(opts)
	}
	return total
}

func cpuCountFromProc(opts Options) int {
	n := 0
	forEachProcCPULine(opts, func(key, _ string) bool {
		if key == "processor" {
			n++
		}
		return true
	})
	return n
}

// cpuModel returns the marketing name of the processor. Arm systems often do
// not publish one, in which case this is empty and nothing pretends otherwise.
func cpuModel(opts Options) string {
	var model string
	forEachProcCPULine(opts, func(key, value string) bool {
		switch key {
		case "model name", "Model", "cpu model":
			model = clean(value)
			return false
		}
		return true
	})
	return model
}

func forEachProcCPULine(opts Options, fn func(key, value string) bool) {
	f, err := os.Open(opts.path("/proc/cpuinfo"))
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		key, value, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		if !fn(strings.TrimSpace(key), strings.TrimSpace(value)) {
			return
		}
	}
}

// memoryBytes reads installed memory. MemTotal is what the kernel can use, so
// it reads a little under the memory physically fitted.
func memoryBytes(opts Options) int64 {
	f, err := os.Open(opts.path("/proc/meminfo"))
	if err != nil {
		return 0
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		key, value, ok := strings.Cut(sc.Text(), ":")
		if !ok || strings.TrimSpace(key) != "MemTotal" {
			continue
		}
		fields := strings.Fields(value)
		if len(fields) == 0 {
			return 0
		}
		kb, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			return 0
		}
		return kb * 1024
	}
	return 0
}

// firmwareMode distinguishes a UEFI boot from a legacy BIOS one. The kernel
// only creates /sys/firmware/efi when it was handed an EFI system table.
func firmwareMode(opts Options) string {
	if st, err := os.Stat(opts.path("/sys/firmware/efi")); err == nil && st.IsDir() {
		return "uefi"
	}
	return "bios"
}

// hypervisors maps a DMI vendor or product string to the platform it means.
// The match is on a prefix because vendors append their own detail.
var hypervisors = []struct{ match, name string }{
	{"qemu", "qemu"},
	{"kvm", "kvm"},
	{"vmware", "vmware"},
	{"microsoft corporation", "hyper-v"},
	{"xen", "xen"},
	{"innotek gmbh", "virtualbox"},
	{"virtualbox", "virtualbox"},
	{"parallels", "parallels"},
	{"bochs", "qemu"},
	{"amazon ec2", "aws"},
	{"google", "gce"},
	{"openstack", "openstack"},
	{"nutanix", "nutanix"},
	{"alibaba cloud", "alibaba"},
	{"red hat", "kvm"},
}

// hypervisor names the platform this is running on, or returns empty on a
// physical machine.
func hypervisor(opts Options) string {
	if t := readSysFile(opts.path("/sys/hypervisor/type")); t != "" {
		return clean(t)
	}
	for _, attr := range []string{"sys_vendor", "product_name", "board_vendor"} {
		v := strings.ToLower(clean(readSysFile(opts.path(dmiDir, attr))))
		if v == "" {
			continue
		}
		for _, h := range hypervisors {
			if strings.HasPrefix(v, h.match) {
				return h.name
			}
		}
	}
	return ""
}

// tpmVersion reports the TPM family, which matters wherever disks are
// encrypted against the platform rather than a passphrase.
func tpmVersion(opts Options) string {
	dir := opts.path("/sys/class/tpm")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if major := readSysFile(filepath.Join(dir, e.Name(), "tpm_version_major")); major != "" {
			if major == "2" {
				return "2.0"
			}
			return major
		}
		// Older kernels expose a 1.2 device with no version attribute.
		if _, err := os.Stat(filepath.Join(dir, e.Name(), "device")); err == nil {
			return "1.2"
		}
	}
	return ""
}
