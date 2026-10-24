package compiler

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// export { b as c } gives c the value of b (22/10/2026): until then the parser took `as` and c for names
// of their own, and an importer of c read nothing. CONTROL: export { b } still gives b.
func TestExportAliasGivesTheLocalValue(t *testing.T) {
	root := writeApps(t, map[string]string{
		"apps/acme/.shared/names.work": "const b = \"giá trị của b\";\nexport { b as c, b };\n",
		"apps/acme/site/router.work":   "import { c, b } from \"app/.shared/names.work\";\nvar result = c + \"|\" + b;\n",
	})
	bytecode, err := CompileFileInApp(filepath.Join(root, "apps", "acme"), filepath.Join(root, "apps", "acme", "site", "router.work"))
	if err != nil {
		t.Fatal(err)
	}
	if got := runAppProgram(t, bytecode); got != "giá trị của b|giá trị của b" {
		t.Fatalf("result = %q", got)
	}
}

// exportsRecorder stands in for the queue module: it records what #exports was handed.
type exportsRecorder struct {
	module string
	names  []string
	keys   []string
}

func (r *exportsRecorder) OnGet(string) value.Value                  { return value.NewNil() }
func (r *exportsRecorder) OnCompare(string, value.Value) value.Value { return value.NewNil() }
func (r *exportsRecorder) OnInvoke(method string, args ...value.Value) value.Value {
	if method != QueueExportsMethod || len(args) != 3 {
		return value.InvalidFailure(value.InvalidArgumentCode, "unexpected "+method)
	}
	r.module = args[1].Text()
	for _, name := range args[2].Array() {
		r.names = append(r.names, name.Text())
	}
	for key := range args[0].Map() {
		r.keys = append(r.keys, key)
	}
	return args[0]
}

// A file of .shared/queue/ hands its export object to kitwork().queue.#exports with its name from the app
// and its exports in the order written, in every bundle that imports it, and the importer gets the object
// back (KITWORK_QUEUE.md mục 3: a job is named by its export). CONTROL: a file elsewhere in .shared/ is
// bundled as before and hands nothing.
func TestQueueModuleHandsItsExportsToTheQueue(t *testing.T) {
	root := writeApps(t, map[string]string{
		"apps/acme/.shared/queue/camOn.work": "export const camOn = 1;\nexport const second = camOn;\n",
		"apps/acme/.shared/plan.work":        "export const plan = 2;\n",
		"apps/acme/site/router.work":         "import { camOn } from \"app/.shared/queue/camOn.work\";\nimport { plan } from \"app/.shared/plan.work\";\nvar result = camOn + plan;\n",
	})
	bytecode, err := CompileFileInApp(filepath.Join(root, "apps", "acme"), filepath.Join(root, "apps", "acme", "site", "router.work"))
	if err != nil {
		t.Fatal(err)
	}
	recorder := &exportsRecorder{}
	kitwork := value.NewFunc(func(...value.Value) value.Value {
		return value.New(map[string]value.Value{"queue": {K: value.Proxy, V: recorder}})
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
	if recorder.module != ".shared/queue/camOn.work" || strings.Join(recorder.names, ",") != "camOn,second" || len(recorder.keys) != 2 {
		t.Fatalf("#exports got module %q names %v keys %v; want .shared/queue/camOn.work, camOn,second, one call", recorder.module, recorder.names, recorder.keys)
	}
}

// In .shared/queue/ a job is named by its export, so an export default and an export under another name
// do not compile, each at its place. CONTROL: the same alias in another folder of .shared/ compiles.
func TestQueueModuleRefusesAnotherName(t *testing.T) {
	for _, c := range []struct{ source, want string }{
		{"const job = 1;\nexport default job;\n", "mail.work:2:8: export default trong .shared/queue/: job mang tên export của nó, viết export const camOn = queue.job(…)"},
		{"const job = 1;\nexport { job as mail };\n", "mail.work:2:17: export { job as mail } trong .shared/queue/: job mang tên export của nó, một job một tên; viết export const mail = queue.job(…)"},
	} {
		root := writeApps(t, map[string]string{
			"apps/acme/.shared/queue/mail.work": c.source,
			"apps/acme/.shared/lib/mail.work":   c.source,
			"apps/acme/site/router.work":        "import \"app/.shared/queue/mail.work\";\n",
			"apps/acme/site/other/router.work":  "import \"app/.shared/lib/mail.work\";\n",
		})
		app := filepath.Join(root, "apps", "acme")
		_, err := CompileFileInApp(app, filepath.Join(app, "site", "router.work"))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: %v\nwant %s", c.source, err, c.want)
		}
		if _, err := CompileFileInApp(app, filepath.Join(app, "site", "other", "router.work")); err != nil {
			t.Errorf("control %q outside .shared/queue/: %v", c.source, err)
		}
		if _, err := CompileModuleFileInApp(app, filepath.Join(app, ".shared", "queue", "mail.work")); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("the loader's compile of %q: %v", c.source, err)
		}
	}
}

// CompileModuleFileInApp compiles a file as the module its importers see: the program returns its export
// object, what the loader of .shared/queue/ reads.
func TestCompileModuleFileReturnsItsExports(t *testing.T) {
	root := writeApps(t, map[string]string{
		"apps/acme/.shared/plan.work": "import { base } from \"app/.shared/base.work\";\nconst hidden = 1;\nexport const plan = base + hidden;\nexport { hidden as shown };\n",
		"apps/acme/.shared/base.work": "export const base = 41;\n",
	})
	app := filepath.Join(root, "apps", "acme")
	bytecode, err := CompileModuleFileInApp(app, filepath.Join(app, ".shared", "plan.work"))
	if err != nil {
		t.Fatal(err)
	}
	if len(bytecode.Files) != 2 || filepath.Base(bytecode.Files[0]) != "plan.work" {
		t.Fatalf("files = %v, want the file first, then what it imports", bytecode.Files)
	}
	vm := runtime.New(bytecode.Program)
	vm.MaxEnergy = 10_000_000
	exported := vm.Run()
	if exported.K != value.Map || exported.Get("plan").N != 42 || exported.Get("shown").N != 1 || exported.Get("hidden").K != value.Nil {
		t.Fatalf("exports = %v", exported.Interface())
	}
}

// The queue's old spellings are compile errors where they are written, with file:line:col (KITWORK_QUEUE.md
// mục 14): a word of the module or of a handle the spec replaced, queue.job outside the app's
// .shared/queue/ (a route, a library of .shared/ a job file imports), status(…).list(), runs({ where }),
// and queued read off a send's receipt (queue.dispatch's { queued, id }). Before, each compiled and failed
// only when the line ran, and kitwork check said nothing. CONTROL: the spec's spellings around them
// compile, in a route and in a job file.
func TestQueueOldSpellingsAreCompileErrors(t *testing.T) {
	const job = "import * as queue from \"kitwork/queue\";\nexport const camOn = queue.job((context) => 1).retry(3);\n"
	const head = "import * as queue from \"kitwork/queue\";\nimport { camOn } from \"app/.shared/queue/camOn.work\";\n"
	compile := func(t *testing.T, files map[string]string) error {
		t.Helper()
		tree := map[string]string{"apps/acme/.shared/queue/camOn.work": job}
		for relative, content := range files {
			tree["apps/acme/"+relative] = content
		}
		root := writeApps(t, tree)
		app := filepath.Join(root, "apps", "acme")
		if _, routed := files["site/router.work"]; routed {
			_, err := CompileFileInApp(app, filepath.Join(app, "site", "router.work"))
			return err
		}
		_, err := CompileModuleFileInApp(app, filepath.Join(app, ".shared", "queue", "camOn.work"))
		return err
	}
	for _, test := range []struct{ name, line, place, want string }{
		{"dispatch", `queue.dispatch("camOn", {});`, "3:7", `queue.dispatch đã bỏ: import { camOn } from "app/.shared/queue/camOn.work" rồi camOn.send(data, { delay, key })`},
		{"jobs", `const all = queue.jobs();`, "3:19", `queue.jobs đã bỏ: các lần chạy là queue.runs()`},
		{"retry by id", `queue.retry("r_1");`, "3:7", `queue.retry đã bỏ: lần thử thêm là luật của job`},
		{"resend", `queue.resend("r_1");`, "3:7", `queue.resend không có`},
		{"failed read", `const failed = queue.failed;`, "3:22", `queue.failed đã bỏ: queue.status("failed").runs()`},
		{"job in a route", `const stray = queue.job((context) => 1);`, "3:21", `queue.job chỉ khai trong .shared/queue/ của app`},
		{"status list", `const read = queue.status("failed").list();`, "3:37", `status("failed").list() không có: trạng thái chỉ có ở lần chạy, viết status("failed").runs()`},
		{"handle status list", `const read = camOn.status("failed").list();`, "3:37", `status("failed").list() không có`},
		{"runs where", `const read = queue.runs({ where: (run) => run.attempt > 1 });`, "3:27", `runs({ where }) không có: điều kiện viết trước hành động, queue.where(fn).runs()`},
		{"handle runs where", `const read = camOn.status("failed").runs({ "where": 1 });`, "3:44", `runs({ where }) không có`},
		{"success", `camOn.success(() => 1);`, "3:7", `camOn.success đã bỏ: việc sau khi chạy xong viết ở cuối handler`},
		{"job list", `const read = camOn.list();`, "3:20", `camOn.list() của một job không có`},
		{"receipt queued", "const receipt = camOn.send({});\nif (!receipt.queued) { fail(\"x\"); }", "4:14", `biên nhận của send không có queued`},
		{"send queued", `const sent = camOn.send({}).queued;`, "3:29", `biên nhận của send không có queued`},
		{"destructured", `const { jobs } = queue;`, "3:9", `queue.jobs đã bỏ`},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := compile(t, map[string]string{"site/router.work": head + test.line + "\n"})
			if err == nil || !strings.Contains(err.Error(), "router.work:"+test.place+": "+test.want) {
				t.Fatalf("got %v\nwant router.work:%s: %s…", err, test.place, test.want)
			}
		})
	}
	for _, test := range []struct{ name, source, place, want string }{
		{"import a removed word", "import { dispatch } from \"kitwork/queue\";\n", "1:1", `queue.dispatch đã bỏ`},
		{"import job in a route", "import { job } from \"kitwork/queue\";\n", "1:1", `queue.job chỉ khai trong .shared/queue/ của app`},
		{"kitwork's queue", "import { queue } from \"kitwork\";\nconst old = queue.failed(\"camOn\");\n", "2:19", `queue.failed đã bỏ`},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := compile(t, map[string]string{"site/router.work": test.source})
			if err == nil || !strings.Contains(err.Error(), "router.work:"+test.place+": "+test.want) {
				t.Fatalf("got %v\nwant router.work:%s: %s…", err, test.place, test.want)
			}
		})
	}
	t.Run("job in a library of .shared/", func(t *testing.T) {
		err := compile(t, map[string]string{
			".shared/jobs.work":        "import * as queue from \"kitwork/queue\";\nexport const make = () => queue.job((context) => 1);\n",
			".shared/queue/camOn.work": "import { make } from \"app/.shared/jobs.work\";\nexport const camOn = make();\n",
		})
		if err == nil || !strings.Contains(err.Error(), "jobs.work:2:33: queue.job chỉ khai trong .shared/queue/ của app") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("control", func(t *testing.T) {
		route := head + "const read = [queue.list(), queue.runs(10), queue.status(\"failed\").runs({ page: 1, limit: 5 }), camOn.runs(2), camOn.status(\"failed\").runs()];\n" +
			"const receipt = camOn.send({ n: 1 }, { key: \"k\" });\nconst id = receipt.id + receipt.status;\nqueue.cancel(id);\nqueue.replay(id);\n"
		if err := compile(t, map[string]string{"site/router.work": route}); err != nil {
			t.Fatalf("the spec's spellings in a route: %v", err)
		}
		if err := compile(t, nil); err != nil {
			t.Fatalf("queue.job in .shared/queue/: %v", err)
		}
	})
}
