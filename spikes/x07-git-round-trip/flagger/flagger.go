// Package flagger is the X07 spike's risky-path flagger: given the
// changes between the session's base and its branch tip, it lists the
// changed paths the host or CI may execute, or that change how git
// behaves (S08-workspace-and-git, "Flagging risky changes").
//
// Throwaway code. Not held to the project gates.
package flagger

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"reflect"
	"strings"
)

// Change is one entry of `git diff-tree -r -z --raw --no-renames`.
type Change struct {
	OldMode, NewMode string
	OldOID, NewOID   string
	Status           byte
	Path             string
}

// ParseRaw parses NUL-separated raw diff output. The paths come from the
// guest, so this is a parser of guest-controlled bytes (fuzz target in
// flagger_test.go).
func ParseRaw(b []byte) ([]Change, error) {
	var out []Change
	for len(b) > 0 {
		if b[0] != ':' {
			return nil, errors.New("raw: entry does not start with ':'")
		}
		i := bytes.IndexByte(b, 0)
		if i < 0 {
			return nil, errors.New("raw: unterminated header")
		}
		f := strings.Split(string(b[1:i]), " ")
		b = b[i+1:]
		if len(f) != 5 || len(f[4]) < 1 {
			return nil, fmt.Errorf("raw: header has %d fields", len(f))
		}
		j := bytes.IndexByte(b, 0)
		if j < 0 {
			return nil, errors.New("raw: unterminated path")
		}
		p := string(b[:j])
		b = b[j+1:]
		st := f[4][0]
		if st == 'R' || st == 'C' {
			return nil, errors.New("raw: renames not expected, run with --no-renames")
		}
		out = append(out, Change{OldMode: f[0], NewMode: f[1], OldOID: f[2], NewOID: f[3], Status: st, Path: p})
	}
	return out, nil
}

// Finding is one flagged path and the rule that flagged it.
type Finding struct {
	Path, Rule, Detail string
}

// Reader returns a blob's content by object id.
type Reader func(oid string) ([]byte, error)

type rule struct {
	name string
	// match on the full path, the base name, or a directory prefix.
	exact, base, dir []string
	suffix           []string
}

var rules = []rule{
	{name: "hooks", dir: []string{".husky/", ".githooks/", ".git-hooks/", ".hooks/"},
		base: []string{"lefthook.yml", ".lefthook.yml", "lefthook.yaml", ".pre-commit-config.yaml", ".pre-commit-hooks.yaml", ".overcommit.yml"}},
	{name: "shell-env", base: []string{".envrc", ".env", ".env.local", ".profile", ".bashrc", ".zshrc"}},
	{name: "toolchain-pin", base: []string{".tool-versions", ".mise.toml", "mise.toml", ".nvmrc", ".node-version",
		".python-version", ".ruby-version", ".go-version", "rust-toolchain", "rust-toolchain.toml", ".sdkmanrc", ".bun-version"},
		dir: []string{".mise/", ".config/mise/"}},
	{name: "build-entry", base: []string{"Makefile", "makefile", "GNUmakefile", "Justfile", "justfile", "Taskfile.yml",
		"Taskfile.yaml", "Rakefile", "build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts",
		"CMakeLists.txt", "meson.build", "setup.py", "setup.cfg", "pyproject.toml", "noxfile.py", "tox.ini",
		"conftest.py", "build.rs", "Package.swift", "Cargo.toml", "BUILD", "BUILD.bazel", "WORKSPACE",
		"MODULE.bazel", "Dockerfile", "docker-compose.yml", "compose.yaml", "Vagrantfile", "Brewfile",
		"make.bash", "make.bat", "make.rc", "all.bash", "run.bash"},
		suffix: []string{".mk"}},
	{name: "agent-editor-config", dir: []string{".vscode/", ".idea/", ".claude/", ".cursor/", ".devcontainer/", ".zed/", ".gemini/", ".codex/"},
		base: []string{"AGENTS.md", "CLAUDE.md", ".mcp.json", "GEMINI.md", ".cursorrules", "copilot-instructions.md"}},
	{name: "ci", dir: []string{".github/workflows/", ".github/actions/", ".circleci/", ".buildkite/", ".gitlab/", ".azure-pipelines/", ".woodpecker/"},
		base: []string{".gitlab-ci.yml", "Jenkinsfile", "azure-pipelines.yml", ".travis.yml", "codefresh.yml", "bitbucket-pipelines.yml", "cloudbuild.yaml"},
		exact: []string{".github/dependabot.yml", ".github/CODEOWNERS", "codereview.cfg"}},
	{name: "git-behavior", base: []string{".gitattributes", ".gitmodules", ".lfsconfig"}},
	{name: "lockfile", base: []string{"package-lock.json", "npm-shrinkwrap.json", "yarn.lock", "pnpm-lock.yaml",
		"bun.lock", "bun.lockb", "go.sum", "Cargo.lock", "Gemfile.lock", "poetry.lock", "uv.lock", "Pipfile.lock",
		"Package.resolved", "composer.lock", "mise.lock", "flake.lock", "pubspec.lock", "mix.lock"}},
}

func matchRule(p string) []string {
	base := path.Base(p)
	var hit []string
	for _, r := range rules {
		ok := false
		for _, e := range r.exact {
			ok = ok || p == e
		}
		for _, e := range r.base {
			ok = ok || base == e
		}
		for _, d := range r.dir {
			ok = ok || strings.HasPrefix(p, d) || strings.Contains(p, "/"+d)
		}
		for _, s := range r.suffix {
			ok = ok || strings.HasSuffix(base, s)
		}
		if ok {
			hit = append(hit, r.name)
		}
	}
	return hit
}

const zero = "0000000000000000000000000000000000000000"

// Flag returns the findings for a list of changes. read may be nil, in
// which case content checks (symlink targets, package.json scripts,
// go.mod toolchain lines) are skipped.
func Flag(changes []Change, read Reader) []Finding {
	var out []Finding
	add := func(c Change, r, d string) { out = append(out, Finding{Path: c.Path, Rule: r, Detail: d}) }
	for _, c := range changes {
		for _, r := range matchRule(c.Path) {
			add(c, r, statusWord(c.Status))
		}
		// Git modes: 100755 executable, 120000 symlink, 160000 gitlink.
		if c.NewMode == "100755" && c.OldMode != "100755" {
			add(c, "executable", "mode "+c.OldMode+" -> 100755")
		}
		if c.NewMode == "160000" {
			add(c, "submodule", "gitlink "+c.NewOID[:12])
		}
		if c.NewMode == "120000" && read != nil {
			t, err := read(c.NewOID)
			switch {
			case err != nil:
				add(c, "symlink", "unreadable target")
			case outside(c.Path, string(t)):
				add(c, "symlink-outside", "-> "+string(t))
			}
		}
		if read == nil || c.Status == 'D' {
			continue
		}
		switch path.Base(c.Path) {
		case "package.json":
			if d := scriptsChanged(c, read); d != "" {
				add(c, "package-scripts", d)
			}
		case "go.mod":
			if d := goToolchainChanged(c, read); d != "" {
				add(c, "toolchain-pin", d)
			}
		}
	}
	return out
}

func statusWord(s byte) string {
	switch s {
	case 'A':
		return "added"
	case 'D':
		return "deleted"
	case 'T':
		return "type changed"
	}
	return "modified"
}

func outside(p, target string) bool {
	if strings.HasPrefix(target, "/") || strings.HasPrefix(target, "~") {
		return true
	}
	j := path.Clean(path.Join(path.Dir(p), target))
	return j == ".." || strings.HasPrefix(j, "../")
}

func blob(read Reader, oid string) []byte {
	if oid == zero || strings.Trim(oid, "0") == "" {
		return nil
	}
	b, _ := read(oid)
	return b
}

func scriptsChanged(c Change, read Reader) string {
	type pkg struct {
		Scripts map[string]string `json:"scripts"`
		Bin     any               `json:"bin"`
	}
	var a, b pkg
	_ = json.Unmarshal(blob(read, c.OldOID), &a)
	if err := json.Unmarshal(blob(read, c.NewOID), &b); err != nil {
		return "unparseable package.json"
	}
	var ch []string
	for k, v := range b.Scripts {
		if a.Scripts[k] != v {
			ch = append(ch, k)
		}
	}
	for k := range a.Scripts {
		if _, ok := b.Scripts[k]; !ok {
			ch = append(ch, k+" (removed)")
		}
	}
	if !reflect.DeepEqual(a.Bin, b.Bin) {
		ch = append(ch, "bin")
	}
	if len(ch) == 0 {
		return ""
	}
	return "scripts changed: " + strings.Join(ch, ", ")
}

func goToolchainChanged(c Change, read Reader) string {
	pick := func(b []byte) string {
		var keep []string
		for _, l := range strings.Split(string(b), "\n") {
			l = strings.TrimSpace(l)
			if strings.HasPrefix(l, "go ") || strings.HasPrefix(l, "toolchain ") || strings.HasPrefix(l, "tool ") {
				keep = append(keep, l)
			}
		}
		return strings.Join(keep, "; ")
	}
	a, b := pick(blob(read, c.OldOID)), pick(blob(read, c.NewOID))
	if a == b {
		return ""
	}
	return "go/toolchain: " + a + " -> " + b
}
