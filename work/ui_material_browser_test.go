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

// materialBrowserFiles is a site whose header is pinned and turns to glass once the page scrolls under
// it (scrolled:material="glass": its tint the page's canvas, the background the engine gives a pinned
// frame that writes none), with a brand glass (background="brand" material="glass"), a paper glass
// whose hover background is brand, a thin glass inside a brand frame, a card that is glass below
// medium and none from it, and solid samples of canvas, paper and brand to tell the tint by. Glass
// first, background second (13/10/2026): a glass/30 over background="paper/80", a brand glass/30, a
// box that turns to glass/30 over paper/80 once the page scrolls (scrolled:material="glass/30"
// scrolled:background="paper/80", the owner's words), and material="none" over paper/80. Its colours
// have a dark look, and the page follows the device (router.appearance is not written).
var materialBrowserFiles = map[string]string{
	"localhost/router.work": `import { router } from "kitwork";

router.colors({
  brand: "#c8742c",
  canvas: { light: "#f6f3ee", dark: "#141110" },
  paper: { light: "#ffffff", dark: "#1f1a17" },
  ink: { light: "#2b211b", dark: "#f3ece6" }
});
`,
	"localhost/page.ui":   `<page><header /><content /></page>`,
	"localhost/header.ui": `<header pin="top" padding="4" scrolled:material="glass"><text>Quán</text></header>`,
	"localhost/content.ui": `<content>
  <box id="brand" background="brand" material="glass" padding="4" color="brand.on"><text>Kính màu nhãn</text></box>
  <box id="hovered" material="glass" hover:background="brand" padding="4" hover:color="brand.on"><text>Rê chuột</text></box>
  <box background="brand" padding="4"><box id="nested" material="glass/60" padding="2"><text>Kính trong khung nhãn</text></box></box>
  <card id="card-none" material="glass" medium:material="none" padding="4"><text>Thẻ không kính từ medium</text></card>
  <box id="solid-canvas" background="canvas" padding="4"><text>canvas</text></box>
  <box id="solid-paper" background="paper" padding="4"><text>paper</text></box>
  <box id="solid-brand" background="brand" padding="4" color="brand.on"><text>brand</text></box>
  <box id="glass-thirty" background="paper/80" material="glass/30" padding="4"></box>
  <box id="brand-thirty" background="brand" material="glass/30" padding="4"></box>
  <box id="scrolled-thirty" scrolled:material="glass/30" scrolled:background="paper/80" padding="4"></box>
  <box id="none-eighty" background="paper/80" material="none" padding="4"></box>
  <box height="3000px"></box>
</content>`,
}

// materialProbe reads the page at the top, scrolled 600px and back at the top (each after the frames a
// scroll needs and the header's short transition), then puts the pointer on the paper glass. A surface
// is read in words: its backdrop filter as computed, the alpha of its background and its tint, the
// solid sample whose colour it has (canvas, paper, brand) or the colour itself.
//
// Opened with ?fallback it first makes this browser one that cannot blur the backdrop: every
// @supports not ((backdrop-filter…) or (-webkit-backdrop-filter…)) rule of the page's stylesheets is
// put back at its own place (same layer block, same @media, same index) under a condition that always
// holds, @supports (display:block), so its rules apply exactly where the stylesheet writes them, as in
// a browser whose @supports-not branch holds; and one unlayered rule turns the backdrop filter off with
// !important, as such a browser ignores the declaration. It reports how many rules it turned
// (fallbacks), so a probe that found none cannot pass. CSS.supports() cannot do this: it answers the
// script, not the stylesheet's own @supports.
const materialProbe = `<script>
(function () {
  var byId = function (id) { return document.getElementById(id); };
  function frames(count, then) { if (count <= 0) { then(); return; } requestAnimationFrame(function () { frames(count - 1, then); }); }
  function settle(then) { frames(3, function () { setTimeout(then, 300); }); }
  function scrollThen(y, then) { window.scrollTo(0, y); settle(then); }
  function send(report) {
    var close = function () { window.close(); };
    fetch("/report", { method: "POST", body: JSON.stringify(report) }).then(close, close);
  }
  // a computed colour as its channels (0 to 255) and its alpha: rgb(), rgba() and color(srgb …) alike
  function colour(value) {
    var m = /^rgba?\((\d+), (\d+), (\d+)(?:, ([\d.]+))?\)$/.exec(value);
    if (m) return { rgb: [m[1], m[2], m[3]].join(","), alpha: m[4] === undefined ? 1 : parseFloat(m[4]) };
    m = /^color\(srgb ([\d.e+-]+) ([\d.e+-]+) ([\d.e+-]+)(?: \/ ([\d.]+))?\)$/.exec(value);
    if (m) return { rgb: [m[1], m[2], m[3]].map(function (c) { return Math.round(parseFloat(c) * 255); }).join(","), alpha: m[4] === undefined ? 1 : parseFloat(m[4]) };
    return { rgb: value, alpha: NaN };
  }
  function surface(element) {
    var style = getComputedStyle(element), shown = colour(style.backgroundColor), tint = shown.rgb;
    var samples = ["canvas", "paper", "brand"];
    for (var i = 0; i < samples.length; i++) {
      if (colour(getComputedStyle(byId("solid-" + samples[i])).backgroundColor).rgb === shown.rgb) { tint = samples[i]; break; }
    }
    return "filter=" + style.backdropFilter + " alpha=" + Math.round(shown.alpha * 100) / 100 + " tint=" + tint;
  }
  function header() { return surface(document.querySelector("header")); }
  // the @supports-not rules of the glass, turned to hold, each where it stands; how many
  function fallBack() {
    var turned = 0;
    function walk(list, owner) {
      for (var i = 0; i < list.length; i++) {
        var rule = list[i];
        if (rule.type === CSSRule.SUPPORTS_RULE && rule.conditionText.trim().indexOf("not") === 0 && rule.conditionText.indexOf("backdrop-filter") >= 0) {
          var inner = [];
          for (var j = 0; j < rule.cssRules.length; j++) inner.push(rule.cssRules[j].cssText);
          owner.deleteRule(i);
          owner.insertRule("@supports (display:block){" + inner.join("") + "}", i);
          turned++;
        } else if (rule.cssRules) {
          walk(rule.cssRules, rule);
        }
      }
    }
    for (var k = 0; k < document.styleSheets.length; k++) walk(document.styleSheets[k].cssRules, document.styleSheets[k]);
    var off = document.createElement("style");
    off.textContent = "*{-webkit-backdrop-filter:none!important;backdrop-filter:none!important}";
    document.head.appendChild(off);
    return turned;
  }
  window.addEventListener("load", function () {
    var fallbacks = location.search.indexOf("fallback") >= 0 ? String(fallBack()) : "none";
    settle(function () {
      try {
        var report = { fallbacks: fallbacks }, bar = getComputedStyle(document.querySelector("header"));
        report.scheme = "dark=" + matchMedia("(prefers-color-scheme: dark)").matches + " theme=" + document.documentElement.getAttribute("data-theme") +
          " canvas=" + colour(getComputedStyle(byId("solid-canvas")).backgroundColor).rgb;
        report.reduced = String(matchMedia("(prefers-reduced-transparency: reduce)").matches);
        report.pointer = String(typeof chrome !== "undefined" && !!chrome.gpuBenchmarking);
        report.top = header();
        report.transition = bar.transitionProperty + " " + bar.transitionDuration;
        report.brand = surface(byId("brand"));
        report.resting = surface(byId("hovered"));
        report.nested = surface(byId("nested"));
        report["card none"] = surface(byId("card-none"));
        report["glass thirty"] = surface(byId("glass-thirty"));
        report["brand thirty"] = surface(byId("brand-thirty"));
        report["none eighty"] = surface(byId("none-eighty"));
        report["scrolled thirty top"] = surface(byId("scrolled-thirty"));
        scrollThen(600, function () {
          report.scrolled = header() + " flag=" + document.documentElement.hasAttribute("data-kitwork-scrolled");
          report["scrolled thirty"] = surface(byId("scrolled-thirty"));
          scrollThen(0, function () {
            report.back = header() + " flag=" + document.documentElement.hasAttribute("data-kitwork-scrolled");
            if (report.pointer !== "true") { send(report); return; }
            var target = byId("hovered");
            target.scrollIntoView({ block: "center" });
            frames(2, function () {
              var middle = target.getBoundingClientRect();
              chrome.gpuBenchmarking.pointerActionSequence([{ source: "mouse", actions: [
                { name: "pointerMove", x: Math.round(middle.left + middle.width / 2), y: Math.round(middle.top + middle.height / 2) }] }],
                function () { settle(function () { report.hovered = "hover=" + target.matches(":hover") + " " + surface(target); send(report); }); });
            });
          });
        });
      } catch (problem) { send({ error: String(problem && problem.stack || problem) }); } // a broken probe reports, not hangs
    });
  });
})();
</script>`

// TestBrowserMaterialGlass proves material (decision M) in a real browser on a page the engine serves,
// in light and in dark, with the reader's transparency allowed and reduced (blink's own setting: a
// browser follows the system's, and Windows with transparency effects off reports reduce, as the
// owner's does):
//   - the pinned header with scrolled:material="glass" has no backdrop filter and its solid canvas at
//     the top; once the page scrolls under it, blur(12px) saturate(1.8) and its canvas at alpha 0.75,
//     the canvas of the mode it shows; back at the top, solid again. Its transition is exactly
//     background-color and the backdrop filter, both spellings, over .15s;
//   - background="brand" material="glass" is brand glass, the colour written once; a glass with no
//     background is paper glass, also inside a brand frame (it never takes the frame's colour), and
//     glass/60 is 60% over blur(8px); a card's material="none" gives back its paper, no filter;
//   - a hover background still wins over a base glass while the pointer is on it: solid brand (the
//     control of the order: the glass wins over the background only at its own condition);
//   - with reduced transparency the glass still shows (decision of 13/10/2026: Kitwork glass shows like
//     the glass of the sites written with Tailwind's backdrop-blur, which no media query turns off):
//     the scrolled header and every glass have their blur and their translucent tint, exactly as with
//     it allowed. CONTROL: the browser does report reduce there, so the setting took;
//   - glass first, background second (decision of 13/10/2026): background="paper/80"
//     material="glass/30" is paper at 30% over blur(8px), not the 24% of paper/80 read by the glass;
//     brand glass/30 is brand at 30%; scrolled:material="glass/30" scrolled:background="paper/80" is
//     nothing at the top and paper glass at 30% once scrolled; material="none" over paper/80 gives back
//     paper/80;
//   - in a browser that cannot blur the backdrop (the profile "no backdrop-filter", the light one opened
//     with ?fallback: see materialProbe) each frame shows its background as written: paper/80 under
//     glass/30 and under the scrolled glass/30, solid brand under brand glass/30 and the brand glass,
//     solid paper for a glass with no background, the header's solid canvas once scrolled; none over
//     paper/80 is still paper/80, and the hover background still wins. CONTROL: the probe turned the
//     page's five fallback rules, one per glass class (material-glass, material-glass/60,
//     material-glass/30, scrolled:material-glass, scrolled:material-glass/30).
func TestBrowserMaterialGlass(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the material browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant := buildUISite(t, materialBrowserFiles)
	if body := serveUI(t, tenant, "http://localhost/").Body.String(); !scrolledTagRe.MatchString(body) ||
		!strings.Contains(body, "scrolled:material-glass") || !strings.Contains(body, "background-canvas") {
		t.Fatalf("the site is not the walk's: the header must be pinned, glass once scrolled, with the scrolled script:\n%.1500s", body)
	}

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
				body = body[:at] + materialProbe + body[at:]
			}
			response.Header().Del("Content-Length")
		}
		response.WriteHeader(answer.Code)
		_, _ = io.WriteString(response, body)
	}))
	defer server.Close()

	const (
		glass    = "filter=blur(12px) saturate(1.8) alpha=0.75"
		thin     = "filter=blur(8px) saturate(1.8) alpha=0.6"
		thirty   = "filter=blur(8px) saturate(1.8) alpha=0.3"
		eighty   = "filter=none alpha=0.8"
		solid    = "filter=none alpha=1"
		clear    = "filter=none alpha=0 tint=0,0,0"
		lightRGB = "246,243,238"
		darkRGB  = "20,17,16"
	)
	// what the light page shows with the reader's transparency allowed
	light := map[string]string{
		"fallbacks":           "none",
		"reduced":             "false",
		"top":                 solid + " tint=canvas",
		"transition":          "background-color, -webkit-backdrop-filter, backdrop-filter 0.15s, 0.15s, 0.15s",
		"scrolled":            glass + " tint=canvas flag=true",
		"back":                solid + " tint=canvas flag=false",
		"brand":               glass + " tint=brand",
		"resting":             glass + " tint=paper",
		"nested":              thin + " tint=paper",
		"card none":           solid + " tint=paper",
		"hovered":             "hover=true filter=blur(12px) saturate(1.8) alpha=1 tint=brand",
		"glass thirty":        thirty + " tint=paper",
		"brand thirty":        thirty + " tint=brand",
		"none eighty":         eighty + " tint=paper",
		"scrolled thirty top": clear,
		"scrolled thirty":     thirty + " tint=paper",
	}
	// with it reduced the page shows exactly the same (13/10/2026: the glass always shows), and the
	// browser reports reduce (the control: the setting took)
	reduced := map[string]string{}
	for key, value := range light {
		reduced[key] = value
	}
	reduced["reduced"] = "true"
	// where the browser cannot blur, every frame shows its background as written (glass first,
	// background second): the five fallback rules turned, no filter anywhere
	fallback := map[string]string{
		"fallbacks":           "5",
		"reduced":             "false",
		"top":                 solid + " tint=canvas",
		"transition":          light["transition"],
		"scrolled":            solid + " tint=canvas flag=true",
		"back":                solid + " tint=canvas flag=false",
		"brand":               solid + " tint=brand",
		"resting":             solid + " tint=paper",
		"nested":              solid + " tint=paper",
		"card none":           solid + " tint=paper",
		"hovered":             "hover=true " + solid + " tint=brand",
		"glass thirty":        eighty + " tint=paper",
		"brand thirty":        solid + " tint=brand",
		"none eighty":         eighty + " tint=paper",
		"scrolled thirty top": clear,
		"scrolled thirty":     eighty + " tint=paper",
	}
	profiles := []struct {
		name, query, settings string
		want                  map[string]string
	}{
		{"light", "", "prefersReducedTransparency=false", light},
		{"dark", "", "preferredColorScheme=0,prefersReducedTransparency=false", map[string]string{
			"fallbacks":       "none",
			"reduced":         "false",
			"top":             solid + " tint=canvas",
			"scrolled":        glass + " tint=canvas flag=true",
			"back":            solid + " tint=canvas flag=false",
			"brand":           glass + " tint=brand",
			"resting":         glass + " tint=paper",
			"nested":          thin + " tint=paper",
			"card none":       solid + " tint=paper",
			"hovered":         "hover=true filter=blur(12px) saturate(1.8) alpha=1 tint=brand",
			"glass thirty":    thirty + " tint=paper",
			"brand thirty":    thirty + " tint=brand",
			"none eighty":     eighty + " tint=paper",
			"scrolled thirty": thirty + " tint=paper",
		}},
		{"reduced transparency", "", "prefersReducedTransparency=true", reduced},
		{"no backdrop-filter", "?fallback", "prefersReducedTransparency=false", fallback},
	}
	for _, profile := range profiles {
		got := runBrowser(t, browser, server.URL+"/"+profile.query, "material, "+profile.name, 1000, profile.settings, reports)
		if message, broke := got["error"]; broke {
			t.Fatalf("%s: the probe broke: %s", profile.name, message)
		}
		if got["pointer"] != "true" {
			t.Errorf("%s: the pointer did not take (chrome.gpuBenchmarking): %v", profile.name, got)
		}
		// the scheme took: the canvas of the mode, and the dark look written on <html> in dark
		dark := profile.name == "dark"
		canvas := lightRGB
		if dark {
			canvas = darkRGB
		}
		failed := false
		if !strings.Contains(got["scheme"], "dark="+map[bool]string{true: "true", false: "false"}[dark]) || !strings.HasSuffix(got["scheme"], " canvas="+canvas) ||
			dark != strings.Contains(got["scheme"], "theme=dark") {
			t.Errorf("%s: the scheme did not take: %s", profile.name, got["scheme"])
			failed = true
		}
		for key, want := range profile.want {
			if got[key] != want {
				t.Errorf("%s, %s:\n got  %s\n want %s", profile.name, key, got[key], want)
				failed = true
			}
		}
		if failed {
			t.Logf("%s: %v", profile.name, got)
		}
	}
}
