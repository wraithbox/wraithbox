package vmcmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"google.golang.org/grpc"

	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostversion/grpccreds"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/diskimage"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/localipc"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/paths"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/tty"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/version"
)

// SystemEnv returns wb's Env: the paths from the user database, never
// HOME (S12-01-platform-interfaces, "Paths"), the controlling terminal,
// and the client of wb-hostd's local IPC endpoint, which checks the
// peer and the build version on the connection (S04-architecture,
// "Inter-process contracts").
func SystemEnv(log *slog.Logger) Env {
	p, err := paths.Current(log)
	return Env{
		Stdout: os.Stdout, Stderr: os.Stderr,
		OpenTerminal: tty.Open,
		DataDir:      p.Data,
		Allocated:    diskimage.Allocated,
		Dial: func(context.Context) (Hostd, error) {
			return dial(log, p.Run)
		},
		Unavailable: err,
		Log:         log,
	}
}

// grpcHostd is the Hostd of a gRPC connection to wb-hostd.
type grpcHostd struct {
	conn   *grpc.ClientConn
	client hostdv1.HostServiceClient
	creds  *grpccreds.Credentials
}

func dial(log *slog.Logger, run string) (Hostd, error) {
	creds := grpccreds.New(log, version.Build())
	conn, err := grpc.NewClient("passthrough:///"+localipc.HostdEndpoint,
		grpccreds.ClientOptions(log, creds, run, localipc.HostdEndpoint)...)
	if err != nil {
		return nil, err
	}
	return &grpcHostd{conn: conn, client: hostdv1.NewHostServiceClient(conn), creds: creds}, nil
}

// ResetVMData calls the RPC. A build version refusal of the connection
// becomes an error that names both versions.
func (h *grpcHostd) ResetVMData(ctx context.Context, vm hostdv1.VM) (*hostdv1.ResetVMDataResponse, error) {
	res, err := h.client.ResetVMData(ctx, &hostdv1.ResetVMDataRequest{Vm: vm}, grpc.WaitForReady(false))
	if err == nil {
		return res, nil
	}
	if r := h.creds.LastRefusal(); r != nil {
		return nil, &versionError{
			rule: r.Rule,
			msg:  fmt.Sprintf("wb-hostd is version %s and wb is version %s, so nothing changed: install Wraith Box again", r.PeerLabel(), r.OwnLabel()),
		}
	}
	return nil, err
}

func (h *grpcHostd) Close() error {
	if h.conn == nil {
		return errors.New("vmcmd: no connection")
	}
	return h.conn.Close()
}
