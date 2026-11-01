package gateway

import (
	"database/sql/driver"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/kitwork/engine/kitdb/pgwire"
)

// A decimal column holds its value times 10^scale as an integer (work/db.sqlite_decimal.go): 35000.00 at scale 2 is
// 3500000 in the file. The result writes such a column as its decimal (result.go, decimalText), but a statement that
// compares or computes with it saw the integer: amount > 1000 kept every row above 10.00, and sum(amount) answered a
// hundred times the sum, with no error. decimalStatement rewrites a read statement so each value of a decimal column
// it uses is the decimal itself, as PostgreSQL's numeric is:
//
//   - compared with a literal or a parameter (amount > 1000, amount BETWEEN $1 AND $2, amount IN (1, 2.5)), the literal
//     becomes the stored integer (amount > 100000): exact, and an index on the column still serves;
//   - anywhere else (sum(amount), amount * 2, amount > other, an item of a subquery's select list), the column becomes
//     kitwork_decimal(amount, 2), the decimal as a REAL; a subquery's SELECT * over a decimal column is written out;
//   - an item of the statement's own select list that is the column alone, a term of ORDER BY, GROUP BY or PARTITION
//     BY that is the column alone, and amount IS NULL stay as they are: the result writes the first as its decimal,
//     and the others do not depend on the unit. In a UNION, INTERSECT or EXCEPT every SELECT reads its decimals as
//     values: the result takes a column's kind from the first SELECT alone.
//
// A view over a decimal column is not followed: its columns read the stored integers.

// decimalTable is a table or view of an attached schema as the rewrite reads it.
type decimalTable struct {
	schema, name string
	columns      []string        // the column names in order: what * stands for
	known        map[string]bool // each column, by lower-case name
	scales       map[string]int  // each decimal column of a positive scale, by lower-case name
}

// keepDecimalTables keeps the tables and views the catalog read, for the rewrite.
func (session *Session) keepDecimalTables(relations []*catalogRelation) {
	tables := map[string]map[string]*decimalTable{}
	count := 0
	for _, relation := range relations {
		table := &decimalTable{schema: relation.schema, name: relation.name, known: map[string]bool{}, scales: map[string]int{}}
		for _, attribute := range relation.columns {
			key := strings.ToLower(attribute.name)
			table.columns = append(table.columns, attribute.name)
			table.known[key] = true
			if relation.kind != "r" || session.columns == nil {
				continue
			}
			if kind, _, scale, found := session.columns.Column(relation.schema, relation.name, attribute.name); found && kind == "decimal" && scale > 0 {
				table.scales[key] = scale
				count++
			}
		}
		schema := strings.ToLower(relation.schema)
		if tables[schema] == nil {
			tables[schema] = map[string]*decimalTable{}
		}
		tables[schema][strings.ToLower(relation.name)] = table
	}
	session.tables, session.decimalColumns = tables, count
}

// decimalLookup is the table a FROM item names: in its schema when one is written, else where SQLite looks, the TEMP
// database first (the catalog's tables, catalog.go), then each schema in the order they were attached. catalog
// reports a table of the catalog or of main, which holds no decimal.
func (session *Session) decimalLookup(schema, name string) (table *decimalTable, catalog bool) {
	schema, name = strings.ToLower(schema), strings.ToLower(name)
	switch schema {
	case "temp", "main":
		return nil, true
	case "":
		if isCatalogTable(name) {
			return nil, true
		}
		for _, attached := range session.order {
			if found := session.tables[strings.ToLower(attached)][name]; found != nil {
				return found, false
			}
		}
		return nil, false
	}
	return session.tables[schema][name], false
}

// decimalStatement is parsed with every decimal column it computes with read as its decimal; parsed itself when it
// uses none.
func (session *Session) decimalStatement(parsed statement) (statement, error) {
	if session.decimalColumns == 0 || (parsed.kind != statementRead && parsed.kind != statementExplain) {
		return parsed, nil
	}
	rewrite := &decimalRewrite{session: session, text: parsed.text, tokens: parsed.tokens}
	text, changed, err := rewrite.run()
	if err != nil || !changed {
		return parsed, err
	}
	return parseStatement(text)
}

// decimalCore is one SELECT of the statement: its select list, its FROM items, the SELECT around it.
type decimalCore struct {
	parent *decimalCore
	// outer is a SELECT whose rows are the statement's rows.
	outer            bool
	listFrom, listTo int // the select list, tokens [listFrom, listTo)
	fromFrom, fromTo int // the FROM clause, tokens [fromFrom, fromTo); fromFrom 0 when there is none
	items            []*decimalItem
	opaque           bool // a join written in parentheses: its items are not read
	joined           bool // NATURAL or USING: SELECT * does not list every column of every item
	names            map[string]bool
	named            int // 0 not yet, 1 reading, 2 read
}

// decimalItem is one item of a FROM clause.
type decimalItem struct {
	name    string        // what the statement calls it: its alias, else its table's name, in lower case
	schema  string        // the schema written before a table's name, in lower case
	table   *decimalTable // a table or view of an attached schema
	derived *decimalCore  // a subquery's first SELECT
	cte     string        // a common table expression's name, in lower case
	values  bool          // a subquery of VALUES: its columns are column1, column2, …
	columns map[string]bool
	known   bool // columns is every column of a table function or of a catalog table
}

// decimalCTE is a common table expression: the columns its header lists, or its first SELECT.
type decimalCTE struct {
	columns map[string]bool
	body    *decimalCore
}

type decimalSplice struct {
	from, to int // bytes of the statement's text
	text     string
}

type decimalRewrite struct {
	session *Session
	text    string
	tokens  []token

	closes    []int          // the ")" closing each "(", -1 for any other token
	owner     []*decimalCore // the SELECT each token belongs to
	clause    []string       // the clause each token stands in, within its own parentheses
	cores     []*decimalCore
	frameCore map[int]*decimalCore // the first SELECT a "(" holds
	valuesAt  map[int]bool         // a "(" whose first word is VALUES
	ctes      map[string]*decimalCTE
	consumed  []bool // names that are not columns: tables, schemas, aliases, a CTE's header, USING's columns

	splices []decimalSplice
}

// tableFunctionColumns are the columns of the two table functions a statement may call (authorize.go).
var tableFunctionColumns = map[string]map[string]bool{
	"json_each": {"key": true, "value": true, "type": true, "atom": true, "id": true, "parent": true, "fullkey": true, "path": true, "json": true, "root": true},
}

func init() { tableFunctionColumns["json_tree"] = tableFunctionColumns["json_each"] }

type decimalFrame struct {
	open   int
	core   *decimalCore // the SELECT whose clauses stand in these parentheses
	owner  *decimalCore // the SELECT around them
	clause string
	cte    string // the CTE whose body these parentheses hold
}

func (frame *decimalFrame) current() *decimalCore {
	if frame.core != nil {
		return frame.core
	}
	return frame.owner
}

func (rewrite *decimalRewrite) run() (string, bool, error) {
	tokens := rewrite.tokens
	count := len(tokens)
	rewrite.closes = make([]int, count)
	rewrite.owner = make([]*decimalCore, count)
	rewrite.clause = make([]string, count)
	rewrite.consumed = make([]bool, count)
	rewrite.frameCore = map[int]*decimalCore{}
	rewrite.valuesAt = map[int]bool{}
	rewrite.ctes = map[string]*decimalCTE{}
	if !rewrite.walk() {
		return "", false, nil // unbalanced parentheses: SQLite says what is wrong
	}
	for _, core := range rewrite.cores {
		rewrite.readFrom(core)
	}
	for index := range tokens {
		if err := rewrite.column(index); err != nil {
			return "", false, err
		}
	}
	for _, core := range rewrite.cores {
		if err := rewrite.expandStars(core); err != nil {
			return "", false, err
		}
	}
	if len(rewrite.splices) == 0 {
		return "", false, nil
	}
	rewrite.nameItems()
	sort.SliceStable(rewrite.splices, func(left, right int) bool { return rewrite.splices[left].from < rewrite.splices[right].from })
	var built strings.Builder
	at := 0
	for _, splice := range rewrite.splices {
		if splice.from < at {
			continue // two rewrites of one span: the first stands
		}
		built.WriteString(rewrite.text[at:splice.from])
		built.WriteString(splice.text)
		at = splice.to
	}
	built.WriteString(rewrite.text[at:])
	return built.String(), true, nil
}

// walk finds every SELECT, the clause each token stands in and the CTEs a WITH names.
func (rewrite *decimalRewrite) walk() bool {
	tokens := rewrite.tokens
	frames := []*decimalFrame{{open: -1}}
	compound := false
	setClause := func(frame *decimalFrame, name string, at int) {
		if frame.core != nil {
			switch frame.clause {
			case "SELECT":
				frame.core.listTo = at
			case "FROM":
				frame.core.fromTo = at
			}
		}
		frame.clause = name
	}
	for index, candidate := range tokens {
		rewrite.closes[index] = -1
		top := frames[len(frames)-1]
		switch {
		case isPunctuation(candidate, "("):
			frame := &decimalFrame{open: index, owner: top.current()}
			if top.clause == "WITH" {
				rewrite.cteFrame(index, frame)
			}
			if index+1 < len(tokens) && tokens[index+1].word() == "VALUES" {
				rewrite.valuesAt[index] = true
			}
			rewrite.owner[index], rewrite.clause[index] = top.current(), top.clause
			frames = append(frames, frame)
			continue
		case isPunctuation(candidate, ")"):
			if len(frames) == 1 {
				return false
			}
			setClause(top, "", index)
			rewrite.closes[top.open] = index
			frames = frames[:len(frames)-1]
			top = frames[len(frames)-1]
			rewrite.owner[index], rewrite.clause[index] = top.current(), top.clause
			continue
		}
		switch word := candidate.word(); word {
		case "SELECT":
			setClause(top, "", index)
			core := &decimalCore{parent: top.owner, outer: top.open < 0, listFrom: index + 1}
			if next := index + 1; next < len(tokens) && (tokens[next].word() == "DISTINCT" || tokens[next].word() == "ALL") {
				core.listFrom = next + 1
			}
			if top.core == nil && top.open >= 0 {
				rewrite.frameCore[top.open] = core
				if top.cte != "" && rewrite.ctes[top.cte].body == nil {
					rewrite.ctes[top.cte].body = core
				}
			}
			top.core = core
			top.clause = "SELECT"
			rewrite.cores = append(rewrite.cores, core)
		case "FROM":
			if top.clause == "SELECT" && !distinctFrom(tokens, index) {
				setClause(top, "FROM", index)
				if top.core != nil {
					top.core.fromFrom = index + 1
				}
			}
		case "WHERE", "GROUP", "HAVING", "WINDOW", "ORDER", "LIMIT", "OFFSET", "PARTITION":
			setClause(top, word, index)
		case "UNION", "INTERSECT", "EXCEPT":
			setClause(top, "COMPOUND", index)
			if top.open < 0 {
				compound = true
			}
		case "WITH":
			setClause(top, "WITH", index)
		case "NATURAL", "USING":
			if top.clause == "FROM" && top.core != nil {
				top.core.joined = true
			}
		}
		rewrite.owner[index], rewrite.clause[index] = top.current(), top.clause
	}
	if len(frames) != 1 {
		return false
	}
	setClause(frames[0], "", len(tokens))
	if compound {
		// The rows of a UNION, INTERSECT or EXCEPT take their columns' kinds from its first SELECT alone: every SELECT
		// of it reads its decimals as values, so no column mixes stored integers with values.
		for _, core := range rewrite.cores {
			core.outer = false
		}
	}
	return true
}

// distinctFrom reports the FROM of IS [NOT] DISTINCT FROM, an operator, not a clause.
func distinctFrom(tokens []token, index int) bool {
	return index >= 2 && tokens[index-1].word() == "DISTINCT" &&
		(tokens[index-2].word() == "IS" || (tokens[index-2].word() == "NOT" && index >= 3 && tokens[index-3].word() == "IS"))
}

// cteFrame reads the "(" at index inside a WITH: the body of the CTE named before AS [NOT] MATERIALIZED, or the
// column list after a CTE's name.
func (rewrite *decimalRewrite) cteFrame(index int, frame *decimalFrame) {
	tokens := rewrite.tokens
	before := index - 1
	if before >= 0 && tokens[before].word() == "MATERIALIZED" {
		before--
		if before >= 0 && tokens[before].word() == "NOT" {
			before--
		}
	}
	if before >= 0 && tokens[before].word() == "AS" {
		name := before - 1
		if name >= 0 && isPunctuation(tokens[name], ")") {
			name = matchingOpen(tokens, name) - 1
		}
		if name >= 0 && isIdentifier(tokens[name]) {
			key := strings.ToLower(unquoted(tokens[name]))
			if rewrite.ctes[key] == nil {
				rewrite.ctes[key] = &decimalCTE{}
			}
			rewrite.consumed[name] = true
			frame.cte = key
		}
		return
	}
	if index >= 1 && isIdentifier(tokens[index-1]) {
		key := strings.ToLower(unquoted(tokens[index-1]))
		cte := &decimalCTE{columns: map[string]bool{}}
		rewrite.ctes[key] = cte
		rewrite.consumed[index-1] = true
		for inner := index + 1; inner < len(tokens) && !isPunctuation(tokens[inner], ")"); inner++ {
			if isIdentifier(tokens[inner]) {
				cte.columns[strings.ToLower(unquoted(tokens[inner]))] = true
				rewrite.consumed[inner] = true
			}
		}
	}
}

// fromStops are the words that end a FROM item's name: none of them is an alias.
var fromStops = map[string]bool{
	"ON": true, "USING": true, "JOIN": true, "NATURAL": true, "LEFT": true, "RIGHT": true, "FULL": true, "INNER": true,
	"CROSS": true, "OUTER": true, "WHERE": true, "GROUP": true, "HAVING": true, "WINDOW": true, "ORDER": true,
	"LIMIT": true, "OFFSET": true, "UNION": true, "INTERSECT": true, "EXCEPT": true, "INDEXED": true, "NOT": true,
	"AS": true, "RETURNING": true,
}

// readFrom reads the items of core's FROM clause.
func (rewrite *decimalRewrite) readFrom(core *decimalCore) {
	if core.fromFrom == 0 {
		return
	}
	tokens := rewrite.tokens
	expect := true
	for index := core.fromFrom; index < core.fromTo; {
		candidate := tokens[index]
		if !expect {
			switch word := candidate.word(); {
			case isPunctuation(candidate, "("):
				if index > 0 && tokens[index-1].word() == "USING" {
					for inner := index + 1; inner < rewrite.closes[index]; inner++ {
						rewrite.consumed[inner] = true
					}
				}
				index = max(rewrite.closes[index], index) + 1
				continue
			case isPunctuation(candidate, ","), word == "JOIN":
				expect = true
			case word == "INDEXED" && index+2 < core.fromTo && tokens[index+1].word() == "BY":
				rewrite.consumed[index+2] = true
				index += 3
				continue
			}
			index++
			continue
		}
		expect = false
		item := &decimalItem{}
		switch {
		case isPunctuation(candidate, "("):
			close := rewrite.closes[index]
			if inner := rewrite.frameCore[index]; inner != nil {
				item.derived = inner
			} else if rewrite.valuesAt[index] {
				item.values = true
			} else {
				core.opaque = true
			}
			index = close + 1
		case isIdentifier(candidate):
			rewrite.consumed[index] = true
			name, schema := unquoted(candidate), ""
			index++
			if index+1 < core.fromTo && isPunctuation(tokens[index], ".") && isIdentifier(tokens[index+1]) {
				schema, name = name, unquoted(tokens[index+1])
				rewrite.consumed[index+1] = true
				index += 2
			}
			item.name, item.schema = strings.ToLower(name), strings.ToLower(schema)
			switch {
			case index < core.fromTo && isPunctuation(tokens[index], "("):
				item.columns, item.known = tableFunctionColumns[item.name]
				index = rewrite.closes[index] + 1
			case schema == "" && rewrite.ctes[item.name] != nil:
				item.cte = item.name
			default:
				// A catalog table's columns are not read: none of them is a decimal.
				item.table, _ = rewrite.session.decimalLookup(schema, name)
			}
		default:
			core.opaque = true
			return
		}
		if index < core.fromTo && tokens[index].word() == "AS" {
			if index+1 < core.fromTo && isIdentifier(tokens[index+1]) {
				item.name = strings.ToLower(unquoted(tokens[index+1]))
				rewrite.consumed[index+1] = true
			}
			index += 2
		} else if index < core.fromTo && isIdentifier(tokens[index]) && !fromStops[tokens[index].word()] {
			item.name = strings.ToLower(unquoted(tokens[index]))
			rewrite.consumed[index] = true
			index++
		}
		core.items = append(core.items, item)
	}
}

// columnsOf is every column an item gives, by lower-case name, and whether that is known.
func (rewrite *decimalRewrite) columnsOf(item *decimalItem) (map[string]bool, bool) {
	switch {
	case item.table != nil:
		return item.table.known, true
	case item.derived != nil:
		return rewrite.namesOf(item.derived)
	case item.cte != "":
		cte := rewrite.ctes[item.cte]
		if cte.columns != nil {
			return cte.columns, true
		}
		if cte.body != nil {
			return rewrite.namesOf(cte.body)
		}
	case item.known:
		return item.columns, true
	}
	return nil, false
}

// namesOf is the name of each column a SELECT gives (what an outer SELECT reads of it as a subquery), and whether
// that is known.
func (rewrite *decimalRewrite) namesOf(core *decimalCore) (map[string]bool, bool) {
	switch core.named {
	case 1:
		return nil, false // a CTE that reads itself
	case 2:
		return core.names, core.names != nil
	}
	core.named = 1
	names := map[string]bool{}
	known := true
	rewrite.eachItem(core, func(from, to int) {
		if !known {
			return
		}
		tokens := rewrite.tokens
		switch {
		case to-from == 1 && isPunctuation(tokens[from], "*"):
			if core.opaque {
				known = false
				return
			}
			for _, item := range core.items {
				columns, ok := rewrite.columnsOf(item)
				if !ok {
					known = false
					return
				}
				for name := range columns {
					names[name] = true
				}
			}
		case to-from == 3 && isPunctuation(tokens[to-1], "*") && isPunctuation(tokens[from+1], "."):
			item := core.item(strings.ToLower(unquoted(tokens[from])))
			if item == nil {
				known = false
				return
			}
			columns, ok := rewrite.columnsOf(item)
			if !ok {
				known = false
				return
			}
			for name := range columns {
				names[name] = true
			}
		default:
			if name := itemName(tokens, from, to); name != "" {
				names[strings.ToLower(name)] = true
			}
		}
	})
	if !known {
		names = nil
	}
	core.names, core.named = names, 2
	return names, known
}

// eachItem calls visit with the tokens of each item of core's select list.
func (rewrite *decimalRewrite) eachItem(core *decimalCore, visit func(from, to int)) {
	start := core.listFrom
	for index := core.listFrom; index <= core.listTo; index++ {
		if index == core.listTo || isPunctuation(rewrite.tokens[index], ",") {
			if start < index {
				visit(start, index)
			}
			start = index + 1
			continue
		}
		if isPunctuation(rewrite.tokens[index], "(") && rewrite.closes[index] > index {
			index = rewrite.closes[index]
		}
	}
}

// itemName is the name SQLite gives the select-list item of tokens [from, to): its alias, else the column it names.
func itemName(tokens []token, from, to int) string {
	last := tokens[to-1]
	if !isIdentifier(last) {
		return ""
	}
	if to-from >= 2 {
		before := tokens[to-2]
		if before.word() == "AS" || endsOperand(before) {
			return unquoted(last)
		}
		if !isPunctuation(before, ".") {
			return ""
		}
	}
	for index := from; index < to; index++ {
		if !isIdentifier(tokens[index]) && !isPunctuation(tokens[index], ".") {
			return ""
		}
	}
	return unquoted(last)
}

func (core *decimalCore) item(name string) *decimalItem {
	for _, item := range core.items {
		if item.name == name {
			return item
		}
	}
	return nil
}

// operandEnds are the keywords that end an operand: an identifier after one is an alias.
var operandEnds = map[string]bool{"END": true, "NULL": true, "TRUE": true, "FALSE": true, "CURRENT_DATE": true,
	"CURRENT_TIME": true, "CURRENT_TIMESTAMP": true}

// endsOperand reports a token after which an identifier is an alias: a name, a literal, a parameter, a ")".
func endsOperand(candidate token) bool {
	switch candidate.kind {
	case tokenQuoted, tokenNumber, tokenString, tokenVariable:
		return true
	case tokenWord:
		return operandEnds[candidate.word()] || !sqliteKeywords[candidate.word()]
	}
	return isPunctuation(candidate, ")")
}

// resolution is what a column reference names.
type resolution struct {
	decimal bool
	scale   int
}

// resolve finds the column a reference names from core outward: qualifier is the table or alias written before it
// ("" for none), schema the schema written before that.
func (rewrite *decimalRewrite) resolve(core *decimalCore, schema, qualifier, column string) resolution {
	schema, qualifier, column = strings.ToLower(schema), strings.ToLower(qualifier), strings.ToLower(column)
	for ; core != nil; core = core.parent {
		if qualifier != "" {
			for _, item := range core.items {
				if item.name != qualifier || (schema != "" && (item.table == nil || strings.ToLower(item.table.schema) != schema)) {
					continue
				}
				if item.table != nil {
					if scale, decimal := item.table.scales[column]; decimal {
						return resolution{decimal: true, scale: scale}
					}
				}
				return resolution{}
			}
			continue
		}
		var found *decimalItem
		providers, unknown := 0, core.opaque
		for _, item := range core.items {
			columns, known := rewrite.columnsOf(item)
			if !known {
				unknown = true
				continue
			}
			if columns[column] {
				providers++
				found = item
			}
		}
		if providers == 1 && !unknown && found.table != nil {
			if scale, decimal := found.table.scales[column]; decimal {
				return resolution{decimal: true, scale: scale}
			}
		}
		if providers > 0 || unknown {
			return resolution{}
		}
	}
	return resolution{}
}

// column rewrites the reference to a decimal column whose column name is the token at index, if it is one.
func (rewrite *decimalRewrite) column(index int) error {
	tokens := rewrite.tokens
	candidate := tokens[index]
	core := rewrite.owner[index]
	if core == nil || rewrite.consumed[index] || !isIdentifier(candidate) {
		return nil
	}
	next := index + 1
	if next < len(tokens) && (isPunctuation(tokens[next], "(") || isPunctuation(tokens[next], ".")) {
		return nil // a call, or the table before a column
	}
	start := qualifiedStart(tokens, index)
	var schema, qualifier string
	switch index - start {
	case 0:
		if index > 0 {
			before := tokens[index-1]
			switch before.word() {
			case "AS", "COLLATE", "OVER", "NULLS":
				return nil
			}
			if endsOperand(before) {
				return nil // an alias
			}
		}
	case 2:
		qualifier = unquoted(tokens[start])
	case 4:
		schema, qualifier = unquoted(tokens[start]), unquoted(tokens[start+2])
	default:
		return nil
	}
	found := rewrite.resolve(core, schema, qualifier, unquoted(candidate))
	if !found.decimal {
		return nil
	}
	clause := rewrite.clause[index]
	switch {
	case clause == "SELECT" && core.outer && rewrite.bareItem(start, next):
		return nil // the result writes it as its decimal
	case (clause == "ORDER" || clause == "GROUP" || clause == "PARTITION") && rewrite.bareTerm(start, next):
		return nil
	case rewrite.nullTest(next):
		return nil
	}
	if from, to, name, ok := rewrite.aggregate(start, next); ok {
		if name != "COUNT" {
			rewrite.wrap(from, to, found.scale) // summed, or its least or greatest, in stored integers: exact
		}
		return nil
	}
	if rewrite.compared(start, next, found.scale) {
		return nil
	}
	rewrite.wrap(start, next, found.scale)
	return nil
}

// wrap reads tokens [from, to), one decimal column or an aggregate of one, as its decimal.
func (rewrite *decimalRewrite) wrap(from, to, scale int) {
	reference := source(rewrite.text, rewrite.tokens, from, to)
	rewrite.splices = append(rewrite.splices, decimalSplice{from: rewrite.tokens[from].start, to: rewrite.tokens[to-1].end,
		text: "kitwork_decimal(" + reference + ", " + strconv.Itoa(scale) + ")"})
}

// aggregate reports tokens [from, to) as the one argument of count, sum, total, avg, min or max (DISTINCT allowed):
// the call's tokens with its FILTER and OVER, and its name.
func (rewrite *decimalRewrite) aggregate(from, to int) (int, int, string, bool) {
	tokens := rewrite.tokens
	open := from - 1
	if open >= 0 && tokens[open].word() == "DISTINCT" {
		open--
	}
	if open < 1 || !isPunctuation(tokens[open], "(") || to >= len(tokens) || !isPunctuation(tokens[to], ")") ||
		tokens[open-1].kind != tokenWord {
		return 0, 0, "", false
	}
	name := tokens[open-1].word()
	switch name {
	case "COUNT", "SUM", "TOTAL", "AVG", "MIN", "MAX":
	default:
		return 0, 0, "", false
	}
	if open >= 2 && isPunctuation(tokens[open-2], ".") {
		return 0, 0, "", false
	}
	end := to + 1
	if end+1 < len(tokens) && tokens[end].word() == "FILTER" && isPunctuation(tokens[end+1], "(") && rewrite.closes[end+1] > 0 {
		end = rewrite.closes[end+1] + 1
	}
	if end+1 < len(tokens) && tokens[end].word() == "OVER" {
		if isPunctuation(tokens[end+1], "(") && rewrite.closes[end+1] > 0 {
			end = rewrite.closes[end+1] + 1
		} else {
			end += 2
		}
	}
	return open - 1, end, name, true
}

// nameItems gives each select-list item the rewrite changed the name SQLite gave it before: the column's name for a
// column alone, its text for an expression. An item with an alias keeps it.
func (rewrite *decimalRewrite) nameItems() {
	tokens := rewrite.tokens
	changed := rewrite.splices
	for _, core := range rewrite.cores {
		rewrite.eachItem(core, func(from, to int) {
			if isPunctuation(tokens[to-1], "*") || rewrite.hasAlias(from, to) {
				return
			}
			start, end := tokens[from].start, tokens[to-1].end
			touched := false
			for _, splice := range changed {
				if splice.from >= start && splice.to <= end {
					touched = true
					break
				}
			}
			if !touched {
				return
			}
			name := itemName(tokens, from, to)
			if name == "" {
				name = rewrite.text[start:end]
			}
			rewrite.splices = append(rewrite.splices, decimalSplice{from: end, to: end, text: " AS " + quoteIdentifier(name)})
		})
	}
}

// hasAlias reports a select-list item of tokens [from, to) that ends in an alias: AS name, or a name after an operand.
func (rewrite *decimalRewrite) hasAlias(from, to int) bool {
	tokens := rewrite.tokens
	if to-from < 2 || !isIdentifier(tokens[to-1]) {
		return false
	}
	before := tokens[to-2]
	return before.word() == "AS" || (!isPunctuation(before, ".") && endsOperand(before))
}

// bareItem reports tokens [from, to) as a whole item of a select list.
func (rewrite *decimalRewrite) bareItem(from, to int) bool {
	tokens := rewrite.tokens
	if from == 0 {
		return false
	}
	switch before := tokens[from-1]; {
	case before.word() == "SELECT", before.word() == "DISTINCT", before.word() == "ALL", isPunctuation(before, ","):
	default:
		return false
	}
	if to >= len(tokens) {
		return true
	}
	switch after := tokens[to]; {
	case isPunctuation(after, ","), isPunctuation(after, ")"), after.word() == "AS":
		return true
	case after.kind == tokenWord && sqliteKeywords[after.word()]:
		return after.word() == "FROM" || after.word() == "UNION" || after.word() == "INTERSECT" || after.word() == "EXCEPT" ||
			after.word() == "ORDER" || after.word() == "LIMIT" || after.word() == "WHERE" || after.word() == "GROUP" ||
			after.word() == "HAVING" || after.word() == "WINDOW"
	case isIdentifier(after):
		return true // its alias
	}
	return false
}

// bareTerm reports tokens [from, to) as a whole term of ORDER BY, GROUP BY or PARTITION BY.
func (rewrite *decimalRewrite) bareTerm(from, to int) bool {
	tokens := rewrite.tokens
	if from == 0 || !(tokens[from-1].word() == "BY" || isPunctuation(tokens[from-1], ",")) {
		return false
	}
	if to >= len(tokens) {
		return true
	}
	after := tokens[to]
	switch after.word() {
	case "ASC", "DESC", "NULLS", "COLLATE", "LIMIT", "OFFSET", "HAVING", "WINDOW", "ORDER", "UNION", "INTERSECT",
		"EXCEPT", "ROWS", "RANGE", "GROUPS":
		return true
	}
	return isPunctuation(after, ",") || isPunctuation(after, ")")
}

// nullTest reports IS NULL, IS NOT NULL, ISNULL or NOTNULL at index: true or false whatever the unit.
func (rewrite *decimalRewrite) nullTest(index int) bool {
	tokens := rewrite.tokens
	if index >= len(tokens) {
		return false
	}
	switch tokens[index].word() {
	case "ISNULL", "NOTNULL":
		return true
	case "IS":
		index++
		if index < len(tokens) && tokens[index].word() == "NOT" {
			index++
		}
		return index < len(tokens) && tokens[index].word() == "NULL"
	case "NOT":
		return index+1 < len(tokens) && tokens[index+1].word() == "NULL"
	}
	return false
}

// boundaryBefore reports index as the start of a whole operand of a comparison: nothing that binds tighter stands
// before it.
func (rewrite *decimalRewrite) boundaryBefore(index int) bool {
	if index == 0 {
		return true
	}
	before := rewrite.tokens[index-1]
	switch before.word() {
	case "WHERE", "AND", "OR", "NOT", "ON", "HAVING", "WHEN", "THEN", "ELSE", "SELECT", "DISTINCT", "ALL":
		return true
	}
	return isPunctuation(before, "(") || isPunctuation(before, ",")
}

// boundaryAfter reports index as the end of a whole operand of a comparison.
func (rewrite *decimalRewrite) boundaryAfter(index int) bool {
	tokens := rewrite.tokens
	if index >= len(tokens) {
		return true
	}
	after := tokens[index]
	switch after.word() {
	case "AND", "OR", "THEN", "ELSE", "END", "WHEN", "GROUP", "ORDER", "LIMIT", "OFFSET", "HAVING", "WINDOW", "UNION",
		"INTERSECT", "EXCEPT", "AS", "FROM":
		return true
	}
	return isPunctuation(after, ")") || isPunctuation(after, ",")
}

// comparison is the comparison operator at index (one or two adjacent characters) and the index after it.
func (rewrite *decimalRewrite) comparison(index int) (int, bool) {
	tokens := rewrite.tokens
	if index >= len(tokens) || tokens[index].kind != tokenPunctuation {
		return 0, false
	}
	first := tokens[index].text
	if index+1 < len(tokens) && tokens[index+1].kind == tokenPunctuation && tokens[index+1].start == tokens[index].end {
		switch first + tokens[index+1].text {
		case "<=", ">=", "<>", "!=", "==":
			return index + 2, true
		case "<<", ">>", "->":
			return 0, false
		}
	}
	switch first {
	case "<", ">", "=":
		return index + 1, true
	}
	return 0, false
}

// comparisonBefore is where the comparison operator that ends just before end starts.
func (rewrite *decimalRewrite) comparisonBefore(end int) (int, bool) {
	tokens := rewrite.tokens
	last := end - 1
	if last < 0 || tokens[last].kind != tokenPunctuation {
		return 0, false
	}
	if last >= 1 && tokens[last-1].kind == tokenPunctuation && tokens[last-1].end == tokens[last].start {
		switch tokens[last-1].text + tokens[last].text {
		case "<=", ">=", "<>", "!=", "==":
			if last >= 2 && tokens[last-2].kind == tokenPunctuation && tokens[last-2].end == tokens[last-1].start {
				return 0, false
			}
			return last - 1, true
		default:
			return 0, false
		}
	}
	switch tokens[last].text {
	case "<", ">", "=":
		return last, true
	}
	return 0, false
}

// literal is the literal or parameter at index (a number with its sign, a string that holds a number, a parameter)
// as the stored integer of a decimal of scale, and the index after it.
func (rewrite *decimalRewrite) literal(index, scale int) (decimalSplice, int, bool) {
	tokens := rewrite.tokens
	if index >= len(tokens) {
		return decimalSplice{}, 0, false
	}
	start := index
	sign := ""
	if isPunctuation(tokens[index], "-") || isPunctuation(tokens[index], "+") {
		sign = tokens[index].text
		index++
		if index >= len(tokens) || tokens[index].kind != tokenNumber {
			return decimalSplice{}, 0, false
		}
	}
	candidate := tokens[index]
	splice := decimalSplice{from: tokens[start].start, to: candidate.end}
	switch candidate.kind {
	case tokenNumber:
		scaled, ok := scaledDecimal(sign+candidate.text, scale)
		if !ok {
			return decimalSplice{}, 0, false
		}
		splice.text = scaled
	case tokenString:
		if strings.HasPrefix(candidate.text, "x") || strings.HasPrefix(candidate.text, "X") {
			return decimalSplice{}, 0, false
		}
		scaled, ok := scaledDecimal(strings.TrimSpace(unescapeString(candidate.text)), scale)
		if !ok {
			return decimalSplice{}, 0, false
		}
		splice.text = scaled
	case tokenVariable:
		splice.text = "kitwork_decimal_input(" + candidate.text + ", " + strconv.Itoa(scale) + ")"
	default:
		return decimalSplice{}, 0, false
	}
	return splice, index + 1, true
}

// literalBefore is the literal or parameter that ends just before end, as literal reads it.
func (rewrite *decimalRewrite) literalBefore(end, scale int) (decimalSplice, int, bool) {
	tokens := rewrite.tokens
	last := end - 1
	if last < 0 {
		return decimalSplice{}, 0, false
	}
	start := last
	if tokens[last].kind == tokenNumber && last >= 1 && (isPunctuation(tokens[last-1], "-") || isPunctuation(tokens[last-1], "+")) &&
		rewrite.boundaryBefore(last-1) {
		start = last - 1
	}
	splice, after, ok := rewrite.literal(start, scale)
	if !ok || after != end || !rewrite.boundaryBefore(start) {
		return decimalSplice{}, 0, false
	}
	return splice, start, true
}

// compared reads a comparison of the decimal column of tokens [from, to) with literals: the column compared with a
// literal either way round, BETWEEN two, IN a list of them. Each literal becomes the stored integer, and the column
// stays as it is.
func (rewrite *decimalRewrite) compared(from, to, scale int) bool {
	tokens := rewrite.tokens
	if !rewrite.boundaryBefore(from) && from > 0 {
		// literal operator column
		operator, ok := rewrite.comparisonBefore(from)
		if !ok || !rewrite.boundaryAfter(to) {
			return false
		}
		splice, _, ok := rewrite.literalBefore(operator, scale)
		if !ok {
			return false
		}
		rewrite.splices = append(rewrite.splices, splice)
		return true
	}
	// column operator literal
	if after, ok := rewrite.comparison(to); ok {
		splice, end, ok := rewrite.literal(after, scale)
		if !ok || !rewrite.boundaryAfter(end) {
			return false
		}
		rewrite.splices = append(rewrite.splices, splice)
		return true
	}
	index := to
	if index < len(tokens) && tokens[index].word() == "NOT" {
		index++
	}
	if index >= len(tokens) {
		return false
	}
	switch tokens[index].word() {
	case "BETWEEN":
		low, end, ok := rewrite.literal(index+1, scale)
		if !ok || end >= len(tokens) || tokens[end].word() != "AND" {
			return false
		}
		high, end, ok := rewrite.literal(end+1, scale)
		if !ok || !rewrite.boundaryAfter(end) {
			return false
		}
		rewrite.splices = append(rewrite.splices, low, high)
		return true
	case "IN":
		open := index + 1
		if open >= len(tokens) || !isPunctuation(tokens[open], "(") || rewrite.closes[open] < 0 {
			return false
		}
		close := rewrite.closes[open]
		var listed []decimalSplice
		for at := open + 1; at < close; {
			splice, end, ok := rewrite.literal(at, scale)
			if !ok || (end != close && !isPunctuation(tokens[end], ",")) {
				return false
			}
			listed = append(listed, splice)
			at = end + 1
		}
		if !rewrite.boundaryAfter(close + 1) {
			return false
		}
		rewrite.splices = append(rewrite.splices, listed...)
		return true
	}
	return false
}

// expandStars writes out a subquery's SELECT * (or name.*) over a table with a decimal column, so an outer SELECT
// reads each decimal as its decimal: the statement's own SELECT * is left, the result writes its decimals.
func (rewrite *decimalRewrite) expandStars(core *decimalCore) error {
	if core.outer {
		return nil
	}
	tokens := rewrite.tokens
	var failure error
	rewrite.eachItem(core, func(from, to int) {
		if failure != nil {
			return
		}
		var items []*decimalItem
		switch {
		case to-from == 1 && isPunctuation(tokens[from], "*"):
			items = core.items
		case to-from == 3 && isPunctuation(tokens[to-1], "*") && isPunctuation(tokens[from+1], ".") && isIdentifier(tokens[from]):
			if item := core.item(strings.ToLower(unquoted(tokens[from]))); item != nil {
				items = []*decimalItem{item}
			}
		default:
			return
		}
		decimal := false
		for _, item := range items {
			if item.table != nil && len(item.table.scales) > 0 {
				decimal = true
			}
		}
		if !decimal {
			return
		}
		if core.opaque || (len(items) > 1 && core.joined) {
			failure = pgwire.NewError("0A000", "SELECT * inside a subquery over a decimal column, with NATURAL, USING or a "+
				"parenthesized join: list the columns, so each decimal reads as its decimal")
			return
		}
		var written []string
		for _, item := range items {
			if item.table == nil {
				failure = pgwire.NewError("0A000", "SELECT * inside a subquery over a decimal column and a subquery, a "+
					"CTE or a function: list the columns, so each decimal reads as its decimal")
				return
			}
			for _, name := range item.table.columns {
				reference := quoteIdentifier(item.name) + "." + quoteIdentifier(name)
				if scale, ok := item.table.scales[strings.ToLower(name)]; ok {
					reference = "kitwork_decimal(" + reference + ", " + strconv.Itoa(scale) + ") AS " + quoteIdentifier(name)
				}
				written = append(written, reference)
			}
		}
		rewrite.splices = append(rewrite.splices, decimalSplice{from: tokens[from].start, to: tokens[to-1].end,
			text: strings.Join(written, ", ")})
	})
	return failure
}

// unescapeString is what a '…' literal holds.
func unescapeString(text string) string {
	if len(text) < 2 {
		return text
	}
	return strings.ReplaceAll(text[1:len(text)-1], "''", "'")
}

// scaledDecimal is the number text writes times 10^scale, written exactly: 1000 at scale 2 is 100000, 4.5 is 450,
// 1.005 is 100.5. False when text is not a decimal number.
func scaledDecimal(text string, scale int) (string, bool) {
	negative := false
	switch {
	case strings.HasPrefix(text, "-"):
		negative, text = true, text[1:]
	case strings.HasPrefix(text, "+"):
		text = text[1:]
	}
	exponent := 0
	if at := strings.IndexAny(text, "eE"); at >= 0 {
		value, err := strconv.Atoi(text[at+1:])
		if err != nil || value > 400 || value < -400 {
			return "", false
		}
		exponent, text = value, text[:at]
	}
	integer, fraction, _ := strings.Cut(text, ".")
	if integer == "" && fraction == "" {
		return "", false
	}
	for _, character := range integer + fraction {
		if character < '0' || character > '9' {
			return "", false
		}
	}
	digits := integer + fraction
	shift := exponent + scale - len(fraction) // the value is digits × 10^shift
	if shift >= 0 {
		digits += strings.Repeat("0", shift)
	} else {
		if len(digits) <= -shift {
			digits = strings.Repeat("0", -shift-len(digits)+1) + digits
		}
		point := len(digits) + shift
		whole, rest := digits[:point], strings.TrimRight(digits[point:], "0")
		digits = whole
		if rest != "" {
			digits += "." + rest
		}
	}
	whole, rest, fractional := strings.Cut(digits, ".")
	whole = strings.TrimLeft(whole, "0")
	if whole == "" {
		whole = "0"
	}
	digits = whole
	if fractional {
		digits += "." + rest
	}
	if negative && digits != "0" {
		digits = "-" + digits
	}
	return digits, true
}

// registerDecimalFunctions registers what the rewrite calls: kitwork_decimal(stored, scale) is a stored decimal as its
// value, kitwork_decimal_input(value, scale) a parameter as the stored integer it compares with.
func registerDecimalFunctions(scalar func(string, int32, func([]driver.Value) (driver.Value, error))) {
	scalar("kitwork_decimal", 2, func(arguments []driver.Value) (driver.Value, error) {
		scale, _ := integerValue(arguments[1])
		switch value := arguments[0].(type) {
		case nil:
			return nil, nil
		case int64:
			return float64(value) / math.Pow10(int(scale)), nil
		case float64:
			return value / math.Pow10(int(scale)), nil
		default:
			// A decimal an older file kept as its text holds its own digits.
			number, err := strconv.ParseFloat(strings.TrimSpace(textValue(value)), 64)
			if err != nil {
				return nil, nil
			}
			return number, nil
		}
	})
	scalar("kitwork_decimal_input", 2, func(arguments []driver.Value) (driver.Value, error) {
		scale, _ := integerValue(arguments[1])
		var text string
		switch value := arguments[0].(type) {
		case nil:
			return nil, nil
		case int64:
			text = strconv.FormatInt(value, 10)
		case float64:
			text = strconv.FormatFloat(value, 'g', -1, 64)
		default:
			text = strings.TrimSpace(textValue(value))
		}
		scaled, ok := scaledDecimal(text, int(scale))
		if !ok {
			return nil, fmt.Errorf("invalid input syntax for type numeric: %q", text)
		}
		if whole, err := strconv.ParseInt(scaled, 10, 64); err == nil {
			return whole, nil
		}
		number, err := strconv.ParseFloat(scaled, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid input syntax for type numeric: %q", text)
		}
		return number, nil
	})
}

// sqliteKeywords are SQLite's keywords (sqlite3_keyword_name): an identifier after one of them is not an alias.
var sqliteKeywords = func() map[string]bool {
	words := map[string]bool{}
	for _, word := range strings.Fields(`ABORT ACTION ADD AFTER ALL ALTER ALWAYS ANALYZE AND AS ASC ATTACH AUTOINCREMENT
		BEFORE BEGIN BETWEEN BY CASCADE CASE CAST CHECK COLLATE COLUMN COMMIT CONFLICT CONSTRAINT CREATE CROSS CURRENT
		CURRENT_DATE CURRENT_TIME CURRENT_TIMESTAMP DATABASE DEFAULT DEFERRABLE DEFERRED DELETE DESC DETACH DISTINCT DO
		DROP EACH ELSE END ESCAPE EXCEPT EXCLUDE EXCLUSIVE EXISTS EXPLAIN FAIL FILTER FIRST FOLLOWING FOR FOREIGN FROM
		FULL GENERATED GLOB GROUP GROUPS HAVING IF IGNORE IMMEDIATE IN INDEX INDEXED INITIALLY INNER INSERT INSTEAD
		INTERSECT INTO IS ISNULL JOIN KEY LAST LEFT LIKE LIMIT MATCH MATERIALIZED NATURAL NO NOT NOTHING NOTNULL NULL
		NULLS OF OFFSET ON OR ORDER OTHERS OUTER OVER PARTITION PLAN PRAGMA PRECEDING PRIMARY QUERY RAISE RANGE RECURSIVE
		REFERENCES REGEXP REINDEX RELEASE RENAME REPLACE RESTRICT RETURNING RIGHT ROLLBACK ROW ROWS SAVEPOINT SELECT SET
		TABLE TEMP TEMPORARY THEN TIES TO TRANSACTION TRIGGER UNBOUNDED UNION UNIQUE UPDATE USING VACUUM VALUES VIEW
		VIRTUAL WHEN WHERE WINDOW WITH WITHOUT TRUE FALSE`) {
		words[word] = true
	}
	return words
}()
