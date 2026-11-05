package policy

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy/match"
)

// The rules of a refusal from Check and FromProto. A refusal from the
// host, path or method checks keeps the rule of package match or
// dnsname.
const (
	// RuleString: a rule name, reason, part, project id or repository
	// that is empty, too long, or not printable ASCII
	// (S09-01-policy-files, "Strings").
	RuleString = "policy-string"
	// RuleBound: a list longer than its bound, or a value past one.
	RuleBound = "policy-bound"
	// RuleValue: an enum value outside its set, a port of 0, a
	// duplicate, or a field that is missing.
	RuleValue = "policy-value-invalid"
	// RuleEndpointForm: an endpoint whose fields don't go together, as
	// OpenShell v0.1.2 refuses them (`l7/mod.rs`, `l7_validate.rs`).
	RuleEndpointForm = "policy-endpoint-form"
	// RuleSource: a source that may not set the entry, or whose project
	// or repository is missing or set where it doesn't belong.
	RuleSource = "policy-source"
	// RuleGuarded: a pass host, a raw TCP entry or an `audit` endpoint on
	// a guarded host (S07-07-hosts-and-modes, "Guarded hosts").
	RuleGuarded = "policy-guarded-host"
	// RuleProtocolConflict: a host and port with two protocols
	// (S07-15-non-http-streams, "One protocol per host and port").
	RuleProtocolConflict = "policy-protocol-conflict"
	// RulePart: a part that is off but no profile and kind has
	// (S07-11-http-policy, "Profile parts").
	RulePart = "profile-part-unknown"
)

// Refusal is a policy that breaks a bound or an invariant. Rule names
// the check, and Field the field, such as `endpoints[3].ports`.
type Refusal struct {
	Rule  string
	Field string
	msg   string
}

// Error returns the field, the rule and the reason. The text holds no
// byte of a refused string, only offsets and lengths, so it is safe to
// log and to show (NFR06-explained-refusals).
func (r *Refusal) Error() string {
	if r == nil {
		return ""
	}
	return r.Field + ": " + r.Rule + ": " + r.msg
}

func refuse(rule, field, format string, args ...any) *Refusal {
	return &Refusal{Rule: rule, Field: field, msg: fmt.Sprintf(format, args...)}
}

// fromMatch keeps the rule and message of a refusal of package match.
func fromMatch(field string, r *match.Refusal) *Refusal {
	return &Refusal{Rule: r.Rule, Field: field, msg: strings.TrimPrefix(r.Error(), r.Rule+": ")}
}

func at(list string, i int) string { return list + "[" + strconv.Itoa(i) + "]" }

// checkString checks a string against S09-01-policy-files, "Strings":
// 1 to maxLen bytes of printable ASCII.
func checkString(field, s string, maxLen int) *Refusal {
	if s == "" || len(s) > maxLen {
		return refuse(RuleString, field, "has %d bytes, want 1 to %d", len(s), maxLen)
	}
	for i := range len(s) {
		if c := s[i]; c < 0x20 || c > 0x7e {
			return refuse(RuleString, field, "has a byte that isn't printable ASCII at offset %d", i)
		}
	}
	return nil
}

// checkProjectID checks the form of S05-04-naming: 32 lowercase
// hexadecimal characters.
func checkProjectID(field, id string) *Refusal {
	if len(id) != ProjectIDLen {
		return refuse(RuleValue, field, "is not %d lowercase hexadecimal characters", ProjectIDLen)
	}
	for i := range len(id) {
		if c := id[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return refuse(RuleValue, field, "is not %d lowercase hexadecimal characters", ProjectIDLen)
		}
	}
	return nil
}

// checkSource checks a source's kind is one of allowed, and that its
// project and repository are set where its kind needs them and empty
// elsewhere.
func checkSource(field string, s Source, allowed ...SourceKind) *Refusal {
	ok := false
	for _, k := range allowed {
		ok = ok || s.Kind == k
	}
	if !ok {
		return refuse(RuleSource, field, "a %s source may not set this", s.Kind)
	}
	needProject := s.Kind == SourceProject || s.Kind == SourceRepository
	switch {
	case needProject && s.Project == "":
		return refuse(RuleSource, field, "a %s source needs a project", s.Kind)
	case s.Project != "" && !needProject && s.Kind != SourceApproval:
		return refuse(RuleSource, field, "a %s source has no project", s.Kind)
	case (s.Kind == SourceRepository) != (s.Repository != ""):
		return refuse(RuleSource, field, "only a repository source has a repository")
	}
	if s.Project != "" {
		if r := checkProjectID(field+".project", s.Project); r != nil {
			return r
		}
	}
	if s.Repository != "" {
		return checkString(field+".repository", s.Repository, MaxRepository)
	}
	return nil
}

// anySource is every source an endpoint may have.
var anySource = []SourceKind{SourceBuiltin, SourceGlobal, SourceProject, SourceRepository, SourceApproval}

// Check checks every bound and invariant of the policy. FromProto runs
// it on each message, and a caller that builds a Policy in Go runs it
// before it sends one. It refuses rather than repairs.
func (p *Policy) Check() *Refusal {
	steps := []func() *Refusal{
		p.checkCounts,
		p.checkEndpoints,
		p.checkPassHosts,
		p.checkRawTCP,
		p.checkPartsOff,
		p.checkProjects,
		p.checkGuarded,
		p.checkProtocols,
	}
	for _, step := range steps {
		if r := step(); r != nil {
			return r
		}
	}
	return nil
}

func bound(field string, n, maxN int) *Refusal {
	if n > maxN {
		return refuse(RuleBound, field, "has %d entries, at most %d", n, maxN)
	}
	return nil
}

func (p *Policy) checkCounts() *Refusal {
	c := counts{
		endpoints: len(p.Endpoints), passHosts: len(p.PassHosts), rawTCP: len(p.RawTCP),
		partsOff: len(p.PartsOff), projects: len(p.Projects), wildcardBudget: p.WildcardBudget,
	}
	for i := range p.Endpoints {
		c.rules += len(p.Endpoints[i].Rules) + len(p.Endpoints[i].DenyRules)
	}
	return c.check()
}

// counts are the sizes of a policy's lists, so Policy.checkCounts and
// FromProto check the same bounds, FromProto before it reads a list.
type counts struct {
	endpoints, rules, passHosts, rawTCP, partsOff, projects int
	wildcardBudget                                          uint32
}

func (c counts) check() *Refusal {
	for _, r := range []*Refusal{
		bound("endpoints", c.endpoints, MaxEndpoints),
		bound("endpoints[*].rules and deny_rules", c.rules, MaxRules),
		bound("pass_hosts", c.passHosts, MaxPassHosts),
		bound("raw_tcp", c.rawTCP, MaxRawTCP),
		bound("parts_off", c.partsOff, MaxPartsOff),
		bound("projects", c.projects, MaxProjects),
	} {
		if r != nil {
			return r
		}
	}
	if c.wildcardBudget > MaxWildcardBudget {
		return refuse(RuleBound, "wildcard_budget", "is %d, at most %d", c.wildcardBudget, MaxWildcardBudget)
	}
	return nil
}

func (p *Policy) checkPassHosts() *Refusal {
	for i, h := range p.PassHosts {
		f := at("pass_hosts", i)
		if h.Host.String() == "" {
			return refuse(RuleValue, f+".host", "is missing")
		}
		// Only the global and project policy set pass hosts
		// (S07-07-hosts-and-modes, "Rules for pass").
		if r := checkSource(f+".source", h.Source, SourceGlobal, SourceProject); r != nil {
			return r
		}
		if r := checkString(f+".reason", h.Reason, MaxReason); r != nil {
			return r
		}
	}
	return nil
}

func (p *Policy) checkRawTCP() *Refusal {
	for i, e := range p.RawTCP {
		f := at("raw_tcp", i)
		switch {
		case e.Host.String() == "":
			return refuse(RuleValue, f+".host", "is missing")
		case e.Port == 0:
			return refuse(RuleValue, f+".port", "is 0")
		}
		// The rules of a pass host apply to a raw TCP entry
		// (S09-01-policy-files, "Raw TCP entries").
		if r := checkSource(f+".source", e.Source, SourceGlobal, SourceProject); r != nil {
			return r
		}
		if r := checkString(f+".reason", e.Reason, MaxReason); r != nil {
			return r
		}
	}
	return nil
}

func (p *Policy) checkProjects() *Refusal {
	seen := map[string]bool{}
	for i, pr := range p.Projects {
		f := at("projects", i)
		if r := checkProjectID(f+".id", pr.ID); r != nil {
			return r
		}
		if seen[pr.ID] {
			return refuse(RuleValue, f+".id", "is a duplicate")
		}
		seen[pr.ID] = true
		if r := bound(f+".repositories", len(pr.Repositories), MaxRepositories); r != nil {
			return r
		}
		for j, repo := range pr.Repositories {
			fr := at(f+".repositories", j)
			if r := checkString(fr, repo, MaxRepository); r != nil {
				return r
			}
			if _, _, r := repositoryHost(repo); r != nil {
				r.Field = fr
				return r
			}
		}
	}
	return nil
}
