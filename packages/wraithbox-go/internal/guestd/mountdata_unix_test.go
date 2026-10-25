//go:build unix

package guestd_test

import (
	"log/slog"
	"slices"
	"testing"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestlink"
)

// mountData runs one MountData from the host's end of a socketpair,
// with the hello first, as wb-hostd calls it.
func mountData(t *testing.T, vols *fakeVolumes, req *guestv1.MountDataRequest) error {
	t.Helper()
	const v = "1.2.3"
	host, guest := socketpair(t)
	dm, _, _ := newDataMount(vols)
	srv := guestd.NewServer(v, slog.New(slog.DiscardHandler), guestd.WithDataMount(dm))
	go func() { _ = srv.Serve(newOneConn(guest)) }()
	defer srv.Stop()
	link, err := guestlink.Hello(t.Context(), host, v, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("Hello: %v", err)
	}
	defer link.Close()
	_, err = link.Client().MountData(t.Context(), req)
	return err
}

// TestMountDataRPC: MountData answers a mount with success, a refused
// mount with FAILED_PRECONDITION and the rule as its ErrorInfo reason,
// and a UUID that isn't canonical with INVALID_ARGUMENT, without a
// diskutil call.
func TestMountDataRPC(t *testing.T) {
	cases := []struct {
		name   string
		uuid   string
		change func(*fakeVolumes)
		code   codes.Code
		rule   string
	}{
		{"mounted", testUUID, func(*fakeVolumes) {}, codes.OK, ""},
		{"encrypted", testUUID, func(f *fakeVolumes) { f.vol.Encrypted = true }, codes.FailedPrecondition, guestd.RuleVolumeEncrypted},
		{"lowercase UUID", "0f8e98a2-bc26-4e5d-92e5-fd76d3bb8802", func(*fakeVolumes) {}, codes.InvalidArgument, guestd.RuleMountRequest},
		{"device for a UUID", "disk5s1", func(*fakeVolumes) {}, codes.InvalidArgument, guestd.RuleMountRequest},
		{"no UUID", "", func(*fakeVolumes) {}, codes.InvalidArgument, guestd.RuleMountRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			vols := newFakeVolumes()
			c.change(vols)
			err := mountData(t, vols, &guestv1.MountDataRequest{VolumeUuid: c.uuid, FirstMount: true})
			st := status.Convert(err)
			if st.Code() != c.code {
				t.Fatalf("MountData = %v, want %v", err, c.code)
			}
			if c.code == codes.InvalidArgument && vols.infoCalls != 0 {
				t.Fatal("asked diskutil about a request it refused")
			}
			if c.rule != "" {
				wantErrorInfo(t, st, c.rule)
			}
		})
	}
}

// TestMountRulesListed: wb-hostd takes every rule MountData refuses
// with, and only those, so a rule added on one side is added on the
// other.
func TestMountRulesListed(t *testing.T) {
	guest := []string{
		guestd.RuleVolumeMissing, guestd.RuleVolumeEncrypted, guestd.RuleVolumeMountFailed,
		guestd.RuleMountPoint, guestd.RuleMountFlags, guestd.RuleMountVolume, guestd.RuleMountRoot,
	}
	if !slices.Equal(guest, guestlink.MountRules) {
		t.Fatalf("wb-guestd's rules %q, wb-hostd's %q", guest, guestlink.MountRules)
	}
	if guestd.ErrorDomain != string(guestv1.File_wraithbox_guest_v1_guest_proto.Package()) {
		t.Fatalf("domain %s isn't the contract's package", guestd.ErrorDomain)
	}
}

// wantErrorInfo: the status has one detail, an ErrorInfo in wb-guestd's
// domain with the rule as its reason.
func wantErrorInfo(t *testing.T, st *status.Status, rule string) {
	t.Helper()
	details := st.Details()
	if len(details) != 1 {
		t.Fatalf("details %v, want one ErrorInfo", details)
	}
	info, ok := details[0].(*errdetails.ErrorInfo)
	if !ok || info.GetReason() != rule || info.GetDomain() != guestd.ErrorDomain {
		t.Fatalf("detail %v, want ErrorInfo %s in %s", details[0], rule, guestd.ErrorDomain)
	}
}
