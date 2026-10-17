package paths

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
)

// lookupUser looks up the effective user in the user database. On
// darwin os/user asks libSystem's getpwuid_r, with or without cgo, and
// reads no variable. The tests replace it.
var lookupUser = func() (*user.User, error) {
	return user.LookupId(strconv.Itoa(os.Geteuid()))
}

// Current returns the paths of the effective user on macOS
// (S12-01-platform-interfaces, "Paths"):
//
//	<config>  ~/.config/wraithbox
//	<data>    ~/Library/Application Support/WraithBox
//	<logs>    ~/Library/Logs/WraithBox
//	run       <data>/run
//
// The home directory is the user database's (S04-03-self-sandboxing,
// as wb-vmd's getpwuid(getuid())), never HOME. An empty or relative
// home is refused with RuleHomeInvalid and logged.
func Current(log *slog.Logger) (Paths, error) {
	u, err := lookupUser()
	if err != nil {
		return Paths{}, fmt.Errorf("paths: look up effective user %d in the user database: %w", os.Geteuid(), err)
	}
	if u.HomeDir == "" || !filepath.IsAbs(u.HomeDir) {
		r := &Refusal{Rule: RuleHomeInvalid, msg: fmt.Sprintf("the user database gives user %s the home directory %q, which is not an absolute path", u.Uid, u.HomeDir)}
		logRefusal(log, r, slog.String("uid", u.Uid))
		return Paths{}, r
	}
	return fromHome(filepath.Clean(u.HomeDir)), nil
}

// fromHome builds the macOS paths under an absolute home directory.
func fromHome(home string) Paths {
	data := filepath.Join(home, "Library", "Application Support", "WraithBox")
	return Paths{
		Config: filepath.Join(home, ".config", "wraithbox"),
		Data:   data,
		Logs:   filepath.Join(home, "Library", "Logs", "WraithBox"),
		Run:    filepath.Join(data, "run"),
	}
}

// Real returns p with each root resolved to its real path, through
// every symbolic link, for a Seatbelt profile parameter: Seatbelt
// matches resolved paths (X23-sandboxed-daemons, condition 5). Run is
// the resolved Data's run/, which EnsureRunDir in localipc refuses when
// it is a symbolic link. Every root must exist: a missing one is an
// error that wraps fs.ErrNotExist, because a profile built from an
// unresolved path wouldn't match.
func (p Paths) Real() (Paths, error) {
	var out Paths
	for _, root := range []struct {
		name     string
		in       string
		resolved *string
	}{
		{"config", p.Config, &out.Config},
		{"data", p.Data, &out.Data},
		{"logs", p.Logs, &out.Logs},
	} {
		resolved, err := filepath.EvalSymlinks(root.in)
		if err != nil {
			return Paths{}, fmt.Errorf("paths: resolve the %s directory %q: %w", root.name, root.in, err)
		}
		*root.resolved = resolved
	}
	out.Run = filepath.Join(out.Data, "run")
	return out, nil
}

// logRefusal logs a refusal with its rule. A nil log is slog.Default().
func logRefusal(log *slog.Logger, r *Refusal, attrs ...slog.Attr) {
	if log == nil {
		log = slog.Default()
	}
	attrs = append([]slog.Attr{
		slog.String("rule", string(r.Rule)),
		slog.String("decision", "refuse"),
		slog.String("reason", r.msg),
	}, attrs...)
	log.LogAttrs(context.Background(), slog.LevelError, "host paths refused", attrs...)
}
