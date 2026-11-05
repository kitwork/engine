package gateway

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/kitwork/engine/kitdb/pgwire"
)

// fakeSearch indexes the name and brand of public.goods and the name of public.many, words already without accents,
// the lower rowid scoring higher.
type fakeSearch struct {
	mutex sync.Mutex
	asked []string
	rows  map[string]map[int64]map[string]string
}

func newFakeSearch() *fakeSearch {
	provider := &fakeSearch{rows: map[string]map[int64]map[string]string{
		"goods": {
			1: {"name": "ban phim logitech mx", "brand": "logitech"},
			2: {"name": "ban phim logitech k380", "brand": "logitech"},
			3: {"name": "chuot logitech g pro", "brand": "logitech"},
			4: {"name": "ban phim co", "brand": "akko"},
		},
		"many": {},
	}}
	for rowid := int64(1); rowid <= 1000; rowid++ {
		provider.rows["many"][rowid] = map[string]string{"name": fmt.Sprintf("item %d", rowid)}
	}
	return provider
}

func (provider *fakeSearch) Fields(_ context.Context, schema, table string, fields []string) ([]SearchField, error) {
	declared := map[string][]SearchField{"goods": {{"brand", 1}, {"name", 5}}, "many": {{"name", 1}}}[table]
	if schema != "public" || declared == nil {
		return nil, pgwire.NewError("42704", "table "+table+" has no search index")
	}
	if len(fields) == 0 {
		return declared, nil
	}
	var named []SearchField
	for _, field := range fields {
		found := false
		for _, candidate := range declared {
			if candidate.Name == field {
				named, found = append(named, candidate), true
			}
		}
		if !found {
			return nil, pgwire.NewError("42703", "column "+field+" is not searchable")
		}
	}
	return named, nil
}

func (provider *fakeSearch) matches(ctx context.Context, schema, table string, fields []string, query SearchQuery) ([]SearchHit, error) {
	named, err := provider.Fields(ctx, schema, table, fields)
	if err != nil {
		return nil, err
	}
	var hits []SearchHit
	for rowid, columns := range provider.rows[table] {
		var text []string
		for _, field := range named {
			text = append(text, columns[field.Name])
		}
		joined := " " + strings.Join(text, " ") + " "
		matched := false
		switch query.Kind {
		case SearchWords, SearchAny:
			matched = query.Kind == SearchWords
			for _, word := range strings.Fields(query.Text) {
				has := strings.Contains(joined, " "+word+" ")
				if query.Kind == SearchWords {
					matched = matched && has
				} else {
					matched = matched || has
				}
			}
		case SearchPhrase:
			matched = strings.Contains(joined, " "+query.Text+" ")
		case SearchPrefix:
			matched = strings.Contains(joined, " "+query.Text)
		}
		if matched {
			hits = append(hits, SearchHit{RowID: rowid, Score: 1 / float64(rowid)})
		}
	}
	sort.Slice(hits, func(left, right int) bool { return hits[left].RowID < hits[right].RowID })
	return hits, nil
}

func (provider *fakeSearch) Search(ctx context.Context, schema, table string, fields []string, query SearchQuery, limit int) ([]SearchHit, error) {
	provider.mutex.Lock()
	provider.asked = append(provider.asked, fmt.Sprintf("search %s %v %d %q %d", table, fields, query.Kind, query.Text, limit))
	provider.mutex.Unlock()
	hits, err := provider.matches(ctx, schema, table, fields, query)
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, err
}

func (provider *fakeSearch) Count(ctx context.Context, schema, table string, fields []string, query SearchQuery) (uint64, error) {
	provider.mutex.Lock()
	provider.asked = append(provider.asked, fmt.Sprintf("count %s %v %q", table, fields, query.Text))
	provider.mutex.Unlock()
	hits, err := provider.matches(ctx, schema, table, fields, query)
	return uint64(len(hits)), err
}

func (provider *fakeSearch) questions() []string {
	provider.mutex.Lock()
	defer provider.mutex.Unlock()
	asked := provider.asked
	provider.asked = nil
	return asked
}

func openSearchSession(t *testing.T, access *fakeAccess) (*Session, *fakeSearch) {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "application")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(directory, "kitwork.db")
	createFile(t, file,
		"CREATE TABLE goods (id INTEGER PRIMARY KEY, name TEXT, brand TEXT, price INTEGER, merchant TEXT)",
		"INSERT INTO goods VALUES (1, 'Bàn phím Logitech MX', 'Logitech', 300, 'shopee'), (2, 'Bàn phím Logitech K380', 'Logitech', 200, 'lazada'), (3, 'Chuột Logitech G Pro', 'Logitech', 150, 'shopee'), (4, 'Bàn phím cơ <Akko>', 'Akko', 500, 'shopee')",
		"CREATE TABLE many (id INTEGER PRIMARY KEY, name TEXT, merchant TEXT)",
		"WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 1000) INSERT INTO many SELECT i, 'item ' || i, CASE WHEN i = 999 THEN 'b' ELSE 'a' END FROM n",
		"CREATE TABLE plain (id INTEGER PRIMARY KEY, match TEXT, search TEXT)",
		"INSERT INTO plain VALUES (1, 'a column named match', 'and one named search')",
		"CREATE TABLE reserved (id INTEGER PRIMARY KEY, _score_ REAL)",
	)
	provider := newFakeSearch()
	session, err := Open(context.Background(), Target{Name: "shop", Schemas: map[string]string{"public": file}}, access, nil,
		Options{Search: provider})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session, provider
}

// MATCH as SQLite's FTS5 writes it: the table (or its alias) for every indexed column, a column, a list; best match
// first with _score_; ANDed with conditions, which get more of the ranked rows when the best ones do not pass them;
// counted, grouped; the four kinds of query; _snippet_.
//
// CONTROL: with matchRead never ranking more candidates, "residual: … = "", want "999"" (the 200 best matches of
// 'item' are rows 1 to 200, none of them in merchant b); with the count answered without the grant, "SELECT
// count(*) FROM goods WHERE goods MATCH 'logitech': no error, want 42501"; with the snippet marking no word, "_snippet_ =
// "1,Bàn phím Logitech MX|…""; with every query read as words, "… MATCH 'akko OR k380' = "", want "2|4""; with a
// table of a column _score_ let through, "… reserved MATCH 'x': 42704 … no search index"; with SEARCH not refused,
// "… * SEARCH 'logitech': 42601 near "*": syntax error"; with the prefix check taking every row, "… 'ban phim logi*'
// LIMIT 5 = "1|2|4", want "1|2""; with the words left out not checked, "… 'ban phim NOT logitech' = "1|2|4", want
// "4""; with a filtered query taken as one without other conditions, the index is asked for 5 rows, not 200.
//
// CONTROL (05/11/2026, words left out by the index in a count): with the rows the index found not taken out, "…
// count(*) … 'ban phim NOT co' = "3", want "2""; with the index asked for the rows of a word left out in a ranked read
// too (as first written, 01/11), "a ranked read with a word left out asked = [count goods [] "logitech" search goods
// [] 0 "logitech" 10000 …]"; with a word not counted first, "a count with a word on more rows asked = [count goods
// [] "logitech" search goods [] 0 "logitech" 2 …]"; with a word on more rows than excludedByIndex not checked on the
// rows, "… NOT logitech' on more rows than the index takes out = "3", want "1"".
func TestMatch(t *testing.T) {
	session, provider := openSearchSession(t, newAccess("public.goods", "public.many", "public.plain", "public.reserved"))
	run := func(query string, parameters ...pgwire.Parameter) string {
		t.Helper()
		return cells(mustRun(t, session, query, parameters...))
	}

	if got := run("SELECT id, name, _score_ FROM goods WHERE goods MATCH 'ban phim logitech' LIMIT 50"); got != "1,Bàn phím Logitech MX,1|2,Bàn phím Logitech K380,0.5" {
		t.Fatalf("table MATCH = %q", got)
	}
	if asked := provider.questions(); fmt.Sprint(asked) != `[search goods [] 0 "ban phim logitech" 50]` {
		t.Fatalf("asked = %v", asked)
	}
	if got := run("SELECT g.id FROM goods AS g WHERE g MATCH $1 AND g.merchant = $2 ORDER BY _score_ DESC LIMIT 5", textParameters("logitech", "shopee")...); got != "1|3" {
		t.Fatalf("alias MATCH $1 AND … = %q", got)
	}
	if got := run("SELECT id FROM public.goods WHERE (name, goods.brand) MATCH 'akko'"); got != "4" {
		t.Fatalf("(name, brand) MATCH = %q", got)
	}
	if asked := provider.questions(); !strings.Contains(fmt.Sprint(asked), `search goods [name brand] 0 "akko"`) {
		t.Fatalf("list asked = %v", asked)
	}
	if got := run("SELECT id FROM goods WHERE name MATCH 'logitech' LIMIT 1 OFFSET 1"); got != "2" {
		t.Fatalf("column MATCH with OFFSET = %q", got)
	}
	if asked := provider.questions(); fmt.Sprint(asked) != `[search goods [name] 0 "logitech" 2]` {
		t.Fatalf("OFFSET asked = %v", asked)
	}
	if got := run("SELECT id FROM many WHERE many MATCH 'item' AND merchant = 'b' LIMIT 1"); got != "999" {
		t.Fatalf("residual: … = %q, want \"999\"", got)
	}
	if asked := provider.questions(); fmt.Sprint(asked) != `[search many [] 0 "item" 200 search many [] 0 "item" 800 search many [] 0 "item" 3200]` {
		t.Fatalf("residual asked = %v", asked)
	}

	// The four kinds of query.
	for query, want := range map[string]string{
		"SELECT id FROM goods WHERE goods MATCH 'akko OR k380'":    "2|4",
		`SELECT id FROM goods WHERE goods MATCH '"phim logitech"'`: "1|2",
		"SELECT id FROM goods WHERE name MATCH 'logi*'":            "1|2|3",
		"SELECT id FROM goods WHERE goods MATCH 'akko or k380'":    "", // or in lower case is a word
	} {
		if got := run(query); got != want {
			t.Fatalf("%s = %q, want %q", query, got, want)
		}
	}
	for _, mixed := range []string{`'"ban phim" OR akko'`, `'OR akko'`, `'akko OR'`, `'"ban phim'`, `'lo*gi'`, `'ban* phim'`,
		`'logitech OR razer*'`, `'NOT co'`, `'ban NOT'`, `'ban phim NOT "co day"'`, `'ban phim NOT co*'`, `'ban NOT co gia'`} {
		expectCode(t, session, "SELECT id FROM goods WHERE goods MATCH "+mixed, "42601", "MATCH reads words")
	}

	// A last word typed in part, and words left out: the index ranks the words, the rows are checked for the rest.
	for query, want := range map[string]string{
		"SELECT id FROM goods WHERE goods MATCH 'ban phim logi*' LIMIT 5":           "1|2",
		"SELECT id FROM goods WHERE goods MATCH 'ban phim NOT logitech'":            "4",
		"SELECT id FROM goods WHERE goods MATCH 'ban phim NOT co'":                  "1|2",
		"SELECT id FROM goods WHERE name MATCH 'phim NOT mx NOT k380'":              "4",
		"SELECT id FROM goods WHERE goods MATCH 'logi* NOT chuot'":                  "1|2",
		"SELECT count(*) FROM goods WHERE goods MATCH 'ban phim NOT co'":            "2",
		"SELECT id, _snippet_ FROM goods WHERE name MATCH 'ban ph* NOT co' LIMIT 1": "1,<b>Bàn</b> phím Logitech MX",
	} {
		if got := run(query); got != want {
			t.Fatalf("%s = %q, want %q", query, got, want)
		}
	}
	if asked := provider.questions(); !strings.Contains(fmt.Sprint(asked), `search goods [] 0 "ban phim" 200`) {
		t.Fatalf("words then a prefix asked = %v (the index ranks the whole words, more of them for the check)", asked)
	}

	// A word left out is checked on the rows a ranked read reads: with LIMIT 1, the index is asked for the 200 best
	// matches, not for the rows of the word. A count reads every match: there the index gives the rows of a word left
	// out, counted first, and they are taken out of the matches, no row checked. A word on more rows than
	// excludedByIndex is not looked for, and is checked on the rows the count reads.
	if got := run("SELECT id FROM goods WHERE goods MATCH 'ban phim NOT logitech' LIMIT 1"); got != "4" {
		t.Fatalf("… 'ban phim NOT logitech' LIMIT 1 = %q, want \"4\"", got)
	}
	if asked := provider.questions(); fmt.Sprint(asked) != `[search goods [] 0 "ban phim" 200]` {
		t.Fatalf("a ranked read with a word left out asked = %v", asked)
	}
	if got := run("SELECT count(*) FROM goods WHERE goods MATCH 'ban phim NOT co'"); got != "2" {
		t.Fatalf("… count(*) … 'ban phim NOT co' = %q, want \"2\"", got)
	}
	if asked := provider.questions(); fmt.Sprint(asked) != `[count goods [] "co" search goods [] 0 "co" 10000 search goods [] 0 "ban phim" 100000]` {
		t.Fatalf("a count with a word left out asked = %v", asked)
	}
	excludedByIndex = 2
	t.Cleanup(func() { excludedByIndex = 10_000 })
	if got := run("SELECT count(*) FROM goods WHERE goods MATCH 'ban phim NOT logitech'"); got != "1" {
		t.Fatalf("… count(*) … NOT logitech' on more rows than the index takes out = %q, want \"1\"", got)
	}
	if asked := provider.questions(); fmt.Sprint(asked) != `[count goods [] "logitech" search goods [] 0 "ban phim" 100000]` {
		t.Fatalf("a count with a word on more rows asked = %v", asked)
	}
	if got := run("SELECT count(*) FROM goods WHERE goods MATCH 'ban phim NOT mx NOT logitech'"); got != "1" {
		t.Fatalf("one word taken out, one checked = %q, want \"1\"", got)
	}
	excludedByIndex = 10_000

	// _snippet_: the passage of the best column around the words, escaped, the words in bold.
	if got := run("SELECT id, _snippet_ FROM goods WHERE goods MATCH 'ban phim' LIMIT 3"); got != "1,<b>Bàn</b> <b>phím</b> Logitech MX|2,<b>Bàn</b> <b>phím</b> Logitech K380|4,<b>Bàn</b> <b>phím</b> cơ &lt;Akko&gt;" {
		t.Fatalf("_snippet_ = %q", got)
	}
	snippets := mustRun(t, session, "SELECT goods._snippet_ FROM goods WHERE brand MATCH 'akko'")
	if cells(snippets) != "<b>Akko</b>" || snippets.Columns[0].Name != "_snippet_" {
		t.Fatalf("goods._snippet_ on brand = %q %v", cells(snippets), snippets.Columns)
	}

	count := mustRun(t, session, "SELECT count(*) AS total FROM goods WHERE goods MATCH 'logitech'")
	if cells(count) != "3" || count.Columns[0].Name != "total" {
		t.Fatalf("count = %q %v", cells(count), count.Columns)
	}
	if asked := provider.questions(); !strings.Contains(fmt.Sprint(asked), `count goods [] "logitech"`) {
		t.Fatalf("count asked = %v", asked)
	}
	if got := run("SELECT merchant, count(*) FROM goods WHERE goods MATCH 'logitech' GROUP BY merchant ORDER BY merchant"); got != "lazada,1|shopee,2" {
		t.Fatalf("GROUP BY = %q", got)
	}
	if got := run("SELECT count(*) FROM goods WHERE goods MATCH 'logitech' AND price < 250"); got != "2" {
		t.Fatalf("count with a condition = %q", got)
	}
	scored := mustRun(t, session, "SELECT _score_ FROM goods WHERE goods MATCH 'logitech' LIMIT 1")
	if scored.Columns[0].DataTypeOID != pgwire.OIDFloat8 {
		t.Fatalf("_score_ type = %d", scored.Columns[0].DataTypeOID)
	}
	columns, err := session.Describe(context.Background(), "SELECT id, _score_, _snippet_ FROM goods WHERE goods MATCH $1 LIMIT 10",
		[]pgwire.Parameter{{Null: true}})
	if err != nil || len(columns) != 3 || columns[1].Name != "_score_" || columns[2].Name != "_snippet_" {
		t.Fatalf("Describe = %v, %v", columns, err)
	}
	if got := run("SELECT match, search FROM plain WHERE match LIKE '%column%'"); got != "a column named match,and one named search" {
		t.Fatalf("columns named match and search = %q", got)
	}

	for _, refused := range []struct{ query, code, message string }{
		{"SELECT id FROM goods WHERE * SEARCH 'logitech'", "42601", "SEARCH is now MATCH"},
		{"SELECT id FROM goods WHERE (name) SEARCH 'logitech'", "42601", "SEARCH is now MATCH"},
		{"SELECT id FROM goods WHERE * MATCH 'logitech'", "42601", "goods MATCH"},
		{"SELECT id FROM goods WHERE goods MATCH 'logitech' OR price > 1", "42601", "OR"},
		{"SELECT id FROM goods WHERE price > 1 OR goods MATCH 'logitech'", "42601", "OR"},
		{"SELECT id FROM goods WHERE NOT goods MATCH 'logitech'", "42601", "ANDed"},
		{"SELECT id FROM goods WHERE goods MATCH 'logitech' ORDER BY price", "42601", "ORDER BY _score_ DESC"},
		{"SELECT g.id FROM goods g JOIN many m ON m.id = g.id WHERE g MATCH 'logitech'", "42601", "one table"},
		{"SELECT id FROM goods WHERE id IN (SELECT id FROM goods WHERE goods MATCH 'x')", "42601", "subquery"},
		{"SELECT id FROM goods WHERE goods MATCH 42", "42601", "string or a parameter"},
		{"SELECT id FROM plain WHERE plain MATCH 'x'", "42704", "no search index"},
		{"SELECT id FROM goods WHERE price MATCH 'x'", "42703", "not searchable"},
		{"SELECT id FROM reserved WHERE reserved MATCH 'x'", "42701", "has a column named _score_"},
		{"SELECT id, _score FROM goods WHERE goods MATCH 'logitech'", "42703", "the columns MATCH adds are _score_ and _snippet_"},
		{"SELECT id, _cursor_ FROM goods WHERE goods MATCH 'logitech'", "0A000", "_cursor_"},
		{"SELECT id FROM goods WHERE goods MATCH 'logitech' LIMIT 5 AFTER 'x'", "0A000", "AFTER"},
	} {
		expectCode(t, session, refused.query, refused.code, refused.message)
	}

	// The rows a MATCH returns are rows of a table the user reads.
	limited, _ := openSearchSession(t, newAccess("public.many"))
	expectCode(t, limited, "SELECT id FROM goods WHERE goods MATCH 'logitech'", "42501", "permission denied for table public.goods")
	expectCode(t, limited, "SELECT count(*) FROM goods WHERE goods MATCH 'logitech'", "42501", "permission denied for table public.goods")
	withoutIndex, err := Open(context.Background(), session.target, newAccess("public.goods"), nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer withoutIndex.Close()
	expectCode(t, withoutIndex, "SELECT id FROM goods WHERE goods MATCH 'logitech'", "0A000", "no full-text index")
}
