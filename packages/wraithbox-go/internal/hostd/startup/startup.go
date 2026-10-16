// Package startup reads wb-hostd's settings and opens its database at
// start, and decides whether wb-hostd runs or is in refusing mode
// (S17-05-upgrades-and-migrations, "Refusing mode").
//
// In refusing mode wb-hostd keeps running, so launchd doesn't restart it
// in a loop. It starts no daemon and no VM, answers wb status, and
// refuses every other request with the rule. Prepare returns the value
// that holds the rule. It never exits the process.
package startup

import (
	"context"
	"errors"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/config"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/state"
)

// ruleStartFailed is the rule of an error that is neither package's
// refusal. Neither returns one, so it marks a bug, and still refuses.
const ruleStartFailed = "hostd-start-failed"

// Refusing says why wb-hostd is in refusing mode. Rule is the rule of
// the refusal, such as state-db-newer or config-key-unknown. Detail
// names the versions, the file and line, or the newest backup
// (NFR06-explained-refusals). Every VM start and every request but wb
// status is refused with it.
type Refusing struct {
	Rule   string
	Detail string
	err    error
}

func (r *Refusing) Error() string { return r.Rule + ": " + r.Detail }

// Unwrap returns the package's refusal.
func (r *Refusing) Unwrap() error { return r.err }

// Host is what a wb-hostd that isn't refusing runs with.
type Host struct {
	Config config.Config
	State  *state.DB
}

// Prepare reads <configDir>/config.toml and opens and migrates
// <dataDir>/state.db, in that order. It returns a Host, or a Refusing
// and no Host. A refused settings file stops it before the database is
// touched, so refusing mode never migrates. Each refusal has been
// logged with its rule by the package that made it.
func Prepare(ctx context.Context, configDir, dataDir string) (*Host, *Refusing) {
	cfg, err := config.Load(ctx, configDir)
	if err != nil {
		return nil, refusing(err)
	}
	db, err := state.Open(ctx, dataDir)
	if err != nil {
		return nil, refusing(err)
	}
	return &Host{Config: cfg, State: db}, nil
}

func refusing(err error) *Refusing {
	if r, ok := errors.AsType[*config.Refusal](err); ok {
		return &Refusing{Rule: string(r.Rule), Detail: r.Error(), err: err}
	}
	if r, ok := errors.AsType[*state.Refusal](err); ok {
		return &Refusing{Rule: string(r.Rule), Detail: r.Error(), err: err}
	}
	return &Refusing{Rule: ruleStartFailed, Detail: err.Error(), err: err}
}
