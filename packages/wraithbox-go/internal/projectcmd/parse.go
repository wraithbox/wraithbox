package projectcmd

import (
	"errors"
	"fmt"
	"strings"

	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/project"
)

// invocation is a parsed `wb project` command line.
type invocation struct {
	sub string
	// project is the P of `--project P`, and byFlag says it was given.
	project string
	byFlag  bool
	// arg is the one argument of move, rename and place.
	arg string
	// placement is place's argument.
	placement hostdv1.Placement
}

// arguments is the number of arguments each subcommand takes, besides
// `--project`.
var arguments = map[string]int{"show": 0, "confirm": 0, "move": 1, "rename": 1, "place": 1, "rm": 0}

// parse reads the subcommand, its argument, and `--project P` or
// `--project=P` anywhere after the subcommand. An argument after `--`
// is never a flag, so a directory can start with `-`. Any other flag is
// a usage error.
func parse(args []string) (invocation, error) {
	if len(args) == 0 {
		return invocation{}, errors.New("wb project needs a subcommand")
	}
	inv := invocation{sub: args[0]}
	want, ok := arguments[inv.sub]
	if !ok {
		return invocation{}, fmt.Errorf("wb project has no subcommand %q", args[0])
	}
	pos, err := inv.flags(args[1:])
	if err != nil {
		return invocation{}, err
	}
	if len(pos) != want {
		return invocation{}, fmt.Errorf("wb project %s takes %d arguments besides --project, not %d", inv.sub, want, len(pos))
	}
	if want == 1 {
		inv.arg = pos[0]
	}
	return inv, inv.check()
}

// flags reads `--project` from args and returns the other arguments.
func (inv *invocation) flags(args []string) ([]string, error) {
	var pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			return append(pos, args[i+1:]...), nil
		case a == "--project":
			if i+1 == len(args) {
				return nil, errors.New("--project needs a project id or name")
			}
			i++
			if err := inv.setProject(args[i]); err != nil {
				return nil, err
			}
		case strings.HasPrefix(a, "--project="):
			if err := inv.setProject(strings.TrimPrefix(a, "--project=")); err != nil {
				return nil, err
			}
		case len(a) > 1 && a[0] == '-':
			return nil, fmt.Errorf("wb project %s has no flag %q", inv.sub, a)
		default:
			pos = append(pos, a)
		}
	}
	return pos, nil
}

func (inv *invocation) setProject(p string) error {
	switch {
	case inv.byFlag:
		return errors.New("--project is given twice")
	case p == "":
		return errors.New("--project needs a project id or name")
	}
	inv.project, inv.byFlag = p, true
	return nil
}

// check refuses what each subcommand doesn't take. `confirm` takes no
// `--project`: it records the identity of the repository in the
// directory, so it needs that repository (S05-03-management-commands).
// A new name is checked before wb-hostd is asked (S05-04-naming,
// "Name").
func (inv *invocation) check() error {
	switch inv.sub {
	case "confirm":
		if inv.byFlag {
			return errors.New("wb project confirm takes no --project: it records the identity of the repository in the directory, so run it there, or name the directory with -C")
		}
	case "rename":
		if err := project.ValidName(inv.arg); err != nil {
			return err
		}
	case "place":
		switch inv.arg {
		case "work":
			inv.placement = hostdv1.Placement_PLACEMENT_WORK
		case "isolated":
			inv.placement = hostdv1.Placement_PLACEMENT_ISOLATED
		default:
			return fmt.Errorf("wb project place takes work or isolated, not %q", inv.arg)
		}
	}
	return nil
}
