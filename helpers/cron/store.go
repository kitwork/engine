// Package cron provides persistent cron job store and scheduling utilities.
package cron

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

func RFC(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// ClaimedRow represents a cron execution slot claimed by a node.
type ClaimedRow struct {
	ID           int64
	Name         string
	ScheduledFor time.Time
	Attempt      int
	MaxAttempts  int
}

// JobRecord represents a scheduled cron job definition.
type JobRecord struct {
	Name          string
	Expression    string
	Timezone      string
	OverlapPolicy string
	MaxAttempts   int
	RetentionDays int
	ContentHash   string
}

// Store defines the interface for durable cron slot locking and execution logs.
type Store interface {
	Label() string
	InitSchema() error
	Sync(identity string, jobs []JobRecord) error
	Reclaim(identity string, bootWipe bool) int64
	HasActive(identity, name string) bool
	InsertSlot(identity, name string, slot time.Time, maxAttempts int) error
	Claim(identity, nodeID string, leaseTTL time.Duration, limit int) []ClaimedRow
	Complete(execID int64, output string, gas int64)
	Fail(execID int64, attempt int, retry bool, availableAt time.Time, errMsg, output string, gas int64)
	RecordSummary(identity, name, status string)
	Heartbeat(nodeID string, leaseTTL time.Duration)
	Retention(identity, name string, completedBefore, failedBefore time.Time)
	ListCrons(identity string) []map[string]any
}

// SqliteStore is a per-tenant SQLite implementation of Store.
type SqliteStore struct {
	DB   *sql.DB
	Node string
}

func NewSqliteStore(db *sql.DB, nodeID string) *SqliteStore {
	return &SqliteStore{DB: db, Node: nodeID}
}

func (s *SqliteStore) Label() string { return "sqlite" }

func (s *SqliteStore) InitSchema() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS crons (
			identity TEXT NOT NULL, node TEXT, name TEXT NOT NULL,
			origin TEXT NOT NULL DEFAULT 'file', source TEXT, content_hash TEXT,
			schedule TEXT NOT NULL, timezone TEXT NOT NULL DEFAULT 'UTC',
			overlap TEXT NOT NULL DEFAULT 'skip', max_attempts INTEGER NOT NULL DEFAULT 1,
			retention INTEGER NOT NULL DEFAULT 30, status TEXT NOT NULL DEFAULT 'active',
			last_run TEXT, last_status TEXT, run_count INTEGER NOT NULL DEFAULT 0,
			fail_count INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			PRIMARY KEY (identity, name))`,
		`CREATE TABLE IF NOT EXISTS cron_runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT, identity TEXT, name TEXT,
			scheduled_for TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'pending',
			attempt INTEGER NOT NULL DEFAULT 0, max_attempts INTEGER NOT NULL DEFAULT 1,
			available_at TEXT, node TEXT, lease_until TEXT, started_at TEXT, finished_at TEXT,
			error_message TEXT, output TEXT, gas_used INTEGER,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			UNIQUE (identity, name, scheduled_for))`,
		`CREATE INDEX IF NOT EXISTS idx_run_claim   ON cron_runs(status, available_at)`,
		`CREATE INDEX IF NOT EXISTS idx_run_history ON cron_runs(identity, name, scheduled_for DESC)`,
	}
	for _, q := range stmts {
		if _, err := s.DB.Exec(q); err != nil {
			return fmt.Errorf("sqlite scheduler schema: %w", err)
		}
	}
	return nil
}

func (s *SqliteStore) Sync(identity string, jobs []JobRecord) error {
	now := RFC(time.Now())
	keep := make([]string, 0, len(jobs))
	for _, job := range jobs {
		keep = append(keep, job.Name)
		_, err := s.DB.Exec(`
			INSERT INTO crons (identity, name, node, origin, source, content_hash, schedule,
				timezone, overlap, max_attempts, retention, status, created_at, updated_at)
			VALUES (?,?,?, 'file', ?, ?, ?, ?, ?, ?, ?, 'active', ?, ?)
			ON CONFLICT(identity, name) DO UPDATE SET node=excluded.node, source=excluded.source,
				content_hash=excluded.content_hash, schedule=excluded.schedule, timezone=excluded.timezone,
				overlap=excluded.overlap, max_attempts=excluded.max_attempts,
				retention=excluded.retention, updated_at=excluded.updated_at
			WHERE crons.content_hash IS NOT excluded.content_hash`,
			identity, job.Name, s.Node, "_cron/"+job.Name+".kitwork.js", job.ContentHash, job.Expression,
			job.Timezone, job.OverlapPolicy, job.MaxAttempts, job.RetentionDays, now, now)
		if err != nil {
			fmt.Printf("[Cron] sqlite sync %s: %v\n", job.Name, err)
		}
	}
	s.deleteMissing("?", identity, keep)
	return nil
}

func (s *SqliteStore) deleteMissing(ph, identity string, keep []string) {
	if len(keep) == 0 {
		s.DB.Exec(fmt.Sprintf(`DELETE FROM crons WHERE identity=%s AND origin='file'`, ph), identity)
		return
	}
	args := make([]any, 0, len(keep)+1)
	args = append(args, identity)
	phs := make([]string, len(keep))
	for i, k := range keep {
		phs[i] = ph
		args = append(args, k)
	}
	q := fmt.Sprintf(`DELETE FROM crons WHERE identity=%s AND origin='file' AND name NOT IN (%s)`,
		ph, strings.Join(phs, ","))
	s.DB.Exec(q, args...)
}

func (s *SqliteStore) Reclaim(identity string, bootWipe bool) int64 {
	if !bootWipe {
		return 0
	}
	res, _ := s.DB.Exec(`UPDATE cron_runs
		SET status='pending', node=NULL, lease_until=NULL, started_at=NULL
		WHERE identity=? AND status='running'`, identity)
	if res == nil {
		return 0
	}
	n, _ := res.RowsAffected()
	return n
}

func (s *SqliteStore) HasActive(identity, name string) bool {
	var n int
	s.DB.QueryRow(`SELECT COUNT(*) FROM cron_runs WHERE identity=? AND name=? AND status IN ('pending','running')`,
		identity, name).Scan(&n)
	return n > 0
}

func (s *SqliteStore) InsertSlot(identity, name string, slot time.Time, maxAttempts int) error {
	now := RFC(time.Now())
	_, err := s.DB.Exec(`INSERT OR IGNORE INTO cron_runs
		(identity, name, scheduled_for, status, attempt, max_attempts, available_at, created_at)
		VALUES (?,?,?, 'pending', 0, ?, ?, ?)`, identity, name, RFC(slot), maxAttempts, now, now)
	return err
}

func (s *SqliteStore) RecordSummary(identity, name, status string) {
	failInc := 0
	if status == "failed" {
		failInc = 1
	}
	s.DB.Exec(`UPDATE crons SET last_run=?, last_status=?, run_count=run_count+1, fail_count=fail_count+?
		WHERE identity=? AND name=?`, RFC(time.Now()), status, failInc, identity, name)
}

func (s *SqliteStore) Claim(identity, nodeID string, leaseTTL time.Duration, limit int) []ClaimedRow {
	rows, err := s.DB.Query(`SELECT id, name, scheduled_for, attempt, max_attempts
		FROM cron_runs WHERE identity=? AND status='pending' AND available_at <= ?
		ORDER BY available_at LIMIT ?`, identity, RFC(time.Now()), limit)
	if err != nil {
		return nil
	}
	type candidate struct {
		id, attempt, maxAttempts int
		name, scheduledFor       string
	}
	var cand []candidate
	for rows.Next() {
		var c candidate
		if rows.Scan(&c.id, &c.name, &c.scheduledFor, &c.attempt, &c.maxAttempts) == nil {
			cand = append(cand, c)
		}
	}
	rows.Close()

	now := time.Now()
	leaseUntil := RFC(now.Add(leaseTTL))
	nowStr := RFC(now)
	out := make([]ClaimedRow, 0, len(cand))
	for _, c := range cand {
		res, err := s.DB.Exec(`UPDATE cron_runs
			SET status='running', node=?, lease_until=?, started_at=?, attempt=?
			WHERE id=? AND status='pending'`, nodeID, leaseUntil, nowStr, c.attempt+1, c.id)
		if err == nil {
			if n, _ := res.RowsAffected(); n == 1 {
				st, _ := time.Parse(time.RFC3339, c.scheduledFor)
				out = append(out, ClaimedRow{
					ID:           int64(c.id),
					Name:         c.name,
					ScheduledFor: st,
					Attempt:      c.attempt + 1,
					MaxAttempts:  c.maxAttempts,
				})
			}
		}
	}
	return out
}

func (s *SqliteStore) Complete(execID int64, output string, gas int64) {
	s.DB.Exec(`UPDATE cron_runs
		SET status='completed', finished_at=?, output=?, gas_used=?
		WHERE id=? AND status='running'`, RFC(time.Now()), output, gas, execID)
}

func (s *SqliteStore) Fail(execID int64, attempt int, retry bool, availableAt time.Time, errMsg, output string, gas int64) {
	now := RFC(time.Now())
	if retry {
		s.DB.Exec(`UPDATE cron_runs
			SET status='pending', attempt=?, available_at=?, node=NULL, lease_until=NULL, started_at=NULL, error_message=?, output=?, gas_used=?
			WHERE id=? AND status='running'`, attempt, RFC(availableAt), errMsg, output, gas, execID)
		return
	}
	s.DB.Exec(`UPDATE cron_runs
		SET status='failed', attempt=?, finished_at=?, error_message=?, output=?, gas_used=?
		WHERE id=? AND status='running'`, attempt, now, errMsg, output, gas, execID)
}

func (s *SqliteStore) Heartbeat(nodeID string, leaseTTL time.Duration) {}

func (s *SqliteStore) Retention(identity, name string, completedBefore, failedBefore time.Time) {
	s.DB.Exec(`DELETE FROM cron_runs WHERE identity=? AND name=? AND status='completed' AND finished_at < ?`,
		identity, name, RFC(completedBefore))
	s.DB.Exec(`DELETE FROM cron_runs WHERE identity=? AND name=? AND status='failed' AND finished_at < ?`,
		identity, name, RFC(failedBefore))
}

func (s *SqliteStore) ListCrons(identity string) []map[string]any {
	rows, err := s.DB.Query(`SELECT name, source, schedule, timezone, overlap, max_attempts, retention,
		status, last_run, last_status, run_count, fail_count, updated_at FROM crons WHERE identity=? ORDER BY name`, identity)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []map[string]any
	for rows.Next() {
		var name, source, sched, tz, overlap, status, updated string
		var lastRun, lastStatus sql.NullString
		var maxAttempts, retention, runCount, failCount int
		if rows.Scan(&name, &source, &sched, &tz, &overlap, &maxAttempts, &retention, &status, &lastRun, &lastStatus, &runCount, &failCount, &updated) != nil {
			continue
		}
		m := map[string]any{
			"name": name, "source": source, "schedule": sched, "timezone": tz, "overlap": overlap,
			"maxAttempts": maxAttempts, "retention": retention, "status": status,
			"runCount": runCount, "failCount": failCount, "updatedAt": updated,
		}
		if lastRun.Valid {
			m["lastRun"] = lastRun.String
		}
		if lastStatus.Valid {
			m["lastStatus"] = lastStatus.String
		}
		out = append(out, m)
	}
	return out
}

// PgStore is a shared Postgres implementation of Store.
type PgStore struct {
	DB   *sql.DB
	Node string
}

func NewPgStore(db *sql.DB, nodeID string) *PgStore {
	return &PgStore{DB: db, Node: nodeID}
}

func (s *PgStore) Label() string { return "postgres" }

func (s *PgStore) InitSchema() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS crons (
			identity TEXT NOT NULL, node TEXT, name TEXT NOT NULL,
			origin TEXT NOT NULL DEFAULT 'file', source TEXT, content_hash TEXT,
			schedule TEXT NOT NULL, timezone TEXT NOT NULL DEFAULT 'UTC',
			overlap TEXT NOT NULL DEFAULT 'skip', max_attempts INTEGER NOT NULL DEFAULT 1,
			retention INTEGER NOT NULL DEFAULT 30, status TEXT NOT NULL DEFAULT 'active',
			last_run TIMESTAMPTZ, last_status TEXT, run_count BIGINT NOT NULL DEFAULT 0,
			fail_count BIGINT NOT NULL DEFAULT 0,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (identity, name))`,
		`CREATE TABLE IF NOT EXISTS cron_runs (
			id BIGSERIAL PRIMARY KEY, identity TEXT, name TEXT,
			scheduled_for TIMESTAMPTZ NOT NULL, status TEXT NOT NULL DEFAULT 'pending',
			attempt INTEGER NOT NULL DEFAULT 0, max_attempts INTEGER NOT NULL DEFAULT 1,
			available_at TIMESTAMPTZ, node TEXT, lease_until TIMESTAMPTZ,
			started_at TIMESTAMPTZ, finished_at TIMESTAMPTZ, error_message TEXT, output TEXT,
			gas_used BIGINT, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			CONSTRAINT uniq_cron_slot UNIQUE (identity, name, scheduled_for))`,
		`CREATE INDEX IF NOT EXISTS idx_run_claim ON cron_runs(status, available_at)`,
		`CREATE INDEX IF NOT EXISTS idx_run_lease ON cron_runs(status, lease_until)`,
	}
	for _, q := range stmts {
		if _, err := s.DB.Exec(q); err != nil {
			return fmt.Errorf("postgres scheduler schema: %w", err)
		}
	}
	return nil
}

func (s *PgStore) Sync(identity string, jobs []JobRecord) error {
	keep := make([]string, 0, len(jobs))
	for _, job := range jobs {
		keep = append(keep, job.Name)
		_, err := s.DB.Exec(`
			INSERT INTO crons (identity, name, node, origin, source, content_hash,
				schedule, timezone, overlap, max_attempts, retention, status)
			VALUES ($1,$2,$3,'file',$4,$5,$6,$7,$8,$9,$10,'active')
			ON CONFLICT(identity, name) DO UPDATE SET node=excluded.node, source=excluded.source,
				content_hash=excluded.content_hash, schedule=excluded.schedule, timezone=excluded.timezone,
				overlap=excluded.overlap, max_attempts=excluded.max_attempts,
				retention=excluded.retention, updated_at=NOW()
			WHERE crons.content_hash IS DISTINCT FROM excluded.content_hash`,
			identity, job.Name, s.Node, "_cron/"+job.Name+".kitwork.js", job.ContentHash, job.Expression,
			job.Timezone, job.OverlapPolicy, job.MaxAttempts, job.RetentionDays)
		if err != nil {
			fmt.Printf("[Cron] pg sync %s: %v\n", job.Name, err)
		}
	}
	s.deleteMissing(identity, keep)
	return nil
}

func (s *PgStore) deleteMissing(identity string, keep []string) {
	if len(keep) == 0 {
		s.DB.Exec(`DELETE FROM crons WHERE identity=$1 AND origin='file'`, identity)
		return
	}
	marks := make([]string, len(keep))
	args := []any{identity}
	for i, name := range keep {
		marks[i] = fmt.Sprintf("$%d", i+2)
		args = append(args, name)
	}
	s.DB.Exec(`DELETE FROM crons WHERE identity=$1 AND origin='file' AND name NOT IN (`+strings.Join(marks, ",")+`)`, args...)
}

func (s *PgStore) Reclaim(identity string, bootWipe bool) int64 {
	res, _ := s.DB.Exec(`UPDATE cron_runs
		SET status='pending', node=NULL, lease_until=NULL, started_at=NULL
		WHERE identity=$1 AND status='running' AND lease_until < NOW()`, identity)
	if res == nil {
		return 0
	}
	n, _ := res.RowsAffected()
	return n
}

func (s *PgStore) HasActive(identity, name string) bool {
	var n int
	s.DB.QueryRow(`SELECT COUNT(*) FROM cron_runs WHERE identity=$1 AND name=$2 AND status IN ('pending','running')`,
		identity, name).Scan(&n)
	return n > 0
}

func (s *PgStore) InsertSlot(identity, name string, slot time.Time, maxAttempts int) error {
	_, err := s.DB.Exec(`INSERT INTO cron_runs
		(identity, name, scheduled_for, status, attempt, max_attempts, available_at, created_at)
		VALUES ($1,$2,$3,'pending',0,$4,NOW(),NOW()) ON CONFLICT (identity, name, scheduled_for) DO NOTHING`,
		identity, name, slot.UTC(), maxAttempts)
	return err
}

func (s *PgStore) RecordSummary(identity, name, status string) {
	failInc := 0
	if status == "failed" {
		failInc = 1
	}
	s.DB.Exec(`UPDATE crons SET last_run=NOW(), last_status=$3, run_count=run_count+1, fail_count=fail_count+$4
		WHERE identity=$1 AND name=$2`, identity, name, status, failInc)
}

func (s *PgStore) Claim(identity, nodeID string, leaseTTL time.Duration, limit int) []ClaimedRow {
	secs := int(leaseTTL.Seconds())
	if secs < 1 {
		secs = 1
	}
	rows, err := s.DB.Query(`
		UPDATE cron_runs SET status='running', node=$1,
			lease_until=NOW() + ($2 * interval '1 second'), started_at=NOW()
		WHERE id IN (
			SELECT id FROM cron_runs
			WHERE identity=$3 AND status='pending' AND available_at <= NOW()
			ORDER BY available_at FOR UPDATE SKIP LOCKED LIMIT $4)
		RETURNING id, name, scheduled_for, attempt, max_attempts`, nodeID, secs, identity, limit)
	if err != nil {
		fmt.Printf("[Cron] pg claim: %v\n", err)
		return nil
	}
	defer rows.Close()
	var won []ClaimedRow
	for rows.Next() {
		var r ClaimedRow
		if rows.Scan(&r.ID, &r.Name, &r.ScheduledFor, &r.Attempt, &r.MaxAttempts) == nil {
			won = append(won, r)
		}
	}
	return won
}

func (s *PgStore) Complete(execID int64, output string, gas int64) {
	s.DB.Exec(`UPDATE cron_runs SET status='completed', finished_at=NOW(), output=$1,
		gas_used=$2, error_message=NULL, lease_until=NULL WHERE id=$3`, output, gas, execID)
}

func (s *PgStore) Fail(execID int64, attempt int, retry bool, availableAt time.Time, errMsg, output string, gas int64) {
	if retry {
		s.DB.Exec(`UPDATE cron_runs SET status='pending', attempt=$1, available_at=$2,
			error_message=$3, output=$4, gas_used=$5, node=NULL, lease_until=NULL WHERE id=$6`,
			attempt, availableAt.UTC(), errMsg, output, gas, execID)
		return
	}
	s.DB.Exec(`UPDATE cron_runs SET status='failed', attempt=$1, finished_at=NOW(),
		error_message=$2, output=$3, gas_used=$4, lease_until=NULL WHERE id=$5`,
		attempt, errMsg, output, gas, execID)
}

func (s *PgStore) Heartbeat(nodeID string, leaseTTL time.Duration) {
	secs := int(leaseTTL.Seconds())
	if secs < 1 {
		secs = 1
	}
	s.DB.Exec(`UPDATE cron_runs SET lease_until=NOW() + ($1 * interval '1 second')
		WHERE node=$2 AND status='running'`, secs, nodeID)
}

func (s *PgStore) Retention(identity, name string, completedBefore, failedBefore time.Time) {
	s.DB.Exec(`DELETE FROM cron_runs WHERE identity=$1 AND name=$2 AND status='completed' AND created_at < $3`,
		identity, name, completedBefore.UTC())
	s.DB.Exec(`DELETE FROM cron_runs WHERE identity=$1 AND name=$2 AND status='failed' AND created_at < $3`,
		identity, name, failedBefore.UTC())
}

func (s *PgStore) ListCrons(identity string) []map[string]any {
	rows, err := s.DB.Query(`SELECT name, source, schedule, timezone, overlap, max_attempts, retention,
		status, last_run, last_status, run_count, fail_count, updated_at FROM crons WHERE identity=$1 ORDER BY name`, identity)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []map[string]any
	for rows.Next() {
		var name, source, sched, tz, overlap, status, updated string
		var lastRun, lastStatus sql.NullString
		var maxAttempts, retention, runCount, failCount int
		if rows.Scan(&name, &source, &sched, &tz, &overlap, &maxAttempts, &retention, &status, &lastRun, &lastStatus, &runCount, &failCount, &updated) != nil {
			continue
		}
		m := map[string]any{
			"name": name, "source": source, "schedule": sched, "timezone": tz, "overlap": overlap,
			"maxAttempts": maxAttempts, "retention": retention, "status": status,
			"runCount": runCount, "failCount": failCount, "updatedAt": updated,
		}
		if lastRun.Valid {
			m["lastRun"] = lastRun.String
		}
		if lastStatus.Valid {
			m["lastStatus"] = lastStatus.String
		}
		out = append(out, m)
	}
	return out
}
