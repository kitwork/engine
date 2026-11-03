// Package statement is the form a database statement of Kitwork takes between the compiler and the
// engine (KITWORK_DATABASE.md mục 6, "Dịch một lần lúc biên dịch"). The compiler reads a chain written on
// a handle imported from .shared/schema/ (db.where(…).orderBy(…).list(…)) and writes it as one template:
// the table, the condition as a tree of columns, literals and numbered holes, the order, the bounds, the
// data of an insert. The query lambdas are never compiled and never run; what the lambda read from
// outside (today, email) is a hole, a value the program computes before the statement, passed beside the
// template. The engine decodes the template once, checks it against the live schema, binds the holes by
// column kind and runs it.
//
// The package holds the format, the catalog rules both sides check names with, and the vocabulary of the
// spec, so the compiler and the engine say the same thing in the same words. It imports nothing of the
// engine.
package statement

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Version is the template format. A template of another version was compiled by another engine: it is
// refused, never guessed. A change of the format bumps it together with the compiler's schema (the
// pairing test of the compiler holds the two together). Version 3 (03/11/2026) adds update, delete and
// upsert, a column and + or - in the place of a value of their data, and an upsert's target; version 4 (the
// same day) history and at().
const Version = 4

// Method is the method a lowered statement invokes on its handle: LOAD db; PUSH template; holes…;
// PUSH "#statement"; INVOKE 1+n. A source never spells # after a dot, so no program calls it by hand.
const Method = "#statement"

// TransactionMethod is what db.transaction((database) => { … }) lowers to: LOAD db; the function; PUSH
// "#transaction"; INVOKE 1 (KITWORK_DATABASE.md mục 3). The function's parameter is a handle whose statements
// run inside the transaction.
const TransactionMethod = "#transaction"

// Ops of a Node.
const (
	OpTable   = "table"   // the table itself: every row
	OpColumn  = "column"  // a column of the statement's table
	OpLiteral = "literal" // a number, string or boolean written in the lambda
	OpNull    = "null"    // null written in the lambda: IS NULL / IS NOT NULL
	OpHole    = "hole"    // a value computed outside the lambda, by its index
	OpTruth   = "truth"   // a hole in the place of a condition: true or false by JavaScript's truthiness
	OpOne     = "one"     // one(array) on the right of === or !==: IN / NOT IN
	// OpMatches is full text (KITWORK_DATABASE.md mục 2): table.matches(text) over every .searchable() column
	// (Column empty), table.column.matches(text) over one; its one argument a literal string or a hole. It stands
	// only in the outermost && chain, alone or under one !. Added with Version 2.
	OpMatches = "matches"
	// OpAdd and OpSubtract are + and - in a value of the data of update or upsert, between a number column of
	// the table and a number (orders.amount + 1000); a column stands there for its value on the row the
	// statement changes. Added with Version 3.
	OpAdd      = "+"
	OpSubtract = "-"
	OpAnd      = "&&"
	OpOr       = "||"
	OpNot      = "!"
	OpEqual    = "==="
	OpUnequal  = "!=="
	OpLess     = "<"
	OpAtMost   = "<="
	OpMore     = ">"
	OpAtLeast  = ">="
)

// Actions of a statement in this format.
const (
	ActionList   = "list"
	ActionFirst  = "first"
	ActionCount  = "count"
	ActionInsert = "insert"
	ActionUpdate = "update"
	ActionDelete = "delete"
	ActionUpsert = "upsert"
	// ActionHistory reads the changes of the rows its condition keeps (KITWORK_DATABASE.md mục 2).
	ActionHistory = "history"
)

// Writes reports an action that changes rows.
func Writes(action string) bool {
	switch action {
	case ActionInsert, ActionUpdate, ActionDelete, ActionUpsert:
		return true
	}
	return false
}

// Statement is one database statement: an action on one table.
type Statement struct {
	Version int    `json:"v"`
	Action  string `json:"action"`
	Table   string `json:"table"`
	// Where is the condition: the lambdas of where() and of the action, joined by &&; nil for every row.
	Where *Node `json:"where,omitempty"`
	// Order lists the keys of orderBy(), in the order written.
	Order []Key `json:"order,omitempty"`
	// Limit and Page are list(n) and list({ page, limit }): a literal or a hole each.
	Limit *Node `json:"limit,omitempty"`
	Page  *Node `json:"page,omitempty"`
	// Bounds is list(x) with x a value the compiler cannot type: a hole, a number or { page, limit } when
	// it runs.
	Bounds *Node `json:"bounds,omitempty"`
	// Values is the data of an insert, an update or an upsert, in the order written.
	Values []Field `json:"values,omitempty"`
	// Target is the columns an upsert compares its data by, in the order the table declares them: a
	// .unique() column, or every column of one .unique("tên") group.
	Target []string `json:"target,omitempty"`
	// At is the moment of at(): a literal or a hole; the statement reads the database as it was then.
	At *Node `json:"at,omitempty"`
	// Holes names, per hole, where its value is written in the source; never the value.
	Holes []Hole `json:"holes,omitempty"`
}

// Node is one node of a condition, or a value.
type Node struct {
	Op     string          `json:"op"`
	Column string          `json:"column,omitempty"`
	Hole   *int            `json:"hole,omitempty"`
	Value  json.RawMessage `json:"value,omitempty"`
	Args   []*Node         `json:"args,omitempty"`
}

// Key is one key of orderBy().
type Key struct {
	Column     string `json:"column"`
	Descending bool   `json:"desc,omitempty"`
}

// Field is one key of an insert's data.
type Field struct {
	Column string `json:"column"`
	Value  *Node  `json:"value"`
}

// Hole is a value from outside the lambda: Site is where it is written (router.work:7:52), which an
// error of its value names instead of the value.
type Hole struct {
	Site string `json:"site"`
}

// Literal is the node of a literal: a number, a string or a boolean.
func Literal(item any) *Node {
	encoded, err := json.Marshal(item)
	if err != nil {
		encoded = []byte("null")
	}
	return &Node{Op: OpLiteral, Value: encoded}
}

// HoleNode is the node of hole index.
func HoleNode(index int) *Node {
	return &Node{Op: OpHole, Hole: &index}
}

// Encode writes a statement as its template. The bytes depend on the statement alone (structs and slices,
// no map), so one source compiles to one constant.
func Encode(s *Statement) string {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(s); err != nil {
		return ""
	}
	return string(bytes.TrimRight(out.Bytes(), "\n"))
}

// Decode reads a template and checks its shape. A template of another version, or one whose shape this
// engine does not know, is an error: it was written by another compiler, or by hand.
func Decode(text string) (*Statement, error) {
	var s Statement
	decoder := json.NewDecoder(bytes.NewReader([]byte(text)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&s); err != nil {
		return nil, fmt.Errorf("câu database không đọc được (%v): dịch lại", err)
	}
	if s.Version != Version {
		return nil, fmt.Errorf("câu database dịch bằng engine khác (bản %d, engine này đọc bản %d): dịch lại", s.Version, Version)
	}
	if err := s.check(); err != nil {
		return nil, fmt.Errorf("câu database hỏng: %v: dịch lại", err)
	}
	return &s, nil
}

func (s *Statement) check() error {
	if err := s.Where.checkMatches(true); err != nil {
		return err
	}
	switch s.Action {
	case ActionList, ActionFirst, ActionCount, ActionInsert, ActionUpdate, ActionDelete, ActionUpsert, ActionHistory:
	default:
		return fmt.Errorf("hành động %q", s.Action)
	}
	if s.Table == "" {
		return fmt.Errorf("không có bảng")
	}
	holes := len(s.Holes)
	if s.Where != nil {
		if err := s.Where.checkCondition(holes); err != nil {
			return err
		}
		if Writes(s.Action) && s.Where.mentionsMatches() {
			return fmt.Errorf("matches trong %s", s.Action)
		}
	}
	for _, key := range s.Order {
		if key.Column == "" {
			return fmt.Errorf("khoá orderBy không có cột")
		}
	}
	for _, bound := range []*Node{s.Limit, s.Page, s.Bounds} {
		if bound != nil {
			if err := bound.checkValue(holes, false); err != nil {
				return err
			}
		}
	}
	if (Writes(s.Action) || s.Action == ActionHistory) && (len(s.Order) != 0 || s.Limit != nil || s.Page != nil || s.Bounds != nil) {
		return fmt.Errorf("%s có thứ tự hay giới hạn", s.Action)
	}
	if s.At != nil {
		if Writes(s.Action) || s.Action == ActionHistory {
			return fmt.Errorf("at() với %s", s.Action)
		}
		if err := s.At.checkValue(holes, false); err != nil {
			return err
		}
		if s.Where.mentionsMatches() {
			return fmt.Errorf("matches với at()")
		}
	}
	if s.Action == ActionHistory && s.Where.mentionsMatches() {
		return fmt.Errorf("matches trong history")
	}
	switch s.Action {
	case ActionInsert, ActionUpsert:
		if s.Where != nil {
			return fmt.Errorf("%s có điều kiện", s.Action)
		}
	case ActionUpdate, ActionDelete:
		if s.Where == nil {
			return fmt.Errorf("%s không có điều kiện", s.Action)
		}
	}
	switch s.Action {
	case ActionInsert, ActionUpdate, ActionUpsert:
		if len(s.Values) == 0 {
			return fmt.Errorf("%s không có dữ liệu", s.Action)
		}
	default:
		if len(s.Values) != 0 {
			return fmt.Errorf("%s có dữ liệu", s.Action)
		}
	}
	if (s.Action == ActionUpsert) != (len(s.Target) != 0) {
		return fmt.Errorf("đích so trùng chỉ của upsert, và upsert cần một")
	}
	for _, field := range s.Values {
		if field.Column == "" || field.Value == nil {
			return fmt.Errorf("dữ liệu %s thiếu cột hay giá trị", s.Action)
		}
		check := field.Value.checkValue(holes, true)
		if s.Action != ActionInsert {
			check = field.Value.checkData(holes)
		}
		if check != nil {
			return check
		}
	}
	return nil
}

// checkData checks a node in the place of a value of update or upsert data: a value, a column of the
// table, or + or - of two of them, with null only alone.
func (n *Node) checkData(holes int) error {
	if n == nil {
		return fmt.Errorf("giá trị rỗng")
	}
	return n.checkOperand(holes, true)
}

func (n *Node) checkOperand(holes int, null bool) error {
	switch n.Op {
	case OpColumn:
		if n.Column == "" {
			return fmt.Errorf("cột không tên")
		}
		return n.arity(0)
	case OpAdd, OpSubtract:
		if err := n.arity(2); err != nil {
			return err
		}
		for _, argument := range n.Args {
			if argument == nil {
				return fmt.Errorf("%s thiếu vế", n.Op)
			}
			if err := argument.checkOperand(holes, false); err != nil {
				return err
			}
		}
		return nil
	}
	return n.checkValue(holes, null)
}

// checkCondition checks a node in the place of a condition.
func (n *Node) checkCondition(holes int) error {
	if n == nil {
		return fmt.Errorf("điều kiện rỗng")
	}
	switch n.Op {
	case OpTable:
		return n.arity(0)
	case OpTruth:
		if err := n.arity(1); err != nil {
			return err
		}
		if n.Args[0].Op != OpHole {
			return fmt.Errorf("truth không trên một hole")
		}
		return n.Args[0].checkValue(holes, false)
	case OpAnd, OpOr:
		if len(n.Args) != 2 {
			return fmt.Errorf("%s cần hai vế, có %d", n.Op, len(n.Args))
		}
		for _, argument := range n.Args {
			if err := argument.checkCondition(holes); err != nil {
				return err
			}
		}
		return nil
	case OpNot:
		if err := n.arity(1); err != nil {
			return err
		}
		return n.Args[0].checkCondition(holes)
	case OpMatches:
		if err := n.arity(1); err != nil {
			return err
		}
		return n.Args[0].checkValue(holes, false)
	case OpEqual, OpUnequal, OpLess, OpAtMost, OpMore, OpAtLeast:
		if err := n.arity(2); err != nil {
			return err
		}
		if n.Args[0].Op != OpColumn || n.Args[0].Column == "" {
			return fmt.Errorf("%s không có cột bên trái", n.Op)
		}
		right := n.Args[1]
		switch right.Op {
		case OpNull, OpOne:
			if n.Op != OpEqual && n.Op != OpUnequal {
				return fmt.Errorf("%s với %s", right.Op, n.Op)
			}
		}
		if right.Op == OpOne {
			if err := right.arity(1); err != nil {
				return err
			}
			return right.Args[0].checkValue(holes, false)
		}
		return right.checkValue(holes, true)
	}
	return fmt.Errorf("điều kiện %q", n.Op)
}

// checkMatches checks that matches stands only where the engine reads it: in the outermost && chain (top), alone or
// under one !.
func (n *Node) checkMatches(top bool) error {
	if n == nil {
		return nil
	}
	switch n.Op {
	case OpMatches:
		return nil
	case OpAnd:
		for _, argument := range n.Args {
			if err := argument.checkMatches(top); err != nil {
				return err
			}
		}
		return nil
	case OpNot:
		if top && len(n.Args) == 1 && n.Args[0].Op == OpMatches {
			return nil
		}
	}
	for _, argument := range n.Args {
		if argument.mentionsMatches() {
			return fmt.Errorf("matches dưới %s", n.Op)
		}
	}
	return nil
}

func (n *Node) mentionsMatches() bool {
	if n == nil {
		return false
	}
	if n.Op == OpMatches {
		return true
	}
	for _, argument := range n.Args {
		if argument.mentionsMatches() {
			return true
		}
	}
	return false
}

// Matches lists the matches of a condition: those it ranks by (positive) and those it only filters by (under !).
func (n *Node) Matches() (positive, negative []*Node) {
	var walk func(*Node)
	walk = func(node *Node) {
		switch {
		case node == nil:
		case node.Op == OpMatches:
			positive = append(positive, node)
		case node.Op == OpNot && len(node.Args) == 1 && node.Args[0].Op == OpMatches:
			negative = append(negative, node.Args[0])
		case node.Op == OpAnd:
			for _, argument := range node.Args {
				walk(argument)
			}
		}
	}
	walk(n)
	return positive, negative
}

// checkValue checks a node in the place of a value: a literal, a hole, and null where null is allowed.
func (n *Node) checkValue(holes int, null bool) error {
	if n == nil {
		return fmt.Errorf("giá trị rỗng")
	}
	switch n.Op {
	case OpLiteral:
		if len(n.Value) == 0 || len(n.Args) != 0 {
			return fmt.Errorf("literal không có giá trị")
		}
		var item any
		if err := json.Unmarshal(n.Value, &item); err != nil {
			return fmt.Errorf("literal: %v", err)
		}
		switch item.(type) {
		case float64, string, bool:
			return nil
		}
		return fmt.Errorf("literal không phải số, chuỗi hay boolean")
	case OpHole:
		if n.Hole == nil || *n.Hole < 0 || *n.Hole >= holes {
			return fmt.Errorf("hole ngoài danh sách %d hole", holes)
		}
		return n.arity(0)
	case OpNull:
		if !null {
			return fmt.Errorf("null ở chỗ không nhận null")
		}
		return n.arity(0)
	}
	return fmt.Errorf("giá trị %q", n.Op)
}

func (n *Node) arity(want int) error {
	if len(n.Args) != want {
		return fmt.Errorf("%s cần %d vế, có %d", n.Op, want, len(n.Args))
	}
	return nil
}

// Columns lists every column a statement names, in the order it names them: the condition, the order,
// the data.
func (s *Statement) Columns() []string {
	var columns []string
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if n.Op == OpColumn || n.Op == OpMatches && n.Column != "" {
			columns = append(columns, n.Column)
		}
		for _, argument := range n.Args {
			walk(argument)
		}
	}
	walk(s.Where)
	for _, key := range s.Order {
		columns = append(columns, key.Column)
	}
	for _, field := range s.Values {
		columns = append(columns, field.Column)
		walk(field.Value)
	}
	columns = append(columns, s.Target...)
	return columns
}
