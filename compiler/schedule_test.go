package compiler

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// A file of .schedule/ hands its export object to kitwork().schedule.#exports with its name from the app and
// its exports in the order written, in every bundle that imports it, and the importer gets the object back
// (KITWORK_SCHEDULE.md mục 6: a schedule is named by its export). CONTROL: a file of .shared/ is bundled as
// before and hands nothing.
func TestScheduleModuleHandsItsExportsToTheSchedule(t *testing.T) {
	root := writeApps(t, map[string]string{
		"apps/acme/.schedule/baoCao.work": "export const baoCaoSang = 1;\nexport const nhacCuoiThang = baoCaoSang;\n",
		"apps/acme/.shared/plan.work":     "export const plan = 2;\n",
		"apps/acme/site/router.work":      "import { baoCaoSang } from \"app/.schedule/baoCao.work\";\nimport { plan } from \"app/.shared/plan.work\";\nvar result = baoCaoSang + plan;\n",
	})
	bytecode, err := CompileFileInApp(filepath.Join(root, "apps", "acme"), filepath.Join(root, "apps", "acme", "site", "router.work"))
	if err != nil {
		t.Fatal(err)
	}
	recorder := &exportsRecorder{}
	kitwork := value.NewFunc(func(...value.Value) value.Value {
		return value.New(map[string]value.Value{"schedule": {K: value.Proxy, V: recorder}})
	})
	vm := runtime.New(bytecode.Program)
	vm.MaxEnergy = 10_000_000
	vm.Builtins = []value.Value{kitwork}
	if returned := vm.Run(); returned.IsInvalid() {
		t.Fatalf("run: %s", returned.Text())
	}
	if got := vm.Vars["result"].Text(); got != "3" {
		t.Fatalf("result = %q: the importer did not get the export object back", got)
	}
	if recorder.module != ".schedule/baoCao.work" || strings.Join(recorder.names, ",") != "baoCaoSang,nhacCuoiThang" || len(recorder.keys) != 2 {
		t.Fatalf("#exports got module %q names %v keys %v; want .schedule/baoCao.work, baoCaoSang,nhacCuoiThang, one call", recorder.module, recorder.names, recorder.keys)
	}
}

// In .schedule/ a schedule is named by its export, so an export default and an export under another name do
// not compile, each at its place. CONTROL: the same alias in a folder of .shared/ compiles.
func TestScheduleModuleRefusesAnotherName(t *testing.T) {
	for _, c := range []struct{ source, want string }{
		{"const one = 1;\nexport default one;\n", "nhip.work:2:8: export default trong .schedule/: lịch mang tên export của nó, viết export const baoCaoSang = schedule.daily(…).run(fn)"},
		{"const one = 1;\nexport { one as nhip };\n", "nhip.work:2:17: export { one as nhip } trong .schedule/: lịch mang tên export của nó, một lịch một tên; viết export const nhip = schedule.daily(…).run(fn)"},
	} {
		root := writeApps(t, map[string]string{
			"apps/acme/.schedule/nhip.work":    c.source,
			"apps/acme/.shared/lib/nhip.work":  c.source,
			"apps/acme/site/other/router.work": "import \"app/.shared/lib/nhip.work\";\n",
		})
		app := filepath.Join(root, "apps", "acme")
		if _, err := CompileModuleFileInApp(app, filepath.Join(app, ".schedule", "nhip.work")); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("the loader's compile of %q: %v\nwant %s", c.source, err, c.want)
		}
		if _, err := CompileFileInApp(app, filepath.Join(app, "site", "other", "router.work")); err != nil {
			t.Errorf("control %q outside .schedule/: %v", c.source, err)
		}
	}
}

// The old spellings of cron and the errors of KITWORK_SCHEDULE.md mục 11 are compile errors where they are
// written, with file:line:col, in the words the runtime uses for them too (utilities/schedule): so `kitwork
// check` finds them before boot. Before, cron.every("1h").handle(fn) in _cron/ compiled and registered, and
// a wrong time ran as no schedule. CONTROL: the spec's first block compiles in .schedule/, its read block in
// a route.
func TestScheduleOldSpellingsAreCompileErrors(t *testing.T) {
	const file = "import * as schedule from \"kitwork/schedule\";\nexport const baoCaoSang = schedule.daily(\"09:00\").run((context) => 1);\n"
	compile := func(t *testing.T, files map[string]string) error {
		t.Helper()
		tree := map[string]string{"apps/acme/.schedule/baoCao.work": file}
		for relative, content := range files {
			tree["apps/acme/"+relative] = content
		}
		root := writeApps(t, tree)
		app := filepath.Join(root, "apps", "acme")
		if _, routed := files["site/router.work"]; routed {
			_, err := CompileFileInApp(app, filepath.Join(app, "site", "router.work"))
			return err
		}
		_, err := CompileModuleFileInApp(app, filepath.Join(app, ".schedule", "baoCao.work"))
		return err
	}
	const head = "import * as schedule from \"kitwork/schedule\";\nimport { baoCaoSang } from \"app/.schedule/baoCao.work\";\n"
	for _, test := range []struct{ name, source, place, want string }{
		// an import is lowered to a declaration where the import begins
		{"cron of kitwork", "import { cron } from \"kitwork\";\n", "1:1", CronRemoved},
		{"kitwork/cron", "import * as cron from \"kitwork/cron\";\n", "1:13", CronRemoved},
		{"a rule in a route", head + "const stray = schedule.daily(\"09:00\").run((context) => 1);\n", "3:24", `schedule.daily(…) chỉ khai trong .schedule/ của app`},
		{"a rule imported in a route", "import { daily } from \"kitwork/schedule\";\n", "1:1", `schedule.daily(…) chỉ khai trong .schedule/ của app`},
		{"jobs", head + "const all = schedule.jobs();\n", "3:22", `schedule.jobs() không có: các lịch là schedule.list(), các lần chạy là schedule.runs()`},
		{"cron read", head + "const raw = schedule.cron;\n", "3:22", `schedule.cron đã bỏ: không có biểu thức cron thô`},
		{"a word it has not", head + "const read = schedule.history();\n", "3:23", `schedule.history không có: lịch khai bằng every, hourly, daily`},
		{"status list", head + "const read = schedule.status(\"failed\").list();\n", "3:40", `status("failed").list() không có: trạng thái chỉ có ở lần chạy, viết status("failed").runs()`},
		{"handle status list", head + "const read = baoCaoSang.status(\"failed\").list();\n", "3:42", `status("failed").list() không có`},
		{"runs where", head + "const read = schedule.runs({ where: (run) => run.attempt > 1 });\n", "3:30", `runs({ where }) không có: điều kiện viết trước hành động, schedule.where(fn).runs()`},
		{"a rule after the handle", head + "baoCaoSang.retry(3);\n", "3:12", `baoCaoSang: sau .run(…)/.once(…) không nối gì; .retry viết trước .run(…)`},
		{"handle of the handle", head + "baoCaoSang.handle((context) => 1);\n", "3:12", `baoCaoSang.handle(fn) đã bỏ: lịch kết thúc bằng .run(fn)`},
		{"handle list", head + "const read = baoCaoSang.list();\n", "3:25", `baoCaoSang.list() không có trên lịch: các lịch đọc bằng schedule.list()`},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := compile(t, map[string]string{"site/router.work": test.source})
			if err == nil || !strings.Contains(err.Error(), "router.work:"+test.place+": "+test.want) {
				t.Fatalf("got %v\nwant router.work:%s: %s…", err, test.place, test.want)
			}
		})
	}
	const schedule = "import * as schedule from \"kitwork/schedule\";\n"
	for _, test := range []struct{ name, line, place, want string }{
		{"25:00", `export const late = schedule.daily("25:00").run((context) => 1);`, "2:30", `giờ "25:00" sai: viết "HH:MM", từ "00:00" tới "23:59"`},
		{"weekly without at", `export const weekly = schedule.weekly("monday").run((context) => 1);`, "2:49", `weekly(…) thiếu .at(…): giờ trong ngày đi qua .at, schedule.weekly("monday").at("09:00").run(fn)`},
		{"daily at", `export const at = schedule.daily("09:00").at("10:00").run((context) => 1);`, "2:43", `giờ đã là tham số của daily: daily("09:00", "18:00"), không nối .at(…)`},
		{"monthly 0", `export const zero = schedule.monthly(0).at("09:00").run((context) => 1);`, "2:30", `monthly(0): ngày sai, monthly nhận ngày trong tháng, số nguyên từ 1 tới 31`},
		{"first and last", `export const both = schedule.monday("09:00").first("month").last("month").run((context) => 1);`, "2:61", `first và last không đi cùng nhau`},
		{"on with run", `export const launch = schedule.on("2026-12-01T09:00:00+07:00").run((context) => 1);`, "2:64", `schedule.on(…) là một thời điểm, chỉ đi với .once(handler)`},
		{"two ends", `export const two = schedule.daily("09:00").once((context) => 1).run((context) => 1);`, "2:65", `.run và .once loại trừ nhau và đứng cuối`},
		// the line of mục 11 as written: the second end is the error, not the first one's missing handler
		{"two ends as the spec writes them", `schedule.daily("09:00").once().run(handler);`, "2:32", `.run và .once loại trừ nhau và đứng cuối: lịch đã kết thúc bằng .once(…), sau đó không nối gì`},
		{"one end without a handler", `export const one = schedule.daily("09:00").once();`, "2:44", `.once(handler) nhận một hàm: .once((context) => { … })`},
		{"handle", `export const old = schedule.daily("08:00").handle((context) => 1);`, "2:44", `.handle(fn) đã bỏ: lịch kết thúc bằng .run(fn), hay .once(fn) cho đúng một lần`},
		{"overlap", `export const old = schedule.every("1m").overlap("skip").run((context) => 1);`, "2:41", `.overlap đã bỏ: lần tới giờ khi lần trước chưa xong còn mở trong spec (KITWORK_SCHEDULE.md mục 12.4)`},
		{"raw cron", `export const old = schedule.cron("*/5 * * * *");`, "2:29", `schedule.cron đã bỏ: không có biểu thức cron thô`},
		{"weekly with a time", `export const old = schedule.weekly("monday 09:00").run((context) => 1);`, "2:29", `weekly("monday 09:00"): giờ đi qua .at(…): schedule.weekly("monday").at("09:00")`},
		{"a rule imported by name", "import { daily } from \"kitwork/schedule\";\nexport const late = daily(\"25:00\").run((context) => 1);", "3:21", `giờ "25:00" sai`},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := compile(t, map[string]string{".schedule/baoCao.work": schedule + test.line + "\n"})
			if err == nil || !strings.Contains(err.Error(), "baoCao.work:"+test.place+": "+test.want) {
				t.Fatalf("got %v\nwant baoCao.work:%s: %s…", err, test.place, test.want)
			}
		})
	}
	t.Run("a rule in a library of .shared/", func(t *testing.T) {
		err := compile(t, map[string]string{
			".shared/schedules.work": schedule + "export const make = () => schedule.daily(\"09:00\").run((context) => 1);\n",
			".schedule/baoCao.work":  "import { make } from \"app/.shared/schedules.work\";\nexport const baoCaoSang = make();\n",
		})
		if err == nil || !strings.Contains(err.Error(), "schedules.work:2:36: schedule.daily(…) chỉ khai trong .schedule/ của app") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("control", func(t *testing.T) {
		example := schedule + "import { camOn } from \"app/.shared/queue/camOn.work\";\n" +
			"export const baoCaoSang = schedule.daily(\"09:00\").retry(3).timeout(\"10m\").run((context) => {\n  context.job.log(context.job.at);\n});\n" +
			"export const nhacCuoiThang = schedule.lastday(\"09:00\").before(3).run((context) => {\n  camOn.send({ kind: \"nhac-thanh-toan\" }, { key: \"nhac:\" + context.job.at });\n});\n" +
			"export const raMat = schedule.on(\"2026-12-01T09:00:00+07:00\").once((context) => 1);\n" +
			"const at = \"09:00\";\nexport const sang = schedule.weekly(\"monday\", \"friday\").at(at).timezone(\"Asia/Ho_Chi_Minh\").run((context) => 1);\n"
		files := map[string]string{
			".schedule/baoCao.work":    example,
			".shared/queue/camOn.work": "import * as queue from \"kitwork/queue\";\nexport const camOn = queue.job((context) => 1);\n",
		}
		if err := compile(t, files); err != nil {
			t.Fatalf("the spec's first block in .schedule/: %v", err)
		}
		files["site/router.work"] = head + "const read = [schedule.list(), schedule.list(10), schedule.runs(), schedule.runs(10), schedule.status(\"failed\").runs(10), baoCaoSang.runs(), baoCaoSang.status(\"failed\").runs({ page: 1, limit: 5 })];\nconst receipt = baoCaoSang.now();\n"
		if err := compile(t, files); err != nil {
			t.Fatalf("the spec's read block in a route: %v", err)
		}
	})
}
