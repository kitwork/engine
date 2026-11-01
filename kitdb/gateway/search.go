package gateway

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/kitwork/engine/kitdb/pgwire"
)

// MATCH is Kitwork's full-text predicate on the port (KITWORK_KITDB.md question 13.5), written as SQLite's FTS5
// writes it, in the WHERE of a SELECT on one table:
//
//	WHERE shopping MATCH 'ban phim logitech'     the table (or its alias): every column its index was declared with
//	WHERE name MATCH 'logitech'                  one of them
//	WHERE (name, brand) MATCH $1                 some of them
//
// AND the other conditions, never under OR or NOT. The words are matched without accents or case; the query is one
// kind: words (each required, the last one may be typed in part: 'quan dui na*'), words with OR between them (any),
// one "phrase", or one prefix*, then words to leave out ('ban phim NOT co'). The rows come best
// match first. Two columns come with MATCH, named like SQLite's own _rowid_: _score_, the row's score (higher is
// better; ORDER BY _score_ DESC is the only order), and _snippet_, the passage of the matched columns around the
// words, the words in <b>…</b>, HTML-escaped. A table with a column of one of those names (or _cursor_, kept for
// paging) is refused. The session asks SearchProvider for the ranked rowids, then runs the statement as SQLite with
// the table joined to them: the user's grants, the other conditions and LIMIT apply as to any statement.

// SearchProvider answers MATCH from the database's full-text indexes. Errors are the client's (a table without an
// index, an index still being built) as pgwire errors.
type SearchProvider interface {
	// Search ranks the rows of schema.table whose fields (every declared one when empty) match the query, best first.
	Search(ctx context.Context, schema, table string, fields []string, query SearchQuery, limit int) ([]SearchHit, error)
	// Count counts them.
	Count(ctx context.Context, schema, table string, fields []string, query SearchQuery) (uint64, error)
	// Fields is what fields name (every declared column when empty), each with its weight, for _snippet_.
	Fields(ctx context.Context, schema, table string, fields []string) ([]SearchField, error)
}

// SearchHit is one matching row: its rowid and its score.
type SearchHit struct {
	RowID int64
	Score float64
}

// SearchField is one indexed column and its weight.
type SearchField struct {
	Name  string
	Boost float64
}

// SearchKind is what a MATCH query asks.
type SearchKind int

const (
	SearchWords  SearchKind = iota // every word
	SearchAny                      // any of the words: 'logitech OR razer'
	SearchPhrase                   // the words in a row: '"ban phim co"'
	SearchPrefix                   // a word that begins so: 'logi*'
)

// SearchQuery is a MATCH query read: its kind and what the index searches (the words, the phrase, the prefix); a
// last word typed in part (words ending in a prefix: 'quan dui na*') and the words to leave out ('ban phim NOT co'),
// which the session checks on the rows the index ranked; the words that mark a snippet.
type SearchQuery struct {
	Kind     SearchKind
	Text     string
	Prefix   string
	Excluded []string
	Words    string
}

// filtered reports a query the session finishes on the rows: a prefix after words, or words left out.
func (query SearchQuery) filtered() bool { return query.Prefix != "" || len(query.Excluded) > 0 }

// maxSearchCandidates is the most rows one MATCH ranks (search.MaximumSearchLimit).
const maxSearchCandidates = 100_000

// The columns MATCH adds to a row; a table may not have a column of these names.
const (
	scoreColumn   = "_score_"
	snippetColumn = "_snippet_"
	cursorColumn  = "_cursor_"
)

var matchColumns = []string{scoreColumn, snippetColumn, cursorColumn}

// matchClause is a MATCH found in a statement.
type matchClause struct {
	termStart, termEnd int // tokens of "<left> MATCH <query>"
	fields             []string
	text               token // a string or a parameter
	schema, table      string
	tableEnd           int    // the token after the FROM item
	reference          string // what the FROM item is called in the statement (its alias, else its name as written)
	orderStart         int    // ORDER BY _score_ DESC, or -1
	orderEnd           int
	insertOrder        int // where ORDER BY goes when there is none (before LIMIT, or at the end)
	limit              token
	offset             token
	hasLimit           bool
	residual           bool // the WHERE has more than MATCH
	aggregate          bool // GROUP BY, or an aggregate in the select list
	countAlias         string
	countOnly          bool  // SELECT count(*) … WHERE <MATCH> alone: answered by Count
	snippets           []int // tokens naming _snippet_ (with their qualifier)
}

func matchError(message string) error { return pgwire.NewError("42601", "MATCH: "+message) }

// matchOperand reports what may follow MATCH or SEARCH: the query (a string or a parameter), or a number, refused
// with a message rather than read as a column of that name.
func matchOperand(candidate token) bool {
	return candidate.kind == tokenString || candidate.kind == tokenVariable || candidate.kind == tokenNumber
}

// predicateAt reports the keyword at index as a predicate: a query after it, a column, a table or a list before it.
func predicateAt(tokens []token, index int, keyword string) bool {
	if index == 0 || index+1 >= len(tokens) || tokens[index].word() != keyword || !matchOperand(tokens[index+1]) {
		return false
	}
	previous := tokens[index-1]
	return isPunctuation(previous, "*") || isPunctuation(previous, ")") || isIdentifier(previous) ||
		(keyword == "SEARCH" && (previous.word() == "WHERE" || previous.word() == "AND"))
}

// hasMatch reports a MATCH predicate in tokens. A column named match is not one.
func hasMatch(tokens []token) bool {
	for index := range tokens {
		if predicateAt(tokens, index, "MATCH") {
			return true
		}
	}
	return false
}

// hasOldSearch reports the KitDB port's SEARCH predicate (* SEARCH 'words'), which is now MATCH.
func hasOldSearch(tokens []token) bool {
	for index := range tokens {
		if predicateAt(tokens, index, "SEARCH") {
			return true
		}
	}
	return false
}

// oldSearch refuses the KitDB port's SEARCH, pointing to MATCH.
func oldSearch(tokens []token) error {
	if !hasOldSearch(tokens) {
		return nil
	}
	return matchError("SEARCH is now MATCH: WHERE <table> MATCH 'words' (every indexed column), <column> MATCH 'words', " +
		"and _score_ for the score")
}

// aggregateNames are the calls that make a select list aggregate.
var aggregateNames = map[string]bool{
	"COUNT": true, "SUM": true, "AVG": true, "MIN": true, "MAX": true, "TOTAL": true, "GROUP_CONCAT": true,
	"STRING_AGG": true, "JSON_GROUP_ARRAY": true, "JSON_GROUP_OBJECT": true, "KITWORK_PG_ARRAY_AGG": true,
}

// parseMatch reads the MATCH of a statement: in the WHERE of its outer SELECT (behind EXPLAIN [QUERY PLAN]), on one
// table.
func parseMatch(tokens []token) (*matchClause, error) {
	start := 0
	if tokens[0].word() == "EXPLAIN" {
		start = 1
		if len(tokens) > 2 && tokens[1].word() == "QUERY" && tokens[2].word() == "PLAN" {
			start = 3
		}
	}
	if start >= len(tokens) || tokens[start].word() != "SELECT" {
		return nil, matchError("it is read in the WHERE of a SELECT")
	}
	// The outer statement's clauses, at depth 0.
	clause := map[string]int{"FROM": -1, "WHERE": -1, "GROUP": -1, "ORDER": -1, "LIMIT": -1, "HAVING": -1, "WINDOW": -1}
	match := -1
	depth := 0
	for index := start; index < len(tokens); index++ {
		candidate := tokens[index]
		switch {
		case isPunctuation(candidate, "("):
			depth++
		case isPunctuation(candidate, ")"):
			depth--
		case predicateAt(tokens, index, "MATCH"):
			if depth != 0 {
				return nil, matchError("it is read in the WHERE of the outer SELECT, not in a subquery")
			}
			if match >= 0 {
				return nil, matchError("a statement has one MATCH")
			}
			match = index
		case depth == 0 && (candidate.word() == "UNION" || candidate.word() == "INTERSECT" || candidate.word() == "EXCEPT"):
			return nil, matchError("it is not read in a compound SELECT (UNION, INTERSECT, EXCEPT)")
		case depth == 0:
			if position, known := clause[candidate.word()]; known && position < 0 {
				clause[candidate.word()] = index
			}
		}
	}
	if match < 0 {
		return nil, matchError("no MATCH")
	}
	if clause["FROM"] < 0 || clause["WHERE"] < 0 || match < clause["WHERE"] {
		return nil, matchError("it is read in the WHERE of a SELECT … FROM one table")
	}
	whereEnd := len(tokens)
	for _, name := range []string{"GROUP", "HAVING", "WINDOW", "ORDER", "LIMIT"} {
		if position := clause[name]; position > clause["WHERE"] && position < whereEnd {
			whereEnd = position
		}
	}
	if match > whereEnd {
		return nil, matchError("it is read in the WHERE, not in " + tokens[whereEnd].text)
	}
	result := &matchClause{orderStart: -1, insertOrder: len(tokens)}

	// The table: FROM name | schema.name, with an alias or not.
	from, end := clause["FROM"], clause["WHERE"]
	item := tokens[from+1 : end]
	nameEnd := 1
	switch {
	case len(item) >= 3 && isIdentifier(item[0]) && isPunctuation(item[1], ".") && isIdentifier(item[2]):
		result.schema, result.table = unquoted(item[0]), unquoted(item[2])
		nameEnd = 3
	case len(item) >= 1 && isIdentifier(item[0]):
		result.schema, result.table = "public", unquoted(item[0])
	default:
		return nil, matchError("it reads one table: FROM name [alias]")
	}
	result.reference = joinedText(item[:nameEnd])
	alias := ""
	rest := item[nameEnd:]
	if len(rest) > 0 && rest[0].word() == "AS" {
		rest = rest[1:]
	}
	switch {
	case len(rest) == 1 && isIdentifier(rest[0]) && !notFunctions[rest[0].word()]:
		result.reference, alias = rest[0].text, unquoted(rest[0])
	case len(rest) != 0:
		return nil, matchError("it reads one table: FROM name [alias], without JOIN")
	}
	result.tableEnd = end
	// What names the table in the statement: its alias, else its name.
	isTable := func(name string) bool {
		if alias != "" {
			return strings.EqualFold(name, alias)
		}
		return strings.EqualFold(name, result.table)
	}

	// The left operand: the table (every declared column), a column, or a list of columns.
	termStart := match
	previous := tokens[match-1]
	switch {
	case isPunctuation(previous, "*"):
		return nil, matchError(fmt.Sprintf("write the table before MATCH for every column: %s MATCH '…'", result.table))
	case isPunctuation(previous, ")"):
		open := matchingOpen(tokens, match-1)
		if open < 0 {
			return nil, matchError("unbalanced parentheses before MATCH")
		}
		// Each part between commas is a column, qualified or not: its last name.
		partStart := open + 1
		for index := open + 1; index <= match-1; index++ {
			if index < match-1 && !isPunctuation(tokens[index], ",") {
				continue
			}
			part := tokens[partStart:index]
			if len(part) == 0 || len(part)%2 == 0 {
				return nil, matchError("write the columns as (name, brand) MATCH 'words'")
			}
			for position, piece := range part {
				if (position%2 == 0 && !isIdentifier(piece)) || (position%2 == 1 && !isPunctuation(piece, ".")) {
					return nil, matchError("write the columns as (name, brand) MATCH 'words'")
				}
			}
			result.fields = append(result.fields, unquoted(part[len(part)-1]))
			partStart = index + 1
		}
		termStart = open
	case isIdentifier(previous):
		termStart = qualifiedStart(tokens, match-1)
		if termStart == match-1 && isTable(unquoted(previous)) {
			break // the table: every declared column
		}
		result.fields = []string{unquoted(previous)}
	default:
		return nil, matchError("write table MATCH 'words', name MATCH 'words' or (name, brand) MATCH 'words'")
	}
	if termStart-1 != clause["WHERE"] && tokens[termStart-1].word() != "AND" {
		return nil, matchError("it is ANDed with the other conditions of the WHERE: never under OR or NOT, nor inside parentheses")
	}
	if tokens[match+1].kind != tokenString && tokens[match+1].kind != tokenVariable {
		return nil, matchError("MATCH takes the query as a string or a parameter: shopping MATCH 'ban phim'")
	}
	result.text = tokens[match+1]
	result.termStart, result.termEnd = termStart, match+2
	if result.termEnd < whereEnd && tokens[result.termEnd].word() != "AND" {
		return nil, matchError("it is ANDed with the other conditions of the WHERE: never under OR")
	}
	depth = 0
	for index := clause["WHERE"] + 1; index < whereEnd; index++ {
		switch {
		case isPunctuation(tokens[index], "("):
			depth++
		case isPunctuation(tokens[index], ")"):
			depth--
		case depth == 0 && tokens[index].word() == "OR":
			return nil, matchError("it is ANDed with the other conditions of the WHERE: put an OR inside parentheses")
		}
	}
	result.residual = termStart-1 != clause["WHERE"] || result.termEnd != whereEnd

	// Aggregates: GROUP BY, or a call of an aggregate in the select list.
	result.aggregate = clause["GROUP"] >= 0
	selectList := tokens[start+1 : from]
	for index := 0; index+1 < len(selectList); index++ {
		if aggregateNames[selectList[index].word()] && isPunctuation(selectList[index+1], "(") {
			result.aggregate = true
		}
	}
	// ORDER BY: _score_ DESC only, or none (best match first). An aggregate reads every match: it orders as it likes.
	if order := clause["ORDER"]; order >= 0 && !result.aggregate {
		orderEnd := len(tokens)
		if limit := clause["LIMIT"]; limit > order {
			orderEnd = limit
		}
		words := make([]string, 0, 4)
		for _, candidate := range tokens[order:orderEnd] {
			words = append(words, strings.ToUpper(unquoted(candidate)))
		}
		if strings.Join(words, " ") != "ORDER BY _SCORE_ DESC" {
			return nil, matchError("rows come best match first: the only order is ORDER BY _score_ DESC")
		}
		result.orderStart, result.orderEnd = order, orderEnd
	}
	if limit := clause["LIMIT"]; limit >= 0 {
		result.insertOrder = limit
		result.hasLimit = true
		if limit+1 < len(tokens) {
			result.limit = tokens[limit+1]
		}
		if limit+3 < len(tokens) && tokens[limit+2].word() == "OFFSET" {
			result.offset = tokens[limit+3]
		} else if limit+3 < len(tokens) && isPunctuation(tokens[limit+2], ",") {
			result.offset, result.limit = tokens[limit+1], tokens[limit+3]
		}
		for index := limit + 1; index < len(tokens); index++ {
			if tokens[index].word() == "AFTER" {
				return nil, pgwire.NewError("0A000", "MATCH: LIMIT … AFTER (paging by _cursor_) is not available yet: use OFFSET")
			}
		}
	}
	if !result.residual && clause["GROUP"] < 0 && clause["HAVING"] < 0 && start == 0 && len(selectList) >= 4 &&
		selectList[0].word() == "COUNT" && isPunctuation(selectList[1], "(") && isPunctuation(selectList[2], "*") &&
		isPunctuation(selectList[3], ")") {
		switch countAlias := selectList[4:]; {
		case len(countAlias) == 0:
			result.countOnly, result.countAlias = true, "count"
		case len(countAlias) == 2 && countAlias[0].word() == "AS" && isIdentifier(countAlias[1]):
			result.countOnly, result.countAlias = true, unquoted(countAlias[1])
		case len(countAlias) == 1 && isIdentifier(countAlias[0]):
			result.countOnly, result.countAlias = true, unquoted(countAlias[0])
		}
	}
	// _snippet_ wherever the statement names it (bare, or qualified by the table); _cursor_ is not there yet.
	for index, candidate := range tokens {
		if !isIdentifier(candidate) || (index > 0 && isPunctuation(tokens[index-1], ".") && index >= 2 && !isTable(unquoted(tokens[index-2]))) {
			continue
		}
		switch strings.ToLower(unquoted(candidate)) {
		case snippetColumn:
			result.snippets = append(result.snippets, index)
		case cursorColumn:
			return nil, pgwire.NewError("0A000", "MATCH: _cursor_ (paging) is not available yet: use OFFSET")
		}
	}
	return result, nil
}

// queryKinds is what a MATCH query may be, for its errors.
const queryKinds = "MATCH reads words ('ban phim'), words ending in a prefix ('quan dui na*'), words with OR between them " +
	"('logitech OR razer'), one phrase ('\"ban phim co\"') or one prefix ('logi*'), then words to leave out, each after NOT " +
	"('ban phim NOT co NOT cu')"

// parseSearchQuery reads a MATCH query: words (the last one may end in *), words with OR between them, one "phrase",
// or one prefix*; then NOT word for each word to leave out.
func parseSearchQuery(text string) (SearchQuery, error) {
	refused := errorf(queryKinds)
	items, ok := queryItems(text)
	if !ok {
		return SearchQuery{}, refused
	}
	query := SearchQuery{}
	main := items
	for index, item := range items {
		if item != "NOT" {
			continue
		}
		main = items[:index]
		rest := items[index:]
		if len(rest)%2 != 0 {
			return SearchQuery{}, refused
		}
		for position := 0; position < len(rest); position += 2 {
			word := rest[position+1]
			if rest[position] != "NOT" || word == "NOT" || word == "OR" || strings.ContainsAny(word, `"*`) {
				return SearchQuery{}, refused
			}
			query.Excluded = append(query.Excluded, word)
		}
		break
	}
	switch {
	case len(main) == 0:
		if len(query.Excluded) > 0 {
			return SearchQuery{}, refused // NOT alone: nothing to find
		}
		query.Kind = SearchWords
	case len(main) == 1 && strings.HasPrefix(main[0], `"`):
		phrase := strings.TrimSpace(main[0][1 : len(main[0])-1])
		if phrase == "" || strings.Contains(phrase, "*") {
			return SearchQuery{}, refused
		}
		query.Kind, query.Text = SearchPhrase, phrase
	default:
		any := false
		for _, item := range main {
			if strings.HasPrefix(item, `"`) {
				return SearchQuery{}, refused
			}
			any = any || item == "OR"
		}
		if any {
			if len(main) < 3 || len(main)%2 == 0 {
				return SearchQuery{}, refused
			}
			var terms []string
			for position, item := range main {
				if (position%2 == 1) != (item == "OR") || strings.Contains(item, "*") {
					return SearchQuery{}, refused
				}
				if position%2 == 0 {
					terms = append(terms, item)
				}
			}
			query.Kind, query.Text = SearchAny, strings.Join(terms, " ")
			break
		}
		last := main[len(main)-1]
		for _, item := range main[:len(main)-1] {
			if strings.Contains(item, "*") {
				return SearchQuery{}, refused
			}
		}
		if strings.Contains(last, "*") {
			if strings.Count(last, "*") != 1 || !strings.HasSuffix(last, "*") || len(last) < 2 {
				return SearchQuery{}, refused
			}
			stem := strings.TrimSuffix(last, "*")
			if len(main) == 1 {
				query.Kind, query.Text = SearchPrefix, stem
				break
			}
			query.Kind, query.Text, query.Prefix = SearchWords, strings.Join(main[:len(main)-1], " "), stem
			break
		}
		query.Kind, query.Text = SearchWords, strings.Join(main, " ")
	}
	query.Words = strings.TrimSpace(query.Text + " " + query.Prefix)
	return query, nil
}

// queryItems splits a query at spaces, a "quoted phrase" one item with its quotes; false for an unclosed quote.
func queryItems(text string) ([]string, bool) {
	var items []string
	var current strings.Builder
	quoted := false
	flush := func() {
		if current.Len() > 0 {
			items = append(items, current.String())
			current.Reset()
		}
	}
	for _, character := range text {
		switch {
		case character == '"':
			if !quoted {
				flush()
			}
			current.WriteRune(character)
			if quoted {
				flush()
			}
			quoted = !quoted
		case !quoted && (character == ' ' || character == '\t' || character == '\n' || character == '\r'):
			flush()
		default:
			current.WriteRune(character)
		}
	}
	flush()
	return items, !quoted
}

func errorf(message string) error { return pgwire.NewError("42601", message) }

// joinedText is tokens' texts without what was between them: a name written public . shopping is public.shopping.
func joinedText(tokens []token) string {
	var builder strings.Builder
	for _, candidate := range tokens {
		builder.WriteString(candidate.text)
	}
	return builder.String()
}

// searchValue is a token's value: a string literal's text, a number, or a parameter's argument.
func searchValue(candidate token, arguments []any) (any, bool) {
	switch candidate.kind {
	case tokenString:
		if strings.HasPrefix(candidate.text, "'") {
			return strings.ReplaceAll(candidate.text[1:len(candidate.text)-1], "''", "'"), true
		}
	case tokenNumber:
		if number, err := strconv.ParseInt(candidate.text, 10, 64); err == nil {
			return number, true
		}
	case tokenVariable:
		name := strings.TrimLeft(candidate.text, "$?:@")
		if position, err := strconv.Atoi(name); err == nil && position >= 1 && position <= len(arguments) {
			return arguments[position-1], true
		}
	}
	return nil, false
}

func searchInteger(candidate token, arguments []any) (int, bool) {
	value, ok := searchValue(candidate, arguments)
	if !ok {
		return 0, false
	}
	switch typed := value.(type) {
	case int64:
		return int(typed), typed >= 0
	case string:
		number, err := strconv.Atoi(strings.TrimSpace(typed))
		return number, err == nil && number >= 0
	case []byte:
		number, err := strconv.Atoi(strings.TrimSpace(string(typed)))
		return number, err == nil && number >= 0
	}
	return 0, false
}

// matchRead runs a read statement that has a MATCH. Without the argument values (Describe), it describes the
// statement over no rows.
func (session *Session) matchRead(
	ctx context.Context,
	parsed statement,
	parameters []pgwire.Parameter,
	describeOnly bool,
) (pgwire.Result, []pgwire.Column, error) {
	clause, err := parseMatch(parsed.tokens)
	if err != nil {
		return pgwire.Result{}, nil, err
	}
	if session.options.Search == nil {
		return pgwire.Result{}, nil, pgwire.NewError("0A000", "MATCH: this port has no full-text index")
	}
	if err := session.checkMatchColumns(ctx, clause); err != nil {
		return pgwire.Result{}, nil, err
	}
	arguments, err := bindParameters(parameters)
	if err != nil {
		return pgwire.Result{}, nil, err
	}
	provider := session.options.Search
	query := SearchQuery{}
	var fields []SearchField
	if !describeOnly {
		value, known := searchValue(clause.text, arguments)
		text, isText := value.(string)
		if bytes, isBytes := value.([]byte); isBytes {
			text, isText = string(bytes), true
		}
		if !known || !isText {
			return pgwire.Result{}, nil, matchError("MATCH takes the query as text")
		}
		if query, err = parseSearchQuery(text); err != nil {
			return pgwire.Result{}, nil, err
		}
	}
	if len(clause.snippets) > 0 || query.filtered() {
		if fields, err = provider.Fields(ctx, clause.schema, clause.table, clause.fields); err != nil {
			return pgwire.Result{}, nil, err
		}
	}
	run := func(hits []SearchHit) (pgwire.Result, []pgwire.Column, error) {
		rewritten, err := parseStatement(session.matchStatement(parsed, clause, query, fields, hits))
		if err != nil {
			return pgwire.Result{}, nil, err
		}
		result, columns, err := session.read(ctx, rewritten, parameters, describeOnly)
		return result, columns, matchColumnHint(err)
	}
	if describeOnly {
		if clause.countOnly {
			return pgwire.Result{}, []pgwire.Column{{Name: clause.countAlias, DataTypeOID: pgwire.OIDInt8, DataTypeSize: 8}}, nil
		}
		return run(nil)
	}

	// A prefix after words and words left out are checked on the rows: they make the WHERE more than the index's.
	countOnly := clause.countOnly && !query.filtered()
	residual := clause.residual || query.filtered()
	if countOnly {
		// The rows counted are rows the user reads.
		allowed, err := session.access.CanSelect(ctx, clause.schema, clause.table)
		if err != nil {
			return pgwire.Result{}, nil, protocolError(err, "checking the permission on table "+clause.schema+"."+clause.table)
		}
		if !allowed {
			return pgwire.Result{}, nil, pgwire.NewError("42501", fmt.Sprintf("permission denied for table %s.%s", clause.schema, clause.table))
		}
		count, err := provider.Count(ctx, clause.schema, clause.table, clause.fields, query)
		if err != nil {
			return pgwire.Result{}, nil, err
		}
		return pgwire.Result{
			Columns:    []pgwire.Column{{Name: clause.countAlias, DataTypeOID: pgwire.OIDInt8, DataTypeSize: 8}},
			Rows:       [][]pgwire.Field{{{Data: []byte(strconv.FormatUint(count, 10))}}},
			CommandTag: "SELECT 1",
		}, nil, nil
	}

	limit, offset, hasLimit := 0, 0, false
	if clause.hasLimit {
		limit, hasLimit = searchInteger(clause.limit, arguments)
		offset, _ = searchInteger(clause.offset, arguments)
	}
	candidates := maxSearchCandidates
	switch {
	case clause.aggregate:
	case hasLimit && !residual:
		candidates = min(max(limit+offset, 1), maxSearchCandidates)
	case hasLimit:
		candidates = min(max((limit+offset)*4, 200), maxSearchCandidates)
	default:
		// Without LIMIT a result holds at most MaxRows rows: one more is enough to refuse a larger one.
		candidates = min(session.options.MaxRows+1, maxSearchCandidates)
	}
	for {
		hits, err := provider.Search(ctx, clause.schema, clause.table, clause.fields, query, candidates)
		if err != nil {
			return pgwire.Result{}, nil, err
		}
		exhausted := len(hits) < candidates
		if clause.aggregate && !exhausted {
			return pgwire.Result{}, nil, pgwire.NewError("54000", fmt.Sprintf(
				"MATCH: an aggregate over MATCH reads at most %d matches, and this query has more: narrow the words or the conditions",
				maxSearchCandidates))
		}
		result, columns, err := run(hits)
		if err != nil || exhausted || !hasLimit || !residual || len(result.Rows) >= limit || candidates >= maxSearchCandidates {
			return result, columns, err
		}
		// The other conditions kept too few of the best matches: rank more of them.
		candidates = min(candidates*4, maxSearchCandidates)
	}
}

// checkMatchColumns refuses a table that has a column named as one MATCH adds.
func (session *Session) checkMatchColumns(ctx context.Context, clause *matchClause) error {
	rows, err := session.connection.QueryContext(ctx, "SELECT name FROM pragma_table_xinfo(?, ?)", clause.table, clause.schema)
	if err != nil {
		return sqliteError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return sqliteError(err)
		}
		for _, reserved := range matchColumns {
			if strings.EqualFold(name, reserved) {
				return pgwire.NewError("42701", fmt.Sprintf(
					"MATCH: table %s.%s has a column named %s, a name MATCH keeps for the column it adds (_score_, _snippet_, _cursor_)",
					clause.schema, clause.table, name))
			}
		}
	}
	return sqliteError(rows.Err())
}

// matchColumnHint adds to SQLite's "no such column: score" in a MATCH statement the names MATCH gives its columns.
func matchColumnHint(err error) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	for _, name := range []string{"_score", "score", "rank", "_snippet", "snippet", "_rowid"} {
		if strings.HasSuffix(message, "no such column: "+name) {
			return pgwire.NewError("42703", message+"; the columns MATCH adds are _score_ and _snippet_")
		}
	}
	return err
}

// matchStatement is the statement with its MATCH answered: the table joined to the ranked rowids (a JSON array
// json_each reads: numbers only), the predicate become true, the order the rank, each _snippet_ the passage of the
// matched columns around the query's words.
func (session *Session) matchStatement(parsed statement, clause *matchClause, query SearchQuery, fields []SearchField, hits []SearchHit) string {
	var ranked strings.Builder
	ranked.WriteByte('[')
	for index, hit := range hits {
		if index > 0 {
			ranked.WriteByte(',')
		}
		ranked.WriteByte('[')
		ranked.WriteString(strconv.FormatInt(hit.RowID, 10))
		ranked.WriteByte(',')
		ranked.WriteString(strconv.FormatFloat(hit.Score, 'g', -1, 64))
		ranked.WriteByte(']')
	}
	ranked.WriteByte(']')
	join := ` JOIN (SELECT CAST(value ->> 0 AS INTEGER) AS "kitwork:rowid", CAST(value ->> 1 AS REAL) AS _score_, key AS "kitwork:rank" FROM json_each('` +
		ranked.String() + `')) AS "kitwork:search" ON ` + clause.reference + `.rowid = "kitwork:search"."kitwork:rowid" `
	term := "1"
	if clause.text.kind == tokenVariable {
		term = "(1 OR " + clause.text.text + " IS NULL)" // the parameter stays in the statement, bound as before
	}
	if query.filtered() {
		columns := make([]string, len(fields))
		for index, field := range fields {
			columns[index] = clause.reference + "." + quoteIdentifier(field.Name)
		}
		listed := strings.Join(columns, ", ")
		conditions := []string{term}
		if query.Prefix != "" {
			conditions = append(conditions, "kitwork_match_prefix("+quoteLiteral(query.Prefix)+", "+listed+")")
		}
		for _, word := range query.Excluded {
			conditions = append(conditions, "NOT kitwork_match_word("+quoteLiteral(word)+", "+listed+")")
		}
		term = "(" + strings.Join(conditions, " AND ") + ")"
	}
	text, tokens := parsed.text, parsed.tokens
	type edit struct {
		from, to    int // byte offsets
		replacement string
	}
	edits := []edit{
		{tokens[clause.tableEnd-1].end, tokens[clause.tableEnd-1].end, join},
		{tokens[clause.termStart].start, tokens[clause.termEnd-1].end, term},
	}
	if len(clause.snippets) > 0 {
		snippet := "kitwork_snippet(" + quoteLiteral(query.Words)
		for _, field := range fields {
			snippet += ", " + clause.reference + "." + quoteIdentifier(field.Name) + ", " + strconv.FormatFloat(field.Boost, 'g', -1, 64)
		}
		snippet += ")"
		for _, index := range clause.snippets {
			from := index
			if index >= 2 && isPunctuation(tokens[index-1], ".") {
				from = index - 2
			}
			edits = append(edits, edit{tokens[from].start, tokens[index].end, snippet + selectItemAlias(tokens, from, index+1, snippetColumn)})
		}
	}
	if !clause.aggregate {
		order := ` ORDER BY "kitwork:search"."kitwork:rank" `
		switch {
		case clause.orderStart >= 0:
			edits = append(edits, edit{tokens[clause.orderStart].start, tokens[clause.orderEnd-1].end, order})
		case clause.insertOrder < len(tokens):
			edits = append(edits, edit{tokens[clause.insertOrder].start, tokens[clause.insertOrder].start, order})
		default:
			edits = append(edits, edit{len(text), len(text), order})
		}
	}
	// Apply from the end, so earlier offsets stay right.
	sort.Slice(edits, func(left, right int) bool { return edits[left].from < edits[right].from })
	for index := len(edits) - 1; index >= 0; index-- {
		text = text[:edits[index].from] + edits[index].replacement + text[edits[index].to:]
	}
	return text
}
