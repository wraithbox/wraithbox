package setup

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/service"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/version"
)

// agentAction is what wb setup does with the agent.
type agentAction int

const (
	// agentKeep: the plist has the same bytes and the agent is loaded.
	agentKeep agentAction = iota
	// agentStart: the plist has the same bytes and the agent isn't
	// loaded.
	agentStart
	// agentReplace: the plist is missing or differs, and no VM runs.
	agentReplace
)

// agent is the plan for the agent.
type agent struct {
	program string
	action  agentAction
}

// planAgent decides what to do with the agent that runs the bundle's
// wb-hostd (S17-02-setup, "The agent"). With the same plist bytes it
// asks wb-hostd nothing. Otherwise replacing the agent stops wb-hostd
// and its VMs, so it first asks wb-hostd, and refuses with
// RuleVMsRunning unless it learns that no VM runs. It changes nothing.
func planAgent(ctx context.Context, env *Env, bundle string) (agent, error) {
	a := agent{program: filepath.Join(bundle, "Contents", "MacOS", "wb-hostd")}
	st, err := env.Service.State(ctx, a.program)
	if err != nil {
		return agent{}, fmt.Errorf("read the agent's state: %w", err)
	}
	switch {
	case st.Current && st.Loaded:
		a.action = agentKeep
		decide(ctx, env.Log, RuleVMsRunning, "keep", "the loaded agent's plist has the same bytes")
	case st.Current:
		a.action = agentStart
		decide(ctx, env.Log, RuleVMsRunning, "start", "the agent's plist has the same bytes, and it isn't loaded")
	default:
		if err := askHostd(ctx, env); err != nil {
			return agent{}, err
		}
		a.action = agentReplace
	}
	return a, nil
}

// stopFix says how to stop wb-hostd, which stops its VMs, once the user
// has stopped them.
func stopFix(env *Env) string {
	return fmt.Sprintf("then stop wb-hostd with: launchctl bootout gui/%d/%s, and run wb setup again", env.UID, service.Label)
}

// askHostd asks the running wb-hostd, if any, whether a VM runs, and
// fails closed (S17-02-setup, "The agent"): only no wb-hostd at all, or
// one of this build that lists no VM, lets wb setup go on. Each other
// answer, and every error, refuses with RuleVMsRunning.
func askHostd(ctx context.Context, env *Env) error {
	vmsRunning := func(msg, fix string) error {
		return refuse(ctx, env.Log, &Refusal{Rule: RuleVMsRunning, Msg: msg, Fix: fix})
	}
	st, err := env.Hostd.State(ctx)
	if err != nil {
		return vmsRunning(
			fmt.Sprintf("replacing the agent stops wb-hostd and every VM, and wb setup can't ask wb-hostd whether a VM runs: %v", err),
			"Stop every VM with wb vm stop, "+stopFix(env)+".")
	}
	switch {
	case st.Kind == HostdNotListening:
		decide(ctx, env.Log, RuleVMsRunning, "replace", "no wb-hostd listens, so no VM runs")
		return nil
	case st.Kind == HostdOtherVersion:
		return vmsRunning(
			fmt.Sprintf("a wb-hostd of build %s runs, and this wb is build %s, so wb setup can't read its VMs, and replacing the agent would stop them", st.Peer, version.Label(env.Version)),
			fmt.Sprintf("Stop its VMs with the wb of build %s (wb vm stop), %s.", st.Peer, stopFix(env)))
	case !st.Listed:
		return vmsRunning(
			"a wb-hostd of this build runs, and it can't list its VMs yet, so wb setup can't tell whether a VM runs, and replacing the agent would stop every VM",
			"Stop every VM with wb vm stop, "+stopFix(env)+".")
	case len(st.VMs) > 0:
		return vmsRunning(
			"replacing the agent would stop these VMs and end their sessions: "+describeVMs(st.VMs),
			"Stop them with wb vm stop, then run wb setup again.")
	}
	decide(ctx, env.Log, RuleVMsRunning, "replace", "wb-hostd lists no running VM")
	return nil
}

// describeVMs names each VM and its sessions. The names come from
// wb-hostd, so each is quoted, which escapes control characters.
func describeVMs(vms []VM) string {
	parts := make([]string, 0, len(vms))
	for _, vm := range vms {
		sessions := "no sessions"
		if len(vm.Sessions) > 0 {
			quoted := make([]string, 0, len(vm.Sessions))
			for _, s := range vm.Sessions {
				quoted = append(quoted, strconv.QuoteToASCII(s))
			}
			sessions = "sessions " + strings.Join(quoted, ", ")
		}
		parts = append(parts, fmt.Sprintf("%s (%s)", strconv.QuoteToASCII(vm.Name), sessions))
	}
	return strings.Join(parts, "; ")
}

// applyAgent carries out the plan: nothing, a start, or a replace that
// writes the plist and runs bootout and bootstrap.
func applyAgent(ctx context.Context, env *Env, a agent, out io.Writer) error {
	switch a.action {
	case agentKeep:
		fmt.Fprintf(out, "wb setup: the agent %s is up to date and loaded\n", service.Label)
		return nil
	case agentStart:
		if err := env.Service.Start(ctx); err != nil {
			return fmt.Errorf("start the agent: %w", err)
		}
		fmt.Fprintf(out, "wb setup: started the agent %s\n", service.Label)
		return nil
	case agentReplace:
		if err := env.Service.Replace(ctx, a.program); err != nil {
			return fmt.Errorf("install the agent: %w", err)
		}
		fmt.Fprintf(out, "wb setup: installed and started the agent %s, which runs %s\n", service.Label, a.program)
		return nil
	}
	return fmt.Errorf("unknown agent action %d", a.action)
}
