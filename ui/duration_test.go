package ui

import (
	"math"
	"strings"
	"testing"
)

// A duration is one number and one unit among ms, s, m and h, read into milliseconds (seconds and up as
// the decimal written: 4.03s is 4030, 0.05m is 3000); digits too many to hold read as +Inf. What is not
// one says which way: a minus before a time (its suggestion is the time without it, two units' as
// one), zero with its unit or without, a number without its unit (suggested in ms from 100 up on the
// milliseconds scale, always in s on the seconds scale), two units or more (said again as one, in the
// smallest), a foreign unit (Vietnamese with or without its marks, English, .ui's in capitals, after a
// space: told .ui's), a space before .ui's own unit, the ISO 8601 form (days as hours), and anything else
// (a word, .5s, a day, a number without its unit after one with it, a comma, P or PT alone, P1M — a
// month in ISO — a minus before a word).
// CONTROL: with m and h left out of durationUnits every row in m or h went red; with two units read as no
// duration, the two-unit rows; with durationForeignUnits empty and the ISO form not read, the rows of a
// foreign word and the ISO rows; with .ui's units in capitals not looked up and the space before a unit
// not noted, 5S, 5MS and 5 s (5 s then read as 5000, quietly), and the rows of the carousel, network,
// terminal and toast tests that write them; with the scale ignored (always ms from 100 up), the
// seconds-scale rows. Each was proven on a copy of duration.go through go test -overlay, the tree
// untouched (run 19/10/2026).
func TestDurationReadsOneNumberAndOneUnit(t *testing.T) {
	const (
		twoUnits   = "có hai đơn vị, .ui viết một số và một đơn vị (ms, s, m hay h)"
		threeUnits = "có 3 đơn vị, .ui viết một số và một đơn vị (ms, s, m hay h)"
		iso        = "là cách viết ISO 8601, .ui viết một số và một đơn vị (ms, s, m hay h)"
		spaced     = "có khoảng trắng trước đơn vị, .ui viết số liền đơn vị"
	)
	foreign := func(written, unit string) string { return "có đơn vị " + written + ", .ui viết là " + unit }
	for _, c := range []struct {
		value   string
		problem durationProblem
		ms      float64
		// the suggestion on the milliseconds scale and on the seconds scale; reason, for two units and a
		// foreign spelling
		milliseconds, seconds, reason string
	}{
		{"800ms", durationRead, 800, "800ms", "800ms", ""},
		{"3s", durationRead, 3000, "3s", "3s", ""},
		{"4.03s", durationRead, 4030, "4.03s", "4.03s", ""},
		{"1.5m", durationRead, 90000, "1.5m", "1.5m", ""},
		{"0.05m", durationRead, 3000, "0.05m", "0.05m", ""},
		{"2h", durationRead, 7200000, "2h", "2h", ""},
		{"0.01h", durationRead, 36000, "0.01h", "0.01h", ""},

		{"-3s", durationNegative, 3000, "3s", "3s", ""},
		{"-2m", durationNegative, 120000, "2m", "2m", ""},
		{"-300", durationNegative, 0, "300ms", "300s", ""},
		{"-1m30s", durationNegative, 90000, "90s", "90s", ""},
		{"-0", durationNegative, 0, "0s", "0s", ""},

		{"0", durationZero, 0, "0s", "0s", ""},
		{"0.0", durationZero, 0, "0.0s", "0.0s", ""},
		{"0s", durationZero, 0, "0s", "0s", ""},
		{"0ms", durationZero, 0, "0ms", "0ms", ""},
		{"0m", durationZero, 0, "0m", "0m", ""},
		{"0h", durationZero, 0, "0h", "0h", ""},

		{"3", durationNoUnit, 0, "3s", "3s", ""},
		{"2.5", durationNoUnit, 0, "2.5s", "2.5s", ""},
		{"99", durationNoUnit, 0, "99s", "99s", ""},
		{"100", durationNoUnit, 0, "100ms", "100s", ""},
		{"300", durationNoUnit, 0, "300ms", "300s", ""},

		{"1m30s", durationTwoUnits, 90000, "90s", "90s", twoUnits},
		{"1h 30m", durationTwoUnits, 5400000, "90m", "90m", twoUnits},
		{"1s500ms", durationTwoUnits, 1500, "1500ms", "1500ms", twoUnits},
		{"30s1m", durationTwoUnits, 90000, "90s", "90s", twoUnits},
		{"1h30m15s", durationTwoUnits, 5415000, "5415s", "5415s", threeUnits},
		{"1 giờ 30 phút", durationTwoUnits, 5400000, "90m", "90m", twoUnits},

		{"5p", durationForeign, 300000, "5m", "5m", foreign("p", "m")},
		{"5phút", durationForeign, 300000, "5m", "5m", foreign("phút", "m")},
		{"5 phút", durationForeign, 300000, "5m", "5m", foreign("phút", "m")},
		{"5phut", durationForeign, 300000, "5m", "5m", foreign("phut", "m")},
		{"5min", durationForeign, 300000, "5m", "5m", foreign("min", "m")},
		{"5Min", durationForeign, 300000, "5m", "5m", foreign("Min", "m")},
		{"5minutes", durationForeign, 300000, "5m", "5m", foreign("minutes", "m")},
		{"2giờ", durationForeign, 7200000, "2h", "2h", foreign("giờ", "h")},
		{"2g", durationForeign, 7200000, "2h", "2h", foreign("g", "h")},
		{"2hr", durationForeign, 7200000, "2h", "2h", foreign("hr", "h")},
		{"1hour", durationForeign, 3600000, "1h", "1h", foreign("hour", "h")},
		{"2 tiếng", durationForeign, 7200000, "2h", "2h", foreign("tiếng", "h")},
		{"30giây", durationForeign, 30000, "30s", "30s", foreign("giây", "s")},
		{"30sec", durationForeign, 30000, "30s", "30s", foreign("sec", "s")},
		{"10seconds", durationForeign, 10000, "10s", "10s", foreign("seconds", "s")},
		{"500msec", durationForeign, 500, "500ms", "500ms", foreign("msec", "ms")},
		{"5S", durationForeign, 5000, "5s", "5s", foreign("S", "s")},
		{"5MS", durationForeign, 5, "5ms", "5ms", foreign("MS", "ms")},
		{"5 s", durationForeign, 5000, "5s", "5s", spaced},
		{"PT5M", durationForeign, 300000, "5m", "5m", iso},
		{"pt2h", durationForeign, 7200000, "2h", "2h", iso},
		{"PT0.5S", durationForeign, 500, "0.5s", "0.5s", iso},
		{"PT1M30S", durationForeign, 90000, "90s", "90s", iso},
		{"P1D", durationForeign, 86400000, "24h", "24h", iso},

		{"fast", durationNotOne, 0, "", "", ""},
		{".5s", durationNotOne, 0, "", "", ""},
		{"5d", durationNotOne, 0, "", "", ""},
		{"5px", durationNotOne, 0, "", "", ""},
		{"1m30", durationNotOne, 0, "", "", ""},
		{"1,5s", durationNotOne, 0, "", "", ""},
		{"5s!", durationNotOne, 0, "", "", ""},
		{"PT", durationNotOne, 0, "", "", ""},
		{"P1M", durationNotOne, 0, "", "", ""},
		{"-fast", durationNotOne, 0, "", "", ""},
		{"", durationNotOne, 0, "", "", ""},
	} {
		d := readDuration(c.value)
		if d.problem != c.problem || d.ms != c.ms {
			t.Errorf("%q: got problem %d, %v ms; want problem %d, %v ms", c.value, d.problem, d.ms, c.problem, c.ms)
			continue
		}
		if c.problem == durationNotOne {
			continue
		}
		if got := d.suggestion(durationMilliseconds); got != c.milliseconds {
			t.Errorf("%q on the milliseconds scale: got %q, want %q", c.value, got, c.milliseconds)
		}
		if got := d.suggestion(durationSeconds); got != c.seconds {
			t.Errorf("%q on the seconds scale: got %q, want %q", c.value, got, c.seconds)
		}
		if c.reason != "" {
			if got := d.reason(); got != c.reason {
				t.Errorf("%q: reason %q, want %q", c.value, got, c.reason)
			}
		}
	}
	// digits too many to hold are a time longer than any range, never a small one
	if d := readDuration("1" + strings.Repeat("0", 400) + "s"); d.problem != durationRead || !math.IsInf(d.ms, 1) {
		t.Errorf("400 digits: got problem %d, %v ms; want a reading of +Inf", d.problem, d.ms)
	}
}

// Minutes and hours are durations on every component that reads one, inside that component's range:
// the carousel's 2s to 60s (0.5m and 1m play, quietly; 2m and 1h are longer than its maximum, told so,
// not "no duration"), the network's delay up to 10s and duration up to 60s (0.1m, 1m, 0m), the
// terminal's typing (0.05m types quietly; 1m types and is told it moves for longer than five seconds).
// CONTROL: with m and h left out of durationUnits (a copy of duration.go through go test -overlay), every
// row went red, each one its own subtest: each value was "không phải một thời lượng" (run 19/10/2026).
func TestDurationMinutesAndHoursOnEachComponent(t *testing.T) {
	// the carousel: within its range they reach the instance; past it, its range's message
	for _, c := range []struct{ written, ms string }{{"0.5m", "30000"}, {"1m", "60000"}, {"0.01h", "36000"}} {
		t.Run("carousel "+c.written, func(t *testing.T) {
			html := carouselQuiet(t, "", `<content>`+carouselOf(` duration="`+c.written+`"`, carouselSlides+carouselStop)+`</content>`, nil)
			mustContain(t, html, `data-kitwork-scope="duration: `+c.ms+`">`)
		})
	}
	for _, written := range []string{"2m", "1.5m", "1h"} {
		t.Run("carousel "+written, func(t *testing.T) {
			source := `<content>` + carouselOf(` duration="`+written+`"`, carouselSlides+carouselStop) + `</content>`
			mustFailExactly(t, content(source), where("content.ui", source, `duration=`)+
				`duration="`+written+`" dài hơn 60 giây: mỗi trang ở lại tới khi người xem chuyển thì bỏ duration`)
		})
	}

	// the network
	for _, c := range []struct{ words, scope string }{
		{` delay="0.1m"`, ` data-kitwork-scope="delay: 6000"`},
		{` duration="1m"`, ` data-kitwork-scope="duration: 60000"`},
		{` delay="0m" duration="0h"`, ` data-kitwork-scope="delay: 0; duration: 0"`},
	} {
		t.Run("network"+c.words, func(t *testing.T) {
			host, _, warned := networkCompile(t, `<content>`+networkWithNotice(` as="$network"`+c.words)+`</content>`)
			if want := `<div class="flex-column" data-kitwork-component="network: $network" role="status"` + c.scope + `>`; host != want || warned != "" {
				t.Errorf("%q:\n got  %s (warnings %q)\n want %s", c.words, host, warned, want)
			}
		})
	}
	for _, c := range []struct{ words, needle, says string }{
		{` delay="1m"`, `delay=`, `delay="1m" dài hơn 10 giây: mất mạng lâu vậy mà không báo thì người xem không biết vì sao trang không chạy; viết delay="10s" hay ngắn hơn`},
		{` duration="2m"`, `duration=`, `duration="2m" dài hơn 60 giây: khung ở lại tới khi đóng thì viết duration="0"`},
		{` duration="0.5h"`, `duration=`, `duration="0.5h" dài hơn 60 giây: khung ở lại tới khi đóng thì viết duration="0"`},
	} {
		t.Run("network"+c.words, func(t *testing.T) {
			source := `<content>` + networkWithNotice(c.words) + `</content>`
			if got, want := compileError(t, content(source)), where("content.ui", source, c.needle)+c.says; got != want {
				t.Errorf("%q:\n got  %s\n want %s", c.words, got, want)
			}
		})
	}

	// the terminal: no longest, only the five seconds it is told past
	terminal := func(attribute string) string {
		return `<content><terminal ` + attribute + `><code>kitwork dev</code></terminal></content>`
	}
	for attribute, scope := range map[string]string{`typing="0.05m"`: "3000", `typing="0.001h"`: "3600"} {
		t.Run("terminal "+attribute, func(t *testing.T) {
			page := terminalPage(t, terminal(attribute))
			mustContain(t, page.Render(Request{Path: "/"}), ` data-kitwork-scope="typing: `+scope+`"`)
			if warned := terminalWarnings(page); warned != "" {
				t.Errorf("%s says nothing:\n%s", attribute, warned)
			}
		})
	}
	t.Run(`terminal typing="1m"`, func(t *testing.T) {
		long := terminalPage(t, terminal(`typing="1m"`))
		mustContain(t, long.Render(Request{Path: "/"}), ` data-kitwork-scope="typing: 60000"`)
		if got, want := terminalWarnings(long), where("content.ui", terminal(`typing="1m"`), "typing")+
			`typing="1m" gõ lâu hơn 5 giây: chuyển động tự chạy quá 5 giây cần một cách để dừng (WCAG 2.2.2 Pause, Stop, Hide); viết typing="5s" hoặc ngắn hơn`; got != want {
			t.Errorf("typing a minute:\n got  %s\n want %s", got, want)
		}
	})
}

// durationToast is a toast whose blueprint draws all a call may say, so the only thing a call is told is
// its duration (TestToastCallsAreRead's).
const durationToast = `<toast as="$toast"><card for="notice of notices; key: notice.id">` +
	`<icon show="notice.tone == 'danger'" value="alert-circle" color="danger" />` +
	`<text show="notice.title" weight="bold">{{ notice.title }}</text><text>{{ notice.message }}</text>` +
	`<button show="notice.action.click" click="act(notice.id)">{{ notice.action.label }}</button>` +
	`<link show="notice.action.link" value="{{ notice.action.link }}">{{ notice.action.label }}</link>` +
	`<button click="dismiss(notice.id)"><icon value="x" alt="Đóng" /></button></card></toast>`

// The toast's duration goes to the browser as written, where the toast reads s and ms only: a duration
// in m or h is told its seconds (zero too: 0m is 0s), and one longer than the toast's 60 seconds its
// range's message first. Within s and ms nothing changed: 30s compiles quietly.
// CONTROL: with the m-and-h check of toastDuration left out (a copy of toast.go through go test
// -overlay), 1m, 0.5m and 0m compiled and their rows went red (run 19/10/2026).
func TestToastDurationInMinutesIsToldItsSeconds(t *testing.T) {
	call := func(click string) map[string]string {
		return toastSite(durationToast, `<content><button click="`+click+`">x</button></content>`)
	}
	compileQuiet(t, call(`$toast.show('x', { duration: '30s' })`))
	for _, c := range []struct{ click, says string }{
		{`$toast.show('x', { duration: '1m' })`, "duration: '1m': thông báo đọc thời lượng bằng s hay ms: viết duration: '60s'"},
		{`$toast.show('x', { duration: '0.5m' })`, "duration: '0.5m': thông báo đọc thời lượng bằng s hay ms: viết duration: '30s'"},
		{`$toast.show('x', { duration: '0m' })`, "duration: '0m': thông báo đọc thời lượng bằng s hay ms: viết duration: '0s'"},
		{`$toast.show('x', { duration: '2m' })`, "duration: '2m' dài hơn 60 giây: thông báo ở lại tới khi đóng thì viết duration: 0"},
		{`$toast.show('x', { duration: '1h' })`, "duration: '1h' dài hơn 60 giây: thông báo ở lại tới khi đóng thì viết duration: 0"},
	} {
		t.Run(c.click, func(t *testing.T) {
			source := call(c.click)
			mustFail(t, source, where("content.ui", source["/site/content.ui"], `click="$toast`), c.says)
		})
	}
}

// Two units and a foreign spelling are told on every component, in its own words, with the one
// duration to write: two units said again in the smallest (1m30s is 90s, 2s500ms is 2500ms), a foreign
// unit in .ui's (5phút is 5m, 1p is 1m, 5giây is 5s, 1min is 1m), the ISO form as plain (PT5M is 5m), a
// space before the unit closed up (3 giây is 3s). The toast, which reads s and ms only, is told its
// seconds (5phút is 300s, 1p is 60s).
// CONTROL: with two units read as no duration, the two-unit rows went red ("không phải một thời
// lượng"); with durationForeignUnits empty and the ISO form not read, the foreign and ISO rows did; each
// on a copy of duration.go through go test -overlay (run 19/10/2026).
func TestDurationTwoUnitsAndForeignUnitsOnEachComponent(t *testing.T) {
	const (
		twoUnits = "có hai đơn vị, .ui viết một số và một đơn vị (ms, s, m hay h)"
		iso      = "là cách viết ISO 8601, .ui viết một số và một đơn vị (ms, s, m hay h)"
	)
	foreign := func(written, unit string) string { return "có đơn vị " + written + ", .ui viết là " + unit }

	for _, c := range []struct{ written, why, write string }{
		{"2s500ms", twoUnits, "2500ms"},
		{"1p", foreign("p", "m"), "1m"},
		{"5giây", foreign("giây", "s"), "5s"},
		{"PT10S", iso, "10s"},
	} {
		source := `<content>` + carouselOf(` duration="`+c.written+`"`, carouselSlides+carouselStop) + `</content>`
		mustFailExactly(t, content(source), where("content.ui", source, `duration=`)+
			`duration="`+c.written+`" `+c.why+`: viết duration="`+c.write+`"`)
	}

	for _, c := range []struct{ words, needle, says string }{
		{` delay="1s500ms"`, `delay=`, `delay="1s500ms" ` + twoUnits + `: viết delay="1500ms"`},
		{` duration="1m30s"`, `duration=`, `duration="1m30s" ` + twoUnits + `: viết duration="90s"`},
		{` delay="5giây"`, `delay=`, `delay="5giây" ` + foreign("giây", "s") + `: viết delay="5s"`},
		{` duration="1min"`, `duration=`, `duration="1min" ` + foreign("min", "m") + `: viết duration="1m"`},
		{` duration="PT30S"`, `duration=`, `duration="PT30S" ` + iso + `: viết duration="30s"`},
	} {
		source := `<content>` + networkWithNotice(c.words) + `</content>`
		if got, want := compileError(t, content(source)), where("content.ui", source, c.needle)+c.says; got != want {
			t.Errorf("%q:\n got  %s\n want %s", c.words, got, want)
		}
	}

	for attribute, says := range map[string]string{
		`typing="1m30s"`:  `typing="1m30s" ` + twoUnits + `: viết typing="90s"`,
		`typing="5phút"`:  `typing="5phút" ` + foreign("phút", "m") + `: viết typing="5m"`,
		`typing="3 giây"`: `typing="3 giây" ` + foreign("giây", "s") + `: viết typing="3s"`,
		`typing="PT5M"`:   `typing="PT5M" ` + iso + `: viết typing="5m"`,
	} {
		source := `<content><terminal ` + attribute + `><code>kitwork dev</code></terminal></content>`
		if got, want := terminalError(t, source), where("content.ui", source, attribute)+says; got != want {
			t.Errorf("%s:\n got  %s\n want %s", attribute, got, want)
		}
	}

	for _, c := range []struct{ click, says string }{
		{`$toast.show('x', { duration: '1m30s' })`, "duration: '1m30s' " + twoUnits + ": viết duration: '90s'"},
		{`$toast.show('x', { duration: '5phút' })`, "duration: '5phút' " + foreign("phút", "m") + ": viết duration: '300s'"},
		{`$toast.show('x', { duration: '1p' })`, "duration: '1p' " + foreign("p", "m") + ": viết duration: '60s'"},
		{`$toast.show('x', { duration: '5giây' })`, "duration: '5giây' " + foreign("giây", "s") + ": viết duration: '5s'"},
		{`$toast.show('x', { duration: 'PT5S' })`, "duration: 'PT5S' " + iso + ": viết duration: '5s'"},
	} {
		source := toastSite(durationToast, `<content><button click="`+c.click+`">x</button></content>`)
		mustFail(t, source, where("content.ui", source["/site/content.ui"], `click="$toast`), c.says)
	}
}
