package background

import (
	"slices"
	"testing"
)

// TestLists checks that every shipped list loads: canonical names that
// pass the name form rule, each once.
func TestLists(t *testing.T) {
	releases := Releases()
	if len(releases) == 0 {
		t.Fatal("no lists")
	}
	for _, r := range releases {
		names, err := List(r)
		if err != nil {
			t.Fatal(err)
		}
		if len(names) == 0 {
			t.Errorf("%s: empty list", r)
		}
	}
}

// TestMacOS27 checks the first macOS list: the 14 names of
// X03-network-path (I486, decision 1).
func TestMacOS27(t *testing.T) {
	names, err := List("macos-27")
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 14 {
		t.Fatalf("%d names, want 14", len(names))
	}
	var got []string
	for _, n := range names {
		got = append(got, n.String())
	}
	for _, want := range []string{"init.push.apple.com", "push.apple.com", "gateway.icloud.com", "time.apple.com"} {
		if !slices.Contains(got, want) {
			t.Errorf("%s isn't listed", want)
		}
	}
}

func TestListRefuses(t *testing.T) {
	if _, err := List("macos-1"); err == nil {
		t.Error("List took a release without a list")
	}
	for _, data := range []string{
		"Push.apple.com\n",
		"push.apple.com.\n",
		"*.apple.com\n",
		"10.0.0.1\n",
		"push.apple.com\npush.apple.com\n",
		"bad\x00name\n",
	} {
		if _, err := parse("test", data); err == nil {
			t.Errorf("parse took %q", data)
		}
	}
	names, err := parse("test", "# comment\n\n  time.apple.com  \n")
	if err != nil || len(names) != 1 || names[0].String() != "time.apple.com" {
		t.Errorf("parse = %v, %v", names, err)
	}
}
