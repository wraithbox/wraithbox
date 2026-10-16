package selfsandbox

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"runtime"
	"strconv"
	"sync"
	"syscall"
)

// probePath is the file the deny probe opens. Every planned profile
// denies reading it, and a Seatbelt denial of a file open is EPERM
// (S04-03-self-sandboxing, "Confined first thing in main").
const probePath = "/etc/passwd"

// confineMu serializes Confine. confined is set once a profile applied,
// and confinedBy names it. darwin lets a confined process
// apply the same profile again, and refuses any other one, so Confine
// refuses every second call itself.
var (
	confineMu  sync.Mutex
	confined   bool
	confinedBy string
)

// Confine applies the Seatbelt profile p to the calling process, with
// params as its parameters, and then runs the deny probe. In order it:
//
//  1. refuses a second call with RuleInitFailed once a profile applied,
//     and checks the profile and the parameters (checkProfile), so a bad
//     set leaves the process unconfined;
//  2. sets GOMAXPROCS to the CPU count, which stops the runtime from
//     reading the CPU count again, a read the profile denies
//     (X23-sandboxed-daemons, condition 2);
//  3. calls sandbox_init_with_parameters (X23-sandboxed-daemons,
//     condition 1);
//  4. opens probePath for reading, which must fail with EPERM. The probe
//     is here so that no daemon can leave it out (SEC12-least-privilege).
//
// It logs the decision with its rule, and returns a *Refusal on any
// failure. The caller exits non-zero then (X23-sandboxed-daemons,
// condition 7). After a probe refusal the process is confined, with a
// profile that doesn't deny what it must.
func Confine(p Profile, params map[string]string) error {
	confineMu.Lock()
	defer confineMu.Unlock()
	if confined {
		return logged(&Refusal{Rule: RuleInitFailed, Profile: p.Name, msg: "the process is already confined by profile " + strconv.Quote(confinedBy)})
	}
	if r := checkProfile(p, params); r != nil {
		return logged(r)
	}
	runtime.GOMAXPROCS(runtime.NumCPU())
	if msg, ok := sandboxInit(p.Text, params); !ok {
		return logged(&Refusal{Rule: RuleInitFailed, Profile: p.Name, msg: "sandbox_init_with_parameters: " + msg})
	}
	confined, confinedBy = true, p.Name
	if r := probe(p.Name); r != nil {
		return logged(r)
	}
	slog.Default().LogAttrs(context.Background(), slog.LevelInfo, "self-sandbox applied",
		slog.String("rule", string(RuleApplied)),
		slog.String("decision", "confined"),
		slog.String("profile", p.Name),
		slog.String("probe", probePath))
	return nil
}

// probe opens probePath, which the profile must deny.
func probe(profile string) *Refusal {
	f, err := os.Open(probePath)
	switch {
	case err == nil:
		_ = f.Close()
		return &Refusal{Rule: RuleProbeAllowed, Profile: profile, msg: "the deny probe opened " + probePath + " after the profile applied"}
	case errors.Is(err, syscall.EPERM):
		return nil
	default:
		return &Refusal{Rule: RuleProbeUnexpected, Profile: profile, msg: "the deny probe failed without EPERM", err: err}
	}
}

// logged logs a refusal with its rule and returns it.
func logged(r *Refusal) error {
	slog.Default().LogAttrs(context.Background(), slog.LevelError, "self-sandbox refused",
		slog.String("rule", string(r.Rule)),
		slog.String("decision", "refuse"),
		slog.String("profile", r.Profile),
		slog.String("reason", r.Error()))
	return r
}
