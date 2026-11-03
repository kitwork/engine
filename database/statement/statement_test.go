package statement

import (
	"reflect"
	"strings"
	"testing"
)

// every names each op of the format slice 1 reads: a condition with &&, ||, !, the six comparisons, null,
// one, a hole in the place of a condition and the whole table, an order, a page.
func every() *Statement {
	hole := func(index int) *Node { return HoleNode(index) }
	column := func(name string) *Node { return &Node{Op: OpColumn, Column: name} }
	compare := func(op string, left, right *Node) *Node { return &Node{Op: op, Args: []*Node{left, right}} }
	return &Statement{
		Version: Version,
		Action:  ActionList,
		Table:   "orders",
		Where: &Node{Op: OpAnd, Args: []*Node{
			&Node{Op: OpOr, Args: []*Node{
				compare(OpEqual, column("day"), hole(0)),
				&Node{Op: OpNot, Args: []*Node{compare(OpMore, column("amount"), Literal(5))}},
			}},
			&Node{Op: OpAnd, Args: []*Node{
				&Node{Op: OpAnd, Args: []*Node{
					compare(OpUnequal, column("note"), &Node{Op: OpNull}),
					compare(OpEqual, column("id"), &Node{Op: OpOne, Args: []*Node{hole(1)}}),
				}},
				&Node{Op: OpOr, Args: []*Node{
					&Node{Op: OpTruth, Args: []*Node{hole(2)}},
					&Node{Op: OpAnd, Args: []*Node{
						&Node{Op: OpTable},
						&Node{Op: OpAnd, Args: []*Node{
							compare(OpLess, column("amount"), Literal(1.5)),
							&Node{Op: OpAnd, Args: []*Node{
								compare(OpAtMost, column("item"), Literal("trà")),
								compare(OpAtLeast, column("amount"), Literal(true)),
							}},
						}},
					}},
				}},
			}},
		}},
		Order: []Key{{Column: "amount", Descending: true}, {Column: "id"}},
		Page:  Literal(2),
		Limit: hole(3),
		Holes: []Hole{{Site: "router.work:3:49"}, {Site: "router.work:4:1"}, {Site: "router.work:5:1"}, {Site: "router.work:6:1"}},
	}
}

// A template decodes to the statement it was encoded from, two encodes give the same bytes, and a
// template of another version or of a shape the format has not is refused, saying to compile again.
func TestStatementRoundTripDeterminismVersion(t *testing.T) {
	original := every()
	encoded := Encode(original)
	if encoded == "" {
		t.Fatal("Encode gave nothing")
	}
	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode(%s): %v", encoded, err)
	}
	if !reflect.DeepEqual(decoded, original) {
		t.Fatalf("round trip changed the statement:\n%#v\n%#v", decoded, original)
	}
	for attempt := 0; attempt < 20; attempt++ {
		if again := Encode(every()); again != encoded {
			t.Fatalf("encode %d gave other bytes:\n%s\n%s", attempt, again, encoded)
		}
	}
	if !strings.Contains(encoded, `"op":"&&"`) {
		t.Fatalf("%s escapes &&: a template reads as written", encoded)
	}

	other := strings.Replace(encoded, `{"v":4,`, `{"v":3,`, 1)
	if _, err := Decode(other); err == nil || !strings.Contains(err.Error(), "dịch bằng engine khác (bản 3, engine này đọc bản 4): dịch lại") {
		t.Fatalf("Decode of version 3: %v, want the error that says to compile again", err)
	}
	lonely := `{"v":4,"action":"list","table":"orders","where":{"op":"&&","args":[{"op":"table"}]}}`
	if _, err := Decode(lonely); err == nil || !strings.Contains(err.Error(), "&& cần hai vế, có 1") || !strings.Contains(err.Error(), "dịch lại") {
		t.Fatalf("Decode of && with one side: %v, want malformed", err)
	}
	for template, want := range map[string]string{
		`{"v":4,"action":"list","table":"orders","where":{"op":"===","args":[{"op":"column","column":"day"},{"op":"hole","hole":0}]}}`: "hole ngoài danh sách 0 hole",
		`{"v":4,"action":"drop","table":"orders"}`:                                                                                                                        `hành động "drop"`,
		`{"v":4,"action":"list","table":"orders","sql":"DROP TABLE orders"}`:                                                                                              "unknown field",
		`{"v":4,"action":"insert","table":"orders"}`:                                                                                                                      "insert không có dữ liệu",
		`{"v":4,"action":"list","table":"orders","where":{"op":"<","args":[{"op":"column","column":"note"},{"op":"null"}]}}`:                                              "null với <",
		`{"v":4,"action":"list","table":"orders","where":{"op":"||","args":[{"op":"table"},{"op":"matches","args":[{"op":"literal","value":"x"}]}]}}`:                     "matches dưới ||",
		`{"v":4,"action":"list","table":"orders","where":{"op":"!","args":[{"op":"&&","args":[{"op":"table"},{"op":"matches","args":[{"op":"literal","value":"x"}]}]}]}}`: "matches dưới !",
		`{"v":4,"action":"list","table":"orders","where":{"op":"matches","args":[{"op":"column","column":"item"}]}}`:                                                      `giá trị "column"`,
	} {
		if _, err := Decode(template); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Decode(%s) = %v, want %q", template, err, want)
		}
	}
}

// matches stands in the outermost && chain, alone or under one !: Matches lists the ones that rank (positive) and the
// ones that only filter (negative), and a table.column.matches names its column among the statement's.
func TestMatchesInTheFormat(t *testing.T) {
	text := func(value string) *Node { return &Node{Op: OpMatches, Args: []*Node{Literal(value)}} }
	onItem := &Node{Op: OpMatches, Column: "item", Args: []*Node{HoleNode(0)}}
	where := &Node{Op: OpAnd, Args: []*Node{
		&Node{Op: OpAnd, Args: []*Node{text("cà phê"), &Node{Op: OpNot, Args: []*Node{text("đá")}}}},
		&Node{Op: OpAnd, Args: []*Node{onItem, &Node{Op: OpUnequal, Args: []*Node{{Op: OpColumn, Column: "note"}, {Op: OpNull}}}}},
	}}
	s := &Statement{Version: Version, Action: ActionList, Table: "orders", Where: where, Holes: []Hole{{Site: "router.work:2:40"}}}
	decoded, err := Decode(Encode(s))
	if err != nil {
		t.Fatal(err)
	}
	positive, negative := decoded.Where.Matches()
	if len(positive) != 2 || len(negative) != 1 || positive[1].Column != "item" {
		t.Fatalf("matches: %d positive (second on %q), %d negative; want 2, item, 1", len(positive), positive[1].Column, len(negative))
	}
	if got := strings.Join(decoded.Columns(), ","); got != "item,note" {
		t.Fatalf("columns = %s, want item,note", got)
	}
}

// shop is the catalog of the slice's schema, in declaration order.
func shop() *Database {
	return &Database{Export: "db", Tables: []Table{{Name: "orders", Columns: []Column{
		{Name: "id", Kind: "id", Primary: true, Generated: true},
		{Name: "email", Kind: "text"},
		{Name: "item", Kind: "text"},
		{Name: "amount", Kind: "decimal", Precision: 12, Scale: 2},
		{Name: "note", Kind: "text", Nullable: true},
		{Name: "day", Kind: "date"},
		{Name: "createdAt", Kind: "timestamp", WriteProtected: true},
	}}}}
}

// The rules both the compiler and the engine check names with list the real names in declaration order
// and never a value: an unknown table, an unknown column, every missing required column of an insert at
// once, and a column the engine fills.
func TestCatalogMessages(t *testing.T) {
	database := shop()
	if _, err := database.Table("order"); err == nil || err.Error() != "không có bảng order; bảng của db: orders" {
		t.Fatalf("Table(order) = %v", err)
	}
	orders, err := database.Table("orders")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := orders.Column("emial"); err == nil ||
		err.Error() != "bảng orders không có cột emial; cột: id, email, item, amount, note, day, createdAt" {
		t.Fatalf("Column(emial) = %v", err)
	}
	if err := orders.CheckInsert([]string{"amount"}); err == nil ||
		err.Error() != "insert vào orders thiếu cột bắt buộc: email, item, day (cột được bỏ trống khai .nullable())" {
		t.Fatalf("CheckInsert(amount) = %v", err)
	}
	if err := orders.CheckInsert([]string{"email", "item", "amount", "day", "createdAt"}); err == nil ||
		!strings.HasPrefix(err.Error(), "createdAt tự điền lúc ghi") {
		t.Fatalf("CheckInsert(createdAt) = %v", err)
	}
	if err := orders.CheckInsert([]string{"email", "item", "amount", "day"}); err != nil {
		t.Fatalf("CheckInsert without the nullable note: %v", err)
	}
	if err := database.CheckNames(&Statement{Version: Version, Action: ActionList, Table: "orders",
		Order: []Key{{Column: "amout"}}}); err == nil || !strings.Contains(err.Error(), "không có cột amout") {
		t.Fatalf("CheckNames of an order by amout = %v", err)
	}
}
