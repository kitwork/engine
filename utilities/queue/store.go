// Package queue keeps the runs of an app's background work in kitwork_runs, the engine's table in the app's
// own database (KITWORK_QUEUE.md mục 2, 11): one row per run, written before send() answers, claimed with a
// lease and a token, settled only by the holder of the token (fencing), kept for `keep` after it ends.
// Beside it, kitwork_definitions holds what each job was declared with, so list() reads the definitions
// whether anything was sent or not.
//
// Both tables carry a kind ('queue' today, 'schedule' when KITWORK_SCHEDULE.md moves onto the same store),
// so the two share the runs, the lease and the retry without sharing a namespace.
//
// The statements are KitDB's SQL (the app's database is a KitDB file in .database/application/): ON
// CONFLICT, LIMIT/OFFSET written as numbers, no MAX(), no CASE, no SKIP LOCKED. Columns are TEXT and
// INTEGER alone, so the app's own ORM, which reads every table of the file's catalogue, reads these two as
// well. Times are text of one width (2026-10-22T08:30:00.000Z), so text order is time order. A write is
// one writer at a time (Database.Write), which makes a read and the write after it one step: a claim, a
// settle, a cancel and a replay check the row and change it with nothing in between.
package queue

import (
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Database is where the runs are kept: the app's database through SQL. Write runs one write at a time,
// with every other write of the database (the app's own included) held back while it runs.
type Database interface {
	Read(fn func(db *sql.DB) error) error
	Write(fn func(db *sql.DB) error) error
}

// Sequencer is a Database that numbers new runs itself, inside Write: the number after the highest seq
// of the table, read once and counted on from there, rather than read from the table at every send. A
// Database that is not one is read each time.
type Sequencer interface {
	NextSeq(db *sql.DB) (int64, error)
}

// HighestSeq reads the highest seq of kitwork_runs, 0 for an empty table: where a Sequencer starts.
func HighestSeq(db *sql.DB) (int64, error) {
	var last sql.NullInt64
	err := db.QueryRow(`SELECT seq FROM kitwork_runs ORDER BY seq DESC LIMIT 1`).Scan(&last)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	return last.Int64, nil
}

// The statuses of a run (mục 7).
const (
	Pending    = "pending"
	Running    = "running"
	Completed  = "completed"
	Failed     = "failed"
	Cancelling = "cancelling"
	Cancelled  = "cancelled"
)

// Statuses is every status a run can have, in the order of its life.
var Statuses = []string{Pending, Running, Completed, Failed, Cancelling, Cancelled}

// ErrNotFound is a run this kind has no row of: never sent, pruned by keep, or another job's.
var ErrNotFound = errors.New("queue: run not found")

// StateError is an operation the run's status refuses: cancel of a run that ended, replay of one that
// has not.
type StateError struct {
	Run Run
}

func (e *StateError) Error() string {
	return fmt.Sprintf("queue: run %s is %s", e.Run.ID, e.Run.Status)
}

// Run is one row of kitwork_runs. Times are the stored text ("" when not set).
type Run struct {
	ID           string
	Name         string
	Key          string
	Status       string
	Attempt      int
	MaxAttempts  int
	TimeoutMS    int
	Data         string // JSON, as sent
	Log          string
	ErrorCode    string
	ErrorMessage string
	CreatedAt    string
	StartedAt    string
	FinishedAt   string
	AvailableAt  string
	ReplayOf     string
}

// Claim is a run this worker won the right to run: Token is the lease it holds, which every settle names.
type Claim struct {
	Run
	Token string
}

// NewRun is what send() and replay() store.
type NewRun struct {
	Name        string
	Data        string
	Key         string
	MaxAttempts int
	Timeout     time.Duration
	Available   time.Time
	ReplayOf    string
}

// Definition is one job as declared: its name, the file, the extra attempts and the time limit written.
type Definition struct {
	Name        string
	Source      string
	Retry       int
	Timeout     string // as written ("30s"), "" when none
	TimeoutMS   int
	ContentHash string
}

// Store is the runs of one kind in one database.
type Store struct {
	database Database
	kind     string
}

// New is the store of kind's runs in database.
func New(database Database, kind string) *Store {
	return &Store{database: database, kind: kind}
}

// stampLayout is how the tables write a time.
const stampLayout = "2006-01-02T15:04:05.000Z"

// Stamp is a time as the tables keep it: UTC to the millisecond, one width.
func Stamp(t time.Time) string { return t.UTC().Format(stampLayout) }

// Schema is the statements that make both tables, each a no-op when its table is there.
var Schema = []string{
	`CREATE TABLE IF NOT EXISTS kitwork_runs (
		id TEXT PRIMARY KEY,
		seq INTEGER NOT NULL,
		kind TEXT NOT NULL,
		name TEXT NOT NULL,
		dedupe_key TEXT,
		status TEXT NOT NULL,
		attempt INTEGER NOT NULL,
		max_attempts INTEGER NOT NULL,
		timeout_ms INTEGER NOT NULL,
		data TEXT,
		log TEXT,
		error_code TEXT,
		error_message TEXT,
		available_at TEXT NOT NULL,
		lease_token TEXT,
		lease_until TEXT,
		created_at TEXT NOT NULL,
		started_at TEXT,
		finished_at TEXT,
		replay_of TEXT,
		UNIQUE (kind, name, dedupe_key))`,
	`CREATE INDEX IF NOT EXISTS kitwork_runs_due ON kitwork_runs (kind, status, available_at)`,
	`CREATE INDEX IF NOT EXISTS kitwork_runs_seq ON kitwork_runs (seq)`,
	`CREATE TABLE IF NOT EXISTS kitwork_definitions (
		kind TEXT NOT NULL,
		name TEXT NOT NULL,
		source TEXT NOT NULL,
		retry INTEGER NOT NULL,
		timeout TEXT,
		timeout_ms INTEGER NOT NULL,
		content_hash TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		PRIMARY KEY (kind, name))`,
}

// InitSchema makes both tables in db.
func InitSchema(db *sql.DB) error {
	for _, statement := range Schema {
		if _, err := db.Exec(statement); err != nil {
			return fmt.Errorf("kitwork_runs: %w", err)
		}
	}
	return nil
}

const runColumns = `id, name, dedupe_key, status, attempt, max_attempts, timeout_ms, data, log, error_code,
	error_message, created_at, started_at, finished_at, available_at, replay_of`

type scanner interface {
	Scan(dest ...any) error
}

func scanRun(row scanner) (Run, error) {
	var run Run
	var key, data, log, code, message, started, finished, replayOf sql.NullString
	err := row.Scan(&run.ID, &run.Name, &key, &run.Status, &run.Attempt, &run.MaxAttempts, &run.TimeoutMS,
		&data, &log, &code, &message, &run.CreatedAt, &started, &finished, &run.AvailableAt, &replayOf)
	if err != nil {
		return Run{}, err
	}
	run.Key, run.Data, run.Log = key.String, data.String, log.String
	run.ErrorCode, run.ErrorMessage = code.String, message.String
	run.StartedAt, run.FinishedAt, run.ReplayOf = started.String, finished.String, replayOf.String
	return run, nil
}

// nullable is s, or NULL for "".
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// RunID is a new run's id: r_ and 16 letters and digits.
func RunID() string { return "r_" + randomText(10) }

func randomText(bytes int) string {
	buffer := make([]byte, bytes)
	if _, err := rand.Read(buffer); err != nil {
		panic(err)
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buffer))
}

func newToken() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buffer)
}

// get reads one run of this kind by id, under whatever the caller holds.
func (s *Store) get(db *sql.DB, id string) (Run, error) {
	run, err := scanRun(db.QueryRow(`SELECT `+runColumns+` FROM kitwork_runs WHERE id = $1 AND kind = $2`, id, s.kind))
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	return run, err
}

// Get reads one run by id.
func (s *Store) Get(id string) (Run, error) {
	var run Run
	err := s.database.Read(func(db *sql.DB) error {
		var err error
		run, err = s.get(db, id)
		return err
	})
	return run, err
}

// insert stores a run as pending. A key another run of the same job holds stores nothing: the run holding
// it is given back with duplicate true. The UNIQUE (kind, name, dedupe_key) of the table decides, not a read
// before; a run without a key never collides (NULL keys are distinct).
func (s *Store) insert(db *sql.DB, run NewRun, now time.Time) (Run, bool, error) {
	var seq int64
	var err error
	if sequencer, ok := s.database.(Sequencer); ok {
		seq, err = sequencer.NextSeq(db)
	} else {
		seq, err = HighestSeq(db)
		seq++
	}
	if err != nil {
		return Run{}, false, err
	}
	var key, replayOf any
	if run.Key != "" {
		key = run.Key
	}
	if run.ReplayOf != "" {
		replayOf = run.ReplayOf
	}
	available := run.Available
	if available.IsZero() || available.Before(now) {
		available = now
	}
	id := RunID()
	var inserted string
	err = db.QueryRow(`INSERT INTO kitwork_runs (id, seq, kind, name, dedupe_key, status, attempt, max_attempts,
		timeout_ms, data, available_at, created_at, replay_of)
		VALUES ($1, $2, $3, $4, $5, 'pending', 0, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (kind, name, dedupe_key) DO NOTHING RETURNING id`,
		id, seq, s.kind, run.Name, key, run.MaxAttempts, int(run.Timeout/time.Millisecond), run.Data,
		Stamp(available), Stamp(now), replayOf).Scan(&inserted)
	if errors.Is(err, sql.ErrNoRows) {
		held, err := scanRun(db.QueryRow(`SELECT `+runColumns+` FROM kitwork_runs
			WHERE kind = $1 AND name = $2 AND dedupe_key = $3`, s.kind, run.Name, run.Key))
		if err != nil {
			return Run{}, false, fmt.Errorf("kitwork_runs: key %q is held, its run not read: %w", run.Key, err)
		}
		return held, true, nil
	}
	if err != nil {
		return Run{}, false, err
	}
	stored, err := s.get(db, inserted)
	return stored, false, err
}

// Enqueue stores a run, or gives back the run holding its key (duplicate true).
func (s *Store) Enqueue(run NewRun, now time.Time) (Run, bool, error) {
	var stored Run
	var duplicate bool
	err := s.database.Write(func(db *sql.DB) error {
		var err error
		stored, duplicate, err = s.insert(db, run, now)
		return err
	})
	return stored, duplicate, err
}

// except is the condition that leaves out the runs of the jobs in names, its arguments numbered from
// first: " AND name <> $3 AND name <> $4", in the names' order.
func except(names map[string]bool, first int) (string, []any) {
	sorted := make([]string, 0, len(names))
	for name, out := range names {
		if out {
			sorted = append(sorted, name)
		}
	}
	sort.Strings(sorted)
	var condition strings.Builder
	arguments := make([]any, 0, len(sorted))
	for index, name := range sorted {
		fmt.Fprintf(&condition, " AND name <> $%d", first+index)
		arguments = append(arguments, name)
	}
	return condition.String(), arguments
}

// Claim takes up to limit runs that are due, oldest due first, each with a new lease token held until
// leaseUntil, and leaves the runs of the jobs in withheld pending (a job whose file did not load: its runs
// wait for the fix). It also gives when the first pending run it left is due, the zero time when it left
// none: the worker does not look again before then, unless a send, a replay or a retry tells it of an
// earlier one. The reads and the updates are one step (Database.Write), and every update checks the run
// is still pending.
func (s *Store) Claim(now time.Time, lease time.Duration, limit int, withheld map[string]bool) ([]Claim, time.Time, error) {
	if limit < 1 {
		return nil, now, nil
	}
	var claims []Claim
	var next time.Time
	err := s.database.Write(func(db *sql.DB) error {
		condition, names := except(withheld, 3)
		rows, err := db.Query(fmt.Sprintf(`SELECT `+runColumns+` FROM kitwork_runs
			WHERE kind = $1 AND status = 'pending' AND available_at <= $2%s
			ORDER BY available_at, seq LIMIT %d`, condition, limit), append([]any{s.kind, Stamp(now)}, names...)...)
		if err != nil {
			return err
		}
		var due []Run
		for rows.Next() {
			run, err := scanRun(rows)
			if err != nil {
				rows.Close()
				return err
			}
			due = append(due, run)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, run := range due {
			token := newToken()
			result, err := db.Exec(`UPDATE kitwork_runs SET status = 'running', attempt = $1, lease_token = $2,
				lease_until = $3, started_at = $4 WHERE id = $5 AND status = 'pending'`,
				run.Attempt+1, token, Stamp(now.Add(lease)), Stamp(now), run.ID)
			if err != nil {
				return err
			}
			if affected, _ := result.RowsAffected(); affected != 1 {
				continue
			}
			run.Status, run.Attempt, run.StartedAt = Running, run.Attempt+1, Stamp(now)
			claims = append(claims, Claim{Run: run, Token: token})
		}
		condition, names = except(withheld, 2)
		var first string
		err = db.QueryRow(fmt.Sprintf(`SELECT available_at FROM kitwork_runs WHERE kind = $1 AND status = 'pending'%s
			ORDER BY available_at LIMIT 1`, condition), append([]any{s.kind}, names...)...).Scan(&first)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return nil
		case err != nil:
			return err
		}
		next, err = time.Parse(stampLayout, first)
		return err
	})
	return claims, next, err
}

// held reads a run and reports whether token still holds it: the run is running or cancelling under that
// lease. A run reclaimed, settled or cancelled since is held by no one, or by another token.
func (s *Store) held(db *sql.DB, id, token string) (Run, bool, error) {
	run, err := s.get(db, id)
	if errors.Is(err, ErrNotFound) {
		return Run{}, false, nil
	}
	if err != nil {
		return Run{}, false, err
	}
	var current sql.NullString
	if err := db.QueryRow(`SELECT lease_token FROM kitwork_runs WHERE id = $1`, id).Scan(&current); err != nil {
		return Run{}, false, err
	}
	if !current.Valid || current.String != token || (run.Status != Running && run.Status != Cancelling) {
		return run, false, nil
	}
	return run, true, nil
}

// Complete records a run that ran to its end, as the holder of token; the run's log is replaced by log. It
// reports whether token still held the run: an attempt whose lease was taken from it (timed out, reclaimed)
// never reports success. A run being cancelled that finished first is completed (mục 7).
func (s *Store) Complete(id, token, log string, now time.Time) (bool, error) {
	won := false
	err := s.database.Write(func(db *sql.DB) error {
		_, held, err := s.held(db, id, token)
		if err != nil || !held {
			return err
		}
		result, err := db.Exec(`UPDATE kitwork_runs SET status = 'completed', finished_at = $1, log = $2,
			error_code = NULL, error_message = NULL, lease_token = NULL, lease_until = NULL
			WHERE id = $3 AND lease_token = $4`, Stamp(now), log, id, token)
		if err != nil {
			return err
		}
		affected, _ := result.RowsAffected()
		won = affected == 1
		return nil
	})
	return won, err
}

// Fail records an attempt that failed, as the holder of token. A run with attempts left goes back to
// pending until retryAt; one without, or any when retryAt is the zero time (a failure no attempt mends),
// ends failed (the dead letter: the row stays, with its data and its error); one being cancelled ends
// cancelled, never retried. code and message "" store no error. It returns the status the run took, ""
// when token no longer held it.
func (s *Store) Fail(id, token, code, message, log string, retryAt, now time.Time) (string, error) {
	status := ""
	err := s.database.Write(func(db *sql.DB) error {
		run, held, err := s.held(db, id, token)
		if err != nil || !held {
			return err
		}
		switch {
		case run.Status == Cancelling:
			status = Cancelled
		case run.Attempt < run.MaxAttempts && !retryAt.IsZero():
			status = Pending
		default:
			status = Failed
		}
		var result sql.Result
		if status == Pending {
			result, err = db.Exec(`UPDATE kitwork_runs SET status = 'pending', available_at = $1, log = $2,
				error_code = $3, error_message = $4, lease_token = NULL, lease_until = NULL
				WHERE id = $5 AND lease_token = $6`, Stamp(retryAt), log, nullable(code), nullable(message), id, token)
		} else {
			result, err = db.Exec(`UPDATE kitwork_runs SET status = $1, finished_at = $2, log = $3,
				error_code = $4, error_message = $5, lease_token = NULL, lease_until = NULL
				WHERE id = $6 AND lease_token = $7`, status, Stamp(now), log, nullable(code), nullable(message), id, token)
		}
		if err != nil {
			status = ""
			return err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			status = ""
		}
		return nil
	})
	return status, err
}

// Extend moves the lease of a run this worker still holds to until. It reports whether token still holds
// it, and the run's status (cancelling tells the worker to stop the attempt).
func (s *Store) Extend(id, token string, until time.Time) (string, bool, error) {
	status, holds := "", false
	err := s.database.Write(func(db *sql.DB) error {
		run, held, err := s.held(db, id, token)
		if err != nil || !held {
			return err
		}
		if _, err := db.Exec(`UPDATE kitwork_runs SET lease_until = $1 WHERE id = $2 AND lease_token = $3`,
			Stamp(until), id, token); err != nil {
			return err
		}
		status, holds = run.Status, true
		return nil
	})
	return status, holds, err
}

// Reclaim gives back the runs whose lease ran out, leaving those in skip (the attempts this process still
// runs and extends). A run with attempts left is due again now; one without ends failed; one being
// cancelled ends cancelled. The attempt that lost its lease counts: it ran, and a run that stops its
// process every time must not run for ever. It returns how many runs it gave back, and when the first lease
// still held by no attempt in skip runs out (the zero time when none is held): a process that stopped
// while it ran an attempt leaves a lease that has not run out yet when the next one boots.
func (s *Store) Reclaim(now time.Time, skip map[string]bool) (int, time.Time, error) {
	count := 0
	var next time.Time
	err := s.database.Write(func(db *sql.DB) error {
		rows, err := db.Query(`SELECT `+runColumns+`, lease_until FROM kitwork_runs
			WHERE kind = $1 AND status IN ('running', 'cancelling')`, s.kind)
		if err != nil {
			return err
		}
		var lost []Run
		for rows.Next() {
			var until sql.NullString
			run, err := scanRun(rowWith{rows, &until})
			if err != nil {
				rows.Close()
				return err
			}
			if skip[run.ID] {
				continue
			}
			if until.Valid && until.String >= Stamp(now) {
				if expires, err := time.Parse(stampLayout, until.String); err == nil && (next.IsZero() || expires.Before(next)) {
					next = expires
				}
				continue
			}
			lost = append(lost, run)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, run := range lost {
			var result sql.Result
			message := fmt.Sprintf("lần thử %d mất lease (engine dừng hay treo khi đang chạy)", run.Attempt)
			switch {
			case run.Status == Cancelling:
				result, err = db.Exec(`UPDATE kitwork_runs SET status = 'cancelled', finished_at = $1,
					lease_token = NULL, lease_until = NULL WHERE id = $2 AND status = 'cancelling'`, Stamp(now), run.ID)
			case run.Attempt < run.MaxAttempts:
				result, err = db.Exec(`UPDATE kitwork_runs SET status = 'pending', available_at = $1,
					error_code = 'TIMEOUT', error_message = $2, lease_token = NULL, lease_until = NULL
					WHERE id = $3 AND status = 'running'`, Stamp(now), message, run.ID)
			default:
				result, err = db.Exec(`UPDATE kitwork_runs SET status = 'failed', finished_at = $1,
					error_code = 'TIMEOUT', error_message = $2, lease_token = NULL, lease_until = NULL
					WHERE id = $3 AND status = 'running'`, Stamp(now), message, run.ID)
			}
			if err != nil {
				return err
			}
			if affected, _ := result.RowsAffected(); affected == 1 {
				count++
			}
		}
		return nil
	})
	return count, next, err
}

// rowWith scans a row of runColumns followed by more columns: the run, then extra.
type rowWith struct {
	row   scanner
	extra *sql.NullString
}

func (r rowWith) Scan(dest ...any) error { return r.row.Scan(append(dest, r.extra)...) }

// Cancel cancels a run (mục 9). A pending run ends cancelled at once; a running one becomes cancelling,
// for its worker to stop; a cancelling or cancelled one is given back as it is. A run that ended
// (completed, failed) is a *StateError. name, when not "", is the job the run must belong to: another
// job's run is ErrNotFound, as an id that does not exist.
func (s *Store) Cancel(id, name string, now time.Time) (Run, error) {
	var run Run
	err := s.database.Write(func(db *sql.DB) error {
		var err error
		run, err = s.get(db, id)
		if err != nil {
			return err
		}
		if name != "" && run.Name != name {
			return ErrNotFound
		}
		switch run.Status {
		case Pending:
			if _, err := db.Exec(`UPDATE kitwork_runs SET status = 'cancelled', finished_at = $1
				WHERE id = $2 AND status = 'pending'`, Stamp(now), id); err != nil {
				return err
			}
		case Running:
			if _, err := db.Exec(`UPDATE kitwork_runs SET status = 'cancelling'
				WHERE id = $1 AND status = 'running'`, id); err != nil {
				return err
			}
		case Cancelling, Cancelled:
			return nil
		default:
			return &StateError{Run: run}
		}
		run, err = s.get(db, id)
		return err
	})
	return run, err
}

// Replay makes a new run from the data of an ended one (failed, cancelled, completed), keeping the old
// one: its own id, attempts and retries, from the job's current definition (mục 9). A run not ended is a
// *StateError; a job no longer declared is ErrNoDefinition: one kitwork_definitions has no row of, or, when
// declared is not nil, one it says no file of the app declares now. name, when not "", is the job the run
// must belong to.
func (s *Store) Replay(id, name string, now time.Time, declared func(name string) bool) (Run, Definition, error) {
	var replayed Run
	var definition Definition
	err := s.database.Write(func(db *sql.DB) error {
		run, err := s.get(db, id)
		if err != nil {
			return err
		}
		if name != "" && run.Name != name {
			return ErrNotFound
		}
		if run.Status != Failed && run.Status != Cancelled && run.Status != Completed {
			return &StateError{Run: run}
		}
		var found bool
		definition, found, err = s.definition(db, run.Name)
		if err != nil {
			return err
		}
		if !found || (declared != nil && !declared(run.Name)) {
			definition = Definition{Name: run.Name}
			return ErrNoDefinition
		}
		replayed, _, err = s.insert(db, NewRun{
			Name: run.Name, Data: run.Data, MaxAttempts: definition.Retry + 1,
			Timeout: time.Duration(definition.TimeoutMS) * time.Millisecond, ReplayOf: run.ID,
		}, now)
		return err
	})
	return replayed, definition, err
}

// ErrNoDefinition is a job no file declares any more: its runs cannot be replayed.
var ErrNoDefinition = errors.New("queue: job no longer declared")

// Filter chooses runs: of one job, in one status ("" for any).
type Filter struct {
	Name   string
	Status string
}

// Runs reads runs newest first, limit of them after skipping offset.
func (s *Store) Runs(filter Filter, limit, offset int) ([]Run, error) {
	var runs []Run
	err := s.database.Read(func(db *sql.DB) error {
		where := []string{"kind = $1"}
		arguments := []any{s.kind}
		if filter.Name != "" {
			arguments = append(arguments, filter.Name)
			where = append(where, fmt.Sprintf("name = $%d", len(arguments)))
		}
		if filter.Status != "" {
			arguments = append(arguments, filter.Status)
			where = append(where, fmt.Sprintf("status = $%d", len(arguments)))
		}
		rows, err := db.Query(fmt.Sprintf(`SELECT `+runColumns+` FROM kitwork_runs WHERE %s
			ORDER BY seq DESC LIMIT %d OFFSET %d`, strings.Join(where, " AND "), limit, offset), arguments...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			run, err := scanRun(rows)
			if err != nil {
				return err
			}
			runs = append(runs, run)
		}
		return rows.Err()
	})
	return runs, err
}

// Prune deletes the runs that ended before before (keep): their keys are free again, and replay of them is
// refused. It returns how many it deleted.
func (s *Store) Prune(before time.Time) (int, error) {
	count := 0
	err := s.database.Write(func(db *sql.DB) error {
		result, err := db.Exec(`DELETE FROM kitwork_runs WHERE kind = $1
			AND status IN ('completed', 'failed', 'cancelled') AND finished_at < $2`, s.kind, Stamp(before))
		if err != nil {
			return err
		}
		affected, _ := result.RowsAffected()
		count = int(affected)
		return nil
	})
	return count, err
}

func (s *Store) definition(db *sql.DB, name string) (Definition, bool, error) {
	var definition Definition
	var timeout sql.NullString
	err := db.QueryRow(`SELECT name, source, retry, timeout, timeout_ms, content_hash FROM kitwork_definitions
		WHERE kind = $1 AND name = $2`, s.kind, name).Scan(&definition.Name, &definition.Source, &definition.Retry,
		&timeout, &definition.TimeoutMS, &definition.ContentHash)
	if errors.Is(err, sql.ErrNoRows) {
		return Definition{}, false, nil
	}
	definition.Timeout = timeout.String
	return definition, err == nil, err
}

// Definitions reads the declared jobs by name, limit of them after skipping offset.
func (s *Store) Definitions(limit, offset int) ([]Definition, error) {
	var definitions []Definition
	err := s.database.Read(func(db *sql.DB) error {
		rows, err := db.Query(fmt.Sprintf(`SELECT name, source, retry, timeout, timeout_ms, content_hash
			FROM kitwork_definitions WHERE kind = $1 ORDER BY name LIMIT %d OFFSET %d`, limit, offset), s.kind)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var definition Definition
			var timeout sql.NullString
			if err := rows.Scan(&definition.Name, &definition.Source, &definition.Retry, &timeout,
				&definition.TimeoutMS, &definition.ContentHash); err != nil {
				return err
			}
			definition.Timeout = timeout.String
			definitions = append(definitions, definition)
		}
		return rows.Err()
	})
	return definitions, err
}

// Withheld is what a load could not read: the jobs whose declaration has a problem, by name, and the files
// that did not compile or run, by source (.shared/queue/camOn.work). Their definitions stay as they were
// stored: a deploy with a mistake in a file is not the removal of its jobs.
type Withheld struct {
	Names   map[string]bool
	Sources map[string]bool
}

// Sync makes kitwork_definitions what the files declare: a definition whose content hash is unchanged is
// not written again, one no file declares any more is deleted, unless withheld holds it (its name, or the
// file it was stored from). It returns the names of the definitions it kept for withheld, whose runs the
// worker leaves pending until the file loads again. Runs are never touched: a pending run of a job that is
// gone stays, and fails when claimed, saying so.
func (s *Store) Sync(definitions []Definition, withheld Withheld, now time.Time) ([]string, error) {
	var kept []string
	err := s.database.Write(func(db *sql.DB) error {
		stored := map[string]string{}
		sources := map[string]string{}
		rows, err := db.Query(`SELECT name, source, content_hash FROM kitwork_definitions WHERE kind = $1`, s.kind)
		if err != nil {
			return err
		}
		for rows.Next() {
			var name, source, hash string
			if err := rows.Scan(&name, &source, &hash); err != nil {
				rows.Close()
				return err
			}
			stored[name], sources[name] = hash, source
		}
		if err := rows.Close(); err != nil {
			return err
		}
		declared := map[string]bool{}
		for _, definition := range definitions {
			declared[definition.Name] = true
			if hash, found := stored[definition.Name]; found && hash == definition.ContentHash {
				continue
			}
			var timeout any
			if definition.Timeout != "" {
				timeout = definition.Timeout
			}
			if _, err := db.Exec(`INSERT INTO kitwork_definitions (kind, name, source, retry, timeout, timeout_ms,
				content_hash, updated_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
				ON CONFLICT (kind, name) DO UPDATE SET source = excluded.source, retry = excluded.retry,
				timeout = excluded.timeout, timeout_ms = excluded.timeout_ms, content_hash = excluded.content_hash,
				updated_at = excluded.updated_at`,
				s.kind, definition.Name, definition.Source, definition.Retry, timeout, definition.TimeoutMS,
				definition.ContentHash, Stamp(now)); err != nil {
				return fmt.Errorf("kitwork_definitions %s: %w", definition.Name, err)
			}
		}
		for name := range stored {
			if declared[name] {
				continue
			}
			if withheld.Names[name] || withheld.Sources[sources[name]] {
				kept = append(kept, name)
				continue
			}
			if _, err := db.Exec(`DELETE FROM kitwork_definitions WHERE kind = $1 AND name = $2`, s.kind, name); err != nil {
				return err
			}
		}
		sort.Strings(kept)
		return nil
	})
	return kept, err
}
