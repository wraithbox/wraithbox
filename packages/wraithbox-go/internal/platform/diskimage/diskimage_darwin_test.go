package diskimage_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/diskfmt"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/diskimage"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/plist"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmbundle"
)

// image is an image created under the temporary name in a new
// directory.
type image struct {
	files           diskimage.Files
	dir, tmp, final string
	h               diskfmt.Handle
}

func created(t *testing.T) image {
	t.Helper()
	dir := t.TempDir()
	files, err := diskimage.NewFiles()
	if err != nil {
		t.Fatal(err)
	}
	img := image{files: files, dir: dir, tmp: filepath.Join(dir, vmbundle.DataDiskTempName), final: filepath.Join(dir, vmbundle.DataDiskName)}
	img.h, err = files.Create(img.tmp, 8<<30)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = img.h.Close() })
	return img
}

// TestCreateSparse: the image is created sparse, mode 0600 and
// exclusively, and the identity check sees one link and no block.
func TestCreateSparse(t *testing.T) {
	img := created(t)
	files, dir, tmp := img.files, img.dir, img.tmp
	kept, err := img.h.Stat()
	if err != nil {
		t.Fatal(err)
	}
	named, err := files.Lstat(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if kept != named || named.Nlink != 1 || named.Blocks != 0 || !named.Regular {
		t.Errorf("kept %s, named %s: want the same sparse file with one link", kept, named)
	}
	if info, err := os.Lstat(tmp); err != nil || info.Size() != 8<<30 || info.Mode().Perm() != 0o600 {
		t.Errorf("lstat %v, %v: want %d bytes and mode 0600", info, err, int64(8<<30))
	}
	if n, err := diskimage.Allocated(tmp); err != nil || n != 0 {
		t.Errorf("Allocated = %d, %v, want 0 for a sparse file", n, err)
	}
	if _, err := diskimage.Allocated(dir); err == nil {
		t.Error("Allocated of a directory")
	}
	if _, err := files.Create(tmp, 1); !errors.Is(err, fs.ErrExist) {
		t.Errorf("a second Create: err = %v, want fs.ErrExist", err)
	}
}

// TestRenameExcl: the rename refuses an existing final name and keeps
// it, and renames once the name is free.
func TestRenameExcl(t *testing.T) {
	img := created(t)
	files, tmp, final := img.files, img.tmp, img.final
	if err := os.WriteFile(final, []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := files.RenameExcl(tmp, final); !errors.Is(err, fs.ErrExist) {
		t.Errorf("RenameExcl over a file: err = %v, want fs.ErrExist", err)
	}
	if b, _ := os.ReadFile(final); string(b) != "kept" {
		t.Errorf("the file at the final name is %q, want it kept", b)
	}
	if err := os.Remove(final); err != nil {
		t.Fatal(err)
	}
	if err := files.RenameExcl(tmp, final); err != nil {
		t.Fatal(err)
	}
	if _, err := files.Lstat(tmp); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Lstat of the old name: err = %v, want fs.ErrNotExist", err)
	}
}

// TestCreateRefusesLink: a symbolic link at the temporary name isn't
// followed, and its target isn't created.
func TestCreateRefusesLink(t *testing.T) {
	dir := t.TempDir()
	files, _ := diskimage.NewFiles()
	target := filepath.Join(dir, "target")
	tmp := filepath.Join(dir, vmbundle.DataDiskTempName)
	if err := os.Symlink(target, tmp); err != nil {
		t.Fatal(err)
	}
	if h, err := files.Create(tmp, 1<<30); err == nil {
		_ = h.Close()
		t.Fatal("Create followed a symbolic link")
	}
	if _, err := os.Lstat(target); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the link's target exists: %v", err)
	}
	ident, err := files.Lstat(tmp)
	if err != nil || ident.Regular {
		t.Errorf("Lstat of a link = %s, %v, want a link", ident, err)
	}
}

func TestUnlink(t *testing.T) {
	dir := t.TempDir()
	files, _ := diskimage.NewFiles()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := files.Unlink(sub); err == nil {
		t.Error("Unlink removed a directory")
	}
	if err := files.Unlink(filepath.Join(dir, "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Unlink of a missing name: err = %v, want fs.ErrNotExist", err)
	}
}

// TestRegistryNoBacking reads the I/O Registry for the host's own disk,
// which no image backs, and for a disk that doesn't exist. It attaches
// nothing.
func TestRegistryNoBacking(t *testing.T) {
	tool, err := diskimage.NewTool(nil)
	if err != nil {
		t.Fatal(err)
	}
	p, err := tool.BackingPath(t.Context(), "disk0")
	t.Logf("disk0: %v", err)
	if err == nil {
		t.Errorf("BackingPath(disk0) = %q, want an error", p)
	}
	if p, err := tool.BackingPath(t.Context(), "disk9876"); err == nil {
		t.Errorf("BackingPath(disk9876) = %q, want an error", p)
	}
	if p, err := tool.BackingPath(t.Context(), "disk0s1"); err == nil {
		t.Errorf("BackingPath(disk0s1) = %q, want an error", p)
	}
}

// TestNamesNotClaimed reads DiskImageMounter's Info.plist and checks
// that neither data disk name ends in an extension it claims, so a
// double click doesn't attach either (S06-05-disks, "Names on the
// host"; SEC15-no-guest-disk-mount).
func TestNamesNotClaimed(t *testing.T) {
	const info = "/System/Library/CoreServices/DiskImageMounter.app/Contents/Info.plist"
	data, err := os.ReadFile(info)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := claimedExtensions(data)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(claimed, "dmg") || !slices.Contains(claimed, "img") {
		t.Fatalf("read %v from %s, which misses dmg or img: the format changed", claimed, info)
	}
	for _, name := range []string{vmbundle.DataDiskName, vmbundle.DataDiskTempName} {
		for _, ext := range claimed {
			if strings.HasSuffix(strings.ToLower(name), "."+strings.ToLower(ext)) {
				t.Errorf("%s ends in .%s, which DiskImageMounter claims", name, ext)
			}
		}
	}
}

// claimedExtensions returns every extension in CFBundleDocumentTypes'
// CFBundleTypeExtensions, and in the public.filename-extension tags of
// the exported and imported type declarations.
func claimedExtensions(data []byte) ([]string, error) {
	v, err := plist.Decode(data)
	if err != nil {
		return nil, err
	}
	root, _ := v.(map[string]any)
	var out []string
	docs, _ := root["CFBundleDocumentTypes"].([]any)
	for _, d := range docs {
		doc, _ := d.(map[string]any)
		out = append(out, stringList(doc["CFBundleTypeExtensions"])...)
	}
	for _, key := range []string{"UTExportedTypeDeclarations", "UTImportedTypeDeclarations"} {
		decls, _ := root[key].([]any)
		for _, d := range decls {
			decl, _ := d.(map[string]any)
			tags, _ := decl["UTTypeTagSpecification"].(map[string]any)
			out = append(out, stringList(tags["public.filename-extension"])...)
		}
	}
	return out, nil
}

// stringList reads a string or an array of strings.
func stringList(v any) []string {
	switch s := v.(type) {
	case string:
		return []string{s}
	case []any:
		var out []string
		for _, e := range s {
			if str, ok := e.(string); ok {
				out = append(out, str)
			}
		}
		return out
	}
	return nil
}
