package hostversion

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/localipc"
)

// overLocalIPC runs Exchange on both ends of a real localipc connection.
func overLocalIPC(t *testing.T, server, client string) (rs, rc result, logS string) {
	t.Helper()
	data, err := os.MkdirTemp("", "hv") //nolint:usetesting // t.TempDir's path holds the test name, which pushes run/<name>.sock past sun_path's 104 bytes
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(data) })
	run := filepath.Join(data, "run")
	ctx := context.Background()
	ln, err := localipc.Listen(ctx, nil, run, "hostd")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	ls, bs := testLog()
	done := make(chan result, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			done <- result{"", err}
			return
		}
		defer c.Close()
		p, err := Exchange(ctx, ls, c, server)
		done <- result{p, err}
	}()
	c, err := localipc.Dial(ctx, nil, run, "hostd")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	p, err := Exchange(ctx, nil, c, client)
	rc = result{p, err}
	rs = <-done
	return rs, rc, bs.String()
}

func TestExchangeOverLocalIPC(t *testing.T) {
	rs, rc, _ := overLocalIPC(t, "1.2.3", "1.2.3")
	if rs.err != nil || rc.err != nil || rs.peer != "1.2.3" || rc.peer != "1.2.3" {
		t.Fatalf("server %+v, client %+v", rs, rc)
	}
	rs, rc, logS := overLocalIPC(t, "1.2.3", "1.2.4")
	wantRule(t, rs.err, RuleMismatch)
	wantRule(t, rc.err, RuleMismatch)
	for _, want := range []string{`"rule":"peer-version-mismatch"`, `"peer_uid":`, `"peer_pid":`} {
		if !strings.Contains(logS, want) {
			t.Errorf("server log = %s, want %s", logS, want)
		}
	}
	rs, rc, _ = overLocalIPC(t, "", "")
	wantRule(t, rs.err, RuleUnstamped)
	wantRule(t, rc.err, RuleUnstamped)
}
