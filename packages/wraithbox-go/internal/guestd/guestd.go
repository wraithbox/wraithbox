// Package guestd is wb-guestd's gRPC service, GuestService of
// proto/wraithbox/guest/v1/guest.proto, which wb-hostd calls over the
// host-guest socket (S06-12-guest-agent, "`wb-guestd`").
//
// It serves Hello, SetTime, MountData, and the project users' calls:
// EnsureUsers, UnlockUser, KillUser and RemoveUser. The session
// messages, exec and the git transport join it later. When the last
// connection from the host ends, it runs the stop loop
// (S13-01-process-hardening, "When the link drops").
package guestd

import (
	"context"
	"log/slog"

	"google.golang.org/grpc"
	"google.golang.org/grpc/peer"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd/procs"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd/users"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestmount"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestusers"
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

// Option changes the service NewServer registers.
type Option func(*service)

// WithClock makes SetTime read and set c instead of the guest's clock.
func WithClock(c Clock) Option { return func(s *service) { s.clock = c } }

// WithDataMount makes MountData mount and check with d instead of the
// guest's own volumes.
func WithDataMount(d *DataMount) Option { return func(s *service) { s.dataMount = d } }

// NewServer returns a gRPC server with GuestService registered. own is
// the build version Hello reports, normally version.Build(): "" for an
// unstamped build, which the host refuses.
func NewServer(own string, log *slog.Logger, opts ...Option) *grpc.Server {
	svc := &service{version: own, log: log, clock: systemClock{}}
	for _, o := range opts {
		o(svc)
	}
	if svc.dataMount == nil {
		svc.dataMount = NewDataMount(&guestmount.System{Log: log}, nil, log)
	}
	if svc.users == nil {
		svc.users = systemUsers(log)
	}
	if svc.loops == nil {
		svc.loops = SystemProcesses(log)
	}
	if svc.emptier == nil {
		svc.emptier = &guestusers.System{Log: log}
	}
	if svc.openRoot == nil {
		svc.openRoot = func() (guestusers.Root, error) { return guestusers.OpenRoot(HomePath) }
	}
	srv := grpc.NewServer(grpc.MaxRecvMsgSize(MaxRecvMsgSize),
		grpc.StatsHandler(&linkWatch{loops: svc.loops, log: log}))
	guestv1.RegisterGuestServiceServer(srv, svc)
	return srv
}

type service struct {
	guestv1.UnimplementedGuestServiceServer

	version   string
	log       *slog.Logger
	clock     Clock
	dataMount *DataMount
	users     *users.Manager
	loops     *procs.Loops
	emptier   users.Emptier
	openRoot  func() (guestusers.Root, error)
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
