// Package version reports the Wraith Box build version shared by every Go
// binary in this module.
package version

// Version is overridden at link time for release builds:
//
//	go build -ldflags "-X github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/version.Version=1.2.3"
var Version = "0.0.0-dev"

// String formats the version line every binary prints for --version.
func String(binary string) string {
	return binary + " (Wraith Box) " + Version
}
