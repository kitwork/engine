package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kitwork/engine/kitdb/analytics"
	"github.com/kitwork/engine/kitdb/gateway"
	"github.com/kitwork/engine/kitdb/pgwire"
)

// portAccess lets the bench's session read every table.
type portAccess struct{}

func (portAccess) CanSelect(context.Context, string, string) (bool, error) { return true, nil }
func (portAccess) Command(context.Context, string) (string, bool, error)   { return "", false, nil }
func (portAccess) Current(context.Context) (bool, error)                   { return true, nil }

// cmdPort runs the analytic queries of RESULTS_2026-10-27.md through two sessions of the read-only port on the file
// (kitdb/gateway), as a PostgreSQL client sends them: one whose registry finds the copy in the copies' file at copies
// (built by `kitdbsqlite analytics`), one without copies, SQLite answering. The results must be the same field for
// field. Evidence for KITWORK_KITDB.md mục 6 and 7 (05/11/2026).
func cmdPort(path, copies string) {
	ctx := context.Background()
	registry := analytics.NewRegistry()
	registry.Place = func(string) string { return copies }
	defer registry.Close()
	open := func(options gateway.Options) *gateway.Session {
		session, err := gateway.Open(ctx, gateway.Target{Name: "shopping", Schemas: map[string]string{"public": path}},
			portAccess{}, nil, options)
		must(err)
		return session
	}
	withCopy := open(gateway.Options{Analytics: registry})
	defer withCopy.Close()
	withoutCopy := open(gateway.Options{})
	defer withoutCopy.Close()
	fieldsOf := func(result pgwire.Result) string {
		var text strings.Builder
		for _, column := range result.Columns {
			fmt.Fprintf(&text, "%s:%d ", column.Name, column.DataTypeOID)
		}
		for _, row := range result.Rows {
			for _, field := range row {
				fmt.Fprintf(&text, "%q,", field.Data)
			}
			text.WriteString("|")
		}
		return text.String()
	}
	results := make([]pgwire.Result, len(analyticsQueries))
	times := make([]time.Duration, len(analyticsQueries))
	for pass := range 2 { // the copy's file warm, as the second of two statements finds it
		for position, item := range analyticsQueries {
			start := time.Now()
			result, err := withCopy.Execute(ctx, item.sql, nil)
			must(err)
			if pass == 1 {
				results[position], times[position] = result, time.Since(start)
			}
		}
	}
	fmt.Printf("%-36s %12s %12s  %s\n", "query through the port", "copy", "SQLite", "result")
	for position, item := range analyticsQueries {
		start := time.Now()
		want, err := withoutCopy.Execute(ctx, item.sql, nil)
		must(err)
		took := time.Since(start)
		verdict := "the same"
		if fieldsOf(results[position]) != fieldsOf(want) {
			verdict = fmt.Sprintf("DIFFERENT: %.160s against %.160s", fieldsOf(results[position]), fieldsOf(want))
		}
		fmt.Printf("%-36s %12s %12s  %d rows, %s\n", item.name, duration(times[position]), duration(took), len(want.Rows), verdict)
	}
}
