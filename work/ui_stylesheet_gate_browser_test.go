package work

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The browser gate of Kitwork UI's own CSS (decision K): the bugs the studies found in the CSS the
// engine wrote for .ui pages before (Tailwind words through jit/css, ordered alphabetically), each
// measured in a real browser on a page the engine serves now, and each with a control — the same page
// as the engine served it before the switch, which must still show the bug, so the measurement is
// known to tell the two apart.
//
// testdata/stylesheet_gate/site is the source (router.work and content.ui); the test boots it as a
// tenant and serves what the engine makes of it today: the document and its five stylesheets
// (reboot, colors, fonts, icons, page). testdata/stylesheet_gate/before is what the engine served for
// that same source before the switch: the document, its page.<hash>.css and icons.<hash>.css, as
// written (development mode), made by the build css-baseline captured (rebuilt from the snapshot of
// the files the switch changed; it serves the example pages byte for byte as the baseline holds
// them). Its scripts are not kept: the gate measures CSS, so neither side needs the kernel, and the
// probe opens the popover itself.
//
// One probe runs on both sides. It reads every case into a report of words (no pixel counts that
// depend on the typeface), so the same words are wanted on any machine. The browser runs in real
// time, not on the virtual clock of the other browser tests: the probe moves a real pointer through
// chrome.gpuBenchmarking (--enable-gpu-benchmarking), so :hover is the browser's own, and that input
// needs the compositor's frames. After reporting, the page closes its window, which ends the browser.
//
// The profiles, each a browser of its own, in the light scheme, the pointer moved in each:
//   - wide: a window of 1000px (medium holds, large does not);
//   - narrow: 600px (below medium);
//   - hover none: 1000px on a device that cannot hover (blink's primary and available hover types set
//     to none: (hover: none) matches), the pointer moved all the same, as a tap would;
//   - forced colors: 1000px in forced colours (Windows contrast themes; blink's inForcedColors).

// gateProfile is one browser run of the gate: a window width and blink's settings beyond the two
// every run has (runBrowser).
type gateProfile struct {
	name     string
	width    int
	settings string
}

var gateProfiles = []gateProfile{
	{name: "wide", width: 1000},
	{name: "narrow", width: 600},
	{name: "hover none", width: 1000, settings: "primaryHoverType=1,availableHoverTypes=1"},
	{name: "forced colors", width: 1000, settings: "inForcedColors=true"},
}

// gateProbe measures the cases of testdata/stylesheet_gate/site/content.ui (found by their id) and
// POSTs one report: case → words.
const gateProbe = `<script>
(function () {
  var byId = function (id) { return document.getElementById(id); };
  var style = function (element, property) { return getComputedStyle(element).getPropertyValue(property).trim(); };
  var box = function (element) { return element.getBoundingClientRect(); };
  var near = function (a, b) { return Math.abs(a - b) < 1; };
  function frames(count, then) { if (count <= 0) { then(); return; } requestAnimationFrame(function () { frames(count - 1, then); }); }
  function send(report) {
    var close = function () { window.close(); };
    fetch("/report", { method: "POST", body: JSON.stringify(report) }).then(close, close);
  }
  // hover puts the pointer on the middle of an element through the browser's own input, then reads
  // once the frames after the move have run
  function hover(element, read, then) {
    element.scrollIntoView({ block: "center" });
    frames(2, function () {
      var middle = box(element);
      chrome.gpuBenchmarking.pointerActionSequence([{ source: "mouse", actions: [
        { name: "pointerMove", x: Math.round(middle.left + middle.width / 2), y: Math.round(middle.top + middle.height / 2) }] }],
        function () { frames(3, function () { read(); then(); }); });
    });
  }
  // paint names the system colour a value is (forced colours paint with them): the one expected when
  // it is that one (two may share a value), else the first that matches, else the value itself
  function paint(value, expected) {
    var named = function (name) {
      var sample = document.createElement("span");
      sample.style.forcedColorAdjust = "none";
      sample.style.color = name;
      document.body.appendChild(sample);
      var same = getComputedStyle(sample).color === value;
      sample.remove();
      return same;
    };
    if (named(expected)) return expected;
    var names = ["Canvas", "CanvasText", "LinkText", "ButtonFace", "ButtonText", "GrayText"];
    for (var i = 0; i < names.length; i++) if (named(names[i])) return names[i];
    return value;
  }
  function turned(element) { return style(element, "rotate") === "180deg" || /^matrix\(-1, /.test(style(element, "transform")); }
  function measure() {
    var report = {};
    report.medium = String(matchMedia("(min-width: 768px)").matches);
    report["hover none"] = String(matchMedia("(hover: none)").matches);
    report["forced colors"] = String(matchMedia("(forced-colors: active)").matches);
    report.pointer = String(typeof chrome !== "undefined" && !!chrome.gpuBenchmarking);

    // flex="column" medium:flex="row" align="top": where the two children stand in their box
    var align = byId("align"), frame = box(align), first = box(align.children[0]), second = box(align.children[1]);
    var start = first.left - frame.left, end = frame.right - second.right;
    report.align = style(align, "flex-direction") +
      " tops=" + (near(first.top, frame.top) && near(second.top, frame.top) ? "at-top" : "apart") +
      " across=" + (near(start, 0) ? "start" : near(start, end) ? "centred" : "other");

    // lines="2" show="false" medium:show="true": hidden, or shown and clamped to two lines
    var lines = byId("lines"), lineHeight = parseFloat(style(lines, "line-height"));
    report.lines = style(lines, "display") === "none" ? "hidden" :
      "shown " + (box(lines).height <= 2 * lineHeight + 0.5 ? "clamped" : "unclamped");

    // <button show="false" medium:show="true">: hidden, or shown with the display a button has
    report["button show"] = style(byId("button-show"), "display");

    // <card background="brand" radius="full">: what is written beats the card's look
    var card = byId("card");
    report.card = style(card, "background-color") + " " + style(card, "border-top-left-radius");

    // open:rotate="180" inside an open accordion that holds a closed one: the nearest that opens
    var chevrons = document.querySelectorAll("summary [class*=icon-chevron-down]");
    report.open = "outer=" + (turned(chevrons[0]) ? "turned" : "still") + " inner=" + (turned(chevrons[1]) ? "turned" : "still");

    // <badge color="success" background="paper">: the written background beats the tint
    var badge = byId("badge"), tint = byId("badge-plain"), paper = style(byId("card-plain"), "background-color");
    report.badge = "background=" + (style(badge, "background-color") === paper ? "written" :
      style(badge, "background-color") === style(tint, "background-color") ? "tint" : style(badge, "background-color")) +
      " color=" + (style(badge, "color") === style(tint, "color") ? "tint" : style(badge, "color"));

    // a popover holding a placed child (the engine writes place-anchor on it): it floats below its
    // opener, at the start of the box that holds both, not beside the opener in their row; the child
    // sits at its top-end corner
    var popover = byId("popover");
    popover.hidden = false; // as the dropdown shows it when open
    var dropdown = popover.parentElement, opener = box(dropdown.querySelector("button"));
    var surface = box(popover), corner = box(popover.firstElementChild);
    var edge = parseFloat(style(popover, "border-top-width"));
    report.popover = style(popover, "position") +
      " at=" + (near(surface.left, box(dropdown).left) ? "start" : surface.left >= opener.right - 0.5 ? "beside" : "elsewhere") +
      " below=" + (surface.top > opener.bottom ? "yes" : "no") +
      " corner=" + (near(corner.top, surface.top + edge) && near(corner.right, surface.right - edge) ? "top-end" : "elsewhere");
    popover.hidden = true;

    // forced colours: each icon (a mask over its background colour) in the system colour of where it
    // stands; never the canvas it sits on
    var icon = function (element, expected) { return paint(style(element, "background-color"), expected); };
    report.icons = "text=" + icon(byId("icon-text"), "CanvasText") + " link=" + icon(byId("link").querySelector("i"), "LinkText") +
      " button=" + icon(byId("button-icon").querySelector("i"), "ButtonText");

    var resting = style(byId("gated"), "background-color");
    var steps = [];
    if (report.pointer === "true") {
      steps.push(function (then) {
        hover(byId("disabled"), function () {
          report["disabled hovered"] = "hover=" + byId("disabled").matches(":hover") + " opacity=" + style(byId("disabled"), "opacity");
        }, then);
      });
      steps.push(function (then) {
        hover(byId("hovered"), function () {
          report.hovered = "hover=" + byId("hovered").matches(":hover") + " opacity=" + style(byId("hovered"), "opacity");
        }, then);
      });
      steps.push(function (then) {
        hover(byId("gated"), function () {
          var shown = style(byId("gated"), "background-color");
          report["gated hovered"] = "hover=" + byId("gated").matches(":hover") + " background=" +
            (shown === resting ? "resting" : shown === "rgb(200, 116, 44)" ? "brand" : shown);
        }, then);
      });
    }
    var next = function () { var step = steps.shift(); if (step) step(next); else send(report); };
    next();
  }
  window.addEventListener("load", function () {
    (document.fonts ? document.fonts.ready : Promise.resolve()).then(function () {
      frames(2, function () {
        try { measure(); } catch (problem) { send({ error: String(problem && problem.stack || problem) }); } // a broken probe reports, not hangs
      });
    });
  });
})();
</script>`

// gateServer serves one side of the gate: the document at / with the probe in it, every other path
// from files, and the reports the probe sends.
type gateServer struct {
	*httptest.Server
	reports chan map[string]string
}

func newGateServer(t *testing.T, document func() []byte, file func(path string) ([]byte, bool)) *gateServer {
	t.Helper()
	side := &gateServer{reports: make(chan map[string]string, 1)}
	side.Server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/":
			page := string(document())
			at := strings.LastIndex(page, "</body>")
			if at < 0 {
				t.Errorf("the document has no </body>:\n%.500s", page)
				http.Error(response, "no body", http.StatusInternalServerError)
				return
			}
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			response.Header().Set("Cache-Control", "no-store")
			_, _ = io.WriteString(response, page[:at]+gateProbe+page[at:])
		case "/report":
			var report map[string]string
			body, _ := io.ReadAll(request.Body)
			if err := json.Unmarshal(body, &report); err != nil {
				t.Errorf("report %s: %v", body, err)
			}
			select {
			case side.reports <- report:
			default:
			}
		default:
			body, ok := file(request.URL.Path)
			if !ok {
				http.NotFound(response, request)
				return
			}
			if strings.HasSuffix(request.URL.Path, ".css") {
				response.Header().Set("Content-Type", "text/css; charset=utf-8")
			}
			_, _ = response.Write(body)
		}
	}))
	t.Cleanup(side.Close)
	return side
}

// measure opens the side in a headless browser of the profile and returns the probe's report.
func (side *gateServer) measure(t *testing.T, browser string, profile gateProfile) map[string]string {
	t.Helper()
	return runBrowser(t, browser, side.URL+"/", profile.name, profile.width, profile.settings, side.reports)
}

// runBrowser opens url in a headless browser running in real time, the window width wide, and returns
// the first report the page sends on reports. blink's settings are settings plus two every run has:
// the light scheme (the system may be dark), and scripts may close their window — the page closes it
// once it has reported, which ends the browser, even after Drive moves have grown its history (the
// browser is killed if it lingers).
func runBrowser(t *testing.T, browser, url, name string, width int, settings string, reports <-chan map[string]string) map[string]string {
	t.Helper()
	arguments := []string{"--headless=new", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage",
		"--disable-background-networking", "--disable-default-apps", "--disable-extensions", "--disable-sync",
		"--metrics-recording-only", "--no-first-run", "--enable-gpu-benchmarking",
		"--window-size=" + strconv.Itoa(width) + ",800",
		"--user-data-dir=" + filepath.Join(t.TempDir(), "profile")}
	if runtime.GOOS == "windows" {
		arguments = append(arguments, "--disable-background-mode", "--disable-breakpad", "--disable-crashpad-for-testing")
	}
	blink := "preferredColorScheme=1,allowScriptsToCloseWindows=true" // light; window.close() always ends the run
	if settings != "" {
		blink += "," + settings
	}
	arguments = append(arguments, "--blink-settings="+blink, url)
	deadline, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	command := exec.CommandContext(deadline, browser, arguments...)
	var output strings.Builder
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		t.Fatalf("start %s: %v", browser, err)
	}
	exited := make(chan struct{})
	go func() { _ = command.Wait(); close(exited) }()
	var report map[string]string
	select {
	case report = <-reports:
		select {
		case <-exited:
		case <-time.After(10 * time.Second):
			_ = command.Process.Kill()
			<-exited
		}
	case <-exited:
		select {
		case report = <-reports:
		default:
			t.Fatalf("%s: the browser ended without a report\n%.1500s", name, output.String())
		}
	case <-deadline.Done():
		<-exited
		t.Fatalf("%s: no report within the time\n%.1500s", name, output.String())
	}
	return report
}

// findHeadlessBrowserForGate is the browser the gate runs in: Chrome, Chromium or Edge, unless
// KITWORK_BROWSER_TESTS turns the browser tests off.
func findHeadlessBrowserForGate() string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("KITWORK_BROWSER_TESTS"))) {
	case "0", "false", "off", "no":
		return ""
	}
	for _, name := range []string{"google-chrome", "chromium", "chromium-browser", "chrome", "msedge"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	if runtime.GOOS != "windows" {
		return ""
	}
	for _, path := range []string{
		filepath.Join(os.Getenv("ProgramFiles"), "Google", "Chrome", "Application", "chrome.exe"),
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Google", "Chrome", "Application", "chrome.exe"),
	} {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return ""
}

// gateSides boots the gate's source as a tenant (the engine now) and serves the capture of before.
func gateSides(t *testing.T) (now, before *gateServer) {
	t.Helper()
	files := map[string]string{}
	for _, name := range []string{"router.work", "content.ui"} {
		source, err := os.ReadFile(filepath.Join("testdata", "stylesheet_gate", "site", name))
		if err != nil {
			t.Fatal(err)
		}
		files["localhost/"+name] = string(source)
	}
	tenant := buildUISite(t, files)
	var serving sync.Mutex
	serve := func(path string) (int, []byte) {
		serving.Lock()
		defer serving.Unlock()
		recorded := serveUI(t, tenant, "http://localhost"+path)
		return recorded.Code, recorded.Body.Bytes()
	}
	now = newGateServer(t, func() []byte {
		code, body := serve("/")
		if code != http.StatusOK {
			t.Errorf("the gate's page answers %d:\n%.800s", code, body)
		}
		return body
	}, func(path string) ([]byte, bool) {
		code, body := serve(path)
		return body, code == http.StatusOK
	})
	captured := filepath.Join("testdata", "stylesheet_gate", "before")
	before = newGateServer(t, func() []byte {
		body, err := os.ReadFile(filepath.Join(captured, "index.html"))
		if err != nil {
			t.Error(err)
		}
		return body
	}, func(path string) ([]byte, bool) {
		if !strings.HasSuffix(path, ".css") {
			return nil, false // the gate measures CSS: the scripts of before are not kept
		}
		body, err := os.ReadFile(filepath.Join(captured, filepath.FromSlash(strings.TrimPrefix(path, "/"))))
		return body, err == nil
	})
	return now, before
}

// gateCase is one bug of the CSS before: in which profile, which word of the report, what the CSS of
// .ui must show now, and what the CSS before showed (the bug, which the control must still see).
type gateCase struct {
	name, profile, key, now, before string
}

var gateCases = []gateCase{
	// The align of a responsive direction was resolved once, for the direction written without a
	// prefix (column: items-center justify-start), so from medium the row kept the column's axes:
	// children at the start, centred top to bottom. Now align only sets variables the direction reads.
	{`flex="column" medium:flex="row" align="top", from medium`, "wide", "align",
		"row tops=at-top across=centred", "row tops=apart across=start"},
	// show="false" medium:show="true" guessed the display a class gave back (md:block): the clamp of
	// lines="2" was lost from medium. Now show-true is revert-layer in a layer of its own.
	{`lines="2" show="false" medium:show="true", from medium`, "wide", "lines",
		"shown clamped", "shown unclamped"},
	// and below medium, .hidden came before .line-clamp-2 alphabetically: the text was never hidden.
	{`lines="2" show="false" medium:show="true", below medium`, "narrow", "lines",
		"hidden", "shown clamped"},
	// a button shown from medium was md:inline (a row item: block), no longer the button's flex
	{`<button show="false" medium:show="true">, from medium`, "wide", "button show",
		"flex", "block"},
	// and below medium .hidden lost to .inline-flex: the button stayed
	{`<button show="false" medium:show="true">, below medium`, "narrow", "button show",
		"none", "flex"},
	// the card's own look (bg-paper rounded-xl) sorted after the author's bg-brand rounded-full. Now the
	// look is in the elements layer and what is written in the page layer.
	{`<card background="brand" radius="full">`, "wide", "card",
		"rgb(200, 116, 44) 999px", "rgb(255, 255, 255) 12px"},
	// open: was Tailwind's group-open: any open accordion above turned the chevron. Now it follows the
	// nearest element that opens.
	{`open:rotate="180" in a closed accordion inside an open one`, "wide", "open",
		"outer=turned inner=still", "outer=turned inner=turned"},
	// states sorted alphabetically: disabled: before hover:, so a hovered disabled button lit up
	{`<button disabled="true" hover:opacity="80" disabled:opacity="50">, hovered`, "wide", "disabled hovered",
		"hover=true opacity=0.5", "hover=true opacity=0.8"},
	// the badge's tint (bg-success-soft) sorted after the written bg-paper. Now color sets the tint
	// variable the badge reads, so a written background wins whatever the order.
	{`<badge color="success" background="paper">`, "wide", "badge",
		"background=written color=tint", "background=tint color=tint"},
	// the parent of a placed child got .relative, which sorted after the popover's .absolute: the
	// popover fell into its row, beside its opener. Now place-anchor weighs nothing in the elements
	// layer, under the popover's own place in the page layer.
	{`a <popover> holding a placed child`, "wide", "popover",
		"absolute at=start below=yes corner=top-end", "relative at=beside below=no corner=top-end"},
	// hover: was a bare :hover: a touch screen kept the hover look after a tap. Now it is inside
	// @media (hover: hover).
	{`hover:background="brand" on a device that cannot hover`, "hover none", "gated hovered",
		"hover=true background=resting", "hover=true background=brand"},
	// icons are a mask over background-color: currentColor, which forced colours repaint as the
	// canvas: every icon vanished. Now reboot.css paints them in the system colour of where they stand.
	{`<icon> in text, in a link and in a button, in forced colours`, "forced colors", "icons",
		"text=CanvasText link=LinkText button=ButtonText", "text=Canvas link=Canvas button=Canvas"},
}

// gateHarness are the words every report must have, on both sides: the profile took (the width, the
// hover type, forced colours), and the pointer really hovers — an enabled button lights up, and the
// gated hover applies where the device can hover.
var gateHarness = map[string]map[string]string{
	"wide":          {"medium": "true", "hover none": "false", "forced colors": "false", "pointer": "true", "hovered": "hover=true opacity=0.8", "gated hovered": "hover=true background=brand"},
	"narrow":        {"medium": "false", "hover none": "false", "forced colors": "false", "pointer": "true", "hovered": "hover=true opacity=0.8"},
	"hover none":    {"medium": "true", "hover none": "true", "forced colors": "false", "pointer": "true"},
	"forced colors": {"medium": "true", "hover none": "false", "forced colors": "true", "pointer": "true"},
}

// TestBrowserStylesheetGateRepros measures each bug the studies of decision K found, on the page the
// engine serves now (it must hold) and on the same page as the engine served it before the switch
// (the control: it must still show that bug, as it did, so the measurement can tell them apart).
func TestBrowserStylesheetGateRepros(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the stylesheet gate in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	now, before := gateSides(t)
	reports := map[string]map[string]map[string]string{"now": {}, "before": {}}
	for _, profile := range gateProfiles {
		reports["now"][profile.name] = now.measure(t, browser, profile)
		reports["before"][profile.name] = before.measure(t, browser, profile)
	}
	for _, side := range []string{"now", "before"} {
		for _, profile := range gateProfiles {
			if message, broke := reports[side][profile.name]["error"]; broke {
				t.Fatalf("the probe broke on %s, %s: %s", side, profile.name, message)
			}
		}
		for profile, words := range gateHarness {
			for key, want := range words {
				if got := reports[side][profile][key]; got != want {
					t.Errorf("harness, %s, %s: %s = %q, want %q (the profile or the pointer did not take)", side, profile, key, got, want)
				}
			}
		}
	}
	for _, bug := range gateCases {
		if got := reports["now"][bug.profile][bug.key]; got != bug.now {
			t.Errorf("%s (%s): the CSS of .ui shows %q, want %q", bug.name, bug.profile, got, bug.now)
		}
		if got := reports["before"][bug.profile][bug.key]; got != bug.before {
			t.Errorf("control, %s (%s): the CSS before the switch shows %q, but it had the bug %q: the gate no longer tells them apart", bug.name, bug.profile, got, bug.before)
		}
	}
	if t.Failed() {
		for _, side := range []string{"now", "before"} {
			for _, profile := range gateProfiles {
				t.Logf("%s, %s: %v", side, profile.name, reports[side][profile.name])
			}
		}
	}
}
