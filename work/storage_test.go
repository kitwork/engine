package work

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kitwork/engine/app"
	"github.com/kitwork/engine/storage"
	"github.com/kitwork/engine/utilities/safepath"
	"github.com/kitwork/engine/value"
)

// kitwork/storage (KITWORK_STORAGE.md) driven through real sites: router.work compiled and served, each
// value written by one request and read by another.

// storageHeader leads every route of these sites: the first line of a route's own source is line 3.
const storageHeader = "import * as router from \"kitwork/router\";\nimport { memory, disk, datastore } from \"kitwork/storage\";\n"

// storageSite serves files (each a folder's router.work, under storageHeader) on a tenant closed when the
// test ends.
func storageSite(t *testing.T, files map[string]string) *Tenant {
	t.Helper()
	all := map[string]string{"router.work": `import * as router from "kitwork/router";`}
	for path, source := range files {
		all[path] = storageHeader + source
	}
	tn := NewTenant(writeSite(t, all), "localhost")
	tn.MaxEnergy = 50_000_000
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	t.Cleanup(tn.Close)
	return tn
}

// storageGet serves path and gives its status and body.
func storageGet(t *testing.T, tn *Tenant, path string) (int, string) {
	t.Helper()
	rec := serveGet(tn, path, nil)
	return rec.Code, rec.Body.String()
}

// storageFailureText is what safe gave back as JSON: { ok, code, message }.
type storageFailureText struct {
	OK      bool   `json:"ok"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func readFailure(t *testing.T, body string) storageFailureText {
	t.Helper()
	var failure storageFailureText
	if err := json.Unmarshal([]byte(body), &failure); err != nil {
		t.Fatalf("not a failure: %s", body)
	}
	return failure
}

// The clock of storage, set for a test: now moves only when the test moves it.
func storageTestClock(t *testing.T) func(time.Duration) {
	t.Helper()
	var mu sync.Mutex
	now := time.Unix(1_800_000_000, 0)
	saved := storageClock
	storageClock = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	t.Cleanup(func() { storageClock = saved })
	return func(by time.Duration) { mu.Lock(); now = now.Add(by); mu.Unlock() }
}

// get() is null with nothing kept; set() gives nothing; a value comes back as it was written and of its
// own (mục 5); expire() is for the next write: get() and exists() see nothing once it has passed, ttl()
// counts down in milliseconds, and null when there is no expiry (mục 6). A value of a request is the next
// request's to read: memory() is the app's, not the request's.
//
// CONTROL: with the memory store never finding an entry expired, after the clock moved past 30s "after expiry:
// {… \"exists\":true, … \"value\":{\"name\":\"Kitwork\",…}}, want value null, exists false, ttl null".
func TestStorageMemoryValuesAndExpiry(t *testing.T) {
	advance := storageTestClock(t)
	tn := storageSite(t, map[string]string{
		"write/router.work": `router.get((context) => {
  const user = memory("users").segment("123");
  const before = user.get();
  const answer = user.expire("30s").set({ name: "Kitwork", tags: ["a"] });
  memory("forever").set(null);
  return context.json({ before: before, answer: answer, ttl: user.ttl(), exists: user.exists() });
});`,
		"read/router.work": `router.get((context) => {
  const user = memory("users").segment("123");
  const first = user.get();
  if (first) { first.name = "changed"; }
  return context.json({ value: user.get(), exists: user.exists(), ttl: user.ttl(),
    keptNull: memory("forever").exists(), nullTtl: memory("forever").ttl(), none: memory("none").get() });
});`,
		"remove/router.work": `router.get((context) => context.json({ first: memory("users").segment("123").delete(), second: memory("users").segment("123").delete() }));`,
	})
	if status, body := storageGet(t, tn, "/write"); status != http.StatusOK || body != `{"answer":null,"before":null,"exists":true,"ttl":30000}` {
		t.Fatalf("/write = %d %s", status, body)
	}
	advance(10 * time.Second)
	if status, body := storageGet(t, tn, "/read"); status != http.StatusOK ||
		body != `{"exists":true,"keptNull":true,"none":null,"nullTtl":null,"ttl":20000,"value":{"name":"Kitwork","tags":["a"]}}` {
		t.Fatalf("/read = %d %s, want the value as written, a copy of its own, 20s left", status, body)
	}
	advance(20 * time.Second)
	if _, body := storageGet(t, tn, "/read"); !strings.Contains(body, `"exists":false`) || !strings.Contains(body, `"value":null`) || !strings.Contains(body, `"ttl":null`) {
		t.Fatalf("after expiry: %s, want value null, exists false, ttl null", body)
	}
	storageGet(t, tn, "/write")
	if _, body := storageGet(t, tn, "/remove"); body != `{"first":true,"second":false}` {
		t.Fatalf("/remove = %s, want true then false", body)
	}
}

// A key is built with segment(), never joined: segment("a:b") and segment("a", "b") are two keys, though
// key() reads the same for a person; name(), key() and keys() touch nothing, and keys() is an array of the
// caller's (mục 4). A number is a segment written as text.
//
// CONTROL: with storage.Key.Encoded joining with ":", the two values were one: {"joined":"two","split":"two"}.
func TestStorageSegmentsNeverMeet(t *testing.T) {
	tn := storageSite(t, map[string]string{
		"keys/router.work": `router.get((context) => {
  const users = memory("users");
  users.segment("a:b").set("one");
  users.segment("a", "b").set("two");
  const profile = users.segment(123).segment("profile");
  const keys = profile.keys();
  keys.push("x");
  return context.json({ joined: users.segment("a:b").get(), split: users.segment("a").segment("b").get(),
    name: profile.name(), key: profile.key(), keys: profile.keys(), root: users.keys(), number: users.segment(123).get() === users.segment("123").get() });
});`,
	})
	if _, body := storageGet(t, tn, "/keys"); body != `{"joined":"one","key":"users:123:profile","keys":["123","profile"],"name":"users","number":true,"root":[],"split":"two"}` {
		t.Fatalf("/keys = %s", body)
	}
}

// What cannot be written as JSON is refused by name, never dropped: a function, a handle of the engine, the
// context; a value past the ceiling is INVALID_ARGUMENT; a wrong argument says how to write it (mục 5).
//
// CONTROL: with storage.Encode writing a function as null, set({ run: () => 1 }) answered ok: "function:
// {OK:true Code: Message:}", "a function was kept".
func TestStorageRefusesWhatCannotBeKept(t *testing.T) {
	saved := storageValueLimit
	storageValueLimit = 64
	t.Cleanup(func() { storageValueLimit = saved })
	tn := storageSite(t, map[string]string{
		"refuse/router.work": `const outcome = (r) => r.ok ? { ok: true } : { ok: false, code: r.error.code, message: r.error.message };
router.get((context) => {
  const kind = context.query("kind");
  const box = memory("box");
  if (kind === "function") { return context.json(outcome(safe(() => box.set({ run: () => 1 })))); }
  if (kind === "handle") { return context.json(outcome(safe(() => box.set(memory("other"))))); }
  if (kind === "context") { return context.json(outcome(safe(() => box.set(context)))); }
  if (kind === "large") { return context.json(outcome(safe(() => box.set("x".repeat(100))))); }
  if (kind === "ttl") { return context.json(outcome(safe(() => box.set(1, "5m")))); }
  if (kind === "expire") { return context.json(outcome(safe(() => box.expire("nonsense")))); }
  if (kind === "segment") { return context.json(outcome(safe(() => box.segment("")))); }
  return context.json(outcome(safe(() => box.segment({ id: 1 }))));
});`,
	})
	cases := map[string]storageFailureText{
		"function": {Code: "INVALID_ARGUMENT", Message: `memory("box").set: không lưu được hàm ở .run; giá trị lưu được là thứ viết được thành JSON (chuỗi, số, boolean, null, mảng, object thường) và bytes (KITWORK_STORAGE.md mục 5)`},
		"handle":   {Code: "INVALID_ARGUMENT", Message: `memory("box").set: không lưu được handle hay object của engine (database, job, routine, luồng, context); giá trị lưu được là thứ viết được thành JSON (chuỗi, số, boolean, null, mảng, object thường) và bytes (KITWORK_STORAGE.md mục 5)`},
		"context":  {Code: "INVALID_ARGUMENT", Message: `memory("box").set: không lưu được handle hay object của engine (database, job, routine, luồng, context); giá trị lưu được là thứ viết được thành JSON (chuỗi, số, boolean, null, mảng, object thường) và bytes (KITWORK_STORAGE.md mục 5)`},
		"large":    {Code: "INVALID_ARGUMENT", Message: `memory("box").set: giá trị dài hơn 101 byte, quá trần 64 byte của một giá trị (KITWORK_STORAGE.md mục 5)`},
		"ttl":      {Code: "INVALID_ARGUMENT", Message: `memory("box").set nhận một giá trị; hạn đặt trước bằng expire(): memory("box").expire("5m").set(giá_trị)`},
		"expire":   {Code: "INVALID_ARGUMENT", Message: `expire("nonsense"): không đọc được hạn; expire(hạn) nhận một khoảng ("30s", "5m", "1d"), số mili giây, hay một mốc giờ ("nextday 03:00")`},
		"segment":  {Code: "INVALID_ARGUMENT", Message: `segment(…) nhận chuỗi không rỗng hay số, đoạn thứ 1 là chuỗi rỗng: dựng khoá bằng từng đoạn, users.segment(id, "profile")`},
		"object":   {Code: "INVALID_ARGUMENT", Message: `segment(…) nhận chuỗi không rỗng hay số, đoạn thứ 1 là object: dựng khoá bằng từng đoạn, users.segment(id, "profile")`},
	}
	for kind, want := range cases {
		_, body := storageGet(t, tn, "/refuse?kind="+kind)
		if got := readFailure(t, body); got != want {
			t.Errorf("%s: %+v\nwant %+v", kind, got, want)
		}
	}
	if _, body := storageGet(t, tn, "/refuse?kind=function"); strings.Contains(body, `"ok":true`) {
		t.Fatal("a function was kept")
	}
}

// get(() => …) reads or computes: requests that miss one key at once run the function once, the others wait
// for it and read what it kept, with the handle's expiry (mục 6). Here the first request is held inside the
// function until the second is waiting for it.
//
// CONTROL: with readOrCompute running fn without a flight (every miss its own run), "the function ran 2
// times for two misses at once, want 1", and the second was never seen waiting.
func TestStorageReadOrComputeRunsOnceForMissesAtOnce(t *testing.T) {
	var runs atomic.Int32
	inside, waiting := newGate(), newGate()
	notifyHook(t, func(name string, _ ...value.Value) {
		if name == "computing" {
			runs.Add(1)
			inside.open()
			waiting.wait(t, "the second request waits for the first")
		}
	})
	saved := storageFlightWaiting
	storageFlightWaiting = func(key string) {
		if key == "menu" {
			waiting.open()
		}
	}
	t.Cleanup(func() { storageFlightWaiting = saved })
	tn := storageSite(t, map[string]string{
		"menu/router.work": `router.get((context) => context.json(memory("menu").expire("5m").get(() => { testNotify("computing"); return { items: ["phở", "bún"] }; })));`,
	})
	answers := make(chan string, 2)
	go func() { _, body := storageGet(t, tn, "/menu"); answers <- body }()
	inside.wait(t, "the first request computes")
	go func() { _, body := storageGet(t, tn, "/menu"); answers <- body }()
	for range 2 {
		select {
		case body := <-answers:
			if body != `{"items":["phở","bún"]}` {
				t.Errorf("answer = %s", body)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("a request did not answer")
		}
	}
	if got := runs.Load(); got != 1 {
		t.Fatalf("the function ran %d times for two misses at once, want 1", got)
	}
	if _, body := storageGet(t, tn, "/menu"); body != `{"items":["phở","bún"]}` || runs.Load() != 1 {
		t.Fatalf("a hit ran the function again: %s, %d runs", body, runs.Load())
	}
}

// The function of get(fn) runs on the caller's VM: a failure goes up to the caller as its own, which safe
// catches, and nothing is kept; the next call runs it again. A get(fn) of the same key from inside fn would
// wait for itself: it fails instead.
//
// CONTROL: without the RunsInside check the inner get waited on its own flight until the request's
// deadline: "nested = 500 after the 2s deadline: the inner get waited for itself".
func TestStorageReadOrComputeFailureAndReentry(t *testing.T) {
	tn := storageSite(t, map[string]string{
		"fail/router.work": `router.get((context) => {
  const first = safe(() => memory("rates").get(() => fail("nguồn hỏng")));
  const kept = memory("rates").exists();
  const again = memory("rates").get(() => 42);
  return context.json({ ok: first.ok, code: first.error.code, message: first.error.message, kept: kept, again: again });
});`,
		"nested/router.work": `router.get((context) => {
  const outer = safe(() => memory("loop").get(() => memory("loop").get(() => 1)));
  return context.json({ ok: outer.ok, code: outer.error.code, message: outer.error.message });
});`,
	})
	if _, body := storageGet(t, tn, "/fail"); body != `{"again":42,"code":"FAIL","kept":false,"message":"nguồn hỏng","ok":false}` {
		t.Fatalf("/fail = %s", body)
	}
	// a deadline of its own, so a request that waits for itself ends and the test reads why
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	rec := httptest.NewRecorder()
	tn.Serve(rec, httptest.NewRequest(http.MethodGet, "http://localhost/nested", nil).WithContext(ctx))
	if ctx.Err() != nil {
		t.Fatalf("nested = %d after the 2s deadline: the inner get waited for itself", rec.Code)
	}
	want := storageFailureText{Code: "INVALID_ARGUMENT", Message: `memory("loop").get(fn): hàm đang tính khoá "loop" lại đọc chính khoá đó bằng get(fn), sẽ đợi chính nó; đọc bằng get() hay tách khoá`}
	if got := readFailure(t, rec.Body.String()); got != want {
		t.Fatalf("nested = %+v, want %+v", got, want)
	}
}

// A file only declares while it loads (mục 9): get, set and the other actions at its top level fail the
// load at the place written; a handle made at the top level is fine, and used later by a handler.
//
// CONTROL: with the loadingVMs check removed from storageHandle.call, the file loaded, running set at load:
// "run = <nil>".
func TestStorageActionsRefusedAtFileLevel(t *testing.T) {
	root := writeSite(t, map[string]string{
		"router.work": `import * as router from "kitwork/router";`,
		"top/router.work": storageHeader + `memory("hits").set(1);
router.get((context) => context.json("loaded"));`,
	})
	tn := NewTenant(root, "localhost")
	t.Cleanup(tn.Close)
	err := tn.Run()
	want := `router.work:3:15: memory("hits").set ở cấp file: lúc nạp, file chỉ khai; tạo handle ở cấp file thì được (const flags = memory("hits")), còn set gọi trong một handler, một job, một lịch hay một hàm (KITWORK_STORAGE.md mục 9)`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("run = %v\nwant the load refused with %s", err, want)
	}

	declared := storageSite(t, map[string]string{
		"flags/router.work": `const flags = datastore("flags").segment("checkout");
const hits = memory("hits");
router.get((context) => { hits.set((hits.get() ?? 0) + 1); return context.json({ hits: hits.get(), key: flags.key() }); });`,
	})
	if _, body := storageGet(t, declared, "/flags"); body != `{"hits":1,"key":"flags:checkout"}` {
		t.Fatalf("/flags = %s", body)
	}
}

// The storage is the app's (mục 3, 9): two sites of one app read one value, another app with the same key
// sees nothing of it, and the app's own tenant (a job, a schedule) shares it with its sites.
//
// CONTROL: with the store kept on each Tenant instead of on the app's runtime, the second site of the app
// read null: "b.example of the same app = null".
func TestStorageIsTheAppsAndOnlyTheApps(t *testing.T) {
	root := t.TempDir()
	write := func(identity, domain, source string) {
		dir := filepath.Join(root, identity, domain)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "router.work"), []byte(storageHeader+source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const route = `router.get((context) => { if (context.query("set")) { memory("shared").set(context.query("set")); } return context.json(memory("shared").get()); });`
	write("alpha", "a.example", route)
	write("alpha", "b.example", route)
	write("beta", "c.example", route)
	open := func(appRuntime *app.Runtime, domain string) *Tenant {
		siteRuntime, err := appRuntime.Site(root, domain)
		if err != nil {
			t.Fatal(err)
		}
		generation, _ := siteRuntime.PrepareGeneration()
		tn := NewTenantWithRuntime(root, domain, appRuntime, siteRuntime, generation)
		if err := tn.Run(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(tn.Close)
		return tn
	}
	alpha, beta := app.NewRuntime("alpha"), app.NewRuntime("beta")
	t.Cleanup(alpha.Close)
	t.Cleanup(beta.Close)
	a, b, c := open(alpha, "a.example"), open(alpha, "b.example"), open(beta, "c.example")
	get := func(tn *Tenant, domain, path string) string {
		rec := httptest.NewRecorder()
		tn.Serve(rec, httptest.NewRequest(http.MethodGet, "http://"+domain+path, nil))
		return rec.Body.String()
	}
	if body := get(a, "a.example", "/?set=alpha"); body != `"alpha"` {
		t.Fatalf("a.example = %s", body)
	}
	if body := get(b, "b.example", "/"); body != `"alpha"` {
		t.Fatalf("b.example of the same app = %s, want \"alpha\"", body)
	}
	if body := get(c, "c.example", "/"); body != `null` {
		t.Fatalf("c.example of another app = %s, want null", body)
	}
	appTenant := NewAppTenantWithRuntime(root, "alpha", alpha)
	shared, err := appTenant.storage()
	if err != nil {
		t.Fatal(err)
	}
	data, found := shared.memory.Get(storage.Key{Place: storage.Memory, Name: "shared"}.Encoded())
	if decoded, _ := storage.Decode(data); !found || decoded.Text() != "alpha" {
		t.Fatalf("the app's own tenant reads %q %v, want the sites' value", decoded.Text(), found)
	}
}

// disk() and datastore() keep a value across a restart: the app's runtime closed and opened again on the
// same folder reads what was written, in .database/disk/ and .database/storage/ of the app, with its
// expiry; a value that has expired is gone (mục 2, 6).
//
// CONTROL: with diskPlace.set writing no file, the value written was not there to read: "written = {…
// \"disk\":null …}"; with datastoreAccess.set not committing, "written = {\"code\":null, … \"flag\":null, …}".
func TestStorageDiskAndDatastoreSurviveARestart(t *testing.T) {
	advance := storageTestClock(t)
	root := writeSite(t, map[string]string{
		"router.work": `import * as router from "kitwork/router";`,
		"keep/router.work": storageHeader + `router.get((context) => {
  if (context.query("write")) {
    disk("thumbnails").segment("p1", "small").set({ bytes: 3 });
    datastore("flags").segment("checkout").set({ newFlow: true });
    datastore("codes").segment("otp").expire("1m").set("123456");
  }
  return context.json({ disk: disk("thumbnails").segment("p1", "small").get(), flag: datastore("flags").segment("checkout").get(),
    code: datastore("codes").segment("otp").get(), ttl: datastore("codes").segment("otp").ttl() });
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
	if body := serve("/keep?write=1"); body != `{"code":"123456","disk":{"bytes":3},"flag":{"newFlow":true},"ttl":60000}` {
		t.Fatalf("written = %s", body)
	}
	for _, place := range []string{filepath.Join("disk"), filepath.Join("storage", datastoreFile)} {
		if _, err := os.Stat(filepath.Join(root, "test", ".database", place)); err != nil {
			t.Fatalf(".database/%s of the app: %v", place, err)
		}
	}
	advance(30 * time.Second)
	if body := serve("/keep"); body != `{"code":"123456","disk":{"bytes":3},"flag":{"newFlow":true},"ttl":30000}` {
		t.Fatalf("after a restart = %s", body)
	}
	advance(30 * time.Second)
	if body := serve("/keep"); body != `{"code":null,"disk":{"bytes":3},"flag":{"newFlow":true},"ttl":null}` {
		t.Fatalf("after the code expired = %s", body)
	}
}

// Full, disk() and datastore() refuse the write with TOO_LARGE and push nothing kept out (mục 9); a value
// that has expired is swept to make room first. memory() pushes out the key used least recently instead.
//
// CONTROL: with the ceiling check removed from diskPlace.set, the third key was written: ok true.
func TestStorageFullPlacesRefuseTheWrite(t *testing.T) {
	advance := storageTestClock(t)
	savedDisk, savedData, savedMemory := storageDiskKeys, storageDatastoreKeys, storageMemoryKeys
	storageDiskKeys, storageDatastoreKeys, storageMemoryKeys = 2, 2, 2
	t.Cleanup(func() { storageDiskKeys, storageDatastoreKeys, storageMemoryKeys = savedDisk, savedData, savedMemory })
	tn := storageSite(t, map[string]string{
		"fill/router.work": `const outcome = (r) => r.ok ? { ok: true } : { ok: false, code: r.error.code };
router.get((context) => {
  const place = context.query("place") === "disk" ? disk("full") : (context.query("place") === "datastore" ? datastore("full") : memory("full"));
  place.segment("a").expire("1m").set(1);
  place.segment("b").set(2);
  const third = outcome(safe(() => place.segment("c").set(3)));
  const kept = [place.segment("a").get(), place.segment("b").get()];
  return context.json({ third: third, kept: kept });
});`,
		"later/router.work": `router.get((context) => {
  const place = context.query("place") === "disk" ? disk("full") : datastore("full");
  return context.json({ ok: safe(() => place.segment("c").set(3)).ok, a: place.segment("a").get(), c: place.segment("c").get() });
});`,
	})
	for _, place := range []string{"disk", "datastore"} {
		_, body := storageGet(t, tn, "/fill?place="+place)
		if body != `{"kept":[1,2],"third":{"code":"TOO_LARGE","ok":false}}` {
			t.Errorf("%s full = %s, want TOO_LARGE and both kept", place, body)
		}
	}
	if _, body := storageGet(t, tn, "/fill?place=memory"); body != `{"kept":[null,2],"third":{"ok":true}}` {
		t.Errorf("memory full = %s, want the coldest key pushed out", body)
	}
	advance(2 * time.Minute)
	for _, place := range []string{"disk", "datastore"} {
		if _, body := storageGet(t, tn, "/later?place="+place); body != `{"a":null,"c":3,"ok":true}` {
			t.Errorf("%s after a expired = %s, want the expired value swept and c written", place, body)
		}
	}
}

// cache of "kitwork" is gone: its import, kitwork().cache and kitwork/cache fail to compile naming
// memory() of kitwork/storage; a word kitwork/storage has not fails the same way. Each is refused where it is
// written, inside a function no one calls at load too: the compiler sees it, not the first call.
//
// CONTROL: with storageDeclaration and storageMember taken out of the resolver, the forms written inside a
// function loaded: "const later = () => kitwork().cache.get(\"x\");: <nil>".
func TestStorageOldCacheSpellingsAreErrors(t *testing.T) {
	cases := map[string]string{
		`const later = () => kitwork().cache.get("x");`:                                     "cache đã bỏ: import { memory } from \"kitwork/storage\"",
		`const later = () => kitwork().sse;`:                                                "sse đã bỏ: luồng mở bằng context.sse trong router.get",
		`import * as storage from "kitwork/storage"; const later = () => storage.get("x");`: "storage.get không có: kitwork/storage có memory, disk, datastore",
		`import { router, cache } from "kitwork";`:                                          "cache đã bỏ: import { memory } from \"kitwork/storage\"",
		`import { router } from "kitwork"; const c = kitwork().cache;`:                      "cache đã bỏ: import { memory } from \"kitwork/storage\"",
		`import * as cache from "kitwork/cache";`:                                           "cache đã bỏ: import { memory } from \"kitwork/storage\"",
		`import { get } from "kitwork/storage";`:                                            "storage.get không có: kitwork/storage có memory, disk, datastore",
		`import * as storage from "kitwork/storage"; storage.get("x");`:                     "storage.get không có: kitwork/storage có memory, disk, datastore",
		`import { router, sse } from "kitwork";`:                                            "sse đã bỏ: luồng mở bằng context.sse trong router.get",
		`import { router } from "kitwork"; kitwork().sse.publish("a", 1);`:                  "sse đã bỏ: luồng mở bằng context.sse trong router.get",
	}
	for source, want := range cases {
		root := writeSite(t, map[string]string{
			"router.work":     `import * as router from "kitwork/router";`,
			"old/router.work": source + "\nimport * as router2 from \"kitwork/router\";\nrouter2.get((context) => context.json(1));",
		})
		tn := NewTenant(root, "localhost")
		err := tn.Run()
		tn.Close()
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v\nwant %s", source, err, want)
		}
	}
}

// serveWithin serves path with a deadline of its own, so a request that would wait for ever ends and the
// test reads why: the body, and whether the deadline came first.
func serveWithin(tn *Tenant, path string, within time.Duration) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), within)
	defer cancel()
	rec := httptest.NewRecorder()
	tn.Serve(rec, httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil).WithContext(ctx))
	return strings.TrimSpace(rec.Body.String()), ctx.Err() != nil
}

// Two get(fn) nested in opposite orders (fn of A reads B in one request, fn of B reads A in another) would
// each wait for the other: the request that would close the circle fails with CONFLICT, its flight ends,
// and the other goes on, computing B itself (mục 6).
//
// CONTROL: with Flights.Wait never refusing a circle, the two requests waited for each other until the
// first one's deadline ended it: "/a = Internal Server Error", and /b, let go only then, computed A itself:
// "/b = {\"code\":null,\"message\":null,\"ok\":true} (after its deadline: false), want CONFLICT".
func TestStorageReadOrComputeCircleOfKeysFailsOne(t *testing.T) {
	aLeads, bLeads, aWaits := newGate(), newGate(), newGate()
	notifyHook(t, func(name string, _ ...value.Value) {
		switch name {
		case "a-leads":
			aLeads.open()
			bLeads.wait(t, "the second request leads B")
		case "b-leads":
			bLeads.open()
			aWaits.wait(t, "the first request waits for B")
		}
	})
	saved := storageFlightWaiting
	storageFlightWaiting = func(key string) {
		if key == "B" {
			aWaits.open()
		}
	}
	t.Cleanup(func() { storageFlightWaiting = saved })
	tn := storageSite(t, map[string]string{
		"a/router.work": `router.get((context) => context.json(memory("A").get(() => { testNotify("a-leads"); return "a, " + memory("B").get(() => "b of a"); })));`,
		"b/router.work": `router.get((context) => {
  const r = safe(() => memory("B").get(() => { testNotify("b-leads"); return "b, " + memory("A").get(() => "a of b"); }));
  return context.json({ ok: r.ok, code: r.error.code, message: r.error.message });
});`,
	})
	type answer struct {
		path, body string
		late       bool
	}
	answers := make(chan answer, 2)
	serve := func(path string) {
		body, late := serveWithin(tn, path, 3*time.Second)
		answers <- answer{path, body, late}
	}
	go serve("/a")
	aLeads.wait(t, "the first request leads A")
	go serve("/b")
	got := map[string]answer{}
	for range 2 {
		select {
		case one := <-answers:
			got[one.path] = one
		case <-time.After(5 * time.Second):
			t.Fatal("a request did not answer")
		}
	}
	want := storageFailureText{Code: "CONFLICT", Message: `memory("A").get(fn): khoá "A" đang được một request khác tính, và hàm đó đang đợi chính khoá request này đang tính: hai get(fn) lồng nhau theo hai thứ tự ngược nhau; request này dừng để request kia đi tiếp (thử lại được), hay lồng các khoá theo một thứ tự`}
	if b := got["/b"]; b.late || !strings.HasPrefix(b.body, "{") || readFailure(t, b.body) != want {
		t.Errorf("/b = %s (after its deadline: %v), want CONFLICT", b.body, b.late)
	}
	if a := got["/a"]; a.late || a.body != `"a, b of a"` {
		t.Errorf("/a = %s (after its deadline: %v), want \"a, b of a\": B computed by the request that went on", a.body, a.late)
	}
	if app, _ := tn.storage(); app.flights.Running() != 0 {
		t.Errorf("%d flights left in the air", app.flights.Running())
	}
}

// panickingPlace is a place whose second read panics: the read inside the flight, after the miss.
type panickingPlace struct{ reads int }

func (p *panickingPlace) get(string) ([]byte, bool, error) {
	p.reads++
	if p.reads == 2 {
		panic("place broke")
	}
	return nil, false, nil
}
func (p *panickingPlace) set(string, []byte, time.Time) error    { return nil }
func (p *panickingPlace) expiry(string) (time.Time, bool, error) { return time.Time{}, false, nil }
func (p *panickingPlace) remove(string) (bool, error)            { return false, nil }

// A run of get(fn) that panics still ends its flight: the panic goes up to the boundary that reports it, and
// the key is free for the next miss, which leads instead of waiting for a run that is over.
//
// CONTROL: with End called after compute instead of deferred, "a flight that panicked is still in the air:
// 1 running".
func TestStorageFlightEndsWhenItsRunPanics(t *testing.T) {
	app := &appStorage{}
	handle := &storageHandle{key: storage.Key{Place: storage.Memory, Name: "rates"}}
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		handle.readOrCompute(nil, app, &panickingPlace{}, "rates", nil)
	}()
	if recovered != "place broke" {
		t.Fatalf("recovered %v, want the place's panic to go up", recovered)
	}
	if running := app.flights.Running(); running != 0 {
		t.Fatalf("a flight that panicked is still in the air: %d running", running)
	}
	if _, leading := app.flights.Begin("rates", "next"); !leading {
		t.Fatal("the next miss waits for a run that is over")
	}
}

// A waiter whose leader failed runs fn itself and gets its own value (mục 6: the leader's failure is the
// leader's); a waiter whose request ends while it waits stops waiting at once, and the leader goes on.
//
// CONTROL: with waitFor giving up once the flight ended (not reading again), the second request read null:
// "first = \"FAIL: nguồn hỏng\", second = null, want its own value"; with the stop case taken out of
// waitFor's select, the cancelled waiter answered only once the leader was let go: "the cancelled waiter
// did not return within 1s".
func TestStorageReadOrComputeWaitersAfterAFailedLeadAndACancel(t *testing.T) {
	inside, waiting, release := newGate(), newGate(), newGate()
	var gates sync.Mutex
	current := func() (*gate, *gate) { gates.Lock(); defer gates.Unlock(); return inside, waiting }
	notifyHook(t, func(name string, args ...value.Value) {
		if name != "computing" || len(args) == 0 {
			return
		}
		in, wait := current()
		switch args[0].Text() {
		case "first":
			in.open()
			wait.wait(t, "the second request waits for the first")
		case "held":
			in.open()
			release.wait(t, "the test lets the leader go")
		}
	})
	saved := storageFlightWaiting
	storageFlightWaiting = func(key string) { _, wait := current(); wait.open() }
	t.Cleanup(func() { storageFlightWaiting = saved })
	tn := storageSite(t, map[string]string{
		"rates/router.work": `router.get((context) => {
  const who = context.query("who");
  const r = safe(() => memory("rates").segment(context.query("key")).get(() => { testNotify("computing", who); return who === "first" ? fail("nguồn hỏng") : who + " own"; }));
  return context.json(r.ok ? r.value : r.error.code + ": " + r.error.message);
});`,
	})
	answers := make(chan string, 1)
	go func() { body, _ := serveWithin(tn, "/rates?key=a&who=first", 3*time.Second); answers <- body }()
	inside.wait(t, "the first request computes")
	second, _ := serveWithin(tn, "/rates?key=a&who=second", 3*time.Second)
	first := <-answers
	if first != `"FAIL: nguồn hỏng"` || second != `"second own"` {
		t.Fatalf("first = %s, second = %s, want its own value", first, second)
	}

	gates.Lock()
	inside, waiting = newGate(), newGate()
	gates.Unlock()
	go func() { body, _ := serveWithin(tn, "/rates?key=b&who=held", 5*time.Second); answers <- body }()
	held, waits := current()
	held.wait(t, "the leader computes")
	ctx, cancel := context.WithCancel(context.Background())
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		tn.Serve(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://localhost/rates?key=b&who=cancelled", nil).WithContext(ctx))
	}()
	waits.wait(t, "the waiter waits for the leader")
	cancel()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Error("the cancelled waiter did not return within 1s")
	}
	release.open()
	if body := <-answers; body != `"held own"` {
		t.Fatalf("the leader = %s, want its value", body)
	}
	<-returned
}

// disk() never reads or writes outside the app's folder: a .database/disk/ that leads elsewhere is refused
// with DENIED at every access, and nothing is written there. Here the folder of the place is set outside
// the app directly, as a link would resolve it; TestStorageDiskRefusesALinkOutOfTheApp makes the link
// where the system lets a test make one.
//
// CONTROL: with diskPlace.inside refusing nothing, the value was written outside the app: "set = <nil>,
// want the path refused".
func TestStorageDiskStaysInsideTheApp(t *testing.T) {
	appRoot, elsewhere := t.TempDir(), t.TempDir()
	boundary, err := safepath.NewBoundary(appRoot)
	if err != nil {
		t.Fatal(err)
	}
	place := &diskPlace{dir: filepath.Join(elsewhere, "disk"), app: boundary}
	key := storage.Key{Place: storage.Disk, Name: "x"}.Encoded()
	if err := place.set(key, []byte("1"), time.Time{}); !errors.Is(err, errStorageOutside) {
		t.Fatalf("set = %v, want the path refused", err)
	}
	if _, _, err := place.get(key); !errors.Is(err, errStorageOutside) {
		t.Fatalf("get = %v, want the path refused", err)
	}
	if _, err := place.remove(key); !errors.Is(err, errStorageOutside) {
		t.Fatalf("delete = %v, want the path refused", err)
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Fatalf("written outside the app: %v", entries)
	}
	failure, _ := value.FailureFrom(storageFailure(`disk("x")`, place.set(key, []byte("1"), time.Time{})))
	want := value.Failure{Code: value.DeniedCode, Message: `disk("x"): disk() đi ra ngoài thư mục của app: .database/disk/ của app dẫn tới chỗ khác qua một liên kết; không đọc, không ghi ở đó (KITWORK_STORAGE.md mục 9)`}
	if failure.Code != want.Code || failure.Message != want.Message {
		t.Fatalf("failure = %s %q\nwant %s %q", failure.Code, failure.Message, want.Code, want.Message)
	}
}

// A .database/disk/ that is a link into another folder is refused end to end (skipped where the system does
// not let a test make a link, as Windows without the privilege).
func TestStorageDiskRefusesALinkOutOfTheApp(t *testing.T) {
	elsewhere := t.TempDir()
	tn := storageSite(t, map[string]string{
		"write/router.work": `router.get((context) => { const r = safe(() => disk("x").set(1)); return context.json({ code: r.error.code }); });`,
	})
	database := tn.resolveApp(".database")
	if err := os.MkdirAll(database, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(database, "disk")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, body := storageGet(t, tn, "/write"); body != `{"code":"DENIED"}` {
		t.Fatalf("/write through a link = %s, want DENIED", body)
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Fatalf("written through the link: %v", entries)
	}
}

// Full, disk() and datastore() sweep only when something kept can have expired since they last looked: a
// place full of values that never expire refuses each further write without reading all it holds again,
// and sweeps again once a value it keeps has expired (mục 9, 10.5).
//
// CONTROL: with storageSweepDue always true, every refused write swept again: "disk swept 4 times, want 2",
// "datastore swept 4 times, want 1".
func TestStorageFullPlaceSweepsOnlyWhenSomethingCanHaveExpired(t *testing.T) {
	advance := storageTestClock(t)
	savedDisk, savedData := storageDiskKeys, storageDatastoreKeys
	storageDiskKeys, storageDatastoreKeys = 2, 2
	t.Cleanup(func() { storageDiskKeys, storageDatastoreKeys = savedDisk, savedData })
	var mu sync.Mutex
	sweeps := map[string]int{}
	savedSwept := storageSwept
	storageSwept = func(place string) { mu.Lock(); sweeps[place]++; mu.Unlock() }
	t.Cleanup(func() { storageSwept = savedSwept })
	tn := storageSite(t, map[string]string{
		"put/router.work": `router.get((context) => {
  const place = context.query("place") === "disk" ? disk("full") : datastore("full");
  const handle = context.query("expire") ? place.segment(context.query("key")).expire(context.query("expire")) : place.segment(context.query("key"));
  const r = safe(() => handle.set(1));
  return context.json(r.ok ? "ok" : r.error.code);
});`,
	})
	for _, place := range []string{"disk", "datastore"} {
		put := func(key, expire string) string {
			_, body := storageGet(t, tn, "/put?place="+place+"&key="+key+"&expire="+expire)
			return body
		}
		steps := []struct{ key, expire, want string }{
			{"a", "10s", `"ok"`}, {"b", "", `"ok"`},
			{"c", "", `"TOO_LARGE"`}, // full: disk has not read its files yet (a sweep); datastore counted a's expiry
			{"c", "", `"TOO_LARGE"`}, // a has not expired: no sweep
		}
		for _, step := range steps {
			if got := put(step.key, step.expire); got != step.want {
				t.Fatalf("%s: set %s = %s, want %s", place, step.key, got, step.want)
			}
		}
		advance(11 * time.Second)
		if got := put("c", ""); got != `"ok"` {
			t.Fatalf("%s: set c once a expired = %s, want a swept and c kept", place, got)
		}
		if got := put("d", ""); got != `"TOO_LARGE"` { // b and c never expire: no sweep
			t.Fatalf("%s: set d = %s, want TOO_LARGE", place, got)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	for place, want := range map[string]int{"disk": 2, "datastore": 1} {
		if sweeps[place] != want {
			t.Errorf("%s swept %d times, want %d", place, sweeps[place], want)
		}
	}
}
