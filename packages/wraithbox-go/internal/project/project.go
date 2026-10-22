// Package project resolves a directory to the project it belongs to: the
// repository's project key, its recorded identity and its default name
// (S05-04-naming). It keeps no state. Storing and comparing what it
// returns, and registration, are elsewhere.
//
// Everything it reads from the repository is treated as an attacker's.
// A downloaded archive can hold a `.git` file or a symbolic link that
// names another project's repository, and any remote URL. A directory
// never resolves to another project's repository (SEC08-proj-isolation),
// git runs only through hostgit.Command, with no hooks and no GIT_*
// variable of the caller (SEC03-no-host-exec), and every git run has a
// deadline and a cap on what is read from it (SEC13-bounded-resources).
// Each decision is logged with the rule that made it.
package project

import (
	"context"
	"log/slog"
	"strconv"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/paths"
)

// Realm is the host realm of a project key (S05-04-naming, "Project
// key").
type Realm string

// RealmNative is the realm of a repository on the native host. The WSL
// realm comes with S08-10-wsl.
const RealmNative Realm = "native"

// Key is a project's key: the realm, and the absolute path of the
// repository's git common directory as git printed it (S05-04-naming,
// "Path").
type Key struct {
	Realm Realm
	Path  string
}

// Kind is how the directory holds its repository.
type Kind string

// The kinds a directory resolves as.
const (
	// KindMain: the directory's `.git` is the repository's directory.
	KindMain Kind = "main"
	// KindWorktree: a `.git` file of a linked worktree.
	KindWorktree Kind = "worktree"
	// KindSubmodule: a `.git` file of a submodule.
	KindSubmodule Kind = "submodule"
)

// Repo is a resolved repository. Only Resolve makes one, after every
// check of S05-04-naming, "Resolving the key", has passed.
type Repo struct {
	// Key is the project key.
	Key Key
	// Dir is the directory that holds the `.git` entry, in canonical
	// form. Identity runs git there.
	Dir string
	// Kind is how Dir holds the repository.
	Kind Kind
	// Name is the default name (S05-04-naming, "Name").
	Name string
}

// Resolver resolves directories and reads identities. The zero value
// refuses every directory: Git and Own must be set.
type Resolver struct {
	// Git is the absolute path of the git binary, from hostgit.Resolve,
	// whose version the caller has checked (S08-08-host-git).
	Git string
	// Own are Wraith Box's own directories. A key under <data>,
	// <config> or <logs> is refused (S05-04-naming, "Wraith Box's own
	// directories"). All three must be set.
	Own paths.Paths
	// Log gets every decision. Nil is slog.Default().
	Log *slog.Logger

	// testEnv is added to git's environment. Only the tests set it, to
	// give git a global configuration of their own, such as
	// GIT_CONFIG_GLOBAL with insteadOf rules, because Command takes HOME
	// from the user database. It is unexported, so code outside this
	// package can't set it.
	testEnv []string
	// limits replaces defaultLimits when set. Only the tests set it.
	limits *limits
}

// Rule names the rule behind a decision, for the log and later the audit
// log.
type Rule string

// The rules of this package. Each refusal has its own.
const (
	// RuleResolved: the directory resolved to a project key.
	RuleResolved Rule = "project-resolved"
	// RuleIdentityRead: the repository's identity was read.
	RuleIdentityRead Rule = "project-identity-read"

	// RuleOwnUnset: the Resolver has no <data>, <config> or <logs>, so
	// it can't check the key against them.
	RuleOwnUnset Rule = "project-own-dirs-unset"
	// RuleHostUnsupported: this host has no canonical form of a path yet
	// (S12-platforms).
	RuleHostUnsupported Rule = "project-host-unsupported"
	// RuleDirUnreadable: a directory or `.git` entry on the way couldn't
	// be read, a path couldn't be put in canonical form, or the
	// directory's canonical form holds a control character or isn't
	// UTF-8.
	RuleDirUnreadable Rule = "project-dir-unreadable"
	// RuleNotInRepo: no `.git` entry from the directory up to the root.
	RuleNotInRepo Rule = "project-not-in-repo"
	// RuleGitLink: the `.git` entry is a symbolic link, which could name
	// another project's repository.
	RuleGitLink Rule = "project-git-link"
	// RuleGitEntryType: the `.git` entry is neither a directory, a file
	// nor a symbolic link.
	RuleGitEntryType Rule = "project-git-entry-type"
	// RuleGitFailed: a git run failed, exited nonzero or timed out,
	// "dubious ownership" included.
	RuleGitFailed Rule = "project-git-failed"
	// RuleGitOutput: git's output is over its cap, or isn't in the form
	// the step expects.
	RuleGitOutput Rule = "project-git-output"
	// RuleGitDirElsewhere: the `.git` directory isn't the repository git
	// resolved.
	RuleGitDirElsewhere Rule = "project-git-dir-elsewhere"
	// RuleWorktreeElsewhere: the `.git` directory's core.worktree names
	// a directory other than the one that holds that `.git`.
	RuleWorktreeElsewhere Rule = "project-worktree-elsewhere"
	// RuleGitFileElsewhere: the `.git` file names a repository that is
	// neither this directory's linked worktree nor its submodule.
	RuleGitFileElsewhere Rule = "project-git-file-elsewhere"
	// RuleOwnDirectory: the key is under <data>, <config> or <logs>.
	RuleOwnDirectory Rule = "project-own-directory"

	// RuleRemoteName: a remote name starts with `-`, holds a control
	// character, is empty, or is too long.
	RuleRemoteName Rule = "project-remote-name"
	// RuleTooManyRemotes: more remotes than the cap.
	RuleTooManyRemotes Rule = "project-too-many-remotes"
	// RuleTooManyURLs: a remote with more URLs than the cap.
	RuleTooManyURLs Rule = "project-too-many-urls"
	// RuleURLTooLong: a remote URL longer than the cap.
	RuleURLTooLong Rule = "project-url-too-long"
	// RuleTooManyRoots: more root commits than the cap.
	RuleTooManyRoots Rule = "project-too-many-root-commits"
	// RuleRootCommitForm: a root commit that isn't a 40- or
	// 64-character lowercase object ID.
	RuleRootCommitForm Rule = "project-root-commit-form"
)

// Refusal is the error of every refusal in this package. Its message
// says why (NFR06-explained-refusals).
type Refusal struct {
	Rule Rule

	msg string
	err error
	// exitCode is git's exit code for a RuleGitFailed refusal of a run
	// that exited, and 0 otherwise.
	exitCode int
}

func (r *Refusal) Error() string { return r.msg }

// Unwrap returns the cause, such as the error of a failed run.
func (r *Refusal) Unwrap() error { return r.err }

func (r *Resolver) log() *slog.Logger {
	if r.Log != nil {
		return r.Log
	}
	return slog.Default()
}

// logRefusal logs a refusal with its rule and the directory asked for.
func (r *Resolver) logRefusal(ctx context.Context, msg, dir string, ref *Refusal) {
	r.log().LogAttrs(ctx, slog.LevelWarn, msg,
		slog.String("rule", string(ref.Rule)),
		slog.String("decision", "refuse"),
		slog.String("dir", strconv.Quote(dir)),
		slog.String("reason", ref.msg))
}
