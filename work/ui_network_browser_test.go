package work

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// networkHost is the network the overlay of networkBrowserFiles draws, as an author would: placed at
// the bottom start, a notice of its own (an icon for each side of the connection, the words of each,
// close()), and its two events said through the toast too, so a step counts them on screen. It counts
// the network gone at once (delay="0"): the proofs that use it are about what follows once it is
// counted (the notice, close(), Drive staying, history), each step on the timing it was written for;
// the wait itself, the component's own second, is TestBrowserNetworkTimes'. "Back online" keeps the
// component's own four seconds (no duration written).
const networkHost = `  <box component="network" as="$network" place="bottom-start" delay="0" offline="$toast.info('Mất mạng.', { duration: 0 })" online="$toast.info('Có mạng lại.', { duration: 0 })">
    <card id="notice" show="open" flex="row" gap="3" padding="3" shadow="large">
      <icon show="!connected" value="wifi-off" alt="Ngoại tuyến" />
      <icon show="connected" value="wifi" alt="Trực tuyến" />
      <box gap="1" width="full">
        <text id="words" weight="bold">{{ connected ? 'Đã có mạng lại' : 'Bạn đang ngoại tuyến' }}</text>
        <text size="small">{{ connected ? 'Liên kết mở lại được.' : 'Trang vẫn đọc được; liên kết chờ có mạng.' }}</text>
      </box>
      <button id="close" click="close()"><icon value="x" alt="Đóng" /></button>
    </card>
  </box>
`

// networkBrowserFiles is a site that moves with Drive (router.reload(false)): its overlay holds the toast
// of the toast test and networkHost; the first page links to /tiep, which links back, and to /tiep?slow,
// which the server holds past Drive's watchdog.
var networkBrowserFiles = map[string]string{
	"localhost/router.work": `import { router } from "kitwork";

router.language("vi");
router.reload(false);
`,
	"localhost/page.ui":    `<page><content /><overlay /></page>`,
	"localhost/overlay.ui": strings.Replace(toastBrowserFiles["localhost/overlay.ui"], "</overlay>", networkHost+"</overlay>", 1),
	"localhost/content.ui": `<content padding="4" gap="2">
  <text id="here">Một</text>
  <link id="next" value="/tiep">Tiếp</link>
  <link id="slow" value="/tiep?slow">Chậm</link>
</content>`,
	"localhost/tiep/content.ui": `<content padding="4" gap="2">
  <text id="here">Tiếp</text>
  <link id="back" value="/">Về</link>
</content>`,
}

// networkHeard is put in every document before its own scripts run, so it hears the page start: each
// event the network component says, as it passes the document on its way up from the host
// (window.__said), each the browser itself fires on the window (window.__browser), and every complaint
// of the page's scripts in the console (window.__complaints).
const networkHeard = `(function () {
  window.__said = [];
  window.__browser = [];
  window.__complaints = [];
  ["warn", "error"].forEach(function (level) {
    var write = console[level];
    console[level] = function () { window.__complaints.push(Array.prototype.join.call(arguments, " ")); return write.apply(console, arguments); };
  });
  window.addEventListener("error", function (event) { window.__complaints.push(String(event.message)); });
  ["online", "offline"].forEach(function (name) {
    document.addEventListener(name, function (event) {
      var host = event.target.getAttribute && event.target.getAttribute("data-kitwork-component");
      if (host && host.indexOf("network") === 0) window.__said.push(name);
    }, true);
    window.addEventListener(name, function (event) { if (event.target === window) window.__browser.push(name); });
  });
})()`

// networkProbe is what each step reads the page with, installed once a page has loaded: the network's
// notice (closed, or shown with its icon and words; then connected and open as the component has them),
// where the page is (its address, its words, whether a move is still going), what the component and
// the browser said since the last read, the toast's notices (words ×count, those leaving left out), and
// a promise for the next event of a name on the document.
const networkProbe = `(function () {
  if (window.__network) return "ready";
  var host = document.querySelector('[data-kitwork-component^="network"]');
  var toast = document.querySelector('[data-kitwork-component^="toast"]');
  var start = window.setTimeout;
  var shown = function (el) { return !!el && !el.hidden && el.checkVisibility(); };
  var take = function (name) { var list = window[name].join(" "); window[name] = []; return list || "nothing"; };
  window.__network = {
    host: host,
    notice: function () {
      var card = document.getElementById("notice"), network = kit.scope.$network;
      var icon = Array.prototype.filter.call(card.querySelectorAll(":scope > i"), shown).map(function (i) { return (i.className.match(/icon-([a-z0-9-]+)/) || [])[1]; }).join("+");
      return (shown(card) ? "shown " + icon + " " + document.getElementById("words").textContent : "closed") +
        " connected=" + network.connected + " open=" + network.open;
    },
    page: function () {
      return [location.pathname, document.getElementById("here").textContent,
        document.documentElement.classList.contains("kitwork-loading") ? "loading" : "settled"].join(" ");
    },
    said: function () { return take("__said"); },
    browser: function () { return take("__browser"); },
    toast: function () {
      var rows = Array.prototype.filter.call(toast.children, function (c) { return c.hasAttribute("data-kit-key") && !c.hasAttribute("data-leaving"); });
      return rows.map(function (row) { var p = row.querySelectorAll("p"); return p[1].textContent + (shown(p[2]) ? p[2].textContent : ""); }).join(" ; ") || "no notice";
    },
    clear: function () { kit.scope.$toast.clear(); return new Promise(function (done) { start(done, 250); }); },
    next: function (name) {
      return new Promise(function (done) {
        var timer = start(function () { done("never"); }, 5000);
        document.addEventListener(name, function () { clearTimeout(timer); done(name); }, { once: true });
      });
    },
    frames: function () { return new Promise(function (done) { requestAnimationFrame(function () { requestAnimationFrame(done); }); }); },
    wait: function (ms) { return new Promise(function (done) { start(done, ms); }); }
  };
  return "installed";
})()`

// networkStep runs one step of the proof after the probe: body is the inside of an async function, which
// returns what the step reads. A document that goes while the step runs (a move given to the browser,
// location.assign, its error page when offline) ends the proof, said as that.
func networkStep(d *devtools, body string) string {
	d.t.Helper()
	d.evaluate(networkProbe)
	script := `(async function () { var N = window.__network; ` + body + ` })()`
	result, problem := d.try("Runtime.evaluate", map[string]any{"expression": script, "awaitPromise": true, "returnByValue": true})
	if problem != "" {
		d.t.Fatalf("the page went while the step ran (%s): a move left it for a whole load", problem)
	}
	var out struct {
		Result struct {
			Value any `json:"value"`
		} `json:"result"`
		Exception *struct{ Text string } `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(result, &out); err != nil {
		d.t.Fatal(err)
	}
	if out.Exception != nil {
		d.t.Fatalf("the step threw: %s\n%s", out.Exception.Text, body)
	}
	if out.Result.Value == nil {
		return ""
	}
	return fmt.Sprint(out.Result.Value)
}

// networkScripts are the files of the page's own scripts, from its HTML; networkHash is the hash in the
// name of each.
var (
	networkScripts = regexp.MustCompile(`<script[^>]* src="?(/_kitwork/[^" >]+)`)
	networkHash    = regexp.MustCompile(`\.[0-9a-f]{16}\.`)
)

// networkOffline takes the network away from the page, or gives it back, as DevTools does: requests
// fail at once, and the browser says so (navigator.onLine, the window's offline and online events).
func networkOffline(d *devtools, offline bool) {
	d.t.Helper()
	d.call("Network.emulateNetworkConditions", map[string]any{"offline": offline, "latency": 0, "downloadThroughput": -1, "uploadThroughput": -1})
}

// networkReload loads the page again and waits for the new document (the old one is marked, so a read
// that reaches it before the navigation is tried again).
func networkReload(d *devtools) {
	d.t.Helper()
	d.evaluate(`window.__stale = true`)
	d.call("Page.reload", map[string]any{"ignoreCache": true})
	d.loaded(`(window.__stale ? "wait of 0" : ` + exampleSettle + `)`)
}

// TestBrowserNetwork proves the network component and Drive's offline move on a page the engine serves,
// in a real browser whose network DevTools takes away and gives back, step by step, its host counting
// the network gone at once (delay="0", networkHost):
//   - online as it starts: the notice closed (open false), connected true, nothing said: online is never
//     said where nothing went missing; the host is a status region fixed at the bottom start;
//   - offline: the notice shows, wifi-off and the offline words, at the bottom start of the screen; the
//     component says offline once, and the author's handler on the host runs (the toast);
//   - close() closes it; the window saying offline again changes nothing (not open, not said again);
//   - offline, a link clicked: the page stays (its address, its words; no browser error page) and the
//     move is over (no kitwork-loading); the notice closed opens again, and offline is said again;
//   - online again: the back words with the wifi icon, online said once, and the notice closes by itself
//     after four seconds; closed in those seconds, it stays closed;
//   - Drive moves to /tiep; offline, Back: the address comes back to /tiep with its content, the notice
//     opens, offline is said once, and nothing moves after; online, Back moves to / as ever;
//   - offline by the browser's word, a move the server holds past Drive's watchdog (3s) stays too;
//   - a page that starts offline opens the notice and says offline as it starts, and never online;
//   - the page loads the component's own file and not the network service's.
func TestBrowserNetwork(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the network browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant := buildUISite(t, networkBrowserFiles)
	// 8, as the server writes it: the component's own file, not the network service (about 4.7 kB of
	// HTTP and uploads the page never uses), which a mention of it in the component would ship
	var scripts []string
	for _, match := range networkScripts.FindAllStringSubmatch(serveUI(t, tenant, "http://localhost/").Body.String(), -1) {
		if strings.Contains(match[1], "network") {
			scripts = append(scripts, networkHash.ReplaceAllString(match[1], ".<hash>."))
		}
	}
	if got := strings.Join(scripts, " "); got != "/_kitwork/components/network.<hash>.js" {
		t.Errorf("the page's network scripts:\n got  %s\n want /_kitwork/components/network.<hash>.js", got)
	}
	var serving sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Has("slow") {
			// held past Drive's watchdog (3s), or until the page gives up on it
			select {
			case <-time.After(3500 * time.Millisecond):
			case <-request.Context().Done():
			}
		}
		forward := request.Clone(request.Context())
		forward.Host = "localhost"
		serving.Lock()
		defer serving.Unlock()
		tenant.Serve(response, forward)
	}))
	defer server.Close()

	d := openDevtools(t, browser, server.URL+"/", 1280, 800, "")
	d.loaded(exampleSettle)
	d.call("Page.enable", map[string]any{})
	d.call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": networkHeard})
	d.call("Network.enable", map[string]any{})
	networkReload(d)
	check := func(step, got, want string) {
		t.Helper()
		if got != want {
			t.Errorf("%s:\n got  %s\n want %s", step, got, want)
		}
	}
	// complaints is what the page's scripts said in the console since its document started
	complaints := func() string { return d.evaluate(`JSON.stringify(window.__complaints)`) }

	// 1. online as it starts
	check("online as it starts", networkStep(d, `
  await N.frames();
  await N.wait(300);
  var style = getComputedStyle(N.host);
  return [N.page(), N.notice(), N.said(), N.toast(), N.host.getAttribute("role"), style.position, navigator.onLine].join(" | ");`),
		`/ Một settled | closed connected=true open=false | nothing | no notice | status | fixed | true`)

	// 2. offline
	networkOffline(d, true)
	check("offline", networkStep(d, `
  await N.frames();
  await N.wait(300);
  var box = document.getElementById("notice").getBoundingClientRect();
  return [N.page(), N.notice(), N.said(), N.browser(), N.toast(), navigator.onLine, Math.round(box.left) + "," + Math.round(innerHeight - box.bottom)].join(" | ");`),
		`/ Một settled | shown wifi-off Bạn đang ngoại tuyến connected=false open=true | offline | offline | Mất mạng. | false | 16,16`)

	// 3. close(), then the window says offline again
	check("closed, and offline again from the window", networkStep(d, `
  document.getElementById("close").click();
  await N.frames();
  var closed = N.notice();
  window.dispatchEvent(new Event("offline"));
  await N.frames();
  await N.wait(200);
  return [closed, N.notice(), N.said(), N.browser(), N.toast()].join(" | ");`),
		`closed connected=false open=false | closed connected=false open=false | nothing | offline | Mất mạng.`)

	// 4. offline, a link: Drive stays, and the notice is told again
	check("offline, a link", networkStep(d, `
  var stayed = N.next("kitwork:offline");
  document.getElementById("next").click();
  var heard = await stayed;
  await N.frames();
  await N.wait(300);
  return [heard, N.page(), N.notice(), N.said(), N.toast()].join(" | ");`),
		`kitwork:offline | / Một settled | shown wifi-off Bạn đang ngoại tuyến connected=false open=true | offline | Mất mạng.×2`)

	// 5. back online: the back words, then closed by itself
	networkOffline(d, false)
	check("back online", networkStep(d, `
  await N.frames();
  await N.wait(300);
  var back = [N.page(), N.notice(), N.said(), N.browser(), N.toast()].join(" | ");
  await N.wait(4000);
  return back + " || " + N.notice() + " | " + N.said();`),
		`/ Một settled | shown wifi Đã có mạng lại connected=true open=true | online | online | Mất mạng.×2 ; Có mạng lại. || closed connected=true open=false | nothing`)
	networkStep(d, `await N.clear(); return "";`)
	networkOffline(d, true)
	check("offline once more", networkStep(d, `await N.wait(300); return N.notice() + " | " + N.said();`),
		`shown wifi-off Bạn đang ngoại tuyến connected=false open=true | offline`)
	networkOffline(d, false)
	check("closed while back online", networkStep(d, `
  await N.frames();
  await N.wait(300);
  var back = N.notice() + " | " + N.said();
  document.getElementById("close").click();
  await N.frames();
  var closed = N.notice();
  await N.wait(4200);
  return [back, closed, N.notice(), N.said(), N.toast()].join(" | ");`),
		`shown wifi Đã có mạng lại connected=true open=true | online | closed connected=true open=false | closed connected=true open=false | nothing | Mất mạng. ; Có mạng lại.`)

	// 6. Drive moves to /tiep; offline, Back stays on /tiep; online, Back moves
	check("a move online", networkStep(d, `
  await N.clear();
  var arrived = N.next("kitwork:load");
  document.getElementById("next").click();
  var heard = await arrived;
  await N.wait(200);
  return [heard, N.page(), history.state.i, N.notice(), N.said()].join(" | ");`),
		`kitwork:load | /tiep Tiếp settled | 1 | closed connected=true open=false | nothing`)
	networkOffline(d, true)
	check("offline on /tiep", networkStep(d, `
  await N.wait(300);
  var told = N.notice() + " | " + N.said();
  document.getElementById("close").click();
  await N.frames();
  return told + " | " + N.notice();`),
		`shown wifi-off Bạn đang ngoại tuyến connected=false open=true | offline | closed connected=false open=false`)
	check("offline, Back", networkStep(d, `
  var stayed = N.next("kitwork:offline");
  history.back();
  var heard = await stayed;
  await N.wait(400);
  var soon = [heard, N.page(), history.state.i, N.notice(), N.said()].join(" | ");
  await N.wait(1000);
  return soon + " || " + [N.page(), history.state.i, N.said()].join(" | ");`),
		`kitwork:offline | /tiep Tiếp settled | 1 | shown wifi-off Bạn đang ngoại tuyến connected=false open=true | offline || /tiep Tiếp settled | 1 | nothing`)
	networkOffline(d, false)
	check("online, Back", networkStep(d, `
  await N.wait(300);
  var said = N.said();
  var arrived = N.next("kitwork:load");
  history.back();
  var heard = await arrived;
  await N.wait(200);
  return [said, heard, N.page(), history.state.i].join(" | ");`),
		`online | kitwork:load | / Một settled | 0`)
	// offline by the browser's word while a request still goes: a move the server holds past Drive's
	// watchdog stays too, the notice closed opens again, and the answer that comes after changes nothing
	check("offline, a move past the watchdog", networkStep(d, `
  document.getElementById("close").click();
  await N.frames();
  var closed = N.notice();
  var online = Object.getOwnPropertyDescriptor(Navigator.prototype, "onLine");
  Object.defineProperty(Navigator.prototype, "onLine", { configurable: true, get: function () { return false; } });
  var stayed = N.next("kitwork:offline"), started = performance.now();
  document.getElementById("slow").click();
  var heard = await stayed, after = performance.now() - started;
  await N.wait(800);
  Object.defineProperty(Navigator.prototype, "onLine", online);
  return [closed, heard, after >= 3000 ? "after the watchdog" : Math.round(after) + "ms", N.page(), history.state.i, N.notice(), N.said()].join(" | ");`),
		`closed connected=true open=false | kitwork:offline | after the watchdog | / Một settled | 0 | shown wifi-off Bạn đang ngoại tuyến connected=false open=true | offline`)
	check("complaints of the page moved through", complaints(), "[]")

	// 7. a page that starts offline
	var override struct {
		Identifier string `json:"identifier"`
	}
	if err := json.Unmarshal(d.call("Page.addScriptToEvaluateOnNewDocument", map[string]any{
		"source": `Object.defineProperty(Navigator.prototype, "onLine", { configurable: true, get: function () { return false; } })`,
	}), &override); err != nil {
		t.Fatal(err)
	}
	networkReload(d)
	check("a page that starts offline", networkStep(d, `
  await N.frames();
  await N.wait(300);
  return [N.page(), N.notice(), N.said(), N.toast()].join(" | ");`),
		`/ Một settled | shown wifi-off Bạn đang ngoại tuyến connected=false open=true | offline | Mất mạng.`)
	d.call("Page.removeScriptToEvaluateOnNewDocument", map[string]any{"identifier": override.Identifier})

	// 8. the files the page loaded: the component's, not the network service's
	check("the files loaded", d.evaluate(`performance.getEntriesByType("resource").map(function (entry) { return new URL(entry.name).pathname; }).filter(function (path) { return path.indexOf("network") >= 0; }).map(function (path) { return path.replace(/\.[0-9a-f]{16}\./, ".<hash>."); }).join(" ")`),
		`/_kitwork/components/network.<hash>.js`)
	check("complaints of the page that started offline", complaints(), "[]")
}

// networkTimesHeard is put in every document before its own scripts run, beside networkHeard: when
// each thing happened (performance.now()), in order, in window.__times: the browser's own offline and
// online on the window ("browser offline"), the component's events on its host ("said offline"),
// Drive's stay (kitwork:offline: "drive offline", heard before the component, which listens later),
// and each change of $network's two words, read every 20ms once the kernel has started it
// ("disconnected+open": connected false, open true). The component says its events at once in what
// changes the words, so a change is always listed after the event of the same moment.
const networkTimesHeard = `(function () {
  window.__times = [];
  var at = function (what) { window.__times.push([what, performance.now()]); };
  ["online", "offline"].forEach(function (name) {
    document.addEventListener(name, function (event) {
      var host = event.target.getAttribute && event.target.getAttribute("data-kitwork-component");
      if (host && host.indexOf("network") === 0) at("said " + name);
    }, true);
    window.addEventListener(name, function (event) { if (event.target === window) at("browser " + name); });
  });
  document.addEventListener("kitwork:offline", function () { at("drive offline"); });
  var last = "";
  setInterval(function () {
    var network = window.kit && kit.scope && kit.scope.$network;
    if (!network) return;
    var now = (network.connected ? "connected" : "disconnected") + "+" + (network.open ? "open" : "closed");
    if (now !== last) { if (last) at(now); last = now; }
  }, 20);
})()`

// networkTimesRead goes before the body of each step of the times proof. times() is what happened
// since the last read, names() writes it as its names in order ("nothing" for none), first() is when
// the first of a name happened in it (NaN when it did not), within() says words when the time from
// one moment to another is from low to high milliseconds, else that time ("NaNms" when one never came;
// each time it measures is kept in window.__measured, which the proof logs), and held() is the two times the instance holds, delay then duration. until() waits (up to three
// seconds) for a name to happen and says it ("never …" when it does not): an event said before the step
// began (an online said at once) is found too, where a listener added then would miss it.
const networkTimesRead = `var times = function () { var list = window.__times; window.__times = []; return list; };
  var names = function (list) { return list.map(function (entry) { return entry[0]; }).join(", ") || "nothing"; };
  var first = function (list, name) { for (var i = 0; i < list.length; i++) if (list[i][0] === name) return list[i][1]; return NaN; };
  var until = async function (name) {
    for (var waited = 0; waited < 3000 && isNaN(first(window.__times, name)); waited += 10) await N.wait(10);
    return isNaN(first(window.__times, name)) ? "never " + name : name;
  };
  var within = function (from, to, low, high, words) {
    var ms = to - from;
    (window.__measured = window.__measured || []).push(words + ": " + Math.round(ms) + "ms");
    return ms >= low && ms <= high ? words : Math.round(ms) + "ms";
  };
  var held = function () { var network = kit.scope.$network; return network.delay + " " + network.duration; };
`

// networkHostTimes finds the times a page's network host carries to its instance (data-kitwork-scope),
// "" for none.
var networkHostTimes = regexp.MustCompile(`data-kitwork-component="network: \$network"[^>]*?(?: data-kitwork-scope="([^"]*)")?>`)

// TestBrowserNetworkTimes proves the network host's two times in a real browser whose network DevTools
// takes away and gives back, on networkBrowserFiles' site with its host's times written two ways.
//
// delay="1s" duration="2s": the page carries both to the instance, in milliseconds;
//   - a break shorter than the delay (offline, then online again some 400ms later) says nothing and
//     opens nothing, connected stays true throughout, and no online follows: nothing went missing;
//   - a break longer than the delay is said once, not before the delay is over and within half a
//     second after; the notice opens then, not before; the author's handler runs (the toast);
//   - back online, "back online" opens at once, and closes by itself after the duration, not before;
//   - offline again, a link clicked before the delay is over: Drive stays (kitwork:offline), and the
//     notice opens at the click, without waiting; the wait under way ends there, so nothing is said
//     again when the delay would have run out.
//
// duration="0" (delay not written): the page carries the duration alone, and the instance keeps its own
// delay, a second: the network gone is said after it; back online, "back online" stays past the
// component's own four seconds, until close().
func TestBrowserNetworkTimes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the network times browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	const (
		offline = "shown wifi-off Bạn đang ngoại tuyến connected=false open=true"
		back    = "shown wifi Đã có mạng lại connected=true open=true"
		closed  = "closed connected=true open=false"
	)
	// site is networkBrowserFiles with its host's times written as given in place of networkHost's
	// delay="0", the times its page carries to the instance checked, served to a browser of its own (a
	// browser lives 40 seconds), and a step that reads it after networkTimesRead
	site := func(t *testing.T, written, carried string) (*devtools, func(string) string) {
		t.Helper()
		files := make(map[string]string, len(networkBrowserFiles))
		for name, source := range networkBrowserFiles {
			files[name] = source
		}
		if strings.Count(files["localhost/overlay.ui"], ` delay="0"`) != 1 {
			t.Fatal(`the overlay's network host no longer writes delay="0"`)
		}
		files["localhost/overlay.ui"] = strings.Replace(files["localhost/overlay.ui"], ` delay="0"`, written, 1)
		tenant := buildUISite(t, files)
		host := networkHostTimes.FindStringSubmatch(serveUI(t, tenant, "http://localhost/").Body.String())
		if host == nil || host[1] != carried {
			t.Fatalf("the page's network host carries %q to its instance, want data-kitwork-scope=%q", host, carried)
		}
		var serving sync.Mutex
		server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			forward := request.Clone(request.Context())
			forward.Host = "localhost"
			serving.Lock()
			defer serving.Unlock()
			tenant.Serve(response, forward)
		}))
		t.Cleanup(server.Close)
		d := openDevtools(t, browser, server.URL+"/", 1280, 800, "")
		d.loaded(exampleSettle)
		d.call("Page.enable", map[string]any{})
		d.call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": networkHeard})
		d.call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": networkTimesHeard})
		d.call("Network.enable", map[string]any{})
		networkReload(d)
		return d, func(body string) string {
			t.Helper()
			return networkStep(d, networkTimesRead+body)
		}
	}
	check := func(t *testing.T, step, got, want string) {
		t.Helper()
		if got != want {
			t.Errorf("%s:\n got  %s\n want %s", step, got, want)
		}
	}
	// gone reads a break longer than the delay, the network taken away just before: the offline said,
	// what happened until half a second after it, when it came after the browser's own, the notice as it
	// opened and the toast
	const gone = `
  var said = await until("said offline");
  await N.frames();
  var shown = N.notice();
  await N.wait(500);
  var list = times();
  return [said, names(list), within(first(list, "browser offline"), first(list, "said offline"), 950, 1500, "said after the delay"), shown, N.toast()].join(" | ");`

	t.Run(`delay="1s" duration="2s"`, func(t *testing.T) {
		d, step := site(t, ` delay="1s" duration="2s"`, "delay: 1000; duration: 2000")
		check(t, "online as it starts", step(`
  await N.frames();
  await N.wait(300);
  return [N.page(), N.notice(), names(times()), held()].join(" | ");`),
			"/ Một settled | "+closed+" | nothing | 1000 2000")

		// a break shorter than the delay
		networkOffline(d, true)
		time.Sleep(400 * time.Millisecond)
		networkOffline(d, false)
		check(t, "a break shorter than the delay", step(`
  await N.wait(1500);
  var list = times();
  return [names(list), within(first(list, "browser offline"), first(list, "browser online"), 0, 900, "shorter than the delay"), N.notice(), N.said(), N.toast()].join(" | ");`),
			"browser offline, browser online | shorter than the delay | "+closed+" | nothing | no notice")

		// a break longer than the delay
		networkOffline(d, true)
		check(t, "a break longer than the delay", step(gone),
			"said offline | browser offline, said offline, disconnected+open | said after the delay | "+offline+" | Mất mạng.")

		// back online: open at once, closed after the duration
		networkOffline(d, false)
		check(t, "back online", step(`
  var said = await until("said online");
  await N.frames();
  var shown = N.notice();
  for (var tries = 0; tries < 120 && kit.scope.$network.open; tries++) await N.wait(50);
  await N.frames();
  var list = times();
  return [said, shown, names(list), within(first(list, "said online"), first(list, "connected+closed"), 1950, 2600, "closed after the duration"), N.notice(), N.toast()].join(" | ");`),
			"said online | "+back+" | browser online, said online, connected+open, connected+closed | closed after the duration | "+closed+" | Mất mạng. ; Có mạng lại.")

		// offline again, a link clicked before the delay is over
		networkStep(d, `await N.clear(); return "";`)
		networkOffline(d, true)
		check(t, "a link clicked offline before the delay is over", step(`
  for (var tries = 0; tries < 40 && navigator.onLine; tries++) await N.wait(25);
  await N.wait(100);
  var stayed = N.next("kitwork:offline"), clicked = performance.now();
  document.getElementById("next").click();
  var heard = await stayed;
  await N.frames();
  var page = N.page(), shown = N.notice();
  await N.wait(1500);
  var list = times();
  return [heard, page, shown, names(list), within(first(list, "browser offline"), clicked, 0, 800, "clicked before the delay was over"),
    within(clicked, first(list, "said offline"), 0, 300, "said at the click"), N.notice(), N.toast()].join(" | ");`),
			"kitwork:offline | / Một settled | "+offline+" | browser offline, drive offline, said offline, disconnected+open | clicked before the delay was over | said at the click | "+offline+" | Mất mạng.")
		networkOffline(d, false)
		check(t, "online after the link", step(`
  var said = await until("said online");
  await N.wait(100);
  return [said, N.notice(), names(times())].join(" | ");`),
			"said online | "+back+" | browser online, said online, connected+open")
		check(t, "complaints of the page", d.evaluate(`JSON.stringify(window.__complaints)`), "[]")
		t.Logf("measured: %s", d.evaluate(`(window.__measured || []).join("; ")`))
	})

	t.Run(`duration="0"`, func(t *testing.T) {
		d, step := site(t, ` duration="0"`, "duration: 0")
		check(t, "online as it starts", step(`
  await N.frames();
  await N.wait(300);
  return [N.page(), N.notice(), names(times()), held()].join(" | ");`),
			"/ Một settled | "+closed+" | nothing | 1000 0")

		// the component's own delay, a second
		networkOffline(d, true)
		check(t, "a break longer than the delay", step(gone),
			"said offline | browser offline, said offline, disconnected+open | said after the delay | "+offline+" | Mất mạng.")

		// back online: it stays past the component's own four seconds, until close()
		networkOffline(d, false)
		check(t, "back online, until close()", step(`
  var said = await until("said online");
  await N.wait(4600);
  var list = times(), stayed = N.notice();
  document.getElementById("close").click();
  await N.frames();
  var shut = N.notice();
  await N.wait(300);
  return [said, names(list), stayed, shut, names(times()), N.notice()].join(" | ");`),
			"said online | browser online, said online, connected+open | "+back+" | "+closed+" | connected+closed | "+closed)
		check(t, "complaints of the page", d.evaluate(`JSON.stringify(window.__complaints)`), "[]")
		t.Logf("measured: %s", d.evaluate(`(window.__measured || []).join("; ")`))
	})
}

// networkHistoryFiles is a site of three pages that moves with Drive and slides between them
// (router.transition("slide")), its overlay networkBrowserFiles' (the toast and networkHost). The
// first two are taller than the screen, with a place in the middle (#cuoi) an in-page link goes to,
// and a link to the next page that the server holds (?held) until the proof lets it answer.
var networkHistoryFiles = map[string]string{
	"localhost/router.work": `import { router } from "kitwork";

router.language("vi");
router.reload(false);
router.transition("slide");
`,
	"localhost/page.ui":    `<page><content /><overlay /></page>`,
	"localhost/overlay.ui": networkBrowserFiles["localhost/overlay.ui"],
	"localhost/content.ui": `<content padding="4" gap="2">
  <text id="here">Một</text>
  <link id="next" value="/tiep">Tiếp</link>
  <link id="held" value="/tiep?held">Giữ</link>
  <link id="slow" value="/tiep?slow">Chậm</link>
  <link id="jump" value="#cuoi">Xuống giữa trang</link>
  <box min-height="100dvh" />
  <text id="cuoi">Giữa trang</text>
  <box min-height="100dvh" />
</content>`,
	"localhost/tiep/content.ui": `<content padding="4" gap="2">
  <text id="here">Tiếp</text>
  <link id="next" value="/ba">Ba</link>
  <link id="back" value="/">Về</link>
  <link id="held" value="/ba?held">Giữ</link>
  <link id="jump" value="#cuoi">Xuống giữa trang</link>
  <box min-height="100dvh" />
  <text id="cuoi">Giữa trang</text>
  <box min-height="100dvh" />
</content>`,
	"localhost/ba/content.ui": `<content padding="4" gap="2">
  <text id="here">Ba</text>
  <link id="back" value="/">Về</link>
</content>`,
}

// networkHistoryHeard is put in every document before its own scripts run, beside networkHeard: each
// way Drive moves the content (the values it gives <html data-kitwork-moving>: "slide", "slide back"),
// and each kitwork:offline and kitwork:load, in order.
const networkHistoryHeard = `(function () {
  window.__moves = [];
  window.__drive = [];
  var set = Element.prototype.setAttribute;
  Element.prototype.setAttribute = function (name, value) {
    if (name === "data-kitwork-moving" && this === document.documentElement) window.__moves.push(value);
    return set.apply(this, arguments);
  };
  ["kitwork:offline", "kitwork:load"].forEach(function (name) {
    document.addEventListener(name, function () { window.__drive.push(name.slice(8)); });
  });
})()`

// networkWithoutNavigation takes the Navigation API away from a document, as in a browser without it:
// Drive then knows where an entry sits only by the index it stamps on it (history.state.i).
const networkWithoutNavigation = `Object.defineProperty(window, "navigation", { configurable: true, value: undefined })`

// networkHistoryRead goes before the body of each step of the history proof. address() is the address
// with its #fragment, the page's words, whether a move still goes and Drive's index of the entry
// (history.state.i); where() adds history.length and the Navigation API's index ("none" without it);
// place() is "at #cuoi" when that place stands at the top of the screen, else how far the page is
// scrolled; drive() is what Drive said since the last read (offline, load) and moves() the ways it
// moved the content; bar() is whether the bar of a move shows (kit.progress, which the progressbar draws).
const networkHistoryRead = `var address = function () {
    return [location.pathname + location.hash, document.getElementById("here").textContent,
      document.documentElement.classList.contains("kitwork-loading") ? "loading" : "settled", "i=" + (history.state && history.state.i)].join(" ");
  };
  var where = function () { return address() + " length=" + history.length + " index=" + (window.navigation ? navigation.currentEntry.index : "none"); };
  var place = function () {
    var mark = document.getElementById("cuoi");
    return mark && Math.abs(mark.getBoundingClientRect().top) < 2 ? "at #cuoi" : "y=" + Math.round(scrollY);
  };
  var drive = function () { var list = window.__drive.join(" "); window.__drive = []; return list || "nothing"; };
  var moves = function () { var list = window.__moves.join(","); window.__moves = []; return list || "none"; };
  var bar = function () { return kit.progress.active ? "bar shown" : "bar idle"; };
`

// TestBrowserNetworkHistory proves Drive's moves through history when the network is gone, and its
// moves between the #fragments of one page, on a site the engine serves, in a real browser: once with
// the Navigation API (Drive reads where an entry sits from it) and once without (Drive counts by the
// index it stamps on each entry, history.state.i).
//   - (b) on the page as it loaded, offline, the notice closed: an in-page link moves to its #fragment
//     and the place on the page, and nothing else: no kitwork:offline, the notice stays closed, nothing
//     said again; Back comes back to the place the page was scrolled to. Online, Forward and Back
//     between the two fetch nothing;
//   - (a) offline on the third page, two quick Backs: the address comes back to the third page, the
//     same entry (history.length, the index), its content stays, and offline is told;
//   - (c) / then /#cuoi (its link clicked twice: the second click is to the address already there, and
//     the browser replaces the entry) then /tiep, offline, history.go(-2): the address comes back to
//     /tiep. With the Navigation API, the same over an entry the page's own script pushed, which Drive
//     never hears of, in one return (one kitwork:offline);
//   - (d) a page fetched ahead (the pointer rested on its link) that the server holds, offline, the link
//     clicked: after the 3s watchdog the move is over (no kitwork-loading, the bar idle), and the answer
//     that comes after is not swapped in;
//   - (e) online, on a page loaded afresh: an in-page link (twice: the entry is replaced, not added),
//     links, Back and Forward between pages and #fragments, each to its place, the page fetched only for
//     a move to another page, and the slide going back on Back and forward on Forward and on links.
func TestBrowserNetworkHistory(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the network history browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	t.Run("with the Navigation API", func(t *testing.T) { networkHistoryProof(t, browser, true) })
	t.Run("without the Navigation API", func(t *testing.T) { networkHistoryProof(t, browser, false) })
}

func networkHistoryProof(t *testing.T, browser string, navigation bool) {
	tenant := buildUISite(t, networkHistoryFiles)
	var (
		serving, seen sync.Mutex
		pages         []string // the pages Drive asked the server for (X-Kitwork-Hydrate)
		release       = make(chan struct{})
		released      sync.Once
		heldArrived   = make(chan struct{}, 1)
		heldEnded     = make(chan string, 1)
	)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-Kitwork-Hydrate") == "1" {
			seen.Lock()
			pages = append(pages, request.URL.RequestURI())
			seen.Unlock()
		}
		if request.URL.Query().Has("held") {
			// said without waiting: a second held request (a page gone wrong) never holds the server up
			ended := func(how string) {
				select {
				case heldEnded <- how:
				default:
				}
			}
			select {
			case heldArrived <- struct{}{}:
			default:
			}
			select {
			case <-release:
			case <-request.Context().Done():
				ended("given up")
				return
			case <-time.After(30 * time.Second):
			}
			defer ended("answered")
		}
		if request.URL.Query().Has("slow") {
			time.Sleep(800 * time.Millisecond) // a page that answers late, without holding the server
		}
		forward := request.Clone(request.Context())
		forward.Host = "localhost"
		serving.Lock()
		defer serving.Unlock()
		tenant.Serve(response, forward)
	}))
	defer server.Close()
	letGo := func() { released.Do(func() { close(release) }) }
	defer letGo() // before the server closes, which waits for the held page
	// fetched is the pages Drive asked the server for since the last read
	fetched := func() string {
		seen.Lock()
		defer seen.Unlock()
		list := strings.Join(pages, " ")
		pages = nil
		if list == "" {
			return "none"
		}
		return list
	}

	d := openDevtools(t, browser, server.URL+"/", 1280, 800, "")
	d.loaded(exampleSettle)
	d.call("Page.enable", map[string]any{})
	d.call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": networkHeard})
	d.call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": networkHistoryHeard})
	if !navigation {
		d.call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": networkWithoutNavigation})
	}
	d.call("Network.enable", map[string]any{})
	networkReload(d)
	fetched()
	check := func(step, got, want string) {
		t.Helper()
		if got != want {
			t.Errorf("%s:\n got  %s\n want %s", step, got, want)
		}
	}
	step := func(body string) string {
		t.Helper()
		return networkStep(d, networkHistoryRead+body)
	}
	complaints := func() string { return d.evaluate(`JSON.stringify(window.__complaints)`) }
	// entry is how where() writes an entry: Drive's index, history.length, the Navigation API's index
	entry := func(i, length, index int) string {
		if !navigation {
			return fmt.Sprintf("i=%d length=%d index=none", i, length)
		}
		return fmt.Sprintf("i=%d length=%d index=%d", i, length, index)
	}
	const (
		told   = "shown wifi-off Bạn đang ngoại tuyến connected=false open=true"
		closed = "closed connected=false open=false"
		// closeNotice reads the notice the network's going opened, and closes it
		closeNotice = `
  await N.wait(300);
  var shown = [N.notice(), N.said(), N.browser()].join(" | ");
  document.getElementById("close").click();
  await N.frames();
  return shown + " | " + N.notice();`
	)

	check("as it starts", step(`return where() + " | " + place();`), "/ Một settled "+entry(0, 1, 0)+" | y=0")

	// (b) an in-page link offline, on the page as it loaded (no move of Drive yet)
	networkOffline(d, true)
	check("offline, the notice closed", step(closeNotice), told+" | offline | offline | "+closed)
	check("offline, an in-page link", step(`
  scrollTo(0, 120);
  await N.frames();
  document.getElementById("jump").click();
  await N.frames();
  await N.wait(400);
  return [where(), place(), drive(), moves(), N.notice(), N.said(), N.browser()].join(" | ");`),
		"/#cuoi Một settled "+entry(1, 2, 1)+" | at #cuoi | nothing | none | "+closed+" | nothing | nothing")
	check("offline, Back from the #fragment", step(`
  history.back();
  await N.frames();
  await N.wait(400);
  return [where(), place(), drive(), moves(), N.notice(), N.said(), N.browser()].join(" | ");`),
		"/ Một settled "+entry(0, 2, 0)+" | y=120 | nothing | none | "+closed+" | nothing | nothing")
	networkOffline(d, false)
	check("online, Forward and Back between the #fragments", step(`
  await N.wait(300);
  var said = N.said() + " | " + N.browser();
  history.forward();
  await N.frames();
  await N.wait(400);
  var forward = [where(), place(), drive(), moves()].join(" | ");
  history.back();
  await N.frames();
  await N.wait(400);
  return [said, forward, [where(), place(), drive(), moves()].join(" | ")].join(" || ");`),
		"online | online || /#cuoi Một settled "+entry(1, 2, 1)+" | at #cuoi | nothing | none || / Một settled "+entry(0, 2, 0)+" | y=120 | nothing | none")
	check("pages fetched for the #fragment moves", fetched(), "none")

	// (a) two quick Backs offline from the third page
	check("online, links to the third page", step(`
  var arrived = N.next("kitwork:load");
  document.getElementById("next").click();
  await arrived;
  await N.wait(300);
  arrived = N.next("kitwork:load");
  document.getElementById("next").click();
  await arrived;
  await N.wait(300);
  return [where(), drive(), moves()].join(" | ");`),
		"/ba Ba settled "+entry(2, 3, 2)+" | load load | slide,slide")
	check("pages fetched for the two links", fetched(), "/tiep /ba")
	networkOffline(d, true)
	check("offline on the third page, the notice closed", step(closeNotice), told+" | offline | offline | "+closed)
	// The second Back may reach the page before the first move has failed, or after: then the first
	// return is under way when the second Back lands, and Drive returns again from there. Either way the
	// address ends on the third page, and offline is told once or more (only offline: no load).
	check("offline, two quick Backs", step(`
  var many = function (list) { return /^offline( offline)*$/.test(list) ? "offline told" : list; };
  var stayed = N.next("kitwork:offline");
  history.back();
  history.back();
  var heard = await stayed;
  await N.wait(1500);
  return [heard, where(), many(drive()), moves(), N.notice(), many(N.said())].join(" | ");`),
		"kitwork:offline | /ba Ba settled "+entry(2, 3, 2)+" | offline told | none | "+told+" | offline told")
	networkOffline(d, false)

	// (c) #fragment entries between: / then /#cuoi (twice) then /tiep, offline, history.go(-2)
	check("online, / then /#cuoi twice then /tiep", step(`
  await N.wait(300);
  N.said(); N.browser();
  var arrived = N.next("kitwork:load");
  document.getElementById("back").click();
  await arrived;
  await N.wait(300);
  document.getElementById("jump").click();
  await N.frames();
  document.getElementById("jump").click();
  await N.frames();
  await N.wait(200);
  var jumped = where();
  arrived = N.next("kitwork:load");
  document.getElementById("next").click();
  await arrived;
  await N.wait(300);
  return [jumped, where(), drive(), moves()].join(" | ");`),
		"/#cuoi Một settled "+entry(4, 5, 4)+" | /tiep Tiếp settled "+entry(5, 6, 5)+" | load load | slide,slide")
	check("pages fetched on the way", fetched(), "/ /tiep")
	networkOffline(d, true)
	check("offline on /tiep, the notice closed", step(closeNotice), told+" | offline | offline | "+closed)
	check("offline, history.go(-2) over the #fragment", step(`
  var stayed = N.next("kitwork:offline");
  history.go(-2);
  var heard = await stayed;
  await N.wait(1500);
  return [heard, where(), drive(), moves(), N.notice(), N.said()].join(" | ");`),
		"kitwork:offline | /tiep Tiếp settled "+entry(5, 6, 5)+" | offline | none | "+told+" | offline")
	networkOffline(d, false)

	// (d) a page fetched ahead, its answer held past Drive's watchdog
	check("online, the pointer rests on a held link", step(`
  await N.wait(300);
  N.said(); N.browser();
  document.getElementById("held").dispatchEvent(new MouseEvent("mouseover", { bubbles: true }));
  await N.wait(300);
  return where();`), "/tiep Tiếp settled "+entry(5, 6, 5))
	select {
	case <-heldArrived:
	case <-time.After(5 * time.Second):
		t.Fatal("the pointer rested on the held link and its page was never fetched ahead")
	}
	networkOffline(d, true)
	check("offline, the held link clicked", step(`
  await N.wait(300);
  N.said(); N.browser();
  document.getElementById("close").click();
  await N.frames();
  var stayed = N.next("kitwork:offline"), started = performance.now();
  document.getElementById("held").click();
  await N.wait(1000);
  var during = where() + " " + bar();
  var heard = await stayed, after = performance.now() - started;
  await N.wait(500);
  return [during, heard, after >= 3000 ? "after the watchdog" : Math.round(after) + "ms", where(), bar(), drive(), N.notice(), N.said()].join(" | ");`),
		"/tiep Tiếp loading "+entry(5, 6, 5)+" bar shown | kitwork:offline | after the watchdog | /tiep Tiếp settled "+entry(5, 6, 5)+" | bar idle | offline | "+told+" | offline")
	select {
	case how := <-heldEnded:
		t.Fatalf("the held page ended (%s) before the proof let it answer", how)
	default:
	}
	letGo()
	select {
	case how := <-heldEnded:
		check("the held page", how, "answered")
	case <-time.After(5 * time.Second):
		t.Fatal("the held page never answered")
	}
	check("the held answer, after the move ended", step(`
  var came = function () { return performance.getEntriesByType("resource").some(function (entry) { return entry.name.indexOf("?held") >= 0; }); };
  for (var tries = 0; tries < 30 && !came(); tries++) await N.wait(100);
  var answer = came() ? "the answer came" : "no answer";
  await N.wait(500);
  return [answer, where(), bar(), drive(), moves()].join(" | ");`),
		"the answer came | /tiep Tiếp settled "+entry(5, 6, 5)+" | bar idle | nothing | none")
	check("pages fetched for the held link", fetched(), "/ba?held")
	networkOffline(d, false)

	// (c) again over an entry the page's own script pushed, which Drive never hears of (no popstate): the
	// Navigation API knows where it sits, Drive's own count does not (without the API the return takes
	// two steps, each told)
	if navigation {
		check("online, / then an entry of the page's own then /tiep", step(`
  await N.wait(300);
  N.said(); N.browser();
  var arrived = N.next("kitwork:load");
  document.getElementById("back").click();
  await arrived;
  await N.wait(300);
  history.pushState(null, "", "#panel");
  arrived = N.next("kitwork:load");
  document.getElementById("next").click();
  await arrived;
  await N.wait(300);
  return [where(), drive(), moves()].join(" | ");`),
			"/tiep Tiếp settled i=7 length=9 index=8 | load load | slide,slide")
		check("pages fetched on that way", fetched(), "/ /tiep")
		networkOffline(d, true)
		check("offline on /tiep again, the notice closed", step(closeNotice), told+" | offline | offline | "+closed)
		check("offline, history.go(-2) over the entry of the page's own", step(`
  var stayed = N.next("kitwork:offline");
  history.go(-2);
  var heard = await stayed;
  await N.wait(1500);
  return [heard, where(), drive(), N.said()].join(" | ");`),
			"kitwork:offline | /tiep Tiếp settled i=7 length=9 index=8 | offline | offline")
		networkOffline(d, false)
	}
	check("complaints of the page moved through", complaints(), "[]")

	// (e) online, on a page loaded afresh
	d.evaluate(`window.__stale = true`)
	d.call("Page.navigate", map[string]any{"url": server.URL + "/"})
	d.loaded(`(window.__stale ? "wait of 0" : ` + exampleSettle + `)`)
	fetched()
	check("online, an in-page link twice", step(`
  await N.wait(200);
  var length = history.length;
  document.getElementById("jump").click();
  await N.frames();
  await N.wait(300);
  var once = [address(), place(), "+" + (history.length - length)].join(" ");
  document.getElementById("jump").click();
  await N.frames();
  await N.wait(300);
  return [once, [address(), place(), "+" + (history.length - length)].join(" "), drive(), moves()].join(" | ");`),
		"/#cuoi Một settled i=1 at #cuoi +1 | /#cuoi Một settled i=1 at #cuoi +1 | nothing | none")
	check("pages fetched for the in-page link", fetched(), "none")
	check("online, a link, Back to the #fragment, Back again", step(`
  var arrived = N.next("kitwork:load");
  document.getElementById("next").click();
  await arrived;
  await N.wait(300);
  var linked = [address(), place(), drive(), moves()].join(" ");
  arrived = N.next("kitwork:load");
  history.back();
  await arrived;
  await N.wait(300);
  var back = [address(), place(), drive(), moves()].join(" ");
  history.back();
  await N.frames();
  await N.wait(400);
  return [linked, back, [address(), place(), drive(), moves()].join(" ")].join(" | ");`),
		"/tiep Tiếp settled i=2 y=0 load slide | /#cuoi Một settled i=1 at #cuoi load slide back | / Một settled i=0 y=0 nothing none")
	check("pages fetched: the link and the Back to another page", fetched(), "/tiep /")
	check("online, Forward to the #fragment, Forward to the page after", step(`
  history.forward();
  await N.frames();
  await N.wait(400);
  var fragment = [address(), place(), drive(), moves()].join(" ");
  var arrived = N.next("kitwork:load");
  history.forward();
  await arrived;
  await N.wait(300);
  return [fragment, [address(), place(), drive(), moves()].join(" ")].join(" | ");`),
		"/#cuoi Một settled i=1 at #cuoi nothing none | /tiep Tiếp settled i=2 y=0 load slide")
	check("pages fetched: the Forward to another page", fetched(), "/tiep")
	check("online, an in-page link on /tiep, Back twice, history.go(2) to its #fragment", step(`
  document.getElementById("jump").click();
  await N.frames();
  await N.wait(300);
  var jumped = [address(), place(), drive(), moves()].join(" ");
  history.back();
  await N.frames();
  await N.wait(400);
  var back = [address(), place(), drive(), moves()].join(" ");
  var arrived = N.next("kitwork:load");
  history.back();
  await arrived;
  await N.wait(300);
  var first = [address(), place(), drive(), moves()].join(" ");
  arrived = N.next("kitwork:load");
  history.go(2);
  await arrived;
  await N.wait(300);
  return [jumped, back, first, [address(), place(), drive(), moves()].join(" ")].join(" | ");`),
		"/tiep#cuoi Tiếp settled i=3 at #cuoi nothing none | /tiep Tiếp settled i=2 y=0 nothing none | /#cuoi Một settled i=1 at #cuoi load slide back | /tiep#cuoi Tiếp settled i=3 at #cuoi load slide")
	check("pages fetched: the two moves to another page", fetched(), "/ /tiep")
	check("online, a link back to the first page", step(`
  var arrived = N.next("kitwork:load");
  document.getElementById("back").click();
  await arrived;
  await N.wait(300);
  return [address(), place(), drive(), moves()].join(" ");`), "/ Một settled i=4 y=0 load slide")
	check("pages fetched: the link", fetched(), "/")
	// a Back whose page answers late, then a second Back to the page on screen before it does: the
	// address names the page on screen again, so the late page is never swapped in
	check("online, a slow page, Back to it, Back again before it answers", step(`
  var arrived = N.next("kitwork:load");
  document.getElementById("slow").click();
  await arrived;
  await N.wait(300);
  arrived = N.next("kitwork:load");
  document.getElementById("back").click();
  await arrived;
  await N.wait(300);
  var before = [address(), drive(), moves()].join(" ");
  history.back();
  await N.wait(150);
  var during = address();
  history.back();
  await N.wait(1500);
  return [before, during, [address(), place(), bar(), drive(), moves()].join(" ")].join(" | ");`),
		"/ Một settled i=6 load load slide,slide | /tiep Một loading i=5 | / Một settled i=4 y=0 bar idle nothing none")
	check("pages fetched: the slow page twice and the link", fetched(), "/tiep?slow / /tiep?slow")
	check("complaints of the page loaded afresh", complaints(), "[]")
}
