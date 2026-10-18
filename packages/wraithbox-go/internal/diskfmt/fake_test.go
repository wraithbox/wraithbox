package diskfmt_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sync"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/diskfmt"
)

// inode is a file in the fake file system.
type inode struct {
	ino     uint64
	nlink   uint64
	blocks  int64
	regular bool
}

// fakeFiles is an in-memory file system: names map to inodes, so a test
// can replace a name or add a hard link as an attacker would.
type fakeFiles struct {
	mu      sync.Mutex
	next    uint64
	names   map[string]*inode
	renames []string
}

func newFakeFiles() *fakeFiles { return &fakeFiles{next: 100, names: map[string]*inode{}} }

func (f *fakeFiles) newInode(regular bool) *inode {
	f.next++
	return &inode{ino: f.next, nlink: 1, regular: regular}
}

// put makes a new file at name, as another process would.
func (f *fakeFiles) put(name string, regular bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if old := f.names[name]; old != nil {
		old.nlink--
	}
	f.names[name] = f.newInode(regular)
}

// link adds the hard link to the file at name.
func (f *fakeFiles) link(name, to string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := f.names[name]
	n.nlink++
	f.names[to] = n
}

func (f *fakeFiles) has(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.names[name] != nil
}

func (f *fakeFiles) ino(name string) uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	if n := f.names[name]; n != nil {
		return n.ino
	}
	return 0
}

func ident(n *inode) diskfmt.Identity {
	return diskfmt.Identity{Dev: 1, Ino: n.ino, Nlink: n.nlink, Blocks: n.blocks, Regular: n.regular}
}

func (f *fakeFiles) Create(path string, _ int64) (diskfmt.Handle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.names[path] != nil {
		return nil, fmt.Errorf("create %s: %w", path, fs.ErrExist)
	}
	n := f.newInode(true)
	f.names[path] = n
	return &fakeHandle{f: f, n: n}, nil
}

func (f *fakeFiles) Lstat(path string) (diskfmt.Identity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := f.names[path]
	if n == nil {
		return diskfmt.Identity{}, fmt.Errorf("lstat %s: %w", path, fs.ErrNotExist)
	}
	return ident(n), nil
}

func (f *fakeFiles) Unlink(path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := f.names[path]
	if n == nil {
		return fmt.Errorf("unlink %s: %w", path, fs.ErrNotExist)
	}
	if !n.regular {
		return fmt.Errorf("unlink %s: is a directory", path)
	}
	n.nlink--
	delete(f.names, path)
	return nil
}

func (f *fakeFiles) RenameExcl(from, to string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.names[to] != nil {
		return fmt.Errorf("rename %s: %w", to, fs.ErrExist)
	}
	n := f.names[from]
	if n == nil {
		return fmt.Errorf("rename %s: %w", from, fs.ErrNotExist)
	}
	f.names[to] = n
	delete(f.names, from)
	f.renames = append(f.renames, from+" -> "+to)
	return nil
}

type fakeHandle struct {
	f      *fakeFiles
	n      *inode
	closed bool
}

func (h *fakeHandle) Stat() (diskfmt.Identity, error) {
	h.f.mu.Lock()
	defer h.f.mu.Unlock()
	if h.closed {
		return diskfmt.Identity{}, errors.New("closed")
	}
	return ident(h.n), nil
}

func (h *fakeHandle) Close() error { h.closed = true; return nil }

// fakeTool is a diskutil that attaches the inode at a name in the fake
// file system. Its hooks run before each step, so a test can change the
// file system at that point.
type fakeTool struct {
	files *fakeFiles
	uuid  string

	beforeAttach func()
	afterAttach  func()
	beforeErase  func()
	// attachBacking and backing, when set, replace the backing path the
	// attach and BackingPath report. attachNoBacking makes the attach
	// report none, as diskutil on macOS 27 does, and attachAmbiguous one
	// that ParseAttach reads as "" but named, such as two that disagree. backingFrom is how many
	// BackingPath calls report the true path before backing replaces it.
	attachBacking   string
	attachNoBacking bool
	attachAmbiguous bool
	backing         string
	backingFrom     int
	backingCalls    int
	attachErr       error
	eraseErr        error
	// detachFails is how many detaches fail before one succeeds.
	detachFails int

	attached map[string]attachedDisk
	calls    []string
	paths    []string
	detached []string
	next     int
}

type attachedDisk struct {
	path string
	n    *inode
}

func newFakeTool(files *fakeFiles) *fakeTool {
	return &fakeTool{files: files, uuid: "0F1E2D3C-4B5A-6978-8796-A5B4C3D2E1F0", attached: map[string]attachedDisk{}}
}

func (t *fakeTool) Attach(_ context.Context, path string) (diskfmt.Attachment, error) {
	t.calls = append(t.calls, "attach")
	if t.beforeAttach != nil {
		t.beforeAttach()
	}
	t.paths = append(t.paths, path)
	if t.attachErr != nil {
		return diskfmt.Attachment{}, t.attachErr
	}
	t.files.mu.Lock()
	n := t.files.names[path]
	t.files.mu.Unlock()
	if n == nil {
		return diskfmt.Attachment{}, fmt.Errorf("attach %s: %w", path, fs.ErrNotExist)
	}
	t.next++
	dev := fmt.Sprintf("disk%d", 8+t.next)
	t.attached[dev] = attachedDisk{path: path, n: n}
	if t.afterAttach != nil {
		t.afterAttach()
	}
	att := diskfmt.Attachment{Device: dev, BackingPath: path, BackingNamed: true}
	switch {
	case t.attachNoBacking:
		att.BackingPath, att.BackingNamed = "", false
	case t.attachAmbiguous:
		att.BackingPath = ""
	case t.attachBacking != "":
		att.BackingPath = t.attachBacking
	}
	return att, nil
}

func (t *fakeTool) BackingPath(_ context.Context, device string) (string, error) {
	t.calls = append(t.calls, "backing")
	d, ok := t.attached[device]
	if !ok {
		return "", fmt.Errorf("%s isn't attached", device)
	}
	t.backingCalls++
	if t.backing != "" && t.backingCalls > t.backingFrom {
		return t.backing, nil
	}
	return d.path, nil
}

func (t *fakeTool) Erase(_ context.Context, device string) error {
	if t.beforeErase != nil {
		t.beforeErase()
	}
	t.calls = append(t.calls, "erase")
	if t.eraseErr != nil {
		return t.eraseErr
	}
	d, ok := t.attached[device]
	if !ok {
		return fmt.Errorf("%s isn't attached", device)
	}
	t.files.mu.Lock()
	d.n.blocks = 2048
	t.files.mu.Unlock()
	return nil
}

func (t *fakeTool) VolumeUUID(context.Context, string) (string, error) {
	t.calls = append(t.calls, "uuid")
	return t.uuid, nil
}

func (t *fakeTool) Detach(_ context.Context, device string) error {
	t.calls = append(t.calls, "detach")
	if t.detachFails > 0 {
		t.detachFails--
		return fmt.Errorf("%s is busy", device)
	}
	if _, ok := t.attached[device]; !ok {
		return fmt.Errorf("%s isn't attached", device)
	}
	delete(t.attached, device)
	t.detached = append(t.detached, device)
	return nil
}
