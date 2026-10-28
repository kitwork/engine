package hydrate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A page of router.view runs in the kernel (28/10/2026, docs/KITWORK_BRIDGE.md): the server draws its
// list as the marker pair, the template hidden after the start marker, and the rows with the list's id
// and their key. The kernel adopts that list, and writes a field only when its value changes, so a box
// the person ticked stays ticked while the call it made is out.

// drawnList is the list a page of router.view arrives with: two rows drawn, a template hidden before them.
const drawnList = `
var kit = window.kit;
var ul = el("ul");
var start = document.createComment("kit-for:start id=s1");
var template = el("li", { "data-kitwork-for": "todo of todos; key: todo.id", "data-kitwork-template": "", "hidden": "" });
template.appendChild(el("input", { "type": "checkbox", "data-kitwork-bind": "checked: todo.done" }));
template.appendChild(el("span", { "data-kitwork-bind": "text: todo.title" }));
function drawn(id, title, done) {
  var li = el("li", { "data-kitwork-item": "s1", "data-kitwork-key": String(id) });
  var box = el("input", { "type": "checkbox", "data-kitwork-bind": "checked: todo.done" });
  box.checked = done;
  var span = el("span", { "data-kitwork-bind": "text: todo.title" });
  span.textContent = title;
  li.appendChild(box);
  li.appendChild(span);
  return li;
}
var end = document.createComment("kit-for:end");
ul.appendChild(start);
ul.appendChild(template);
var first = drawn(1, "Mua sữa", false), second = drawn(2, "Gửi thư", true);
ul.appendChild(first);
ul.appendChild(second);
ul.appendChild(end);
document.body.appendChild(ul);
kit.scope.$.todos = [{ id: 1, title: "Mua sữa", done: false }, { id: 2, title: "Gửi thư", done: true }];
kit.render();
function rows() { return ul.querySelectorAll("[data-kitwork-item]"); }
function box(row) { return row.childNodes[0]; }  // the shim reads attribute selectors only
function text(row) { return row.childNodes[1].textContent; }
`

func TestDrawnListIsAdoptedByKey(t *testing.T) {
	runNodeDOMScript(t, "drawn_for.test.js", drawnList+`
if (ul.childNodes[0] !== start) throw new Error("the start marker the server drew should stay the list's start");
if (ul.childNodes[ul.childNodes.length - 1] !== end) throw new Error("the end marker the server drew should stay the list's end");
if (ul.querySelectorAll("[data-kitwork-template]").length !== 0) throw new Error("the template should leave the page");
var r = rows();
if (r.length !== 2) throw new Error("expected the 2 drawn rows, got " + r.length);
if (r[0] !== first || r[1] !== second) throw new Error("the rows the server drew should be adopted, not rebuilt");

kit.scope.$.todos = kit.scope.$.todos.concat([{ id: 3, title: "Gọi mẹ", done: false }]);
kit.render();
r = rows();
if (r.length !== 3) throw new Error("after a new row: expected 3, got " + r.length);
if (r[0] !== first || r[1] !== second) throw new Error("after a new row: the drawn rows were rebuilt");
var made = r[2];
if (made.getAttribute("data-kitwork-item") !== "s1" || made.getAttribute("data-kitwork-key") !== "3") throw new Error("the new row should carry the list's id and its key, got " + made.getAttribute("data-kitwork-item") + " " + made.getAttribute("data-kitwork-key"));
if (made.hasAttribute("hidden") || made.hasAttribute("data-kitwork-template")) throw new Error("a row made from the template should not be hidden");
if (text(made) !== "Gọi mẹ") throw new Error("the new row's text = " + text(made));

kit.scope.$.todos = kit.scope.$.todos.filter(function (todo) { return todo.id !== 1; });
kit.render();
r = rows();
if (r.length !== 2 || r[0] !== second || r[1] !== made) throw new Error("after removing row 1: the others should stay as they are");
console.log("a drawn list is adopted by key, grows from its template and loses a row by key");
`)
}

// fieldStays is the box a person ticks while the call it made is out.
const fieldStays = drawnList + `
var tick = box(first);
tick.checked = true;            // the person ticks it; the data still says not done
kit.render();                   // the paint that shows the call pending
if (tick.checked !== true) throw new Error("a paint whose data did not change unticked the box the person ticked");
kit.forget(first);              // the call failed: the row goes back to its data
kit.render();
if (tick.checked !== false) throw new Error("after kit.forget the box should show the data again (not done)");
tick.checked = true;
kit.scope.$.todos = [{ id: 1, title: "Mua sữa", done: true }, { id: 2, title: "Gửi thư", done: true }];
kit.render();                   // the call answered: done
if (tick.checked !== true) throw new Error("the answer says done: the box should be ticked");
kit.scope.$.todos = [{ id: 1, title: "Mua sữa", done: false }, { id: 2, title: "Gửi thư", done: true }];
kit.render();                   // the data changes back: the field follows it
if (tick.checked !== false) throw new Error("new data should still reach the field");
console.log("a ticked box stays until its data changes; kit.forget puts it back");
`

func TestFieldKeepsWhatThePersonDidUntilItsDataChanges(t *testing.T) {
	runNodeDOMScript(t, "field_stays.test.js", fieldStays)
}

// The control: the kernel that wrote a field on every paint unticks the box (the test above fails).
func TestFieldStaysFailsWithoutTheMemo(t *testing.T) {
	memo := "if (has(written, name) && written[name] === next) return;"
	runtime := composedRuntime()
	if !strings.Contains(runtime, memo) {
		t.Fatal("the memo of writeBinding moved: update this control")
	}
	out, err := runNodeDOMWith(t, strings.Replace(runtime, memo, "", 1), fieldStays)
	if err == nil || !strings.Contains(out, "unticked the box") {
		t.Fatalf("without the memo the box should be unticked; got err=%v\n%s", err, out)
	}
}

func TestElementSaysWhereADirectiveRuns(t *testing.T) {
	runNodeDOMScript(t, "acting.test.js", drawnList+`
var seen = [];
kit.scope.$.where = function () { var el = kit.element(); seen.push(el && el.tagName); return ""; };
var probe = el("b", { "data-kitwork-bind": "text: where()" });
document.body.appendChild(probe);
kit.render();
if (seen.indexOf("B") < 0) throw new Error("a binding should run with kit.element() its element, saw " + JSON.stringify(seen));
if (kit.element() !== null) throw new Error("kit.element() should be null between directives");
console.log("kit.element() is the element a directive runs on");
`)
}

// runNodeDOMWith runs assertions on the DOM shim with a given runtime, returning the output and the
// error instead of failing: a control case that must fail.
func runNodeDOMWith(t *testing.T, runtime, assertions string) (string, error) {
	t.Helper()
	node := requireNode(t)
	path := filepath.Join(t.TempDir(), "control.test.js")
	if err := os.WriteFile(path, []byte(blockDOMShim+"\n"+runtime+"\n"+assertions), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, path).CombinedOutput()
	return string(out), err
}

// A page that runs in the kernel declares its page scope in its data block (ui/page_live.go writes it in the
// skeleton with every name): what it reads of its bind is no undeclared name; a name it does not have still is.
func TestPageDataDeclaresThePageScope(t *testing.T) {
	doc := `<!doctype html><html><head></head><body><main>` +
		`<p data-kitwork-bind="show: todos.length === 0; disabled: add.pending">x</p>` +
		`<li data-kitwork-for="todo of todos; key: todo.id"><b data-kitwork-bind="text: todo.title"></b></li>` +
		`<i data-kitwork-bind="text: lefft"></i>` +
		`<script type="application/json" data-kitwork-page>{"jobs":["add"],"state":{"left":null,"todos":null},"vars":[]}</script>` +
		`</main></body></html>`
	got := strings.Join(undeclaredWarnings(doc), "\n")
	for _, name := range []string{`"todos"`, `"add"`, `"todo"`, `"left"`} {
		if strings.Contains(got, "reads "+name) {
			t.Errorf("%s is declared by the page's data block:\n%s", name, got)
		}
	}
	if !strings.Contains(got, `reads "lefft"`) {
		t.Errorf("a name the page does not have should still be told:\n%s", got)
	}
}
