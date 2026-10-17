package work

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// exampleWindow measures the window of /components at the screen it is drawn on: how many terminals the
// section holds, whether the tablist is drawn and sits in the window's bar, the file drawn (its code's
// language), how many Copy buttons a person sees and whether the one there sits in the bar beside the
// tablist (not in it), the underline of each tab, the scheme, and where the window is on the page (to
// picture it alone).
const exampleWindow = `(function () {
  var win = document.getElementById("tabs").lastElementChild;
  var bar = win.firstElementChild;
  var tablist = win.querySelector('[role="tablist"]');
  win.scrollIntoView({ block: "center" });
  return new Promise(function (done) { requestAnimationFrame(function () { requestAnimationFrame(function () {
    var drawn = function (el) { return getComputedStyle(el).display !== "none" && el.getClientRects().length > 0; };
    var shown = Array.prototype.filter.call(win.querySelectorAll('[role="tabpanel"]'), drawn);
    var copies = Array.prototype.filter.call(win.querySelectorAll("button"), function (b) { return drawn(b) && /Copy/.test(b.textContent); });
    var r = win.getBoundingClientRect();
    done(JSON.stringify({
      terminals: document.querySelectorAll('#tabs [data-kitwork-component^="terminal"]').length,
      tabs: drawn(tablist) && bar.contains(tablist),
      shown: shown.map(function (panel) { return panel.querySelector("code").className; }).join(" "),
      copies: copies.length,
      bar: copies.length === 1 && bar.contains(copies[0]) && !tablist.contains(copies[0]),
      cue: Array.prototype.map.call(tablist.querySelectorAll('[role="tab"]'), function (b) { return b.value + ":" + getComputedStyle(b).textDecorationLine; }).join(" "),
      dark: matchMedia("(prefers-color-scheme: dark)").matches,
      x: r.left + scrollX - 16, y: r.top + scrollY - 16, width: r.width + 32, height: r.height + 32 }));
  }); }); });
})()`

// exampleFind is what find-in-page reaches: a word only router.work holds, then one only page.ui holds.
const exampleFind = `(function () {
  var found = function (word) { getSelection().removeAllRanges(); return window.find(word, true, false, true); };
  var out = [found("ctx.title"), found("drink.price")].join(" ");
  getSelection().removeAllRanges();
  return out;
})()`

// exampleCopies presses the Copy a person sees and says what reached the clipboard: how many copies, and
// the tab whose file each is, exactly ("" when it is no one file: both joined, a prompt, a part).
const exampleCopies = `(function () {
  var copied = [];
  Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText: function (text) { copied.push(text); return Promise.resolve(); } } });
  var win = document.getElementById("tabs").lastElementChild;
  var panels = Array.prototype.slice.call(win.querySelectorAll('[role="tabpanel"]'));
  Array.prototype.forEach.call(win.querySelectorAll("button"), function (b) {
    if (getComputedStyle(b).display !== "none" && b.getClientRects().length > 0 && /Copy/.test(b.textContent)) b.click();
  });
  return new Promise(function (done) { setTimeout(function () {
    done(copied.length + " " + copied.map(function (text) {
      return panels.filter(function (panel) { return panel.querySelector("code").textContent === text; }).map(function (panel) {
        return document.getElementById(panel.getAttribute("aria-labelledby")).value;
      }).join(",");
    }).join(" "));
  }, 300); });
})()`

// exampleTypist is what the typing probes below read the window with: the file drawn (its code's
// language), how many highlight ranges typing holds (0 when all is shown), where typing stands (the
// file the untyped range starts in and whether any of that file is typed before it, "all" when nothing
// is untyped), two frames, a tab by its name, and the <pre> of router.work.
const exampleTypist = `
  var win = document.getElementById("tabs").lastElementChild;
  var drawn = function (el) { return getComputedStyle(el).display !== "none" && el.getClientRects().length > 0; };
  var file = function () { return Array.prototype.filter.call(win.querySelectorAll('[role="tabpanel"]'), drawn).map(function (panel) { return panel.querySelector("code").className; }).join(" "); };
  var count = function () {
    var n = 0;
    ["kitwork-terminal-untyped", "kitwork-terminal-caret"].forEach(function (name) { var h = CSS.highlights.get(name); if (h) n += h.size; });
    return n;
  };
  var untyped = function () {
    var found = null, h = CSS.highlights.get("kitwork-terminal-untyped");
    if (h) h.forEach(function (range) { if (win.contains(range.startContainer)) found = range; });
    if (!found) return "all";
    var code = found.startContainer.parentElement.closest("code");
    if (!code) return "outside";
    var before = document.createRange();
    before.setStart(code, 0);
    before.setEnd(found.startContainer, found.startOffset);
    return code.className + " " + (before.toString().length === 0 ? "nothing typed" : "typing");
  };
  var frames = function (then) { requestAnimationFrame(function () { requestAnimationFrame(then); }); };
  var tab = function (name) { return win.querySelector('[role="tab"][value="' + name + '"]'); };
  var routerPre = win.querySelector("code.language-work").parentElement;
`

// exampleTyping reads typing in the window with motion allowed, from a load with the window far below
// the screen: what waits (the untyped range at the router file's first character), what types once the
// window is scrolled to (the router file, some of it typed), what a click on page.ui shows two frames
// later (the page file, every highlight range gone: all of it at once), the same 600ms later (typing
// did not start again on it), and what a click back on router.work shows (the router file, all of it).
// A range is told by the file it stands in and how many of that file's characters come before it.
const exampleTyping = `(function () {` + exampleTypist + `
  var out = [];
  return new Promise(function (done) {
    var top = win.getBoundingClientRect().top;
    out.push("waits " + (top > innerHeight ? "" : "on screen ") + untyped());
    win.scrollIntoView({ block: "center" });
    var start = Date.now();
    (function poll() {
      if (untyped() !== "language-work typing" && Date.now() - start < 3000) { setTimeout(poll, 20); return; }
      out.push("types " + untyped());
      tab("page").click();
      frames(function () {
        out.push("switched " + file() + " " + untyped() + " " + count());
        setTimeout(function () {
          out.push("later " + file() + " " + untyped() + " " + count());
          tab("router").click();
          frames(function () {
            out.push("back " + file() + " " + untyped() + " " + count());
            done(out.join(" | "));
          });
        }, 600);
      });
    })();
  });
})()`

// examplePartly scrolls the window, from a load far above it, until its bar and a little of its first
// panel are on screen — a third of it, less than the half typing waits for — and says, 600ms later
// (twice typing's first wait), whether it is so and where typing stands, with the middle of the
// page.ui tab (where a real click lands).
const examplePartly = `(function () {` + exampleTypist + `
  var box = win.getBoundingClientRect();
  scrollBy(0, box.top - (innerHeight - Math.round(box.height / 3)));
  return new Promise(function (done) { setTimeout(function () {
    var r = win.getBoundingClientRect(), b = tab("page").getBoundingClientRect(), seen = innerHeight - r.top;
    var partly = seen > 0 && seen < r.height / 2 && seen < innerHeight / 2 && b.top >= 0 && b.bottom <= innerHeight;
    done(JSON.stringify({ waits: (partly ? "partly " : "not partly ") + untyped(), x: b.left + b.width / 2, y: b.top + b.height / 2 }));
  }, 600); });
})()`

// exampleSeen says, two frames after a switch, the file drawn and where typing stands, then scrolls the
// window to the middle of the screen and says the same 700ms later: past typing's first wait (300ms),
// well before its end (2s).
const exampleSeen = `(function () {` + exampleTypist + `
  return new Promise(function (done) { frames(function () {
    var out = ["switched " + file() + " " + untyped() + " " + count()];
    win.scrollIntoView({ block: "center" });
    setTimeout(function () { out.push("seen " + file() + " " + untyped() + " " + count()); done(out.join(" | ")); }, 700);
  }); });
})()`

// exampleRewind scrolls the window to the middle of a phone's screen and lets router.work type; once
// its <pre> has scrolled sideways after the caret, typing still under way, it does then — "switch":
// page.ui chosen, router.work again two frames later; "copy": Copy pressed — and says, two frames
// after, the file drawn, where typing stands and how far router.work's <pre> is scrolled.
const exampleRewind = `(function (then) {` + exampleTypist + `
  Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText: function () { return Promise.resolve(); } } });
  win.scrollIntoView({ block: "center" });
  var start = Date.now();
  return new Promise(function (done) {
    (function poll() {
      if (!(routerPre.scrollLeft > 0 && untyped() === "language-work typing") && Date.now() - start < 3000) { setTimeout(poll, 10); return; }
      var out = ["followed " + (routerPre.scrollLeft > 0) + " " + untyped()];
      if (then === "copy") win.firstElementChild.querySelector('button:not([role="tab"])').click();
      else tab("page").click();
      frames(function () {
        if (then === "switch") tab("router").click();
        frames(function () {
          out.push(file() + " " + untyped() + " " + count() + " scrolled " + routerPre.scrollLeft);
          done(out.join(" | "));
        });
      });
    })();
  });
})`

// The window of the kitwork example's /components — the two files of a tiny site, router.work and
// page.ui, in one terminal — drawn by a real browser, light and dark, at a desktop's width and a
// phone's, on each tab:
//   - one terminal: its bar holds the tablist and one Copy, beside the tablist, not in it; the tab
//     chosen shows its file and hides the other, so find-in-page reaches only the one shown; the tab
//     chosen is told by more than its fill (underlined, in its text's colour: WCAG 1.4.11);
//   - the one Copy copies exactly the file shown: router.work's on the first tab, page.ui's once its
//     tab is chosen, never both;
//   - with motion allowed, typing waits while the window is off screen, plays on router.work once it is
//     on screen, and a click on page.ui shows that file at once (no highlight range left), and it is not
//     typed afterwards; back on router.work, that file is all there too: typing plays once;
//   - a switch the reader makes (a real click, a real key) while only the bar and a little of the window
//     are on screen, typing still waiting, shows page.ui at once too, never typed; a switch nobody made
//     (from a script, as a restore is) plans typing again on page.ui;
//   - on a phone, router.work's <pre>, scrolled sideways after the caret, reads from its first column
//     again after a switch away and back while it types, as after a Copy;
//   - 375, 320 and 280px (reflow, WCAG 1.4.10): the page does not scroll sideways, and every button of
//     the bar sits inside its padding with its whole label: the bar may wrap, Copy is never pushed out.
//
// The pictures differ — the file shown and the scheme change what is drawn — and are saved to
// KITWORK_TABS_SHOTS when it names a folder (window-<scheme>-<width>-<tab>.png).
func TestBrowserExampleTabsWindowShots(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the tabs window screenshots in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant, _ := examplePage(t, "/components")
	server := exampleServer(tenant)
	defer server.Close()

	folder := os.Getenv("KITWORK_TABS_SHOTS")
	d := openDevtools(t, browser, server.URL+"/components", 1280, 900, "")
	d.loaded(exampleSettle)
	shots := map[string]image.Image{}
	for _, scheme := range []string{"light", "dark"} {
		d.call("Emulation.setEmulatedMedia", map[string]any{"features": []map[string]string{
			{"name": "prefers-color-scheme", "value": scheme},
			{"name": "prefers-reduced-motion", "value": "reduce"},
		}})
		for _, width := range []int{1280, 375} {
			for _, screen := range []struct {
				tab   string // the tab clicked first, "" for none
				want  string
				found string
			}{
				{"", `"terminals":1,"tabs":true,"shown":"language-work","copies":1,"bar":true,"cue":"router:underline page:none"`, "true false"},
				{"page", `"terminals":1,"tabs":true,"shown":"language-ui","copies":1,"bar":true,"cue":"router:none page:underline"`, "false true"},
			} {
				emulate(d, width, 900)
				d.call("Page.reload", map[string]any{"ignoreCache": true})
				d.loaded(exampleSettle)
				if screen.tab != "" {
					d.evaluate(`document.querySelector('#tabs [role="tab"][value="` + screen.tab + `"]').click()`)
				}
				found := d.evaluate(exampleWindow)
				var where struct {
					Dark                bool
					X, Y, Width, Height float64
				}
				if err := json.Unmarshal([]byte(found), &where); err != nil {
					t.Fatalf("%s: %v", found, err)
				}
				name := scheme + "-" + strconv.Itoa(width) + "-" + map[string]string{"": "router", "page": "page"}[screen.tab]
				if !strings.Contains(found, screen.want) || where.Dark != (scheme == "dark") {
					t.Errorf("%s: %s\n want %s, dark %v", name, found, screen.want, scheme == "dark")
				}
				if got := d.evaluate(exampleFind); got != screen.found {
					t.Errorf("%s: find-in-page reaches ctx.title, drink.price: %s, want %s", name, got, screen.found)
				}
				d.evaluate(exampleWindow) // back to the window: find-in-page may have scrolled
				raw, picture := clipShot(t, d, where.X, where.Y, where.Width, where.Height)
				shots[name] = picture
				if folder != "" {
					if err := os.WriteFile(filepath.Join(folder, "window-"+name+".png"), raw, 0o644); err != nil {
						t.Fatal(err)
					}
				}
				want := "1 " + map[string]string{"": "router", "page": "page"}[screen.tab]
				if got := d.evaluate(exampleCopies); got != want {
					t.Errorf("%s: the Copy in the bar put on the clipboard %q, want the file shown alone (%s)", name, got, want)
				}
			}
		}
	}
	for _, pair := range [][2]string{
		{"light-1280-router", "light-1280-page"}, {"dark-1280-router", "dark-1280-page"},
		{"light-375-router", "light-375-page"}, {"dark-375-router", "dark-375-page"},
		{"light-1280-router", "dark-1280-router"}, {"light-375-page", "dark-375-page"},
	} {
		if samePicture(shots[pair[0]], shots[pair[1]]) {
			t.Errorf("the window %s and %s are drawn the same", pair[0], pair[1])
		}
	}

	// Typing, with motion allowed, from a load at the top of the page, the window far below.
	emulate(d, 1280, 900)
	d.call("Emulation.setEmulatedMedia", map[string]any{"features": []map[string]string{
		{"name": "prefers-color-scheme", "value": "light"},
		{"name": "prefers-reduced-motion", "value": "no-preference"},
	}})
	d.evaluate(`scrollTo(0, 0)`)
	d.call("Page.reload", map[string]any{"ignoreCache": true})
	d.loaded(exampleSettle)
	if got, want := d.evaluate(exampleTyping), "waits language-work nothing typed | types language-work typing | "+
		"switched language-ui all 0 | later language-ui all 0 | back language-work all 0"; got != want {
		t.Errorf("typing in the window: %s\n want %s", got, want)
	}

	// A switch before typing's clock has run, the window's bar and a little of its panel on screen (less
	// than half: typing still waits). Made by the reader — a real click on page.ui, or ArrowRight on
	// router.work focused without scrolling — it shows page.ui at once, and page.ui is not typed when the
	// window is then scrolled to. Made by nobody — page.ui chosen from a script, no click, no key, as a
	// tab the browser kept is restored as the kernel starts — it plans typing again on page.ui, which
	// types once the window is seen.
	for _, early := range []struct{ by, want string }{
		{"click", "switched language-ui all 0 | seen language-ui all 0"},
		{"key", "switched language-ui all 0 | seen language-ui all 0"},
		{"script", "switched language-ui language-ui nothing typed 2 | seen language-ui language-ui typing 2"},
	} {
		d.evaluate(`scrollTo(0, 0)`)
		d.call("Page.reload", map[string]any{"ignoreCache": true})
		d.loaded(exampleSettle)
		var at struct {
			Waits string
			X, Y  float64
		}
		if got := d.evaluate(examplePartly); json.Unmarshal([]byte(got), &at) != nil || at.Waits != "partly language-work nothing typed" {
			t.Errorf("before the %s: %s, want the window partly on screen, typing waiting", early.by, got)
			continue
		}
		switch early.by {
		case "click":
			for _, kind := range []string{"mousePressed", "mouseReleased"} {
				d.call("Input.dispatchMouseEvent", map[string]any{"type": kind, "x": at.X, "y": at.Y, "button": "left", "clickCount": 1})
			}
		case "key":
			d.evaluate(`document.querySelector('#tabs [role="tab"][value="router"]').focus({ preventScroll: true })`)
			for _, kind := range []string{"rawKeyDown", "keyUp"} {
				d.call("Input.dispatchKeyEvent", map[string]any{"type": kind, "key": "ArrowRight", "code": "ArrowRight", "windowsVirtualKeyCode": 39})
			}
		case "script":
			d.evaluate(`kit.scopeFor(document.getElementById("tabs")).$files.select("page")`)
		}
		if got := d.evaluate(exampleSeen); got != early.want {
			t.Errorf("a switch by %s before typing's clock ran: %s\n want %s", early.by, got, early.want)
		}
	}

	// On a phone, router.work's <pre> scrolls sideways after the caret as it types. Switched away from and
	// back to while it types, it reads from its first column again, as it does once typing ends or after
	// a Copy (pressed while it types, its <pre> in sight: the control).
	emulate(d, 375, 812)
	for _, then := range []string{"switch", "copy"} {
		d.evaluate(`scrollTo(0, 0)`)
		d.call("Page.reload", map[string]any{"ignoreCache": true})
		d.loaded(exampleSettle)
		if got, want := d.evaluate(exampleRewind+`("`+then+`")`), "followed true language-work typing | language-work all 0 scrolled 0"; got != want {
			t.Errorf("router.work at 375px, %s while it types: %s\n want %s", then, got, want)
		}
	}

	// As wide as a phone (375px), as narrow as reflow asks (320px), and narrower (280px): the page does not
	// overflow sideways (a phone's layout viewport grows to what overflows it, so the width is the screen's,
	// not innerWidth), and every button of the bar is inside its padding with its whole label (none
	// squeezed narrower than its words). The bar may wrap: Copy goes under the tabs, never out.
	for _, width := range []int{375, 320, 280} {
		emulate(d, width, 812)
		narrow := d.evaluate(`(function (width) {
  var win = document.getElementById("tabs").lastElementChild;
  win.scrollIntoView({ block: "center" });
  return new Promise(function (done) { requestAnimationFrame(function () { requestAnimationFrame(function () {
    var bar = win.firstElementChild, tops = {}, box = bar.getBoundingClientRect(), style = getComputedStyle(bar), inside = true, whole = true;
    var left = box.left + parseFloat(style.paddingLeft), right = box.right - parseFloat(style.paddingRight);
    Array.prototype.forEach.call(bar.querySelectorAll("button"), function (b) {
      var r = b.getBoundingClientRect();
      tops[Math.round(r.top)] = true;
      inside = inside && r.left >= left - 0.5 && r.right <= right + 0.5;
      whole = whole && b.scrollWidth <= b.clientWidth;
    });
    var widest = Math.max(document.documentElement.scrollWidth, innerWidth);
    done(JSON.stringify({ overflow: Math.round(widest - width), inside: inside, whole: whole, lines: Object.keys(tops).length }));
  }); }); });
})(` + strconv.Itoa(width) + `)`)
		if want := `{"overflow":0,"inside":true,"whole":true,`; !strings.HasPrefix(narrow, want) {
			t.Errorf("%dpx wide: %s, want %s…", width, narrow, want)
		}
		t.Logf("%dpx wide: %s", width, narrow)
	}
}

// The kitwork example's /docs/getting-started gives the one step whose commands differ between shells —
// setting ALLOW_LOCAL and PORT for the session — in two tabs, bash and PowerShell, with persist="true":
// the choice is kept under the set of their names (bash+powershell), through kit.storage, so the
// reader's shell is shown on every page with the same tabs, on every visit. In a real browser: nothing
// kept, bash is shown; PowerShell clicked, it is shown and kit.storage keeps "powershell" under
// tabs:bash+powershell (in localStorage, kitwork:tabs:bash+powershell); reloaded, PowerShell is shown,
// chosen and the tab in the tab order. The step card is pictured each time (shell-before.png,
// shell-powershell.png, shell-reloaded.png in KITWORK_TABS_SHOTS), and the pictures differ where the
// shell does.
func TestBrowserExampleShellTabsPersist(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the shell tabs proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant, page := examplePage(t, "/docs/getting-started")
	if !strings.Contains(page, `data-kitwork-persist="bash+powershell"`) {
		t.Fatalf("/docs/getting-started has no pre-paint of the shell tabs:\n%.3000s", page)
	}
	server := exampleServer(tenant)
	defer server.Close()

	folder := os.Getenv("KITWORK_TABS_SHOTS")
	d := openDevtools(t, browser, server.URL+"/docs/getting-started", 1280, 900, "")
	d.loaded(exampleSettle)
	d.call("Emulation.setEmulatedMedia", map[string]any{"features": []map[string]string{{"name": "prefers-reduced-motion", "value": "reduce"}}})
	d.evaluate(`localStorage.clear()`)
	const card = `(function () {
  var tablist = document.querySelector('[role="tablist"][aria-label="Shell"]');
  var card = tablist.closest("li");
  card.scrollIntoView({ block: "center" });
  return new Promise(function (done) { requestAnimationFrame(function () { requestAnimationFrame(function () {
    var shown = Array.prototype.filter.call(card.querySelectorAll('[role="tabpanel"]'), function (panel) { return getComputedStyle(panel).display !== "none"; });
    var r = card.getBoundingClientRect();
    done(JSON.stringify({
      state: shown.map(function (panel) { return panel.querySelector("code").className; }).join(" ") + " " +
        Array.prototype.map.call(tablist.querySelectorAll('[role="tab"]'), function (b) { return b.value + ":" + b.getAttribute("aria-selected") + b.getAttribute("tabindex"); }).join(" ") +
        " kept " + localStorage.getItem("kitwork:tabs:bash+powershell"),
      x: r.left + scrollX - 8, y: r.top + scrollY - 8, width: r.width + 16, height: r.height + 16 }));
  }); }); });
})()`
	pictures := map[string]image.Image{}
	for _, step := range []struct {
		name, do, want string
	}{
		{"before", "location.reload()", `language-bash bash:true0 powershell:false-1 kept null`},
		{"powershell", `document.querySelector('[role="tab"][value="powershell"]').click()`, `language-powershell bash:false-1 powershell:true0 kept "powershell"`},
		{"reloaded", "location.reload()", `language-powershell bash:false-1 powershell:true0 kept "powershell"`},
	} {
		if step.do == "location.reload()" {
			d.call("Page.reload", map[string]any{"ignoreCache": true})
			d.loaded(exampleSettle)
		} else {
			d.evaluate(step.do)
		}
		got := d.evaluate(card)
		var where struct {
			State               string
			X, Y, Width, Height float64
		}
		if err := json.Unmarshal([]byte(got), &where); err != nil {
			t.Fatalf("%s: %v", got, err)
		}
		if where.State != step.want {
			t.Errorf("%s: %s, want %s", step.name, where.State, step.want)
		}
		raw, picture := clipShot(t, d, where.X, where.Y, where.Width, where.Height)
		pictures[step.name] = picture
		if folder != "" {
			if err := os.WriteFile(filepath.Join(folder, "shell-"+step.name+".png"), raw, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	if samePicture(pictures["before"], pictures["powershell"]) {
		t.Error("the step is drawn the same with bash and with PowerShell chosen")
	}
}

// exampleSettle waits for the fonts and two frames, and says where the page is.
const exampleSettle = `document.fonts.ready.then(function () { return new Promise(function (done) { requestAnimationFrame(function () { requestAnimationFrame(function () { done(Math.round(scrollY) + " of " + document.documentElement.scrollHeight); }); }); }); })`

// exampleServer serves the kitwork example's tenant to a browser, one request at a time.
func exampleServer(tenant *Tenant) *httptest.Server {
	var serving sync.Mutex
	return httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		forward := request.Clone(request.Context())
		forward.Host = "kitwork.localhost"
		serving.Lock()
		defer serving.Unlock()
		tenant.Serve(response, forward)
	}))
}

// clipShot pictures a part of the page, beyond the screen if need be: the PNG and the picture.
func clipShot(t *testing.T, d *devtools, x, y, width, height float64) ([]byte, image.Image) {
	t.Helper()
	var shot struct{ Data string }
	if err := json.Unmarshal(d.call("Page.captureScreenshot", map[string]any{"format": "png", "captureBeyondViewport": true,
		"clip": map[string]any{"x": x, "y": y, "width": width, "height": height, "scale": 1}}), &shot); err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(shot.Data)
	if err != nil {
		t.Fatal(err)
	}
	picture, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return raw, picture
}

// samePicture reports whether two pictures are the same size and the same in every pixel.
func samePicture(a, b image.Image) bool {
	if a == nil || b == nil || a.Bounds() != b.Bounds() {
		return a == nil && b == nil
	}
	bounds := a.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r1, g1, b1, a1 := a.At(x, y).RGBA()
			r2, g2, b2, a2 := b.At(x, y).RGBA()
			if r1 != r2 || g1 != g2 || b1 != b2 || a1 != a2 {
				return false
			}
		}
	}
	return true
}
