package work

import (
	"database/sql"
	"time"

	cronhelper "github.com/kitwork/engine/helpers/cron"
)

type cronStore interface {
	label() string
	initSchema() error
	sync(identity string, jobs []*CronJob) error
	reclaim(identity string, bootWipe bool) int64
	hasActive(identity, name string) bool
	insertSlot(identity, name string, slot time.Time, maxAttempts int) error
	claim(identity, nodeID string, leaseTTL time.Duration, limit int) []claimedRow
	complete(execID int64, output string, gas int64)
	fail(execID int64, attempt int, retry bool, availableAt time.Time, errMsg, output string, gas int64)
	recordSummary(identity, name, status string)
	heartbeat(nodeID string, leaseTTL time.Duration)
	retention(identity, name string, completedBefore, failedBefore time.Time)
	listCrons(identity string) []map[string]any
}

func cronJobsToRecords(jobs []*CronJob) []cronhelper.JobRecord {
	records := make([]cronhelper.JobRecord, len(jobs))
	for i, j := range jobs {
		records[i] = cronhelper.JobRecord{
			Name:          j.Name,
			Expression:    j.Expression,
			Timezone:      j.Timezone,
			OverlapPolicy: j.OverlapPolicy,
			MaxAttempts:   j.MaxAttempts,
			RetentionDays: j.RetentionDays,
			ContentHash:   j.ContentHash,
		}
	}
	return records
}

type sqliteStore struct {
	inner *cronhelper.SqliteStore
}

func newSqliteStore(db *sql.DB, nodeID string) *sqliteStore {
	return &sqliteStore{inner: cronhelper.NewSqliteStore(db, nodeID)}
}

func (s *sqliteStore) label() string         { return s.inner.Label() }
func (s *sqliteStore) initSchema() error     { return s.inner.InitSchema() }
func (s *sqliteStore) sync(identity string, jobs []*CronJob) error {
	return s.inner.Sync(identity, cronJobsToRecords(jobs))
}
func (s *sqliteStore) reclaim(identity string, bootWipe bool) int64 {
	return s.inner.Reclaim(identity, bootWipe)
}
func (s *sqliteStore) hasActive(identity, name string) bool {
	return s.inner.HasActive(identity, name)
}
func (s *sqliteStore) insertSlot(identity, name string, slot time.Time, maxAttempts int) error {
	return s.inner.InsertSlot(identity, name, slot, maxAttempts)
}
func (s *sqliteStore) claim(identity, nodeID string, leaseTTL time.Duration, limit int) []claimedRow {
	claimed := s.inner.Claim(identity, nodeID, leaseTTL, limit)
	out := make([]claimedRow, len(claimed))
	for i, c := range claimed {
		out[i] = claimedRow{
			id:           c.ID,
			name:         c.Name,
			scheduledFor: cronhelper.RFC(c.ScheduledFor),
			attempt:      c.Attempt,
			maxAttempts:  c.MaxAttempts,
		}
	}
	return out
}
func (s *sqliteStore) complete(execID int64, output string, gas int64) {
	s.inner.Complete(execID, output, gas)
}
func (s *sqliteStore) fail(execID int64, attempt int, retry bool, availableAt time.Time, errMsg, output string, gas int64) {
	s.inner.Fail(execID, attempt, retry, availableAt, errMsg, output, gas)
}
func (s *sqliteStore) recordSummary(identity, name, status string) {
	s.inner.RecordSummary(identity, name, status)
}
func (s *sqliteStore) heartbeat(nodeID string, leaseTTL time.Duration) {
	s.inner.Heartbeat(nodeID, leaseTTL)
}
func (s *sqliteStore) retention(identity, name string, completedBefore, failedBefore time.Time) {
	s.inner.Retention(identity, name, completedBefore, failedBefore)
}
func (s *sqliteStore) listCrons(identity string) []map[string]any {
	return s.inner.ListCrons(identity)
}

type pgStore struct {
	inner *cronhelper.PgStore
}

func newPgStore(db *sql.DB, nodeID string) *pgStore {
	return &pgStore{inner: cronhelper.NewPgStore(db, nodeID)}
}

func (s *pgStore) label() string         { return s.inner.Label() }
func (s *pgStore) initSchema() error     { return s.inner.InitSchema() }
func (s *pgStore) sync(identity string, jobs []*CronJob) error {
	return s.inner.Sync(identity, cronJobsToRecords(jobs))
}
func (s *pgStore) reclaim(identity string, bootWipe bool) int64 {
	return s.inner.Reclaim(identity, bootWipe)
}
func (s *pgStore) hasActive(identity, name string) bool {
	return s.inner.HasActive(identity, name)
}
func (s *pgStore) insertSlot(identity, name string, slot time.Time, maxAttempts int) error {
	return s.inner.InsertSlot(identity, name, slot, maxAttempts)
}
func (s *pgStore) claim(identity, nodeID string, leaseTTL time.Duration, limit int) []claimedRow {
	claimed := s.inner.Claim(identity, nodeID, leaseTTL, limit)
	out := make([]claimedRow, len(claimed))
	for i, c := range claimed {
		out[i] = claimedRow{
			id:           c.ID,
			name:         c.Name,
			scheduledFor: cronhelper.RFC(c.ScheduledFor),
			attempt:      c.Attempt,
			maxAttempts:  c.MaxAttempts,
		}
	}
	return out
}
func (s *pgStore) complete(execID int64, output string, gas int64) {
	s.inner.Complete(execID, output, gas)
}
func (s *pgStore) fail(execID int64, attempt int, retry bool, availableAt time.Time, errMsg, output string, gas int64) {
	s.inner.Fail(execID, attempt, retry, availableAt, errMsg, output, gas)
}
func (s *pgStore) recordSummary(identity, name, status string) {
	s.inner.RecordSummary(identity, name, status)
}
func (s *pgStore) heartbeat(nodeID string, leaseTTL time.Duration) {
	s.inner.Heartbeat(nodeID, leaseTTL)
}
func (s *pgStore) retention(identity, name string, completedBefore, failedBefore time.Time) {
	s.inner.Retention(identity, name, completedBefore, failedBefore)
}
func (s *pgStore) listCrons(identity string) []map[string]any {
	return s.inner.ListCrons(identity)
}
