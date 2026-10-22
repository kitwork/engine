package ui

import (
	"fmt"
	"strconv"
	"strings"
)

// The carousel (18/10/2026, docs/components/carousel.md) is one of Kitwork's components, written as its
// tag, <carousel>. The author draws the slides, the buttons and the dots; the engine
// recognises them and writes the wiring:
//
//	<carousel as="$hero" alt="Món nổi bật" duration="5s">
//	  <list>
//	    <item><image value="/a.jpg" alt="Cà phê sữa" /></item>
//	    <item><image value="/b.jpg" alt="Bạc xỉu" /></item>
//	  </list>
//	  <button click="previous()"><icon value="chevron-left" alt="Món trước" /></button>
//	  <button click="next()"><icon value="chevron-right" alt="Món sau" /></button>
//	  <button click="toggle()"><icon value="{{ playing ? 'player-pause' : 'player-play' }}" alt="Dừng, chạy" /></button>
//	  <button for="mon, i of mons" click="go(i)" active:background="brand" />
//	</carousel>
//
//   - The host is a region a screen reader names: role="region", aria-roledescription (carousel, in the
//     page's language), aria-label from alt.
//   - The one <list> written right inside the host is the track (class carousel-track): a row that
//     scrolls sideways and snaps to each slide, so a finger, a trackpad and the arrow keys move it, and
//     the browser smooths it unless less motion is asked for. tabindex="0" takes the arrow keys; aria-live
//     is polite while it does not play by itself, off while it does (a moving region is not read out).
//   - Each <item> of it is a slide: aria-roledescription (slide), and "1 / 3" as its name when the
//     slides are written out (the component names every slide as it starts, a for's too). The component
//     marks the current one aria-current, so active: on an item draws it, and fade shows it.
//   - A <button> whose click is go(…) of a place the page knows (a number, a for's index) is a dot: the
//     server writes the place as its value and aria-controls names the track; its click becomes
//     go(Number($this.value)), which the browser can run (a for over the page's data is the server's),
//     and the component marks the current dot aria-current, which active: draws (the server marks the
//     first). selected: is the tab's (aria-selected) and never holds on a dot: told. A go(…) of the
//     component's own names (go(current + 2)) is a button like next(), no dot.
//   - transition (transition.go): slide (the default), fade (the slides on one another, the current one
//     shown: carousel-fade), none (it jumps: carousel-none). duration: how long each slide stays when it
//     plays by itself ("5s", the toast's word); not written, it plays only when play() is called. Playing
//     by itself needs a way to stop it (WCAG 2.2.2): no pause() or toggle() on the page is told.
//
// What the instance starts with goes to the server's first paint: count is the slides written, or, when
// a for repeats some or an if may leave one out, an input the server reads off the page's data
// (2 + (mons).length, ((promo) ? 1 : 0)); playing is whether a duration is written.

// carouselScope is one carousel host: what ctx.carousel points to inside it.
type carouselScope struct {
	node      *Node
	component *Attr
	alias     string
	label     string
	// labelBound: alt reads data (alt="{{ product.name }}"), so it stays the host's own alt, which the
	// tag binds to aria-label like any <box alt>; the engine then writes no name of its own
	labelBound bool
	track      *Node
	trackID    string        // the track's id: the author's, else the engine's; each dot controls it
	slides     map[*Node]int // each <item> of the track, its place; -1 for one written with for or if
	count      int           // the slides when each is written out, else -1
	countFrom  string        // else how many, as an expression of the page (2 + (mons).length)
	duration   float64       // milliseconds each slide stays while it plays, 0 when not written
	durationAt *Attr
	transition string
	stops      bool // a click inside the host calls pause() or toggle()
	// start is the slide it starts on: current written as a number (current="2"), else 0; startAt is
	// that attribute, nil when not written so (from data it stays an input of the call)
	start   int
	startAt *Attr
}

// carouselStrangers are the words other carousels use, each with what to write here.
var carouselStrangers = map[string]string{
	"autoplay": `tự chạy là duration: mỗi trang ở lại bao lâu (duration="5s")`,
	"interval": `mỗi trang ở lại bao lâu là duration (duration="5s")`,
	"timeout":  `mỗi trang ở lại bao lâu là duration (duration="5s")`,
	"delay":    `mỗi trang ở lại bao lâu là duration (duration="5s")`,
	"speed":    `nhanh chậm của lúc chuyển là việc của engine; mỗi trang ở lại bao lâu là duration (duration="5s")`,
	"loop":     `next() ở trang cuối đã quay về trang đầu, previous() ở trang đầu về trang cuối: bỏ loop`,
	"infinite": `next() ở trang cuối đã quay về trang đầu, previous() ở trang đầu về trang cuối: bỏ infinite`,
	"index":    `trang đang hiện là current (current="0" là trang đầu)`,
	"active":   `trang đang hiện là current (current="0" là trang đầu)`,
	"count":    `count là số trang, engine đếm từ các <item> của dải: bỏ count`,
	"playing":  `carousel tự chạy khi có duration (duration="5s"); không tự chạy thì bỏ duration: bỏ playing`,
}

// classes are the host's: the carousel, and how a slide gives way to the next (reboot.css).
func (s *carouselScope) classes() []string {
	out := []string{"carousel"}
	switch s.transition {
	case "fade", "none":
		out = append(out, "carousel-"+s.transition)
	}
	return out
}

// inputs are what the kernel seeds the instance with: the duration written, in milliseconds.
func (s *carouselScope) inputs() []input {
	var out []input
	if s.duration > 0 {
		ms := strconv.FormatFloat(s.duration, 'f', -1, 64)
		out = append(out, input{name: "duration", in: &interpolation{parts: []part{{text: ms}}}, source: ms})
	}
	if s.startAt != nil {
		// a number, not the text written: the server's first paint adds to it ((current + 1) + ' / ' + count)
		at := strconv.Itoa(s.start)
		out = append(out, input{name: "current", in: &interpolation{parts: []part{{text: at}}}, source: at})
	}
	return out
}

// startState is the instance as the server paints it: the slides written, whether it plays.
func (s *carouselScope) startState(state map[string]any) map[string]any {
	out := make(map[string]any, len(state)+2)
	for name, v := range state {
		out[name] = v
	}
	if s.count >= 0 {
		out["count"] = float64(s.count)
	}
	out["playing"] = s.duration > 0
	out["current"] = float64(s.start)
	return out
}

// carouselHost reads the carousel's own words off its host — alt, transition, duration — and finds its
// track; the host then goes on as the host of a component without a UI.
func (c *compiler) carouselHost(n *Node, component *Attr) (*Node, *carouselScope, error) {
	scope := &carouselScope{node: n, component: component, transition: transitions[0], slides: map[*Node]int{}}
	host := *n
	host.Attrs = nil
	for i := range n.Attrs {
		a := &n.Attrs[i]
		chain, base := "", a.Name
		if j := strings.LastIndex(a.Name, ":"); j >= 0 && !isEvent(a.Name) {
			chain, base = a.Name[:j], a.Name[j+1:]
		}
		if says := carouselStrangers[base]; says != "" {
			return nil, nil, errAttr(n, a, "carousel không có %s: %s", base, says)
		}
		if transitionStrangers[base] {
			return nil, nil, errAttr(n, a, "carousel không có %s: cách một trang nhường trang sau là transition (slide, fade hay none)", base)
		}
		switch base {
		case "role", "aria-live", "aria-label", "aria-roledescription":
			return nil, nil, errAttr(n, a, "vai trò của carousel là việc của engine (role=\"region\", trang chiếu đánh số): bỏ %s; tên của nó là alt (alt=\"Món nổi bật\")", base)
		case "transition":
			if value := strings.TrimSpace(a.Value); chain == "" && slideDirection(value) {
				// a carousel has no place: its slides move along the line, never toward an edge
				return nil, nil, errAttr(n, a, `transition=%q: trang chiếu trượt theo dòng chữ của trang, trang sau đến từ cuối dòng; transition chỉ là một chữ: slide, fade hay none`, value)
			}
			transition, err := transitionOf(n, a, chain, "carousel")
			if err != nil {
				return nil, nil, err
			}
			scope.transition = transition
			continue
		case "duration":
			ms, err := c.carouselDuration(n, a, chain)
			if err != nil {
				return nil, nil, err
			}
			scope.duration, scope.durationAt = ms, a
			continue
		case "alt":
			if chain != "" {
				return nil, nil, errAttr(n, a, "alt của carousel không nhận tiền tố: nó là tên của carousel")
			}
			if strings.Contains(a.Value, "{{") {
				scope.labelBound = true
				host.Attrs = append(host.Attrs, *a)
				continue
			}
			scope.label = strings.TrimSpace(a.Value)
			continue
		}
		if base == "current" && chain == "" && !strings.Contains(a.Value, "{{") {
			// the slide it starts on, written as a number: read as one, so the first paint counts from it
			at, err := strconv.Atoi(strings.TrimSpace(a.Value))
			if err != nil || at < 0 {
				return nil, nil, errAttr(n, a, `current là trang nó bắt đầu, đếm từ 0 (current="0" là trang đầu), không phải %q`, a.Value)
			}
			scope.start, scope.startAt = at, a
			continue
		}
		host.Attrs = append(host.Attrs, *a)
	}
	if err := c.carouselTrack(n, scope); err != nil {
		return nil, nil, err
	}
	if scope.startAt != nil && scope.count >= 0 && scope.start >= scope.count {
		return nil, nil, errAttr(n, scope.startAt, `current="%d" quá số trang: dải có %d trang, đếm từ 0 (trang cuối là current="%d")`, scope.start, scope.count, scope.count-1)
	}
	return &host, scope, nil
}

// carouselTrack finds the track: the one <list> right inside the host. Its items are the slides.
func (c *compiler) carouselTrack(n *Node, scope *carouselScope) error {
	const how = `<list><item>…</item><item>…</item></list>`
	for _, ch := range n.Elements() {
		if ch.Tag != "list" {
			continue
		}
		if scope.track != nil {
			return errAt(ch, "carousel có một dải trang: <list> ở %s:%d:%d đã là dải của nó; gộp các trang vào một <list>", scope.track.File, scope.track.Line, scope.track.Column)
		}
		scope.track = ch
	}
	if scope.track == nil {
		return errAttr(n, scope.component, "carousel cần một <list> ngay bên trong, mỗi <item> là một trang chiếu: %s", how)
	}
	for _, word := range []string{"flex", "grid", "columns"} {
		if a := attrNamedBase(scope.track, word); a != nil {
			return errAttr(scope.track, a, "dải trang của carousel là một hàng engine đặt (cuộn ngang, dừng ở mỗi trang): bỏ %s; độ rộng của mỗi trang là width trên <item>", a.Name)
		}
	}
	// written out, each slide's place is known, and so is its name ("2 / 3"); one written with for
	// repeats, one written with if may not be there, and the component names them all as it starts.
	// How many the server paints is then the page's data: a for's list, only its rows the if keeps, and
	// 1 or 0 for an if.
	written := 0
	var lists []string
	for _, it := range scope.track.Elements() {
		switch it.Tag {
		case "empty":
			continue // what the list shows when it has no item: no slide
		case "fragment":
			return errAt(it, "mỗi trang của carousel là một <item> viết thẳng trong <list> (for và if đặt trên <item>): bỏ <fragment>")
		}
		var kept *expr
		if a := attrNamed(it, "if"); a.Name == "if" {
			e, err := compileExpr(a.Value, it, a)
			if err != nil {
				return err
			}
			kept = e
		}
		if a := attrNamed(it, "for"); a.Name == "for" {
			scope.slides[it] = -1
			each, err := parseFor(a.Value, it, a)
			if err != nil {
				return err
			}
			rows := "(" + each.list.source + ")"
			if kept != nil {
				params := each.item
				if each.index != "" {
					params += ", " + each.index
				}
				rows += ".filter((" + params + ") => (" + kept.source + "))"
			}
			lists = append(lists, rows+".length")
			continue
		}
		if kept != nil {
			scope.slides[it] = -1
			lists = append(lists, "(("+kept.source+") ? 1 : 0)")
			continue
		}
		scope.slides[it] = written
		written++
	}
	if len(scope.slides) == 0 {
		return errAt(scope.track, "dải trang của carousel chưa có trang nào: mỗi <item> trong %s là một trang", how)
	}
	scope.count = written
	if len(lists) > 0 {
		scope.count = -1
		scope.countFrom = strings.Join(append([]string{strconv.Itoa(written)}, lists...), " + ")
	}
	scope.trackID = strings.TrimSpace(attrNamed(scope.track, "id").Value)
	if scope.trackID == "" {
		scope.trackID = fmt.Sprintf("kitwork-carousel-%d", c.next("carousel"))
	}
	return nil
}

// slideDirection reports whether a transition is slide and a direction (slide-left, slide up): the words
// other libraries write, where a carousel's slides only move along the line.
func slideDirection(value string) bool {
	first, rest, ok := strings.Cut(strings.ToLower(value), "-")
	if !ok {
		first, rest, _ = strings.Cut(strings.ToLower(value), " ")
	}
	return first == "slide" && transitionDirections[strings.TrimSpace(rest)]
}

// attrNamedBase is the attribute an element writes under that name, with or without a prefix, nil for none.
func attrNamedBase(n *Node, name string) *Attr {
	for i := range n.Attrs {
		a := &n.Attrs[i]
		if a.Name == name || strings.HasSuffix(a.Name, ":"+name) {
			return a
		}
	}
	return nil
}

// carouselDuration reads duration= on a carousel: how long each slide stays while it plays by itself,
// a duration (duration.go: "5s", "800ms", "0.5m"), from 2 seconds (a slide is read) to 60.
func (c *compiler) carouselDuration(n *Node, a *Attr, chain string) (float64, error) {
	value := strings.TrimSpace(a.Value)
	switch {
	case chain != "":
		return 0, errAttr(n, a, "duration của carousel không nhận tiền tố: nó được quyết một lần khi trang dựng")
	case a.Bare || value == "":
		return 0, errAttr(n, a, `duration cần một thời lượng: mỗi trang ở lại bao lâu (duration="5s"); không tự chạy thì bỏ duration`)
	case strings.Contains(value, "{{"):
		return 0, errAttr(n, a, `duration của carousel là một thời lượng cố định (duration="5s"), không có {{ }}: nó được quyết một lần khi trang dựng`)
	}
	d := readDuration(value)
	switch d.problem {
	case durationNegative:
		return 0, errAttr(n, a, `duration không nhận thời lượng âm (%s): viết một thời lượng như duration="5s"`, value)
	case durationZero:
		return 0, errAttr(n, a, `duration="%s": không tự chạy thì bỏ duration`, a.Value)
	case durationNoUnit:
		return 0, errAttr(n, a, `duration="%s" thiếu đơn vị: viết duration="%s"`, a.Value, d.suggestion(durationMilliseconds))
	case durationTwoUnits, durationForeign:
		return 0, errAttr(n, a, `duration="%s" %s: viết duration="%s"`, a.Value, d.reason(), d.suggestion(durationMilliseconds))
	case durationNotOne:
		return 0, errAttr(n, a, `duration="%s" không phải một thời lượng: viết duration="5s" hay duration="800ms"`, a.Value)
	}
	if d.ms > 60000 {
		return 0, errAttr(n, a, `duration="%s" dài hơn 60 giây: mỗi trang ở lại tới khi người xem chuyển thì bỏ duration`, a.Value)
	}
	if d.ms < toastShortest {
		c.warn(n.File, a.Line, a.Column, `duration="`+a.Value+`" dưới 2 giây: người xem đọc không kịp một trang; viết duration từ "2s"`)
	}
	return d.ms, nil
}

// carouselWords are the words of the host a screen reader hears, in the page's language.
func (c *compiler) carouselWords() (region, slide string) {
	if language := strings.ToLower(c.site.Language); language != "" && !strings.HasPrefix(language, "vi") {
		return "carousel", "slide"
	}
	return "trình chiếu", "trang chiếu"
}

// carouselLabel is the host's name: alt, else the word for a carousel in the page's language.
func (c *compiler) carouselLabel(s *carouselScope) string {
	if s.label != "" {
		return s.label
	}
	if language := strings.ToLower(c.site.Language); language != "" && !strings.HasPrefix(language, "vi") {
		return "Carousel"
	}
	return "Trình chiếu"
}

// carouselElement writes what the engine adds to an element inside a carousel's host: the track's focus
// and live region, a slide's role and name, a dot's aria-current. It also notes a click that stops it.
func (c *compiler) carouselElement(n *Node, p *attrPlan, o *elemOp, w *strings.Builder, cx ctx) error {
	s := cx.carousel
	_, slide := c.carouselWords()
	switch {
	case n == s.track:
		if p.id == "" {
			writeAttr(w, "id", s.trackID)
		}
		w.WriteString(` tabindex="0"`)
		in, err := parseInterpolation("{{ playing ? 'off' : 'polite' }}", n, s.component)
		if err != nil {
			return err
		}
		o.dynAttrs = append(o.dynAttrs, dynAttr{name: "aria-live", in: in})
		return nil
	}
	if at, ok := s.slides[n]; ok {
		writeAttr(w, "aria-roledescription", slide)
		// a slide the author names with alt keeps that name (the item writes it as aria-label)
		if at >= 0 && s.count > 0 && attrNamed(n, "alt").Name != "alt" {
			writeAttr(w, "aria-label", fmt.Sprintf("%d / %d", at+1, s.count))
		}
		if at >= 0 && s.count > 0 {
			// the slide it starts on, before the component starts: fade shows it from the first frame
			current := "false"
			if at == s.start {
				current = "true"
			}
			writeAttr(w, "aria-current", current)
		}
		return nil
	}
	if n.Tag != "button" {
		return nil
	}
	for i := range p.events {
		a := &p.events[i]
		head, _, _ := strings.Cut(a.Name, ":")
		if !strings.Contains(","+head+",", ",click,") {
			continue
		}
		e, err := compileExpr(a.Value, n, a)
		if err != nil {
			return err
		}
		if owner := cx.scope.declaring("go"); owner == nil || owner.component == nil || owner.component.name != "carousel" {
			return nil
		}
		walkIR(e.ir, func(node []any) {
			if bareCall(node, "pause") || bareCall(node, "toggle") {
				s.stops = true
			}
		})
		arg, ok := goArgument(e.ir, a.Value)
		if !ok {
			continue
		}
		place, err := compileExpr(arg, n, a)
		if err != nil {
			return err
		}
		if client, err := cx.scope.onClient(place, n, a); err != nil || client {
			return err // go(current + 2): a button that moves, no dot
		}
		if v := p.semantic["value"]; v != nil {
			return errAttr(n, v, "value của một chấm là trang go(…) trỏ tới, engine viết từ go(%s): bỏ value", arg)
		}
		for j := range n.Attrs {
			if strings.HasPrefix(n.Attrs[j].Name, "selected:") {
				c.warn(n.File, n.Attrs[j].Line, n.Attrs[j].Column, n.Attrs[j].Name+`: một chấm của carousel là trang đang hiện khi go(…) trỏ tới nó, và vẽ bằng active: (aria-current), không phải selected: (của tab); viết active:`+strings.TrimPrefix(n.Attrs[j].Name, "selected:"))
			}
		}
		// the place, written by the server (a for's index is the server's), read back in the browser
		for _, d := range []struct{ name, source string }{
			{"value", "{{ " + arg + " }}"},
			{"aria-current", "{{ (" + arg + ") == " + strconv.Itoa(s.start) + " ? 'true' : 'false' }}"},
		} {
			in, err := parseInterpolation(d.source, n, a)
			if err != nil {
				return err
			}
			o.dynAttrs = append(o.dynAttrs, dynAttr{name: d.name, in: in})
		}
		writeAttr(w, "aria-controls", s.trackID)
		p.events[i].Value = "go(Number($this.value))"
	}
	return nil
}

// goArgument reads a click that is one bare go(…) into the source of its one argument, as written.
func goArgument(ir any, written string) (string, bool) {
	node, ok := ir.([]any)
	if !ok || !bareCall(node, "go") {
		return "", false
	}
	if args, ok := node[2].([]any); !ok || len(args) != 1 {
		return "", false
	}
	source := strings.TrimSuffix(strings.TrimSpace(written), ";")
	if !strings.HasPrefix(source, "go(") || !strings.HasSuffix(source, ")") {
		return "", false
	}
	return strings.TrimSpace(source[len("go(") : len(source)-1]), true
}

// bareCall reports whether an expression node is a call of a bare name: name(…).
func bareCall(node []any, name string) bool {
	if len(node) != 3 || node[0] != "call" {
		return false
	}
	callee, ok := node[1].([]any)
	return ok && len(callee) == 2 && callee[0] == "$" && callee[1] == name
}

// checkCarousels tells a carousel that plays by itself with nothing on the page to stop it: no click in
// its host calls pause() or toggle(), and none calls them through its alias (WCAG 2.2.2).
func (c *compiler) checkCarousels() {
	for _, s := range c.carousels {
		if s.duration <= 0 || s.stops {
			continue
		}
		if s.alias != "" {
			for _, use := range c.aliasExprs {
				walkIR(use.e.ir, func(node []any) {
					if len(node) != 4 || node[0] != "()" {
						return
					}
					ref, ok := node[1].([]any)
					method, _ := node[2].(string)
					if ok && len(ref) == 2 && ref[0] == "$" && ref[1] == s.alias && (method == "pause" || method == "toggle") {
						s.stops = true
					}
				})
			}
		}
		if !s.stops {
			c.warn(s.node.File, s.durationAt.Line, s.durationAt.Column, `carousel tự chạy (duration="`+strings.TrimSpace(s.durationAt.Value)+`") mà trang không có cách dừng: nội dung tự chuyển cần một nút dừng (WCAG 2.2.2); viết <button click="toggle()"> trong <carousel>, hay gọi $…pause() ở chỗ khác`)
		}
	}
}
