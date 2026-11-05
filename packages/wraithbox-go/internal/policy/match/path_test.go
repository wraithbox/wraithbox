package match

import (
	"strings"
	"testing"
)

// TestCanonicalPath takes its cases from the tests of OpenShell v0.1.2,
// `crates/openshell-supervisor-network/src/l7/path.rs` (commit
// 6648bd0c), where they hold under S07-08-requests-and-credentials. The
// name of each upstream test is in the comment.
func TestCanonicalPath(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		// literal_dot_segments_resolve
		{"/a/./b", "/a/b"},
		{"/a/b/.", "/a/b/"},
		{"/a/../b", "/b"},
		{"/a/b/..", "/a/"},
		// doubled_slashes_collapse
		{"//", "/"},
		{"//public//../secret", "/secret"},
		{"/public//secret", "/public/secret"},
		// legitimate_percent_encoded_bytes_round_trip
		{"/search/a%3Fb", "/search/a%3Fb"},
		{"/users/%40alice", "/users/@alice"},
		// percent_encoded_non_ascii_bytes_round_trip,
		// mixed_case_percent_normalizes_to_uppercase
		{"/users/caf%C3%A9", "/users/caf%C3%A9"},
		{"/a/caf%c3%a9", "/a/caf%C3%A9"},
		// empty_and_root_equivalent (the empty path is refused here,
		// since the caller always has a path that starts with /)
		{"/", "/"},
		// regression_dot_slash_dotdot, regression_double_slash_prefix
		{"/public/./../secret", "/secret"},
		{"//public/../secret", "/secret"},
		// encoded_slash_preserved_when_opted_in: here always, as data
		// (S07-08, "Encoded slashes"). OpenShell refuses it by default.
		{"/a/%2f/b", "/a/%2F/b"},
		{"/@scope%2fname", "/@scope%2Fname"},
		{"/api/v4/projects/group%2Fproject", "/api/v4/projects/group%2Fproject"},
		// encoded_slash_detection_on_canonical_paths_is_exact
		{"/a/%252F/b", "/a/%252F/b"},
		{"/a/100%25/b", "/a/100%25/b"},
		// path_parameters_preserved_when_disabled: here always, since
		// the canonical path is the one sent upstream.
		{"/a;jsessionid=xyz/b", "/a;jsessionid=xyz/b"},
		// Wraith Box's own cases.
		{"/%67raphql", "/graphql"},
		{"/graphql/", "/graphql/"},
		{"/v1/oauth/%74oken", "/v1/oauth/token"},
		{"/a%2e%2eb", "/a..b"},
		{"/a b", "/a%20b"},
		{"/*", "/*"},
		{"/%2A", "/*"},
	} {
		got, r := CanonicalPath(tc.in)
		if r != nil || got != tc.want {
			t.Errorf("CanonicalPath(%q) = %q, %v, want %q", tc.in, got, r, tc.want)
			continue
		}
		if again, r := CanonicalPath(got); r != nil || again != got {
			t.Errorf("CanonicalPath(%q) = %q, %v, not idempotent", got, again, r)
		}
	}
}

func TestCanonicalPathRefused(t *testing.T) {
	for _, in := range []string{
		"",
		"a/b",
		"*",
		// traversal_above_root_is_rejected
		"/..",
		"/a/../..",
		// percent_encoded_dot_segments_resolve_the_same_way: OpenShell
		// resolves these. S07-08, "Request form", refuses them.
		"/public/%2e%2e/secret",
		"/public/%2E%2E/secret",
		"/public/%2e/secret",
		// S07-08's own examples.
		"/wget%2F..%2Fcurl",
		"/wget%2F%2e%2e%2Fcurl",
		// regression_dotdot_with_path_parameter_is_resolved_not_smuggled:
		// OpenShell strips the parameter and resolves. Here the segment is
		// refused, since the parameter stays.
		"/public/..;/secret",
		"/public/.;x/secret",
		"/public/%2e%2e;/secret",
		"/public/..%3B/secret",
		// null_and_control_bytes_rejected
		"/a%00b",
		"/a%0Ab",
		"/a%0Db",
		"/a%7Fb",
		"/a\x00b",
		"/a\tb",
		// non_ascii_raw_byte_rejected
		"/caf\xc3\xa9",
		// fragment_rejected, and a query left in by the caller.
		"/a#frag",
		"/a?q=1",
		// S07-08: a backslash, raw or encoded, and malformed escapes.
		"/a\\b",
		"/a%5Cb",
		"/a%zz",
		"/a%",
		"/a%4",
		// path_too_long_rejected
		"/" + strings.Repeat("a", MaxPath),
		// Short enough raw, too long once each [ is written as %5B: a
		// fixed point must pass its own length check (found by
		// FuzzPathMatch).
		"/" + strings.Repeat("[", MaxPath/3+1),
	} {
		if got, r := CanonicalPath(in); r == nil || r.Rule != RulePathForm {
			t.Errorf("CanonicalPath(%q) = %q, %v, want a %s refusal", in, got, r, RulePathForm)
		}
	}
}

// TestRefusalPrintable checks that a refusal never repeats the bytes it
// refused (NFR06-explained-refusals: the message goes to a log and to
// the guest).
func TestRefusalPrintable(t *testing.T) {
	for _, in := range []string{"/a\x1b[31mb", "/\xff", "/a%0a"} {
		_, r := CanonicalPath(in)
		if r == nil {
			t.Fatalf("CanonicalPath(%q) accepted", in)
		}
		for _, c := range []byte(r.Error()) {
			if c < 0x20 || c >= 0x7f {
				t.Errorf("refusal of %q has the byte %#x: %q", in, c, r.Error())
			}
		}
	}
	var nilRefusal *Refusal
	if nilRefusal.Error() != "" {
		t.Error("a nil Refusal has a message")
	}
}
