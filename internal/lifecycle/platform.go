// Package lifecycle installs, updates, removes and diagnoses the managed
// Prefactor CLI in ~/.prefactor, as the TypeScript CLI does, so either CLI
// can update an installation made by the other.
package lifecycle

import (
	"fmt"
	"path/filepath"
	"runtime"
)

// Platform is the system a release is chosen for, named as the TypeScript
// CLI names it: OS darwin, linux or windows; Arch x64 or arm64; Libc glibc
// or musl on Linux and empty elsewhere.
type Platform struct {
	OS      string
	Arch    string
	Libc    string
	Rosetta bool
}

// DetectPlatform describes the running system. A darwin x64 process under
// Rosetta reports arm64, so updates fetch the native build.
func DetectPlatform(getenv func(string) string) (Platform, error) {
	var p Platform
	switch runtime.GOOS {
	case "darwin", "linux", "windows":
		p.OS = runtime.GOOS
	default:
		return p, fmt.Errorf("Unsupported platform: %s", runtime.GOOS)
	}
	switch runtime.GOARCH {
	case "amd64":
		p.Arch = "x64"
	case "arm64":
		p.Arch = "arm64"
	default:
		return p, fmt.Errorf("Unsupported architecture: %s", runtime.GOARCH)
	}

	if p.OS == "darwin" && p.Arch == "x64" {
		switch getenv("PREFACTOR_INSTALL_TEST_ROSETTA") {
		case "1":
			p.Rosetta = true
		case "0":
		default:
			p.Rosetta = translatedByRosetta()
		}
		if p.Rosetta {
			p.Arch = "arm64"
		}
	}

	if p.OS == "linux" {
		p.Libc = "glibc"
		if usesMusl() {
			p.Libc = "musl"
		}
	}
	return p, nil
}

// usesMusl reports whether the system's C library is musl, as on Alpine.
// The CLI itself is static, but the asset name records the libc.
var usesMusl = func() bool {
	matches, _ := filepath.Glob("/lib/ld-musl-*.so.1")
	return len(matches) > 0
}
