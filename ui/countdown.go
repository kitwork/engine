package ui

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Kitwork's countdown (decisions of 19/10/2026): time that runs out, counted from the moment play()
// starts it. It has no UI of its own: the author draws the time left, once, in a <time>, and the
// buttons, as ordinary markup:
//
//	<countdown as="$tea" duration="5m" ready="play()" finish="$toast.success('Trà đã sẵn sàng')" flex="row" gap="3">
//	  <text>Còn <time>{{ minutes }}:{{ String(seconds).padStart(2, '0') }}</time></text>
//	  <button click="toggle()">{{ playing ? 'Tạm dừng' : 'Chạy' }}</button>
//	  <button click="stop()">Dừng</button>
//	</countdown>
//	<button click="$tea.pause()">Tạm dừng pha trà</button>
//
//   - The host is a box (layout words stay the box's) with no role. duration, the one word of its own,
//     is a literal of one number and one unit (duration.go), whole seconds from 1s to 24h, read when
//     the page compiles; it reaches the instance in milliseconds (data-kitwork-scope="duration: 300000").
//   - Its state, the bare names inside the host and $tea.<name> outside: playing, remaining (milliseconds
//     left, a whole second), hours, minutes and seconds (its parts), duration. The server paints the
//     start: all of duration left, not playing, so $tea.minutes outside reads 5 before the kernel runs.
//   - The one <time> written without value inside the host (inside a <text> or not) is the time left:
//     the engine writes role="timer" on it (a timer's live region is off by its role, so no aria-live)
//     and a datetime bound to the parts, a valid HTML duration (PT0H5M0S), and the class countdown-time,
//     whose digits keep one width (reboot.css). A <time value="…"> in the host is an ordinary time.
//   - finish is its event (componentEvents): sent once a run, when the time runs out while playing, and
//     heard on its host only. ready="play()" on the host starts it as it comes alive.
//   - hours, minutes and seconds are also how markup sets the time (decisions of 20/10/2026): written
//     like a var, minutes = 3, seconds++, or by a field two-way on one outside the <time>
//     (KITWORK_UI §11 "Một chiều và hai chiều"). Each part is a whole number in its range (0-24, 0-59,
//     0-59): the JS clamps what is written, never above it; remaining, duration and playing stay its own.
//   - The fields written in the <time> are the clock itself (decisions of 20/10/2026, evening), its time
//     segments: <time><input value="{{ minutes }}" alt="Phút" />:<input value="{{ seconds }}" alt="Giây"
//     /></time>, in the order of a clock (hours, minutes, seconds). The same fields show the time ticking
//     and take edits. The engine writes each as a text field of digits, no spinner (inputmode="numeric",
//     autocomplete="off", spellcheck="false", size="2"), the part in two digits (05, 00, 02 hours), the
//     placeholder 00 unless the author wrote one, a spinbutton with its range and value
//     (aria-valuemin/max/now/valuetext) and data-part, its part, and no binding: the countdown's JS owns
//     the segments' DOM, so the kernel's two-way field never rewrites one under the fingers (it would
//     write 5 for 05, and parse what is typed as a number). The <time> holding segments is a group named
//     by its alt (or "Thời gian còn lại"), not a timer, and one long bar (decisions of 20/10/2026, late
//     evening): reboot.css draws .countdown-time[role="group"] as one field, its fields borderless and two
//     digits wide inside it, and the JS lets the caret run through the bar as through one field. min and
//     max on a segment narrow the range an edit sets it to, never widen it (countdownSegment).
//
// What does not fit is told where it is written: no duration, or one that is no whole number of seconds
// from 1s to 24h; the words other countdowns have (autoplay, interval, format, until…), a role, the
// state written on the tag; an empty host, no <time> for the time left, two, one a for repeats, one that
// reads none of remaining, hours, minutes, seconds, or one that cannot show all of duration (5m read
// only by seconds); a segment that is no bare part, two of one part, one out of a clock's order (seconds
// before minutes), fields that do not read as a clock to the seconds (hours alone, minutes alone, hours
// and minutes, hours and seconds: countdownBarGaps), one a for repeats, one with a word
// the engine writes (type="number" among them), readonly, disabled or input=, a min or max that is not a
// whole number of its range or widens it, or that the duration does not start in, and a <select> or a
// <textarea> in the <time> (a segment is an <input>); a call of a method it does not have,
// a write of what it keeps (remaining, duration, playing, a method), a number written to a part outside
// its range (minutes = 120), a write that would set a part its <time> does not show (hours++ read
// without hours) and a field two-way on what it keeps or on such a part, wherever its names reach
// (countdownEvents, countdownField) or through its alias (checkCountdowns), where a field of a part is
// one too: the kernel writes a field back to a bare name only. A host without finish, one nothing ever
// starts, and a field outside the <time> two-way on a part that nothing makes readonly while it plays
// are warnings.

// countdownScope is one countdown host: what ctx.countdown points to inside it.
type countdownScope struct {
	node      *Node
	component *Attr
	// alias is its as, aliases the frame that as is written in: an alias of the same name in another
	// instance's own .ui names another countdown
	alias   string
	aliases *aliasFrame
	// duration is how long it counts, in milliseconds; written is its attribute as the author wrote it
	duration float64
	written  *Attr
	time     *Node // the <time> that holds the time left
	// reads are the names of the time left the <time> reads (remaining, hours, minutes, seconds): what of
	// a time it can show, the tag's duration and a number markup writes to a part alike
	reads map[string]bool
	// segments are the fields written in the <time>, each a part of the time shown and edited there, by
	// the node the compile reaches; order the same in the order written (a clock's: hours, minutes,
	// seconds); label is the <time>'s alt, its name as the group of them
	segments map[*Node]*countdownSegment
	order    []*countdownSegment
	label    *Attr
	// starts: ready on the host, an event inside it, or a call through its alias calls play() or toggle()
	starts bool
}

// countdownSegment is a field in the countdown's <time>: one part of the time (hours, minutes, seconds),
// shown ticking and edited there. low and high are what an edit sets it to: the part's range, or the
// author's narrower min and max.
type countdownSegment struct {
	node      *Node
	part      string
	low, high float64
}

// countdownMost is the most each part holds: a countdown counts 24 hours at most.
var countdownMost = map[string]float64{"hours": 24, "minutes": 59, "seconds": 59}

// segmentOf is the segment of a part, nil when the <time> has none.
func (s *countdownScope) segmentOf(part string) *countdownSegment {
	for _, seg := range s.order {
		if seg.part == part {
			return seg
		}
	}
	return nil
}

// partRange is what a write sets a part to: its segment's range, else the part's.
func (s *countdownScope) partRange(part string) (low, high float64, seg *countdownSegment) {
	if seg = s.segmentOf(part); seg != nil {
		return seg.low, seg.high, seg
	}
	return 0, countdownMost[part], nil
}

// hides reports whether a part is larger than every part its <time> shows: the time a write of it adds is
// never seen (hours where the <time> reads no hours, minutes where it reads seconds alone).
func (s *countdownScope) hides(part string) bool {
	switch {
	case s.reads["remaining"]:
		return false
	case part == "hours":
		return !s.reads["hours"]
	case part == "minutes":
		return !s.reads["minutes"] && !s.reads["hours"]
	}
	return false
}

// countdownHidden is how a part its <time> does not show is told: what the <time> reads, why the time
// set there is not seen, and what to read.
var countdownHidden = map[string][2]string{
	"hours":   {"không đọc hours", "minutes chỉ tới 59; đọc thêm hours"},
	"minutes": {"chỉ đọc seconds", "seconds đếm từ 59 về 0 rồi quay lại; đọc thêm minutes"},
}

// isHost reports whether n is the countdown's host as written: the tag, or a copy the compile made of it
// (the box it builds), by where it stands. Every instance of a countdown written once (in a component's
// .ui used twice) stands there.
func (s *countdownScope) isHost(n *Node) bool {
	return n != nil && n.File == s.node.File && n.Line == s.node.Line && n.Column == s.node.Column
}

// countdownFinishOnly reports whether an event attribute listens for finish and nothing else: finish,
// finish:once, but not ready,finish (the ready of it starts the countdown).
func countdownFinishOnly(name string) bool {
	head, _, _ := strings.Cut(name, ":")
	for _, event := range strings.Split(head, ",") {
		if strings.TrimSpace(event) != "finish" {
			return false
		}
	}
	return true
}

// countdownLongest is the longest a countdown counts: it counts from when the page opens and over again
// when the page reloads, so a day is the most a time written on the page can mean.
const countdownLongest = 24 * 60 * 60 * 1000

// countdownMethods are what a countdown does, inside its host or through its alias; countdownNames
// what it holds, as the time left reads it.
var (
	countdownMethods = []string{"play", "pause", "toggle", "stop"}
	countdownNames   = []string{"playing", "remaining", "hours", "minutes", "seconds", "duration"}
)

// countdownLeft are the names of the time left the <time> reads: one is the least it holds.
var countdownLeft = []string{"remaining", "hours", "minutes", "seconds"}

// countdownSettable are the names markup writes, the parts of the time: written, they set it.
var countdownSettable = []string{"hours", "minutes", "seconds"}

// countdownStrangers are the words of other countdowns and of the web, written on the tag, each with
// what to write here; %s is the word as written.
var countdownStrangers = func() map[string]string {
	out := map[string]string{}
	add := func(message string, words ...string) {
		for _, word := range words {
			out[word] = message
		}
	}
	add(`countdown không có %s: chạy ngay khi trang mở là ready="play()"`, "autoplay", "autostart", "start")
	add(`countdown không có %s: đếm bao lâu là duration (duration="5m")`, "time", "length", "total")
	add(`countdown không có %s: trong Kitwork timeout là giới hạn thời gian của một request; đếm bao lâu là duration, hết giờ là finish`, "timeout")
	add(`countdown không có %[1]s: countdown đếm từng giây, engine lo; bỏ %[1]s`, "interval", "step", "precision", "millisecond")
	add(`countdown không có %s: thời gian do người viết vẽ, <time>{{ minutes }}:{{ String(seconds).padStart(2, '0') }}</time>`, "format")
	add(`countdown không có %s: hết giờ thì chạy lại là finish="play()"`, "loop", "repeat")
	add(`countdown chưa đếm tới một thời điểm: đếm bao lâu là duration (duration="10m"); until chưa có`, "until", "deadline", "date", "target", "at")
	add(`countdown chưa có %s: chạy lúc nào là ready="play()" hay một nút click="play()"`, "delay")
	add(`countdown không có %s: chữ số đổi ngay; hiệu ứng cho chữ số là animate-… trên phần con`, "transition", "animation", "animate", "motion", "effect")
	add(`countdown không có %s: thời gian còn lại không giữ qua lần tải lại; một hạn phải giữ là việc của server`, "persist")
	add(`countdown chưa có %s: chữ quanh <time> đã nói nó đếm gì (Còn <time>…</time>)`, "alt")
	add(`countdown không có %s: seconds là phần giây còn lại, engine tính từ duration; đếm 30 giây là duration="30s"`, "seconds")
	add(`countdown không có %s: minutes là phần phút còn lại, engine tính từ duration; đếm 5 phút là duration="5m"`, "minutes")
	add(`countdown không có %s: hours là phần giờ còn lại, engine tính từ duration; đếm 2 giờ là duration="2h"`, "hours")
	add(`countdown không có %s: remaining là thời gian còn lại (mili giây), engine tính từ duration; đếm 30 giây là duration="30s"`, "remaining")
	add(`countdown không có %s: playing là trạng thái của nó; chạy khi trang mở là ready="play()"`, "playing")
	add(`countdown không có %s: đang dừng là !playing`, "paused")
	add(`%s là hàm của countdown, không phải một chữ trên thẻ: gọi nó trong một sự kiện (ready="play()", click="pause()")`, "play", "pause", "toggle", "stop")
	add(`vai trò của countdown là việc của engine (role="timer" trên <time> của nó): bỏ %s`, "role")
	add(`countdown không có sự kiện %s: hết giờ là finish (finish="$toast.info('Hết giờ')")`, "end", "ended", "done", "complete", "expire", "onfinish")
	add(`countdown không gửi %s: remaining, hours, minutes, seconds tự vẽ lại mỗi giây; việc lúc hết giờ là finish`, "tick")
	return out
}()

// countdownCallStrangers are the methods of other countdowns, each with what does it here.
var countdownCallStrangers = map[string]string{
	"resume":   "chạy tiếp là play() (sau pause() chạy tiếp; sau stop() hay finish chạy lại từ đầu)",
	"start":    "chạy tiếp là play() (sau pause() chạy tiếp; sau stop() hay finish chạy lại từ đầu)",
	"continue": "chạy tiếp là play() (sau pause() chạy tiếp; sau stop() hay finish chạy lại từ đầu)",
	"reset":    "stop() đưa thời gian về đầu, play() chạy lại",
	"restart":  "stop() đưa thời gian về đầu, play() chạy lại",
	"cancel":   "dừng và về đầu là stop()",
}

// countdownFinishCalled is the error of finish called as a method: it is the event sent at the end.
const countdownFinishCalled = `finish là sự kiện lúc hết giờ, không gọi được: dừng sớm là stop(), việc lúc hết giờ viết ở finish="…"`

// inputs are what the kernel seeds the instance with before its init: the duration, in milliseconds.
func (s *countdownScope) inputs() []input {
	ms := strconv.FormatFloat(s.duration, 'f', -1, 64)
	return []input{{name: "duration", in: &interpolation{parts: []part{{text: ms}}}, source: ms}}
}

// startState is the instance as the server paints it: all of duration left, not playing.
func (s *countdownScope) startState(state map[string]any) map[string]any {
	out := make(map[string]any, len(state)+6)
	for name, v := range state {
		out[name] = v
	}
	hours, minutes, seconds := countdownParts(s.duration)
	out["duration"], out["remaining"] = s.duration, s.duration
	out["hours"], out["minutes"], out["seconds"] = hours, minutes, seconds
	out["playing"] = false
	return out
}

// countdownParts are the hours, minutes and seconds of a time left, in milliseconds: 0-24, 0-59, 0-59.
func countdownParts(ms float64) (hours, minutes, seconds float64) {
	whole := math.Ceil(ms / 1000)
	return math.Floor(whole / 3600), math.Mod(math.Floor(whole/60), 60), math.Mod(whole, 60)
}

// countdownDatetime is the <time>'s datetime, bound to the parts: a valid HTML duration (PT0H5M0S).
const countdownDatetime = "{{ 'PT' + hours + 'H' + minutes + 'M' + seconds + 'S' }}"

// countdownHost reads the countdown's own word (duration) and the words it does not have off its host,
// and finds the <time> that holds the time left; the host then goes on as the host of a component
// without a UI. What its events call is checked as each element compiles (countdownEvents).
func (c *compiler) countdownHost(n *Node, component *Attr) (*Node, *countdownScope, error) {
	scope := &countdownScope{node: n, component: component}
	host := *n
	host.Attrs = nil
	finishes := false
	for i := range n.Attrs {
		a := &n.Attrs[i]
		chain, base := "", a.Name
		if j := strings.LastIndex(a.Name, ":"); j >= 0 && !isEvent(a.Name) {
			chain, base = a.Name[:j], a.Name[j+1:]
		}
		head, _, _ := strings.Cut(a.Name, ":")
		says := countdownStrangers[base]
		if says == "" && strings.HasPrefix(base, "aria-") {
			says = countdownStrangers["role"]
		}
		word := base
		if says == "" && countdownStrangers[head] != "" {
			says, word = countdownStrangers[head], head // an event of other countdowns, with its modifiers: done:once
		}
		if says != "" {
			if strings.Contains(says, "%") {
				says = fmt.Sprintf(says, word)
			}
			return nil, nil, errAttr(n, a, "%s", says)
		}
		if base == "duration" {
			ms, err := c.countdownDuration(n, a, chain)
			if err != nil {
				return nil, nil, err
			}
			scope.duration, scope.written = ms, a
			continue
		}
		if isEvent(a.Name) && strings.Contains(","+head+",", ",finish,") {
			finishes = true
		}
		host.Attrs = append(host.Attrs, *a)
	}
	if len(n.SignificantChildren()) == 0 {
		return nil, nil, errAttr(n, component, `countdown rỗng: vẽ thời gian còn lại bên trong, <countdown duration="60s"><time>{{ remaining / 1000 }}</time></countdown>`)
	}
	if scope.written == nil {
		return nil, nil, errAttr(n, component, `countdown cần duration: đếm bao lâu (duration="5m")`)
	}
	if err := c.countdownTime(n, scope); err != nil {
		return nil, nil, err
	}
	if t := scope.time; len(scope.segments) > 0 && attrNamed(t, "alt").Name == "alt" {
		// the name of the group of segments: the countdown's, so the <time> the host compiles goes without
		// it (a <time>'s own alt says nothing, and is told so); the parsed tree is shared by every
		// instance, so the way down to it is copied, never changed
		label, time := *attrNamed(t, "alt"), *t
		time.Attrs = nil
		for _, a := range t.Attrs {
			if a.Name != "alt" {
				time.Attrs = append(time.Attrs, a)
			}
		}
		host.Children, _ = countdownReplace(host.Children, t, &time)
		scope.time, scope.label = &time, &label
	}
	if !finishes {
		c.warn(n.File, component.Line, component.Column, `countdown không có finish: hết giờ thì người dùng trình đọc màn hình không biết (role="timer" không đọc số đếm); viết finish="$toast.info('Hết giờ')" hay finish="$reader.say('Hết giờ')"`)
	}
	return &host, scope, nil
}

// countdownTime finds the one <time> written without value inside the host, never inside a component
// nested in it (whose names and whose <time> are its own), the time left, and checks it is one: no for
// repeats it, on itself or around it in the host. Then that it reads the time left, all of it: a duration
// of a minute or more needs minutes beside seconds, one of an hour or more hours beside minutes
// (remaining holds all of it). It reads the countdown's names bare, or through its alias
// ($tea.minutes, the way a name both it and a countdown around it declare is read).
func (c *compiler) countdownTime(n *Node, scope *countdownScope) error {
	var second, repeated *Node // a second one, and the for that repeats the first: the errors
	var find func(nodes []*Node, repeats *Node)
	find = func(nodes []*Node, repeats *Node) {
		for _, ch := range nodes {
			if ch.IsText() || c.callsComponent(ch) {
				continue
			}
			around := repeats
			if around == nil && attrNamed(ch, "for").Name == "for" {
				around = ch
			}
			if ch.Tag == "time" && attrNamed(ch, "value").Name != "value" { // with value: an ordinary time, of a moment it names
				switch {
				case scope.time == nil:
					scope.time, repeated = ch, around
				case second == nil:
					second = ch
				}
			}
			find(ch.Children, around)
		}
	}
	find(n.Children, nil)
	t := scope.time
	switch {
	case t == nil:
		return errAttr(n, scope.component, `countdown cần một <time> chứa thời gian còn lại, engine viết role="timer" lên nó: <time>{{ minutes }}:{{ String(seconds).padStart(2, '0') }}</time>`)
	case second != nil:
		return errAt(second, "countdown có một thời gian còn lại: <time> ở %s:%d:%d đã là của nó; gộp vào một <time>", t.File, t.Line, t.Column)
	case repeated == t:
		return errAttr(t, attrNamed(t, "for"), "countdown có một thời gian còn lại: <time> của nó không lặp bằng for; bỏ for")
	case repeated != nil:
		return errAttr(repeated, attrNamed(repeated, "for"), "countdown có một thời gian còn lại: for này lặp <time> ở %s:%d:%d của nó; đặt <time> ngoài for", t.File, t.Line, t.Column)
	}
	alias := strings.TrimSpace(attrNamed(n, "as").Value)
	reads := map[string]bool{}
	var walk func(nodes []*Node)
	walk = func(nodes []*Node) {
		for _, ch := range nodes {
			if !ch.IsText() {
				walk(ch.Children)
				continue
			}
			if !strings.Contains(ch.Text, "{{") {
				continue
			}
			in, err := parseInterpolation(ch.Text, ch, nil)
			if err != nil {
				continue // the compile of the text reports it
			}
			for _, p := range in.parts {
				if p.e == nil {
					continue
				}
				for _, name := range usedNames(p.e.ir) {
					if containsName(countdownLeft, name) {
						reads[name] = true
					}
				}
				walkIR(p.e.ir, func(node []any) {
					if len(node) != 3 || node[0] != "." || alias == "" {
						return
					}
					ref, ok := node[1].([]any)
					name, _ := node[2].(string)
					if ok && len(ref) == 2 && ref[0] == "$" && ref[1] == alias && containsName(countdownLeft, name) {
						reads[name] = true // its own, through its alias: the host is where the alias is set
					}
				})
			}
		}
	}
	walk(t.Children)
	if err := c.countdownSegments(t, scope); err != nil {
		return err
	}
	for _, seg := range scope.segments {
		reads[seg.part] = true // a segment shows its part
	}
	scope.reads = reads
	if len(reads) == 0 {
		return errAt(t, "<time> của countdown không đọc thời gian còn lại: đọc remaining, hours, minutes hay seconds bên trong")
	}
	if unshown := countdownUnshown(reads, scope.duration); unshown != "" {
		return errAt(t, `duration="%s" %s`, strings.TrimSpace(scope.written.Value), unshown)
	}
	if has, lacks := countdownBarGaps(scope.order); len(lacks) > 0 {
		var clock []string
		for _, part := range countdownSettable[countdownRank(scope.order[0].part):] {
			clock = append(clock, `<input value="{{ `+part+` }}" />`)
		}
		return errAt(t, "<time> của countdown có ô %s mà không có ô %s: các ô là một đồng hồ đọc tới giây, không thiếu phần nào ở giữa; viết %s",
			strings.Join(has, ", "), strings.Join(lacks, ", "), strings.Join(clock, ":"))
	}
	return nil
}

// countdownBarGaps are the parts a <time> of fields holds (has) and those it lacks from its largest part
// to the seconds (lacks, none when it reads as a clock to the seconds: seconds; minutes, seconds; hours,
// minutes, seconds), the fields in a clock's order (countdownSegment checks it). A bar with no seconds
// cannot show its time (hours alone read 00 for all of 5 minutes, minutes alone the last 59 seconds) nor
// take a second typed, and one with a part missing between (hours, seconds) reads 00:30 for 30 seconds
// and an hour once 1 is typed first (the review of 20/10/2026, late evening).
func countdownBarGaps(order []*countdownSegment) (has, lacks []string) {
	if len(order) == 0 {
		return nil, nil
	}
	for _, part := range countdownSettable[countdownRank(order[0].part):] {
		if seg := (&countdownScope{order: order}).segmentOf(part); seg != nil {
			has = append(has, part)
		} else {
			lacks = append(lacks, part)
		}
	}
	return has, lacks
}

// countdownSegments finds the fields written in the countdown's <time>, never inside a component nested
// there: each is one part of the time (countdownSegment).
func (c *compiler) countdownSegments(t *Node, scope *countdownScope) error {
	scope.segments, scope.order = map[*Node]*countdownSegment{}, nil
	var walk func(nodes []*Node, repeats *Node) error
	walk = func(nodes []*Node, repeats *Node) error {
		for _, ch := range nodes {
			if ch.IsText() || c.callsComponent(ch) {
				continue
			}
			around := repeats
			if around == nil && attrNamed(ch, "for").Name == "for" {
				around = ch
			}
			if ch.Tag == "input" {
				if err := c.countdownSegment(ch, scope, around); err != nil {
					return err
				}
				continue
			}
			if ch.Tag == "select" || ch.Tag == "textarea" {
				// a field of another kind is no segment: the engine writes a segment, an <input>, and a
				// choice of times is the author's buttons, beside the <time>
				return errAt(ch, `<%s> trong <time> của countdown không phải một phần của thời gian: mỗi phần là một <input value="{{ minutes }}" />, engine viết nó (hai chữ số, bàn phím số); nút chọn là của người viết, đặt ngoài <time>: <button click="minutes++">+1</button>`, ch.Tag)
			}
			if err := walk(ch.Children, around); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(t.Children, nil)
}

// countdownUnits are the parts in words, as a time is said (5 phút).
var countdownUnits = map[string]string{"hours": "giờ", "minutes": "phút", "seconds": "giây"}

// countdownSegment reads one field in the countdown's <time> as a segment: its value is one part, bare
// (value="{{ minutes }}"), one field a part, never repeated; the field is the engine's to write (no type,
// inputmode, autocomplete, step, pattern: a text field of digits with no spinner) and is edited even
// while it plays (no readonly, no disabled: focused, the countdown holds). min and max narrow what an
// edit sets it to: whole numbers within the part's range, the duration starting inside them. input= is
// never heard on it (the countdown takes the keys); change= is, sent by the countdown.
func (c *compiler) countdownSegment(n *Node, scope *countdownScope, repeats *Node) error {
	value := attrNamed(n, "value")
	part := ""
	if in, err := parseInterpolation(value.Value, n, value); value.Name == "value" && err == nil {
		if single := in.single(); single != nil {
			if ref, ok := single.ir.([]any); ok && len(ref) == 2 && ref[0] == "$" {
				part, _ = ref[1].(string)
			}
		}
	}
	if !containsName(countdownSettable, part) {
		return errAttr(n, value, `ô trong <time> của countdown là một phần của thời gian: value="{{ minutes }}" (hours, minutes hay seconds, tên trần); ô khác đặt ngoài <time>`)
	}
	if repeats != nil {
		return errAttr(repeats, attrNamed(repeats, "for"), "một phần của thời gian không lặp bằng for: mỗi phần một ô; bỏ for")
	}
	if other := scope.segmentOf(part); other != nil {
		return errAttr(n, value, "<time> của countdown đã có ô %s ở %s:%d:%d: mỗi phần một ô", part, other.node.File, other.node.Line, other.node.Column)
	}
	if k := len(scope.order); k > 0 && countdownRank(scope.order[k-1].part) > countdownRank(part) {
		// the bar reads as a clock and the caret runs through it as through one field, from the hours
		// to the seconds: a field of a larger part after a smaller one would read 30:05 for 5 minutes
		before := scope.order[k-1]
		return errAttr(n, value, `ô %s đứng sau ô %s ở %s:%d:%d: các ô của <time> theo thứ tự của đồng hồ, giờ rồi phút rồi giây: <input value="{{ %[1]s }}" />:<input value="{{ %[2]s }}" />`,
			part, before.part, before.node.File, before.node.Line, before.node.Column)
	}
	most := countdownMost[part]
	seg := &countdownSegment{node: n, part: part, high: most}
	for i := range n.Attrs {
		a := &n.Attrs[i]
		switch a.Name {
		case "type":
			if strings.TrimSpace(a.Value) == "number" {
				return errAttr(n, a, `một phần của thời gian không có type="number": engine viết ô (hai chữ số, bàn phím số, không có nút tăng giảm); bỏ type, nút tăng giảm là của người viết, <button click="%s++">+1</button>`, part)
			}
			return errAttr(n, a, "một phần của thời gian không có type: engine viết ô (hai chữ số, bàn phím số); bỏ type")
		case "inputmode", "autocomplete", "step", "pattern":
			return errAttr(n, a, `một phần của thời gian không có %[1]s: engine viết ô (inputmode="numeric", autocomplete="off"); bỏ %[1]s`, a.Name)
		case "readonly", "disabled":
			// disabled="{{ playing }}" as readonly would: a field disabled while it plays takes no focus, so a
			// click never holds it, and it locks and unlocks in turn
			return errAttr(n, a, "một phần của thời gian không có %s: bấm vào thì countdown dừng ở đúng số đó, Enter hay rời <time> thì chạy tiếp từ số đã sửa; chỉ để hiện thì viết {{ %s }} trong <time>", a.Name, part)
		case "min", "max":
			example := "30"
			if part == "hours" {
				example = "12"
			}
			if a.Name == "min" {
				example = "1"
			}
			written := strings.TrimSpace(a.Value)
			if strings.Contains(written, "{{") {
				return errAttr(n, a, `%s của một phần là một số cố định (%s="%s"), không có {{ }}: engine viết khoảng của ô khi trang dựng`, a.Name, a.Name, example)
			}
			number, err := strconv.ParseFloat(written, 64)
			if err != nil || math.IsInf(number, 0) || number != math.Floor(number) {
				return errAttr(n, a, `%s="%s" của %s không phải một số nguyên: viết một số từ 0 tới %d, như %s="%s"`, a.Name, a.Value, part, int(most), a.Name, example)
			}
			if number < 0 || number > most {
				return errAttr(n, a, `%s="%s" rộng hơn khoảng của %s (0 tới %d): min, max của một phần chỉ hẹp lại, như %s="%s"`, a.Name, a.Value, part, int(most), a.Name, example)
			}
			if a.Name == "min" {
				seg.low = number
			} else {
				seg.high = number
			}
		default:
			// the countdown takes every key itself (a native time field's typing), so the browser's input
			// never comes; a segment left changed sends change, as a text field does
			if head, _, _ := strings.Cut(a.Name, ":"); isEvent(a.Name) && strings.Contains(","+head+",", ",input,") {
				return errAttr(n, a, `một phần của thời gian không gửi input: countdown nhận từng phím và tự viết ô; ô đã đổi gửi change khi focus rời nó (Enter, Tab), change="…"`)
			}
		}
	}
	if seg.low > seg.high {
		return errAttr(n, attrNamed(n, "min"), `min="%s" lớn hơn max="%s": ô %s không còn số nào`, strings.TrimSpace(attrNamed(n, "min").Value), strings.TrimSpace(attrNamed(n, "max").Value), part)
	}
	hours, minutes, seconds := countdownParts(scope.duration)
	start := map[string]float64{"hours": hours, "minutes": minutes, "seconds": seconds}[part]
	if start < seg.low || start > seg.high {
		bound := attrNamed(n, "max")
		if start < seg.low {
			bound = attrNamed(n, "min")
		}
		return errAttr(n, bound, `duration="%s" bắt đầu ở %s %s, ngoài khoảng của ô %s (%s tới %s): viết duration trong khoảng đó, hay nới min, max`,
			strings.TrimSpace(scope.written.Value), countdownNumber(start), countdownUnits[part], part, countdownNumber(seg.low), countdownNumber(seg.high))
	}
	scope.segments[n] = seg
	scope.order = append(scope.order, seg)
	return nil
}

// countdownRank is where a part stands in a clock, from the left: hours 0, minutes 1, seconds 2.
func countdownRank(part string) int {
	for i, name := range countdownSettable {
		if name == part {
			return i
		}
	}
	return -1
}

// countdownNumber writes a number as markup does.
func countdownNumber(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// countdownReplace is nodes with old put in as replaced, every node on the way down to it copied: the
// parsed tree is shared, a component's .ui read once for every instance. found is whether old is there.
func countdownReplace(nodes []*Node, old, replaced *Node) (out []*Node, found bool) {
	for i, ch := range nodes {
		next := ch
		switch {
		case ch == old:
			next = replaced
		case ch.IsText():
			continue
		default:
			children, ok := countdownReplace(ch.Children, old, replaced)
			if !ok {
				continue
			}
			copied := *ch
			copied.Children = children
			next = &copied
		}
		out = append([]*Node(nil), nodes...)
		out[i] = next
		return out, true
	}
	return nodes, false
}

// countdownUnshown is what of a time of ms a <time> reading the names in reads cannot show, "" when it
// shows all of it: a minute or more read by seconds without minutes (seconds goes from 59 back to 0 and round again),
// an hour or more read without hours (minutes stops at 59); remaining holds all of it, in milliseconds.
// It is said after the time ("dài hơn một giờ mà <time> không đọc hours: …").
func countdownUnshown(reads map[string]bool, ms float64) string {
	switch {
	case reads["remaining"]:
	case ms >= 60*1000 && reads["seconds"] && !reads["minutes"]:
		reading := "chỉ đọc seconds"
		if reads["hours"] {
			reading = "đọc hours và seconds mà không đọc minutes"
		}
		return countdownLonger(ms, 60*1000, "một phút") + " mà <time> " + reading + ": seconds đếm từ 59 về 0 rồi quay lại; đọc thêm minutes"
	case ms >= 60*60*1000 && !reads["hours"]:
		return countdownLonger(ms, 60*60*1000, "một giờ") + " mà <time> không đọc hours: minutes chỉ tới 59; đọc thêm hours"
	}
	return ""
}

// countdownPartSeconds are the seconds of one of each part.
var countdownPartSeconds = map[string]float64{"hours": 3600, "minutes": 60, "seconds": 1}

// writeProblem is the error of a write of a part (minutes = 3, seconds++, an assignment, ++ or --), ""
// for one that sets a time its <time> shows. A number the compile reads (minutes = 120) outside the
// part's range, or its segment's, or not whole, is told what the countdown makes of it: each part is a
// whole number in its range, clamped, never carried to the next (KITWORK_UI's var, countdown.md "Đặt thời
// gian bằng markup"). A write that can raise a part its <time> does not show (hours++ read without hours,
// hours = 1, a value the compile does not know) is told: the time it adds is never seen. A value the
// compile does not know of a part shown, and any --, are the browser's to clamp.
func (s *countdownScope) writeProblem(node []any) string {
	name, _ := node[1].(string)
	if len(node) == 3 && node[0] == "=" {
		if literal, ok := node[2].([]any); ok && len(literal) == 2 && literal[0] == "#" {
			if number, ok := literal[1].(float64); ok {
				written := name + " = " + countdownNumber(number)
				low, high, seg := s.partRange(name)
				clamped := math.Max(low, math.Min(high, math.Floor(number)))
				switch {
				case number < low || number > high:
					of := name + " (" + countdownNumber(low) + " tới " + countdownNumber(high) + ")"
					if seg != nil && (low > 0 || high < countdownMost[name]) {
						of = fmt.Sprintf("ô %s ở %s:%d:%d (%s tới %s)", name, seg.node.File, seg.node.Line, seg.node.Column, countdownNumber(low), countdownNumber(high))
					}
					said := written + " ngoài khoảng của " + of + ": countdown giữ mỗi phần trong khoảng của nó, ghi " + countdownNumber(clamped)
					if number > high && high == countdownMost[name] {
						said += "; " + map[string]string{"hours": "countdown đếm nhiều nhất 24 giờ", "minutes": "thời gian dài hơn ghi ở hours", "seconds": "thời gian dài hơn ghi ở minutes"}[name]
					}
					return said
				case number != math.Floor(number):
					return written + " không phải một số nguyên: mỗi phần của countdown là số nguyên, countdown ghi " + countdownNumber(clamped)
				case number > 0:
					if unshown := countdownUnshown(s.reads, number*countdownPartSeconds[name]*1000); unshown != "" {
						return written + " đặt thời gian " + unshown
					}
				}
				return ""
			}
		}
	}
	if node[0] == "--" || !s.hides(name) {
		return ""
	}
	written := name + "++"
	if node[0] == "=" {
		written = name + " = …"
	}
	hidden := countdownHidden[name]
	return written + " ghi " + countdownUnits[name] + " mà <time> " + hidden[0] + ": thời gian thêm vào không hiện được, " + hidden[1]
}

// countdownLonger says how a duration stands to a minute or an hour it reaches: longer, or that long.
func countdownLonger(ms, than float64, what string) string {
	if ms == than {
		return "dài đúng " + what
	}
	return "dài hơn " + what
}

// countdownEvents checks the bare names an element's events call and write, wherever a countdown's names
// reach them: on its host and inside it, in a layout or a row there, in a component nested there that
// does not declare the name. A name means what the nearest level that declares it holds, frame by frame,
// as the kernel resolves it. A call of its state, and a write of what it keeps (remaining, duration,
// playing, a method), are told what to write; a write of a part (minutes = 3, seconds++) sets its time
// and starts nothing, and a number outside its range or a write its <time> cannot show is told
// (writeProblem). A name nothing declares, where the countdown is the nearest component the browser
// runs, is told what the countdown has when it is a word of other countdowns (resume, reset…), finish,
// or one close to a method (paus); any other is wireEvents' to tell. A call of play() or toggle() notes
// that it starts, anywhere but in its host's own finish (the end of a run that already started). A
// field two-way on one of its names, or a segment of its <time>, is read first (countdownField).
// runtimeAttributes calls it before wireEvents and bindPairs, for every element the kernel wires, with
// the element as it compiles (o).
func (c *compiler) countdownEvents(n *Node, o *elemOp, events []Attr, scope *frame) error {
	var nearest *frame // the nearest countdown around, for a name nothing declares
	for s := scope; s != nil && nearest == nil; s = s.parent {
		if s.countdown != nil {
			nearest = s
		}
	}
	if nearest == nil {
		return nil
	}
	if err := c.countdownField(n, o, scope); err != nil {
		return err
	}
	for i := range events {
		a := &events[i]
		e, err := compileExpr(a.Value, n, a)
		if err != nil {
			continue // wireEvents reports it
		}
		var found error
		free := usedNames(e.ir) // a lambda's parameter is its own (list.forEach(seconds => …))
		walkIR(e.ir, func(node []any) {
			name, writes := countdownTarget(node)
			if found != nil || name == "" || !containsName(free, name) {
				return
			}
			if owner := scope.declaring(name); owner != nil && owner.countdown != nil {
				s := owner.countdown
				switch {
				case writes && containsName(countdownSettable, name):
					// its time, set: the countdown's setter clamps the part and counts the parts (v1.0.0.js),
					// and starts nothing; a number out of range, or a time its <time> cannot show, is told
					if problem := s.writeProblem(node); problem != "" {
						found = errAttr(n, a, "%s", problem)
					}
				case writes:
					found = errAttr(n, a, "%s", countdownWriteProblem(s, name))
				case name == "play" || name == "toggle":
					if !s.isHost(n) || !countdownFinishOnly(a.Name) {
						s.starts = true
					}
				case !containsName(countdownMethods, name):
					found = errAttr(n, a, "%s", countdownCallProblem(name, name)) // its state, called
				}
				return
			} else if writes || owner != nil || clientGlobals[name] || scope.live() != nearest {
				return // a name of another level, the kernel's, or one a component nearer tells
			}
			known := name == "finish" || name == "left" || name == "time" || countdownCallStrangers[name] != ""
			if known || nearestWords(name, countdownMethods) != "" {
				found = errAttr(n, a, "%s", countdownCallProblem(name, name))
			}
		})
		if found != nil {
			return found
		}
	}
	return nil
}

// countdownTarget is the bare name an expression node calls (play()) or writes (playing = true,
// seconds++), and which; "" for any other node.
func countdownTarget(node []any) (name string, writes bool) {
	switch {
	case len(node) == 3 && node[0] == "call":
		if callee, ok := node[1].([]any); ok && len(callee) == 2 && callee[0] == "$" {
			name, _ = callee[1].(string)
		}
		return name, false
	case len(node) >= 2 && (node[0] == "=" || node[0] == "++" || node[0] == "--"):
		name, _ = node[1].(string)
		return name, true
	}
	return "", false
}

// countdownWriteProblem is the error of a countdown's name written by markup that it keeps: remaining,
// duration and playing are its own (written, the clock and the screen would part: playing written true,
// toggle() pauses a run that never started), its time is set through the parts, its methods are called.
func countdownWriteProblem(s *countdownScope, name string) string {
	switch {
	case name == "duration":
		return `duration của countdown chỉ đọc: thời gian lúc trang mở là duration="` + strings.TrimSpace(s.written.Value) + `" trên <countdown>; đổi thời gian là ghi hours, minutes, seconds (minutes = 3)`
	case name == "remaining":
		return "remaining của countdown chỉ đọc, countdown tự tính: đổi thời gian là ghi hours, minutes, seconds (minutes = 3)"
	case containsName(countdownMethods, name):
		return name + " là hàm của countdown, không gán được: gọi nó, " + name + "()"
	}
	return name + " của countdown chỉ đọc, countdown tự tính: chạy là play(), tạm dừng là pause(), về đầu là stop()"
}

// countdownField checks a field two-way on a countdown's name (KITWORK_UI §11, "Một chiều và hai
// chiều"): value="{{ minutes }}" on a field the user edits (an input of text or number, a textarea, a
// select), checked="{{ … }}" on a checkbox or a radio, the name bare and the countdown's where it
// stands. A field readonly or disabled for good (readonly, readonly="true") only shows the name, one
// way, and is told nothing. A part is the countdown's time, written like a var; with no readonly and
// no disabled at all the field is a warning: while it plays, the clock rewrites the part in the field
// the user is typing in. A field two-way on a part its <time> does not show is an error: what is typed
// in it is never seen (hours where the <time> reads no hours). remaining, duration and playing are the
// countdown's to write: a field two-way on one, even only at times (readonly="{{ !editing }}"), is an
// error. A segment of the <time> is no such field: the engine writes it (countdownSegmentWords).
func (c *compiler) countdownField(n *Node, o *elemOp, scope *frame) error {
	if n.Tag == "input" {
		for s := scope; s != nil; s = s.parent {
			if s.countdown != nil && s.countdown.segments[n] != nil {
				c.countdownSegmentWords(o, s.countdown, s.countdown.segments[n])
				return nil
			}
		}
	}
	field := readCountdownField(n)
	if !field.edited {
		return nil
	}
	for i := range n.Attrs {
		a := &n.Attrs[i]
		single := field.single(n, a)
		if single == nil {
			continue
		}
		ref, ok := single.ir.([]any)
		if !ok || len(ref) != 2 || ref[0] != "$" {
			continue
		}
		name, _ := ref[1].(string)
		owner := scope.declaring(name)
		if owner == nil || owner.countdown == nil {
			continue
		}
		if containsName(countdownSettable, name) {
			if owner.countdown.hides(name) {
				hidden := countdownHidden[name]
				return errAttr(n, a, "ô nối hai chiều với %s mà <time> %s: %s người dùng gõ không hiện được, %s", name, hidden[0], countdownUnits[name], hidden[1])
			}
			if !field.held {
				c.warn(n.File, a.Line, a.Column, `ô nối hai chiều với `+name+` mà không có readonly="{{ playing }}": lúc đang chạy, đồng hồ ghi đè số người dùng đang gõ trong ô; viết readonly="{{ playing }}"`)
			}
			continue
		}
		if kept := field.keptProblem(name); kept != "" {
			return errAttr(n, a, "%s", kept)
		}
	}
	return nil
}

// countdownFieldRead is an element read as a field: edited when it is one the user edits (an input of
// text or number, a textarea, a select, a checkbox or a radio) and is not readonly or disabled for good
// (readonly, readonly="true": one way, it only shows what it reads); held when a readonly or a disabled
// bound to a value makes it one way at times; choice for a checkbox or a radio, whose checked is what
// the user edits, where any other field's is its value (but a hidden field's or a button's).
type countdownFieldRead struct {
	edited, held, choice bool
}

// readCountdownField reads n as a field.
func readCountdownField(n *Node) countdownFieldRead {
	if n.Tag != "input" && n.Tag != "textarea" && n.Tag != "select" {
		return countdownFieldRead{}
	}
	kind := strings.ToLower(strings.TrimSpace(attrNamed(n, "type").Value))
	field := countdownFieldRead{edited: true, choice: n.Tag == "input" && (kind == "checkbox" || kind == "radio")}
	if n.Tag == "input" && countdownFixedInputs[kind] {
		field.edited = false
	}
	for _, word := range []string{"readonly", "disabled"} {
		a := attrNamed(n, word)
		value := strings.TrimSpace(a.Value)
		switch {
		case a.Name != word || value == "false":
		case a.Bare || value == "" || value == "true":
			field.edited = false // one way for good: the field only shows what it reads
		default:
			field.held = true
		}
	}
	return field
}

// single is the one expression of a, when a is what the user edits in the field n and holds nothing but
// it (value="{{ minutes }}"); nil otherwise: a value that does not compile is the attribute's to tell,
// and one of words around a name ("còn {{ minutes }}") is no field of a name.
func (field countdownFieldRead) single(n *Node, a *Attr) *expr {
	if !field.edited || !(a.Name == "checked" && field.choice || a.Name == "value" && !field.choice) {
		return nil
	}
	in, err := parseInterpolation(a.Value, n, a)
	if err != nil {
		return nil
	}
	return in.single()
}

// keptProblem is the error of a field two-way on what the countdown keeps, "" for any other name:
// remaining and duration are told to set the time through the parts, playing what to call, each how to
// only show it (on a choice disabled="true": a checkbox's readonly does not hold it).
func (field countdownFieldRead) keptProblem(name string) string {
	switch name {
	case "playing":
		return `playing của countdown chỉ đọc: chạy là play(), tạm dừng là pause(), một nút đổi qua lại là click="toggle()"; chỉ để hiện thì thêm disabled="true"`
	case "remaining", "duration":
		show := `readonly="true"`
		if field.choice {
			show = `disabled="true"`
		}
		return name + ` của countdown chỉ đọc: sửa thời gian bằng hours, minutes, seconds (value="{{ minutes }}"); chỉ để hiện thì thêm ` + show
	}
	return ""
}

// countdownFieldThroughAlias checks a field whose value is a countdown's name read through its alias
// (<input value="{{ $tea.minutes }}">, outside its host or in it): the kernel writes a field back to a
// bare name only, so the field shows the name one way and what is typed in it sets nothing; writing
// through an alias is not in the expression grammar yet ($tea.minutes = 3 is not either). A part is told
// to be written bare inside the host, or only shown; what the countdown keeps is told what it is told
// bare. A field readonly or disabled for good only shows it, and is told nothing.
func (c *compiler) countdownFieldThroughAlias(use aliasExpr) error {
	if use.attr == nil {
		return nil
	}
	field := readCountdownField(use.node)
	single := field.single(use.node, use.attr)
	if single == nil {
		return nil
	}
	read, ok := single.ir.([]any)
	if !ok || len(read) != 3 || read[0] != "." {
		return nil
	}
	if _, alias, ok := c.countdownAlias(read[1], use.frame); ok {
		name, _ := read[2].(string)
		if containsName(countdownSettable, name) {
			return toastError(use, `%s.%s qua alias chưa ghi được: kernel chỉ ghi ngược vào một tên trần, nên ô này chỉ hiện, gõ vào không đặt thời gian; ô đặt thời gian viết tên trần bên trong <countdown>, value="{{ %s }}"; chỉ để hiện thì thêm readonly="true"`, alias, name, name)
		}
		if kept := field.keptProblem(name); kept != "" {
			return toastError(use, "%s", kept)
		}
	}
	return nil
}

// countdownFixedInputs are the inputs whose value the user does not edit: no field of a name.
var countdownFixedInputs = map[string]bool{"hidden": true, "button": true, "submit": true, "reset": true, "image": true, "file": true}

// countdownCallProblem is what is wrong with calling name on a countdown, "" for a method it has; read
// is how its state is read where it is called (minutes inside the host, $tea.minutes through the alias).
func countdownCallProblem(name, read string) string {
	switch {
	case containsName(countdownMethods, name):
		return ""
	case name == "finish":
		return countdownFinishCalled
	case name == "left" || name == "time":
		return countdownLeftProblem(name)
	case countdownCallStrangers[name] != "":
		return "countdown không có " + name + ": " + countdownCallStrangers[name]
	case containsName(countdownNames, name):
		return name + " là trạng thái của countdown, không gọi được: đọc " + read + "; countdown có: " + strings.Join(countdownMethods, ", ")
	}
	return "countdown không có " + name + "; countdown có: " + strings.Join(countdownMethods, ", ") + nearestWords(name, countdownMethods)
}

// countdownLeftProblem is the error of the time left read under another library's name.
func countdownLeftProblem(name string) string {
	return "countdown không có " + name + ": thời gian còn lại là remaining (mili giây), hay hours, minutes, seconds"
}

// countdownReadProblem is what is wrong with reading name off a countdown's alias, "" for a name it has.
func countdownReadProblem(name string) string {
	switch {
	case containsName(countdownNames, name), containsName(countdownMethods, name):
		return ""
	case name == "finish":
		return countdownFinishCalled
	case name == "left" || name == "time":
		return countdownLeftProblem(name)
	case countdownCallStrangers[name] != "":
		return "countdown không có " + name + ": " + countdownCallStrangers[name]
	}
	return "countdown không có " + name + "; trạng thái của countdown là " + strings.Join(countdownNames, ", ") + nearestWords(name, countdownNames)
}

// countdownSeconds is the duration a countdown suggests for one written another way: itself in .ui's
// spelling when it is whole seconds, else its seconds rounded up, as the countdown shows its start.
func countdownSeconds(d duration) string {
	if math.Mod(d.ms, 1000) == 0 && d.ms > 0 {
		return d.suggestion(durationSeconds)
	}
	return strconv.FormatFloat(math.Max(1, math.Ceil(d.ms/1000)), 'f', -1, 64) + "s"
}

// countdownUnitWords are the units in words, for the message of a unit written another way.
var countdownUnitWords = map[string]string{"ms": "mili giây", "s": "giây", "m": "phút", "h": "giờ"}

// countdownDuration reads duration= on a countdown: how long it counts, a literal duration (duration.go:
// "30s", "5m", "1.5m", "1h") of whole seconds from 1s to 24h, in milliseconds. A number written without
// its unit is seconds (300 → 300s).
func (c *compiler) countdownDuration(n *Node, a *Attr, chain string) (float64, error) {
	value := strings.TrimSpace(a.Value)
	switch {
	case chain != "":
		return 0, errAttr(n, a, "duration của countdown không nhận tiền tố: nó được quyết một lần khi trang dựng")
	case a.Bare || value == "":
		return 0, errAttr(n, a, `countdown cần duration: đếm bao lâu (duration="5m")`)
	case strings.Contains(value, "{{"):
		return 0, errAttr(n, a, `duration của countdown là một thời lượng cố định (duration="5m"), không có {{ }}: nó được quyết một lần khi trang dựng`)
	}
	d := readDuration(value)
	// a time whose spelling is not .ui's (a number without its unit, two units, a unit written another
	// way) means a time all the same: one no spelling makes right, nothing or longer than 24 hours, is told
	// that; any other is told its spelling with a time the countdown takes, in whole seconds rounded up
	if d.problem == durationNoUnit || d.problem == durationTwoUnits || d.problem == durationForeign {
		meant := d.ms
		if d.problem == durationNoUnit {
			number, _ := strconv.ParseFloat(d.number, 64) // digits too many to hold are +Inf, longer than any
			meant = number * 1000
		}
		switch {
		case meant == 0:
			return 0, errAttr(n, a, `duration="%s": countdown cần ít nhất một giây để đếm, như duration="30s"`, a.Value)
		case meant > countdownLongest && d.problem == durationNoUnit:
			return 0, errAttr(n, a, `duration="%s" thiếu đơn vị, mà %s giây dài hơn 24 giờ: countdown đếm từ lúc trang mở và đếm lại khi tải lại trang; đếm tới một thời điểm (until) chưa có`, a.Value, d.number)
		case meant > countdownLongest:
			return 0, errAttr(n, a, `duration="%s" dài hơn 24 giờ: countdown đếm từ lúc trang mở và đếm lại khi tải lại trang; đếm tới một thời điểm (until) chưa có`, a.Value)
		}
	}
	switch d.problem {
	case durationNegative:
		// the time without its minus, in whole seconds (a number without its unit read as seconds); one
		// out of range is told the house's example
		example, time := "5m", d
		if time.unit == "" {
			number, _ := strconv.ParseFloat(time.number, 64)
			time.ms = number * 1000
		}
		if time.ms > 0 && time.ms <= countdownLongest {
			example = countdownSeconds(time)
		}
		return 0, errAttr(n, a, `duration không nhận thời lượng âm (%s): viết một thời lượng như duration="%s"`, value, example)
	case durationZero:
		return 0, errAttr(n, a, `duration="%s": countdown cần ít nhất một giây để đếm, như duration="30s"`, a.Value)
	case durationNoUnit:
		number, _ := strconv.ParseFloat(d.number, 64)
		if math.Mod(number, 1) != 0 {
			// seconds and a part of one: the whole seconds the countdown would show, rounded up
			return 0, errAttr(n, a, `duration="%s" thiếu đơn vị: viết duration="%s" (%s giây, countdown đếm từng giây)`, a.Value, countdownSeconds(duration{ms: number * 1000}), d.number)
		}
		also := ""
		if number >= 60 {
			switch {
			case math.Mod(number, 3600) == 0:
				also = ` hay duration="` + strconv.FormatFloat(number/3600, 'f', -1, 64) + `h"`
			case math.Mod(number, 60) == 0:
				also = ` hay duration="` + strconv.FormatFloat(number/60, 'f', -1, 64) + `m"`
			}
		}
		return 0, errAttr(n, a, `duration="%s" thiếu đơn vị: viết duration="%s" (%s giây)%s`, a.Value, d.suggestion(durationSeconds), d.number, also)
	case durationTwoUnits:
		count := strconv.Itoa(d.units)
		if d.units == 2 {
			count = "hai"
		}
		return 0, errAttr(n, a, `duration="%s" là %s đơn vị: viết một số với một đơn vị, duration="%s"`, a.Value, count, countdownSeconds(d))
	case durationForeign:
		if d.foreign == "" && !d.iso {
			// a space before its unit (5 s): the spelling is .ui's but for the space
			return 0, errAttr(n, a, `duration="%s" %s: viết duration="%s"`, a.Value, d.reason(), countdownSeconds(d))
		}
		return 0, errAttr(n, a, `duration="%s": %s viết %s, viết duration="%s"`, a.Value, countdownUnitWords[d.unit], d.unit, countdownSeconds(d))
	case durationNotOne:
		return 0, errAttr(n, a, `duration="%s" không phải một thời lượng: viết duration="30s", duration="5m" hay duration="1h"`, a.Value)
	}
	if d.ms > countdownLongest {
		return 0, errAttr(n, a, `duration="%s" dài hơn 24 giờ: countdown đếm từ lúc trang mở và đếm lại khi tải lại trang; đếm tới một thời điểm (until) chưa có`, a.Value)
	}
	if math.Mod(d.ms, 1000) != 0 {
		return 0, errAttr(n, a, `duration="%s": countdown đếm từng giây, viết một số giây tròn như duration="%s"`, a.Value, countdownSeconds(d))
	}
	return d.ms, nil
}

// countdownEnglish reports whether the site is written in a language other than Vietnamese: its words
// are then English, as carousel's and toast's are.
func (c *compiler) countdownEnglish() bool {
	language := strings.ToLower(c.site.Language)
	return language != "" && !strings.HasPrefix(language, "vi")
}

// countdownSaid is a number of a part as a screen reader says it, the segment's aria-valuetext: 5 phút,
// 5 minutes, 1 minute. The JS writes the same (v1.0.0.js, write), by the page's language.
func (c *compiler) countdownSaid(part string, n float64) string {
	if !c.countdownEnglish() {
		return countdownNumber(n) + " " + countdownUnits[part]
	}
	if n == 1 {
		return "1 " + strings.TrimSuffix(part, "s")
	}
	return countdownNumber(n) + " " + part
}

// countdownSegmentText is a part as its segment shows it: two digits, 05, 00, 02 hours (decisions of
// 20/10/2026, late evening: the bar reads 00:00:00 empty, so it reads 02:00:00 for two hours).
func countdownSegmentText(n float64) string {
	return fmt.Sprintf("%02d", int(n))
}

// countdownSegmentWords writes a segment as the engine draws it, in place of the value it was written
// with, and binds nothing of it: the countdown's JS owns the field, writing the part as it ticks and as
// it is typed (v1.0.0.js), so the kernel's two-way field (which would write 5 for 05, and parse what
// is typed) never touches it. A text field of digits with no spinner, sized for two, never spellchecked
// or filled in by the browser, its placeholder 00 (an empty bar reads 00:00:00) unless the author
// wrote one; a spinbutton (the arrow keys step it) with its range and the part it starts at, said in
// words (5 phút), named by its alt, else the part's word (Phút); data-part is the part, how the JS
// finds its segments; value the start in two digits (05), which is what shows without JS.
func (c *compiler) countdownSegmentWords(o *elemOp, s *countdownScope, seg *countdownSegment) {
	hours, minutes, seconds := countdownParts(s.duration)
	start := map[string]float64{"hours": hours, "minutes": minutes, "seconds": seconds}[seg.part]
	var attrs []dynAttr
	for _, d := range o.dynAttrs {
		if d.name != "value" {
			attrs = append(attrs, d)
		}
	}
	add := func(name, value string) {
		attrs = append(attrs, dynAttr{name: name, in: &interpolation{parts: []part{{text: value}}}})
	}
	if attrNamed(seg.node, "alt").Name != "alt" {
		name := map[string]string{"hours": "Giờ", "minutes": "Phút", "seconds": "Giây"}[seg.part]
		if c.countdownEnglish() {
			name = strings.ToUpper(seg.part[:1]) + seg.part[1:]
		}
		add("aria-label", name)
	}
	add("role", "spinbutton")
	add("inputmode", "numeric")
	add("autocomplete", "off")
	add("spellcheck", "false")
	add("size", "2")
	if attrNamed(seg.node, "placeholder").Name != "placeholder" {
		add("placeholder", "00")
	}
	add("aria-valuemin", countdownNumber(seg.low))
	add("aria-valuemax", countdownNumber(seg.high))
	add("aria-valuenow", countdownNumber(start))
	add("aria-valuetext", c.countdownSaid(seg.part, start))
	add("data-part", seg.part)
	add("value", countdownSegmentText(start))
	o.dynAttrs = attrs
}

// countdownElement writes what the engine adds to the countdown's <time>: the timer's role, and its
// datetime bound to the parts, which the server writes as it starts (PT0H5M0S). A <time> of segments is
// no timer but the group of them, its fields the clock, named by its alt (the alt the host took off it)
// or by the words for the time left: a timer is a live region whose content is the time, and that
// content is now fields a person edits, each with a name and a value of its own. The group is also the
// bar: reboot.css draws .countdown-time[role="group"] as one field holding its fields, so the role is
// what styles it, and no class of its own is needed.
func (c *compiler) countdownElement(n *Node, o *elemOp, w *strings.Builder, cx ctx) error {
	switch s := cx.countdown; {
	case len(s.segments) == 0:
		w.WriteString(` role="timer"`)
	case s.label != nil:
		w.WriteString(` role="group"`)
		if err := c.attrValue(n, s.label, "aria-label", false, o, w); err != nil {
			return err
		}
	default:
		w.WriteString(` role="group"`)
		label := "Thời gian còn lại"
		if c.countdownEnglish() {
			label = "Time left"
		}
		writeAttr(w, "aria-label", label)
	}
	in, err := parseInterpolation(countdownDatetime, n, cx.countdown.component)
	if err != nil {
		return err
	}
	o.dynAttrs = append(o.dynAttrs, dynAttr{name: "datetime", in: in})
	return nil
}

// countdownAlias reads $alias where an expression reads it, when the alias names a countdown: that
// countdown (nil when none of the page's is found) and the alias.
func (c *compiler) countdownAlias(ir any, frame *aliasFrame) (*countdownScope, string, bool) {
	ref, ok := ir.([]any)
	if !ok || len(ref) != 2 || ref[0] != "$" {
		return nil, "", false
	}
	alias, _ := ref[1].(string)
	if !aliasName(alias) || frame == nil {
		return nil, "", false
	}
	if def := frame.defOf(alias); def == nil || def.kitwork == nil || def.name != "countdown" {
		return nil, "", false
	}
	owner := frame
	for ; owner != nil; owner = owner.parent {
		if _, ok := owner.set[alias]; ok {
			break
		}
	}
	for _, s := range c.countdowns {
		if s.alias == alias && s.aliases == owner {
			return s, alias, true
		}
	}
	return nil, alias, true
}

// countdownStarts notes that a call through its alias starts s, and every instance of the same countdown
// (written at the same place with the same alias): a component's .ui used twice holds the call once, and
// an expression is kept once where it is written (noteAliasExpr), with the alias frame of the first
// instance it was read in, so only the first instance's countdown is found by it.
func (c *compiler) countdownStarts(s *countdownScope) {
	for _, other := range c.countdowns {
		if other == s || other.alias == s.alias && other.isHost(s.node) {
			other.starts = true
		}
	}
}

// checkCountdowns checks, once every as of the page is known, each call through a countdown's alias (a
// method it has), each name read off it (its state) and each field two-way on one through it
// (countdownFieldThroughAlias), and notes the calls that start it; then tells a countdown nothing on the
// page ever starts.
func (c *compiler) checkCountdowns() error {
	if len(c.countdowns) == 0 {
		return nil
	}
	for _, use := range c.aliasExprs {
		var err error
		walkIR(use.e.ir, func(node []any) {
			if err != nil {
				return
			}
			switch {
			case len(node) == 4 && node[0] == "()":
				s, alias, ok := c.countdownAlias(node[1], use.frame)
				if !ok {
					return
				}
				method, _ := node[2].(string)
				if problem := countdownCallProblem(method, alias+"."+method); problem != "" {
					err = toastError(use, "%s", problem)
					return
				}
				if s != nil && (method == "play" || method == "toggle") && (!s.isHost(use.node) || use.attr == nil || !countdownFinishOnly(use.attr.Name)) {
					c.countdownStarts(s) // its host's own finish="$tea.play()" runs only once a run has ended
				}
			case len(node) == 3 && node[0] == ".":
				if _, _, ok := c.countdownAlias(node[1], use.frame); !ok {
					return
				}
				name, _ := node[2].(string)
				if problem := countdownReadProblem(name); problem != "" {
					err = toastError(use, "%s", problem)
				}
			}
		})
		if err == nil {
			err = c.countdownFieldThroughAlias(use) // a field the kernel would show one way only
		}
		if err != nil {
			return err
		}
	}
	for _, s := range c.countdowns {
		if !s.starts {
			c.warn(s.node.File, s.component.Line, s.component.Column, `countdown không bao giờ chạy: không có ready="play()", không nút nào gọi play() hay toggle(); viết ready="play()" để chạy khi trang mở`)
		}
	}
	return nil
}
