package work

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// terminalReachFiles is a site whose terminal is reached from outside its host through its alias,
// written in .ui (decision T2, 12/10/2026): a button before the host calls $pick.copy() and a text
// reads $pick.copied. The terminal holds a PowerShell command for a wide screen (medium and up), a bash
// command for a narrow one, and a command for both; it types them in 2 seconds. Two neutral buttons
// beside it, one disabled.
var terminalReachFiles = map[string]string{
	"localhost/router.work": `import { router } from "kitwork";

router.colors({ brand: "#c8742c" });
`,
	"localhost/page.ui": `<page><content /></page>`,
	"localhost/content.ui": `<content padding="4" gap="4">
  <button id="outside" click="$pick.copy()">Chép từ ngoài</button>
  <text id="state">{{ $pick.copied ? 'yes' : 'no' }}</text>
  <box flex="row" gap="2"><button id="on">Bật</button><button id="off" disabled="true">Tắt</button></box>
  <box id="pick">
    <box component="terminal" as="$pick" typing="2s" background="panel">
      <box padding="4">
        <code language="powershell" show="false" medium:show="true">Get-ChildItem -Recurse -Force -ErrorAction SilentlyContinue</code>
        <code language="bash" medium:show="false">ls -la --color=never --group-directories-first</code>
        <code language="bash">echo done</code>
        <button id="copy-pick" click="copy()">{{ copied ? 'Đã chép' : 'Chép' }}</button>
      </box>
    </box>
  </box>
</content>`,
}

// terminalReachProbe reads "pick" while it types and once it has: the caret never sits in a command
// that is not drawn at this width (sampled every few milliseconds); then the button outside every
// component copies through the alias — only the commands in sight — the text outside reads copied
// through the alias, and the status region says so; a selection of the whole host never takes the
// sentence the status region says; the disabled neutral button reads as one (its words dimmed, its
// translucent fill fainter than an enabled one's, and still there: ink/4 beside ink/6).
const terminalReachProbe = `<script>
(function () {` + terminalProbeCommon + `
  var copied = [];
  Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText: function (text) {
    copied.push(text);
    return Promise.resolve();
  } } });
  var samples = 0, hidden = 0;
  (function watch() {
    var mark = rangeIn("kitwork-terminal-caret", "pick");
    if (mark) {
      samples++;
      var code = mark.startContainer.parentElement && mark.startContainer.parentElement.closest("code");
      if (code && !code.getClientRects().length) hidden++;
    }
    if (!report.caret) setTimeout(watch, 4);
  })();
  window.addEventListener("load", function () {
    try {
      until(function () { return samples > 0 && typed("pick") === "all"; }, 15000, function () {
        report.caret = JSON.stringify({ typed: samples > 5, hidden: hidden });
        byId("outside").click();
        after(300, function () {
          report.copied = JSON.stringify(copied);
          report.outside = JSON.stringify([byId("state").textContent, byId("copy-pick").textContent, status("pick")]);
          var selection = getSelection();
          selection.selectAllChildren(host("pick"));
          var text = String(selection);
          selection.removeAllRanges();
          report.selection = JSON.stringify({ commands: text.indexOf("echo done") >= 0, said: text.indexOf("Đã chép lệnh") >= 0 });
          var on = getComputedStyle(byId("on")), off = getComputedStyle(byId("off"));
          var alpha = function (colour) {
            var m = /\/\s*([\d.]+)\s*\)$/.exec(colour) || /^rgba\(.*,\s*([\d.]+)\s*\)$/.exec(colour);
            return m ? parseFloat(m[1]) : (colour === "transparent" ? 0 : 1);
          };
          var fill = alpha(off.backgroundColor);
          report.disabled = JSON.stringify({ words: on.color !== off.color, fill: fill > 0 && fill < alpha(on.backgroundColor) });
          send();
        });
      });
    } catch (problem) { report.error = String(problem && problem.stack || problem); send(); }
  });
})();
</script>`

// terminalWordsFiles is a site in a language given by router.language with two terminals: the
// clipboard takes the commands of "ok" and refuses those of "no".
func terminalWordsFiles(language string) map[string]string {
	return map[string]string{
		"localhost/router.work": `import { router } from "kitwork";

router.colors({ brand: "#c8742c" });
router.language("` + language + `");
`,
		"localhost/page.ui": `<page><content /></page>`,
		"localhost/content.ui": `<content padding="4" gap="4">
  <box id="ok"><box component="terminal"><code>echo ok</code><button id="copy-ok" click="copy()">Copy</button></box></box>
  <box id="no"><box component="terminal"><code>refuse me</code><button id="copy-no" click="copy()">Copy</button></box></box>
</content>`,
	}
}

// terminalWordsProbe copies both terminals and reads what each status region says.
const terminalWordsProbe = `<script>
(function () {` + terminalProbeCommon + `
  Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText: function (text) {
    return text.indexOf("refuse") >= 0 ? Promise.reject(new Error("refused")) : Promise.resolve();
  } } });
  window.addEventListener("load", function () {
    try {
      after(200, function () {
        byId("copy-ok").click();
        byId("copy-no").click();
        after(300, function () {
          report.said = JSON.stringify([document.documentElement.lang, status("ok"), status("no")]);
          send();
        });
      });
    } catch (problem) { report.error = String(problem && problem.stack || problem); send(); }
  });
})();
</script>`

// serveWithProbe serves a tenant's pages through a test server, the probe put before </body> of "/",
// and gathers the probe's report.
func serveWithProbe(t *testing.T, tenant *Tenant, probe string) (*httptest.Server, chan map[string]string) {
	t.Helper()
	var serving sync.Mutex
	reports := make(chan map[string]string, 1)
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
		forward.Header.Del("Accept-Encoding")
		answer := httptest.NewRecorder()
		serving.Lock()
		tenant.Serve(answer, forward)
		serving.Unlock()
		for name, values := range answer.Header() {
			response.Header()[name] = values
		}
		body := answer.Body.String()
		if request.URL.Path == "/" {
			if at := strings.LastIndex(body, "</body>"); at >= 0 {
				body = body[:at] + probe + body[at:]
			}
			response.Header().Del("Content-Length")
		}
		response.WriteHeader(answer.Code)
		_, _ = io.WriteString(response, body)
	}))
	t.Cleanup(server.Close)
	return server, reports
}

// TestBrowserTerminalReach proves in a real browser what the review of 12/10/2026 found missing:
//   - written in .ui outside every component, $pick.copy() and $pick.copied reach the terminal (no
//     markup put in by hand): the copy happens, and the text outside follows copied;
//   - a command a show hides at this width is neither typed nor copied, wide (PowerShell) or narrow
//     (bash);
//   - a selection of the whole host never takes what the status region says;
//   - a disabled neutral button's words are dimmed beside an enabled one's, its fill fainter;
//   - the status region speaks the page's language: English for en, Vietnamese for vi-VN.
func TestBrowserTerminalReach(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the terminal browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	page := serveUI(t, buildUISite(t, terminalReachFiles), "http://localhost/").Body.String()
	if !strings.Contains(page, `data-kitwork-event="click: $pick.copy()"`) {
		t.Fatalf("the button outside every component carries no event:\n%.3000s", page)
	}

	for _, run := range []struct {
		name   string
		width  int
		copied string
	}{
		{"wide", 1000, `["Get-ChildItem -Recurse -Force -ErrorAction SilentlyContinue\necho done"]`},
		{"narrow", 375, `["ls -la --color=never --group-directories-first\necho done"]`},
	} {
		server, reports := serveWithProbe(t, buildUISite(t, terminalReachFiles), terminalReachProbe)
		got := runBrowser(t, browser, server.URL+"/", "terminal reach, "+run.name, run.width, "", reports)
		if message, broke := got["error"]; broke {
			t.Fatalf("%s: the probe broke: %s", run.name, message)
		}
		for key, value := range map[string]string{
			"caret":     `{"typed":true,"hidden":0}`,
			"copied":    run.copied,
			"outside":   `["yes","Đã chép","status:reader:Đã chép lệnh vào bộ nhớ tạm"]`,
			"selection": `{"commands":true,"said":false}`,
			"disabled":  `{"words":true,"fill":true}`,
		} {
			if got[key] != value {
				t.Errorf("%s, %s:\n got  %s\n want %s", run.name, key, got[key], value)
			}
		}
	}

	for _, run := range []struct{ language, said string }{
		{"en", `["en","status:reader:Copied the commands to the clipboard","status:reader:Could not copy the commands"]`},
		{"vi-VN", `["vi-VN","status:reader:Đã chép lệnh vào bộ nhớ tạm","status:reader:Không chép được lệnh"]`},
	} {
		server, reports := serveWithProbe(t, buildUISite(t, terminalWordsFiles(run.language)), terminalWordsProbe)
		got := runBrowser(t, browser, server.URL+"/", "terminal words, "+run.language, 1000, "", reports)
		if message, broke := got["error"]; broke {
			t.Fatalf("%s: the probe broke: %s", run.language, message)
		}
		if got["said"] != run.said {
			t.Errorf("%s:\n got  %s\n want %s", run.language, got["said"], run.said)
		}
	}
}
