package bake

import (
	"os"

	"golang.org/x/sys/unix"
)

// cloneFile asks the filesystem for a reflink. btrfs and XFS support it; on
// anything else this fails and the caller falls back to a plain copy.
func cloneFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if err := unix.IoctlFileClone(int(out.Fd()), int(in.Fd())); err != nil {
		os.Remove(dst)
		return err
	}
	return out.Close()
}
