package compiler

import (
	"strings"
	"testing"
)

// The go of before and the misuses of kitwork/routine are compile errors where they are written
// (KITWORK_ROUTINE.md mục 2 to 5); what the spec writes compiles.
//
// CONTROL: each check was taken out in turn, and the programs it refuses compiled: with checkBareGo
// returning nil, the two bare go; with routineDeclaration not refusing go off "kitwork", the import and the
// pattern of it; with routineMember not refusing kitwork().go, the aliased import and kitwork().go; with routineRequest returning
// false, the five functions of go that use the request; with markRoutine not marking the names a
// destructured parameter gives (parameterPart), the request taken apart; with routineArguments not checking wait, the two
// mixed waits and the rules on a handle; with routineMember not checking the module's words, routine.run.
func TestRoutineCompileErrors(t *testing.T) {
	const header = "import * as routine from \"kitwork/routine\";\n"
	for _, c := range []struct {
		name, source, want string
	}{
		{"go bare", `go(() => 1);`, "<source>:1:1: " + RoutineGoMoved},
		{"go bare in a handler", "const handle = (context) => {\n  go(() => context.query(\"a\"));\n};", "<source>:2:3: " + RoutineGoMoved},
		{"go off kitwork", `import { go } from "kitwork";`, "<source>:1:1: " + RoutineGoRemoved},
		{"go off kitwork, aliased", `import { go as start } from "kitwork";`, "<source>:1:1: " + RoutineGoRemoved},
		{"go off kitwork, taken apart", `const { go } = kitwork();`, "<source>:1:9: " + RoutineGoRemoved},
		{"kitwork().go", `kitwork().go(() => 1);`, "<source>:1:11: " + RoutineGoRemoved},
		{"context in go", header + "const handle = (context) => {\n  routine.go(() => context.json(1));\n};",
			"<source>:3:20: hàm của routine.go dùng context của request: `go` chạy sau khi request xong: lấy giá trị cần trước rồi đưa vào"},
		{"request in go, taken apart", header + "const handle = ({ request }) => routine.go(() => request.json());",
			"<source>:2:50: hàm của routine.go dùng request của request: `go` chạy sau khi request xong: lấy giá trị cần trước rồi đưa vào"},
		{"response in go after its rules", header + "const handle = (response) => routine.retry(1).go(() => { const r = response; });",
			"<source>:2:68: hàm của routine.go dùng response của request: `go` chạy sau khi request xong: lấy giá trị cần trước rồi đưa vào"},
		{"context in a go imported by name", "import { go } from \"kitwork/routine\";\nconst handle = (context) => go(() => context.query(\"a\"));",
			"<source>:2:38: hàm của routine.go dùng context của request: `go` chạy sau khi request xong: lấy giá trị cần trước rồi đưa vào"},
		{"context in a go inside a wait", header + "const handle = (context) => routine.wait(() => routine.go(() => context.query(\"a\")));",
			"<source>:2:65: hàm của routine.go dùng context của request: `go` chạy sau khi request xong: lấy giá trị cần trước rồi đưa vào"},
		{"wait of functions and a handle", header + "routine.wait(() => 1, routine.go(() => 2));", "<source>:2:9: " + RoutineWaitMixed},
		{"wait of a function and a const handle", header + "const task = routine.go(() => 2);\nroutine.wait(() => 1, task);", "<source>:3:9: " + RoutineWaitMixed},
		{"rules on a handle", header + "const task = routine.go(() => 2);\nroutine.retry(2).wait(task);", "<source>:3:18: " + RoutineRulesOnHandle},
		{"a word the module has not", header + "routine.run(() => 1);", "<source>:2:9: " + RoutineUnknownWord("run")},
		{"a word taken apart off the module", header + "const { spawn } = routine;", "<source>:2:9: " + RoutineUnknownWord("spawn")},
		{"a word imported that the module has not", `import { spawn } from "kitwork/routine";`, "<source>:1:1: " + RoutineUnknownWord("spawn")},
		{"go of a value", header + "routine.go(send(1));", "<source>:2:9: " + RoutineArgumentNotAFunction("go", "giá trị tính xong trước khi routine chạy", 1)},
		{"go of nothing", header + "routine.go();", "<source>:2:9: routine.go nhận ít nhất một hàm: routine.go(() => …)"},
		{"cancel of a function", header + "routine.cancel(() => 1);", "<source>:2:9: routine.cancel nhận handle của routine.go, không nhận hàm: const task = routine.go(fn); routine.cancel(task)"},
	} {
		_, err := CompileSource(c.source)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v\nwant %s", c.name, err, c.want)
		}
	}
}

// What the spec writes compiles: a value taken from the request before go, the request read in a function
// of wait, handles waited for, a go of the program's own, the words imported by name. A variable that is
// not a parameter is not the request's by its name: an HTTP result called response (KITWORK_HTTP.md mục 8),
// a const request of the file.
//
// CONTROL: with routineRequest refusing every outer name context, request or response, as it did, the two
// programs of a response and a request that are not the request's failed: "hàm của routine.go dùng
// response của request".
func TestRoutineSpecCompiles(t *testing.T) {
	const header = "import * as routine from \"kitwork/routine\";\n"
	for _, source := range []string{
		header + `const handle = (context) => {
  const email = context.request.json().email;
  routine.go(() => sendWelcome(email));
  return context.status(202).json({ queued: true });
};`,
		header + `const handle = (context) => routine.wait(() => context.query("a"), () => context.request.json());`,
		header + `const [users, products] = routine.wait(() => loadUsers(), () => loadProducts());
const report = routine.go(() => buildReport(day));
const result = routine.wait(report);
const [a, b] = routine.go(() => processA(), () => processB());
routine.cancel(a, b);
const users2 = routine.retry(2).timeout("5s").wait(() => loadUsers());
routine.retry(3).timeout("30s").go(() => refreshCache());
const both = routine.wait(a, b);`,
		header + `const handle = (context) => routine.go((context) => context);`,
		`const go = (f) => f(); go(() => 1);`,
		`import { go, wait, cancel, retry, timeout } from "kitwork/routine";
const task = go(() => 1);
const all = wait(task, go(() => 2));
cancel(task);
const later = retry(1).timeout("1s").wait(() => 3);`,
		`import { routine } from "kitwork"; routine.wait(() => 1);`,
		header + `const handle = (context) => {
  const response = http.get("https://example.com").send();
  routine.go(() => save(response.status));
  return context.json(response.ok);
};`,
		header + `const request = { id: 1 };
const handle = (context) => routine.go(() => save(request.id));`,
	} {
		if _, err := CompileSource(source); err != nil {
			t.Errorf("%v\n%s", err, source)
		}
	}
}
