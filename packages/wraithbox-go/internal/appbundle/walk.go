package appbundle

import (
	"debug/buildinfo"
	"debug/macho"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/version"
)

// Files are the files Walk finds under a root, by path relative to it,
// with slashes.
type Files struct {
	MachO    []string
	Profiles []string
	Links    []string
}

// Walk finds every Mach-O file, provisioning profile and symbolic link
// under root. It knows a Mach-O file by its magic number, not its name
// or place, so a program left out of the layout table can't slip past
// the check. It doesn't follow links. Any other file type, or a file it
// can't read, is an error: the check fails closed.
func Walk(root string) (Files, error) {
	var found Files
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		switch mode := d.Type(); {
		case mode&fs.ModeSymlink != 0:
			found.Links = append(found.Links, rel)
			return nil
		case mode.IsDir():
			return nil
		case !mode.IsRegular():
			return fmt.Errorf("%s is neither a file, a directory nor a link (%s)", rel, mode)
		}
		if d.Name() == ProfileName {
			found.Profiles = append(found.Profiles, rel)
		}
		isMachO, err := IsMachO(path)
		if isMachO {
			found.MachO = append(found.MachO, rel)
		}
		return err
	})
	return found, err
}

// The magic numbers of Mach-O files, thin and fat, in either byte order.
var machOMagic = []uint32{
	macho.Magic32, macho.Magic64, macho.MagicFat,
	0xcefaedfe, 0xcffaedfe, 0xbebafeca,
}

// IsMachO reports whether the file at path starts with a Mach-O magic
// number. A file shorter than one isn't a Mach-O file.
func IsMachO(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	var head [4]byte
	if _, err := io.ReadFull(f, head[:]); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return false, nil
		}
		return false, err
	}
	return slices.Contains(machOMagic, binary.BigEndian.Uint32(head[:])), nil
}

// The section the Swift build links the stamp into
// (WraithBoxVM/Version.swift).
const (
	stampSegment = "__TEXT"
	stampSection = "__wb_version"
)

// ReadStamp returns the build version linked into the program at path,
// or "" when it has none. A Swift program has it in its __wb_version
// section, as `mise run swift:build` links it. A Go program was linked
// with `-X <version.StampVar>=<version>`, which its build information
// records. A stamp outside the version form counts as none.
func ReadStamp(path string) (string, error) {
	f, err := macho.Open(path)
	if err != nil {
		return "", fmt.Errorf("%s is not a thin Mach-O file: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	if s := f.Section(stampSection); s != nil && s.Seg == stampSegment {
		data, err := s.Data()
		if err != nil {
			return "", err
		}
		return validOrNone(string(data)), nil
	}
	// A program without the section and without Go build information
	// has no stamp: that is the answer, and the check refuses it.
	if info, err := buildinfo.ReadFile(path); err == nil {
		return validOrNone(goStamp(info)), nil
	}
	return "", nil
}

// goStamp returns the value the build's -ldflags gave version.StampVar.
func goStamp(info *buildinfo.BuildInfo) string {
	for _, s := range info.Settings {
		if s.Key != "-ldflags" {
			continue
		}
		for field := range strings.FieldsSeq(s.Value) {
			if v, ok := strings.CutPrefix(field, version.StampVar+"="); ok {
				return v
			}
		}
	}
	return ""
}

func validOrNone(s string) string {
	if version.Valid(s) {
		return s
	}
	return ""
}
