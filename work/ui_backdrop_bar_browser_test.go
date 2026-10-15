package work

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// backdropBarFiles is a site whose page has a backdrop (cirrus) and the light/dark toggle a Kitwork UI
// page writes (component="appearance"), with a second page under the same page.ui that Drive moves to
// without a reload (router.reload(false)). Its colours have a dark look; the page follows the device.
var backdropBarFiles = map[string]string{
	"localhost/router.work": `import { router } from "kitwork";

router.colors({ brand: "#c8742c", canvas: { light: "#f6f9fc", dark: "#0f1822" } });
router.reload(false);
`,
	"localhost/page.ui":      `<page background="cirrus"><header><button component="appearance" click="toggle()">Đổi</button><link value="/b">Sang b</link></header><content /></page>`,
	"localhost/content.ui":   `<content><text>Trang đầu</text></content>`,
	"localhost/b/content.ui": `<content><text>Trang b</text></content>`,
}

// TestBrowserPageBackdropBarFollowsTheMode proves in a real browser that the colour of the browser's bar
// (the first <meta name="theme-color"> whose media holds, as a browser picks it) follows the mode the
// page shows, not only the device's (decision D4: the bar matches the page). On a light device:
//   - at first the page and the bar are light, cirrus's light corner, #849cdd — the device decides;
//   - the visitor turns dark with the page's toggle (kit.appearance): the bar turns #202f5e with it;
//   - a move by Drive to /b, the same page.ui, keeps the page and the bar dark (no reload);
//   - a reload keeps both dark from the first paint: the pre-paint reads the stored choice and sets the
//     bar as it sets the page.
//
// The keyboard focus ring each mode resolves to rides along (--kitwork-focus, computed): in light the
// page's own ring, ink #0a2540, written in page.css after colors.css, since the brand #c8742c stands
// 1.30:1 from cirrus's corner #849cdd; in dark the palette's, the brand #d79565, 5.14:1 from #202f5e
// (both by hand).
func TestBrowserPageBackdropBarFollowsTheMode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the theme-color browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant := buildUISite(t, backdropBarFiles)
	if body := serveUI(t, tenant, "http://localhost/").Body.String(); !strings.Contains(body, `data-kitwork-dark="#202f5e"`) || !strings.Contains(body, "data-kitwork-backdrop") {
		t.Fatalf("the site is not the test's: it must write the backdrop and the bar's colour per mode:\n%.1500s", body)
	}
	var serving sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		forward := request.Clone(request.Context())
		forward.Host = "localhost"
		serving.Lock()
		defer serving.Unlock()
		tenant.Serve(response, forward)
	}))
	defer server.Close()

	// frames waits two frames, then says where the page is, the mode it shows, the bar and the ground of
	// <html>; marked says whether this document is the one the marker was set in (no reload since)
	const look = `new Promise(function (done) { requestAnimationFrame(function () { requestAnimationFrame(function () {
	  var bar = "";
	  document.querySelectorAll('meta[name="theme-color"]').forEach(function (m) { if (!bar && (!m.media || matchMedia(m.media).matches)) bar = m.content; });
	  done(location.pathname + " theme=" + document.documentElement.getAttribute("data-theme") + " bar=" + bar +
	    " html=" + getComputedStyle(document.documentElement).backgroundColor + " focus=" + getComputedStyle(document.documentElement).getPropertyValue("--kitwork-focus").trim() +
	    " marked=" + (window.__marker === 1) + " of 1");
	}); }); })`
	// ready waits for the kernel to have bound the toggle (kit.appearance is the toggle's service)
	const ready = `new Promise(function (done) { var tries = 0; (function wait() {
	  if (window.kit && window.kit.appearance || tries++ > 100) { window.__marker = 1; done(String(!!(window.kit && window.kit.appearance))); return; }
	  setTimeout(wait, 50); })(); })`
	d := openDevtools(t, browser, server.URL+"/", 800, 600, "")
	steps := []struct{ what, got, want string }{}
	steps = append(steps, struct{ what, got, want string }{"loaded on a light device", d.loaded(look), "/ theme=light bar=#849cdd html=rgb(132, 156, 221) focus=#0a2540 marked=false of 1"})
	if got := d.evaluate(ready); got != "true" {
		t.Fatalf("the kernel never bound the toggle: %s", got)
	}
	d.evaluate(`document.querySelector('[data-kitwork-component="appearance"]').click()`)
	steps = append(steps, struct{ what, got, want string }{"turned dark with the toggle", d.evaluate(look), "/ theme=dark bar=#202f5e html=rgb(32, 47, 94) focus=#d79565 marked=true of 1"})
	d.evaluate(`document.querySelector('a[href="/b"]').click()`)
	moved := d.evaluate(`new Promise(function (done) { var tries = 0; (function wait() {
	  if (location.pathname === "/b" || tries++ > 100) { done(location.pathname); return; } setTimeout(wait, 50); })(); })`)
	steps = append(steps, struct{ what, got, want string }{"moved to " + moved + " by Drive", d.evaluate(look), "/b theme=dark bar=#202f5e html=rgb(32, 47, 94) focus=#d79565 marked=true of 1"})
	d.evaluate(`setTimeout(function () { location.reload(); }, 0); "reloading"`)
	steps = append(steps, struct{ what, got, want string }{"reloaded", d.loaded(look), "/b theme=dark bar=#202f5e html=rgb(32, 47, 94) focus=#d79565 marked=false of 1"})
	for _, s := range steps {
		if s.got != s.want {
			t.Errorf("%s:\n got  %s\n want %s", s.what, s.got, s.want)
		}
	}
}

// TestBrowserPageBackdropThatFadesShowsTheCanvas looks at the pixels a real browser paints for a page
// whose backdrop fades to transparent (<page background="glow">: brand-85 at the top, transparent from
// 70%). The see-through part of the fixed layer shows the canvas, as the contrast check composites it:
// the screen's bottom-right corner is the canvas, #f6f9fc, and <html>'s ground is the canvas; the glow
// is at the top of the screen; the page names no theme-color. CONTROL: the top of the screen is not the
// canvas, so the screenshot shows the glow at all.
func TestBrowserPageBackdropThatFadesShowsTheCanvas(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the faded backdrop screenshot in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant := buildUISite(t, map[string]string{
		"localhost/router.work": backdropBrowserFiles["localhost/router.work"],
		"localhost/page.ui":     `<page background="glow"><content /></page>`,
		"localhost/content.ui":  `<content><box height="2000px"></box></content>`,
	})
	var serving sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		forward := request.Clone(request.Context())
		forward.Host = "localhost"
		serving.Lock()
		defer serving.Unlock()
		tenant.Serve(response, forward)
	}))
	defer server.Close()
	d := openDevtools(t, browser, server.URL+"/", 800, 400, "")
	state := d.loaded(`new Promise(function (done) { requestAnimationFrame(function () { requestAnimationFrame(function () {
	  done("html=" + getComputedStyle(document.documentElement).backgroundColor + " bars=" + document.querySelectorAll('meta[name="theme-color"]').length +
	    " layer=" + !!document.querySelector("[data-kitwork-backdrop].background-glow") + " of 1");
	}); }); })`)
	if want := "html=rgb(246, 249, 252) bars=0 layer=true of 1"; state != want {
		t.Errorf("the page:\n got  %s\n want %s", state, want)
	}
	shot := d.screenshot()
	b := shot.Bounds()
	hex := func(x, y int) string {
		r, g, bl, _ := shot.At(x, y).RGBA()
		return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, bl>>8)
	}
	if corner := hex(b.Max.X-3, b.Max.Y-3); !nearHex(corner, "#f6f9fc") {
		t.Errorf("the screen's bottom-right corner is %s, want the canvas #f6f9fc showing through the glow's transparent end", corner)
	}
	if top := hex((b.Min.X+b.Max.X)/2, b.Min.Y+2); nearHex(top, "#f6f9fc") {
		t.Errorf("CONTROL: the top of the screen is %s, the canvas: the glow is not drawn", top)
	}
}
