// Command wb-diskfmt formats a VM's new data disk on the host, the one
// exception SEC15-no-guest-disk-mount makes (S06-13-data-disk-format, "Formatting a
// data disk"; S04-04-vm-processes, "`wb-diskfmt`").
//
// Its start interface: no arguments; WB_DISKFMT_SLOT in its fixed
// environment, work or isolated; descriptor 2 its log pipe; and
// descriptor 3 one end of a Unix datagram socketpair for the exchange
// with wb-hostd (proto/wraithbox/diskfmt/v1). Before it confines itself
// it checks the slot and the slot's bundle, and reads nothing from
// wb-hostd. It then confines itself to the data disk's two names in
// that bundle, exchanges version
// records with wb-hostd (S04-08-records-and-bounds, "Between host
// processes"), runs one format, and exits. It is never restarted
// (S04-07-supervision).
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/diskfmt"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/diskimage"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/fdchan"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/paths"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/selfsandbox"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/version"
)

// exchangeFD is the descriptor of the exchange with wb-hostd.
const exchangeFD = 3

// dataDir returns <data>, from the home directory in the user database,
// never HOME (S12-01-platform-interfaces, "Paths"). The shipped binary
// has no other source: only a test replaces it.
var dataDir = func(log *slog.Logger) (string, error) {
	p, err := paths.Current(log)
	if err != nil {
		return "", err
	}
	return p.Data, nil
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil)).With(slog.String("version", version.Build()))
	slog.SetDefault(log)
	os.Exit(run(context.Background(), log, os.Args[1:], os.Getenv, version.Build()))
}

// run is wb-diskfmt's main, with own its build version. It returns the
// exit status: 0 after a format and rename, 1 after any refusal, each
// logged with its rule.
func run(ctx context.Context, log *slog.Logger, args []string, getenv func(string) string, own string) int {
	bundle, r := checkStart(log, args, getenv)
	if r != nil {
		logStartRefusal(log, r)
		return 1
	}
	ch, err := fdchan.New(os.NewFile(exchangeFD, "exchange"), maxRecord)
	if err != nil {
		logStartRefusal(log, &startRefusal{Rule: ruleDescriptor, Detail: "descriptor 3 isn't the exchange socket: " + err.Error()})
		return 1
	}
	defer func() { _ = ch.Close() }()
	// SEC12-least-privilege: confined to the data disk's names in the
	// bundle before any byte
	// from wb-hostd is read (S04-04-vm-processes, "`wb-diskfmt`").
	if err := selfsandbox.Confine(profile, map[string]string{"BUNDLE": bundle}); err != nil {
		return 1
	}
	files, err := diskimage.NewFiles()
	if err != nil {
		logStartRefusal(log, &startRefusal{Rule: ruleUnsupported, Detail: err.Error()})
		return 1
	}
	tool, err := diskimage.NewTool(log)
	if err != nil {
		logStartRefusal(log, &startRefusal{Rule: ruleUnsupported, Detail: err.Error()})
		return 1
	}
	if !versions(log, ch, own) {
		return 1
	}
	f := &diskfmt.Formatter{Bundle: bundle, Files: files, Tool: tool, Log: log}
	defer func() { _ = f.Close() }()
	return serve(ctx, log, ch, f)
}
