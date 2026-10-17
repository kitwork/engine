package ui

import (
	"strings"
	"unicode"
)

// transition is how a notice of the toast or of the network comes and goes (17/10/2026,
// docs/components/toast.md, network.md): Kitwork's word for an effect, the one router.transition takes,
// with the same three values. It is no CSS transition — .ui never writes that property, the engine
// animates a state change itself — so a value written the CSS way is told what to write instead.
//
//	<box component="toast" as="$toast" transition="fade">
//	<box component="network" place="bottom-start" transition="slide">
//
//   - slide: comes in from the edge its place names and goes back to it. The default of a placed host
//     (the toast always is); a host in the flow has no edge, so slide there is an error.
//   - fade: comes and goes in place, placed or not.
//   - none: at once. The default of a network host in the flow.
//
// The direction of a slide is the place's, never a word of its own (slide-up beside place="top" would
// say two things). How fast is the engine's: 200ms in, 150ms out, nothing with less motion asked for.
// The classes are reboot.css's: transition-fade and transition-none; slide is the placed host's own.

// transitions are the values, the default of a placed host first.
var transitions = []string{"slide", "fade", "none"}

// transitionDirections are the words other libraries put after slide: here the direction is the place's.
var transitionDirections = map[string]bool{"up": true, "down": true, "left": true, "right": true, "in": true, "out": true, "top": true, "bottom": true}

// transitionStrangers are the words other libraries give the same choice, each told to write transition.
var transitionStrangers = map[string]bool{"animation": true, "animate": true, "motion": true, "effect": true}

// transitionClasses are the host's classes for a transition, none for slide (the placed host's own).
func transitionClasses(transition string) []string {
	switch transition {
	case "fade", "none":
		return []string{"transition-" + transition}
	}
	return nil
}

// transitionOf reads transition= on the host of who (toast, network): one of the three, a word fixed when
// the page compiles. A value of CSS's transition (a property, a time, an easing) is told it is not one.
func transitionOf(n *Node, a *Attr, chain, who string) (string, error) {
	value := strings.TrimSpace(a.Value)
	switch {
	case chain != "":
		return "", errAttr(n, a, "transition của %s không nhận tiền tố: nó được quyết một lần khi trang dựng", who)
	case a.Bare || value == "":
		return "", errAttr(n, a, "transition của %s cần một giá trị: %s", who, strings.Join(transitions, ", "))
	case strings.Contains(value, "{{"):
		return "", errAttr(n, a, "transition của %s là chữ cố định (%s), không có {{ }}: nó được quyết một lần khi trang dựng", who, strings.Join(transitions, ", "))
	case containsName(transitions, value):
		return value, nil
	case cssTransition(value):
		return "", errAttr(n, a, "transition=%q là cách viết của CSS; transition của .ui là kiểu hiệu ứng: slide (trượt từ mép), fade (hiện dần, mờ dần) hay none (ngay); nhanh chậm là việc của engine", value)
	}
	if first, rest, ok := strings.Cut(strings.ToLower(value), "-"); ok || strings.Contains(value, " ") {
		if !ok {
			first, rest, _ = strings.Cut(strings.ToLower(value), " ")
		}
		rest = strings.TrimSpace(rest)
		switch {
		case first == "slide" && transitionDirections[rest]:
			return "", errAttr(n, a, "transition=%q: hướng trượt là của place (ở mép dưới thì trượt lên khi hiện, ở mép trên thì trượt xuống); transition chỉ là một chữ: slide, fade hay none", value)
		case first == "fade" && (rest == "in" || rest == "out"):
			return "", errAttr(n, a, "transition=%q: fade đã gồm hiện dần lúc vào và mờ dần lúc ra; viết transition=\"fade\"", value)
		}
	}
	return "", errAttr(n, a, "transition của %s là slide, fade hay none, không phải %q", who, value)
}

// cssTransition reports whether a value reads as CSS's transition: a function (cubic-bezier(…),
// steps(…)), or a time, an easing or a property among its words, alone or several. Words of no such
// kind ("slide fade", "slide-up fade") are not CSS's: they are told the three values.
func cssTransition(value string) bool {
	if strings.ContainsAny(value, "()") {
		return true
	}
	for _, word := range strings.FieldsFunc(value, func(r rune) bool { return unicode.IsSpace(r) || r == ',' }) {
		if durationRe.MatchString(word) {
			return true
		}
		switch word {
		case "all", "opacity", "transform", "translate", "ease", "ease-in", "ease-out", "ease-in-out", "linear":
			return true
		}
	}
	return false
}
