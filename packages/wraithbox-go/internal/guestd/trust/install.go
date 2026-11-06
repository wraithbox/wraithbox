package trust

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	pkcs12 "software.sslmate.com/src/go-pkcs12"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/fileowner"
)

// The modes of the trust directory and its files (S09-08-guest-trust,
// "Files").
const (
	dirMode  fs.FileMode = 0o755
	fileMode fs.FileMode = 0o644
)

// Refusal is an install that didn't complete: the rule, and what was
// found, which only the guest's log holds.
type Refusal struct {
	Rule   string
	Detail string
}

func (r *Refusal) Error() string { return "trust: " + r.Rule + ": " + r.Detail }

// Installer writes the trust files. Its methods are safe for
// concurrent use: one install runs at a time.
type Installer struct {
	log *slog.Logger

	// dir, roots and owner are Dir, PublicRootsPath and root's user ID.
	// Tests in this package set others, so CI writes to a temporary
	// directory.
	dir   string
	roots string
	owner uint32

	mu sync.Mutex
}

// NewInstaller returns the Installer of the guest's trust directory,
// which wb-guestd runs as root.
func NewInstaller(log *slog.Logger) *Installer {
	return &Installer{log: log, dir: Dir, roots: PublicRootsPath, owner: 0}
}

// file is one trust file's name in the directory and its whole content.
type file struct {
	name string
	data []byte
}

// built is what one install writes: the three files in the order they
// are written, and how many public roots the bundle took and skipped.
type built struct {
	files   []file
	roots   int
	skipped int
}

// Install makes ders the guest's Wraith Box CAs, in place of the ones
// before (S09-08-guest-trust, "Guest trust"). It checks the request
// with CheckCAs, reads the public roots again, builds the three files
// in memory, checks the directory, and then writes each file to a
// temporary file in the directory, syncs it and renames it into place,
// so a client never reads half a file. A refused request or an unsafe
// directory writes nothing. A failure after some renames leaves those
// files in place, and the next install writes all three again.
//
// It logs one line with each CA's SHA-256 fingerprint, the rule and the
// decision.
func (in *Installer) Install(ctx context.Context, ders [][]byte) *Refusal {
	in.mu.Lock()
	defer in.mu.Unlock()
	attrs := []slog.Attr{slog.Int("cas", len(ders)), slog.Any("ca_sha256", fingerprints(ders))}
	b, ref := in.install(ders)
	if ref != nil {
		level := slog.LevelWarn
		if ref.Rule == RuleFailed {
			level = slog.LevelError
		}
		in.log.LogAttrs(ctx, level, "trust not installed", append(attrs,
			slog.String("rule", ref.Rule), slog.String("decision", "refuse"), slog.String("found", ref.Detail))...)
		return ref
	}
	in.log.LogAttrs(ctx, slog.LevelInfo, "trust installed", append(attrs,
		slog.String("rule", RuleInstalled), slog.String("decision", "accept"),
		slog.Int("public_roots", b.roots), slog.Int("public_roots_skipped", b.skipped),
		slog.String("dir", in.dir))...)
	return nil
}

// install runs one install and returns what it wrote.
func (in *Installer) install(ders [][]byte) (built, *Refusal) {
	cas, err := CheckCAs(ders)
	if err != nil {
		return built{}, &Refusal{Rule: RuleRefused, Detail: err.Error()}
	}
	b, err := in.build(cas)
	if err != nil {
		return built{}, &Refusal{Rule: RuleFailed, Detail: err.Error()}
	}
	root, ref := in.openDir()
	if ref != nil {
		return built{}, ref
	}
	defer func() { _ = root.Close() }()
	for _, f := range b.files {
		if err := writeFile(root, f.name, f.data); err != nil {
			return built{}, &Refusal{Rule: RuleFailed, Detail: fmt.Sprintf("%s: %v", f.name, err)}
		}
	}
	if err := syncDir(root); err != nil {
		return built{}, &Refusal{Rule: RuleFailed, Detail: fmt.Sprintf("sync %s: %v", in.dir, err)}
	}
	return b, nil
}

// build reads the public roots and builds the three files.
func (in *Installer) build(cas []*x509.Certificate) (built, error) {
	data, err := readRoots(in.roots)
	if err != nil {
		return built{}, err
	}
	roots, skipped, err := ParseRoots(data)
	if err != nil {
		return built{}, fmt.Errorf("public roots %s: %w", in.roots, err)
	}
	bundle := bundleOf(roots, cas)
	store, err := javaTrustStore(bundle)
	if err != nil {
		return built{}, err
	}
	return built{
		files: []file{
			{name: CAFile, data: encodePEM(cas)},
			{name: BundleFile, data: encodePEM(bundle)},
			{name: JavaTrustStoreFile, data: store},
		},
		roots:   len(roots),
		skipped: skipped,
	}, nil
}

// readRoots reads the public roots file, which must be a regular file
// of at most MaxRootsSize bytes.
func readRoots(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("public roots: %w", err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("public roots: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("public roots %s: not a regular file (%v)", path, info.Mode().Type())
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxRootsSize+1))
	if err != nil {
		return nil, fmt.Errorf("public roots %s: %w", path, err)
	}
	return data, nil
}

// javaTrustStore encodes certs as a PKCS12 trust store without a
// password, encryption or MAC (S09-08-guest-trust, "Files"). Each alias
// is the certificate's SHA-256 fingerprint, so no two are the same.
func javaTrustStore(certs []*x509.Certificate) ([]byte, error) {
	entries := make([]pkcs12.TrustStoreEntry, 0, len(certs))
	for _, c := range certs {
		entries = append(entries, pkcs12.TrustStoreEntry{Cert: c, FriendlyName: Fingerprint(c.Raw)})
	}
	data, err := pkcs12.Passwordless.EncodeTrustStoreEntries(entries, "")
	if err != nil {
		return nil, fmt.Errorf("java trust store: %w", err)
	}
	return data, nil
}

// openDir makes the trust directory when it is missing, checks it, and
// opens it. Every write goes through the returned os.Root, so no name
// in it is followed out of the directory.
func (in *Installer) openDir() (*os.Root, *Refusal) {
	if err := in.makeDir(); err != nil {
		return nil, &Refusal{Rule: RuleFailed, Detail: err.Error()}
	}
	info, err := os.Lstat(in.dir)
	if err != nil {
		return nil, &Refusal{Rule: RuleFailed, Detail: err.Error()}
	}
	if reason := in.unsafe(info); reason != "" {
		return nil, &Refusal{Rule: RuleDirUnsafe, Detail: in.dir + ": " + reason}
	}
	root, err := os.OpenRoot(in.dir)
	if err != nil {
		return nil, &Refusal{Rule: RuleFailed, Detail: err.Error()}
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		_ = root.Close()
		return nil, &Refusal{Rule: RuleDirUnsafe, Detail: in.dir + ": replaced while it was opened"}
	}
	return root, nil
}

// makeDir makes the trust directory with mode 0755 when nothing is at
// its path. Anything there already is left for the check.
func (in *Installer) makeDir() error {
	if _, err := os.Lstat(in.dir); !errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(in.dir), dirMode); err != nil {
		return err
	}
	if err := os.Mkdir(in.dir, dirMode); err != nil {
		return err
	}
	// Mkdir's mode is masked by the umask.
	return os.Chmod(in.dir, dirMode)
}

// unsafe returns why the trust directory isn't safe to write, or "":
// a symbolic link, not a directory, another mode than 0755 (setuid,
// setgid and sticky bits included), or another owner
// (S09-08-guest-trust, "Files").
func (in *Installer) unsafe(info fs.FileInfo) string {
	mode := info.Mode()
	switch {
	case mode&fs.ModeSymlink != 0:
		return "a symbolic link"
	case !mode.IsDir():
		return "not a directory"
	}
	if perm := mode & (fs.ModePerm | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky); perm != dirMode {
		return fmt.Sprintf("mode %v, want %v", perm, dirMode)
	}
	uid, err := fileowner.UID(info)
	if err != nil {
		return err.Error()
	}
	if uid != in.owner {
		return fmt.Sprintf("owned by user ID %d, want %d", uid, in.owner)
	}
	return ""
}

// writeFile writes data to a new temporary file in root with mode 0644,
// syncs it, and renames it to name. On an error it removes the
// temporary file.
func writeFile(root *os.Root, name string, data []byte) (err error) {
	tmp := "." + name + ".tmp-" + rand.Text()
	// O_EXCL: the name is new, and a link put there isn't followed.
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = root.Remove(tmp)
		}
	}()
	_, err = f.Write(data)
	if err == nil {
		// OpenFile's mode is masked by the umask.
		err = f.Chmod(fileMode)
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return root.Rename(tmp, name)
}

// syncDir syncs the directory, so the renames are on disk.
func syncDir(root *os.Root) error {
	d, err := root.Open(".")
	if err != nil {
		return err
	}
	err = d.Sync()
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	return err
}

// fingerprints returns the SHA-256 fingerprint of each of the first
// MaxCAs certificates of a request, for the log.
func fingerprints(ders [][]byte) []string {
	out := make([]string, 0, min(len(ders), MaxCAs))
	for _, der := range ders[:min(len(ders), MaxCAs)] {
		out = append(out, Fingerprint(der))
	}
	return out
}
