package state

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
)

// rawDB opens path without this package, as a test fixture or to look
// at a result.
func rawDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func exec(t *testing.T, db *sql.DB, statements ...string) {
	t.Helper()
	for _, s := range statements {
		if _, err := db.ExecContext(t.Context(), s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}

func version(t *testing.T, path string) int {
	t.Helper()
	var v int
	if err := rawDB(t, path).QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func count(t *testing.T, path, table string) int {
	t.Helper()
	var n int
	if err := rawDB(t, path).QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatalf("count %s in %s: %v", table, path, err)
	}
	return n
}

func hasTable(t *testing.T, path, table string) bool {
	t.Helper()
	var n int
	err := rawDB(t, path).QueryRowContext(t.Context(),
		"SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name = ?", table).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	return n == 1
}

// fixture writes a database at version v with the given statements run
// first, closed again before the test opens it.
func fixture(t *testing.T, dir string, v int, statements ...string) string {
	t.Helper()
	path := filepath.Join(dir, FileName)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	exec(t, db, statements...)
	exec(t, db, "PRAGMA user_version = "+strconv.Itoa(v))
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func bakFiles(t *testing.T, dir string) []string {
	t.Helper()
	var names []string
	for _, b := range backups(dir) {
		names = append(names, b.name)
	}
	slices.Sort(names)
	return names
}

func mustOpen(t *testing.T, dir string, o options) *DB {
	t.Helper()
	db, err := open(t.Context(), dir, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func refusal(t *testing.T, err error, want Rule) *Refusal {
	t.Helper()
	r, ok := errors.AsType[*Refusal](err)
	if !ok {
		t.Fatalf("error %v (%T) is not a *Refusal", err, err)
	}
	if r.Rule != want {
		t.Fatalf("rule = %s, want %s (%v)", r.Rule, want, r)
	}
	return r
}

// testMigrations is a schema of two versions with rows, so an upgrade
// has data to keep.
func testMigrations() []migration {
	return []migration{
		{name: "t1", apply: func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, "CREATE TABLE t (a INTEGER)")
			return err
		}},
		{name: "t2", apply: func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, "ALTER TABLE t ADD COLUMN b INTEGER")
			return err
		}},
	}
}

func opts(m []migration) options {
	return options{migrations: m, vacuumInto: vacuumInto}
}

// C12-install-and-downloads, "Migrations": a new database is created
// and migrated to the newest version, with nothing to back up.
func TestC12NewDatabaseMigrated(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if v := version(t, db.Path()); v != len(migrations) {
		t.Errorf("version = %d, want %d", v, len(migrations))
	}
	if !hasTable(t, db.Path(), "vm_slots") {
		t.Error("vm_slots missing")
	}
	if b := bakFiles(t, dir); len(b) != 0 {
		t.Errorf("a new database was backed up: %v", b)
	}
}

// C12-install-and-downloads, "Migrations": a version 0 database with
// rows is migrated, with a backup that holds the rows.
func TestC12Version0WithRowsMigratedWithBackup(t *testing.T) {
	dir := t.TempDir()
	path := fixture(t, dir, 0, "CREATE TABLE legacy (x TEXT)", "INSERT INTO legacy VALUES ('a'), ('b')")
	mustOpen(t, dir, opts(migrations))

	if v := version(t, path); v != len(migrations) {
		t.Errorf("version = %d, want %d", v, len(migrations))
	}
	if n := count(t, path, "legacy"); n != 2 {
		t.Errorf("legacy rows = %d after the migration, want 2", n)
	}
	bak := filepath.Join(dir, BackupName(0))
	if got := bakFiles(t, dir); !slices.Equal(got, []string{BackupName(0)}) {
		t.Fatalf("backups = %v, want %s", got, BackupName(0))
	}
	if n := count(t, bak, "legacy"); n != 2 || version(t, bak) != 0 {
		t.Errorf("backup has %d rows at version %d, want 2 at 0", n, version(t, bak))
	}
}

// An upgrade from version 1 to 2 keeps the rows, and backs up version 1.
func TestUpgradeKeepsRows(t *testing.T) {
	dir := t.TempDir()
	m := testMigrations()
	db := mustOpen(t, dir, opts(m[:1]))
	exec(t, db.db, "INSERT INTO t (a) VALUES (1), (2), (3)")
	_ = db.Close()

	db = mustOpen(t, dir, opts(m))
	exec(t, db.db, "INSERT INTO t (a, b) VALUES (4, 4)")
	if v := version(t, db.Path()); v != 2 {
		t.Errorf("version = %d, want 2", v)
	}
	if n := count(t, db.Path(), "t"); n != 4 {
		t.Errorf("rows = %d, want 4", n)
	}
	bak := filepath.Join(dir, BackupName(1))
	if n := count(t, bak, "t"); n != 3 || version(t, bak) != 1 {
		t.Errorf("backup has %d rows at version %d, want 3 at 1", n, version(t, bak))
	}
}

// C12-install-and-downloads, "Migrations": a newer database refuses
// with state-db-newer, and nothing changes.
func TestC12NewerDatabaseRefused(t *testing.T) {
	dir := t.TempDir()
	path := fixture(t, dir, 7, "CREATE TABLE future (x)")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = open(t.Context(), dir, opts(testMigrations()))
	r := refusal(t, err, RuleNewer)
	if r.Version != 7 || r.Latest != 2 {
		t.Errorf("refusal versions = %d and %d, want 7 and 2", r.Version, r.Latest)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("the newer database was changed")
	}
	if b := bakFiles(t, dir); len(b) != 0 {
		t.Errorf("a newer database was backed up: %v", b)
	}
}

// C12-install-and-downloads, "Migrations": a failing migration rolls
// back, leaves user_version where it was, and refuses with
// state-db-migration-failed.
func TestC12FailedMigrationRollsBack(t *testing.T) {
	dir := t.TempDir()
	m := testMigrations()
	m[1] = migration{name: "fails", apply: func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "CREATE TABLE half_done (x)"); err != nil {
			return err
		}
		return errors.New("injected")
	}}
	_, err := open(t.Context(), dir, opts(m))
	r := refusal(t, err, RuleMigrationFailed)
	if r.Version != 1 || r.Latest != 2 {
		t.Errorf("refusal versions = %d and %d, want 1 and 2", r.Version, r.Latest)
	}
	path := filepath.Join(dir, FileName)
	if v := version(t, path); v != 1 {
		t.Errorf("version = %d after the failed migration, want 1", v)
	}
	if hasTable(t, path, "half_done") {
		t.Error("the failed migration's table was kept")
	}
}

// C12-install-and-downloads, "Migrations": a backup that can't be
// written refuses with state-db-backup-failed, and no migration runs.
func TestC12FailedBackupStopsMigration(t *testing.T) {
	dir := t.TempDir()
	path := fixture(t, dir, 0, "CREATE TABLE legacy (x)")
	o := opts(migrations)
	o.vacuumInto = func(context.Context, *sql.DB, string) error { return errors.New("disk full") }
	_, err := open(t.Context(), dir, o)
	r := refusal(t, err, RuleBackupFailed)
	if r.Version != 0 {
		t.Errorf("refusal version = %d, want 0", r.Version)
	}
	if v := version(t, path); v != 0 || hasTable(t, path, "vm_slots") {
		t.Errorf("a migration ran after the failed backup (version %d)", v)
	}
	if b := bakFiles(t, dir); len(b) != 0 {
		t.Errorf("backups = %v after a failed backup, want none", b)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "state.db.bak.tmp-*")); len(left) != 0 {
		t.Errorf("temporary copies left: %v", left)
	}
}

// C12-install-and-downloads, "Migrations": an existing .bak name is
// kept unchanged, and the migration goes on.
func TestC12ExistingBackupKept(t *testing.T) {
	dir := t.TempDir()
	path := fixture(t, dir, 0, "CREATE TABLE legacy (x)")
	bak := filepath.Join(dir, BackupName(0))
	if err := os.WriteFile(bak, []byte("earlier attempt"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustOpen(t, dir, opts(migrations))
	got, err := os.ReadFile(bak)
	if err != nil || string(got) != "earlier attempt" {
		t.Errorf("backup = %q, %v; want it unchanged", got, err)
	}
	if v := version(t, path); v != len(migrations) {
		t.Errorf("version = %d, want %d", v, len(migrations))
	}
}

// Anything but a regular file at the backup's name doesn't count as a
// kept backup: the backup fails, and no migration runs.
func TestBackupNameNotARegularFile(t *testing.T) {
	dir := t.TempDir()
	path := fixture(t, dir, 0, "CREATE TABLE legacy (x)")
	if err := os.Mkdir(filepath.Join(dir, BackupName(0)), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := open(t.Context(), dir, opts(migrations))
	refusal(t, err, RuleBackupFailed)
	if v := version(t, path); v != 0 {
		t.Errorf("version = %d after the failed backup, want 0", v)
	}
}

// A migration that a second wb-hostd ran between this one's version
// read and its transaction is skipped, not run twice.
func TestMigrationRunByAnotherStartSkipped(t *testing.T) {
	dir := t.TempDir()
	path := fixture(t, dir, 0, "CREATE TABLE legacy (x)")
	m := testMigrations()
	o := opts(m)
	o.vacuumInto = func(ctx context.Context, db *sql.DB, dst string) error {
		if err := vacuumInto(ctx, db, dst); err != nil {
			return err
		}
		// The other wb-hostd runs migration 1 while this one backs up.
		exec(t, rawDB(t, path), "CREATE TABLE t (a INTEGER)", "PRAGMA user_version = 1")
		return nil
	}
	mustOpen(t, dir, o)
	if v := version(t, path); v != 2 {
		t.Errorf("version = %d, want 2", v)
	}
}

// Only the two newest backups stay. Other files are never deleted.
func TestOnlyTwoNewestBackupsKept(t *testing.T) {
	dir := t.TempDir()
	m := testMigrations()[:1]
	for i := range 4 {
		m = append(m, migration{name: "noop" + strconv.Itoa(i), apply: func(context.Context, *sql.Tx) error { return nil }})
	}
	fixture(t, dir, 4, "CREATE TABLE t (a)")
	others := []string{"state.db.vx.bak", "notes.bak", "state.db.v1.bak.orig"}
	for _, name := range append([]string{BackupName(1), BackupName(2), BackupName(3)}, others...) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mustOpen(t, dir, opts(m))
	if got, want := bakFiles(t, dir), []string{BackupName(3), BackupName(4)}; !slices.Equal(got, want) {
		t.Errorf("backups = %v, want %v", got, want)
	}
	for _, name := range others {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s was removed: %v", name, err)
		}
	}
}

func TestLeftoverCopyRemoved(t *testing.T) {
	dir := t.TempDir()
	fixture(t, dir, 0)
	left := filepath.Join(dir, "state.db.bak.tmp-123")
	if err := os.WriteFile(left, []byte("half"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustOpen(t, dir, opts(migrations))
	if _, err := os.Stat(left); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("leftover copy kept: %v", err)
	}
}

func TestOpenRefused(t *testing.T) {
	t.Run("directory at the name", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, FileName), 0o700); err != nil {
			t.Fatal(err)
		}
		_, err := Open(t.Context(), dir)
		refusal(t, err, RuleOpenFailed)
	})
	t.Run("missing directory", func(t *testing.T) {
		_, err := Open(t.Context(), filepath.Join(t.TempDir(), "missing"))
		refusal(t, err, RuleOpenFailed)
	})
	t.Run("question mark", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "a?b")
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Skipf("the file system takes no '?': %v", err)
		}
		_, err := Open(t.Context(), dir)
		refusal(t, err, RuleOpenFailed)
	})
	t.Run("negative version", func(t *testing.T) {
		dir := t.TempDir()
		path := fixture(t, dir, -1, "CREATE TABLE legacy (x)")
		_, err := Open(t.Context(), dir)
		if r := refusal(t, err, RuleOpenFailed); r.Version != -1 {
			t.Errorf("refusal version = %d, want -1", r.Version)
		}
		if b := bakFiles(t, dir); len(b) != 0 {
			t.Errorf("backups = %v, want none", b)
		}
		if v := version(t, path); v != -1 || hasTable(t, path, "vm_slots") {
			t.Errorf("the database was migrated (version %d)", v)
		}
	})
	t.Run("not a database", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, FileName), bytes.Repeat([]byte("x"), 4096), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := Open(t.Context(), dir)
		refusal(t, err, RuleOpenFailed)
	})
}

func TestNextGeneration(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	for _, want := range []uint64{1, 2, 3} {
		if g, err := db.NextGeneration(ctx, "macos-work"); err != nil || g != want {
			t.Fatalf("NextGeneration = %d, %v; want %d", g, err, want)
		}
	}
	if g, err := db.NextGeneration(ctx, "macos-isolated"); err != nil || g != 1 {
		t.Errorf("another slot's first generation = %d, %v; want 1", g, err)
	}
	for _, slot := range []string{"", string(bytes.Repeat([]byte("s"), 65))} {
		if _, err := db.NextGeneration(ctx, slot); !errors.Is(err, errSlotName) {
			t.Errorf("slot %q: err = %v, want errSlotName", slot, err)
		}
	}
	_ = db.Close()

	db = mustOpen(t, dir, opts(migrations))
	if g, err := db.NextGeneration(ctx, "macos-work"); err != nil || g != 4 {
		t.Errorf("after a restart NextGeneration = %d, %v; want 4", g, err)
	}
}
