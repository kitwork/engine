// Command kitdbsqlite opens one KitDB-on-SQLite database (engine/kitdb/sqlite)
// to try it by hand: an interactive shell in the terminal, or a small local web
// page with a full-text search box and an SQL box.
//
//	kitdbsqlite shell <kitwork.db> [-search table:column=boost,…] [-show table:column,…]
//	kitdbsqlite serve <kitwork.db> [-listen 127.0.0.1:8111] [-search …] [-show …]
//
// -search makes a table searchable (repeat it for more tables); -show adds
// columns to each search hit. Every write goes through the database's one
// writer and its history, exactly as an application would write.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/kitwork/engine/internal/benchclock"
	kitdb "github.com/kitwork/engine/kitdb/sqlite"
	"github.com/kitwork/engine/search"
)

type repeated []string

func (values *repeated) String() string         { return strings.Join(*values, " ") }
func (values *repeated) Set(value string) error { *values = append(*values, value); return nil }

// tool is the open database and what each searchable table shows.
type tool struct {
	database *kitdb.Database
	search   []kitdb.SearchTable
	show     map[string][]string
}

func parseSearch(values []string) ([]kitdb.SearchTable, error) {
	var tables []kitdb.SearchTable
	for _, value := range values {
		table, columns, found := strings.Cut(value, ":")
		if !found || table == "" || columns == "" {
			return nil, fmt.Errorf("-search %q: write table:column=boost,column", value)
		}
		entry := kitdb.SearchTable{Table: table}
		for _, column := range strings.Split(columns, ",") {
			name, boost, weighted := strings.Cut(column, "=")
			searchColumn := kitdb.SearchColumn{Name: name}
			if weighted {
				parsed, err := strconv.ParseFloat(boost, 64)
				if err != nil || parsed <= 0 {
					return nil, fmt.Errorf("-search %q: boost %q is not a positive number", value, boost)
				}
				searchColumn.Boost = parsed
			}
			entry.Columns = append(entry.Columns, searchColumn)
		}
		tables = append(tables, entry)
	}
	return tables, nil
}

func parseShow(values []string) (map[string][]string, error) {
	show := map[string][]string{}
	for _, value := range values {
		table, columns, found := strings.Cut(value, ":")
		if !found || table == "" || columns == "" {
			return nil, fmt.Errorf("-show %q: write table:column,column", value)
		}
		show[table] = append(show[table], strings.Split(columns, ",")...)
	}
	return show, nil
}

func main() {
	if len(os.Args) < 3 || (os.Args[1] != "shell" && os.Args[1] != "serve") {
		fmt.Fprintln(os.Stderr, "usage: kitdbsqlite shell|serve <kitwork.db> [-search table:column=boost,…] [-show table:column,…] [-listen address]")
		os.Exit(2)
	}
	mode, path := os.Args[1], os.Args[2]
	flags := flag.NewFlagSet(mode, flag.ExitOnError)
	var searchFlags, showFlags repeated
	flags.Var(&searchFlags, "search", "table:column=boost,column — make a table searchable (repeatable)")
	flags.Var(&showFlags, "show", "table:column,column — extra columns of each search hit (repeatable)")
	listen := flags.String("listen", "127.0.0.1:8111", "address of the web page (serve)")
	_ = flags.Parse(os.Args[3:])
	searchTables, err := parseSearch(searchFlags)
	if err != nil {
		fail(err)
	}
	show, err := parseShow(showFlags)
	if err != nil {
		fail(err)
	}
	ctx := context.Background()
	start := benchclock.Now()
	database, err := kitdb.Open(ctx, path, kitdb.Options{Search: searchTables, HistoryOfEveryTable: true})
	if err != nil {
		fail(err)
	}
	defer database.Close()
	fmt.Printf("mở %s trong %s, rev %d\n", path, since(start), database.Revision())
	t := &tool{database: database, search: searchTables, show: show}
	if mode == "shell" {
		t.shell(ctx)
		return
	}
	if err := t.serve(ctx, *listen); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "kitdbsqlite:", err)
	os.Exit(1)
}

func since(start benchclock.Stamp) string {
	elapsed := benchclock.Since(start)
	if elapsed >= time.Second {
		return fmt.Sprintf("%.2f s", elapsed.Seconds())
	}
	return fmt.Sprintf("%.2f ms", float64(elapsed.Microseconds())/1000)
}

// queryResult is one statement's outcome, for the shell and the page alike.
type queryResult struct {
	Kind         string   `json:"kind"`
	Revision     uint64   `json:"revision"`
	Milliseconds float64  `json:"milliseconds"`
	Columns      []string `json:"columns,omitempty"`
	Rows         [][]any  `json:"rows,omitempty"`
	Truncated    bool     `json:"truncated,omitempty"`
	RowsAffected int64    `json:"rowsAffected,omitempty"`
}

const maximumRows = 200

// run executes one statement through the call its kind needs.
func (t *tool) run(ctx context.Context, statement string) (queryResult, error) {
	statement = strings.TrimSpace(statement)
	kind, err := kitdb.Classify(statement)
	if err != nil {
		return queryResult{}, err
	}
	start := benchclock.Now()
	result := queryResult{}
	switch kind {
	case kitdb.SchemaStatement:
		result.Kind = "schema"
		result.Revision, err = t.database.Migrate(ctx, "kitdbsqlite", statement)
	case kitdb.DataStatement:
		result.Kind = "write"
		result.Revision, err = t.database.Write(ctx, "kitdbsqlite", func(tx *kitdb.Transaction) error {
			outcome, err := tx.Exec(statement)
			if err != nil {
				return err
			}
			result.RowsAffected, _ = outcome.RowsAffected()
			return nil
		})
	default:
		result.Kind = "read"
		err = t.database.Read(ctx, func(snapshot *kitdb.Snapshot) error {
			result.Revision = snapshot.Revision
			rows, err := snapshot.Query(statement)
			if err != nil {
				return err
			}
			defer rows.Close()
			result.Columns, result.Rows, result.Truncated, err = collect(rows)
			return err
		})
	}
	result.Milliseconds = float64(benchclock.Since(start).Microseconds()) / 1000
	return result, err
}

func collect(rows *sql.Rows) ([]string, [][]any, bool, error) {
	columns, err := rows.Columns()
	if err != nil {
		return nil, nil, false, err
	}
	var collected [][]any
	truncated := false
	for rows.Next() {
		if len(collected) == maximumRows {
			truncated = true
			break
		}
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for position := range values {
			pointers[position] = &values[position]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, nil, false, err
		}
		for position, value := range values {
			if bytes, isBytes := value.([]byte); isBytes {
				values[position] = fmt.Sprintf("<blob %d byte>", len(bytes))
			}
		}
		collected = append(collected, values)
	}
	return columns, collected, truncated, rows.Err()
}

// searchHit is one ranked row: its searchable columns highlighted, in the
// order they were declared, and the other columns asked for with -show.
type searchHit struct {
	RowID       int64              `json:"rowid"`
	Score       float64            `json:"score"`
	Highlighted []highlightedField `json:"highlighted"`
	Values      map[string]any     `json:"values"`
}

type highlightedField struct {
	Column string `json:"column"`
	HTML   string `json:"html"`
}

type searchResult struct {
	Table        string      `json:"table"`
	Revision     uint64      `json:"revision"`
	Milliseconds float64     `json:"milliseconds"`
	Hits         []searchHit `json:"hits"`
}

func (t *tool) searchTable(name string) (kitdb.SearchTable, bool) {
	for _, table := range t.search {
		if table.Table == name {
			return table, true
		}
	}
	return kitdb.SearchTable{}, false
}

// find ranks rows of table for text in one snapshot, then reads those rows in
// the same snapshot, highlighting the matched words.
func (t *tool) find(ctx context.Context, table, text, mode string, limit int) (searchResult, error) {
	config, known := t.searchTable(table)
	if !known {
		return searchResult{}, fmt.Errorf("table %q is not searchable; open it with -search %s:column", table, table)
	}
	fields := make([]string, len(config.Columns))
	for position, column := range config.Columns {
		fields[position] = column.Name
	}
	query := search.MatchQuery{Fields: fields, Text: text}
	switch mode {
	case "", "all":
	case "any":
		query.Operator = search.QueryAny
	case "prefix":
		query = search.MatchQuery{Fields: fields, Prefix: text}
	case "phrase":
		query = search.MatchQuery{Field: fields[0], Phrase: text}
	default:
		return searchResult{}, fmt.Errorf("mode %q: all, any, prefix or phrase", mode)
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	result := searchResult{Table: table}
	start := benchclock.Now()
	err := t.database.Read(ctx, func(snapshot *kitdb.Snapshot) error {
		result.Revision = snapshot.Revision
		hits, err := snapshot.Search(table, query, search.SearchOptions{Limit: limit})
		if err != nil {
			return err
		}
		if len(hits) == 0 {
			return nil
		}
		columns := append(append([]string(nil), fields...), t.show[table]...)
		selectList := "_rowid_"
		for _, column := range columns {
			selectList += ", " + quote(column)
		}
		marks := strings.TrimSuffix(strings.Repeat("?, ", len(hits)), ", ")
		arguments := make([]any, len(hits))
		for position, hit := range hits {
			arguments[position] = hit.RowID
		}
		rows, err := snapshot.Query(`SELECT `+selectList+` FROM `+quote(table)+` WHERE _rowid_ IN (`+marks+`)`, arguments...)
		if err != nil {
			return err
		}
		defer rows.Close()
		byRow := map[int64]map[string]any{}
		for rows.Next() {
			values := make([]any, len(columns)+1)
			pointers := make([]any, len(values))
			for position := range values {
				pointers[position] = &values[position]
			}
			if err := rows.Scan(pointers...); err != nil {
				return err
			}
			named := map[string]any{}
			for position, column := range columns {
				named[column] = values[position+1]
			}
			byRow[values[0].(int64)] = named
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for _, hit := range hits {
			values := byRow[hit.RowID]
			entry := searchHit{RowID: hit.RowID, Score: hit.Score, Values: map[string]any{}}
			for _, column := range t.show[table] {
				entry.Values[column] = values[column]
			}
			for _, field := range fields {
				textValue, _ := values[field].(string)
				entry.Highlighted = append(entry.Highlighted, highlightedField{Column: field, HTML: highlight(ctx, textValue, text)})
			}
			result.Hits = append(result.Hits, entry)
		}
		return nil
	})
	result.Milliseconds = float64(benchclock.Since(start).Microseconds()) / 1000
	return result, err
}

func quote(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

// highlight returns an HTML-escaped excerpt of text with matched words in <mark>.
func highlight(ctx context.Context, text, query string) string {
	if text == "" {
		return ""
	}
	fragment, err := search.Highlight(ctx, search.VietnameseAnalyzer(), text, query, search.FragmentOptions{MaxTokens: 48, ContextTokens: 12})
	if err != nil || len(fragment.Matches) == 0 {
		if len(text) > 400 {
			text = text[:400] + "…"
		}
		return escape(text)
	}
	var builder strings.Builder
	if fragment.PrefixElided {
		builder.WriteString("… ")
	}
	position := 0
	for _, match := range fragment.Matches {
		builder.WriteString(escape(fragment.Text[position:match.Start]))
		builder.WriteString("<mark>")
		builder.WriteString(escape(fragment.Text[match.Start:match.End]))
		builder.WriteString("</mark>")
		position = match.End
	}
	builder.WriteString(escape(fragment.Text[position:]))
	if fragment.SuffixElided {
		builder.WriteString(" …")
	}
	return builder.String()
}

func escape(text string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;").Replace(text)
}

var errQuit = errors.New("quit")
