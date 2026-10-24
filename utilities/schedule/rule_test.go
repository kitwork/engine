package schedule

import (
	"strings"
	"testing"
	"time"
)

func text(s string) Arg     { return Arg{Kind: String, Text: s} }
func number(n float64) Arg  { return Arg{Kind: Number, Number: n} }
func handler() Arg          { return Arg{Kind: Function} }
func at(s string) time.Time { return mustParse(s) }
func mustParse(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

// call is one call of a chain: the word and its arguments.
type call struct {
	word string
	args []Arg
}

// chain declares a rule, its first call schedule.<word>(…) and the rest chained after it; the error is the
// first a call gave.
func chain(calls ...call) (*Rule, error) {
	c, err := Begin(calls[0].word, calls[0].args)
	if err != nil {
		return nil, err
	}
	for _, next := range calls[1:] {
		if err := c.Then(next.word, next.args); err != nil {
			return nil, err
		}
	}
	return c.Rule(), nil
}

func is(word string, args ...Arg) call { return call{word, args} }

// The next and the last due time of each kind of rule, in UTC, in Asia/Ho_Chi_Minh (UTC+7, no summer
// time) and in America/New_York across its changes of summer time. 23/10/2026 is a Friday; October has 31
// days, November 30. CONTROL (validated by disabling the code): with the zone ignored (the old engine counted
// every schedule in UTC), daily("09:00") in Ho Chi Minh is 09:00Z instead of 02:00Z; with the move of .before
// ignored, lastday("09:00").before(3) is the 31st; with every() truncated from Go's zero time (the slice's
// first grid), every("7m") falls on 00:01 and every("5h") on 01:00 of Ho Chi Minh, and every("1d") in New
// York is 23:00 the day summer time ends.
func TestRuleNextAndLatest(t *testing.T) {
	saigon := call{"timezone", []Arg{text("Asia/Ho_Chi_Minh")}}
	newYork := call{"timezone", []Arg{text("America/New_York")}}
	for _, test := range []struct {
		name   string
		calls  []call
		from   string
		next   string
		latest string
	}{
		{"daily in UTC", []call{is("daily", text("09:00"))}, "2026-10-23T08:00:00Z", "2026-10-23T09:00:00Z", "2026-10-22T09:00:00Z"},
		{"daily, the due time itself is not next", []call{is("daily", text("09:00"))}, "2026-10-23T09:00:00Z", "2026-10-24T09:00:00Z", "2026-10-23T09:00:00Z"},
		{"daily in Ho Chi Minh", []call{is("daily", text("09:00")), saigon}, "2026-10-23T03:00:00Z", "2026-10-24T02:00:00Z", "2026-10-23T02:00:00Z"},
		{"daily, two times", []call{is("daily", text("18:00"), text("09:00")), saigon}, "2026-10-23T03:00:00Z", "2026-10-23T11:00:00Z", "2026-10-23T02:00:00Z"},
		{"lastday before 3", []call{is("lastday", text("09:00")), is("before", number(3)), saigon}, "2026-10-23T03:00:00Z", "2026-10-28T02:00:00Z", "2026-09-27T02:00:00Z"},
		{"lastday before 3, the next month", []call{is("lastday", text("09:00")), is("before", number(3)), saigon}, "2026-10-28T02:00:00Z", "2026-11-27T02:00:00Z", "2026-10-28T02:00:00Z"},
		{"lastday before 3, February", []call{is("lastday", text("09:00")), is("before", number(3))}, "2027-02-01T00:00:00Z", "2027-02-25T09:00:00Z", "2027-01-28T09:00:00Z"},
		// a move past the end of the month stays the month it came from: October's is 1 November, November's 1 December
		{"lastday after 1 crosses the month", []call{is("lastday", text("09:00")), is("after", number(1)), saigon}, "2026-10-31T03:00:00Z", "2026-11-01T02:00:00Z", "2026-10-01T02:00:00Z"},
		{"lastday after 1, once a month", []call{is("lastday", text("09:00")), is("after", number(1)), saigon}, "2026-11-01T02:00:00Z", "2026-12-01T02:00:00Z", "2026-11-01T02:00:00Z"},
		{"lastday", []call{is("lastday", text("23:00"))}, "2026-11-01T00:00:00Z", "2026-11-30T23:00:00Z", "2026-10-31T23:00:00Z"},
		{"firstday", []call{is("firstday", text("09:00"))}, "2026-10-23T00:00:00Z", "2026-11-01T09:00:00Z", "2026-10-01T09:00:00Z"},
		{"doubleday after 1", []call{is("doubleday", text("09:00")), is("after", number(1))}, "2026-12-13T10:00:00Z", "2027-01-02T09:00:00Z", "2026-12-13T09:00:00Z"},
		{"doubleday", []call{is("doubleday", text("09:00"))}, "2026-10-23T00:00:00Z", "2026-11-11T09:00:00Z", "2026-10-10T09:00:00Z"},
		{"monday first of the month", []call{is("monday", text("09:00")), is("first", text("month"))}, "2026-10-23T00:00:00Z", "2026-11-02T09:00:00Z", "2026-10-05T09:00:00Z"},
		{"friday last of the month", []call{is("friday", text("17:00")), is("last", text("month"))}, "2026-10-23T00:00:00Z", "2026-10-30T17:00:00Z", "2026-09-25T17:00:00Z"},
		{"friday last of the month, before 1", []call{is("friday", text("17:00")), is("last", text("month")), is("before", number(1))}, "2026-10-23T00:00:00Z", "2026-10-29T17:00:00Z", "2026-09-24T17:00:00Z"},
		{"friday", []call{is("friday", text("17:00"))}, "2026-10-23T18:00:00Z", "2026-10-30T17:00:00Z", "2026-10-23T17:00:00Z"},
		{"weekly", []call{is("weekly", text("monday"), text("friday")), is("at", text("09:00"))}, "2026-10-23T10:00:00Z", "2026-10-26T09:00:00Z", "2026-10-23T09:00:00Z"},
		{"monthly 31 skips a month of 30", []call{is("monthly", number(31)), is("at", text("09:00"))}, "2026-10-31T10:00:00Z", "2026-12-31T09:00:00Z", "2026-10-31T09:00:00Z"},
		{"monthly 1 and 15", []call{is("monthly", number(15), number(1)), is("at", text("09:00"))}, "2026-10-23T00:00:00Z", "2026-11-01T09:00:00Z", "2026-10-15T09:00:00Z"},
		{"hourly 5 starts again each day", []call{is("hourly", number(5))}, "2026-10-23T21:00:00Z", "2026-10-24T00:00:00Z", "2026-10-23T20:00:00Z"},
		{"every 30s", []call{is("every", text("30s"))}, "2026-10-23T03:00:05Z", "2026-10-23T03:00:30Z", "2026-10-23T03:00:00Z"},
		{"every 30s in Ho Chi Minh is on :00 and :30 too", []call{is("every", text("30s")), saigon}, "2026-10-23T03:00:29Z", "2026-10-23T03:00:30Z", "2026-10-23T03:00:00Z"},
		{"every 2h on the zone's even hours", []call{is("every", text("2h")), saigon}, "2026-10-23T03:30:00Z", "2026-10-23T05:00:00Z", "2026-10-23T03:00:00Z"},
		// an interval that does not divide a day starts again at midnight of the zone, as hourly(n) does
		{"every 7m starts at midnight", []call{is("every", text("7m")), saigon}, "2026-10-22T17:03:00Z", "2026-10-22T17:07:00Z", "2026-10-22T17:00:00Z"},
		{"every 7m, the last of the day", []call{is("every", text("7m")), saigon}, "2026-10-23T16:57:00Z", "2026-10-23T17:00:00Z", "2026-10-23T16:55:00Z"},
		{"every 5h is hourly 5", []call{is("every", text("5h")), saigon}, "2026-10-22T17:30:00Z", "2026-10-22T22:00:00Z", "2026-10-22T17:00:00Z"},
		{"every 1d at midnight of the zone", []call{is("every", text("1d")), saigon}, "2026-10-23T03:00:00Z", "2026-10-23T17:00:00Z", "2026-10-22T17:00:00Z"},
		{"every 1w on Mondays", []call{is("every", text("1w"))}, "2026-10-23T00:00:00Z", "2026-10-26T00:00:00Z", "2026-10-19T00:00:00Z"},
		// summer time in New York ends 1/11/2026 at 02:00 (EDT, -04:00, to EST, -05:00) and begins 8/3/2026
		{"every 1d, the day summer time ends", []call{is("every", text("1d")), newYork}, "2026-11-01T04:00:00Z", "2026-11-02T05:00:00Z", "2026-11-01T04:00:00Z"},
		{"every 1d, the day summer time begins", []call{is("every", text("1d")), newYork}, "2026-03-08T05:00:00Z", "2026-03-09T04:00:00Z", "2026-03-08T05:00:00Z"},
		{"every 2h counts the time that passes after the change", []call{is("every", text("2h")), newYork}, "2026-11-01T04:00:00Z", "2026-11-01T06:00:00Z", "2026-11-01T04:00:00Z"},
		{"every 2h starts again at midnight after the change", []call{is("every", text("2h")), newYork}, "2026-11-02T04:30:00Z", "2026-11-02T05:00:00Z", "2026-11-02T04:00:00Z"},
		{"on, before it", []call{is("on", text("2026-12-01T09:00:00+07:00"))}, "2026-10-23T03:00:00Z", "2026-12-01T02:00:00Z", ""},
		{"on, after it", []call{is("on", text("2026-12-01T09:00:00+07:00"))}, "2026-12-01T02:00:00Z", "", "2026-12-01T02:00:00Z"},
	} {
		t.Run(test.name, func(t *testing.T) {
			rule, err := chain(test.calls...)
			if err != nil {
				t.Fatal(err)
			}
			from := at(test.from)
			next, ok := rule.Next(from)
			if got := stampOrEmpty(next, ok); got != test.next {
				t.Errorf("%s.Next(%s) = %s, want %s", rule.Text(), test.from, got, test.next)
			}
			latest, ok := rule.Latest(from)
			if got := stampOrEmpty(latest, ok); got != test.latest {
				t.Errorf("%s.Latest(%s) = %s, want %s", rule.Text(), test.from, got, test.latest)
			}
		})
	}
}

// every() has one grid, whatever the offset does: around each change of summer time (New York, an hour;
// Lord Howe, half an hour; Santiago, whose clock skips its midnight) and in a zone half an hour off UTC
// (Kolkata), for every time t of four days, the
// next due time is on the grid (Latest(Next(t)) is itself), so the dispatcher never stores a due time the
// rule never had, and nothing of the grid falls between Latest(t) and Next(t), so it never runs a due time
// late. CONTROL (validated by disabling the code): with every() truncated from Go's zero time, the offset
// read at t, the grid of New York breaks the day summer time ends: at 06:00:17Z every("7m") has Latest 01:54
// EDT and Next 01:01 EST, with 01:57 EDT of its grid between them; with the midnight Santiago skips left where
// Go's time puts it, every("1d") of 6/9/2026 falls at 23:00 of the 5th.
func TestEveryHasOneGridAcrossChangesOfOffset(t *testing.T) {
	for _, zone := range []struct {
		name string
		from []string
	}{
		{"America/New_York", []string{"2026-03-07T00:00:00Z", "2026-10-30T00:00:00Z"}},
		{"Australia/Lord_Howe", []string{"2026-04-03T00:00:00Z", "2026-10-02T00:00:00Z"}},
		{"America/Santiago", []string{"2026-09-04T00:00:00Z", "2026-04-02T00:00:00Z"}},
		{"Asia/Kolkata", []string{"2026-10-22T00:00:00Z"}},
	} {
		for _, interval := range []string{"30s", "7m", "45m", "1h", "2h", "5h", "1d", "3d", "1w"} {
			rule, err := chain(is("every", text(interval)), is("timezone", text(zone.name)))
			if err != nil {
				t.Fatal(err)
			}
			for _, from := range zone.from {
				start := at(from)
				for step := time.Duration(0); step < 4*oneDay; step += 7*time.Minute + 13*time.Second {
					moment := start.Add(step)
					latest, _ := rule.Latest(moment)
					next, _ := rule.Next(moment)
					onGrid, _ := rule.Latest(next)
					after, _ := rule.Next(latest)
					switch {
					case latest.After(moment) || !next.After(moment):
						t.Fatalf("every(%q) in %s at %s: latest %s, next %s", interval, zone.name, moment, latest, next)
					case !onGrid.Equal(next):
						t.Fatalf("every(%q) in %s at %s: next %s is not on the grid, Latest(next) = %s", interval, zone.name, moment, next.In(rule.Location), onGrid.In(rule.Location))
					case !after.Equal(next):
						t.Fatalf("every(%q) in %s at %s: Next(latest %s) = %s, but Next = %s", interval, zone.name, moment, latest.In(rule.Location), after.In(rule.Location), next.In(rule.Location))
					case rule.Interval >= oneDay && !next.Equal(dayStart(civil(next.In(rule.Location).Date()), rule.Location)):
						t.Fatalf("every(%q) in %s: next %s is not when its day begins", interval, zone.name, next.In(rule.Location))
					}
				}
			}
		}
	}
}

func stampOrEmpty(t time.Time, ok bool) string {
	if !ok {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// What list() shows of a rule: as written, without the rules of the runs.
func TestRuleText(t *testing.T) {
	for want, calls := range map[string][]call{
		`daily("09:00", "18:00")`:                 {is("daily", text("18:00"), text("9:00")), is("retry", number(3))},
		`lastday("09:00").before(3)`:              {is("lastday", text("09:00")), is("before", number(3))},
		`weekly("monday", "friday").at("09:00")`:  {is("weekly", text("monday"), text("friday")), is("at", text("09:00"))},
		`monthly(1, 15).at("09:00")`:              {is("monthly", number(1), number(15)), is("at", text("09:00"))},
		`friday("17:00").last("month").before(1)`: {is("friday", text("17:00")), is("last", text("month")), is("before", number(1))},
		`every("30s")`:                            {is("every", text("30s")), is("timezone", text("Asia/Ho_Chi_Minh"))},
		`hourly(2)`:                               {is("hourly", number(2))},
		`on("2026-12-01T09:00:00+07:00")`:         {is("on", text("2026-12-01T09:00:00+07:00"))},
		`doubleday("09:00").after(1)`:             {is("doubleday", text("09:00")), is("after", number(1))},
	} {
		rule, err := chain(calls...)
		if err != nil {
			t.Fatalf("%s: %v", want, err)
		}
		if got := rule.Text(); got != want {
			t.Errorf("Text() = %s, want %s", got, want)
		}
	}
}

// The errors of KITWORK_SCHEDULE.md mục 11, and the old spellings of cron, each in the words the author
// reads. A chain written right is the control: it gives no error.
func TestChainErrors(t *testing.T) {
	for _, test := range []struct {
		name  string
		calls []call
		want  string
	}{
		{"control", []call{is("daily", text("09:00")), is("retry", number(3)), is("timeout", text("10m")), is("run", handler())}, ""},
		{"25:00", []call{is("daily", text("25:00")), is("run", handler())}, `giờ "25:00" sai: viết "HH:MM", từ "00:00" tới "23:59"`},
		{"weekly without at", []call{is("weekly", text("monday")), is("run", handler())}, `weekly(…) thiếu .at(…): giờ trong ngày đi qua .at, schedule.weekly("monday").at("09:00").run(fn)`},
		{"monthly without at", []call{is("monthly", number(1)), is("once", handler())}, `monthly(…) thiếu .at(…): giờ trong ngày đi qua .at, schedule.monthly(1).at("09:00").once(fn)`},
		{"daily at", []call{is("daily", text("09:00")), is("at", text("10:00"))}, `giờ đã là tham số của daily: daily("09:00", "18:00"), không nối .at(…)`},
		{"monthly 0", []call{is("monthly", number(0)), is("at", text("09:00"))}, "monthly(0): ngày sai, monthly nhận ngày trong tháng, số nguyên từ 1 tới 31"},
		{"first and last", []call{is("monday", text("09:00")), is("first", text("month")), is("last", text("month"))}, "first và last không đi cùng nhau: một lịch là lần đầu hay lần cuối của tháng"},
		{"before and after", []call{is("lastday", text("09:00")), is("before", number(1)), is("after", number(1))}, "before và after không đi cùng nhau: một lịch dời một chiều"},
		{"first on daily", []call{is("daily", text("09:00")), is("first", text("month"))}, `.first("month") chỉ đi với một thứ trong tuần: schedule.monday("09:00").first("month")`},
		{"on with run", []call{is("on", text("2026-12-01T09:00:00+07:00")), is("run", handler())}, `schedule.on(…) là một thời điểm, chỉ đi với .once(handler): schedule.on("2026-12-01T09:00:00+07:00").once(fn)`},
		{"two ends", []call{is("daily", text("09:00")), is("once", handler()), is("run", handler())}, ".run và .once loại trừ nhau và đứng cuối: lịch đã kết thúc bằng .once(…), sau đó không nối gì"},
		{"once without a handler", []call{is("daily", text("09:00")), is("once")}, ".once(handler) nhận một hàm: .once((context) => { … })"},
		{"a rule after run", []call{is("daily", text("09:00")), is("run", handler()), is("retry", number(1))}, "sau .run(…) không nối gì: .retry viết trước .run(…)"},
		{"on without a zone", []call{is("on", text("2026-12-01T09:00:00"))}, `schedule.on("2026-12-01T09:00:00"): thời điểm viết đủ ngày, giờ và múi giờ, như "2026-12-01T09:00:00+07:00"`},
		{"timezone not IANA", []call{is("daily", text("09:00")), is("timezone", text("Mars/Base"))}, `.timezone("Mars/Base"): không phải múi giờ IANA: .timezone("Asia/Ho_Chi_Minh"), "UTC", "Europe/Paris"`},
		{"timezone Local", []call{is("daily", text("09:00")), is("timezone", text("Local"))}, `.timezone("Local"): múi giờ là một tên IANA, không theo đồng hồ của máy chủ: .timezone("Asia/Ho_Chi_Minh")`},
		{"retry twice", []call{is("daily", text("09:00")), is("retry", number(1)), is("retry", number(2))}, ".retry nối hai lần: một lịch một luật, .retry(3)"},
		{"retry negative", []call{is("daily", text("09:00")), is("retry", number(-1))}, ".retry(n) nhận số lần thử thêm, số nguyên từ 0 tới 100: .retry(3) là bốn lần tổng cộng"},
		{"jobs", []call{is("jobs")}, "schedule.jobs() không có: các lịch là schedule.list(), các lần chạy là schedule.runs()"},
		{"a rule chained on a rule", []call{is("daily", text("09:00")), is("weekly", text("monday"))}, ".weekly là một lịch, không nối sau lịch khác: mỗi lịch một quy tắc giờ, schedule.weekly(…)"},
		// the old spellings of cron
		{"raw cron", []call{is("cron", text("*/5 * * * *"))}, "schedule.cron đã bỏ: không có biểu thức cron thô"},
		{"handle", []call{is("daily", text("08:00")), is("handle", handler())}, ".handle(fn) đã bỏ: lịch kết thúc bằng .run(fn)"},
		{"inline handler", []call{is("daily", text("08:00"), handler())}, "handler đi sau, trong .run(fn), không phải tham số của daily"},
		{"daily of nothing", []call{is("daily")}, `daily() thiếu giờ: daily("09:00")`},
		{"keep", []call{is("daily", text("08:00")), is("keep", text("90d"))}, `.keep đã bỏ: thời gian giữ lịch sử là luật của cả .schedule/, một dòng trong .schedule/config.work: schedule.keep("7d")`},
		{"overlap", []call{is("every", text("1m")), is("overlap", text("skip"))}, ".overlap đã bỏ: lần tới giờ khi lần trước chưa xong còn mở"},
		{"success", []call{is("every", text("1m")), is("success", handler())}, ".success đã bỏ"},
		{"error", []call{is("every", text("1m")), is("error", handler())}, `.error đã bỏ: các lần chạy thất bại đọc bằng schedule.status("failed").runs()`},
		{"misfire", []call{is("every", text("1m")), is("misfire", text("skip"))}, ".misfire đã bỏ"},
		{"weekly with a time", []call{is("weekly", text("monday 09:00"))}, `weekly("monday 09:00"): giờ đi qua .at(…): schedule.weekly("monday").at("09:00")`},
		{"monthly with a time", []call{is("monthly", text("15th 09:00"))}, `monthly("15th 09:00"): ngày là số, giờ đi qua .at(…): schedule.monthly(15).at("09:00")`},
		{"a short day", []call{is("weekly", text("mon")), is("at", text("09:00"))}, `thứ "mon": viết đủ tên, "monday"`},
		{"every a time of day", []call{is("every", text("08:00"))}, `every("08:00"): every nhận một khoảng ("30m", "2h"); giờ trong ngày là daily("08:00")`},
		{"every milliseconds", []call{is("every", text("5000"))}, `every nhận một khoảng viết bằng chuỗi có đơn vị: every("30s")`},
		{"every a number", []call{is("every", number(5000))}, `every nhận một khoảng viết bằng chuỗi có đơn vị: every("30s")`},
		{"every a month", []call{is("every", text("1mo"))}, `every("1mo"): tháng và năm không dài đều nhau; viết monthly(1).at("09:00")`},
		{"every under a second", []call{is("every", text("500ms"))}, `every("500ms"): khoảng ngắn nhất là một giây`},
		{"every a day and some hours", []call{is("every", text("36h"))}, `every("36h"): khoảng từ một ngày trở lên là số ngày tròn, tới giờ lúc nửa đêm: every("1d"), every("2d"), every("1w")`},
		{"every 25h", []call{is("every", text("25h"))}, `every("25h"): khoảng từ một ngày trở lên là số ngày tròn`},
		{"every 24h is a day", []call{is("every", text("24h")), is("run", handler())}, ""},
		{"every 48h is two days", []call{is("every", text("48h")), is("run", handler())}, ""},
		{"hourly minutes", []call{is("hourly", text("15m"))}, "hourly(n) nhận số giờ, số nguyên từ 1 tới 24"},
		{"daily a weekday", []call{is("daily", text("monday 09:00"))}, `daily("monday 09:00"): thứ trong tuần là lịch riêng, schedule.monday("09:00")`},
		{"before on daily", []call{is("daily", text("09:00")), is("before", number(1))}, ".before(n) dời theo ngày, chỉ đi với lịch theo ngày"},
		{"timezone on on", []call{is("on", text("2026-12-01T09:00:00+07:00")), is("timezone", text("UTC"))}, `schedule.on(…) mang múi giờ trong chính thời điểm`},
		{"an unknown word", []call{is("daily", text("09:00")), is("every2", text("x"))}, ".every2 không có trên lịch"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := chain(test.calls...)
			switch {
			case test.want == "" && err != nil:
				t.Fatalf("a chain written right failed: %v", err)
			case test.want != "" && err == nil:
				t.Fatalf("no error, want %q", test.want)
			case test.want != "" && !strings.Contains(err.Error(), test.want):
				t.Fatalf("error = %q\n want %q", err.Error(), test.want)
			}
		})
	}
}

// An argument the compiler cannot see (a name) checks the shape of the chain and nothing of its value.
func TestChainWithUnknownArguments(t *testing.T) {
	unknown := Arg{}
	if _, err := chain(is("daily", unknown), is("run", unknown)); err != nil {
		t.Fatalf("daily(x).run(fn): %v", err)
	}
	if _, err := chain(is("weekly", unknown), is("run", unknown)); err == nil || !strings.Contains(err.Error(), "thiếu .at(…)") {
		t.Fatalf("weekly(x).run(fn) without .at: %v", err)
	}
	if _, err := chain(is("daily", unknown), is("at", unknown)); err == nil || !strings.Contains(err.Error(), "giờ đã là tham số của daily") {
		t.Fatalf("daily(x).at(y): %v", err)
	}
}

// Stamp is the due time as context.job.at and list() write it: in the schedule's zone, its offset written.
func TestStamp(t *testing.T) {
	saigon, err := LoadZone("Asia/Ho_Chi_Minh")
	if err != nil {
		t.Fatal(err)
	}
	if got := Stamp(at("2026-10-28T02:00:00Z"), saigon); got != "2026-10-28T09:00:00+07:00" {
		t.Errorf("Stamp = %s", got)
	}
	if got := Stamp(at("2026-10-28T02:00:00Z").Add(123*time.Millisecond), nil); got != "2026-10-28T02:00:00.123Z" {
		t.Errorf("Stamp in UTC = %s", got)
	}
}
