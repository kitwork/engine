package work

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// lazyBrowserFiles is a site whose page is long on a phone: a section near the top, then, far below,
// sections that write lazy (one estimating 500px by lazy="true", one 900px, one undone from medium on),
// and a lazy box in a centred column whose content is 300px wide. A pinned header stands over it all.
var lazyBrowserFiles = map[string]string{
	"localhost/router.work": `import { router } from "kitwork";

router.colors({ brand: "#c8742c", canvas: "#f6f3ee", paper: "#ffffff", ink: "#2b211b" });
`,
	"localhost/page.ui":   `<page><header /><content /></page>`,
	"localhost/header.ui": `<header pin="top" padding="4"><text>Quán</text></header>`,
	"localhost/content.ui": `<content gap="4">
  <section id="near" lazy="true" gap="2">` + strings.Repeat(`<text>Gần màn hình</text>`, 20) + `</section>
  <box height="3000px"></box>
  <section id="far" lazy="true" gap="2">` + strings.Repeat(`<text>Xa màn hình</text>`, 20) + `</section>
  <section id="sized" lazy="900px" gap="2">` + strings.Repeat(`<text>Xa nữa</text>`, 10) + `</section>
  <box align="center"><box id="centred" lazy="true"><box width="300px" height="40px" background="brand"></box></box></box>
  <section id="undone" lazy="true" medium:lazy="false" gap="2">` + strings.Repeat(`<text>Từ medium luôn vẽ</text>`, 10) + `</section>
  <box height="3000px"></box>
  <text id="end">Cuối trang</text>
</content>`,
}

// lazyState reads, for each id, whether the browser draws what the element holds (its first child is
// not skipped for content-visibility) and its height in px (and, for the centred box, its width).
const lazyState = `(function (ids) {
  return ids.map(function (id) {
    var element = document.getElementById(id), box = element.getBoundingClientRect();
    return id + "=" + (element.firstElementChild.checkVisibility({ contentVisibilityAuto: true }) ? "drawn" : "skipped") +
      " " + Math.round(box.height) + (id === "centred" ? "x" + Math.round(box.width) : "");
  }).join(" ");
})`

// frames waits two frames: whatever the last change asked of the browser is drawn.
const frames = `new Promise(function (done) { requestAnimationFrame(function () { requestAnimationFrame(function () { done(""); }); }); })`

// emulate sets the screen the page lays out for: Lighthouse's phone (412×823) or a desktop.
func emulate(d *devtools, width, height int) {
	d.call("Emulation.setDeviceMetricsOverride", map[string]any{"width": width, "height": height, "deviceScaleFactor": 1, "mobile": width < 768})
}

// TestBrowserLazySkipsWhatIsFar proves lazy in a real browser, on a phone's screen (412×823):
//   - at load the section near the top is drawn; the ones far below are skipped and keep their estimate
//     (lazy="true" 500px, lazy="900px" 900px);
//   - the skipped box in a centred column is 0 wide, never an estimate: the page does not overflow the
//     phone sideways (scrollWidth is the screen's);
//   - scrolled to, a section is drawn at its own height and the box at its own width; scrolled away
//     again they are skipped but keep that size (auto: nothing below them moves);
//   - print draws everything (the page under print media);
//   - lazy="false" from medium: on a desktop screen the far section that undoes it is drawn from the
//     start, the one that does not stays skipped.
func TestBrowserLazySkipsWhatIsFar(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the lazy browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant := buildUISite(t, lazyBrowserFiles)
	var serving sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		forward := request.Clone(request.Context())
		forward.Host = "localhost"
		serving.Lock()
		defer serving.Unlock()
		tenant.Serve(response, forward)
	}))
	defer server.Close()
	d := openDevtools(t, browser, server.URL+"/khong-co", 800, 900, "")
	emulate(d, 412, 823)
	d.call("Page.navigate", map[string]any{"url": server.URL + "/"})
	d.loaded(`(location.pathname === "/" ? "" : "wait") + " of " + (location.pathname === "/" ? document.documentElement.scrollHeight : 0)`)
	d.evaluate(frames)
	state := func(ids ...string) string {
		quoted, _ := json.Marshal(ids)
		return d.evaluate(lazyState + "(" + string(quoted) + ")")
	}
	heightless := regexp.MustCompile(` \d+(x\d+)?`)
	got := map[string]string{}
	got["near"] = heightless.ReplaceAllString(state("near"), "")
	got["load"] = state("far", "sized", "centred", "undone")
	got["overflow"] = d.evaluate(`document.documentElement.scrollWidth + " of " + document.documentElement.clientWidth`)
	// print: everything is drawn
	d.call("Emulation.setEmulatedMedia", map[string]any{"media": "print"})
	d.evaluate(frames)
	got["print"] = heightless.ReplaceAllString(state("far", "sized", "centred", "undone"), "")
	d.call("Emulation.setEmulatedMedia", map[string]any{"media": ""})
	d.evaluate(frames)
	// scrolled to: drawn at its own size; scrolled away: skipped at that size
	d.evaluate(`document.getElementById("far").scrollIntoView()`)
	d.evaluate(frames)
	d.evaluate(frames)
	reached := state("far", "centred")
	d.evaluate(`window.scrollTo(0, 0)`)
	d.evaluate(frames)
	d.evaluate(frames)
	// skipped again, an element keeps the box it had until the page lays it out anew: here the page grows
	// 1px narrower (as a scrollbar, a font or a drawer arriving would), so it is laid out from what it
	// estimates, which auto makes the size it last had. Edge adds that auto by itself under
	// content-visibility:auto (a rule written 500px reads "auto 500px"), so only the unit test sees the
	// rule's own auto, kept for a browser that does not
	// (ui/stylesheet/lazy_test.go)
	d.evaluate(`document.body.style.paddingRight = "1px"`)
	d.evaluate(frames)
	away := state("far", "centred")
	d.evaluate(`document.body.style.paddingRight = ""`)
	own := regexp.MustCompile(`far=drawn (\d+)`).FindStringSubmatch(reached)
	if own == nil || own[1] == "500" {
		t.Fatalf("the far section, scrolled to, is not drawn at its own height: %s", reached)
	}
	got["reached"] = strings.Replace(reached, "far=drawn "+own[1]+" ", "far=drawn H ", 1)
	got["away"] = strings.Replace(away, "far=skipped "+own[1]+" ", "far=skipped H ", 1)
	// desktop: lazy="false" from medium draws the far section that writes it from the start
	emulate(d, 1024, 768)
	d.call("Page.reload", map[string]any{})
	d.loaded(`(document.getElementById("undone") ? "" : "wait") + " of " + (document.getElementById("undone") ? 1 : 0)`)
	d.evaluate(frames)
	got["desktop"] = heightless.ReplaceAllString(state("far", "undone"), "")

	want := map[string]string{
		"near":     "near=drawn",
		"load":     "far=skipped 500 sized=skipped 900 centred=skipped 500x0 undone=skipped 500",
		"overflow": "412 of 412", // the skipped box in the centred column is 0 wide: nothing overflows the phone
		"print":    "far=drawn sized=drawn centred=drawn undone=drawn",
		"reached":  "far=drawn H centred=drawn 40x300",
		"away":     "far=skipped H centred=skipped 40x300", // auto: the size it last had, nothing below moves
		"desktop":  "far=skipped undone=drawn",
	}
	for _, name := range []string{"near", "load", "overflow", "print", "reached", "away", "desktop"} {
		if got[name] != want[name] {
			t.Errorf("%s:\n got  %s\n want %s", name, got[name], want[name])
		}
	}
}

// colorsLazyServer serves the kitwork example in-process as the engine does while developing. /colors
// with ?control is the same page with every lazy class taken out of its HTML (the stylesheet unchanged).
func colorsLazyServer(t *testing.T) *httptest.Server {
	t.Helper()
	if _, err := os.Stat(filepath.Join(examplesRoot, "kitwork", "kitwork.localhost", "colors", "content.ui")); err != nil {
		t.Skipf("no colour gallery beside the engine (%v)", err)
	}
	root := copyExamples(t)
	saved := AllowLocal
	AllowLocal = true
	t.Cleanup(func() { AllowLocal = saved })
	const host = "kitwork.localhost"
	tenant := NewTenant(root, host)
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	lazyClass := regexp.MustCompile(`\s[a-z,+]*:?lazy-[^\s"]+`)
	var serving sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forward := r.Clone(r.Context())
		forward.Host = host
		forward.Header.Del("Accept-Encoding")
		serving.Lock()
		defer serving.Unlock()
		if r.URL.RawQuery != "control" {
			tenant.Serve(w, forward)
			return
		}
		answer := httptest.NewRecorder()
		tenant.Serve(answer, forward)
		for k, v := range answer.Header() {
			w.Header()[k] = v
		}
		w.Header().Del("Content-Length")
		w.WriteHeader(answer.Code)
		_, _ = w.Write(lazyClass.ReplaceAll(answer.Body.Bytes(), nil))
	}))
	t.Cleanup(server.Close)
	return server
}

// openColors opens /colors (with query and hash as given) on a screen, fresh, and waits for it to have
// loaded and drawn.
func openColors(d *devtools, url string) {
	d.call("Page.navigate", map[string]any{"url": url})
	d.loaded(`(location.pathname === "/colors" ? "" : "wait") + " of " + (location.pathname === "/colors" ? document.documentElement.scrollHeight : 0)`)
	d.evaluate(frames)
}

// lazyGroups says, for each lazy element of the page in order, whether what it holds is drawn and
// whether its height is the estimate it holds at this width ("estimate") or another ("own").
const lazyGroups = `Array.prototype.map.call(document.querySelectorAll('[class*="lazy-"]'), function (element) {
  var estimate = parseFloat(getComputedStyle(element).containIntrinsicBlockSize.replace("auto ", ""));
  return (element.firstElementChild.checkVisibility({ contentVisibilityAuto: true }) ? "drawn" : "skipped") +
    (Math.abs(element.getBoundingClientRect().height - estimate) < 1 ? " estimate" : " own");
}).join(", ")`

// shiftWalk scrolls the page from the top to the bottom by step px (a script of innerHeight may say it),
// two frames apart, and reports every layout shift the page records meanwhile, with where it was.
func shiftWalk(step string) string {
	return `new Promise(function (done) {
  window.scrollTo(0, 0);
  var shifts = [], total = 0;
  new PerformanceObserver(function (list) { list.getEntries().forEach(function (entry) {
    total += entry.value; shifts.push(entry.value.toFixed(4) + "@" + Math.round(window.scrollY));
  }); }).observe({ type: "layout-shift" });
  var step = ` + step + `, y = 0, steps = 0;
  (function next() {
    y += step; steps++;
    window.scrollTo(0, y);
    requestAnimationFrame(function () { requestAnimationFrame(function () {
      if (y < document.documentElement.scrollHeight) { next(); return; }
      setTimeout(function () { done("bottom=" + (Math.round(window.scrollY + innerHeight) >= document.documentElement.scrollHeight - 1) +
        " shifts=" + shifts.length + " total=" + total.toFixed(4) + (shifts.length ? " " + shifts.slice(0, 6).join(" ") : "")); }, 300);
    }); });
  })();
})`
}

// jumpWalk jumps, from the top of a fresh page, straight to each lazy group in turn, its top 100px under
// the top of the screen, and reports every layout shift meanwhile. A jump lands on a group the browser
// has not drawn: what lies under it is on the screen only if its estimate is shorter than the screen
// below it, and moves when the group is drawn at another height. Walks of small steps cannot tell: the
// browser draws each group before it reaches the screen.
const jumpWalk = `new Promise(function (done) {
  var shifts = [], total = 0;
  new PerformanceObserver(function (list) { list.getEntries().forEach(function (entry) {
    total += entry.value; shifts.push(entry.value.toFixed(4) + "@" + Math.round(window.scrollY));
  }); }).observe({ type: "layout-shift" });
  var groups = document.querySelectorAll('[class*="lazy-"]'), i = 0;
  (function next() {
    if (i === groups.length) {
      setTimeout(function () { done("groups=" + groups.length + " shifts=" + shifts.length + " total=" + total.toFixed(4) + (shifts.length ? " " + shifts.slice(0, 6).join(" ") : "")); }, 300);
      return;
    }
    window.scrollTo(0, groups[i++].getBoundingClientRect().top + window.scrollY - 100);
    setTimeout(function () { requestAnimationFrame(function () { requestAnimationFrame(next); }); }, 150);
  })();
})`

// pdfPages counts the pages of a PDF the browser printed: its page objects.
var pdfPage = regexp.MustCompile(`/Type\s*/Page\b[^s]`)

// TestBrowserLazyColorsPage proves lazy on the page it was made for, the colour gallery of the kitwork
// example (/colors), whose four swatch groups below the first write lazy with an estimate for each
// width, on Lighthouse's phone (412×823) and on a desktop (1350×940):
//   - at load the four groups are skipped, each at the height it estimates at that width;
//   - no layout shift while the page scrolls from the top to the bottom (a PerformanceObserver of
//     layout-shift): by 150px (a wheel), by 87.5% of the screen (page down) and by two screens at a
//     time, each walk on a fresh load, so that no group is remembered yet;
//   - nor when the page jumps straight to each group, its top 100px under the top of the screen: the
//     estimates hold the groups' heights, so what lies under a group stays off the screen until it is
//     drawn (jumpWalk);
//   - a jump to an anchor below skipped groups (/colors#backdrops, #webgradients, loaded so) lands at
//     the scroll padding the pinned header asks for, 96px, and the group under it is drawn;
//   - find in page reaches the last swatch of the last group (window.find, the browser's own finder):
//     found, selected and on the screen.
//
// And on the phone:
//   - a screen reader reads the swatches without the page drawing them: in the whole accessibility tree
//     (--force-renderer-accessibility, the tree a running screen reader turns on) the first and last
//     swatch names of the groups are text that is not ignored, while every group is still skipped. The
//     protocol's own smaller tree, without a screen reader, holds the headings but not what a skipped
//     group holds (logged, the browser's choice);
//   - print draws everything: under print media every group is drawn, and the PDF has as many pages as
//     the same page without lazy (?control) and at least 97% of its bytes (a skipped group would print
//     its estimate blank: no letters, no gradients).
func TestBrowserLazyColorsPage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the lazy browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	server := colorsLazyServer(t)
	const skipped = "skipped estimate, skipped estimate, skipped estimate, skipped estimate"
	for _, screen := range [][2]int{{412, 823}, {1350, 940}} {
		name := strconv.Itoa(screen[0]) + "px"
		d := openDevtools(t, browser, server.URL+"/khong-co", 800, 900, "")
		emulate(d, screen[0], screen[1])
		openColors(d, server.URL+"/colors")
		if got := d.evaluate(lazyGroups); got != skipped {
			t.Errorf("%s at load:\n got  %s\n want %s", name, got, skipped)
		}
		for _, step := range []string{"150", "Math.round(innerHeight * 0.875)", "innerHeight * 2"} {
			openColors(d, server.URL+"/colors?walk")
			if got := d.evaluate(shiftWalk(step)); got != "bottom=true shifts=0 total=0.0000" {
				t.Errorf("%s, scrolled by %s from the top to the bottom: %s", name, step, got)
			}
		}
		openColors(d, server.URL+"/colors?jump")
		if got := d.evaluate(jumpWalk); got != "groups=4 shifts=0 total=0.0000" {
			t.Errorf("%s, a jump straight to each group: %s", name, got)
		}
		for _, anchor := range []string{"backdrops", "webgradients"} {
			openColors(d, server.URL+"/colors?anchor#"+anchor)
			got := d.evaluate(`new Promise(function (done) { setTimeout(function () {
			  var target = document.getElementById("` + anchor + `"), group = target.querySelector('[class*="lazy-"]');
			  done(Math.round(target.getBoundingClientRect().top) + " of " + getComputedStyle(document.documentElement).scrollPaddingTop +
			    " group=" + (group.firstElementChild.checkVisibility({ contentVisibilityAuto: true }) ? "drawn" : "skipped"));
			}, 800); })`)
			if got != "96 of 96px group=drawn" {
				t.Errorf("%s, /colors#%s: %s, want 96 of 96px group=drawn", name, anchor, got)
			}
		}
		openColors(d, server.URL+"/colors?find")
		found := d.evaluate(`new Promise(function (done) { var found = window.find("zaffre"); requestAnimationFrame(function () { requestAnimationFrame(function () {
		  var box = getSelection().rangeCount ? getSelection().getRangeAt(0).getBoundingClientRect() : { top: -1, bottom: -1 };
		  done("found=" + found + " selection=" + getSelection() + " on screen=" + (box.top >= 0 && box.bottom <= innerHeight)); }); }); })`)
		if found != "found=true selection=zaffre on screen=true" {
			t.Errorf("%s, find in page: %s", name, found)
		}
		if screen[0] != 412 {
			continue
		}
		// print: under print media every group is drawn; the PDF matches the page without lazy
		openColors(d, server.URL+"/colors")
		d.call("Emulation.setEmulatedMedia", map[string]any{"media": "print"})
		d.evaluate(frames)
		if got := d.evaluate(lazyGroups); !strings.HasPrefix(got, "drawn") || strings.Contains(got, "skipped") {
			t.Errorf("under print media: %s, want every group drawn", got)
		}
		d.call("Emulation.setEmulatedMedia", map[string]any{"media": ""})
		printed := func() []byte {
			var pdf struct{ Data string }
			_ = json.Unmarshal(d.call("Page.printToPDF", map[string]any{"printBackground": true}), &pdf)
			data, _ := base64.StdEncoding.DecodeString(pdf.Data)
			return data
		}
		lazy := printed()
		openColors(d, server.URL+"/colors?control")
		if got := d.evaluate(lazyGroups); got != "" {
			t.Fatalf("the control still carries lazy: %s", got)
		}
		control := printed()
		pages, controlPages := len(pdfPage.FindAll(lazy, -1)), len(pdfPage.FindAll(control, -1))
		if pages != controlPages || pages < 10 || float64(len(lazy)) < 0.97*float64(len(control)) {
			t.Errorf("printed: %d pages, %d bytes; without lazy %d pages, %d bytes", pages, len(lazy), controlPages, len(control))
		}
		t.Logf("printed: %d pages, %d bytes; without lazy %d pages, %d bytes", pages, len(lazy), controlPages, len(control))
		// a screen reader: the whole tree, as a running screen reader has it, then the protocol's own
		for _, flags := range [][]string{{"--force-renderer-accessibility"}, nil} {
			reader := openDevtools(t, browser, server.URL+"/khong-co", 800, 900, "", flags...)
			emulate(reader, 412, 823)
			openColors(reader, server.URL+"/colors")
			reader.call("Accessibility.enable", map[string]any{})
			var document struct {
				Result struct {
					ObjectID string `json:"objectId"`
				} `json:"result"`
			}
			if err := json.Unmarshal(reader.call("Runtime.evaluate", map[string]any{"expression": "document"}), &document); err != nil {
				t.Fatal(err)
			}
			var read []string
			for _, text := range []string{"cirrus", "warm-flame", "180 Perfect Blue", "aliceblue", "yellowgreen", "alabaster-white", "zaffre", "Figma · 202"} {
				var answer struct {
					Nodes []struct {
						Ignored bool `json:"ignored"`
						Role    struct {
							Value string `json:"value"`
						} `json:"role"`
					} `json:"nodes"`
				}
				if err := json.Unmarshal(reader.call("Accessibility.queryAXTree", map[string]any{"objectId": document.Result.ObjectID, "accessibleName": text}), &answer); err != nil {
					t.Fatal(err)
				}
				heard := false
				for _, node := range answer.Nodes {
					heard = heard || !node.Ignored
				}
				if heard {
					read = append(read, text)
				}
			}
			groups := reader.evaluate(lazyGroups)
			if flags == nil {
				t.Logf("without a screen reader the protocol's own tree reads %v (groups %s)", read, groups)
				continue
			}
			if strings.Join(read, ",") != "cirrus,warm-flame,180 Perfect Blue,aliceblue,yellowgreen,alabaster-white,zaffre,Figma · 202" || groups != skipped {
				t.Errorf("a screen reader reads %v while the groups are %s; want every name, every group skipped", read, groups)
			}
		}
	}
}

// drawWalk scrolls the page from the top to the bottom by 87.5% of the screen, two frames apart, so the
// browser draws every lazy group once, then goes back to the top.
const drawWalk = `new Promise(function (done) {
  var y = 0;
  (function next() {
    window.scrollTo(0, y);
    requestAnimationFrame(function () { requestAnimationFrame(function () {
      if (y < document.documentElement.scrollHeight) { y += Math.round(innerHeight * 0.875); next(); return; }
      window.scrollTo(0, 0);
      requestAnimationFrame(function () { requestAnimationFrame(function () { done(""); }); });
    }); });
  })();
})`

// lazyFit says, for each lazy group, whether the height the browser drew it at is its estimate at this
// width (within 1.5%, 8px at least): "fits", or "off <drawn> for <estimate>". Read after drawWalk.
const lazyFit = `Array.prototype.map.call(document.querySelectorAll('[class*="lazy-"]'), function (element) {
  var estimate = parseFloat(getComputedStyle(element).containIntrinsicBlockSize.replace("auto ", "")), height = element.getBoundingClientRect().height;
  return Math.abs(height - estimate) <= Math.max(8, estimate * 0.015) ? "fits" : "off " + Math.round(height) + " for " + Math.round(estimate);
}).join(", ")`

// lazyHeights is the height of each lazy group, in px, as the page holds it now.
const lazyHeights = `Array.prototype.map.call(document.querySelectorAll('[class*="lazy-"]'), function (element) {
  return Math.round(element.getBoundingClientRect().height);
}).join(",")`

// thumbWalk drags the scrollbar's thumb, as it were: from the top it jumps to 15%, 30% … 90% of the
// page's height, two frames and 150ms apart, and reports every layout shift meanwhile and the page's
// height at the start and the end.
const thumbWalk = `new Promise(function (done) {
  var shifts = [], total = 0, start = document.documentElement.scrollHeight, fractions = [0.15, 0.3, 0.45, 0.6, 0.75, 0.9], i = 0;
  new PerformanceObserver(function (list) { list.getEntries().forEach(function (entry) {
    total += entry.value; shifts.push(entry.value.toFixed(4) + "@" + Math.round(window.scrollY));
  }); }).observe({ type: "layout-shift" });
  (function next() {
    if (i === fractions.length) {
      setTimeout(function () { done("shifts=" + shifts.length + " total=" + total.toFixed(4) + " height " + start + "→" + document.documentElement.scrollHeight +
        (shifts.length ? " " + shifts.slice(0, 6).join(" ") : "")); }, 300);
      return;
    }
    window.scrollTo(0, Math.round(document.documentElement.scrollHeight * fractions[i++]));
    setTimeout(function () { requestAnimationFrame(function () { requestAnimationFrame(next); }); }, 150);
  })();
})`

// TestBrowserLazyColorsPageAtAReadersFontAndAfterAWidthChange proves two limits of lazy's estimate on the
// colour gallery (15/10/2026, the review of lazy):
//   - a reader's font size: every swatch, gap and line of /colors is sized in rem, so its estimates are
//     written in rem too. At the browser's default (16px) and at a reader's 20px, on a phone (412×823)
//     and a desktop (1350×940), each group drawn is the height it estimates (lazyFit). Estimates in px
//     fell 25% short at 20px: the page grew by thousands of px as its groups were drawn, and the
//     scrollbar's thumb jumped;
//   - a width change after the groups were drawn (a phone turned, a window resized): the browser keeps
//     the height it last drew each group at (contain-intrinsic-size: auto), whatever the estimate at the
//     new width says, until it draws the group again; the estimates by width hold only for a group never
//     drawn. Recorded, not cured: no CSS resets that height in Chromium (KITWORK_UI §7, KITWORK_UI_ENGINE
//     traps). Drawn at 412, then laid out at 1350, every group keeps its 412 height; a thumb walk over the
//     page is logged. CONTROL: the same steps without lazy (?control) shift nothing.
func TestBrowserLazyColorsPageAtAReadersFontAndAfterAWidthChange(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the lazy browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	server := colorsLazyServer(t)
	const fit = "fits, fits, fits, fits"
	for _, font := range []int{16, 20} {
		d := openDevtools(t, browser, server.URL+"/khong-co", 800, 900, "")
		d.call("Page.enable", map[string]any{})
		d.call("Page.setFontSizes", map[string]any{"fontSizes": map[string]any{"standard": font, "fixed": 13}})
		for _, screen := range [][2]int{{412, 823}, {1350, 940}} {
			emulate(d, screen[0], screen[1])
			openColors(d, server.URL+"/colors?font")
			if got := d.evaluate(`getComputedStyle(document.documentElement).fontSize`); got != strconv.Itoa(font)+"px" {
				t.Fatalf("the reader's font size is %s, want %dpx", got, font)
			}
			d.evaluate(drawWalk)
			if got := d.evaluate(lazyFit); got != fit {
				t.Errorf("font %dpx, %dpx wide, every group drawn: %s; want each the height it estimates", font, screen[0], got)
			}
		}
	}
	// a width change after the groups were drawn
	for _, query := range []string{"", "control"} {
		d := openDevtools(t, browser, server.URL+"/khong-co", 800, 900, "")
		emulate(d, 412, 823)
		openColors(d, server.URL+"/colors?"+query)
		d.evaluate(drawWalk)
		drawn := d.evaluate(lazyHeights)
		emulate(d, 1350, 940)
		d.evaluate(frames)
		kept := d.evaluate(lazyHeights)
		walk := d.evaluate(thumbWalk)
		if query == "control" {
			if !strings.HasPrefix(walk, "shifts=0 ") {
				t.Errorf("CONTROL: without lazy, a thumb walk after the width change: %s, want no shift", walk)
			}
			continue
		}
		t.Logf("drawn at 412: %s; laid out at 1350: %s; a thumb walk: %s", drawn, kept, walk)
		if drawn == "" || kept != drawn {
			t.Errorf("after the width change the groups are %s, want the heights drawn at 412 (%s) kept: the limit KITWORK_UI §7 states", kept, drawn)
		}
	}
}
