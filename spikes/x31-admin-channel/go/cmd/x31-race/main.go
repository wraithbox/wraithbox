// x31-race: run the update at start of S06-12-guest-agent (the product's
// code, copied into ../../gu) in two processes at once, wb-guestd's split
// (it installs wb-guestd) and wb-guestroot's (it installs wb-guestroot),
// into the same install directory, many times. For X31-admin-channel,
// approach step 5. Throwaway. Run as root: PrepareDir requires root to own
// every directory.
//
//	x31-race parent <n> <base> <src> fresh|existing
//	x31-race child <dir> <src> <self> <startUnixNano>
//
// <src> stands in for the tools disk: VERSION, wb-guestd and wb-guestroot.
// Each round uses <base>/<i>/WraithBox/libexec. "fresh" leaves it missing,
// so both make it; "existing" makes it first, as at a boot, where launchd
// runs both daemons from it. The children start at the same instant and
// print one JSON line: installed (the install ran and it would exec),
// or the refusal with its rule.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"x31/gu"
)

var errWouldExec = errors.New("would exec")

type plat struct{ prepareErr error }

func (*plat) OpenDisk(path string) (*os.Root, error) { return os.OpenRoot(path) }
func (p *plat) PrepareDir(dir string) error {
	p.prepareErr = gu.PrepareDir(dir)
	return p.prepareErr
}
func (*plat) Exec(string) error { return errWouldExec }

func main() {
	switch os.Args[1] {
	case "parent":
		parent()
	case "child":
		child()
	}
}

func child() {
	dir, src, self := os.Args[2], os.Args[3], os.Args[4]
	start, _ := strconv.ParseInt(os.Args[5], 10, 64)
	gu.SelfName = self
	var logBuf bytes.Buffer
	p := &plat{}
	cfg := gu.Config{
		Own: "0.0.0-x31.1", Log: slog.New(slog.NewJSONHandler(&logBuf, nil)),
		Disk: src, Dir: dir, Platform: p,
	}
	time.Sleep(time.Until(time.Unix(0, start)))
	err := gu.AtStart(context.Background(), cfg)
	r := map[string]any{"self": self}
	switch {
	case errors.Is(err, errWouldExec):
		r["outcome"] = "installed"
	case p.prepareErr != nil:
		r["outcome"] = "refused"
		r["err"] = p.prepareErr.Error()
	default:
		r["outcome"] = "refused"
		r["log"] = strings.TrimSpace(logBuf.String())
	}
	b, _ := json.Marshal(r)
	fmt.Println(string(b))
}

func parent() {
	n, _ := strconv.Atoi(os.Args[2])
	base, src, mode := os.Args[3], os.Args[4], os.Args[5]
	counts := map[string]int{}
	examples := map[string]string{}
	for i := range n {
		dir := filepath.Join(base, strconv.Itoa(i), "WraithBox", "libexec")
		if mode == "existing" {
			if err := gu.PrepareDir(dir); err != nil {
				fmt.Println("prepare:", err)
				os.Exit(1)
			}
		}
		start := strconv.FormatInt(time.Now().Add(50*time.Millisecond).UnixNano(), 10)
		var outs [2]bytes.Buffer
		var cmds [2]*exec.Cmd
		for j, self := range []string{"wb-guestd", "wb-guestroot"} {
			cmds[j] = exec.Command(os.Args[0], "child", dir, src, self, start)
			cmds[j].Stdout = &outs[j]
			cmds[j].Stderr = os.Stderr
			_ = cmds[j].Start()
		}
		var key []string
		for j := range cmds {
			_ = cmds[j].Wait()
			var r map[string]any
			_ = json.Unmarshal(outs[j].Bytes(), &r)
			k := fmt.Sprintf("%v:%v", r["self"], r["outcome"])
			key = append(key, k)
			if r["outcome"] != "installed" {
				examples[k] = strings.TrimSpace(outs[j].String())
			}
		}
		counts[strings.Join(key, " ")]++
		// Both programs must be in place with the disk's bytes.
		for _, name := range []string{"wb-guestd", "wb-guestroot"} {
			a, _ := os.ReadFile(filepath.Join(src, name))
			b, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil || !bytes.Equal(a, b) {
				counts["missing-or-wrong:"+name]++
			}
		}
		left, _ := filepath.Glob(filepath.Join(dir, ".*.new"))
		counts["temp-files-left"] += len(left)
	}
	b, _ := json.MarshalIndent(map[string]any{"mode": mode, "rounds": n, "counts": counts, "examples": examples}, "", "  ")
	fmt.Println(string(b))
}
