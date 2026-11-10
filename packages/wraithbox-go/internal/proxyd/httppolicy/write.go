package httppolicy

import (
	"net/url"
	"slices"
	"strings"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy/builtin"
)

// isRead reports whether a method is a read: GET, HEAD or OPTIONS.
// Methods have passed checkMethod, so they are in upper case.
func isRead(method string) bool {
	return method == "GET" || method == "HEAD" || method == "OPTIONS"
}

// The paths of git's smart HTTP transport, after a repository's path.
const (
	infoRefs    = "/info/refs"
	uploadPack  = "/git-upload-pack"
	receivePack = "/git-receive-pack"
	uploadSvc   = "git-upload-pack"
)

// isWrite reports whether a request on a git hosting host writes, as
// SEC06-repo-writes limits writes to the project set
// (S07-19-request-decisions, "Writes to git hosting"). A write is:
//   - a request whose query names a git service other than
//     `git-upload-pack`, which is how git asks for the
//     `git-receive-pack` advertisement, whatever its method and path;
//   - any method but GET, HEAD and OPTIONS, except a `git-upload-pack`
//     POST on a transport path (transportRepo), which is a fetch, and a request to the host's GraphQL path,
//     which the GraphQL filter decides (M3.7b, refused until then).
func (h *hostRules) isWrite(method, path, rawQuery string) bool {
	if namesPushService(rawQuery) {
		return true
	}
	if isRead(method) {
		return false
	}
	if _, suffix, ok := h.transportRepo(path); ok && method == "POST" && suffix == uploadPack {
		return false
	}
	return !h.isGraphQL(path)
}

// transportRepo reads the repository of a path of git's smart HTTP
// transport, `{repo}/info/refs`, `{repo}/git-upload-pack` or
// `{repo}/git-receive-pack`, and returns the suffix it found. It reads
// one only on a host that serves the transport (repoForms.transport).
// On GitHub and Gitea the repository is `/{owner}/{repo}`. On GitLab
// and a host a remote names, it has two segments or more, and its first
// segment isn't `api`, and no segment is `-`. So an API path that ends in a transport suffix,
// such as a file named `git-upload-pack`, isn't the transport.
func (h *hostRules) transportRepo(path string) (repo, suffix string, ok bool) {
	if !h.forms.transport {
		return "", "", false
	}
	for _, s := range []string{infoRefs, uploadPack, receivePack} {
		r, found := strings.CutSuffix(path, s)
		if found && h.isTransportRepo(r) {
			return r, s, true
		}
	}
	return "", "", false
}

// partGit is the part of git's transport rules (S07-11-http-policy,
// "GitHub parts").
const partGit = "git"

// isTransport reports whether a path is one of git's transport
// (transportRepo).
func (h *hostRules) isTransport(path string) bool {
	_, _, ok := h.transportRepo(path)
	return ok
}

// isTransportRepo reports whether a path is a repository path of git's
// transport on this host (transportRepo).
func (h *hostRules) isTransportRepo(repo string) bool {
	rest, ok := strings.CutPrefix(repo, "/")
	segments := strings.Split(rest, "/")
	if !ok || len(segments) < 2 || slices.Contains(segments, "") {
		return false
	}
	if h.kind == builtin.GitHub || h.kind == builtin.Gitea {
		return len(segments) == 2
	}
	// GitLab reserves the segment `-` for its web routes, such as
	// `/{project}/-/create/{ref}`, so no repository path has one.
	return !strings.EqualFold(segments[0], "api") && !slices.Contains(segments, "-")
}

// namesPushService reports whether a query has a `service` parameter
// other than `git-upload-pack`. Parameters are split on `&` and `;`,
// and the name compares without regard to case, so a server that reads
// either finds no other service than the one checked. A parameter that
// doesn't decode counts as one, which fails closed.
func namesPushService(rawQuery string) bool {
	for pair := range strings.FieldsFuncSeq(rawQuery, func(r rune) bool { return r == '&' || r == ';' }) {
		k, v, _ := strings.Cut(pair, "=")
		name, err := url.QueryUnescape(k)
		if err != nil {
			return true
		}
		if !strings.EqualFold(strings.TrimSpace(name), "service") {
			continue
		}
		value, err := url.QueryUnescape(v)
		if err != nil || value != uploadSvc {
			return true
		}
	}
	return false
}

// isGraphQL reports whether a canonical path is the host's GraphQL
// path in one of the forms a server may route to it: in any letter
// case, and followed by a `/`, a `;` parameter, a `.` format suffix or
// an escape (C03-policy-and-approvals, "Profile parts").
func (h *hostRules) isGraphQL(path string) bool {
	gp := h.graphQLPath
	if gp == "" {
		return false
	}
	lower := strings.ToLower(path)
	if lower == gp {
		return true
	}
	return strings.HasPrefix(lower, gp) && strings.IndexByte("/;.%", lower[len(gp)]) >= 0
}

// inProjectSet reports whether a write names a repository of the
// project set on this host. A write whose repository can't be read
// from its path is outside it, which fails closed (SEC06-repo-writes).
func (h *hostRules) inProjectSet(path string) bool {
	repo, ok := h.repository(path)
	if !ok {
		return false
	}
	key, ok := h.repoKey(repo)
	return ok && h.repos[key]
}

// apiRepoSuffix is a repository name suffix that git's transport drops
// and an API may not: in an API path it names no repository that can
// be checked.
const apiRepoSuffix = ".git"

func hasAPIRepoSuffix(name string) bool {
	return strings.HasSuffix(strings.ToLower(name), apiRepoSuffix)
}

// repository reads the repository a canonical path names, in the
// forms the host serves (apiForms):
//   - git's transport: `{repo}/info/refs`, `{repo}/git-upload-pack` and
//     `{repo}/git-receive-pack`;
//   - GitHub's API: `/repos/{owner}/{repo}[/...]`;
//   - GitLab's API: `/api/v4/projects/{url-encoded path}[/...]`. A
//     numeric project id names no repository that can be checked;
//   - the API of Gitea and Forgejo: `/api/v1/repos/{owner}/{repo}[/...]`.
func (h *hostRules) repository(path string) (string, bool) {
	if repo, _, ok := h.transportRepo(path); ok {
		return repo, true
	}
	f := h.forms
	switch {
	case f.githubAPI:
		return ownerRepo(path, "/repos/")
	case f.giteaAPI && strings.HasPrefix(path, "/api/v1/"):
		return ownerRepo(path, "/api/v1/repos/")
	case f.gitlabAPI && strings.HasPrefix(path, "/api/v4/"):
		return gitlabProject(path)
	}
	return "", false
}

// ownerRepo reads `/{owner}/{repo}` after prefix.
func ownerRepo(path, prefix string) (string, bool) {
	rest, ok := strings.CutPrefix(path, prefix)
	if !ok {
		return "", false
	}
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" || hasAPIRepoSuffix(parts[1]) {
		return "", false
	}
	return "/" + parts[0] + "/" + parts[1], true
}

// gitlabProject reads the project path of `/api/v4/projects/{id}`. The
// id names a path only when it holds an encoded `/`, and nothing else
// escaped, since a GitLab path has only letters, digits, `_`, `-` and
// `.`. An id with an empty segment, from a leading, trailing or doubled
// `%2F`, is malformed and names no repository, so the write it is in is
// refused (SEC06-repo-writes).
func gitlabProject(path string) (string, bool) {
	rest, ok := strings.CutPrefix(path, "/api/v4/projects/")
	if !ok {
		return "", false
	}
	id, _, _ := strings.Cut(rest, "/")
	if !strings.Contains(id, "%2F") {
		return "", false
	}
	decoded := strings.ReplaceAll(id, "%2F", "/")
	if strings.Contains(decoded, "%") || hasAPIRepoSuffix(decoded) {
		return "", false
	}
	if slices.Contains(strings.Split(decoded, "/"), "") {
		return "", false
	}
	return "/" + decoded, true
}
