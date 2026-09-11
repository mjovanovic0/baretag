package inventory

const dmiDir = "/sys/class/dmi/id"

// serialSources are the DMI fields that may carry a machine's service tag, in
// the order support engineers expect them to be quoted. Chassis first: on
// rack servers that is the number printed on the pull-out tag, while
// board_serial belongs to a field-replaceable part and can change under
// the same asset.
var serialSources = []string{
	"chassis_serial",
	"product_serial",
	"board_serial",
}

// applyDMI fills in the machine identity fields. The serial attributes are
// mode 0400, so this only produces results when running as root.
func (inv *Inventory) applyDMI(opts Options) {
	for _, src := range serialSources {
		if v := clean(readSysFile(opts.path(dmiDir, src))); meaningful(v) {
			inv.Serial = v
			inv.SerialSrc = src
			break
		}
	}

	if v := clean(readSysFile(opts.path(dmiDir, "sys_vendor"))); meaningful(v) {
		inv.Vendor = v
	}
	if v := clean(readSysFile(opts.path(dmiDir, "product_name"))); meaningful(v) {
		inv.Product = v
	}
	if v := clean(readSysFile(opts.path(dmiDir, "product_uuid"))); meaningful(v) {
		inv.UUID = v
	}
}
