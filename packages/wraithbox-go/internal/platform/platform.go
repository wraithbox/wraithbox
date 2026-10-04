// Package platform describes the host Wraith Box runs on and which guest
// operating systems each host supports. See docs/spec/S12-platforms.md.
//
// Platform-specific behavior is behind interfaces in this package's
// subpackages, implemented in files with GOOS suffixes; shared code never
// switches on runtime.GOOS itself.
package platform

import (
	"os"
	"runtime"
)

// OS is a host or guest operating system.
type OS string

// Operating systems, as named in S12-platforms.
const (
	MacOS   OS = "macos"
	Windows OS = "windows"
	Linux   OS = "linux"
)

// Support is how far a host/guest combination is supported.
type Support int

// Support levels.
const (
	Unsupported Support = iota // not possible or not permitted
	Later                      // in the design, not yet a delivery target
	V1                         // first delivery target
)

// GuestSupport reports the support level of guest on host (S12-platforms,
// platform matrix).
func GuestSupport(host, guest OS) Support {
	switch {
	case host == MacOS && guest == MacOS:
		return V1
	case guest == MacOS:
		// macOS guests are only possible, and only licensed, on Apple hardware.
		return Unsupported
	case host == MacOS || host == Windows || host == Linux:
		return Later
	default:
		return Unsupported
	}
}

// Host describes the machine wb is running on.
type Host struct {
	OS OS
	// WSL is true when running inside a WSL 2 distribution. wb then acts as
	// a client of the Wraith Box services on the Windows side (S12-platforms).
	WSL bool
}

// Detect returns the current host.
func Detect() Host {
	return detect(runtime.GOOS, os.Getenv, fileExists)
}

func detect(goos string, getenv func(string) string, exists func(string) bool) Host {
	switch goos {
	case "darwin":
		return Host{OS: MacOS}
	case "windows":
		return Host{OS: Windows}
	default:
		return Host{OS: Linux, WSL: isWSL(getenv, exists)}
	}
}

// isWSL needs both signals: the variable WSL sets for every distribution
// process, and the interop registration that lets Linux run Windows
// binaries. The variable alone is easy to inherit by accident.
func isWSL(getenv func(string) string, exists func(string) bool) bool {
	if getenv("WSL_DISTRO_NAME") == "" {
		return false
	}
	return exists("/proc/sys/fs/binfmt_misc/WSLInterop") ||
		exists("/proc/sys/fs/binfmt_misc/WSLInterop-late")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
