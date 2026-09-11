//go:build !darwin && !linux

package bake

import "errors"

// cloneFile has no implementation here, so copying always falls back to
// reading and writing the bytes.
func cloneFile(src, dst string) error { return errors.New("cloning is not supported here") }
