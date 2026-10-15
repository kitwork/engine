package hydrate

import (
	"os"
	"path/filepath"
	"testing"
)

// The eight grouped attributes of a Kitwork UI page (KITWORK_UI.md §11) driven through the real
// kernel on the node DOM shim (blocks_dom_test.go). Each test sets a hand-written data-kit-* control
// beside the grouped form where the two should agree, and asserts the reason a regression would
// break — the value written, the handler that ran, the error named — not merely that something moved.

// groupedHelpers: an event the shim's document can dispatch, a working classList for the one element
// that needs it (the shim's is a no-op), and console.error captured as "message@directive".
const groupedHelpers = `
var kit = window.kit;
function evt(type, target, extra) {
  var e = { type: type, target: target, defaultPrevented: false,
    preventDefault: function () { this.defaultPrevented = true; }, stopPropagation: function () {} };
  if (extra) for (var k in extra) e[k] = extra[k];
  return e;
}
function realClassList(node) {
  var set = {};
  node.classList = {
    add: function (c) { set[c] = true; }, remove: function (c) { delete set[c]; },
    contains: function (c) { return !!set[c]; },
    toggle: function (c, force) { var on = force === undefined ? !set[c] : !!force; if (on) set[c] = true; else delete set[c]; return on; }
  };
  return set;
}
var reported = [];
var originalError = console.error;
console.error = function (error, directive) { reported.push(String(error && error.message || error) + "@" + directive); };
function check(ok, message) { if (!ok) { console.error = originalError; throw new Error(message); } }
`

// bind: text is text, never HTML; show is the hidden property; aria-* is spelled "true"/"false"; any
// other name goes through writeBinding. The hand-written data-kit-text beside it is the control that
// both spellings paint in the same pass.
func TestGroupedBindWritesTextShowAndAria(t *testing.T) {
	const assertions = groupedHelpers + `
var host = el("section", { "data-kitwork-scope": "open: false; count: 2; label: '<b>hi</b>'" });
var text = el("span", { "data-kitwork-bind": "text: label" });
var panel = el("div", { "data-kitwork-bind": "show: open; aria-expanded: open; title: 'n=' + count" });
var legacy = el("b", { "data-kit-text": "count" });
host.appendChild(text); host.appendChild(panel); host.appendChild(legacy);
document.body.appendChild(host);
kit.render();

check(legacy.textContent === "2", "control: the hand-written data-kit-text did not paint, got " + JSON.stringify(legacy.textContent));
check(text.textContent === "<b>hi</b>", "text: must be the literal text, got " + JSON.stringify(text.textContent));
check(panel.hidden === true, "show: false must hide the element");
check(panel.getAttribute("aria-expanded") === "false", "aria-expanded must read \"false\", got " + panel.getAttribute("aria-expanded"));
check(panel.getAttribute("title") === "n=2", "title must be written through writeBinding, got " + panel.getAttribute("title"));

kit.scopeFor(host).open = true;
kit.scopeFor(host).count = 3;
kit.render();
check(panel.hidden === false, "show: true must reveal the element");
check(panel.getAttribute("aria-expanded") === "true", "aria-expanded must read \"true\", got " + panel.getAttribute("aria-expanded"));
check(panel.getAttribute("title") === "n=3", "title must follow count, got " + panel.getAttribute("title"));
check(legacy.textContent === "3", "control: data-kit-text must follow count, got " + legacy.textContent);
check(reported.length === 0, "nothing should be reported: " + reported.join(" | "));
console.error = originalError;
console.log("grouped bind: text (literal), show, aria-*, attribute — beside data-kit-text");
`
	runNodeDOMScript(t, "grouped-bind.test.js", assertions)
}

// class: data-kitwork-class="name: condition; …" toggles each named class — the exact name, a dot
// in it kept (py-1.5), the author's static class never touched — truthy on, falsy off. A pair that
// does not compile is reported naming data-kitwork-class and the pair, while the pairs beside it on
// the same element still toggle (the control). The hand-written data-kit-class expression is
// unchanged (the control that the class pass ran beside it). class.<name> is no longer a bind name:
// such a pair in data-kitwork-bind is an attribute write like any other name — the attribute
// appearing is the control that the pair WAS read — and never touches a class.
func TestGroupedClassTogglesEachNamedClass(t *testing.T) {
	const assertions = groupedHelpers + `
var host = el("section", { "data-kitwork-scope": "open: false; value: 40; mode: 'light'" });
var row = el("div", { "data-kitwork-class": "done: value == 100; icon-sun: mode == 'dark'; py-1.5: open; idle: !open" });
var classes = realClassList(row);
classes.card = true; // what the author wrote in class="card"
var broken = el("div", { "data-kitwork-class": "bad: 1 +; good: open" });
var brokenClasses = realClassList(broken);
var legacy = el("p", { "data-kit-class": "{ ring: open }" });
var legacyClasses = realClassList(legacy);
var oldBind = el("div", { "data-kitwork-bind": "class.active: open" });
var oldClasses = realClassList(oldBind);
host.appendChild(row); host.appendChild(broken); host.appendChild(legacy); host.appendChild(oldBind);
document.body.appendChild(host);
kit.render();

check(!classes.done && !classes["icon-sun"] && !classes["py-1.5"], "falsy conditions must leave their classes off, got " + JSON.stringify(classes));
check(classes.idle === true, "a truthy condition must add its class at the first paint, got " + JSON.stringify(classes));
check(classes.card === true, "the author's static class must not be touched");
check(!legacyClasses.ring, "control: data-kit-class must leave ring off while open is false");

var s = kit.scopeFor(host);
s.value = 100; s.mode = "dark"; s.open = true;
kit.render();
check(classes.done === true, "done: value == 100 must add done, got " + JSON.stringify(classes));
check(classes["icon-sun"] === true, "icon-sun: mode == 'dark' must add icon-sun, got " + JSON.stringify(classes));
check(classes["py-1.5"] === true, "a class name with a dot must be toggled whole, got " + JSON.stringify(classes));
check(!classes.idle, "idle: !open must remove idle, got " + JSON.stringify(classes));
check(classes.card === true, "the author's static class must survive the toggles");
check(legacyClasses.ring === true, "control: the hand-written data-kit-class must still add ring");
check(brokenClasses.good === true && !brokenClasses.bad, "the pair beside one that does not compile must still toggle, got " + JSON.stringify(brokenClasses));
var named = reported.filter(function (r) { return /@data-kitwork-class bad$/.test(r); });
check(named.length === 1 && reported.length === 1, "the pair that does not compile must be reported once, naming data-kitwork-class bad, got " + JSON.stringify(reported));

check(!oldClasses.active, "a class.<name> pair in data-kitwork-bind must not toggle a class any more, got " + JSON.stringify(oldClasses));
check(oldBind.getAttribute("class.active") === "", "control: the class.<name> bind pair must be read, as an attribute write, got " + JSON.stringify(oldBind.getAttribute("class.active")));

s.value = 40; s.open = false;
kit.render();
check(!classes.done && !classes["py-1.5"], "falsy again must remove the classes, got " + JSON.stringify(classes));
check(classes.idle === true && classes["icon-sun"] === true, "classes whose condition still holds must stay, got " + JSON.stringify(classes));
check(classes.card === true, "the author's static class must survive removal of the toggled ones");
check(oldBind.getAttribute("class.active") === null, "control: the class.<name> bind pair follows open as an attribute, got " + JSON.stringify(oldBind.getAttribute("class.active")));
console.error = originalError;
console.log("grouped class: each named class toggled (dotted names whole, static class kept), a broken pair named, class.<name> in bind toggles nothing");
`
	runNodeDOMScript(t, "grouped-class.test.js", assertions)
}

// value / checked on a field are two-way when the value is a bare name: the server-rendered value
// seeds a key nobody owns, typing writes back, state writes the field. A value that is an expression
// is one-way — typing into it leaves state alone — which is the control.
func TestGroupedBindValueAndCheckedAreTwoWay(t *testing.T) {
	const assertions = groupedHelpers + `
var form = el("form", { "data-kitwork-scope": "query: 'start'; done: true; other: 'x'" });
var input = el("input", { "data-kitwork-bind": "value: query" });
var check1 = el("input", { type: "checkbox", "data-kitwork-bind": "checked: done" });
var oneWay = el("input", { "data-kitwork-bind": "value: other + '!'" });
var seeded = el("input", { "data-kitwork-bind": "value: fresh" });
seeded.value = "from the server";
form.appendChild(input); form.appendChild(check1); form.appendChild(oneWay); form.appendChild(seeded);
document.body.appendChild(form);
kit.render();
var s = kit.scopeFor(form);

check(input.value === "start", "value: query must write the field, got " + JSON.stringify(input.value));
check(check1.checked === true, "checked: done must check the box");
check(oneWay.value === "x!", "a value expression must still write the field one way, got " + JSON.stringify(oneWay.value));
check(s.fresh === "from the server", "a key nobody owns must be seeded from the field's own value, got " + JSON.stringify(s.fresh));

input.value = "typed";
document.dispatchEvent(evt("input", input));
check(s.query === "typed", "typing must write back into query, got " + JSON.stringify(s.query));
check1.checked = false;
document.dispatchEvent(evt("input", check1));
check(s.done === false, "unchecking must write false into done, got " + JSON.stringify(s.done));

oneWay.value = "mine";
document.dispatchEvent(evt("input", oneWay));
check(s.other === "x", "control: a one-way field must not write back, other = " + JSON.stringify(s.other));

s.query = "from state";
s.done = true;
kit.render();
check(input.value === "from state", "state must write the field, got " + JSON.stringify(input.value));
check(check1.checked === true, "state must check the box again");
console.error = originalError;
console.log("grouped bind: value/checked two-way on a bare name, seeded, one-way on an expression");
`
	runNodeDOMScript(t, "grouped-model.test.js", assertions)
}

// style: each pair a CSS property written through writeStyle — custom properties allowed, a value
// that could fetch (url()) refused and reported under the grouped attribute's name, while the safe
// pair beside it on the same element still writes (the control); null restores what the author wrote.
func TestGroupedStyleWritesSafelyAndRestores(t *testing.T) {
	const assertions = groupedHelpers + `
var host = el("section", { "data-kitwork-scope": "size: 40; tone: 'red'; image: 'url(https://example.test/x.png)'" });
var bar = el("div", { "data-kitwork-style": "width: size + '%'; --progress: size; color: tone" });
bar.style.setProperty("color", "blue", "");
var evil = el("div", { "data-kitwork-style": "background-image: image; height: '3px'" });
host.appendChild(bar); host.appendChild(evil);
document.body.appendChild(host);
kit.render();

check(bar.style.getPropertyValue("width") === "40%", "width not written: " + bar.style.getPropertyValue("width"));
check(bar.style.getPropertyValue("--progress") === "40", "a custom property must be written: " + bar.style.getPropertyValue("--progress"));
check(bar.style.getPropertyValue("color") === "red", "color not written: " + bar.style.getPropertyValue("color"));
check(evil.style.getPropertyValue("background-image") === "", "url() must be refused, got " + evil.style.getPropertyValue("background-image"));
check(evil.style.getPropertyValue("height") === "3px", "control: the safe pair beside it must still write, got " + evil.style.getPropertyValue("height"));
var named = reported.filter(function (r) { return r === "hydrate: unsafe value for data-kitwork-style background-image@data-kitwork-style background-image"; });
check(named.length === 1, "the refusal must be reported naming data-kitwork-style background-image, got " + JSON.stringify(reported));

kit.scopeFor(host).tone = null;
kit.render();
check(bar.style.getPropertyValue("color") === "blue", "null must restore the authored color, got " + bar.style.getPropertyValue("color"));
console.error = originalError;
console.log("grouped style: properties + custom property, url() refused and named, null restores");
`
	runNodeDOMScript(t, "grouped-style.test.js", assertions)
}

// event: the Kitwork UI names mapped to the DOM's and the modifiers of the hand-written family.
// click runs on the element and its ancestors; click-outside runs only for a click elsewhere (the
// inside click is the control); keydown.escape filters the key (another key is the control);
// double-click is dblclick; input.debounce(n) coalesces; pointer-enter does not bubble, so a child's
// event is not its ancestor's (a bubbling click on the same child is the control); ready runs once,
// after the first paint; any other name is a custom event.
func TestGroupedEventsMapNamesAndModifiers(t *testing.T) {
	const assertions = groupedHelpers + `
var box = el("div", { "data-kitwork-scope": "n: 0; open: true; esc: 0; q: 0; hits: ''; enters: 0; readies: 0; saved: 0",
  "data-kitwork-event": "click-outside: open = false; keydown.escape: esc++" });
var button = el("button", { "data-kitwork-event": "click: n++; double-click: hits = hits + 'dbl,'" });
var search = el("input", { "data-kitwork-event": "input.debounce(30): q++" });
var hover = el("div", { "data-kitwork-event": "pointer-enter: enters++; click: hits = hits + 'hover,'" });
var inner = el("span", {});
hover.appendChild(inner);
var ready = el("p", { "data-kitwork-event": "ready: readies++; saved: saved++" });
box.appendChild(button); box.appendChild(search); box.appendChild(hover); box.appendChild(ready);
document.body.appendChild(box);
kit.render();
var s = kit.scopeFor(box);
check(s.readies === 1, "ready must run once after the first paint, readies = " + s.readies);

document.dispatchEvent(evt("click", button));
check(s.n === 1, "click must run, n = " + s.n);
check(s.open === true, "control: a click inside must not run click-outside");
document.dispatchEvent(evt("click", document.body));
check(s.open === false, "click-outside must run for a click elsewhere");
check(s.n === 1, "a click elsewhere must not run the button's click, n = " + s.n);

document.dispatchEvent(evt("keydown", button, { key: "a" }));
check(s.esc === 0, "control: another key must not pass keydown.escape");
document.dispatchEvent(evt("keydown", button, { key: "Escape" }));
check(s.esc === 1, "keydown.escape must run on Escape (bubbling up from the button), esc = " + s.esc);

document.dispatchEvent(evt("dblclick", button));
check(s.hits === "dbl,", "double-click must listen for dblclick, hits = " + s.hits);

document.dispatchEvent(evt("pointerenter", inner));
check(s.enters === 0, "pointer-enter must not run for a child's own pointerenter (it does not bubble)");
document.dispatchEvent(evt("click", inner));
check(s.hits === "dbl,hover,", "control: a click on the same child bubbles to the ancestor, hits = " + s.hits);
document.dispatchEvent(evt("pointerenter", hover));
check(s.enters === 1, "pointer-enter must run for the element's own pointerenter, enters = " + s.enters);

document.dispatchEvent(evt("saved", ready));
check(s.saved === 1, "an unknown name must listen for a custom event of that name, saved = " + s.saved);

kit.render();
kit.render();
check(s.readies === 1, "ready must not run again on a later paint, readies = " + s.readies);

document.dispatchEvent(evt("input", search));
document.dispatchEvent(evt("input", search));
document.dispatchEvent(evt("input", search));
check(s.q === 0, "input.debounce(30) must wait, q = " + s.q);
setTimeout(function () {
  check(s.q === 1, "input.debounce(30) must run once after the burst, q = " + s.q);
  check(reported.length === 0, "nothing should be reported: " + reported.join(" | "));
  console.error = originalError;
  console.log("grouped events: click, click-outside, keydown.escape, double-click, pointer-enter (no bubble), custom, ready, debounce");
}, 90);
`
	runNodeDOMScript(t, "grouped-events.test.js", assertions)
}

// component: data-kitwork-component="name@version: $alias" mounts the registered blueprint, the alias
// reaches it from outside its subtree, and data-kitwork-scope sets its parameters over the
// blueprint's defaults, each value run in the scope AROUND the host. The hand-written host with its
// own data-kit-alias is the control. A name nothing registered is reported under the grouped
// attribute, and a value that does not read is reported once.
func TestGroupedComponentAliasAndScope(t *testing.T) {
	const assertions = groupedHelpers + `
kit.component("counter", { count: 0, step: 1, inc: function () { this.count += this.step; } });
var page = el("section", { "data-kitwork-scope": "limit: 5" });
var host = el("div", { "data-kitwork-component": "counter@1.0.0: $counter", "data-kitwork-scope": "step: limit; count: 10" });
var label = el("span", { "data-kitwork-bind": "text: count" });
var plus = el("button", { "data-kitwork-event": "click: inc()" });
host.appendChild(label); host.appendChild(plus);
var outside = el("button", { "data-kitwork-event": "click: $counter.inc()" });
var outLabel = el("b", { "data-kitwork-bind": "text: $counter.count" });
page.appendChild(host); page.appendChild(outside); page.appendChild(outLabel);
var legacyHost = el("div", { "data-kit-component": "counter", "data-kit-alias": "$old", "data-kit-scope": "step: 2" });
var legacyLabel = el("i", { "data-kit-text": "$old.step" });
var missing = el("div", { "data-kitwork-component": "nowhere" });
var broken = el("div", { "data-kitwork-component": "counter: counter" });
document.body.appendChild(page); document.body.appendChild(legacyHost); document.body.appendChild(legacyLabel);
document.body.appendChild(missing); document.body.appendChild(broken);
kit.render();
kit.render();

check(legacyLabel.textContent === "2", "control: the hand-written host and its data-kit-alias, got " + legacyLabel.textContent);
check(label.textContent === "10", "data-kitwork-scope must override the blueprint's count, got " + label.textContent);
check(kit.scopeFor(host).step === 5, "step: limit must be run in the scope around the host, step = " + kit.scopeFor(host).step);
document.dispatchEvent({ type: "click", target: plus, preventDefault: function () {}, stopPropagation: function () {} });
check(label.textContent === "15", "inc() must run the blueprint's method in the host's scope, got " + label.textContent);
document.dispatchEvent({ type: "click", target: outside, preventDefault: function () {}, stopPropagation: function () {} });
check(label.textContent === "20" && outLabel.textContent === "20", "$counter must reach the host from outside its subtree, got " + label.textContent + " / " + outLabel.textContent);
var counters = kit.components.counter;
check(Array.isArray(counters) && counters.length === 2, "both hosts must be active under the versionless name, got " + JSON.stringify(counters && counters.length));

var missed = reported.filter(function (r) { return r === 'hydrate: no component registered as "nowhere"@data-kitwork-component'; });
check(missed.length === 1, "a missing component must be reported once, naming data-kitwork-component: " + JSON.stringify(reported));
var unread = reported.filter(function (r) { return r.indexOf("counter: counter") >= 0 && /@data-kitwork-component$/.test(r); });
check(unread.length === 1, "an alias without $ must be reported once, naming the attribute: " + JSON.stringify(reported));
check(reported.length === 2, "nothing else should be reported: " + JSON.stringify(reported));
console.error = originalError;
console.log("grouped component: name@version: $alias, scope over defaults in the parent scope, missing and unreadable reported");
`
	runNodeDOMScript(t, "grouped-component.test.js", assertions)
}

// if mounts and unmounts; for materialises rows keyed by "; key: …", and a keyed row MOVES when the
// list reorders (the same node, new place). Rows carry the kernel's own data-kit-item / data-kit-key
// and never the data-kitwork-for they were made from (or they would be collected again).
func TestGroupedIfAndForWithKey(t *testing.T) {
	const assertions = groupedHelpers + `
var host = el("section", { "data-kitwork-scope": "open: false; rows: [{ id: 'a', name: 'A' }, { id: 'b', name: 'B' }]" });
var branch = el("div", { "data-kitwork-if": "open" });
branch.appendChild(el("span", { "data-probe": "if" }));
var list = el("ul", {});
var row = el("li", { "data-kitwork-for": "row, i of rows; key: row.id", "data-kitwork-bind": "text: i + ':' + row.name" });
list.appendChild(row);
host.appendChild(branch); host.appendChild(list);
document.body.appendChild(host);
kit.render();

check(document.querySelectorAll("[data-probe]").length === 0, "if: a false condition must leave the branch out");
kit.scopeFor(host).open = true;
kit.render();
var probes = document.querySelectorAll("[data-probe]");
check(probes.length === 1, "if: a true condition must mount the branch, got " + probes.length);
check(!probes[0].parentNode.hasAttribute("data-kitwork-if"), "if: the mounted copy must not carry data-kitwork-if");

function rows() { return list.childNodes.filter(function (n) { return n.nodeType === 1; }); }
var r = rows();
check(r.length === 2, "for: two rows expected, got " + r.length);
check(r[0].getAttribute("data-kit-key") === "a" && r[1].getAttribute("data-kit-key") === "b", "for: rows must be keyed by row.id, got " + r.map(function (n) { return n.getAttribute("data-kit-key"); }));
check(r[0].textContent === "0:A" && r[1].textContent === "1:B", "for: row content, got " + r.map(function (n) { return n.textContent; }));
check(!r[0].hasAttribute("data-kitwork-for"), "for: a row must not carry data-kitwork-for");
var nodeB = r[1];
kit.scopeFor(host).rows = [{ id: "b", name: "B" }, { id: "a", name: "A" }];
kit.render();
r = rows();
check(r.length === 2 && r[0] === nodeB, "for: the keyed row must move, not be rebuilt");
check(r[0].textContent === "0:B", "for: a moved row must take its new index, got " + r[0].textContent);

kit.scopeFor(host).open = false;
kit.render();
check(document.querySelectorAll("[data-probe]").length === 0, "if: a false condition must unmount the branch again");
check(reported.length === 0, "nothing should be reported: " + reported.join(" | "));
console.error = originalError;
console.log("grouped if/for: mount, unmount, keyed rows move");
`
	runNodeDOMScript(t, "grouped-structure.test.js", assertions)
}

// The grouped readers are cached by VALUE, not by element: when a Drive swap rewrites an attribute on
// an element it keeps (morph patches attributes in place), the next paint and the next event read the
// new value. The first read is the control that the attribute is read at all.
func TestGroupedValueCacheFollowsAChangedAttribute(t *testing.T) {
	const assertions = groupedHelpers + `
var host = el("section", { "data-kitwork-scope": "a: 'first'; b: 'second'; n: 0; m: 0" });
var span = el("span", { "data-kitwork-bind": "text: a" });
var button = el("button", { "data-kitwork-event": "click: n++" });
var bar = el("div", { "data-kitwork-style": "width: '10px'" });
var badge = el("i", { "data-kitwork-class": "first: true" });
var badgeClasses = realClassList(badge);
host.appendChild(span); host.appendChild(button); host.appendChild(bar); host.appendChild(badge);
document.body.appendChild(host);
kit.render();
var s = kit.scopeFor(host);
check(span.textContent === "first", "control: the bind was not read at all, got " + span.textContent);
check(badgeClasses.first === true && !badgeClasses.second, "control: the class was not read at all, got " + JSON.stringify(badgeClasses));
document.dispatchEvent(evt("click", button));
check(s.n === 1 && s.m === 0, "control: the first handler, n = " + s.n + ", m = " + s.m);

span.setAttribute("data-kitwork-bind", "text: b");
button.setAttribute("data-kitwork-event", "click: m++");
bar.setAttribute("data-kitwork-style", "width: '20px'");
badge.setAttribute("data-kitwork-class", "second: true");
kit.render();
check(span.textContent === "second", "a rewritten data-kitwork-bind must be read again, got " + span.textContent);
check(bar.style.getPropertyValue("width") === "20px", "a rewritten data-kitwork-style must be read again, got " + bar.style.getPropertyValue("width"));
check(badgeClasses.second === true, "a rewritten data-kitwork-class must be read again, got " + JSON.stringify(badgeClasses));
document.dispatchEvent(evt("click", button));
check(s.m === 1 && s.n === 1, "a rewritten data-kitwork-event must run the new handler only, n = " + s.n + ", m = " + s.m);
console.error = originalError;
console.log("grouped cache: keyed by value — a rewritten bind, class, style and event are read afresh");
`
	runNodeDOMScript(t, "grouped-cache.test.js", assertions)
}

// morph keeps an element across a swap only when it is the same component: a host whose
// data-kitwork-component (or data-kitwork-scope) changed is a different element and is replaced;
// the same grouped component on both sides is kept (the control).
func TestMorphTreatsGroupedHostsAsIdentity(t *testing.T) {
	const assertions = groupedHelpers + `
function page(component, scopeLiteral) {
  var root = el("main", {});
  var attrs = { "data-kitwork-component": component };
  if (scopeLiteral) attrs["data-kitwork-scope"] = scopeLiteral;
  root.appendChild(el("div", attrs));
  return root;
}
var same = page("tabs: $tabs");
var kept = same.childNodes[0];
kit.morph(same, page("tabs: $tabs"));
check(same.childNodes[0] === kept, "control: the same grouped component must be kept across the swap");

var other = page("tabs: $tabs");
var was = other.childNodes[0];
kit.morph(other, page("toast: $note"));
check(other.childNodes[0] !== was, "a different data-kitwork-component must replace the element, not patch it");
check(other.childNodes[0].getAttribute("data-kitwork-component") === "toast: $note", "the new host must arrive, got " + other.childNodes[0].getAttribute("data-kitwork-component"));

var scoped = page("tabs", "index: 0");
var before = scoped.childNodes[0];
kit.morph(scoped, page("tabs", "index: 2"));
check(scoped.childNodes[0] !== before, "a different data-kitwork-scope must replace the element");
console.error = originalError;
console.log("morph: grouped component and scope are identity");
`
	runNodeDOMScript(t, "grouped-morph.test.js", assertions)
}

// intersect-enter / intersect-leave in data-kitwork-event: the intersect file (jit/js/directives)
// watches each pair and dispatches it under the pair's own name on the element, which the kernel
// listens for. A fake IntersectionObserver reports in and out; the enter handler runs only on the way
// in and the leave handler only on the way out (each the other's control), and .once stops after one.
// A modifier's argument may hold a dot (margin(-1.5rem)): the name is cut at the dots outside the
// parentheses, as the server's splitEventName cuts it, so the observer gets the whole margin and the
// kernel's handler stays valid — cut at every dot, it would read "margin(-1" and "5rem)".
func TestGroupedIntersectEnterAndLeave(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "js", "directives", "intersect.js"))
	if err != nil {
		t.Fatal(err)
	}
	assertions := groupedHelpers + `
var observers = [];
window.IntersectionObserver = function (callback, options) {
  this.callback = callback; this.options = options; this.disconnected = false;
  this.observe = function (target) { this.target = target; };
  this.disconnect = function () { this.disconnected = true; };
  observers.push(this);
};
global.CustomEvent = function (type, init) { this.type = type; this.bubbles = !!(init && init.bubbles); };
var host = el("section", { "data-kitwork-scope": "inside: 0; outside: 0; first: 0; near: 0",
  "data-kitwork-event": "intersect-enter: inside++; intersect-leave: outside++" });
var once = el("div", { "data-kitwork-event": "intersect-enter.once: first++" });
var margin = el("div", { "data-kitwork-event": "intersect-enter.margin(-1.5rem): near++" });
host.appendChild(once);
host.appendChild(margin);
document.body.appendChild(host);
// an element's dispatchEvent reaches the document's delegated listeners, as a bubbling event does
[host, once, margin].forEach(function (node) {
  node.dispatchEvent = function (e) { e.target = node; e.preventDefault = function () {}; e.stopPropagation = function () {}; document.dispatchEvent(e); };
});
` + string(source) + `
// The shim's kernel booted before these elements existed, so the intersect file's start hook ran at
// once (scanning them); the paint below is the one a page gets at boot, where the kernel learns to
// listen for each pair's name.
kit.render();
var s = kit.scopeFor(host);
check(observers.length === 4, "one observer per intersect pair, got " + observers.length);
function report(observer, inside) { observer.callback([{ isIntersecting: inside, intersectionRatio: inside ? 1 : 0 }]); }
var enter = observers[0], leave = observers[1], onceObserver = observers[2], marginObserver = observers[3];
check(marginObserver.options.rootMargin === "-1.5rem", "the observer must get the whole margin, got " + JSON.stringify(marginObserver.options.rootMargin));
report(marginObserver, true);
check(s.near === 1, "intersect-enter.margin(-1.5rem) must run on the way in, near = " + s.near);
check(s.inside === 0, "a child's margin pair must not answer the ancestor's, inside = " + s.inside);
report(enter, true); report(leave, true);
check(s.inside === 1 && s.outside === 0, "on the way in only intersect-enter runs, inside = " + s.inside + ", outside = " + s.outside);
report(enter, false); report(leave, false);
check(s.inside === 1 && s.outside === 1, "on the way out only intersect-leave runs, inside = " + s.inside + ", outside = " + s.outside);
report(onceObserver, true);
check(s.first === 1 && onceObserver.disconnected, "intersect-enter.once must run and stop watching, first = " + s.first);
check(s.inside === 1, "a child's intersect must not answer the ancestor's, inside = " + s.inside);
check(reported.length === 0, "nothing should be reported: " + reported.join(" | "));
console.error = originalError;
console.log("grouped intersect: enter on the way in, leave on the way out, once stops");
`
	runNodeDOMScript(t, "grouped-intersect.test.js", assertions)
}
