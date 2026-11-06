package work

import (
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

const writeShopSchema = `import { table, id, string, decimal, integer, date } from "kitwork/schema";
import { connection } from "kitwork/database";

const orders = table({ id: id(), email: string().unique(), item: string(), amount: decimal(12, 2), note: string().nullable() });
const clickDays = table({ id: id(), linkId: string().unique("linkDay"), day: date().unique("linkDay"), count: integer() });

export const db = connection().tables({ orders, clickDays });
`

const writeShopRoute = `import * as router from "kitwork/router";
import { db } from "app/.shared/schema/shop.work";

router.post((context) => {
  const request = context.request;
  const email = request.form("email");
  const item = request.form("item");
  const amount = Number(request.form("amount"));
  return context.json(db.insert((orders) => ({ email, item, amount })));
});

router.get((context) => {
  const request = context.request;
  const action = request.query("do");
  const email = request.query("email");
  const linkId = request.query("link");
  const today = "2026-11-03";
  if (action === "update") {
    return context.json({ changed: db.where((orders) => orders.email === email).update((orders) => ({ amount: orders.amount + 1000, note: "đã sửa" })) });
  }
  if (action === "delete") {
    return context.json({ removed: db.delete((orders) => orders.email === email) });
  }
  if (action === "click") {
    return context.json(db.upsert((clickDays) => ({ linkId, day: today, count: clickDays.count + 1 })));
  }
  if (action === "clicks") {
    return context.json(db.list((clickDays) => clickDays));
  }
  return context.json(db.list((orders) => orders));
});
`

// getJSON serves GET path and reads its JSON.
func getJSON(t *testing.T, tenant *Tenant, path string, into any) {
	t.Helper()
	status, body := managedGet(tenant, path)
	if status != http.StatusOK || json.Unmarshal([]byte(body), into) != nil {
		t.Fatalf("GET %s = %d %s", path, status, body)
	}
}

// update, delete and upsert run on the app's database (KITWORK_DATABASE.md mục 2, lát 3): update changes the rows of
// its condition, its data reading the row (a decimal + 1000 stays exact) and gives how many it changed; delete gives
// how many it removed; upsert inserts the row its target does not find and changes the one it finds, one statement,
// its data reading the row it changes (count + 1 counts 1, 2, 3). Each is a commit of the history.
//
// CONTROL (03/11/2026): with the + of the data rendered as the column alone, "a@x.vn after the update = 35000 đã sửa,
// want 36000 and the note"; with the upsert's conflict clause left out, the click fails on the unique key, "GET
// /?do=click&link=l1 = 500 Internal Server Error"; with the update's condition dropped, "the update changed 2 rows,
// want 1".
func TestManagedDatabaseUpdateDeleteUpsert(t *testing.T) {
	managed := newManagedApp(t, map[string]string{".shared/schema/shop.work": writeShopSchema, "shop.example/router.work": writeShopRoute})
	tenant := managed.site("shop.example")
	for _, order := range [][]string{{"a@x.vn", "Cà phê", "35000"}, {"b@x.vn", "Trà đào", "40000"}} {
		if status, body := managedPost(tenant, "/", url.Values{"email": {order[0]}, "item": {order[1]}, "amount": {order[2]}}); status != http.StatusOK {
			t.Fatalf("insert %v = %d %s", order, status, body)
		}
	}

	var changed map[string]float64
	getJSON(t, tenant, "/?do=update&email=a@x.vn", &changed)
	if changed["changed"] != 1 {
		t.Fatalf("the update changed %v rows, want 1", changed["changed"])
	}
	getJSON(t, tenant, "/?do=update&email=nobody@x.vn", &changed)
	if changed["changed"] != 0 {
		t.Fatalf("an update of no row changed %v rows, want 0", changed["changed"])
	}
	var orders []map[string]any
	getJSON(t, tenant, "/", &orders)
	byEmail := map[string]map[string]any{}
	for _, order := range orders {
		byEmail[order["email"].(string)] = order
	}
	if amount := byEmail["a@x.vn"]["amount"]; amount != 36000.0 || byEmail["a@x.vn"]["note"] != "đã sửa" {
		t.Fatalf("a@x.vn after the update = %v %v, want 36000 and the note", amount, byEmail["a@x.vn"]["note"])
	}
	if amount := byEmail["b@x.vn"]["amount"]; amount != 40000.0 || byEmail["b@x.vn"]["note"] != nil {
		t.Fatalf("b@x.vn after the update = %v %v, want it as it was", amount, byEmail["b@x.vn"]["note"])
	}

	var removed map[string]float64
	getJSON(t, tenant, "/?do=delete&email=b@x.vn", &removed)
	if removed["removed"] != 1 {
		t.Fatalf("the delete removed %v rows, want 1", removed["removed"])
	}
	getJSON(t, tenant, "/", &orders)
	if len(orders) != 1 || orders[0]["email"] != "a@x.vn" {
		t.Fatalf("after the delete = %v", orders)
	}

	var row map[string]any
	var first string
	for click := 1; click <= 3; click++ {
		getJSON(t, tenant, "/?do=click&link=l1", &row)
		if row["count"] != float64(click) {
			t.Fatalf("click %d counts %v", click, row["count"])
		}
		if first == "" {
			first, _ = row["id"].(string)
		} else if row["id"] != first {
			t.Fatalf("click %d is the row %v, want the first's %s", click, row["id"], first)
		}
	}
	getJSON(t, tenant, "/?do=click&link=l2", &row)
	if row["count"] != 1.0 || row["id"] == first {
		t.Fatalf("another link = %v, want a row of its own counting 1", row)
	}
	var clicks []map[string]any
	getJSON(t, tenant, "/?do=clicks", &clicks)
	if len(clicks) != 2 {
		t.Fatalf("clickDays holds %d rows, want 2", len(clicks))
	}

	history, _ := historyOf(t, filepath.Join(managed.root, "acme", ".database", "application", "kitwork.db"))
	got := strings.Join(history, " | ")
	for _, want := range []string{"Update orders a@x.vn", "Delete orders", "Insert clickDays", "Update clickDays"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the history %q has no %q", got, want)
		}
	}
}
