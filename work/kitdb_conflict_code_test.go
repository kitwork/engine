package work

import (
	"net/http"
	"strings"
	"testing"
)

// A KitDB write that meets another row is CONFLICT (14/10/2026), as the closed set says of a unique
// constraint and as the SQL path already answered: a key that exists, a unique field, a composite
// unique constraint. They were DATABASE, so a handler could not tell "taken" from "the database is
// down". CONTROL: a write that meets nothing has no error.
func TestKitDBUniqueViolationIsAConflict(t *testing.T) {
	serve := safeSite(t, map[string]string{
		"": `import { router, database } from "kitwork";
const { kitdb, struct, text } = database;
const items = struct({
  id: text().key(),
  label: text().unique(),
  shelf: text().unique("place", 1),
  slot: text().unique("place", 2),
});
const db = kitdb("conflict.kitdb", { items });
router.get((ctx) => {
  if (db.items.count() == 0) db.items.create({ id: "a", label: "một", shelf: "s1", slot: "1" });
  const key = safe(() => db.items.create({ id: "a", label: "hai", shelf: "s2", slot: "1" }));
  const field = safe(() => db.items.create({ id: "b", label: "một", shelf: "s2", slot: "1" }));
  const place = safe(() => db.items.create({ id: "c", label: "ba", shelf: "s1", slot: "1" }));
  const fresh = safe(() => db.items.create({ id: "d", label: "bốn", shelf: "s4", slot: "1" }));
  return ctx.json({ key: key.error.code, field: field.error.code, place: place.error.code, fresh: fresh.error });
});
`,
	})
	got := serve("/")
	body := strings.TrimSpace(got.Body.String())
	if got.Code != http.StatusOK || body != `{"field":"CONFLICT","fresh":null,"key":"CONFLICT","place":"CONFLICT"}` {
		t.Fatalf("GET / = %d %s, want CONFLICT for each write that met another", got.Code, body)
	}
}
