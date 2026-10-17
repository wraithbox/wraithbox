package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"syscall"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/diskfmt"
	diskfmtv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/diskfmt/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostversion"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/fdchan"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmbundle"
)

// maxRecord is the largest message of the exchange.
const maxRecord = diskfmt.MaxRecord

// How long wb-diskfmt waits for each request. wb-hostd sends the size
// at once, and the rename once it has recorded the volume UUID. A
// wb-hostd that sends neither in time leaves a temporary file, which
// the next format deletes.
const (
	formatWait = 30 * time.Second
	renameWait = 2 * time.Minute
)

// ruleMessage: a message from wb-hostd that isn't the request the step
// expects, can't be read, or carries a descriptor.
const ruleMessage = "diskfmt-message-invalid"

// channel is the exchange socket: *fdchan.Channel, or a test's fake.
type channel interface {
	Send(record []byte, fd syscall.Conn) error
	Recv() (*fdchan.Message, error)
	SetReadDeadline(t time.Time) error
}

// versions sends this build's version record, the first message on
// the channel, and checks wb-hostd's, which must be the same build
// (S04-08-records-and-bounds, "Between host processes"). A refusal is
// logged with its rule and both versions, and the run ends without
// another message.
func versions(log *slog.Logger, ch channel, own string) bool {
	if err := ch.Send(hostversion.Record(own), nil); err != nil {
		log.LogAttrs(context.Background(), slog.LevelError, "version record not sent", slog.String("error", err.Error()))
		return false
	}
	if err := ch.SetReadDeadline(time.Now().Add(hostversion.Timeout)); err != nil {
		return false
	}
	m, err := ch.Recv()
	if err != nil {
		log.LogAttrs(context.Background(), slog.LevelError, "no version record from wb-hostd",
			slog.String("rule", ruleMessage), slog.String("decision", "refuse"), slog.String("error", err.Error()))
		return false
	}
	if m.File != nil {
		_ = m.Close()
		log.LogAttrs(context.Background(), slog.LevelError, "wb-hostd's version record carried a descriptor",
			slog.String("rule", ruleMessage), slog.String("decision", "refuse"))
		return false
	}
	_, err = hostversion.CheckRecord(log, own, m.Record)
	return err == nil
}

// serve runs the exchange of proto/wraithbox/diskfmt/v1: the size, the
// format, the volume UUID, the rename. Every message from wb-hostd is
// read as untrusted: one that isn't the expected request ends the run.
func serve(ctx context.Context, log *slog.Logger, ch channel, f *diskfmt.Formatter) int {
	req, r := receive(ch, formatWait)
	if r != nil {
		return refuse(ctx, log, ch, r)
	}
	format := req.GetFormat()
	if format == nil {
		return refuse(ctx, log, ch, &diskfmt.Refusal{Rule: ruleMessage, Detail: "the first request isn't a format request"})
	}
	uuid, r := f.Format(ctx, format.GetSizeGib())
	if r != nil {
		return refuse(ctx, log, ch, r)
	}
	if err := send(ch, &diskfmtv1.ShimMessage{Kind: &diskfmtv1.ShimMessage_Formatted{Formatted: &diskfmtv1.FormatResult{
		VolumeUuid: uuid, TemporaryName: vmbundle.DataDiskTempName,
	}}}); err != nil {
		log.LogAttrs(ctx, slog.LevelError, "format result not sent", slog.String("error", err.Error()))
		return 1
	}
	req, r = receive(ch, renameWait)
	if r != nil {
		return refuse(ctx, log, ch, r)
	}
	if req.GetRename() == nil {
		return refuse(ctx, log, ch, &diskfmt.Refusal{Rule: ruleMessage, Detail: "the second request isn't a rename request"})
	}
	if r := f.Rename(ctx); r != nil {
		return refuse(ctx, log, ch, r)
	}
	if err := send(ch, &diskfmtv1.ShimMessage{Kind: &diskfmtv1.ShimMessage_Renamed{Renamed: &diskfmtv1.RenameResult{
		FinalName: vmbundle.DataDiskName,
	}}}); err != nil {
		log.LogAttrs(ctx, slog.LevelError, "rename result not sent", slog.String("error", err.Error()))
		return 1
	}
	return 0
}

// receive reads one HostMessage within wait.
func receive(ch channel, wait time.Duration) (*diskfmtv1.HostMessage, *diskfmt.Refusal) {
	if err := ch.SetReadDeadline(time.Now().Add(wait)); err != nil {
		return nil, &diskfmt.Refusal{Rule: ruleMessage, Detail: "the read deadline can't be set: " + err.Error()}
	}
	m, err := ch.Recv()
	if err != nil {
		return nil, &diskfmt.Refusal{Rule: ruleMessage, Detail: "no request from wb-hostd: " + err.Error()}
	}
	if m.File != nil {
		_ = m.Close()
		return nil, &diskfmt.Refusal{Rule: ruleMessage, Detail: "a request carried a descriptor"}
	}
	var msg diskfmtv1.HostMessage
	if err := proto.Unmarshal(m.Record, &msg); err != nil {
		return nil, &diskfmt.Refusal{Rule: ruleMessage, Detail: "a request can't be read: " + err.Error()}
	}
	return &msg, nil
}

func send(ch channel, m *diskfmtv1.ShimMessage) error {
	b, err := proto.Marshal(m)
	if err != nil {
		return err
	}
	if len(b) > maxRecord {
		return fmt.Errorf("a message of %d bytes is over %d", len(b), maxRecord)
	}
	return ch.Send(b, nil)
}

// refuse logs r, sends it to wb-hostd with its detail cut to
// MaxDetail, and returns the exit status 1. A refusal from the format
// was logged with its rule where it was made.
func refuse(ctx context.Context, log *slog.Logger, ch channel, r *diskfmt.Refusal) int {
	if r.Rule == ruleMessage {
		log.LogAttrs(ctx, slog.LevelError, "wb-diskfmt request refused",
			slog.String("rule", string(r.Rule)), slog.String("decision", "refuse"), slog.String("reason", r.Detail))
	}
	err := send(ch, &diskfmtv1.ShimMessage{Kind: &diskfmtv1.ShimMessage_Refusal{Refusal: &diskfmtv1.Refusal{
		Rule: string(r.Rule), Detail: bound(r.Detail),
	}}})
	if err != nil && !errors.Is(err, syscall.EPIPE) {
		log.LogAttrs(ctx, slog.LevelError, "refusal not sent", slog.String("rule", string(r.Rule)), slog.String("error", err.Error()))
	}
	return 1
}

func bound(s string) string {
	if len(s) <= diskfmt.MaxDetail {
		return s
	}
	return strings.ToValidUTF8(s[:diskfmt.MaxDetail], "") + " [cut]"
}
