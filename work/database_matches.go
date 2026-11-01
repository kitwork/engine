package work

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/kitwork/engine/database/fulltext"
	"github.com/kitwork/engine/database/statement"
	"github.com/kitwork/engine/search"
	"github.com/kitwork/engine/utilities/query"
	"github.com/kitwork/engine/value"
)

// matches is full text on SQLite (KITWORK_DATABASE.md mục 2): table.matches(q) over every .searchable() column,
// table.column.matches(q) over one, by words, without accents, every word required. The index is the table's in
// engine/database/fulltext, the same one the database port's MATCH reads, folded up to date before each statement
// (the engine tracks the table, db.sqlite_columns.go). The index gives the rowids that match, ranked; the rest of the
// condition runs as SQL on those rows; a row comes back with score (the sum of what its positive matches give) and
// snippet (the passage of its best column, the words in <b>, HTML escaped). Without ascending or descending the rows
// come in the order of score; with them, every row that matches is ordered (open question 11 leans that way).
// !table.matches(q) only filters.

const (
	// matchesCandidates is how many rows of the index one statement reads at most: the rows the rest of the condition
	// is run on, bound in one IN.
	matchesCandidates = 30_000
	// matchesResourceName is the app resource holding the index files the app reads.
	matchesResourceName = "fulltext"
)

// fullTextFiles are the index files an app holds, released when the app closes.
type fullTextFiles struct {
	mutex    sync.Mutex
	releases map[string]func()
	closed   bool
}

func (files *fullTextFiles) Close() {
	files.mutex.Lock()
	releases := files.releases
	files.releases, files.closed = nil, true
	files.mutex.Unlock()
	for _, release := range releases {
		release()
	}
}

// fullTextFile holds path for the app until it closes.
func (t *Tenant) fullTextFile(path string) error {
	if t.appRuntime == nil {
		return fmt.Errorf("matches needs the app's runtime")
	}
	resource, _, err := t.appRuntime.InstallResource(matchesResourceName, &fullTextFiles{releases: map[string]func(){}})
	if err != nil {
		return err
	}
	files, ok := resource.(*fullTextFiles)
	if !ok {
		return fmt.Errorf("app resource %s is not the full-text files", matchesResourceName)
	}
	files.mutex.Lock()
	defer files.mutex.Unlock()
	if files.closed {
		return fmt.Errorf("the app is closing")
	}
	if files.releases[path] != nil {
		return nil
	}
	release, err := fulltext.Shared().Acquire(path)
	if err != nil {
		return err
	}
	files.releases[path] = release
	return nil
}

// matchesTerm is one matches of a statement, bound: the columns it reads and the user's text.
type matchesTerm struct {
	fields []string
	text   string
}

func (term matchesTerm) query() search.MatchQuery {
	return search.MatchQuery{Fields: term.fields, Text: term.text}
}

// matchesHit is a row the index gave, with what ranks it.
type matchesHit struct {
	rowID int64
	score float64
	rank  int
}

// runMatches runs a statement whose condition has matches.
func (m *ManagedDatabase) runMatches(prepared *preparedStatement, binder *statementBinder, positive, negative []*statement.Node) value.Value {
	s := prepared.statement
	empty := func() value.Value {
		switch s.Action {
		case statement.ActionFirst:
			return value.NewNil()
		case statement.ActionCount:
			return value.New(0)
		}
		return value.New([]value.Value{})
	}
	if m.proxy.engine == "kitdb" {
		return value.InvalidFailure(value.UnsupportedCode, "matches chạy trên database SQLite của app; database KitDB không có")
	}
	searchable := matchesColumns(prepared.columns)
	if len(searchable) == 0 {
		return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("bảng %s không có cột .searchable(): dịch lại", s.Table))
	}
	bind := func(node *statement.Node) (matchesTerm, value.Value) {
		item, site := binder.value(node.Args[0])
		if item.K != value.String {
			return matchesTerm{}, value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("matches ở %s nhận một chuỗi", site))
		}
		term := matchesTerm{text: strings.TrimSpace(item.String())}
		if node.Column == "" {
			for _, column := range searchable {
				term.fields = append(term.fields, column.Name)
			}
		} else {
			if spec := prepared.columns[node.Column]; spec == nil || !spec.searchable {
				return matchesTerm{}, value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("cột %s.%s không có .searchable(): dịch lại", s.Table, node.Column))
			}
			term.fields = []string{node.Column}
		}
		return term, value.Value{}
	}
	var ranking, excluding []matchesTerm
	for _, node := range positive {
		term, failure := bind(node)
		if failure.Failed() {
			return failure
		}
		if term.text == "" {
			return empty() // empty text matches no row (KITWORK_DATABASE.md mục 2)
		}
		ranking = append(ranking, term)
	}
	for _, node := range negative {
		term, failure := bind(node)
		if failure.Failed() {
			return failure
		}
		if term.text != "" {
			excluding = append(excluding, term)
		}
	}
	predicate, truth, failure := binder.predicate(withoutMatches(s.Where))
	if failure.Failed() {
		return failure
	}
	limit, offset := 1, 0
	if s.Action == statement.ActionList {
		if limit, offset, failure = binder.bounds(); failure.Failed() {
			return failure
		}
	}
	if truth == bindFalse || limit == 0 {
		return empty()
	}
	if truth == bindTrue {
		predicate = nil
	}
	table, failure := m.proxy.table(s.Table)
	if table == nil {
		return failure
	}
	if predicate != nil {
		// a decimal on SQLite is a scaled integer: what it is compared with is too (db.sqlite_decimal.go)
		if message := sqliteWritePredicate(s.Table, prepared.columns, predicate); message != "" {
			return value.InvalidFailure(value.InvalidArgumentCode, message)
		}
	}
	ctx := context.Background()
	if m.proxy.scope != nil {
		ctx = m.proxy.scope.Context()
	}
	file := table.sqliteFile()
	if err := m.proxy.tenant.fullTextFile(file); err != nil {
		return value.InvalidFailure(value.UnavailableCode, fmt.Sprintf("matches trên %s: %v", s.Table, err))
	}
	index, err := fulltext.Shared().Open(file, s.Table, filepath.Base(filepath.Dir(file))+"."+s.Table, searchable, false)
	if err != nil {
		return matchesFailure(s.Table, err)
	}
	for _, term := range append(append([]matchesTerm{}, ranking...), excluding...) {
		for _, field := range term.fields {
			if !matchesIndexed(index, field) {
				return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf(
					"cột %s.%s không nằm trong chỉ mục tìm kiếm của bảng (application.work khai cột: %s)", s.Table, field, strings.Join(index.ColumnNames(), ", ")))
			}
		}
	}
	run := &matchesRun{m: m, ctx: ctx, s: s, columns: prepared.columns, index: index, ranking: ranking, excluding: excluding,
		predicate: predicate, primary: table.primaryKeyColumn()}
	if run.primary == "" {
		return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("matches trên %s cần một cột id()", s.Table))
	}
	if s.Action == statement.ActionCount {
		return run.count()
	}
	rows := run.list(limit, offset)
	if s.Action == statement.ActionFirst {
		if rows.Failed() || rows.K != value.Array {
			return rows
		}
		if list := rows.Array(); len(list) > 0 {
			return list[0]
		}
		return value.NewNil()
	}
	return rows
}

// matchesColumns is the .searchable() columns of a table as its index holds them: by name, each with its weight.
func matchesColumns(columns map[string]*ColumnSpec) []fulltext.Column {
	var found []fulltext.Column
	for name, spec := range columns {
		if spec.searchable {
			found = append(found, fulltext.Column{Name: name, Boost: float64(max(spec.searchWt, 1))})
		}
	}
	sort.Slice(found, func(left, right int) bool { return found[left].Name < found[right].Name })
	return found
}

func matchesIndexed(index *fulltext.Table, field string) bool {
	for _, column := range index.Columns() {
		if column.Name == field {
			return true
		}
	}
	return false
}

// withoutMatches is a condition with each matches, and each !matches, read as true: what runs as SQL.
func withoutMatches(node *statement.Node) *statement.Node {
	switch {
	case node == nil:
		return nil
	case node.Op == statement.OpMatches,
		node.Op == statement.OpNot && len(node.Args) == 1 && node.Args[0].Op == statement.OpMatches:
		return &statement.Node{Op: statement.OpTable}
	case node.Op == statement.OpAnd:
		return &statement.Node{Op: statement.OpAnd, Args: []*statement.Node{withoutMatches(node.Args[0]), withoutMatches(node.Args[1])}}
	}
	return node
}

// matchesFailure says why the index did not answer.
func matchesFailure(table string, err error) value.Value {
	var building *fulltext.BuildingError
	if errors.As(err, &building) {
		progress := "chưa bắt đầu"
		if building.Total > 0 {
			progress = fmt.Sprintf("%d/%d dòng", building.Built, building.Total)
		}
		return value.InvalidFailure(value.UnavailableCode, fmt.Sprintf("chỉ mục tìm kiếm của bảng %s đang dựng (%s): thử lại sau", table, progress))
	}
	return value.InvalidFailure(value.DatabaseCode, fmt.Sprintf("matches trên %s: %v", table, err))
}

// matchesRun is one run of a statement with matches.
type matchesRun struct {
	m         *ManagedDatabase
	ctx       context.Context
	s         *statement.Statement
	columns   map[string]*ColumnSpec
	index     *fulltext.Table
	ranking   []matchesTerm
	excluding []matchesTerm
	predicate *query.Predicate
	primary   string
}

// hits ranks at most limit rows by the first positive matches, keeps those every other one finds and none of the
// filtering ones does, and reports whether the index had more.
func (run *matchesRun) hits(limit int) ([]matchesHit, bool, value.Value) {
	found, err := run.index.Search(run.ctx, run.ranking[0].query(), limit)
	if err != nil {
		return nil, false, matchesFailure(run.s.Table, err)
	}
	more := len(found) == limit
	scores := make(map[string]float64, len(found))
	for _, hit := range found {
		scores[hit.ID] = hit.Score
	}
	sets := func(term matchesTerm) (map[string]float64, value.Value) {
		hits, err := run.index.Search(run.ctx, term.query(), search.MaximumSearchLimit)
		if err != nil {
			return nil, matchesFailure(run.s.Table, err)
		}
		if len(hits) == search.MaximumSearchLimit {
			return nil, value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf(
				"matches(%q) trên %s khớp quá %d dòng: một câu đọc từng ấy dòng của chỉ mục cho mỗi matches sau cái đầu; thu hẹp chữ tìm",
				term.text, run.s.Table, search.MaximumSearchLimit))
		}
		set := make(map[string]float64, len(hits))
		for _, hit := range hits {
			set[hit.ID] = hit.Score
		}
		return set, value.Value{}
	}
	for _, term := range run.ranking[1:] {
		set, failure := sets(term)
		if failure.Failed() {
			return nil, false, failure
		}
		for id := range scores {
			if score, ok := set[id]; ok {
				scores[id] += score
			} else {
				delete(scores, id)
			}
		}
	}
	for _, term := range run.excluding {
		set, failure := sets(term)
		if failure.Failed() {
			return nil, false, failure
		}
		for id := range set {
			delete(scores, id)
		}
	}
	hits := make([]matchesHit, 0, len(scores))
	for position, hit := range found {
		score, ok := scores[hit.ID]
		if !ok {
			continue
		}
		rowID, err := fulltext.RowID(hit)
		if err != nil {
			return nil, false, value.InvalidFailure(value.DatabaseCode, fmt.Sprintf("chỉ mục của %s giữ tài liệu %q không phải rowid", run.s.Table, hit.ID))
		}
		hits = append(hits, matchesHit{rowID: rowID, score: score, rank: position})
	}
	// several positive matches rank by the sum of their scores
	if len(run.ranking) > 1 {
		sort.SliceStable(hits, func(left, right int) bool { return hits[left].score > hits[right].score })
	}
	return hits, more, value.Value{}
}

// query is a fresh query of the table: the rest of the condition, and only the rows of ids.
func (run *matchesRun) query(ids []any) (*SchemaTable, value.Value) {
	table, failure := run.m.proxy.table(run.s.Table)
	if table == nil {
		return nil, failure
	}
	if run.predicate != nil {
		table.builder().WherePredicate(*run.predicate)
	}
	table.builder().In("_rowid_", ids)
	return table, value.Value{}
}

// keys maps the rowids of hits to the key of their rows, which the rows a query gives carry.
func (run *matchesRun) keys(hits []matchesHit) (map[string]matchesHit, []any, value.Value) {
	ids := make([]any, len(hits))
	byRowID := make(map[int64]matchesHit, len(hits))
	for position, hit := range hits {
		ids[position] = hit.rowID
		byRowID[hit.rowID] = hit
	}
	byKey := make(map[string]matchesHit, len(hits))
	if len(hits) == 0 {
		return byKey, ids, value.Value{}
	}
	table, _ := run.m.proxy.table(run.s.Table)
	source := table.source()
	if source == nil || source.db() == nil {
		return nil, nil, value.InvalidFailure(value.UnavailableCode, "matches: database unavailable")
	}
	marks := strings.TrimSuffix(strings.Repeat("?, ", len(ids)), ", ")
	rows, err := source.db().QueryContext(run.ctx, `SELECT _rowid_, `+quoteSearchIdentifier(run.primary)+` FROM `+
		quoteSearchIdentifier(run.s.Table)+` WHERE _rowid_ IN (`+marks+`)`, ids...)
	if err != nil {
		return nil, nil, value.InvalidFailure(value.DatabaseCode, fmt.Sprintf("matches trên %s: %v", run.s.Table, err))
	}
	defer rows.Close()
	for rows.Next() {
		var rowID int64
		var key any
		if err := rows.Scan(&rowID, &key); err != nil {
			return nil, nil, value.InvalidFailure(value.DatabaseCode, fmt.Sprintf("matches trên %s: %v", run.s.Table, err))
		}
		byKey[matchesKey(key)] = byRowID[rowID]
	}
	if err := rows.Err(); err != nil {
		return nil, nil, value.InvalidFailure(value.DatabaseCode, fmt.Sprintf("matches trên %s: %v", run.s.Table, err))
	}
	return byKey, ids, value.Value{}
}

// matchesKey is a key as text, the same for the value SQLite stores and the value a row carries.
func matchesKey(key any) string {
	switch typed := key.(type) {
	case int64:
		return strconv.FormatInt(typed, 10)
	case float64:
		if typed == float64(int64(typed)) {
			return strconv.FormatInt(int64(typed), 10)
		}
		return strconv.FormatFloat(typed, 'g', -1, 64)
	case []byte:
		return string(typed)
	case value.Value:
		if typed.K == value.Number {
			return matchesKey(typed.N)
		}
		return typed.String()
	}
	return fmt.Sprint(key)
}

// list gives the rows of the page: in the order of score, or of the statement's keys when it has some.
func (run *matchesRun) list(limit, offset int) value.Value {
	needed := offset + limit
	if len(run.s.Order) > 0 {
		hits, more, failure := run.hits(matchesCandidates)
		if failure.Failed() {
			return failure
		}
		if more {
			return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf(
				"matches cùng orderBy xếp mọi dòng khớp, và %s khớp quá %d dòng: thu hẹp chữ tìm hay bỏ orderBy để xếp theo độ khớp", run.s.Table, matchesCandidates))
		}
		byKey, ids, failure := run.keys(hits)
		if failure.Failed() {
			return failure
		}
		table, failure := run.query(ids)
		if table == nil {
			return failure
		}
		for _, key := range run.s.Order {
			direction := "ASC"
			if key.Descending {
				direction = "DESC"
			}
			table.OrderBy(key.Column, direction)
		}
		table.Limit(limit)
		table.builder().Limited(limit)
		if offset > 0 {
			table.Offset(offset)
		}
		rows := table.List()
		if rows.Failed() {
			return rows
		}
		return run.decorate(rows, byKey, false)
	}
	candidates := min(max(needed*4, 64), matchesCandidates)
	for {
		hits, more, failure := run.hits(candidates)
		if failure.Failed() {
			return failure
		}
		byKey, ids, failure := run.keys(hits)
		if failure.Failed() {
			return failure
		}
		table, failure := run.query(ids)
		if table == nil {
			return failure
		}
		table.Limit(len(ids) + 1)
		table.builder().Limited(len(ids) + 1)
		rows := table.List()
		if rows.Failed() {
			return rows
		}
		ranked := run.decorate(rows, byKey, true)
		list := ranked.Array()
		if len(list) >= needed || !more || candidates == matchesCandidates {
			if len(list) <= offset {
				if more && offset > 0 {
					return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf(
						"trang này của matches trên %s nằm sau %d dòng khớp nhất mà một câu đọc: thu hẹp chữ tìm", run.s.Table, matchesCandidates))
				}
				return value.New([]value.Value{})
			}
			return value.New(list[offset:min(needed, len(list))])
		}
		candidates = min(candidates*4, matchesCandidates)
	}
}

// decorate gives each row its score and snippet, read only, and puts the rows in the order of the index when ranked.
func (run *matchesRun) decorate(rows value.Value, byKey map[string]matchesHit, ranked bool) value.Value {
	if rows.K != value.Array {
		return value.New([]value.Value{})
	}
	type decorated struct {
		row  value.Value
		rank int
	}
	snippetColumns := map[string]bool{}
	for _, term := range run.ranking {
		for _, field := range term.fields {
			snippetColumns[field] = true
		}
	}
	var columns []searchColumn
	for _, column := range run.index.Columns() {
		if snippetColumns[column.Name] {
			columns = append(columns, searchColumn{name: column.Name, weight: max(int(column.Boost), 1)})
		}
	}
	words := make([]string, len(run.ranking))
	for position, term := range run.ranking {
		words[position] = term.text
	}
	text := strings.Join(words, " ")
	list := make([]decorated, 0, len(rows.Array()))
	for _, row := range rows.Array() {
		if row.K != value.Map {
			continue
		}
		fields := row.Map()
		hit, ok := byKey[matchesKey(fields[run.primary])]
		if !ok {
			continue
		}
		snippet, err := bestSearchSnippet(run.ctx, fields, columns, text)
		if err != nil {
			return value.InvalidFailure(value.DatabaseCode, fmt.Sprintf("matches trên %s: snippet: %v", run.s.Table, err))
		}
		fields["score"] = value.New(hit.score)
		fields["snippet"] = value.New(snippet)
		list = append(list, decorated{row: row, rank: hit.rank})
	}
	if ranked {
		sort.SliceStable(list, func(left, right int) bool { return list[left].rank < list[right].rank })
	}
	result := make([]value.Value, len(list))
	for position, item := range list {
		result[position] = item.row
	}
	return value.New(result)
}

// count counts the rows that match: on the index alone when matches is the whole condition, else on the rows the
// index gives (at most search.MaximumSearchLimit), the rest of the condition run on them.
func (run *matchesRun) count() value.Value {
	if len(run.ranking) == 1 && len(run.excluding) == 0 && run.predicate == nil {
		var counted uint64
		err := run.index.Reading(run.ctx, func(index *search.Index) error {
			found, err := index.Count(run.ctx, run.ranking[0].query(), nil)
			counted = found
			return err
		})
		if err != nil {
			return matchesFailure(run.s.Table, err)
		}
		return value.New(float64(counted))
	}
	hits, more, failure := run.hits(search.MaximumSearchLimit)
	if failure.Failed() {
		return failure
	}
	if more {
		return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf(
			"đếm matches trên %s đọc tối đa %d dòng khớp, và câu này khớp nhiều hơn: thu hẹp chữ tìm", run.s.Table, search.MaximumSearchLimit))
	}
	total := 0.0
	for start := 0; start < len(hits); start += matchesCandidates {
		chunk := hits[start:min(start+matchesCandidates, len(hits))]
		ids := make([]any, len(chunk))
		for position, hit := range chunk {
			ids[position] = hit.rowID
		}
		table, failure := run.query(ids)
		if table == nil {
			return failure
		}
		counted := table.Count()
		if counted.Failed() {
			return counted
		}
		total += counted.N
	}
	return value.New(total)
}
