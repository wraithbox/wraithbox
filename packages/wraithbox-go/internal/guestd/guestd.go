// Package guestd is wb-guestd's gRPC service, GuestService of
// proto/wraithbox/guest/v1/guest.proto, which wb-hostd calls over the
// host-guest socket (S06-12-guest-agent, "`wb-guestd`").
//
// It serves Hello only so far. The session messages, users, exec and the
// git transport join it later.
package guestd

import (
	"context"
	"log/slog"

	"google.golang.org/grpc"
	"google.golang.org/grpc/peer"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/version"
)

// MaxRecvMsgSize is the largest message wb-guestd takes from the host.
// The host sends nothing large.
const MaxRecvMsgSize = 64 << 10

// ServedPackages is the full name of each .proto package wb-guestd
// serves, which wb-hostd checks against its own list
// (S04-08-records-and-bounds, "With `wb-guestd`"). It comes from the
// generated descriptor, so it changes with the contract.
func ServedPackages() []string {
	return []string{string(guestv1.File_wraithbox_guest_v1_guest_proto.Package())}
}

// NewServer returns a gRPC server with GuestService registered. own is
// the build version Hello reports, normally version.Build(): "" for an
// unstamped build, which the host refuses.
func NewServer(own string, log *slog.Logger) *grpc.Server {
	srv := grpc.NewServer(grpc.MaxRecvMsgSize(MaxRecvMsgSize))
	guestv1.RegisterGuestServiceServer(srv, &service{version: own, log: log})
	return srv
}

type service struct {
	guestv1.UnimplementedGuestServiceServer

	version string
	log     *slog.Logger
}

// Hello reports wb-guestd's build version and the packages it serves.
func (s *service) Hello(ctx context.Context, _ *guestv1.HelloRequest) (*guestv1.HelloResponse, error) {
	from := "unknown"
	if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
		from = p.Addr.String()
	}
	s.log.LogAttrs(ctx, slog.LevelInfo, "hello",
		slog.String("peer", from), slog.String("version", version.Label(s.version)))
	return &guestv1.HelloResponse{
		GuestdVersion:  s.version,
		ServedPackages: ServedPackages(),
	}, nil
}
