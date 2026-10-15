package stylesheet

import (
	"math"
	"strconv"
	"strings"
)

// material="none|glass|glass/N" (decision M, 12/10/2026; glass/N, decision of 13/10/2026) is the
// surface of a frame, named after Apple's Material and Fluent's Materials. Glass is frosted: the frame's
// own background, its tint, made translucent, with what lies behind it blurred and saturated. glass/N
// says how much of the glass the tint covers, N from 10 to 95, and glass alone is glass/75: glass/60 is
// thin, glass/88 thick — the three that were glass.thin, glass and glass.thick, the same CSS byte for
// byte. The thicker the glass, the more it blurs (glassBlur). The author never repeats the colour:
// background="brand" material="glass" is brand glass, and a frame that writes no background is paper
// glass. none is no material: medium:material="none" undoes material="glass".
//
// Glass first, background second (decision of 13/10/2026, "vậy triển khai đi"): where the browser
// blurs, the glass is the tint at the material's coverage, the tint being the background's colour
// without its /N (an operator colour keeps its operator: brand+15/80 is brand+15 glass); where it
// cannot, the frame shows its background exactly as written, /N included. scrolled:material="glass/30"
// scrolled:background="paper/80" is paper at 30% over the blurred page, and paper/80 without a blur:
// the /N of background is the fallback's opacity, the material's N the glass's. Before, the tint
// carried the alpha into the glass (glass/30 over paper/80 was paper at 24%) and the compiler warned
// of the /N; the warning is gone with it. material="none" gives the background back as written.
//
// On the web the tint is made translucent the way the table writes an alpha (paper/80 is
// color-mix(in srgb, … 80%, transparent)), over a backdrop-filter written twice, -webkit- first for
// Safari 15.4 to 17. A background class hands the material two colours, only on a page that has a
// material (Rules.CSS, handOver): the colour as written in backgroundVariable, for the fallback and for
// none, and the tint in glassVariable, for the glass. reboot.css starts every frame with a material
// from nothing, so a frame never takes the colour of the frame around it. In the page layer the
// material comes right after background: at one condition the glass wins over the plain background,
// and a later condition still wins over both (hover:background="brand" over material="glass", as hover
// over anything unprefixed). Where the browser cannot blur the backdrop the background shows as
// written; that is the one fallback. The glass always shows, also to a reader whose system asks for
// less transparency (prefers-reduced-transparency): decision of 13/10/2026. The owner's Windows has
// transparency effects off, so Chrome and Edge report reduce and every .ui glass went solid while his
// other sites, Tailwind backdrop-blur with no such media query, showed glass; he chose that Kitwork
// glass shows the same.
//
// A gradient background is unchanged by this: it is an image and hands nothing over, and a glass and a
// gradient that win the cascade at one moment are a compile error (gradientUnderGlass, ui/gradient.go:
// the gradient would be drawn over the glass), so a glass never reads one.
//
// Native, for the record (not built): SwiftUI .thinMaterial, .regularMaterial, .thickMaterial tinted
// with .tint() (iOS 26: .glassEffect()); Windows Acrylic; Android the background as written (the
// fallback: glass first, background second).

// Glass is what one material value makes of its tint: how much of the tint shows (percent), how far
// what lies behind is blurred (px) and how saturated (percent).
type Glass struct{ Opacity, Blur, Saturate int }

// materialWords are the values of material a value from data may be (Materials, the table's domain),
// none first, then the thin, the regular and the thick glass. Any glass/N from 10 to 95 is written too.
var materialWords = []string{"none", "glass/60", "glass", "glass/88"}

// How much of a glass its tint covers: glass alone, and the least and the most glass/N may write.
const (
	glassCoverage = 75
	leastGlass    = 10
	mostGlass     = 95
)

// GlassOf is the glass of a material value, glass or glass/N with N from 10 to 95; ok is false for
// none (no glass) and for a word that is not material's.
func GlassOf(value string) (Glass, bool) {
	v := strings.TrimSpace(value)
	n := glassCoverage
	if v != "glass" {
		coverage, ok := strings.CutPrefix(v, "glass/")
		if !ok {
			return Glass{}, false
		}
		if n, ok = whole(coverage, leastGlass, mostGlass); !ok {
			return Glass{}, false
		}
	}
	return Glass{Opacity: n, Blur: glassBlur(n), Saturate: 180}, true
}

// glassBlur is how far a glass whose tint covers n percent blurs what lies behind it, in px: 8 up to
// glass/60, 12 at glass/75, 20 from glass/88 on, along a straight line between them, rounded — the
// thin, regular and thick glass of before at their own blur, and a thicker glass never less blurred.
func glassBlur(n int) int {
	switch {
	case n <= 60:
		return 8
	case n >= 88:
		return 20
	case n <= 75:
		return int(math.Round(8 + float64(n-60)*4/15))
	}
	return int(math.Round(12 + float64(n-75)*8/13))
}

// The two custom properties a background class hands a material over in, on a page that has one
// (handOver). backgroundVariable is the background exactly as written, /N included: the fallback and
// none read it. glassVariable is the tint, that colour without its /N: the glass reads it at its own
// coverage. --kitwork-background kept its name and meaning (the frame's background); the tint took a
// name of its own, after the one that reads it, that no other variable of the engine starts with or
// is: --kitwork-tint is a badge's fill, --kitwork-focus the focus ring, --kitwork-strong-* and
// --kitwork-gradient-* the colours per mode.
const (
	backgroundVariable = "--kitwork-background"
	glassVariable      = "--kitwork-glass"
)

// tint is the colour a glass is made of: the frame's own background without its /N, else paper.
const tint = "var(" + glassVariable + ",var(--paper))"

// fallback is what a frame with a glass shows where the browser cannot blur: its own background as
// written, else paper.
const fallback = "var(" + backgroundVariable + ",var(--paper))"

// handOver is what a background class adds to its rule on a page with a material: its colour as
// written, and the tint a glass is made of.
func handOver(written, glass string) string {
	return backgroundVariable + ":" + written + ";" + glassVariable + ":" + glass
}

// GlassColour is the colour a glass over a background value is made of: the value without its /N
// (paper/80 is paper glass, brand+15/80 brand+15 glass: the operator stays). A value with no /N, and
// one that is no colour value (a gradient's name, a value from data), comes back as it is.
func GlassColour(background string) string {
	c, ok := SplitColour(background)
	if !ok || !c.Translucent {
		return strings.TrimSpace(background)
	}
	c.Translucent, c.Alpha = false, 100
	return c.String()
}

// noBackdropFilter is the one condition the glass falls back to the background as written under: a
// browser that cannot blur the backdrop, whichever way it is written. There is no other: since the
// decision of 13/10/2026 a reader who asked for less transparency (prefers-reduced-transparency:
// Windows' transparency effects off, macOS and iOS' Reduce Transparency) sees the glass like anyone
// else, as on the sites written with Tailwind's backdrop-blur, which no such media query turns off.
const noBackdropFilter = "@supports not ((backdrop-filter:blur(1px)) or (-webkit-backdrop-filter:blur(1px)))"

// readMaterial reads material. A glass is the tint at its opacity over the blurred, saturated
// backdrop, and one scoped rule wrapped in the condition it falls back under, right after its own, so
// the fixed order holds for it too: the background as written, else paper. none gives back what the
// frame has without a material: the background a class hands over as written (a card's and a
// popover's paper, reboot.css), else nothing, and no filter.
func readMaterial(_ *Table, in Intent, v string) (reading, *Problem) {
	if v == "none" {
		return reading{declarations: "background-color:var(" + backgroundVariable + ",transparent);-webkit-backdrop-filter:none;backdrop-filter:none"}, nil
	}
	if g, ok := GlassOf(v); ok {
		filter := "blur(" + strconv.Itoa(g.Blur) + "px) saturate(" + strconv.Itoa(g.Saturate) + "%)"
		return reading{
			declarations: "background-color:color-mix(in srgb," + tint + " " + strconv.Itoa(g.Opacity) + "%,transparent);" +
				"-webkit-backdrop-filter:" + filter + ";backdrop-filter:" + filter,
			scoped: []scopedRule{
				{wrap: noBackdropFilter, declarations: "background-color:" + fallback},
			},
		}, nil
	}
	return reading{}, materialProblem(in, v)
}

// materialProblem refuses a word material does not take, and names the one meant when a word says it:
// the glass words of before (glass.thin is glass/60, glass.thick glass/88: decision of 13/10/2026), a
// coverage written after a dot (glass.70 is glass/70) or outside 10 to 95 (glass/5 is glass/10), and a
// word another system names a glass or no glass with.
func materialProblem(in Intent, v string) *Problem {
	const accepts = "material nhận none, glass hoặc glass/N (N từ 10 đến 95; glass là glass/75)"
	p := problem(in, accepts+", không có \""+v+"\"")
	say := func(suggestion, message string) *Problem {
		p.Suggestion = suggestion
		p.Message = "material=\"" + v + "\": " + message
		return p
	}
	if coverage, ok := strings.CutPrefix(v, "glass/"); ok {
		suggestion := ""
		if n, ok := nearestCoverage(coverage); ok {
			suggestion = "glass/" + strconv.Itoa(n)
		}
		return say(suggestion, "N sau glass/ là độ phủ của màu kính, số nguyên từ 10 đến 95 (glass/60 mỏng, glass/75 vừa, glass/88 dày)")
	}
	if coverage, ok := strings.CutPrefix(v, "glass."); ok {
		if n, ok := nearestCoverage(coverage); ok {
			suggestion := "glass/" + strconv.Itoa(n)
			message := "độ phủ của kính viết sau dấu /: " + suggestion
			if strconv.Itoa(n) != coverage {
				message += " (N từ 10 đến 95)"
			}
			return say(suggestion, message)
		}
	}
	switch v {
	case "glass.thin", "glass-thin", "thin", "thin-glass", "ultrathin", "ultra-thin":
		return say("glass/60", "bậc chữ của kính đã bỏ (13/10/2026): kính mỏng giờ là glass/60 (màu kính phủ 60%)")
	case "glass.thick", "glass-thick", "thick", "thick-glass", "ultrathick", "ultra-thick":
		return say("glass/88", "bậc chữ của kính đã bỏ (13/10/2026): kính dày giờ là glass/88 (màu kính phủ 88%)")
	case "regular", "true", "blur", "frosted", "translucent", "frost":
		p.Suggestion = "glass"
	case "false", "solid", "opaque":
		p.Suggestion = "none"
	case "acrylic", "mica":
		return say("glass", v+" là chất liệu cửa sổ của Windows, chưa có; khung thì viết glass (glass/60 mỏng, glass/75 vừa, glass/88 dày)")
	}
	if p.Suggestion != "" {
		p.Message = "material=\"" + v + "\": viết material=\"" + p.Suggestion + "\". " + accepts
	}
	return p
}

// nearestCoverage is a coverage written in digits brought into 10 to 95: 5 is 10, 100 is 95, 070 is 70.
// ok is false for anything but digits.
func nearestCoverage(digits string) (int, bool) {
	if !isDigits(digits) {
		return 0, false
	}
	n, err := strconv.Atoi(digits)
	if err != nil || n > mostGlass {
		n = mostGlass // more digits than an int holds: as far past 95 as can be
	}
	return max(n, leastGlass), true
}

// TintOf finds the backgrounds a material at its condition is tinted with (decision M): of an
// element's background intents, those written for the condition that holds whenever the material's
// does — no width or one that starts no wider, no state or the material's own — and comes last in the
// fixed order, the one the cascade hands the variable from. Several come back only when several
// backgrounds are written for that one condition (the values a {{ }} spells out); none means the glass
// is paper, or a card's or a popover's paper (reboot.css). The material's state is one word or none: a
// group (hover,focus:material) holds under each of its words, so ask for each. A pinned frame that
// writes no background gets the page's from the compiler, a background like any other: pass it.
func TintOf(material Intent, backgrounds []Intent) []Intent {
	var out []Intent
	for _, i := range tintsOf(material, backgrounds) {
		out = append(out, backgrounds[i])
	}
	return out
}

// tintsOf is TintOf as places in backgrounds.
func tintsOf(material Intent, backgrounds []Intent) []int {
	m, p := parseCondition(material)
	if p != nil || len(m.states) > 1 {
		return nil
	}
	width := indexOf(widthWords, m.width) + 1
	state := 0
	if len(m.states) == 1 {
		state = indexOf(stateWords, m.states[0]) + 1
	}
	var out []int
	bestState, bestWidth := -1, -1
	for i, b := range backgrounds {
		c, p := parseCondition(b)
		if p != nil {
			continue
		}
		w := indexOf(widthWords, c.width) + 1
		s := 0
		if len(c.states) > 0 {
			if state == 0 || indexOf(c.states, stateWords[state-1]) < 0 {
				continue
			}
			s = state
		}
		switch {
		case w > width:
			continue // starts on a wider screen than the material: not there whenever the material is
		case s > bestState || (s == bestState && w > bestWidth):
			out, bestState, bestWidth = []int{i}, s, w
		case s == bestState && w == bestWidth:
			out = append(out, i)
		}
	}
	return out
}

// readsOf is every background a glass at its condition can read at some moment, its tint and, where
// the browser cannot blur, its fallback: both are the variables the background that wins the cascade
// at that moment hands over, and the glass shows while no background that holds then comes after it
// in the fixed order. A background written for another condition is read too when both can hold at
// once and it comes before the glass (a wider width under a scrolled: glass, an earlier state under a
// hover: one), unless a background that holds whenever both do comes after it. The fewest conditions
// that make both hold are where it is read if anywhere: more only add backgrounds that hide it or beat
// the glass. The material's state is one word or none (ask for each of a group); its own states are
// passed in all, for its place in the order. It was the reach of the warning about a /N under a
// material (CheckMaterial, removed with the decision of 13/10/2026 that the /N is the fallback's); it
// stays as the statement, tested, of which backgrounds a glass reads.
func readsOf(material Intent, all []string, backgrounds []Intent) []int {
	m, p := parseCondition(material)
	if p != nil || len(m.states) > 1 {
		return nil
	}
	type held struct {
		width  int
		states []string
	}
	conditions := make([]held, len(backgrounds))
	for i, b := range backgrounds {
		c, p := parseCondition(b)
		if p != nil {
			conditions[i].width = -1
			continue
		}
		conditions[i] = held{width: indexOf(widthWords, c.width) + 1, states: c.states}
	}
	// key is where a class stands in the fixed order at a moment: its latest state that holds, its
	// width, background before material; ok is false when it does not hold then
	key := func(width int, states []string, rank int, at held) ([3]int, bool) {
		if width < 0 || width > at.width {
			return [3]int{}, false
		}
		state := 0
		if len(states) > 0 {
			state = -1
			for _, s := range states {
				if indexOf(at.states, s) >= 0 && indexOf(stateWords, s)+1 > state {
					state = indexOf(stateWords, s) + 1
				}
			}
			if state < 0 {
				return [3]int{}, false
			}
		}
		return [3]int{state, width, rank}, true
	}
	less := func(a, b [3]int) bool {
		return a[0] < b[0] || a[0] == b[0] && (a[1] < b[1] || a[1] == b[1] && a[2] < b[2])
	}
	width := indexOf(widthWords, m.width) + 1
	var own []string // the material's own states, for its place in the order
	for _, s := range all {
		if s != "" {
			own = append(own, s)
		}
	}
	var out []int
	for i, b := range conditions {
		if b.width < 0 {
			continue
		}
		words := b.states
		if len(words) == 0 {
			words = []string{""}
		}
		for _, word := range words {
			at := held{width: max(width, b.width)}
			for _, s := range []string{strings.Join(m.states, ""), word} {
				if s != "" && indexOf(at.states, s) < 0 {
					at.states = append(at.states, s)
				}
			}
			glass, _ := key(width, own, 1, at)
			mine, holds := key(b.width, b.states, 0, at)
			if !holds || !less(mine, glass) {
				continue // it beats the glass whenever both hold
			}
			read := true
			for j, other := range conditions {
				if k, ok := key(other.width, other.states, 0, at); j != i && ok && less(mine, k) {
					read = false // a later background holds then: it is the tint, or it beats the glass
					break
				}
			}
			if read && indexOfInt(out, i) < 0 {
				out = append(out, i)
			}
		}
	}
	return out
}

func indexOfInt(list []int, v int) int {
	for i, x := range list {
		if x == v {
			return i
		}
	}
	return -1
}
