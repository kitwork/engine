package work

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// exampleToastSay closes every notice and presses the demo buttons of /components named (by their words,
// in order; none: nothing is closed or pressed), waits for the notices to settle, and says each notice
// on screen — its icons shown with the name a screen reader hears (icon:name), title, words, the count
// of repeats, button — and the rectangle the stack's notices take on the screen (to picture it), with
// the page's scheme.
const exampleToastSay = `(function (labels) {
  var host = document.querySelector('[data-kitwork-component^="toast"]');
  if (labels.length) kit.scope.$toast.clear();
  return new Promise(function (done) { setTimeout(function () {
    var buttons = Array.prototype.slice.call(document.querySelectorAll("#toast button"));
    labels.forEach(function (label) { buttons.filter(function (b) { return b.textContent.trim() === label; })[0].click(); });
    setTimeout(function () { requestAnimationFrame(function () { requestAnimationFrame(function () {
      var rows = Array.prototype.filter.call(host.children, function (c) { return c.hasAttribute("data-kit-key"); });
      var shown = function (el) { return !!el && !el.hidden; };
      var box = null;
      rows.forEach(function (row) {
        var r = row.getBoundingClientRect();
        box = box ? { left: Math.min(box.left, r.left), top: Math.min(box.top, r.top), right: Math.max(box.right, r.right), bottom: Math.max(box.bottom, r.bottom) } : { left: r.left, top: r.top, right: r.right, bottom: r.bottom };
      });
      // the picture: the notices and 24px around, within the screen (a clip past its edge draws another part)
      if (box) box = { left: Math.max(0, box.left - 24), top: Math.max(0, box.top - 24), right: Math.min(innerWidth, box.right + 24), bottom: Math.min(innerHeight, box.bottom + 24) };
      var inside = rows.every(function (row) { var r = row.getBoundingClientRect(); return r.left >= 0 && r.right <= innerWidth && r.bottom <= innerHeight && r.top >= 0; });
      done(JSON.stringify({
        notices: rows.map(function (row) {
          var icons = Array.prototype.filter.call(row.querySelectorAll(":scope > i"), shown).map(function (i) {
            return (i.className.match(/icon-([a-z0-9-]+)/) || [])[1] + (i.getAttribute("aria-label") ? ":" + i.getAttribute("aria-label") : "");
          });
          var p = row.querySelectorAll("p"), act = row.querySelector('button[data-kitwork-event^="click: act"]'), count = row.querySelector('[data-kitwork-bind*="notice.count"]');
          return [icons.join("+"), shown(p[0]) ? p[0].textContent : "", p[1].textContent, shown(count) ? count.textContent : "", shown(act) ? act.textContent : ""].join("|");
        }).join(" ; "),
        inside: inside,
        overflow: Math.max(document.documentElement.scrollWidth, innerWidth) - innerWidth,
        dark: matchMedia("(prefers-color-scheme: dark)").matches,
        x: box ? box.left + scrollX : 0, y: box ? box.top + scrollY : 0, width: box ? box.right - box.left : 1, height: box ? box.bottom - box.top : 1 }));
    }); }); }, 350);
  }, 300); });
})`

// exampleToastLong closes every notice and says a danger whose words hold one long unbroken address,
// then says whether its dismiss button is inside the notice and the notice inside the screen, how far
// the stack and the page scroll sideways, and the rectangle around the notice (to picture it).
const exampleToastLong = `(function () {
  var host = document.querySelector('[data-kitwork-component^="toast"]');
  kit.scope.$toast.clear();
  return new Promise(function (done) { setTimeout(function () {
    kit.scope.$toast.danger("Could not open https://example.com/uploads/2026/10/17/a-very-long-file-name-without-any-break-points-abcdefghijklmnopqrstuvwxyz.jpg", { title: "Upload failed" });
    setTimeout(function () { requestAnimationFrame(function () { requestAnimationFrame(function () {
      var row = host.querySelector(":scope > [data-kit-key]"), card = row.getBoundingClientRect();
      var x = row.querySelector('button[data-kitwork-event^="click: dismiss"]').getBoundingClientRect();
      var left = Math.max(0, card.left - 12), top = Math.max(0, card.top - 12);
      done(JSON.stringify({
        inside: x.left >= card.left && x.right <= card.right && card.left >= 0 && card.right <= innerWidth,
        stack: host.scrollWidth - host.clientWidth,
        overflow: Math.max(document.documentElement.scrollWidth, innerWidth) - innerWidth,
        x: left + scrollX, y: top + scrollY, width: Math.min(innerWidth, card.right + 12) - left, height: Math.min(innerHeight, card.bottom + 12) - top }));
    }); }); }, 350);
  }, 300); });
})()`

// exampleToastTall closes every notice and says three with a title and a link each, more than a short
// screen holds, then says how many are on screen, whether the newest (at the bottom edge, nearest it)
// is inside the screen, and whether the stack is taller than its room (it scrolls inside).
const exampleToastTall = `(function () {
  var host = document.querySelector('[data-kitwork-component^="toast"]');
  kit.scope.$toast.clear();
  return new Promise(function (done) { setTimeout(function () {
    kit.scope.$toast.success("The menu is saved and on the site.", { title: "Menu saved", action: { label: "Open the menu", link: "#toast" } });
    kit.scope.$toast.success("The photo is on the page of the dish.", { title: "Photo uploaded", action: { label: "Open the dish", link: "#toast" } });
    kit.scope.$toast.danger("The order is still in the cart: try again.", { title: "Order not saved", action: { label: "Open the cart", link: "#toast" } });
    setTimeout(function () { requestAnimationFrame(function () { requestAnimationFrame(function () {
      var rows = Array.prototype.filter.call(host.children, function (c) { return c.hasAttribute("data-kit-key"); });
      var newest = rows[rows.length - 1].getBoundingClientRect();
      done(JSON.stringify({ count: rows.length, newest: newest.top >= 0 && newest.bottom <= innerHeight, scrolls: host.scrollHeight > host.clientHeight }));
    }); }); }, 350);
  }, 300); });
})()`

// exampleToastSection scrolls the demo section of /components into the middle of the screen and says
// where it is on the page and how many buttons it holds.
const exampleToastSection = `(function () {
  var section = document.getElementById("toast");
  section.scrollIntoView({ block: "center" });
  return new Promise(function (done) { requestAnimationFrame(function () { requestAnimationFrame(function () {
    var r = section.getBoundingClientRect();
    done(JSON.stringify({ buttons: section.querySelectorAll("button").length, x: r.left + scrollX - 16, y: r.top + scrollY - 16, width: r.width + 32, height: r.height + 32 }));
  }); }); });
})()`

// screenShot pictures a rectangle of the screen as it stands (a stack fixed to it stays where it is:
// captured beyond the screen, the page would grow and the stack move to its bottom).
func screenShot(t *testing.T, d *devtools, x, y, width, height float64) ([]byte, image.Image) {
	t.Helper()
	var shot struct{ Data string }
	if err := json.Unmarshal(d.call("Page.captureScreenshot", map[string]any{"format": "png",
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

// The toast of the kitwork example, drawn by a real browser: overlay.ui draws one stack for the whole
// site, /components says notices through $toast from its demo section.
//   - the demo section holds eight buttons, one for each tone, one with a title and an action, saving
//     and finishing, and one that stays;
//   - three tones said one after another stack at the bottom end, the newest nearest the edge, each with
//     its own icon, named for a screen reader by its tone (a danger reached later is told an error), in
//     light and dark, at a desktop's width and a phone's; the stack sits inside the screen and the page
//     does not scroll sideways (375, 320 and 280px: reflow, WCAG 1.4.10), nor does a notice whose words
//     hold one long unbroken address: its dismiss button stays inside it, inside the screen;
//   - three notices with a title and a link on a phone turned sideways (568×320), taller than the
//     screen: the stack scrolls inside, and its newest, at the bottom edge, is on screen;
//   - Start saving shows one notice that works (the loader), Finish saving turns that same notice into
//     Saved, with the success icon; Success pressed twice is one notice that counts ×2;
//   - the notice with a title and an action shows both; its button chooses page.ui in the window above
//     (the tabs' $files) and closes the notice.
//
// The pictures differ where the scheme, the width or the notice do, and are saved to KITWORK_TOAST_SHOTS
// when it names a folder.
func TestBrowserExampleToastShots(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the toast screenshots in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant, page := examplePage(t, "/components")
	if !strings.Contains(page, `data-kitwork-component="toast: $toast"`) || !strings.Contains(page, `id="toast"`) {
		t.Fatalf("/components has no toast or no demo section:\n%.2000s", page)
	}
	server := exampleServer(tenant)
	defer server.Close()
	folder := os.Getenv("KITWORK_TOAST_SHOTS")
	save := func(name string, raw []byte) {
		if folder == "" {
			return
		}
		if err := os.WriteFile(filepath.Join(folder, name+".png"), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	d := openDevtools(t, browser, server.URL+"/components", 1280, 900, "")
	d.loaded(exampleSettle)
	// a scheme is the page's as it loads (its appearance is painted before the first frame): reloaded
	scheme := func(name string) {
		d.call("Emulation.setEmulatedMedia", map[string]any{"features": []map[string]string{
			{"name": "prefers-color-scheme", "value": name},
			{"name": "prefers-reduced-motion", "value": "reduce"},
		}})
		d.call("Page.reload", map[string]any{"ignoreCache": true})
		d.loaded(exampleSettle)
		d.evaluate(`document.getElementById("toast").scrollIntoView({ block: "center" })`)
	}
	type said struct {
		Notices             string
		Inside              bool
		Overflow            float64
		Dark                bool
		X, Y, Width, Height float64
	}
	say := func(labels ...string) said {
		t.Helper()
		list, _ := json.Marshal(append([]string{}, labels...))
		got := d.evaluate(exampleToastSay + `(` + string(list) + `)`)
		var out said
		if err := json.Unmarshal([]byte(got), &out); err != nil {
			t.Fatalf("%s: %v", got, err)
		}
		return out
	}
	pictures := map[string]image.Image{}
	shoot := func(name string, at said) {
		raw, picture := screenShot(t, d, at.X, at.Y, at.Width, at.Height)
		pictures[name] = picture
		save(name, raw)
	}

	// the demo section
	scheme("light")
	emulate(d, 1280, 900)
	var section struct {
		Buttons             int
		X, Y, Width, Height float64
	}
	if err := json.Unmarshal([]byte(d.evaluate(exampleToastSection)), &section); err != nil || section.Buttons != 8 {
		t.Fatalf("the demo section: %+v %v, want 8 buttons", section, err)
	}
	raw, _ := clipShot(t, d, section.X, section.Y, section.Width, section.Height)
	save("section-light-1280", raw)

	// three tones, light and dark, desktop and phone
	const three = "circle-check:Success||Saved.|| ; alert-triangle:Warning||Your session ends in five minutes.|| ; alert-circle:Error||Could not save. Check the connection and try again.||"
	for _, screen := range []struct {
		scheme string
		width  int
	}{{"light", 1280}, {"dark", 1280}, {"light", 375}, {"dark", 375}} {
		emulate(d, screen.width, 800)
		scheme(screen.scheme)
		at := say("Success", "Warning", "Danger")
		name := "stack-" + screen.scheme + "-" + strconv.Itoa(screen.width)
		if at.Notices != three || !at.Inside || at.Overflow != 0 || at.Dark != (screen.scheme == "dark") {
			t.Errorf("%s: %+v\n want %s inside the screen, no overflow", name, at, three)
		}
		shoot(name, at)
	}
	for _, pair := range [][2]string{{"stack-light-1280", "stack-dark-1280"}, {"stack-light-375", "stack-dark-375"}, {"stack-light-1280", "stack-light-375"}} {
		if samePicture(pictures[pair[0]], pictures[pair[1]]) {
			t.Errorf("%s and %s are drawn the same", pair[0], pair[1])
		}
	}
	// narrower: reflow, and a long unbroken address that wraps inside its notice
	for _, width := range []int{320, 280} {
		emulate(d, width, 700)
		if at := say("Success", "Warning", "Danger"); at.Notices != three || !at.Inside || at.Overflow != 0 {
			t.Errorf("%dpx: %+v, want three notices inside the screen and no overflow", width, at)
		}
		var long struct {
			Inside              bool
			Stack, Overflow     float64
			X, Y, Width, Height float64
		}
		if err := json.Unmarshal([]byte(d.evaluate(exampleToastLong)), &long); err != nil {
			t.Fatal(err)
		}
		if !long.Inside || long.Stack != 0 || long.Overflow != 0 {
			t.Errorf("%dpx, a long address: %+v, want the dismiss button inside the notice, inside the screen, nothing scrolling sideways", width, long)
		}
		raw, _ := screenShot(t, d, long.X, long.Y, long.Width, long.Height)
		save("long-"+strconv.Itoa(width), raw)
	}
	// a phone turned sideways: taller than the screen, the stack shows its newest
	emulate(d, 568, 320)
	var tall struct {
		Count           int
		Newest, Scrolls bool
	}
	if err := json.Unmarshal([]byte(d.evaluate(exampleToastTall)), &tall); err != nil {
		t.Fatal(err)
	}
	if tall.Count != 3 || !tall.Newest || !tall.Scrolls {
		t.Errorf("568×320: %+v, want three notices, taller than the screen, the newest on screen", tall)
	}
	var screen struct{ X, Y float64 }
	_ = json.Unmarshal([]byte(d.evaluate(`JSON.stringify({ x: scrollX, y: scrollY })`)), &screen)
	raw, _ = screenShot(t, d, screen.X, screen.Y, 568, 320)
	save("landscape-568", raw)

	// saving, then saved: one notice
	emulate(d, 1280, 800)
	scheme("light")
	before := say("Start saving")
	shoot("saving-before", before)
	node := d.evaluate(`(function () { window.__saving = document.querySelector('[data-kitwork-component^="toast"] > [data-kit-key="save"]'); return String(!!window.__saving); })()`)
	got := d.evaluate(`(function () {
  Array.prototype.filter.call(document.querySelectorAll("#toast button"), function (b) { return b.textContent.trim() === "Finish saving"; })[0].click();
  return new Promise(function (done) { setTimeout(function () { done(""); }, 350); });
})()`)
	_ = got
	after := say() // nothing pressed: what is on screen now
	shoot("saving-after", after)
	same := d.evaluate(`String(document.querySelector('[data-kitwork-component^="toast"] > [data-kit-key="save"]') === window.__saving)`)
	if before.Notices != "loader-2:Loading||Saving…||" || after.Notices != "circle-check:Success||Saved.||" || node != "true" || same != "true" {
		t.Errorf("saving: before %q after %q, one node %s %s", before.Notices, after.Notices, node, same)
	}
	if samePicture(pictures["saving-before"], pictures["saving-after"]) {
		t.Error("saving and saved are drawn the same")
	}
	// the same words twice: one notice, counted
	twice := say("Success", "Success")
	shoot("twice", twice)
	if twice.Notices != "circle-check:Success||Saved.|×2|" {
		t.Errorf("Success pressed twice: %q, want one notice that counts ×2", twice.Notices)
	}

	// a title and an action, which chooses page.ui in the window above
	titled := say("With a title and an action")
	shoot("title-action", titled)
	if titled.Notices != "info-circle:Information|A page is two files|page.ui draws what router.work gives it.||Show page.ui" {
		t.Errorf("the notice with a title and an action: %q", titled.Notices)
	}
	chosen := d.evaluate(`(function () {
  var button = document.querySelector('[data-kitwork-component^="toast"] [data-kitwork-event^="click: act"]:not([hidden])');
  button.click();
  return new Promise(function (done) { setTimeout(function () {
    var rows = document.querySelectorAll('[data-kitwork-component^="toast"] > [data-kit-key]').length;
    done(kit.scope.$files.selected + " " + rows);
  }, 300); });
})()`)
	if chosen != "page 0" {
		t.Errorf("the action chose %q, want page.ui chosen and the notice closed (page 0)", chosen)
	}

	// the page with its stack, as a person sees it
	at := say("Info", "Success", "Danger")
	var scroll struct{ X, Y float64 }
	_ = json.Unmarshal([]byte(d.evaluate(`JSON.stringify({ x: scrollX, y: scrollY })`)), &scroll)
	whole, _ := screenShot(t, d, scroll.X, scroll.Y, 1280, 800)
	_ = at
	save("page-light-1280", whole)
}
