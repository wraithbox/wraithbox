package project

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/paths"
)

// The rules of the files wb keeps per project.
const (
	// RuleIDForm: a project id that isn't 32 lowercase hexadecimal
	// characters, so no path is built from it.
	RuleIDForm Rule = "project-id-form"
	// RuleProjectDir: the project directory can't be made, or what is
	// there isn't a directory of the user's own.
	RuleProjectDir Rule = "project-dir-unsafe"
	// RuleFilesLeft: wb project rm left some of the project's files.
	RuleFilesLeft Rule = "project-files-left"
)

// idLen is the length of a project id: 128 bits in hexadecimal
// (S05-04-naming, "Project id").
const idLen = 32

// CheckID checks a project id's format: 32 lowercase hexadecimal
// characters. wb builds a path under <data> or <config> from an id only
// after this check, also from an id wb-hostd returned
// (S04-06-git-confinement, "`wb` takes no path, URL or program from
// `wb-hostd`"; SEC12-least-privilege).
func CheckID(id string) error {
	if len(id) != idLen {
		return &Refusal{Rule: RuleIDForm, msg: fmt.Sprintf("the project id %q is not %d lowercase hexadecimal characters", id, idLen)}
	}
	for i := range len(id) {
		if c := id[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return &Refusal{Rule: RuleIDForm, msg: fmt.Sprintf("the project id %q is not %d lowercase hexadecimal characters", id, idLen)}
		}
	}
	return nil
}

// IsID reports whether s has a project id's format. `--project P`
// takes a P of that form as an id, and any other as a name.
func IsID(s string) bool { return CheckID(s) == nil }

// Root is the projects root, <data>/projects. wb setup creates it, and
// wb-hostd can't write it (S04-06-git-confinement, "Who creates what").
func Root(data string) string { return filepath.Join(data, "projects") }

// Dir returns the project directory <data>/projects/<id>, after it
// checks the id (S05-01-agent-commands, "First run").
func Dir(data, id string) (string, error) {
	if err := CheckID(id); err != nil {
		return "", err
	}
	return filepath.Join(Root(data), id), nil
}

// EnsureDir returns the project directory, and creates it with mode
// 0700 when it is missing: at registration, and at a later session
// start when it is gone (S05-01-agent-commands, "First run"). It accepts
// an existing entry only when it is a directory, not a symbolic link,
// owned by the user. So wb-hostd, which can't write the projects root,
// can't have wb use a directory of its choosing (SEC12-least-privilege).
func EnsureDir(data, id string) (string, error) {
	dir, err := Dir(data, id)
	if err != nil {
		return "", err
	}
	err = platform.CheckOwnDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		err = os.Mkdir(dir, 0o700)
		if errors.Is(err, fs.ErrNotExist) {
			return "", &Refusal{Rule: RuleProjectDir, err: err, msg: fmt.Sprintf("the projects root %q is missing: run wb setup", Root(data))}
		}
		if err == nil || errors.Is(err, fs.ErrExist) {
			err = platform.CheckOwnDir(dir)
		}
	}
	if err != nil {
		return "", &Refusal{Rule: RuleProjectDir, err: err, msg: fmt.Sprintf("the project directory %q can't be used: %v", dir, err)}
	}
	return dir, nil
}

// RemoveFiles removes what wb keeps for a removed project: the project
// directory <data>/projects/<id>, with export.git and landing.git in it,
// and the settings and policy files <config>/projects/<id>.toml and
// <config>/projects/<id>.policy.yaml (S05-04-naming, "Removing").
// wb-hostd can't write either directory, so wb removes them itself.
//
// os.RemoveAll removes a symbolic link without following it, and on
// Unix walks the tree with openat and unlinkat. So a link that wb-hostd
// planted in landing.git can't point the removal at a file outside the
// directory. A missing file is not an error. Any other failure is a
// refusal with RuleFilesLeft that names each path left.
func RemoveFiles(p paths.Paths, id string) error {
	dir, err := Dir(p.Data, id)
	if err != nil {
		return err
	}
	var left []error
	if err := os.RemoveAll(dir); err != nil {
		left = append(left, err)
	}
	for _, f := range []string{id + ".toml", id + ".policy.yaml"} {
		err := os.Remove(filepath.Join(p.Config, "projects", f))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			left = append(left, err)
		}
	}
	if len(left) == 0 {
		return nil
	}
	joined := errors.Join(left...)
	return &Refusal{Rule: RuleFilesLeft, err: joined, msg: fmt.Sprintf("some of project %s's files were left: %v", id, joined)}
}
