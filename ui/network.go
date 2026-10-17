package ui

import (
	"fmt"
	"strconv"
	"strings"
)

// Kitwork's network component (17/10/2026, docs/components/network.md): whether the page can reach the
// network, and the moment that changes. The author draws the notice, as the toast's:
//
//	<box component="network" as="$network" place="bottom-start">
//	  <card show="open" flex="row" gap="3" padding="3">
//	    <icon show="!connected" value="wifi-off" color="warning+5" alt="Offline" />
//	    <icon show="connected" value="wifi" color="success" alt="Online" />
//	    <text weight="bold">{{ connected ? 'Back online' : 'You are offline' }}</text>
//	    <button click="close()"><icon value="x" alt="Dismiss" /></button>
//	  </card>
//	</box>
//
// connected is the browser's word; open is whether the notice shows (from going offline until the
// visitor closes it, and a few seconds after the connection comes back); close() closes it until the
// next change. offline and online are the component's events, heard on its host only
// (componentEventsHere). Written empty, the host is heard and never seen (host.go).
//
//   - place (optional) floats the host at an edge or corner of the screen, the toast's six (classes
//     network and network-<place>, reboot.css); without it the host stays where it is written, a line
//     of a sidebar, a banner over a form.
//   - A host with something inside is a status region (role="status"; on an <li>, which keeps its role
//     in its list, aria-live="polite" aria-atomic="true"): what shows there, and what it changes to, a
//     screen reader says. The role is the engine's, never written. A region (sidebar, footer…) is no
//     host: it keeps its landmark.
//   - Placed, it floats over whatever the page holds there: the contrast check measures it so (contrast.go).
//   - delay (optional, 1s when not written) is how long the network must stay gone before the component
//     counts it gone: a shorter break (a change of Wi-Fi) says nothing, opens nothing, sends no event.
//     The progressbar's word, with its meaning: how long before it shows. duration (optional, 4s) is
//     how long "back online" stays before it closes by itself, 0 until it is closed: the toast's word,
//     with its meaning. Both are read when the page compiles and reach the instance in milliseconds
//     (data-kitwork-scope); timeout and the toast's other strangers are told which of the two to write.

// networkSpec is what the network's host says about itself.
type networkSpec struct {
	place  string // "" when not written: the host stays in the page's flow
	status bool   // something is drawn inside: the host is a status region
	// delay and duration as written, in milliseconds; nil when not written (the component's own: 1s, 4s)
	delay, duration *float64
	// transition is how the notice comes and goes (transition.go): slide when placed, none in the flow,
	// unless written
	transition string
}

// networkLongestDelay is the longest the component may wait before it says the network is gone: past it,
// a page that does not answer goes untold for too long. networkLongestDuration is the toast's longest.
const (
	networkLongestDelay    = 10000
	networkLongestDuration = toastLongest
)

// networkStrangers are the words other libraries give these two times, each with the one to write.
var networkStrangers = map[string]string{
	"timeout": "duration", "time": "duration", "autoclose": "duration", "autoClose": "duration",
	"wait": "delay", "debounce": "delay", "grace": "delay",
}

// inputs are the parameters the kernel seeds the instance with before its init: the times written.
func (s *networkSpec) inputs() []input {
	var out []input
	for _, time := range []struct {
		name string
		ms   *float64
	}{{"delay", s.delay}, {"duration", s.duration}} {
		if time.ms == nil {
			continue
		}
		ms := strconv.FormatFloat(*time.ms, 'f', -1, 64)
		out = append(out, input{name: time.name, in: &interpolation{parts: []part{{text: ms}}}, source: ms})
	}
	return out
}

// classes are the host's own: where it floats (none in the flow) and how its notice comes and goes.
func (s *networkSpec) classes() []string {
	if s.place == "" {
		return transitionClasses(map[string]string{"fade": "fade"}[s.transition]) // in the flow only fade moves
	}
	return append([]string{"network", "network-" + s.place}, transitionClasses(s.transition)...)
}

// networkHost reads the network's own words (place, delay, duration) off its host; the host then goes on
// as the host of a component without a UI.
func (c *compiler) networkHost(n *Node) (*Node, *networkSpec, error) {
	spec := &networkSpec{status: len(n.SignificantChildren()) > 0}
	host := *n
	host.Attrs = nil
	var written *Attr // the transition written, nil for the default
	for i := range n.Attrs {
		a := &n.Attrs[i]
		chain, base := "", a.Name
		if j := strings.LastIndex(a.Name, ":"); j >= 0 && !isEvent(a.Name) {
			chain, base = a.Name[:j], a.Name[j+1:]
		}
		if word := networkStrangers[base]; word != "" {
			return nil, nil, errAttr(n, a, "network không có %s: %s", base, networkTimes(word))
		}
		if transitionStrangers[base] {
			return nil, nil, errAttr(n, a, "network không có %s: hiệu ứng hiện và ẩn là transition (slide, fade hay none)", base)
		}
		if base == "transition" {
			transition, err := transitionOf(n, a, chain, "network")
			if err != nil {
				return nil, nil, err
			}
			written = a
			spec.transition = transition
			continue
		}
		switch base {
		case "role", "aria-live", "aria-atomic":
			return nil, nil, errAttr(n, a, "vai trò của network là việc của engine (role=\"status\" khi bên trong có thông báo): bỏ %s", base)
		case "delay", "duration":
			if chain != "" {
				return nil, nil, errAttr(n, a, "%s của network không nhận tiền tố: nó được quyết một lần khi trang dựng", base)
			}
			ms, err := c.networkTime(n, a, base)
			if err != nil {
				return nil, nil, err
			}
			if base == "delay" {
				spec.delay = &ms
				continue
			}
			// on an empty host the one thing to say is that nothing shows: how fast it would read is
			// beside the point, and writing it 2s would still leave the host empty. An empty host with an
			// alias may be drawn elsewhere ($network.open on a card of the page): its duration counts.
			if !spec.status && attrNamed(n, "as").Name != "as" {
				c.warn(n.File, a.Line, a.Column, fmt.Sprintf("duration=%q trên network rỗng: thẻ chủ rỗng không hiện gì, duration không đóng gì; vẽ thông báo bên trong (<card show=\"open\">…</card>) hay bỏ duration", strings.TrimSpace(a.Value)))
				continue
			}
			if ms > 0 && ms < toastShortest {
				c.warn(n.File, a.Line, a.Column, `duration="`+a.Value+`" dưới 2 giây: người xem đọc không kịp; viết duration từ "2s", hay bỏ duration (4 giây)`)
			}
			spec.duration = &ms
			continue
		case "place":
		default:
			host.Attrs = append(host.Attrs, *a)
			continue
		}
		value := strings.TrimSpace(a.Value)
		switch {
		case chain != "":
			return nil, nil, errAttr(n, a, "place của network không nhận tiền tố: nó được quyết một lần khi trang dựng")
		case a.Bare || value == "":
			return nil, nil, errAttr(n, a, "place của network cần một giá trị: %s", strings.Join(toastPlaces, ", "))
		case strings.Contains(value, "{{"):
			return nil, nil, errAttr(n, a, "place của network là chữ cố định, không có {{ }}: nó được quyết một lần khi trang dựng")
		case !containsName(toastPlaces, value):
			return nil, nil, errAttr(n, a, "place của network là một mép hay góc màn hình: %s; không phải %q", strings.Join(toastPlaces, ", "), value)
		case !spec.status:
			c.warn(n.File, a.Line, a.Column, fmt.Sprintf("place=%q trên network rỗng: thẻ chủ rỗng chỉ nghe sự kiện và không hiện, place không đặt gì; vẽ thông báo bên trong (<card show=\"open\">…</card>) hay bỏ place", value))
			continue
		}
		spec.place = value
	}
	// the transition: what it moves needs a notice drawn inside, and a slide needs an edge to come from
	switch {
	case written != nil && !spec.status:
		c.warn(n.File, written.Line, written.Column, fmt.Sprintf("transition=%q trên network rỗng: thẻ chủ rỗng không hiện gì, transition không làm gì; vẽ thông báo bên trong (<card show=\"open\">…</card>) hay bỏ transition", strings.TrimSpace(written.Value)))
		spec.transition = "none" // told it does nothing, it writes nothing: as place and duration there
	case written != nil && spec.transition == "slide" && spec.place == "":
		return nil, nil, errAttr(n, written, "transition=\"slide\" trượt vào từ mép màn hình mà network không có place: viết place (%s), hay transition=\"fade\" cho khung nằm trong trang", strings.Join(toastPlaces, ", "))
	case written == nil && spec.place != "":
		spec.transition = transitions[0]
	case written == nil:
		spec.transition = "none"
	}
	return &host, spec, nil
}

// networkTimes says what each of the two times is, ending on the one a stranger meant.
func networkTimes(word string) string {
	if word == "delay" {
		return `chờ mạng mất bao lâu rồi mới báo là delay (delay="1s"); khung "đã có mạng lại" ở lại bao lâu là duration (duration="4s")`
	}
	return `khung "đã có mạng lại" ở lại bao lâu rồi tự đóng là duration (duration="4s", 0 là ở lại tới khi đóng); chờ mạng mất bao lâu rồi mới báo là delay (delay="1s")`
}

// networkTime reads delay= or duration= on a network's host: "1s", "800ms", or 0 (delay: say it at once;
// duration: stay until closed), in milliseconds. Anything else is an error that says what to write; a
// duration that reads too fast is told by the host, which knows whether anything shows (networkHost).
func (c *compiler) networkTime(n *Node, a *Attr, word string) (float64, error) {
	value := strings.TrimSpace(a.Value)
	example, zero := `delay="1s"`, "báo ngay"
	if word == "duration" {
		example, zero = `duration="4s"`, "ở lại tới khi đóng"
	}
	switch {
	case a.Bare || value == "":
		return 0, errAttr(n, a, "%s cần một thời lượng: viết %s (0 là %s)", word, example, zero)
	case strings.Contains(value, "{{"):
		return 0, errAttr(n, a, "%s của network là một thời lượng cố định (%s), không có {{ }}: nó được quyết một lần khi trang dựng", word, example)
	case strings.HasPrefix(value, "-") && (durationRe.MatchString(value[1:]) || unitlessRe.MatchString(value[1:])):
		// a number after the minus is a negative time; anything else after it is no time at all (below)
		return 0, errAttr(n, a, "%s không nhận thời lượng âm (%s): viết một thời lượng như %s", word, value, example)
	case unitlessRe.MatchString(value):
		if number, _ := strconv.ParseFloat(value, 64); number == 0 {
			return 0, nil
		}
		return 0, errAttr(n, a, `%s="%s" thiếu đơn vị: viết %s="%s"`, word, a.Value, word, durationSuggestion(value))
	}
	m := durationRe.FindStringSubmatch(value)
	if m == nil {
		return 0, errAttr(n, a, `%s="%s" không phải một thời lượng: viết %s hay %s="800ms" (0 là %s)`, word, a.Value, example, word, zero)
	}
	// the digits the pattern took, read as the decimal they are written in, seconds a thousand times
	// that: "4.03s" is 4030 on the instance, where 4.03 * 1000 is 4030.0000000000005. Digits too many
	// to hold read as +Inf (strconv's ErrRange), longer than either limit, and are told so below.
	digits := m[1]
	if m[2] == "s" {
		digits += "e3"
	}
	ms, _ := strconv.ParseFloat(digits, 64)
	switch {
	case word == "delay" && ms > networkLongestDelay:
		return 0, errAttr(n, a, `delay="%s" dài hơn 10 giây: mất mạng lâu vậy mà không báo thì người xem không biết vì sao trang không chạy; viết delay="10s" hay ngắn hơn`, a.Value)
	case word == "duration" && ms > networkLongestDuration:
		return 0, errAttr(n, a, `duration="%s" dài hơn 60 giây: khung ở lại tới khi đóng thì viết duration="0"`, a.Value)
	}
	return ms, nil
}
