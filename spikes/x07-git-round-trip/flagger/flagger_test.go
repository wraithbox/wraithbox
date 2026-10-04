package flagger

import "testing"

func FuzzParseRaw(f *testing.F) {
	f.Add([]byte(":100644 100755 aaaa bbbb M\x00script.sh\x00"))
	f.Add([]byte(":000000 120000 0000 cccc A\x00link\x00:100644 100644 a b M\x00.github/workflows/ci.yml\x00"))
	f.Fuzz(func(t *testing.T, b []byte) {
		cs, err := ParseRaw(b)
		if err != nil {
			return
		}
		for i := range cs {
			// NewOID shorter than 12 must not panic in Flag.
			if len(cs[i].NewOID) < 12 {
				cs[i].NewMode = "100644"
			}
		}
		_ = Flag(cs, func(string) ([]byte, error) { return b, nil })
	})
}

func TestFlag(t *testing.T) {
	z := zero
	cs := []Change{
		{OldMode: "000000", NewMode: "100644", OldOID: z, NewOID: "1111111111111111111111111111111111111111", Status: 'A', Path: ".github/workflows/x.yml"},
		{OldMode: "100644", NewMode: "100755", OldOID: "2", NewOID: "3333333333333333333333333333333333333333", Status: 'M', Path: "tools/run.sh"},
		{OldMode: "000000", NewMode: "120000", OldOID: z, NewOID: "4444444444444444444444444444444444444444", Status: 'A', Path: "a/link"},
		{OldMode: "100644", NewMode: "100644", OldOID: "5", NewOID: "6666666666666666666666666666666666666666", Status: 'M', Path: "src/main.go"},
	}
	read := func(oid string) ([]byte, error) {
		if oid[0] == '4' {
			return []byte("../../etc/passwd"), nil
		}
		return nil, nil
	}
	got := Flag(cs, read)
	want := map[string]string{".github/workflows/x.yml": "ci", "tools/run.sh": "executable", "a/link": "symlink-outside"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for _, f := range got {
		if want[f.Path] != f.Rule {
			t.Errorf("%s: got %s want %s", f.Path, f.Rule, want[f.Path])
		}
	}
}
