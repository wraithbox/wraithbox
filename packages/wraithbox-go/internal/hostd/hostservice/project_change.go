package hostservice

import (
	"context"
	"fmt"
	"log/slog"
	"slices"

	"google.golang.org/grpc/codes"

	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/state"
)

// edit changes a project row. It returns the row after the change, or a
// refusal.
type edit func(tx *state.ProjectTx, before state.Project) (state.Project, error)

// update runs fn on the project with id in one transaction and writes
// the row it returns. A row that fn left as it was is not written. Once
// the transaction has committed, a change is recorded under command
// with rule (S09-11-audit, "Project settings"). id is checked before
// the transaction takes state.db's write lock.
func (s *ProjectServer) update(ctx context.Context, method, command, rule, id string, fn edit) (state.Project, error) {
	if err := checkID(id); err != nil {
		return state.Project{}, s.refuse(ctx, method, err)
	}
	var c *change
	var after state.Project
	err := s.Store.Projects(ctx, func(tx *state.ProjectTx) error {
		before, err := byID(tx, id)
		if err != nil {
			return err
		}
		after, err = fn(tx, before)
		if err != nil || sameProject(before, after) {
			return err
		}
		c = &change{command: command, rule: rule, before: &before, after: &after}
		return tx.Update(after)
	})
	if err != nil {
		return state.Project{}, s.refuse(ctx, method, err)
	}
	s.record(ctx, c)
	if c == nil {
		rule = ruleProjectUnchanged
	}
	s.accept(ctx, method, rule, slog.String("project", after.ID))
	return after, nil
}

func sameProject(a, b state.Project) bool {
	return a.Realm == b.Realm && a.KeyPath == b.KeyPath && a.Name == b.Name && a.Placement == b.Placement &&
		a.ConfigTrust == b.ConfigTrust && slices.Equal(a.URLs, b.URLs) && slices.Equal(a.RootCommits, b.RootCommits)
}

// ConfirmProject records the repository's current identity and clears
// config_trust, so a replaced repository's .wraithbox/ isn't read until
// the user trusts it again (S05-04-naming, "Confirming";
// SEC09-host-policy). Its record holds both identities.
func (s *ProjectServer) ConfirmProject(ctx context.Context, req *hostdv1.ConfirmProjectRequest) (*hostdv1.ConfirmProjectResponse, error) {
	const method = "ConfirmProject"
	if err := s.admit(); err != nil {
		return nil, s.refuse(ctx, method, err)
	}
	current, err := checkIdentity(req.GetIdentity())
	if err != nil {
		return nil, s.refuse(ctx, method, err)
	}
	row, err := s.update(ctx, method, "wb project confirm", ruleProjectConfirmed, req.GetId(),
		func(_ *state.ProjectTx, p state.Project) (state.Project, error) {
			p.URLs, p.RootCommits, p.ConfigTrust = current.URLs, current.RootCommits, false
			return p, nil
		})
	if err != nil {
		return nil, err
	}
	return &hostdv1.ConfirmProjectResponse{Project: projectProto(row)}, nil
}

// MoveProject gives a project a new key, and keeps its id and recorded
// identity (S05-04-naming, "Moving"). A key another project has is
// refused, and names that project.
func (s *ProjectServer) MoveProject(ctx context.Context, req *hostdv1.MoveProjectRequest) (*hostdv1.MoveProjectResponse, error) {
	const method = "MoveProject"
	if err := s.admit(); err != nil {
		return nil, s.refuse(ctx, method, err)
	}
	if err := checkKey(req.GetKey()); err != nil {
		return nil, s.refuse(ctx, method, err)
	}
	realm, path := req.GetKey().GetRealm(), req.GetKey().GetPath()
	row, err := s.update(ctx, method, "wb project move", ruleProjectMoved, req.GetId(),
		func(tx *state.ProjectTx, p state.Project) (state.Project, error) {
			if err := keyFree(tx, realm, path, p.ID); err != nil {
				return state.Project{}, err
			}
			p.Realm, p.KeyPath = realm, path
			return p, nil
		})
	if err != nil {
		return nil, err
	}
	return &hostdv1.MoveProjectResponse{Project: projectProto(row)}, nil
}

// RenameProject changes a project's name (S05-04-naming, "Name").
func (s *ProjectServer) RenameProject(ctx context.Context, req *hostdv1.RenameProjectRequest) (*hostdv1.RenameProjectResponse, error) {
	const method = "RenameProject"
	if err := s.admit(); err != nil {
		return nil, s.refuse(ctx, method, err)
	}
	if err := checkName(req.GetName()); err != nil {
		return nil, s.refuse(ctx, method, err)
	}
	row, err := s.update(ctx, method, "wb project rename", ruleProjectRenamed, req.GetId(),
		func(_ *state.ProjectTx, p state.Project) (state.Project, error) {
			p.Name = req.GetName()
			return p, nil
		})
	if err != nil {
		return nil, err
	}
	return &hostdv1.RenameProjectResponse{Project: projectProto(row)}, nil
}

// PlaceProject sets a registered project's placement. The value it has
// succeeds without a change. Any other is refused: a project's home
// and Claude Code state are in its VM, and how they move is I232
// (S06-04-vms-and-placement, "Changing placement"). This interim rule
// fails closed until M2.10 lands the move.
func (s *ProjectServer) PlaceProject(ctx context.Context, req *hostdv1.PlaceProjectRequest) (*hostdv1.PlaceProjectResponse, error) {
	const method = "PlaceProject"
	if err := s.admit(); err != nil {
		return nil, s.refuse(ctx, method, err)
	}
	want, err := placementOf(req.GetPlacement(), "")
	if err != nil {
		return nil, s.refuse(ctx, method, err)
	}
	row, err := s.update(ctx, method, "wb project place", ruleProjectUnchanged, req.GetId(),
		func(_ *state.ProjectTx, p state.Project) (state.Project, error) {
			if p.Placement == want {
				return p, nil
			}
			return state.Project{}, &projectRefusal{
				code: codes.FailedPrecondition, rule: RuleProjectPlaceHasHome,
				msg: fmt.Sprintf("project %s is placed %s, and its home is in that VM. Moving a registered project to the %s VM "+
					"waits for I232. Today, remove it with wb project rm, then register it again with wb project place %s",
					p.Name, p.Placement, want, want),
			}
		})
	if err != nil {
		return nil, err
	}
	return &hostdv1.PlaceProjectResponse{Project: projectProto(row)}, nil
}

// RemoveProject deletes a project's row in one transaction, and records
// the values removed (S05-04-naming, "Removing"). Each later package
// that adds rows per project deletes them in this transaction too.
func (s *ProjectServer) RemoveProject(ctx context.Context, req *hostdv1.RemoveProjectRequest) (*hostdv1.RemoveProjectResponse, error) {
	const method = "RemoveProject"
	if err := s.admit(); err != nil {
		return nil, s.refuse(ctx, method, err)
	}
	if err := checkID(req.GetId()); err != nil {
		return nil, s.refuse(ctx, method, err)
	}
	var before state.Project
	err := s.Store.Projects(ctx, func(tx *state.ProjectTx) error {
		var err error
		before, err = byID(tx, req.GetId())
		if err != nil {
			return err
		}
		// The project's users go in this transaction, so no user row
		// outlives its project, and each guest gets a pending removal
		// (S05-04-naming, "The project user"; SEC08-proj-isolation).
		if _, err := tx.QueueUserRemovals(before.ID, s.now()); err != nil {
			return err
		}
		return tx.Delete(before.ID)
	})
	if err != nil {
		return nil, s.refuse(ctx, method, err)
	}
	if s.UsersRemoved != nil {
		s.UsersRemoved()
	}
	s.record(ctx, &change{command: "wb project rm", rule: ruleProjectRemoved, before: &before})
	s.accept(ctx, method, ruleProjectRemoved, slog.String("project", before.ID))
	return &hostdv1.RemoveProjectResponse{Project: projectProto(before)}, nil
}
