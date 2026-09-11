// Package lldp listens for the advertisement a switch sends down each link and
// reports which switch and which port is on the other end of the cable.
//
// This is the one fact about a machine that cannot be read out of it. Knowing
// that eno1 is plugged into tor-a-r14 port Eth1/7 turns a rack of identical
// boxes into something you can wire up and verify without tracing cables by
// hand, and it is the reason a boot time inventory is worth waiting for.
package lldp

import (
	"encoding/binary"
	"fmt"
	"strings"
	"time"
)

// EtherType is the protocol number LLDP frames carry.
const EtherType = 0x88CC

// Wait is a sensible listening period. Switches advertise every thirty seconds
// by default, so anything shorter usually hears nothing at all.
const Wait = 35 * time.Second

// Neighbour is what answered on a link.
type Neighbour struct {
	Switch string
	Port   string
}

// TLV types from IEEE 802.1AB. Only the ones worth putting in a QR code are
// named here.
const (
	tlvEnd        = 0
	tlvPortID     = 2
	tlvPortDescr  = 4
	tlvSystemName = 5
)

// Port ID subtypes. Three is a MAC address and needs formatting; the rest are
// text a person can read off a switch.
const portSubtypeMAC = 3

// Parse reads an LLDP frame payload, which is everything after the ethernet
// header, and returns what it says about the sender.
func Parse(payload []byte) (Neighbour, bool) {
	var (
		n         Neighbour
		portID    string
		portDescr string
	)

	for off := 0; off+2 <= len(payload); {
		header := binary.BigEndian.Uint16(payload[off : off+2])
		typ := header >> 9
		length := int(header & 0x01FF)
		off += 2

		if typ == tlvEnd {
			break
		}
		if off+length > len(payload) {
			break
		}
		value := payload[off : off+length]
		off += length

		switch typ {
		case tlvSystemName:
			n.Switch = printable(value)
		case tlvPortID:
			if len(value) < 2 {
				continue
			}
			if value[0] == portSubtypeMAC {
				portID = macString(value[1:])
				continue
			}
			portID = printable(value[1:])
		case tlvPortDescr:
			portDescr = printable(value)
		}
	}

	// A port identifier such as "Eth1/7" is more use than a description such
	// as "10GbE uplink", but some switches only send the latter.
	n.Port = portID
	if n.Port == "" {
		n.Port = portDescr
	}
	return n, n.Switch != "" || n.Port != ""
}

// printable keeps a TLV's text and drops anything that would make a mess of a
// console line or a QR payload.
func printable(b []byte) string {
	var sb strings.Builder
	for _, c := range b {
		if c >= 0x20 && c < 0x7f {
			sb.WriteByte(c)
		}
	}
	return strings.TrimSpace(sb.String())
}

func macString(b []byte) string {
	if len(b) != 6 {
		return ""
	}
	parts := make([]string, len(b))
	for i, c := range b {
		parts[i] = fmt.Sprintf("%02x", c)
	}
	return strings.Join(parts, ":")
}
