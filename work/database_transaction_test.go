package work

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const transactionShopRoute = `import * as router from "kitwork/router";
import { db } from "app/.shared/schema/shop.work";

const save = (email) => db.insert((orders) => ({ email, item: "Bánh mì", amount: 1 }));

router.get((context) => {
  const request = context.request;
  const action = request.query("do");
  const email = request.query("email");
  if (action === "order") {
    return context.json(db.transaction((database) => {
      const created = database.insert((orders) => ({ email, item: "Cà phê", amount: 35000 }));
      database.where((orders) => orders.email === email).update((orders) => ({ amount: orders.amount + 1000 }));
      const read = database.first((orders) => orders.email === email);
      return { created: created.email, amount: read === null ? 0 : read.amount };
    }));
  }
  if (action === "fail") {
    return context.json(safe(() => db.transaction((database) => {
      database.insert((orders) => ({ email, item: "Trà", amount: 1 }));
      database.insert((orders) => ({ email: "a@x.vn", item: "Trà", amount: 1 }));
      return 1;
    })));
  }
  if (action === "outer") {
    return context.json(safe(() => db.transaction((database) => {
      save(email);
      return 1;
    })));
  }
  if (action === "nothing") {
    return context.json({ result: db.transaction((database) => null) });
  }
  return context.json(db.list((orders) => orders));
});
`

// db.transaction((database) => { … }) runs its function inside one transaction of the file's writer (KITWORK_DATABASE.md
// mục 3): what it writes through the handle it reads back, it commits as one revision when the function returns, and
// gives what the function returned; a failure inside rolls back everything it wrote and goes on; a write through db
// from inside it (a function written outside, which the compiler cannot see) is refused with words, not waited for.
//
// CONTROL (03/11/2026): with the handle's statements going to the file's writer instead of its transaction, each is a
// write from inside the transaction and the order fails, "GET /?do=order&email=a@x.vn = 500 Internal Server Error";
// with the failure not rolling back, "new@x.vn is in the database after the transaction failed"; with the writer not
// refusing a write from inside a request, the request never answers and the test runs out of time.
func TestManagedDatabaseTransaction(t *testing.T) {
	managed := newManagedApp(t, map[string]string{".shared/schema/shop.work": writeShopSchema, "shop.example/router.work": transactionShopRoute})
	tenant := managed.site("shop.example")

	var order map[string]any
	getJSON(t, tenant, "/?do=order&email=a@x.vn", &order)
	if order["created"] != "a@x.vn" || order["amount"] != 36000.0 {
		t.Fatalf("the order = %v, want a@x.vn read back at 36000", order)
	}
	history, _ := historyOf(t, filepath.Join(managed.root, "acme", ".database", "application", "kitwork.db"))
	if last := history[len(history)-1]; last != "Insert orders a@x.vn" {
		t.Fatalf("the transaction's commit = %q, want one insert of the row as it ended", last)
	}

	var failed struct {
		OK    bool `json:"ok"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	getJSON(t, tenant, "/?do=fail&email=new@x.vn", &failed)
	if failed.OK || !strings.Contains(failed.Error.Message, "UNIQUE") {
		t.Fatalf("the failed transaction = %+v, want the unique email's failure", failed)
	}
	var orders []map[string]any
	getJSON(t, tenant, "/", &orders)
	for _, row := range orders {
		if row["email"] == "new@x.vn" {
			t.Fatal("new@x.vn is in the database after the transaction failed")
		}
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		getJSON(t, tenant, "/?do=outer&email=outer@x.vn", &failed)
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("outer did not answer in 15s")
	}
	if failed.OK || !strings.Contains(failed.Error.Message, "ghi qua db bên trong db.transaction") {
		t.Fatalf("a write through db inside the transaction = %+v, want the error that says to write on its handle", failed)
	}

	var nothing map[string]any
	if status, body := managedGet(tenant, "/?do=nothing"); status != http.StatusOK || !strings.Contains(body, `"result":null`) {
		t.Fatalf("a transaction returning null = %d %s, want null as its result", status, body)
	}
	_ = nothing
	getJSON(t, tenant, "/", &orders)
	if len(orders) != 1 {
		t.Fatalf("the database holds %d orders, want the one the first transaction wrote", len(orders))
	}
}
