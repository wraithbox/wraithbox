package contracttest_test

import (
	"google.golang.org/protobuf/proto"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
)

// projectUser is a ProjectUser with every field set.
func projectUser() *guestv1.ProjectUser {
	return &guestv1.ProjectUser{
		ProjectId:         "p-0123456789abcdef",
		UserName:          "wbp10000",
		Uid:               10000,
		Gid:               10000,
		GroupName:         "wbp10000",
		UserGeneratedUid:  "6F9619FF-8B86-4D11-B42D-00C04FC964FF",
		GroupGeneratedUid: "0B2E8C4A-1D3F-4E5A-9B6C-7D8E9F0A1B2C",
		HomePath:          "/private/var/wbhomes/wbp10000",
	}
}

// userMessages holds a value of every message of the project user
// calls in wraithbox.guest.v1 (S06-03-image-updates), with every field
// set. A round trip only needs every field on the wire, so a value
// may pair fields no guest would send together.
func userMessages() []proto.Message {
	result := &guestv1.EnsureUserResult{Uid: 10000, Result: guestv1.EnsureResult_ENSURE_RESULT_REFUSED, Rule: "home-owned-by-other"}
	kill := &guestv1.KillUserResponse{
		KillId: 1 << 40, ProjectId: "p-0123456789abcdef",
		Result: guestv1.KillResult_KILL_RESULT_PROCESSES_LEFT, ProcessesLeft: 3,
	}
	return []proto.Message{
		projectUser(),
		&guestv1.EnsureUsersRequest{Users: []*guestv1.ProjectUser{projectUser()}},
		&guestv1.EnsureUsersResponse{Results: []*guestv1.EnsureUserResult{result}},
		result,
		&guestv1.UnlockUserRequest{Uid: 10000},
		&guestv1.UnlockUserResponse{},
		&guestv1.KillUserRequest{KillId: 1 << 40, ProjectId: "p-0123456789abcdef", Uid: 10000},
		kill,
		&guestv1.CleanupRecord{
			KillId: 1 << 40, ProjectId: "p-0123456789abcdef",
			Result: guestv1.KillResult_KILL_RESULT_PROCESSES_LEFT, ProcessesLeft: 3,
		},
		&guestv1.RemoveUserRequest{ProjectId: "p-0123456789abcdef", Uid: 10000, Gid: 10000, HomePath: "/private/var/wbhomes/wbp10000"},
		&guestv1.RemoveUserResponse{
			ProjectId: "p-0123456789abcdef", Uid: 10000,
			Result: guestv1.RemoveResult_REMOVE_RESULT_REFUSED, ProcessesLeft: 2, Rule: "home-not-owned",
		},
	}
}
