package work

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/kitwork/engine/app"
	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/database/statement"
	"github.com/kitwork/engine/utilities/query"
	"github.com/kitwork/engine/value"
)

// managedShopSchema is the schema of KITWORK_DATABASE.md's first slice, as the spec writes it.
const managedShopSchema = `import { struct, id, text, decimal, date, timestamp } from "kitwork/schema";
import { connection } from "kitwork/database";

const orders = struct({ id: id(), email: text(), item: text(), amount: decimal(12, 2), note: text().nullable(), day: date().index(), createdAt: timestamp().created() });

export const db = connection().schema({ orders });
`

// managedRoute is a router.work whose POST inserts an order from the form's fields (amount as typed
// when exact=1, else as a number; an empty note is null) and whose GET runs get.
func managedRoute(get string) string {
	return `import * as router from "kitwork/router";
import { db } from "app/.shared/schema/shop.work";
import { desc, asc, one } from "kitwork/database";

router.post((context) => {
  const request = context.request;
  const email = request.form("email");
  const item = request.form("item");
  const amount = request.form("exact") === "1" ? request.form("amount") : Number(request.form("amount"));
  const day = request.form("day");
  const note = request.form("note") === "" ? null : request.form("note");
  return context.json(db.insert((orders) => ({ email, item, amount, day, note })));
});

router.get((context) => {
  const request = context.request;
  const today = "2026-10-22";
` + get + `
});
`
}

// managedSite is one site of an app whose router.work is managedRoute(get).
func managedSite(t *testing.T, get string) *Tenant {
	t.Helper()
	return newManagedApp(t, map[string]string{"shop.example/router.work": managedRoute(get)}).site("shop.example")
}

// seed inserts orders through the POST route, each email, item, amount, day, then the note and
// exact=1 when given; it gives their ids.
func seed(t *testing.T, tenant *Tenant, orders ...[]string) []string {
	t.Helper()
	var ids []string
	for _, order := range orders {
		form := url.Values{"email": {order[0]}, "item": {order[1]}, "amount": {order[2]}, "day": {order[3]}}
		if len(order) > 4 {
			form.Set("note", order[4])
		}
		if len(order) > 5 {
			form.Set("exact", order[5])
		}
		status, body := managedPost(tenant, "/", form)
		var created map[string]any
		if status != http.StatusOK || json.Unmarshal([]byte(body), &created) != nil || created["id"] == nil {
			t.Fatalf("insert %v = %d %s", order, status, body)
		}
		ids = append(ids, created["id"].(string))
	}
	return ids
}

// answer runs the GET route and reads its JSON object.
func answer(t *testing.T, tenant *Tenant, query string) map[string]any {
	t.Helper()
	status, body := managedGet(tenant, "/"+query)
	var result map[string]any
	if status != http.StatusOK || json.Unmarshal([]byte(body), &result) != nil {
		t.Fatalf("GET /%s = %d %s", query, status, body)
	}
	return result
}

// count is how many rows a list of an answer holds.
func count(t *testing.T, result map[string]any, key string) int {
	t.Helper()
	rows, ok := result[key].([]any)
	if !ok {
		t.Fatalf("%s is %#v, not a list", key, result[key])
	}
	return len(rows)
}

// managedApp is a host of many apps in a temp folder: the app acme with the shop schema and the files
// given (paths from the app's folder), its sites served by tenants that share one app runtime.
type managedApp struct {
	t       *testing.T
	root    string
	runtime *app.Runtime
	cache   *compiler.FileCache
}

func newManagedApp(t *testing.T, files map[string]string) *managedApp {
	t.Helper()
	root := t.TempDir()
	all := map[string]string{"acme/.shared/schema/shop.work": managedShopSchema}
	for name, source := range files {
		all["acme/"+name] = source
	}
	writeAppTree(t, root, all)
	runtime := app.NewRuntime("acme")
	t.Cleanup(runtime.Close)
	return &managedApp{t: t, root: root, runtime: runtime, cache: compiler.NewFileCache(filepath.Join(t.TempDir(), "bytecode"))}
}

// site boots the tenant of one domain of the app.
func (a *managedApp) site(domain string) *Tenant {
	a.t.Helper()
	siteRuntime, err := a.runtime.Site(a.root, domain)
	if err != nil {
		a.t.Fatal(err)
	}
	generation, err := siteRuntime.PrepareGeneration()
	if err != nil {
		a.t.Fatal(err)
	}
	if err := generation.SetBytecodeCache(a.cache); err != nil {
		a.t.Fatal(err)
	}
	tenant := NewTenantWithRuntimeLayout(a.root, domain, RootLayoutMultiTenant, a.runtime, siteRuntime, generation)
	a.t.Cleanup(tenant.Close)
	if err := tenant.Run(); err != nil {
		a.t.Fatalf("run %s: %v", domain, err)
	}
	return tenant
}

// managedGet serves GET path and gives the status and the body.
func managedGet(tenant *Tenant, path string) (int, string) {
	recorder := httptest.NewRecorder()
	tenant.Serve(recorder, httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil))
	return recorder.Code, recorder.Body.String()
}

// managedPost posts a form to path and gives the status and the body.
func managedPost(tenant *Tenant, path string, form url.Values) (int, string) {
	request := httptest.NewRequest(http.MethodPost, "http://localhost"+path, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()
	tenant.Serve(recorder, request)
	return recorder.Code, recorder.Body.String()
}

// rowsOf reads a JSON array of rows.
func rowsOf(t *testing.T, body string) []map[string]any {
	t.Helper()
	var rows []map[string]any
	if err := json.Unmarshal([]byte(body), &rows); err != nil {
		t.Fatalf("rows %q: %v", body, err)
	}
	return rows
}

// The app's database is declared once in .shared/schema/ and used by any site of the app with
// import { db } from "app/.shared/schema/shop.work": insert writes a row with its id and createdAt filled,
// list reads back the rows the condition keeps. The data lives in the app's folder,
// apps/<identity>/.database/application/, never in a site's .data/, and a second site of the app reads
// the same rows.
func TestManagedDatabaseListAndInsert(t *testing.T) {
	route := `import * as router from "kitwork/router";
import { db } from "app/.shared/schema/shop.work";

router.post((context) => {
  const email = context.request.form("email");
  const item = context.request.form("item");
  const amount = Number(context.request.form("amount"));
  const day = context.request.form("day");
  return context.json(db.insert((orders) => ({ email, item, amount, day })));
});
router.get((context) => {
  const day = context.request.query("day");
  return context.json(db.list((orders) => orders.day === day));
});
`
	shop := newManagedApp(t, map[string]string{
		"shop.example/router.work":  route,
		"other.example/router.work": route,
	})
	tenant := shop.site("shop.example")

	for _, order := range []url.Values{
		{"email": {"an@example.com"}, "item": {"cà phê"}, "amount": {"35000"}, "day": {"2026-10-22"}},
		{"email": {"binh@example.com"}, "item": {"trà"}, "amount": {"20000"}, "day": {"2026-10-22"}},
		{"email": {"chi@example.com"}, "item": {"bánh"}, "amount": {"15000"}, "day": {"2026-10-21"}},
	} {
		status, body := managedPost(tenant, "/", order)
		if status != http.StatusOK {
			t.Fatalf("POST %v = %d %s", order, status, body)
		}
		var created map[string]any
		if err := json.Unmarshal([]byte(body), &created); err != nil || created["id"] == nil || created["createdAt"] == nil ||
			created["email"] != order.Get("email") {
			t.Fatalf("POST %v gave %s, want the row with its id and createdAt", order, body)
		}
	}

	status, body := managedGet(tenant, "/?day=2026-10-22")
	if status != http.StatusOK {
		t.Fatalf("GET = %d %s", status, body)
	}
	rows := rowsOf(t, body)
	if len(rows) != 2 {
		t.Fatalf("today's orders: %d rows %s, want the 2 of 2026-10-22", len(rows), body)
	}
	for _, row := range rows {
		if row["day"] != "2026-10-22" || row["id"] == nil || row["createdAt"] == nil {
			t.Fatalf("row %v: want day 2026-10-22 with an id and a createdAt", row)
		}
	}

	// the app's folder holds the data, the site's folder nothing
	appData := filepath.Join(shop.root, "acme", ".database", "application", "application.kitdb")
	if _, err := os.Stat(appData); err != nil {
		t.Fatalf("the app's database is not at %s: %v", appData, err)
	}
	if _, err := os.Stat(filepath.Join(shop.root, "acme", "shop.example", ".data")); !os.IsNotExist(err) {
		t.Fatalf("the site's .data/ exists (%v): the app's database lives in the app's folder", err)
	}
	if status, _ := managedGet(tenant, "/.database/application/application.kitdb"); status == http.StatusOK {
		t.Fatalf("the app's database file is served")
	}

	// another site of the app reads the same rows
	other := shop.site("other.example")
	if _, body := managedGet(other, "/?day=2026-10-22"); len(rowsOf(t, body)) != 2 {
		t.Fatalf("other.example reads %s, want the same 2 rows", body)
	}
}

// A condition keeps both sides of && and ||: the statement is translated when it compiles, never run on a
// proxy of the row, where a proxy is always true and a && b kept b alone (the request-time path of the
// older spelling still does; KITWORK_DATABASE.md mục 9). A value from outside in the place of a condition
// is true or false by JavaScript's truthiness; null written in the lambda is IS NULL, and a null from
// outside matches no row either way (a session.userId that is null never reads every row, mục 4).
func TestManagedDatabaseCompoundConditions(t *testing.T) {
	tenant := managedSite(t, `  const onlyPaid = request.query("only") === "1";
  const missing = null;
  return context.json({
    and: db.list((orders) => orders.day === today && orders.amount > 5),
    or: db.list((orders) => orders.day === today || orders.amount > 5),
    not: db.list((orders) => !(orders.amount > 5)),
    isNull: db.list((orders) => orders.note === null),
    notNull: db.list((orders) => orders.note !== null),
    nullHole: db.list((orders) => orders.note === missing),
    notNullHole: db.list((orders) => orders.note !== missing),
    paid: db.list((orders) => !onlyPaid || orders.amount > 5)
  });`)
	seed(t, tenant,
		[]string{"a@example.com", "trà", "3", "2026-10-22"},
		[]string{"b@example.com", "cà phê", "9", "2026-10-22"},
		[]string{"c@example.com", "bánh", "9", "2026-10-21"},
		[]string{"d@example.com", "nước", "1", "2026-10-20", "gọi trước"},
	)
	result := answer(t, tenant, "?only=0")
	for key, want := range map[string]int{"and": 1, "or": 3, "not": 2, "isNull": 3, "notNull": 1, "nullHole": 0, "notNullHole": 0, "paid": 4} {
		if got := count(t, result, key); got != want {
			t.Errorf("%s: %d rows, want %d", key, got, want)
		}
	}
	if got := count(t, answer(t, tenant, "?only=1"), "paid"); got != 2 {
		t.Errorf("!onlyPaid || amount > 5 with onlyPaid: %d rows, want 2", got)
	}
}

// A decimal is stored as its exact text and compares as a number: "35000" > "5" as text is false, so
// amount > 5 and orderBy(desc(amount)) gave wrong rows without a word. one(…) (IN) compares the same way.
func TestManagedDatabaseDecimalCompareAndOrder(t *testing.T) {
	tenant := managedSite(t, `  const amounts = [35000];
  return context.json({
    big: db.list((orders) => orders.amount > 5),
    exact: db.list((orders) => orders.amount === 35000),
    among: db.list((orders) => orders.amount === one(amounts)),
    notAmong: db.list((orders) => orders.amount !== one(amounts)),
    ordered: db.orderBy((orders) => desc(orders.amount)).list((orders) => orders).map((row) => row.amount)
  });`)
	seed(t, tenant,
		[]string{"a@example.com", "a", "5", "2026-10-22"},
		[]string{"b@example.com", "b", "35000", "2026-10-22"},
		[]string{"c@example.com", "c", "100.5", "2026-10-22"},
		[]string{"d@example.com", "d", "35000.00", "2026-10-22", "", "1"},
	)
	result := answer(t, tenant, "")
	if got := count(t, result, "big"); got != 3 {
		t.Errorf("amount > 5: %d rows, want 3", got)
	}
	if got := count(t, result, "exact"); got != 2 {
		t.Errorf("amount === 35000: %d rows, want 2 (35000 and 35000.00)", got)
	}
	if got := count(t, result, "among"); got != 2 {
		t.Errorf("amount === one([35000]): %d rows, want 2 (35000 and 35000.00)", got)
	}
	if got := count(t, result, "notAmong"); got != 2 {
		t.Errorf("amount !== one([35000]): %d rows, want 2 (5 and 100.5)", got)
	}
	if got := result["ordered"]; !reflect.DeepEqual(got, []any{35000.0, 35000.0, 100.5, 5.0}) {
		t.Errorf("desc(amount): %v, want [35000 35000 100.5 5]", got)
	}
}

// A decimal compares digit by digit, whatever its exponent: "1e999999" is past every amount and
// "1e-999999" short of every one above zero, and a comparison with either costs what its text is long,
// never 10^999999 built in memory (big.Rat took about 19 ms and 400 KB a comparison, so one such value
// in a filter or a row made every scan pay that for each row it read).
func TestKitDBDecimalCompare(t *testing.T) {
	for _, test := range []struct {
		left, right string
		want        int
	}{
		{"35000", "35000.00", 0}, {"35000", "5", 1}, {"5", "35000", -1}, {"100.5", "100.50", 0},
		{"0.05", "0.5", -1}, {".5", "0.5", 0}, {"5.", "5", 0}, {"1e3", "1000", 0}, {"1E+3", "999.999", 1},
		{"-0", "0", 0}, {"+5", "5", 0}, {"0012.3400", "12.34", 0}, {"-5", "-35000", 1}, {"-0.1", "0", -1},
		{"1e999999", "35000", 1}, {"1e-999999", "0", 1}, {"1e-999999", "0.0001", -1}, {"-1e999999", "-5", -1},
		{"1e0000000000000000000003", "1000", 0},
	} {
		if got := kitDBCompareKind("decimal", value.New(test.left), value.New(test.right)); got != test.want {
			t.Errorf("%s against %s: %d, want %d", test.left, test.right, got, test.want)
		}
		if got := kitDBCompareKind("decimal", value.New(test.right), value.New(test.left)); got != -test.want {
			t.Errorf("%s against %s: %d, want %d", test.right, test.left, got, -test.want)
		}
	}
	// a number of the VM compares as the text a write stores it as
	if got := kitDBCompareKind("decimal", value.New(0.1), value.New("0.10")); got != 0 {
		t.Errorf("0.1 against \"0.10\": %d, want 0", got)
	}
	// a text that does not read as a decimal compares as text
	if got := kitDBCompareKind("decimal", value.New("abc"), value.New("abd")); got != -1 {
		t.Errorf("abc against abd: %d, want -1 (as text)", got)
	}

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for index := 0; index < 20; index++ {
		kitDBCompareKind("decimal", value.New("1e999999"), value.New("35000"))
		kitDBCompareKind("decimal", value.New("1e-999999"), value.New("0.0001"))
	}
	runtime.ReadMemStats(&after)
	// big.Rat allocated some 3 MB a comparison; the bound leaves room for what other goroutines allocate
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 1<<20 {
		t.Fatalf("40 comparisons with an exponent of a million allocated %d bytes, want under 1 MiB", allocated)
	}

	// the flat conditions of the older query builder, IN and BETWEEN compare a decimal the same way
	table := &SchemaTable{table: "orders", columns: map[string]*ColumnSpec{"amount": {kind: "decimal"}}}
	row := map[string]value.Value{"amount": value.New("35000.00")}
	for _, condition := range []query.Condition{
		{Column: "amount", Operator: "=", Value: value.New(35000)},
		{Column: "amount", Operator: ">", Value: value.New(5)},
		{Column: "amount", Operator: ">=", Value: value.New("35000")},
		{Column: "amount", Operator: "<", Value: value.New(100000)},
		{Column: "amount", Operator: "<=", Value: value.New("3.5e4")},
		{Column: "amount", Operator: "<>", Value: value.New(5)},
	} {
		if matched, err := table.kitDBConditionMatches(row, condition); err != nil || !matched {
			t.Errorf("35000.00 %s %v: %v %v, want a match", condition.Operator, condition.Value, matched, err)
		}
	}
	if truth, err := kitDBEvaluateIn(value.New("35000.00"), "decimal", value.New([]value.Value{value.New(1), value.New(35000)})); err != nil || truth != kitDBTrue {
		t.Errorf("35000.00 in [1, 35000]: %v %v, want true", truth, err)
	}
	if truth, err := kitDBEvaluateBetween(value.New("35000.00"), "decimal", value.New([]value.Value{value.New(5), value.New(100000)})); err != nil || truth != kitDBTrue {
		t.Errorf("35000.00 between 5 and 100000: %v %v, want true", truth, err)
	}
	if truth, err := kitDBEvaluateBetween(value.New("5"), "decimal", value.New([]value.Value{value.New(10), value.New(100)})); err != nil || truth != kitDBFalse {
		t.Errorf("5 between 10 and 100: %v %v, want false", truth, err)
	}
}

// An index finds a decimal by its stored text, a condition compares it as a number: the same equality
// gives the same rows whether the column has an index or not ("35000" and "35000.00" are one amount).
func TestManagedDatabaseDecimalIndexAgreesWithScan(t *testing.T) {
	schema := strings.Replace(managedShopSchema, "amount: decimal(12, 2)", "amount: decimal(12, 2).index()", 1)
	shop := newManagedApp(t, map[string]string{
		".shared/schema/shop.work": schema,
		"shop.example/router.work": managedRoute(`  return context.json({
    indexed: db.list((orders) => orders.amount === 35000),
    scanned: db.list((orders) => orders.amount === 35000 || orders.amount === 1),
    among: db.list((orders) => orders.amount === one([35000]))
  });`),
	})
	tenant := shop.site("shop.example")
	seed(t, tenant,
		[]string{"a@example.com", "a", "35000", "2026-10-22"},
		[]string{"b@example.com", "b", "35000.00", "2026-10-22", "", "1"},
		[]string{"c@example.com", "c", "7", "2026-10-22"},
	)
	result := answer(t, tenant, "")
	for _, key := range []string{"indexed", "scanned", "among"} {
		if got := count(t, result, key); got != 2 {
			t.Errorf("%s: %d rows, want 2 (35000 and 35000.00)", key, got)
		}
	}
}

// === is equality whatever the text holds: "100%" is that text, not a pattern (the older query builder
// read a % as LIKE). one(array) is IN: an empty array matches nothing, !== one([]) everything, and
// anything but an array is an error naming where it is written, never the value.
func TestManagedDatabaseEqualityIsNotLikeAndOne(t *testing.T) {
	tenant := managedSite(t, `  const q = "100%";
  const ids = request.query("id") === "" ? [] : [request.query("id")];
  const none = [];
  const notArray = "x";
  const wrong = safe(() => db.list((orders) => orders.id === one(notArray)));
  return context.json({
    literal: db.list((orders) => orders.item === "100%"),
    value: db.list((orders) => orders.item === q),
    picked: db.list((orders) => orders.id === one(ids)),
    nothing: db.list((orders) => orders.id === one(none)),
    everything: db.list((orders) => orders.id !== one(none)),
    code: wrong.error.code,
    message: wrong.error.message
  });`)
	ids := seed(t, tenant,
		[]string{"a@example.com", "100%", "1", "2026-10-22"},
		[]string{"b@example.com", "1000", "1", "2026-10-22"},
	)
	result := answer(t, tenant, "?id="+ids[0])
	for key, want := range map[string]int{"literal": 1, "value": 1, "picked": 1, "nothing": 0, "everything": 2} {
		if got := count(t, result, key); got != want {
			t.Errorf("%s: %d rows, want %d", key, got, want)
		}
	}
	message, _ := result["message"].(string)
	if result["code"] != "INVALID_ARGUMENT" || !strings.Contains(message, "one(…) ở router.work:") ||
		!strings.Contains(message, "nhận một mảng") || strings.Contains(message, `"x"`) {
		t.Errorf("one(notArray): %v %q", result["code"], message)
	}
}

// orderBy then list(n) keeps the first n in that order; list({ page, limit }) a page of it. A list
// without a limit gives 1000 rows (KITWORK_DATABASE.md mục 2), not the 60 or 120 of the older query
// builder, and list(10000) all it asks for; a limit past 10000, and a bound that is not a number, are
// errors.
func TestManagedDatabaseOrderLimitPage(t *testing.T) {
	tenant := managedSite(t, `  if (request.query("fill") === "1") {
    for (let index = 0; index < 1001; index++) {
      const email = "many@example.com";
      const item = "item";
      const day = "2026-10-20";
      db.insert((orders) => ({ email, item, amount: index, day }));
    }
  }
  const ceiling = 10000;
  const tooMany = 10001;
  const text = "x";
  return context.json({
    top: db.where((orders) => orders.day === today).orderBy((orders) => desc(orders.amount)).list(2).map((row) => row.amount),
    page: db.where((orders) => orders.day === today).orderBy((orders) => desc(orders.amount)).list({ page: 2, limit: 1 }).map((row) => row.amount),
    many: db.where((orders) => orders.day === "2026-10-20").list(500).length,
    all: db.list((orders) => orders.day === "2026-10-20").length,
    ceiling: db.where((orders) => orders.day === "2026-10-20").list(ceiling).length,
    tooMany: safe(() => db.where((orders) => orders).list(tooMany)).error,
    notANumber: safe(() => db.where((orders) => orders.day === today).list(text)).error
  });`)
	seed(t, tenant,
		[]string{"a@example.com", "a", "3", "2026-10-22"},
		[]string{"b@example.com", "b", "9", "2026-10-22"},
		[]string{"c@example.com", "c", "7", "2026-10-22"},
	)
	result := answer(t, tenant, "?fill=1")
	if !reflect.DeepEqual(result["top"], []any{9.0, 7.0}) {
		t.Errorf("orderBy(desc(amount)).list(2): %v, want [9 7]", result["top"])
	}
	if !reflect.DeepEqual(result["page"], []any{7.0}) {
		t.Errorf("list({ page: 2, limit: 1 }): %v, want [7]", result["page"])
	}
	if result["many"] != 500.0 || result["all"] != 1000.0 || result["ceiling"] != 1001.0 {
		t.Errorf("of 1001 rows list(500) gave %v, list() %v and list(10000) %v rows, want 500, 1000 and 1001",
			result["many"], result["all"], result["ceiling"])
	}
	for key, want := range map[string]string{"tooMany": "quá 10000 dòng: đọc theo trang, list({ page, limit })", "notANumber": "nhận số hay { page, limit }"} {
		failure, _ := result[key].(map[string]any)
		message, _ := failure["message"].(string)
		if failure["code"] != "INVALID_ARGUMENT" || !strings.Contains(message, want) {
			t.Errorf("%s: %v, want INVALID_ARGUMENT %q", key, result[key], want)
		}
	}
}

// A page from the request is read without setting aside room for the rows before it: a page far past the
// rows gives [] at the cost of the rows there are, and a page past what an int32 counts is INVALID_ARGUMENT
// naming where it is written. A read used to make room for (page-1)*limit rows up front, so one GET of
// ?page=2000000000 killed the process, out of memory, past any recover.
func TestManagedDatabaseFarPage(t *testing.T) {
	tenant := managedSite(t, `  const page = Number(request.query("page"));
  const ordered = safe(() => db.orderBy((orders) => desc(orders.createdAt)).list({ page, limit: 100 }));
  const plain = safe(() => db.where((orders) => orders).list({ page, limit: 100 }));
  return context.json({
    ordered: ordered.ok ? ordered.value.length : ordered.error,
    plain: plain.ok ? plain.value.length : plain.error
  });`)
	seed(t, tenant, []string{"a@example.com", "a", "3", "2026-10-22"})
	if result := answer(t, tenant, "?page=1"); result["ordered"] != 1.0 || result["plain"] != 1.0 {
		t.Fatalf("page 1: %v, want the one row twice", result)
	}

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	result := answer(t, tenant, "?page=50001")
	runtime.ReadMemStats(&after)
	if result["ordered"] != 0.0 || result["plain"] != 0.0 {
		t.Fatalf("page 50001: %v, want no rows twice", result)
	}
	// room for 5 000 000 rows was some 280 MB a read
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 64<<20 {
		t.Fatalf("page 50001 of a table of one row allocated %d MB, want under 64 MB", allocated>>20)
	}

	result = answer(t, tenant, "?page=2000000000")
	for _, key := range []string{"ordered", "plain"} {
		failure, _ := result[key].(map[string]any)
		message, _ := failure["message"].(string)
		if failure["code"] != "INVALID_ARGUMENT" || !strings.HasPrefix(message, "page ở router.work:") ||
			!strings.HasSuffix(message, " quá lớn: trang bắt đầu sau 2147483647 dòng") {
			t.Errorf("%s at page 2000000000: %v", key, result[key])
		}
	}
}

// first gives the row or null; count gives a number, said either way.
func TestManagedDatabaseFirstAndCount(t *testing.T) {
	tenant := managedSite(t, `  const id = request.query("id");
  const missing = "khong-co";
  return context.json({
    found: db.first((orders) => orders.id === id),
    missing: db.first((orders) => orders.id === missing),
    today: db.count((orders) => orders.day === today),
    described: db.where((orders) => orders.day === today).count()
  });`)
	ids := seed(t, tenant,
		[]string{"a@example.com", "a", "3", "2026-10-22"},
		[]string{"b@example.com", "b", "9", "2026-10-22"},
		[]string{"c@example.com", "c", "7", "2026-10-21"},
	)
	result := answer(t, tenant, "?id="+ids[1])
	found, _ := result["found"].(map[string]any)
	if found["id"] != ids[1] || found["email"] != "b@example.com" {
		t.Errorf("first(id === x): %v", result["found"])
	}
	if result["missing"] != nil {
		t.Errorf("first of nothing: %v, want null", result["missing"])
	}
	if result["today"] != 2.0 || result["described"] != 2.0 {
		t.Errorf("count: %v and %v, want 2 each", result["today"], result["described"])
	}
}

// A value of the wrong kind for its column is INVALID_ARGUMENT naming the column and where the value is
// written, never the value, before anything is written; safe(() => …) reads it as { ok: false }.
func TestManagedDatabaseKindsAndInsertErrors(t *testing.T) {
	tenant := managedSite(t, `  const x = "abc";
  const email = null;
  const compared = safe(() => db.list((orders) => orders.amount > x));
  const inserted = safe(() => db.insert((orders) => ({ email, item: "a", amount: 1, day: today })));
  return context.json({
    compared: compared.error,
    inserted: { ok: inserted.ok, code: inserted.error.code, message: inserted.error.message },
    rows: db.count((orders) => orders)
  });`)
	seed(t, tenant, []string{"a@example.com", "a", "3", "2026-10-22"})
	result := answer(t, tenant, "")
	compared, _ := result["compared"].(map[string]any)
	message, _ := compared["message"].(string)
	if compared["code"] != "INVALID_ARGUMENT" || !strings.Contains(message, "cột amount là decimal(12, 2): giá trị ở router.work:") ||
		strings.Contains(message, "abc") {
		t.Errorf("amount > x with x a text: %v", result["compared"])
	}
	inserted, _ := result["inserted"].(map[string]any)
	message, _ = inserted["message"].(string)
	if inserted["ok"] != false || inserted["code"] != "INVALID_ARGUMENT" || !strings.Contains(message, "cột email của orders không nhận null") {
		t.Errorf("insert with email null: %v", result["inserted"])
	}
	if result["rows"] != 1.0 {
		t.Errorf("rows after the refused insert: %v, want 1", result["rows"])
	}
}

// A text that is not UTF-8 is a value of the wrong kind like any other: INVALID_ARGUMENT naming the column
// and where the value is written, never the value, and nothing is written. A form sent in another encoding
// brings one: Git Bash's curl on Windows sends "cà phê sữa" in the ANSI code page, item=c%E0+ph%EA+s%3Fa
// (22/10/2026). It went past the check to the storage, whose row encoder refused it as DATABASE in KitDB's
// words ("kitdb: encode row field "item": string is not valid UTF-8"); the /database demo answered 200 with
// it, and three such posts read as rows written that the next page did not show.
func TestManagedDatabaseRefusesTextThatIsNotUTF8(t *testing.T) {
	route := `import * as router from "kitwork/router";
import { db } from "app/.shared/schema/shop.work";

router.post((context) => {
  const email = context.request.form("email");
  const item = context.request.form("item");
  const amount = Number(context.request.form("amount"));
  const day = context.request.form("day");
  const inserted = safe(() => db.insert((orders) => ({ email, item, amount, day })));
  return context.json({ ok: inserted.ok, error: inserted.error });
});
router.get((context) => {
  const item = context.request.query("item");
  const found = safe(() => db.list((orders) => orders.item === item));
  return context.json({ ok: found.ok, error: found.error, rows: db.count((orders) => orders) });
});
`
	tenant := newManagedApp(t, map[string]string{"shop.example/router.work": route}).site("shop.example")
	refused := func(what string, body string) {
		t.Helper()
		var result struct {
			OK    bool `json:"ok"`
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(body), &result); err != nil {
			t.Fatalf("%s: %v %s", what, err, body)
		}
		if result.OK || result.Error.Code != value.InvalidArgumentCode ||
			!strings.Contains(result.Error.Message, "cột item là text(): giá trị ở router.work:") ||
			!strings.Contains(result.Error.Message, "không phải chuỗi UTF-8") ||
			strings.Contains(result.Error.Message, "s?a") || strings.ContainsRune(result.Error.Message, utf8.RuneError) {
			t.Errorf("%s: %s, want INVALID_ARGUMENT naming the column item and where it is written, not the value", what, body)
		}
	}

	// the bytes the caller's curl sent: "cà phê sữa" in Windows-1252, ữ already a ?
	latin := "c\xe0 ph\xea s?a"
	if utf8.ValidString(latin) || (url.Values{"item": {latin}}).Encode() != "item=c%E0+ph%EA+s%3Fa" {
		t.Fatalf("CONTROL: the form is not the one curl sent")
	}
	if status, body := managedPost(tenant, "/", url.Values{
		"email": {"an@example.com"}, "item": {"cà phê sữa"}, "amount": {"35000"}, "day": {"2026-10-22"},
	}); status != http.StatusOK || !strings.Contains(body, `"ok":true`) {
		t.Fatalf("CONTROL: the same order in UTF-8 = %d %s", status, body)
	}
	_, body := managedPost(tenant, "/", url.Values{
		"email": {"binh@example.com"}, "item": {latin}, "amount": {"62000"}, "day": {"2026-10-22"},
	})
	refused("insert", body)

	// a condition takes the rule a write follows: such a text is no value of the column
	_, body = managedGet(tenant, "/?item=c%E0")
	refused("condition", body)
	var after struct {
		Rows float64 `json:"rows"`
	}
	if _, body := managedGet(tenant, "/?item=x"); json.Unmarshal([]byte(body), &after) != nil || after.Rows != 1 {
		t.Fatalf("rows after the refused insert: %s, want the 1 written in UTF-8", body)
	}
}

// goSchema builds, in Go, the tables a schema file builds with kitwork/schema: name → columns in
// declaration order, each a builder of the module and its modifiers.
type goColumn struct {
	name      string
	builder   string
	arguments []value.Value
	modifiers []string
}

func goStruct(t *testing.T, columns ...goColumn) value.Value {
	t.Helper()
	schema := &SchemaModule{}
	builders := map[string]value.Value{
		"id": schema.Id(), "text": schema.Text(), "integer": schema.Integer(), "float": schema.Float(),
		"decimal": schema.Decimal(), "boolean": schema.Boolean(), "date": schema.Date(), "json": schema.Json(),
		"timestamp": schema.Timestamp(),
	}
	fields := map[string]value.Value{}
	for _, column := range columns {
		built := builders[column.builder].Call(column.builder, column.arguments...)
		for _, modifier := range column.modifiers {
			built = built.Invoke(modifier)
		}
		fields[column.name] = built
	}
	defined := schema.Struct().Call("struct", value.New(fields))
	if defined.K != value.Proxy {
		t.Fatalf("struct: %v", defined)
	}
	return defined
}

// goOrders is the orders table of the slice's schema.
func goOrders(t *testing.T) value.Value {
	return goStruct(t,
		goColumn{name: "id", builder: "id"},
		goColumn{name: "email", builder: "text"},
		goColumn{name: "item", builder: "text"},
		goColumn{name: "amount", builder: "decimal", arguments: []value.Value{value.New(12), value.New(2)}},
		goColumn{name: "note", builder: "text", modifiers: []string{"nullable"}},
		goColumn{name: "day", builder: "date", modifiers: []string{"index"}},
		goColumn{name: "createdAt", builder: "timestamp", modifiers: []string{"created"}},
	)
}

// goManaged declares connection().schema(tables) for tenant, as a schema file does.
func goManaged(t *testing.T, tenant *Tenant, tables map[string]value.Value) *ManagedDatabase {
	t.Helper()
	declared := (&ConnectionDeclaration{tenant: tenant}).OnInvoke("schema", value.New(tables))
	managed, ok := declared.V.(*ManagedDatabase)
	if !ok {
		t.Fatalf("connection().schema: %v", declared)
	}
	return managed
}

// failureOf reads a failure's code and message.
func failureOf(item value.Value) (string, string) {
	failure, ok := value.FailureFrom(item)
	if !ok {
		return "", ""
	}
	return failure.Code, failure.Message
}

// A statement is checked against the live schema when it first runs, before anything opens: a template
// naming a column the table has not, one with a literal of the wrong kind, and a call with too few
// values are refused, and the app's .database/ is never made.
func TestManagedDatabaseRuntimeValidationBeforeOpen(t *testing.T) {
	shop := newManagedApp(t, map[string]string{"shop.example/router.work": `import * as router from "kitwork/router";`})
	tenant := shop.site("shop.example")
	managed := goManaged(t, tenant, map[string]value.Value{"orders": goOrders(t)})
	for template, want := range map[string]string{
		`{"v":1,"action":"list","table":"orders","where":{"op":"===","args":[{"op":"column","column":"x"},{"op":"literal","value":1}]}}`:                                    "bảng orders không có cột x; cột: id, email, item, amount, note, day, createdAt",
		`{"v":1,"action":"list","table":"orders","where":{"op":">","args":[{"op":"column","column":"amount"},{"op":"literal","value":"abc"}]}}`:                             "cột amount là decimal(12, 2): giá trị ở trong lambda không phải số hay chuỗi thập phân",
		`{"v":1,"action":"list","table":"orders","where":{"op":"===","args":[{"op":"column","column":"day"},{"op":"hole","hole":0}]},"holes":[{"site":"router.work:3:1"}]}`: "câu database nhận 1 giá trị, có 0: dịch lại",
		`{"v":1,"action":"list","table":"users"}`: "không có bảng users; bảng của database: orders",
		// an insert the compiler did not see (a stale artifact, a forged template) meets the same rules
		`{"v":1,"action":"insert","table":"orders","values":[{"column":"email","value":{"op":"literal","value":"a"}},{"column":"item","value":{"op":"literal","value":"b"}},{"column":"amount","value":{"op":"literal","value":1}},{"column":"day","value":{"op":"literal","value":"2026-10-22"}},{"column":"createdAt","value":{"op":"literal","value":"2026-10-22T00:00:00Z"}}]}`: "createdAt tự điền lúc ghi (timestamp().created() hay .updated()), không viết trong dữ liệu",
		`{"v":1,"action":"insert","table":"orders","values":[{"column":"amount","value":{"op":"literal","value":1}},{"column":"day","value":{"op":"literal","value":"2026-10-22"}}]}`:                                                                                                                                                                                               "insert vào orders thiếu cột bắt buộc: email, item (cột được bỏ trống khai .nullable())",
	} {
		code, message := failureOf(managed.OnInvoke(statement.Method, value.New(template)))
		if code != value.InvalidArgumentCode || message != want {
			t.Errorf("%s:\n  got  %s %q\n  want INVALID_ARGUMENT %q", template, code, message, want)
		}
	}
	if _, err := os.Stat(filepath.Join(shop.root, "acme", ".database")); !os.IsNotExist(err) {
		t.Fatalf("the app's .database/ was made (%v): a refused statement opened the storage", err)
	}
}

// A call the compiler did not translate reaches the handle by its own name (db.list(lambda)): it is
// refused, and its lambda never runs; reading a table off the handle is refused with the spec's spelling.
func TestManagedDatabaseRefusesUntranslatedCalls(t *testing.T) {
	shop := newManagedApp(t, map[string]string{"shop.example/router.work": `import * as router from "kitwork/router";`})
	managed := goManaged(t, shop.site("shop.example"), map[string]value.Value{"orders": goOrders(t)})
	ran := 0
	lambda := value.NewFunc(func(_ ...value.Value) value.Value {
		ran++
		return value.New(true)
	})
	code, message := failureOf(managed.OnInvoke("list", lambda))
	if code != value.UnsupportedCode || !strings.HasPrefix(message, "db.list(…) chỉ chạy khi viết thẳng trên tên import từ app/.shared/schema/") {
		t.Errorf("db.list(lambda): %s %q", code, message)
	}
	if ran != 0 {
		t.Errorf("the lambda ran %d times", ran)
	}
	code, message = failureOf(managed.OnGet("orders"))
	if code != value.InvalidArgumentCode || message != "db.orders không có: câu viết db.list((bảng) => …), bảng là tham số của lambda" {
		t.Errorf("db.orders: %s %q", code, message)
	}
}

// The compiler reads the schema file's struct({ … }) literals; the engine sees what their builders made.
// Both give the same tables, columns, order and kinds, so a name the compiler let through is one the
// engine knows.
func TestManagedDatabaseCatalogParity(t *testing.T) {
	schema := `import { struct, id, text, integer, float, decimal, boolean, date, json, timestamp } from "kitwork/schema";
import { connection } from "kitwork/database";

const users = struct({ id: id(), email: text().unique(), name: text().search(), role: text().default("customer"), active: boolean().default(true), createdAt: timestamp().created(), updatedAt: timestamp().updated() });
const orders = struct({ id: id(), item: text(), quantity: integer(), weight: float().nullable(), amount: decimal(12, 2).index("day_amount"), note: text().nullable(), day: date().index().index("day_amount"), meta: json().nullable(), createdAt: timestamp().created() });

export const db = connection().schema({ users, orders });
`
	shop := newManagedApp(t, map[string]string{
		".shared/schema/shop.work": schema,
		"shop.example/router.work": "import * as router from \"kitwork/router\";\nimport { db } from \"app/.shared/schema/shop.work\";\nrouter.get((context) => context.json(db.count((orders) => orders)));\n",
	})
	tenant := shop.site("shop.example")
	appRoot := filepath.Join(shop.root, "acme")
	compiled, err := compiler.SchemaCatalog(appRoot, filepath.Join(appRoot, ".shared", "schema", "shop.work"))
	if err != nil {
		t.Fatal(err)
	}
	schemaRegMu.Lock()
	proxy := schemaReg[tenantScopeKey(tenant)+"|kitdb|"+kitDBApplicationName]
	schemaRegMu.Unlock()
	if proxy == nil {
		t.Fatal("the schema file's database is not registered")
	}
	live := catalogOf(proxy)
	live.Export = "db"
	byName := func(database *statement.Database) *statement.Database {
		sorted := *database
		sorted.Tables = append([]statement.Table(nil), database.Tables...)
		sort.Slice(sorted.Tables, func(left, right int) bool { return sorted.Tables[left].Name < sorted.Tables[right].Name })
		return &sorted
	}
	if want, got := byName(compiled["db"]), byName(live); !reflect.DeepEqual(got, want) {
		t.Fatalf("the engine's catalog:\n%#v\nthe compiler's:\n%#v", got, want)
	}
}

// The same statements run on SQLite give the rows KitDB gives: the template is storage-neutral, and the
// SQL backends render its grouped condition (query.whereClause), every value an argument.
func TestManagedDatabaseSQLiteParity(t *testing.T) {
	shop := newManagedApp(t, map[string]string{"shop.example/router.work": `import * as router from "kitwork/router";`})
	tenant := shop.site("shop.example")
	items := func() value.Value {
		return goStruct(t,
			goColumn{name: "id", builder: "id"},
			goColumn{name: "name", builder: "text"},
			goColumn{name: "n", builder: "integer"},
			goColumn{name: "day", builder: "date"},
			goColumn{name: "note", builder: "text", modifiers: []string{"nullable"}},
		)
	}
	kitdb := goManaged(t, tenant, map[string]value.Value{"items": items()})
	declared := declareDbProxy(tenant, nil, "sqlite", value.New("parity.db"), value.New(map[string]value.Value{"items": items()}))
	sqlite := newManagedDatabase(declared.V.(*dbProxy))

	injection := "' OR 1=1 --"
	inserts := [][]value.Value{
		{value.New("a"), value.New(1), value.New("2026-10-22"), value.NewNil()},
		{value.New("b"), value.New(5), value.New("2026-10-22"), value.New("x")},
		{value.New("c"), value.New(9), value.New("2026-10-21"), value.NewNil()},
		{value.New(injection), value.New(2), value.New("2026-10-22"), value.NewNil()},
	}
	insert := `{"v":1,"action":"insert","table":"items","values":[{"column":"name","value":{"op":"hole","hole":0}},{"column":"n","value":{"op":"hole","hole":1}},{"column":"day","value":{"op":"hole","hole":2}},{"column":"note","value":{"op":"hole","hole":3}}],"holes":[{"site":"a"},{"site":"b"},{"site":"c"},{"site":"d"}]}`
	for _, database := range []*ManagedDatabase{kitdb, sqlite} {
		for _, row := range inserts {
			if created := database.OnInvoke(statement.Method, append([]value.Value{value.New(insert)}, row...)...); created.IsError {
				t.Fatalf("insert: %v", created)
			}
		}
	}
	for _, test := range []struct {
		template string
		holes    []value.Value
		// want is the rows' names in order, or the count
		want string
	}{
		{`{"v":1,"action":"list","table":"items","where":{"op":"&&","args":[{"op":"===","args":[{"op":"column","column":"day"},{"op":"hole","hole":0}]},{"op":"||","args":[{"op":"<","args":[{"op":"column","column":"n"},{"op":"hole","hole":1}]},{"op":"!","args":[{"op":"===","args":[{"op":"column","column":"note"},{"op":"null"}]}]}]}]},"order":[{"column":"n","desc":true}],"holes":[{"site":"a"},{"site":"b"}]}`,
			[]value.Value{value.New("2026-10-22"), value.New(2)}, "b, a"},
		{`{"v":1,"action":"list","table":"items","where":{"op":"===","args":[{"op":"column","column":"name"},{"op":"hole","hole":0}]},"holes":[{"site":"a"}]}`,
			[]value.Value{value.New(injection)}, injection},
		{`{"v":1,"action":"list","table":"items","where":{"op":"===","args":[{"op":"column","column":"n"},{"op":"one","args":[{"op":"hole","hole":0}]}]},"order":[{"column":"n"}],"holes":[{"site":"a"}]}`,
			[]value.Value{value.New([]value.Value{value.New(1), value.New(9)})}, "a, c"},
		{`{"v":1,"action":"count","table":"items","where":{"op":">=","args":[{"op":"column","column":"n"},{"op":"literal","value":2}]}}`, nil, "3"},
		{`{"v":1,"action":"first","table":"items","where":{"op":"!==","args":[{"op":"column","column":"note"},{"op":"null"}]}}`, nil, "b"},
		// a null from outside matches no row, with === or !==
		{`{"v":1,"action":"list","table":"items","where":{"op":"===","args":[{"op":"column","column":"note"},{"op":"hole","hole":0}]},"holes":[{"site":"a"}]}`,
			[]value.Value{value.NewNil()}, ""},
		{`{"v":1,"action":"list","table":"items","where":{"op":"!==","args":[{"op":"column","column":"note"},{"op":"hole","hole":0}]},"holes":[{"site":"a"}]}`,
			[]value.Value{value.NewNil()}, ""},
	} {
		results := make([]any, 0, 2)
		for index, database := range []*ManagedDatabase{kitdb, sqlite} {
			result := database.OnInvoke(statement.Method, append([]value.Value{value.New(test.template)}, test.holes...)...)
			if result.IsError {
				t.Fatalf("%s: %v", test.template, result)
			}
			if got := namesOf(result.Interface()); got != test.want {
				t.Errorf("%s:\n  %s gave %q, want %q", test.template, []string{"KitDB", "SQLite"}[index], got, test.want)
			}
			results = append(results, withoutIDs(result.Interface()))
		}
		if !reflect.DeepEqual(results[0], results[1]) {
			t.Errorf("%s:\n  KitDB  %v\n  SQLite %v", test.template, results[0], results[1])
		}
	}
}

// namesOf is the names of the rows a statement gives, in order, or the count it gives.
func namesOf(item any) string {
	switch item := item.(type) {
	case []any:
		names := make([]string, 0, len(item))
		for _, row := range item {
			names = append(names, namesOf(row))
		}
		return strings.Join(names, ", ")
	case map[string]any:
		return fmt.Sprint(item["name"])
	}
	return fmt.Sprint(item)
}

// withoutIDs drops the generated ids, which the two backends make apart.
func withoutIDs(item any) any {
	switch item := item.(type) {
	case []any:
		out := make([]any, 0, len(item))
		for _, row := range item {
			out = append(out, withoutIDs(row))
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for key, field := range item {
			if key != "id" {
				out[key] = fmt.Sprint(field)
			}
		}
		return out
	}
	return fmt.Sprint(item)
}

// A read keeps the rows it gives back through kitDBRowCollector, the one way readKitDBRows has: an order
// no index covers keeps the best rows of the scan in batches, so the rows and their order are a full
// sort's and the comparisons stay O(N log K), not the O(N·K log K) of a sort after every row; rows in the
// scan's order before the page are skipped, never kept.
func TestKitDBOrderedScanSortsInBatches(t *testing.T) {
	random := rand.New(rand.NewSource(7))
	rows := make([]kitDBStoredRow, 5000)
	for index := range rows {
		rows[index] = kitDBStoredRow{
			key:    []byte(fmt.Sprintf("row-%05d", index)),
			values: map[string]value.Value{"amount": value.New(random.Intn(300))},
		}
	}
	compared := 0
	less := func(left, right kitDBStoredRow) bool {
		compared++
		if comparison := kitDBCompareValues(left.values["amount"], right.values["amount"]); comparison != 0 {
			return comparison > 0
		}
		return bytes.Compare(left.key, right.key) < 0
	}
	// the page of 900 rows after the first 100
	collector := newKitDBRowCollector(900, 100, less)
	for _, row := range rows {
		if done, err := collector.add(row); done || err != nil {
			t.Fatalf("an ordered read stopped at a row: %v %v", done, err)
		}
	}
	got := collector.result()
	batched := compared
	reference := append([]kitDBStoredRow(nil), rows...)
	sort.SliceStable(reference, func(left, right int) bool { return less(reference[left], reference[right]) })
	if len(got) != 900 {
		t.Fatalf("%d rows kept, want 900", len(got))
	}
	for index := range got {
		if !bytes.Equal(got[index].key, reference[100+index].key) {
			t.Fatalf("row %d is %s, a full sort gives %s", index, got[index].key, reference[100+index].key)
		}
	}
	// a full sort of the 5000 rows makes about 5000·log2(5000) comparisons; the batches stay within a few
	// of that, where a sort after every row made tens of millions
	if bound := int(5000 * math.Log2(2000) * 4); batched > bound {
		t.Fatalf("%d comparisons, want at most %d", batched, bound)
	}

	// in the scan's order: the rows past the offset, the read done at the limit, nothing kept before
	collector = newKitDBRowCollector(3, 4990, nil)
	stopped := -1
	for index, row := range rows {
		if done, _ := collector.add(row); done {
			stopped = index
			break
		}
	}
	got = collector.result()
	if stopped != 4992 || len(got) != 3 || string(got[0].key) != "row-04990" || cap(collector.rows) > 4 {
		t.Fatalf("offset 4990, limit 3: stopped at %d with %d rows (first %s, room for %d), want rows 4990-4992 and room for a few",
			stopped, len(got), got[0].key, cap(collector.rows))
	}
	if empty := newKitDBRowCollector(10, 5, nil).result(); empty == nil || len(empty) != 0 {
		t.Fatalf("a read of no rows gave %#v, want an empty list", empty)
	}
}

// The name of the app's database is the engine's: a site's kitdb("…") cannot open it, however the name is
// written (a read opens a name as kitDBRel reads it, so "./@application" is "@application"). The site would
// otherwise open the app's database with its own schema and options, drop and token among them.
func TestKitDBApplicationNameIsReserved(t *testing.T) {
	shop := newManagedApp(t, map[string]string{"shop.example/router.work": `import * as router from "kitwork/router";`})
	tenant := shop.site("shop.example")
	legacy := &Database{tenant: tenant}
	users := goStruct(t, goColumn{name: "id", builder: "id"}, goColumn{name: "email", builder: "text"})
	for _, name := range []string{"@application", "/@application", "./@application", `\@application`, "x/../@application", " @application", "@other"} {
		declared := legacy.Kitdb(value.New(name), value.New(map[string]value.Value{"users": users}),
			value.New(map[string]value.Value{"drop": value.New(true)}))
		code, message := failureOf(declared)
		want := fmt.Sprintf("db: tên %q dành cho database của app; database của app khai export const db = connection().schema({ … }) trong .shared/schema/", name)
		if code != value.InvalidArgumentCode || message != want {
			t.Errorf("kitdb(%q): %s %q, want INVALID_ARGUMENT %q", name, code, message, want)
		}
	}
	if _, err := os.Stat(filepath.Join(shop.root, "acme", ".database")); !os.IsNotExist(err) {
		t.Fatalf("the app's .database/ was made (%v)", err)
	}
	// a name that only holds @ further in is a file of the site
	if declared := legacy.Kitdb(value.New("notes/@archive"), value.New(map[string]value.Value{"users": users})); declared.K != value.Proxy {
		t.Fatalf("kitdb(\"notes/@archive\"): %v, want a database of the site", declared)
	}
}

// kitwork/schema refuses, where a schema file is evaluated, what the compiler refuses where it is read: a
// modifier of the older spellings, a column of another table (the foreign key not built yet), a builder of
// the spec the engine has not.
func TestSchemaModuleRefusesOlderSpellings(t *testing.T) {
	shop := newManagedApp(t, map[string]string{"shop.example/router.work": `import * as router from "kitwork/router";`})
	tenant := shop.site("shop.example")
	schema := &SchemaModule{}
	declare := func(tables map[string]value.Value) value.Value {
		return (&ConnectionDeclaration{tenant: tenant}).OnInvoke("schema", value.New(tables))
	}

	older := schema.Struct().Call("struct", value.New(map[string]value.Value{
		"id":    schema.Id().Call("id"),
		"email": schema.Text().Call("text").Invoke("notNull"),
	}))
	if code, message := failureOf(declare(map[string]value.Value{"users": older})); code != value.InvalidArgumentCode ||
		message != "bảng users, cột email: .notNull() là cách viết cũ: cột bắt buộc là mặc định; cột được bỏ trống viết .nullable()" {
		t.Errorf("text().notNull(): %s %q", code, message)
	}

	users := goStruct(t, goColumn{name: "id", builder: "id"}, goColumn{name: "email", builder: "text"})
	foreign := schema.Struct().Call("struct", value.New(map[string]value.Value{
		"id":     schema.Id().Call("id"),
		"userId": users.Get("id"),
	}))
	if code, message := failureOf(foreign); code != value.UnsupportedCode || !strings.Contains(message, "khoá ngoại bảng.cột chưa có trong engine hôm nay") {
		t.Errorf("userId: users.id: %s %q", code, message)
	}
	if code, message := failureOf(schema.Bigint().Call("bigint")); code != value.UnsupportedCode || message != "bigint() chưa có trong engine hôm nay (lát 9 của KITWORK_DATABASE.md)" {
		t.Errorf("bigint(): %s %q", code, message)
	}
	if code, message := failureOf(declare(map[string]value.Value{"session": users})); code != value.InvalidArgumentCode || !strings.HasPrefix(message, "session là tên dành riêng") {
		t.Errorf("a table named session: %s %q", code, message)
	}
}
