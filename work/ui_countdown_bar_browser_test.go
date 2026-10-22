package work

import (
	"net/http"
	"strings"
	"testing"
)

// countdownBarFiles is a site whose page holds the countdown of the bar (the decisions of 20/10/2026,
// late evening): one <time> of three fields, hours, minutes and seconds, the author's buttons for a
// minute more, toggle() and stop(); its end said through the toast of the toast test.
var countdownBarFiles = map[string]string{
	"localhost/router.work": countdownBrowserFiles["localhost/router.work"],
	"localhost/page.ui":     `<page><content /><overlay /></page>`,
	"localhost/overlay.ui":  toastBrowserFiles["localhost/overlay.ui"],
	"localhost/content.ui": `<content padding="4" gap="2">
  <countdown as="$tea" duration="5m" finish="$toast.success('Trà đã sẵn sàng')" flex="row" align="center" gap="3">
    <time id="left" alt="Thời gian"><input id="hours" value="{{ hours }}" alt="Giờ" />:<input id="minutes" value="{{ minutes }}" alt="Phút" />:<input id="seconds" value="{{ seconds }}" alt="Giây" /></time>
    <button id="more" click="minutes++">+1 phút</button>
    <button id="toggle" click="toggle()">{{ playing ? 'Tạm dừng' : 'Chạy' }}</button>
    <button id="stop" click="stop()">Dừng</button>
  </countdown>
</content>`,
}

// countdownBarRead is what each step of the bar reads: the three fields as they show, the instance's
// state, which element has the focus with where its caret is.
const countdownBarRead = `var bar = function () { return ["hours", "minutes", "seconds"].map(function (id) { return document.getElementById(id).value; }).join(":"); };
  var focused = function () { var a = document.activeElement; return a === document.body ? "body" : a.id + " " + a.selectionStart + "-" + a.selectionEnd; };`

// TestBrowserCountdownBar proves the bar (the decisions of 20/10/2026, late evening: "count down tôi
// muốn nó là một thanh dài gồm 3 thanh giờ phút giây và placeholder là 00:00:00 … được điền vào như
// một input bình thường", then "đồng hồ cần nhận con trỏ") on a page the engine serves, in a real
// browser whose clock runs in real time, with its real mouse and keys (DevTools Input.dispatchMouseEvent
// at a point between two digits, Input.dispatchKeyEvent, Input.insertText):
//   - as the server paints it: 00:05:00, each field's placeholder 00, a group named Thời gian; the page
//     warns of nothing;
//   - the bar's look: one field around the three (the field's fill, its 1px edge, its radius and its
//     padding on the <time>, the three fields and the colons inside it), the fields with no edge, no
//     fill, no padding of their own, two digits wide, their digits in the middle; the focus ring is the
//     bar's, never a field's;
//   - Chạy: it counts (00:04:59 a second in); a click between the 0 and the 4 of the minutes holds it
//     there, the caret where the click put it (minutes 1-1), nothing selected, nothing changing for
//     1.2 s;
//   - Backspace takes the 0 (4, the caret at the start); Backspace again goes back into the hours and
//     takes their last digit (0, the caret at its end); Delete there goes on into the minutes, two
//     digits again since the focus left them (04), and takes their first (4); Delete again takes the 4
//     (empty: its placeholder shows, 0); ArrowRight at the end of the minutes goes on into the seconds,
//     ArrowLeft at their start back; ":" goes on into the seconds, 0 and 3 take the places of the 5 and
//     the 9: the time is 00:00:03, held, the tag's 5 minutes kept as duration, the fields the focus
//     left in two digits again;
//   - Enter plays on from it, the focus off the bar: the seconds go down once a second, finish comes
//     once at 3 s with the end painted (00:00:00), its toast shown; Dừng returns to 00:05:00;
//   - Insert of 75 over the minutes selected is 59 (never above the max); Escape puts back 00:05:00.
//
// CONTROL, each against a copy served through -overlay (the tree untouched), the proof failing at the
// step named (run 20/10/2026, late evening): reboot.css without the bar's three rules ("the bar's look":
// block, no fill, no radius, no padding on the <time>, and each field its own fill and padding, 89px
// wide, its digits at the start); countdown v1.0.0.js whose begin() selects the segment again ("a click
// between the 0 and the 4 of the minutes": minutes 0-2); whose keydown has no arrows across the bar
// ("ArrowRight at the end of a field goes on into the next": minutes 0-0 | minutes 0-0); whose erase()
// does not cross into the segment before ("Backspace at the start of the minutes goes back into the
// hours": 00:4:59, the caret kept at the start of the minutes); whose done() does not play on ("Enter
// plays on from it": no finish, 00:00:03 held).
func TestBrowserCountdownBar(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the countdown browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant := buildUISite(t, countdownBarFiles)
	page := serveUI(t, tenant, "http://localhost/")
	served := page.Body.String()
	if page.Code != http.StatusOK || warningsScriptRe.MatchString(served) {
		t.Fatalf("/ = %d, or it links warnings:\n%.3000s", page.Code, served)
	}
	for _, want := range []string{
		`<time class="countdown-time" id="left" role="group" aria-label="Thời gian"`,
		`placeholder="00" aria-valuemin="0" aria-valuemax="24" aria-valuenow="0" aria-valuetext="0 giờ" data-part="hours" value="00">:`,
		`placeholder="00" aria-valuemin="0" aria-valuemax="59" aria-valuenow="5" aria-valuetext="5 phút" data-part="minutes" value="05">:`,
		`placeholder="00" aria-valuemin="0" aria-valuemax="59" aria-valuenow="0" aria-valuetext="0 giây" data-part="seconds" value="00"></time>`,
	} {
		if !strings.Contains(served, want) {
			t.Errorf("the page as the server writes it has no %s", want)
		}
	}
	server := carouselServer(tenant)
	defer server.Close()
	check := func(t *testing.T, step, got, want string) {
		t.Helper()
		if got != want {
			t.Errorf("%s:\n got  %s\n want %s", step, got, want)
		}
	}
	d := openDevtools(t, browser, server.URL+"/", 1280, 800, "")
	d.loaded(exampleSettle)
	d.call("Page.enable", map[string]any{})
	d.call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": countdownHeard})
	networkReload(d)
	step := func(body string) string { d.t.Helper(); return countdownStep(d, countdownBarRead+body) }

	check(t, "as the server paints it", step(`await C.frames();
  var fields = Array.prototype.map.call(document.querySelectorAll("#left input"), function (f) { return f.placeholder; });
  return [bar(), C.state(), "placeholders " + fields.join(" ")].join(" | ");`), "00:05:00 | 300000 0:5:0 held 300000 | placeholders 00 00 00")

	check(t, "the bar's look", step(`
  var time = document.getElementById("left"), fields = Array.prototype.slice.call(time.querySelectorAll("input"));
  var plain = document.createElement("input");
  time.parentNode.appendChild(plain);
  var b = getComputedStyle(time), p = getComputedStyle(plain);
  var look = [b.display, b.borderTopWidth + " " + b.borderTopStyle,
    b.backgroundColor === p.backgroundColor ? "the field's fill" : b.backgroundColor + " not " + p.backgroundColor,
    b.borderTopLeftRadius === p.borderTopLeftRadius ? "the field's radius" : b.borderTopLeftRadius,
    b.paddingTop + " " + b.paddingLeft === p.paddingTop + " " + p.paddingLeft ? "the field's padding" : b.paddingTop + " " + b.paddingLeft].join(", ");
  plain.remove();
  var box = time.getBoundingClientRect(), c = document.createElement("canvas").getContext("2d");
  var own = fields.map(function (f) {
    var s = getComputedStyle(f), r = f.getBoundingClientRect();
    c.font = [s.fontStyle, s.fontWeight, s.fontSize, s.fontFamily].join(" ");
    var digits = c.measureText("00").width;
    return [s.borderTopWidth, s.backgroundColor, s.paddingLeft, s.textAlign, Math.abs(r.width - digits - 2) < 1.5 ? "two digits wide" : r.width + "px for " + digits,
      r.left > box.left && r.right < box.right && r.top > box.top && r.bottom < box.bottom ? "inside" :
        "outside by " + [r.left - box.left, box.right - r.right, r.top - box.top, box.bottom - r.bottom].map(Math.round).join(",")].join(" ");
  });
  var same = own.every(function (o) { return o === own[0]; }) ? "each field: " + own[0] : own.join(" / ");
  fields[1].focus();
  await C.frames();
  var ring = getComputedStyle(time).outlineStyle + " " + getComputedStyle(time).outlineWidth + ", the field's " + getComputedStyle(fields[1]).outlineStyle;
  fields[1].blur();
  return [look, same, "the ring: " + ring].join(" | ");`),
		"flex, 1px solid, the field's fill, the field's radius, the field's padding | "+
			"each field: 0px rgba(0, 0, 0, 0) 0px center two digits wide inside | the ring: solid 2px, the field's none")

	check(t, "Chạy: it counts", step(`
  window.__played = C.click("toggle");
  await C.wait(1300 - (C.now() - window.__played));
  return bar() + " " + C.state();`), "00:04:59 299000 0:4:59 playing 300000")

	countdownClickAt(t, d, "minutes", 1)
	check(t, "a click between the 0 and the 4 of the minutes holds it, the caret there", step(`
  var clicked = C.now(), at = [bar(), C.state(), focused()].join(" ");
  await C.wait(1200);
  return [at, "then " + C.changes("time", clicked, Infinity), bar() + " " + C.state()].join(" | ");`),
		"00:04:59 299000 0:4:59 held 300000 minutes 1-1 | then none | 00:04:59 299000 0:4:59 held 300000")

	countdownKey(d, "Backspace")
	check(t, "Backspace takes the 0", step(`await C.frames(); return [bar(), C.state(), focused()].join(" ");`),
		"00:4:59 299000 0:4:59 held 300000 minutes 0-0")
	countdownKey(d, "Backspace")
	check(t, "Backspace at the start of the minutes goes back into the hours and takes their last digit", step(`await C.frames(); return [bar(), C.state(), focused()].join(" ");`),
		"0:04:59 299000 0:4:59 held 300000 hours 1-1")
	countdownKey(d, "Delete")
	check(t, "Delete at the end of the hours goes on into the minutes and takes their first digit", step(`await C.frames(); return [bar(), C.state(), focused()].join(" ");`),
		"00:4:59 299000 0:4:59 held 300000 minutes 0-0")
	countdownKey(d, "Delete")
	check(t, "Delete takes the 4: the minutes empty, 0", step(`await C.frames(); return [bar(), C.state(), focused()].join(" ");`),
		"00::59 59000 0:0:59 held 300000 minutes 0-0")
	countdownKey(d, "ArrowRight")
	right := step(`return focused();`)
	countdownKey(d, "ArrowLeft")
	check(t, "ArrowRight at the end of a field goes on into the next, ArrowLeft at its start back", right+" | "+step(`return focused();`),
		"seconds 0-0 | minutes 2-2")
	countdownKey(d, ":")
	countdownKey(d, "0")
	countdownKey(d, "3")
	check(t, "\":\" goes on into the seconds, 0 and 3 take the places of the 5 and the 9", step(`await C.frames(); return [bar(), C.state(), focused()].join(" ");`),
		"00:00:03 3000 0:0:3 held 300000 seconds 2-2")

	countdownKey(d, "Enter")
	check(t, "Enter plays on from it, the toast at the end", step(`
  var resumed = C.when("toggle", "Tạm dừng", window.__played + 1500);
  await C.until(function () { return C.finished() >= 1; }, 4500);
  await C.wait(400);
  var f = window.__finishes[0];
  if (!f) return "no finish: " + bar() + " " + C.state() + " | " + C.changes("time", window.__played, Infinity);
  return [focused(), C.changes("time", resumed, Infinity, resumed, [1000, 2000, 3000]),
    "finish ×" + C.finished() + " " + C.near(f.at - resumed, 3000), C.toast(), bar() + " " + C.state()].join(" | ");`),
		"body | :: PT0H0M2S at 1s, :: PT0H0M1S at 2s, :: PT0H0M0S at 3s | finish ×1 at 3s | Trà đã sẵn sàng | 00:00:00 0 0:0:0 held 300000")
	check(t, "Dừng returns to 00:05:00", step(`C.click("stop"); await C.frames(); return bar() + " " + C.state();`), "00:05:00 300000 0:5:0 held 300000")

	countdownClickAt(t, d, "minutes", 2)
	countdownSelectAll(d)
	countdownInsert(d, "75")
	check(t, "75 over the minutes selected is 59", step(`await C.frames(); return [bar(), C.state(), focused()].join(" ");`),
		"00:59:00 3540000 0:59:0 held 3540000 minutes 2-2")
	countdownKey(d, "Escape")
	check(t, "Escape puts back 00:05:00", step(`await C.frames(); return [bar(), C.state(), focused()].join(" ");`),
		"00:05:00 300000 0:5:0 held 300000 body")
	check(t, "complaints of the page", d.evaluate(`JSON.stringify(window.__complaints)`), "[]")
}
