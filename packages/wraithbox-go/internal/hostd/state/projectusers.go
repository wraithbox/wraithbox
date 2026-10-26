package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/projectuser"
)

// createProjectUsers is the project-users migration (S06-03-image-updates,
// "Host state per project user", "Allocation"; S05-04-naming, "The
// project user"):
//
//   - project_user_ids is one row, the counter every UID and GID comes
//     from. It starts at projectuser.FirstID and never goes down: no
//     statement of this package lowers it or deletes the row.
//   - project_users is one row per project per data disk, keyed by the
//     disk's slot and volume UUID, so a new data disk gives each project
//     a new user. UID and GID are one number. A row is deleted only by
//     QueueUserRemovals.
//   - user_removals is the pending removals of project users, which
//     survive restarts until the guest answers REMOVED.
func createProjectUsers(ctx context.Context, tx *sql.Tx) error {
	for _, s := range []string{
		`CREATE TABLE project_user_ids (
			one INTEGER PRIMARY KEY NOT NULL CHECK (one = 1),
			next_id INTEGER NOT NULL CHECK (next_id >= 10000)
		) STRICT`,
		`INSERT INTO project_user_ids (one, next_id) VALUES (1, 10000)`,
		`CREATE TABLE project_users (
			project_id TEXT NOT NULL REFERENCES projects (id),
			slot TEXT NOT NULL CHECK (length(slot) BETWEEN 1 AND 64),
			volume_uuid TEXT NOT NULL CHECK (length(volume_uuid) = 36),
			uid INTEGER NOT NULL UNIQUE CHECK (uid >= 10000),
			gid INTEGER NOT NULL CHECK (gid = uid),
			user_name TEXT NOT NULL UNIQUE CHECK (length(user_name) BETWEEN 1 AND 64),
			group_name TEXT NOT NULL CHECK (group_name = user_name),
			user_generated_uid TEXT NOT NULL UNIQUE CHECK (length(user_generated_uid) = 36),
			group_generated_uid TEXT NOT NULL UNIQUE CHECK (length(group_generated_uid) = 36
				AND group_generated_uid != user_generated_uid),
			home_path TEXT NOT NULL CHECK (length(home_path) BETWEEN 1 AND 1024),
			PRIMARY KEY (project_id, slot, volume_uuid)
		) STRICT`,
		`CREATE TABLE user_removals (
			slot TEXT NOT NULL CHECK (length(slot) BETWEEN 1 AND 64),
			uid INTEGER NOT NULL CHECK (uid >= 10000),
			project_id TEXT NOT NULL CHECK (length(project_id) = 32),
			gid INTEGER NOT NULL,
			home_path TEXT NOT NULL CHECK (length(home_path) BETWEEN 1 AND 1024),
			queued_unix INTEGER NOT NULL,
			PRIMARY KEY (slot, uid)
		) STRICT`,
	} {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return err
		}
	}
	return nil
}

// ProjectUser is a project's user on one data disk.
type ProjectUser struct {
	ProjectID string
	// Slot and VolumeUUID name the data disk.
	Slot       string
	VolumeUUID string
	// UID and GID are one number from the counter.
	UID       uint32
	GID       uint32
	UserName  string
	GroupName string
	// UserGeneratedUID and GroupGeneratedUID are wb-hostd's own, never a
	// value the guest reported.
	UserGeneratedUID  string
	GroupGeneratedUID string
	HomePath          string
}

// UserRemoval is a pending removal of a removed project's user.
type UserRemoval struct {
	Slot      string
	ProjectID string
	UID       uint32
	GID       uint32
	HomePath  string
	Queued    time.Time
}

// ErrUserIDsExhausted is the error of an allocation past
// projectuser.LastID, under the rule projectuser.RuleIDsExhausted.
var ErrUserIDsExhausted = errors.New("state: no project user IDs are left on this host (" + projectuser.RuleIDsExhausted + ")")

const projectUserColumns = `project_id, slot, volume_uuid, uid, gid, user_name, group_name,
	user_generated_uid, group_generated_uid, home_path`

// ProjectUser returns projectID's user on slot's data disk, and makes
// one when there is none: it takes the next number of the counter, draws
// two GeneratedUIDs, and commits both in one transaction before it
// returns, so before the guest hears of the user (S06-03-image-updates,
// "Allocation"). created says it made the user. A slot without a
// formatted data disk fails with ErrDataDiskNotRecorded, a project
// without a row with ErrProjectNotFound, and a counter past
// projectuser.LastID with ErrUserIDsExhausted.
func (d *DB) ProjectUser(ctx context.Context, projectID, slot string) (ProjectUser, bool, error) {
	if slot == "" || len(slot) > 64 {
		return ProjectUser{}, false, errSlotName
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return ProjectUser{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	volume, err := formattedVolume(ctx, tx, slot)
	if err != nil {
		return ProjectUser{}, false, err
	}
	users, err := queryUsers(ctx, tx, `SELECT `+projectUserColumns+` FROM project_users
		WHERE project_id = ? AND slot = ? AND volume_uuid = ?`, projectID, slot, volume)
	if err != nil || len(users) == 1 {
		return firstUser(users), false, err
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM projects WHERE id = ?`, projectID).Scan(&n); err != nil {
		return ProjectUser{}, false, err
	}
	if n != 1 {
		return ProjectUser{}, false, ErrProjectNotFound
	}
	u, err := allocate(ctx, tx, projectID, slot, volume)
	if err != nil {
		return ProjectUser{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return ProjectUser{}, false, err
	}
	return u, true, nil
}

func firstUser(users []ProjectUser) ProjectUser {
	if len(users) == 0 {
		return ProjectUser{}
	}
	return users[0]
}

// formattedVolume returns the volume UUID of slot's formatted data disk.
func formattedVolume(ctx context.Context, tx *sql.Tx, slot string) (string, error) {
	var volume string
	err := tx.QueryRowContext(ctx, `SELECT volume_uuid FROM data_disks WHERE slot = ? AND formatted = 1`, slot).Scan(&volume)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrDataDiskNotRecorded
	}
	return volume, err
}

// allocate takes the counter's number for a new user of projectID on the
// disk, moves the counter past it, and inserts the user's row, in tx.
func allocate(ctx context.Context, tx *sql.Tx, projectID, slot, volume string) (ProjectUser, error) {
	var next int64
	if err := tx.QueryRowContext(ctx, `SELECT next_id FROM project_user_ids WHERE one = 1`).Scan(&next); err != nil {
		return ProjectUser{}, err
	}
	// SEC08-proj-isolation: the counter only goes up, so no number is
	// handed out twice. Past the ceiling nothing is handed out.
	if next < int64(projectuser.FirstID) || next > int64(projectuser.LastID) {
		return ProjectUser{}, ErrUserIDsExhausted
	}
	id := uint32(next)
	if _, err := tx.ExecContext(ctx, `UPDATE project_user_ids SET next_id = ? WHERE one = 1 AND next_id = ?`,
		next+1, next); err != nil {
		return ProjectUser{}, err
	}
	name := projectuser.UserName(id)
	u := ProjectUser{
		ProjectID: projectID, Slot: slot, VolumeUUID: volume, UID: id, GID: id,
		UserName: name, GroupName: name, HomePath: projectuser.HomePath(name),
		UserGeneratedUID: projectuser.NewGeneratedUID(), GroupGeneratedUID: projectuser.NewGeneratedUID(),
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO project_users (`+projectUserColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ProjectID, u.Slot, u.VolumeUUID, int64(u.UID), int64(u.GID), u.UserName, u.GroupName,
		u.UserGeneratedUID, u.GroupGeneratedUID, u.HomePath)
	if err != nil {
		return ProjectUser{}, err
	}
	return u, nil
}

// ProjectUsers returns every project user on slot's formatted data disk,
// by UID. A slot without one has none.
func (d *DB) ProjectUsers(ctx context.Context, slot string) ([]ProjectUser, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT `+projectUserColumns+` FROM project_users
		WHERE slot = ? AND volume_uuid = (SELECT volume_uuid FROM data_disks WHERE slot = ? AND formatted = 1)
		ORDER BY uid`, slot, slot)
	if err != nil {
		return nil, err
	}
	return scanUsers(rows)
}

// userID returns v as a project user ID, and whether it is one: in the
// range of projectuser.
func userID(v int64) (uint32, bool) {
	if v < int64(projectuser.FirstID) || v > int64(projectuser.LastID) {
		return 0, false
	}
	return uint32(v), true
}

// queryer is a *sql.Tx or a *sql.DB.
type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func queryUsers(ctx context.Context, q queryer, query string, args ...any) ([]ProjectUser, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return scanUsers(rows)
}

func scanUsers(rows *sql.Rows) ([]ProjectUser, error) {
	defer func() { _ = rows.Close() }()
	var out []ProjectUser
	for rows.Next() {
		var (
			u        ProjectUser
			uid, gid int64
		)
		err := rows.Scan(&u.ProjectID, &u.Slot, &u.VolumeUUID, &uid, &gid, &u.UserName, &u.GroupName,
			&u.UserGeneratedUID, &u.GroupGeneratedUID, &u.HomePath)
		if err != nil {
			return nil, err
		}
		id, ok := userID(uid)
		if !ok || gid != uid {
			return nil, fmt.Errorf("state: project user of %s has UID %d and GID %d", u.ProjectID, uid, gid)
		}
		u.UID, u.GID = id, id
		out = append(out, u)
	}
	return out, rows.Err()
}

// QueueUserRemovals is the host half of removing projectID's users
// (S05-04-naming, "The project user"). In the caller's transaction, it
// deletes the project's user rows, and adds a pending removal for each,
// one per data disk the project had a user on. So the host-side state
// is gone before any request goes to a guest, and the IDs stay used
// because the counter never hands them out again. The project's own row
// can be deleted after it in the same transaction. It returns the
// removals it added.
func (p *ProjectTx) QueueUserRemovals(projectID string, at time.Time) ([]UserRemoval, error) {
	users, err := queryUsers(p.ctx, p.tx, `SELECT `+projectUserColumns+` FROM project_users
		WHERE project_id = ? ORDER BY slot, uid`, projectID)
	if err != nil {
		return nil, err
	}
	out := make([]UserRemoval, 0, len(users))
	for _, u := range users {
		r := UserRemoval{Slot: u.Slot, ProjectID: u.ProjectID, UID: u.UID, GID: u.GID, HomePath: u.HomePath, Queued: at.UTC()}
		_, err := p.tx.ExecContext(p.ctx, `INSERT INTO user_removals (slot, uid, project_id, gid, home_path, queued_unix)
			VALUES (?, ?, ?, ?, ?, ?)`, r.Slot, int64(r.UID), r.ProjectID, int64(r.GID), r.HomePath, at.Unix())
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if _, err := p.tx.ExecContext(p.ctx, `DELETE FROM project_users WHERE project_id = ?`, projectID); err != nil {
		return nil, err
	}
	return out, nil
}

// UserRemovals returns slot's pending removals, oldest first.
func (d *DB) UserRemovals(ctx context.Context, slot string) ([]UserRemoval, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT slot, uid, project_id, gid, home_path, queued_unix
		FROM user_removals WHERE slot = ? ORDER BY queued_unix, uid`, slot)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []UserRemoval
	for rows.Next() {
		var (
			r           UserRemoval
			uid, gid, q int64
		)
		if err := rows.Scan(&r.Slot, &uid, &r.ProjectID, &gid, &r.HomePath, &q); err != nil {
			return nil, err
		}
		id, ok := userID(uid)
		if !ok || gid != uid {
			return nil, fmt.Errorf("state: removal of %s has UID %d and GID %d", r.ProjectID, uid, gid)
		}
		r.UID, r.GID, r.Queued = id, id, time.Unix(q, 0).UTC()
		out = append(out, r)
	}
	return out, rows.Err()
}

// ClearUserRemoval deletes slot's pending removal of projectID's user
// uid, and reports whether there was one. A removal of another project,
// or of another UID, stays (S05-04-naming, "The project user").
func (d *DB) ClearUserRemoval(ctx context.Context, slot, projectID string, uid uint32) (bool, error) {
	res, err := d.db.ExecContext(ctx, `DELETE FROM user_removals WHERE slot = ? AND project_id = ? AND uid = ?`,
		slot, projectID, int64(uid))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}
