package work

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kitwork/engine/app"
	queuestore "github.com/kitwork/engine/utilities/queue"
)

// scheduleEntries is the schedules the app's clock runs, by name.
func scheduleEntries(t *testing.T, tenant *Tenant) map[string]*scheduleEntry {
	t.Helper()
	worker := tenant.scheduleWorker()
	if worker == nil {
		t.Fatal("the app has no clock")
	}
	worker.mu.Lock()
	defer worker.mu.Unlock()
	entries := map[string]*scheduleEntry{}
	for _, entry := range worker.book.entries {
		entries[entry.name] = entry
	}
	return entries
}

// A schedule that writes no .timezone(…) counts in the app's zone, router.timezone(…) of the config.work at
// the app's folder (KITWORK_SCHEDULE.md mục 5, KITWORK_ROUTER.md mục 4): daily("09:00") at 08:30 in
// Asia/Ho_Chi_Minh is due at 09:00 there, 02:00 UTC, and boot says nothing of UTC; one with its own zone keeps
// it. The zone is in the state the clock keeps, so a zone the app changes counts the schedule anew, as another
// rule does.
//
// CONTROL (go test -overlay, 24/10/2026): with runScheduleFile counting it in scheduleDefaultLocation as before,
// sang was next due at 09:00 UTC and its state ended in UTC.
func TestScheduleCountsInTheAppsZone(t *testing.T) {
	now := instant(t, "2026-10-25T01:30:00Z")
	setClock(t, now)
	q := newQueueApp(t, map[string]string{
		"config.work": routerImport + "router.timezone(\"Asia/Ho_Chi_Minh\");\n",
		".schedule/baoCao.work": `import * as schedule from "kitwork/schedule";
export const sang = schedule.daily("09:00").run((context) => 1);
export const rieng = schedule.daily("09:00").timezone("UTC").run((context) => 1);
`,
	})
	entries := scheduleEntries(t, q.app)
	for name, want := range map[string]string{"sang": "2026-10-25T02:00:00Z", "rieng": "2026-10-25T09:00:00Z"} {
		entry := entries[name]
		if entry == nil {
			t.Fatalf("%s is not on the clock:\n%s", name, q.output)
		}
		next, ok := entry.next(now)
		if !ok || !next.Equal(instant(t, want)) {
			t.Errorf("%s is next due %s, want %s", name, next.UTC().Format(time.RFC3339), want)
		}
	}
	if !strings.HasSuffix(entries["sang"].state, " Asia/Ho_Chi_Minh") {
		t.Errorf("sang's state %q does not carry the app's zone", entries["sang"].state)
	}
	if strings.Contains(q.output, "không viết .timezone") {
		t.Errorf("boot said a schedule counts in UTC in an app that declares its zone:\n%s", q.output)
	}
}

// One app in the layout of one app (RootLayoutSingle): its folder is its site's, so the config.work there is the
// app's and the site's at once, read the same way by the app's clock as by the site (appFolderIsASite, sameFolder):
// router.timezone beside router.reload and router.guard. The schedules count in the app's zone with nothing said
// of the file or of UTC, and the site guards and counts its cache in the same zone. The clock of an app with no
// router file yet (schedules only) reads its config.work the same way.
//
// CONTROL (go test -overlay, 24/10/2026): with appFolderIsASite answering false, the clock said "[Schedule]
// initialize router app/config.work: config.work:3:7: router.reload chỉ viết ở gốc site…" and sang counted in
// UTC (next due 09:00Z) while the site kept Asia/Ho_Chi_Minh; with it reading only a router file as before, the
// app with no router file did the same.
func TestOneAppInItsOwnFolderCountsItsSchedulesInItsZone(t *testing.T) {
	now := instant(t, "2026-10-25T01:30:00Z")
	setClock(t, now)
	saved := AllowLocal
	AllowLocal = true
	t.Cleanup(func() { AllowLocal = saved })
	config := routerImport + "router.timezone(\"Asia/Ho_Chi_Minh\");\nrouter.reload(false);\n" +
		"router.guard((context) => {\n  if (context.query(\"stop\") === \"yes\") {\n    return false;\n  }\n});\n"
	schedule := "import * as schedule from \"kitwork/schedule\";\nexport const sang = schedule.daily(\"09:00\").run((context) => 1);\n"
	for _, c := range []struct {
		name string
		site bool
	}{
		{"one app, one site", true},
		{"one app, schedules only", false},
	} {
		root := t.TempDir()
		files := map[string]string{"config.work": config, ".schedule/sang.work": schedule}
		if c.site {
			files["router.work"] = routerImport + "router.get((context) => context.text(\"root\"));\n"
		}
		writeAppTree(t, root, files)
		appRuntime := app.NewRuntime(RootAppIdentity)
		t.Cleanup(appRuntime.Close)
		var clock *Tenant
		output := captureOutput(t, func() {
			clock = NewAppTenantWithRuntimeLayout(root, RootAppIdentity, RootLayoutSingle, appRuntime)
			if err := clock.Run(); err != nil {
				t.Fatal(err)
			}
		})
		t.Cleanup(clock.Close)
		if strings.Contains(output, "initialize router") || strings.Contains(output, "không viết .timezone") {
			t.Errorf("%s: the clock said:\n%s", c.name, output)
		}
		entry := scheduleEntries(t, clock)["sang"]
		if entry == nil {
			t.Fatalf("%s: sang is not on the clock:\n%s", c.name, output)
		}
		if next, ok := entry.next(now); entry.location.String() != "Asia/Ho_Chi_Minh" || !ok || !next.Equal(instant(t, "2026-10-25T02:00:00Z")) {
			t.Errorf("%s: sang counts in %s, next due %s; want Asia/Ho_Chi_Minh, 2026-10-25T02:00:00Z", c.name, entry.location, next.UTC().Format(time.RFC3339))
		}
		if !c.site {
			continue
		}
		siteRuntime, err := appRuntime.Site(root, "localhost")
		if err != nil {
			t.Fatal(err)
		}
		site := NewTenantWithRuntimeLayout(root, "localhost", RootLayoutSingle, appRuntime, siteRuntime)
		t.Cleanup(site.Close)
		if err := site.Run(); err != nil {
			t.Fatalf("%s: the site: %v", c.name, err)
		}
		if code := serveAt(site, http.MethodGet, "http://localhost/?stop=yes").Code; code != http.StatusForbidden {
			t.Errorf("%s: the guard of the one config.work: GET /?stop=yes = %d, want 403", c.name, code)
		}
		if zone := site.routeTree().root.folder.timezone; zone == nil || zone.String() != "Asia/Ho_Chi_Minh" {
			t.Errorf("%s: the site's zone = %v, want Asia/Ho_Chi_Minh", c.name, zone)
		}
	}
}

// The app's config.work that does not run where the schedules load is said in one line, and the schedules that
// count in the app's zone wait for it, as those of a file that does not load (KITWORK_SCHEDULE.md mục 13): off
// the clock, nothing said of UTC, their kept state untouched, so a once() keeps the due time it chose and is not
// chosen again when the file is fixed. A schedule with its own .timezone(…) runs on. The file here declares its
// zone and fails for another reason (router.guard at the app's root), as an author's typo does.
//
// CONTROL: the site of the same app does not boot with it (TestAppConfigWorkIsTheParentOfEverySite's last
// step), which is where its author reads it first. CONTROL (go test -overlay, 24/10/2026): with the whole
// error printed, the line went on with the VM's frame on a second line. CONTROL (go test -overlay, 25/10/2026):
// with LoadScheduleFiles not withholding them (the clock counting them in UTC, as before), the broken boot put
// sang and moBan on the clock, said "lịch sang không viết .timezone(…), và app không khai router.timezone(…)"
// though the app declares its zone, and wrote moBan's state anew; once fixed, moBan was declared at 03:00 and
// chose 26/10 02:00, a second run of a once() whose due time 25/10 02:00 had been chosen.
func TestSchedulesInTheAppsZoneWaitForABrokenAppConfig(t *testing.T) {
	clock := setClock(t, instant(t, "2026-10-25T01:30:00Z"))
	root := t.TempDir()
	const good = routerImport + "router.timezone(\"Asia/Ho_Chi_Minh\");\n"
	const broken = good + "router.guard((context) => true);\n"
	writeQueueApp(t, root, map[string]string{
		"config.work": good,
		".schedule/sang.work": `import * as schedule from "kitwork/schedule";
export const sang = schedule.daily("09:00").run((context) => 1);
export const moBan = schedule.daily("09:00").once((context) => 1);
export const rieng = schedule.daily("09:00").timezone("Asia/Tokyo").run((context) => 1);
`,
	})
	config := filepath.Join(root, "acme", "config.work")
	// boot starts the app's clock alone (its site would not boot with the broken file), and gives what it said,
	// the schedules on the clock and the state kitwork_schedules keeps; the clock is closed after
	boot := func(source string) (string, map[string]*scheduleEntry, map[string]queuestore.ScheduleState) {
		t.Helper()
		if err := os.WriteFile(config, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		appRuntime := app.NewRuntime("acme")
		defer appRuntime.Close()
		var tenant *Tenant
		output := captureOutput(t, func() {
			tenant = NewAppTenantWithRuntimeLayout(root, "acme", RootLayoutMultiTenant, appRuntime)
			if err := tenant.Run(); err != nil {
				t.Fatal(err)
			}
		})
		defer tenant.Close()
		states, err := tenant.scheduleStore().ScheduleStates()
		if err != nil {
			t.Fatal(err)
		}
		return output, scheduleEntries(t, tenant), states
	}

	_, _, first := boot(good)
	chosen := first["moBan"]
	if !chosen.OnceAt.Equal(instant(t, "2026-10-25T02:00:00Z")) || !strings.HasSuffix(chosen.Rule, " Asia/Ho_Chi_Minh") {
		t.Fatalf("moBan's state = %+v, want 09:00 in the app's zone, 02:00Z, chosen", chosen)
	}

	// the due time passes while the app is down; it boots with a config.work that does not run
	clock.set(instant(t, "2026-10-25T02:30:00Z"))
	output, entries, states := boot(broken)
	said := `[Schedule] initialize router app/config.work: config.work:3:7: router.guard chỉ viết ở gốc site và trong nhánh`
	// the clock reads the file once, at start: the line says the fix waits for the app to start again
	waits := "; lịch không viết .timezone(…) đếm theo múi giờ của app, nên chờ, không tới giờ, tới khi file này chạy được và app khởi động lại (đồng hồ của app đọc file này một lần, lúc khởi động)\n"
	if strings.Count(output, "initialize router app/config.work") != 1 || !strings.Contains(output, said) || !strings.Contains(output, waits) {
		t.Errorf("boot did not say once %q … %q:\n%s", said, waits, output)
	}
	if strings.Contains(output, "UTC") || strings.Contains(output, "app không khai") {
		t.Errorf("boot said a schedule counts in UTC, or that the app declares no zone, while its config.work fails:\n%s", output)
	}
	if !strings.Contains(output, "[Schedule] lịch moBan, sang chưa nạp được") {
		t.Errorf("boot did not say moBan and sang wait:\n%s", output)
	}
	for _, name := range []string{"sang", "moBan"} {
		if entry := entries[name]; entry != nil {
			t.Errorf("%s is on the clock in %s while the app's zone is not known", name, entry.location)
		}
		if kept, was := states[name], first[name]; kept.Rule != was.Rule || !kept.DeclaredAt.Equal(was.DeclaredAt) ||
			!kept.OnceAt.Equal(was.OnceAt) || !kept.LastAt.Equal(was.LastAt) {
			t.Errorf("%s's state while the app's config.work fails = %+v, want it kept, %+v", name, kept, first[name])
		}
	}
	if entry := entries["rieng"]; entry == nil || entry.location.String() != "Asia/Tokyo" {
		t.Errorf("rieng, with its own zone, is not on the clock in Asia/Tokyo: %+v", entry)
	}

	// fixed: moBan keeps the due time it chose, declared when it was first, never chosen again
	clock.set(instant(t, "2026-10-25T03:00:00Z"))
	output, entries, states = boot(good)
	if strings.Contains(output, "initialize router") || strings.Contains(output, "không viết .timezone") {
		t.Errorf("the fixed boot said:\n%s", output)
	}
	if entries["moBan"] == nil || entries["sang"] == nil {
		t.Fatalf("moBan and sang are not on the clock once the file is fixed:\n%s", output)
	}
	if again := states["moBan"]; !again.DeclaredAt.Equal(chosen.DeclaredAt) || !again.OnceAt.Equal(chosen.OnceAt) || again.Rule != chosen.Rule {
		t.Errorf("moBan once the file is fixed: declared %s, due %s, rule %q; want declared %s, due %s, rule %q",
			again.DeclaredAt.UTC().Format(time.RFC3339), again.OnceAt.UTC().Format(time.RFC3339), again.Rule,
			chosen.DeclaredAt.UTC().Format(time.RFC3339), chosen.OnceAt.UTC().Format(time.RFC3339), chosen.Rule)
	}
}
