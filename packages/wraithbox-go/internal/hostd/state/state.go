// Package state opens wb-hostd's database, <data>/state.db, and migrates
// it to the schema this build knows (S17-05-upgrades-and-migrations,
// "Migrations", and S04-00-architecture, "Host state").
//
// The schema version is PRAGMA user_version. The migrations are a
// numbered, forward-only list compiled in (migrations.go). Each runs in
// one transaction that also sets the new version. Before the first
// pending migration, Open writes a backup, state.db.v<N>.bak, next to
// the database. A failed backup, a failed migration and a database newer
// than this build each give a *Refusal, which puts wb-hostd in refusing
// mode (S17-05-upgrades-and-migrations, "Refusing mode").
//
// Only wb-hostd opens this database. The database file and every backup
// are mode 600.
package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	// The cgo-free SQLite driver of S10-01-libraries, "State".
	_ "modernc.org/sqlite"
)

// FileName is the database's name in <data>.
const FileName = "state.db"

// busyTimeoutMS is how long a statement waits for a lock. Only wb-hostd
// opens the database, so a wait means a second wb-hostd.
const busyTimeoutMS = 5000

// Rule names the rule behind a refusal, for the log and for the answer
// wb gets in refusing mode.
type Rule string

// The rules of this package. Each refusal has its own
// (S17-05-upgrades-and-migrations, "Refusing mode").
const (
	// RuleNewer: the database's version is above the newest this build
	// knows. Nothing is changed.
	RuleNewer Rule = "state-db-newer"
	// RuleBackupFailed: the backup before the first pending migration
	// couldn't be written. No migration runs.
	RuleBackupFailed Rule = "state-db-backup-failed"
	// RuleMigrationFailed: a migration failed. Its transaction is rolled
	// back, so the version stays at the last one that succeeded.
	RuleMigrationFailed Rule = "state-db-migration-failed"
	// RuleOpenFailed: the database can't be opened or its version can't
	// be read.
	RuleOpenFailed Rule = "state-db-open-failed"
)

// Refusal is the error of every refusal in this package. It names the
// versions and the newest backup (S17-05-upgrades-and-migrations,
// "Refusing mode", NFR06-explained-refusals).
type Refusal struct {
	Rule Rule
	Path string
	// Version is the database's version when the refusal was made, -1
	// when it couldn't be read.
	Version int
	// Latest is the newest version this build knows.
	Latest int
	// Backup is the path of the newest backup, empty when there is none.
	Backup string

	msg string
	err error
}

func (r *Refusal) Error() string { return r.msg }

// Unwrap returns the cause, such as SQLite's error.
func (r *Refusal) Unwrap() error { return r.err }

// DB is the open, migrated database.
type DB struct {
	db   *sql.DB
	path string
}

// Close closes the database.
func (d *DB) Close() error { return d.db.Close() }

// Path returns the database file's path.
func (d *DB) Path() string { return d.path }

// Open opens <dataDir>/state.db, creating it mode 600 when it is
// missing, and migrates it to the newest version. dataDir must exist.
// A refusal is a *Refusal, and is logged with its rule.
func Open(ctx context.Context, dataDir string) (*DB, error) {
	return open(ctx, dataDir, options{migrations: migrations, vacuumInto: vacuumInto})
}

// options lets a test inject migrations and a failing backup.
type options struct {
	migrations []migration
	vacuumInto func(ctx context.Context, db *sql.DB, dst string) error
}

func open(ctx context.Context, dataDir string, o options) (*DB, error) {
	path := filepath.Join(dataDir, FileName)
	db, r := openAndMigrate(ctx, dataDir, path, o)
	if r != nil {
		r.Path = path
		r.Latest = len(o.migrations)
		logRefusal(ctx, r)
		return nil, r
	}
	return db, nil
}

func openAndMigrate(ctx context.Context, dataDir, path string, o options) (*DB, *Refusal) {
	created, r := ensureFile(path)
	if r != nil {
		return nil, r
	}
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, openFailed(path, err)
	}
	// One connection: wb-hostd's writes are short, and SQLite takes one
	// writer at a time anyway. It also keeps the DSN's pragmas on every
	// statement.
	db.SetMaxOpenConns(1)
	d := &DB{db: db, path: path}
	if r := d.migrate(ctx, dataDir, created, o); r != nil {
		_ = db.Close()
		return nil, r
	}
	return d, nil
}

// dsn builds the driver's name for path. The driver reads what follows
// the first '?' as parameters, which is why ensureFile refuses a path
// with one.
func dsn(path string) string {
	return fmt.Sprintf("%s?_pragma=busy_timeout(%d)&_pragma=foreign_keys(1)&_txlock=immediate", path, busyTimeoutMS)
}

// ensureFile creates the database file mode 600 when it is missing, and
// reports whether it did. An existing file must be a regular file, and
// is set to mode 600.
func ensureFile(path string) (bool, *Refusal) {
	if strings.ContainsRune(path, '?') {
		return false, openFailed(path, errors.New("the path contains '?'"))
	}
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return false, openFailed(path, err)
		}
		if err := f.Close(); err != nil {
			return false, openFailed(path, err)
		}
		return true, nil
	}
	if err != nil {
		return false, openFailed(path, err)
	}
	if !info.Mode().IsRegular() {
		return false, openFailed(path, fmt.Errorf("not a regular file (%s)", info.Mode().Type()))
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return false, openFailed(path, err)
	}
	return false, nil
}

func openFailed(path string, err error) *Refusal {
	return &Refusal{
		Rule:    RuleOpenFailed,
		Version: -1,
		err:     err,
		msg:     fmt.Sprintf("the state database %s can't be opened: %v", path, err),
	}
}

// migrate brings the database from its version to the newest. created
// says this start made the file, so there is nothing to back up.
func (d *DB) migrate(ctx context.Context, dataDir string, created bool, o options) *Refusal {
	version, err := d.userVersion(ctx)
	if err != nil {
		return openFailed(d.path, err)
	}
	latest := len(o.migrations)
	if version < 0 {
		r := openFailed(d.path, fmt.Errorf("user_version is %d, below 0", version))
		r.Version = version
		return r
	}
	if version > latest {
		return d.newer(dataDir, version, latest)
	}
	if version == latest {
		logMigrated(ctx, d.path, version, version, "")
		return nil
	}
	backup := ""
	if !created {
		var r *Refusal
		if backup, r = writeBackup(ctx, d.db, dataDir, version, o.vacuumInto); r != nil {
			return r
		}
	}
	for v := version; v < latest; v++ {
		err := d.apply(ctx, v+1, latest, o.migrations[v])
		if n, ok := errors.AsType[*newerError](err); ok {
			return d.newer(dataDir, n.version, latest)
		}
		if err != nil {
			return &Refusal{
				Rule:    RuleMigrationFailed,
				Version: v,
				Backup:  newestBackup(dataDir),
				err:     err,
				msg: fmt.Sprintf("the state database %s failed to migrate from version %d to %d (%s): %v; it stays at version %d",
					d.path, v, v+1, o.migrations[v].name, err, v),
			}
		}
	}
	logMigrated(ctx, d.path, version, latest, backup)
	return nil
}

// newer refuses a database at version, above latest.
func (d *DB) newer(dataDir string, version, latest int) *Refusal {
	return &Refusal{
		Rule:    RuleNewer,
		Version: version,
		Backup:  newestBackup(dataDir),
		msg: fmt.Sprintf("the state database %s is version %d, newer than version %d that this wb-hostd knows: install the newer Wraith Box again",
			d.path, version, latest),
	}
}

// newerError is apply's error for a version that a newer wb-hostd moved
// past latest while this one started.
type newerError struct{ version int }

func (e *newerError) Error() string {
	return fmt.Sprintf("the version is %d, newer than this wb-hostd knows", e.version)
}

func (d *DB) userVersion(ctx context.Context) (int, error) {
	var v int
	err := d.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v)
	return v, err
}

// apply runs one migration and sets user_version to to, in one
// transaction. On any error the transaction rolls back, and the version
// stays as it was. The transaction begins IMMEDIATE, so it holds the
// write lock from its first statement. It reads the version again under
// that lock, and skips a migration that a second wb-hostd ran in the
// meantime. A version above latest is a *newerError.
func (d *DB) apply(ctx context.Context, to, latest int, m migration) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var current int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil {
		return err
	}
	if current > latest {
		return &newerError{version: current}
	}
	if current >= to {
		slog.Default().LogAttrs(ctx, slog.LevelInfo, "state database migration already run",
			slog.String("path", d.path), slog.String("migration", m.name), slog.Int("version", current))
		return nil
	}
	if current != to-1 {
		return fmt.Errorf("the version changed to %d before migration %s", current, m.name)
	}
	if err := m.apply(ctx, tx); err != nil {
		return err
	}
	// PRAGMA takes no bound parameter. to is an int this package
	// counted, never input.
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", to)); err != nil {
		return err
	}
	return tx.Commit()
}

func logRefusal(ctx context.Context, r *Refusal) {
	slog.Default().LogAttrs(ctx, slog.LevelError, "state database refused",
		slog.String("rule", string(r.Rule)), slog.String("decision", "refuse"),
		slog.String("path", r.Path), slog.Int("version", r.Version), slog.Int("latest", r.Latest),
		slog.String("backup", r.Backup), slog.String("reason", r.msg))
}

func logMigrated(ctx context.Context, path string, from, to int, backup string) {
	slog.Default().LogAttrs(ctx, slog.LevelInfo, "state database ready",
		slog.String("decision", "accept"), slog.String("path", path),
		slog.Int("from", from), slog.Int("version", to), slog.String("backup", backup))
}
