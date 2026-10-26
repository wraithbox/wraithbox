package appbundle

import (
	"strings"
	"testing"
)

func TestC12BundleIdentity(t *testing.T) {
	cases := []struct {
		team, bundleID string
		want           string
		wantErr        bool
	}{
		{"", "", AdhocBundleID, false},
		{"", "com.example.wraithbox", "com.example.wraithbox", false},
		{"", "nl.wraithbox.dev.abcde12345", "nl.wraithbox.dev.abcde12345", false},
		{"", "Com.Example-1.App", "Com.Example-1.App", false},
		// A team: the team ID in lowercase, or WB_BUNDLE_ID.
		{testTeam, "", "nl.wraithbox.dev.abcde12345", false},
		{testTeam, "com.example.wraithbox", "com.example.wraithbox", false},
		// Refused: a team that isn't an Apple team ID, even with a bundle
		// identifier.
		{"abcde12345", "", "", true},
		{"ABCDE1234", "", "", true},
		{"ABCDE12345 ", "com.example.wraithbox", "", true},
		{"ABCDE;1234", "", "", true},
		// Refused: not reverse-DNS.
		{"", "wraithbox", "", true},
		{"", "com..example", "", true},
		{"", ".com.example", "", true},
		{"", "com.example.", "", true},
		{"", "com.example wraithbox", "", true},
		{"", "com.example/wraithbox", "", true},
		{"", "com.example_wraithbox.app", "", true},
		{"", "com.café.app", "", true},
		{"", "com.example.$(id)", "", true},
		{"", "com." + strings.Repeat("a", maxBundleIDLen), "", true},
	}
	for _, tc := range cases {
		got, err := BundleID(tc.team, tc.bundleID)
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("BundleID(%q, %q) = %q, %v; want %q, error %v", tc.team, tc.bundleID, got, err, tc.want, tc.wantErr)
		}
	}
}

func TestC12BundleIdentityTeamMessage(t *testing.T) {
	_, err := BundleID("abcde12345", "")
	if err == nil {
		t.Fatal("BundleID took a lowercase team")
	}
	for _, want := range []string{TeamEnv, "organizational unit", "Signing identity"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message %q doesn't name %q", err, want)
		}
	}
}

func TestValidTeamID(t *testing.T) {
	for s, want := range map[string]bool{
		testTeam:      true,
		"0123456789":  true,
		"abcde12345":  false,
		"ABCDE1234":   false,
		"ABCDE123456": false,
		"ABCDE-1234":  false,
		"":            false,
	} {
		if got := ValidTeamID(s); got != want {
			t.Errorf("ValidTeamID(%q) = %v, want %v", s, got, want)
		}
	}
}
