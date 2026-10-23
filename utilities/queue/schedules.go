package queue

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"
)

// The state of an app's schedules (KITWORK_SCHEDULE.md mục 5, 9). A schedule's runs are rows of
// kitwork_runs of kind 'schedule', one per due time, keyed by the due time (dedupe_key), so a due time is
// stored once whatever restarts; kitwork_schedules keeps, per schedule, what the next boot must not lose:
// the rule it was synced with, the due time a once() chose, and the last due time handled.

// ScheduleState is one schedule's row of kitwork_schedules. Times are UTC; OnceAt is zero for a schedule
// that is not a once().
type ScheduleState struct {
	Name string
	// Rule is the rule as written, its zone with it; another rule is another schedule's state.
	Rule       string
	DeclaredAt time.Time
	OnceAt     time.Time
	// LastAt is the last due time handled: stored as a run, or passed over because a run of the schedule had
	// not ended. A due time at or before it is never stored again.
	LastAt time.Time
}

// ScheduleStates reads the state of every schedule, by name.
func (s *Store) ScheduleStates() (map[string]ScheduleState, error) {
	states := map[string]ScheduleState{}
	err := s.database.Read(func(db *sql.DB) error {
		rows, err := db.Query(`SELECT name, rule, declared_at, once_at, last_at FROM kitwork_schedules`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var state ScheduleState
			var declared, last string
			var once sql.NullString
			if err := rows.Scan(&state.Name, &state.Rule, &declared, &once, &last); err != nil {
				return err
			}
			if state.DeclaredAt, err = time.Parse(stampLayout, declared); err != nil {
				return err
			}
			if state.LastAt, err = time.Parse(stampLayout, last); err != nil {
				return err
			}
			if once.Valid {
				if state.OnceAt, err = time.Parse(stampLayout, once.String); err != nil {
					return err
				}
			}
			states[state.Name] = state
		}
		return rows.Err()
	})
	return states, err
}

// SyncSchedules writes the states in put (a schedule declared for the first time, or with another rule)
// and deletes those of the names in remove (no file declares them any more), in one write.
func (s *Store) SyncSchedules(put []ScheduleState, remove []string, now time.Time) error {
	if len(put) == 0 && len(remove) == 0 {
		return nil
	}
	return s.database.Write(func(db *sql.DB) error {
		for _, state := range put {
			var once any
			if !state.OnceAt.IsZero() {
				once = Stamp(state.OnceAt)
			}
			if _, err := db.Exec(`INSERT INTO kitwork_schedules (name, rule, declared_at, once_at, last_at, updated_at)
				VALUES ($1, $2, $3, $4, $5, $6)
				ON CONFLICT (name) DO UPDATE SET rule = excluded.rule, declared_at = excluded.declared_at,
				once_at = excluded.once_at, last_at = excluded.last_at, updated_at = excluded.updated_at`,
				state.Name, state.Rule, Stamp(state.DeclaredAt), once, Stamp(state.LastAt), Stamp(now)); err != nil {
				return fmt.Errorf("kitwork_schedules %s: %w", state.Name, err)
			}
		}
		sorted := append([]string(nil), remove...)
		sort.Strings(sorted)
		for _, name := range sorted {
			if _, err := db.Exec(`DELETE FROM kitwork_schedules WHERE name = $1`, name); err != nil {
				return err
			}
		}
		return nil
	})
}

// The outcomes of Dispatch.
const (
	// Dispatched: the run of the due time is stored.
	Dispatched = "dispatched"
	// Busy: a run of the schedule has not ended (pending, running): the due time is passed over
	// (KITWORK_SCHEDULE.md mục 12.4 is open; the engine keeps one run of a schedule at a time).
	Busy = "busy"
	// Held: the due time has a run already (another process stored it, or a restart in the same due time).
	Held = "held"
)

// ErrNoSchedule is a schedule kitwork_schedules has no row of: its state was not synced.
var ErrNoSchedule = errors.New("queue: schedule not synced")

// Dispatch stores the run of one due time of a schedule, run.Key the due time, and moves the schedule's
// LastAt to at, in one write. A due time at or before LastAt was handled and stores nothing (Held); so does
// a key held. When skipBusy, a run of the schedule that has not ended passes the due time over (Busy), its
// LastAt moved all the same: a schedule that runs longer than its interval does not pile up runs.
func (s *Store) Dispatch(at time.Time, run NewRun, skipBusy bool, now time.Time) (Run, string, error) {
	var stored Run
	outcome := ""
	err := s.database.Write(func(db *sql.DB) error {
		var last string
		err := db.QueryRow(`SELECT last_at FROM kitwork_schedules WHERE name = $1`, run.Name).Scan(&last)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNoSchedule
		}
		if err != nil {
			return err
		}
		if last >= Stamp(at) {
			outcome = Held
			return nil
		}
		if skipBusy {
			var id string
			err := db.QueryRow(`SELECT id FROM kitwork_runs WHERE kind = $1 AND name = $2
				AND status IN ('pending', 'running', 'cancelling') LIMIT 1`, s.kind, run.Name).Scan(&id)
			switch {
			case err == nil:
				outcome = Busy
			case !errors.Is(err, sql.ErrNoRows):
				return err
			}
		}
		if outcome == "" {
			var duplicate bool
			stored, duplicate, err = s.insert(db, run, now)
			if err != nil {
				return err
			}
			outcome = Dispatched
			if duplicate {
				outcome = Held
			}
		}
		_, err = db.Exec(`UPDATE kitwork_schedules SET last_at = $1, updated_at = $2 WHERE name = $3`,
			Stamp(at), Stamp(now), run.Name)
		return err
	})
	return stored, outcome, err
}
