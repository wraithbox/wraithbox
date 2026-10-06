// Command wb-pre-receive is the X26 spike's pre-receive hook. In the
// product it would be a subcommand of wb-hostd. git runs it from the
// hooks directory wb-hostd owns, with the environment wb-hostd set on
// receive-pack.
//
// What the guest sees on refusal is one line on stderr. The decision log
// goes to WB_HOOK_LOG, a file the guest cannot read.
//
// Throwaway code for X26-pre-receive-check. Not held to the project gates.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"x26/prereceive"
)

func main() {
	t0 := time.Now()
	logw := io.Discard
	if p := os.Getenv("WB_HOOK_LOG"); p != "" {
		f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err == nil {
			defer f.Close()
			logw = f
		}
	}
	err := run(logw)
	fmt.Fprintf(logw, "hook elapsed=%s err=%v\n", time.Since(t0), err)
	if err != nil {
		var r *prereceive.Refusal
		if errors.As(err, &r) {
			fmt.Fprintf(os.Stderr, "wb: push refused (%s)\n", r.Rule)
		} else {
			fmt.Fprintln(os.Stderr, "wb: push refused (check failed)")
		}
		os.Exit(1)
	}
}

func envU(name string) uint64 {
	v, err := strconv.ParseUint(os.Getenv(name), 10, 64)
	if err != nil {
		// Fail closed: a missing limit refuses everything.
		return 0
	}
	return v
}

func run(logw io.Writer) error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	dir, _ = filepath.Abs(dir)
	q := os.Getenv("GIT_QUARANTINE_PATH")
	if q == "" {
		return errors.New("no GIT_QUARANTINE_PATH")
	}
	cfg := &prereceive.Config{
		Git:           os.Getenv("WB_GIT"),
		GitDir:        dir,
		Quarantine:    q,
		Prefix:        os.Getenv("WB_REF_PREFIX"),
		MaxObjectSize: envU("WB_MAX_OBJECT_SIZE"),
		MaxTotal:      envU("WB_MAX_TOTAL"),
		PathMax:       int(envU("WB_PATH_MAX")),
		NameMax:       int(envU("WB_NAME_MAX")),
		CheckStray:    os.Getenv("WB_CHECK_STRAY") == "1",
		Log:           logw,
	}
	if cfg.Git == "" || cfg.Prefix == "" {
		return errors.New("hook not configured")
	}
	cmds, err := prereceive.ReadCommands(os.Stdin)
	if err != nil {
		return err
	}
	return cfg.Check(cmds)
}
