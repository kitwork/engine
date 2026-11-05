package analytics

import (
	"cmp"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/kitwork/engine/kitdb/columnar"
)

// ErrOverflow is sum() of integers past 64 bits, which SQLite refuses with "integer overflow" too.
var ErrOverflow = errors.New("analytics: integer overflow")

// ErrNotAnswered means a query the copy does not answer as SQLite would (a comparison of a text column with a number,
// a NULL in an IN list): the rows answer it.
var ErrNotAnswered = errors.New("analytics: the copy does not answer this query")

// Operators of a Condition.
const (
	OpAnd     = "and"
	OpOr      = "or"
	OpNot     = "not"
	OpEqual   = "="
	OpUnequal = "!="
	OpLess    = "<"
	OpAtMost  = "<="
	OpMore    = ">"
	OpAtLeast = ">="
	OpIn      = "in"
	OpNull    = "null"    // IS NULL
	OpNotNull = "notnull" // IS NOT NULL
)

// Condition is a WHERE of a query, read as SQLite reads it: a comparison of a column with a value, IN, IS NULL, and
// AND, OR, NOT over them, three-valued (a comparison with NULL is neither true nor false; a row is kept when the
// whole is true).
type Condition struct {
	Op     string
	Column string
	Value  any   // int64, float64 or string
	Values []any // OpIn's list
	Args   []*Condition
}

// Aggregate is one aggregate of a query: count (Column "" is count(*)), sum, avg, min or max of a column.
type Aggregate struct {
	Function string
	Column   string
}

// Query is what the copy answers: the rows its Filter keeps, grouped by Group, each group's Aggregates. Without
// Group it answers one row.
type Query struct {
	Filter     *Condition
	Group      []string
	Aggregates []Aggregate
}

// Result is a query's rows: the group's values then the aggregates', a group a row in ascending order of its values
// (NULL first), as SQLite's GROUP BY gives them.
type Result struct {
	Rows [][]any
}

// columnsOf are the columns query reads, each once, in the order first named.
func (query *Query) columnsOf() []string {
	var columns []string
	seen := map[string]bool{}
	add := func(name string) {
		if name != "" && !seen[name] {
			seen[name] = true
			columns = append(columns, name)
		}
	}
	var walk func(*Condition)
	walk = func(condition *Condition) {
		if condition == nil {
			return
		}
		add(condition.Column)
		for _, argument := range condition.Args {
			walk(argument)
		}
	}
	walk(query.Filter)
	for _, name := range query.Group {
		add(name)
	}
	for _, aggregate := range query.Aggregates {
		add(aggregate.Column)
	}
	return columns
}

// check refuses what the copy would answer otherwise than SQLite: a column it does not hold, a function it does
// not know, a comparison of a column with a value of another kind.
func (v *View) check(query *Query) error {
	manifest := v.table.copy.Manifest
	kindOfColumn := func(name string) (columnar.Kind, error) {
		position := manifest.position(name)
		if position < 0 {
			return 0, fmt.Errorf("%w: the copy of %q has no column %q", ErrNotAnswered, manifest.Table, name)
		}
		return manifest.Columns[position].Kind, nil
	}
	var walk func(*Condition) error
	walk = func(condition *Condition) error {
		switch condition.Op {
		case OpAnd, OpOr, OpNot:
			if len(condition.Args) == 0 || condition.Op == OpNot && len(condition.Args) != 1 {
				return fmt.Errorf("%w: %s of %d conditions", ErrNotAnswered, condition.Op, len(condition.Args))
			}
			for _, argument := range condition.Args {
				if err := walk(argument); err != nil {
					return err
				}
			}
			return nil
		case OpNull, OpNotNull:
			_, err := kindOfColumn(condition.Column)
			return err
		case OpEqual, OpUnequal, OpLess, OpAtMost, OpMore, OpAtLeast, OpIn:
			kind, err := kindOfColumn(condition.Column)
			if err != nil {
				return err
			}
			values := condition.Values
			if condition.Op != OpIn {
				values = []any{condition.Value}
			}
			if len(values) == 0 {
				return fmt.Errorf("%w: IN of no value", ErrNotAnswered)
			}
			for _, value := range values {
				if !comparable(kind, value) {
					return fmt.Errorf("%w: %s compared with %T", ErrNotAnswered, condition.Column, value)
				}
			}
			return nil
		}
		return fmt.Errorf("%w: operator %q", ErrNotAnswered, condition.Op)
	}
	if query.Filter != nil {
		if err := walk(query.Filter); err != nil {
			return err
		}
	}
	for _, name := range query.Group {
		if _, err := kindOfColumn(name); err != nil {
			return err
		}
	}
	if len(query.Aggregates) == 0 && len(query.Group) == 0 {
		return fmt.Errorf("%w: a query of no aggregate", ErrNotAnswered)
	}
	for _, aggregate := range query.Aggregates {
		switch aggregate.Function {
		case "count":
			if aggregate.Column == "" {
				continue
			}
		case "sum", "avg", "min", "max":
			if aggregate.Column == "" {
				return fmt.Errorf("%w: %s of no column", ErrNotAnswered, aggregate.Function)
			}
		default:
			return fmt.Errorf("%w: function %q", ErrNotAnswered, aggregate.Function)
		}
		kind, err := kindOfColumn(aggregate.Column)
		if err != nil {
			return err
		}
		if kind == columnar.Text && (aggregate.Function == "sum" || aggregate.Function == "avg") {
			return fmt.Errorf("%w: %s of a text column", ErrNotAnswered, aggregate.Function)
		}
	}
	return nil
}

// comparable reports a value SQLite compares with a column of kind as its own kind: a number with a number column, a
// text with a text column. Anything else goes by SQLite's affinity rules, which the rows apply.
func comparable(kind columnar.Kind, value any) bool {
	switch value.(type) {
	case int64, float64:
		return kind == columnar.Integer || kind == columnar.Float
	case string:
		return kind == columnar.Text
	}
	return false
}

// Truth values of a row under a condition.
const (
	truthFalse uint8 = iota
	truthTrue
	truthNull
)

// evaluator evaluates a condition over the rows of a batch, column by column.
type evaluator struct {
	columns map[string]int // the batch's column of a name
	buffers [][]uint8
}

func (e *evaluator) buffer() []uint8 {
	if len(e.buffers) == 0 {
		return make([]uint8, columnar.BatchRows)
	}
	last := e.buffers[len(e.buffers)-1]
	e.buffers = e.buffers[:len(e.buffers)-1]
	return last
}

func (e *evaluator) release(buffer []uint8) { e.buffers = append(e.buffers, buffer) }

// evaluate writes condition's truth for each row of batch into out.
func (e *evaluator) evaluate(condition *Condition, batch *columnar.Batch, out []uint8) {
	rows := batch.Rows
	switch condition.Op {
	case OpAnd, OpOr:
		e.evaluate(condition.Args[0], batch, out)
		next := e.buffer()
		for _, argument := range condition.Args[1:] {
			e.evaluate(argument, batch, next)
			for row := range rows {
				out[row] = combine(condition.Op, out[row], next[row])
			}
		}
		e.release(next)
	case OpNot:
		e.evaluate(condition.Args[0], batch, out)
		for row := range rows {
			if out[row] != truthNull {
				out[row] ^= 1
			}
		}
	case OpNull, OpNotNull:
		valid := batch.Columns[e.columns[condition.Column]].Valid
		want := uint8(0)
		if condition.Op == OpNotNull {
			want = 1
		}
		for row := range rows {
			out[row] = truthFalse
			if valid[row] == want {
				out[row] = truthTrue
			}
		}
	default:
		vector := &batch.Columns[e.columns[condition.Column]]
		for row, flag := range vector.Valid[:rows] {
			out[row] = truthFalse
			if flag == 0 {
				out[row] = truthNull
			}
		}
		if condition.Op != OpIn {
			markHolding(condition.Op, vector, rows, condition.Value, out)
			return
		}
		for _, value := range condition.Values {
			markHolding(OpEqual, vector, rows, value, out)
		}
	}
}

// markHolding marks true each row of vector whose value op compared with value keeps; a NULL row stays as it is.
func markHolding(op string, vector *columnar.Vector, rows int, value any, out []uint8) {
	valid := vector.Valid[:rows]
	switch vector.Field.Kind {
	case columnar.Text:
		markOrdered(op, vector.Texts[:rows], valid, value.(string), out)
	case columnar.Float:
		if number, isReal := value.(float64); isReal {
			markOrdered(op, vector.Floats[:rows], valid, number, out)
			return
		}
		number := value.(int64)
		for row, cell := range vector.Floats[:rows] {
			if valid[row] != 0 && holds(op, -compareIntegerReal(number, cell)) {
				out[row] = truthTrue
			}
		}
	default:
		if number, isInteger := value.(int64); isInteger {
			markOrdered(op, vector.Integers[:rows], valid, number, out)
			return
		}
		number := value.(float64)
		for row, cell := range vector.Integers[:rows] {
			if valid[row] != 0 && holds(op, compareIntegerReal(cell, number)) {
				out[row] = truthTrue
			}
		}
	}
}

// markOrdered is markHolding for a column and a value of one kind.
func markOrdered[T cmp.Ordered](op string, cells []T, valid []byte, value T, out []uint8) {
	switch op {
	case OpEqual:
		for row, cell := range cells {
			if cell == value && valid[row] != 0 {
				out[row] = truthTrue
			}
		}
	case OpUnequal:
		for row, cell := range cells {
			if cell != value && valid[row] != 0 {
				out[row] = truthTrue
			}
		}
	case OpLess:
		for row, cell := range cells {
			if cell < value && valid[row] != 0 {
				out[row] = truthTrue
			}
		}
	case OpAtMost:
		for row, cell := range cells {
			if cell <= value && valid[row] != 0 {
				out[row] = truthTrue
			}
		}
	case OpMore:
		for row, cell := range cells {
			if cell > value && valid[row] != 0 {
				out[row] = truthTrue
			}
		}
	case OpAtLeast:
		for row, cell := range cells {
			if cell >= value && valid[row] != 0 {
				out[row] = truthTrue
			}
		}
	}
}

// combine is SQLite's AND and OR of two truth values.
func combine(op string, a, b uint8) uint8 {
	if op == OpAnd {
		switch {
		case a == truthFalse || b == truthFalse:
			return truthFalse
		case a == truthNull || b == truthNull:
			return truthNull
		}
		return truthTrue
	}
	switch {
	case a == truthTrue || b == truthTrue:
		return truthTrue
	case a == truthNull || b == truthNull:
		return truthNull
	}
	return truthFalse
}

func holds(op string, order int) bool {
	switch op {
	case OpEqual, OpIn:
		return order == 0
	case OpUnequal:
		return order != 0
	case OpLess:
		return order < 0
	case OpAtMost:
		return order <= 0
	case OpMore:
		return order > 0
	case OpAtLeast:
		return order >= 0
	}
	return false
}

// compareIntegerReal orders an integer and a real exactly, as SQLite's sqlite3IntFloatCompare does.
func compareIntegerReal(integer int64, real float64) int {
	switch {
	case math.IsNaN(real):
		return 1
	case real < -9223372036854775808.0:
		return 1
	case real >= 9223372036854775808.0:
		return -1
	}
	truncated := int64(real)
	if integer != truncated {
		return cmp.Compare(integer, truncated)
	}
	return cmp.Compare(float64(integer), real) // the same whole part: the fraction decides
}

// accumulator is one aggregate's state in one group.
type accumulator struct {
	count    int64
	integer  int64 // sum of integers
	overflow bool
	real     float64 // sum of reals, Kahan-Babuska-Neumaier as SQLite's sum()
	error    float64
	approx   bool // a real was summed
	// extremes: a minimum and a maximum are held, of the column's kind
	extremes                       bool
	minimumInteger, maximumInteger int64
	minimumReal, maximumReal       float64
	minimumText, maximumText       string
}

func (a *accumulator) keepInteger(value int64) {
	if !a.extremes {
		a.minimumInteger, a.maximumInteger, a.extremes = value, value, true
		return
	}
	a.minimumInteger = min(a.minimumInteger, value)
	a.maximumInteger = max(a.maximumInteger, value)
}

func (a *accumulator) keepReal(value float64) {
	if !a.extremes {
		a.minimumReal, a.maximumReal, a.extremes = value, value, true
		return
	}
	a.minimumReal = min(a.minimumReal, value)
	a.maximumReal = max(a.maximumReal, value)
}

// keepText keeps a copy of value: the batch's texts are not to be kept.
func (a *accumulator) keepText(value string) {
	if !a.extremes {
		a.minimumText, a.maximumText, a.extremes = strings.Clone(value), strings.Clone(value), true
		return
	}
	if value < a.minimumText {
		a.minimumText = strings.Clone(value)
	}
	if value > a.maximumText {
		a.maximumText = strings.Clone(value)
	}
}

// add takes the cell at row of vector into the accumulator of function.
func (a *accumulator) add(function string, vector *columnar.Vector, row int) {
	if vector.Valid[row] == 0 {
		return
	}
	switch function {
	case "count":
		a.count++
	case "min", "max":
		a.count++
		switch vector.Field.Kind {
		case columnar.Integer:
			a.keepInteger(vector.Integers[row])
		case columnar.Float:
			a.keepReal(vector.Floats[row])
		default:
			a.keepText(vector.Texts[row])
		}
	default:
		if vector.Field.Kind == columnar.Float {
			a.addReal(vector.Floats[row])
		} else {
			a.addInteger(vector.Integers[row])
		}
	}
}

func (a *accumulator) addInteger(value int64) {
	a.count++
	a.addSum(value)
}

// addSum adds value to the integer sum, noting an overflow (Go's integers wrap, as the test below relies on).
func (a *accumulator) addSum(value int64) {
	sum := a.integer + value
	if (value > 0 && sum < a.integer) || (value < 0 && sum > a.integer) {
		a.overflow = true
	}
	a.integer = sum
}

func (a *accumulator) addReal(value float64) {
	a.count++
	if !a.approx {
		a.approx = true
		a.real = float64(a.integer)
	}
	s := a.real
	t := s + value
	if math.Abs(s) > math.Abs(value) {
		a.error += (s - t) + value
	} else {
		a.error += (value - t) + s
	}
	a.real = t
}

// compareValues orders two values of one column: NULL first, numbers by value, texts byte by byte.
func compareValues(a, b any) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return -1
	case b == nil:
		return 1
	}
	switch x := a.(type) {
	case int64:
		switch y := b.(type) {
		case int64:
			return cmp.Compare(x, y)
		case float64:
			return compareIntegerReal(x, y)
		}
	case float64:
		switch y := b.(type) {
		case float64:
			return cmp.Compare(x, y)
		case int64:
			return -compareIntegerReal(y, x)
		}
	case string:
		return cmp.Compare(x, b.(string))
	}
	return 0
}

// result is an aggregate's value as SQLite gives it.
func (a *accumulator) result(function string, kind columnar.Kind) (any, error) {
	switch function {
	case "count":
		return a.count, nil
	case "min", "max":
		if !a.extremes {
			return nil, nil
		}
		minimum := function == "min"
		switch kind {
		case columnar.Integer:
			return pick(minimum, a.minimumInteger, a.maximumInteger), nil
		case columnar.Float:
			return pick(minimum, a.minimumReal, a.maximumReal), nil
		}
		return pick(minimum, a.minimumText, a.maximumText), nil
	case "sum":
		if a.count == 0 {
			return nil, nil
		}
		if kind == columnar.Integer {
			if a.overflow {
				return nil, ErrOverflow
			}
			return a.integer, nil
		}
		return a.real + a.error, nil
	case "avg":
		if a.count == 0 {
			return nil, nil
		}
		if kind == columnar.Integer {
			if a.overflow {
				return nil, ErrOverflow
			}
			return float64(a.integer) / float64(a.count), nil
		}
		return (a.real + a.error) / float64(a.count), nil
	}
	return nil, fmt.Errorf("%w: function %q", ErrNotAnswered, function)
}

func pick[T any](first bool, a, b T) any {
	if first {
		return a
	}
	return b
}

// group is the state of one group.
type group struct {
	key          []any
	accumulators []accumulator
}

// Execute answers query from the copy as the view's snapshot holds the table. A block the filter cannot match is
// skipped by its statistics; without a filter and a group, the counts, integer sums, minimums and maximums of a block
// without a changed row are taken from its statistics.
func (v *View) Execute(ctx context.Context, query Query) (Result, error) {
	if err := v.check(&query); err != nil {
		return Result{}, err
	}
	manifest := v.table.copy.Manifest
	columns := query.columnsOf()
	index := map[string]int{}
	kinds := map[string]columnar.Kind{}
	for position, name := range columns {
		index[name] = position + 1 // the rowid is column 0
		kinds[name] = manifest.Columns[manifest.position(name)].Kind
	}
	if len(columns) == 0 { // count(*) alone: read the rowid
		columns = nil
	}
	var groups []*group
	newGroup := func(key []any) int32 {
		groups = append(groups, &group{key: key, accumulators: make([]accumulator, len(query.Aggregates))})
		return int32(len(groups) - 1)
	}
	var single *group
	if len(query.Group) == 0 {
		single = groups[newGroup(nil)]
		groups = nil
	}
	// a group is found by its value: one integer or one text column by a map of it, more columns by a key of their
	// values; a NULL of one column is a group of its own
	integerGroups, textGroups, keyGroups := map[int64]int32{}, map[string]int32{}, map[string]int32{}
	nullGroup := int32(-1)
	keyBuffer := make([]byte, 0, 64)
	groupOf := func(batch *columnar.Batch, row int) int32 {
		if len(query.Group) == 1 {
			vector := &batch.Columns[index[query.Group[0]]]
			switch {
			case vector.Valid[row] == 0:
				if nullGroup < 0 {
					nullGroup = newGroup([]any{nil})
				}
				return nullGroup
			case vector.Field.Kind == columnar.Integer:
				value := vector.Integers[row]
				found, known := integerGroups[value]
				if !known {
					found = newGroup([]any{value})
					integerGroups[value] = found
				}
				return found
			case vector.Field.Kind == columnar.Text:
				value := vector.Texts[row]
				found, known := textGroups[value]
				if !known {
					kept := strings.Clone(value)
					found = newGroup([]any{kept})
					textGroups[kept] = found
				}
				return found
			}
		}
		keyBuffer = keyBuffer[:0]
		for _, name := range query.Group {
			keyBuffer = appendKey(keyBuffer, cellValue(&batch.Columns[index[name]], row))
		}
		found, known := keyGroups[string(keyBuffer)]
		if !known {
			key := make([]any, len(query.Group))
			for position, name := range query.Group {
				key[position] = cellValue(&batch.Columns[index[name]], row)
				if text, isText := key[position].(string); isText {
					key[position] = strings.Clone(text)
				}
			}
			found = newGroup(key)
			keyGroups[string(keyBuffer)] = found
		}
		return found
	}
	statisticsOnly := query.Filter == nil && len(query.Group) == 0
	for _, aggregate := range query.Aggregates {
		if aggregate.Column != "" && kinds[aggregate.Column] != columnar.Integer && aggregate.Function != "count" {
			statisticsOnly = false // a real's sum is not in the statistics, a text has no minimum there
		}
		if aggregate.Function == "avg" || (aggregate.Function == "sum" && aggregate.Column != "" && kinds[aggregate.Column] != columnar.Integer) {
			statisticsOnly = false
		}
	}
	statisticsOf := func(statistics *columnar.BlockStatistics, name string) *columnar.ColumnStatistics {
		return &statistics.Columns[index[name]]
	}
	// A block that holds no changed row is answered by its statistics when the query asks only what they hold: its
	// counts, integer sums, minimums and maximums go into taken.
	taken := make([]accumulator, len(query.Aggregates))
	takeStatistics := func(statistics *columnar.BlockStatistics) {
		for position, aggregate := range query.Aggregates {
			accumulator := &taken[position]
			if aggregate.Column == "" {
				accumulator.count += int64(statistics.Rows)
				continue
			}
			column := statisticsOf(statistics, aggregate.Column)
			accumulator.count += int64(statistics.Rows - int(column.Nulls))
			switch aggregate.Function {
			case "sum":
				low, high := column.IntegerSumLow, column.IntegerSumHigh
				if !(high == 0 && low <= math.MaxInt64) && !(high == -1 && low > math.MaxInt64) {
					accumulator.overflow = true // the block's sum alone passes 64 bits
				}
				accumulator.addSum(int64(low))
			case "min", "max":
				if column.HasValue {
					accumulator.keepInteger(column.IntegerMin)
					accumulator.keepInteger(column.IntegerMax)
				}
			}
		}
	}
	decide := func(statistics *columnar.BlockStatistics, touched bool) (columnar.BlockAction, error) {
		if query.Filter != nil && !mayMatch(query.Filter, func(name string) *columnar.ColumnStatistics {
			return statisticsOf(statistics, name)
		}, statistics.Rows) {
			return columnar.SkipBlock, nil
		}
		if statisticsOnly && !touched {
			takeStatistics(statistics)
			return columnar.UseBlockStatistics, nil
		}
		return columnar.ScanBlock, nil
	}
	e := &evaluator{columns: index}
	truth := make([]uint8, columnar.BatchRows)
	selected := make([]int32, 0, columnar.BatchRows)
	owners := make([]int32, columnar.BatchRows)
	_, err := v.Scan(ctx, columns, decide, func(batch *columnar.Batch, skip []bool) error {
		if query.Filter != nil {
			e.evaluate(query.Filter, batch, truth)
		}
		selected = selected[:0]
		for row := range batch.Rows {
			if skip != nil && skip[row] || query.Filter != nil && truth[row] != truthTrue {
				continue
			}
			selected = append(selected, int32(row))
		}
		if single == nil {
			for position, row := range selected {
				owners[position] = groupOf(batch, int(row))
			}
		}
		for position, aggregate := range query.Aggregates {
			if single != nil {
				accumulator := &single.accumulators[position]
				if aggregate.Column == "" {
					accumulator.count += int64(len(selected))
					continue
				}
				vector := &batch.Columns[index[aggregate.Column]]
				if aggregate.Function == "sum" && vector.Field.Kind == columnar.Integer {
					for _, row := range selected { // the most common: a loop of its own
						if vector.Valid[row] != 0 {
							accumulator.count++
							accumulator.addSum(vector.Integers[row])
						}
					}
					continue
				}
				for _, row := range selected {
					accumulator.add(aggregate.Function, vector, int(row))
				}
				continue
			}
			if aggregate.Column == "" {
				for at := range selected {
					groups[owners[at]].accumulators[position].count++
				}
				continue
			}
			vector := &batch.Columns[index[aggregate.Column]]
			for at, row := range selected {
				groups[owners[at]].accumulators[position].add(aggregate.Function, vector, int(row))
			}
		}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	if single != nil {
		for position := range query.Aggregates {
			merge(&single.accumulators[position], &taken[position])
		}
	}
	var ordered []*group
	if single != nil {
		ordered = []*group{single}
	} else {
		ordered = groups
		slices.SortFunc(ordered, func(a, b *group) int {
			for position := range a.key {
				if order := compareValues(a.key[position], b.key[position]); order != 0 {
					return order
				}
			}
			return 0
		})
	}
	result := Result{Rows: make([][]any, 0, len(ordered))}
	for _, found := range ordered {
		row := append([]any(nil), found.key...)
		for position, aggregate := range query.Aggregates {
			value, err := found.accumulators[position].result(aggregate.Function, kinds[aggregate.Column])
			if err != nil {
				return Result{}, err
			}
			row = append(row, value)
		}
		result.Rows = append(result.Rows, row)
	}
	return result, nil
}

// merge adds what the statistics gave into an accumulator of the scanned rows (integers only).
func merge(into, from *accumulator) {
	into.count += from.count
	into.addSum(from.integer)
	into.overflow = into.overflow || from.overflow
	if from.extremes {
		into.keepInteger(from.minimumInteger)
		into.keepInteger(from.maximumInteger)
	}
}

// mayMatch reports whether a block may hold a row the condition keeps, from its statistics: false only when no row
// can (a range outside the block's minimum and maximum, a block of NULLs alone).
func mayMatch(condition *Condition, statisticsOf func(string) *columnar.ColumnStatistics, rows int) bool {
	switch condition.Op {
	case OpAnd:
		for _, argument := range condition.Args {
			if !mayMatch(argument, statisticsOf, rows) {
				return false
			}
		}
		return true
	case OpOr:
		for _, argument := range condition.Args {
			if mayMatch(argument, statisticsOf, rows) {
				return true
			}
		}
		return false
	case OpNull:
		return statisticsOf(condition.Column).Nulls > 0
	case OpNotNull:
		return int(statisticsOf(condition.Column).Nulls) < rows
	case OpEqual, OpLess, OpAtMost, OpMore, OpAtLeast, OpIn:
		statistics := statisticsOf(condition.Column)
		if !statistics.HasValue {
			return false // every row NULL: no comparison is true
		}
		values := condition.Values
		if condition.Op != OpIn {
			values = []any{condition.Value}
		}
		for _, value := range values {
			if rangeMayHold(statistics, condition.Op, value) {
				return true
			}
		}
		return false
	}
	return true // NOT, !=: no shortcut
}

// rangeMayHold reports whether a block whose column lies within its statistics' minimum and maximum may hold a value
// that op compared with value keeps.
func rangeMayHold(statistics *columnar.ColumnStatistics, op string, value any) bool {
	var low, high any
	switch statistics.Field.Kind {
	case columnar.Integer:
		low, high = statistics.IntegerMin, statistics.IntegerMax
	case columnar.Float:
		low, high = statistics.FloatMin, statistics.FloatMax
	default:
		return true // a text block keeps no minimum and maximum
	}
	switch op {
	case OpEqual, OpIn:
		return compareValues(low, value) <= 0 && compareValues(high, value) >= 0
	case OpLess:
		return compareValues(low, value) < 0
	case OpAtMost:
		return compareValues(low, value) <= 0
	case OpMore:
		return compareValues(high, value) > 0
	case OpAtLeast:
		return compareValues(high, value) >= 0
	}
	return true
}

// cellValue is the cell at row of vector as SQLite gives it.
func cellValue(vector *columnar.Vector, row int) any {
	if vector.Valid[row] == 0 {
		return nil
	}
	switch vector.Field.Kind {
	case columnar.Float:
		return vector.Floats[row]
	case columnar.Text:
		return vector.Texts[row]
	}
	return vector.Integers[row]
}

// appendKey writes a group's value into its key: a tag of the kind, then the value.
func appendKey(key []byte, value any) []byte {
	switch typed := value.(type) {
	case nil:
		return append(key, 0)
	case int64:
		key = append(key, 1)
		return binary.LittleEndian.AppendUint64(key, uint64(typed))
	case float64:
		key = append(key, 2)
		return binary.LittleEndian.AppendUint64(key, math.Float64bits(typed))
	case string:
		key = append(key, 3)
		key = binary.LittleEndian.AppendUint32(key, uint32(len(typed)))
		return append(key, typed...)
	}
	return key
}
