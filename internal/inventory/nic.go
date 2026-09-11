package inventory

import (
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/mjovanovic/baretag/internal/lldp"
)

const netDir = "/sys/class/net"

// NIC is one network interface as reported by the kernel.
type NIC struct {
	Name  string   `json:"name"`
	MAC   string   `json:"mac,omitempty"`
	Speed int      `json:"speed_mbps"` // 0 when the kernel cannot report a speed
	State string   `json:"state"`      // operstate: up, down, unknown
	IPs   []string `json:"ips,omitempty"`
	// PCI is the slot the card sits in. Interface names are renamed by
	// firmware and by udev; a slot address identifies the physical port.
	PCI string `json:"pci,omitempty"`
	// Switch and Port come from the neighbour's own LLDP advertisement, and
	// are empty unless listening was enabled and something answered.
	Switch string `json:"switch,omitempty"`
	Port   string `json:"port,omitempty"`
}

// SpeedString renders the link speed the way a network engineer reads it.
func (n NIC) SpeedString() string {
	switch {
	case n.Speed <= 0:
		return "-"
	case n.Speed%1000 == 0:
		return strconv.Itoa(n.Speed/1000) + "G"
	default:
		return strconv.Itoa(n.Speed) + "M"
	}
}

// virtualPrefixes are interface kinds that are created by software and say
// nothing about the hardware in the chassis.
var virtualPrefixes = []string{
	"veth", "docker", "br-", "cni", "flannel", "virbr", "tun", "tap",
	"ovs-", "genev", "vxlan", "kube-", "nodelocaldns", "dummy",
}

func looksVirtual(name string) bool {
	for _, p := range virtualPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// collectNICs walks sysfs for link properties and the kernel interface list for
// addresses. Addresses come from the net package rather than sysfs because
// sysfs has no representation of a configured IP.
func collectNICs(opts Options) []NIC {
	entries, err := os.ReadDir(opts.path(netDir))
	if err != nil {
		return nil
	}

	addrs := interfaceAddrs()
	var nics []NIC

	for _, e := range entries {
		name := e.Name()
		if name == "lo" {
			continue
		}

		// An interface backed by real hardware has a device link into the PCI,
		// USB or platform tree. Software interfaces do not, so they are only
		// worth reporting when they carry an address or the caller asked for
		// everything.
		_, hwErr := os.Stat(opts.path(netDir, name, "device"))
		physical := hwErr == nil
		if !opts.AllNICs && !physical && (looksVirtual(name) || len(addrs[name]) == 0) {
			continue
		}

		nic := NIC{
			Name:  name,
			MAC:   readSysFile(opts.path(netDir, name, "address")),
			State: readSysFile(opts.path(netDir, name, "operstate")),
			IPs:   addrs[name],
		}
		if nic.State == "" {
			nic.State = "unknown"
		}
		// speed reads back as -1, or fails with EINVAL, whenever the link is
		// down or the driver has no notion of a line rate, as with virtio.
		if s, err := strconv.Atoi(readSysFile(opts.path(netDir, name, "speed"))); err == nil && s > 0 {
			nic.Speed = s
		}

		nic.PCI = pciAddress(opts, name)

		nics = append(nics, nic)
	}

	sort.Slice(nics, func(i, j int) bool { return nics[i].Name < nics[j].Name })
	return nics
}

// pciAddress finds the slot an interface's card occupies. The device link
// points into the PCI tree, and the uevent beside it names the same slot, so
// either will do and the second works against a captured tree.
func pciAddress(opts Options, name string) string {
	dev := opts.path(netDir, name, "device")

	for _, line := range strings.Split(readSysFile(filepath.Join(dev, "uevent")), "\n") {
		if slot, ok := strings.CutPrefix(strings.TrimSpace(line), "PCI_SLOT_NAME="); ok {
			return slot
		}
	}
	if target, err := os.Readlink(dev); err == nil {
		return filepath.Base(target)
	}
	return ""
}

// interfaceAddrs maps interface name to its assigned addresses in CIDR form.
// Link-local addresses are dropped: they are derived from the MAC and would
// only make the QR code bigger.
func interfaceAddrs() map[string][]string {
	out := map[string][]string{}

	ifaces, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, iface := range ifaces {
		list, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range list {
			ipnet, ok := a.(*net.IPNet)
			if !ok || ipnet.IP.IsLinkLocalUnicast() || ipnet.IP.IsLoopback() {
				continue
			}
			out[iface.Name] = append(out[iface.Name], ipnet.String())
		}
	}
	return out
}

// applyNeighbours asks every live link what is on the other end of it. Links
// that are down are not offered: nothing will answer, and waiting on them
// would hold up the whole collection for no result.
func (inv *Inventory) applyNeighbours(opts Options) {
	// Pointing the collectors at a captured tree must not put traffic on the
	// machine running the tests.
	if opts.LLDPWait <= 0 || opts.Root != "" {
		return
	}

	var live []string
	for _, n := range inv.NICs {
		if n.State == "up" {
			live = append(live, n.Name)
		}
	}

	for name, neighbour := range lldp.Listen(live, opts.LLDPWait) {
		for i := range inv.NICs {
			if inv.NICs[i].Name == name {
				inv.NICs[i].Switch = neighbour.Switch
				inv.NICs[i].Port = neighbour.Port
			}
		}
	}
}
