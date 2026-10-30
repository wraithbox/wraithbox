package setup

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform"
)

// MinMacOS is the oldest macOS major version wb setup accepts
// (S12-03-host-prerequisites, "Prerequisites"): 27, because wb image
// build needs the guest provisioning options that are new in macOS 27
// (NFR04-host-platforms, I143).
const MinMacOS = 27

// hostFix is the fix of every host refusal.
var hostFix = fmt.Sprintf("Run Wraith Box on a Mac with Apple Silicon and macOS %d or later.", MinMacOS)

// checkHost refuses a host that is later in the platform matrix with
// RuleHostLater, and a Mac without Apple Silicon or with a macOS older
// than MinMacOS with RuleHostUnsupported (S12-03-host-prerequisites,
// "Prerequisites"). A release it can't read is refused too, because a
// failed check refuses before anything is installed.
func checkHost(ctx context.Context, env *Env) error {
	if platform.HostSupport(env.Host.OS) != platform.V1 || env.Host.WSL {
		return refuse(ctx, env.Log, &Refusal{
			Rule: RuleHostLater,
			Msg:  fmt.Sprintf("Wraith Box on %s hosts is later in the platform matrix (S12-platforms), so this build doesn't install there", hostName(env.Host)),
			Fix:  hostFix,
		})
	}
	rel, err := env.ReadRelease()
	if err != nil {
		return refuse(ctx, env.Log, &Refusal{
			Rule: RuleHostUnsupported,
			Msg:  fmt.Sprintf("the Mac's CPU and macOS version can't be read: %v", err),
			Fix:  hostFix,
		})
	}
	if !rel.AppleSilicon {
		return refuse(ctx, env.Log, &Refusal{
			Rule: RuleHostUnsupported,
			Msg:  "this Mac has no Apple Silicon CPU, which Wraith Box needs to run macOS guests",
			Fix:  hostFix,
		})
	}
	major, ok := majorVersion(rel.Version)
	if !ok || major < MinMacOS {
		return refuse(ctx, env.Log, &Refusal{
			Rule: RuleHostUnsupported,
			Msg:  fmt.Sprintf("this Mac runs macOS %s, and Wraith Box needs macOS %d or later", strconv.Quote(rel.Version), MinMacOS),
			Fix:  hostFix,
		})
	}
	decide(ctx, env.Log, RuleHostUnsupported, "accept", fmt.Sprintf("Apple Silicon, macOS %s", rel.Version))
	return nil
}

// majorVersion reads the major number of a product version such as
// "27.0.1".
func majorVersion(v string) (int, bool) {
	major, _, _ := strings.Cut(v, ".")
	n, err := strconv.Atoi(major)
	return n, err == nil && n > 0
}

func hostName(h platform.Host) string {
	switch {
	case h.WSL:
		return "WSL"
	case h.OS == platform.Linux:
		return "Linux"
	case h.OS == platform.Windows:
		return "Windows"
	}
	return strconv.Quote(string(h.OS))
}
