package inventory

import (
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
)

const netDir = "/sys/class/net"

// NIC is one network interface as reported by the kernel.
type NIC struct {
	Name  string   `json:"name"`
	MAC   string   `json:"mac,omitempty"`
	Speed int      `json:"speed_mbps"` // 0 when the kernel cannot report a speed
	State string   `json:"state"`      // operstate: up, down, unknown
	IPs   []string `json:"ips,omitempty"`
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

		nics = append(nics, nic)
	}

	sort.Slice(nics, func(i, j int) bool { return nics[i].Name < nics[j].Name })
	return nics
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
