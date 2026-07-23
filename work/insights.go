package work

import (
	"database/sql"
	"strings"
	"time"

	"github.com/kitwork/engine/value"
)

// Insights is the per-site usage-signal namespace — import { insights } from "kitwork".
//
// The first signal it captures is SEARCH: what visitors typed, and whether the site had an answer. A
// query that returns ZERO results is a CONTENT GAP measured from real demand — the seed of the SEO
// loop (what to write next) with no AI, no keyword tool, no external API.
//
//	insights.search(query, resultCount)   // log one visitor search (site-wide result count)
//	insights.gaps()                       // queries that currently return nothing, most-wanted first
//	insights.searches()                   // top queries overall
//
// Storage is the tenant's own .data/insights.db (per DOMAIN — these are this site's visitors), the same
// disposable-runtime tier as the collection index; it is analytics, rebuildable is not the point but it
// is gitignored and never source.
type Insights struct {
	tenant *Tenant
}

func (w *KitWork) Insights() *Insights {
	return &Insights{tenant: w.tenant}
}

func (in *Insights) db() *sql.DB {
	db := sqliteFor(in.tenant, "insights.db").db()
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
	return db
}

// normalizeQuery folds variants together so "Kitwork  VM" and "kitwork vm" are one gap, not two.
func normalizeQuery(raw string) string {
	q := strings.ToLower(strings.TrimSpace(raw))
	q = strings.Join(strings.Fields(q), " ") // collapse runs of whitespace
	if len(q) > 120 {
		q = q[:120]
	}
	return q
}

// Search logs one visitor query with the SITE-WIDE result count (the route knows the total across every
// collection + other sources). Queries shorter than 2 chars are ignored as noise.
func (in *Insights) Search(args ...value.Value) value.Value {
	if len(args) == 0 {
		return value.Value{K: value.Nil}
	}
	q := normalizeQuery(args[0].String())
	if len(q) < 2 {
		return value.Value{K: value.Nil}
	}
	results := 0
	if len(args) > 1 {
		results = int(args[1].N)
	}
	db := in.db()
	if db == nil {
		return value.Value{K: value.Invalid, V: "insights: store unavailable"}
	}
	miss := 0
	if results == 0 {
		miss = 1
	}
	now := rfc(time.Now())
	db.Exec(`INSERT INTO searches (query, total, misses, results_last, first_seen, last_seen)
		VALUES (?, 1, ?, ?, ?, ?)
		ON CONFLICT(query) DO UPDATE SET total=total+1, misses=misses+?, results_last=?, last_seen=?`,
		q, miss, results, now, now, miss, results, now)
	return value.Value{K: value.Nil}
}

// Gaps returns queries that CURRENTLY return nothing (results_last = 0), most-searched first — the
// actionable content-gap list. Optional limit (default 50).
func (in *Insights) Gaps(args ...value.Value) value.Value {
	return in.report(`WHERE results_last = 0 ORDER BY total DESC, last_seen DESC`, args...)
}

// Searches returns the top queries overall (whether or not they had results). Optional limit.
func (in *Insights) Searches(args ...value.Value) value.Value {
	return in.report(`ORDER BY total DESC, last_seen DESC`, args...)
}

func (in *Insights) report(where string, args ...value.Value) value.Value {
	limit := 50
	if len(args) > 0 && args[0].N > 0 {
		limit = int(args[0].N)
	}
	db := in.db()
	if db == nil {
		return value.Value{K: value.Invalid, V: "insights: store unavailable"}
	}
	rows, err := db.Query(`SELECT query, total, misses, results_last, last_seen FROM searches `+where+` LIMIT ?`, limit)
	if err != nil {
		return value.Value{K: value.Invalid, V: err.Error()}
	}
	defer rows.Close()
	out := make([]map[string]any, 0, limit)
	for rows.Next() {
		var q, lastSeen string
		var total, misses, resultsLast int
		if rows.Scan(&q, &total, &misses, &resultsLast, &lastSeen) != nil {
			continue
		}
		out = append(out, map[string]any{
			"query": q, "total": total, "misses": misses,
			"resultsLast": resultsLast, "lastSeen": lastSeen,
		})
	}
	return collectionValue(out)
}
