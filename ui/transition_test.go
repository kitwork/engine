package ui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/kitwork/engine/ui/stylesheet"
)

// transition (17/10/2026, transition.go) is how a notice of the toast or of the network comes and goes:
// slide, fade or none, Kitwork's word for an effect and never CSS's property. These tests read what the
// compiler writes for it (a class on the host, none for slide), what it refuses and warns of, each at
// its place with its exact words, and the reboot.css rules the classes stand for.

// hostClasses is the class attribute of the first element of html whose data-kitwork-component names
// component (with or without an alias), "" when there is none.
func hostClasses(t *testing.T, html, component string) string {
	t.Helper()
	tag := regexp.MustCompile(`<[a-z]+ [^>]*data-kitwork-component="` + component + `(?:: [^"]*)?"[^>]*>`).FindString(html)
	if tag == "" {
		t.Fatalf("no host of %s in\n%s", component, html)
	}
	class := regexp.MustCompile(` class="([^"]*)"`).FindStringSubmatch(tag)
	if class == nil {
		return ""
	}
	return class[1]
}

// transitionClassCount is how many classes of a transition the page carries, wherever they stand.
func transitionClassCount(html string) int {
	return strings.Count(html, "transition-fade") + strings.Count(html, "transition-none")
}

// The toast is always placed, so slide (its default) is the placed host's own rules and writes no class;
// fade and none each write theirs after the stack's place, and nothing else of the page carries one (the
// notice, the regions a screen reader hears). Written with spaces around, it reads the same; the place
// does not change the class; the author's own classes on the host stay first. All compile quietly.
// CONTROL: transitionClasses returning nothing, and toastScope.classes() not appending it, each turn
// the fade and none rows red (run 17/10/2026).
func TestTransitionClassesOfTheToast(t *testing.T) {
	for _, c := range []struct{ name, words, class string }{
		{"not written: slide", ``, "flex-column toast toast-bottom-end"},
		{"slide", ` transition="slide"`, "flex-column toast toast-bottom-end"},
		{"fade", ` transition="fade"`, "flex-column toast toast-bottom-end transition-fade"},
		{"none", ` transition="none"`, "flex-column toast toast-bottom-end transition-none"},
		{"spaces around", ` transition=" fade "`, "flex-column toast toast-bottom-end transition-fade"},
		{"fade at the top", ` place="top" transition="fade"`, "flex-column toast toast-top transition-fade"},
		{"before its place", ` transition="none" place="top-start"`, "flex-column toast toast-top-start transition-none"},
		{"beside the author's own", ` gap="2" transition="fade" padding="4"`, "flex-column gap-2 padding-4 toast toast-bottom-end transition-fade"},
	} {
		t.Run(c.name, func(t *testing.T) {
			html := compileQuiet(t, toastSite(toastStack(` as="$toast"`+c.words), `<content><text>x</text></content>`))
			if got := hostClasses(t, html, "toast"); got != c.class {
				t.Errorf("classes of the host:\n got  %q\n want %q", got, c.class)
			}
			want := strings.Count(c.class, "transition-")
			if got := transitionClassCount(html); got != want {
				t.Errorf("the page carries %d classes of a transition, want %d (the host's):\n%s", got, want, html)
			}
		})
	}
}

// The network placed slides by default, as the toast does: no class; fade and none each write theirs
// after its place. In the flow it has no edge to slide from, so its default is none, which writes
// nothing (nothing moves there without a class); fade writes transition-fade, the one rule a host in the
// flow moves by. The order the words are written in changes nothing (slide before its place is no
// error), and the author's classes stay first. All compile quietly.
// CONTROL: networkSpec.classes() writing nothing in the flow turns the in-flow fade row red; written
// with transitionClasses(s.transition) in the flow, the in-flow none row carries transition-none and
// turns red; the placed rows follow transitionClasses (run 17/10/2026).
func TestTransitionClassesOfTheNetwork(t *testing.T) {
	for _, c := range []struct{ name, words, class string }{
		{"placed, not written: slide", ` place="top"`, "flex-column network network-top"},
		{"placed, slide", ` place="top" transition="slide"`, "flex-column network network-top"},
		{"placed, slide before its place", ` transition="slide" place="bottom"`, "flex-column network network-bottom"},
		{"placed, fade", ` place="bottom-start" transition="fade"`, "flex-column network network-bottom-start transition-fade"},
		{"placed, none", ` place="top-end" transition="none"`, "flex-column network network-top-end transition-none"},
		{"in the flow, not written: none", ``, "flex-column"},
		{"in the flow, fade", ` transition="fade"`, "flex-column transition-fade"},
		{"in the flow, none", ` transition="none"`, "flex-column"},
		{"in the flow beside the author's own", ` gap="2" transition="fade"`, "flex-column gap-2 transition-fade"},
	} {
		t.Run(c.name, func(t *testing.T) {
			source := `<content>` + networkWithNotice(` as="$network"`+c.words) + `</content>`
			host, html, warned := networkCompile(t, source)
			if warned != "" {
				t.Errorf("want no warning, got:\n%s", warned)
			}
			if got := hostClasses(t, host, "network"); got != c.class {
				t.Errorf("classes of the host:\n got  %q\n want %q", got, c.class)
			}
			want := strings.Count(c.class, "transition-")
			if got := transitionClassCount(html); got != want {
				t.Errorf("the page carries %d classes of a transition, want %d (the host's):\n%s", got, want, html)
			}
		})
	}
}

// transitionHosts are the two hosts that take transition, each writing words on its host on a line of
// their own (line 3, column 3: the place is counted across lines), with the file and source an error
// is placed in. The network is placed, so slide is one of its values here.
var transitionHosts = []struct {
	who   string
	write func(words string) (files map[string]string, file, source string)
}{
	{"toast", func(words string) (map[string]string, string, string) {
		overlay := toastStack("\n  as=\"$toast\"\n  " + words)
		return toastSite(overlay, `<content><text>x</text></content>`), "overlay.ui", `<overlay>` + overlay + `</overlay>`
	}},
	{"network", func(words string) (map[string]string, string, string) {
		source := `<content>` + networkWithNotice("\n  place=\"top\"\n  "+words) + `</content>`
		return content(source), "content.ui", source
	}},
}

// mustFailExactly compiles a site that must not compile and checks its error, place and words, is want.
func mustFailExactly(t *testing.T, files map[string]string, want string) {
	t.Helper()
	if got := compileError(t, files); got != want {
		t.Errorf("error:\n got  %q\n want %q", got, want)
	}
}

// What transition does not take is an error at the attribute, on either host, in the author's words
// (WHO is the host's component): a prefix (a width, a state: it is decided once, when the page
// compiles), no value (bare, empty, only spaces), a value from data, a value of CSS's transition (a
// time, an easing, a property, a function, several of them: told what .ui's transition is, and that
// speed is the engine's), and any other word (a direction of its own, another case, two of the three).
// The words other libraries give the choice (animation, animate, motion, effect), with any value or
// prefix, are told to write transition.
// CONTROL: each written right compiles (the class tests above); each branch of transitionOf, the
// strangers' map and cssTransition were disabled in turn and their rows went red (run 17/10/2026).
func TestTransitionWrittenWrongIsAnError(t *testing.T) {
	const (
		prefix   = "transition của WHO không nhận tiền tố: nó được quyết một lần khi trang dựng"
		noValue  = "transition của WHO cần một giá trị: slide, fade, none"
		fromData = "transition của WHO là chữ cố định (slide, fade, none), không có {{ }}: nó được quyết một lần khi trang dựng"
		css      = "transition=VALUE là cách viết của CSS; transition của .ui là kiểu hiệu ứng: slide (trượt từ mép), fade (hiện dần, mờ dần) hay none (ngay); nhanh chậm là việc của engine"
		other    = "transition của WHO là slide, fade hay none, không phải VALUE"
		stranger = "WHO không có NAME: hiệu ứng hiện và ẩn là transition (slide, fade hay none)"
	)
	cases := []struct{ name, words, says string }{
		{"a width", `medium:transition="fade"`, prefix},
		{"a state", `hover:transition="fade"`, prefix},
		{"bare", `transition`, noValue},
		{"empty", `transition=""`, noValue},
		{"only spaces", `transition="  "`, noValue},
		{"from data", `transition="{{ effect }}"`, fromData},
		{"from data beside a word", `transition="fade {{ x }}"`, fromData},
	}
	for _, value := range []string{"0.2s", "200ms", "opacity 0.2s", "all 0.3s ease", "fade 0.2s", "opacity, translate",
		"cubic-bezier(0.4, 0, 0.2, 1)", "all", "opacity", "transform", "translate", "ease", "ease-in", "ease-out", "ease-in-out", "linear"} {
		cases = append(cases, struct{ name, words, says string }{"CSS's " + value, `transition="` + value + `"`, strings.Replace(css, "VALUE", `"`+value+`"`, 1)})
	}
	for _, value := range []string{"Fade", "NONE", "zoom", "true", "slide fade", "fade, none", "slide-up fade", "slide-sideways"} {
		cases = append(cases, struct{ name, words, says string }{"the word " + value, `transition="` + value + `"`, strings.Replace(other, "VALUE", `"`+value+`"`, 1)})
	}
	// a direction after slide is the place's; fade-in and fade-out are what fade already does
	for _, value := range []string{"slide-up", "slide-down", "slide-left", "slide-in", "Slide-Up", "slide up", "slide-bottom"} {
		cases = append(cases, struct{ name, words, says string }{"the direction " + value, `transition="` + value + `"`,
			`transition="` + value + `": hướng trượt là của place (ở mép dưới thì trượt lên khi hiện, ở mép trên thì trượt xuống); transition chỉ là một chữ: slide, fade hay none`})
	}
	for _, value := range []string{"fade-in", "fade-out", "fade in"} {
		cases = append(cases, struct{ name, words, says string }{"the half " + value, `transition="` + value + `"`,
			`transition="` + value + `": fade đã gồm hiện dần lúc vào và mờ dần lúc ra; viết transition="fade"`})
	}
	for _, words := range []struct{ name, written string }{
		{"animation", `animation="fade"`}, {"animate", `animate="slide-in"`}, {"motion", `motion="none"`},
		{"effect", `effect="fade"`}, {"animation", `medium:animation="fade"`}, {"effect", `effect`},
	} {
		cases = append(cases, struct{ name, words, says string }{"the stranger " + words.written, words.written, strings.Replace(stranger, "NAME", words.name, 1)})
	}
	for _, host := range transitionHosts {
		for _, c := range cases {
			t.Run(host.who+"/"+c.name, func(t *testing.T) {
				files, file, source := host.write(c.words)
				mustFailExactly(t, files, where(file, source, c.words)+strings.ReplaceAll(c.says, "WHO", host.who))
			})
		}
	}
}

// A slide comes in from the edge its place names; a network in the flow has none, so slide written there
// is an error at the transition, naming place and fade. A value it does not take on an empty host stays
// an error (only what would work and does nothing is a warning, below).
// CONTROL: slide with a place compiles quietly, in either order (TestTransitionClassesOfTheNetwork); the
// check disabled, slide in the flow compiled and this test went red (run 17/10/2026).
func TestTransitionSlideNeedsAPlace(t *testing.T) {
	const slide = `transition="slide" trượt vào từ mép màn hình mà network không có place: viết place (bottom-end, bottom, bottom-start, top-end, top, top-start), hay transition="fade" cho khung nằm trong trang`
	for _, words := range []string{"\n  transition=\"slide\"", ` as="$network" gap="2" transition="slide"`} {
		source := `<content><box gap="2">` + networkWithNotice(words) + `</box></content>`
		mustFailExactly(t, content(source), where("content.ui", source, `transition=`)+slide)
	}
	source := `<content><box component="network" transition="0.2s" offline="close()" /></content>`
	mustFailExactly(t, content(source), where("content.ui", source, `transition=`)+
		`transition="0.2s" là cách viết của CSS; transition của .ui là kiểu hiệu ứng: slide (trượt từ mép), fade (hiện dần, mờ dần) hay none (ngay); nhanh chậm là việc của engine`)
}

// Written on an empty network, transition is a warning at its own word: the host is never seen, so
// nothing comes or goes. As place and duration there, what it was told does nothing it does not do: the
// host carries no class of a transition. Slide on an empty host is that warning, not the error of a
// slide without a place (nothing slides); a place and a transition on one empty host are each told at
// their own word, the place first whichever is written first (the transition is read once the whole
// host is: whether anything is inside, where it stands); an alias does not change it (a notice drawn
// elsewhere on the page is no child of the host, so the host's transition never reaches it).
// CONTROL: the same words on a host with a notice say nothing (TestTransitionClassesOfTheNetwork); the
// warning disabled, every row went red; the empty host written transition-fade (before 17/10/2026's fix)
// turned the fade rows red (run 17/10/2026).
func TestTransitionOnAnEmptyNetworkIsAWarning(t *testing.T) {
	const empty = ` trên network rỗng: thẻ chủ rỗng không hiện gì, transition không làm gì; vẽ thông báo bên trong (<card show="open">…</card>) hay bỏ transition`
	const place = `place="top" trên network rỗng: thẻ chủ rỗng chỉ nghe sự kiện và không hiện, place không đặt gì; vẽ thông báo bên trong (<card show="open">…</card>) hay bỏ place`
	for _, c := range []struct {
		name, words string
		said        []string // needle, message: one warning each, in this order
		class       string
	}{
		{"fade", ` transition="fade"`, []string{`transition=`, `transition="fade"` + empty}, "flex-column show-false"},
		{"none", ` transition="none"`, []string{`transition=`, `transition="none"` + empty}, "flex-column show-false"},
		{"slide without a place", ` transition="slide"`, []string{`transition=`, `transition="slide"` + empty}, "flex-column show-false"},
		{"spaces around", ` transition=" fade "`, []string{`transition=`, `transition="fade"` + empty}, "flex-column show-false"},
		{"a place and a slide", ` place="top" transition="slide"`, []string{`place=`, place, `transition=`, `transition="slide"` + empty}, "flex-column show-false"},
		{"a fade and a place", ` transition="fade" place="top"`, []string{`place=`, place, `transition=`, `transition="fade"` + empty}, "flex-column show-false"},
		{"with an alias", ` as="$network" transition="fade"`, []string{`transition=`, `transition="fade"` + empty}, "flex-column show-false"},
	} {
		t.Run(c.name, func(t *testing.T) {
			source := `<content><box component="network"` + c.words + ` offline="close()" /></content>`
			host, html, warned := networkCompile(t, source)
			var want []string
			for i := 0; i < len(c.said); i += 2 {
				want = append(want, where("content.ui", source, c.said[i])+c.said[i+1])
			}
			if warned != strings.Join(want, "\n") {
				t.Errorf("warnings:\n got  %s\n want %s", warned, strings.Join(want, "\n"))
			}
			if got := hostClasses(t, host, "network"); got != c.class {
				t.Errorf("classes of the host:\n got  %q\n want %q", got, c.class)
			}
			if n := transitionClassCount(html); n != 0 {
				t.Errorf("an empty host carries %d classes of a transition it was told does nothing:\n%s", n, html)
			}
		})
	}
}

// The two classes are element words (the engine writes them without an attribute and reboot.css styles
// them, so no page rule is made or looked for: TestRebootStylesEveryElementWord in ui/stylesheet reads
// each). CONTROL: either dropped from ElementWords turns this red; its rule dropped from reboot.css
// turns TestRebootStylesEveryElementWord red (run 17/10/2026).
func TestTransitionClassesAreElementWords(t *testing.T) {
	words := stylesheet.ElementWords()
	for _, class := range []string{"transition-fade", "transition-none"} {
		if !containsName(words, class) {
			t.Errorf("%s is not an element word: %v", class, words)
		}
	}
	for _, transition := range transitions {
		for _, class := range transitionClasses(transition) {
			if !containsName(words, class) {
				t.Errorf("transition %q writes %s, which is not an element word", transition, class)
			}
		}
	}
	if got := strings.Join(transitions, ", "); got != "slide, fade, none" {
		t.Errorf("transitions are %s, want slide, fade, none (slide, the placed default, first)", got)
	}
}

// What the classes stand for, in reboot.css: a notice of a placed host (and of a host written fade)
// moves on opacity and translate in 0.2s, display too (allow-discrete: a notice hidden by show stays
// drawn while it goes); it starts transparent half a rem from its edge (above it at the top places);
// fade starts transparent where it stands; it goes (the toast's data-leaving, a hidden network
// notice) transparent toward its edge in 0.15s, fade in place; none moves nothing. Every rule is
// inside :where(), zero specificity, so the one written later wins where two match one notice (a
// placed host written fade or none has both its place's rules and its transition's): the fade rules
// come after the slide ones, in @starting-style and out of it, and none after everything that moves.
// With less motion asked for, the reboot's rule makes it instant whatever the transition.
// CONTROL: the fade rule of @starting-style moved before the slide one, the fade rule of going moved
// before the top places' one, and the none rule moved before the moving one each turned this red; each
// rule removed, too (run 17/10/2026).
func TestTransitionRebootRulesLetFadeAndNoneWin(t *testing.T) {
	css := string(stylesheet.Reboot().CSS)
	layer := strings.Index(css, "@layer reboot{")
	if layer < 0 {
		t.Fatalf("no reboot layer in reboot.css")
	}
	const (
		moves      = `:where(.toast>*,.network>*,.transition-fade>*){transition:opacity 0.2s ease-out,translate 0.2s ease-out,display 0.2s allow-discrete}`
		slideStart = `:where(.toast>*,.network>*){opacity:0;translate:0 0.5rem}`
		topStart   = `:where(.toast-top-end>*,.toast-top>*,.toast-top-start>*,.network-top-end>*,.network-top>*,.network-top-start>*){translate:0 -0.5rem}`
		fadeStart  = `:where(.transition-fade>*){opacity:0;translate:none}`
		slideOut   = `:where(.toast>[data-leaving],.network>[hidden],.transition-fade>[hidden]){opacity:0;translate:0 0.5rem;transition-duration:0.15s}`
		topOut     = `:where(.toast-top-end,.toast-top,.toast-top-start,.network-top-end,.network-top,.network-top-start)>:where([data-leaving],[hidden]){translate:0 -0.5rem}`
		fadeOut    = `:where(.transition-fade>[data-leaving],.transition-fade>[hidden]){translate:none}`
		none       = `:where(.transition-none>*){transition:none}`
		lessMotion = `@media (prefers-reduced-motion:reduce){*,::before,::after{`
	)
	at := func(rule string) int {
		t.Helper()
		if n := strings.Count(css, rule); n != 1 {
			t.Fatalf("reboot.css has %d of %s, want one", n, rule)
		}
		i := strings.Index(css, rule)
		if i < layer {
			t.Errorf("%s is not in the reboot layer", rule)
		}
		return i
	}
	// the starting styles are the three rules of one @starting-style, the slide ones first
	starting := `@starting-style{` + slideStart + topStart + fadeStart + `}`
	if n := strings.Count(css, "@starting-style{"); n != 1 || !strings.Contains(css, starting) {
		t.Errorf("want one @starting-style, the slide rules then fade's:\n %s\nreboot.css has %d", starting, n)
	}
	in, start, top, fadeIn := at(moves), at(slideStart), at(topStart), at(fadeStart)
	out, topGoes, fadeGoes, still := at(slideOut), at(topOut), at(fadeOut), at(none)
	for _, order := range []struct {
		before, after int
		says          string
	}{
		{start, fadeIn, "fade starts where it stands only after the slide's start from the edge"},
		{top, fadeIn, "fade starts where it stands only after the top places' start from above"},
		{out, fadeGoes, "fade goes in place only after the slide's going toward the edge"},
		{topGoes, fadeGoes, "fade goes in place only after the top places' going upward"},
		{in, still, "none moves nothing only after the rule that moves a notice"},
		{out, still, "none moves nothing only after the rule that gives going its 0.15s"},
		{fadeGoes, still, "none is the last of the notice's rules"},
	} {
		if order.before > order.after {
			t.Errorf("out of order: %s", order.says)
		}
	}
	if i := strings.Index(css, lessMotion); i < layer || !strings.Contains(css[i:i+strings.Index(css[i:], "}")], "transition-duration:0.01ms!important") {
		t.Errorf("reboot.css lacks %s…transition-duration:0.01ms!important: a notice would move for a reader who asked for less motion", lessMotion)
	}
}
