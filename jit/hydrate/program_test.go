package hydrate

import (
	"strings"
	"testing"
)

// A page with a program (ui/page_program.go, 28/10/2026): its elements carry data-k marks, its program says
// what each mark writes and which names it reads, and call.js runs again only what reads a name that
// changed. The todo page with 1.000 rows, as the server draws it, on the DOM shim; view.runs counts the
// values the program computes.
const programPage = `
var kit = window.kit;
var P = kit.compile;
var program = {
  g: [
    { l: -1, b: [{ n: "value", x: P("title"), r: ["title"] }] },
    { l: -1, b: [{ n: "disabled", x: P("add.pending"), r: ["add"] }] },
    { l: -1, e: [{ n: "submit.prevent", x: P("add()"), r: ["add"] }] },
    { l: -1, b: [{ n: "show", x: P("add.error"), r: ["add"] }, { n: "text", x: P("add.error.message"), r: ["add"] }] },
    { l: -1, b: [{ n: "show", x: P("todos.length === 0"), r: ["todos"] }] },
    { l: 0, b: [{ n: "checked", x: P("todo.done") }, { n: "disabled", x: P("toggle.pending"), r: ["toggle"] }],
      e: [{ n: "change", x: P("toggle(todo.id, !todo.done)"), r: ["toggle"] }] },
    { l: 0, b: [{ n: "text", x: P("todo.title") }], c: [{ n: "color-ink-30", x: P("todo.done") }] },
    { l: 0, b: [{ n: "disabled", x: P("remove.pending"), r: ["remove"] }], e: [{ n: "click", x: P("remove(todo.id)"), r: ["remove"] }] },
    { l: -1, b: [{ n: "text", x: P("'Còn ' + left + ' việc chưa xong.'"), r: ["left"] }] }
  ],
  l: [{ i: "todo", a: P("todos"), k: P("todo.id"), r: ["todos"] }]
};
kit.pages = { p1: program };

var N = 1000, todos = [];
for (var i = 1; i <= N; i++) todos.push({ id: i, title: "Việc số " + i, done: false });
var main = el("main");
var form = el("form", { "data-k": "2" });
var input = el("input", { "data-k": "0" });
var add = el("button", { "data-k": "1", "type": "submit" });
form.appendChild(input); form.appendChild(add);
main.appendChild(form);
var notice = el("p", { "data-k": "3", "hidden": "" });
main.appendChild(notice);
var empty = el("p", { "data-k": "4", "hidden": "" });
main.appendChild(empty);
var ul = el("ul");
ul.appendChild(document.createComment("kit-for:start id=s1"));
function row(attrs, todo) {
  var li = el("li", attrs);
  var box = el("input", { "type": "checkbox", "data-k": "5" });
  box.checked = !!(todo && todo.done);
  var text = el("p", { "data-k": "6" });
  text.textContent = todo ? todo.title : "";
  var remove = el("button", { "data-k": "7" });
  li.appendChild(box); li.appendChild(text); li.appendChild(remove);
  return li;
}
ul.appendChild(row({ "data-k-for": "0", "data-kitwork-template": "", "hidden": "" }, null));
todos.forEach(function (todo) { ul.appendChild(row({ "data-kitwork-item": "s1", "data-kitwork-key": String(todo.id) }, todo)); });
ul.appendChild(document.createComment("kit-for:end"));
main.appendChild(ul);
var left = el("p", { "data-k": "8" });
left.textContent = "Còn " + N + " việc chưa xong.";
main.appendChild(left);
var block = el("script", { "type": "application/json", "data-kitwork-page": "" });
block.textContent = JSON.stringify({ state: { title: "", todos: todos, left: N }, jobs: ["add", "toggle", "remove"], vars: ["title"], program: "p1" });
main.appendChild(block);
document.body.appendChild(main);

var server = todos.map(function (t) { return { id: t.id, title: t.title, done: t.done }; });
var out = [], refuse = false;
global.fetch = function (url, options) {
  var body = JSON.parse(options.body), job = options.headers["Kitwork-Call"], answer;
  if (refuse) answer = { seq: body.seq, error: { code: 500, message: "đơn đã khoá" } };
  else if (job === "toggle") {
    server = server.map(function (t) { return t.id === body.args[0] ? { id: t.id, title: t.title, done: body.args[1] } : t; });
    answer = { seq: body.seq, patch: { left: server.filter(function (t) { return !t.done; }).length },
      lists: { todos: { key: "id", set: server.filter(function (t) { return t.id === body.args[0]; }) } } };
  } else if (job === "remove") {
    server = server.filter(function (t) { return t.id !== body.args[0]; });
    answer = { seq: body.seq, patch: { left: server.filter(function (t) { return !t.done; }).length }, lists: { todos: { key: "id", remove: [body.args[0]] } } };
  } else if (job === "add") {
    var made = { id: server.length + 1000, title: body.vars.title, done: false };
    server = server.concat([made]);
    answer = { seq: body.seq, patch: { title: "", left: server.filter(function (t) { return !t.done; }).length }, lists: { todos: { key: "id", set: [made] } } };
  }
  return new Promise(function (resolve) {
    out.push(function () {
      resolve({ redirected: false, ok: !refuse, status: refuse ? 500 : 200, headers: { get: function () { return "application/json"; } }, json: function () { return Promise.resolve(answer); } });
    });
  });
};
function tick() { return new Promise(function (r) { setTimeout(r, 0); }); }
async function deliver() { while (out.length) { out.shift()(); await tick(); await tick(); } }
function rows() { return ul.querySelectorAll("[data-kitwork-item]"); }
function fire(type, target) {
  var e = { type: type, target: target, defaultPrevented: false, preventDefault: function () { this.defaultPrevented = true; }, stopPropagation: function () {} };
  document.dispatchEvent(e);
  return e;
}
` + "\n__CALL_JS__\n"

func programScript(callJS, assertions string) string {
	return strings.Replace(programPage, "__CALL_JS__", callJS, 1) + assertions
}

// typing: one keystroke computes what reads title, the field itself; nothing of the thousand rows
const typingAssertions = `
(async function () {
  var view = kit.page.view();
  if (!view) throw new Error("the page should run on its program");
  if (rows().length !== N) throw new Error("the drawn rows should be adopted, got " + rows().length);
  var before = view.runs;
  input.value = "m"; fire("input", input);
  input.value = "mu"; fire("input", input);
  input.value = "mua"; fire("input", input);
  var ran = view.runs - before;
  if (view.state.title !== "mua") throw new Error("what is typed is the var title, got " + view.state.title);
  if (ran > 3) throw new Error("three keystrokes computed " + ran + " values: only what reads title should run again");
  console.log("three keystrokes at " + N + " rows computed " + ran + " values");
})().catch(function (e) { console.error(e && e.stack || e); process.exit(1); });
`

func TestProgramRunsOnlyWhatReadsTheNameTyped(t *testing.T) {
	runNodeDOMScript(t, "program_typing.test.js", programScript(callJS, typingAssertions))
}

// The control: a flush that runs every mark for any name computes thousands of values per keystroke.
func TestProgramTypingWithoutTheNameFilterRunsEverything(t *testing.T) {
	pageFilter := "Object.keys(pageMarks).forEach(function (n) { if (pageEls[n]) runGroup(+n, pageEls[n], state, pageMarks[n]); });"
	rowFilter := "if (!rowReaders[name] || isJob[name]) return;"
	if !strings.Contains(callJS, pageFilter) || !strings.Contains(callJS, rowFilter) {
		t.Fatal("the filter of call.js moved: update this control")
	}
	mutated := strings.Replace(callJS, pageFilter, "Object.keys(pageEls).forEach(function (n) { runGroup(+n, pageEls[n], state); });", 1)
	mutated = strings.Replace(mutated, rowFilter, "if (isJob[name]) return; rowReaders[name] = groups.map(function (g, i) { return i; });", 1)
	out, err := runNodeDOMWith(t, composedRuntime(), programScript(mutated, typingAssertions))
	if err == nil || !strings.Contains(out, "only what reads title should run again") {
		t.Fatalf("without the filter the keystrokes should run everything; got err=%v\n%s", err, out)
	}
}

// ticking: the box stays ticked while the call is out, only its row is pending, and the answer runs again
// that row and what reads the count, nothing of the other rows
const tickAssertions = `
(async function () {
  var view = kit.page.view();
  var first = rows()[0], second = rows()[1];
  var before = view.runs;
  first.childNodes[0].checked = true;          // the browser ticks the box
  fire("change", first.childNodes[0]);
  if (first.childNodes[0].disabled !== true) throw new Error("the row whose call is out should be pending");
  if (second.childNodes[0].disabled === true) throw new Error("another row should not be pending");
  if (first.childNodes[0].checked !== true) throw new Error("the box should stay ticked while its call is out");
  await deliver();
  var ran = view.runs - before;
  if (first.childNodes[0].checked !== true || first.childNodes[0].disabled) throw new Error("after the answer: ticked, not pending");
  if (left.textContent !== "Còn " + (N - 1) + " việc chưa xong.") throw new Error("the count should follow: " + left.textContent);
  if (rows()[0] !== first || rows()[1] !== second) throw new Error("the rows should stay the same nodes");
  if (ran > 40) throw new Error("a tick computed " + ran + " values: only its row and what reads the count should run again");
  console.log("a tick at " + N + " rows computed " + ran + " values");
})().catch(function (e) { console.error(e && e.stack || e); process.exit(1); });
`

func TestProgramTickRunsItsRowOnly(t *testing.T) {
	runNodeDOMScript(t, "program_tick.test.js", programScript(callJS, tickAssertions))
}

// The control: a reconcile that takes every row for changed runs the whole list again on a tick.
func TestProgramTickWithoutRowIdentityRunsEveryRow(t *testing.T) {
	identity := "} else if (row.item !== item) {"
	if !strings.Contains(callJS, identity) {
		t.Fatal("the row identity of call.js moved: update this control")
	}
	out, err := runNodeDOMWith(t, composedRuntime(), programScript(strings.Replace(callJS, identity, "} else if (true) {", 1), tickAssertions))
	if err == nil || !strings.Contains(out, "only its row and what reads the count should run again") {
		t.Fatalf("without the row identity a tick should run every row; got err=%v\n%s", err, out)
	}
}

// a row added and removed by the answers, a form that calls a job stays on the page, a failed call puts its
// box back and shows its error
func TestProgramRowsFormAndErrors(t *testing.T) {
	runNodeDOMScript(t, "program_rows.test.js", programScript(callJS, `
(async function () {
  input.value = "Gọi mẹ"; fire("input", input);
  var submitted = fire("submit", form);
  if (!submitted.defaultPrevented) throw new Error("a form that calls a job should stay on the page");
  if (add.disabled !== true) throw new Error("add.pending should disable its button while the call is out");
  await deliver();
  var all = rows();
  if (all.length !== N + 1) throw new Error("the answer's new row should be made, got " + all.length);
  var made = all[all.length - 1];
  if (made.hasAttribute("hidden") || made.childNodes[1].textContent !== "Gọi mẹ") throw new Error("the new row should show its title");
  if (input.value !== "") throw new Error("the answer set title to \"\": the field should follow, got " + input.value);

  fire("click", rows()[2].childNodes[2]);       // remove the third row
  await deliver();
  if (rows().length !== N) throw new Error("the removed row should leave, got " + rows().length);

  refuse = true;
  var box = rows()[0].childNodes[0];
  box.checked = true; fire("change", box);
  await deliver();
  if (box.checked !== false) throw new Error("a failed call should put its box back to the data");
  refuse = false;
  console.log("rows made and removed by the answers; the form stays; a failed call goes back");
})().catch(function (e) { console.error(e && e.stack || e); process.exit(1); });
`))
}
