package platform

// Release describes the hardware and operating system release that
// `wb setup` checks against the host prerequisites
// (S12-03-host-prerequisites, "Prerequisites").
type Release struct {
	// AppleSilicon is whether the Mac has an Apple Silicon CPU. It is
	// the hardware's answer, so it is true for a program that runs
	// translated as well.
	AppleSilicon bool
	// Version is the operating system's product version, such as
	// "15.4.1".
	Version string
}

// HostSupport reports the support level of a host OS, the first column
// of the platform matrix (S12-platforms): macOS hosts are V1, Windows
// and Linux hosts are later.
func HostSupport(host OS) Support {
	switch host {
	case MacOS:
		return V1
	case Windows, Linux:
		return Later
	default:
		return Unsupported
	}
}
