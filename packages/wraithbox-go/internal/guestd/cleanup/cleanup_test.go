package cleanup_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd/cleanup"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd/procs"
)

const project = "0123456789abcdef0123456789abcdef"

func TestMaxLeft(t *testing.T) {
	if cleanup.MaxLeft != procs.MaxLeft {
		t.Errorf("cleanup.MaxLeft = %d, procs.MaxLeft = %d", cleanup.MaxLeft, procs.MaxLeft)
	}
}

func TestFormatParse(t *testing.T) {
	for _, r := range []cleanup.Record{
		{KillID: 1, ProjectID: project},
		{KillID: 1<<64 - 1, ProjectID: project, Left: cleanup.MaxLeft},
		{KillID: 42, ProjectID: project, Left: 3},
	} {
		data, err := cleanup.Format(r)
		if err != nil {
			t.Fatal(err)
		}
		if len(data) > cleanup.MaxSize {
			t.Errorf("Format wrote %d bytes, over MaxSize", len(data))
		}
		got, err := cleanup.Parse(data)
		if err != nil || got != r {
			t.Errorf("Parse(Format(%+v)) = %+v, %v", r, got, err)
		}
	}
	want := "wb-cleanup 1\nkill 42\nproject " + project + "\nleft 3\n"
	if data, _ := cleanup.Format(cleanup.Record{KillID: 42, ProjectID: project, Left: 3}); string(data) != want {
		t.Errorf("Format = %q, want %q", data, want)
	}
	for _, bad := range []cleanup.Record{
		{ProjectID: project}, {KillID: 1, ProjectID: "x"}, {KillID: 1, ProjectID: project, Left: cleanup.MaxLeft + 1},
	} {
		if _, err := cleanup.Format(bad); err == nil {
			t.Errorf("Format(%+v) succeeded", bad)
		}
	}
}

// TestParseRefuses: Parse takes only the form Format writes.
func TestParseRefuses(t *testing.T) {
	good := "wb-cleanup 1\nkill 42\nproject " + project + "\nleft 3\n"
	for name, data := range map[string]string{
		"empty":        "",
		"no newline":   strings.TrimSuffix(good, "\n"),
		"extra line":   good + "x\n",
		"blank line":   good + "\n",
		"version 2":    strings.Replace(good, "wb-cleanup 1", "wb-cleanup 2", 1),
		"crlf":         strings.ReplaceAll(good, "\n", "\r\n"),
		"kill 0":       strings.Replace(good, "kill 42", "kill 0", 1),
		"leading zero": strings.Replace(good, "kill 42", "kill 042", 1),
		"plus sign":    strings.Replace(good, "kill 42", "kill +42", 1),
		"kill over":    strings.Replace(good, "kill 42", "kill 18446744073709551616", 1),
		"space":        strings.Replace(good, "kill 42", "kill  42", 1),
		"order":        "wb-cleanup 1\nproject " + project + "\nkill 42\nleft 3\n",
		"uppercase":    strings.Replace(good, project, strings.ToUpper(project), 1),
		"short id":     strings.Replace(good, project, project[1:], 1),
		"left over":    strings.Replace(good, "left 3", "left 1002", 1),
		"left 32 bits": strings.Replace(good, "left 3", "left 4294967296", 1),
		"left zero":    strings.Replace(good, "left 3", "left 00", 1),
		"too big":      good + strings.Repeat("x", cleanup.MaxSize),
		"trailing":     strings.Replace(good, "left 3", "left 3 ", 1),
	} {
		if r, err := cleanup.Parse([]byte(data)); err == nil {
			t.Errorf("%s: Parse = %+v", name, r)
		}
	}
}

func TestFileName(t *testing.T) {
	if got := cleanup.FileName(10000); got != "10000" {
		t.Errorf("FileName = %q", got)
	}
	if uid, err := cleanup.UIDOf("10000"); err != nil || uid != 10000 {
		t.Errorf("UIDOf = %d, %v", uid, err)
	}
	for _, bad := range []string{"", "9999", "010000", "1000000000", "10000.tmp", "-1", "x"} {
		if _, err := cleanup.UIDOf(bad); err == nil {
			t.Errorf("UIDOf(%q) succeeded", bad)
		}
	}
}

// FuzzCleanupRecordFile: Parse never panics on a record file, which
// guest root can write, and what it takes is in bounds and comes back
// the same through Format.
func FuzzCleanupRecordFile(f *testing.F) {
	f.Add([]byte("wb-cleanup 1\nkill 42\nproject " + project + "\nleft 3\n"))
	f.Add([]byte("wb-cleanup 1\nkill 18446744073709551615\nproject " + project + "\nleft 1001\n"))
	f.Add([]byte("wb-cleanup 1\nkill 0\nproject \nleft \n"))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, data []byte) {
		r, err := cleanup.Parse(data)
		if err != nil {
			return
		}
		if err := r.Check(); err != nil {
			t.Fatalf("Parse took %+v: %v", r, err)
		}
		again, err := cleanup.Format(r)
		if err != nil || !bytes.Equal(again, data) {
			t.Fatalf("Format(Parse(%q)) = %q, %v", data, again, err)
		}
	})
}
