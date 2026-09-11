package bake

import "golang.org/x/sys/unix"

// cloneFile asks APFS for a copy on write clone, which is instant and uses no
// space until the copy is modified. Baking touches a few megabytes of a
// gigabyte sized image, so this is the difference between a build that needs
// room for a second ISO and one that does not.
func cloneFile(src, dst string) error {
	return unix.Clonefile(src, dst, 0)
}
