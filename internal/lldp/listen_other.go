//go:build !linux

package lldp

import "time"

// Listen has nothing to do away from Linux. Collection runs on the machine
// being inventoried, which is always Linux; this exists so the tool still
// builds on the workstation that prepares images and decodes scans.
func Listen(interfaces []string, wait time.Duration) map[string]Neighbour {
	return map[string]Neighbour{}
}
