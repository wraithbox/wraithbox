package platform

import "testing"

// TestHasDrivePrefix: git for Windows reads an ASCII letter and a colon
// as a drive letter (S08-01-into-the-guest, "Remote URLs").
func TestHasDrivePrefix(t *testing.T) {
	for s, want := range map[string]bool{
		`C:/x`: true, `c:\x`: true, `Z:`: true,
		`host:x`: false, `1:x`: false, `C`: false, ``: false, `/x`: false,
	} {
		if got := HasDrivePrefix(s); got != want {
			t.Errorf("HasDrivePrefix(%q) = %v, want %v", s, got, want)
		}
	}
}
