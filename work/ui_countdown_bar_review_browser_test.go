package work

import (
	"encoding/json"
	"net/http"
	"testing"
)

// countdownBarReviewFiles is the bar's page (countdownBarFiles) with a link before the countdown, to
// go back to with Shift+Tab, and one after it, to drag, beside plain text; /tiep is where the links go.
var countdownBarReviewFiles = map[string]string{
	"localhost/router.work": countdownBrowserFiles["localhost/router.work"],
	"localhost/view.ui":     `<view><content /><overlay /></view>`,
	"localhost/overlay.ui":  toastBrowserFiles["localhost/overlay.ui"],
	"localhost/content.ui": `<content padding="4" gap="2">
  <link id="first" value="/tiep">Trước thanh</link>
  <countdown as="$tea" duration="5m" finish="$toast.success('Trà đã sẵn sàng')" flex="row" align="center" gap="3">
    <time id="left" alt="Thời gian"><input id="hours" value="{{ hours }}" alt="Giờ" />:<input id="minutes" value="{{ minutes }}" alt="Phút" />:<input id="seconds" value="{{ seconds }}" alt="Giây" /></time>
    <button id="more" click="minutes++">+1 phút</button>
    <button id="toggle" click="toggle()">{{ playing ? 'Tạm dừng' : 'Chạy' }}</button>
    <button id="stop" click="stop()">Dừng</button>
  </countdown>
  <link id="next" value="/tiep">Một liên kết để kéo</link>
  <text id="plain">Chữ thường</text>
</content>`,
	"localhost/tiep/content.ui": `<content padding="4"><text>Tiếp</text></content>`,
}

// countdownKeyWith presses a key with modifiers held (2 Ctrl, 1 Alt, 8 Shift), as a person does.
func countdownKeyWith(d *devtools, key string, code, modifiers int) {
	d.t.Helper()
	for _, kind := range []string{"rawKeyDown", "keyUp"} {
		d.call("Input.dispatchKeyEvent", map[string]any{"type": kind, "key": key, "code": key, "windowsVirtualKeyCode": code, "modifiers": modifiers})
	}
}

// TestBrowserCountdownBarReview proves, on a page the engine serves, in a real browser whose clock runs
// in real time, with its real mouse, keys and input method (DevTools Input.dispatchMouseEvent at a
// point between two digits, Input.dispatchKeyEvent, Input.insertText, Input.imeSetComposition), what
// the review of the bar (20/10/2026, late evening) found:
//   - a click before the first digit of the hours, then 3 and 0: 24:00:00, the caret at the end (it
//     was 20:00:00: the 0 went over the 4 of the 24 shown); before the first of the seconds, 6 and 5:
//     00:05:59 (it was 00:05:55);
//   - the hours selected, 2 and 5 (a typo for 2): 24:00:00; Backspace: 2:05:00, the 5 minutes there
//     again (they were gone, 2:00:00);
//   - 3 typed over the 5 of the minutes, Tab to the seconds: change minutes=03; Escape puts back
//     00:05:00, and a change says minutes=05 (none came: the page's handler kept 03);
//   - a full-width 3 at the caret (Input.insertText "３"), and １２ an input method commits over the
//     minutes selected: 00:03:00, then 00:12:00 (both were refused);
//   - Ctrl+Backspace at the end of the minutes takes 05 (it took the 5);
//   - playing, a click into the minutes holds it, Enter plays on, and Tab goes on to +1 phút, the button
//     after the bar, playing still (it went into the seconds and held it again); a click into the
//     seconds holds it, Escape plays on, and Shift+Tab goes to the link before the bar, playing still;
//   - playing, a click into the minutes holds it, then the link after the bar dragged 40px away (Chrome
//     sends dragstart, drop and dragend, no mouseup): it plays on (it stayed held, reading Chạy, until a
//     later click anywhere);
//   - the page complains of nothing.
//
// CONTROL, each against a copy of countdown v1.0.0.js with one fix taken out, served through -overlay
// (the tree untouched), the proof failing at the steps named and no other (run 21/10/2026): type() not
// typing on what was typed ("3 and 0 at the start of the hours": 20:05:00; "6 and 5 at the start of the
// seconds": 00:05:55); part() keeping no wrote ("Backspace: 2:05:00": 2:00:00); done() telling no
// segment ("Escape puts back 00:05:00, and a change says so": minutes=03 alone); wide() giving the text
// back as it came ("a full-width 3": 00:05:00; "１２ an input method commits": 00:05:00); erase() with
// no word ("Ctrl+Backspace": 00:0:00, minutes 1-1); the Enter branch setting no tabindex ("Enter, then
// Tab goes on to +1 phút": held Chạy seconds, stopped; "Escape, then Shift+Tab": held Chạy minutes);
// init() with no dragend listener ("the link dragged away: it plays on": held Chạy, no mouseup). The
// copy from before the review fails at every one of those steps.
func TestBrowserCountdownBarReview(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the countdown browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant := buildUISite(t, countdownBarReviewFiles)
	page := serveUI(t, tenant, "http://localhost/")
	if served := page.Body.String(); page.Code != http.StatusOK || warningsScriptRe.MatchString(served) {
		t.Fatalf("/ = %d, or it links warnings:\n%.3000s", page.Code, served)
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
	d.call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": countdownEditsHeard})
	networkReload(d)
	const read = countdownBarRead + `
  var all = function () { return [bar(), C.state(), focused()].join(" "); };
  var playing = function () { var tea = kit.scope.$tea; return (tea.playing ? "playing" : "held") + " " + document.getElementById("toggle").textContent; };`
	step := func(body string) string { d.t.Helper(); return countdownStep(d, read+body) }

	check(t, "as the server paints it", step(`return all();`), "00:05:00 300000 0:5:0 held 300000 body")

	// a digit after one the max took goes on with what was typed
	countdownClickAt(t, d, "hours", 0)
	countdownKey(d, "3")
	countdownKey(d, "0")
	check(t, "3 and 0 at the start of the hours", step(`await C.frames(); return all();`), "24:00:00 86400000 24:0:0 held 86400000 hours 2-2")
	countdownKey(d, "Escape")
	countdownClickAt(t, d, "seconds", 0)
	countdownKey(d, "6")
	countdownKey(d, "5")
	check(t, "6 and 5 at the start of the seconds", step(`await C.frames(); return all();`), "00:05:59 359000 0:5:59 held 359000 seconds 2-2")
	countdownKey(d, "Escape")

	// over 24 hours, the other parts kept while the edit lasts
	countdownClickAt(t, d, "hours", 1)
	countdownSelectAll(d)
	countdownKey(d, "2")
	countdownKey(d, "5")
	check(t, "2 and 5 over the hours: 24:00:00", step(`await C.frames(); return all();`), "24:00:00 86400000 24:0:0 held 86400000 hours 2-2")
	countdownKey(d, "Backspace")
	check(t, "Backspace: 2:05:00", step(`await C.frames(); return all();`), "2:05:00 7500000 2:5:0 held 7500000 hours 1-1")
	countdownKey(d, "Escape")

	// Escape putting back minutes whose change was sent
	countdownStep(d, `await C.frames(); window.__changes = []; return "";`)
	countdownClickAt(t, d, "minutes", 1)
	countdownKey(d, "3")
	countdownKey(d, "Tab")
	check(t, "03, Tab to the seconds: change", step(`await C.frames(); return all() + " " + window.__changes.join(" ");`),
		"00:03:00 180000 0:3:0 held 180000 seconds 0-2 minutes=03")
	countdownKey(d, "Escape")
	check(t, "Escape puts back 00:05:00, and a change says so", step(`await C.frames(); return all() + " " + window.__changes.join(" ");`),
		"00:05:00 300000 0:5:0 held 300000 body minutes=03 minutes=05")

	// a full-width input method's digits
	countdownClickAt(t, d, "minutes", 1)
	countdownInsert(d, "３")
	check(t, "a full-width 3", step(`await C.frames(); return all();`), "00:03:00 180000 0:3:0 held 180000 minutes 2-2")
	countdownSelectAll(d)
	d.call("Input.imeSetComposition", map[string]any{"text": "１２", "selectionStart": 2, "selectionEnd": 2})
	countdownInsert(d, "１２")
	check(t, "１２ an input method commits", step(`await C.frames(); return all();`), "00:12:00 720000 0:12:0 held 720000 minutes 2-2")
	countdownKey(d, "Escape")

	// a word
	countdownClickAt(t, d, "minutes", 2)
	countdownKeyWith(d, "Backspace", 8, 2)
	check(t, "Ctrl+Backspace at the end of the minutes", step(`await C.frames(); return all();`), "00::00 0 0:0:0 held 0 minutes 0-0")
	countdownKey(d, "Escape")
	check(t, "Escape", step(`await C.frames(); return all();`), "00:05:00 300000 0:5:0 held 300000 body")

	// Enter, Escape: the next Tab goes on past the bar
	countdownStep(d, `C.click("toggle"); await C.wait(1300); return "";`)
	countdownClickAt(t, d, "minutes", 1)
	check(t, "playing, a click into the minutes holds it", step(`await C.frames(); return playing() + " " + focused();`), "held Chạy minutes 1-1")
	countdownKey(d, "Enter")
	check(t, "Enter plays on", step(`await C.frames(); return playing() + " " + focused();`), "playing Tạm dừng body")
	countdownKey(d, "Tab")
	check(t, "Enter, then Tab goes on to +1 phút", step(`await C.frames(); var at = kit.scope.$tea.remaining, was = playing() + " " + document.activeElement.id;
  await C.wait(1100); return was + " | " + (kit.scope.$tea.remaining < at ? "counting" : "stopped at " + at) + " " + playing();`),
		"playing Tạm dừng more | counting playing Tạm dừng")
	countdownClickAt(t, d, "seconds", 1)
	countdownKey(d, "Escape")
	check(t, "Escape plays on", step(`await C.frames(); return playing() + " " + focused();`), "playing Tạm dừng body")
	carouselShiftTab(d)
	check(t, "Escape, then Shift+Tab goes to the link before the bar", step(`await C.frames(); var at = kit.scope.$tea.remaining, was = playing() + " " + document.activeElement.id;
  await C.wait(1100); return was + " | " + (kit.scope.$tea.remaining < at ? "counting" : "stopped at " + at) + " " + playing();`),
		"playing Tạm dừng first | counting playing Tạm dừng")

	// a press that becomes a drag
	countdownClickAt(t, d, "minutes", 1)
	check(t, "a click into the minutes holds it", step(`await C.frames(); window.__events = []; return playing() + " " + focused();`), "held Chạy minutes 1-1")
	var from, to struct{ X, Y float64 }
	_ = json.Unmarshal([]byte(d.evaluate(`(function () { var r = document.getElementById("next").getBoundingClientRect(); return JSON.stringify({ X: r.left + 8, Y: r.top + r.height / 2 }); })()`)), &from)
	_ = json.Unmarshal([]byte(d.evaluate(`(function () { var r = document.getElementById("plain").getBoundingClientRect(); return JSON.stringify({ X: r.left + r.width / 2, Y: r.bottom + 40 }); })()`)), &to)
	d.call("Input.dispatchMouseEvent", map[string]any{"type": "mouseMoved", "x": from.X, "y": from.Y})
	d.call("Input.dispatchMouseEvent", map[string]any{"type": "mousePressed", "x": from.X, "y": from.Y, "button": "left", "clickCount": 1})
	for i := 1; i <= 10; i++ {
		x, y := from.X+(to.X-from.X)*float64(i)/10, from.Y+(to.Y-from.Y)*float64(i)/10
		d.call("Input.dispatchMouseEvent", map[string]any{"type": "mouseMoved", "x": x, "y": y, "button": "left", "buttons": 1})
	}
	d.call("Input.dispatchMouseEvent", map[string]any{"type": "mouseReleased", "x": to.X, "y": to.Y, "button": "left", "clickCount": 1})
	check(t, "the link dragged away: it plays on", step(`await C.wait(1300); return playing() + " " + document.activeElement.id + " " + window.__events.filter(function (e) { return /^mouseup/.test(e); }).length;`),
		"playing Tạm dừng next 0")
	countdownStep(d, `C.click("stop"); await C.frames(); return "";`)
	check(t, "complaints of the page", d.evaluate(`JSON.stringify(window.__complaints)`), "[]")
}
