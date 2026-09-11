package lldp

import (
	"encoding/binary"
	"testing"
)

// tlv assembles one advertisement field. The header packs a seven bit type and
// a nine bit length into two bytes.
func tlv(typ int, value ...byte) []byte {
	out := make([]byte, 2, 2+len(value))
	binary.BigEndian.PutUint16(out, uint16(typ)<<9|uint16(len(value)))
	return append(out, value...)
}

func frame(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return append(out, tlv(tlvEnd)...)
}

func str(subtype byte, s string) []byte {
	return append([]byte{subtype}, s...)
}

// TestParseCiscoStyle covers the common case: a locally assigned port name and
// a system name, which is what a switch port and switch hostname look like.
func TestParseCiscoStyle(t *testing.T) {
	payload := frame(
		tlv(1, 4, 0x00, 0x1b, 0x54, 0xc2, 0x0a, 0x01), // chassis id, MAC
		tlv(tlvPortID, str(7, "Ethernet1/7")...),
		tlv(3, 0x00, 0x78), // ttl
		tlv(tlvPortDescr, []byte("to node-07 eno1")...),
		tlv(tlvSystemName, []byte("tor-a-r14")...),
		tlv(6, []byte("Cisco NX-OS")...),
	)

	got, ok := Parse(payload)
	if !ok {
		t.Fatal("Parse found nothing in a well formed advertisement")
	}
	if got.Switch != "tor-a-r14" {
		t.Errorf("Switch = %q, want tor-a-r14", got.Switch)
	}
	// The port identifier is more use than the description.
	if got.Port != "Ethernet1/7" {
		t.Errorf("Port = %q, want Ethernet1/7", got.Port)
	}
}

// TestParseFallsBackToDescription covers switches that put nothing readable in
// the port identifier.
func TestParseFallsBackToDescription(t *testing.T) {
	payload := frame(
		tlv(tlvPortID, str(3, "\x00\x1b\x54\xc2\x0a\x07")...), // a MAC, not a name
		tlv(tlvPortDescr, []byte("GigabitEthernet0/7")...),
		tlv(tlvSystemName, []byte("access-sw-2")...),
	)

	got, ok := Parse(payload)
	if !ok {
		t.Fatal("Parse found nothing")
	}
	if got.Port != "00:1b:54:c2:0a:07" {
		t.Errorf("Port = %q, want the MAC formatted readably", got.Port)
	}
	if got.Switch != "access-sw-2" {
		t.Errorf("Switch = %q", got.Switch)
	}
}

func TestParseIgnoresRubbish(t *testing.T) {
	for name, payload := range map[string][]byte{
		"empty":                    {},
		"truncated header":         {0x02},
		"length runs past the end": {0x04, 0xff, 'a'},
		"only an end marker":       frame(),
	} {
		if _, ok := Parse(payload); ok {
			t.Errorf("%s: Parse claimed to find a neighbour", name)
		}
	}
}

// TestParseSurvivesUnprintableText guards the console and the QR payload from
// whatever a switch decides to put in a text field.
func TestParseSurvivesUnprintableText(t *testing.T) {
	payload := frame(
		tlv(tlvPortID, str(7, "Eth1/1\x00\x07")...),
		tlv(tlvSystemName, []byte("  sw\x1b[31m-a  ")...),
	)

	got, ok := Parse(payload)
	if !ok {
		t.Fatal("Parse found nothing")
	}
	if got.Port != "Eth1/1" {
		t.Errorf("Port = %q, want the control bytes dropped", got.Port)
	}
	if got.Switch != "sw[31m-a" {
		t.Errorf("Switch = %q, want the escape character dropped and ends trimmed", got.Switch)
	}
}

// TestListenWithoutWaitDoesNothing is the guard that keeps the default fast:
// collection must not touch the network unless asked.
func TestListenWithoutWait(t *testing.T) {
	if got := Listen([]string{"eth0"}, 0); len(got) != 0 {
		t.Errorf("Listen with no wait returned %d neighbours", len(got))
	}
}
