package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// LaunchctlPath is launchctl's fixed path. It is never looked up
	// through PATH (SEC03-no-host-exec; S17-02-setup, "The agent").
	LaunchctlPath = "/bin/launchctl"
	// LaunchctlEnv is launchctl's whole environment (S17-02-setup, "The
	// agent"). Nothing of the caller's environment reaches it.
	LaunchctlEnv = "PATH=/usr/bin:/bin"
	// codeNotLoaded is launchctl print's exit code for a service the
	// domain doesn't have ("Could not find service").
	codeNotLoaded = 113
	// maxPlistSize bounds the read of the plist on disk. One Replace
	// writes is under 1 KiB, so a larger file is never Current.
	maxPlistSize = 64 << 10
	// unloadWait bounds the wait for launchd to drop the agent after a
	// bootout, and unloadPoll is the step between two checks.
	unloadWait = 10 * time.Second
	unloadPoll = 100 * time.Millisecond
)

// New returns the LaunchAgent manager for the user: the plist
// <home>/Library/LaunchAgents/nl.wraithbox.hostd.plist, loaded into the
// launchd domain gui/<uid>.
func New(cfg Config) (Manager, error) {
	if !filepath.IsAbs(cfg.Home) {
		return nil, fmt.Errorf("service: the home directory %q is not an absolute path", cfg.Home)
	}
	run := cfg.Run
	if run == nil {
		run = Exec
	}
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	return &launchAgent{
		plist: PlistPath(cfg.Home),
		uid:   cfg.UID,
		run:   run,
		log:   log,
		poll:  unloadPoll,
	}, nil
}

// PlistPath is the LaunchAgent's plist under the home directory home.
func PlistPath(home string) string {
	return filepath.Join(home, "Library", "LaunchAgents", Label+".plist")
}

type launchAgent struct {
	plist string
	uid   int
	run   Runner
	log   *slog.Logger
	// poll is the step of the wait after a bootout. The tests shorten it.
	poll time.Duration
}

// Plist returns the LaunchAgent plist that runs program, the absolute
// path of wb-hostd (S17-02-setup, "The agent"; S04-07-supervision,
// "Who starts whom"). It has these keys and no other:
//
//   - Label: nl.wraithbox.hostd;
//   - ProgramArguments: program alone;
//   - RunAtLoad and KeepAlive: true, so launchd starts wb-hostd at
//     login and again whenever it exits.
//
// AbandonProcessGroup isn't set, so launchd stops the processes
// wb-hostd started when it stops wb-hostd. A program that isn't an
// absolute path of valid UTF-8 without control characters is refused,
// so the plist names exactly the program it was given.
func Plist(program string) ([]byte, error) {
	if !filepath.IsAbs(program) || !utf8.ValidString(program) || strings.ContainsFunc(program, unicode.IsControl) {
		return nil, fmt.Errorf("service: the program %q is not an absolute path of printable UTF-8", program)
	}
	var esc bytes.Buffer
	if err := xmlEscape(&esc, program); err != nil {
		return nil, err
	}
	return fmt.Appendf(nil, `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>KeepAlive</key>
	<true/>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
</dict>
</plist>
`, Label, esc.String()), nil
}

// xmlEscape writes s with the five XML special characters escaped.
func xmlEscape(w io.Writer, s string) error {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	_, err := r.WriteString(w, s)
	return err
}

func (a *launchAgent) State(ctx context.Context, program string) (State, error) {
	want, err := Plist(program)
	if err != nil {
		return State{}, err
	}
	have, err := a.readPlist()
	if err != nil {
		return State{}, err
	}
	loaded, err := a.loaded(ctx)
	if err != nil {
		return State{}, err
	}
	return State{Current: have != nil && bytes.Equal(have, want), Loaded: loaded}, nil
}

// readPlist returns the plist on disk, or nil when there is none, when
// it isn't a regular file, or when it is over maxPlistSize: none of
// those is Current.
func (a *launchAgent) readPlist() ([]byte, error) {
	fi, err := os.Lstat(a.plist)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("service: lstat %s: %w", a.plist, err)
	}
	if !fi.Mode().IsRegular() || fi.Size() > maxPlistSize {
		return nil, nil
	}
	data, err := os.ReadFile(a.plist)
	if err != nil {
		return nil, fmt.Errorf("service: read %s: %w", a.plist, err)
	}
	return data, nil
}

func (a *launchAgent) Replace(ctx context.Context, program string) error {
	data, err := Plist(program)
	if err != nil {
		return err
	}
	if err := a.writePlist(data); err != nil {
		return err
	}
	if err := a.bootout(ctx); err != nil {
		return err
	}
	return a.bootstrap(ctx)
}

func (a *launchAgent) Start(ctx context.Context) error { return a.bootstrap(ctx) }

// writePlist writes data to a new file next to the plist, gives it mode
// 0644, and renames it over the plist (S17-02-setup, "The agent"). So
// launchd never reads a plist that is half written. It makes
// ~/Library/LaunchAgents with mode 0700 when it is missing.
func (a *launchAgent) writePlist(data []byte) (err error) {
	dir := filepath.Dir(a.plist)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("service: make %s: %w", dir, err)
	}
	f, err := os.CreateTemp(dir, "."+Label+".*.plist")
	if err != nil {
		return fmt.Errorf("service: write the plist: %w", err)
	}
	defer func() {
		if err != nil {
			_ = f.Close()
			_ = os.Remove(f.Name())
		}
	}()
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("service: write %s: %w", f.Name(), err)
	}
	// launchd reads the plist, and launchctl refuses one that its group
	// or others can write. 0644 is what S17-02-setup gives.
	if err := f.Chmod(0o644); err != nil {
		return fmt.Errorf("service: chmod %s: %w", f.Name(), err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("service: sync %s: %w", f.Name(), err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("service: close %s: %w", f.Name(), err)
	}
	if err := os.Rename(f.Name(), a.plist); err != nil {
		return fmt.Errorf("service: rename %s over %s: %w", f.Name(), a.plist, err)
	}
	a.log.LogAttrs(context.Background(), slog.LevelInfo, "LaunchAgent plist written", slog.String("path", a.plist))
	return nil
}

// target is the service in the user's GUI domain, gui/<uid>/<label>.
func (a *launchAgent) target() string { return a.domain() + "/" + Label }

func (a *launchAgent) domain() string { return "gui/" + strconv.Itoa(a.uid) }

// launchctl runs /bin/launchctl with args and only LaunchctlEnv, and
// logs the run.
func (a *launchAgent) launchctl(ctx context.Context, args ...string) (Result, error) {
	res, err := a.run(ctx, Command{Path: LaunchctlPath, Args: args, Env: []string{LaunchctlEnv}})
	attrs := []slog.Attr{slog.String("args", strings.Join(args, " "))}
	if err != nil {
		a.log.LogAttrs(ctx, slog.LevelWarn, "launchctl didn't run", append(attrs, slog.String("error", err.Error()))...)
		return Result{}, fmt.Errorf("service: %s %s: %w", LaunchctlPath, strings.Join(args, " "), err)
	}
	a.log.LogAttrs(ctx, slog.LevelInfo, "launchctl ran", append(attrs, slog.Int("code", res.Code))...)
	return res, nil
}

// failed is the error of a launchctl run that exited with a code that
// means failure. The output is quoted, so a byte in it can't reach the
// terminal as a control sequence.
func failed(args []string, res Result) error {
	return fmt.Errorf("service: %s %s exited with %d: %s", LaunchctlPath, strings.Join(args, " "), res.Code,
		strconv.QuoteToASCII(strings.TrimSpace(string(res.Output))))
}

// loaded asks launchd whether the agent is loaded, with launchctl print:
// exit 0 is loaded, codeNotLoaded is not, and any other code is an error.
func (a *launchAgent) loaded(ctx context.Context) (bool, error) {
	args := []string{"print", a.target()}
	res, err := a.launchctl(ctx, args...)
	switch {
	case err != nil:
		return false, err
	case res.Code == 0:
		return true, nil
	case res.Code == codeNotLoaded:
		return false, nil
	default:
		return false, failed(args, res)
	}
}

// bootout unloads the agent, which stops wb-hostd, and waits until
// launchd no longer has it. A bootout of an agent that isn't loaded
// counts as done.
func (a *launchAgent) bootout(ctx context.Context) error {
	args := []string{"bootout", a.target()}
	res, err := a.launchctl(ctx, args...)
	if err != nil {
		return err
	}
	if res.Code != 0 {
		loaded, err := a.loaded(ctx)
		if err != nil {
			return err
		}
		if loaded {
			return failed(args, res)
		}
		return nil
	}
	return a.waitUnloaded(ctx)
}

// waitUnloaded checks every poll, for up to unloadWait, until launchd no
// longer has the agent, so the bootstrap that follows doesn't find the
// old one still there.
func (a *launchAgent) waitUnloaded(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, unloadWait)
	defer cancel()
	for {
		loaded, err := a.loaded(ctx)
		if err != nil || !loaded {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("service: %s is still loaded %s after its bootout: %w", Label, unloadWait, context.Cause(ctx))
		case <-time.After(a.poll):
		}
	}
}

// bootstrap loads the agent from its plist into gui/<uid>, which starts
// wb-hostd (RunAtLoad).
func (a *launchAgent) bootstrap(ctx context.Context) error {
	args := []string{"bootstrap", a.domain(), a.plist}
	res, err := a.launchctl(ctx, args...)
	if err != nil {
		return err
	}
	if res.Code != 0 {
		return failed(args, res)
	}
	return nil
}
