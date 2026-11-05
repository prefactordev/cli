//go:build !darwin

package lifecycle

// translatedByRosetta is false off macOS.
func translatedByRosetta() bool { return false }
