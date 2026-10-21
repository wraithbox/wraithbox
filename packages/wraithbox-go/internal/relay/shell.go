package relay

import (
	"errors"
	"fmt"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guesttext"

	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
	terminalv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/terminal/v1"
)

// Bounds of ShellOpened (proto/wraithbox/hostd/v1/shell.proto).
const (
	maxShellID     = 128
	maxProjectName = 128
	maxGuestUser   = 64
)

// ErrShellOpened is wrapped by OpenShell's error when the first
// response isn't a ShellOpened within its bounds.
var ErrShellOpened = errors.New("relay: wb-hostd's first answer isn't a shell within bounds")

// errNotFrame is wrapped by a hostd stream's Recv when a later response
// isn't a server frame.
var errNotFrame = errors.New("a response that isn't a server frame")

// Shell is the debug shell wb-hostd opened, with each field stripped of
// the escaped set (S16-03-around-the-stream, "Guest text elsewhere").
type Shell struct {
	ID, Project, User string
}

// Label is the line wb prints on standard error before the relay
// starts (S05-03-management-commands, "`wb shell`").
func (s Shell) Label() string {
	return fmt.Sprintf("wb: debug shell %s in project %s, as guest user %s. A debug shell returns no work.", s.ID, s.Project, s.User)
}

// CheckOpened checks a ShellOpened against its bounds, and strips each
// field. The fields name things in the guest, so they may hold guest
// text.
func CheckOpened(o *hostdv1.ShellOpened) (Shell, error) {
	fields := []struct {
		name, value string
		limit       int
	}{
		{"shell id", o.GetShellId(), maxShellID},
		{"project name", o.GetProjectName(), maxProjectName},
		{"guest user", o.GetGuestUser(), maxGuestUser},
	}
	for _, f := range fields {
		if f.value == "" || len(f.value) > f.limit {
			return Shell{}, fmt.Errorf("%w: a %s of %d bytes, want 1 to %d", ErrShellOpened, f.name, len(f.value), f.limit)
		}
	}
	s := Shell{ID: guesttext.Strip(o.GetShellId()), Project: guesttext.Strip(o.GetProjectName()), User: guesttext.Strip(o.GetGuestUser())}
	if s.ID == "" || s.Project == "" || s.User == "" {
		return Shell{}, fmt.Errorf("%w: a field with nothing printable", ErrShellOpened)
	}
	return s, nil
}

// ShellClient is wb's end of a ShellService.Shell call
// (hostdv1.ShellService_ShellClient).
type ShellClient interface {
	Send(*hostdv1.ShellRequest) error
	Recv() (*hostdv1.ShellResponse, error)
	CloseSend() error
}

// OpenShell sends the ShellOpen and reads the ShellOpened, and returns
// the terminal stream that follows.
func OpenShell(c ShellClient, projectID string, setup *terminalv1.Setup) (Stream, Shell, error) {
	open := &hostdv1.ShellRequest{Msg: &hostdv1.ShellRequest_Open{Open: &hostdv1.ShellOpen{ProjectId: projectID, Setup: setup}}}
	if err := c.Send(open); err != nil {
		return nil, Shell{}, fmt.Errorf("relay: open the shell: %w", err)
	}
	resp, err := c.Recv()
	if err != nil {
		return nil, Shell{}, fmt.Errorf("relay: open the shell: %w", err)
	}
	opened := resp.GetOpened()
	if opened == nil {
		return nil, Shell{}, fmt.Errorf("%w: no ShellOpened", ErrShellOpened)
	}
	s, err := CheckOpened(opened)
	if err != nil {
		return nil, Shell{}, err
	}
	return hostdStream{c}, s, nil
}

// hostdStream is the terminal stream of a ShellService.Shell call.
type hostdStream struct {
	c ShellClient
}

func (h hostdStream) Send(f *terminalv1.ClientFrame) error {
	return h.c.Send(&hostdv1.ShellRequest{Msg: &hostdv1.ShellRequest_Frame{Frame: f}})
}

// Recv returns the next server frame. A response of another kind is an
// error, which ends the relay with 255.
func (h hostdStream) Recv() (*terminalv1.ServerFrame, error) {
	resp, err := h.c.Recv()
	if err != nil {
		return nil, err
	}
	f := resp.GetFrame()
	if f == nil {
		return nil, errNotFrame
	}
	return f, nil
}

func (h hostdStream) CloseSend() error { return h.c.CloseSend() }
