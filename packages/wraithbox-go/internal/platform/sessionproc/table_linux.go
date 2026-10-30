package sessionproc

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

// procRoot is where linux's process table is.
const procRoot = "/proc"

// System reads the process table of the guest it runs in, from /proc.
// No linux guest runs yet (S12-platforms). It is here so the session
// tests run on CI's Linux job.
type System struct{}

// ByUID reads /proc/<pid>/stat and /proc/<pid>/status of every process,
// and keeps those with uid as their real or effective user ID. A
// process gone in between, and a zombie, are left out.
func (System) ByUID(uid uint32) ([]Proc, error) {
	ents, err := os.ReadDir(procRoot)
	if err != nil {
		return nil, fmt.Errorf("sessionproc: read %s: %w", procRoot, err)
	}
	var out []Proc
	for _, e := range ents {
		pid, err := strconv.ParseInt(e.Name(), 10, 32)
		if err != nil || pid < 1 {
			continue
		}
		p, err := readProc(int32(pid))
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, unix.ESRCH) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if p.State != 'Z' && (p.UID == uid || p.RUID == uid) {
			out = append(out, p.Proc)
		}
	}
	return out, nil
}

// procState is a Proc with its state letter.
type procState struct {
	Proc

	State byte
}

// readProc reads one process's stat and status files.
func readProc(pid int32) (procState, error) {
	dir := procRoot + "/" + strconv.Itoa(int(pid))
	stat, err := readSmall(dir + "/stat")
	if err != nil {
		return procState{}, err
	}
	st, err := parseStat(stat)
	if err != nil {
		return procState{}, fmt.Errorf("%s/stat: %w", dir, err)
	}
	status, err := readSmall(dir + "/status")
	if err != nil {
		return procState{}, err
	}
	ruid, euid, err := parseUIDs(status)
	if err != nil {
		return procState{}, fmt.Errorf("%s/status: %w", dir, err)
	}
	return procState{
		Proc:  Proc{PID: pid, Start: st.start, UID: euid, RUID: ruid, SID: st.session, Name: st.name},
		State: st.state,
	}, nil
}

// readSmall reads at most maxProcFile+1 bytes of name, so a file over
// the bound fails its parser.
func readSmall(name string) ([]byte, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(io.LimitReader(f, maxProcFile+1))
}

// Kill reads p's stat file right before kill(2), and sends nothing when
// that process has another start time.
func (System) Kill(p Proc) error {
	if p.PID < 2 {
		return ErrPID
	}
	now, err := readProc(p.PID)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, unix.ESRCH) {
		return nil
	}
	if err != nil {
		return err
	}
	if now.Start != p.Start {
		return nil
	}
	if err := unix.Kill(int(p.PID), unix.SIGKILL); err != nil && !errors.Is(err, unix.ESRCH) {
		return fmt.Errorf("sessionproc: kill %d: %w", p.PID, err)
	}
	return nil
}
