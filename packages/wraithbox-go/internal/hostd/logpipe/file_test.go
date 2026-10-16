package logpipe

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// Rotation at 10 MiB keeps 5 files, and a record is never split across
// two (S04-08-records-and-bounds, "Logs and health").
func TestRotation(t *testing.T) {
	dir := t.TempDir()
	lf, err := OpenFile(dir, "wb-netd")
	if err != nil {
		t.Fatal(err)
	}
	const recSize = 64 << 10
	perFile := RotateSize / recSize
	total := perFile*(KeepFiles+2) + 3
	for i := range total {
		rec := bytes.Repeat([]byte{'a' + byte(i%26)}, recSize-1)
		rec = append(rec, '\n')
		if _, err := lf.Write(rec); err != nil {
			t.Fatal(err)
		}
	}
	if err := lf.Close(); err != nil {
		t.Fatal(err)
	}
	checkRotated(t, dir, recSize)

	// The current file holds the last records, .1 the ones before.
	cur, err := os.ReadFile(filepath.Join(dir, "wb-netd.log"))
	if err != nil {
		t.Fatal(err)
	}
	if n := len(cur) / recSize; n != 3 || cur[len(cur)-2] != 'a'+byte((total-1)%26) {
		t.Errorf("current file holds %d records ending in %q, want 3 ending in the last", n, cur[len(cur)-2])
	}
}

// checkRotated: dir holds the current file and 4 rotated ones, each at
// most RotateSize and made of whole records.
func checkRotated(t *testing.T, dir string, recSize int64) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	want := []string{"wb-netd.log", "wb-netd.log.1", "wb-netd.log.2", "wb-netd.log.3", "wb-netd.log.4"}
	if !slices.Equal(names, want) {
		t.Fatalf("files = %v, want %v", names, want)
	}
	for _, name := range names {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() > RotateSize || info.Size()%recSize != 0 {
			t.Errorf("%s is %d bytes, want at most %d in whole records", name, info.Size(), RotateSize)
		}
	}
}

func TestReopenAppends(t *testing.T) {
	dir := t.TempDir()
	for i := range 2 {
		lf, err := OpenFile(dir, "wb-hostd")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fmt.Fprintf(lf, "record %d\n", i); err != nil {
			t.Fatal(err)
		}
		_ = lf.Close()
	}
	got, err := os.ReadFile(filepath.Join(dir, "wb-hostd.log"))
	if err != nil || string(got) != "record 0\nrecord 1\n" {
		t.Errorf("file = %q, %v", got, err)
	}
}

func TestWriteAfterClose(t *testing.T) {
	lf, err := OpenFile(t.TempDir(), "wb-hostd")
	if err != nil {
		t.Fatal(err)
	}
	_ = lf.Close()
	if _, err := lf.Write([]byte("x\n")); !errors.Is(err, os.ErrClosed) {
		t.Errorf("Write after Close = %v, want os.ErrClosed", err)
	}
}

// Copy into a File, end to end.
func TestCopyToFile(t *testing.T) {
	dir := t.TempDir()
	lf, err := OpenFile(dir, "wb-vmd-work")
	if err != nil {
		t.Fatal(err)
	}
	if err := copyWith(bytes.NewReader([]byte("one\ntwo\n")), lf, "wb-vmd-work", newFakeClock().now); err != nil {
		t.Fatal(err)
	}
	_ = lf.Close()
	out, err := os.ReadFile(filepath.Join(dir, "wb-vmd-work.log"))
	if err != nil {
		t.Fatal(err)
	}
	if recs := decode(t, out); len(recs) != 2 || recs[1].Line != "two" {
		t.Errorf("records = %+v", recs)
	}
}
