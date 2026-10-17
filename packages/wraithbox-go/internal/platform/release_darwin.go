package platform

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// ReadRelease reads the Mac's CPU and macOS version from the kernel:
// hw.optional.arm64, which an Intel Mac doesn't have, and
// kern.osproductversion. Neither depends on the environment.
func ReadRelease() (Release, error) {
	version, err := unix.Sysctl("kern.osproductversion")
	if err != nil {
		return Release{}, fmt.Errorf("read sysctl kern.osproductversion: %w", err)
	}
	arm64, err := unix.SysctlUint32("hw.optional.arm64")
	switch {
	case errors.Is(err, unix.ENOENT):
		arm64 = 0
	case err != nil:
		return Release{}, fmt.Errorf("read sysctl hw.optional.arm64: %w", err)
	}
	return Release{AppleSilicon: arm64 == 1, Version: version}, nil
}
