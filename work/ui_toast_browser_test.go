package work

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// toastBrowserFiles is a site with one toast in its overlay, drawn as an author would (an icon for each
// tone, a title, the words, a count of repeats, a button and a link, Dismiss), a page of buttons that
// say each kind of notice through $toast, a tabs a notice's action chooses in, and a second page Drive
// moves to (router.reload(false)).
var toastBrowserFiles = map[string]string{
	"localhost/router.work": `import { router } from "kitwork";

router.colors({ brand: "#c8742c" });
router.language("vi");
router.reload(false);
`,
	"localhost/page.ui": `<page><content /><overlay /></page>`,
	"localhost/overlay.ui": `<overlay>
  <box component="toast" as="$toast" gap="2">
    <card for="notice of notices; key: notice.id" flex="row" gap="3" padding="3" shadow="large">
      <icon show="notice.loading" value="loader-2" />
      <icon show="notice.tone == 'info' && !notice.loading" value="info-circle" />
      <icon show="notice.tone == 'success'" value="circle-check" />
      <icon show="notice.tone == 'warning'" value="alert-triangle" />
      <icon show="notice.tone == 'danger'" value="alert-circle" />
      <box gap="1" width="full">
        <text show="notice.title" weight="bold">{{ notice.title }}</text>
        <text>{{ notice.message }}</text>
        <text show="notice.count > 1">{{ '×' + notice.count }}</text>
        <button show="notice.action.click" click="act(notice.id)">{{ notice.action.label }}</button>
        <link show="notice.action.link" value="{{ notice.action.link }}">{{ notice.action.label }}</link>
      </box>
      <button click="dismiss(notice.id)"><icon value="x" alt="Đóng thông báo" /></button>
    </card>
    <text show="waiting > 0">{{ 'Còn ' + waiting + ' thông báo' }}</text>
  </box>
</overlay>`,
	"localhost/content.ui": `<content padding="4" gap="2">
  <button id="before">Trước</button>
  <button id="success" click="$toast.success('Đã lưu.')">success</button>
  <button id="info" click="$toast.info('Có bản mới.')">info</button>
  <button id="warning" click="$toast.warning('Sắp hết phiên.')">warning</button>
  <button id="danger" click="$toast.danger('Không lưu được.')">danger</button>
  <button id="titled" click="$toast.show('Đã xoá ghi chú.', { title: 'Ghi chú', action: { label: 'Hoàn tác', click: () => $files.select('b') } })">titled</button>
  <button id="linked" click="$toast.show('Đã thêm vào giỏ.', { action: { label: 'Xem giỏ', link: '/gio-hang' } })">linked</button>
  <button id="loading" click="$toast.loading('Đang lưu…', { id: 'save' })">loading</button>
  <button id="done" click="$toast.update('save', 'Đã lưu.', { tone: 'success' })">done</button>
  <button id="stay" click="$toast.info('Ở lại tới khi đóng.', { duration: 0 })">stay</button>
  <button id="short" click="$toast.info('Hai giây.', { duration: '2s' })">short</button>
  <box component="tabs" as="$files" alt="Tệp" flex="row" gap="1">
    <button click="select('a')">a</button>
    <button click="select('b')">b</button>
  </box>
  <box id="panel-a" show="$files.selected == 'a'"><text>a</text></box>
  <box id="panel-b" show="$files.selected == 'b'"><text>b</text></box>
  <link id="next" value="/tiep">Tiếp</link>
</content>`,
	"localhost/tiep/content.ui": `<content padding="4" gap="2">
  <text id="arrived">Tiếp</text>
  <button id="again" click="$toast.info('Ở trang mới.')">again</button>
</content>`,
}

// toastProbe is what each step reads the toast with, installed once a page has loaded: each notice on
// screen as key|icons shown|title|words|count|button|link>href|leaving, how many wait, whether time
// stands still, what the two regions hold now and every line they were given (window.__heard: what a
// screen reader is told, last), the delays of the timers a call started (window.__delays:
// every setTimeout of a second or more, logged as it is asked for), and two frames.
const toastProbe = `(function () {
  if (window.__toast) return "ready";
  var host = document.querySelector('[data-kitwork-component^="toast"]');
  var start = window.setTimeout;
  window.__delays = [];
  window.setTimeout = function (fn, ms) { if (ms >= 1000) window.__delays.push(ms); return start.apply(window, arguments); };
  window.__warned = [];
  var warn = console.warn;
  console.warn = function () { window.__warned.push(Array.prototype.join.call(arguments, " ")); return warn.apply(console, arguments); };
  var shown = function (el) { return !!el && !el.hidden; };
  var rows = function () { return Array.prototype.filter.call(host.children, function (c) { return c.hasAttribute("data-kit-key"); }); };
  var describe = function (row) {
    var icons = Array.prototype.filter.call(row.querySelectorAll(":scope > i"), shown).map(function (i) { return (i.className.match(/icon-([a-z0-9-]+)/) || [])[1]; });
    var p = row.querySelectorAll("p"), act = row.querySelector('button[data-kitwork-event^="click: act"]'), link = row.querySelector("a");
    return [row.getAttribute("data-kit-key"), icons.join("+"), shown(p[0]) ? p[0].textContent : "", p[1].textContent, shown(p[2]) ? p[2].textContent : "",
      shown(act) ? act.textContent : "", shown(link) ? link.textContent + ">" + link.getAttribute("href") : "", row.hasAttribute("data-leaving") ? "leaving" : ""].join("|");
  };
  var said = function (role) {
    var region = host.querySelector(':scope > .reader[role="' + role + '"]');
    return Array.prototype.map.call(region.children, function (line) { return line.textContent; }).join(" / ");
  };
  // heard: every line a region was given, as it was given (a region says what is added to it)
  window.__heard = [];
  Array.prototype.forEach.call(host.querySelectorAll(":scope > .reader"), function (region) {
    new MutationObserver(function (records) {
      records.forEach(function (record) {
        Array.prototype.forEach.call(record.addedNodes, function (line) { window.__heard.push([region.getAttribute("role"), line.textContent]); });
      });
    }).observe(region, { childList: true });
  });
  window.__toast = {
    host: host, rows: rows,
    state: function (still) {
      var scope = kit.scopeFor(host);
      var waitingText = Array.prototype.filter.call(host.children, function (c) { return c.tagName === "P"; })[0];
      var drawn = rows().filter(function (row) { return !still || !row.hasAttribute("data-leaving"); });
      return JSON.stringify({ rows: drawn.map(describe), waiting: scope.waiting, paused: scope.paused,
        told: shown(waitingText) ? waitingText.textContent : "" });
    },
    said: function () { return "status: " + said("status") + " | alert: " + said("alert"); },
    last: function (role, n) {
      return window.__heard.filter(function (line) { return line[0] === role; }).map(function (line) { return line[1]; }).slice(-n).join(" / ");
    },
    delays: function (fn) { window.__delays = []; fn(); return window.__delays.join(","); },
    // painted: the kernel's paint for the last change has run (it is a microtask queued before this one)
    painted: function () { return new Promise(function (done) { queueMicrotask(done); }); },
    frames: function () { return new Promise(function (done) { requestAnimationFrame(function () { requestAnimationFrame(done); }); }); },
    wait: function (ms) { return new Promise(function (done) { start(done, ms); }); },
    click: function (id) { document.getElementById(id).click(); },
    // focus: where the keyboard is, as key:name inside a notice (its words, else its icon's name), the
    // element's id or tag outside one
    focus: function () {
      var at = document.activeElement, row = at && at.closest ? at.closest("[data-kit-key]") : null;
      if (!row || row.parentElement !== host) return at ? at.id || at.tagName : "none";
      var icon = at.querySelector("i[aria-label]");
      return row.getAttribute("data-kit-key") + ":" + (at.textContent.trim() || (icon ? icon.getAttribute("aria-label") : at.tagName));
    },
    clear: function () { kit.scope.$toast.clear(); return new Promise(function (done) { start(done, 250); }); }
  };
  return "installed";
})()`

// toastStep runs one step of the proof after the probe: body is the inside of an async function, which
// returns what the step reads.
func toastStep(d *devtools, body string) string {
	d.t.Helper()
	d.evaluate(toastProbe)
	return d.evaluate(`(async function () { var T = window.__toast; ` + body + ` })()`)
}

// toastKey presses one key the way a person does: down, then up.
func toastKey(d *devtools, key string, code int) {
	for _, kind := range []string{"rawKeyDown", "keyUp"} {
		d.call("Input.dispatchKeyEvent", map[string]any{"type": kind, "key": key, "code": key, "windowsVirtualKeyCode": code})
	}
}

// toastEnter presses Enter on the button that has the focus, as a person does: down with its
// character (what makes a button act), then up.
func toastEnter(d *devtools) {
	d.call("Input.dispatchKeyEvent", map[string]any{"type": "keyDown", "key": "Enter", "code": "Enter", "windowsVirtualKeyCode": 13, "text": "\r"})
	d.call("Input.dispatchKeyEvent", map[string]any{"type": "keyUp", "key": "Enter", "code": "Enter", "windowsVirtualKeyCode": 13})
}

// toastRegionsHeard is how the browser's accessibility tree, what a screen reader is given, has the two
// regions of the stack: "<role> live=<politeness> atomic=<whether a change says the whole region>".
func toastRegionsHeard(d *devtools) string {
	d.t.Helper()
	d.call("Accessibility.enable", map[string]any{})
	var heard []string
	for _, role := range []string{"status", "alert"} {
		var region struct {
			Result struct {
				ObjectID string `json:"objectId"`
			} `json:"result"`
		}
		if err := json.Unmarshal(d.call("Runtime.evaluate", map[string]any{"expression": `document.querySelector('[data-kitwork-component^="toast"] > .reader[role="` + role + `"]')`}), &region); err != nil || region.Result.ObjectID == "" {
			d.t.Fatalf("no %s region: %v", role, err)
		}
		var tree struct {
			Nodes []struct {
				Properties []struct {
					Name  string `json:"name"`
					Value struct {
						Value any `json:"value"`
					} `json:"value"`
				} `json:"properties"`
			} `json:"nodes"`
		}
		if err := json.Unmarshal(d.call("Accessibility.getPartialAXTree", map[string]any{"objectId": region.Result.ObjectID, "fetchRelatives": false}), &tree); err != nil || len(tree.Nodes) == 0 {
			d.t.Fatalf("the %s region is not in the accessibility tree: %v", role, err)
		}
		said := map[string]string{}
		for _, property := range tree.Nodes[0].Properties {
			said[property.Name] = fmt.Sprint(property.Value.Value)
		}
		heard = append(heard, role+" live="+said["live"]+" atomic="+said["atomic"])
	}
	return strings.Join(heard, "; ")
}

// TestBrowserToast proves the toast on a page the engine serves, in a real browser, step by step:
//   - the server paints no notice and the blueprint is gone once the kernel starts; the stack is a
//     fixed region at the bottom end of the screen, named for a screen reader;
//   - each tone takes its time (success 4s, info 5s, warning 8s; danger and a notice that is loading
//     stay: no timer), is said in its words in the polite region, danger in the assertive one, a
//     warning and an error with what they are;
//   - a fourth notice past limit (3) sends the oldest to wait, told by waiting, and a notice that leaves
//     brings it back; the same words said again are no second notice: the first counts ×2, becomes the
//     newest (nearest the edge) and keeps its node;
//   - loading then update is one notice, its node kept, its words and icon changed, its time started;
//     an update of a notice closed creates nothing;
//   - a title and an action: at least 8s, said with the key that reaches it; its button runs the action
//     where it was written ($files.select('b') chooses tab b) and closes the notice; a link goes where
//     it says, closes its notice when followed, and an address that is no page (javascript:) is dropped;
//   - time stands still while the pointer is on a notice and while the page is hidden, and goes on
//     with what was left (2s at least);
//   - F8 brings the focus to the stack and holds its time, Tab reaches a notice, Escape closes it and
//     hands the focus to the next notice, which keeps it once the notice closed has gone (the kernel
//     moves the row after it then), then back where it was; a notice never takes the focus;
//   - a finger dragged far across a notice closes it, a little and slowly does not; with less motion
//     asked for, a notice goes at once (no fade);
//   - Drive: the stack in overlay.ui stays across a move (the same node), its plain notice too; a
//     notice whose action belonged to the page left closes; the new page says a notice through $toast;
//   - with less motion, Escape still hands the focus on; Enter on the action of the first of two
//     notices runs it, and the focus goes to the next and stays; all closed with nowhere known to go
//     back to, the focus goes to the page, never back into a notice fading out;
//   - each region says what is added to it, alone (aria-atomic false in the accessibility tree), holds
//     only the newest line and drops it soon: a reader that reaches the stack hears each notice once;
//   - F8 while a modal dialog holds the page is the browser's (the time does not stand still); a
//     mouse that closes a notice and stays still lets the others' time go on (no pointerout comes);
//   - an id the author gives is a name: a number is refused (said in the console, in Vietnamese, like
//     every complaint of the toast's) and the notice numbered, so two notices never share a key.
func TestBrowserToast(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the toast browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant := buildUISite(t, toastBrowserFiles)
	page := serveUI(t, tenant, "http://localhost/").Body.String()
	if !strings.Contains(page, `/_kitwork/components/toast.`) || !strings.Contains(page, `data-kitwork-for="notice of notices; key: notice.id"`) {
		t.Fatalf("the page does not load the toast or draw its blueprint:\n%.3000s", page)
	}
	var serving sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		forward := request.Clone(request.Context())
		forward.Host = "localhost"
		serving.Lock()
		defer serving.Unlock()
		tenant.Serve(response, forward)
	}))
	defer server.Close()

	d := openDevtools(t, browser, server.URL+"/", 1280, 800, "")
	d.loaded(exampleSettle)
	d.call("Emulation.setEmulatedMedia", map[string]any{"features": []map[string]string{{"name": "prefers-reduced-motion", "value": "no-preference"}}})
	check := func(step, got, want string) {
		t.Helper()
		if got != want {
			t.Errorf("%s:\n got  %s\n want %s", step, got, want)
		}
	}

	check("the page as it starts", toastStep(d, `
  var h = T.host, style = getComputedStyle(h);
  return [document.querySelectorAll("[data-kitwork-for]").length, T.rows().length, h.getAttribute("role"), h.getAttribute("aria-label"),
    h.getAttribute("tabindex"), style.position, style.pointerEvents, T.said()].join(" ");`),
		`0 0 region Thông báo (F8) -1 fixed none status:  | alert: `)

	// each tone, its time, in the order the eye sees them; the fourth sends the oldest to wait
	check("four tones", toastStep(d, `
  var times = ["success", "info", "warning", "danger"].map(function (id) { return id + "=" + T.delays(function () { T.click(id); }); });
  await T.frames();
  await T.wait(400);
  return times.join(" ") + " " + T.state() + " " + T.said();`),
		`success=4000 info=5000 warning=8000 danger= {"rows":["2|info-circle||Có bản mới.||||","3|alert-triangle||Sắp hết phiên.||||","4|alert-circle||Không lưu được.||||"],"waiting":1,"paused":false,"told":"Còn 1 thông báo"} `+
			`status: Đã lưu. / Có bản mới. / Cảnh báo: Sắp hết phiên. | alert: Lỗi: Không lưu được.`)
	check("a notice that leaves brings back the one waiting", toastStep(d, `
  T.rows()[2].querySelector('button[data-kitwork-event^="click: dismiss"]').click();
  await T.painted();
  var leaving = T.state();
  await T.wait(250);
  return leaving + " " + T.state();`),
		`{"rows":["1|circle-check||Đã lưu.||||","2|info-circle||Có bản mới.||||","3|alert-triangle||Sắp hết phiên.||||","4|alert-circle||Không lưu được.||||leaving"],"waiting":0,"paused":false,"told":""} `+
			`{"rows":["1|circle-check||Đã lưu.||||","2|info-circle||Có bản mới.||||","3|alert-triangle||Sắp hết phiên.||||"],"waiting":0,"paused":false,"told":""}`)
	check("the same words again", toastStep(d, `
  var node = T.rows()[1];
  var time = T.delays(function () { T.click("info"); });
  await T.frames();
  return time + " " + (T.rows()[2] === node) + " " + T.state();`),
		`5000 true {"rows":["1|circle-check||Đã lưu.||||","3|alert-triangle||Sắp hết phiên.||||","2|info-circle||Có bản mới.|×2|||"],"waiting":0,"paused":false,"told":""}`)

	// loading, then done: one notice
	check("loading then done", toastStep(d, `
  await T.clear();
  var loading = T.delays(function () { T.click("loading"); });
  await T.frames();
  var node = T.rows()[0], before = T.state();
  var done = T.delays(function () { T.click("done"); });
  await T.frames();
  await T.wait(400);
  var after = T.state(), same = T.rows()[0] === node, said = T.last("status", 2);
  kit.scope.$toast.dismiss("save");
  var again = kit.scope.$toast.update("save", "Lại.", { tone: "danger" });
  await T.wait(250);
  return [loading || "none", done, same, before, after, said, again, T.rows().length].join(" ");`),
		`none 4000 true {"rows":["save|loader-2||Đang lưu…||||"],"waiting":0,"paused":false,"told":""} {"rows":["save|circle-check||Đã lưu.||||"],"waiting":0,"paused":false,"told":""} `+
			`Đang lưu… / Đã lưu. false 0`)

	// a title and an action; a link
	check("a title and an action", toastStep(d, `
  var time = T.delays(function () { T.click("titled"); });
  await T.frames();
  await T.wait(400);
  var before = T.state(), said = T.last("status", 1);
  T.rows()[0].querySelector('button[data-kitwork-event^="click: act"]').click();
  await T.frames();
  var chosen = document.getElementById("panel-b").hidden ? "a" : "b";
  await T.wait(250);
  return [time, before, said, chosen, T.rows().length].join(" ");`),
		`8000 {"rows":["5|info-circle|Ghi chú|Đã xoá ghi chú.||Hoàn tác||"],"waiting":0,"paused":false,"told":""} Ghi chú. Đã xoá ghi chú. Nhấn F8 để tới “Hoàn tác”. b 0`)
	check("a link", toastStep(d, `
  T.click("linked");
  var bad = kit.scope.$toast.show("Xấu.", { action: { label: "X", link: "javascript:alert(1)" } });
  await T.frames();
  var before = T.state(), warned = window.__warned.filter(function (w) { return w.indexOf("link") >= 0; }).join(" + ");
  var link = T.rows()[0].querySelector("a");
  link.addEventListener("click", function (event) { event.preventDefault(); }, { once: true });
  link.click();
  await T.wait(250);
  var after = T.state();
  await T.clear();
  return [bad, before, warned, after].join(" ");`),
		`7 {"rows":["6|info-circle||Đã thêm vào giỏ.|||Xem giỏ>/gio-hang|","7|info-circle||Xấu.||||"],"waiting":0,"paused":false,"told":""} `+
			`kitwork toast: link của action chỉ đi tới đường dẫn trong site (/…, ./…, #…), http:, https:, mailto: hoặc tel:, không phải javascript:alert(1) `+
			`{"rows":["7|info-circle||Xấu.||||"],"waiting":0,"paused":false,"told":""}`)

	// time stands still under the pointer and while the page is hidden
	check("paused under the pointer", toastStep(d, `
  var first = T.delays(function () { T.click("short"); });
  await T.frames();
  var row = T.rows()[0];
  row.dispatchEvent(new PointerEvent("pointerover", { bubbles: true, pointerType: "mouse" }));
  var held = T.state();
  await T.wait(2300);
  var still = T.rows().length;
  var resumed = T.delays(function () { row.dispatchEvent(new PointerEvent("pointerout", { bubbles: true, pointerType: "mouse", relatedTarget: document.body })); });
  var going = T.state();
  await T.wait(2800);
  return [first, held, still, resumed, going, T.rows().length].join(" ");`),
		`2000 {"rows":["8|info-circle||Hai giây.||||"],"waiting":0,"paused":true,"told":""} 1 2000 {"rows":["8|info-circle||Hai giây.||||"],"waiting":0,"paused":false,"told":""} 0`)
	check("paused while hidden", toastStep(d, `
  T.click("short");
  await T.frames();
  Object.defineProperty(document, "hidden", { configurable: true, get: function () { return true; } });
  document.dispatchEvent(new Event("visibilitychange"));
  var hidden = kit.scope.$toast.paused;
  delete document.hidden;
  document.dispatchEvent(new Event("visibilitychange"));
  var shown = kit.scope.$toast.paused;
  await T.clear();
  return hidden + " " + shown;`),
		`true false`)

	// the keyboard
	d.evaluate(`document.getElementById("before").focus()`)
	toastKey(d, "F8", 119)
	check("F8 with nothing to reach is the browser's", d.evaluate(`document.activeElement.id`), "before")
	check("a notice never takes the focus", toastStep(d, `
  T.click("stay");
  T.click("titled");
  await T.frames();
  return document.activeElement.id + " " + T.rows().length;`), "before 2")
	toastKey(d, "F8", 119)
	check("F8 reaches the stack", toastStep(d, `return (document.activeElement === T.host) + " " + kit.scope.$toast.paused;`), "true true")
	toastKey(d, "Tab", 9)
	check("Tab reaches a notice", d.evaluate(`document.activeElement.closest("[data-kit-key]").getAttribute("data-kit-key") + " " + document.activeElement.querySelector("i").getAttribute("aria-label")`), "10 Đóng thông báo")
	toastKey(d, "Escape", 27)
	// and keeps it once the notice closed has gone (150ms): the kernel moves the row after it then
	check("Escape closes it and the next notice has the focus", toastStep(d, `
  await T.frames();
  var soon = document.activeElement.textContent;
  await T.wait(300);
  return soon + " " + T.focus() + " " + T.state();`),
		`Hoàn tác 11:Hoàn tác {"rows":["11|info-circle|Ghi chú|Đã xoá ghi chú.||Hoàn tác||"],"waiting":0,"paused":true,"told":""}`)
	toastKey(d, "Escape", 27)
	check("the last closed, the focus goes back", toastStep(d, `await T.wait(250); return document.activeElement.id + " " + T.state();`),
		`before {"rows":[],"waiting":0,"paused":false,"told":""}`)

	// a finger
	check("a swipe", toastStep(d, `
  T.click("stay");
  await T.frames();
  var row = T.rows()[0], box = row.getBoundingClientRect(), y = box.top + box.height / 2;
  var fire = function (type, x) { row.dispatchEvent(new PointerEvent(type, { bubbles: true, pointerType: "touch", pointerId: 7, isPrimary: true, clientX: x, clientY: y })); };
  fire("pointerdown", box.left + 20);
  fire("pointermove", box.left + 40);
  var dragged = row.style.translate + "/" + getComputedStyle(row).translate; // with the finger, at once
  await T.wait(300);
  fire("pointerup", box.left + 40);
  await T.frames();
  var stays = T.rows().length + " " + JSON.stringify(row.style.translate);
  fire("pointerdown", box.left + 20);
  fire("pointermove", box.left + 260);
  fire("pointerup", box.left + 260);
  await T.frames();
  var first = parseFloat(getComputedStyle(row).translate);
  await T.wait(60);
  var then = parseFloat(getComputedStyle(row).translate);
  var going = then > first ? "goes on the way it was swiped" : "goes back (" + first + " then " + then + ")";
  await T.wait(250);
  return [dragged, stays, going, T.rows().length].join(" ");`),
		`20px/20px 1 "" goes on the way it was swiped 0`)

	// less motion: no fade, a notice goes at once
	d.call("Emulation.setEmulatedMedia", map[string]any{"features": []map[string]string{{"name": "prefers-reduced-motion", "value": "reduce"}}})
	check("less motion", toastStep(d, `
  T.click("stay");
  await T.frames();
  kit.scope.$toast.dismiss(T.rows()[0].getAttribute("data-kit-key"));
  await T.frames();
  return String(T.rows().length);`), "0")
	d.call("Emulation.setEmulatedMedia", map[string]any{"features": []map[string]string{{"name": "prefers-reduced-motion", "value": "no-preference"}}})

	// where the stack stands, at a desktop's width and a phone's
	check("bottom end at 1280", toastStep(d, `
  T.click("stay");
  await T.frames();
  await T.wait(500);
  var r = T.rows()[0].getBoundingClientRect();
  return [Math.round(innerWidth - r.right), Math.round(innerHeight - r.bottom), Math.round(r.width)].join(" ");`), "16 16 384")
	emulate(d, 375, 700)
	check("across a phone", toastStep(d, `
  await T.frames();
  var r = T.rows()[0].getBoundingClientRect();
  return [Math.round(r.left), Math.round(innerWidth - r.right), Math.round(innerHeight - r.bottom), Math.max(document.documentElement.scrollWidth, innerWidth) - innerWidth].join(" ");`), "12 12 12 0")
	emulate(d, 1280, 800)

	// Drive: the stack stays, the notice of the page left goes
	check("a move", toastStep(d, `
  T.click("titled");
  await T.frames();
  window.__hostBefore = T.host;
  var arrived = new Promise(function (done) { document.addEventListener("kitwork:load", function () { done(); }, { once: true }); });
  document.getElementById("next").click();
  await arrived;
  await T.wait(250);
  var same = document.querySelector('[data-kitwork-component^="toast"]') === window.__hostBefore;
  var kept = T.state();
  document.getElementById("again").click();
  await T.frames();
  return [!!document.getElementById("arrived"), same, kept, T.state()].join(" ");`),
		`true true {"rows":["14|info-circle||Ở lại tới khi đóng.||||"],"waiting":0,"paused":false,"told":""} `+
			`{"rows":["14|info-circle||Ở lại tới khi đóng.||||","16|info-circle||Ở trang mới.||||"],"waiting":0,"paused":false,"told":""}`)

	// From here on the page is the one Drive moved to (/tiep): notices are said through $toast from a
	// script, as a handler would. Less motion: the notice goes at once, and the focus stays with the next.
	reduce := map[string]any{"features": []map[string]string{{"name": "prefers-reduced-motion", "value": "reduce"}}}
	moving := map[string]any{"features": []map[string]string{{"name": "prefers-reduced-motion", "value": "no-preference"}}}
	d.call("Emulation.setEmulatedMedia", reduce)
	toastStep(d, `await T.clear(); kit.scope.$toast.info("Một.", { duration: 0 }); kit.scope.$toast.info("Hai.", { duration: 0 }); await T.frames(); document.getElementById("again").focus(); return "";`)
	toastKey(d, "F8", 119)
	toastKey(d, "Tab", 9)
	toastKey(d, "Escape", 27)
	check("less motion: Escape hands the focus on", toastStep(d, `
  await T.frames();
  var soon = T.focus();
  await T.wait(300);
  return [soon, T.focus(), T.rows().length, kit.scope.$toast.paused].join(" ");`), "18:Đóng thông báo 18:Đóng thông báo 1 true")
	d.call("Emulation.setEmulatedMedia", moving)

	// the action of the first of two notices, pressed with Enter: the focus goes to the next and stays
	toastStep(d, `await T.clear(); window.__acted = 0; kit.scope.$toast.show("Làm.", { action: { label: "Làm", click: function () { window.__acted++; } } }); kit.scope.$toast.info("Ba.", { duration: 0 }); await T.frames(); document.getElementById("again").focus(); return "";`)
	toastKey(d, "F8", 119)
	toastKey(d, "Tab", 9)
	check("Tab reaches the action", d.evaluate(`window.__toast.focus()`), "19:Làm")
	toastEnter(d)
	check("a keyboard act hands the focus on", toastStep(d, `
  await T.frames();
  var soon = T.focus();
  await T.wait(400);
  return [window.__acted, soon, T.focus(), T.state()].join(" ");`),
		`1 20:Đóng thông báo 20:Đóng thông báo {"rows":["20|info-circle||Ba.||||"],"waiting":0,"paused":true,"told":""}`)
	toastKey(d, "Escape", 27)
	check("and back", toastStep(d, `await T.wait(250); return T.focus() + " " + T.rows().length;`), "again 0")

	// all closed while the keyboard is in the stack, with nowhere known to go back to: the focus goes to
	// the page and stays there, never back into a notice fading out (nor its pause)
	toastStep(d, `kit.scope.$toast.info("Bốn.", { duration: 0 }); kit.scope.$toast.info("Năm.", { duration: 0 }); await T.frames(); document.activeElement.blur(); return "";`)
	toastKey(d, "F8", 119)
	toastKey(d, "Tab", 9)
	check("all closed from the keyboard", toastStep(d, `
  var at = T.focus();
  kit.scope.$toast.clear();
  await T.frames();
  var soon = T.focus() + " " + kit.scope.$toast.paused;
  await T.wait(300);
  return [at, soon, T.focus(), kit.scope.$toast.paused, T.rows().length].join(" ");`), "21:Đóng thông báo BODY false BODY false 0")

	// each region says what is added to it, alone (aria-atomic="false"), holds only the newest line
	// (two in one moment both) and drops it soon after: a reader that reaches the stack hears each
	// notice once, in the notice
	check("said once, alone", toastStep(d, `
  window.__heard = [];
  kit.scope.$toast.success("Một lần.");
  await T.wait(400);
  kit.scope.$toast.info("Lần hai.");
  await T.wait(300);
  return T.said();`), "status: Lần hai. | alert: ")
	check("the regions say what is added, alone", toastRegionsHeard(d), "status live=polite atomic=false; alert live=assertive atomic=false")
	check("and drop it", toastStep(d, `await T.wait(1200); var said = T.said(); await T.clear(); return T.last("status", 5) + " ; " + said;`),
		"Một lần. / Lần hai. ; status:  | alert: ")

	// F8 while the page cannot give the stack the focus (a modal dialog is open) is the browser's: the
	// time does not stand still for a focus that never came
	toastStep(d, `var dialog = document.createElement("dialog"); dialog.id = "modal"; dialog.innerHTML = '<button id="inside">Trong</button>'; document.body.appendChild(dialog); dialog.showModal(); document.getElementById("inside").focus(); kit.scope.$toast.info("Sau hộp thoại.", { duration: "2s" }); await T.frames(); return "";`)
	toastKey(d, "F8", 119)
	check("F8 under a modal dialog", toastStep(d, `
  var at = T.focus(), paused = kit.scope.$toast.paused;
  var dialog = document.getElementById("modal");
  dialog.close();
  dialog.remove();
  await T.wait(2400);
  return [at, paused, T.rows().length].join(" ");`), "inside false 0")

	// a mouse that closes a notice and stays still: the browser says nothing (no pointerout) when the
	// notice under it goes, and the time of the others goes on
	var dismiss struct{ X, Y float64 }
	if err := json.Unmarshal([]byte(toastStep(d, `
  kit.scope.$toast.info("A.", { duration: 0 });
  kit.scope.$toast.info("B.", { duration: "3s" });
  kit.scope.$toast.info("C.", { duration: "3s" });
  await T.frames();
  await T.wait(300);
  var r = T.rows()[0].querySelector('button[data-kitwork-event^="click: dismiss"]').getBoundingClientRect();
  return JSON.stringify({ x: r.left + r.width / 2, y: r.top + r.height / 2 });`)), &dismiss); err != nil {
		t.Fatal(err)
	}
	d.call("Input.dispatchMouseEvent", map[string]any{"type": "mouseMoved", "x": dismiss.X, "y": dismiss.Y})
	for _, kind := range []string{"mousePressed", "mouseReleased"} {
		d.call("Input.dispatchMouseEvent", map[string]any{"type": kind, "x": dismiss.X, "y": dismiss.Y, "button": "left", "clickCount": 1})
	}
	check("a mouse that closes a notice and stays still", toastStep(d, `
  await T.wait(400);
  var held = kit.scope.$toast.paused + " " + T.rows().length;
  await T.wait(3000);
  return held + " " + T.rows().length;`), "false 2 0")
	d.call("Input.dispatchMouseEvent", map[string]any{"type": "mouseMoved", "x": 5, "y": 5})

	// an id the author gives is a name: a number is the toast's own, and one given would meet it (two
	// notices of one key, a dismiss that closes the other)
	check("an id the author gives is a name", toastStep(d, `
  await T.clear();
  window.__warned = [];
  var n = kit.scope.$toast.info("Thăm dò.");
  kit.scope.$toast.dismiss(n);
  await T.wait(250);
  var author = kit.scope.$toast.info("Của người viết.", { id: n + 2, duration: 0 });
  var one = kit.scope.$toast.info("Tự đánh số một.", { duration: 0 });
  var two = kit.scope.$toast.info("Tự đánh số hai.", { duration: 0 });
  var gone = kit.scope.$toast.dismiss(two);
  await T.wait(250);
  var rows = T.rows().map(function (row) { return (Number(row.getAttribute("data-kit-key")) - n) + ":" + row.querySelectorAll("p")[1].textContent; });
  var warned = window.__warned.join(" + ").replace(String(n + 2), "N");
  window.__warned = [];
  await T.clear();
  return [author - n, one - n, two - n, gone, rows.join(" | "), warned].join(" ; ");`),
		"1 ; 2 ; 3 ; true ; 1:Của người viết. | 2:Tự đánh số một. ; kitwork toast: id của thông báo là một tên bắt đầu bằng chữ cái (chữ, số, - và _): số là của toast (1, 2, 3…), không phải N")
	if errors := d.evaluate(`JSON.stringify((window.__warned || []).filter(function (w) { return w.indexOf("link") < 0; }))`); errors != "[]" {
		t.Errorf("the toast complained: %s", errors)
	}
}
