// Command wb-netd is the guest-facing network service of one VM: it runs
// the userspace TCP/IP stack, DHCP and DNS for the VM's virtual NIC, and
// hands accepted streams to wb-proxyd (S07-egress-gateway). It holds no
// secrets and makes no outbound connections.
//
// wb-hostd starts one per VM (S04-09-netd-start).
// Its start interface: no arguments, an empty environment, and these
// descriptors, each an end of a Unix datagram socketpair but the log
// pipe:
//
//	2  the log pipe
//	3  the control channel
//	4  the packet transport
//	5  the accounting channel
//	6  the event channel
//
// It confines itself first, then checks the descriptors, and exits with
// the rule netd-descriptor-invalid when one is wrong. It reads the start
// message from the control channel, starts the stack, and serves until
// wb-hostd closes the control channel. It is never restarted
// (S04-07-supervision).
package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/selfsandbox"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/version"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil)).With(slog.String("version", version.Build()))
	slog.SetDefault(log)
	os.Exit(run(context.Background(), log, os.Args[1:], version.Build()))
}

// run is wb-netd's main, with own its build version. It returns the exit
// status: 0 when wb-hostd closed the control channel, 1 after a refusal
// or a failure, each logged with its rule.
func run(ctx context.Context, log *slog.Logger, args []string, own string) int {
	if len(args) > 0 {
		log.LogAttrs(ctx, slog.LevelError, "wb-netd start refused",
			slog.String("rule", ruleArguments), slog.String("decision", "refuse"),
			slog.Int("arguments", len(args)))
		return 1
	}
	// The local time zone is loaded on its first use. Use it before the
	// profile denies its file, so log records keep their offset.
	_ = time.Now().Local().String()
	// SEC12-least-privilege: confined before any descriptor is read
	// (S04-03-self-sandboxing, "Confined first thing in main").
	if err := selfsandbox.Confine(profile, nil); err != nil {
		return 1
	}
	ch, err := openDescriptors()
	if err != nil {
		log.LogAttrs(ctx, slog.LevelError, "wb-netd start refused",
			slog.String("rule", ruleDescriptorInvalid), slog.String("decision", "refuse"),
			slog.String("reason", err.Error()))
		return 1
	}
	return serve(ctx, log, ch, own)
}
