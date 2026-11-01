package work

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kitwork/engine/app"
	"github.com/kitwork/engine/storage"
)

// datastore() is the SQLite file .database/storage/kitwork.db of the app since 30/10/2026: one row of
// kitwork_storage a value, its key the encoded key as bytes, its value encoded, its expiry in milliseconds
// (NULL for never); no KitDB file is made.
//
// CONTROL: with the expiry written as NULL whatever expire() said, "otp expire = {0 false}, want …".
func TestStorageDatastoreIsTheSQLiteFile(t *testing.T) {
	storageTestClock(t)
	tn := storageSite(t, map[string]string{
		"put/router.work": `router.get((context) => {
  datastore("flags").segment("checkout").set({ newFlow: true });
  datastore("codes").segment("otp").expire("1m").set("123456");
  return context.json("ok");
});`,
	})
	if _, body := storageGet(t, tn, "/put"); body != `"ok"` {
		t.Fatalf("put = %s", body)
	}
	if _, err := os.Stat(tn.resolveApp(".database", "storage", datastoreLegacyFile)); !os.IsNotExist(err) {
		t.Fatalf("storage.kitdb: %v, want none", err)
	}
	rows := readDatastoreFile(t, tn.resolveApp(".database", "storage", "kitwork.db"))
	if len(rows) != 2 {
		t.Fatalf("kitwork_storage holds %d rows, want 2: %v", len(rows), rows)
	}
	flag := storage.Key{Place: storage.Datastore, Name: "flags", Segments: []string{"checkout"}}.Encoded()
	otp := storage.Key{Place: storage.Datastore, Name: "codes", Segments: []string{"otp"}}.Encoded()
	if got := rows[flag]; got.expire.Valid {
		t.Errorf("checkout expire = %v, want NULL (never)", got.expire)
	}
	if decoded, err := storage.Decode(rows[flag].value); err != nil || !decoded.Get("newFlow").Truthy() {
		t.Errorf("checkout value = %v %v, want { newFlow: true }", decoded, err)
	}
	if want := storageClock().Add(time.Minute).UnixMilli(); rows[otp].expire != (sql.NullInt64{Int64: want, Valid: true}) {
		t.Errorf("otp expire = %v, want %d", rows[otp].expire, want)
	}
}

type datastoreRow struct {
	value  []byte
	expire sql.NullInt64
}

// readDatastoreFile reads kitwork_storage of the file at path on a connection of its own, read-only.
func readDatastoreFile(t *testing.T, path string) map[string]datastoreRow {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT key, value, expire FROM kitwork_storage`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]datastoreRow{}
	for rows.Next() {
		var key []byte
		var row datastoreRow
		if err := rows.Scan(&key, &row.value, &row.expire); err != nil {
			t.Fatal(err)
		}
		out[string(key)] = row
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// Reading datastore() of an app that never wrote one makes nothing: get, exists, ttl and delete leave no
// .database/storage/.
//
// CONTROL: with datastoreAccess.read asking for the file to be made, ".database/storage/ = <nil>, want none".
func TestStorageDatastoreReadMakesNoFile(t *testing.T) {
	tn := storageSite(t, map[string]string{
		"look/router.work": `router.get((context) => {
  const flag = datastore("flags").segment("checkout");
  return context.json({ get: flag.get(), exists: flag.exists(), ttl: flag.ttl(), deleted: flag.delete() });
});`,
	})
	if _, body := storageGet(t, tn, "/look"); body != `{"deleted":false,"exists":false,"get":null,"ttl":null}` {
		t.Fatalf("look = %s", body)
	}
	if _, err := os.Stat(tn.resolveApp(".database", "storage")); !os.IsNotExist(err) {
		t.Fatalf(".database/storage/ = %v, want none", err)
	}
}

// The ceilings count what the file holds when the app starts again: two keys kept by one runtime fill a
// datastore() of two keys for the next.
//
// CONTROL: with datastorePlace.count not reading the file, the third key was written: "after a restart = \"ok\"".
func TestStorageDatastoreCountsTheFileAfterARestart(t *testing.T) {
	saved := storageDatastoreKeys
	storageDatastoreKeys = 2
	t.Cleanup(func() { storageDatastoreKeys = saved })
	root := writeSite(t, map[string]string{
		"router.work": `import * as router from "kitwork/router";`,
		"put/router.work": storageHeader + `router.get((context) => {
  const r = safe(() => datastore("full").segment(context.query("key")).set(1));
  return context.json(r.ok ? "ok" : r.error.code);
});`,
	})
	serve := func(path string) string {
		appRuntime := app.NewRuntime("test")
		defer appRuntime.Close()
		siteRuntime, _ := appRuntime.Site(root, "localhost")
		generation, _ := siteRuntime.PrepareGeneration()
		tn := NewTenantWithRuntime(root, "localhost", appRuntime, siteRuntime, generation)
		defer tn.Close()
		if err := tn.Run(); err != nil {
			t.Fatal(err)
		}
		return serveGet(tn, path, nil).Body.String()
	}
	if a, b := serve("/put?key=a"), serve("/put?key=b"); a != `"ok"` || b != `"ok"` {
		t.Fatalf("a = %s, b = %s", a, b)
	}
	if body := serve("/put?key=c"); body != `"TOO_LARGE"` {
		t.Fatalf("after a restart = %s, want TOO_LARGE", body)
	}
}

// Boot of an app whose datastore() is still in the KitDB file says so, and reads nothing from it; once the
// file's values were copied (the mark in kitwork_copies), boot says nothing. The KitDB file is never opened.
//
// CONTROL: with sayDatastoreLeftBehind not called at boot, "boot said …, want the line about storage.kitdb".
func TestStorageDatastoreSaysTheKitDBFileLeftBehind(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "test", ".database", "storage")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(folder, datastoreLegacyFile)
	if err := os.WriteFile(legacy, []byte("not a KitDB file: nothing may open it"), 0o644); err != nil {
		t.Fatal(err)
	}
	boot := func() string {
		return captureOutput(t, func() {
			tenant := NewAppTenant(root, "test")
			defer tenant.Close()
			if err := tenant.Run(); err != nil {
				t.Fatal(err)
			}
		})
	}
	if said := boot(); !strings.Contains(said, datastoreLegacyLine) {
		t.Fatalf("boot said %q, want the line about storage.kitdb", said)
	}
	if data, err := os.ReadFile(legacy); err != nil || string(data) != "not a KitDB file: nothing may open it" {
		t.Fatalf("storage.kitdb = %q %v, want it as it was", data, err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(folder, "kitwork.db")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(kitDBCopySchema); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO kitwork_copies (source, digest, rows, at) VALUES (?, 'x', 0, 0)`, datastoreLegacySource); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if said := boot(); strings.Contains(said, "storage.kitdb") {
		t.Fatalf("boot after the copy said %q, want nothing of storage.kitdb", said)
	}
}

// An app with routes only boots no tenant of its own: its first use of datastore() says the KitDB file left
// behind, once for the run of the app, whichever request comes first.
//
// CONTROL: with openDatastore not saying it, "the app said \"\", want the line about storage.kitdb once".
func TestStorageDatastoreSaysTheKitDBFileOnFirstUse(t *testing.T) {
	tn := storageSite(t, map[string]string{
		"look/router.work": `router.get((context) => context.json(datastore("flags").segment("checkout").get()));`,
	})
	folder := tn.resolveApp(".database", "storage")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, datastoreLegacyFile), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	said := captureOutput(t, func() {
		for range 3 {
			if _, body := storageGet(t, tn, "/look"); body != "null" {
				t.Errorf("look = %s, want null", body)
			}
		}
	})
	if strings.Count(said, datastoreLegacyLine) != 1 {
		t.Fatalf("the app said %q, want the line about storage.kitdb once", said)
	}
}

// expire() takes spans up to the longest time.Duration: a value of datastore() kept for 250 years is kept and
// its ttl() counts them; disk(), which writes nanoseconds, keeps it until the last instant they can say (year
// 2262) instead of wrapping past it; a longer number is INVALID_ARGUMENT naming the maximum.
//
// CONTROL: with datastore()'s expiry kept in nanoseconds, the 250 years wrapped past 2262 and the value was gone
// at once: "far = {…\"datastore\":{\"days\":null,\"get\":null}…}"; with disk()'s record not held at 2262, the
// same for disk; with no maximum in expire(), "\"tooFar\":\"ok\"".
func TestStorageKeepsAFarExpiry(t *testing.T) {
	storageTestClock(t)
	tn := storageSite(t, map[string]string{
		"far/router.work": `const keep = (place) => {
  const far = place.segment("k").expire(250 * 365 * 24 * 3600 * 1000);
  far.set(1);
  return { get: far.get(), days: far.ttl() === null ? null : Math.floor(far.ttl() / 86400000) };
};
router.get((context) => {
  const tooFar = safe(() => datastore("far").segment("x").expire(10000000000000));
  return context.json({ datastore: keep(datastore("far")), disk: keep(disk("far")), tooFar: tooFar.ok ? "ok" : tooFar.error.code });
});`,
	})
	if _, body := storageGet(t, tn, "/far"); body != `{"datastore":{"days":91250,"get":1},"disk":{"days":85918,"get":1},"tooFar":"INVALID_ARGUMENT"}` {
		t.Fatalf("far = %s", body)
	}
}
