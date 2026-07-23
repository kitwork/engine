package work

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// ── Scheduler: pluggable coordination store ─────────────────────────────────────────────────────────
//
// The dispatcher logic (dispatchDue → claim → run → record) is identical whether a job's durable state
// lives in a per-tenant SQLite file (single node) or a SHARED Postgres (cluster). The only differences
// are dialect + how "claim exactly one slot" is enforced across nodes, so those hide behind cronStore.
//
// A cron's NATURAL KEY is (identity, name) — the app + the filename. There is no opaque hash id: the
// `crons` PK is (identity, name) and `cron_runs` carries those two columns directly, so every row reads
// as "which app, which cron" with nothing to decode. The idempotency arbiter is
// UNIQUE(identity, name, scheduled_for), node-independent so all nodes agree on a slot.
//
//	sqliteStore : one file per app (apps/<identity>/.data/scheduler.db). Reclaim = boot-only.
//	pgStore     : one shared DB for every app + node. NOW() is the clock; INSERT … ON CONFLICT DO
//	              NOTHING is the arbiter; SELECT … FOR UPDATE SKIP LOCKED is the claim; lease + heartbeat
//	              + expiry-reclaim let a crashed node's work move.
type cronStore interface {
	label() string
	initSchema() error
	sync(identity string, jobs []*CronJob) error
	reclaim(identity string, bootWipe bool) int64 // orphaned 'running' → 'pending'; bootWipe wipes ALL (SQLite boot)
	hasActive(identity, name string) bool
	insertSlot(identity, name string, slot time.Time, maxAttempts int) error
	claim(identity, nodeID string, leaseTTL time.Duration, limit int) []claimedRow
	complete(execID int64, output string, gas int64)
	fail(execID int64, attempt int, retry bool, availableAt time.Time, errMsg, output string, gas int64)
	// recordSummary rolls a TERMINAL run into the compact per-cron summary on `crons` (last_run,
	// last_status, run_count, fail_count) so monitoring reads ONE row per cron, not the run log.
	recordSummary(identity, name, status string)
	heartbeat(nodeID string, leaseTTL time.Duration)
	// retention prunes cron_runs: successes are transient (deleted once past `completedBefore`, a short
	// window), failures are kept for history (deleted past `failedBefore`, the .retention() window).
	retention(identity, name string, completedBefore, failedBefore time.Time)
}

func rfc(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// ── SQLite backend ──────────────────────────────────────────────────────────────────────────────────

type sqliteStore struct {
	db   *sql.DB
	node string // this process's kitid — stored on crons.node when it syncs a cron
}

func (s *sqliteStore) label() string { return "sqlite" }

func (s *sqliteStore) initSchema() error {
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
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("sqlite scheduler schema: %w", err)
		}
	}
	return nil
}

func (s *sqliteStore) sync(identity string, jobs []*CronJob) error {
	now := rfc(time.Now())
	keep := make([]string, 0, len(jobs))
	for _, job := range jobs {
		keep = append(keep, job.Name)
		_, err := s.db.Exec(`
			INSERT INTO crons (identity, name, node, origin, source, content_hash, schedule,
				timezone, overlap, max_attempts, retention, status, created_at, updated_at)
			VALUES (?,?,?, 'file', ?, ?, ?, ?, ?, ?, ?, 'active', ?, ?)
			ON CONFLICT(identity, name) DO UPDATE SET node=excluded.node, source=excluded.source,
				content_hash=excluded.content_hash, schedule=excluded.schedule, timezone=excluded.timezone,
				overlap=excluded.overlap, max_attempts=excluded.max_attempts,
				retention=excluded.retention, updated_at=excluded.updated_at
			WHERE crons.content_hash IS NOT excluded.content_hash`,
			identity, job.Name, s.node, "_cron/"+job.Name+".kitwork.js", job.ContentHash, job.Expression,
			job.Timezone, job.OverlapPolicy, job.MaxAttempts, job.RetentionDays, now, now)
		if err != nil {
			fmt.Printf("[Cron] sqlite sync %s: %v\n", job.Name, err)
		}
	}
	deleteMissing(s.db, "?", identity, keep)
	return nil
}

func (s *sqliteStore) reclaim(identity string, bootWipe bool) int64 {
	if !bootWipe {
		return 0 // single node: a 'running' row is a live in-flight goroutine, never reclaim mid-run
	}
	res, _ := s.db.Exec(`UPDATE cron_runs
		SET status='pending', node=NULL, lease_until=NULL, started_at=NULL
		WHERE identity=? AND status='running'`, identity)
	return rowsAffected(res)
}

func (s *sqliteStore) hasActive(identity, name string) bool {
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM cron_runs WHERE identity=? AND name=? AND status IN ('pending','running')`,
		identity, name).Scan(&n)
	return n > 0
}

func (s *sqliteStore) insertSlot(identity, name string, slot time.Time, maxAttempts int) error {
	now := rfc(time.Now())
	_, err := s.db.Exec(`INSERT OR IGNORE INTO cron_runs
		(identity, name, scheduled_for, status, attempt, max_attempts, available_at, created_at)
		VALUES (?,?,?, 'pending', 0, ?, ?, ?)`, identity, name, rfc(slot), maxAttempts, now, now)
	return err
}

func (s *sqliteStore) recordSummary(identity, name, status string) {
	s.db.Exec(`UPDATE crons SET last_run=?, last_status=?, run_count=run_count+1, fail_count=fail_count+?
		WHERE identity=? AND name=?`, rfc(time.Now()), status, failInc(status), identity, name)
}

func (s *sqliteStore) claim(identity, nodeID string, leaseTTL time.Duration, limit int) []claimedRow {
	// SQLite has no SKIP LOCKED: materialize the pending rows (closing the cursor BEFORE any UPDATE,
	// since SQLite locks the file while a result set is open), then take each with a status-guarded
	// UPDATE — the guard is what makes the claim atomic (only one winner even under concurrency).
	rows, err := s.db.Query(`SELECT id, name, scheduled_for, attempt, max_attempts
		FROM cron_runs WHERE identity=? AND status='pending' AND available_at <= ?
		ORDER BY available_at LIMIT ?`, identity, rfc(time.Now()), limit)
	if err != nil {
		return nil
	}
	var pending []claimedRow
	for rows.Next() {
		var r claimedRow
		if rows.Scan(&r.id, &r.name, &r.scheduledFor, &r.attempt, &r.maxAttempts) == nil {
			pending = append(pending, r)
		}
	}
	rows.Close()

	now := time.Now()
	leaseUntil := rfc(now.Add(leaseTTL))
	var won []claimedRow
	for _, r := range pending {
		res, err := s.db.Exec(`UPDATE cron_runs
			SET status='running', node=?, lease_until=?, started_at=?
			WHERE id=? AND status='pending'`, nodeID, leaseUntil, rfc(now), r.id)
		if err == nil && rowsAffected(res) > 0 {
			won = append(won, r)
		}
	}
	return won
}

func (s *sqliteStore) complete(execID int64, output string, gas int64) {
	// Keep node as the record of WHICH node ran it (a completed row is never re-claimed anyway).
	s.db.Exec(`UPDATE cron_runs SET status='completed', finished_at=?, output=?, gas_used=?,
		error_message=NULL, lease_until=NULL WHERE id=?`, rfc(time.Now()), output, gas, execID)
}

func (s *sqliteStore) fail(execID int64, attempt int, retry bool, availableAt time.Time, errMsg, output string, gas int64) {
	if retry {
		// Return to pending for another attempt — clear the lease so ANY node may take the retry.
		s.db.Exec(`UPDATE cron_runs SET status='pending', attempt=?, available_at=?,
			error_message=?, output=?, gas_used=?, node=NULL, lease_until=NULL WHERE id=?`,
			attempt, rfc(availableAt), errMsg, output, gas, execID)
		return
	}
	s.db.Exec(`UPDATE cron_runs SET status='failed', attempt=?, finished_at=?,
		error_message=?, output=?, gas_used=?, lease_until=NULL WHERE id=?`,
		attempt, rfc(time.Now()), errMsg, output, gas, execID)
}

func (s *sqliteStore) heartbeat(nodeID string, leaseTTL time.Duration) {} // single node: goroutine holds the work, no lease race

func (s *sqliteStore) retention(identity, name string, completedBefore, failedBefore time.Time) {
	s.db.Exec(`DELETE FROM cron_runs WHERE identity=? AND name=? AND status='completed' AND created_at < ?`,
		identity, name, rfc(completedBefore))
	s.db.Exec(`DELETE FROM cron_runs WHERE identity=? AND name=? AND status='failed' AND created_at < ?`,
		identity, name, rfc(failedBefore))
}

// ── Postgres backend (shared cluster store) ──────────────────────────────────────────────────────────

type pgStore struct {
	db   *sql.DB
	node string // this process's kitid — stored on crons.node when it syncs a cron
}

func (s *pgStore) label() string { return "postgres" }

func (s *pgStore) initSchema() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS crons (
			identity VARCHAR(128) NOT NULL, node VARCHAR(255), name VARCHAR(255) NOT NULL,
			origin VARCHAR(16) NOT NULL DEFAULT 'file', source VARCHAR(512), content_hash VARCHAR(64),
			schedule VARCHAR(100) NOT NULL, timezone VARCHAR(64) NOT NULL DEFAULT 'UTC',
			overlap VARCHAR(16) NOT NULL DEFAULT 'skip', max_attempts INT NOT NULL DEFAULT 1,
			retention INT NOT NULL DEFAULT 30, status VARCHAR(16) NOT NULL DEFAULT 'active',
			last_run TIMESTAMPTZ, last_status VARCHAR(16), run_count BIGINT NOT NULL DEFAULT 0,
			fail_count BIGINT NOT NULL DEFAULT 0,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (identity, name))`,
		`CREATE TABLE IF NOT EXISTS cron_runs (
			id BIGSERIAL PRIMARY KEY, identity VARCHAR(128), name VARCHAR(255),
			scheduled_for TIMESTAMPTZ NOT NULL, status VARCHAR(16) NOT NULL DEFAULT 'pending',
			attempt INT NOT NULL DEFAULT 0, max_attempts INT NOT NULL DEFAULT 1,
			available_at TIMESTAMPTZ, node VARCHAR(255), lease_until TIMESTAMPTZ,
			started_at TIMESTAMPTZ, finished_at TIMESTAMPTZ, error_message TEXT, output TEXT,
			gas_used BIGINT, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			CONSTRAINT uniq_cron_slot UNIQUE (identity, name, scheduled_for))`,
		`CREATE INDEX IF NOT EXISTS idx_run_claim ON cron_runs(status, available_at)`,
		`CREATE INDEX IF NOT EXISTS idx_run_lease ON cron_runs(status, lease_until)`,
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("postgres scheduler schema: %w", err)
		}
	}
	return nil
}

func (s *pgStore) sync(identity string, jobs []*CronJob) error {
	keep := make([]string, 0, len(jobs))
	for _, job := range jobs {
		keep = append(keep, job.Name)
		_, err := s.db.Exec(`
			INSERT INTO crons (identity, name, node, origin, source, content_hash,
				schedule, timezone, overlap, max_attempts, retention, status)
			VALUES ($1,$2,$3,'file',$4,$5,$6,$7,$8,$9,$10,'active')
			ON CONFLICT(identity, name) DO UPDATE SET node=excluded.node, source=excluded.source,
				content_hash=excluded.content_hash, schedule=excluded.schedule, timezone=excluded.timezone,
				overlap=excluded.overlap, max_attempts=excluded.max_attempts,
				retention=excluded.retention, updated_at=NOW()
			WHERE crons.content_hash IS DISTINCT FROM excluded.content_hash`,
			identity, job.Name, s.node, "_cron/"+job.Name+".kitwork.js", job.ContentHash, job.Expression,
			job.Timezone, job.OverlapPolicy, job.MaxAttempts, job.RetentionDays)
		if err != nil {
			fmt.Printf("[Cron] pg sync %s: %v\n", job.Name, err)
		}
	}
	deleteMissingPG(s.db, identity, keep)
	return nil
}

func (s *pgStore) reclaim(identity string, bootWipe bool) int64 {
	// Shared DB: NEVER wipe all 'running' — other live nodes own theirs. Only reclaim leases that have
	// EXPIRED (a node stopped heartbeating). Safe at boot AND periodically. bootWipe is ignored: a lease
	// is the only signal that a node is alive, so expiry — not process boot — is what frees a slot.
	res, _ := s.db.Exec(`UPDATE cron_runs
		SET status='pending', node=NULL, lease_until=NULL, started_at=NULL
		WHERE identity=$1 AND status='running' AND lease_until < NOW()`, identity)
	return rowsAffected(res)
}

func (s *pgStore) hasActive(identity, name string) bool {
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM cron_runs WHERE identity=$1 AND name=$2 AND status IN ('pending','running')`,
		identity, name).Scan(&n)
	return n > 0
}

func (s *pgStore) insertSlot(identity, name string, slot time.Time, maxAttempts int) error {
	// The slot arbiter: many nodes fire the same instant; UNIQUE(identity, name, scheduled_for) lets
	// exactly one INSERT land, the rest hit ON CONFLICT DO NOTHING and stand down. NOW() is the DB clock.
	_, err := s.db.Exec(`INSERT INTO cron_runs
		(identity, name, scheduled_for, status, attempt, max_attempts, available_at, created_at)
		VALUES ($1,$2,$3,'pending',0,$4,NOW(),NOW()) ON CONFLICT (identity, name, scheduled_for) DO NOTHING`,
		identity, name, slot.UTC(), maxAttempts)
	return err
}

func (s *pgStore) recordSummary(identity, name, status string) {
	s.db.Exec(`UPDATE crons SET last_run=NOW(), last_status=$3, run_count=run_count+1, fail_count=fail_count+$4
		WHERE identity=$1 AND name=$2`, identity, name, status, failInc(status))
}

// failInc is 1 for a terminal failure, else 0 — kept out of SQL so both dialects stay simple.
func failInc(status string) int {
	if status == "failed" {
		return 1
	}
	return 0
}

func (s *pgStore) claim(identity, nodeID string, leaseTTL time.Duration, limit int) []claimedRow {
	// One atomic statement claims up to `limit` due slots for THIS app: the inner SELECT … FOR UPDATE
	// SKIP LOCKED row-locks them so a concurrent node grabs DIFFERENT rows (never blocks, never double-
	// runs), the UPDATE flips them to running + leases them to this node, RETURNING hands them back.
	secs := int(leaseTTL.Seconds())
	if secs < 1 {
		secs = 1
	}
	rows, err := s.db.Query(`
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
	var won []claimedRow
	for rows.Next() {
		var r claimedRow
		var slot time.Time
		if rows.Scan(&r.id, &r.name, &slot, &r.attempt, &r.maxAttempts) == nil {
			r.scheduledFor = rfc(slot)
			won = append(won, r)
		}
	}
	return won
}

func (s *pgStore) complete(execID int64, output string, gas int64) {
	s.db.Exec(`UPDATE cron_runs SET status='completed', finished_at=NOW(), output=$1,
		gas_used=$2, error_message=NULL, lease_until=NULL WHERE id=$3`, output, gas, execID)
}

func (s *pgStore) fail(execID int64, attempt int, retry bool, availableAt time.Time, errMsg, output string, gas int64) {
	if retry {
		s.db.Exec(`UPDATE cron_runs SET status='pending', attempt=$1, available_at=$2,
			error_message=$3, output=$4, gas_used=$5, node=NULL, lease_until=NULL WHERE id=$6`,
			attempt, availableAt.UTC(), errMsg, output, gas, execID)
		return
	}
	s.db.Exec(`UPDATE cron_runs SET status='failed', attempt=$1, finished_at=NOW(),
		error_message=$2, output=$3, gas_used=$4, lease_until=NULL WHERE id=$5`,
		attempt, errMsg, output, gas, execID)
}

func (s *pgStore) heartbeat(nodeID string, leaseTTL time.Duration) {
	secs := int(leaseTTL.Seconds())
	if secs < 1 {
		secs = 1
	}
	s.db.Exec(`UPDATE cron_runs SET lease_until=NOW() + ($1 * interval '1 second')
		WHERE node=$2 AND status='running'`, secs, nodeID)
}

func (s *pgStore) retention(identity, name string, completedBefore, failedBefore time.Time) {
	s.db.Exec(`DELETE FROM cron_runs WHERE identity=$1 AND name=$2 AND status='completed' AND created_at < $3`,
		identity, name, completedBefore.UTC())
	s.db.Exec(`DELETE FROM cron_runs WHERE identity=$1 AND name=$2 AND status='failed' AND created_at < $3`,
		identity, name, failedBefore.UTC())
}

// ── shared helpers ──────────────────────────────────────────────────────────────────────────────────

// deleteMissing removes file-origin crons for an app whose file no longer exists (name not in keep).
func deleteMissing(db *sql.DB, ph, identity string, keep []string) {
	if len(keep) == 0 {
		db.Exec(`DELETE FROM crons WHERE identity=? AND origin='file'`, identity)
		return
	}
	marks := strings.TrimRight(strings.Repeat(ph+",", len(keep)), ",")
	args := append([]any{identity}, toAny(keep)...)
	db.Exec(`DELETE FROM crons WHERE identity=? AND origin='file' AND name NOT IN (`+marks+`)`, args...)
}

func deleteMissingPG(db *sql.DB, identity string, keep []string) {
	if len(keep) == 0 {
		db.Exec(`DELETE FROM crons WHERE identity=$1 AND origin='file'`, identity)
		return
	}
	marks := make([]string, len(keep))
	args := []any{identity}
	for i, name := range keep {
		marks[i] = fmt.Sprintf("$%d", i+2)
		args = append(args, name)
	}
	db.Exec(`DELETE FROM crons WHERE identity=$1 AND origin='file' AND name NOT IN (`+strings.Join(marks, ",")+`)`, args...)
}

func toAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

func rowsAffected(res sql.Result) int64 {
	if res == nil {
		return 0
	}
	n, _ := res.RowsAffected()
	return n
}
