package work

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

// uuid() is a column holding a UUID that comes from elsewhere, a payment's or another system's (06/11/2026): its value
// is a string the binder checks as PostgreSQL's uuid reads it (8-4-4-4-12 hexadecimal digits) and keeps lower case, so
// a condition in capitals finds it; an insert that leaves it out gets no UUID made up, the key's is id("uuid"); kitwork
// migrate writes it as PostgreSQL's uuid, and the structure file says so.
//
// CONTROL (06/11/2026): with autoValue generating a UUID for every uuid() column again, "refundId was made up"; with
// the binder taking any string for a uuid(), "not-a-uuid" was written; with coerceWrite keeping the case, the row
// written in capitals was not found by its UUID in lower case ("the row read back is []").
func TestSchemaUUIDColumn(t *testing.T) {
	const schema = `import { table, id, string, uuid } from "kitwork/schema";
import { connection } from "kitwork/database";

const payments = table({ id: id(), paymentId: uuid(), refundId: uuid().nullable(), note: string().nullable() });

export const db = connection().tables({ payments });
`
	const route = `import * as router from "kitwork/router";
import { db } from "app/.shared/schema/shop.work";

router.post((context) => {
  const paymentId = context.request.form("paymentId");
  const inserted = safe(() => db.insert((payments) => ({ paymentId, note: "cà phê" })));
  return context.json({ ok: inserted.ok, error: inserted.error });
});
router.get((context) => {
  const paymentId = context.request.query("paymentId");
  return context.json({ rows: db.list((payments) => payments.paymentId === paymentId) });
});
`
	root := t.TempDir()
	writeAppTree(t, root, map[string]string{
		"acme/.shared/schema/shop.work": schema,
		"acme/shop.example/router.work": route,
	})
	plans := migrateAppForTest(t, root, "acme", RootLayoutMultiTenant)
	statements := ""
	for _, plan := range plans {
		statements += strings.Join(plan.Statements, "\n")
	}
	if !strings.Contains(statements, `"paymentId" uuid NOT NULL`) || !strings.Contains(statements, `"refundId" uuid`) {
		t.Fatalf("kitwork migrate wrote\n%s\nwant the uuid() columns as PostgreSQL's uuid", statements)
	}
	structure, err := os.ReadFile(filepath.Join(root, "acme", ".database", "application", "kitwork.db.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(structure), `"paymentId" uuid NOT NULL`) {
		t.Fatalf("the structure file says\n%s\nwant paymentId as uuid", structure)
	}

	tenant := managedAppAt(t, root).site("shop.example")
	post := func(paymentId string) (bool, string, string) {
		t.Helper()
		status, body := managedPost(tenant, "/", url.Values{"paymentId": {paymentId}})
		var result struct {
			OK    bool `json:"ok"`
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if status != http.StatusOK || json.Unmarshal([]byte(body), &result) != nil {
			t.Fatalf("POST / = %d %s", status, body)
		}
		return result.OK, result.Error.Code, result.Error.Message
	}
	list := func(paymentId string) []map[string]any {
		t.Helper()
		status, body := managedGet(tenant, "/?paymentId="+url.QueryEscape(paymentId))
		var result struct {
			Rows []map[string]any `json:"rows"`
		}
		if status != http.StatusOK || json.Unmarshal([]byte(body), &result) != nil {
			t.Fatalf("GET / = %d %s", status, body)
		}
		return result.Rows
	}

	const given = "0192F3A4-5B6C-7D8E-9FA0-B1C2D3E4F5A6"
	if ok, code, message := post(given); !ok {
		t.Fatalf("a UUID in capitals was refused: %s %s", code, message)
	}
	rows := list(strings.ToLower(given))
	if len(rows) != 1 || rows[0]["paymentId"] != strings.ToLower(given) {
		t.Fatalf("the row read back is %v, want paymentId kept lower case", rows)
	}
	if rows[0]["refundId"] != nil {
		t.Fatalf("refundId was made up: %v", rows[0]["refundId"])
	}
	if found := list(given); len(found) != 1 {
		t.Fatalf("the condition in capitals found %d rows, want the one row", len(found))
	}

	for _, wrong := range []string{"not-a-uuid", "0192f3a45b6c7d8e9fa0b1c2d3e4f5a6", "0192f3a4-5b6c-7d8e-9fa0-b1c2d3e4f5ag"} {
		ok, code, message := post(wrong)
		if ok || code != value.InvalidArgumentCode ||
			!strings.Contains(message, "cột paymentId là uuid(): giá trị ở router.work:") ||
			!strings.Contains(message, "không phải UUID (8-4-4-4-12 chữ số hệ 16)") || strings.Contains(message, wrong) {
			t.Errorf("%q was answered ok=%v %s %q, want INVALID_ARGUMENT naming the column, not the value", wrong, ok, code, message)
		}
	}
	if ok, _, _ := post(""); ok {
		t.Errorf("an empty paymentId was written")
	}
	if all := list(strings.ToLower(given)); len(all) != 1 {
		t.Fatalf("after the refused writes: %v", all)
	}
}
