//go:build unix

package hostservice_test

import (
	"slices"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"

	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/hostservice"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/state"
)

// twoProjects registers a at /r/a/.git with urlA and b at /r/b/.git
// with urlB, both work and named foo.
func twoProjects(t *testing.T) (f *projectFixture, a, b *hostdv1.Project) {
	t.Helper()
	f = newProjects(t, nil)
	a = f.register(t, "/r/a/.git", urls(urlA), hostdv1.Placement_PLACEMENT_UNSPECIFIED).GetProject()
	b = f.register(t, "/r/b/.git", urls(urlB), hostdv1.Placement_PLACEMENT_UNSPECIFIED).GetProject()
	return f, a, b
}

// TestProjectMove: a move keeps the id and the recorded identity, and
// is recorded. A move onto another project's key is refused, and names
// that project (S05-04-naming, "Moving").
func TestProjectMove(t *testing.T) {
	f, a, b := twoProjects(t)
	_, err := f.c.MoveProject(t.Context(), &hostdv1.MoveProjectRequest{Id: a.GetId(), Key: key("/r/b/.git")})
	msg := wantRefused(t, "move onto b", err, codes.FailedPrecondition, hostservice.RuleProjectKeyTaken)
	if !strings.Contains(msg, b.GetId()) {
		t.Errorf("the refusal doesn't name b: %s", msg)
	}
	moved, err := f.c.MoveProject(t.Context(), &hostdv1.MoveProjectRequest{Id: a.GetId(), Key: key("/r/moved/.git")})
	if err != nil {
		t.Fatal(err)
	}
	if p := moved.GetProject(); p.GetId() != a.GetId() || p.GetKey().GetPath() != "/r/moved/.git" ||
		!slices.Equal(p.GetIdentity().GetUrls(), []string{urlA}) {
		t.Errorf("moved %v", p)
	}
	before, after := f.lastRecord(t, "wb project move", a.GetId())
	if before["location"] != "/r/a/.git" || after["location"] != "/r/moved/.git" {
		t.Errorf("record before %v after %v", before, after)
	}
	_, err = f.c.MoveProject(t.Context(), &hostdv1.MoveProjectRequest{Id: a.GetId(), Key: key("moved")})
	wantRefused(t, "relative key", err, codes.InvalidArgument, hostservice.RuleProjectForm)
}

// TestProjectRename: an invalid name is refused, and a rename is
// recorded (S05-04-naming, "Name").
func TestProjectRename(t *testing.T) {
	f, a, _ := twoProjects(t)
	_, err := f.c.RenameProject(t.Context(), &hostdv1.RenameProjectRequest{Id: a.GetId(), Name: "-x"})
	wantRefused(t, "invalid name", err, codes.InvalidArgument, hostservice.RuleProjectNameInvalid)
	if _, err := f.c.RenameProject(t.Context(), &hostdv1.RenameProjectRequest{Id: a.GetId(), Name: "bar"}); err != nil {
		t.Fatal(err)
	}
	if before, after := f.lastRecord(t, "wb project rename", a.GetId()); before["name"] != "foo" || after["name"] != "bar" {
		t.Errorf("record before %v after %v", before, after)
	}
}

// TestProjectPlace: the placement a project has succeeds without a
// change or a record. Another one is refused naming I232 and today's
// way, and a missing one is refused (S06-04-vms-and-placement,
// "Changing placement").
func TestProjectPlace(t *testing.T) {
	f, a, _ := twoProjects(t)
	n := len(f.records(t))
	same, err := f.c.PlaceProject(t.Context(), &hostdv1.PlaceProjectRequest{Id: a.GetId(), Placement: hostdv1.Placement_PLACEMENT_WORK})
	if err != nil || same.GetProject().GetPlacement() != hostdv1.Placement_PLACEMENT_WORK || len(f.records(t)) != n {
		t.Errorf("place work on a work project: %v %v", same, err)
	}
	_, err = f.c.PlaceProject(t.Context(), &hostdv1.PlaceProjectRequest{Id: a.GetId(), Placement: hostdv1.Placement_PLACEMENT_ISOLATED})
	msg := wantRefused(t, "place change", err, codes.FailedPrecondition, hostservice.RuleProjectPlaceHasHome)
	for _, want := range []string{"I232", "wb project rm", "wb project place isolated"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal misses %q: %s", want, msg)
		}
	}
	_, err = f.c.PlaceProject(t.Context(), &hostdv1.PlaceProjectRequest{Id: a.GetId()})
	wantRefused(t, "place without a value", err, codes.InvalidArgument, hostservice.RuleProjectPlacement)
}

// TestProjectRemove: a removal returns the project as it was and
// records its values. Then the id is unknown (S05-04-naming,
// "Removing").
func TestProjectRemove(t *testing.T) {
	f, a, _ := twoProjects(t)
	removed, err := f.c.RemoveProject(t.Context(), &hostdv1.RemoveProjectRequest{Id: a.GetId()})
	if err != nil || removed.GetProject().GetKey().GetPath() != "/r/a/.git" {
		t.Fatalf("remove: %v %v", removed, err)
	}
	before, after := f.lastRecord(t, "wb project rm", a.GetId())
	if before["location"] != "/r/a/.git" || !slices.Equal(listOf(before["urls"]), []string{urlA}) || after != nil {
		t.Errorf("record before %v after %v", before, after)
	}
	_, err = f.c.GetProject(t.Context(), &hostdv1.GetProjectRequest{By: &hostdv1.GetProjectRequest_Id{Id: a.GetId()}})
	wantRefused(t, "get after rm", err, codes.NotFound, hostservice.RuleProjectUnknown)
	_, err = f.c.RemoveProject(t.Context(), &hostdv1.RemoveProjectRequest{Id: a.GetId()})
	wantRefused(t, "rm twice", err, codes.NotFound, hostservice.RuleProjectUnknown)
}

// TestProjectRemoveQueuesUserRemovals: a removal deletes the project's
// users with it, queues one removal per data disk, and then asks the
// links to send them (S05-04-naming, "The project user").
func TestProjectRemoveQueuesUserRemovals(t *testing.T) {
	kicks := 0
	f := newProjects(t, func(s *hostservice.ProjectServer) { s.UsersRemoved = func() { kicks++ } })
	a := f.register(t, "/r/a/.git", urls(urlA), hostdv1.Placement_PLACEMENT_UNSPECIFIED).GetProject()
	disk := state.DataDisk{Slot: "macos-work", VolumeUUID: "0F1E2D3C-4B5A-4978-8796-A5B4C3D2E1F0", TempName: "data.wbdisk.tmp", SizeGiB: 128}
	if err := f.db.RecordDataDiskFormat(t.Context(), disk); err != nil {
		t.Fatal(err)
	}
	if err := f.db.MarkDataDiskFormatted(t.Context(), disk.Slot, disk.VolumeUUID); err != nil {
		t.Fatal(err)
	}
	u, _, err := f.db.ProjectUser(t.Context(), a.GetId(), disk.Slot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.c.RemoveProject(t.Context(), &hostdv1.RemoveProjectRequest{Id: a.GetId()}); err != nil {
		t.Fatal(err)
	}
	rms, err := f.db.UserRemovals(t.Context(), disk.Slot)
	if err != nil || len(rms) != 1 || rms[0].ProjectID != a.GetId() || rms[0].UID != u.UID || rms[0].HomePath != u.HomePath {
		t.Fatalf("removals %+v %v, want the one of %+v", rms, err, u)
	}
	if users, err := f.db.ProjectUsers(t.Context(), disk.Slot); err != nil || len(users) != 0 {
		t.Fatalf("users left %+v %v", users, err)
	}
	if kicks != 1 {
		t.Fatalf("%d sends asked for, want 1", kicks)
	}
}
