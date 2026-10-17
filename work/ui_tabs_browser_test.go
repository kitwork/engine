package work

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// tabsRouterCode and tabsPageCode are the two files the window shows, given by the view.
const (
	tabsRouterCode = "import { router } from \"kitwork\";\n\nrouter.view(() => {\n  const title = \"Menu\";\n});"
	tabsPageCode   = "<page padding=\"6\">\n  <text heading=\"1\">{{ title }}</text>\n</page>"
)

// tabsBrowserFiles is a site with a file-tabbed terminal — the tabs in the terminal's own bar, one
// panel of code for each, typing="2s", persist="true": its choice kept under the set of its tab names,
// page+router — and more tabs around it: $also has the same tabs written the other way round (the same
// set, so the same name) and starts from router; $other has the same tabs and no persist (it chooses on
// its own); $partial persists another set (go+router); $shell and $console are two different sets kept
// under one name the author chose (persist="shell"), $console with a tab $shell does not have (cmd).
var tabsBrowserFiles = map[string]string{
	"localhost/router.work": `import { router } from "kitwork";

router.colors({ brand: "#c8742c" });
router.language("vi");
router.view(() => {
  const routerCode = ` + jsString(tabsRouterCode) + `;
  const pageCode = ` + jsString(tabsPageCode) + `;
});
`,
	"localhost/page.ui": `<page><content /></page>`,
	"localhost/content.ui": `<content padding="4" gap="4">
  <box id="window" max-width="640px">
    <box component="terminal" as="$code" typing="2s" background="panel" radius="medium">
      <box flex="row" align="center" gap="2" padding-x="3" padding-y="2">
        <box component="tabs" as="$files" persist="true" alt="Tệp" flex="row" gap="1">
          <button id="tab-router" click="select('router')">router.work</button>
          <button id="tab-page" click="select('page')">page.ui</button>
        </box>
        <button id="copy" click="copy()">{{ copied ? 'Đã chép' : 'Chép' }}</button>
      </box>
      <box id="file-router" show="$files.selected == 'router'" padding="4"><code language="work">{{ routerCode }}</code></box>
      <box id="file-page" show="$files.selected == 'page'" padding="4"><code language="ui">{{ pageCode }}</code></box>
    </box>
  </box>
  <box component="tabs" as="$also" persist="true" selected="router" alt="Cũng vậy" flex="row" gap="1">
    <button click="select('page')">page</button>
    <button click="select('router')">router</button>
  </box>
  <box id="also-router" show="$also.selected == 'router'"><text>router</text></box>
  <box id="also-page" show="$also.selected == 'page'"><text>page</text></box>
  <box component="tabs" as="$other" alt="Riêng" flex="row" gap="1">
    <button click="select('router')">router</button>
    <button click="select('page')">page</button>
  </box>
  <box id="other-router" show="$other.selected == 'router'"><text>router</text></box>
  <box id="other-page" show="$other.selected == 'page'"><text>page</text></box>
  <box component="tabs" as="$partial" persist="true" alt="Bộ khác" flex="row" gap="1">
    <button click="select('router')">router</button>
    <button click="select('go')">go</button>
  </box>
  <box id="partial-router" show="$partial.selected == 'router'"><text>router</text></box>
  <box id="partial-go" show="$partial.selected == 'go'"><text>go</text></box>
  <box component="tabs" as="$shell" persist="shell" alt="Shell" flex="row" gap="1">
    <button click="select('bash')">bash</button>
    <button click="select('powershell')">PowerShell</button>
  </box>
  <box id="shell-bash" show="$shell.selected == 'bash'"><text>bash</text></box>
  <box id="shell-powershell" show="$shell.selected == 'powershell'"><text>PowerShell</text></box>
  <box component="tabs" as="$console" persist="shell" alt="Console" flex="row" gap="1">
    <button id="console-bash" click="select('bash')">bash</button>
    <button id="console-powershell" click="select('powershell')">PowerShell</button>
    <button id="console-cmd" click="select('cmd')">cmd</button>
  </box>
  <box id="console-bash-panel" show="$console.selected == 'bash'"><text>bash</text></box>
  <box id="console-powershell-panel" show="$console.selected == 'powershell'"><text>PowerShell</text></box>
  <box id="console-cmd-panel" show="$console.selected == 'cmd'"><text>cmd</text></box>
</content>`,
}

// jsString writes s as a string literal of the language, as JSON.stringify writes it (a double-quoted
// JSON string reads the same; <, > and & are written as they are).
func jsString(s string) string {
	var out strings.Builder
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(s)
	return strings.TrimSuffix(out.String(), "\n")
}

// tabsHead runs first in <head>, before the page's body and so before the engine's pre-paint: it
// collects every error from there on (window.__errors: console errors, uncaught errors, rejections
// nobody handled), makes storage throw in phase 4 (blocked site data), and reads every frame the browser
// paints until the kernel is there: which file panels each frame shows, once the panels are in the
// document (window.__frames: the states seen, in order, and whether there was such a frame at all).
const tabsHead = `<script>
(function () {
  var errors = window.__errors = [];
  var original = console.error;
  console.error = function () { errors.push(Array.prototype.map.call(arguments, String).join(" ")); return original.apply(console, arguments); };
  window.addEventListener("error", function (event) { errors.push(String(event.message)); });
  window.addEventListener("unhandledrejection", function (event) { errors.push("unhandled: " + String(event.reason && (event.reason.code || event.reason.message) || event.reason)); });
  if (location.search === "?phase=4") {
    Storage.prototype.getItem = function () { throw new Error("blocked"); };
    Storage.prototype.setItem = function () { throw new Error("blocked"); };
  }
  var seen = [], count = 0;
  (function frame() {
    if (window.kit && window.kit.component) { window.__frames = { seen: seen.join(" "), early: count > 0 }; return; }
    var router = document.getElementById("file-router"), page = document.getElementById("file-page");
    if (router && page) {
      var now = [router, page].filter(function (panel) { return !panel.hidden; }).map(function (panel) { return panel.id; }).join(",");
      count++;
      if (seen[seen.length - 1] !== now) seen.push(now);
    }
    requestAnimationFrame(frame);
  })();
})();
</script>`

// tabsGroups is what the probes read the tabs of tabsBrowserFiles with, in a document: the tab each
// tabs shows (its panels not hidden), and the names its persist keeps, as kit.storage keeps them in
// localStorage (kitwork:tabs:<name>, the name as JSON).
const tabsGroups = `
  function pick(doc, ids) {
    var shown = Object.keys(ids).filter(function (name) { return !doc.getElementById(ids[name]).hidden; });
    return shown.join(",") || "none";
  }
  function groups(doc) {
    var line = function (prefix, names, suffix) {
      var ids = {};
      names.forEach(function (name) { ids[name] = prefix + name + (suffix || ""); });
      return pick(doc, ids);
    };
    return "files:" + line("file-", ["router", "page"]) + " also:" + line("also-", ["router", "page"]) +
      " other:" + line("other-", ["router", "page"]) + " partial:" + line("partial-", ["router", "go"]) +
      " shell:" + line("shell-", ["bash", "powershell"]) + " console:" + line("console-", ["bash", "powershell", "cmd"], "-panel");
  }
  function kept() {
    var out = [];
    try {
      for (var i = 0; i < localStorage.length; i++) {
        var key = localStorage.key(i);
        if (key.indexOf("kitwork:tabs:") === 0) out.push(key.slice("kitwork:tabs:".length) + "=" + localStorage.getItem(key));
      }
    } catch (problem) { return "storage refuses"; }
    return out.sort().join(" ") || "nothing";
  }
`

// tabsProbe reads the page in six loads of one browser, each a phase (?phase=N), the viewer's storage
// kept between them. It runs where the parser meets it, at the end of <body>: after the engine's
// pre-paint, before any of Kitwork's deferred scripts, so "before" is what a person sees before the
// kernel runs.
//  1. Nothing kept: typing starts on the router file, the one shown; a click on page.ui while it types
//     shows the page file at once (typing finished, no range left), flips aria-selected and tabindex,
//     and $also follows (the same set, written the other way round) — not $other (the same tabs, no
//     persist), not $partial (another set); kit.storage keeps "page" under page+router. The arrows, End
//     and Home move the choice and the focus along the tablist and wrap; Ctrl+Home is not the tablist's.
//     copy() copies only the file shown, and says it copied code, not commands. Then the name an author
//     chose: PowerShell in $console is followed by $shell (another set, the same name); cmd, a tab $shell
//     does not have, is kept and $shell stays on PowerShell. Kept: page+router and shell, nothing else.
//  2. All that kept, every script of Kitwork's answered late: the pre-paint has shown page — in $also
//     too — and cmd in $console before the kernel, $shell as the server painted it (it has no cmd), and
//     no frame painted before the kernel shows the router file; the kernel keeps it all, and typing plays
//     on the page file, the one shown, to its end.
//  3. A kept name that is no tab ("gone"): router, as the server painted it, before and after.
//  4. Storage that throws from the first script on (blocked site data): router; a click still chooses
//     page.
//  5. The window starts far below; scrolled to and its router tab focused, it starts typing, and an
//     arrow four frames later — inside typing's first wait, before the first character — chooses
//     page.ui: the viewer's own switch, so the page file is there at once, untyped by nothing.
//  6. Two windows of the site, this one and a frame of the same page: a choice in one is followed in the
//     other (a storage event), by every tabs kept under that name — not by $other, which keeps nothing,
//     not by $partial, another set.
//
// Errors are collected in every phase from the first script on.
const tabsProbe = `<script>
(function () {` + terminalProbeCommon + tabsGroups + `
  var phase = location.search.replace("?phase=", "") || "1";
  var part = {};
  var errors = window.__errors;
  var clipboard = [];
  Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText: function (text) { clipboard.push(text); return Promise.resolve(); } } });
  function attrs(id) { var el = byId(id); return [el.getAttribute("role"), el.getAttribute("aria-selected"), el.getAttribute("tabindex"), el.getAttribute("aria-controls")].join("|"); }
  function files() { return pick(document, { "file-router": "file-router", "file-page": "file-page" }); }
  function where(range) { if (!range) return "none"; var panel = range.startContainer.parentElement.closest("[role=tabpanel]"); return panel ? panel.id : "outside"; }
  function state() { return attrs("tab-router") + " " + attrs("tab-page") + " " + groups(document); }
  function frames(then) { requestAnimationFrame(function () { requestAnimationFrame(then); }); }
  function press(name, extra) {
    var init = { key: name, bubbles: true, cancelable: true };
    for (var k in (extra || {})) init[k] = extra[k];
    document.activeElement.dispatchEvent(new KeyboardEvent("keydown", init));
  }
  function post(next) {
    part.errors = JSON.stringify(errors);
    var body = {};
    for (var key in part) body[phase + " " + key] = part[key];
    if (!next) body.done = "true";
    var go = function () { if (next) location.href = "/?phase=" + next; else window.close(); };
    fetch("/report", { method: "POST", body: JSON.stringify(body) }).then(go, go);
  }
  if (phase === "frame") return; // the second window of phase 6: the first reads it
  part.before = state();
  if (phase === "5") byId("window").style.marginTop = "3000px"; // far below, before the terminal starts
  // where typing first stands: polled every frame from before the kernel starts
  (function first() {
    var range = rangeIn("kitwork-terminal-untyped", "window");
    if (range) part["first typing"] = where(range); else requestAnimationFrame(first);
  })();

  var phases = {
    "1": function () {
      until(function () { return part["first typing"] && typed("window") !== "all" && caret("window") !== "" && typed("window").length > "router.workpage.uiChép".length + 2; }, 5000, function () {
        part.typing = where(rangeIn("kitwork-terminal-untyped", "window")) + " " + (typed("window") === "all" ? "done" : "typing");
        byId("tab-page").click();
        frames(function () {
          part.switched = state() + " typed " + typed("window") + " ranges " + ranges() + " kept " + kept();
          var steps = [];
          var keys = [["ArrowRight"], ["End"], ["Home"], ["ArrowLeft"], ["Home", { ctrlKey: true }]];
          byId("tab-page").focus();
          (function next(i) {
            if (i === keys.length) {
              part.keys = steps.join(", ");
              byId("copy").click();
              after(300, function () {
                part.copied = JSON.stringify(clipboard);
                part.said = status("window");
                byId("console-powershell").click();
                frames(function () {
                  part.named = groups(document) + " kept " + kept();
                  byId("console-cmd").click();
                  frames(function () {
                    part["named cmd"] = groups(document) + " kept " + kept();
                    post("2");
                  });
                });
              });
              return;
            }
            press(keys[i][0], keys[i][1]);
            after(60, function () {
              steps.push(document.activeElement.id + " " + files() + " " + document.activeElement.getAttribute("aria-selected") + document.activeElement.getAttribute("tabindex"));
              next(i + 1);
            });
          })(0);
        });
      });
    },
    "2": function () {
      until(function () { return window.__frames; }, 2000, function () {
        part.frames = JSON.stringify(window.__frames);
      });
      after(100, function () {
        part.restored = state() + " kept " + kept();
        until(function () { return typed("window") === "all"; }, 6000, function () {
          part.typed = typed("window") + " " + ranges();
          localStorage.setItem("kitwork:tabs:page+router", JSON.stringify("gone")); // what phase 3 opens with
          localStorage.removeItem("kitwork:tabs:shell");
          post("3");
        });
      });
    },
    "3": function () {
      after(100, function () {
        part.after = state() + " kept " + kept();
        post("4");
      });
    },
    "4": function () {
      after(100, function () {
        var before = files();
        byId("tab-page").click();
        frames(function () {
          part.after = before + " then " + files() + " kept " + kept();
          post("5");
        });
      });
    },
    "5": function () {
      // nothing typed: the untyped range starts at the first character of the router file
      var untyped = function () {
        var range = rangeIn("kitwork-terminal-untyped", "window");
        return range ? where(range) + (range.startOffset === 0 ? " nothing typed" : " typing") : "all typed";
      };
      part.waits = untyped();
      byId("window").scrollIntoView({ block: "center" });
      byId("tab-router").focus({ preventScroll: true });
      // four frames: the terminal has seen itself on screen and its clock has run, well inside the wait
      // before the first character (300ms)
      frames(function () { frames(function () {
        part.typing = untyped();
        press("ArrowRight");
        frames(function () {
          part.switched = [document.activeElement.id, files(), typed("window"), ranges()].join(" ");
          after(500, function () {
            part.later = [typed("window"), ranges()].join(" ") + " kept " + kept();
            post("6");
          });
        });
      }); });
    },
    "6": function () {
      var frame = document.createElement("iframe");
      frame.src = "/?phase=frame";
      document.body.appendChild(frame);
      var other = function () { return frame.contentDocument; };
      until(function () { return frame.contentWindow && frame.contentWindow.kit && frame.contentWindow.kit.component && other() && other().readyState === "complete"; }, 5000, function () {
        after(200, function () {
          part.opened = groups(document) + " | " + groups(other());
          byId("tab-router").click();
          until(function () { return pick(other(), { router: "file-router" }) === "router"; }, 2000, function () {
            part["frame followed"] = groups(other());
            other().getElementById("tab-page").click();
            until(function () { return files() === "file-page"; }, 2000, function () {
              part["first followed"] = groups(document) + " kept " + kept();
              part["frame errors"] = JSON.stringify(frame.contentWindow.__errors);
              post("");
            });
          });
        });
      });
    }
  };
  window.addEventListener("load", function () {
    try { phases[phase](); } catch (problem) { part.error = String(problem && problem.stack || problem); post(""); }
  });
})();
</script>`

// TestBrowserTabsInATerminal proves the tabs component on a page the engine serves, in a real browser,
// the window of a file-tabbed terminal (tabsProbe says what each phase reads): the server paints the
// first tab, the pre-paint shows a tab kept before the first frame, typing plays once on the file shown,
// a switch shows the other file at once, the keyboard moves along the tablist, copy() copies the file
// shown and says it copied code; persist="true" keeps a choice under the set of the tab names, so every
// tabs with that set follows it — in this window and in another — and no other; persist="shell" shares
// a choice between different sets under the name an author chose; no persist keeps nothing; the
// viewer's browser keeps it all through kit.storage, and a kept name that is no tab or storage that
// throws changes nothing.
func TestBrowserTabsInATerminal(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the tabs browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant := buildUISite(t, tabsBrowserFiles)
	page := serveUI(t, tenant, "http://localhost/").Body.String()
	if !strings.Contains(page, `/_kitwork/components/tabs.`) || !strings.Contains(page, `/_kitwork/components/terminal.`) {
		t.Fatalf("the page does not load the tabs and the terminal:\n%.3000s", page)
	}

	var serving, merging sync.Mutex
	merged := map[string]string{}
	reports := make(chan map[string]string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/report" {
			var part map[string]string
			body, _ := io.ReadAll(request.Body)
			if err := json.Unmarshal(body, &part); err != nil {
				t.Errorf("report %s: %v", body, err)
			}
			merging.Lock()
			for key, value := range part {
				merged[key] = value
			}
			done := part["done"] == "true"
			merging.Unlock()
			if done {
				select {
				case reports <- merged:
				default:
				}
			}
			return
		}
		if strings.HasPrefix(request.URL.Path, "/_kitwork/") && strings.Contains(request.Referer(), "phase=2") {
			time.Sleep(800 * time.Millisecond) // phase 2: Kitwork's scripts come late, the page is painted long before
		}
		forward := request.Clone(request.Context())
		forward.Host = "localhost"
		forward.Header.Del("Accept-Encoding") // the probe goes into the document as text
		answer := httptest.NewRecorder()
		serving.Lock()
		tenant.Serve(answer, forward)
		serving.Unlock()
		for name, values := range answer.Header() {
			response.Header()[name] = values
		}
		response.Header().Set("Cache-Control", "no-store") // each phase fetches the scripts again
		body := answer.Body.String()
		if request.URL.Path == "/" {
			at := strings.LastIndex(body, "</body>")
			if at < 0 || !strings.Contains(body, `<meta charset="utf-8">`) {
				t.Errorf("/ has no </body> or no charset:\n%.500s", body)
			} else {
				body = body[:at] + tabsProbe + body[at:]
				body = strings.Replace(body, `<meta charset="utf-8">`, `<meta charset="utf-8">`+tabsHead, 1)
			}
			response.Header().Del("Content-Length")
		}
		response.WriteHeader(answer.Code)
		_, _ = io.WriteString(response, body)
	}))
	defer server.Close()

	got := runBrowser(t, browser, server.URL+"/", "tabs", 1000, "", reports)
	for key, value := range got {
		if strings.HasSuffix(key, " error") {
			t.Fatalf("the probe broke in phase %s: %s", key, value)
		}
	}
	router := "tab|true|0|file-router tab|false|-1|file-page "
	page2 := "tab|false|-1|file-router tab|true|0|file-page "
	painted := router + "files:router also:router other:router partial:router shell:bash console:bash"
	want := map[string]string{
		"1 before":         painted,
		"1 first typing":   "file-router",
		"1 typing":         "file-router typing",
		"1 switched":       page2 + `files:page also:page other:router partial:router shell:bash console:bash typed all ranges 0 kept page+router="page"`,
		"1 keys":           "tab-router file-router true0, tab-page file-page true0, tab-router file-router true0, tab-page file-page true0, tab-page file-page true0",
		"1 copied":         `[` + jsString(tabsPageCode) + `]`,
		"1 said":           "status:reader:Đã chép mã vào bộ nhớ tạm",
		"1 named":          `files:page also:page other:router partial:router shell:powershell console:powershell kept page+router="page" shell="powershell"`,
		"1 named cmd":      `files:page also:page other:router partial:router shell:powershell console:cmd kept page+router="page" shell="cmd"`,
		"1 errors":         "[]",
		"2 before":         page2 + "files:page also:page other:router partial:router shell:bash console:cmd",
		"2 frames":         `{"seen":"file-page","early":true}`,
		"2 first typing":   "file-page",
		"2 restored":       page2 + `files:page also:page other:router partial:router shell:bash console:cmd kept page+router="page" shell="cmd"`,
		"2 typed":          "all 0",
		"2 errors":         "[]",
		"3 before":         painted,
		"3 after":          painted + ` kept page+router="gone"`,
		"3 errors":         "[]",
		"4 before":         painted,
		"4 after":          "file-router then file-page kept storage refuses",
		"4 errors":         "[]",
		"5 before":         painted,
		"5 waits":          "file-router nothing typed",
		"5 typing":         "file-router nothing typed",
		"5 switched":       "tab-page file-page all 0",
		"5 later":          `all 0 kept page+router="page"`,
		"5 errors":         "[]",
		"6 before":         page2 + "files:page also:page other:router partial:router shell:bash console:bash",
		"6 opened":         "files:page also:page other:router partial:router shell:bash console:bash | files:page also:page other:router partial:router shell:bash console:bash",
		"6 frame followed": "files:router also:router other:router partial:router shell:bash console:bash",
		"6 first followed": `files:page also:page other:router partial:router shell:bash console:bash kept page+router="page"`,
		"6 errors":         "[]",
		"6 frame errors":   "[]",
	}
	failed := false
	for key, value := range want {
		if got[key] != value {
			t.Errorf("%s:\n got  %s\n want %s", key, got[key], value)
			failed = true
		}
	}
	if failed {
		t.Logf("report: %v", got)
	}
}
