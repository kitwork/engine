package work

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/value"
)

// The router of a Kitwork UI folder answers only its words when it runs (KITWORK_ROUTER.md mục 3, 11): the
// compiler refuses each word below where a router.work writes it, and one it could not follow (a source
// compiled with no file's name keeps every old form) is refused by the router as it runs, in the compiler's
// words from the same table: a removed word, the failure hooks among them, naming the new spelling; an unknown
// one naming the nearest; a folder call chained; a door chained with what is no rule of a route. A template's
// folder keeps every old word: the same lines run there.
//
// CONTROL (go test -overlay, 24/10/2026): with TreeKitWork.Router giving the folder itself to a Kitwork UI
// folder too, every row went red: each ran (router.ratelimit counted, router.colours said and passed over,
// .error() and router.error() kept, the second .cache taken over the first), but .cach, which failed as a
// call of null.
func TestUIRouterRefusesWhatTheCompilerCouldNotFollow(t *testing.T) {
	tenant, err := bootAppSite(t, t.TempDir(), map[string]string{
		"acme/localhost/router.work": routerImport + "router.get((context) => context.text(\"x\"));\n",
	}, "localhost")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	run := func(ui bool, source string) error {
		t.Helper()
		node := &RouteNode{seg: "nhanh", parent: tenant.routeTree().root}
		node.folder = &FolderRouter{tenant: tenant, node: node, ui: ui, running: UIRouterFileName,
			methods: map[string]*FolderMethod{}, outputs: map[string]*FolderMethod{}, meta: map[string]value.Value{}}
		bc, err := compiler.CompileSource(routerImport + "const r = router;\n" + source + "\n")
		if err != nil {
			t.Fatalf("%s: compile: %v", source, err)
		}
		return runFolderRouter(tenant, node, bc)
	}
	const handler = `(context) => context.text("x")`
	removed := func(word string) string {
		message, _ := compiler.RouterRemovedWord(word)
		return message
	}
	hook := func(word string) string {
		message, _ := compiler.RouteRuleRemoved("get", word)
		return message
	}
	for _, c := range []struct{ source, want string }{
		{`r.ratelimit({ ip: 5, second: 1 });`, removed("ratelimit")},
		{`r.title("Quán");`, removed("title")},
		{`r.error(` + handler + `);`, removed("error")},
		{`r.notfound(` + handler + `);`, removed("notfound")},
		{`r.colours({ brand: "#111111" });`, compiler.RouterUnknownWord("colours", false)},
		{`r.GET(` + handler + `);`, compiler.RouterUnknownWord("GET", false)},
		{`r.guard(() => true).get(` + handler + `);`, compiler.RouterCallChained("guard", ".get(…)")},
		{`r.language("vi").x;`, compiler.RouterCallChained("language", ".x")},
		{`r.get(` + handler + `).error(` + handler + `);`, hook("error")},
		{`r.get(` + handler + `).success(` + handler + `);`, hook("success")},
		{`r.get(` + handler + `).finally(` + handler + `);`, hook("finally")},
		{`r.get(` + handler + `).then(` + handler + `);`, hook("then")},
		{`r.get(` + handler + `).catch(` + handler + `);`, hook("catch")},
		{`r.get(` + handler + `).ratelimit({ ip: 5 });`, hook("ratelimit")},
		{`r.get(` + handler + `).cach("5m");`, compiler.RouteRuleUnknown("get", "cach")},
		{`r.get(` + handler + `).cache("5m").cache("1h");`, ".cache nối hai lần sau router.get(…)"},
	} {
		if err := run(true, c.source); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: run = %v\nwant %s", c.source, err, c.want)
		}
	}
	for _, source := range []string{
		`r.ratelimit({ ip: 5, second: 1 });`,
		`r.error(` + handler + `);`,
		`r.colours({ brand: "#111111" });`,
		`r.get(` + handler + `).error(` + handler + `).ratelimit({ ip: 5 });`,
	} {
		if err := run(false, source); err != nil {
			t.Errorf("CONTROL: a template's folder keeps %s, run = %v", source, err)
		}
	}
}

// router.limit({ rate, per, by }) of a folder (KITWORK_ROUTER.md mục 4): one count for each client and each
// rule over the folder's subtree, the parent's and the child's added up, all before any guard. A route's
// .limit() runs with them, before the guards, in a Kitwork UI site; a template's after its folder's guards,
// as before.
//
// CONTROL (go test -overlay, 24/10/2026): with addLimit keeping no rule, every request answered 200; with the
// route's limit checked after the guards again, the guarded route answered 403 twice.
func TestRouterLimitAtTheFolder(t *testing.T) {
	tenant, err := bootAppSite(t, t.TempDir(), map[string]string{
		"acme/localhost/router.work":     routerImport + "router.limit({ rate: 3, per: \"1m\", by: \"global\" });\nrouter.get((context) => context.text(\"root\"));\n",
		"acme/localhost/a/config.work":   routerImport + "router.limit({ rate: 2, per: \"1m\" });\n",
		"acme/localhost/a/b/router.work": routerImport + "router.get((context) => context.text(\"b\"));\n",
		"acme/localhost/c/router.work":   routerImport + "router.get((context) => context.text(\"c\"));\n",
	}, "localhost")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	var codes []int
	for _, target := range []string{"/a/b", "/a/b", "/a/b", "/c"} {
		rec := serveAt(tenant, http.MethodGet, "http://localhost"+target)
		codes = append(codes, rec.Code)
		if rec.Code == http.StatusTooManyRequests && rec.Header().Get("Retry-After") != "60" {
			t.Errorf("%s: Retry-After = %q, want 60", target, rec.Header().Get("Retry-After"))
		}
	}
	// the branch's 2 for a client stops the third /a/b; the root's 3 for everyone stops /c after it
	if want := []int{200, 200, 429, 429}; !equalInts(codes, want) {
		t.Errorf("codes = %v, want %v", codes, want)
	}

	const guarded = "router.guard(() => false);\n"
	ui, err := bootAppSite(t, t.TempDir(), map[string]string{
		"acme/localhost/router.work": routerImport + guarded + "router.get((context) => context.text(\"x\")).limit({ rate: 1, per: \"1m\" });\n",
	}, "localhost")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	template, err := bootAppSite(t, t.TempDir(), map[string]string{
		"acme/localhost/router.kitwork.js": "import { router } from \"kitwork\";\n" + guarded + "router.get((ctx) => ctx.text(\"x\")).limit({ rate: 1, per: \"1m\" });\n",
	}, "localhost")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, c := range []struct {
		name   string
		tenant *Tenant
		want   []int
	}{
		{"router.work", ui, []int{403, 429}},             // the limit before the guard
		{"router.kitwork.js", template, []int{403, 403}}, // the guard first, as before
	} {
		got := []int{serveAt(c.tenant, http.MethodGet, "http://localhost/").Code, serveAt(c.tenant, http.MethodGet, "http://localhost/").Code}
		if !equalInts(got, c.want) {
			t.Errorf("%s: codes = %v, want %v", c.name, got, c.want)
		}
	}

	// HEAD runs as GET (KITWORK_ROUTER.md mục 2), so it counts against the GET route's own limit, one count with
	// GET's. CONTROL (go test -overlay, 24/10/2026): with routeMethod not taking GET's route for HEAD, both rows
	// answered [200 200].
	for _, methods := range [][]string{{http.MethodHead, http.MethodHead}, {http.MethodGet, http.MethodHead}} {
		limited, err := bootAppSite(t, t.TempDir(), map[string]string{
			"acme/localhost/router.work": routerImport + "router.get((context) => context.text(\"x\")).limit({ rate: 1, per: \"1m\" });\n",
		}, "localhost")
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		got := []int{serveAt(limited, methods[0], "http://localhost/").Code, serveAt(limited, methods[1], "http://localhost/").Code}
		if want := []int{200, 429}; !equalInts(got, want) {
			t.Errorf("%s then %s: codes = %v, want %v", methods[0], methods[1], got, want)
		}
	}
}

func equalInts(got, want []int) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// A rate limit of a router.work or a config.work is read strictly (KITWORK_ROUTER.md mục 4): rate a whole
// number above 0, per a span written as a word, by "ip" or "global", nothing else; a rule it cannot read stops
// the boot where it is written, never a one-second window read some other way. A template keeps its lenient
// router.ratelimit (TestTemplateRateLimitByWarnsOnce).
//
// CONTROL (go test -overlay, 24/10/2026): with limitRule reading as FolderMethod.Limit does (no shape refused,
// a key it does not know passed over), every row went red: each booted, but the two of by, which checkLimitBy
// still refused (by: 3 as by: "3" không có). With limitRule taking any text of by, the by: "" rows booted and
// by: "IP" stopped only as checkLimitBy's "không có". With output() giving a built-in door no folder, the
// sitemap row booted.
func TestRouterLimitIsReadStrictly(t *testing.T) {
	for _, c := range []struct{ written, want string }{
		{`router.limit(5, "1m");`, `router.work:2:7: router.limit nhận một object { rate, per, by }`},
		{`router.limit({ rate: 0, per: "1m" });`, `router.limit({ rate: 0 }): rate là một số nguyên lớn hơn 0`},
		{`router.limit({ rate: 1.5, per: "1m" });`, `router.limit({ rate: 1.5 }): rate là một số nguyên lớn hơn 0`},
		{`router.limit({ rate: "5", per: "1m" });`, `router.limit({ rate: "5" }): rate là một số nguyên lớn hơn 0`},
		{`router.limit({ per: "1m" });`, `router.limit thiếu rate`},
		{`router.limit({ rate: 5 });`, `router.limit thiếu per`},
		{`router.limit({ rate: 5, per: "soon" });`, `router.limit({ per: "soon" }): per là một khoảng viết bằng chữ, "30s", "1m", "1h"`},
		{`router.limit({ rate: 5, per: 60000 });`, `router.limit({ per: 60000 }): per là một khoảng viết bằng chữ`},
		{`router.limit({ rate: 5, per: "1m", by: 3 });`, `router.limit({ by: 3 }): by là một chữ, "ip" (mặc định) hay "global"`},
		{`router.limit({ rate: 5, per: "1m", type: "ip" });`, `router.limit({ type: … }): không có khoá "type"; ý là by?`},
		{`router.limit({ ip: 5, minute: 1 });`, `router.limit({ ip: … }): không có khoá "ip"; đó là cách viết của router.ratelimit đã bỏ`},
		{`router.limit({ rate: 5, per: "1m", by: "browser" });`, `dòng 2: giới hạn tần suất by: "browser" lấy từ header hay cookie client tự gửi`},
		// by is "ip" or "global" and nothing else, an empty one or one in capitals included (24/10/2026)
		{`router.limit({ rate: 5, per: "1m", by: "" });`, `router.work:2:7: router.limit({ by: "" }): by là "ip" (mặc định) hay "global": router.limit({ rate: 600, per: "1m", by: "ip" })`},
		{`router.limit({ rate: 5, per: "1m", by: "IP" });`, `router.work:2:7: router.limit({ by: "IP" }): by là "ip" (mặc định) hay "global"; ý là "ip"?`},
		{`router.get((context) => context.text("x")).limit({ rate: 5, per: "1m", by: "" });`, `router.get(…).limit({ by: "" }): by là "ip" (mặc định) hay "global"`},
		// a built-in door's rules are its folder's: read as strictly
		{`router.sitemap(true).limit({ rate: 1, per: "1m", by: "browser" });`, `dòng 2: giới hạn tần suất by: "browser" lấy từ header hay cookie client tự gửi`},
		{`router.get((context) => context.text("x")).limit({ rate: 5, per: "soon" });`, `router.get(…).limit({ per: "soon" }): per là một khoảng viết bằng chữ`},
		{`router.post((context) => context.text("x")).limit({ rate: 0, per: "1m" });`, `router.post(…).limit({ rate: 0 }): rate là một số nguyên lớn hơn 0`},
	} {
		_, err := bootAppSite(t, t.TempDir(), map[string]string{"acme/localhost/router.work": routerImport + c.written + "\n"}, "localhost")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: run = %v\nwant %s", c.written, err, c.want)
		}
	}
}

// The boundaries of a route's cache count in the app's zone, router.timezone(…) of the config.work at the
// app's folder (KITWORK_ROUTER.md mục 9), UTC when it writes none: "hourly" at 15:40 in Asia/Kolkata keeps
// 20 minutes, to 16:00 there, not 50 (to the next hour of UTC, 16:30 there); "nextday 03:00" at 17:10 in
// Asia/Ho_Chi_Minh keeps 9h50, not 16h50. A template site keeps the server's clock.
//
// CONTROL (go test -overlay, 24/10/2026): with routeNow giving the clock without turning it to the app's zone,
// the Kolkata row kept 50 minutes and the Ho Chi Minh row 16h50; with "hourly" truncating the absolute time as
// before, the Kolkata row kept 50 minutes; with routeNow keeping the server's clock when the app declares no
// zone, the two rows of no zone (a server at +05:30) kept 20m and 11h20m.
func TestRouteCacheCountsInTheAppsZone(t *testing.T) {
	at := time.Date(2026, 10, 25, 10, 10, 0, 0, time.UTC)
	saved := routeCacheClock
	t.Cleanup(func() { routeCacheClock = saved })
	// a server whose clock reads 15:40, half an hour off a whole hour of UTC: a site that declares no zone
	// counts in UTC, not in the server's
	halfHour := time.FixedZone("server", 5*60*60+30*60)
	for _, c := range []struct {
		name, zone, expiry string
		server             *time.Location
		want               time.Duration
	}{
		{"Asia/Kolkata", "Asia/Kolkata", "hourly", time.UTC, 20 * time.Minute},
		{"no zone: UTC", "", "hourly", halfHour, 50 * time.Minute},
		{"Asia/Ho_Chi_Minh", "Asia/Ho_Chi_Minh", "nextday 03:00", time.UTC, 9*time.Hour + 50*time.Minute},
		{"no zone: UTC, next day", "", "nextday 03:00", halfHour, 16*time.Hour + 50*time.Minute},
	} {
		server := c.server
		routeCacheClock = func() time.Time { return at.In(server) }
		files := map[string]string{
			"acme/localhost/router.work": routerImport + "router.get((context) => context.text(\"x\")).persist(\"" + c.expiry + "\");\n",
		}
		if c.zone != "" {
			files["acme/config.work"] = routerImport + "router.timezone(\"" + c.zone + "\");\n"
		}
		tenant, err := bootAppSite(t, t.TempDir(), files, "localhost")
		if err != nil {
			t.Fatalf("%s: run: %v", c.name, err)
		}
		if rec := serveAt(tenant, http.MethodGet, "http://localhost/"); rec.Code != http.StatusOK {
			t.Fatalf("%s: GET / = %d", c.name, rec.Code)
		}
		key, ok := cacheKey(httptest.NewRequest(http.MethodGet, "http://localhost/", nil), tenant.routeTree().root.folder.methods[http.MethodGet])
		record, stored := tenant.persistStore.Get(hashKey(key))
		if !ok || !stored {
			t.Fatalf("%s: the answer was not persisted", c.name)
		}
		if left := time.Until(record.ExpireAt); left < c.want-time.Minute || left > c.want+time.Minute {
			t.Errorf("%s: %q keeps %s, want %s", c.name, c.expiry, left.Round(time.Minute), c.want)
		}
	}
	template, err := bootAppSite(t, t.TempDir(), map[string]string{
		"acme/config.work":                 routerImport + "router.timezone(\"Asia/Kolkata\");\n",
		"acme/localhost/router.kitwork.js": "import { router } from \"kitwork\";\nrouter.get((ctx) => ctx.text(\"x\")).persist(\"hourly\");\n",
	}, "localhost")
	if err != nil {
		t.Fatalf("template: run: %v", err)
	}
	server := time.FixedZone("server", 3*60*60)
	routeCacheClock = func() time.Time { return at.In(server) }
	if got := template.routeNow(); got.Location() != server {
		t.Errorf("CONTROL: a template site keeps the server's clock, got %s", got.Location())
	}
}

// "hourly" ends at the next o'clock the wall clock of now's zone reads, a zone half an hour off UTC included,
// and on a day the zone changes its offset: in the hour a clock turned back repeats, the o'clock comes after
// 30 minutes (01:30 EDT, then 01:00 EST in New York), not 1h30; a zone turning half an hour reads its o'clock
// after both halves (Australia/Lord_Howe).
//
// CONTROL (go test -overlay, 24/10/2026): with Truncate(time.Hour) as before, Asia/Kolkata kept 50m and both
// Lord_Howe rows 15m; with the next hour by the calendar (Hour()+1), New York and Paris on the day they fall back
// kept 1h30m.
func TestHourlyLandsOnTheHourOfTheZone(t *testing.T) {
	for _, zone := range []string{"Asia/Kolkata", "Asia/Ho_Chi_Minh", "UTC"} {
		location, err := time.LoadLocation(zone)
		if err != nil {
			t.Fatal(err)
		}
		now := time.Date(2026, 10, 25, 15, 40, 0, 0, location)
		if got := calendarExpiry("hourly")(now); got != 20*time.Minute {
			t.Errorf("%s: hourly at 15:40 keeps %s, want 20m", zone, got)
		}
	}
	for _, c := range []struct {
		zone, at string
		want     time.Duration
	}{
		{"America/New_York", "2026-11-01T05:30:00Z", 30 * time.Minute},    // 01:30 EDT, then 01:00 EST
		{"America/New_York", "2026-11-01T06:30:00Z", 30 * time.Minute},    // 01:30 EST, then 02:00 EST
		{"Europe/Paris", "2026-10-25T00:30:00Z", 30 * time.Minute},        // 02:30 CEST, then 02:00 CET
		{"America/New_York", "2026-03-08T06:30:00Z", 30 * time.Minute},    // 01:30 EST, then 03:00 EDT
		{"Australia/Lord_Howe", "2026-04-04T14:45:00Z", 45 * time.Minute}, // 01:45 +11, 01:30 +10:30, then 02:00
		{"Australia/Lord_Howe", "2026-10-03T15:15:00Z", 45 * time.Minute}, // 01:45 +10:30, 02:30 +11, then 03:00
	} {
		location, err := time.LoadLocation(c.zone)
		if err != nil {
			t.Fatal(err)
		}
		now := instant(t, c.at).In(location)
		got := calendarExpiry("hourly")(now)
		if end := now.Add(got); got != c.want || end.Minute() != 0 || end.Second() != 0 {
			t.Errorf("%s at %s: hourly keeps %s, to %s; want %s", c.zone, now.Format("15:04 -07:00"), got, end.Format("15:04 -07:00"), c.want)
		}
	}
}

// What a site's generation is built from includes every config.work it reads (KITWORK_ROUTER.md mục 15): one
// appearing in a folder, the app's appearing, and a library the app's imports changing are each a change of
// the site, so the next generation is built from them.
//
// CONTROL (go test -overlay, 24/10/2026): with the files of appRouterConfig not watched, the second and third
// rows saw no change; with compileUIFolder not watching what a folder's config.work imports, the fourth. The
// first stays green without config.work's own watch: the site's .work and .ui files are watched as its tree of
// templates as well (site.SourceManifest), the app's are outside it.
func TestConfigWorkIsWatched(t *testing.T) {
	base := map[string]string{
		"acme/localhost/router.work":   routerImport + "router.get((context) => context.text(\"x\"));\n",
		"acme/localhost/a/router.work": routerImport + "router.get((context) => context.text(\"a\"));\n",
	}
	for _, c := range []struct {
		name   string
		before map[string]string
		change map[string]string
	}{
		{"a branch's config.work appears", nil, map[string]string{"acme/localhost/a/config.work": routerImport + "router.language(\"vi\");\n"}},
		{"the app's config.work appears", nil, map[string]string{"acme/config.work": routerImport + "router.language(\"vi\");\n"}},
		{"a library of the app's changes",
			map[string]string{
				"acme/.shared/brand.work": "export const brand = \"#111111\";\n",
				"acme/config.work":        routerImport + "import { brand } from \"app/.shared/brand.work\";\nrouter.colors({ brand });\n",
			},
			map[string]string{"acme/.shared/brand.work": "export const brand = \"#222222\";\n"}},
		{"a library of a branch's config.work changes",
			map[string]string{
				"acme/.shared/rules.work":      "export const closed = (context) => false;\n",
				"acme/localhost/a/config.work": routerImport + "import { closed } from \"app/.shared/rules.work\";\nrouter.guard(closed);\n",
			},
			map[string]string{"acme/.shared/rules.work": "export const closed = (context) => true;\n"}},
	} {
		root := t.TempDir()
		files := map[string]string{}
		for name, source := range base {
			files[name] = source
		}
		for name, source := range c.before {
			files[name] = source
		}
		tenant, err := bootAppSite(t, root, files, "localhost")
		if err != nil {
			t.Fatalf("%s: run: %v", c.name, err)
		}
		if changed, err := tenant.SourcesChanged(); err != nil || changed {
			t.Fatalf("%s: changed before the change = %v, %v", c.name, changed, err)
		}
		writeAppTree(t, root, c.change)
		if changed, err := tenant.SourcesChanged(); err != nil || !changed {
			t.Errorf("%s: changed = %v, %v; want the generation to see it", c.name, changed, err)
		}
	}
}
