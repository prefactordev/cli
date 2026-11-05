package lifecycle

import "golang.org/x/sys/unix"

// translatedByRosetta reports whether this process runs under Rosetta.
func translatedByRosetta() bool {
	translated, err := unix.SysctlUint32("sysctl.proc_translated")
	return err == nil && translated == 1
}
