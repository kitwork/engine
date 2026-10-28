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
  var body = JSON.parse(options.body), job = new URL(url, "http://x").searchParams.get("kitwork-call"), answer;
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

// A page's revisions (work/view_patch.go): seeded from its data block, sent with each call, replaced by those
// an answer brings; a name an answer sends whole replaces the page's value.
func TestProgramSendsAndKeepsRevisions(t *testing.T) {
	page := strings.Replace(programPage, `program: "p1" });`, `program: "p1", revs: { todos: "r0", left: "l0" } });`, 1)
	if page == programPage {
		t.Fatal("the data block of the test page moved: update this test")
	}
	script := strings.Replace(page, "__CALL_JS__", callJS, 1) + `
(async function () {
  var sent = [];
  global.fetch = function (url, options) {
    var body = JSON.parse(options.body);
    sent.push(body.revs);
    var answer = sent.length === 1
      ? { seq: body.seq, patch: { left: 7, todos: [{ id: 1, title: "Một", done: false }] }, revs: { todos: "r1", left: "l1" } }
      : { seq: body.seq, revs: {} };
    return Promise.resolve({ redirected: false, ok: true, status: 200, headers: { get: function () { return "application/json"; } }, json: function () { return Promise.resolve(answer); } });
  };
  var view = kit.page.view();
  view.state.toggle(1, true);
  await tick(); await tick(); await tick();
  view.state.toggle(1, false);
  await tick(); await tick(); await tick();
  if (JSON.stringify(sent[0]) !== JSON.stringify({ todos: "r0", left: "l0" })) throw new Error("the first call should send the page's revisions, sent " + JSON.stringify(sent[0]));
  if (JSON.stringify(sent[1]) !== JSON.stringify({ todos: "r1", left: "l1" })) throw new Error("the next call should send the answer's revisions, sent " + JSON.stringify(sent[1]));
  if (rows().length !== 1 || view.state.left !== 7) throw new Error("a list sent whole replaces the page's: " + rows().length + " rows, left " + view.state.left);
  console.log("revisions seeded, sent and kept; a whole list replaces the page's");
})().catch(function (e) { console.error(e && e.stack || e); process.exit(1); });
`
	runNodeDOMScript(t, "program_revs.test.js", script)
}

// A tab seen again (28/10/2026) asks the server, with its revisions, for what changed while it was not seen,
// and takes what comes whole; tabs of one browser tell each other the data an answer brought.
const keptAcrossTabsPage = `
global.__sent = [];
global.fetch = function (url, options) {
  var body = JSON.parse(options.body);
  global.__sent.push({ url: url, headers: options.headers, body: body });
  var answer = new URL(url, "http://x").searchParams.has("kitwork-refresh")
    ? { seq: body.seq, patch: { left: 1, todos: [{ id: 1, title: "Một", done: false }, { id: 2, title: "Hai", done: true }] }, revs: { todos: "r2", left: "l2" }, stamp: 300 }
    : { seq: body.seq, patch: { left: 0, title: "" }, lists: { todos: { key: "id", set: [{ id: 1, title: "Việc số 1", done: true }] } }, revs: { todos: "r1", left: "l1" }, stamp: 200 };
  return Promise.resolve({ redirected: false, ok: true, status: 200, headers: { get: function () { return "application/json"; } }, json: function () { return Promise.resolve(answer); } });
};
`

func keptAcrossTabsScript(callJS, assertions string) string {
	page := strings.Replace(programPage, `program: "p1" });`, `program: "p1", revs: { todos: "r0", left: "l0" }, stamp: 100 });`, 1)
	return strings.Replace(page, "__CALL_JS__", keptAcrossTabsPage+callJS, 1) + assertions
}

const seenAgainAssertions = `
(async function () {
  var view = kit.page.view();
  document.visibilityState = "visible";
  document.dispatchEvent({ type: "visibilitychange" });
  await tick(); await tick(); await tick();
  var asked = global.__sent.filter(function (s) { return s.url.indexOf("kitwork-refresh=1") >= 0; });
  if (asked.length !== 1) throw new Error("a tab seen again should ask for what changed, asked " + asked.length + " times");
  if (JSON.stringify(asked[0].body.revs) !== JSON.stringify({ todos: "r0", left: "l0" })) throw new Error("it should send its revisions, sent " + JSON.stringify(asked[0].body));
  if (rows().length !== 2 || view.state.left !== 1) throw new Error("what changed should replace the page's: " + rows().length + " rows, left " + view.state.left);
  console.log("a tab seen again asks with its revisions and takes what changed");
})().catch(function (e) { console.error(e && e.stack || e); process.exit(1); });
`

func TestTabSeenAgainAsksForWhatChanged(t *testing.T) {
	runNodeDOMScript(t, "seen_again.test.js", keptAcrossTabsScript(callJS, seenAgainAssertions))
}

// The control: with no listener for the tab being seen again, it never asks.
func TestTabSeenAgainWithoutTheListenerAsksNothing(t *testing.T) {
	listener := `document.addEventListener("visibilitychange", function () {`
	if !strings.Contains(callJS, listener) {
		t.Fatal("the visibility listener of call.js moved: update this control")
	}
	out, err := runNodeDOMWith(t, composedRuntime(), keptAcrossTabsScript(strings.Replace(callJS, listener, `document.addEventListener("not-visibilitychange", function () {`, 1), seenAgainAssertions))
	if err == nil || !strings.Contains(out, "should ask for what changed") {
		t.Fatalf("without the listener the tab should not ask; got err=%v\n%s", err, out)
	}
}

const tellOtherTabsAssertions = `
(async function () {
  var view = kit.page.view();
  var channel = global.__channels.filter(function (c) { return c.name === "kitwork:page"; })[0];
  if (!channel) throw new Error("call.js should open the channel kitwork:page");
  view.state.toggle(1, true);
  await tick(); await tick(); await tick();
  var told = channel.sent[channel.sent.length - 1];
  if (!told || told.page !== "/" || told.stamp !== 200 || told.revs.todos !== "r1" || told.values.left !== 0 || told.values.todos.length !== N) {
    throw new Error("an answer should be told to the other tabs with its data whole and its stamp: " + JSON.stringify(told && { page: told.page, stamp: told.stamp, revs: told.revs, left: told.values.left }));
  }
  // another tab of the browser told this one
  channel.onmessage({ data: { page: "/", stamp: 250, values: { left: 7, todos: [{ id: 9, title: "Từ tab khác", done: false }] }, revs: { left: "l9", todos: "r9" } } });
  if (rows().length !== 1 || view.state.left !== 7) throw new Error("what another tab told should replace the page's: " + rows().length + " rows, left " + view.state.left);
  channel.onmessage({ data: { page: "/khac", stamp: 260, values: { left: 1 }, revs: { left: "x" } } });
  if (view.state.left !== 7) throw new Error("what another page told is not this page's");
  console.log("an answer is told to the other tabs, and what they tell is taken");
})().catch(function (e) { console.error(e && e.stack || e); process.exit(1); });
`

func TestTabsOfOneBrowserTellEachOther(t *testing.T) {
	runNodeDOMScript(t, "tell_tabs.test.js", keptAcrossTabsScript(callJS, tellOtherTabsAssertions))
}

// A message or an answer that arrives late (28/10/2026): every answer carries the moment the server read its
// data (stamp), and a tab takes no data read before the data it holds. Here the page was drawn at 100, the
// server answers its calls with data read at 200; another tab tells it data read at 250, then, late, data read
// at 240; its own call answers after that.
const olderDataAssertions = `
(async function () {
  var view = kit.page.view();
  var channel = global.__channels.filter(function (c) { return c.name === "kitwork:page"; })[0];
  channel.onmessage({ data: { page: "/", stamp: 250, values: { left: 7, todos: [{ id: 9, title: "Từ tab khác", done: false }] }, revs: { left: "l9", todos: "r9" } } });
  if (rows().length !== 1 || view.state.left !== 7) throw new Error("data read after the tab's should be taken: " + rows().length + " rows, left " + view.state.left);
  channel.onmessage({ data: { page: "/", stamp: 240, values: { left: 3, todos: [] }, revs: { left: "l3", todos: "r3" } } });
  if (rows().length !== 1 || view.state.left !== 7) throw new Error("a message read before what the tab holds should not take it back: " + rows().length + " rows, left " + view.state.left);
  view.set("title", "mua sữa");
  view.state.add();
  await tick(); await tick(); await tick();
  if (rows().length !== 1 || view.state.left !== 7) throw new Error("an answer read before what the tab holds should not take it back: " + rows().length + " rows, left " + view.state.left);
  if (view.state.title !== "") throw new Error("the page's own var still comes from its answer, title " + JSON.stringify(view.state.title));
  console.log("a tab takes no data read before the data it holds");
})().catch(function (e) { console.error(e && e.stack || e); process.exit(1); });
`

func TestTabsTakeNoDataReadBeforeTheirs(t *testing.T) {
	runNodeDOMScript(t, "older_data.test.js", keptAcrossTabsScript(callJS, olderDataAssertions))
}

// The controls: a tab that takes what it is told whatever its stamp goes back to the late message's data; one
// that takes every answer goes back to its own late answer's.
func TestTabsThatIgnoreTheStampGoBack(t *testing.T) {
	for _, c := range []struct{ name, drop, keep, want string }{
		{"a message", " || !(m.stamp > stamp)) return;", ") return;", "a message read before"},
		{"an answer", "if (answer.stamp && answer.stamp <= stamp) {", "if (false) {", "an answer read before"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if !strings.Contains(callJS, c.drop) {
				t.Fatal("the stamp's check of call.js moved: update this control")
			}
			out, err := runNodeDOMWith(t, composedRuntime(), keptAcrossTabsScript(strings.Replace(callJS, c.drop, c.keep, 1), olderDataAssertions))
			if err == nil || !strings.Contains(out, c.want) {
				t.Fatalf("without the check the tab should go back; got err=%v\n%s", err, out)
			}
		})
	}
}

// A call names its job in the page's URL and carries no header of Kitwork's own (KITWORK_ROUTER_VIEW.md câu
// 7b, 28/10/2026): the shell of a native app passes a request's method, URL and body, and not always its
// headers (the desktop's none, Android's Content-Type and Accept only). The page's own query stays.
const namedInTheURLAssertions = `
(async function () {
  var view = kit.page.view();
  view.state.toggle(1, true);
  await tick(); await tick();
  var sent = global.__sent[global.__sent.length - 1];
  if (!sent || sent.url !== "/?kitwork-call=toggle") throw new Error("a call should name its job in the page's URL, sent to " + (sent && sent.url));
  var names = Object.keys(sent.headers).sort().join(",");
  if (names !== "Accept,Content-Type") throw new Error("a call should carry no header of Kitwork's own, sent " + names);
  location.search = "?tab=2";
  document.visibilityState = "visible";
  document.dispatchEvent({ type: "visibilitychange" });
  await tick(); await tick();
  sent = global.__sent[global.__sent.length - 1];
  if (sent.url !== "/?tab=2&kitwork-refresh=1") throw new Error("the page's own query should stay, the ask after it, sent to " + sent.url);
  console.log("a call names its job in the page's URL and carries no header of Kitwork's own");
})().catch(function (e) { console.error(e && e.stack || e); process.exit(1); });
`

func TestCallsNameTheJobInThePagesURL(t *testing.T) {
	runNodeDOMScript(t, "named_in_url.test.js", keptAcrossTabsScript(callJS, namedInTheURLAssertions))
}

// The control: the job's name in a header, as before, is what a native app's shell does not carry.
func TestCallsNamingTheJobInAHeaderAreCaught(t *testing.T) {
	headers := `headers: { "Content-Type": "application/json", "Accept": "application/json" },`
	if !strings.Contains(callJS, headers) {
		t.Fatal("the transport of call.js moved: update this control")
	}
	old := `headers: { "Content-Type": "application/json", "Accept": "application/json", "Kitwork-Call": call.name },`
	out, err := runNodeDOMWith(t, composedRuntime(), keptAcrossTabsScript(strings.Replace(callJS, headers, old, 1), namedInTheURLAssertions))
	if err == nil || !strings.Contains(out, "no header of Kitwork's own") {
		t.Fatalf("a call with a header of its own should be caught; got err=%v\n%s", err, out)
	}
}

// The control: a tab that does not take what it is told keeps its old rows.
func TestTabsThatDoNotTakeWhatTheyAreToldStayOld(t *testing.T) {
	take := "        put(k, m.values[k]);\n"
	if !strings.Contains(callJS, take) {
		t.Fatal("the channel's listener of call.js moved: update this control")
	}
	out, err := runNodeDOMWith(t, composedRuntime(), keptAcrossTabsScript(strings.Replace(callJS, take, "", 1), tellOtherTabsAssertions))
	if err == nil || !strings.Contains(out, "what another tab told should replace") {
		t.Fatalf("a tab that does not take what it is told should keep its old rows; got err=%v\n%s", err, out)
	}
}
