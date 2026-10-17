package platform

import "testing"

func TestHostSupport(t *testing.T) {
	for host, want := range map[OS]Support{
		MacOS:   V1,
		Windows: Later,
		Linux:   Later,
		"plan9": Unsupported,
	} {
		if got := HostSupport(host); got != want {
			t.Errorf("HostSupport(%q) = %v, want %v", host, got, want)
		}
	}
}
