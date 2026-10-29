package hydrate

import (
	"strings"
	"testing"
)

// A search typed in (29/10/2026): the field holds the var query, its input event calls the job search, and the
// server answers with what the query finds. A call made as the field is typed in waits for a pause in the
// typing, and the next keystroke's call to the same job takes its place; a call made any other way (the
// button that clears; the box beside it, ticked, which fires input too) takes the ones waiting with it, them
// first. The server's answers are held until deliver() lets them go.
const searchTypedPage = `
var kitwork = window.kitwork;
var P = kitwork.compile;
kitwork.pages = { s1: {
  g: [
    { l: -1, b: [{ n: "value", x: P("query"), r: ["query"] }], e: [{ n: "input", x: P("search()"), r: ["search"] }] },
    { l: -1, e: [{ n: "click", x: P("clear()"), r: ["clear"] }] },
    { l: -1, b: [{ n: "text", x: P("found"), r: ["found"] }] },
    { l: -1, e: [{ n: "input", x: P("flip()"), r: ["flip"] }] }
  ],
  l: []
} };
var main = el("main");
var input = el("input", { "data-kitwork-mark": "0" });
var clear = el("button", { "data-kitwork-mark": "1" });
var found = el("p", { "data-kitwork-mark": "2" });
var box = el("input", { "type": "checkbox", "data-kitwork-mark": "3" });
main.appendChild(input); main.appendChild(clear); main.appendChild(found); main.appendChild(box);
var block = el("script", { "type": "application/json", "data-kitwork-page": "" });
block.textContent = JSON.stringify({ state: { query: "", found: 0 }, jobs: ["search", "clear", "flip"], vars: ["query"], program: "s1" });
main.appendChild(block);
document.body.appendChild(main);

var sent = [], out = [];
global.fetch = function (url, options) {
  var body = JSON.parse(options.body), job = new URL(url, "http://x").searchParams.get("kitwork-call");
  sent.push({ job: job, vars: body.vars });
  var answer = job === "clear" ? { seq: body.seq, patch: { query: "", found: 0 } }
    : job === "flip" ? { seq: body.seq } : { seq: body.seq, patch: { found: body.vars.query.length } };
  return new Promise(function (resolve) {
    out.push(function () {
      resolve({ redirected: false, ok: true, status: 200, headers: { get: function () { return "application/json"; } }, json: function () { return Promise.resolve(answer); } });
    });
  });
};
function tick() { return new Promise(function (r) { setTimeout(r, 0); }); }
function wait(ms) { return new Promise(function (r) { setTimeout(r, ms); }); }
async function deliver() { while (out.length) { out.shift()(); await tick(); await tick(); } }
function fire(type, target) {
  var e = { type: type, target: target, defaultPrevented: false, preventDefault: function () { this.defaultPrevented = true; }, stopPropagation: function () {} };
  document.dispatchEvent(e);
  return e;
}
function jobs() { return sent.map(function (s) { return s.job; }).join(","); }
` + "\n__CALL_JS__\n"

const searchTypedAssertions = `
(async function () {
  var view = kitwork.page.view();
  if (!view) throw new Error("the page should run on its program");
  input.value = "m"; fire("input", input);
  input.value = "mu"; fire("input", input);
  input.value = "mua"; fire("input", input);
  await tick();
  if (sent.length !== 0) throw new Error("a call made as a field is typed in should wait for a pause in the typing, sent " + jobs());
  if (view.state.search.pending !== true) throw new Error("search.pending should be true while its call waits");
  await wait(260);
  if (sent.length !== 1) throw new Error("three keystrokes should be one call, sent " + jobs());
  if (sent[0].vars.query !== "mua") throw new Error("the call should carry what is typed when it leaves, sent " + JSON.stringify(sent[0].vars));
  await deliver();
  if (view.state.search.pending !== false) throw new Error("search.pending should end with the answer: the calls replaced end with it");
  if (found.textContent !== "3") throw new Error("the answer should be painted, got " + found.textContent);

  input.value = "mua s"; fire("input", input);
  fire("click", clear);
  await tick();
  if (jobs() !== "search,search") throw new Error("typing then pressing a button should send what was typed first, sent " + jobs());
  await deliver();
  if (jobs() !== "search,search,clear") throw new Error("the button's call should follow, sent " + jobs());
  await deliver();
  if (input.value !== "" || found.textContent !== "0") throw new Error("the button's answer should land last, got " + JSON.stringify(input.value) + " " + found.textContent);
  console.log("three keystrokes are one call after a pause; a button pressed after typing goes after it");
})().catch(function (e) { console.error(e && e.stack || e); process.exit(1); });
`

func searchTypedScript(callJS, assertions string) string {
	return strings.Replace(searchTypedPage, "__CALL_JS__", callJS, 1) + assertions
}

func TestTypingInAFieldIsOneCallAfterAPause(t *testing.T) {
	runNodeDOMScript(t, "typing.test.js", searchTypedScript(callJS, searchTypedAssertions))
}

// The controls: a call made by typing that leaves at once, and a call made any other way that leaves before
// the typing waiting, are caught.
func TestTypingThatCallsWrongIsCaught(t *testing.T) {
	cases := []struct{ name, from, to, want string }{
		{"each keystroke at once", `if (e && e.type === "input" && typedIn(e.target)) {`, `if (false) {`, "should wait for a pause"},
		{"a button before the typing", "    release(typed[typed.length - 1]);\n    queue.push(call);", "    queue.push(call);", "what was typed first"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !strings.Contains(callJS, c.from) {
				t.Fatal("call.js moved: update this control")
			}
			out, err := runNodeDOMWith(t, composedRuntime(), searchTypedScript(strings.Replace(callJS, c.from, c.to, 1), searchTypedAssertions))
			if err == nil || !strings.Contains(out, c.want) {
				t.Fatalf("%s should be caught; got err=%v\n%s", c.name, err, out)
			}
		})
	}
}

// A box ticked fires input too, and is no typing: two ticks made at once are two calls, each as it is made.
const boxTickedAssertions = `
(async function () {
  box.checked = true; fire("input", box);
  box.checked = false; fire("input", box);
  await tick();
  if (jobs() !== "flip") throw new Error("a box ticked should call at once, sent " + jobs());
  await deliver();
  if (jobs() !== "flip,flip") throw new Error("two ticks should be two calls, sent " + jobs());
  await deliver();
  console.log("a box ticked calls at once, each tick its own call");
})().catch(function (e) { console.error(e && e.stack || e); process.exit(1); });
`

func TestABoxTickedIsNoTyping(t *testing.T) {
	runNodeDOMScript(t, "box_ticked.test.js", searchTypedScript(callJS, boxTickedAssertions))
}

// The control: a call made in any input event taken for typing makes the ticks wait, and keeps only the last.
func TestABoxTickedTakenForTypingIsCaught(t *testing.T) {
	typed := `if (e && e.type === "input" && typedIn(e.target)) {`
	if !strings.Contains(callJS, typed) {
		t.Fatal("make of call.js moved: update this control")
	}
	out, err := runNodeDOMWith(t, composedRuntime(), searchTypedScript(strings.Replace(callJS, typed, `if (e && e.type === "input" && e.target) {`, 1), boxTickedAssertions))
	if err == nil || !strings.Contains(out, "should call at once") {
		t.Fatalf("a box ticked taken for typing should be caught; got err=%v\n%s", err, out)
	}
}

// On a page the kernel paints (a site component's job), kitwork.event() is the event whose handler runs now:
// the input event while it is dispatched, null between, and null for a handler debounce(n) runs later, so the
// author's own pause is not added to the bridge's.
const dispatchingAssertions = `
var kitwork = window.kitwork;
var seen = [];
kitwork.scope.$.probe = function () { var e = kitwork.event(); seen.push(e ? e.type : null); };
var now = el("input", { "data-kitwork-event": "input: probe()" });
var later = el("input", { "data-kitwork-event": "input.debounce(10): probe()" });
document.body.appendChild(now); document.body.appendChild(later);
kitwork.render();
document.dispatchEvent({ type: "input", target: now, stopPropagation: function () {} });
if (seen[0] !== "input") throw new Error("kitwork.event() should be the input event its handler runs in, saw " + JSON.stringify(seen));
if (kitwork.event() !== null) throw new Error("kitwork.event() should be null between handlers");
document.dispatchEvent({ type: "input", target: later, stopPropagation: function () {} });
setTimeout(function () {
  if (seen.length !== 2 || seen[1] !== null) throw new Error("a handler debounce(n) runs later should see no event, saw " + JSON.stringify(seen));
  console.log("kitwork.event() is the event a handler runs in, while it is dispatched");
}, 40);
`

func TestEventSaysWhatAHandlerRunsIn(t *testing.T) {
	runNodeDOMScript(t, "dispatching.test.js", dispatchingAssertions)
}

// The control: a kernel that says the event even for a handler it runs later is caught.
func TestEventOfADeferredHandlerIsCaught(t *testing.T) {
	runtime := composedRuntime()
	set := "dispatching = now ? e : null;"
	if !strings.Contains(runtime, set) {
		t.Fatal("the kernel's dispatching moved: update this control")
	}
	out, err := runNodeDOMWith(t, strings.Replace(runtime, set, "dispatching = e;", 1), dispatchingAssertions)
	if err == nil || !strings.Contains(out, "should see no event") {
		t.Fatalf("a deferred handler that sees its event should be caught; got err=%v\n%s", err, out)
	}
}

// A slow search (29/10/2026): the first call is out, and the person types on, pausing each time. Each pause
// sends a call to the queue behind the one out; the last typed takes the place of the one waiting there, so
// when the first is answered, only the last goes, with what is typed then.
const slowSearchAssertions = `
(async function () {
  var view = kitwork.page.view();
  input.value = "m"; fire("input", input);
  await wait(260);
  if (jobs() !== "search") throw new Error("the first pause should send its call, sent " + jobs());
  input.value = "mu"; fire("input", input);
  await wait(260);
  input.value = "mua"; fire("input", input);
  await wait(260);
  if (jobs() !== "search") throw new Error("calls typed while one is out should wait for it, sent " + jobs());
  if (view.state.search.pending !== true) throw new Error("search.pending should be true while a call waits");
  await deliver();
  await deliver();
  if (jobs() !== "search,search") throw new Error("the last typed should take the place of the one waiting before it: one call after the slow one, sent " + jobs());
  if (sent[1].vars.query !== "mua") throw new Error("the call that goes should carry what is typed last, sent " + JSON.stringify(sent[1].vars));
  if (view.state.search.pending !== false) throw new Error("search.pending should end with the last answer: the call replaced ends with it");
  if (found.textContent !== "3") throw new Error("the last answer should be painted, got " + found.textContent);
  console.log("typing on while a slow search is out sends only the last typed after it");
})().catch(function (e) { console.error(e && e.stack || e); process.exit(1); });
`

func TestTypingOnWhileASearchIsOutSendsOnlyTheLast(t *testing.T) {
	runNodeDOMScript(t, "slow_search.test.js", searchTypedScript(callJS, slowSearchAssertions))
}

// The control: a call typed that leaves the one before it in the queue sends both after the slow one.
func TestTypingOnThatKeepsEveryCallIsCaught(t *testing.T) {
	keep := "      queue = queue.filter(function (queued) {\n        if (!queued.typed || !sameField(queued, waiting)) return true;"
	if !strings.Contains(callJS, keep) {
		t.Fatal("release of call.js moved: update this control")
	}
	out, err := runNodeDOMWith(t, composedRuntime(), searchTypedScript(strings.Replace(callJS, keep, "      queue = queue.filter(function (queued) {\n        if (true) return true;", 1), slowSearchAssertions))
	if err == nil || !strings.Contains(out, "take the place of the one waiting before it") {
		t.Fatalf("a call typed that keeps the one before it should be caught; got err=%v\n%s", err, out)
	}
}
