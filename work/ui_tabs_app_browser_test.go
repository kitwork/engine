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

	jitjs "github.com/kitwork/engine/jit/js"
)

// tabsAppFiles is a tabs that persists (persist="true": kept under page+router) and its two panels.
var tabsAppFiles = map[string]string{
	"localhost/router.work": `import { router } from "kitwork";

router.colors({ brand: "#c8742c" });
router.language("vi");
`,
	"localhost/page.ui": `<page><content /></page>`,
	"localhost/content.ui": `<content padding="4" gap="4">
  <box component="tabs" as="$files" persist="true" alt="Tệp" flex="row" gap="1">
    <button id="tab-router" click="select('router')">router.work</button>
    <button id="tab-page" click="select('page')">page.ui</button>
  </box>
  <box id="file-router" show="$files.selected == 'router'"><text>router</text></box>
  <box id="file-page" show="$files.selected == 'page'"><text>page</text></box>
</content>`,
}

// tabsAppHead runs first in <head>, before the kernel: it is the shell. It seeds the native bridge a
// desktop or mobile shell gives the page (kit.bridge, call(action, params) answering a promise), here
// carried to the test (/native), which answers as a shell would: ?store=native, a store of its own that
// answers storage.get late (an asynchronous bridge); ?store=none, UNSUPPORTED for every call, as the
// shells answer storage.* today (native/capability). It collects every error, and which file each frame
// shows, from the first frame the panels are in the document.
const tabsAppHead = `<script>
(function () {
  var errors = window.__errors = [];
  var original = console.error;
  console.error = function () { errors.push(Array.prototype.map.call(arguments, String).join(" ")); return original.apply(console, arguments); };
  window.addEventListener("error", function (event) { errors.push(String(event.message)); });
  window.addEventListener("unhandledrejection", function (event) { errors.push("unhandled: " + String(event.reason && (event.reason.code || event.reason.message) || event.reason)); });
  var store = new URLSearchParams(location.search).get("store");
  var calls = window.__calls = [];
  window.__pending = 0; // calls the shell has not answered yet: a page left before then loses them
  window.kit = { bridge: { platform: "windows", call: function (action, params) {
    if (String(action).indexOf("storage.") === 0) calls.push(action + " " + (params && params.key) + (params && params.value !== undefined ? "=" + params.value : ""));
    window.__pending++;
    var settled = function () { window.__pending--; };
    return fetch("/native?store=" + store, { method: "POST", body: JSON.stringify({ action: action, params: params || {} }) })
      .then(function (response) { return response.json(); })
      .then(function (answer) {
        settled();
        if (answer.ok) return answer.value;
        var problem = new Error(answer.message);
        problem.name = "KitworkError";
        problem.code = answer.code;
        throw problem;
      }, function (problem) { settled(); throw problem; });
  } } };
  var seen = window.__seen = [];
  (function frame() {
    var router = document.getElementById("file-router"), page = document.getElementById("file-page");
    if (router && page) {
      var now = [router, page].filter(function (panel) { return !panel.hidden; }).map(function (panel) { return panel.id; }).join(",") || "none";
      if (seen[seen.length - 1] !== now) seen.push(now);
    }
    if (!window.__stop) requestAnimationFrame(frame);
  })();
})();
</script>`

// tabsAppProbe reads the page in five loads of one browser, at the end of <body>, before the kernel:
//  1. (a native store, empty) router, as the server painted it; page clicked, it is shown, and the
//     choice goes over the bridge (storage.set tabs:page+router=page), not into the WebView's
//     localStorage.
//  2. (the store keeps page, and answers 500ms late) the first frames show router — no pre-paint can
//     read a store behind an asynchronous bridge — and page once the bridge answers: the kernel restores
//     it as early as the store lets it.
//  3. (the same, late again) router clicked before the store answers: the late answer (page) does not
//     undo the viewer's choice, which is kept (router).
//  4. (a shell whose bridge answers UNSUPPORTED, as every shell does storage.* today) router; page
//     clicked, it is shown; the calls fail, nothing is kept anywhere, and nothing is an error.
//  5. (the same, reloaded) router: nothing was kept.
const tabsAppProbe = `<script>
(function () {
  var phase = new URLSearchParams(location.search).get("phase") || "1";
  var part = {};
  var byId = function (id) { return document.getElementById(id); };
  function files() { return ["file-router", "file-page"].filter(function (id) { return !byId(id).hidden; }).join(",") || "none"; }
  function web() {
    var out = [];
    for (var i = 0; i < localStorage.length; i++) if (localStorage.key(i).indexOf("kitwork:") === 0) out.push(localStorage.key(i));
    return out.join(" ") || "nothing";
  }
  function after(ms, then) { setTimeout(then, ms); }
  function until(test, ms, then) {
    var start = Date.now();
    (function poll() { if (test() || Date.now() - start > ms) then(); else setTimeout(poll, 20); })();
  }
  function post(next) {
    // every call answered first: the shell has what the page asked it to keep
    until(function () { return window.__pending === 0; }, 5000, function () {
      part.errors = JSON.stringify(window.__errors);
      part.calls = window.__calls.join(", ");
      var body = {};
      for (var key in part) body[phase + " " + key] = part[key];
      if (!next) body.done = "true";
      var go = function () { if (next) location.href = "/?phase=" + next; else window.close(); };
      fetch("/report", { method: "POST", body: JSON.stringify(body) }).then(go, go);
    });
  }
  part.before = files();
  var phases = {
    "1": function () {
      after(300, function () {
        byId("tab-page").click();
        after(300, function () { part.after = files() + " web " + web(); post("2&store=native"); });
      });
    },
    "2": function () {
      until(function () { return files() === "file-page"; }, 5000, function () {
        // two frames more, so the frame that drew the change is among those seen
        requestAnimationFrame(function () { requestAnimationFrame(function () {
          window.__stop = true;
          part.seen = window.__seen.join(" ");
          post("3&store=native");
        }); });
      });
    },
    "3": function () {
      // at load the kernel has started the tabs, whose storage.get is on its way (500ms)
      byId("tab-router").click();
      after(1000, function () { window.__stop = true; part.after = files() + " seen " + window.__seen.join(" ") + " web " + web(); post("4&store=none"); });
    },
    "4": function () {
      after(300, function () {
        byId("tab-page").click();
        after(500, function () { part.after = files() + " web " + web(); post("5&store=none"); });
      });
    },
    "5": function () {
      after(800, function () { part.after = files() + " web " + web(); post(""); });
    }
  };
  window.addEventListener("load", function () {
    try { phases[phase](); } catch (problem) { part.error = String(problem && problem.stack || problem); post(""); }
  });
})();
</script>`

// TestBrowserTabsPersistInAnApp proves where a tabs keeps its choice in a desktop or mobile app: through
// kit.storage, which there is the native bridge, asynchronous. The page is served as to an app (the
// kernel with the bridge, jitjs.Native) and the test is the shell (tabsAppHead). With a native store:
// the choice goes over the bridge and nowhere else, and on the next visit the kernel shows it once the
// store answers — after the first frames, which show the server's tab (the gap docs/components/tabs.md
// names: in an app nothing can read the store before the first frame); a choice the viewer makes before
// the store answers wins over the late answer. With the bridge the shells have today (storage.* is
// UNSUPPORTED in native/capability), nothing is kept and nothing breaks.
func TestBrowserTabsPersistInAnApp(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the tabs app browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	previous, previousFiles := NativeShell, jitjs.Native
	NativeShell, jitjs.Native = true, true
	defer func() { NativeShell, jitjs.Native = previous, previousFiles }()
	tenant := buildUISite(t, tabsAppFiles)

	var serving, merging, storing sync.Mutex
	merged := map[string]string{}
	native := map[string]any{} // the shell's own store
	reports := make(chan map[string]string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/report":
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
		case "/native":
			var call struct {
				Action string
				Params map[string]any
			}
			body, _ := io.ReadAll(request.Body)
			_ = json.Unmarshal(body, &call)
			answer := map[string]any{"ok": false, "code": "UNSUPPORTED", "message": "unsupported native capability: " + call.Action}
			if request.URL.Query().Get("store") == "native" {
				key, _ := call.Params["key"].(string)
				switch call.Action {
				case "storage.get":
					storing.Lock()
					value := native[key]
					storing.Unlock()
					time.Sleep(500 * time.Millisecond) // the bridge answers late
					answer = map[string]any{"ok": true, "value": value}
				case "storage.set":
					storing.Lock()
					native[key] = call.Params["value"]
					storing.Unlock()
					answer = map[string]any{"ok": true, "value": true}
				}
			}
			response.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(response).Encode(answer)
			return
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
		response.Header().Set("Cache-Control", "no-store")
		body := answer.Body.String()
		if request.URL.Path == "/" {
			at := strings.LastIndex(body, "</body>")
			if at < 0 || !strings.Contains(body, `<meta charset="utf-8">`) {
				t.Errorf("/ has no </body> or no charset:\n%.500s", body)
			} else {
				body = body[:at] + tabsAppProbe + body[at:]
				body = strings.Replace(body, `<meta charset="utf-8">`, `<meta charset="utf-8">`+tabsAppHead, 1)
			}
			response.Header().Del("Content-Length")
		}
		response.WriteHeader(answer.Code)
		_, _ = io.WriteString(response, body)
	}))
	defer server.Close()

	got := runBrowser(t, browser, server.URL+"/?phase=1&store=native", "tabs-app", 1000, "", reports)
	for key, value := range got {
		if strings.HasSuffix(key, " error") {
			t.Fatalf("the probe broke in phase %s: %s", key, value)
		}
	}
	want := map[string]string{
		"1 before": "file-router",
		"1 after":  "file-page web nothing",
		"1 calls":  "storage.get tabs:page+router, storage.set tabs:page+router=page",
		"1 errors": "[]",
		"2 before": "file-router",
		"2 seen":   "file-router file-page",
		"2 calls":  "storage.get tabs:page+router",
		"2 errors": "[]",
		"3 before": "file-router",
		"3 after":  "file-router seen file-router web nothing",
		"3 calls":  "storage.get tabs:page+router, storage.set tabs:page+router=router",
		"3 errors": "[]",
		"4 before": "file-router",
		"4 after":  "file-page web nothing",
		"4 calls":  "storage.get tabs:page+router, storage.set tabs:page+router=page",
		"4 errors": "[]",
		"5 before": "file-router",
		"5 after":  "file-router web nothing",
		"5 errors": "[]",
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
	storing.Lock()
	defer storing.Unlock()
	if kept, _ := json.Marshal(native); string(kept) != `{"tabs:page+router":"router"}` {
		t.Errorf("the shell's store holds %s, want the last choice made with a store there (router)", kept)
	}
}
