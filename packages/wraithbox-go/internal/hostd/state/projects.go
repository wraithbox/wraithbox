package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// createProjects is the projects migration: one row per registered
// project (S05-04-naming). The id is the project's identity in every
// other record and never changes. The key (realm and canonical path of
// the common directory) is unique, and a move changes it. Placement and
// config_trust are kept here (S09-03-project-settings, "Settings
// contents"), and the identity is the recorded URL set and root commits
// as JSON arrays of strings.
func createProjects(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `CREATE TABLE projects (
		id TEXT PRIMARY KEY NOT NULL CHECK (length(id) = 32 AND id NOT GLOB '*[^0-9a-f]*'),
		realm TEXT NOT NULL CHECK (realm IN ('native')),
		key_path TEXT NOT NULL CHECK (length(key_path) BETWEEN 1 AND 4096),
		name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 64),
		placement TEXT NOT NULL CHECK (placement IN ('work', 'isolated')),
		config_trust INTEGER NOT NULL CHECK (config_trust IN (0, 1)),
		urls TEXT NOT NULL CHECK (json_valid(urls) AND json_type(urls) = 'array'),
		root_commits TEXT NOT NULL CHECK (json_valid(root_commits) AND json_type(root_commits) = 'array'),
		created_unix INTEGER NOT NULL,
		UNIQUE (realm, key_path)
	) STRICT`)
	return err
}

// The placements a project row holds.
const (
	PlacementWork     = "work"
	PlacementIsolated = "isolated"
)

// Project is a row of the projects table.
type Project struct {
	// ID is 32 lowercase hex characters, from crypto/rand.
	ID string
	// Realm and KeyPath are the project's key.
	Realm   string
	KeyPath string
	Name    string
	// Placement is PlacementWork or PlacementIsolated.
	Placement   string
	ConfigTrust bool
	// URLs and RootCommits are the recorded identity, sorted and without
	// duplicates. Both empty is an empty identity.
	URLs        []string
	RootCommits []string
	Created     time.Time
}

// The project errors.
var (
	// ErrProjectIDTaken: an insert with an id another row holds. The
	// caller draws a new id.
	ErrProjectIDTaken = errors.New("state: the project id is taken")
	// ErrProjectKeyTaken: an insert or a move onto a key another row
	// holds.
	ErrProjectKeyTaken = errors.New("state: the project key is taken")
	// ErrProjectNotFound: an update or delete of an id without a row.
	ErrProjectNotFound = errors.New("state: no project with that id")
)

// ProjectTx reads and writes projects in one transaction.
type ProjectTx struct {
	ctx context.Context
	tx  *sql.Tx
}

// Projects runs fn in one transaction, which commits when fn returns nil
// and rolls back otherwise. The transaction begins IMMEDIATE (dsn), so a
// read in fn sees no write of another wb-hostd before fn's own write.
func (d *DB) Projects(ctx context.Context, fn func(*ProjectTx) error) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(&ProjectTx{ctx: ctx, tx: tx}); err != nil {
		return err
	}
	return tx.Commit()
}

const projectColumns = `id, realm, key_path, name, placement, config_trust, urls, root_commits, created_unix`

// ByID returns the project with id, and whether there is one.
func (p *ProjectTx) ByID(id string) (Project, bool, error) {
	return p.one(`SELECT `+projectColumns+` FROM projects WHERE id = ?`, id)
}

// ByKey returns the project with the key, and whether there is one.
func (p *ProjectTx) ByKey(realm, keyPath string) (Project, bool, error) {
	return p.one(`SELECT `+projectColumns+` FROM projects WHERE realm = ? AND key_path = ?`, realm, keyPath)
}

// ByName returns the projects named name, oldest first. Names aren't
// unique.
func (p *ProjectTx) ByName(name string) ([]Project, error) {
	return p.many(`SELECT `+projectColumns+` FROM projects WHERE name = ? ORDER BY created_unix, id`, name)
}

// All returns every project, oldest first.
func (p *ProjectTx) All() ([]Project, error) {
	return p.many(`SELECT ` + projectColumns + ` FROM projects ORDER BY created_unix, id`)
}

// Insert adds row. An id or a key another row holds fails with
// ErrProjectIDTaken or ErrProjectKeyTaken, checked in this transaction
// before the insert.
func (p *ProjectTx) Insert(row Project) error {
	if _, ok, err := p.ByID(row.ID); err != nil || ok {
		return orTaken(err, ok, ErrProjectIDTaken)
	}
	if _, ok, err := p.ByKey(row.Realm, row.KeyPath); err != nil || ok {
		return orTaken(err, ok, ErrProjectKeyTaken)
	}
	urls, roots, err := encodeIdentity(row)
	if err != nil {
		return err
	}
	_, err = p.tx.ExecContext(p.ctx, `INSERT INTO projects (`+projectColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		row.ID, row.Realm, row.KeyPath, row.Name, row.Placement, boolInt(row.ConfigTrust), urls, roots, row.Created.Unix())
	return err
}

// Update writes every column of row but the id and the created time to
// the row with its id. A key another row holds fails with
// ErrProjectKeyTaken, and an id without a row with ErrProjectNotFound.
func (p *ProjectTx) Update(row Project) error {
	other, ok, err := p.ByKey(row.Realm, row.KeyPath)
	if err != nil {
		return err
	}
	if ok && other.ID != row.ID {
		return ErrProjectKeyTaken
	}
	urls, roots, err := encodeIdentity(row)
	if err != nil {
		return err
	}
	res, err := p.tx.ExecContext(p.ctx, `UPDATE projects SET realm = ?, key_path = ?, name = ?, placement = ?,
		config_trust = ?, urls = ?, root_commits = ? WHERE id = ?`,
		row.Realm, row.KeyPath, row.Name, row.Placement, boolInt(row.ConfigTrust), urls, roots, row.ID)
	if err != nil {
		return err
	}
	return oneRow(res, ErrProjectNotFound)
}

// Delete removes the row with id. An id without a row fails with
// ErrProjectNotFound.
func (p *ProjectTx) Delete(id string) error {
	res, err := p.tx.ExecContext(p.ctx, `DELETE FROM projects WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return oneRow(res, ErrProjectNotFound)
}

func orTaken(err error, taken bool, sentinel error) error {
	if err != nil {
		return err
	}
	if taken {
		return sentinel
	}
	return nil
}

func (p *ProjectTx) one(query string, args ...any) (Project, bool, error) {
	rows, err := p.many(query, args...)
	if err != nil || len(rows) == 0 {
		return Project{}, false, err
	}
	return rows[0], true, nil
}

func (p *ProjectTx) many(query string, args ...any) ([]Project, error) {
	rows, err := p.tx.QueryContext(p.ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Project
	for rows.Next() {
		row, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func scanProject(rows *sql.Rows) (Project, error) {
	var (
		row         Project
		trust       int64
		urls, roots string
		createdUnix int64
	)
	err := rows.Scan(&row.ID, &row.Realm, &row.KeyPath, &row.Name, &row.Placement, &trust, &urls, &roots, &createdUnix)
	if err != nil {
		return Project{}, err
	}
	row.ConfigTrust = trust == 1
	row.Created = time.Unix(createdUnix, 0).UTC()
	if err := json.Unmarshal([]byte(urls), &row.URLs); err != nil {
		return Project{}, fmt.Errorf("state: project %s has unreadable urls: %w", row.ID, err)
	}
	if err := json.Unmarshal([]byte(roots), &row.RootCommits); err != nil {
		return Project{}, fmt.Errorf("state: project %s has unreadable root commits: %w", row.ID, err)
	}
	// An empty list reads as nil, as it was before it was written.
	if len(row.URLs) == 0 {
		row.URLs = nil
	}
	if len(row.RootCommits) == 0 {
		row.RootCommits = nil
	}
	return row, nil
}

func encodeIdentity(row Project) (string, string, error) {
	urls, err := json.Marshal(nonNil(row.URLs))
	if err != nil {
		return "", "", err
	}
	roots, err := json.Marshal(nonNil(row.RootCommits))
	if err != nil {
		return "", "", err
	}
	return string(urls), string(roots), nil
}

// nonNil gives an empty list for nil, so the column holds [] and not
// null.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
