package hydrate

import (
	"strings"
	"testing"
)

// call.js on the kernel (28/10/2026, docs/KITWORK_BRIDGE.md): the page's data seeded into its scope, a job
// called by name from the markup, its answer the data it changed, applied by name and a list by row.
//
// The network here is a fake server that runs each call as it arrives, against its own data, and a wire
// that delivers what is out newest first: the case where an answer computed from an older page arrives
// after a newer one.

// callPage is the todo page as the server draws it, the page's data at the end, and call.js loaded on the
// kernel, with the fake server and the wire.
const callPage = drawnList + `
var page = el("script", { "type": "application/json", "data-kitwork-page": "" });
page.textContent = JSON.stringify({ state: { title: "", left: 1, todos: kit.scope.$.todos }, jobs: ["toggle", "remove"], vars: ["title"] });
document.body.appendChild(page);
first.childNodes[0].setAttribute("data-kitwork-event", "change: toggle(todo.id, !todo.done)");
first.childNodes[0].setAttribute("data-kitwork-bind", "checked: todo.done; disabled: toggle.pending");
second.childNodes[0].setAttribute("data-kitwork-event", "change: toggle(todo.id, !todo.done)");
second.childNodes[0].setAttribute("data-kitwork-bind", "checked: todo.done; disabled: toggle.pending");
var notice = el("p", { "data-kitwork-bind": "text: toggle.error ? toggle.error.message : ''" });
document.body.appendChild(notice);

var server = [{ id: 1, title: "Mua sữa", done: false }, { id: 2, title: "Gửi thư", done: true }];
var out = [], sent = [], refuse = false;
global.fetch = function (url, options) {
  var body = JSON.parse(options.body), job = new URL(url, "http://x").searchParams.get("kitwork-call");
  sent.push({ job: job, body: body });
  var answer;
  if (refuse) {
    answer = { seq: body.seq, error: { code: 500, message: "đơn đã khoá" } };
  } else {
    var id = body.args[0], done = body.args[1];
    server = server.map(function (t) { return t.id === id ? { id: t.id, title: t.title, done: done } : t; });
    var row = server.filter(function (t) { return t.id === id; })[0];
    answer = { seq: body.seq, patch: { left: server.filter(function (t) { return !t.done; }).length }, lists: { todos: { key: "id", set: [row] } } };
  }
  return new Promise(function (resolve) {
    out.push(function () {
      resolve({ redirected: false, ok: !refuse, status: refuse ? 500 : 200, headers: { get: function () { return "application/json"; } }, json: function () { return Promise.resolve(answer); } });
    });
  });
};
function tick() { return new Promise(function (r) { setTimeout(r, 0); }); }
// the wire: everything out is delivered newest first, until nothing is out
async function deliver() {
  for (var round = 0; round < 10 && out.length; round++) {
    var now = out.splice(0, out.length).reverse();
    for (var i = 0; i < now.length; i++) { now[i](); await tick(); }
    await tick();
  }
}
function change(row) {
  var input = box(row);
  input.checked = !input.checked; // the browser ticks the box
  document.dispatchEvent({ type: "change", target: input, defaultPrevented: false, preventDefault: function () {}, stopPropagation: function () {} });
}
` + "\n__CALL_JS__\n"

// orderAssertions: two boxes changed one after the other; whatever the wire does, the page ends on the
// server's last answer.
const orderAssertions = `
(async function () {
  kit.render();
  change(first);   // tick "Mua sữa": not done → done
  change(second);  // untick "Gửi thư": done → not done
  if (sent.length !== 1) throw new Error("a second call left while the first was out: " + sent.length + " out");
  await deliver();
  if (sent.length !== 2) throw new Error("expected both calls sent, got " + sent.length);
  if (sent[1].body.seq !== sent[0].body.seq + 1) throw new Error("calls should be numbered in order: " + JSON.stringify(sent.map(function (s) { return s.body.seq; })));
  var s = kit.scope.$;
  if (s.left !== 1) throw new Error("left should be the server's last answer (1), got " + s.left + ": an older answer landed on a newer page");
  var byId = {}; s.todos.forEach(function (t) { byId[t.id] = t.done; });
  if (byId[1] !== true || byId[2] !== false) throw new Error("rows should be the server's: " + JSON.stringify(s.todos));
  if (rows()[0] !== first || rows()[1] !== second) throw new Error("the rows should stay the same nodes");
  if (box(first).checked !== true || box(second).checked !== false) throw new Error("the boxes should show the answers");
  if (box(first).disabled || box(second).disabled) throw new Error("no call is out: nothing should be disabled");
  console.log("calls go one at a time; the page ends on the server's last answer");
})().catch(function (e) { console.error(e && e.stack || e); process.exit(1); });
`

func callScript(callJS, assertions string) string {
	return strings.Replace(callPage, "__CALL_JS__", callJS, 1) + assertions
}

func TestCallsGoOneAtATime(t *testing.T) {
	runNodeDOMScript(t, "call_order.test.js", callScript(callJS, orderAssertions))
}

// The control: with every call sent at once, the wire's older answer lands last and the page shows it, the
// failure the queue is there for (the check that one call is out at a time taken away, to reach it).
func TestCallsAtOnceLetAnOldAnswerWin(t *testing.T) {
	gate := "if (flying || !queue.length) return;"
	oneOut := `if (sent.length !== 1) throw new Error("a second call left while the first was out: " + sent.length + " out");`
	if !strings.Contains(callJS, gate) || !strings.Contains(orderAssertions, oneOut) {
		t.Fatal("the queue of call.js or the test moved: update this control")
	}
	script := callScript(strings.Replace(callJS, gate, "if (!queue.length) return;", 1), strings.Replace(orderAssertions, oneOut, "", 1))
	out, err := runNodeDOMWith(t, composedRuntime(), script)
	if err == nil || !strings.Contains(out, "an older answer landed on a newer page") {
		t.Fatalf("without the queue the older answer should win; got err=%v\n%s", err, out)
	}
}

// A call out marks its own row pending, never another's; a call that fails puts its box back to the data
// and shows the error; the page's state is the server's data, seeded from the page.
func TestCallPendingErrorAndSeed(t *testing.T) {
	runNodeDOMScript(t, "call_pending.test.js", callScript(callJS, `
(async function () {
  kit.render();
  if (kit.scope.$.left !== 1 || kit.scope.$.title !== "" || typeof kit.scope.$.toggle !== "function") throw new Error("the page's data and jobs should be in its scope");
  change(first);
  kit.render();
  if (box(first).disabled !== true) throw new Error("the row whose call is out should be pending");
  if (box(second).disabled === true) throw new Error("another row should not be pending");
  if (box(first).checked !== true) throw new Error("the box the person ticked should stay ticked while its call is out");
  await deliver();
  if (box(first).disabled) throw new Error("after the answer the row is not pending");

  refuse = true;
  change(second); // untick: the server refuses
  kit.render();
  if (box(second).checked !== false) throw new Error("the box should show what the person did while the call is out");
  await deliver();
  if (box(second).checked !== true) throw new Error("a call that failed should put the box back to the data (done)");
  if (notice.textContent !== "đơn đã khoá") throw new Error("the error should reach toggle.error: " + JSON.stringify(notice.textContent));
  refuse = false;
  change(second);
  kit.render();
  if (notice.textContent !== "") throw new Error("a new call of the job should clear its error, got " + JSON.stringify(notice.textContent));
  await deliver();
  console.log("pending per row, a failed call goes back, the error shows and clears");
})().catch(function (e) { console.error(e && e.stack || e); process.exit(1); });
`))
}

// A list's change by row: removed rows go, set rows replace theirs or come at the end, an order reorders.
func TestCallAppliesAListByRow(t *testing.T) {
	runNodeDOMScript(t, "call_list.test.js", callScript(callJS, `
(async function () {
  kit.render();
  global.fetch = function () {
    return Promise.resolve({ redirected: false, ok: true, status: 200, headers: { get: function () { return "application/json"; } },
      json: function () { return Promise.resolve({ seq: 1, lists: { todos: { key: "id", remove: [1], set: [{ id: 3, title: "Gọi mẹ", done: false }], order: [3, 2] } } }); } });
  };
  kit.scope.$.toggle(1, true);
  await tick(); await tick(); await tick();
  var ids = kit.scope.$.todos.map(function (t) { return t.id; }).join(",");
  if (ids !== "3,2") throw new Error("after remove 1, set 3 and order 3,2 the list should be 3,2, got " + ids);
  if (rows().length !== 2 || rows()[1] !== second) throw new Error("the kept row should stay the same node");
  console.log("a list's change applies by row");
})().catch(function (e) { console.error(e && e.stack || e); process.exit(1); });
`))
}
