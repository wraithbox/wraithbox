package vmcmd

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc"

	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostversion/grpccreds"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/localipc"
)

// TestVersionRefusalNamesBoth: when wb-hostd's endpoint refuses wb's
// build version, wb prints one wraith box line that names both
// versions, and exits 255 (S04-08-records-and-bounds, "Between host
// processes"). A test binary is unstamped, so wb-hostd's 1.2.3 refuses
// it.
func TestVersionRefusalNamesBoth(t *testing.T) {
	data, err := os.MkdirTemp("", "vc") //nolint:usetesting // t.TempDir's path holds the test name, which pushes run/<name>.sock past sun_path's 104 bytes
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(data) })
	run := filepath.Join(data, "run")
	log := slog.New(slog.DiscardHandler)
	ln, err := localipc.Listen(t.Context(), log, run, localipc.HostdEndpoint)
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer(grpc.Creds(grpccreds.New(log, "1.2.3")))
	hostdv1.RegisterHostServiceServer(srv, &hostdv1.UnimplementedHostServiceServer{})
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(srv.Stop)

	var stderr bytes.Buffer
	env := Env{
		Stdout: &bytes.Buffer{}, Stderr: &stderr, Log: log,
		Dial: func(context.Context) (Hostd, error) { return dial(log, run) },
	}
	if code := Run(t.Context(), env, []string{"status", "work"}); code != exitFailure {
		t.Fatalf("exit %d", code)
	}
	line := strings.TrimRight(stderr.String(), "\n")
	for _, want := range []string{"wraith box: ", `wb-hostd is version "1.2.3"`, "wb is version unstamped", "(peer-version-unstamped)"} {
		if !strings.Contains(line, want) || strings.Contains(line, "\n") {
			t.Errorf("standard error %q misses %q", line, want)
		}
	}
}
