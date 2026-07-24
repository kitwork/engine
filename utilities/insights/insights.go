// Package insights provides per-site analytics and content gap tracking.
package insights

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// SearchRecord contains search metrics for a single query.
type SearchRecord struct {
	Query       string `json:"query"`
	Total       int    `json:"total"`
	Misses      int    `json:"misses"`
	ResultsLast int    `json:"resultsLast"`
	LastSeen    string `json:"lastSeen"`
}

// Store wraps an insights SQLite database.
type Store struct {
	db *sql.DB
}

// NewStore initializes an Insights store on a given database.
func NewStore(db *sql.DB) *Store {
	if db == nil {
		return nil
	}
	db.Exec(`CREATE TABLE IF NOT EXISTS searches (
		query TEXT PRIMARY KEY,
		total INTEGER NOT NULL DEFAULT 0,
		misses INTEGER NOT NULL DEFAULT 0,
		results_last INTEGER NOT NULL DEFAULT 0,
		first_seen TEXT NOT NULL,
		last_seen TEXT NOT NULL)`)
	return &Store{db: db}
}

// NormalizeQuery folds variants together so "Kitwork  VM" and "kitwork vm" are one gap.
func NormalizeQuery(raw string) string {
	q := strings.ToLower(strings.TrimSpace(raw))
	q = strings.Join(strings.Fields(q), " ")
	if len(q) > 120 {
		q = q[:120]
	}
	return q
}

// RecordSearch logs one visitor query with the result count.
func (s *Store) RecordSearch(query string, results int) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("insights store unavailable")
	}
	q := NormalizeQuery(query)
	if len(q) < 2 {
		return nil
	}
	miss := 0
	if results == 0 {
		miss = 1
	}
	now := time.Now().Format(time.RFC3339)
	_, err := s.db.Exec(`INSERT INTO searches (query, total, misses, results_last, first_seen, last_seen)
		VALUES (?, 1, ?, ?, ?, ?)
		ON CONFLICT(query) DO UPDATE SET total=total+1, misses=misses+?, results_last=?, last_seen=?`,
		q, miss, results, now, now, miss, results, now)
	return err
}

// Gaps returns queries that currently return 0 results.
func (s *Store) Gaps(limit int) ([]SearchRecord, error) {
	return s.report(`WHERE results_last = 0 ORDER BY total DESC, last_seen DESC`, limit)
}

// Searches returns the top queries overall.
func (s *Store) Searches(limit int) ([]SearchRecord, error) {
	return s.report(`ORDER BY total DESC, last_seen DESC`, limit)
}

func (s *Store) report(whereClause string, limit int) ([]SearchRecord, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("insights store unavailable")
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`SELECT query, total, misses, results_last, last_seen FROM searches `+whereClause+` LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]SearchRecord, 0, limit)
	for rows.Next() {
		var rec SearchRecord
		if err := rows.Scan(&rec.Query, &rec.Total, &rec.Misses, &rec.ResultsLast, &rec.LastSeen); err != nil {
			continue
		}
		out = append(out, rec)
	}
	return out, nil
}
