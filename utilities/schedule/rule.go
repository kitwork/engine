// Package schedule is the rule of a schedule of KITWORK_SCHEDULE.md: what each word of kitwork/schedule
// declares (daily("09:00"), lastday("09:00").before(3), on("2026-12-01T09:00:00+07:00")…), the errors of
// mục 11 in the author's words, and the times a rule is due. It runs nothing and stores nothing: the
// compiler reads a chain written with literals through it (the error is then a compile error at file:line:
// col), the runtime reads every chain through it as it is evaluated, and the dispatcher asks it when a
// schedule is due next. One reading, so the compiler and the runtime never disagree on what a chain means.
package schedule

import (
	"fmt"
	"sort"
	"strings"
	"time"

	// A schedule's .timezone("Asia/Ho_Chi_Minh") is an IANA name; Windows and a scratch container have
	// no zoneinfo of their own, so the engine carries the database (about 450 KB).
	_ "time/tzdata"
)

// Kind is the word a rule begins with, by what it counts.
type Kind int

const (
	Every     Kind = iota + 1 // every("30s"): an interval
	Hourly                    // hourly(n): every n hours at :00
	Daily                     // daily("09:00", …)
	Weekday                   // monday("09:00") … sunday
	Weekly                    // weekly("monday", …).at("09:00")
	Monthly                   // monthly(1, 15).at("09:00")
	Doubleday                 // doubleday("09:00"): 1/1, 2/2 … 12/12
	Firstday                  // firstday("09:00")
	Lastday                   // lastday("09:00")
	On                        // on("2026-12-01T09:00:00+07:00"): one instant
)

// Clock is a time of day as written, "09:00".
type Clock struct {
	Hour   int
	Minute int
}

func (c Clock) String() string { return fmt.Sprintf("%02d:%02d", c.Hour, c.Minute) }

// Rule is what a chain declares: when (the rule of time), and the rules of its runs.
type Rule struct {
	Kind Kind
	// Word is the word the rule begins with as written: "daily", "monday", "on"…
	Word string
	// Interval and IntervalText are every's; Hours is hourly's n.
	Interval     time.Duration
	IntervalText string
	Hours        int
	// Clocks are the times of day: the parameters of daily, of a day of the week, doubleday, firstday and
	// lastday, or what .at(…) gives weekly and monthly. Sorted, each once.
	Clocks []Clock
	// Weekdays are the day of a day-of-the-week rule (one) or weekly's (several); Days monthly's.
	Weekdays []time.Weekday
	Days     []int
	// Instant is on's.
	Instant     time.Time
	InstantText string
	// Nth is "first" or "last" of the month (.first("month"), .last("month")), "" for every week.
	Nth string
	// Shift moves each day the rule falls on by whole calendar days, the time of day kept: .before(3) is
	// -3, .after(1) is 1. The month a day belongs to is the one before the move.
	Shift int
	// Zone is the IANA name of .timezone(…) as written, "" when the rule does not say (the app's zone);
	// Location is it loaded.
	Zone     string
	Location *time.Location
	// Retry is the attempts after the first (.retry(n)); Timeout bounds each attempt, 0 for none.
	Retry       int
	Timeout     time.Duration
	TimeoutText string
	// Once is .once(handler): one due time only.
	Once bool
}

// Mode is how the rule runs: "once" for .once(…), "recurring" for .run(…).
func (r *Rule) Mode() string {
	if r.Once {
		return "once"
	}
	return "recurring"
}

// Text is the rule of time as written, without the rules of the runs: daily("09:00"),
// lastday("09:00").before(3), weekly("monday", "friday").at("09:00").
func (r *Rule) Text() string {
	clocks := func() string {
		parts := make([]string, len(r.Clocks))
		for i, clock := range r.Clocks {
			parts[i] = fmt.Sprintf("%q", clock.String())
		}
		return strings.Join(parts, ", ")
	}
	var text string
	switch r.Kind {
	case Every:
		text = fmt.Sprintf("every(%q)", r.IntervalText)
	case Hourly:
		text = fmt.Sprintf("hourly(%d)", r.Hours)
	case Weekly:
		days := make([]string, len(r.Weekdays))
		for i, day := range r.Weekdays {
			days[i] = fmt.Sprintf("%q", strings.ToLower(day.String()))
		}
		text = fmt.Sprintf("weekly(%s).at(%s)", strings.Join(days, ", "), clocks())
	case Monthly:
		days := make([]string, len(r.Days))
		for i, day := range r.Days {
			days[i] = fmt.Sprint(day)
		}
		text = fmt.Sprintf("monthly(%s).at(%s)", strings.Join(days, ", "), clocks())
	case On:
		text = fmt.Sprintf("on(%q)", r.InstantText)
	default:
		text = fmt.Sprintf("%s(%s)", r.Word, clocks())
	}
	if r.Nth != "" {
		text += fmt.Sprintf(".%s(%q)", r.Nth, "month")
	}
	switch {
	case r.Shift < 0:
		text += fmt.Sprintf(".before(%d)", -r.Shift)
	case r.Shift > 0:
		text += fmt.Sprintf(".after(%d)", r.Shift)
	}
	return text
}

// location is the zone the rule counts in: its own, or UTC when it has none (the caller resolves the
// app's zone into Location first).
func (r *Rule) location() *time.Location {
	if r.Location != nil {
		return r.Location
	}
	return time.UTC
}

// scanDays is how many days a calendar rule looks through for its next or last due time. The longest gap
// any rule has is monthly(31) across two months of 30 days and February, about 62 days; a year leaves room.
const scanDays = 400

// Next is the first time after t the rule is due, false when it never is again (an on() at or before t).
func (r *Rule) Next(t time.Time) (time.Time, bool) {
	switch r.Kind {
	case On:
		if r.Instant.After(t) {
			return r.Instant, true
		}
		return time.Time{}, false
	case Every:
		return r.everyNext(t), true
	}
	location := r.location()
	local := t.In(location)
	date := civil(local.Year(), local.Month(), local.Day())
	for scanned := 0; scanned < scanDays; scanned++ {
		if r.falls(date) {
			for _, clock := range r.clocks() {
				due := time.Date(date.Year(), date.Month(), date.Day(), clock.Hour, clock.Minute, 0, 0, location)
				if due.After(t) {
					return due, true
				}
			}
		}
		date = date.AddDate(0, 0, 1)
	}
	return time.Time{}, false
}

// Latest is the last time at or before t the rule was due, false when it never was (an on() after t).
func (r *Rule) Latest(t time.Time) (time.Time, bool) {
	switch r.Kind {
	case On:
		if !r.Instant.After(t) {
			return r.Instant, true
		}
		return time.Time{}, false
	case Every:
		return r.everyLatest(t), true
	}
	location := r.location()
	local := t.In(location)
	date := civil(local.Year(), local.Month(), local.Day())
	clocks := r.clocks()
	for scanned := 0; scanned < scanDays; scanned++ {
		if r.falls(date) {
			for index := len(clocks) - 1; index >= 0; index-- {
				clock := clocks[index]
				due := time.Date(date.Year(), date.Month(), date.Day(), clock.Hour, clock.Minute, 0, 0, location)
				if !due.After(t) {
					return due, true
				}
			}
		}
		date = date.AddDate(0, 0, -1)
	}
	return time.Time{}, false
}

// The due times of every() (mục 12.1 is open; this is the engine's rule). An interval under a day counts
// from midnight of the rule's zone, and starts again at each midnight, as hourly(n) does: every("30s") falls
// on :00 and :30, every("2h") on 00:00, 02:00… of the zone, not of UTC, and every("7m") on 00:00, 00:07…
// 23:55, then 00:00 again. Within a day it counts the time that passes, so on a day summer time changes the
// times after the change move by the hour until midnight. An interval of whole days (every("1d"),
// every("1w")) falls on midnight of the zone, every n days counted from 1 January of year 1, a Monday:
// every("1w") falls on Mondays. Next and Latest read this one grid, so no change of offset puts a due time
// between them that the rule never had.

const oneDay = 24 * time.Hour

// dayStart is when the calendar day date (civil) begins in location: its midnight, or, where the clock skips
// midnight (Santiago jumps from 00:00 to 01:00), the change of offset, its first instant. It grows with
// date, so the days never overlap.
func dayStart(date time.Time, location *time.Location) time.Time {
	start := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, location)
	if year, month, day := start.In(location).Date(); year != date.Year() || month != date.Month() || day != date.Day() {
		// Go put the midnight that does not exist in the day before, at the old offset: move it by the jump
		_, before := start.Zone()
		_, after := start.Add(oneDay).Zone()
		start = start.Add(time.Duration(after-before) * time.Second)
	}
	return start
}

// dayOf is the calendar day of location t falls in (civil), when it begins and when the next one begins:
// start <= t < end.
func dayOf(t time.Time, location *time.Location) (date, start, end time.Time) {
	local := t.In(location)
	date = civil(local.Year(), local.Month(), local.Day())
	start = dayStart(date, location)
	for start.After(t) {
		date = date.AddDate(0, 0, -1)
		start = dayStart(date, location)
	}
	end = dayStart(date.AddDate(0, 0, 1), location)
	for !end.After(t) {
		date, start = date.AddDate(0, 0, 1), end
		end = dayStart(date.AddDate(0, 0, 1), location)
	}
	return date, start, end
}

// dayNumber is how many days the calendar day date (civil) comes after 1 January of year 1.
func dayNumber(date time.Time) int64 {
	const unixDay = 719162 // 1 January 1970, counted from 1 January of year 1
	return (date.Unix() + unixDay*86400) / 86400
}

// everyLatest is the last time at or before t every()'s interval falls on.
func (r *Rule) everyLatest(t time.Time) time.Time {
	location := r.location()
	date, start, _ := dayOf(t, location)
	if r.Interval%oneDay == 0 {
		days := int64(r.Interval / oneDay)
		return dayStart(date.AddDate(0, 0, -int(dayNumber(date)%days)), location)
	}
	return start.Add(t.Sub(start) / r.Interval * r.Interval)
}

// everyNext is the first time after t every()'s interval falls on.
func (r *Rule) everyNext(t time.Time) time.Time {
	location := r.location()
	date, start, end := dayOf(t, location)
	if r.Interval%oneDay == 0 {
		days := int64(r.Interval / oneDay)
		return dayStart(date.AddDate(0, 0, int(days-dayNumber(date)%days)), location)
	}
	if next := start.Add((t.Sub(start)/r.Interval + 1) * r.Interval); next.Before(end) {
		return next
	}
	return end
}

// civil is a calendar day, held at noon UTC so stepping by days never meets a change of offset.
func civil(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 12, 0, 0, 0, time.UTC)
}

// clocks are the times of day the rule is due on a day it falls on.
func (r *Rule) clocks() []Clock {
	if r.Kind != Hourly {
		return r.Clocks
	}
	var clocks []Clock
	for hour := 0; hour < 24; hour += r.Hours {
		clocks = append(clocks, Clock{Hour: hour})
	}
	return clocks
}

// falls reports whether the rule is due on the calendar day date: the day before the move of .before or
// .after (origin) is one the rule names.
func (r *Rule) falls(date time.Time) bool {
	origin := date.AddDate(0, 0, -r.Shift)
	year, month, day := origin.Date()
	last := daysIn(year, month)
	switch r.Kind {
	case Daily, Hourly:
		return true
	case Weekday:
		if origin.Weekday() != r.Weekdays[0] {
			return false
		}
		switch r.Nth {
		case "first":
			return day <= 7
		case "last":
			return day > last-7
		}
		return true
	case Weekly:
		for _, weekday := range r.Weekdays {
			if origin.Weekday() == weekday {
				return true
			}
		}
	case Monthly:
		for _, wanted := range r.Days {
			if day == wanted {
				return true
			}
		}
	case Doubleday:
		return int(month) == day
	case Firstday:
		return day == 1
	case Lastday:
		return day == last
	}
	return false
}

// daysIn is how many days month has in year.
func daysIn(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// sortClocks sorts clocks by time of day.
func sortClocks(clocks []Clock) {
	sort.Slice(clocks, func(i, j int) bool {
		return clocks[i].Hour*60+clocks[i].Minute < clocks[j].Hour*60+clocks[j].Minute
	})
}

// Stamp is a due time as an author reads it, context.job.at and next of list(): RFC 3339 in the
// schedule's zone, its offset written ("2026-10-28T09:00:00+07:00"), to the millisecond when it has one (a
// now() is due when it is called).
func Stamp(t time.Time, location *time.Location) string {
	if location == nil {
		location = time.UTC
	}
	return t.In(location).Format("2006-01-02T15:04:05.999Z07:00")
}
