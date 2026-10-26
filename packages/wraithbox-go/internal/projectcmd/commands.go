package projectcmd

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/project"
)

// target returns the project a command acts on: the one `--project`
// names, with no git run, or the one of the repository in the
// directory, after the identity check (S05-04-naming, "Which commands").
func (c *cmd) target(ctx context.Context) (*hostdv1.Project, error) {
	if c.inv.byFlag {
		return c.get(ctx)
	}
	_, p, err := c.open.Find(ctx, c.dir)
	if err != nil {
		return nil, err
	}
	return checked(p)
}

// get asks wb-hostd for the project of `--project P`. A P in the form of
// a project id names an id, and any other P a name. wb-hostd refuses a
// name that more than one project has, with their ids.
func (c *cmd) get(ctx context.Context) (*hostdv1.Project, error) {
	req := &hostdv1.GetProjectRequest{By: &hostdv1.GetProjectRequest_Name{Name: c.inv.project}}
	if project.IsID(c.inv.project) {
		req.By = &hostdv1.GetProjectRequest_Id{Id: c.inv.project}
	}
	res, err := c.hostd.GetProject(ctx, req)
	if err != nil {
		return nil, err
	}
	return checked(res.GetProject())
}

// checked refuses a project from wb-hostd whose id isn't in the id
// form, before wb builds a path or a command line from it
// (S04-06-git-confinement).
func checked(p *hostdv1.Project) (*hostdv1.Project, error) {
	if err := project.CheckID(p.GetId()); err != nil {
		return nil, err
	}
	return p, nil
}

// show prints the project's id, name, location, recorded identity,
// placement and config_trust.
func (c *cmd) show(ctx context.Context) error {
	p, err := c.target(ctx)
	if err != nil {
		return err
	}
	w := c.env.Stdout
	line(w, "id", p.GetId())
	line(w, "name", p.GetName())
	line(w, "location", p.GetKey().GetPath())
	printIdentity(w, "recorded", p.GetIdentity())
	line(w, "placement", project.PlacementName(p.GetPlacement()))
	line(w, "config_trust", onOff(p.GetConfigTrust()))
	return nil
}

// confirm records the repository's current identity as the project's,
// and clears config_trust (S05-04-naming, "Confirming"). It skips the
// identity check, which is what it answers.
func (c *cmd) confirm(ctx context.Context) error {
	l, id, err := c.open.FindID(ctx, c.dir)
	if err != nil {
		return err
	}
	res, err := c.hostd.ConfirmProject(ctx, &hostdv1.ConfirmProjectRequest{Id: id, Identity: project.IdentityProto(l.Identity)})
	if err != nil {
		return err
	}
	p := res.GetProject()
	w := c.env.Stdout
	line(w, "confirmed", fmt.Sprintf("project %s (%s)", p.GetName(), p.GetId()))
	printIdentity(w, "recorded", p.GetIdentity())
	line(w, "placement", project.PlacementName(p.GetPlacement()))
	line(w, "config_trust", onOff(p.GetConfigTrust()))
	return nil
}

// move points the project at the repository in <dir>, a path relative
// to the directory when it isn't absolute. It resolves <dir> with the
// rules of every other directory, and prints any difference between
// the recorded identity and <dir>'s (S05-04-naming, "Moving").
func (c *cmd) move(ctx context.Context) error {
	p, err := c.target(ctx)
	if err != nil {
		return err
	}
	to := c.inv.arg
	if !filepath.IsAbs(to) {
		to = filepath.Join(c.dir, to)
	}
	l, err := c.open.Read(ctx, to)
	if err != nil {
		return err
	}
	res, err := c.hostd.MoveProject(ctx, &hostdv1.MoveProjectRequest{Id: p.GetId(), Key: project.KeyProto(l.Repo.Key)})
	if err != nil {
		return err
	}
	w := c.env.Stdout
	line(w, "moved", fmt.Sprintf("project %s (%s)", res.GetProject().GetName(), res.GetProject().GetId()))
	line(w, "location", res.GetProject().GetKey().GetPath())
	rule := project.CheckIdentity(project.IdentityOf(p.GetIdentity()), l.Identity)
	if rule == project.RuleIdentityMatched {
		return nil
	}
	printIdentity(w, "recorded", p.GetIdentity())
	printIdentity(w, "now", project.IdentityProto(l.Identity))
	line(w, "note", fmt.Sprintf("the repository is not the one recorded (%s): the next session start refuses until wb project confirm", rule))
	return nil
}

// rename changes the project's name. parse checked the name.
func (c *cmd) rename(ctx context.Context) error {
	p, err := c.target(ctx)
	if err != nil {
		return err
	}
	res, err := c.hostd.RenameProject(ctx, &hostdv1.RenameProjectRequest{Id: p.GetId(), Name: c.inv.arg})
	if err != nil {
		return err
	}
	line(c.env.Stdout, "renamed", fmt.Sprintf("project %s (%s) is now %s", p.GetName(), p.GetId(), res.GetProject().GetName()))
	return nil
}

// place sets the project's placement. From a directory whose
// repository isn't a project yet, it registers one with that placement
// first, by the rules of a session start (S05-03-management-commands,
// "`wb project place`"). wb-hostd refuses another placement for a
// project that has one, and names I232.
func (c *cmd) place(ctx context.Context) error {
	if c.inv.byFlag {
		p, err := c.get(ctx)
		if err != nil {
			return err
		}
		return c.setPlacement(ctx, p)
	}
	op, err := c.open.OpenToPlace(ctx, c.dir, c.inv.placement)
	if err != nil {
		return err
	}
	for _, l := range op.Notice() {
		fmt.Fprintln(c.env.Stderr, printable(l))
	}
	if op.Registered {
		return nil
	}
	return c.setPlacement(ctx, op.Project)
}

// setPlacement asks wb-hostd to give p the placement of the command
// line, and prints the placement p has.
func (c *cmd) setPlacement(ctx context.Context, p *hostdv1.Project) error {
	res, err := c.hostd.PlaceProject(ctx, &hostdv1.PlaceProjectRequest{Id: p.GetId(), Placement: c.inv.placement})
	if err != nil {
		return err
	}
	line(c.env.Stdout, "placement", fmt.Sprintf("project %s (%s) is %s", res.GetProject().GetName(), res.GetProject().GetId(),
		project.PlacementName(res.GetProject().GetPlacement())))
	return nil
}

// rm removes the project from state.db, then the files wb keeps for it
// (S05-04-naming, "Removing"). It skips the identity check, so a
// replaced repository's project can be dropped.
func (c *cmd) rm(ctx context.Context) error {
	var id string
	if c.inv.byFlag {
		p, err := c.get(ctx)
		if err != nil {
			return err
		}
		id = p.GetId()
	} else {
		_, found, err := c.open.FindID(ctx, c.dir)
		if err != nil {
			return err
		}
		id = found
	}
	res, err := c.hostd.RemoveProject(ctx, &hostdv1.RemoveProjectRequest{Id: id})
	if err != nil {
		return err
	}
	what := fmt.Sprintf("project %s (%s)", res.GetProject().GetName(), id)
	if err := project.RemoveFiles(c.env.Paths, id); err != nil {
		line(c.env.Stderr, "removed from wb-hostd", what)
		return err
	}
	line(c.env.Stdout, "removed", what)
	return nil
}

// printIdentity prints an identity's URLs and root commits, one per
// line.
func printIdentity(w io.Writer, label string, id *hostdv1.ProjectIdentity) {
	if len(id.GetUrls()) == 0 && len(id.GetRootCommits()) == 0 {
		line(w, label+" identity", "none")
		return
	}
	for _, u := range id.GetUrls() {
		line(w, label+" URL", u)
	}
	for _, r := range id.GetRootCommits() {
		line(w, label+" root commit", r)
	}
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}
