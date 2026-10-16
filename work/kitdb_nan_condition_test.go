package work

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/utilities/query"
	"github.com/kitwork/engine/value"
)

// NaN equals nothing and is ordered with nothing in a KitDB query, as in JavaScript (16/10/2026, the
// owner's E1): Number("abc") is NaN since E2, and kitDBCompareValues gives 0 for NaN beside any number,
// so where("age", "=", bad) matched every age, <= bad every row and in [bad] every age. A program's
// where() is read as flat conditions; != is true beside NaN, a missing age included, as null != NaN is
// in JavaScript. CONTROL: a number and null match as they did.
func TestKitDBWhereWithNaNMatchesNoNumber(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	source := `import { router, database } from "kitwork";
const { kitdb, struct, text, int } = database;
const people = struct({ id: text().key(), age: int() });
const db = kitdb("nan.kitdb", { people });
router.get((ctx) => {
  db.people.create({ id: "p1", age: 30 });
  db.people.create({ id: "p2", age: 5 });
  db.people.create({ id: "p3" });
  const bad = Number("abc");
  const count = (operator, item) => db.people.where("age", operator, item).count();
  return ctx.json({
    nan: isNaN(bad),
    flat: [count("=", bad), count("!=", bad), count(">=", bad), count("<=", bad), count(">", bad), count("<", bad),
      count("in", [bad]), count("not in", [bad]), db.people.where("age", bad).count(), count("=", parseInt("abc"))],
    control: [count("=", 30), count("=", null), count("!=", 30), count(">=", 5), count(">", 5), count("in", [5, 30])]
  });
});`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	defer tenant.Close()
	recorder := httptest.NewRecorder()
	tenant.Serve(recorder, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	want := `{"control":[1,1,2,2,1,2],"flat":[0,3,0,0,0,0,0,3,0,0],"nan":true}`
	if got := strings.TrimSpace(recorder.Body.String()); got != want {
		t.Fatalf("answer\n%s\nwant\n%s", got, want)
	}
}

// The predicates (a grouped condition, as KitSQL builds one) and IN and BETWEEN read NaN as the flat
// conditions do: =, <, >, <= and >= false, != true; a missing value stays unknown. KitSQL itself refuses
// a number that is not finite before it reaches them (kitdb SQL: invalid number), so its SQL reading is
// unchanged. Until 16/10/2026 NaN compared as 0: = NaN, >= NaN, <= NaN, IN [NaN] and BETWEEN NaN AND 40
// held for every age, and != NaN for none. CONTROL: a number.
func TestKitDBPredicateWithNaNMatchesNoNumber(t *testing.T) {
	rows := []value.Value{value.New(30), value.New(5), value.NewNil()}
	nan := value.ToNumber(value.New("abc"))
	count := func(operator string, item any) int {
		matched := 0
		for _, row := range rows {
			truth, err := kitDBEvaluatePredicateCondition(
				query.Condition{Column: "age", Operator: operator, Value: item},
				func(string) (value.Value, string, error) { return row, "integer", nil },
			)
			if err != nil {
				t.Fatalf("age %s %v: %v", operator, item, err)
			}
			if truth == kitDBTrue {
				matched++
			}
		}
		return matched
	}
	list := func(items ...value.Value) value.Value { return value.New(items) }
	for _, row := range []struct {
		operator string
		item     any
		want     int
	}{
		{"=", nan, 0},
		{"!=", nan, 2},
		{"<>", nan, 2},
		{">=", nan, 0},
		{"<=", nan, 0},
		{">", nan, 0},
		{"<", nan, 0},
		{"in", list(nan), 0},
		{"not in", list(nan), 2},
		{"between", list(nan, value.New(40)), 0},
		{"between", list(value.New(1), nan), 0},
		{"not between", list(nan, value.New(40)), 2},
		// CONTROL
		{"=", value.New(30), 1},
		{"!=", value.New(30), 1},
		{"in", list(value.New(5), nan), 1},
		{"between", list(value.New(1), value.New(10)), 1},
	} {
		if got := count(row.operator, row.item); got != row.want {
			t.Errorf("age %s %v matched %d rows, want %d", row.operator, value.New(row.item).Text(), got, row.want)
		}
	}
}
