package work

import (
	"encoding/json"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// exampleNetworkSay waits (up to three seconds: the example's delay, a second, and two to spare) until
// the network notice of the page is in the state asked for — "offline": open and saying so; "online":
// open and saying it is back; "closed": not shown; "": nothing to wait for — and how long it waited for
// it (to the poll's 50ms: the notice the network's going opens waits the delay), then two frames, and
// says what is on screen: where the page is (its path, whether a move is still going, whether the menu
// is open), $network's state, the notice (its icons shown with the name a screen reader hears,
// icon:name, its title and words), its rectangle and those
// of its icon, title and close button (to judge the alignment), whether it is inside the screen, the
// edge it sits at (on screen, within 24px of the bottom or the top) and whether at the start, the
// contrast of its title, words, icon and close button against the card (computed colours, as the
// screen paints them), the toast's notices on screen and how much of them the network notice covers,
// how far the page scrolls sideways, the scheme, and where the screen is on the page (to picture it
// whole).
const exampleNetworkSay = `(function (want) {
  var host = function () { return document.querySelector('[data-kitwork-component^="network"]'); };
  var shown = function (el) { return !!el && !el.hidden && el.getClientRects().length > 0; };
  var ready = function () {
    var h = host(), n = window.kit && kit.scope && kit.scope.$network;
    if (want === "") return true;
    if (!h || !n || !h.firstElementChild) return false;
    if (want === "closed") return !n.open && !shown(h.firstElementChild);
    return n.open && n.connected === (want === "online") && shown(h.firstElementChild);
  };
  var start = Date.now();
  return new Promise(function (done) {
    (function poll() {
      if (!ready() && Date.now() - start < 3000) { setTimeout(poll, 50); return; }
      var reached = ready(), waited = Date.now() - start;
      // the notice's way in (a fade and half a rem, @starting-style) ends before it is measured
      var card = host() && host().firstElementChild;
      Promise.all(card ? card.getAnimations().map(function (a) { return a.finished.catch(function () {}); }) : []).then(function () {
      requestAnimationFrame(function () { requestAnimationFrame(function () {
        var h = host(), n = window.kit && kit.scope && kit.scope.$network;
        var out = { reached: reached, waited: waited, path: location.pathname, loading: document.documentElement.classList.contains("kitwork-loading"),
          menu: !!(window.kit && kit.scope && kit.scope.$menu && kit.scope.$menu.open),
          bar: window.kit && kit.progress && kit.progress.active ? Math.round(kit.progress.value) + "%" : "idle",
          overflow: Math.max(document.documentElement.scrollWidth, innerWidth) - innerWidth,
          dark: matchMedia("(prefers-color-scheme: dark)").matches, scrollX: scrollX, scrollY: scrollY };
        if (!h || !n) { out.notice = "no network host"; done(JSON.stringify(out)); return; }
        out.connected = n.connected; out.open = n.open;
        var card = h.firstElementChild;
        out.shown = shown(card);
        if (out.shown) {
          // a colour as the screen paints it, whatever CSS wrote it in (rgb, oklab, color-mix): r, g, b, a
          var paint = document.createElement("canvas").getContext("2d", { willReadFrequently: true });
          var rgba = function (colour) {
            paint.clearRect(0, 0, 1, 1); paint.fillStyle = "#000"; paint.fillStyle = colour; paint.fillRect(0, 0, 1, 1);
            var c = paint.getImageData(0, 0, 1, 1).data; return [c[0], c[1], c[2], c[3] / 255];
          };
          var over = function (top, under) { return [0, 1, 2].map(function (k) { return top[k] * top[3] + under[k] * (1 - top[3]); }); };
          // the card's ground: its own background over those under it, down to the first opaque one
          var layers = [];
          for (var e = card; e; e = e.parentElement) { var b = rgba(getComputedStyle(e).backgroundColor); if (b[3] > 0) layers.push(b); if (b[3] === 1) break; }
          var ground = [255, 255, 255];
          for (var i = layers.length - 1; i >= 0; i--) ground = over(layers[i], ground);
          var luminance = function (c) { var l = c.map(function (v) { v /= 255; return v <= 0.04045 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4); }); return 0.2126 * l[0] + 0.7152 * l[1] + 0.0722 * l[2]; };
          var contrast = function (el) {
            if (!el) return 0;
            var a = luminance(over(rgba(getComputedStyle(el).color), ground)), b = luminance(ground);
            return Math.round((Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05) * 100) / 100;
          };
          var colour = function (c) { return "rgb(" + c.map(Math.round).join(", ") + ")"; };
          var icon = Array.prototype.filter.call(card.querySelectorAll(":scope > i"), shown)[0];
          var texts = card.querySelectorAll("p"), close = card.querySelector('button[data-kitwork-event*="close()"]');
          var closeIcon = close && close.querySelector("i");
          var box = function (el) { if (!el) return "-"; var r = el.getBoundingClientRect(); return Math.round(r.left) + "," + Math.round(r.top) + " " + Math.round(r.width) + "x" + Math.round(r.height); };
          var r = card.getBoundingClientRect();
          out.notice = Array.prototype.filter.call(card.querySelectorAll(":scope > i"), shown).map(function (i) {
            return (i.className.match(/icon-([a-z0-9-]+)/) || [])[1] + (i.getAttribute("aria-label") ? ":" + i.getAttribute("aria-label") : "");
          }).join("+") + "|" + (texts[0] ? texts[0].textContent.trim() : "") + "|" + (texts[1] ? texts[1].textContent.trim() : "");
          out.parts = "card " + box(card) + " · icon " + box(icon) + " · title " + box(texts[0]) + " · words " + box(texts[1]) + " · close " + box(close) + " (its icon " + box(closeIcon) + ")";
          out.x = r.left; out.y = r.top; out.width = r.width; out.height = r.height;
          out.inside = r.left >= 0 && r.top >= 0 && r.right <= innerWidth && r.bottom <= innerHeight;
          out.edge = r.bottom <= innerHeight && innerHeight - r.bottom <= 24 ? "bottom" : r.top >= 0 && r.top <= 24 ? "top" : "";
          out.start = r.left <= 24;
          out.ground = colour(ground);
          out.ink = colour(over(rgba(getComputedStyle(texts[0]).color), ground)) + " / " + colour(over(rgba(getComputedStyle(texts[1]).color), ground));
          out.contrast = { title: contrast(texts[0]), words: contrast(texts[1]), icon: contrast(icon), close: contrast(closeIcon) };
          // the toast's notices on screen, and how much of them the network notice covers (px²)
          var toast = document.querySelector('[data-kitwork-component^="toast"]');
          var rows = toast ? Array.prototype.filter.call(toast.children, function (c) { return c.hasAttribute("data-kit-key") && !c.hasAttribute("data-leaving"); }) : [];
          out.toasts = rows.length;
          out.toast = rows.map(box).join(" ; ");
          out.covered = rows.reduce(function (sum, row) {
            var t = row.getBoundingClientRect();
            return sum + Math.max(0, Math.min(r.right, t.right) - Math.max(r.left, t.left)) * Math.max(0, Math.min(r.bottom, t.bottom) - Math.max(r.top, t.top));
          }, 0);
        }
        done(JSON.stringify(out));
      }); }); });
    })();
  });
})`

// exampleNetworkCentre says the middle of the first element on screen a selector finds, in the
// screen's pixels, for a click as a person makes it ("" when none is on screen).
const exampleNetworkCentre = `(function (selector) {
  var found = Array.prototype.filter.call(document.querySelectorAll(selector), function (el) {
    var r = el.getBoundingClientRect();
    return r.width > 0 && r.height > 0 && r.bottom > 0 && r.top < innerHeight && el.checkVisibility();
  })[0];
  if (!found) return "";
  var r = found.getBoundingClientRect();
  return JSON.stringify({ x: r.left + r.width / 2, y: r.top + r.height / 2 });
})`

// exampleNetworkParts finds the classes of the network's host on a page the engine wrote.
var exampleNetworkParts = regexp.MustCompile(`<div class="([^"]*)" data-kitwork-component="network: \$network"([^>]*)>`)

// The times overlay.ui writes on the example's network host (delay="1s" duration="4s"), in milliseconds,
// as the page carries them to the instance.
const (
	exampleNetworkDelay    = 1000
	exampleNetworkDuration = 4000
)

// The network notice of the kitwork example, drawn by a real browser whose network DevTools takes away
// and gives back: overlay.ui draws it once for the whole site, at the bottom start, its icon, title and
// words, and a close button, the network counted gone after a second (delay="1s") and "Back online"
// said for four (duration="4s"); Drive moves between pages (router.reload(false)).
//   - the page carries both times to the instance, in milliseconds;
//   - online as a page starts, nothing shows;
//   - offline: the notice opens after the delay, not before it is over and within 600ms after;
//     wifi-off named Offline, "You are offline", inside the screen at the bottom start, its title and
//     words 4.5:1 and its icons 3:1 against the card, the page not scrolling sideways;
//   - closed with its button, then a link of the navigation clicked (on a phone, in the menu): the page
//     stays (its path, the move over) and the notice opens again at once: the visitor tried something;
//   - online again: wifi named Online, "Back online", in place; it stays for the duration and closes by
//     itself after: open a second before the four are over, closed just after (read once, at the
//     desktop's width in light);
//   - on /components, offline (the notice opening after the delay), with a toast said from the demo
//     section: the network notice covers none of the toast's; where their columns would meet (under
//     52rem: a tablet, a phone) it stands at the top while the toast holds a notice, and goes back to
//     the bottom once the toast is empty.
//
// At a desktop's width (1280×800) and a phone's (390×844), light and dark (the toast also at a tablet's,
// 768×1024); the pictures of the whole
// screen differ where the scheme or the width does, and are saved to KITWORK_NETWORK_SHOTS when it names
// a folder. What each shot measures is logged.
func TestBrowserExampleNetworkShots(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the network screenshots in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	// booted through /components (served with no warning, its toast demo is used below); overlay.ui is
	// on every page
	tenant, page := examplePage(t, "/components")
	host := exampleNetworkParts.FindStringSubmatch(page)
	if host == nil || !strings.HasSuffix(host[1], " network network-bottom-start") || !strings.Contains(host[2], `role="status"`) {
		t.Fatalf("/components has no network host fixed at the bottom start, a status region: %q", host)
	}
	if times := fmt.Sprintf(` data-kitwork-scope="delay: %d; duration: %d"`, exampleNetworkDelay, exampleNetworkDuration); !strings.Contains(host[2], times) {
		t.Fatalf("/components' network host carries %q, want%s: overlay.ui's delay=\"1s\" duration=\"4s\"", host[2], times)
	}
	server := exampleServer(tenant)
	defer server.Close()
	folder := os.Getenv("KITWORK_NETWORK_SHOTS")
	if folder != "" {
		if err := os.MkdirAll(folder, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// a browser lives 40 seconds (openDevtools): each half of the proof has its own
	var d *devtools
	open := func() {
		d = openDevtools(t, browser, server.URL+"/", 1280, 800, "")
		d.loaded(exampleSettle)
		d.call("Network.enable", map[string]any{})
	}
	open()

	// the network as DevTools takes it away: requests fail at once, and the browser says so
	// (navigator.onLine, the window's offline and online events)
	connection := func(online bool) {
		d.call("Network.emulateNetworkConditions", map[string]any{"offline": !online, "latency": 0, "downloadThroughput": -1, "uploadThroughput": -1})
	}
	// media the page is drawn for: the screen ("") or print, in a scheme, with less motion (each
	// notice is pictured as it stands, not on its way in)
	media := func(kind, scheme string) {
		d.call("Emulation.setEmulatedMedia", map[string]any{"media": kind, "features": []map[string]string{
			{"name": "prefers-color-scheme", "value": scheme},
			{"name": "prefers-reduced-motion", "value": "reduce"},
		}})
	}
	// a page in a scheme, loaded while online (its appearance is painted before the first frame); the
	// old document is marked, so a read that reaches it before the navigation is tried again
	visit := func(path, scheme string) {
		media("", scheme)
		d.evaluate(`window.__stale = true`)
		d.call("Page.navigate", map[string]any{"url": server.URL + path})
		d.loaded(`(window.__stale ? "wait of 0" : ` + exampleSettle + `)`)
	}
	type said struct {
		Reached, Loading, Menu, Dark bool
		Waited                       float64
		Path, Notice, Parts, Bar     string
		Connected, Open, Shown       bool
		X, Y, Width, Height          float64
		Inside, Start                bool
		Edge                         string
		Ground, Ink                  string
		Contrast                     struct{ Title, Words, Icon, Close float64 }
		Toasts                       int
		Toast                        string
		Covered, Overflow            float64
		ScrollX, ScrollY             float64
	}
	// say reads the page (exampleNetworkSay); a page that left while it waited (a move that loaded the
	// next page whole, offline: the browser's error page) is told with where it went
	say := func(want string) said {
		t.Helper()
		var got struct {
			Result struct {
				Value string `json:"value"`
			} `json:"result"`
		}
		result, problem := d.try("Runtime.evaluate", map[string]any{"expression": exampleNetworkSay + `(` + strconv.Quote(want) + `)`, "awaitPromise": true, "returnByValue": true})
		if problem != "" {
			where, _ := d.try("Runtime.evaluate", map[string]any{"expression": "location.href", "returnByValue": true})
			_ = json.Unmarshal(where, &got)
			t.Fatalf("waiting for the notice %q, the page left (%s): it is now %s", want, problem, got.Result.Value)
		}
		var out said
		if err := json.Unmarshal(result, &got); err != nil || json.Unmarshal([]byte(got.Result.Value), &out) != nil {
			t.Fatalf("%s: %v", result, err)
		}
		return out
	}
	// press clicks the middle of what a selector finds, as a person does: the mouse moves there, then
	// presses and lets go
	press := func(selector string) {
		t.Helper()
		got := d.evaluate(exampleNetworkCentre + `(` + strconv.Quote(selector) + `)`)
		var at struct{ X, Y float64 }
		if err := json.Unmarshal([]byte(got), &at); err != nil {
			t.Fatalf("nothing on screen is %s", selector)
		}
		for _, kind := range []string{"mouseMoved", "mousePressed", "mouseReleased"} {
			d.call("Input.dispatchMouseEvent", map[string]any{"type": kind, "x": at.X, "y": at.Y, "button": "left", "clickCount": 1})
		}
	}
	pictures := map[string]image.Image{}
	shoot := func(name string, at said, width, height int) {
		t.Helper()
		raw, picture := screenShot(t, d, at.ScrollX, at.ScrollY, float64(width), float64(height))
		pictures[name] = picture
		if folder != "" {
			if err := os.WriteFile(filepath.Join(folder, name+".png"), raw, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		t.Logf("%s: %s | %s | inside %v %s start %v | ground %s ink %s | contrast title %.2f words %.2f icon %.2f close %.2f | toasts %d %s covered %.0fpx² | overflow %.0f | path %s menu %v bar %s | waited %.0fms",
			name, at.Notice, at.Parts, at.Inside, at.Edge, at.Start, at.Ground, at.Ink, at.Contrast.Title, at.Contrast.Words, at.Contrast.Icon, at.Contrast.Close,
			at.Toasts, at.Toast, at.Covered, at.Overflow, at.Path, at.Menu, at.Bar, at.Waited)
	}
	// a shown notice: inside the screen at the start of the edge named, readable, the page not
	// scrolling sideways
	readable := func(name string, at said, edge string) {
		t.Helper()
		if !at.Inside || !at.Start || at.Edge != edge || at.Overflow != 0 {
			t.Errorf("%s: the notice at %.0f,%.0f %.0fx%.0f, inside %v, at the %s, at the start %v, overflow %.0f; want inside the screen at the %s start, no overflow",
				name, at.X, at.Y, at.Width, at.Height, at.Inside, at.Edge, at.Start, at.Overflow, edge)
		}
		if at.Contrast.Title < 4.5 || at.Contrast.Words < 4.5 || at.Contrast.Icon < 3 || at.Contrast.Close < 3 {
			t.Errorf("%s: contrast %+v on %s, want the words 4.5:1 and the icons 3:1", name, at.Contrast, at.Ground)
		}
	}
	// delayed: the notice the network's going opened waited the delay, said right after the network went
	// (the poll's start is a few milliseconds after the browser's offline, and reads every 50ms)
	delayed := func(name string, at said) {
		t.Helper()
		if at.Waited < exampleNetworkDelay-100 || at.Waited > exampleNetworkDelay+600 {
			t.Errorf("%s: the notice opened %.0fms after the network went, want after the delay (%dms), not before it is over and within 600ms after",
				name, at.Waited, exampleNetworkDelay)
		}
	}
	const offline, online = "wifi-off:Offline|You are offline|This page still reads. Links wait for the connection.", "wifi:Online|Back online|Links open again."

	for _, screen := range []struct {
		width, height int
		scheme        string
	}{{1280, 800, "light"}, {1280, 800, "dark"}, {390, 844, "light"}, {390, 844, "dark"}} {
		suffix := screen.scheme + "-" + strconv.Itoa(screen.width)
		connection(true)
		emulate(d, screen.width, screen.height)
		visit("/", screen.scheme)
		if at := say(""); at.Open || at.Shown || !at.Connected || at.Dark != (screen.scheme == "dark") {
			t.Errorf("%s, online as it starts: %+v, want no notice", suffix, at)
		}

		// offline: the notice opens once the delay is over
		connection(false)
		at := say("offline")
		if !at.Reached || at.Notice != offline || at.Path != "/" {
			t.Errorf("offline-%s: %q on %s, want %q", suffix, at.Notice, at.Path, offline)
		}
		delayed("offline-"+suffix, at)
		readable("offline-"+suffix, at, "bottom")
		shoot("offline-"+suffix, at, screen.width, screen.height)
		if suffix == "light-1280" { // printed, the open notice is not on every sheet
			media("print", screen.scheme)
			if shown := d.evaluate(`getComputedStyle(document.querySelector('[data-kitwork-component^="network"]')).display`); shown != "none" {
				t.Errorf("offline-%s, printed: the network host is display %s, want none", suffix, shown)
			}
			media("", screen.scheme)
		}

		// closed, then a link of the navigation: the page stays, the notice opens again
		press(`[data-kitwork-component^="network"] button[data-kitwork-event*="close()"]`)
		if closed := say("closed"); !closed.Reached {
			t.Errorf("link-%s: the close button left the notice open: %+v", suffix, closed)
		}
		if screen.width < 768 {
			press(`[data-kitwork-component^="dropdown"] > button`) // the navigation is in the menu
		}
		press(`a[href="/docs"]`)
		if again := say("offline"); !again.Reached || again.Waited > 500 {
			t.Errorf("link-%s: the notice opened again %.0fms after the click (reached %v), want at once: Drive's stay does not wait the delay", suffix, again.Waited, again.Reached)
		}
		// the move's bar runs to its end (100%, then gone in 300ms): pictured once it is gone
		d.evaluate(`new Promise(function (done) { var start = Date.now(); (function poll() { if (kit.progress.active && Date.now() - start < 2000) setTimeout(poll, 50); else done(""); })(); })`)
		at = say("offline")
		if !at.Reached || at.Notice != offline || at.Path != "/" || at.Loading || at.Bar != "idle" {
			t.Errorf("link-%s: %q on %s, moving %v, bar %s; want the page to stay on / with the move over, and the notice open again: %q",
				suffix, at.Notice, at.Path, at.Loading, at.Bar, offline)
		}
		readable("link-"+suffix, at, "bottom")
		shoot("link-"+suffix, at, screen.width, screen.height)

		// online again: back online, then (once: it takes the duration, four seconds) it closes by itself
		connection(true)
		at = say("online")
		if !at.Reached || at.Notice != online {
			t.Errorf("online-%s: %q, want %q", suffix, at.Notice, online)
		}
		readable("online-"+suffix, at, "bottom")
		shoot("online-"+suffix, at, screen.width, screen.height)
		if suffix != "light-1280" {
			continue
		}
		// open read a second before the duration is over and just after, counted from the notice read
		// (some tens of milliseconds after online was said)
		if open := d.evaluate(fmt.Sprintf(`new Promise(function (done) {
  var open = [];
  setTimeout(function () { open.push(kit.scope.$network.open); }, %d);
  setTimeout(function () { open.push(kit.scope.$network.open); done(open.join(" ")); }, %d);
})`, exampleNetworkDuration-1000, exampleNetworkDuration+300)); open != "true false" {
			t.Errorf("online-%s: open is %s a second before the duration (%dms) is over and just after, want true false: it stays for the duration, then closes by itself",
				suffix, open, exampleNetworkDuration)
		}
	}
	for _, pair := range [][2]string{{"offline-light-1280", "offline-dark-1280"}, {"offline-light-390", "offline-dark-390"}, {"offline-light-1280", "online-light-1280"}} {
		if samePicture(pictures[pair[0]], pictures[pair[1]]) {
			t.Errorf("%s and %s are drawn the same", pair[0], pair[1])
		}
	}

	// offline on /components with a toast said: two notices that want the bottom edge. Two corners' 26rem
	// columns meet under 52rem (a tablet's 768px), and under 640px both span the screen: there, while the
	// toast holds a notice at the bottom, the network's goes to the top; once the toast is empty it comes
	// back. From 52rem both stay at the bottom, side by side.
	open()
	for _, screen := range []struct {
		width, height int
		scheme        string
	}{{1280, 800, "light"}, {768, 1024, "light"}, {390, 844, "light"}, {390, 844, "dark"}} {
		name := fmt.Sprintf("toast-%s-%d", screen.scheme, screen.width)
		edge := "bottom"
		if screen.width < 832 {
			edge = "top"
		}
		connection(true)
		emulate(d, screen.width, screen.height)
		visit("/components", screen.scheme)
		d.evaluate(`document.getElementById("toast").scrollIntoView({ block: "center" })`)
		connection(false)
		delayed(name, say("offline"))
		d.evaluate(`(function () {
  Array.prototype.filter.call(document.querySelectorAll("#toast button"), function (b) { return b.textContent.trim() === "Warning"; })[0].click();
  return new Promise(function (done) { setTimeout(function () { done(""); }, 350); });
})()`)
		at := say("offline")
		if !at.Reached || at.Notice != offline || at.Toasts != 1 {
			t.Errorf("%s: %q with %d toasts, want the network notice and one toast", name, at.Notice, at.Toasts)
		}
		readable(name, at, edge)
		shoot(name, at, screen.width, screen.height)
		if at.Covered > 0 {
			t.Errorf("%s: the network notice (%s) covers %.0fpx² of the toast's (%s)", name, at.Parts, at.Covered, at.Toast)
		}
		d.evaluate(`(function () { kit.scope.$toast.clear(); return new Promise(function (done) { setTimeout(function () { done(""); }, 400); }); })()`)
		at = say("offline")
		if at.Toasts != 0 {
			t.Errorf("%s, the toast cleared: %d notices left", name, at.Toasts)
		}
		readable(name+", the toast cleared", at, "bottom")
		shoot("toast-gone-"+screen.scheme+"-"+strconv.Itoa(screen.width), at, screen.width, screen.height)
	}
	connection(true)
}
