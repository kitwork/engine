package work

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/kitwork/engine/ui"
	"github.com/kitwork/engine/ui/stylesheet"
)

// scrolledBrowserFiles is a site with Drive on whose / and /hai carry a pinned bar that floats up once
// the page scrolls (scrolled:shadow="small") beside a box that never does, and whose /khong carries no
// scrolled: class at all. Every page is long enough to scroll 600px in a window 800px high.
var scrolledBrowserFiles = map[string]string{
	"localhost/router.work": `import { router } from "kitwork";

router.colors({ brand: "#c8742c", canvas: "#f6f3ee", paper: "#ffffff", ink: "#2b211b" });
router.reload(false);
`,
	"localhost/page.ui": `<page><content /></page>`,
	"localhost/content.ui": `<content>
  <box id="bar" pin="top" padding="4" scrolled:shadow="small"><text>Thanh</text></box>
  <box id="plain" padding="4"><text>Hộp</text></box>
  <link id="next" value="/hai">Hai</link>
  <box height="3000px"></box>
</content>`,
	"localhost/hai/content.ui": `<content>
  <box id="bar" pin="top" padding="4" scrolled:shadow="small"><text>Thanh hai</text></box>
  <box id="plain" padding="4"><text>Hộp</text></box>
  <box height="3000px"></box>
</content>`,
	"localhost/khong/content.ui": `<content>
  <box id="plain" padding="4"><text>Không có scrolled</text></box>
  <link id="next" value="/">Về đầu</link>
  <box height="3000px"></box>
</content>`,
}

// scrolledProbe reads the state after the frames a change needs to reach the flag and the bar's short
// transition to end: the flag on <html>, the bar's and the plain box's shadows in words (none, or small
// when it is the small shadow), and how far the page is scrolled (read through pageYOffset, which the
// spy below leaves alone). It also says what the script made, the mark, and where among the children
// of <html> it went (order: their names, an engine element with its data-kitwork-generated in
// brackets), and spies on the script: every
// read of scrollY or scrollTop whose stack runs through /_kitwork/scrolled. is written down with where
// it ran — in an animation frame ("frame"), in an event (its type, "scroll"), or straight in the
// script's own run ("script"), the read that makes the browser lay the page out before it paints.
const scrolledProbe = `
  var SMALL = "rgba(0, 0, 0, 0.05) 0px 1px 2px 0px";
  function shadow(id) {
    var element = document.getElementById(id);
    if (!element) return id + "=absent";
    var value = getComputedStyle(element).boxShadow;
    return id + "=" + (value === "none" ? "none" : value === SMALL ? "small" : value.replace(/ /g, ""));
  }
  function words() {
    return "flag=" + (document.documentElement.hasAttribute("data-kitwork-scrolled") ? "on" : "off") + " " +
      shadow("bar") + " " + shadow("plain") + " at=" + Math.round(window.pageYOffset);
  }
  function mark() {
    var marks = document.querySelectorAll('[data-kitwork-generated="scrolled"]:not(script)');
    if (marks.length !== 1) return "marks=" + marks.length;
    var element = marks[0], style = getComputedStyle(element), box = element.getBoundingClientRect();
    var focused = document.activeElement;
    element.focus();
    var focusable = document.activeElement === element;
    if (focused && focused.focus) focused.focus();
    return "parent=" + element.parentNode.nodeName.toLowerCase() + " " + style.position + " " + style.visibility +
      " aria-hidden=" + element.getAttribute("aria-hidden") + " focusable=" + focusable +
      " box=" + Math.round(box.left) + "," + Math.round(box.top + window.pageYOffset) + " " + Math.round(box.width) + "x" + Math.round(box.height);
  }
  function order() {
    return Array.prototype.map.call(document.documentElement.children, function (element) {
      return element.nodeName.toLowerCase() + (element.hasAttribute("data-kitwork-generated") ? "[" + element.getAttribute("data-kitwork-generated") + "]" : "");
    }).join(",");
  }
  var reads = [], frameDepth = 0, request = window.requestAnimationFrame;
  window.requestAnimationFrame = function (callback) {
    return request.call(window, function (time) {
      frameDepth++;
      try { callback(time); } finally { frameDepth--; }
    });
  };
  function spy(owner, name) {
    var own = Object.getOwnPropertyDescriptor(owner, name);
    Object.defineProperty(owner, name, { configurable: true, enumerable: own.enumerable, set: own.set, get: function () {
      if ((new Error().stack || "").indexOf("/_kitwork/scrolled.") >= 0) {
        reads.push(name + "@" + (frameDepth > 0 ? "frame" : window.event ? window.event.type : "script"));
      }
      return own.get.call(this);
    } });
  }
  spy(window, "scrollY");
  spy(Element.prototype, "scrollTop");
  function readWords() {
    var seen = {};
    reads.forEach(function (read) { seen[read.split("@")[1]] = true; });
    return reads.length ? "first=" + reads[0] + " where=" + Object.keys(seen).sort().join(",") : "none";
  }
  function frames(count, then) { if (count <= 0) { then(); return; } requestAnimationFrame(function () { frames(count - 1, then); }); }
  function settle(then) { frames(3, function () { setTimeout(then, 300); }); }
  function scrollThen(y, then) { window.scrollTo(0, y); settle(then); }
  function walk(log, steps, done) {
    if (!steps.length) { done(); return; }
    scrollThen(steps[0][1], function () { log[steps[0][0]] = words(); walk(log, steps.slice(1), done); });
  }
  function send(report) {
    var close = function () { window.close(); };
    fetch("/report", { method: "POST", body: JSON.stringify(report) }).then(close, close);
  }
`

// scrolledFromTheTop is the controller of the walk that starts on /: the page is scrolled before
// scrolled.<hash>.js runs, and every scroll event of the walk is swallowed, before the script and after
// it, so nothing the script could listen to ever arrives: the flag follows the page by the observer
// alone. Then the page scrolls to the top, 4px (under the threshold), 8px (the threshold: still off),
// 9px (past it: on), 600px, and a Drive move to /hai, which shows /hai from its top, scrolled again
// there.
const scrolledFromTheTop = `<script>
(function () {` + scrolledProbe + `
  var log = {};
  var swallow = function (event) { event.stopImmediatePropagation(); };
  window.addEventListener("scroll", swallow, true);
  window.scrollTo(0, 600);
  window.addEventListener("load", function () {
    settle(function () {
      var bar = getComputedStyle(document.getElementById("bar"));
      log.loaded = words();
      log.mark = mark();
      log.order = order();
      log.transition = bar.transitionProperty + " " + bar.transitionDuration + " plain=" + getComputedStyle(document.getElementById("plain")).transitionDuration;
      walk(log, [["top", 0], ["wobble", 4], ["edge", 8], ["past", 9], ["scrolled", 600]], function () {
        document.addEventListener("kitwork:load", function () {
          settle(function () {
            log.moved = words();
            log.movedMark = mark();
            walk(log, [["movedScrolled", 600]], function () { log.reads = readWords(); send(log); });
          });
        });
        document.getElementById("next").click();
      });
    });
  });
})();
</script>`

// scrolledWithoutObserver is the controller of the walk on /?fallback, a browser without
// IntersectionObserver: the page is scrolled before the script runs and every scroll event is swallowed
// until the page has loaded, so only the script's own first read can set the flag, and that read must
// come in an animation frame, never in the script's run. Then its scroll listener follows the page:
// the top, 8px, 9px, the top again.
const scrolledWithoutObserver = `<script>
(function () {` + scrolledProbe + `
  var log = {};
  delete window.IntersectionObserver;
  var swallow = function (event) { event.stopImmediatePropagation(); };
  window.addEventListener("scroll", swallow, true);
  window.scrollTo(0, 600);
  window.addEventListener("load", function () {
    settle(function () {
      window.removeEventListener("scroll", swallow, true);
      log.loaded = words();
      log.mark = mark();
      log.start = readWords();
      walk(log, [["top", 0], ["edge", 8], ["past", 9], ["back", 0]], function () { log.reads = readWords(); send(log); });
    });
  });
})();
</script>`

// scrolledWithAnElementRoot is the controller of the walk on /?element-root, a browser whose
// IntersectionObserver takes only an element for a root (before 2020): given the document, it throws,
// once. The script falls back to the implicit root and the flag follows the page as on /: set at the
// start when the page is already scrolled, off at the top and at 8px, on at 9px; and, the page made
// wider than the window, off at its top scrolled 200px to the side (the implicit root reaches past
// both sides too).
const scrolledWithAnElementRoot = `<script>
(function () {` + scrolledProbe + `
  var log = {}, browserObserver = window.IntersectionObserver, refused = 0;
  window.IntersectionObserver = function (callback, options) {
    if (options && options.root && options.root.nodeType === 9) {
      refused++;
      throw new TypeError("the root must be an Element");
    }
    return new browserObserver(callback, options);
  };
  var wide = document.createElement("div");
  wide.style.cssText = "width:3000px;height:1px";
  document.body.appendChild(wide);
  window.scrollTo(0, 600);
  window.addEventListener("load", function () {
    settle(function () {
      log.loaded = words() + " refused=" + refused;
      log.mark = mark();
      walk(log, [["top", 0], ["edge", 8], ["past", 9]], function () {
        window.scrollTo(200, 0);
        settle(function () {
          log.sideways = words() + " x=" + Math.round(window.pageXOffset);
          log.reads = readWords();
          send(log);
        });
      });
    });
  });
})();
</script>`

// scrolledSideways is the controller of the walk on /?wide: the page gets a box 3000px wide, so it
// scrolls sideways in a window 1000px wide. A scroll to the side is not a scroll away from the top: at
// the top, 2px, 200px and 2000px to the side, the flag stays off; 9px down it is on, and back at the
// top, still 200px to the side, off again (a strip 1px wide at the left of the page leaves the view
// after 2px to the side, and while it is out the observer has nothing to say about the top).
const scrolledSideways = `<script>
(function () {` + scrolledProbe + `
  var log = {};
  var wide = document.createElement("div");
  wide.style.cssText = "width:3000px;height:1px";
  document.body.appendChild(wide);
  var steps = [["top", 0, 0], ["nudged", 2, 0], ["sideways", 200, 0], ["sidewaysPast", 200, 9], ["sidewaysBack", 200, 0], ["far", 2000, 0]];
  function next() {
    if (!steps.length) { log.reads = readWords(); send(log); return; }
    var step = steps.shift();
    window.scrollTo(step[1], step[2]);
    settle(function () { log[step[0]] = words() + " x=" + Math.round(window.pageXOffset); next(); });
  }
  window.addEventListener("load", function () {
    settle(function () {
      var page = document.documentElement;
      log.width = "scrollWidth=" + page.scrollWidth + (page.scrollWidth > page.clientWidth ? " wider than the window" : " no wider than the window");
      next();
    });
  });
})();
</script>`

// scrolledAlone is the controller of the walk on /alone: / as Page.Document writes it with no head
// from the engine (a page shown on its own), its stylesheets and the scrolled script inline at the end
// of <head>, a classic script the parser runs where it meets it, before <body> exists. The mark goes
// into <html> there, before <body>, and the flag follows the page as on /: on when the page is already
// scrolled at the start, off at the top and at 8px, on at 9px.
const scrolledAlone = `<script>
(function () {` + scrolledProbe + `
  var log = {};
  window.scrollTo(0, 600);
  window.addEventListener("load", function () {
    settle(function () {
      log.loaded = words();
      log.mark = mark();
      log.order = order();
      walk(log, [["top", 0], ["edge", 8], ["past", 9]], function () { send(log); });
    });
  });
})();
</script>`

// scrolledAroundAFrame is a page of the test's own, not the site's: / in a frame 400px high, 100px
// below its top, with room to scroll past it. The frame's page stays at its own top while the page
// around it scrolls 300px, which takes the frame's top 200px out of the window: the frame's flag stays
// off, for the frame's page is not scrolled. Then the frame's page scrolls 600px itself (on) and back
// to its top (off), the page around it still scrolled.
const scrolledAroundAFrame = `<!doctype html>
<html><head><meta charset="utf-8"><title>Khung</title></head>
<body style="margin:0">
<div style="height:100px"></div>
<iframe id="frame" src="/?framed" style="display:block;width:600px;height:400px;border:0"></iframe>
<div style="height:3000px"></div>
<script>
(function () {
  var log = {}, frame = document.getElementById("frame");
  function frames(count, then) { if (count <= 0) { then(); return; } requestAnimationFrame(function () { frames(count - 1, then); }); }
  function settle(then) { frames(3, function () { setTimeout(then, 300); }); }
  function words() {
    var inside = frame.contentWindow;
    return "flag=" + (inside.document.documentElement.hasAttribute("data-kitwork-scrolled") ? "on" : "off") +
      " frame=" + Math.round(inside.pageYOffset) + " around=" + Math.round(window.pageYOffset);
  }
  function step(name, move, then) { move(); settle(function () { log[name] = words(); then(); }); }
  function send(report) {
    var close = function () { window.close(); };
    fetch("/report", { method: "POST", body: JSON.stringify(report) }).then(close, close);
  }
  function walk() {
    step("top", function () {}, function () {
      step("aroundPast", function () { window.scrollTo(0, 300); }, function () {
        step("frameScrolled", function () { frame.contentWindow.scrollTo(0, 600); }, function () {
          step("frameBack", function () { frame.contentWindow.scrollTo(0, 0); }, function () { send(log); });
        });
      });
    });
  }
  if (frame.contentWindow.location.search === "?framed" && frame.contentDocument.readyState === "complete") walk();
  else frame.addEventListener("load", walk, { once: true });
})();
</script>
</body></html>`

// scrolledArriving is the controller of the walk that starts on /khong, a page without scrolled: and
// so without the script: scrolled there, nothing sets the flag. A Drive move to / brings the script
// with the page (Drive loads a script the arriving page links), the flag follows the page / is shown
// at, and its scrolls after.
const scrolledArriving = `<script>
(function () {` + scrolledProbe + `
  var log = {};
  function script() { return document.querySelectorAll('script[data-kitwork-generated="scrolled"]').length; }
  window.addEventListener("load", function () {
    scrollThen(600, function () {
      log.without = words() + " scripts=" + script() + " " + mark();
      document.addEventListener("kitwork:load", function () {
        settle(function () {
          log.arrived = words() + " scripts=" + script();
          log.arrivedMark = mark();
          walk(log, [["scrolled", 600], ["top", 0]], function () { send(log); });
        });
      });
      document.getElementById("next").click();
    });
  });
})();
</script>`

// TestBrowserScrolledFollowsThePage proves scrolled: in a real browser on pages the engine serves:
//   - the bar has no shadow at the top, the small shadow once the page is scrolled, none again back at
//     the top; a scroll of 4px, and of 8px (the threshold), does not set it, 9px does; the plain box
//     beside it never changes (the shadow is the scrolled: class's, not the page's);
//   - the flag follows the page with every scroll event swallowed, from before the script runs to the
//     end of the walk: no scroll listener; the browser's IntersectionObserver reports it. It is set at
//     the start, by the observer's first report, when the page is already scrolled before the script
//     runs (a reload in the middle of a page);
//   - the script never reads the scroll position (scrollY, scrollTop), the read that makes the browser
//     lay the whole page out inside it; its mark is one element, a child of <html>, absolutely placed
//     at the top of the page, 1px by 8px, hidden, aria-hidden, never focused;
//   - the bar's transition is exactly box-shadow, .15s, and the plain box has none;
//   - a Drive move shows the arriving page at its top and the flag follows it (off), and on with its
//     next scroll; the mark is still the one, where it was;
//   - a page without scrolled: has no script and no mark, so scrolling it sets nothing (the control); a
//     Drive move from it to a page with scrolled: brings the script, which makes its mark, and the flag
//     follows from there;
//   - in a browser without IntersectionObserver (/?fallback) the flag still follows the page: the
//     script makes no mark and listens to scroll; its first read sets the flag at the start, in an
//     animation frame, never in the script's own run, and the threshold is the same;
//   - a page shown in a frame (/around, the test's own page, with / in an iframe) keeps its flag off
//     while the page around the frame scrolls the frame's top out of the window, and follows its own
//     scroll: the observer measures the page's own viewport, as scrollY did;
//   - in a browser that refuses a document for a root (/?element-root) the script still makes its
//     mark and the flag follows the page, by the implicit root, with no read of the scroll position;
//   - a page that scrolls sideways (/?wide, a box 3000px wide) keeps its flag off at its top however
//     far it is scrolled to the side, on 9px down and off back at the top, still to the side: only the
//     distance from the top counts, as with scrollY;
//   - a page shown on its own (/alone, Page.Document with no head from the engine), the script inline
//     at the end of <head>, puts its one mark into <html> before <body>, where the deferred script puts
//     it after; the flag follows the page there the same way.
func TestBrowserScrolledFollowsThePage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the scrolled browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant := buildUISite(t, scrolledBrowserFiles)
	for page, links := range map[string]bool{"/": true, "/hai": true, "/khong": false} {
		body := serveUI(t, tenant, "http://localhost"+page).Body.String()
		if got := scrolledTagRe.MatchString(body); got != links {
			t.Fatalf("the site is not the walk's: %s links the scrolled script: %v, want %v", page, got, links)
		}
	}
	_, home := compiledHome(t, tenant)
	alone := home.page.Document(ui.Request{Path: "/"})
	if inline := strings.Index(alone, "<script>\n"+string(stylesheet.ScrolledScript().JavaScript)+"</script>\n"); inline < 0 ||
		inline > strings.Index(alone, "</head>") || scrolledTagRe.MatchString(alone) || !strings.Contains(alone, "</body>") {
		t.Fatalf("the page on its own is not the walk's: the scrolled script is not inline in its <head>:\n%.3000s", alone)
	}

	var serving sync.Mutex
	moved := map[string]string{}
	reports := make(chan map[string]string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		path := request.URL.Path
		if path == "/report" {
			var report map[string]string
			body, _ := io.ReadAll(request.Body)
			if err := json.Unmarshal(body, &report); err != nil {
				t.Errorf("report %s: %v", body, err)
			}
			select {
			case reports <- report:
			default:
			}
			return
		}
		if path == "/around" {
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(response, scrolledAroundAFrame)
			return
		}
		if path == "/alone" {
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			at := strings.LastIndex(alone, "</body>")
			_, _ = io.WriteString(response, alone[:at]+scrolledAlone+alone[at:])
			return
		}
		forward := request.Clone(request.Context())
		forward.Host = "localhost"
		forward.Header.Del("Accept-Encoding") // the controller goes into the document as text
		answer := httptest.NewRecorder()
		serving.Lock()
		if request.Header.Get("X-Kitwork-Hydrate") != "" {
			moved[path] = "drive"
		}
		tenant.Serve(answer, forward)
		serving.Unlock()
		for name, values := range answer.Header() {
			response.Header()[name] = values
		}
		body := answer.Body.String()
		controller := map[string]string{"/": scrolledFromTheTop, "/khong": scrolledArriving}[path]
		if path == "/" && request.URL.RawQuery == "fallback" {
			controller = scrolledWithoutObserver
		}
		if path == "/" && request.URL.RawQuery == "element-root" {
			controller = scrolledWithAnElementRoot
		}
		if path == "/" && request.URL.RawQuery == "wide" {
			controller = scrolledSideways
		}
		if path == "/" && request.URL.RawQuery == "framed" {
			controller = "" // the page around the frame walks it
		}
		if controller != "" && request.Header.Get("X-Kitwork-Hydrate") == "" {
			at := strings.LastIndex(body, "</body>")
			if at < 0 {
				t.Errorf("%s has no </body>:\n%.500s", path, body)
			} else {
				body = body[:at] + controller + body[at:]
			}
			response.Header().Del("Content-Length")
		}
		response.WriteHeader(answer.Code)
		_, _ = io.WriteString(response, body)
	}))
	defer server.Close()

	check := func(walk string, got, want map[string]string) {
		t.Helper()
		for step, words := range want {
			if got[step] != words {
				t.Errorf("%s, %s:\n got  %s\n want %s", walk, step, got[step], words)
			}
		}
	}
	const mark = "parent=html absolute hidden aria-hidden=true focusable=false box=0,0 1x8"
	check("from the top", runBrowser(t, browser, server.URL+"/", "scrolled from the top", 1000, "", reports), map[string]string{
		"loaded":        "flag=on bar=small plain=none at=600",
		"mark":          mark,
		"order":         "head,body,div[scrolled]",
		"transition":    "box-shadow 0.15s plain=0s",
		"top":           "flag=off bar=none plain=none at=0",
		"wobble":        "flag=off bar=none plain=none at=4",
		"edge":          "flag=off bar=none plain=none at=8",
		"past":          "flag=on bar=small plain=none at=9",
		"scrolled":      "flag=on bar=small plain=none at=600",
		"moved":         "flag=off bar=none plain=none at=0",
		"movedMark":     mark,
		"movedScrolled": "flag=on bar=small plain=none at=600",
		"reads":         "none",
	})
	check("arriving", runBrowser(t, browser, server.URL+"/khong", "scrolled arriving", 1000, "", reports), map[string]string{
		"without":     "flag=off bar=absent plain=none at=600 scripts=0 marks=0",
		"arrived":     "flag=off bar=none plain=none at=0 scripts=1",
		"arrivedMark": mark,
		"scrolled":    "flag=on bar=small plain=none at=600",
		"top":         "flag=off bar=none plain=none at=0",
	})
	check("without IntersectionObserver", runBrowser(t, browser, server.URL+"/?fallback", "scrolled without IntersectionObserver", 1000, "", reports), map[string]string{
		"loaded": "flag=on bar=small plain=none at=600",
		"mark":   "marks=0",
		"start":  "first=scrollY@frame where=frame",
		"top":    "flag=off bar=none plain=none at=0",
		"edge":   "flag=off bar=none plain=none at=8",
		"past":   "flag=on bar=small plain=none at=9",
		"back":   "flag=off bar=none plain=none at=0",
		"reads":  "first=scrollY@frame where=frame,scroll",
	})
	check("a root that must be an element", runBrowser(t, browser, server.URL+"/?element-root", "scrolled with an element root", 1000, "", reports), map[string]string{
		"loaded":   "flag=on bar=small plain=none at=600 refused=1",
		"mark":     mark,
		"top":      "flag=off bar=none plain=none at=0",
		"edge":     "flag=off bar=none plain=none at=8",
		"past":     "flag=on bar=small plain=none at=9",
		"sideways": "flag=off bar=none plain=none at=0 x=200",
		"reads":    "none",
	})
	check("in a frame", runBrowser(t, browser, server.URL+"/around", "scrolled in a frame", 1000, "", reports), map[string]string{
		"top":           "flag=off frame=0 around=0",
		"aroundPast":    "flag=off frame=0 around=300",
		"frameScrolled": "flag=on frame=600 around=300",
		"frameBack":     "flag=off frame=0 around=300",
	})
	check("sideways", runBrowser(t, browser, server.URL+"/?wide", "scrolled sideways", 1000, "", reports), map[string]string{
		"width":        "scrollWidth=3000 wider than the window",
		"top":          "flag=off bar=none plain=none at=0 x=0",
		"nudged":       "flag=off bar=none plain=none at=0 x=2",
		"sideways":     "flag=off bar=none plain=none at=0 x=200",
		"sidewaysPast": "flag=on bar=small plain=none at=9 x=200",
		"sidewaysBack": "flag=off bar=none plain=none at=0 x=200",
		"far":          "flag=off bar=none plain=none at=0 x=2000",
		"reads":        "none",
	})
	check("on its own", runBrowser(t, browser, server.URL+"/alone", "scrolled on its own", 1000, "", reports), map[string]string{
		"loaded": "flag=on bar=small plain=none at=600",
		"mark":   mark,
		"order":  "head,div[scrolled],body",
		"top":    "flag=off bar=none plain=none at=0",
		"edge":   "flag=off bar=none plain=none at=8",
		"past":   "flag=on bar=small plain=none at=9",
	})
	serving.Lock()
	defer serving.Unlock()
	for _, page := range []string{"/hai", "/"} {
		if moved[page] == "" {
			t.Errorf("%s was loaded whole, not moved to by Drive (%v)", page, moved)
		}
	}
}
