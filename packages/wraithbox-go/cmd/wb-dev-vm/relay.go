package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"sync"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/localipc"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmdlink"
)

// maxRelays is the most clients relayed at once. wb-vmd caps the
// connects in flight itself.
const maxRelays = 16

// The wb-vmd rules after which hello tries again: wb-guestd isn't
// listening yet, or the guest didn't answer the connect in time.
var retryRules = map[string]bool{"vm-connect-failed": true, "vm-connect-timeout": true}

// retryable reports whether a refused connect is worth another try.
func retryable(err error) bool {
	r, ok := errors.AsType[*vmdlink.Refusal](err)
	return ok && retryRules[r.VMDRule]
}

// endpointOf splits the -listen path into the directory and the
// endpoint name of localipc: <directory>/<name>.sock. localipc makes the
// directory with mode 0700 when it is missing, refuses one with group or
// other permission bits or a symbolic link, gives the socket mode 0600,
// and closes a client of another user ID before it reads a byte
// (S04-architecture, "Inter-process contracts").
func endpointOf(path string) (dir, name string, err error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", "", err
	}
	base := filepath.Base(abs)
	name, ok := strings.CutSuffix(base, ".sock")
	if !ok || name == "" {
		return "", "", fmt.Errorf("-listen %q doesn't end in <name>.sock: %w", path, errUsage)
	}
	return filepath.Dir(abs), name, nil
}

// serveRelay listens at <dir>/<name>.sock and relays each client to a
// connection from dial, until ctx ends.
func serveRelay(ctx context.Context, log *slog.Logger, dir, name string, dial func(context.Context) (net.Conn, error), stdout io.Writer) error {
	ln, err := localipc.Listen(ctx, log, dir, name)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "listening at %s\n", ln.Addr())
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	slots := make(chan struct{}, maxRelays)
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		client, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		select {
		case slots <- struct{}{}:
		default:
			log.Warn("client refused", "rule", "relay-cap", "decision", "refuse", "max", maxRelays)
			_ = client.Close()
			continue
		}
		wg.Go(func() {
			defer func() { <-slots }()
			relayClient(ctx, log, client, dial)
		})
	}
}

// relayClient connects to the guest for one client and copies bytes
// both ways, without parsing them, until both sides are done.
func relayClient(ctx context.Context, log *slog.Logger, client net.Conn, dial func(context.Context) (net.Conn, error)) {
	defer func() { _ = client.Close() }()
	guest, err := dial(ctx)
	if err != nil {
		log.Warn("guest connection failed", "error", err)
		return
	}
	defer func() { _ = guest.Close() }()
	relay(client, guest)
}

// relay copies a to b and b to a. When one side ends its writes, the
// other side's writes end too, so a half-close passes through.
func relay(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Go(func() { copyHalf(b, a) })
	wg.Go(func() { copyHalf(a, b) })
	wg.Wait()
}

// halfCloser is a connection that can end its writes alone.
type halfCloser interface{ CloseWrite() error }

func copyHalf(dst, src net.Conn) {
	_, _ = io.Copy(dst, src)
	if hc, ok := dst.(halfCloser); ok {
		_ = hc.CloseWrite()
		return
	}
	_ = dst.Close()
}
