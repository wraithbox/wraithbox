package projectcmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"google.golang.org/grpc"

	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostversion/grpccreds"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/localipc"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/paths"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/version"
)

// SystemEnv returns wb's Env: the paths from the user database, never
// HOME (S12-01-platform-interfaces, "Paths"), and the client of
// wb-hostd's local IPC endpoint, which checks the peer and the build
// version on the connection (S04-architecture, "Inter-process
// contracts").
func SystemEnv(log *slog.Logger) Env {
	p, err := paths.Current(log)
	return Env{
		Stdout: os.Stdout, Stderr: os.Stderr,
		Paths:       p,
		Unavailable: err,
		Dial: func(context.Context) (Hostd, error) {
			return dial(log, p.Run)
		},
		Log: log,
	}
}

// grpcHostd is the Hostd of a gRPC connection to wb-hostd.
type grpcHostd struct {
	hostdv1.ProjectServiceClient
	conn *grpc.ClientConn
}

func (h *grpcHostd) Close() error { return h.conn.Close() }

func dial(log *slog.Logger, run string) (Hostd, error) {
	creds := grpccreds.New(log, version.Build())
	opts := append(grpccreds.ClientOptions(log, creds, run, localipc.HostdEndpoint),
		grpc.WithChainUnaryInterceptor(versionChecked(creds)), grpc.WithDefaultCallOptions(grpc.WaitForReady(false)))
	conn, err := grpc.NewClient("passthrough:///"+localipc.HostdEndpoint, opts...)
	if err != nil {
		return nil, err
	}
	return &grpcHostd{ProjectServiceClient: hostdv1.NewProjectServiceClient(conn), conn: conn}, nil
}

// versionChecked returns a call's error, or in its place the
// connection's build version refusal (S04-08-records-and-bounds,
// "Between host processes"). A refused connection never carried the
// request.
func versionChecked(creds *grpccreds.Credentials) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		err := invoker(ctx, method, req, reply, cc, opts...)
		if err == nil {
			return nil
		}
		if r := creds.LastRefusal(); r != nil {
			return &versionError{
				rule: r.Rule,
				msg:  fmt.Sprintf("wb-hostd is version %s and wb is version %s, so nothing changed: install Wraith Box again", r.PeerLabel(), r.OwnLabel()),
			}
		}
		return err
	}
}
