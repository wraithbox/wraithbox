// Package volspace reads the free space of a volume and the space a
// file takes, and allocates a file whose blocks are really allocated.
// wb-hostd's audit writer keeps free space for the audit log with it
// (S09-11-audit, "Free space"): the free bytes and the identity of the
// volume that holds <logs>, the allocated bytes of a VM's data disk, and
// the audit log's reserve file.
//
// macOS and Linux read the volume with statfs(2) and stat(2), and a file
// with lstat(2). Allocate uses fcntl(F_PREALLOCATE) on macOS and
// fallocate(2) on Linux. Windows returns an error that wraps
// errors.ErrUnsupported for every call (S12-01-platform-interfaces,
// "Unimplemented platforms fail clearly").
package volspace

// Space is the free space of the volume that holds a path, and the
// volume's identity.
type Space struct {
	// Free is the bytes a process without privileges can still allocate
	// on the volume.
	Free uint64
	// Volume identifies the volume: two paths on one volume have the same
	// value, and paths on two volumes don't.
	Volume uint64
}

// Stat returns the space of the volume that holds path, which must
// exist. It follows a symbolic link at path.
func Stat(path string) (Space, error) { return stat(path) }

// Allocated returns the bytes the file at path takes on its volume,
// which are fewer than its size for a sparse file. It doesn't follow a
// symbolic link at path.
func Allocated(path string) (uint64, error) { return allocated(path) }

// Allocate creates the file path with mode 0600, which must not exist,
// and allocates size bytes to it, so that its blocks are allocated and
// not sparse. It doesn't follow a symbolic link at path. When the
// allocation fails, the file is removed and the error returned.
func Allocate(path string, size int64) error { return allocate(path, size) }
