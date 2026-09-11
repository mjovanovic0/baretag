package bake

import (
	"encoding/binary"
	"fmt"
)

// CheckELF makes sure the file really is a binary the booted machine can run.
// Baking a macOS build produces an ISO that looks right and fails silently at
// boot, which is a bad way to find out.
func CheckELF(b []byte) error {
	const (
		class64      = 2
		machineAMD64 = 0x3e
	)
	if len(b) < 20 || string(b[:4]) != "\x7fELF" {
		return fmt.Errorf("not an ELF binary; a linux/amd64 build is needed")
	}
	if b[4] != class64 {
		return fmt.Errorf("not a 64 bit ELF binary")
	}
	if m := binary.LittleEndian.Uint16(b[18:20]); m != machineAMD64 {
		return fmt.Errorf("ELF machine type %#x, want x86-64 (%#x)", m, machineAMD64)
	}
	return nil
}
