package work

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const historyShopRoute = `import * as router from "kitwork/router";
import { db } from "app/.shared/schema/shop.work";
import { desc } from "kitwork/database";

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
  const moment = request.query("at");
  if (action === "update") {
    return context.json({ changed: db.where((orders) => orders.email === email).update((orders) => ({ amount: orders.amount + 1000, note: "đã sửa" })) });
  }
  if (action === "delete") {
    return context.json({ removed: db.delete((orders) => orders.email === email) });
  }
  if (action === "history") {
    return context.json(db.history((orders) => orders.email === email));
  }
  if (action === "before") {
    return context.json(safe(() => db.at(moment).list((orders) => orders)));
  }
  if (action === "at") {
    return context.json({ rows: db.at(moment).list((orders) => orders), count: db.at(moment).count((orders) => orders) });
  }
  if (action === "page") {
    return context.json({
      first: db.at(moment).orderBy((orders) => desc(orders.amount)).list(1).map((row) => row.email),
      second: db.at(moment).orderBy((orders) => desc(orders.amount)).list({ page: 2, limit: 1 }).map((row) => row.email),
    });
  }
  return context.json(db.list((orders) => orders));
});
`

// history() gives the changes of the rows its condition keeps, oldest first, { at, before, after, by }, a row since
// deleted included; at(moment) reads the database as it was then, going backward from the live file with no copy
// made, a page in an order and its second page included; a moment before the table was made, which the schema's
// change stands between, is read from a copy restored from the history and says the table was not there
// (KITWORK_DATABASE.md mục 2, "Thời điểm và lịch sử"; KITWORK_KITDB.md mục 5).
//
// CONTROL (03/11/2026): with the condition run on the after image alone, the deleted row's delete is gone, "history of
// b@x.vn = [insert], want insert, delete"; with at() reading the live file, "at revision 4: a@x.vn 36000 (1), want
// a@x.vn 36000, b@x.vn 40000 (2)". CONTROL (04/11/2026): with every at() read from a restored copy, "at() of the
// revisions after the tables restored copies"; with a row's last change after the moment taken for what it was, the
// order inserted and deleted since comes back, "at revision 3: a@x.vn 36000, b@x.vn 40000 (2), want a@x.vn 36000 (1)".
func TestManagedDatabaseHistoryAndAt(t *testing.T) {
	managed := newManagedApp(t, map[string]string{".shared/schema/shop.work": writeShopSchema, "shop.example/router.work": historyShopRoute})
	tenant := managed.site("shop.example")
	before := time.Now().Add(-time.Hour)
	post := func(email, item, amount string) {
		t.Helper()
		if status, body := managedPost(tenant, "/", url.Values{"email": {email}, "item": {item}, "amount": {amount}}); status != http.StatusOK {
			t.Fatalf("insert %s = %d %s", email, status, body)
		}
	}
	var answer map[string]any
	post("a@x.vn", "Cà phê", "35000")
	getJSON(t, tenant, "/?do=update&email=a@x.vn", &answer)
	post("b@x.vn", "Trà đào", "40000")
	getJSON(t, tenant, "/?do=delete&email=b@x.vn", &answer)

	var changes []map[string]any
	getJSON(t, tenant, "/?do=history&email=a@x.vn", &changes)
	if len(changes) != 2 || changes[0]["before"] != nil || changes[1]["before"] == nil || changes[1]["after"] == nil {
		t.Fatalf("history of a@x.vn = %v, want its insert then its update", changes)
	}
	inserted, updated := changes[0]["after"].(map[string]any), changes[1]["after"].(map[string]any)
	if inserted["amount"] != 35000.0 || changes[1]["before"].(map[string]any)["amount"] != 35000.0 || updated["amount"] != 36000.0 ||
		updated["note"] != "đã sửa" || changes[0]["by"] != nil {
		t.Fatalf("history of a@x.vn = %v, want 35000 then 35000 → 36000 with the note, by nobody", changes)
	}
	if first, second := changes[0]["at"].(float64), changes[1]["at"].(float64); first <= 0 || second < first {
		t.Fatalf("history of a@x.vn moments %v, %v", first, second)
	}
	getJSON(t, tenant, "/?do=history&email=b@x.vn", &changes)
	if len(changes) != 2 || changes[1]["after"] != nil || changes[1]["before"].(map[string]any)["email"] != "b@x.vn" {
		kinds := make([]string, len(changes))
		for index, change := range changes {
			kinds[index] = map[bool]string{true: "insert", false: "delete"}[change["before"] == nil]
		}
		t.Fatalf("history of b@x.vn = %v, want insert, delete", kinds)
	}

	_, moments := historyOf(t, filepath.Join(managed.root, "acme", ".database", "application", "kitwork.db"))
	var revisions []uint64
	for revision := range moments {
		revisions = append(revisions, revision)
	}
	// the commits: the tables (a Barrier), insert a, update a, insert b, delete b
	if len(revisions) != 5 {
		t.Fatalf("the history holds %d commits, want 5", len(revisions))
	}
	at := func(revision uint64) string {
		var read struct {
			Rows  []map[string]any `json:"rows"`
			Count float64          `json:"count"`
		}
		getJSON(t, tenant, "/?do=at&at="+url.QueryEscape(moments[revision].Format(time.RFC3339Nano)), &read)
		lines := []string{}
		for _, row := range read.Rows {
			lines = append(lines, row["email"].(string)+" "+fmtNumber(row["amount"]))
		}
		return strings.Join(lines, ", ") + " (" + fmtNumber(read.Count) + ")"
	}
	for revision, want := range map[uint64]string{2: "a@x.vn 35000 (1)", 3: "a@x.vn 36000 (1)", 4: "a@x.vn 36000, b@x.vn 40000 (2)", 5: "a@x.vn 36000 (1)"} {
		if got := at(revision); got != want {
			t.Fatalf("at revision %d: %s, want %s", revision, got, want)
		}
	}
	for revision, want := range map[uint64]string{2: "[a@x.vn] []", 4: "[b@x.vn] [a@x.vn]", 5: "[a@x.vn] []"} {
		var page struct {
			First  []string `json:"first"`
			Second []string `json:"second"`
		}
		getJSON(t, tenant, "/?do=page&at="+url.QueryEscape(moments[revision].Format(time.RFC3339Nano)), &page)
		if got := fmt.Sprint(page.First, " ", page.Second); got != want {
			t.Fatalf("at revision %d the largest amount first, a page of one: %s, want %s", revision, got, want)
		}
	}
	if _, err := os.Stat(filepath.Join(managed.root, "acme", ".database", "application", "kitwork.db.at")); err == nil {
		t.Fatal("at() of the revisions after the tables restored copies, want them read going backward")
	}
	var refused struct {
		OK    bool `json:"ok"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	getJSON(t, tenant, "/?do=before&at="+url.QueryEscape(before.Format(time.RFC3339)), &refused)
	if refused.OK || !strings.Contains(refused.Error.Message, "lúc đó database chưa có bảng orders") {
		t.Fatalf("at an hour before the database = %+v, want the error that says so", refused)
	}
}

func fmtNumber(item any) string {
	if number, ok := item.(float64); ok {
		return strconv.FormatFloat(number, 'f', -1, 64)
	}
	return "?"
}
