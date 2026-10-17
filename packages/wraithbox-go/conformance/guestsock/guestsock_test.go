//go:build conformance && darwin

package guestsock

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/user"
	"slices"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestsock"
)

const (
	// floodTime is how long the self-CID flood runs (#115 item 1).
	floodTime = 30 * time.Second

	// logWindow is wb-guestd's log window per kind
	// (internal/platform/guestsock).
	logWindow = 10 * time.Second

	// defaultLogPath is where the test guest's LaunchDaemon sends
	// wb-guestd's standard error. WB_GUESTD_LOG overrides it.
	defaultLogPath = "/var/log/wb-guestd.log"
)

// localCID returns the guest's own CID, and skips the test outside a
// guest: on a host, socket(AF_VSOCK) fails.
func localCID(t *testing.T) uint32 {
	t.Helper()
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Skipf("not in a guest: socket(AF_VSOCK): %v", err)
	}
	defer unix.Close(fd)
	cid, err := unix.IoctlGetInt(fd, unix.IOCTL_VM_SOCKETS_GET_LOCAL_CID)
	if err != nil || cid <= int(guestsock.HostCID) {
		t.Skipf("not in a guest: local CID %d, %v", cid, err)
	}
	return uint32(cid)
}

// requireStandardUser skips the test unless it runs as a user who isn't
// root and isn't in the admin or wheel group, as a project user is
// (X27-vsock-confinement, condition 5).
func requireStandardUser(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("needs a standard user outside admin and wheel; running as root")
	}
	groups, err := os.Getgroups()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"admin", "wheel"} {
		g, err := user.LookupGroup(name)
		if err != nil {
			t.Fatalf("group %s: %v", name, err)
		}
		gid, _ := strconv.Atoi(g.Gid)
		if slices.Contains(groups, gid) {
			t.Skipf("needs a standard user outside admin and wheel; this user is in %s", name)
		}
	}
}

func requireRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
}

// bind tries to bind a vsock port on cid, and closes the socket.
func bind(t *testing.T, cid, port uint32) error {
	t.Helper()
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("socket(AF_VSOCK): %v", err)
	}
	defer unix.Close(fd)
	return unix.Bind(fd, &unix.SockaddrVM{CID: cid, Port: port})
}

// connectSelf connects to a port on the guest's own CID. It succeeds when
// something listens there, and wb-guestd's accept then fails with
// ECONNABORTED (X27-vsock-confinement).
func connectSelf(cid, port uint32) error {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	return unix.Connect(fd, &unix.SockaddrVM{CID: cid, Port: port})
}

// requireGuestdListening fails the test unless something listens on
// wb-guestd's port.
func requireGuestdListening(t *testing.T, cid uint32) {
	t.Helper()
	if err := connectSelf(cid, guestsock.Port); err != nil {
		t.Fatalf("nothing listens on port %d (connect: %v); is wb-guestd running?", guestsock.Port, err)
	}
}

// TestFreeLowPortsNeedRoot: a project user can't bind a free port below
// 1024 (C09-guest-and-sessions, X27-vsock-confinement condition 4). The
// rule is only in the XNU source, so each guest image is checked, with
// the wildcard CID and the guest's own.
func TestFreeLowPortsNeedRoot(t *testing.T) {
	self := localCID(t)
	requireStandardUser(t)
	for _, cid := range []uint32{unix.VMADDR_CID_ANY, self} {
		for _, port := range []uint32{1, 80, 1022} {
			if err := bind(t, cid, port); !errors.Is(err, unix.EACCES) {
				t.Errorf("bind CID %d port %d as uid %d: %v, want EACCES", cid, port, os.Geteuid(), err)
			}
		}
	}
}

// TestGuestdPortRefused: a project user can't bind wb-guestd's port while
// it runs (C09-guest-and-sessions, X27-vsock-confinement condition 4).
func TestGuestdPortRefused(t *testing.T) {
	cid := localCID(t)
	requireStandardUser(t)
	requireGuestdListening(t, cid)
	for _, c := range []uint32{unix.VMADDR_CID_ANY, cid} {
		err := bind(t, c, guestsock.Port)
		if err == nil {
			t.Fatalf("bind CID %d port %d as uid %d succeeded while wb-guestd runs", c, guestsock.Port, os.Geteuid())
		}
		t.Logf("bind CID %d port %d as uid %d: %v", c, guestsock.Port, os.Geteuid(), err)
	}
}

// TestSelfCIDFlood: after 30 s of connects to the guest's own CID,
// wb-guestd still holds its port, and its log holds only rate-limited
// records of the flood (#115 item 1, X27-vsock-confinement condition 3).
// It runs as root, which can read the log and would get the port if
// wb-guestd had let it go. The host connects to the port during the
// flood, outside this test, and wb-guestd's log shows those connections
// served.
func TestSelfCIDFlood(t *testing.T) {
	cid := localCID(t)
	requireRoot(t)
	requireGuestdListening(t, cid)
	logPath := os.Getenv("WB_GUESTD_LOG")
	if logPath == "" {
		logPath = defaultLogPath
	}
	start := logSize(t, logPath)

	connects, failures := 0, 0
	for end := time.Now().Add(floodTime); time.Now().Before(end); {
		if err := connectSelf(cid, guestsock.Port); err != nil {
			failures++
			continue
		}
		connects++
	}
	t.Logf("%d self-CID connects in %v, %d failed", connects, floodTime, failures)
	if connects < 100 {
		t.Fatalf("only %d connects reached wb-guestd's port; the flood didn't run", connects)
	}
	time.Sleep(time.Second)

	if err := bind(t, unix.VMADDR_CID_ANY, guestsock.Port); !errors.Is(err, unix.EADDRINUSE) {
		t.Fatalf("root bind of port %d after the flood: %v, want EADDRINUSE (wb-guestd holds it)", guestsock.Port, err)
	}
	checkFloodLog(t, logPath, start)
}

// checkFloodLog reads what wb-guestd logged since offset: one JSON object
// per line, and per rule at most one record per log window of the flood.
func checkFloodLog(t *testing.T, path string, offset int64) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	perRule := map[string]int{}
	lines := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		lines++
		var rec struct {
			Rule string `json:"rule"`
		}
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			t.Errorf("log line %d isn't one JSON object: %q", lines, sc.Text())
			continue
		}
		perRule[rec.Rule]++
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	t.Logf("%d log lines during the flood, by rule: %v", lines, perRule)
	// One record per window, one more for a window cut by the flood's
	// start, and one for the self-connect of requireGuestdListening.
	limit := int(floodTime/logWindow) + 2
	for rule, n := range perRule {
		if rule != "" && n > limit {
			t.Errorf("%d records with rule %s, want at most %d in %v", n, rule, limit, floodTime)
		}
	}
	if perRule[guestsock.RuleAcceptFailed]+perRule[guestsock.RulePeerNotHost] == 0 {
		t.Errorf("no %s or %s record during the flood", guestsock.RuleAcceptFailed, guestsock.RulePeerNotHost)
	}
}

func logSize(t *testing.T, path string) int64 {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("wb-guestd's log (WB_GUESTD_LOG): %v", err)
	}
	return fi.Size()
}
