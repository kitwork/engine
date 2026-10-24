package schedule

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ArgKind is what is known of an argument: its kind when it is a literal (the compiler) or a value (the
// runtime), Unknown when the compiler cannot see it (a name, a call).
type ArgKind int

const (
	Unknown ArgKind = iota
	String
	Number
	Function
	Other
)

// Arg is one argument of a call of a chain.
type Arg struct {
	Kind   ArgKind
	Text   string
	Number float64
}

// Chain is a schedule being declared: schedule.<rule>(…) and every call chained after it. Each call is
// checked as it is chained; the first error is the one the author reads. A Chain is used by one goroutine.
type Chain struct {
	rule Rule
	// chained is the words chained so far (at, first, last, before, after, retry, timeout, timezone).
	chained map[string]bool
	// ended is "run" or "once" once the chain ended.
	ended string
}

// RuleWords are the words of kitwork/schedule that begin a schedule: they are written only in .schedule/.
var RuleWords = map[string]bool{
	"every": true, "hourly": true, "daily": true, "weekly": true, "monthly": true,
	"doubleday": true, "firstday": true, "lastday": true, "on": true,
	"monday": true, "tuesday": true, "wednesday": true, "thursday": true, "friday": true, "saturday": true, "sunday": true,
}

// ReadWords are the words of kitwork/schedule that read: anywhere in the app.
var ReadWords = map[string]bool{"list": true, "runs": true, "status": true, "where": true}

var weekdays = map[string]time.Weekday{
	"sunday": time.Sunday, "monday": time.Monday, "tuesday": time.Tuesday, "wednesday": time.Wednesday,
	"thursday": time.Thursday, "friday": time.Friday, "saturday": time.Saturday,
}

// weekdayAbbreviations are the short names the old cron read: refused, the full name said.
var weekdayAbbreviations = map[string]string{
	"sun": "sunday", "mon": "monday", "tue": "tuesday", "wed": "wednesday", "thu": "thursday", "fri": "friday", "sat": "saturday",
}

// removedWords says what the spec writes for each word of the module the old cron had, or that the spec
// keeps out (KITWORK_SCHEDULE.md mục 3, 11, 13).
var removedWords = map[string]string{
	"cron":        `schedule.cron đã bỏ: không có biểu thức cron thô; viết daily("09:00"), weekly("monday").at("09:00"), monthly(1).at("09:00") hay every("30m")`,
	"jobs":        `schedule.jobs() không có: các lịch là schedule.list(), các lần chạy là schedule.runs()`,
	"schedule":    `schedule.schedule("tên") không có: tên lịch là tên export, export const baoCaoSang = schedule.daily("09:00").run(fn)`,
	"yearly":      `schedule.yearly chưa có: hằng năm còn là đề xuất (KITWORK_SCHEDULE.md mục 3), ngày 29/2 và múi giờ chưa có luật`,
	"lastworkday": `schedule.lastworkday chưa có: cần hợp đồng về lịch làm việc (ngày nghỉ, ngày lễ) trước (KITWORK_SCHEDULE.md mục 3)`,
	"handle":      `schedule.handle đã bỏ: lịch khai trong .schedule/<tên>.work bằng export const baoCaoSang = schedule.daily("09:00").run(fn)`,
}

// removedChained is the same for the words chained after a rule the old cron had, or the spec keeps out.
var removedChained = map[string]string{
	"handle":     `.handle(fn) đã bỏ: lịch kết thúc bằng .run(fn), hay .once(fn) cho đúng một lần`,
	"keep":       `.keep đã bỏ: thời gian giữ lịch sử là luật của cả .schedule/, một dòng trong .schedule/config.work: schedule.keep("7d")`,
	"success":    `.success đã bỏ: việc sau khi chạy xong viết ở cuối handler`,
	"error":      `.error đã bỏ: các lần chạy thất bại đọc bằng schedule.status("failed").runs(); handler biết lần thử cuối qua context.job.isFinalAttempt`,
	"overlap":    `.overlap đã bỏ: lần tới giờ khi lần trước chưa xong còn mở trong spec (KITWORK_SCHEDULE.md mục 12.4); engine bỏ lần tới giờ mới`,
	"misfire":    `.misfire đã bỏ: lần tới giờ bị lỡ khi máy tắt còn mở trong spec (KITWORK_SCHEDULE.md mục 12.3); engine chạy bù một lần, lần gần nhất`,
	"cron":       `.cron đã bỏ: không có biểu thức cron thô; viết daily("09:00"), weekly("monday").at("09:00"), monthly(1).at("09:00") hay every("30m")`,
	"occurrence": `.occurrence chưa có: thứ thứ n của tháng còn là đề xuất (KITWORK_SCHEDULE.md mục 4); có .first("month") và .last("month")`,
	"persist":    `.persist không có: mọi lịch đều lưu bền, mỗi lần tới giờ là một lần chạy trong kitwork_runs`,
	"list":       `.list() không có trên lịch: các lịch đọc bằng schedule.list()`,
}

// RemovedWord is what the spec writes for word, a word of kitwork/schedule it replaced or keeps out.
func RemovedWord(word string) (string, bool) {
	message, removed := removedWords[word]
	return message, removed
}

// RemovedChained is the same for a word chained after a rule.
func RemovedChained(word string) (string, bool) {
	message, removed := removedChained[word]
	return message, removed
}

// UnknownWord is the error of schedule.<word> the module does not have.
func UnknownWord(word string) string {
	return fmt.Sprintf("schedule.%s không có: lịch khai bằng every, hourly, daily, monday … sunday, weekly, monthly, doubleday, firstday, lastday, on; đọc bằng list, runs, status, where; .schedule/config.work gọi keep, session, database", word)
}

// Begin starts a chain: schedule.<word>(args). word is one of RuleWords; any other is an error.
func Begin(word string, args []Arg) (*Chain, error) {
	if message, removed := RemovedWord(word); removed {
		return nil, fmt.Errorf("%s", message)
	}
	if !RuleWords[word] {
		return nil, fmt.Errorf("%s", UnknownWord(word))
	}
	c := &Chain{chained: map[string]bool{}}
	c.rule.Word = word
	if err := c.begin(word, args); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Chain) begin(word string, args []Arg) error {
	r := &c.rule
	for _, arg := range args {
		if arg.Kind == Function {
			return fmt.Errorf(`handler đi sau, trong .run(fn), không phải tham số của %s: schedule.%s(…).run((context) => { … })`, word, word)
		}
	}
	switch word {
	case "every":
		r.Kind = Every
		if len(args) != 1 {
			return fmt.Errorf(`every nhận một khoảng: every("30s"), every("5m"), every("2h")`)
		}
		if args[0].Kind == Unknown {
			return nil
		}
		interval, err := parseInterval(args[0])
		if err != nil {
			return err
		}
		r.Interval, r.IntervalText = interval, strings.TrimSpace(args[0].Text)
	case "hourly":
		r.Kind = Hourly
		if len(args) != 1 {
			return fmt.Errorf(`hourly(n) nhận số giờ: hourly(1) là mỗi giờ lúc :00, hourly(2) là 00:00, 02:00, 04:00…`)
		}
		if args[0].Kind == Unknown {
			return nil
		}
		hours, ok := whole(args[0], 1, 24)
		if !ok {
			return fmt.Errorf(`hourly(n) nhận số giờ, số nguyên từ 1 tới 24: hourly(1) là mỗi giờ lúc :00, hourly(2) là 00:00, 02:00, 04:00…`)
		}
		r.Hours = hours
	case "weekly":
		r.Kind = Weekly
		if len(args) == 0 {
			return fmt.Errorf(`weekly() thiếu thứ: weekly("monday", "friday").at("09:00")`)
		}
		seen := map[time.Weekday]bool{}
		for _, arg := range args {
			if arg.Kind == Unknown {
				continue
			}
			day, err := weekdayOf(arg)
			if err != nil {
				return err
			}
			if seen[day] {
				return fmt.Errorf("weekly: thứ %q viết hai lần", strings.ToLower(day.String()))
			}
			seen[day] = true
			r.Weekdays = append(r.Weekdays, day)
		}
	case "monthly":
		r.Kind = Monthly
		if len(args) == 0 {
			return fmt.Errorf(`monthly() thiếu ngày: monthly(1, 15).at("09:00")`)
		}
		seen := map[int]bool{}
		for _, arg := range args {
			if arg.Kind == Unknown {
				continue
			}
			if arg.Kind == String {
				return fmt.Errorf(`monthly(%q): ngày là số, giờ đi qua .at(…): schedule.monthly(15).at("09:00")`, arg.Text)
			}
			day, ok := whole(arg, 1, 31)
			if !ok {
				return fmt.Errorf("monthly(%s): ngày sai, monthly nhận ngày trong tháng, số nguyên từ 1 tới 31", argText(arg))
			}
			if seen[day] {
				return fmt.Errorf("monthly: ngày %d viết hai lần", day)
			}
			seen[day] = true
			r.Days = append(r.Days, day)
		}
	case "on":
		r.Kind = On
		if len(args) != 1 {
			return fmt.Errorf(`schedule.on nhận một thời điểm: schedule.on("2026-12-01T09:00:00+07:00").once(fn)`)
		}
		if args[0].Kind == Unknown {
			return nil
		}
		text := strings.TrimSpace(args[0].Text)
		instant, err := time.Parse(time.RFC3339, text)
		if args[0].Kind != String || err != nil {
			return fmt.Errorf(`schedule.on(%s): thời điểm viết đủ ngày, giờ và múi giờ, như "2026-12-01T09:00:00+07:00"`, argText(args[0]))
		}
		// the offset as written, never the server's zone (time.Parse gives Local when the offsets match)
		_, offset := instant.Zone()
		zone := time.UTC
		if offset != 0 {
			zone = time.FixedZone(instant.Format("-07:00"), offset)
		}
		r.Instant, r.InstantText = instant.In(zone), text
	default:
		// daily, a day of the week, doubleday, firstday, lastday: the times of day are the parameters
		switch word {
		case "daily":
			r.Kind = Daily
		case "doubleday":
			r.Kind = Doubleday
		case "firstday":
			r.Kind = Firstday
		case "lastday":
			r.Kind = Lastday
		default:
			r.Kind = Weekday
			r.Weekdays = []time.Weekday{weekdays[word]}
		}
		if len(args) == 0 {
			return fmt.Errorf(`%s() thiếu giờ: %s("09:00")`, word, word)
		}
		clocks, err := clocksOf(word, args)
		if err != nil {
			return err
		}
		r.Clocks = clocks
	}
	return nil
}

// Then chains .<word>(args) after the chain. After .run or .once nothing chains.
func (c *Chain) Then(word string, args []Arg) error {
	if c.ended != "" {
		if IsEnd(word) {
			return TwoEnds(c.ended)
		}
		return fmt.Errorf("sau .%s(…) không nối gì: .%s viết trước .%s(…)", c.ended, word, c.ended)
	}
	if message, removed := RemovedChained(word); removed {
		return fmt.Errorf("%s", message)
	}
	if RuleWords[word] {
		return fmt.Errorf(".%s là một lịch, không nối sau lịch khác: mỗi lịch một quy tắc giờ, schedule.%s(…)", word, word)
	}
	r := &c.rule
	once := func() error {
		if c.chained[word] {
			return fmt.Errorf(".%s nối hai lần: một lịch một luật", word)
		}
		c.chained[word] = true
		return nil
	}
	switch word {
	case "at":
		if r.Kind != Weekly && r.Kind != Monthly {
			switch r.Kind {
			case Every, Hourly, On:
				return fmt.Errorf(`.at(…) chỉ đi với weekly(…) và monthly(…): weekly("monday").at("09:00")`)
			}
			return fmt.Errorf(`giờ đã là tham số của %s: %s("09:00", "18:00"), không nối .at(…)`, r.Word, r.Word)
		}
		if c.chained["at"] {
			return fmt.Errorf(`.at nối hai lần: mọi giờ viết trong một lần, .at("09:00", "18:00")`)
		}
		c.chained["at"] = true
		if len(args) == 0 {
			return fmt.Errorf(`.at() thiếu giờ: .at("09:00")`)
		}
		clocks, err := clocksOf("at", args)
		if err != nil {
			return err
		}
		r.Clocks = clocks
	case "first", "last":
		if r.Kind != Weekday {
			return fmt.Errorf(`.%s("month") chỉ đi với một thứ trong tuần: schedule.monday("09:00").%s("month")`, word, word)
		}
		if c.chained["first"] || c.chained["last"] {
			if c.chained[word] {
				return fmt.Errorf(".%s nối hai lần: một lịch một luật", word)
			}
			return fmt.Errorf("first và last không đi cùng nhau: một lịch là lần đầu hay lần cuối của tháng")
		}
		c.chained[word] = true
		if len(args) != 1 || (args[0].Kind != Unknown && (args[0].Kind != String || args[0].Text != "month")) {
			return fmt.Errorf(`.%s nhận "month": .%s("month") là lần %s của thứ đó trong tháng`, word, word, map[string]string{"first": "đầu", "last": "cuối"}[word])
		}
		r.Nth = word
	case "before", "after":
		switch r.Kind {
		case Every, Hourly, Daily:
			return fmt.Errorf(".%s(n) dời theo ngày, chỉ đi với lịch theo ngày (monday … sunday, weekly, monthly, doubleday, firstday, lastday); %s đã chạy mọi ngày", word, r.Word)
		case On:
			return fmt.Errorf(".%s(n) không đi với schedule.on(…): on là một thời điểm, viết thẳng thời điểm muốn", word)
		}
		if c.chained["before"] || c.chained["after"] {
			if c.chained[word] {
				return fmt.Errorf(".%s nối hai lần: một lịch một luật", word)
			}
			return fmt.Errorf("before và after không đi cùng nhau: một lịch dời một chiều")
		}
		c.chained[word] = true
		if len(args) != 1 {
			return fmt.Errorf(".%s(n) nhận số ngày lịch: lastday(\"09:00\").before(3)", word)
		}
		if args[0].Kind == Unknown {
			return nil
		}
		days, ok := whole(args[0], 1, 31)
		if !ok {
			return fmt.Errorf(".%s(n) nhận số ngày lịch, số nguyên từ 1 tới 31: lastday(\"09:00\").before(3)", word)
		}
		if word == "before" {
			days = -days
		}
		r.Shift = days
	case "retry":
		if err := once(); err != nil {
			return fmt.Errorf(".retry nối hai lần: một lịch một luật, .retry(3)")
		}
		if len(args) != 1 {
			return fmt.Errorf(".retry(n) nhận số lần thử thêm, số nguyên từ 0 tới 100: .retry(3) là bốn lần tổng cộng")
		}
		if args[0].Kind == Unknown {
			return nil
		}
		retry, ok := whole(args[0], 0, 100)
		if !ok {
			return fmt.Errorf(".retry(n) nhận số lần thử thêm, số nguyên từ 0 tới 100: .retry(3) là bốn lần tổng cộng")
		}
		r.Retry = retry
	case "timeout":
		if err := once(); err != nil {
			return fmt.Errorf(`.timeout nối hai lần: một lịch một luật, .timeout("10m")`)
		}
		if len(args) != 1 || (args[0].Kind != Unknown && args[0].Kind != String) {
			return fmt.Errorf(`.timeout(hạn) nhận một khoảng viết bằng chuỗi: .timeout("10m"), "30s", "1h"`)
		}
		if args[0].Kind == Unknown {
			return nil
		}
		text := strings.TrimSpace(args[0].Text)
		timeout, err := parseDuration(text)
		if err != nil || timeout <= 0 || timeout > 24*time.Hour {
			return fmt.Errorf(`.timeout(%q): hạn của một lần thử là một khoảng từ 1ms tới 24h: "30s", "10m", "1h"`, text)
		}
		r.Timeout, r.TimeoutText = timeout, text
	case "timezone":
		if r.Kind == On {
			return fmt.Errorf(`schedule.on(…) mang múi giờ trong chính thời điểm ("…+07:00"); .timezone không đổi gì`)
		}
		if err := once(); err != nil {
			return fmt.Errorf(`.timezone nối hai lần: một lịch một múi giờ`)
		}
		if len(args) != 1 || (args[0].Kind != Unknown && args[0].Kind != String) {
			return fmt.Errorf(`.timezone nhận tên múi giờ IANA: .timezone("Asia/Ho_Chi_Minh")`)
		}
		if args[0].Kind == Unknown {
			return nil
		}
		location, err := LoadZone(args[0].Text)
		if err != nil {
			return err
		}
		r.Zone, r.Location = args[0].Text, location
	case "run", "once":
		if r.Kind == On && word == "run" {
			return fmt.Errorf(`schedule.on(…) là một thời điểm, chỉ đi với .once(handler): schedule.on("2026-12-01T09:00:00+07:00").once(fn)`)
		}
		if (r.Kind == Weekly || r.Kind == Monthly) && !c.chained["at"] {
			example := `weekly("monday").at("09:00")`
			if r.Kind == Monthly {
				example = `monthly(1).at("09:00")`
			}
			return fmt.Errorf("%s(…) thiếu .at(…): giờ trong ngày đi qua .at, schedule.%s.%s(fn)", r.Word, example, word)
		}
		c.ended = word
		r.Once = word == "once"
		if len(args) != 1 || (args[0].Kind != Unknown && args[0].Kind != Function) {
			return fmt.Errorf(".%s(handler) nhận một hàm: .%s((context) => { … })", word, word)
		}
	default:
		return fmt.Errorf(".%s không có trên lịch: nối .at, .first, .last, .before, .after, .retry, .timeout, .timezone rồi kết thúc bằng .run(fn) hay .once(fn)", word)
	}
	return nil
}

// IsEnd reports a word that ends a chain: run, once.
func IsEnd(word string) bool { return word == "run" || word == "once" }

// TwoEnds is the error of .run or .once chained after the chain ended with .<first>(…). A reader of a chain
// says it even when .<first>(…) was written wrong itself (.once().run(handler)): the second end is what the
// author fixes first.
func TwoEnds(first string) error {
	return fmt.Errorf(".run và .once loại trừ nhau và đứng cuối: lịch đã kết thúc bằng .%s(…), sau đó không nối gì", first)
}

// Ended is "run" or "once" when the chain ended, "" while it has not.
func (c *Chain) Ended() string { return c.ended }

// Rule is the rule declared so far.
func (c *Chain) Rule() *Rule { return &c.rule }

// LoadZone loads an IANA zone of .timezone(…). "Local" (the server's clock) and "" are not names.
func LoadZone(name string) (*time.Location, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" || trimmed == "Local" || trimmed != name {
		return nil, fmt.Errorf(`.timezone(%q): múi giờ là một tên IANA, không theo đồng hồ của máy chủ: .timezone("Asia/Ho_Chi_Minh")`, name)
	}
	location, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf(`.timezone(%q): không phải múi giờ IANA: .timezone("Asia/Ho_Chi_Minh"), "UTC", "Europe/Paris"`, name)
	}
	return location, nil
}

var clockPattern = regexp.MustCompile(`^(\d{1,2}):(\d{2})$`)

// clocksOf reads the times of day of word's parameters, sorted, each once.
func clocksOf(word string, args []Arg) ([]Clock, error) {
	seen := map[Clock]bool{}
	var clocks []Clock
	for _, arg := range args {
		if arg.Kind == Unknown {
			continue
		}
		if arg.Kind != String {
			return nil, fmt.Errorf(`%s nhận giờ viết bằng chuỗi: %s("09:00")`, word, word)
		}
		text := strings.TrimSpace(arg.Text)
		if word == "daily" && weekdayInText(text) {
			return nil, fmt.Errorf(`daily(%q): thứ trong tuần là lịch riêng, schedule.monday("09:00"), hay weekly("monday").at("09:00")`, arg.Text)
		}
		match := clockPattern.FindStringSubmatch(text)
		hour, minute := -1, -1
		if match != nil {
			hour, _ = strconv.Atoi(match[1])
			minute, _ = strconv.Atoi(match[2])
		}
		if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
			return nil, fmt.Errorf(`giờ %q sai: viết "HH:MM", từ "00:00" tới "23:59"`, arg.Text)
		}
		clock := Clock{Hour: hour, Minute: minute}
		if seen[clock] {
			return nil, fmt.Errorf("giờ %q viết hai lần", clock.String())
		}
		seen[clock] = true
		clocks = append(clocks, clock)
	}
	sortClocks(clocks)
	return clocks, nil
}

// weekdayInText reports a day of the week written in a text ("monday 09:00", "mon 9:00").
func weekdayInText(text string) bool {
	first := strings.ToLower(strings.Fields(text + " ")[0])
	_, full := weekdays[first]
	_, short := weekdayAbbreviations[first]
	return full || short
}

// weekdayOf reads a day of the week of weekly's parameters.
func weekdayOf(arg Arg) (time.Weekday, error) {
	if arg.Kind != String {
		return 0, fmt.Errorf(`weekly nhận thứ viết bằng chuỗi: weekly("monday", "friday").at("09:00")`)
	}
	text := strings.TrimSpace(arg.Text)
	fields := strings.Fields(text)
	if len(fields) > 1 {
		return 0, fmt.Errorf(`weekly(%q): giờ đi qua .at(…): schedule.weekly(%q).at(%q)`, arg.Text, strings.ToLower(fields[0]), fields[1])
	}
	if day, ok := weekdays[text]; ok {
		return day, nil
	}
	if full, ok := weekdayAbbreviations[strings.ToLower(text)]; ok {
		return 0, fmt.Errorf("thứ %q: viết đủ tên, %q", arg.Text, full)
	}
	if full, ok := weekdays[strings.ToLower(text)]; ok {
		return 0, fmt.Errorf("thứ %q: viết chữ thường, %q", arg.Text, strings.ToLower(full.String()))
	}
	return 0, fmt.Errorf("thứ %q không có: monday, tuesday, wednesday, thursday, friday, saturday, sunday", arg.Text)
}

// whole reads a whole number from low to high.
func whole(arg Arg, low, high int) (int, bool) {
	if arg.Kind != Number || arg.Number != math.Trunc(arg.Number) || arg.Number < float64(low) || arg.Number > float64(high) {
		return 0, false
	}
	return int(arg.Number), true
}

// argText is an argument as written, for an error.
func argText(arg Arg) string {
	switch arg.Kind {
	case String:
		return strconv.Quote(arg.Text)
	case Number:
		return strconv.FormatFloat(arg.Number, 'f', -1, 64)
	}
	return "…"
}

var intervalDays = regexp.MustCompile(`^(\d+)(d|w)$`)

// parseInterval reads every()'s interval: "30s", "5m", "2h", "1d", "1w", at least a second, over a day only
// whole days, never a month or a year (they are not all the same length).
func parseInterval(arg Arg) (time.Duration, error) {
	usage := `every nhận một khoảng viết bằng chuỗi có đơn vị: every("30s"), every("5m"), every("2h")`
	if arg.Kind != String {
		return 0, fmt.Errorf("%s", usage)
	}
	text := strings.TrimSpace(arg.Text)
	if clockPattern.MatchString(text) {
		return 0, fmt.Errorf(`every(%q): every nhận một khoảng ("30m", "2h"); giờ trong ngày là daily(%q)`, arg.Text, text)
	}
	lower := strings.ToLower(text)
	if strings.HasSuffix(lower, "mo") || strings.HasSuffix(lower, "y") {
		return 0, fmt.Errorf(`every(%q): tháng và năm không dài đều nhau; viết monthly(1).at("09:00")`, arg.Text)
	}
	interval, err := parseDuration(text)
	if err != nil {
		if _, notNumber := strconv.ParseFloat(text, 64); notNumber == nil {
			// a bare number: the old cron read it as milliseconds
			return 0, fmt.Errorf("%s", usage)
		}
		return 0, fmt.Errorf(`every(%q) không đọc được: một khoảng như "30s", "5m", "2h", "1d"`, arg.Text)
	}
	if interval < time.Second {
		return 0, fmt.Errorf(`every(%q): khoảng ngắn nhất là một giây, every("1s")`, arg.Text)
	}
	if interval > oneDay && interval%oneDay != 0 {
		// a whole number of days falls on midnight of the zone; a day and some hours would fall on another hour each time
		return 0, fmt.Errorf(`every(%q): khoảng từ một ngày trở lên là số ngày tròn, tới giờ lúc nửa đêm: every("1d"), every("2d"), every("1w"); giờ trong ngày là daily("09:00")`, arg.Text)
	}
	return interval, nil
}

// parseDuration reads a duration as Go writes one ("30s", "1h30m", "250ms"), or a whole number of days or
// weeks ("1d", "2w").
func parseDuration(text string) (time.Duration, error) {
	text = strings.ToLower(strings.TrimSpace(text))
	if duration, err := time.ParseDuration(text); err == nil {
		return duration, nil
	}
	match := intervalDays.FindStringSubmatch(text)
	if match == nil {
		return 0, fmt.Errorf("không đọc được %q", text)
	}
	count, err := strconv.Atoi(match[1])
	if err != nil || count <= 0 {
		return 0, fmt.Errorf("không đọc được %q", text)
	}
	unit := 24 * time.Hour
	if match[2] == "w" {
		unit *= 7
	}
	return time.Duration(count) * unit, nil
}
