// Package queue provides the durable job-queue store: definitions, messages, claiming and history.
//
// It is deliberately a SIBLING of utilities/cron rather than a generalisation of it. The two share a
// shape — a definitions table, a work table, lease-based claiming, retry with backoff — but they do
// not share a QUESTION. Cron asks "is this slot due yet?", and its arbiter is time:
// UNIQUE(identity, name, scheduled_for). A queue asks "did someone ask for this?", and its arbiter is
// the caller's own key: UNIQUE(identity, name, dedupe_key). Merging them would force one schema to
// carry both a schedule and a payload, and every reader would then have to know which half is dead.
//
// What IS shared is the hard part, and it is shared by copying the technique, not the code: the
// database clock is the only clock on Postgres, the UNIQUE constraint is the arbiter rather than an
// application check, and a claim is an UPDATE that either wins or does not.
package queue

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

func RFC(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// ClaimedJob is one message this node has won the right to run.
type ClaimedJob struct {
	ID          int64
	Name        string
	Payload     string // JSON as dispatched; the runtime decodes it
	Attempt     int
	MaxAttempts int
}

// QueueRecord is a handler definition — a projection of one _queue/*.kitwork.js file.
type QueueRecord struct {
	Name          string
	MaxAttempts   int
	RetentionDays int
	ContentHash   string
}

// Store is the durable backend. Two implementations: per-app SQLite for a single node, shared
// Postgres for many. The authoring model is identical either way; presence of a system database is
// the switch, never a flag.
type Store interface {
	Label() string
	InitSchema() error
	Sync(identity string, queues []QueueRecord) error
	// Enqueue records one message. Reports whether a row was actually created: a repeat of a
	// dispatch that carried the same dedupe key is DROPPED, and the caller is told so rather than
	// left to assume.
	Enqueue(identity, name, payload, dedupeKey string, maxAttempts int, availableAt time.Time) (int64, bool, error)
	Claim(identity, nodeID string, leaseTTL time.Duration, limit int) []ClaimedJob
	Complete(jobID int64, output string, gas int64)
	Fail(jobID int64, attempt int, retry bool, availableAt time.Time, errMsg, output string, gas int64)
	RecordSummary(identity, name, status string)
	Heartbeat(nodeID string, leaseTTL time.Duration)
	Reclaim(identity string, bootWipe bool) int64
	Retention(identity, name string, completedBefore, failedBefore time.Time)
	ListQueues(identity string) []map[string]any
	ListJobs(identity, name, status string, limit int) []map[string]any
	RetryFailed(identity, name string, jobID int64) int64
}

// ── SQLite: one node, zero external infrastructure ───────────────────────────────────────────────

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
		`CREATE TABLE IF NOT EXISTS queues (
			identity TEXT NOT NULL, node TEXT, name TEXT NOT NULL,
			origin TEXT NOT NULL DEFAULT 'file', source TEXT, content_hash TEXT,
			max_attempts INTEGER NOT NULL DEFAULT 1, retention INTEGER NOT NULL DEFAULT 30,
			status TEXT NOT NULL DEFAULT 'active',
			last_run TEXT, last_status TEXT,
			processed_count INTEGER NOT NULL DEFAULT 0, failed_count INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			PRIMARY KEY (identity, name))`,
		// dedupe_key is NULLABLE and the UNIQUE index treats NULLs as distinct — which is exactly the
		// intended semantics: a dispatch WITHOUT a key is a new message every time, and a dispatch
		// WITH one can be repeated safely. The constraint does the deduplication, not a prior SELECT,
		// so two nodes racing on the same key still produce one row.
		`CREATE TABLE IF NOT EXISTS queue_jobs (
			id INTEGER PRIMARY KEY AUTOINCREMENT, identity TEXT NOT NULL, name TEXT NOT NULL,
			payload TEXT, dedupe_key TEXT,
			status TEXT NOT NULL DEFAULT 'pending',
			attempt INTEGER NOT NULL DEFAULT 0, max_attempts INTEGER NOT NULL DEFAULT 1,
			available_at TEXT, node TEXT, lease_until TEXT, started_at TEXT, finished_at TEXT,
			error_message TEXT, output TEXT, gas_used INTEGER,
			created_at TEXT NOT NULL DEFAULT (datetime('now')))`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uniq_queue_dedupe ON queue_jobs(identity, name, dedupe_key)`,
		`CREATE INDEX IF NOT EXISTS idx_queue_claim   ON queue_jobs(identity, status, available_at)`,
		`CREATE INDEX IF NOT EXISTS idx_queue_history ON queue_jobs(identity, name, id DESC)`,
	}
	for _, q := range stmts {
		if _, err := s.DB.Exec(q); err != nil {
			return fmt.Errorf("sqlite queue schema: %w", err)
		}
	}
	return nil
}

func (s *SqliteStore) Sync(identity string, queues []QueueRecord) error {
	now := RFC(time.Now())
	keep := make([]string, 0, len(queues))
	for _, q := range queues {
		keep = append(keep, q.Name)
		_, err := s.DB.Exec(`
			INSERT INTO queues (identity, name, node, origin, source, content_hash,
				max_attempts, retention, status, created_at, updated_at)
			VALUES (?,?,?, 'file', ?, ?, ?, ?, 'active', ?, ?)
			ON CONFLICT(identity, name) DO UPDATE SET node=excluded.node, source=excluded.source,
				content_hash=excluded.content_hash, max_attempts=excluded.max_attempts,
				retention=excluded.retention, updated_at=excluded.updated_at
			WHERE queues.content_hash IS NOT excluded.content_hash`,
			identity, q.Name, s.Node, "_queue/"+q.Name+".kitwork.js", q.ContentHash,
			q.MaxAttempts, q.RetentionDays, now, now)
		if err != nil {
			fmt.Printf("[Queue] sqlite sync %s: %v\n", q.Name, err)
		}
	}
	s.deleteMissing(identity, keep)
	return nil
}

// deleteMissing drops handler DEFINITIONS whose file is gone. It never touches queue_jobs: a pending
// message for a deleted handler must stay visible as a failure ("no registered handler"), because
// silently vanishing work is the failure mode a durable queue exists to prevent.
func (s *SqliteStore) deleteMissing(identity string, keep []string) {
	if len(keep) == 0 {
		s.DB.Exec(`DELETE FROM queues WHERE identity=? AND origin='file'`, identity)
		return
	}
	args := make([]any, 0, len(keep)+1)
	args = append(args, identity)
	marks := make([]string, len(keep))
	for i, name := range keep {
		marks[i] = "?"
		args = append(args, name)
	}
	s.DB.Exec(`DELETE FROM queues WHERE identity=? AND origin='file' AND name NOT IN (`+
		strings.Join(marks, ",")+`)`, args...)
}

func (s *SqliteStore) Enqueue(identity, name, payload, dedupeKey string, maxAttempts int, availableAt time.Time) (int64, bool, error) {
	now := RFC(time.Now())
	var key any
	if dedupeKey != "" {
		key = dedupeKey
	}
	// max_attempts is PINNED onto the row at dispatch, read from the handler definition — so editing
	// a handler's .retry() changes the next dispatch, never the messages already waiting. The
	// caller's value is the fallback for a dispatch that arrives before the definition is synced.
	res, err := s.DB.Exec(`INSERT OR IGNORE INTO queue_jobs
		(identity, name, payload, dedupe_key, status, attempt, max_attempts, available_at, created_at)
		VALUES (?,?,?,?, 'pending', 0,
			COALESCE((SELECT max_attempts FROM queues WHERE identity=? AND name=?), ?), ?, ?)`,
		identity, name, payload, key, identity, name, maxAttempts, RFC(availableAt), now)
	if err != nil {
		return 0, false, err
	}
	// OR IGNORE turns a dedupe collision into zero rows rather than an error — the affected count is
	// how we tell "queued" from "already queued".
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return 0, false, nil
	}
	id, _ := res.LastInsertId()
	return id, true, nil
}

// Claim materialises candidates OUT of their result set before updating: SQLite holds a read lock on
// the file while rows are open, so an in-flight UPDATE against the same table would deadlock. The
// UPDATE then re-checks status='pending', so two dispatchers in one process still cannot both win.
func (s *SqliteStore) Claim(identity, nodeID string, leaseTTL time.Duration, limit int) []ClaimedJob {
	rows, err := s.DB.Query(`SELECT id, name, payload, attempt, max_attempts
		FROM queue_jobs WHERE identity=? AND status='pending' AND available_at <= ?
		ORDER BY available_at, id LIMIT ?`, identity, RFC(time.Now()), limit)
	if err != nil {
		return nil
	}
	type candidate struct {
		id                int64
		name, payload     string
		attempt, maxTries int
	}
	var cand []candidate
	for rows.Next() {
		var c candidate
		var payload sql.NullString
		if rows.Scan(&c.id, &c.name, &payload, &c.attempt, &c.maxTries) == nil {
			c.payload = payload.String
			cand = append(cand, c)
		}
	}
	rows.Close()

	now := time.Now()
	leaseUntil := RFC(now.Add(leaseTTL))
	nowStr := RFC(now)
	out := make([]ClaimedJob, 0, len(cand))
	for _, c := range cand {
		res, err := s.DB.Exec(`UPDATE queue_jobs
			SET status='running', node=?, lease_until=?, started_at=?, attempt=?
			WHERE id=? AND status='pending'`, nodeID, leaseUntil, nowStr, c.attempt+1, c.id)
		if err != nil {
			continue
		}
		if n, _ := res.RowsAffected(); n == 1 {
			out = append(out, ClaimedJob{
				ID: c.id, Name: c.name, Payload: c.payload,
				Attempt: c.attempt + 1, MaxAttempts: c.maxTries,
			})
		}
	}
	return out
}

func (s *SqliteStore) Complete(jobID int64, output string, gas int64) {
	s.DB.Exec(`UPDATE queue_jobs SET status='completed', finished_at=?, output=?, gas_used=?,
		error_message=NULL, lease_until=NULL WHERE id=? AND status='running'`,
		RFC(time.Now()), output, gas, jobID)
}

func (s *SqliteStore) Fail(jobID int64, attempt int, retry bool, availableAt time.Time, errMsg, output string, gas int64) {
	if retry {
		s.DB.Exec(`UPDATE queue_jobs
			SET status='pending', attempt=?, available_at=?, node=NULL, lease_until=NULL,
				started_at=NULL, error_message=?, output=?, gas_used=?
			WHERE id=? AND status='running'`,
			attempt, RFC(availableAt), errMsg, output, gas, jobID)
		return
	}
	// Terminal failure. The row STAYS, with its error and its payload — this table is the dead-letter
	// queue. A separate DLQ table would be the same rows under another name, and would need its own
	// retry path back.
	s.DB.Exec(`UPDATE queue_jobs
		SET status='failed', attempt=?, finished_at=?, error_message=?, output=?, gas_used=?, lease_until=NULL
		WHERE id=? AND status='running'`, attempt, RFC(time.Now()), errMsg, output, gas, jobID)
}

func (s *SqliteStore) RecordSummary(identity, name, status string) {
	failInc := 0
	if status == "failed" {
		failInc = 1
	}
	s.DB.Exec(`UPDATE queues SET last_run=?, last_status=?, processed_count=processed_count+1,
		failed_count=failed_count+? WHERE identity=? AND name=?`,
		RFC(time.Now()), status, failInc, identity, name)
}

// Heartbeat is a no-op on SQLite: one node owns the file, so a lease can never be held by a process
// this one cannot see.
func (s *SqliteStore) Heartbeat(nodeID string, leaseTTL time.Duration) {}

// Reclaim returns abandoned work to pending. On SQLite only at boot, and unconditionally: a 'running'
// row can only be from a previous life of this single node, so it is orphaned by definition.
func (s *SqliteStore) Reclaim(identity string, bootWipe bool) int64 {
	if !bootWipe {
		return 0
	}
	res, _ := s.DB.Exec(`UPDATE queue_jobs
		SET status='pending', node=NULL, lease_until=NULL, started_at=NULL
		WHERE identity=? AND status='running'`, identity)
	if res == nil {
		return 0
	}
	n, _ := res.RowsAffected()
	return n
}

func (s *SqliteStore) Retention(identity, name string, completedBefore, failedBefore time.Time) {
	s.DB.Exec(`DELETE FROM queue_jobs WHERE identity=? AND name=? AND status='completed' AND finished_at < ?`,
		identity, name, RFC(completedBefore))
	s.DB.Exec(`DELETE FROM queue_jobs WHERE identity=? AND name=? AND status='failed' AND finished_at < ?`,
		identity, name, RFC(failedBefore))
}

func (s *SqliteStore) ListQueues(identity string) []map[string]any {
	rows, err := s.DB.Query(`SELECT name, source, max_attempts, retention, status,
		last_run, last_status, processed_count, failed_count, updated_at
		FROM queues WHERE identity=? ORDER BY name`, identity)
	if err != nil {
		return nil
	}
	defer rows.Close()
	return scanQueues(rows)
}

func (s *SqliteStore) ListJobs(identity, name, status string, limit int) []map[string]any {
	q, args := listJobsQuery("?", identity, name, status, limit)
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	return scanJobs(rows)
}

// RetryFailed moves dead-lettered rows back to pending. `attempt` resets to 0 so a replayed job gets
// its full budget again: an operator asking for a retry means "try this properly", not "try it once
// more before giving up".
//
// available_at is bound from Go rather than written as datetime('now'). SQLite compares these columns
// as TEXT, and datetime() renders "2026-07-29 03:11:37" while every other write here uses RFC 3339
// ("2026-07-29T03:11:37Z") — the two sort differently at the space/T, so mixing them would make
// availability depend on which code path wrote the row.
func (s *SqliteStore) RetryFailed(identity, name string, jobID int64) int64 {
	args := []any{RFC(time.Now()), identity}
	where := "identity=? AND status='failed'"
	if name != "" {
		where += " AND name=?"
		args = append(args, name)
	}
	if jobID > 0 {
		where += " AND id=?"
		args = append(args, jobID)
	}
	res, err := s.DB.Exec(`UPDATE queue_jobs SET status='pending', attempt=0, node=NULL,
		lease_until=NULL, started_at=NULL, finished_at=NULL, available_at=?
		WHERE `+where, args...)
	if err != nil || res == nil {
		return 0
	}
	n, _ := res.RowsAffected()
	return n
}

// ── Postgres: many nodes, one clock ──────────────────────────────────────────────────────────────

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
		`CREATE TABLE IF NOT EXISTS queues (
			identity TEXT NOT NULL, node TEXT, name TEXT NOT NULL,
			origin TEXT NOT NULL DEFAULT 'file', source TEXT, content_hash TEXT,
			max_attempts INTEGER NOT NULL DEFAULT 1, retention INTEGER NOT NULL DEFAULT 30,
			status TEXT NOT NULL DEFAULT 'active',
			last_run TIMESTAMPTZ, last_status TEXT,
			processed_count BIGINT NOT NULL DEFAULT 0, failed_count BIGINT NOT NULL DEFAULT 0,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (identity, name))`,
		`CREATE TABLE IF NOT EXISTS queue_jobs (
			id BIGSERIAL PRIMARY KEY, identity TEXT NOT NULL, name TEXT NOT NULL,
			payload TEXT, dedupe_key TEXT,
			status TEXT NOT NULL DEFAULT 'pending',
			attempt INTEGER NOT NULL DEFAULT 0, max_attempts INTEGER NOT NULL DEFAULT 1,
			available_at TIMESTAMPTZ, node TEXT, lease_until TIMESTAMPTZ,
			started_at TIMESTAMPTZ, finished_at TIMESTAMPTZ,
			error_message TEXT, output TEXT, gas_used BIGINT,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uniq_queue_dedupe ON queue_jobs(identity, name, dedupe_key)`,
		`CREATE INDEX IF NOT EXISTS idx_queue_claim ON queue_jobs(identity, status, available_at)`,
		`CREATE INDEX IF NOT EXISTS idx_queue_lease ON queue_jobs(status, lease_until)`,
	}
	for _, q := range stmts {
		if _, err := s.DB.Exec(q); err != nil {
			return fmt.Errorf("postgres queue schema: %w", err)
		}
	}
	return nil
}

func (s *PgStore) Sync(identity string, queues []QueueRecord) error {
	keep := make([]string, 0, len(queues))
	for _, q := range queues {
		keep = append(keep, q.Name)
		_, err := s.DB.Exec(`
			INSERT INTO queues (identity, name, node, origin, source, content_hash,
				max_attempts, retention, status)
			VALUES ($1,$2,$3,'file',$4,$5,$6,$7,'active')
			ON CONFLICT(identity, name) DO UPDATE SET node=excluded.node, source=excluded.source,
				content_hash=excluded.content_hash, max_attempts=excluded.max_attempts,
				retention=excluded.retention, updated_at=NOW()
			WHERE queues.content_hash IS DISTINCT FROM excluded.content_hash`,
			identity, q.Name, s.Node, "_queue/"+q.Name+".kitwork.js", q.ContentHash,
			q.MaxAttempts, q.RetentionDays)
		if err != nil {
			fmt.Printf("[Queue] pg sync %s: %v\n", q.Name, err)
		}
	}
	s.deleteMissing(identity, keep)
	return nil
}

func (s *PgStore) deleteMissing(identity string, keep []string) {
	if len(keep) == 0 {
		s.DB.Exec(`DELETE FROM queues WHERE identity=$1 AND origin='file'`, identity)
		return
	}
	marks := make([]string, len(keep))
	args := []any{identity}
	for i, name := range keep {
		marks[i] = fmt.Sprintf("$%d", i+2)
		args = append(args, name)
	}
	s.DB.Exec(`DELETE FROM queues WHERE identity=$1 AND origin='file' AND name NOT IN (`+
		strings.Join(marks, ",")+`)`, args...)
}

func (s *PgStore) Enqueue(identity, name, payload, dedupeKey string, maxAttempts int, availableAt time.Time) (int64, bool, error) {
	var key any
	if dedupeKey != "" {
		key = dedupeKey
	}
	var id int64
	// See the SQLite twin: max_attempts is pinned from the handler definition at dispatch time, with
	// the caller's value as the fallback before that definition exists.
	err := s.DB.QueryRow(`INSERT INTO queue_jobs
		(identity, name, payload, dedupe_key, status, attempt, max_attempts, available_at, created_at)
		VALUES ($1,$2,$3,$4,'pending',0,
			COALESCE((SELECT max_attempts FROM queues WHERE identity=$1 AND name=$2), $5), $6, NOW())
		ON CONFLICT (identity, name, dedupe_key) DO NOTHING
		RETURNING id`, identity, name, payload, key, maxAttempts, availableAt.UTC()).Scan(&id)
	// DO NOTHING returns no row on a dedupe collision, which arrives here as ErrNoRows. That is the
	// success path for an idempotent dispatch, not a failure.
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return id, true, nil
}

// Claim is one statement: the UPDATE selects its own targets with FOR UPDATE SKIP LOCKED, so a node
// takes rows no other node is already taking, without a lock table and without a round trip that
// another node could interleave with.
func (s *PgStore) Claim(identity, nodeID string, leaseTTL time.Duration, limit int) []ClaimedJob {
	secs := int(leaseTTL.Seconds())
	if secs < 1 {
		secs = 1
	}
	rows, err := s.DB.Query(`
		UPDATE queue_jobs SET status='running', node=$1,
			lease_until=NOW() + ($2 * interval '1 second'), started_at=NOW(), attempt=attempt+1
		WHERE id IN (
			SELECT id FROM queue_jobs
			WHERE identity=$3 AND status='pending' AND available_at <= NOW()
			ORDER BY available_at, id FOR UPDATE SKIP LOCKED LIMIT $4)
		RETURNING id, name, payload, attempt, max_attempts`, nodeID, secs, identity, limit)
	if err != nil {
		fmt.Printf("[Queue] pg claim: %v\n", err)
		return nil
	}
	defer rows.Close()
	var won []ClaimedJob
	for rows.Next() {
		var j ClaimedJob
		var payload sql.NullString
		if rows.Scan(&j.ID, &j.Name, &payload, &j.Attempt, &j.MaxAttempts) == nil {
			j.Payload = payload.String
			won = append(won, j)
		}
	}
	return won
}

func (s *PgStore) Complete(jobID int64, output string, gas int64) {
	s.DB.Exec(`UPDATE queue_jobs SET status='completed', finished_at=NOW(), output=$1, gas_used=$2,
		error_message=NULL, lease_until=NULL WHERE id=$3`, output, gas, jobID)
}

func (s *PgStore) Fail(jobID int64, attempt int, retry bool, availableAt time.Time, errMsg, output string, gas int64) {
	if retry {
		s.DB.Exec(`UPDATE queue_jobs SET status='pending', attempt=$1, available_at=$2,
			error_message=$3, output=$4, gas_used=$5, node=NULL, lease_until=NULL, started_at=NULL
			WHERE id=$6`, attempt, availableAt.UTC(), errMsg, output, gas, jobID)
		return
	}
	s.DB.Exec(`UPDATE queue_jobs SET status='failed', attempt=$1, finished_at=NOW(),
		error_message=$2, output=$3, gas_used=$4, lease_until=NULL WHERE id=$5`,
		attempt, errMsg, output, gas, jobID)
}

func (s *PgStore) RecordSummary(identity, name, status string) {
	failInc := 0
	if status == "failed" {
		failInc = 1
	}
	s.DB.Exec(`UPDATE queues SET last_run=NOW(), last_status=$3, processed_count=processed_count+1,
		failed_count=failed_count+$4 WHERE identity=$1 AND name=$2`, identity, name, status, failInc)
}

func (s *PgStore) Heartbeat(nodeID string, leaseTTL time.Duration) {
	secs := int(leaseTTL.Seconds())
	if secs < 1 {
		secs = 1
	}
	s.DB.Exec(`UPDATE queue_jobs SET lease_until=NOW() + ($1 * interval '1 second')
		WHERE node=$2 AND status='running'`, secs, nodeID)
}

// Reclaim frees only EXPIRED leases. A running job on a live node is not orphaned merely because
// another node is looking at it — the heartbeat is what distinguishes busy from dead, so the
// condition here is lease expiry and never boot.
func (s *PgStore) Reclaim(identity string, bootWipe bool) int64 {
	res, _ := s.DB.Exec(`UPDATE queue_jobs
		SET status='pending', node=NULL, lease_until=NULL, started_at=NULL
		WHERE identity=$1 AND status='running' AND lease_until < NOW()`, identity)
	if res == nil {
		return 0
	}
	n, _ := res.RowsAffected()
	return n
}

func (s *PgStore) Retention(identity, name string, completedBefore, failedBefore time.Time) {
	s.DB.Exec(`DELETE FROM queue_jobs WHERE identity=$1 AND name=$2 AND status='completed' AND created_at < $3`,
		identity, name, completedBefore.UTC())
	s.DB.Exec(`DELETE FROM queue_jobs WHERE identity=$1 AND name=$2 AND status='failed' AND created_at < $3`,
		identity, name, failedBefore.UTC())
}

func (s *PgStore) ListQueues(identity string) []map[string]any {
	rows, err := s.DB.Query(`SELECT name, source, max_attempts, retention, status,
		last_run, last_status, processed_count, failed_count, updated_at
		FROM queues WHERE identity=$1 ORDER BY name`, identity)
	if err != nil {
		return nil
	}
	defer rows.Close()
	return scanQueues(rows)
}

func (s *PgStore) ListJobs(identity, name, status string, limit int) []map[string]any {
	q, args := listJobsQuery("$", identity, name, status, limit)
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	return scanJobs(rows)
}

// RetryFailed is the Postgres twin of the SQLite one, except availability comes from NOW(): on the
// shared store the database clock is the only clock, so a replay scheduled by a node with a skewed
// clock must not become available at a time the other nodes disagree with.
func (s *PgStore) RetryFailed(identity, name string, jobID int64) int64 {
	args := []any{identity}
	where := "identity=$1 AND status='failed'"
	if name != "" {
		args = append(args, name)
		where += fmt.Sprintf(" AND name=$%d", len(args))
	}
	if jobID > 0 {
		args = append(args, jobID)
		where += fmt.Sprintf(" AND id=$%d", len(args))
	}
	res, err := s.DB.Exec(`UPDATE queue_jobs SET status='pending', attempt=0, node=NULL,
		lease_until=NULL, started_at=NULL, finished_at=NULL, available_at=NOW()
		WHERE `+where, args...)
	if err != nil || res == nil {
		return 0
	}
	n, _ := res.RowsAffected()
	return n
}

// ── shared SQL shaping ───────────────────────────────────────────────────────────────────────────

// placeholders builds the driver's parameter marks. SQLite wants "?" every time; Postgres wants
// $1, $2, … in order. One helper rather than two copies of every query, because the only difference
// between the two dialects HERE is the mark.
func placeholders(style string, n int) []string {
	out := make([]string, n)
	for i := range out {
		if style == "$" {
			out[i] = fmt.Sprintf("$%d", i+1)
		} else {
			out[i] = "?"
		}
	}
	return out
}

func listJobsQuery(style, identity, name, status string, limit int) (string, []any) {
	if limit < 1 {
		limit = 50
	}
	args := []any{identity}
	where := []string{"identity=%s"}
	if name != "" {
		args = append(args, name)
		where = append(where, "name=%s")
	}
	if status != "" {
		args = append(args, status)
		where = append(where, "status=%s")
	}
	args = append(args, limit)

	marks := placeholders(style, len(args))
	clauses := make([]string, len(where))
	for i, w := range where {
		clauses[i] = fmt.Sprintf(w, marks[i])
	}
	q := `SELECT id, name, status, attempt, max_attempts, payload, error_message, output,
			gas_used, created_at, finished_at
		FROM queue_jobs WHERE ` + strings.Join(clauses, " AND ") +
		` ORDER BY id DESC LIMIT ` + marks[len(marks)-1]
	return q, args
}

func scanQueues(rows *sql.Rows) []map[string]any {
	var out []map[string]any
	for rows.Next() {
		var name, source, status, updated string
		var lastRun, lastStatus sql.NullString
		var maxAttempts, retention int
		var processed, failed int64
		if rows.Scan(&name, &source, &maxAttempts, &retention, &status,
			&lastRun, &lastStatus, &processed, &failed, &updated) != nil {
			continue
		}
		m := map[string]any{
			"name": name, "source": source, "maxAttempts": maxAttempts, "retention": retention,
			"status": status, "processed": processed, "failed": failed, "updatedAt": updated,
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

func scanJobs(rows *sql.Rows) []map[string]any {
	var out []map[string]any
	for rows.Next() {
		var id int64
		var name, status, created string
		var attempt, maxAttempts int
		var payload, errMsg, output, finished sql.NullString
		var gas sql.NullInt64
		if rows.Scan(&id, &name, &status, &attempt, &maxAttempts, &payload, &errMsg,
			&output, &gas, &created, &finished) != nil {
			continue
		}
		m := map[string]any{
			"id": id, "name": name, "status": status, "attempt": attempt,
			"maxAttempts": maxAttempts, "createdAt": created,
		}
		if payload.Valid {
			m["payload"] = payload.String
		}
		if errMsg.Valid {
			m["error"] = errMsg.String
		}
		if output.Valid {
			m["output"] = output.String
		}
		if gas.Valid {
			m["gas"] = gas.Int64
		}
		if finished.Valid {
			m["finishedAt"] = finished.String
		}
		out = append(out, m)
	}
	return out
}
