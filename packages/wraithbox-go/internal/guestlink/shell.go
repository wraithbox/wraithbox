package guestlink

import (
	"context"
	"log/slog"
	"time"

	"google.golang.org/grpc"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
)

// The bounds of the calls a debug shell makes (proto/wraithbox/guest/v1;
// S04-08-records-and-bounds, "Host work the guest can cause", row "RPCs
// from `wb-guestd`": values in code).
const (
	// UnlockMaxRecvMsgSize is the largest UnlockUser answer the host
	// decodes. The answer is empty.
	UnlockMaxRecvMsgSize = 1 << 10
	// UnlockTimeout is how long wb-guestd has to answer one UnlockUser.
	UnlockTimeout = 10 * time.Second
	// ShellMaxRecvMsgSize is the receive cap of a Shell stream: one
	// server frame of 32 KiB of data and its framing, with room to
	// spare. Set on the call, so gRPC never decodes more before the
	// frame checks of internal/hostd/shells (SEC13-bounded-resources).
	ShellMaxRecvMsgSize = 64 << 10
)

// UnlockUser asks wb-guestd to unlock project user uid for a session
// start (S13-01-process-hardening, "Locked users"). A failed call is a
// *UsersRefusal, logged with its rule: the session isn't started.
func (l *Link) UnlockUser(ctx context.Context, uid uint32, log *slog.Logger) error {
	callCtx, cancel := context.WithTimeout(ctx, UnlockTimeout)
	defer cancel()
	_, err := l.Client().UnlockUser(callCtx, &guestv1.UnlockUserRequest{Uid: uid}, grpc.MaxCallRecvMsgSize(UnlockMaxRecvMsgSize))
	attrs := []slog.Attr{slog.Uint64("uid", uint64(uid))}
	if err != nil {
		return logUsersRefusal(ctx, log, "guest user unlock refused", attrs, callRefusalOf(ctx, callCtx, err))
	}
	log.LogAttrs(ctx, slog.LevelInfo, "guest user unlocked", append(attrs, slog.String("decision", "unlock"))...)
	return nil
}

// Shell opens a GuestService.Shell stream, with ShellMaxRecvMsgSize set
// on the call. The stream ends with ctx.
func (l *Link) Shell(ctx context.Context) (guestv1.GuestService_ShellClient, error) {
	return l.Client().Shell(ctx, grpc.MaxCallRecvMsgSize(ShellMaxRecvMsgSize))
}
