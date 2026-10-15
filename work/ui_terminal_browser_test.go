package work

import (
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// terminalBrowserFiles is a site with three terminals the author draws, each typing its commands:
//   - "near", at the top of the page, typing="800ms": a bar holding its name, its copy button and a
//     command (a <code> in the bar is a command too), then a screen with a bash command, what it
//     printed, a dropdown whose <code> is the dropdown's own (not a command), and a powershell command;
//     the page reaches it from outside by its alias, $install;
//   - "long", typing="3s", in a column as wide as a phone: the kitwork example's quickstart and a request
//     to the server, its first and its last command wider than their <pre>;
//   - "far", typing="1s", far below them, off screen until the probe scrolls to it; the clipboard stub
//     refuses its commands.
var terminalBrowserFiles = map[string]string{
	"localhost/router.work": `import { router } from "kitwork";

router.colors({ brand: "#c8742c" });
`,
	"localhost/page.ui": `<page><content /></page>`,
	"localhost/content.ui": `<content padding="4" gap="4">
  <box id="near">
    <box component="terminal" as="$install" typing="800ms" background="panel" radius="medium">
      <box flex="row" align="center" gap="3" padding-x="4" padding-y="2">
        <text size="small" font="code">bash</text>
        <button id="copy-near" click="copy()">{{ copied ? 'Đã chép' : 'Chép' }}</button>
        <code>git status</code>
      </box>
      <box padding="4">
        <code language="bash">npm i kitwork</code>
        <text>added 1 package</text>
        <box component="dropdown">
          <button click="toggle()">Mở</button>
          <popover><code>not a command</code></popover>
        </box>
        <code language="powershell">kitwork dev</code>
      </box>
    </box>
  </box>
  <box id="long" max-width="320px">
    <box component="terminal" typing="3s" background="panel">
      <box flex="row" padding-x="4" padding-y="2">
        <button id="copy-long" click="copy()">{{ copied ? 'Đã chép' : 'Chép' }}</button>
      </box>
      <box padding="4">
        <code language="bash">git clone https://github.com/kitwork/engine.git</code>
        <text>Cloning into 'engine'...</text>
        <code language="bash">cd engine</code>
        <code language="bash">go run . check</code>
        <code language="bash">go run .</code>
        <code language="bash">curl -fsS http://localhost:8080/ -H 'Host: kitwork.localhost'</code>
      </box>
    </box>
  </box>
  <box height="2400px"></box>
  <box id="far">
    <box component="terminal" typing="1s" background="panel">
      <box padding="4">
        <code language="powershell">Get-ChildItem</code>
        <text>content.ui</text>
        <code language="powershell">kitwork dev</code>
        <button id="copy-far" click="copy()">{{ copied ? 'Đã chép' : 'Chép' }}</button>
      </box>
    </box>
  </box>
  <box height="2400px"></box>
</content>`,
}

// terminalLines are the terminals of terminalBrowserFiles as a reader without JavaScript reads them, a
// line for each block: every prompt, command and text, the dropdown's code with no prompt.
var terminalLines = map[string]string{
	"near": "bash\nChép\n$ git status\n$ npm i kitwork\nadded 1 package\nMở\nnot a command\nPS> kitwork dev\n",
	"far":  "PS> Get-ChildItem\ncontent.ui\nPS> kitwork dev\nChép\n",
}

// terminalOutside is markup the probe puts on the page, outside every terminal: a button that copies
// "near" through its alias, as the kernel reads data-kitwork-event wherever it is. (The .ui compiler
// does not write an event outside every component yet: ui/runtime.go, runtimeAttributes.)
const terminalOutside = `<button id="outside" type="button" data-kitwork-event="click: $install.copy()">Chép từ ngoài</button>`

// terminalProbeCommon is what every probe reads the terminals with: the host of a terminal, its range
// in a highlight, how much of it is typed (the text before the start of its range in the highlight
// kitwork-terminal-untyped; "all" when it has none), the character under its caret, the number of
// ranges every terminal has in the two highlights, and a report sent to the test.
const terminalProbeCommon = `
  var report = {};
  var byId = function (id) { return document.getElementById(id); };
  var host = function (id) { return byId(id).querySelector("[data-kitwork-component^=terminal]"); };
  var status = function (id) { var region = host(id).lastElementChild; return region.getAttribute("role") + ":" + region.className + ":" + region.textContent; };
  function rangeIn(name, id) {
    var found = null, h = host(id);
    var named = window.CSS && CSS.highlights && CSS.highlights.get(name);
    if (named) named.forEach(function (range) { if (h.contains(range.startContainer)) found = range; });
    return found;
  }
  function typed(id) {
    var range = rangeIn("kitwork-terminal-untyped", id);
    if (!range) return "all";
    var before = document.createRange();
    before.setStart(host(id), 0);
    before.setEnd(range.startContainer, range.startOffset);
    return before.toString();
  }
  function caret(id) { var range = rangeIn("kitwork-terminal-caret", id); return range ? range.toString() : ""; }
  function ranges() {
    var count = 0;
    ["kitwork-terminal-untyped", "kitwork-terminal-caret"].forEach(function (name) {
      var found = window.CSS && CSS.highlights && CSS.highlights.get(name);
      if (found) count += found.size;
    });
    return count;
  }
  function send() {
    var close = function () { window.close(); };
    fetch("/report", { method: "POST", body: JSON.stringify(report) }).then(close, close);
  }
  function after(ms, then) { setTimeout(then, ms); }
  function until(test, ms, then) {
    var start = Date.now();
    (function poll() { if (test() || Date.now() - start > ms) then(); else setTimeout(poll, 20); })();
  }
  // how long a terminal types: from the first frame its untyped range is there to the first frame it
  // is gone, each frame read once its animation frame callbacks (the terminal's among them) have run —
  // a message posted from the probe's own callback is a task after them — at the frame's time
  var timing = {};
  function clock(ids, then) {
    var channel = new MessageChannel(), frameTime = 0;
    channel.port1.onmessage = function () {
      var done = true;
      ids.forEach(function (id) {
        var t = timing[id] || (timing[id] = { seen: {} });
        var has = !!rangeIn("kitwork-terminal-untyped", id);
        if (has && t.start == null) t.start = frameTime;
        if (has) t.seen[typed(id).length] = true;
        if (!has && t.start != null && t.end == null) t.end = frameTime;
        if (t.end == null) done = false;
      });
      if (done || frameTime > 20000) then(); else requestAnimationFrame(frame);
    };
    function frame(time) { frameTime = time; channel.port2.postMessage(0); }
    requestAnimationFrame(frame);
  }
  function took(id, total) {
    var t = timing[id];
    if (!t || t.start == null || t.end == null) return "never typed";
    var spent = t.end - t.start;
    return Math.abs(spent - total) <= 1000 / 60 ? total + "ms within a frame" : Math.round(spent) + "ms";
  }
`

// terminalProbe reads the terminals. Its first part runs where the parser meets it, at the end of
// <body>, before any of Kitwork's deferred scripts (the kernel, the terminal component): the terminals'
// text and the highlights as the HTML alone has them. Then, once the components run: "near" and "long"
// type, each in the time it was given to within a frame, gradually, while "far" waits off screen with
// its caret on its first character; "far" types once scrolled on screen, holds still while scrolled off
// again, and finishes on screen, leaving no range at all. Then the copies: "near"'s button puts on the
// clipboard only the commands, one a line — the bar's, then the screen's, no prompt, no text, not the
// dropdown's code — and says so on the button (copied) and, a beat later, in the polite status region
// the engine wrote; "far"'s clipboard refuses and the region says so, its button unchanged; a second
// copy while the first is still said empties the region, then says it again; the button outside every
// terminal copies "near" through its alias; two seconds on, copied is false and the regions are empty.
// The clipboard is a stub on the page (navigator.clipboard.writeText records what it is given, and
// refuses PowerShell's), because headless Chrome cannot read its clipboard back.
const terminalProbe = `<script>
(function () {` + terminalProbeCommon + `
  var copied = [];
  Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText: function (text) {
    copied.push(text);
    return text.indexOf("Get-ChildItem") >= 0 ? Promise.reject(new Error("refused")) : Promise.resolve();
  } } });
  // before any script of Kitwork runs
  report.before = JSON.stringify({ near: host("near").textContent, long: host("long").textContent, far: host("far").textContent, ranges: ranges() });
  clock(["near", "long"], function () {
    report.near = took("near", 800) + ", " + (Object.keys(timing.near.seen).length > 5 ? "gradually" : "at once");
    report.long = took("long", 3000) + ", " + (Object.keys(timing.long.seen).length > 5 ? "gradually" : "at once");
    report["near text"] = String(host("near").textContent === {{near}});
  });

  function scrollTo(id, then) {
    if (id) byId(id).scrollIntoView({ block: "center" }); else window.scrollTo(0, 0);
    requestAnimationFrame(function () { requestAnimationFrame(then); });
  }
  window.addEventListener("load", function () {
    try {
      report["far waits"] = JSON.stringify([typed("far"), caret("far")]);
      until(function () { return report.long; }, 15000, function () {
        report["far still"] = JSON.stringify([typed("far"), caret("far")]);
        scrollTo("far", function () {
          after(400, function () {
            var part = typed("far");
            report["far on screen"] = String(part !== "all" && part.length > "PS> ".length);
            scrollTo("", function () {
              after(200, function () {
                var held = typed("far");
                after(800, function () {
                  report["far off screen"] = held === "all" ? "done too soon" : String(typed("far") === held);
                  scrollTo("far", function () {
                    until(function () { return typed("far") === "all"; }, 15000, function () {
                      report["far done"] = "text=" + String(host("far").textContent === {{far}}) + " ranges=" + ranges();
                      byId("copy-near").click();
                      byId("copy-far").click();
                      after(300, function () {
                        report.said = JSON.stringify([byId("copy-near").textContent, status("near"), byId("copy-far").textContent, status("far")]);
                        // a second copy while the first is still said: what the status region says
                        var heard = [];
                        var region = host("near").lastElementChild;
                        new MutationObserver(function () { heard.push(region.textContent); })
                          .observe(region, { childList: true, characterData: true, subtree: true });
                        byId("copy-near").click();
                        after(300, function () {
                          report["said again"] = JSON.stringify(heard);
                          byId("outside").click();
                          after(300, function () {
                            report.copied = JSON.stringify(copied);
                            report.outside = JSON.stringify([byId("copy-near").textContent, status("near")]);
                            after(2300, function () {
                              report.later = JSON.stringify([byId("copy-near").textContent, status("near"), byId("copy-far").textContent, status("far")]);
                              send();
                            });
                          });
                        });
                      });
                    });
                  });
                });
              });
            });
          });
        });
      });
    } catch (problem) { report.error = String(problem && problem.stack || problem); send(); }
  });
})();
</script>`

// terminalReducedProbe reads the terminals once the page has loaded under reduced motion, or in forced
// colours (where a highlight's transparent text is painted in the system's colours, so what is not
// typed yet would show as a selection): everything shows at once, and stays so.
const terminalReducedProbe = `<script>
(function () {` + terminalProbeCommon + `
  window.addEventListener("load", function () {
    report.reduced = String(matchMedia("(prefers-reduced-motion: reduce)").matches);
    report.forced = String(matchMedia("(forced-colors: active)").matches);
    after(300, function () {
      report.ranges = String(ranges());
      after(1500, function () {
        report.later = String(ranges());
        send();
      });
    });
  });
})();
</script>`

// terminalNarrowProbe reads "long", in its column as wide as a phone (a headless window is never that
// narrow), from the moment its typing starts:
//   - it types in its three seconds, to within a frame;
//   - while it types, the caret lies inside the visible box of the <pre> it types in at every sample
//     (the <pre> scrolls sideways after it), and once a command is entered — its <pre> scrolled — that
//     <pre> is back at its start, its prompt in sight;
//   - with no navigator.clipboard (a page served over plain HTTP, not from localhost, has none) a copy
//     goes through kit.clipboard's selection — document.execCommand("copy"), a stub here recording the
//     text selected in the textarea it is given —, leaves nothing behind, keeps the focus on the button
//     and says so.
const terminalNarrowProbe = `<script>
(function () {` + terminalProbeCommon + `
  var copied = [];
  Object.defineProperty(navigator, "clipboard", { configurable: true, value: undefined });
  document.execCommand = function (name) {
    if (name !== "copy") return false;
    var area = document.querySelector("textarea[readonly]");
    copied.push(area ? area.value.slice(area.selectionStart, area.selectionEnd) : String(getSelection()));
    return true;
  };
  var samples = 0, hidden = [], scrolled = 0;
  (function watch() {
    var mark = rangeIn("kitwork-terminal-caret", "long");
    if (mark) {
      var pre = mark.startContainer.parentElement.closest("pre");
      samples++;
      var box = pre.getBoundingClientRect(), at = mark.getBoundingClientRect();
      var left = box.left + pre.clientLeft, right = left + pre.clientWidth;
      if (at.left < left - 0.5 || at.right > right + 0.5) hidden.push(Math.round(at.left) + "-" + Math.round(at.right) + " outside " + Math.round(left) + "-" + Math.round(right));
      scrolled = Math.max(scrolled, pre.scrollLeft);
    }
    if (!report.long) setTimeout(watch, 10);
  })();
  clock(["long"], function () {
    report.long = took("long", 3000);
    var pres = host("long").querySelectorAll("pre"), wider = false, back = true;
    pres.forEach(function (pre) { if (pre.scrollWidth > pre.clientWidth) wider = true; if (pre.scrollLeft !== 0) back = false; });
    report.caret = JSON.stringify({ wider: wider, followed: scrolled > 0, samples: samples > 10, hidden: hidden.slice(0, 3), back: back });
    var button = byId("copy-long");
    button.focus();
    button.click();
    after(300, function () {
      report.copy = JSON.stringify({ copied: copied, said: [button.textContent, status("long")],
        left: document.querySelectorAll("textarea").length, focus: document.activeElement === button });
      send();
    });
  });
})();
</script>`

var terminalTagRe = regexp.MustCompile(`<[^>]*>`)

// terminalOf is the HTML inside the terminal host that follows id="<id>" in body.
func terminalOf(t *testing.T, body, id string) string {
	t.Helper()
	at := strings.Index(body, `id="`+id+`"`)
	if at < 0 {
		t.Fatalf("no %s in\n%.2000s", id, body)
	}
	rest := body[at:]
	open := strings.Index(rest, `data-kitwork-component="terminal`)
	if open < 0 {
		t.Fatalf("no terminal in %s:\n%.2000s", id, rest)
	}
	start := open + strings.Index(rest[open:], ">") + 1
	depth := 1
	tags := regexp.MustCompile(`<(/?)div\b[^>]*>`)
	for from := start; ; {
		m := tags.FindStringSubmatchIndex(rest[from:])
		if m == nil {
			t.Fatalf("the terminal of %s is not closed", id)
		}
		if rest[from+m[2]:from+m[3]] == "/" {
			depth--
		} else {
			depth++
		}
		if depth == 0 {
			return rest[start : from+m[0]]
		}
		from += m[1]
	}
}

// textContent is the text of some HTML as the DOM's textContent reads it: tags left out, characters
// unescaped.
func textContent(markup string) string {
	return html.UnescapeString(terminalTagRe.ReplaceAllString(markup, ""))
}

// linesOf is the text of some HTML a line for each block (a <pre>, a <p>, a <button>, a <div> holding
// text), as a reader without JavaScript reads it.
func linesOf(markup string) string {
	return textContent(strings.NewReplacer("</pre>", "</pre>\n", "</p>", "</p>\n", "</button>", "</button>\n").Replace(markup))
}

// TestBrowserTerminal proves the terminal component in a real browser, on a page the engine serves
// (each probe says what it reads):
//   - the whole text of every terminal is in the HTML — every prompt, command and text the author
//     wrote — before any script runs, and no highlight exists then;
//   - typing takes the time written, to within a frame, gradually, only on screen; under reduced
//     motion and in forced colours everything shows at once;
//   - copy() puts only the commands on the clipboard, one a line, says so politely by itself, and
//     copied goes back to false; the page reaches it through the alias too;
//   - as wide as a phone the caret stays in sight, and a page with no navigator.clipboard copies.
func TestBrowserTerminal(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the terminal browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant := buildUISite(t, terminalBrowserFiles)
	page := serveUI(t, tenant, "http://localhost/").Body.String()
	texts := map[string]string{}
	for _, id := range []string{"near", "long", "far"} {
		texts[id] = textContent(terminalOf(t, page, id))
	}
	for id, want := range terminalLines {
		if got := linesOf(terminalOf(t, page, id)); got != want {
			t.Fatalf("the HTML of %s, as a reader without JavaScript reads it:\n got  %q\n want %q", id, got, want)
		}
	}
	if !strings.Contains(page, `/_kitwork/components/terminal.`) {
		t.Fatalf("the page does not load the terminal component:\n%.3000s", page)
	}

	var serving sync.Mutex
	reports := make(chan map[string]string, 1)
	var probe string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/report" {
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
		body := answer.Body.String()
		if request.URL.Path == "/" {
			at := strings.LastIndex(body, "</body>")
			if at < 0 {
				t.Errorf("/ has no </body>:\n%.500s", body)
			} else {
				body = body[:at] + terminalOutside + probe + body[at:]
			}
			response.Header().Del("Content-Length")
		}
		response.WriteHeader(answer.Code)
		_, _ = io.WriteString(response, body)
	}))
	defer server.Close()

	// the terminals' text, as JavaScript strings the probe compares with
	literal := func(s string) string {
		encoded, _ := json.Marshal(s)
		return string(encoded)
	}
	probe = strings.NewReplacer("{{near}}", literal(texts["near"]), "{{far}}", literal(texts["far"])).Replace(terminalProbe)

	// what the probe reads before any script of Kitwork runs, written as JSON.stringify writes it
	var before strings.Builder
	encoder := json.NewEncoder(&before)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(struct {
		Near   string `json:"near"`
		Long   string `json:"long"`
		Far    string `json:"far"`
		Ranges int    `json:"ranges"`
	}{texts["near"], texts["long"], texts["far"], 0}); err != nil {
		t.Fatal(err)
	}
	got := runBrowser(t, browser, server.URL+"/", "terminal", 1000, "", reports)
	if message, broke := got["error"]; broke {
		t.Fatalf("the probe broke: %s", message)
	}
	commands := `npm i kitwork\nkitwork dev`
	nearCommands := `"git status\n` + commands + `"`
	want := map[string]string{
		"before":         strings.TrimSuffix(before.String(), "\n"),
		"near":           "800ms within a frame, gradually",
		"long":           "3000ms within a frame, gradually",
		"near text":      "true",
		"far waits":      `["PS> ","G"]`,
		"far still":      `["PS> ","G"]`,
		"far on screen":  "true",
		"far off screen": "true",
		"far done":       "text=true ranges=0",
		"said":           `["Đã chép","status:reader:Đã chép lệnh vào bộ nhớ tạm","Chép","status:reader:Không chép được lệnh"]`,
		"said again":     `["","Đã chép lệnh vào bộ nhớ tạm"]`,
		"copied":         `[` + nearCommands + `,"Get-ChildItem\nkitwork dev",` + nearCommands + `,` + nearCommands + `]`,
		"outside":        `["Đã chép","status:reader:Đã chép lệnh vào bộ nhớ tạm"]`,
		"later":          `["Chép","status:reader:","Chép","status:reader:"]`,
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

	probe = terminalReducedProbe
	reduced := runBrowser(t, browser, server.URL+"/", "terminal, reduced motion", 1000, "prefersReducedMotion=true", reports)
	for key, value := range map[string]string{"reduced": "true", "forced": "false", "ranges": "0", "later": "0"} {
		if reduced[key] != value {
			t.Errorf("reduced motion, %s: got %s, want %s (%v)", key, reduced[key], value, reduced)
		}
	}

	forced := runBrowser(t, browser, server.URL+"/", "terminal, forced colours", 1000, "inForcedColors=true", reports)
	for key, value := range map[string]string{"reduced": "false", "forced": "true", "ranges": "0", "later": "0"} {
		if forced[key] != value {
			t.Errorf("forced colours, %s: got %s, want %s (%v)", key, forced[key], value, forced)
		}
	}

	probe = terminalNarrowProbe
	narrow := runBrowser(t, browser, server.URL+"/", "terminal, narrow", 375, "", reports)
	if message, broke := narrow["error"]; broke {
		t.Fatalf("the narrow probe broke: %s", message)
	}
	for key, value := range map[string]string{
		"long":  "3000ms within a frame",
		"caret": `{"wider":true,"followed":true,"samples":true,"hidden":[],"back":true}`,
		"copy":  `{"copied":["git clone https://github.com/kitwork/engine.git\ncd engine\ngo run . check\ngo run .\ncurl -fsS http://localhost:8080/ -H 'Host: kitwork.localhost'"],"said":["Đã chép","status:reader:Đã chép lệnh vào bộ nhớ tạm"],"left":0,"focus":true}`,
	} {
		if narrow[key] != value {
			t.Errorf("narrow, %s:\n got  %s\n want %s", key, narrow[key], value)
		}
	}
}
