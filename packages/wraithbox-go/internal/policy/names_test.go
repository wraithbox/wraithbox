package policy

import "testing"

func TestNameAllowance(t *testing.T) {
	p := &Policy{
		Endpoints: []Endpoint{
			l4(t, "a.example.com", EnforcementEnforce, 443),
			l4(t, "*.example.com", EnforcementEnforce, 443, 8443),
			l4(t, "*.wild.example.org", EnforcementEnforce, 443),
		},
		RawTCP: []RawTCP{{Host: name(t, "db.example.net"), Port: 5432, Source: Source{Kind: SourceGlobal}, Reason: "db"}},
		PassHosts: []PassHost{
			{Host: name(t, "pinned.example.net"), Source: Source{Kind: SourceGlobal}, Reason: "pinning"},
		},
	}
	for _, tc := range []struct {
		host string
		want NameAllowance
	}{
		{"a.example.com", NameByExact},
		{"b.example.com", NameByWildcardOnly},
		{"x.wild.example.org", NameByWildcardOnly},
		{"wild.example.org", NameRefused},
		{"a.b.example.com", NameRefused},
		{"db.example.net", NameByExact},
		{"pinned.example.net", NameRefused},
		{"other.example", NameRefused},
	} {
		t.Run(tc.host, func(t *testing.T) {
			if got := p.NameAllowance(name(t, tc.host)); got != tc.want {
				t.Errorf("NameAllowance(%s) = %s, want %s", tc.host, got, tc.want)
			}
		})
	}
	var none *Policy
	if got := none.NameAllowance(name(t, "a.example.com")); got != NameRefused {
		t.Errorf("a nil policy allows a name: %s", got)
	}
}

func TestPortAllowed(t *testing.T) {
	p := &Policy{
		Endpoints: []Endpoint{l4(t, "*.example.com", EnforcementEnforce, 443)},
		RawTCP:    []RawTCP{{Host: name(t, "db.example.net"), Port: 5432, Source: Source{Kind: SourceGlobal}, Reason: "db"}},
	}
	for _, tc := range []struct {
		host string
		port uint16
		want bool
	}{
		{"a.example.com", 443, true},
		{"a.example.com", 80, false},
		{"db.example.net", 5432, true},
		{"db.example.net", 443, false},
		{"other.example", 443, false},
	} {
		if got := p.PortAllowed(name(t, tc.host), tc.port); got != tc.want {
			t.Errorf("PortAllowed(%s, %d) = %v, want %v", tc.host, tc.port, got, tc.want)
		}
	}
	var none *Policy
	if none.PortAllowed(name(t, "a.example.com"), 443) {
		t.Error("a nil policy allows a port")
	}
}
