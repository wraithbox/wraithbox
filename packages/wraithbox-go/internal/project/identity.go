package project

import (
	"context"
	"errors"
	"log/slog"
	"slices"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gitremote"
)

// objectIDLine is the longest line of rev-list's output: a SHA-256
// object ID and a line end.
const objectIDLine = 65

// Identity is a repository's recorded identity (S05-04-naming,
// "Recorded identity").
type Identity struct {
	// URLs are the compare forms of every URL of every remote
	// (gitremote.Result.Compare), sorted and without duplicates. None
	// holds a user name, password, query string or fragment.
	URLs []string
	// RootCommits are the repository's root commits, sorted. They are
	// read only when it has no remote URL, and are then empty for a
	// repository without commits.
	RootCommits []string
}

// Identity reads the identity of a resolved repository. Remote URLs come
// from `git remote get-url --all`, which applies the user's insteadOf
// rules, and each is cleaned by gitremote. A repository without a remote
// URL gives its root commits instead. Every count and length has a cap.
// Over one, the identity is refused with the cap's rule, never cut. The
// decision is logged with its rule, and never with a URL.
func (r *Resolver) Identity(ctx context.Context, repo *Repo) (Identity, error) {
	if repo == nil || repo.Dir == "" {
		return Identity{}, errors.New("project: Identity needs a repository from Resolve")
	}
	id, ref := r.identity(ctx, repo.Dir)
	if ref != nil {
		r.logRefusal(ctx, "project identity refused", repo.Dir, ref)
		return Identity{}, ref
	}
	r.log().LogAttrs(ctx, slog.LevelInfo, "project identity read",
		slog.String("rule", string(RuleIdentityRead)),
		slog.String("decision", "accept"),
		slog.String("dir", repo.Dir),
		slog.String("key", repo.Key.Path),
		slog.Int("urls", len(id.URLs)),
		slog.Int("root_commits", len(id.RootCommits)))
	return id, nil
}

func (r *Resolver) identity(ctx context.Context, dir string) (Identity, *Refusal) {
	lim := defaultLimits
	if r.limits != nil {
		lim = *r.limits
	}
	urls, ref := r.remoteURLs(ctx, dir, lim)
	if ref != nil || len(urls) > 0 {
		return Identity{URLs: urls}, ref
	}
	// One line more than the cap fits, so a count just over it is
	// refused with the cap's rule rather than as long output.
	out, ref := r.run(ctx, gitRun{
		dir: dir, timeout: revListTimeout, limit: (lim.roots + 1) * objectIDLine,
		args: []string{"rev-list", "--max-parents=0", "--all"},
	})
	if ref != nil {
		return Identity{}, ref
	}
	roots, ref := parseRootCommits(out, lim)
	return Identity{RootCommits: roots}, ref
}

// remoteURLs returns the compare forms of every URL of every remote,
// sorted and without duplicates.
func (r *Resolver) remoteURLs(ctx context.Context, dir string, lim limits) ([]string, *Refusal) {
	out, ref := r.run(ctx, gitRun{
		dir: dir, timeout: gitTimeout, limit: (lim.remotes + 1) * (lim.remoteName + 1),
		args: []string{"remote"},
	})
	if ref != nil {
		return nil, ref
	}
	names, ref := parseRemoteNames(out, lim)
	if ref != nil {
		return nil, ref
	}
	var compare []string
	for _, name := range names {
		// parseRemoteNames refused a name that starts with `-`, so git
		// reads name as a remote name, not an option.
		out, ref := r.run(ctx, gitRun{
			dir: dir, timeout: gitTimeout, limit: (lim.urls + 1) * (lim.urlLen + 1),
			args: []string{"remote", "get-url", "--all", name},
		})
		if ref != nil {
			return nil, ref
		}
		urls, ref := parseURLs(out, name, lim)
		if ref != nil {
			return nil, ref
		}
		for _, u := range urls {
			compare = append(compare, gitremote.Clean(u).Compare)
		}
	}
	slices.Sort(compare)
	return slices.Compact(compare), nil
}
