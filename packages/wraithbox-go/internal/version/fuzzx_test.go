package version

import "testing"

// Throwaway for the #162 demo on macOS and Windows; never on main.
func FuzzX(f *testing.F) {
	f.Add("wb")
	f.Fuzz(func(_ *testing.T, s string) {
		_ = String(s)
	})
}
