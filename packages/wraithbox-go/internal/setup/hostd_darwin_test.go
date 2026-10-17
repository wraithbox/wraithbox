package setup

import (
	"context"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostversion"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/localipc"
)

// shortRun returns a run directory whose wb-hostd.sock fits in sun_path.
// t.TempDir's path holds the test name, which can make it too long.
func shortRun(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "setup") //nolint:usetesting // t.TempDir's path holds the test name, which pushes run/wb-hostd.sock past sun_path's 104 bytes
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "run")
}

var quiet = slog.New(slog.DiscardHandler)

// serveHostd listens at wb-hostd's endpoint in run, and answers each
// connection with handle.
func serveHostd(t *testing.T, run string, handle func(net.Conn)) {
	t.Helper()
	ln, err := localipc.Listen(context.Background(), quiet, run, localipc.HostdEndpoint)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			handle(c)
			_ = c.Close()
		}
	}()
}

func exchangeAs(v string) func(net.Conn) {
	return func(c net.Conn) { _, _ = hostversion.Exchange(context.Background(), quiet, c, v) }
}

// leftoverSocket leaves a socket file with no listener, as a crash does.
func leftoverSocket(t *testing.T, run string) {
	t.Helper()
	makeRunDir(t, run)
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(run, localipc.HostdEndpoint+".sock"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	ln.SetUnlinkOnClose(false)
	_ = ln.Close()
}

func makeRunDir(t *testing.T, run string) {
	t.Helper()
	if err := localipc.EnsureRunDir(quiet, run); err != nil {
		t.Fatal(err)
	}
}

// holdLock leaves a socket with no listener, and holds the endpoint's
// lock as a live listener does: a refused connect then can't prove that
// nothing listens.
func holdLock(t *testing.T, run string) {
	t.Helper()
	leftoverSocket(t, run)
	f, err := os.OpenFile(filepath.Join(run, localipc.HostdEndpoint+".lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatal(err)
	}
}

func unsafeRunDir(t *testing.T, run string) {
	t.Helper()
	makeRunDir(t, run)
	if err := os.Chmod(run, 0o755); err != nil {
		t.Fatal(err)
	}
}

func serveVersion(v string) func(*testing.T, string) {
	return func(t *testing.T, run string) { serveHostd(t, run, exchangeAs(v)) }
}

func serveGarbage(t *testing.T, run string) {
	serveHostd(t, run, func(c net.Conn) { _, _ = c.Write([]byte("HTTP/1.1 200 OK\r\n")) })
}

// TestHostdClient asks a real local IPC endpoint each of the answers of
// S17-02-setup, "The agent", and sees every case it can't be sure of
// come back as an error, which wb setup refuses.
func TestHostdClient(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T, run string)
		want    HostdState
		wantErr bool
	}{
		{"no run directory", func(*testing.T, string) {}, HostdState{Kind: HostdNotListening}, false},
		{"no socket", makeRunDir, HostdState{Kind: HostdNotListening}, false},
		{"leftover socket", leftoverSocket, HostdState{Kind: HostdNotListening}, false},
		{"refused connect while the lock is held", holdLock, HostdState{}, true},
		{"same version", serveVersion(testVersion), HostdState{Kind: HostdSameVersion}, false},
		{"other version", serveVersion("0.0.9"), HostdState{Kind: HostdOtherVersion, Peer: `"0.0.9"`}, false},
		{"malformed record", serveGarbage, HostdState{}, true},
		{"unsafe run directory", unsafeRunDir, HostdState{}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			run := shortRun(t)
			tc.prepare(t, run)
			got, err := NewHostd(quiet, run, testVersion).State(context.Background())
			if (err != nil) != tc.wantErr {
				t.Fatalf("State = %+v, %v; want error %v", got, err, tc.wantErr)
			}
			if got.Kind != tc.want.Kind || got.Peer != tc.want.Peer || got.Listed {
				t.Errorf("State = %+v, want %+v", got, tc.want)
			}
		})
	}
}
