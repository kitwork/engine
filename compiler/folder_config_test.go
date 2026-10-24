package compiler

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// callRecorder stands in for kitwork/queue or kitwork/schedule as the program of a config.work runs: it
// records each call and what it was handed.
type callRecorder struct {
	calls []string
}

func (r *callRecorder) OnGet(string) value.Value                  { return value.NewNil() }
func (r *callRecorder) OnCompare(string, value.Value) value.Value { return value.NewNil() }
func (r *callRecorder) OnInvoke(method string, args ...value.Value) value.Value {
	written := make([]string, 0, len(args))
	for _, argument := range args {
		written = append(written, argument.Text())
	}
	r.calls = append(r.calls, method+"("+strings.Join(written, ", ")+")")
	return value.NewNil()
}

// compileInApp writes files into the app acme and compiles the one at relative as its loader does: a
// config.work of .shared/queue/ or .schedule/ as a config file, a file of .shared/queue/ or .schedule/ as a
// module, anything else as an entry.
func compileInApp(t *testing.T, files map[string]string, relative string) (*Bytecode, error) {
	t.Helper()
	tree := map[string]string{}
	for path, content := range files {
		tree["apps/acme/"+path] = content
	}
	root := writeApps(t, tree)
	app := filepath.Join(root, "apps", "acme")
	file := filepath.Join(app, filepath.FromSlash(relative))
	switch {
	case filepath.Base(relative) == "config.work" && (strings.HasPrefix(relative, ".shared/queue/") || strings.HasPrefix(relative, ".schedule/")):
		return CompileConfigFileInApp(app, file)
	case strings.HasPrefix(relative, ".shared/queue/") || strings.HasPrefix(relative, ".schedule/"):
		return CompileModuleFileInApp(app, file)
	}
	return CompileFileInApp(app, file)
}

// The config.work of a folder configures it with calls on the module's namespace, one call per line at its
// top level (KITWORK_QUEUE.md mục 2, KITWORK_SCHEDULE.md mục 2, 24/10/2026): its program runs each call, in
// the order written, with what the line hands it, a value of the file's own or of an import included. A
// database handle handed to queue.database(db) is read as the root it is, not refused as a value.
//
// CONTROL (24/10/2026): with resolver.folderCall letting nothing through, the queue's file did not compile:
// "config.work:4:7: queue.parallel(…) là một dòng ở cấp file của config.work: queue.parallel(4); không viết
// trong hàm, trong khối hay làm giá trị".
func TestFolderConfigRunsItsCalls(t *testing.T) {
	bytecode, err := compileInApp(t, map[string]string{
		".shared/queue/config.work": "import * as queue from \"kitwork/queue\";\nimport { base } from \"app/.shared/base.work\";\nconst twice = base * 2;\nqueue.parallel(twice);\nqueue.keep(\"30d\");\n",
		".shared/base.work":         "export const base = 2;\n",
	}, ".shared/queue/config.work")
	if err != nil {
		t.Fatal(err)
	}
	recorder := &callRecorder{}
	vm := runtime.New(bytecode.Program)
	vm.MaxEnergy = 10_000_000
	vm.Builtins = []value.Value{value.NewFunc(func(...value.Value) value.Value {
		return value.New(map[string]value.Value{"queue": {K: value.Proxy, V: recorder}})
	})}
	if result := vm.Run(); result.K == value.Invalid {
		t.Fatalf("run: %s", result.Text())
	}
	if got := strings.Join(recorder.calls, " "); got != "parallel(4) keep(30d)" {
		t.Fatalf("calls = %q, want parallel(4) keep(30d)", got)
	}

	const schema = "import { struct, id, text } from \"kitwork/schema\";\nimport { connection } from \"kitwork/database\";\nconst orders = struct({ id: id(), email: text() });\nexport const db = connection().schema({ orders });\n"
	if _, err := compileInApp(t, map[string]string{
		".schedule/config.work":     "import * as schedule from \"kitwork/schedule\";\nimport { db } from \"app/.shared/schema/shop.work\";\n\nschedule.keep(\"7d\");\nschedule.session({ role: \"system\" });\nschedule.database(db);\n",
		".shared/schema/shop.work":  schema,
		".shared/queue/config.work": "import { queue } from \"kitwork\";\nimport { db } from \"app/.shared/schema/shop.work\";\nqueue.database(db);\n",
	}, ".schedule/config.work"); err != nil {
		t.Fatalf("the schedule's config.work as the spec writes it: %v", err)
	}
	if _, err := compileInApp(t, map[string]string{
		".shared/queue/config.work": "import { queue } from \"kitwork\";\nimport { db } from \"app/.shared/schema/shop.work\";\nqueue.database(db);\n",
		".shared/schema/shop.work":  schema,
	}, ".shared/queue/config.work"); err != nil {
		t.Fatalf("queue.database(db) on the namespace import { queue } from \"kitwork\" binds: %v", err)
	}
}

// The const of 17/10 (const parallel = 4), a chain (queue.parallel(4).keep("30d")) and a word called twice
// are compile errors at their place in config.work, each naming what to write: the call, two lines, one line
// (24/10/2026). Before, the const was the spelling, and a call was refused as an old word.
//
// CONTROL (24/10/2026): with readFolderCalls taking no const for the folder's, the seven const and let
// compiled (got <nil>); with it reading no chain, the three chains compiled (got <nil>), the call they begin
// with let through as a line; with it keeping no word seen, the two queue.parallel and the two schedule.keep
// lines compiled (got <nil>).
func TestFolderConfigRefusesTheOldConstAChainAndAWordTwice(t *testing.T) {
	const queueHead = "import * as queue from \"kitwork/queue\";\n"
	const scheduleHead = "import * as schedule from \"kitwork/schedule\";\n"
	for _, test := range []struct{ name, file, source, place, want string }{
		{"const parallel", ".shared/queue/config.work", queueHead + "const parallel = 4;\n", "2:7",
			`const parallel đã bỏ: số lần chạy cùng lúc trên một máy là lời gọi queue.parallel(4), một dòng ở cấp file của .shared/queue/config.work, sau import * as queue from "kitwork/queue"`},
		{"const keep of the queue", ".shared/queue/config.work", "const keep = \"30d\";\n", "1:7",
			`const keep đã bỏ: thời gian giữ lần chạy đã xong là lời gọi queue.keep("30d"), một dòng ở cấp file của .shared/queue/config.work`},
		{"let parallel", ".shared/queue/config.work", queueHead + "let parallel = 4;\n", "2:5", `const parallel đã bỏ`},
		{"const database of the queue", ".shared/queue/config.work", queueHead + "const database = 1;\n", "2:7",
			`const database đã bỏ: database của các lần chạy là lời gọi queue.database(db)`},
		{"const keep of the schedule", ".schedule/config.work", scheduleHead + "const keep = \"7d\";\n", "2:7",
			`const keep đã bỏ: thời gian giữ lần chạy đã xong là lời gọi schedule.keep("7d"), một dòng ở cấp file của .schedule/config.work, sau import * as schedule from "kitwork/schedule"`},
		{"const session", ".schedule/config.work", scheduleHead + "const session = { role: \"system\" };\n", "2:7",
			`const session đã bỏ: phiên của lịch là lời gọi schedule.session({ role: "system" })`},
		{"const database of the schedule", ".schedule/config.work", "const database = 1;\n", "1:7", `const database đã bỏ: database của các lần chạy là lời gọi schedule.database(db)`},
		{"a chain of two words", ".shared/queue/config.work", queueHead + "queue.parallel(4).keep(\"30d\");\n", "2:19",
			`queue.parallel(…).keep(…) nối tiếp: mỗi lời gọi của config.work một dòng, tách thành hai dòng: queue.parallel(…); queue.keep(…);`},
		{"a chain of another word", ".shared/queue/config.work", queueHead + "queue.parallel(4).then;\n", "2:19",
			`queue.parallel(…).then: lời gọi của config.work không trả gì để nối, mỗi lời gọi một dòng: queue.parallel(…);`},
		{"a call of what it gave", ".shared/queue/config.work", queueHead + "queue.parallel(4)();\n", "2:18",
			`queue.parallel(…)(…): lời gọi của config.work không trả gì để gọi, mỗi lời gọi một dòng: queue.parallel(…);`},
		{"a chain in the schedule's", ".schedule/config.work", scheduleHead + "schedule.keep(\"7d\").session({ role: \"system\" });\n", "2:21",
			`schedule.keep(…).session(…) nối tiếp: mỗi lời gọi của config.work một dòng, tách thành hai dòng: schedule.keep(…); schedule.session(…);`},
		{"a word twice", ".shared/queue/config.work", queueHead + "queue.parallel(4);\nqueue.keep(\"30d\");\nqueue.parallel(8);\n", "4:7",
			`queue.parallel gọi hai lần, dòng 2 và dòng 4: mỗi chữ một lần trong config.work, giữ một dòng`},
		{"a word twice in the schedule's", ".schedule/config.work", scheduleHead + "schedule.keep(\"7d\");\nschedule.keep(\"1d\");\n", "3:10",
			`schedule.keep gọi hai lần, dòng 2 và dòng 3`},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := compileInApp(t, map[string]string{test.file: test.source}, test.file)
			if err == nil || !strings.Contains(err.Error(), "config.work:"+test.place+": "+test.want) {
				t.Fatalf("got %v\nwant config.work:%s: %s…", err, test.place, test.want)
			}
		})
	}
}

// A word of a folder's config.work is written there only, as a line of its own at the top level: inside a
// function, a block, or as a value it is a compile error saying so; in any other file (a route, a job, a
// schedule, a library, the other folder's config.work), and imported by name, a compile error naming where it
// is written. queue.parallel and queue.keep outside it no longer say to write a const.
//
// CONTROL (24/10/2026): with queueMember and scheduleWord reading no folder word, queue.parallel(4) in a
// route, a job file, a library and a function of config.work compiled (got <nil>), and schedule.keep in a
// schedule file was "schedule.keep không có: …", not where it is written; import { parallel } stayed refused,
// by queueDeclaration.
func TestFolderConfigCallsOnlyAtTheTopOfTheirConfig(t *testing.T) {
	const queueHead = "import * as queue from \"kitwork/queue\";\n"
	const scheduleHead = "import * as schedule from \"kitwork/schedule\";\n"
	const notAtTop = "là một dòng ở cấp file của config.work: "
	for _, test := range []struct{ name, file, source, place, want string }{
		{"in a function", ".shared/queue/config.work", queueHead + "const set = () => queue.parallel(4);\n", "2:25",
			"queue.parallel(…) " + notAtTop + "queue.parallel(4); không viết trong hàm, trong khối hay làm giá trị"},
		{"in a block", ".shared/queue/config.work", queueHead + "if (true) { queue.keep(\"7d\"); }\n", "2:19", "queue.keep(…) " + notAtTop + `queue.keep("30d")`},
		{"as a value", ".shared/queue/config.work", queueHead + "const p = queue.parallel(4);\n", "2:17", "queue.parallel(…) " + notAtTop},
		{"imported by name", ".shared/queue/config.work", "import { parallel } from \"kitwork/queue\";\nparallel(4);\n", "1:1", "queue.parallel(…) " + notAtTop},
		{"in the schedule's, as a value", ".schedule/config.work", scheduleHead + "const s = schedule.session({});\n", "2:20", "schedule.session(…) " + notAtTop},
		{"in a route", "site/router.work", queueHead + "queue.parallel(4);\n", "2:7",
			"queue.parallel(…) chỉ viết trong .shared/queue/config.work, một dòng ở cấp file: queue.parallel(4); ở file khác nó không có nghĩa"},
		{"in a job file", ".shared/queue/mail.work", queueHead + "queue.keep(\"7d\");\nexport const mail = queue.job((context) => 1);\n", "2:7",
			`queue.keep(…) chỉ viết trong .shared/queue/config.work, một dòng ở cấp file: queue.keep("30d")`},
		{"in a schedule file", ".schedule/nhip.work", scheduleHead + "schedule.keep(\"7d\");\nexport const nhip = schedule.every(\"1h\").run((context) => 1);\n", "2:10",
			`schedule.keep(…) chỉ viết trong .schedule/config.work, một dòng ở cấp file: schedule.keep("7d")`},
		{"the queue's in the schedule's config", ".schedule/config.work", queueHead + "queue.parallel(4);\n", "2:7", "queue.parallel(…) chỉ viết trong .shared/queue/config.work"},
		{"the schedule's in the queue's config", ".shared/queue/config.work", scheduleHead + "schedule.session({ role: \"system\" });\n", "2:10", "schedule.session(…) chỉ viết trong .schedule/config.work"},
		{"imported by name in a route", "site/router.work", "import { keep } from \"kitwork/schedule\";\n", "1:1", "schedule.keep(…) chỉ viết trong .schedule/config.work"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := compileInApp(t, map[string]string{test.file: test.source}, test.file)
			if err == nil || !strings.Contains(err.Error(), filepath.Base(test.file)+":"+test.place+": "+test.want) {
				t.Fatalf("got %v\nwant %s:%s: %s…", err, filepath.Base(test.file), test.place, test.want)
			}
		})
	}
	t.Run("in a library a route imports", func(t *testing.T) {
		_, err := compileInApp(t, map[string]string{
			".shared/settings.work": queueHead + "export const tune = () => queue.parallel(2);\n",
			"site/router.work":      "import { tune } from \"app/.shared/settings.work\";\nvar result = tune;\n",
		}, "site/router.work")
		if err == nil || !strings.Contains(err.Error(), "settings.work:2:33: queue.parallel(…) chỉ viết trong .shared/queue/config.work") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("the old word on a job's handle", func(t *testing.T) {
		_, err := compileInApp(t, map[string]string{
			".shared/queue/camOn.work": queueHead + "export const camOn = queue.job((context) => 1);\n",
			"site/router.work":         "import { camOn } from \"app/.shared/queue/camOn.work\";\ncamOn.parallel(2);\n",
		}, "site/router.work")
		if err == nil || !strings.Contains(err.Error(), "router.work:2:7: camOn.parallel đã bỏ: số lần chạy cùng lúc là luật của cả hàng đợi, một dòng trong .shared/queue/config.work: queue.parallel(4)") {
			t.Fatalf("got %v", err)
		}
	})
}
