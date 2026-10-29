package ui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/kitwork/engine/ui/stylesheet"
)

// The countdown (decisions of 19/10/2026, countdown.go): the author draws the time left in one <time>
// and the buttons; the engine reads duration, writes the host, the timer's role and datetime, the
// server's first paint, and refuses or warns of what does not fit, each at its place with its words.

// countdownTea is the countdown of the decision and the button that pauses it from elsewhere on the
// page, through its alias.
const countdownTea = `<countdown as="$tea" duration="5m" ready="play()" finish="$toast.success('Trà đã sẵn sàng')" flex="row" gap="3">` +
	`<text>Còn <time>{{ minutes }}:{{ String(seconds).padStart(2, '0') }}</time></text>` +
	`<button click="toggle()">{{ playing ? 'Tạm dừng' : 'Chạy' }}</button>` +
	`<button click="stop()">Dừng</button>` +
	`</countdown>` +
	`<button click="$tea.pause()">Tạm dừng pha trà</button>`

// countdownClock is the time left as the decision draws it: minutes, then seconds in two digits.
const countdownClock = `<time>{{ minutes }}:{{ String(seconds).padStart(2, '0') }}</time>`

// countdownStarted are the words of a countdown that warns of nothing: it starts as the page opens and
// says when the time is up.
const countdownStarted = ` ready="play()" finish="$toast.info('Hết giờ')"`

// countdownOf is a countdown with what is written inside it; words go on the tag.
func countdownOf(words, inside string) string {
	return `<countdown` + words + `>` + inside + `</countdown>`
}

// countdownSite is a site whose content.ui is source, with a toast in the overlay whose notice changes
// with its tone (networkToast), so a finish that says a notice through it is told nothing.
func countdownSite(source string) map[string]string {
	return toastSite(networkToast, source)
}

// countdownPage compiles a countdown site that must compile and returns the page rendered at "/" with
// no data, and the warnings, one line each.
func countdownPage(t *testing.T, source string) (string, []string) {
	t.Helper()
	s := memorySite("", countdownSite(source))
	s.Reload = true
	page, warned := compileDiagnose(t, s)
	return page.Render(Request{Path: "/"}), warned
}

// countdownQuiet is countdownPage for a page that must compile with no warning at all.
func countdownQuiet(t *testing.T, source string) string {
	t.Helper()
	html, warned := countdownPage(t, source)
	if len(warned) > 0 {
		t.Errorf("want no warning, got:\n%s", strings.Join(warned, "\n"))
	}
	return html
}

// countdownHostRe is a countdown's host as the page renders it: its opening tag.
var countdownHostRe = regexp.MustCompile(`<[a-z]+ [^>]*data-kitwork-component="countdown[^"]*"[^>]*>`)

// The countdown of the decision: the tag builds a box, the host, with the author's layout words, the
// alias, its events in the order written (ready, then finish) and the duration in milliseconds for the
// instance; the host has no role. The <time> written without value is the time left: the class
// countdown-time (one width for its digits), role="timer" and no aria-live (a timer's is off by its
// role), a datetime bound to the parts and written by the server as it starts (PT0H5M0S), and the
// server's first paint, all of the duration left: 5:00, "Chạy" (not playing). Elsewhere on the page the
// alias calls it. A page in English is painted the same: nothing of the countdown is in words.
// CONTROL: an ordinary <time value>, outside the host or inside it, writes datetime as written and none
// of the timer's words. With countdownElement writing nothing (no role, no datetime binding) this test
// failed on the <time> rows, and with the countdown-time class not added it failed there too (run
// 19/10/2026 on overlaid copies of countdown.go and compile.go).
func TestCountdownHostAndItsTime(t *testing.T) {
	html := countdownQuiet(t, `<content>`+countdownTea+`</content>`)
	mustContain(t, html,
		`<div class="flex-row gap-3" data-kitwork-component="countdown: $tea" data-kitwork-event="ready: play(); finish: $toast.success(&#39;Trà đã sẵn sàng&#39;)" data-kitwork-scope="duration: 300000">`,
		`<p>Còn <time class="countdown-time" role="timer" data-kitwork-bind="datetime: &#39;PT&#39; + hours + &#39;H&#39; + minutes + &#39;M&#39; + seconds + &#39;S&#39;; text: [(minutes), &#39;:&#39;, (String(seconds).padStart(2, &#39;0&#39;))].join(&#39;&#39;)" datetime="PT0H5M0S">5:00</time></p>`,
		`type="button" data-kitwork-bind="text: playing ? &#39;Tạm dừng&#39; : &#39;Chạy&#39;" data-kitwork-event="click: toggle()">Chạy</button>`,
		`type="button" data-kitwork-event="click: stop()">Dừng</button></div>`,
		`type="button" data-kitwork-event="click: $tea.pause()">Tạm dừng pha trà</button>`,
	)
	if host := countdownHostRe.FindString(html); strings.Contains(host, "role=") || strings.Contains(host, "aria-") {
		t.Errorf("the host has a role of its own: %s", host)
	}
	if n := strings.Count(html, `role="timer"`); n != 1 {
		t.Errorf(`role="timer" written %d times, want once:%s`, n, html)
	}
	mustNotContain(t, html, `aria-live`)

	// the <time> straight in the host, not in a text, is the time left the same way
	html = countdownQuiet(t, `<content>`+countdownOf(` duration="90s"`+countdownStarted, `<time>{{ remaining / 1000 }}</time>`)+`</content>`)
	mustContain(t, html, `<div class="flex-column" data-kitwork-component="countdown" data-kitwork-event="ready: play(); finish: $toast.info(&#39;Hết giờ&#39;)" data-kitwork-scope="duration: 90000">`+
		`<time class="countdown-time" role="timer" data-kitwork-bind="datetime: &#39;PT&#39; + hours + &#39;H&#39; + minutes + &#39;M&#39; + seconds + &#39;S&#39;; text: remaining / 1000" datetime="PT0H1M30S">90</time></div>`)

	// CONTROL: a time of a moment, outside the host and inside it beside the time left
	html = countdownQuiet(t, `<content><time value="2026-10-19">19/10</time>`+
		countdownOf(` duration="5m"`+countdownStarted, countdownClock+`<text>từ <time value="2026-10-19">19/10</time></text>`)+`</content>`)
	if n := strings.Count(html, `<time datetime="2026-10-19">19/10</time>`); n != 2 {
		t.Errorf("an ordinary time is written %d times as written, want 2:\n%s", n, html)
	}
	if n := strings.Count(html, `countdown-time`) + strings.Count(html, `role="timer"`); n != 2 {
		t.Errorf("the timer's words are written %d times, want 2 (the time left's class and role):\n%s", n, html)
	}
}

// The server paints the instance as it starts: all of the duration left, in its parts, not playing; the
// duration goes to the instance in milliseconds, a decimal written read as written (1.5m is 90000), and
// the alias read outside the host renders the same start ($tea.minutes is 5 before the kernel runs).
// The datetime is the parts as a valid HTML duration, the hours up to 24.
// CONTROL: with startState leaving the state as the catalog declares it (all 0), the datetime, the
// painted time and the alias rows failed; with inputs() writing nothing, every scope row failed (run
// 19/10/2026 on overlaid copies of countdown.go).
func TestCountdownStartsWhereTheServerPaints(t *testing.T) {
	const clock = `<time>{{ hours }}:{{ minutes }}:{{ seconds }}</time>`
	const outside = `<text>{{ $tea.hours }}|{{ $tea.minutes }}|{{ $tea.seconds }}|{{ $tea.remaining }}|{{ $tea.duration }}|{{ $tea.playing }}</text>`
	for _, c := range []struct{ duration, scope, datetime, shown, read string }{
		{"1s", "1000", "PT0H0M1S", "0:0:1", "0|0|1|1000|1000|false"},
		{"1000ms", "1000", "PT0H0M1S", "0:0:1", "0|0|1|1000|1000|false"},
		{"30s", "30000", "PT0H0M30S", "0:0:30", "0|0|30|30000|30000|false"},
		{"90s", "90000", "PT0H1M30S", "0:1:30", "0|1|30|90000|90000|false"},
		{"1.5m", "90000", "PT0H1M30S", "0:1:30", "0|1|30|90000|90000|false"},
		{"5m", "300000", "PT0H5M0S", "0:5:0", "0|5|0|300000|300000|false"},
		{" 5m ", "300000", "PT0H5M0S", "0:5:0", "0|5|0|300000|300000|false"},
		{"2.25m", "135000", "PT0H2M15S", "0:2:15", "0|2|15|135000|135000|false"},
		{"1h", "3600000", "PT1H0M0S", "1:0:0", "1|0|0|3600000|3600000|false"},
		{"2.5h", "9000000", "PT2H30M0S", "2:30:0", "2|30|0|9000000|9000000|false"},
		{"86399s", "86399000", "PT23H59M59S", "23:59:59", "23|59|59|86399000|86399000|false"},
		{"24h", "86400000", "PT24H0M0S", "24:0:0", "24|0|0|86400000|86400000|false"},
	} {
		html := countdownQuiet(t, `<content>`+countdownOf(` as="$tea" duration="`+c.duration+`"`+countdownStarted, clock)+outside+`</content>`)
		if host := countdownHostRe.FindString(html); !strings.HasSuffix(host, ` data-kitwork-scope="duration: `+c.scope+`">`) {
			t.Errorf("%q: the host is %s, want duration: %s", c.duration, host, c.scope)
		}
		mustContain(t, html, `datetime="`+c.datetime+`">`+c.shown+`</time>`, `>`+c.read+`</p>`)
	}
}

// countdownParts counts a time left in whole seconds, rounded up, as the component shows it.
// CONTROL: rounded down instead, the rows of a part of a second failed (run 19/10/2026 on an overlaid
// copy of countdown.go).
func TestCountdownPartsRoundUp(t *testing.T) {
	for _, c := range []struct{ ms, hours, minutes, seconds float64 }{
		{0, 0, 0, 0}, {1, 0, 0, 1}, {999, 0, 0, 1}, {1000, 0, 0, 1}, {1001, 0, 0, 2},
		{59001, 0, 1, 0}, {3599999, 1, 0, 0}, {86400000, 24, 0, 0},
	} {
		if h, m, s := countdownParts(c.ms); h != c.hours || m != c.minutes || s != c.seconds {
			t.Errorf("%v ms: %v:%v:%v, want %v:%v:%v", c.ms, h, m, s, c.hours, c.minutes, c.seconds)
		}
	}
}

// What duration does not take is an error at the word, saying what to write: none written (at the
// tag), bare or empty, a prefix, a value from data, a negative (told the time without its minus, in
// whole seconds), zero, a number without its unit (seconds: 300 is 300s, or 5m), two units (one number
// in the smaller, in whole seconds), a unit spelled another way (a Vietnamese or English word, capitals,
// ISO 8601) or with a space before it, what is no whole number of seconds (told its seconds rounded up,
// as the countdown shows it), longer than 24 hours (digits too many to hold included), and what is no
// duration at all. A word is told before the empty host: an empty host is told it is empty before it is
// told its duration is missing.
// CONTROL: each written right compiles (TestCountdownStartsWhereTheServerPaints). With the whole-seconds
// check and the 24-hour limit removed from countdownDuration, their rows compiled and this test failed;
// with the foreign branch reading as the spaced one, its rows failed (run 19/10/2026 on overlaid copies
// of countdown.go).
func TestCountdownDurationWrittenWrong(t *testing.T) {
	const missing = `countdown cần duration: đếm bao lâu (duration="5m")`
	const longer = ` dài hơn 24 giờ: countdown đếm từ lúc trang mở và đếm lại khi tải lại trang; đếm tới một thời điểm (until) chưa có`
	const notOne = ` không phải một thời lượng: viết duration="30s", duration="5m" hay duration="1h"`
	nines := strings.Repeat("9", 400)
	for _, c := range []struct{ words, needle, says string }{
		{countdownStarted, `<countdown`, missing},
		{` duration` + countdownStarted, `duration `, missing},
		{` duration=""` + countdownStarted, `duration=`, missing},
		{` duration="  "` + countdownStarted, `duration=`, missing},

		{` medium:duration="5m"`, `medium:duration=`, `duration của countdown không nhận tiền tố: nó được quyết một lần khi trang dựng`},
		{` dark:duration="5m"`, `dark:duration=`, `duration của countdown không nhận tiền tố: nó được quyết một lần khi trang dựng`},
		{` duration="{{ wait }}"`, `duration=`, `duration của countdown là một thời lượng cố định (duration="5m"), không có {{ }}: nó được quyết một lần khi trang dựng`},
		{` duration="{{ minutes }}m"`, `duration=`, `duration của countdown là một thời lượng cố định (duration="5m"), không có {{ }}: nó được quyết một lần khi trang dựng`},

		{` duration="-5m"`, `duration=`, `duration không nhận thời lượng âm (-5m): viết một thời lượng như duration="5m"`},
		{` duration="-30s"`, `duration=`, `duration không nhận thời lượng âm (-30s): viết một thời lượng như duration="30s"`},
		{` duration="-300"`, `duration=`, `duration không nhận thời lượng âm (-300): viết một thời lượng như duration="300s"`},
		{` duration="-1m30s"`, `duration=`, `duration không nhận thời lượng âm (-1m30s): viết một thời lượng như duration="90s"`},
		{` duration="-1500ms"`, `duration=`, `duration không nhận thời lượng âm (-1500ms): viết một thời lượng như duration="2s"`},
		{` duration="-25h"`, `duration=`, `duration không nhận thời lượng âm (-25h): viết một thời lượng như duration="5m"`},
		{` duration="-fast"`, `duration=`, `duration="-fast"` + notOne},

		{` duration="0s"`, `duration=`, `duration="0s": countdown cần ít nhất một giây để đếm, như duration="30s"`},
		{` duration="0"`, `duration=`, `duration="0": countdown cần ít nhất một giây để đếm, như duration="30s"`},
		{` duration="0m"`, `duration=`, `duration="0m": countdown cần ít nhất một giây để đếm, như duration="30s"`},
		{` duration="0.0"`, `duration=`, `duration="0.0": countdown cần ít nhất một giây để đếm, như duration="30s"`},

		{` duration="300"`, `duration=`, `duration="300" thiếu đơn vị: viết duration="300s" (300 giây) hay duration="5m"`},
		{` duration="45"`, `duration=`, `duration="45" thiếu đơn vị: viết duration="45s" (45 giây)`},
		{` duration="90"`, `duration=`, `duration="90" thiếu đơn vị: viết duration="90s" (90 giây)`},
		{` duration="7200"`, `duration=`, `duration="7200" thiếu đơn vị: viết duration="7200s" (7200 giây) hay duration="2h"`},

		{` duration="1m30s"`, `duration=`, `duration="1m30s" là hai đơn vị: viết một số với một đơn vị, duration="90s"`},
		{` duration="1h 30m"`, `duration=`, `duration="1h 30m" là hai đơn vị: viết một số với một đơn vị, duration="90m"`},
		{` duration="1h1m1s"`, `duration=`, `duration="1h1m1s" là 3 đơn vị: viết một số với một đơn vị, duration="3661s"`},
		{` duration="1s500ms"`, `duration=`, `duration="1s500ms" là hai đơn vị: viết một số với một đơn vị, duration="2s"`},

		{` duration="5p"`, `duration=`, `duration="5p": phút viết m, viết duration="5m"`},
		{` duration="5phút"`, `duration=`, `duration="5phút": phút viết m, viết duration="5m"`},
		{` duration="5 phút"`, `duration=`, `duration="5 phút": phút viết m, viết duration="5m"`},
		{` duration="5min"`, `duration=`, `duration="5min": phút viết m, viết duration="5m"`},
		{` duration="PT5M"`, `duration=`, `duration="PT5M": phút viết m, viết duration="5m"`},
		{` duration="30giây"`, `duration=`, `duration="30giây": giây viết s, viết duration="30s"`},
		{` duration="5S"`, `duration=`, `duration="5S": giây viết s, viết duration="5s"`},
		{` duration="2giờ"`, `duration=`, `duration="2giờ": giờ viết h, viết duration="2h"`},
		{` duration="2hours"`, `duration=`, `duration="2hours": giờ viết h, viết duration="2h"`},
		{` duration="5 s"`, `duration=`, `duration="5 s" có khoảng trắng trước đơn vị, .ui viết số liền đơn vị: viết duration="5s"`},

		{` duration="1500ms"`, `duration=`, `duration="1500ms": countdown đếm từng giây, viết một số giây tròn như duration="2s"`},
		{` duration="1.5s"`, `duration=`, `duration="1.5s": countdown đếm từng giây, viết một số giây tròn như duration="2s"`},
		{` duration="500ms"`, `duration=`, `duration="500ms": countdown đếm từng giây, viết một số giây tròn như duration="1s"`},
		{` duration="2001ms"`, `duration=`, `duration="2001ms": countdown đếm từng giây, viết một số giây tròn như duration="3s"`},

		{` duration="25h"`, `duration=`, `duration="25h"` + longer},
		{` duration="86401s"`, `duration=`, `duration="86401s"` + longer},
		{` duration="1441m"`, `duration=`, `duration="1441m"` + longer},
		{` duration="` + nines + `s"`, `duration=`, `duration="` + nines + `s"` + longer},

		{` duration="soon"`, `duration=`, `duration="soon"` + notOne},
		{` duration="5d"`, `duration=`, `duration="5d"` + notOne},
		{` duration=".5m"`, `duration=`, `duration=".5m"` + notOne},
		{` duration="1m30"`, `duration=`, `duration="1m30"` + notOne},
	} {
		t.Run(strings.TrimSpace(c.words), func(t *testing.T) {
			source := `<content>` + countdownOf(c.words, countdownClock) + `</content>`
			if got, want := compileError(t, countdownSite(source)), where("content.ui", source, c.needle)+c.says; got != want {
				t.Errorf("%.60q:\n got  %.300s\n want %.300s", c.words, got, want)
			}
		})
	}

	// a word written wrong is told before the empty host; the empty host before its missing duration
	for _, c := range []struct{ source, needle, says string }{
		{`<content><countdown duration="5p" /></content>`, `duration=`, `duration="5p": phút viết m, viết duration="5m"`},
		{`<content><countdown /></content>`, `<countdown`, `countdown rỗng`},
	} {
		mustFail(t, countdownSite(c.source), where("content.ui", c.source, c.needle), c.says)
	}
}

// The words other countdowns, other libraries and the web give a countdown are an error on its tag,
// each told what it is here, with or without a prefix, an event among them with its modifiers: how long
// is duration, when it starts is ready="play()", the end is finish; the parts and playing are its state,
// computed, never written; its role is the engine's; its methods are called in an event. change is an
// event that bubbles from a field inside the box, and stays the box's.
// CONTROL: the countdown written right compiles (TestCountdownHostAndItsTime), and so does change on it
// (below). With countdownHost no longer reading countdownStrangers, every row compiled or was told
// something else, and this test failed (run 19/10/2026 on an overlaid copy of countdown.go).
func TestCountdownWordsItDoesNotHave(t *testing.T) {
	const left = `countdown chưa đếm tới một thời điểm: đếm bao lâu là duration (duration="10m"); until chưa có`
	for _, c := range []struct{ word, says string }{
		{"autoplay", `countdown không có autoplay: chạy ngay khi trang mở là ready="play()"`},
		{"autostart", `countdown không có autostart: chạy ngay khi trang mở là ready="play()"`},
		{"start", `countdown không có start: chạy ngay khi trang mở là ready="play()"`},
		{"medium:autoplay", `countdown không có autoplay: chạy ngay khi trang mở là ready="play()"`},
		{"time", `countdown không có time: đếm bao lâu là duration (duration="5m")`},
		{"length", `countdown không có length: đếm bao lâu là duration (duration="5m")`},
		{"total", `countdown không có total: đếm bao lâu là duration (duration="5m")`},
		{"timeout", `countdown không có timeout: trong Kitwork timeout là giới hạn thời gian của một request; đếm bao lâu là duration, hết giờ là finish`},
		{"interval", `countdown không có interval: countdown đếm từng giây, engine lo; bỏ interval`},
		{"step", `countdown không có step: countdown đếm từng giây, engine lo; bỏ step`},
		{"precision", `countdown không có precision: countdown đếm từng giây, engine lo; bỏ precision`},
		{"millisecond", `countdown không có millisecond: countdown đếm từng giây, engine lo; bỏ millisecond`},
		{"format", `countdown không có format: thời gian do người viết vẽ, <time>{{ minutes }}:{{ String(seconds).padStart(2, '0') }}</time>`},
		{"loop", `countdown không có loop: hết giờ thì chạy lại là finish="play()"`},
		{"repeat", `countdown không có repeat: hết giờ thì chạy lại là finish="play()"`},
		{"until", left}, {"deadline", left}, {"date", left}, {"target", left}, {"at", left},
		{"delay", `countdown chưa có delay: chạy lúc nào là ready="play()" hay một nút click="play()"`},
		{"transition", `countdown không có transition: chữ số đổi ngay; hiệu ứng cho chữ số là animate-… trên phần con`},
		{"animation", `countdown không có animation: chữ số đổi ngay; hiệu ứng cho chữ số là animate-… trên phần con`},
		{"persist", `countdown không có persist: thời gian còn lại không giữ qua lần tải lại; một hạn phải giữ là việc của server`},
		{"alt", `countdown chưa có alt: chữ quanh <time> đã nói nó đếm gì (Còn <time>…</time>)`},
		{"seconds", `countdown không có seconds: seconds là phần giây còn lại, engine tính từ duration; đếm 30 giây là duration="30s"`},
		{"minutes", `countdown không có minutes: minutes là phần phút còn lại, engine tính từ duration; đếm 5 phút là duration="5m"`},
		{"hours", `countdown không có hours: hours là phần giờ còn lại, engine tính từ duration; đếm 2 giờ là duration="2h"`},
		{"remaining", `countdown không có remaining: remaining là thời gian còn lại (mili giây), engine tính từ duration; đếm 30 giây là duration="30s"`},
		{"playing", `countdown không có playing: playing là trạng thái của nó; chạy khi trang mở là ready="play()"`},
		{"paused", `countdown không có paused: đang dừng là !playing`},
		{"play", `play là hàm của countdown, không phải một chữ trên thẻ: gọi nó trong một sự kiện (ready="play()", click="pause()")`},
		{"role", `vai trò của countdown là việc của engine (role="timer" trên <time> của nó): bỏ role`},
		{"aria-live", `vai trò của countdown là việc của engine (role="timer" trên <time> của nó): bỏ aria-live`},
		{"aria-label", `vai trò của countdown là việc của engine (role="timer" trên <time> của nó): bỏ aria-label`},
		{"end", `countdown không có sự kiện end: hết giờ là finish (finish="$toast.info('Hết giờ')")`},
		{"ended", `countdown không có sự kiện ended: hết giờ là finish (finish="$toast.info('Hết giờ')")`},
		{"done", `countdown không có sự kiện done: hết giờ là finish (finish="$toast.info('Hết giờ')")`},
		{"done:once", `countdown không có sự kiện done: hết giờ là finish (finish="$toast.info('Hết giờ')")`},
		{"complete", `countdown không có sự kiện complete: hết giờ là finish (finish="$toast.info('Hết giờ')")`},
		{"expire", `countdown không có sự kiện expire: hết giờ là finish (finish="$toast.info('Hết giờ')")`},
		{"onfinish", `countdown không có sự kiện onfinish: hết giờ là finish (finish="$toast.info('Hết giờ')")`},
		{"tick", `countdown không gửi tick: remaining, hours, minutes, seconds tự vẽ lại mỗi giây; việc lúc hết giờ là finish`},
	} {
		t.Run(c.word, func(t *testing.T) {
			source := `<content>` + countdownOf(` duration="5m" `+c.word+`="x"`+countdownStarted, countdownClock) + `</content>`
			mustFailExactly(t, countdownSite(source), where("content.ui", source, c.word+`="x"`)+c.says)
		})
	}

	// CONTROL: change, heard from a field inside the box, is the box's
	html := countdownQuiet(t, `<content>`+countdownOf(` duration="5m" change="stop()"`+countdownStarted, countdownClock+`<input type="checkbox" alt="Dừng khi đổi" />`)+`</content>`)
	mustContain(t, html, `data-kitwork-event="change: stop(); ready: play(); finish: $toast.info(&#39;Hết giờ&#39;)"`)
}

// What is inside the host is checked before anything compiles: an empty host (with only spaces, or
// written self-closing, with or without its duration), no <time> without value for the time left (a time
// of a moment is no time left; one inside a component nested in it is that component's), two, one that
// reads none of remaining, hours, minutes, seconds (duration and playing are not the time left), and one
// that cannot show all of the duration: from a minute, seconds alone go round from 59; from an hour,
// minutes alone go up to 59 (a minute or an hour exactly is told so).
// CONTROL: what reads all of it compiles quietly below (remaining is all of it in milliseconds; under a
// minute seconds alone are; minutes alone are the author's choice). With countdownTime finding nothing
// the structure rows failed, and with its rule of the parts removed the parts rows compiled and failed
// (run 19/10/2026 on overlaid copies of countdown.go).
func TestCountdownInsideWrittenWrong(t *testing.T) {
	const empty = `countdown rỗng: vẽ thời gian còn lại bên trong, <countdown duration="60s"><time>{{ remaining / 1000 }}</time></countdown>`
	const noTime = `countdown cần một <time> chứa thời gian còn lại, engine viết role="timer" lên nó: <time>{{ minutes }}:{{ String(seconds).padStart(2, '0') }}</time>`
	const readsNone = `<time> của countdown không đọc thời gian còn lại: đọc remaining, hours, minutes hay seconds bên trong`
	for _, c := range []struct{ name, source, needle, says string }{
		{"empty", `<content><countdown duration="5m"` + countdownStarted + ` /></content>`, `<countdown`, empty},
		{"only spaces", `<content><countdown duration="5m"` + countdownStarted + `>   </countdown></content>`, `<countdown`, empty},
		{"empty, no duration", `<content><countdown /></content>`, `<countdown`, empty},
		{"no time", `<content>` + countdownOf(` duration="5m"`+countdownStarted, `<text>{{ minutes }}:{{ seconds }}</text>`) + `</content>`, `<countdown`, noTime},
		{"only a time of a moment", `<content>` + countdownOf(` duration="5m"`+countdownStarted, `<text>{{ minutes }}</text><time value="2026-10-19">19/10</time>`) + `</content>`, `<countdown`, noTime},
		{"a time inside a component in it", `<content>` + countdownOf(` duration="5m"`+countdownStarted, `<dropdown><button click="toggle()">{{ open }}</button><time>{{ minutes }}</time></dropdown>`) + `</content>`, `<countdown`, noTime},
		{"two times", `<content>` + countdownOf(` duration="5m"`+countdownStarted, `<time>{{ minutes }}</time><text>:<time>{{ seconds }}</time></text>`) + `</content>`, `<time>{{ seconds`,
			"countdown có một thời gian còn lại: <time> ở content.ui:1:%d đã là của nó; gộp vào một <time>"},
		{"a time of words", `<content>` + countdownOf(` duration="5m"`+countdownStarted, `<text>Còn <time>năm phút</time></text>`) + `</content>`, `<time>`, readsNone},
		{"a time of playing", `<content>` + countdownOf(` duration="5m"`+countdownStarted, `<time>{{ playing ? 'chạy' : 'dừng' }}</time>`) + `</content>`, `<time>`, readsNone},
		{"a time of the duration", `<content>` + countdownOf(` duration="5m"`+countdownStarted, `<time>{{ duration / 1000 }}</time>`) + `</content>`, `<time>`, readsNone},

		{"5m by seconds alone", `<content>` + countdownOf(` duration="5m"`+countdownStarted, `<time>{{ seconds }}</time>`) + `</content>`, `<time>`,
			`duration="5m" dài hơn một phút mà <time> chỉ đọc seconds: seconds đếm từ 59 về 0 rồi quay lại; đọc thêm minutes`},
		{"a minute by seconds alone", `<content>` + countdownOf(` duration="60s"`+countdownStarted, `<text>Còn <time>{{ seconds }} giây</time></text>`) + `</content>`, `<time>`,
			`duration="60s" dài đúng một phút mà <time> chỉ đọc seconds: seconds đếm từ 59 về 0 rồi quay lại; đọc thêm minutes`},
		{"2h by hours and seconds", `<content>` + countdownOf(` duration="2h"`+countdownStarted, `<time>{{ hours }}:{{ seconds }}</time>`) + `</content>`, `<time>`,
			`duration="2h" dài hơn một phút mà <time> đọc hours và seconds mà không đọc minutes: seconds đếm từ 59 về 0 rồi quay lại; đọc thêm minutes`},
		{"2h by seconds alone", `<content>` + countdownOf(` duration="2h"`+countdownStarted, `<time>{{ seconds }}</time>`) + `</content>`, `<time>`,
			`duration="2h" dài hơn một phút mà <time> chỉ đọc seconds: seconds đếm từ 59 về 0 rồi quay lại; đọc thêm minutes`},
		{"90m without hours", `<content>` + countdownOf(` duration="90m"`+countdownStarted, countdownClock) + `</content>`, `<time>`,
			`duration="90m" dài hơn một giờ mà <time> không đọc hours: minutes chỉ tới 59; đọc thêm hours`},
		{"an hour without hours", `<content>` + countdownOf(` duration="1h"`+countdownStarted, countdownClock) + `</content>`, `<time>`,
			`duration="1h" dài đúng một giờ mà <time> không đọc hours: minutes chỉ tới 59; đọc thêm hours`},
		{"24h by minutes alone", `<content>` + countdownOf(` duration="24h"`+countdownStarted, `<time>{{ minutes }}</time>`) + `</content>`, `<time>`,
			`duration="24h" dài hơn một giờ mà <time> không đọc hours: minutes chỉ tới 59; đọc thêm hours`},
	} {
		t.Run(c.name, func(t *testing.T) {
			says := c.says
			if strings.Contains(says, "%d") {
				first := strings.Index(c.source, `<time>`)
				says = strings.Replace(says, "%d", itoa(len([]rune(c.source[:first]))+1), 1)
			}
			mustFailExactly(t, countdownSite(c.source), where("content.ui", c.source, c.needle)+says)
		})
	}

	// CONTROL: what holds all of the time left
	for _, c := range []struct{ duration, time string }{
		{"5m", `<time>{{ remaining / 1000 }}</time>`},
		{"90m", `<time>{{ Math.ceil(remaining / 60000) }} phút</time>`},
		{"59s", `<time>{{ seconds }}</time>`},
		{"59m", countdownClock},
		{"90m", `<time>{{ hours }}:{{ minutes }}:{{ seconds }}</time>`},
		{"24h", `<time>{{ hours }} giờ {{ minutes }} phút</time>`},
		{"5m", `<time>{{ minutes }} phút</time>`},
		{"5m", `<text>Còn <time><text weight="bold">{{ minutes }}</text><text>{{ String(seconds).padStart(2, '0') }}</text></time></text>`},
	} {
		countdownQuiet(t, `<content>`+countdownOf(` duration="`+c.duration+`"`+countdownStarted, c.time)+`</content>`)
	}
}

// finish is the countdown's event: written anywhere but on its tag it would wait for what is never sent
// there, so it is an error that names the tag to write, open (a countdown holds the time it draws; the
// network's empty tag stays " />"): on a box, a text, a button, a link, the tag of another of Kitwork's
// components (the carousel, the network, a dropdown), an element inside the network or inside the
// countdown itself, with its modifiers, listed with click.
// CONTROL: the countdown's own tag hears it (TestCountdownHostAndItsTime), and offline off the network
// still names <network offline="…" />. With finish taken out of hydrate.ComponentEvents every row
// compiled and this test failed; with the open shape not chosen, every row ended " />" and failed (run
// 19/10/2026 on overlaid copies of jit/hydrate/grouped_check.go and host.go).
func TestCountdownFinishIsHeardOnItsTagOnly(t *testing.T) {
	const says = `finish là sự kiện của component countdown, chỉ thẻ của nó nghe được: <countdown finish="…">`
	const told = `$toast.info('Hết giờ')`
	for _, c := range []struct{ name, content, needle, says string }{
		{"a box", `<box finish="` + told + `"><text>a</text></box>`, `finish=`, says},
		{"a text", `<text finish="` + told + `">a</text>`, `finish=`, says},
		{"a button", `<button finish="` + told + `">a</button>`, `finish=`, says},
		{"a link", `<link value="/a" finish="` + told + `">a</link>`, `finish=`, says},
		{"a carousel", `<carousel finish="` + told + `"><list><item><text>a</text></item></list></carousel>`, `finish=`, says},
		{"a network", `<network finish="` + told + `" />`, `finish=`, says},
		{"a dropdown", `<dropdown finish="close()"><button click="toggle()">a</button></dropdown>`, `finish=`, says},
		{"inside a network", `<network><box finish="` + told + `"><text>a</text></box></network>`, `finish=`, says},
		{"inside the countdown", countdownOf(` duration="5m"`+countdownStarted, countdownClock+`<button finish="stop()">Dừng</button>`), `finish="stop`, says},
		{"with its modifier", `<box finish:once="` + told + `"><text>a</text></box>`, `finish:once=`,
			`finish là sự kiện của component countdown, chỉ thẻ của nó nghe được: <countdown finish:once="…">`},
		{"listed with click", `<button click,finish="` + told + `">a</button>`, `click,finish=`, says},
		// CONTROL: the network's events keep their empty tag
		{"offline off the network", `<box offline="` + told + `"><text>a</text></box>`, `offline=`,
			`offline là sự kiện của component network, chỉ thẻ của nó nghe được: <network offline="…" />`},
	} {
		t.Run(c.name, func(t *testing.T) {
			source := `<content>` + c.content + `</content>`
			mustFailExactly(t, countdownSite(source), where("content.ui", source, c.needle)+c.says)
		})
	}
}

// A call of what a countdown does not have is an error where it is written, through its alias anywhere
// on the page and as a bare name inside its host: a method misspelt (told the nearest), the words of
// other countdowns (resume, start and continue are play(); reset and restart are stop() then play();
// cancel is stop()), finish (its event, not a method), its state called; and through its alias a name
// it does not hold (left and time are remaining and its parts; another told the nearest).
// CONTROL: its methods and its state, read and called right, compile (the page below); a bare name
// close to nothing of it is the kernel's error, not the countdown's. With checkCountdowns not reading
// the alias's calls the call rows compiled, and not reading the names read off it the read rows (left,
// time, finish read, minute) compiled; with countdownCalls reading nothing the bare rows were told the
// kernel's error instead; each failed this test (run 19/10/2026 on overlaid copies of countdown.go).
func TestCountdownCallsOfWhatItDoesNotHave(t *testing.T) {
	const methods = "countdown có: play, pause, toggle, stop"
	const resume = "chạy tiếp là play() (sau pause() chạy tiếp; sau stop() hay finish chạy lại từ đầu)"
	const leftIs = ": thời gian còn lại là remaining (mili giây), hay hours, minutes, seconds"
	host := countdownOf(` as="$tea" duration="5m"`+countdownStarted, countdownClock)
	for _, c := range []struct{ name, outside, needle, says string }{
		{"a method misspelt", `<button click="$tea.paus()">a</button>`, `click=`, "countdown không có paus; " + methods + "; gần nhất: pause"},
		{"toggle misspelt", `<button click="$tea.toggel()">a</button>`, `click=`, "countdown không có toggel; " + methods + "; gần nhất: toggle"},
		{"no near method", `<button click="$tea.save()">a</button>`, `click=`, "countdown không có save; " + methods},
		{"resume", `<button click="$tea.resume()">a</button>`, `click=`, "countdown không có resume: " + resume},
		{"start", `<button click="$tea.start()">a</button>`, `click=`, "countdown không có start: " + resume},
		{"continue", `<button click="$tea.continue()">a</button>`, `click=`, "countdown không có continue: " + resume},
		{"reset", `<button click="$tea.reset()">a</button>`, `click=`, "countdown không có reset: stop() đưa thời gian về đầu, play() chạy lại"},
		{"restart", `<button click="$tea.restart()">a</button>`, `click=`, "countdown không có restart: stop() đưa thời gian về đầu, play() chạy lại"},
		{"cancel", `<button click="$tea.cancel()">a</button>`, `click=`, "countdown không có cancel: dừng và về đầu là stop()"},
		{"finish called", `<button click="$tea.finish()">a</button>`, `click=`, countdownFinishCalled},
		{"its state called", `<button click="$tea.minutes()">a</button>`, `click=`, "minutes là trạng thái của countdown, không gọi được: đọc $tea.minutes; " + methods},
		{"left", `<text>{{ $tea.left }}</text>`, `{{ $tea.left`, "countdown không có left" + leftIs},
		{"time", `<text show="$tea.time > 0">a</text>`, `show=`, "countdown không có time" + leftIs},
		{"left called", `<button click="$tea.left()">a</button>`, `click=`, "countdown không có left" + leftIs},
		{"finish read", `<text show="$tea.finish">a</text>`, `show=`, countdownFinishCalled},
		{"a name it does not hold", `<text>{{ $tea.minute }}</text>`, `{{ $tea.minute`,
			"countdown không có minute; trạng thái của countdown là playing, remaining, hours, minutes, seconds, duration; gần nhất: minutes"},
	} {
		t.Run(c.name, func(t *testing.T) {
			source := `<content>` + host + c.outside + `</content>`
			mustFailExactly(t, countdownSite(source), where("content.ui", source, c.needle)+c.says)
		})
	}

	// bare names inside the host: the same words, at the event; the host's own ready too
	for _, c := range []struct{ name, words, inside, needle, says string }{
		{"a method misspelt", ``, `<button click="paus()">a</button>`, `click="paus`, "countdown không có paus; " + methods + "; gần nhất: pause"},
		{"stop misspelt in a key", ``, `<box keydown:escape="stpo()"><text>a</text></box>`, `keydown:escape=`, "countdown không có stpo; " + methods + "; gần nhất: stop"},
		{"resume", ``, `<button click="resume()">a</button>`, `click="resume`, "countdown không có resume: " + resume},
		{"start on ready", ` ready="start()"`, ``, `ready="start`, "countdown không có start: " + resume},
		{"reset", ``, `<button click="reset()">a</button>`, `click="reset`, "countdown không có reset: stop() đưa thời gian về đầu, play() chạy lại"},
		{"cancel", ``, `<button click="cancel()">a</button>`, `click="cancel`, "countdown không có cancel: dừng và về đầu là stop()"},
		{"finish called", ``, `<button click="finish()">a</button>`, `click="finish`, countdownFinishCalled},
		{"finish called on finish", ` finish="finish()"`, ``, `finish="finish`, countdownFinishCalled},
		{"its state called", ``, `<button click="seconds()">a</button>`, `click="seconds`, "seconds là trạng thái của countdown, không gọi được: đọc seconds; " + methods},
		{"left called", ``, `<button click="left()">a</button>`, `click="left`, "countdown không có left" + leftIs},
	} {
		t.Run("inside: "+c.name, func(t *testing.T) {
			words := ` duration="5m"` + c.words
			if !strings.Contains(c.words, "ready=") {
				words += ` ready="play()"`
			}
			if !strings.Contains(c.words, "finish=") {
				words += ` finish="$toast.info('Hết giờ')"`
			}
			source := `<content>` + countdownOf(words, countdownClock+c.inside) + `</content>`
			mustFailExactly(t, countdownSite(source), where("content.ui", source, c.needle)+c.says)
		})
	}

	// CONTROL: what it does and holds, called and read through its alias and inside its host
	html := countdownQuiet(t, `<content>`+countdownOf(` as="$tea" duration="5m"`+countdownStarted,
		countdownClock+`<button click="play()">a</button><button click="pause()">b</button><button click="toggle()">c</button><button click="stop()">d</button>`)+
		`<button click="$tea.play()">e</button><button click="$tea.pause()">f</button><button click="$tea.toggle()">g</button><button click="$tea.stop()">h</button>`+
		`<text show="$tea.playing">{{ $tea.remaining }} {{ $tea.hours }} {{ $tea.minutes }} {{ $tea.seconds }} {{ $tea.duration }} {{ $tea.minutes.toString() }}</text></content>`)
	mustContain(t, html, `>300000 0 5 0 300000 5</p>`)
	// a bare name near nothing of the countdown is the kernel's to tell: only the countdown's run here
	mustFail(t, countdownSite(`<content>`+countdownOf(` duration="5m"`+countdownStarted, countdownClock+`<button click="save()">a</button>`)+`</content>`),
		"content.ui:1:", "bên trong countdown sự kiện click chỉ gọi tên của component")
}

// What works and is probably not meant is a warning at the tag, and the page renders: no finish (a
// screen reader is not told the time is up: a timer's role reads no count), and nothing that ever starts
// it (no ready="play()", no event in it and no call through its alias of play() or toggle()). The host's
// own finish="play()" runs only at the end of a run, and pause() starts nothing: neither starts it, nor
// does a button that sets its time (minutes = 3, 20/10/2026). Both on one host are each told once, in
// that order.
// CONTROL: each way that starts it, and finish, say nothing. With the finish warning removed its rows
// failed; with the start never noted (scope.starts left false) the CONTROL rows warned and failed (run
// 19/10/2026 on overlaid copies of countdown.go); with a write of a part noted as a start, the row of
// minutes = 3 was quiet and failed (run 20/10/2026).
func TestCountdownWarnings(t *testing.T) {
	const noFinish = `countdown không có finish: hết giờ thì người dùng trình đọc màn hình không biết (role="timer" không đọc số đếm); viết finish="$toast.info('Hết giờ')" hay finish="$reader.say('Hết giờ')"`
	const never = `countdown không bao giờ chạy: không có ready="play()", không nút nào gọi play() hay toggle(); viết ready="play()" để chạy khi trang mở`
	for _, c := range []struct {
		name, content string
		says          []string
	}{
		{"no finish", countdownOf(` duration="5m" ready="play()"`, countdownClock), []string{noFinish}},
		{"nothing starts it", countdownOf(` duration="5m" finish="$toast.info('Hết giờ')"`, countdownClock+`<button click="stop()">Dừng</button>`), []string{never}},
		{"finish does not start it", countdownOf(` duration="5m" finish="play()"`, countdownClock), []string{never}},
		{"pause through the alias does not", countdownOf(` as="$tea" duration="5m" finish="$toast.info('Hết giờ')"`, countdownClock) + `<button click="$tea.pause()">a</button>`, []string{never}},
		{"setting its time does not", countdownOf(` duration="5m" finish="$toast.info('Hết giờ')"`, countdownClock+`<button click="minutes = 3">3 phút</button>`), []string{never}},
		{"both", `<box>` + countdownOf(` duration="5m"`, countdownClock) + `</box>`, []string{noFinish, never}},
	} {
		t.Run(c.name, func(t *testing.T) {
			source := `<content>` + c.content + `</content>`
			html, warned := countdownPage(t, source)
			var want []string
			for _, says := range c.says {
				want = append(want, where("content.ui", source, `<countdown`)+says)
			}
			if got := strings.Join(warned, "\n"); got != strings.Join(want, "\n") {
				t.Errorf("warnings:\n got  %s\n want %s", got, strings.Join(want, "\n"))
			}
			mustContain(t, html, `data-kitwork-component="countdown`, `>5:00</time>`)
		})
	}

	// CONTROL: each way that starts it
	const finish = ` finish="$toast.info('Hết giờ')"`
	for _, c := range []string{
		countdownOf(` duration="5m" ready="play()"`+finish, countdownClock),
		countdownOf(` duration="5m" ready="toggle()"`+finish, countdownClock),
		countdownOf(` duration="5m"`+finish, countdownClock+`<button click="play()">Chạy</button>`),
		countdownOf(` duration="5m"`+finish, countdownClock+`<button click="toggle()">{{ playing ? 'Dừng' : 'Chạy' }}</button>`),
		countdownOf(` duration="5m"`+finish, countdownClock+`<box keydown:enter="play()"><text>a</text></box>`),
		countdownOf(` as="$tea" duration="5m"`+finish, countdownClock) + `<button click="$tea.play()">Chạy</button>`,
		`<button click="$tea.toggle()">Chạy</button>` + countdownOf(` as="$tea" duration="5m"`+finish, countdownClock),
	} {
		countdownQuiet(t, `<content>`+c+`</content>`)
	}
}

// The countdown's time keeps one width for its digits: the reboot's rule for countdown-time, an element
// word, so the page's own stylesheet writes no rule for it.
// CONTROL: with the rule taken out of reboot.css this test and TestRebootStylesEveryElementWord failed
// (run 19/10/2026 on an overlaid copy of reboot.css).
func TestCountdownTimeHasTabularDigits(t *testing.T) {
	if css := string(stylesheet.Reboot().CSS); !strings.Contains(css, ":where(.countdown-time){font-variant-numeric:tabular-nums}") {
		t.Error("reboot.css gives the countdown's time no tabular digits")
	}
	if !containsName(stylesheet.ElementWords(), "countdown-time") {
		t.Error("countdown-time is no element word: the page's stylesheet would look for a rule of its own")
	}
	s := memorySite("", countdownSite(`<content>`+countdownTea+`</content>`))
	s.Reload = true
	html, css := renderWithCSS(t, s, ".", "/", nil)
	mustContain(t, html, `<time class="countdown-time"`)
	if strings.Contains(css, "countdown-time") {
		t.Errorf("the page's stylesheet writes a rule for countdown-time:\n%s", css)
	}
}

// countdownFiles compiles a site of several files that must compile, the toast of countdownSite in its
// overlay, and returns the page rendered at "/" with no data, and the warnings, one line each.
func countdownFiles(t *testing.T, files map[string]string) (string, []string) {
	t.Helper()
	all := countdownSite("")
	for name, source := range files {
		all[name] = source
	}
	s := memorySite("", all)
	s.Reload = true
	page, warned := compileDiagnose(t, s)
	return page.Render(Request{Path: "/"}), warned
}

// countdownPanelLogic is a site component's logic: a name of its own, none of the countdown's.
const countdownPanelLogic = `import { component } from "kitwork"; component.define("tea-panel", () => { var open = false; });`

// The time left is one on the page, so it is one element: a for on the <time>, or on what holds it in
// the host, at any depth, would write a timer for every row (two role="timer", a datetime each), and is
// an error at that for.
// CONTROL: a for in the host that holds no time left (a row of buttons), and one that repeats a <time
// value> (an ordinary time), compile quietly beside the time left. With the for check taken out of
// countdownTime every error row compiled, its page holding role="timer" twice, and this test failed (run
// 20/10/2026 on an overlaid copy of countdown.go).
func TestCountdownTimeLeftIsNeverRepeated(t *testing.T) {
	const onIt = `countdown có một thời gian còn lại: <time> của nó không lặp bằng for; bỏ for`
	const around = `countdown có một thời gian còn lại: for này lặp <time> ở content.ui:1:%d của nó; đặt <time> ngoài for`
	for _, c := range []struct{ name, inside, says string }{
		{"a for on the time", `<time for="n of [1, 2]">{{ minutes }}:{{ seconds }}</time>`, onIt},
		{"a for on the time in a text", `<text>Còn <time for="n of [1, 2]">{{ minutes }}:{{ seconds }}</time></text>`, onIt},
		{"a for on a box around it", `<box for="n of [1, 2]">` + countdownClock + `</box>`, around},
		{"a for two levels out", `<box for="n of [1, 2]"><text>Còn ` + countdownClock + `</text></box>`, around},
	} {
		t.Run(c.name, func(t *testing.T) {
			source := `<content>` + countdownOf(` duration="5m"`+countdownStarted, c.inside) + `</content>`
			says := c.says
			if strings.Contains(says, "%d") {
				says = strings.Replace(says, "%d", itoa(len([]rune(source[:strings.Index(source, "<time")]))+1), 1)
			}
			mustFailExactly(t, countdownSite(source), where("content.ui", source, `for=`)+says)
		})
	}

	// CONTROL: what a for repeats there is no time left
	html := countdownQuiet(t, `<content>`+countdownOf(` duration="5m"`+countdownStarted,
		countdownClock+`<box for="n of [1, 2]"><button click="stop()">{{ n }}</button><time value="2026-10-19">19/10</time></box>`)+`</content>`)
	if n := strings.Count(html, `role="timer"`); n != 1 {
		t.Errorf(`role="timer" written %d times, want once:%s`, n, html)
	}
}

// A component's .ui holding a countdown that a call through its alias starts, the component used twice:
// each instance's countdown is started by the call its own .ui holds, though the page keeps that
// expression once, where it is written, with the first instance's aliases (noteAliasExpr). Neither is
// told it never runs. Started by nothing, it is told once, at its tag in the component's file.
// CONTROL: with countdownStarts marking only the countdown the alias found, the second instance was told
// it never runs and this test failed (run 20/10/2026 on an overlaid copy of countdown.go).
func TestCountdownStartedInEveryInstanceOfAComponent(t *testing.T) {
	const never = `countdown không bao giờ chạy: không có ready="play()", không nút nào gọi play() hay toggle(); viết ready="play()" để chạy khi trang mở`
	panel := func(button string) string {
		return `<tea-panel><countdown as="$tea" duration="5m" finish="$toast.info('Hết giờ')">` + countdownClock + `</countdown>` + button + `</tea-panel>`
	}
	for _, calls := range []string{`<tea-panel />`, `<tea-panel /><tea-panel />`} {
		html, warned := countdownFiles(t, map[string]string{
			"/site/tea-panel.work": countdownPanelLogic,
			"/site/tea-panel.ui":   panel(`<button click="$tea.play()">Chạy</button>`),
			"/site/content.ui":     `<content>` + calls + `</content>`,
		})
		if len(warned) > 0 {
			t.Errorf("%s: want no warning, got:\n%s", calls, strings.Join(warned, "\n"))
		}
		if got, want := strings.Count(html, `data-kitwork-event="click: $tea.play()"`), strings.Count(calls, "<tea-panel"); got != want {
			t.Errorf("%s: %d buttons start their countdown, want %d", calls, got, want)
		}
	}

	// CONTROL: pause() starts nothing, in either instance
	source := panel(`<button click="$tea.pause()">Tạm dừng</button>`)
	_, warned := countdownFiles(t, map[string]string{
		"/site/tea-panel.work": countdownPanelLogic,
		"/site/tea-panel.ui":   source,
		"/site/content.ui":     `<content><tea-panel /><tea-panel /></content>`,
	})
	if got, want := strings.Join(warned, "\n"), where("tea-panel.ui", source, `<countdown`)+never; got != want {
		t.Errorf("warnings:\n got  %s\n want %s", got, want)
	}
}

// What starts a countdown is found where the kernel finds its names: a play() or toggle() in a layout
// called inside the host, in a component nested there that declares no such name (the popover of a
// dropdown, a site component without a UI), in an event that lists ready with finish (ready,finish, in
// either order). What does not start it is still told it never runs: a toggle() that a nested dropdown
// declares is the dropdown's; the host's own finish, written finish:once or through its alias
// (finish="$tea.play()"), runs only once a run has ended.
// CONTROL: the quiet rows each warned, and this test failed, with countdownEvents reading only an element
// in the host's own file whose nearest component is the countdown (how the bare calls were read before,
// 19/10/2026); with countdownFinishOnly true of any list that holds finish, the two lists warned; with
// checkCountdowns counting the host's own finish="$tea.play()", that row was quiet (run 20/10/2026 on
// overlaid copies of countdown.go).
func TestCountdownStartedFromWhatIsNestedInIt(t *testing.T) {
	const never = `countdown không bao giờ chạy: không có ready="play()", không nút nào gọi play() hay toggle(); viết ready="play()" để chạy khi trang mở`
	const finish = ` finish="$toast.info('Hết giờ')"`
	for _, c := range []struct {
		name  string
		files map[string]string
		warns bool
	}{
		{"a popover's button in a dropdown", map[string]string{"/site/content.ui": `<content>` + countdownOf(` duration="5m"`+finish,
			countdownClock+`<dropdown><button click="open = !open">Menu</button><popover><button click="play()">Chạy</button></popover></dropdown>`) + `</content>`}, false},
		{"a layout's button", map[string]string{
			"/site/tea-buttons.ui": `<box><button click="play()">Chạy</button></box>`,
			"/site/content.ui":     `<content>` + countdownOf(` duration="5m"`+finish, countdownClock+`<tea-buttons />`) + `</content>`}, false},
		{"a site component without a UI", map[string]string{
			"/site/tea-panel.work": countdownPanelLogic,
			"/site/content.ui":     `<content>` + countdownOf(` duration="5m"`+finish, countdownClock+`<tea-panel><button click="toggle()">Chạy</button></tea-panel>`) + `</content>`}, false},
		{"ready listed with finish", map[string]string{"/site/content.ui": `<content>` + countdownOf(` duration="5m" ready,finish="play()"`, countdownClock) + `</content>`}, false},
		{"finish listed with ready", map[string]string{"/site/content.ui": `<content>` + countdownOf(` duration="5m" finish,ready="play()"`, countdownClock) + `</content>`}, false},

		{"the dropdown's own toggle", map[string]string{"/site/content.ui": `<content>` + countdownOf(` duration="5m"`+finish,
			countdownClock+`<dropdown><button click="toggle()">Menu</button><popover><text>a</text></popover></dropdown>`) + `</content>`}, true},
		{"its own finish:once", map[string]string{"/site/content.ui": `<content>` + countdownOf(` duration="5m" finish:once="play()"`, countdownClock) + `</content>`}, true},
		{"its own finish through its alias", map[string]string{"/site/content.ui": `<content>` + countdownOf(` as="$tea" duration="5m" finish="$tea.play()"`, countdownClock) + `</content>`}, true},
		{"its own finish toggling through its alias", map[string]string{"/site/content.ui": `<content>` + countdownOf(` as="$tea" duration="5m" finish="$tea.toggle()"`, countdownClock) + `</content>`}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, warned := countdownFiles(t, c.files)
			var want []string
			if content := c.files["/site/content.ui"]; strings.Contains(content, `<dropdown><button click="toggle()">`) {
				// the dropdown's toggle is the nearest, and the countdown around declares one too
				want = append(want, where("content.ui", content, `click="toggle`)+"toggle ở đây là của dropdown, mà countdown bao ngoài cũng khai toggle: một tên một nghĩa — gọi qua alias ($tên.toggle) hoặc đổi tên")
			}
			if c.warns {
				want = append(want, where("content.ui", c.files["/site/content.ui"], `<countdown`)+never)
			}
			if got := strings.Join(warned, "\n"); got != strings.Join(want, "\n") {
				t.Errorf("warnings:\n got  %s\n want %s", got, strings.Join(want, "\n"))
			}
		})
	}
}

// A name of the countdown called where it reaches is told the countdown's words there too: in a layout
// called inside the host, paus() is told the nearest method, as in the host itself.
// CONTROL: a bare name close to nothing of it is still the kernel's to tell in the layout. With
// countdownEvents reading only the host's own file, paus() in the layout was told the kernel's words,
// and this test failed (run 20/10/2026 on an overlaid copy of countdown.go).
func TestCountdownCallsInALayoutInsideIt(t *testing.T) {
	buttons := `<box><button click="play()">Chạy</button><button click="paus()">Tạm dừng</button></box>`
	files := countdownSite(`<content>` + countdownOf(` duration="5m"`+countdownStarted, countdownClock+`<tea-buttons />`) + `</content>`)
	files["/site/tea-buttons.ui"] = buttons
	mustFailExactly(t, files, where("tea-buttons.ui", buttons, `click="paus`)+"countdown không có paus; countdown có: play, pause, toggle, stop; gần nhất: pause")

	// CONTROL
	files["/site/tea-buttons.ui"] = `<box><button click="save()">Lưu</button></box>`
	mustFail(t, files, "tea-buttons.ui:1:", "bên trong countdown sự kiện click chỉ gọi tên của component")
}

// Markup writes the countdown's time through its parts and nothing else of it (decisions of 20/10/2026):
// hours, minutes and seconds are written like a var (an assignment, ++ or --) on its host, inside it, in
// a component nested there that does not declare the name; what the countdown keeps is an error at the
// event, the clock and the screen would part. duration written, stop() would put that on the screen
// while the server painted the one written; remaining written, the clock would not know it; playing
// written true, toggle() pauses a run that never started, and nothing plays it again. duration and
// remaining are told to write the parts, playing what to call; a method written is told to be called.
// CONTROL: the parts written, on the host's ready, in a button, a key, a popover of a dropdown, by ++
// and --, compile with no warning and reach the kernel as written; a name that a component nested in it
// declares is that component's to write (the dropdown's open), and reading the countdown's state
// compiles. With countdownTarget reading no write, every error row compiled and this test failed; with
// the parts not let through (as on 19/10/2026: every write refused), the CONTROL page failed at its
// ready="minutes = 10" ("minutes của countdown chỉ đọc…") (run 20/10/2026 on overlaid copies of
// countdown.go).
func TestCountdownWritesOnlyItsParts(t *testing.T) {
	const kept = " của countdown chỉ đọc, countdown tự tính: chạy là play(), tạm dừng là pause(), về đầu là stop()"
	const parts = " của countdown chỉ đọc, countdown tự tính: đổi thời gian là ghi hours, minutes, seconds (minutes = 3)"
	for _, c := range []struct{ name, words, inside, needle, says string }{
		{"duration", ``, `<button click="duration = 1000">a</button>`, `click=`, `duration của countdown chỉ đọc: thời gian lúc trang mở là duration="5m" trên <countdown>; đổi thời gian là ghi hours, minutes, seconds (minutes = 3)`},
		{"playing", ``, `<button click="playing = true">a</button>`, `click=`, "playing" + kept},
		{"playing turned over", ``, `<button click="playing = !playing">a</button>`, `click=`, "playing" + kept},
		{"remaining", ``, `<button click="remaining = 0">a</button>`, `click=`, "remaining" + parts},
		{"remaining counted down", ``, `<button click="remaining--">a</button>`, `click=`, "remaining" + parts},
		{"on its own ready", ` ready="playing = true"`, ``, `ready=`, "playing" + kept},
		{"in a popover of a dropdown", ``, `<dropdown><button click="toggle()">Menu</button><popover><button click="remaining = 0">a</button></popover></dropdown>`, `click="remaining`, "remaining" + parts},
		{"a method", ``, `<button click="play = 1">a</button>`, `click=`, "play là hàm của countdown, không gán được: gọi nó, play()"},
	} {
		t.Run(c.name, func(t *testing.T) {
			words := ` duration="5m"` + c.words
			if c.words == "" {
				words += ` ready="play()"`
			}
			source := `<content>` + countdownOf(words+` finish="$toast.info('Hết giờ')"`, countdownClock+c.inside) + `</content>`
			mustFailExactly(t, countdownSite(source), where("content.ui", source, c.needle)+c.says)
		})
	}

	// CONTROL
	countdownQuiet(t, `<content>`+countdownOf(` duration="5m"`+countdownStarted, countdownClock+
		`<dropdown><button click="open = !open">Menu</button><popover><text>a</text></popover></dropdown>`+
		`<button click="$toast.info(String(remaining / 1000) + ' giây ' + (playing ? 'chạy' : 'dừng'))">Còn bao lâu</button>`)+`</content>`)
	html := countdownQuiet(t, `<content>`+countdownOf(` duration="5m" ready="minutes = 10" finish="$toast.info('Hết giờ')"`, countdownClock+
		`<button click="toggle()">Chạy</button>`+
		`<button click="seconds = 3">a</button><button click="seconds++">b</button><button click="--minutes">c</button>`+
		`<box keydown:enter="hours = 0"><text>d</text></box>`+ // the clock reads no hours: an hour written is told (TestCountdownPartWrittenLongerThanItsTimeShows)
		`<dropdown><button click="open = !open">Menu</button><popover><button click="minutes = minutes + 5">e</button></popover></dropdown>`)+`</content>`)
	mustContain(t, html, `data-kitwork-event="ready: minutes = 10; finish: $toast.info(&#39;Hết giờ&#39;)"`,
		`data-kitwork-event="click: seconds = 3"`, `data-kitwork-event="click: seconds++"`, `data-kitwork-event="click: --minutes"`,
		`data-kitwork-event="keydown.enter: hours = 0"`, `data-kitwork-event="click: minutes = minutes + 5"`)
}

// What a duration written another way is told to write is one the countdown takes: whole seconds,
// rounded up (5ms written 5MS is 1s, not 5ms), and a time no spelling makes right is told what is wrong
// with it, not its spelling: nothing (0phút, 0m0s) is the error of zero, longer than 24 hours (PT25H,
// P2D, 25 h, 24h 1s, a number without its unit read as seconds, digits too many to hold) the error of
// that. Every duration a message names besides the one written compiles.
// CONTROL: with the spelling messages suggesting the time as written (d.suggestion) and the range not
// read before them, as on 19/10/2026, the rows failed, among them 5MS told "5ms" and P2D told "48h", and
// so did the check that each suggestion compiles (run 20/10/2026 on an overlaid copy of countdown.go).
func TestCountdownDurationSuggestsWhatItTakes(t *testing.T) {
	const longer = ` dài hơn 24 giờ: countdown đếm từ lúc trang mở và đếm lại khi tải lại trang; đếm tới một thời điểm (until) chưa có`
	const zero = `: countdown cần ít nhất một giây để đếm, như duration="30s"`
	nines := strings.Repeat("9", 400)
	for _, c := range []struct{ duration, says string }{
		{"5MS", `duration="5MS": mili giây viết ms, viết duration="1s"`},
		{"1500msec", `duration="1500msec": mili giây viết ms, viết duration="2s"`},
		{"PT0.5S", `duration="PT0.5S": giây viết s, viết duration="1s"`},
		{"5 ms", `duration="5 ms" có khoảng trắng trước đơn vị, .ui viết số liền đơn vị: viết duration="1s"`},
		{"0.5", `duration="0.5" thiếu đơn vị: viết duration="1s" (0.5 giây, countdown đếm từng giây)`},
		{"1.5", `duration="1.5" thiếu đơn vị: viết duration="2s" (1.5 giây, countdown đếm từng giây)`},
		{"0phút", `duration="0phút"` + zero},
		{"0m0s", `duration="0m0s"` + zero},
		{"PT0S", `duration="PT0S"` + zero},
		{"PT25H", `duration="PT25H"` + longer},
		{"P2D", `duration="P2D"` + longer},
		{"P" + nines + "D", `duration="P` + nines + `D"` + longer},
		{"25 h", `duration="25 h"` + longer},
		{"25giờ", `duration="25giờ"` + longer},
		{"24h 1s", `duration="24h 1s"` + longer},
		{"100000", `duration="100000" thiếu đơn vị, mà 100000 giây` + longer},
		// what was right stays as it was
		{"5phút", `duration="5phút": phút viết m, viết duration="5m"`},
		{"PT1M30S", `duration="PT1M30S": giây viết s, viết duration="90s"`},
		{"300", `duration="300" thiếu đơn vị: viết duration="300s" (300 giây) hay duration="5m"`},
		{"1s500ms", `duration="1s500ms" là hai đơn vị: viết một số với một đơn vị, duration="2s"`},
		{"1500ms", `duration="1500ms": countdown đếm từng giây, viết một số giây tròn như duration="2s"`},
	} {
		t.Run(c.duration, func(t *testing.T) {
			source := `<content>` + countdownOf(` duration="`+c.duration+`"`+countdownStarted, countdownClock) + `</content>`
			got := compileError(t, countdownSite(source))
			if want := where("content.ui", source, `duration=`) + c.says; got != want {
				t.Errorf("\n got  %s\n want %s", got, want)
			}
			for _, m := range regexp.MustCompile(`duration="([^"]*)"`).FindAllStringSubmatch(got, -1) {
				if m[1] != c.duration {
					countdownQuiet(t, `<content>`+countdownOf(` duration="`+m[1]+`"`+countdownStarted, `<time>{{ remaining / 1000 }}</time>`)+`</content>`)
				}
			}
		})
	}
}

// toggle, like play, pause and stop, is a method of the countdown: written as a word on its tag it is
// told to be called in an event, not the words of a site component's name the box also has.
// CONTROL: with toggle left out of the methods' entry of countdownStrangers, it was told "toggle là thuộc
// tính của <box> … đổi tên trong component", and this test failed (run 20/10/2026 on an overlaid copy of
// countdown.go).
func TestCountdownToggleOnItsTagIsAMethod(t *testing.T) {
	for _, word := range []string{"toggle", "play", "pause", "stop"} {
		source := `<content>` + countdownOf(` duration="5m" `+word+`="x"`+countdownStarted, countdownClock) + `</content>`
		mustFailExactly(t, countdownSite(source), where("content.ui", source, word+`="x"`)+
			word+` là hàm của countdown, không phải một chữ trên thẻ: gọi nó trong một sự kiện (ready="play()", click="pause()")`)
	}
}

// The time left may read the countdown's parts through its alias, as a countdown nested in another is
// told to (a name both declare is read $tên.minutes): it is the same countdown's, the host being where
// its alias is set, and the rule of the parts holds the same (5m read by $tea.seconds alone is told).
// A countdown nested in a countdown, written that way, builds with no warning. A countdown's time read
// through another's alias is not its time left.
// CONTROL: with countdownTime counting bare names only, the alias rows were told the <time> reads none
// of the time left, and this test failed (run 20/10/2026 on an overlaid copy of countdown.go).
func TestCountdownTimeReadThroughItsAlias(t *testing.T) {
	html := countdownQuiet(t, `<content>`+countdownOf(` as="$tea" duration="5m"`+countdownStarted, `<time>{{ $tea.minutes }}:{{ $tea.seconds }}</time>`)+`</content>`)
	mustContain(t, html, `datetime="PT0H5M0S">5:0</time>`)

	source := `<content>` + countdownOf(` as="$tea" duration="5m"`+countdownStarted, `<time>{{ $tea.seconds }}</time>`) + `</content>`
	mustFailExactly(t, countdownSite(source), where("content.ui", source, `<time>`)+
		`duration="5m" dài hơn một phút mà <time> chỉ đọc seconds: seconds đếm từ 59 về 0 rồi quay lại; đọc thêm minutes`)

	// a countdown inside a countdown, each read and called through its own alias
	html = countdownQuiet(t, `<content>`+countdownOf(` as="$quiz" duration="10m"`+countdownStarted, countdownClock+
		countdownOf(` as="$question" duration="30s" ready="$question.play()" finish="$toast.info('Hết giờ câu này')"`,
			`<time>{{ $question.seconds }}</time><button click="$question.toggle()">{{ $question.playing ? 'Dừng' : 'Chạy' }}</button>`))+`</content>`)
	if n := strings.Count(html, `role="timer"`); n != 2 {
		t.Errorf(`role="timer" written %d times, want twice (one each):%s`, n, html)
	}
	mustContain(t, html, `datetime="PT0H10M0S">10:00</time>`, `datetime="PT0H0M30S">30</time>`)

	// CONTROL: another countdown's alias
	source = `<content>` + countdownOf(` as="$other" duration="5m"`+countdownStarted, countdownClock) +
		countdownOf(` as="$tea" duration="5m"`+countdownStarted, `<time>{{ $other.minutes }}</time>`) + `</content>`
	mustFailExactly(t, countdownSite(source), where("content.ui", source, `<time>{{ $other`)+
		`<time> của countdown không đọc thời gian còn lại: đọc remaining, hours, minutes hay seconds bên trong`)
}

// countdownSetByFields is the countdown of the decision of 20/10/2026, afternoon: the time set in two
// fields outside the <time>, readonly while it plays, the time left shown while it plays, toggle(),
// stop(), and a button that sets three minutes. Fields outside the <time> are still built this way; the
// evening's form, the fields in the <time> as its segments, is countdownSegmentTea.
const countdownSetByFields = `<countdown as="$tea" duration="5m" finish="$toast.success('Trà đã sẵn sàng')" flex="row" gap="3">
  <input type="number" min="0" max="59" value="{{ minutes }}" readonly="{{ playing }}" alt="Phút" />
  <text>:</text>
  <input type="number" min="0" max="59" value="{{ seconds }}" readonly="{{ playing }}" alt="Giây" />
  <time show="playing">{{ minutes }}:{{ String(seconds).padStart(2, '0') }}</time>
  <button click="toggle()">{{ playing ? 'Tạm dừng' : 'Chạy' }}</button>
  <button click="stop()">Dừng</button>
  <button click="minutes = 3">3 phút</button>
</countdown>`

// The time set by fields, as the decision writes it, builds with no warning, exactly: each field is
// two-way on its part (value: minutes, the bare name the kernel writes back to) and readonly while it
// plays, painted with the part's start (5 and 0) and not readonly (not playing); the time left is shown
// only while it plays (hidden on the server's first paint), and the button that sets three minutes
// reaches the kernel as written. toggle() starts it: setting its time does not, and is not needed to.
// CONTROL: with the parts' writes refused as on 19/10/2026, this page failed at click="minutes = 3"
// ("minutes của countdown chỉ đọc…"); with the field warning given whatever readonly says, it warned at
// both fields (run 20/10/2026 on overlaid copies of countdown.go).
func TestCountdownTimeSetByFields(t *testing.T) {
	const button = `<button class="flex-row background-ink/6 hover:background-ink/10 pressed:background-ink/14 disabled:background-ink/4" type="button"`
	html := countdownQuiet(t, `<content>`+countdownSetByFields+`</content>`)
	mustContain(t, html, `<div class="flex-row gap-3" data-kitwork-component="countdown: $tea" data-kitwork-event="finish: $toast.success(&#39;Trà đã sẵn sàng&#39;)" data-kitwork-scope="duration: 300000">`+
		`<input aria-label="Phút" type="number" min="0" max="59" data-kitwork-bind="value: minutes; readonly: playing" value="5">`+
		`<p>:</p>`+
		`<input aria-label="Giây" type="number" min="0" max="59" data-kitwork-bind="value: seconds; readonly: playing" value="0">`+
		`<time class="countdown-time" role="timer" data-kitwork-bind="datetime: &#39;PT&#39; + hours + &#39;H&#39; + minutes + &#39;M&#39; + seconds + &#39;S&#39;; show: playing; text: [(minutes), &#39;:&#39;, (String(seconds).padStart(2, &#39;0&#39;))].join(&#39;&#39;)" datetime="PT0H5M0S" hidden>5:00</time>`+
		button+` data-kitwork-bind="text: playing ? &#39;Tạm dừng&#39; : &#39;Chạy&#39;" data-kitwork-event="click: toggle()">Chạy</button>`+
		button+` data-kitwork-event="click: stop()">Dừng</button>`+
		button+` data-kitwork-event="click: minutes = 3">3 phút</button></div>`)
}

// A field two-way on what the countdown keeps is an error at its value: remaining and duration are told
// to set the time through the parts, playing what to call; a field two-way only at times (readonly bound
// to a value) is two-way all the same. Each is told how to only show it: readonly="true", on a choice
// disabled="true" (a checkbox's readonly does not hold it).
// CONTROL: a field that only shows it (readonly="true", disabled="true") builds with no warning, its
// binding one way in effect (a readonly field sends no input); a value of words around the name is no
// field of it. With countdownField returning before its checks every error row compiled and this test
// failed, and so did TestCountdownFieldOnAPartWithoutReadonly (run 20/10/2026 on an overlaid copy of
// countdown.go).
func TestCountdownFieldOnWhatItKeeps(t *testing.T) {
	const parts = ` của countdown chỉ đọc: sửa thời gian bằng hours, minutes, seconds (value="{{ minutes }}"); chỉ để hiện thì thêm `
	const playing = `playing của countdown chỉ đọc: chạy là play(), tạm dừng là pause(), một nút đổi qua lại là click="toggle()"; chỉ để hiện thì thêm disabled="true"`
	for _, c := range []struct{ name, field, needle, says string }{
		{"remaining in a number field", `<input type="number" value="{{ remaining }}" alt="Còn" />`, `value=`, "remaining" + parts + `readonly="true"`},
		{"duration in a text field", `<input value="{{ duration }}" alt="Bao lâu" />`, `value=`, "duration" + parts + `readonly="true"`},
		{"remaining readonly at times", `<input type="number" value="{{ remaining }}" readonly="{{ !playing }}" alt="Còn" />`, `value=`, "remaining" + parts + `readonly="true"`},
		{"remaining readonly written false", `<input type="number" value="{{ remaining }}" readonly="false" alt="Còn" />`, `value=`, "remaining" + parts + `readonly="true"`},
		{"remaining in a checkbox", `<input type="checkbox" checked="{{ remaining }}" alt="Còn" />`, `checked=`, "remaining" + parts + `disabled="true"`},
		{"playing in a checkbox", `<input type="checkbox" checked="{{ playing }}" alt="Đang chạy" />`, `checked=`, playing},
		{"playing in a popover of a dropdown", `<dropdown><button click="open = !open">Menu</button><popover><input type="checkbox" checked="{{ playing }}" alt="Đang chạy" /></popover></dropdown>`, `checked=`, playing},
	} {
		t.Run(c.name, func(t *testing.T) {
			source := `<content>` + countdownOf(` duration="5m"`+countdownStarted, countdownClock+c.field) + `</content>`
			mustFailExactly(t, countdownSite(source), where("content.ui", source, c.needle)+c.says)
		})
	}

	// CONTROL: what only shows it
	html := countdownQuiet(t, `<content>`+countdownOf(` duration="5m"`+countdownStarted, countdownClock+
		`<input type="number" value="{{ remaining }}" readonly="true" alt="Còn" />`+
		`<input value="{{ duration }}" disabled="true" alt="Bao lâu" />`+
		`<input type="checkbox" checked="{{ playing }}" disabled="true" alt="Đang chạy" />`+
		`<input value="còn {{ remaining }}" readonly="{{ playing }}" alt="Còn" />`)+`</content>`)
	mustContain(t, html, `readonly data-kitwork-bind="value: remaining" value="300000">`, `data-kitwork-bind="checked: playing">`)
}

// A field two-way on a part that nothing makes readonly while it plays is a warning at its value, and
// the page builds: while it plays, the clock rewrites the part in the field the user is typing in. Any
// readonly or disabled bound to a value is the author's choice of when the field is edited, and is told
// nothing; readonly="false" is none.
// CONTROL: readonly="{{ playing }}" (the decision's), disabled="{{ playing }}", another value bound,
// and a field one way for good say nothing. With countdownField returning before its checks the rows
// warned nothing and failed; with the warning given whatever readonly says, the CONTROL page warned at
// its three fields bound to a value and failed (run 20/10/2026 on overlaid copies of countdown.go).
func TestCountdownFieldOnAPartWithoutReadonly(t *testing.T) {
	const says = ` mà không có readonly="{{ playing }}": lúc đang chạy, đồng hồ ghi đè số người dùng đang gõ trong ô; viết readonly="{{ playing }}"`
	// the hours' field beside a <time> that shows hours: one beside a <time> that does not is an error
	// (TestCountdownPartWrittenWhereItsTimeHidesIt)
	const withHours = `<time>{{ hours }}:{{ minutes }}:{{ seconds }}</time>`
	for _, c := range []struct{ name, field, needle, says string }{
		{"minutes in a number field", countdownClock + `<input type="number" value="{{ minutes }}" alt="Phút" />`, `value="{{ minutes }}" alt`, "ô nối hai chiều với minutes" + says},
		{"seconds in a text field", countdownClock + `<input value="{{ seconds }}" alt="Giây" />`, `value="{{ seconds }}" alt`, "ô nối hai chiều với seconds" + says},
		{"hours, readonly written false", withHours + `<input type="number" value="{{ hours }}" readonly="false" alt="Giờ" />`, `value="{{ hours }}" readonly`, "ô nối hai chiều với hours" + says},
		{"minutes in a popover of a dropdown", countdownClock + `<dropdown><button click="open = !open">Menu</button><popover><input type="number" value="{{ minutes }}" alt="Phút" /></popover></dropdown>`, `value="{{ minutes }}" alt`, "ô nối hai chiều với minutes" + says},
	} {
		t.Run(c.name, func(t *testing.T) {
			source := `<content>` + countdownOf(` duration="5m"`+countdownStarted, c.field) + `</content>`
			html, warned := countdownPage(t, source)
			if got, want := strings.Join(warned, "\n"), where("content.ui", source, c.needle)+c.says; got != want {
				t.Errorf("warnings:\n got  %s\n want %s", got, want)
			}
			mustContain(t, html, `data-kitwork-bind="value: `)
		})
	}

	// CONTROL
	countdownQuiet(t, `<content>`+countdownOf(` duration="5m"`+countdownStarted, withHours+
		`<input type="number" value="{{ minutes }}" readonly="{{ playing }}" alt="Phút" />`+
		`<input type="number" value="{{ seconds }}" disabled="{{ playing }}" alt="Giây" />`+
		`<input type="number" value="{{ hours }}" readonly="{{ minutes > 30 }}" alt="Giờ" />`+
		`<input type="number" value="{{ seconds }}" readonly="true" alt="Giây" />`)+`</content>`)
}

// A field whose value is a countdown's name read through its alias, outside its host or in it, is an
// error at that value (the review of 20/10/2026): the kernel writes a field back to a bare name only, so
// the field shows the name one way and what is typed in it sets nothing, then is painted over (writing
// through an alias is not in the expression grammar yet, docs "Còn mở"). A part is told to be written
// bare inside the host, or only shown; remaining, duration and playing are told what they are told
// bare. A readonly bound to a value leaves the field two-way at times, and is told the same.
// CONTROL: the names read through the alias by a field that only shows them (readonly="true",
// disabled="true"), in a run of text and by a call, and a part's field written bare in the host, compile
// with no warning, the field painted with the part. With checkCountdowns not reading fields (the copy of
// countdown.go before this test) every error row compiled, one way and silent, and this test failed (run
// 20/10/2026 on an overlaid copy of countdown.go).
func TestCountdownFieldThroughItsAlias(t *testing.T) {
	const kept = ` của countdown chỉ đọc: sửa thời gian bằng hours, minutes, seconds (value="{{ minutes }}"); chỉ để hiện thì thêm `
	part := func(name string) string {
		return `$tea.` + name + ` qua alias chưa ghi được: kernel chỉ ghi ngược vào một tên trần, nên ô này chỉ hiện, gõ vào không đặt thời gian; ô đặt thời gian viết tên trần bên trong <countdown>, value="{{ ` + name + ` }}"; chỉ để hiện thì thêm readonly="true"`
	}
	tea := func(inside, outside string) string {
		return `<content>` + countdownOf(` as="$tea" duration="5m"`+countdownStarted, countdownClock+inside) + outside + `</content>`
	}
	for _, c := range []struct{ name, inside, outside, needle, says string }{
		{"a part outside its host", ``, `<input type="number" value="{{ $tea.minutes }}" alt="Phút" />`, `value=`, part("minutes")},
		{"a part outside, readonly at times", ``, `<input type="number" value="{{ $tea.seconds }}" readonly="{{ $tea.playing }}" alt="Giây" />`, `value=`, part("seconds")},
		{"a part in its host through its alias", `<input type="number" value="{{ $tea.hours }}" readonly="{{ playing }}" alt="Giờ" />`, ``, `value=`, part("hours")},
		{"remaining outside", ``, `<input type="number" value="{{ $tea.remaining }}" alt="Còn" />`, `value=`, "remaining" + kept + `readonly="true"`},
		{"duration in a text field outside", ``, `<input value="{{ $tea.duration }}" alt="Bao lâu" />`, `value=`, "duration" + kept + `readonly="true"`},
		{"playing in a checkbox outside", ``, `<input type="checkbox" checked="{{ $tea.playing }}" alt="Đang chạy" />`, `checked=`, `playing của countdown chỉ đọc: chạy là play(), tạm dừng là pause(), một nút đổi qua lại là click="toggle()"; chỉ để hiện thì thêm disabled="true"`},
	} {
		t.Run(c.name, func(t *testing.T) {
			source := tea(c.inside, c.outside)
			mustFailExactly(t, countdownSite(source), where("content.ui", source, c.needle)+c.says)
		})
	}

	// CONTROL
	html := countdownQuiet(t, tea(`<input type="number" value="{{ minutes }}" readonly="{{ playing }}" alt="Phút" />`,
		`<input type="number" value="{{ $tea.minutes }}" readonly="true" alt="Phút" />`+
			`<input type="checkbox" checked="{{ $tea.playing }}" disabled="true" alt="Đang chạy" />`+
			`<text>Còn {{ $tea.minutes }} phút</text><button click="$tea.pause()">Tạm dừng</button>`))
	mustContain(t, html, `data-kitwork-bind="value: minutes; readonly: playing" value="5">`, `data-kitwork-bind="value: $tea.minutes" value="5">`)
}

// A number written to a part is a whole number in the part's range (the decision of 20/10/2026, evening:
// a value above the max is never shown or counted): the countdown clamps each part as it is written and
// never carries it to the next, so a number the compile reads outside the range, or the range a segment
// narrows it to, or not whole, is an error at the event that says what the countdown makes of it:
// minutes = 120 and minutes = 60 are 59 (a longer time is written in hours), hours = 30 is 24, seconds =
// 4000 and seconds = 90 are 59, minutes = 1.5 is 1, minutes = 45 against a segment of max="30" is 30.
// Wherever the countdown's names reach: a button, its own ready, a popover of a dropdown in it.
// CONTROL: a number in the range compiles with no warning (minutes = 59, seconds = 59, hours = 24 where
// the <time> reads hours, minutes = 30 against max="30"), and so does a value the compile does not know
// (minutes = minutes + 90, a negative, ++), the browser's to clamp. With the range and the whole number
// taken out of writeProblem every error row failed: what its <time> shows compiled (hours = 30 beside
// hours, minutes = 1.5, minutes = 45 over a max of 30), the rest was told the morning's error of a time
// not shown (minutes = 120 "dài hơn một giờ…") (run 20/10/2026 on an overlaid copy of countdown.go).
func TestCountdownPartWrittenOutOfItsRange(t *testing.T) {
	const kept = ` ngoài khoảng của `
	const clamps = `: countdown giữ mỗi phần trong khoảng của nó, ghi `
	for _, c := range []struct{ name, words, clock, inside, needle, says string }{
		{"two hours in minutes", ``, countdownClock, `<button click="minutes = 120">2 giờ</button>`, `click=`, `minutes = 120` + kept + `minutes (0 tới 59)` + clamps + `59; thời gian dài hơn ghi ở hours`},
		{"an hour exactly in minutes", ``, countdownClock, `<button click="minutes = 60">1 giờ</button>`, `click=`, `minutes = 60` + kept + `minutes (0 tới 59)` + clamps + `59; thời gian dài hơn ghi ở hours`},
		{"thirty hours", ``, `<time>{{ hours }}:{{ minutes }}:{{ seconds }}</time>`, `<button click="hours = 30">30 giờ</button>`, `click=`, `hours = 30` + kept + `hours (0 tới 24)` + clamps + `24; countdown đếm nhiều nhất 24 giờ`},
		{"seconds of more than an hour", ``, countdownClock, `<button click="seconds = 4000">a</button>`, `click=`, `seconds = 4000` + kept + `seconds (0 tới 59)` + clamps + `59; thời gian dài hơn ghi ở minutes`},
		{"on its own ready", ` ready="minutes = 90"`, countdownClock, `<button click="toggle()">Chạy</button>`, `ready=`, `minutes = 90` + kept + `minutes (0 tới 59)` + clamps + `59; thời gian dài hơn ghi ở hours`},
		{"in a popover of a dropdown", ``, countdownClock, `<dropdown><button click="open = !open">Menu</button><popover><button click="minutes = 75">a</button></popover></dropdown>`, `click="minutes`, `minutes = 75` + kept + `minutes (0 tới 59)` + clamps + `59; thời gian dài hơn ghi ở hours`},
		{"a part of a minute", ``, countdownClock, `<button click="minutes = 1.5">a</button>`, `click=`, `minutes = 1.5 không phải một số nguyên: mỗi phần của countdown là số nguyên, countdown ghi 1`},
		{"over a segment's max", ``, countdownSegments, `<button click="minutes = 45">a</button>`, `click=`, `minutes = 45` + kept + `ô minutes ở content.ui:1:%d (0 tới 30)` + clamps + `30`},
	} {
		t.Run(c.name, func(t *testing.T) {
			words := ` duration="5m"` + c.words
			if c.words == "" {
				words += ` ready="play()"`
			}
			clock := c.clock
			if clock == countdownSegments {
				clock = strings.Replace(clock, `value="{{ minutes }}"`, `value="{{ minutes }}" max="30"`, 1)
			}
			source := `<content>` + countdownOf(words+` finish="$toast.info('Hết giờ')"`, clock+c.inside) + `</content>`
			says := c.says
			if strings.Contains(says, "%d") {
				says = strings.Replace(says, "%d", itoa(len([]rune(source[:strings.Index(source, "<input")]))+1), 1)
			}
			mustFailExactly(t, countdownSite(source), where("content.ui", source, c.needle)+says)
		})
	}
	for _, seconds := range []string{"90", "60"} {
		source := `<content>` + countdownOf(` duration="30s"`+countdownStarted, `<time>{{ seconds }}</time><button click="seconds = `+seconds+`">a</button>`) + `</content>`
		mustFailExactly(t, countdownSite(source), where("content.ui", source, `click=`)+`seconds = `+seconds+kept+`seconds (0 tới 59)`+clamps+`59; thời gian dài hơn ghi ở minutes`)
	}

	// CONTROL
	countdownQuiet(t, `<content>`+countdownOf(` duration="5m"`+countdownStarted, countdownClock+
		`<button click="minutes = 59">a</button><button click="seconds = 59">b</button>`+
		`<button click="hours = 0">d</button><button click="minutes = minutes + 90">e</button><button click="minutes = -120">f</button>`+
		`<button click="minutes++">g</button>`)+`</content>`)
	countdownQuiet(t, `<content>`+countdownOf(` duration="5m"`+countdownStarted, `<time>{{ hours }}:{{ minutes }}:{{ seconds }}</time>`+
		`<button click="hours = 24">a</button><button click="hours = 2">b</button>`)+`</content>`)
	countdownQuiet(t, `<content>`+countdownOf(` duration="5m"`+countdownStarted, strings.Replace(countdownSegments, `value="{{ minutes }}"`, `value="{{ minutes }}" max="30"`, 1)+
		`<button click="minutes = 30">a</button>`)+`</content>`)
	countdownQuiet(t, `<content>`+countdownOf(` duration="30s"`+countdownStarted, `<time>{{ seconds }}</time><button click="seconds = 59">a</button>`)+`</content>`)
}

// A write that can raise a part its <time> does not show is an error at the event: the time it adds is
// never seen (hours where the <time> reads no hours, minutes where it reads seconds alone). A number is
// told the time it sets, as the same time written as duration is (hours = 1 is an hour exactly), and a
// value the compile does not know (hours++, hours = hours + 1) that it adds a time not shown; a field
// two-way on such a part is an error at its value too, what is typed in it never seen. With no hours in
// the time it can show, the countdown counts 59:59 at most: typing never reaches a hidden hour.
// CONTROL: what only lowers it or sets it to 0 compiles (hours--, hours = 0), and so does any write of a
// part the <time> shows (hours++ where it reads hours, or remaining; minutes++ where it reads minutes;
// seconds++ anywhere), and a field two-way on hours beside a <time> that reads hours. With hides()
// always false every error row compiled, but the number's, which writeProblem reads first, and this
// test failed (run 20/10/2026 on an overlaid copy of countdown.go).
func TestCountdownPartWrittenWhereItsTimeHidesIt(t *testing.T) {
	const hours = ` mà <time> không đọc hours: thời gian thêm vào không hiện được, minutes chỉ tới 59; đọc thêm hours`
	const minutes = ` mà <time> chỉ đọc seconds: thời gian thêm vào không hiện được, seconds đếm từ 59 về 0 rồi quay lại; đọc thêm minutes`
	for _, c := range []struct{ name, duration, clock, inside, needle, says string }{
		{"an hour", "5m", countdownClock, `<button click="hours = 1">1 giờ</button>`, `click=`, `hours = 1 đặt thời gian dài đúng một giờ mà <time> không đọc hours: minutes chỉ tới 59; đọc thêm hours`},
		{"an hour more", "5m", countdownClock, `<button click="hours++">+1 giờ</button>`, `click=`, `hours++ ghi giờ` + hours},
		{"an hour more, written out", "5m", countdownClock, `<button click="hours = hours + 1">+1 giờ</button>`, `click=`, `hours = … ghi giờ` + hours},
		{"segments without hours", "5m", countdownSegments, `<button click="hours++">+1 giờ</button>`, `click=`, `hours++ ghi giờ` + hours},
		{"a minute more where only seconds show", "30s", `<time>{{ seconds }}</time>`, `<button click="minutes++">+1</button>`, `click=`, `minutes++ ghi phút` + minutes},
		{"a field on hours", "5m", countdownClock, `<input type="number" value="{{ hours }}" readonly="{{ playing }}" alt="Giờ" />`, `value=`,
			`ô nối hai chiều với hours mà <time> không đọc hours: giờ người dùng gõ không hiện được, minutes chỉ tới 59; đọc thêm hours`},
	} {
		t.Run(c.name, func(t *testing.T) {
			source := `<content>` + countdownOf(` duration="`+c.duration+`"`+countdownStarted, c.clock+c.inside) + `</content>`
			mustFailExactly(t, countdownSite(source), where("content.ui", source, c.needle)+c.says)
		})
	}

	// CONTROL
	countdownQuiet(t, `<content>`+countdownOf(` duration="5m"`+countdownStarted, countdownClock+
		`<button click="hours--">a</button><button click="hours = 0">b</button><button click="minutes++">c</button><button click="seconds++">d</button>`)+`</content>`)
	countdownQuiet(t, `<content>`+countdownOf(` duration="5m"`+countdownStarted, `<time>{{ hours }}:{{ minutes }}:{{ seconds }}</time>`+
		`<button click="hours++">a</button><input type="number" value="{{ hours }}" readonly="{{ playing }}" alt="Giờ" />`)+`</content>`)
	countdownQuiet(t, `<content>`+countdownOf(` duration="5m"`+countdownStarted, `<time>{{ remaining / 1000 }}</time><button click="hours++">a</button>`)+`</content>`)
	countdownQuiet(t, `<content>`+countdownOf(` duration="30s"`+countdownStarted, `<time>{{ seconds }}</time><button click="seconds++">a</button>`)+`</content>`)
}

// countdownSegments is the time of the decision of 20/10/2026, evening: its fields are the clock, the
// minutes and the seconds, each named by its alt.
const countdownSegments = `<time><input value="{{ minutes }}" alt="Phút" />:<input value="{{ seconds }}" alt="Giây" /></time>`

// countdownSegmentTea is the countdown of that decision: the segments, buttons of the author's own for
// a minute more and less, Start or Pause, and Stop.
const countdownSegmentTea = `<countdown as="$tea" duration="5m" finish="$toast.success('Trà đã sẵn sàng')" flex="row" gap="3">` +
	countdownSegments +
	`<button click="minutes++">+1 phút</button>` +
	`<button click="minutes--">−1 phút</button>` +
	`<button click="toggle()">{{ playing ? 'Tạm dừng' : 'Chạy' }}</button>` +
	`<button click="stop()">Dừng</button>` +
	`</countdown>`

// The time's segments, as the decision writes them, build with no warning, exactly: the <time> is no
// timer but the group of its fields, named for what it holds (Thời gian còn lại, an alt on it naming it
// instead, which no warning calls useless), its datetime bound as ever; each field is a text field of
// digits with no spinner (inputmode numeric, never type="number"), never spellchecked or filled in,
// sized for two digits, its placeholder 00, a spinbutton with its range, the part it starts at and that
// said in words, named by its alt or by the part's word, data-part for the countdown's JS, and the start
// in two digits (05, 00, and 02 hours since the bar of 20/10/2026, late evening) with no binding at all:
// the countdown owns it, the kernel's two-way field never touches it. The buttons reach the kernel as
// written. An English site says it in English.
// CONTROL: a display-only countdown is as it was (role="timer", nothing of a group), and a field
// outside the <time> is the kernel's two-way field as it was (TestCountdownTimeSetByFields). With
// countdownSegmentWords writing nothing the field was the kernel's two-way field ("value: minutes",
// value="5") and this test failed; with countdownElement's group taken out the <time> was a timer of
// fields and it failed there too (run 20/10/2026 on overlaid copies of countdown.go).
func TestCountdownTimeSegments(t *testing.T) {
	const button = `<button class="flex-row background-ink/6 hover:background-ink/10 pressed:background-ink/14 disabled:background-ink/4" type="button"`
	const datetime = `data-kitwork-bind="datetime: &#39;PT&#39; + hours + &#39;H&#39; + minutes + &#39;M&#39; + seconds + &#39;S&#39;"`
	const field = ` role="spinbutton" inputmode="numeric" autocomplete="off" spellcheck="false" size="2" placeholder="00"`
	html := countdownQuiet(t, `<content>`+countdownSegmentTea+`</content>`)
	mustContain(t, html, `<div class="flex-row gap-3" data-kitwork-component="countdown: $tea" data-kitwork-event="finish: $toast.success(&#39;Trà đã sẵn sàng&#39;)" data-kitwork-scope="duration: 300000">`+
		`<time class="countdown-time" role="group" aria-label="Thời gian còn lại" `+datetime+` datetime="PT0H5M0S">`+
		`<input aria-label="Phút"`+field+` aria-valuemin="0" aria-valuemax="59" aria-valuenow="5" aria-valuetext="5 phút" data-part="minutes" value="05">:`+
		`<input aria-label="Giây"`+field+` aria-valuemin="0" aria-valuemax="59" aria-valuenow="0" aria-valuetext="0 giây" data-part="seconds" value="00"></time>`+
		button+` data-kitwork-event="click: minutes++">+1 phút</button>`+
		button+` data-kitwork-event="click: minutes--">−1 phút</button>`+
		button+` data-kitwork-bind="text: playing ? &#39;Tạm dừng&#39; : &#39;Chạy&#39;" data-kitwork-event="click: toggle()">Chạy</button>`+
		button+` data-kitwork-event="click: stop()">Dừng</button></div>`)
	mustNotContain(t, html, `role="timer"`, `value: minutes`, `type="number"`)

	// hours in two digits, a time named by its alt, a field named by its part, a narrower max
	html = countdownQuiet(t, `<content>`+countdownOf(` duration="2h"`+countdownStarted,
		`<text>Còn <time alt="Còn lại"><input value="{{ hours }}" />:<input value="{{ minutes }}" max="30" />:<input value="{{ seconds }}" /></time></text>`)+`</content>`)
	mustContain(t, html, `<p>Còn <time class="countdown-time" role="group" aria-label="Còn lại" `+datetime+` datetime="PT2H0M0S">`+
		`<input aria-label="Giờ"`+field+` aria-valuemin="0" aria-valuemax="24" aria-valuenow="2" aria-valuetext="2 giờ" data-part="hours" value="02">:`+
		`<input max="30" aria-label="Phút"`+field+` aria-valuemin="0" aria-valuemax="30" aria-valuenow="0" aria-valuetext="0 phút" data-part="minutes" value="00">:`+
		`<input aria-label="Giây"`+field+` aria-valuemin="0" aria-valuemax="59" aria-valuenow="0" aria-valuetext="0 giây" data-part="seconds" value="00"></time></p>`)

	// an English site
	s := memorySite("", countdownSite(`<content>`+countdownOf(` duration="61s"`+countdownStarted, `<time><input value="{{ minutes }}" />:<input value="{{ seconds }}" /></time>`)+`</content>`))
	s.Reload, s.Language = true, "en"
	page, warned := compileDiagnose(t, s)
	if len(warned) > 0 {
		t.Errorf("want no warning, got:\n%s", strings.Join(warned, "\n"))
	}
	mustContain(t, page.Render(Request{Path: "/"}), `role="group" aria-label="Time left"`,
		`<input aria-label="Minutes"`+field+` aria-valuemin="0" aria-valuemax="59" aria-valuenow="1" aria-valuetext="1 minute" data-part="minutes" value="01">`,
		`<input aria-label="Seconds"`+field+` aria-valuemin="0" aria-valuemax="59" aria-valuenow="1" aria-valuetext="1 second" data-part="seconds" value="01">`)

	// CONTROL: display only, and a field outside the <time>
	html = countdownQuiet(t, `<content>`+countdownOf(` duration="5m"`+countdownStarted, countdownClock+
		`<input type="number" value="{{ minutes }}" readonly="{{ playing }}" alt="Phút" />`)+`</content>`)
	mustContain(t, html, `<time class="countdown-time" role="timer"`, `data-kitwork-bind="value: minutes; readonly: playing" value="5">`)
	mustNotContain(t, html, `role="group"`, `data-part`, `spinbutton`)
}

// A segment is written one way: one bare part as its value, one field a part, never repeated, no word
// the engine writes on it (type="number" is told that the engine writes the field and that the
// buttons are the author's), no readonly (a segment is edited while it plays, the countdown holding);
// min and max are whole numbers of the part's range that only narrow it, and the duration starts
// inside them. Each is told at its place, in the author's words.
// CONTROL: a segment written right compiles with no warning (TestCountdownTimeSegments), and so does
// one with a narrower min and max the duration starts in. With countdownSegment reading only the value,
// every row of a word on the field compiled (type, inputmode, step, readonly, min, max: type="number"
// with the spinner the owner saw, max="99" over what seconds hold) and this test failed (run 20/10/2026 on an overlaid copy of countdown.go).
func TestCountdownSegmentWrittenWrong(t *testing.T) {
	const notAPart = `ô trong <time> của countdown là một phần của thời gian: value="{{ minutes }}" (hours, minutes hay seconds, tên trần); ô khác đặt ngoài <time>`
	const engine = `: engine viết ô (inputmode="numeric", autocomplete="off"); bỏ `
	for _, c := range []struct{ name, duration, inside, needle, says string }{
		{"no value", "5m", `<input alt="Phút" />`, `<input`, notAPart},
		{"remaining", "5m", `<input value="{{ remaining }}" />`, `value=`, notAPart},
		{"padded by the author", "5m", `<input value="{{ String(minutes).padStart(2, '0') }}" />`, `value=`, notAPart},
		{"through its alias", "5m", `<input value="{{ $tea.minutes }}" />`, `value=`, notAPart},
		{"a word", "5m", `<input value="5" />`, `value=`, notAPart},
		{"two of one part", "5m", `<input value="{{ minutes }}" alt="a" />:<input value="{{ minutes }}" alt="b" />`, `value="{{ minutes }}" alt="b"`, "<time> của countdown đã có ô minutes ở content.ui:1:%d: mỗi phần một ô"},
		{"a for", "5m", `<box for="n of [1, 2]"><input value="{{ minutes }}" /></box>`, `for=`, "một phần của thời gian không lặp bằng for: mỗi phần một ô; bỏ for"},
		{"type number", "5m", `<input type="number" value="{{ minutes }}" />`, `type=`, `một phần của thời gian không có type="number": engine viết ô (hai chữ số, bàn phím số, không có nút tăng giảm); bỏ type, nút tăng giảm là của người viết, <button click="minutes++">+1</button>`},
		{"type text", "5m", `<input type="text" value="{{ seconds }}" />`, `type=`, "một phần của thời gian không có type: engine viết ô (hai chữ số, bàn phím số); bỏ type"},
		{"inputmode", "5m", `<input inputmode="numeric" value="{{ minutes }}" />`, `inputmode=`, "một phần của thời gian không có inputmode" + engine + "inputmode"},
		{"step", "5m", `<input step="5" value="{{ minutes }}" />`, `step=`, "một phần của thời gian không có step" + engine + "step"},
		{"readonly", "5m", `<input value="{{ minutes }}" readonly="{{ playing }}" />`, `readonly=`, "một phần của thời gian không có readonly: bấm vào thì countdown dừng ở đúng số đó, Enter hay rời <time> thì chạy tiếp từ số đã sửa; chỉ để hiện thì viết {{ minutes }} trong <time>"},
		{"max widened", "30s", `<input value="{{ seconds }}" max="99" />`, `max=`, `max="99" rộng hơn khoảng của seconds (0 tới 59): min, max của một phần chỉ hẹp lại, như max="30"`},
		{"hours widened", "1h", `<input value="{{ hours }}" max="48" />:<input value="{{ minutes }}" />`, `max=`, `max="48" rộng hơn khoảng của hours (0 tới 24): min, max của một phần chỉ hẹp lại, như max="12"`},
		{"min below 0", "30s", `<input value="{{ seconds }}" min="-1" />`, `min=`, `min="-1" rộng hơn khoảng của seconds (0 tới 59): min, max của một phần chỉ hẹp lại, như min="1"`},
		{"max from data", "30s", `<input value="{{ seconds }}" max="{{ most }}" />`, `max=`, `max của một phần là một số cố định (max="30"), không có {{ }}: engine viết khoảng của ô khi trang dựng`},
		{"max not whole", "30s", `<input value="{{ seconds }}" max="30.5" />`, `max=`, `max="30.5" của seconds không phải một số nguyên: viết một số từ 0 tới 59, như max="30"`},
		{"min over max", "30s", `<input value="{{ seconds }}" min="40" max="30" />`, `min=`, `min="40" lớn hơn max="30": ô seconds không còn số nào`},
		{"duration over max", "45m", `<input value="{{ minutes }}" max="30" />:<input value="{{ seconds }}" />`, `max=`, `duration="45m" bắt đầu ở 45 phút, ngoài khoảng của ô minutes (0 tới 30): viết duration trong khoảng đó, hay nới min, max`},
		{"duration under min", "5m", `<input value="{{ minutes }}" min="10" />:<input value="{{ seconds }}" />`, `min=`, `duration="5m" bắt đầu ở 5 phút, ngoài khoảng của ô minutes (10 tới 59): viết duration trong khoảng đó, hay nới min, max`},
	} {
		t.Run(c.name, func(t *testing.T) {
			source := `<content>` + countdownOf(` as="$tea" duration="`+c.duration+`"`+countdownStarted, `<time>`+c.inside+`</time>`) + `</content>`
			says := c.says
			if strings.Contains(says, "%d") {
				says = strings.Replace(says, "%d", itoa(len([]rune(source[:strings.Index(source, "<input")]))+1), 1)
			}
			mustFailExactly(t, countdownSite(source), where("content.ui", source, c.needle)+says)
		})
	}

	// CONTROL
	html := countdownQuiet(t, `<content>`+countdownOf(` duration="15m"`+countdownStarted,
		`<time><input value="{{ minutes }}" min="10" max="30" alt="Phút" />:<input value="{{ seconds }}" alt="Giây" /></time>`)+`</content>`)
	mustContain(t, html, `aria-valuemin="10" aria-valuemax="30" aria-valuenow="15" aria-valuetext="15 phút" data-part="minutes" value="15">`)
}

// A segment takes no word that would leave it a field the countdown cannot hold or that hears nothing
// (the review of 20/10/2026, evening): disabled, bound or not, as readonly (disabled="{{ playing }}"
// takes the focus away while it plays, so a click never holds it, and it locks and unlocks in turn);
// input=, which never comes (the countdown takes every key; a segment left changed sends change, and
// change= on a segment compiles quiet, wired as written); and a <select> or a <textarea> in the <time>,
// told that a segment is an <input> the engine writes, not that the <time> reads too little.
// CONTROL: against the copy of countdown.go from before the review (-overlay), disabled="{{ playing }}"
// and disabled compiled quiet, input= compiled quiet, and the <select> and the <textarea> were told
// "duration="5m" dài hơn một phút mà <time> chỉ đọc seconds" (each row failed; change= compiled as now).
func TestCountdownSegmentHoldsAndHears(t *testing.T) {
	const disabled = "một phần của thời gian không có disabled: bấm vào thì countdown dừng ở đúng số đó, Enter hay rời <time> thì chạy tiếp từ số đã sửa; chỉ để hiện thì viết {{ minutes }} trong <time>"
	const input = `một phần của thời gian không gửi input: countdown nhận từng phím và tự viết ô; ô đã đổi gửi change khi focus rời nó (Enter, Tab), change="…"`
	const field = `<%s> trong <time> của countdown không phải một phần của thời gian: mỗi phần là một <input value="{{ minutes }}" />, engine viết nó (hai chữ số, bàn phím số); nút chọn là của người viết, đặt ngoài <time>: <button click="minutes++">+1</button>`
	for _, c := range []struct{ name, inside, needle, says string }{
		{"disabled while it plays", `<input value="{{ minutes }}" disabled="{{ playing }}" />:<input value="{{ seconds }}" />`, `disabled=`, disabled},
		{"disabled for good", `<input value="{{ minutes }}" disabled />:<input value="{{ seconds }}" />`, `disabled`, disabled},
		{"input=", `<input value="{{ minutes }}" input="$toast.info('x')" />:<input value="{{ seconds }}" />`, `input=`, input},
		{"input= among others", `<input value="{{ minutes }}" change,input:once="$toast.info('x')" />:<input value="{{ seconds }}" />`, `change,input`, input},
		{"a select", `<select value="{{ minutes }}"><option value="1">1</option></select>:<input value="{{ seconds }}" />`, `<select`, strings.Replace(field, "%s", "select", 1)},
		{"a textarea", `<textarea value="{{ minutes }}"></textarea>:<input value="{{ seconds }}" />`, `<textarea`, strings.Replace(field, "%s", "textarea", 1)},
	} {
		t.Run(c.name, func(t *testing.T) {
			source := `<content>` + countdownOf(` as="$tea" duration="5m"`+countdownStarted, `<time>`+c.inside+`</time>`) + `</content>`
			mustFailExactly(t, countdownSite(source), where("content.ui", source, c.needle)+c.says)
		})
	}

	// CONTROL: change= on a segment is heard as written, with no warning
	html := countdownQuiet(t, `<content>`+countdownOf(` duration="5m"`+countdownStarted,
		`<time><input value="{{ minutes }}" alt="Phút" />:<input value="{{ seconds }}" alt="Giây" change="$toast.info('đã đổi')" /></time>`)+`</content>`)
	mustContain(t, html, `<input aria-label="Giây" data-kitwork-event="change: $toast.info(&#39;đã đổi&#39;)" role="spinbutton"`)
}

// The fields of the <time> stand in a clock's order, hours, then minutes, then seconds (decisions of
// 20/10/2026, late evening: the bar is read, and typed through, as one clock from the left): a field of
// a smaller part before a larger one is told at the larger one's value, naming the field before it,
// and shown the two in their order. Two fields of one part are told as before (checked first).
// CONTROL: hours:minutes:seconds, minutes:seconds and seconds alone compile with no warning. Against a
// copy of countdown.go without the order check (-overlay), every row compiled and this test failed (run
// 20/10/2026).
func TestCountdownSegmentsStandInAClocksOrder(t *testing.T) {
	const says = `ô %s đứng sau ô %s ở content.ui:1:%s: các ô của <time> theo thứ tự của đồng hồ, giờ rồi phút rồi giây: <input value="{{ %s }}" />:<input value="{{ %s }}" />`
	for _, c := range []struct{ name, duration, inside, needle, part, before, beforeNeedle string }{
		{"seconds, then minutes", "5m", `<input value="{{ seconds }}" />:<input value="{{ minutes }}" />`,
			`value="{{ minutes }}"`, "minutes", "seconds", `<input value="{{ seconds }}"`},
		{"minutes, then hours", "5m", `<input value="{{ minutes }}" />:<input value="{{ hours }}" />:<input value="{{ seconds }}" />`,
			`value="{{ hours }}"`, "hours", "minutes", `<input value="{{ minutes }}"`},
		{"hours, seconds, minutes", "2h", `<input value="{{ hours }}" />:<input value="{{ seconds }}" />:<input value="{{ minutes }}" />`,
			`value="{{ minutes }}"`, "minutes", "seconds", `<input value="{{ seconds }}"`},
		{"two of one part, told as before", "5m", `<input value="{{ seconds }}" alt="a" />:<input value="{{ seconds }}" alt="b" />`,
			`value="{{ seconds }}" alt="b"`, "", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			source := `<content>` + countdownOf(` duration="`+c.duration+`"`+countdownStarted, `<time>`+c.inside+`</time>`) + `</content>`
			want := where("content.ui", source, c.needle)
			if c.part == "" {
				want += "<time> của countdown đã có ô seconds ở " + strings.TrimSuffix(where("content.ui", source, `<input`), ": ") + ": mỗi phần một ô"
			} else {
				column := strings.Split(strings.TrimSuffix(where("content.ui", source, c.beforeNeedle), ": "), ":")[2]
				want += fmt.Sprintf(says, c.part, c.before, column, c.part, c.before)
			}
			mustFailExactly(t, countdownSite(source), want)
		})
	}

	// CONTROL
	countdownQuiet(t, `<content>`+countdownOf(` duration="2h"`+countdownStarted, `<time><input value="{{ hours }}" />:<input value="{{ minutes }}" />:<input value="{{ seconds }}" /></time>`)+`</content>`)
	countdownQuiet(t, `<content>`+countdownOf(` duration="5m"`+countdownStarted, countdownSegments)+`</content>`)
	countdownQuiet(t, `<content>`+countdownOf(` duration="30s"`+countdownStarted, `<time><input value="{{ seconds }}" /></time>`)+`</content>`)
}

// The fields of a <time> read as a clock to the seconds, with no part missing between (the review of
// 20/10/2026, late evening: the bar is minutes and seconds, or hours, minutes and seconds): hours alone
// read 00 for all of a 5 minutes' run, minutes alone 00 for its last 59 seconds, hours and minutes 00:04
// a second in, and none of them takes a second typed; hours and seconds read 00:30 for 30 seconds, and 1
// typed first is an hour. Each is told at the <time>, what it has and what it lacks, and shown the clock
// from its largest part to the seconds. The words of a <time> that only shows (no field) are the
// author's, as before.
// CONTROL: seconds alone (30s), minutes and seconds, hours, minutes and seconds compile with no warning
// (TestCountdownSegmentsStandInAClocksOrder), and so does minutes read as text alone, with no field.
// Against a copy of countdown.go whose countdownTime never reports countdownBarGaps (-overlay), every
// row compiled ("expected a compile error", run 21/10/2026).
func TestCountdownBarReadsToTheSeconds(t *testing.T) {
	const says = "<time> của countdown có ô %s mà không có ô %s: các ô là một đồng hồ đọc tới giây, không thiếu phần nào ở giữa; viết %s"
	const three = `<input value="{{ hours }}" />:<input value="{{ minutes }}" />:<input value="{{ seconds }}" />`
	const two = `<input value="{{ minutes }}" />:<input value="{{ seconds }}" />`
	for _, c := range []struct{ name, duration, inside, has, lacks, clock string }{
		{"hours alone", "5m", `<input value="{{ hours }}" alt="Giờ" />`, "hours", "minutes, seconds", three},
		{"minutes alone", "5m", `<input value="{{ minutes }}" alt="Phút" />`, "minutes", "seconds", two},
		{"hours and minutes", "5m", `<input value="{{ hours }}" />:<input value="{{ minutes }}" />`, "hours, minutes", "seconds", three},
		{"hours and minutes of two hours", "2h", `<input value="{{ hours }}" />:<input value="{{ minutes }}" />`, "hours, minutes", "seconds", three},
		{"hours and seconds", "30s", `<input value="{{ hours }}" />:<input value="{{ seconds }}" />`, "hours, seconds", "minutes", three},
	} {
		t.Run(c.name, func(t *testing.T) {
			source := `<content>` + countdownOf(` duration="`+c.duration+`"`+countdownStarted, `<time alt="Thời gian">`+c.inside+`</time>`) + `</content>`
			mustFailExactly(t, countdownSite(source), where("content.ui", source, `<time`)+fmt.Sprintf(says, c.has, c.lacks, c.clock))
		})
	}

	// CONTROL: a <time> that only shows reads what its author writes
	countdownQuiet(t, `<content>`+countdownOf(` duration="5m"`+countdownStarted, `<time>{{ minutes }} phút</time>`)+`</content>`)
}

// The <time> of fields is one long bar (decisions of 20/10/2026, late evening: "một thanh dài gồm 3
// thanh giờ phút giây và placeholder là 00:00:00"): the engine writes it as the countdown's time and a
// group (class countdown-time, role="group"), which is what reboot.css draws as one field, and every
// field in two digits (02 hours) with the placeholder 00, so an empty bar reads 00:00:00; a placeholder
// the author wrote is kept, and no 00 is added beside it. reboot.css gives the group the field's look
// (its fill, its transparent edge, its radius, its padding), the bar the focus ring and the deeper fill
// while the focus is in it, and the fields in it none of their own: no fill, no edge, no padding, no
// ring, two digits wide, the digits in the middle. A <time> that only shows the time (a timer) is no bar.
// CONTROL: the display-only countdown's <time> is role="timer", and the bar's selector holds role="group",
// so it is not drawn as one; each rule of the bar, taken out of the stylesheet in this test, is found
// missing. Against a copy of countdown.go (-overlay) that writes no placeholder and hours as they are
// (as before the bar), this test failed on the fields of the hours and the minutes and on the count of
// placeholders, 1, the author's (run 20/10/2026).
func TestCountdownTimeOfFieldsIsOneBar(t *testing.T) {
	const field = ` role="spinbutton" inputmode="numeric" autocomplete="off" spellcheck="false" size="2"`
	html := countdownQuiet(t, `<content>`+countdownOf(` duration="2h"`+countdownStarted,
		`<time><input value="{{ hours }}" alt="Giờ" />:<input value="{{ minutes }}" alt="Phút" />:<input value="{{ seconds }}" alt="Giây" placeholder="ss" /></time>`)+`</content>`)
	mustContain(t, html, `<time class="countdown-time" role="group" aria-label="Thời gian còn lại"`,
		`<input aria-label="Giờ"`+field+` placeholder="00" aria-valuemin="0" aria-valuemax="24" aria-valuenow="2" aria-valuetext="2 giờ" data-part="hours" value="02">:`,
		`<input aria-label="Phút"`+field+` placeholder="00" aria-valuemin="0" aria-valuemax="59" aria-valuenow="0" aria-valuetext="0 phút" data-part="minutes" value="00">:`,
		`<input aria-label="Giây" placeholder="ss"`+field+` aria-valuemin="0" aria-valuemax="59" aria-valuenow="0" aria-valuetext="0 giây" data-part="seconds" value="00">`)
	if n := strings.Count(html, `placeholder=`); n != 3 {
		t.Errorf("want one placeholder a field, the author's kept: %d in\n%s", n, html)
	}

	css := string(stylesheet.Reboot().CSS)
	const bar = `:where(.countdown-time[role="group"])`
	rules := map[string][]string{
		bar: {"display:inline-flex", "inline-size:max-content", "direction:ltr", "padding:0.5rem 0.75rem", "border:1px solid transparent",
			"border-radius:0.375rem", "background:color-mix(in srgb,var(--ink) 5%,transparent)"},
		`:where(.countdown-time[role="group"]:focus-within)`: {"outline:2px solid var(--kitwork-focus,var(--brand))", "outline-offset:2px",
			"background:color-mix(in srgb,var(--ink) 8%,transparent)"},
		`:where(.countdown-time[role="group"] input)`: {"inline-size:calc(2ch + 2px)", "padding:0", "border:0", "outline:none", "text-align:center", "background:none"},
	}
	missing := func(css string) []string {
		var out []string
		for selector, declarations := range rules {
			i := strings.Index(css, selector+"{")
			if i < 0 {
				out = append(out, selector)
				continue
			}
			body := css[i+len(selector)+1:]
			body = ";" + body[:strings.Index(body, "}")] + ";"
			for _, d := range declarations {
				if !strings.Contains(body, ";"+d+";") {
					out = append(out, selector+" "+d)
				}
			}
		}
		return out
	}
	if gone := missing(css); len(gone) > 0 {
		t.Errorf("reboot.css does not draw the bar: no %s", strings.Join(gone, ", "))
	}

	// CONTROL: a timer is no bar, and each rule of the bar is found missing when it is gone
	html = countdownQuiet(t, `<content>`+countdownOf(` duration="5m"`+countdownStarted, countdownClock)+`</content>`)
	mustContain(t, html, `<time class="countdown-time" role="timer"`)
	mustNotContain(t, html, `role="group"`)
	for selector := range rules {
		i := strings.Index(css, selector+"{")
		if i < 0 {
			continue
		}
		end := i + strings.Index(css[i:], "}") + 1
		if gone := missing(css[:i] + css[end:]); len(gone) == 0 {
			t.Errorf("CONTROL: %s taken out, the check finds nothing", selector)
		}
	}
}
