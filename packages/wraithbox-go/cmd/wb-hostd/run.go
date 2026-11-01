package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/appbundle"
	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/audit"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/datadisk"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/guestconn"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/hostservice"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/kills"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/launch"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/logpipe"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/shells"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/startup"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vmdprovider"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostversion/grpccreds"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/diskclone"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/localipc"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/paths"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/version"
)

// ruleNotInBundle: wb-hostd's real path isn't Contents/MacOS/wb-hostd of
// a bundle, so it has no installed wb-vmd, wb-diskfmt or guest tools
// disk to use, and it is in refusing mode. It never looks for them on
// PATH or in <config> (S17-01-bundle).
const ruleNotInBundle = "hostd-not-in-bundle"

// stopTimeout bounds the wait for calls in progress when wb-hostd stops.
// The calls still running after it are cut off.
const stopTimeout = 5 * time.Second

// options is what run takes from main, and the tests give directly.
type options struct {
	// Paths are <config>, <data>, <logs> and <data>/run.
	Paths paths.Paths
	// Executable is wb-hostd's own path, from os.Executable. Its real
	// path names the installed bundle.
	Executable string
	// Version is wb-hostd's build version.
	Version string
	// wrapVMs, set only by the needs-vm test, wraps what the guest links
	// open connections through, so the test can close wb-hostd's end of
	// the control link. main leaves it nil.
	wrapVMs func(guestconn.VMs) guestconn.VMs
}

// run runs wb-hostd until ctx is done. Its steps, in order: the logs,
// the local IPC endpoint, the configuration and state.db, the VM
// manager, then the wraithbox.hostd.v1 server. A refused configuration, state.db or
// bundle puts it in refusing mode, where it builds no VM manager, so it
// spawns no wb-vmd, and its endpoint answers every call with the rule
// (S17-05-upgrades-and-migrations, "Refusing mode").
func run(ctx context.Context, o options) error {
	logs, err := openLogs(o.Paths.Logs)
	if err != nil {
		return err
	}
	defer logs.close()
	log := logs.log
	// The configuration and state packages log their refusals to the
	// default logger, so it is wb-hostd.log before they run.
	slog.SetDefault(log)
	log.LogAttrs(ctx, slog.LevelInfo, "wb-hostd started",
		slog.String("version", version.Label(o.Version)), slog.Int("pid", os.Getpid()))
	// The endpoint first: its lock makes this the only wb-hostd, before
	// it touches state.db or clears the sockets of a wb-hostd before it.
	ln, err := localipc.Listen(ctx, log, o.Paths.Run, localipc.HostdEndpoint)
	if err != nil {
		return err
	}
	h, err := prepare(ctx, o, log, logs.files)
	if err != nil {
		_ = ln.Close()
		return err
	}
	// The gRPC server closes ln when it stops, but wb-hostd holds the
	// lock of its endpoint for its life (S04-03-self-sandboxing): until
	// h.close has ended every wb-vmd, so a wb-hostd after it never
	// clears the socket of a wb-vmd that still runs.
	release := ln.KeepLock()
	defer func() { _ = release() }()
	defer h.close(ctx)
	return serve(ctx, o, log, ln, h)
}

// hostLogs are wb-hostd's own log, <logs>/wb-hostd.log, and the log
// files of the processes it spawns (S04-03-self-sandboxing, "Daemon
// logs").
type hostLogs struct {
	file  *logpipe.File
	log   *slog.Logger
	files *logpipe.Files
}

func openLogs(dir string) (*hostLogs, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("the log directory: %w", err)
	}
	f, err := logpipe.OpenFile(dir, "wb-hostd")
	if err != nil {
		return nil, fmt.Errorf("wb-hostd's log: %w", err)
	}
	log := slog.New(slog.NewJSONHandler(f, nil))
	return &hostLogs{file: f, log: log, files: logpipe.NewFiles(dir, log)}, nil
}

func (l *hostLogs) close() {
	_ = l.files.Close()
	_ = l.file.Close()
}

// host is what wb-hostd serves. Only server, projects and shells are set
// in refusing mode.
type host struct {
	server   *hostservice.Server
	projects *hostservice.ProjectServer
	shells   *hostservice.ShellServer
	registry *shells.Registry
	vms      *vm.Manager
	provider *vmdprovider.Provider
	links    *guestconn.Links
	audit    *audit.Writer
	closeDB  func() error
}

// prepare reads the configuration and opens state.db, then builds the
// VM manager, or the refusing mode.
func prepare(ctx context.Context, o options, log *slog.Logger, files *logpipe.Files) (*host, error) {
	ready, refusing := startup.Prepare(ctx, o.Paths.Config, o.Paths.Data)
	if refusing != nil {
		return refusingHost(ctx, log, refusing), nil
	}
	bundle, notIn := bundleOf(o.Executable)
	if notIn != nil {
		_ = ready.State.Close()
		return refusingHost(ctx, log, notIn), nil
	}
	h, err := newHost(ctx, o, bundle, ready, log, files)
	if err != nil {
		_ = ready.State.Close()
		return nil, err
	}
	return h, nil
}

func refusingHost(ctx context.Context, log *slog.Logger, r *startup.Refusing) *host {
	log.LogAttrs(ctx, slog.LevelError, "wb-hostd is in refusing mode",
		slog.String("decision", "refuse"), slog.String("rule", r.Rule), slog.String("detail", r.Detail))
	return &host{
		server:   &hostservice.Server{Refusing: r, Log: log},
		projects: &hostservice.ProjectServer{Refusing: r, Log: log},
		shells:   &hostservice.ShellServer{Refusing: r, Log: log},
	}
}

// bundleOf returns the bundle directory of wb-hostd's real path, which
// must be <bundle>/Contents/MacOS/wb-hostd. Any other path is refused
// with ruleNotInBundle.
func bundleOf(exe string) (string, *startup.Refusing) {
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", &startup.Refusing{Rule: ruleNotInBundle, Detail: fmt.Sprintf("wb-hostd's own path %q can't be resolved: %v", exe, err)}
	}
	macos := filepath.Dir(resolved)
	contents := filepath.Dir(macos)
	if filepath.Base(resolved) != "wb-hostd" || filepath.Base(macos) != "MacOS" || filepath.Base(contents) != "Contents" {
		return "", &startup.Refusing{Rule: ruleNotInBundle, Detail: fmt.Sprintf("wb-hostd runs from %q, which is not Contents/MacOS/wb-hostd of a bundle", resolved)}
	}
	return filepath.Dir(contents), nil
}

// newHost builds the VM manager, with wb-vmd as its provider, the APFS
// clone of the guest tools disk, and guest links that follow its runs.
// Every program comes from the bundle wb-hostd runs from (S17-01-bundle).
// It starts no VM. The caller holds wb-hostd's endpoint, so the wb-vmd
// sockets left in the run directory are a crashed wb-hostd's, and are
// cleared before the manager can start a run.
func newHost(ctx context.Context, o options, bundle string, ready *startup.Host, log *slog.Logger, files *logpipe.Files) (*host, error) {
	macos := filepath.Join(bundle, "Contents", "MacOS")
	// The audit writer comes first: the data disks, the VMs and the
	// projects record through it, and none of them runs without it
	// (SEC10-audit).
	aw, err := audit.New(audit.Config{
		Dir: o.Paths.Logs, RetentionDays: ready.Config.AuditRetentionDays, FloorMiB: ready.Config.AuditFreeSpaceFloorMiB,
		DataDir: o.Paths.Data, DataDiskGiB: ready.Config.DataDiskGiB, Version: o.Version, Logger: log,
	})
	if err != nil {
		return nil, err
	}
	disks, err := datadisk.New(datadisk.Config{
		DataDir: o.Paths.Data, Records: ready.State, SizeGiB: ready.Config.DataDiskGiB, Audit: aw, Logger: log,
		Shim: &datadisk.ProcessShim{
			Program: filepath.Join(macos, launch.DiskfmtProgram), Logs: files.Writer, Logger: log, Version: o.Version,
		},
	})
	if err != nil {
		return nil, err
	}
	provider, err := vmdprovider.New(vmdprovider.Config{
		Program: filepath.Join(macos, "wb-vmd"), RunDir: o.Paths.Run, Version: o.Version,
		Logs: files.Writer, Logger: log,
	})
	if err != nil {
		return nil, err
	}
	// A leftover takes only its directory entry: each run's socket has a
	// new name, so the start goes on when one can't be removed.
	if _, err := provider.ClearLeftovers(ctx); err != nil {
		log.LogAttrs(ctx, slog.LevelWarn, "wb-vmd leftover sockets not all removed", slog.String("error", err.Error()))
	}
	var (
		links    *guestconn.Links
		registry *shells.Registry
	)
	// Pending kills are in memory only: a wb-hostd exit stops every VM
	// (S06-10-leftover-processes, "Failing closed").
	tracker := kills.New(kills.Config{Logger: log, Audit: aw})
	m, err := vm.New(vm.Config{
		DataDir: o.Paths.Data, ToolsImage: filepath.Join(bundle, filepath.FromSlash(appbundle.ToolsDiskPath)),
		Resources: ready.Config, Generations: ready.State, Provider: provider, Cloner: diskclone.Cloner{},
		DataDisks: disks, Audit: aw, Logger: log,
		OnRunning: func(slot vm.Slot, generation uint64, ended <-chan struct{}) { links.Run(slot, generation, ended) },
		// A stop is refused while the VM has an open debug shell
		// (S06-09-session-lifecycle, "Idle suspend").
		Sessions: func(slot vm.Slot) []vm.Session { return registry.Sessions(slot) },
		OnEnded: func(slot vm.Slot, generation uint64, end vm.End) {
			if end.Cold {
				tracker.ColdStop(slot, generation, end.Cause)
			}
		},
	})
	if err != nil {
		return nil, err
	}
	var vms guestconn.VMs = m
	if o.wrapVMs != nil {
		vms = o.wrapVMs(m)
	}
	links, err = guestconn.New(guestconn.Config{
		VMs: vms, Version: o.Version, TimeHook: guestconn.SendTime,
		MountHook: guestconn.SendMount(ready.State, time.Now, log), Users: ready.State, Kills: tracker, Logger: log,
		// Each connection kills the users of the shells the last one lost
		// (S13-01-process-hardening, "When the link drops").
		OnLinked: func(slot vm.Slot, generation uint64) { registry.OnLinked(slot, generation) },
	})
	if err != nil {
		return nil, err
	}
	registry = shells.NewRegistry(links, log)
	aw.Attach(m)
	return &host{
		server: &hostservice.Server{VMs: m, Links: links, Audit: aw, Log: log},
		projects: &hostservice.ProjectServer{
			Store: ready.State, Git: hostservice.CheckGit(ctx, ready.Config.Git, aw), Audit: aw, Log: log,
			UsersRemoved: links.SendUserRemovals,
		},
		shells:   &hostservice.ShellServer{Store: ready.State, VMs: m, Links: links, Shells: registry, Audit: aw, Log: log},
		registry: registry,
		vms:      m, provider: provider, links: links, audit: aw, closeDB: ready.State.Close,
	}, nil
}

// close ends every wb-vmd by its lifeline, which stops its VM at once
// (S04-07-supervision, "When `wb-hostd` stops"), waits until the
// manager has logged the end of each run, then closes the guest links,
// the audit writer and state.db.
func (h *host) close(ctx context.Context) {
	log := h.server.Log
	if h.provider != nil {
		if err := h.provider.Close(); err != nil {
			log.LogAttrs(ctx, slog.LevelWarn, "wb-vmd ended with an error", slog.String("error", err.Error()))
		}
		h.vms.Wait()
	}
	if h.registry != nil {
		h.registry.Close()
	}
	if h.links != nil {
		h.links.Close()
	}
	// After the wait, every VM's stop record is queued. The writer closes
	// last before state.db, so a write that fails while it closes has no
	// VM left to stop.
	if h.audit != nil {
		if err := h.audit.Close(); err != nil {
			log.LogAttrs(ctx, slog.LevelError, "audit log not closed", slog.String("error", err.Error()))
		}
	}
	if h.closeDB != nil {
		_ = h.closeDB()
	}
	log.LogAttrs(ctx, slog.LevelInfo, "wb-hostd stopped")
}

// serve serves wraithbox.hostd.v1 on the local IPC endpoint until ctx is
// done. Every connection passes localipc's peer check and the build
// version exchange of grpccreds before a call reaches the server
// (SEC12-least-privilege).
//
// The server never pings a connection: a wb stopped with job control
// can't answer, and keeps its shell. Only a closed connection ends a
// shell (S06-09-session-lifecycle, "Closing the terminal ends the
// session").
func serve(ctx context.Context, o options, log *slog.Logger, ln *localipc.Listener, h *host) error {
	srv := grpc.NewServer(grpc.Creds(grpccreds.New(log, o.Version)),
		grpc.MaxRecvMsgSize(hostservice.MaxMessageSize), grpc.MaxSendMsgSize(hostservice.MaxMessageSize),
		grpc.KeepaliveParams(keepalive.ServerParameters{Time: time.Duration(math.MaxInt64)}))
	hostdv1.RegisterHostServiceServer(srv, h.server)
	hostdv1.RegisterProjectServiceServer(srv, h.projects)
	hostdv1.RegisterShellServiceServer(srv, h.shells)
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()
	log.LogAttrs(ctx, slog.LevelInfo, "wb-hostd serving", slog.String("endpoint", localipc.HostdEndpoint))
	select {
	case err := <-served:
		return fmt.Errorf("the local IPC endpoint: %w", err)
	case <-ctx.Done():
	}
	stopped := make(chan struct{})
	go func() {
		srv.GracefulStop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(stopTimeout):
		srv.Stop()
	}
	if err := <-served; err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		return err
	}
	return nil
}
