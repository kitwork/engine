package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/kitwork/engine/ui/stylesheet"
)

// The carousel (18/10/2026, carousel.go) is written on a container the author chooses; the author draws
// the slides in one <list>, the buttons and the dots, and the engine recognises them and writes the
// wiring. These tests read what the compiler writes for each part, the state the server paints with,
// and what it refuses and warns of, each at its place with its exact words.

// carouselSlides are two slides written out.
const carouselSlides = `<list><item><text>Cà phê</text></item><item><text>Bạc xỉu</text></item></list>`

// carouselStop is the button that stops a carousel that plays by itself.
const carouselStop = `<button click="toggle()">Dừng</button>`

// carouselOf is a carousel with what is written inside it; words go on the tag.
func carouselOf(words, inside string) string {
	return `<carousel` + words + `>` + inside + `</carousel>`
}

// carouselDishes is a list of three for a for to repeat.
var carouselDishes = map[string]any{"mons": []any{"Cà phê", "Bạc xỉu", "Trà đào"}}

// carouselButton is how a button with no fill of its own starts: the neutral fill the engine gives it.
const carouselButton = `<button class="flex-row background-ink/6 hover:background-ink/10 pressed:background-ink/14 disabled:background-ink/4" type="button"`

// carouselPage compiles a site whose content.ui is source, in the page's language, and renders "/" with
// the data given; it returns the page and the warnings, one line each.
func carouselPage(t *testing.T, language, source string, data map[string]any) (string, []string) {
	t.Helper()
	s := memorySite("", content(source))
	s.Reload, s.Language = true, language
	page, warned := compileDiagnose(t, s)
	return page.Render(Request{Path: "/", Scope: data}), warned
}

// carouselQuiet is carouselPage for a page that must compile with no warning at all.
func carouselQuiet(t *testing.T, language, source string, data map[string]any) string {
	t.Helper()
	html, warned := carouselPage(t, language, source, data)
	if len(warned) > 0 {
		t.Errorf("want no warning, got:\n%s", strings.Join(warned, "\n"))
	}
	return html
}

// The host is a region a screen reader names: role="region", aria-roledescription in the page's
// language (Vietnamese when the site says none, or a language of vi), aria-label from alt, else the
// page's word for a carousel. An alt that is empty or only spaces names nothing: the page's word. An alt
// that reads data is the host's own alt, bound to aria-label like any <box alt>, with no second name.
// The slides say their own role in the same language. Only the host is a region.
// CONTROL: a box with alt is named and is no region. The region's role dropped, the English words or
// the vi prefix changed, alt not read or not trimmed, a bound alt named twice, the slides' role or names
// dropped: each turned this red (run 18/10/2026, on a copy of the engine).
func TestCarouselHostIsANamedRegion(t *testing.T) {
	const host = `<div class="flex-column carousel" data-kitwork-component="carousel%s" role="region" aria-roledescription="%s" aria-label="%s">`
	for _, c := range []struct{ name, language, words, alias, region, label, slide string }{
		{"Vietnamese by default", "", ``, ``, "trình chiếu", "Trình chiếu", "trang chiếu"},
		{"vi-VN with alt", "vi-VN", ` as="$hero" alt="Món nổi bật"`, `: $hero`, "trình chiếu", "Món nổi bật", "trang chiếu"},
		{"alt with spaces around", "vi", ` alt="  Món nổi bật "`, ``, "trình chiếu", "Món nổi bật", "trang chiếu"},
		{"an empty alt", "", ` alt=""`, ``, "trình chiếu", "Trình chiếu", "trang chiếu"},
		{"an alt of spaces", "", ` alt="   "`, ``, "trình chiếu", "Trình chiếu", "trang chiếu"},
		{"English", "en", ``, ``, "carousel", "Carousel", "slide"},
		{"en-US with alt", "en-US", ` as="$hero" alt="Featured"`, `: $hero`, "carousel", "Featured", "slide"},
		{"French", "fr", ``, ``, "carousel", "Carousel", "slide"},
	} {
		t.Run(c.name, func(t *testing.T) {
			html := carouselQuiet(t, c.language, `<content>`+carouselOf(c.words, carouselSlides)+`</content>`, nil)
			mustContain(t, html, fmt.Sprintf(host, c.alias, c.region, c.label),
				`<li class="flex-column" aria-roledescription="`+c.slide+`" aria-label="1 / 2" aria-current="true"><p>Cà phê</p></li>`,
				`<li class="flex-column" aria-roledescription="`+c.slide+`" aria-label="2 / 2" aria-current="false"><p>Bạc xỉu</p></li>`)
			if n := strings.Count(html, `role="region"`); n != 1 {
				t.Errorf("role=\"region\" written %d times, want once:\n%s", n, html)
			}
		})
	}

	// an alt from data: the host's own alt, read where it is written, bound as any box's
	html := carouselQuiet(t, "", `<content>`+carouselOf(` alt="Món của {{ shop }}"`, carouselSlides)+`</content>`, map[string]any{"shop": "Quán Gió"})
	mustContain(t, html, fmt.Sprintf(host, ``, "trình chiếu", "Món của Quán Gió"))
	html = carouselQuiet(t, "", `<content>`+carouselOf(` alt="{{ count }} món"`, carouselSlides)+`</content>`, nil)
	mustContain(t, html, `<div class="flex-column carousel" data-kitwork-component="carousel" role="region" aria-roledescription="trình chiếu" data-kitwork-bind="aria-label: [(count), &#39; món&#39;].join(&#39;&#39;)" aria-label="2 món">`)
	if n := strings.Count(html, `aria-label=`); n != 3 {
		t.Errorf("aria-label written %d times, want 3 (the host's, then each slide's):\n%s", n, html)
	}

	// CONTROL: a box's alt names it, and makes no region
	html = carouselQuiet(t, "", `<content><box alt="Món nổi bật"><text>a</text></box></content>`, nil)
	mustContain(t, html, `<div class="flex-column" aria-label="Món nổi bật"><p>a</p></div>`)
	mustNotContain(t, html, `role="region"`, `aria-roledescription`)
}

// transition (transition.go) is how a slide gives way to the next: slide, the default, writes no class
// (the track scrolls); fade and none each write theirs on the host after carousel, never the toast's and
// the network's transition-* classes, and only on the host. Spaces around the word read the same, and
// the author's own classes stay first. The four classes are element words of the stylesheet, whose rules
// reboot.css keeps (TestRebootStylesEveryElementWord reads each).
// CONTROL: transition="fade" on the toast writes transition-fade. The class not made (classes()), not
// put on the host (componentCall), or the transition not kept each turned the fade and none rows red
// (run 18/10/2026).
func TestCarouselTransitionClasses(t *testing.T) {
	for _, c := range []struct{ name, words, class string }{
		{"not written: slide", ``, "flex-column carousel"},
		{"slide", ` transition="slide"`, "flex-column carousel"},
		{"fade", ` transition="fade"`, "flex-column carousel carousel-fade"},
		{"fade with spaces", ` transition=" fade "`, "flex-column carousel carousel-fade"},
		{"none", ` transition="none"`, "flex-column carousel carousel-none"},
		{"the author's classes first", ` gap="3" padding="2" transition="none"`, "flex-column gap-3 padding-2 carousel carousel-none"},
	} {
		t.Run(c.name, func(t *testing.T) {
			html := carouselQuiet(t, "", `<content>`+carouselOf(c.words, carouselSlides)+`</content>`, nil)
			if got := hostClasses(t, html, "carousel"); got != c.class {
				t.Errorf("host classes %q, want %q:\n%s", got, c.class, html)
			}
			mustNotContain(t, html, "transition-fade", "transition-none")
			if n := strings.Count(html, "carousel-fade") + strings.Count(html, "carousel-none"); n != strings.Count(c.class, "carousel-") {
				t.Errorf("a class of the transition written %d times:\n%s", n, html)
			}
		})
	}
	words := map[string]bool{}
	for _, w := range stylesheet.ElementWords() {
		words[w] = true
	}
	for _, w := range []string{"carousel", "carousel-track", "carousel-fade", "carousel-none"} {
		if !words[w] {
			t.Errorf("%s is no element word: the page's stylesheet would leave its rules out", w)
		}
	}
	// CONTROL: the toast's fade is its own class
	html := compileQuiet(t, toastSite(toastStack(` as="$toast" transition="fade"`), `<content><text>x</text></content>`))
	mustContain(t, html, "transition-fade")
	mustNotContain(t, html, "carousel")
}

// The track is the one <list> right inside the host: the class carousel-track after the author's own,
// an id (the engine's, numbered per page; the author's when written, and then no other), tabindex 0 so
// the arrow keys reach it, and aria-live bound to playing: polite on the server's paint when it does not
// play by itself, off when a duration is written (a moving region is not read out).
// CONTROL: a <list> outside a carousel is none of this. The track's class, its id (written, numbered,
// the author's kept, never a second), tabindex, the aria-live binding and playing's start, each removed,
// turned this red (run 18/10/2026).
func TestCarouselTrack(t *testing.T) {
	const live = ` tabindex="0" role="list" data-kitwork-bind="aria-live: playing ? &#39;off&#39; : &#39;polite&#39;" aria-live="%s">`
	html := carouselQuiet(t, "", `<content>`+carouselOf(``, carouselSlides)+`</content>`, nil)
	mustContain(t, html, `<ul class="flex-column carousel-track" id="kitwork-carousel-1"`+fmt.Sprintf(live, "polite")+`<li`)

	html = carouselQuiet(t, "", `<content>`+carouselOf(` duration="5s"`, carouselSlides+carouselStop)+`</content>`, nil)
	mustContain(t, html, `<ul class="flex-column carousel-track" id="kitwork-carousel-1"`+fmt.Sprintf(live, "off")+`<li`)

	// the author's presentation stays the list's, before the track's class
	html = carouselQuiet(t, "", `<content>`+carouselOf(``, `<list gap="2" radius="large"><item><text>a</text></item></list>`)+`</content>`, nil)
	mustContain(t, html, `<ul class="flex-column gap-2 radius-large carousel-track" id="kitwork-carousel-1"`+fmt.Sprintf(live, "polite"))

	// two carousels, two ids; the author's id is the track's own, and the next is still the first number
	two := `<content>` + carouselOf(``, carouselSlides) + carouselOf(``, carouselSlides) + `</content>`
	html = carouselQuiet(t, "", two, nil)
	mustContain(t, html, `id="kitwork-carousel-1"`, `id="kitwork-carousel-2"`)
	mine := `<content>` + carouselOf(``, `<list id="mon-noi-bat"><item><text>a</text></item></list>`) + carouselOf(``, carouselSlides) + `</content>`
	html = carouselQuiet(t, "", mine, nil)
	mustContain(t, html, `<ul class="flex-column carousel-track" id="mon-noi-bat"`+fmt.Sprintf(live, "polite"), `<ul class="flex-column carousel-track" id="kitwork-carousel-1"`)
	if n := strings.Count(html, ` id="`); n != 2 {
		t.Errorf("%d ids written, want one per track:\n%s", n, html)
	}

	// CONTROL
	html = carouselQuiet(t, "", `<content><list><item><text>a</text></item></list></content>`, nil)
	mustContain(t, html, `<ul class="flex-column" role="list"><li class="flex-column"><p>a</p></li></ul>`)
	mustNotContain(t, html, "carousel", "tabindex", "aria-live")
}

// Each <item> of the track is a slide: aria-roledescription in the page's language, and "i / N" as its
// name when every slide is written out, unless the author names it with alt (the item's aria-label).
// How many the instance starts with is the server's first paint: written out, count is the state
// (inside, {{ count }}; outside, $hero.count) and goes to no data-kitwork-scope (the kernel counts the
// slides itself). When a for repeats slides, count is an input the server reads off the page's data,
// on data-kitwork-scope: the for's list, with the slides written beside it, after duration when there is
// one; no slide is named then (the component names them all as it starts). An if on a slide leaves it
// out of the count when it is false, an if beside a for only the rows it keeps, and the <empty> the list
// shows when it has no item is no slide. playing starts true when a duration is written.
// CONTROL: the rows the server paints are the count each time. The count of a for, of an if, the
// filter of an if beside a for (and the for's index in it), the empty skipped, a slide's alt kept, the
// start state, the count bound from data and the places of slides written out, each disabled, turned
// their rows red (run 18/10/2026).
func TestCarouselSlidesAndTheirCount(t *testing.T) {
	const slide = `<li class="flex-column" aria-roledescription="trang chiếu"`
	three := `<list><item><text>a</text></item><item><text>b</text></item><item><text>c</text></item></list>`
	outside := `<text>{{ $hero.count }} {{ $hero.playing }}</text>`
	inside := `<text>{{ (current + 1) + ' / ' + count }}</text>`

	// written out: each named, count the state
	html := carouselQuiet(t, "", `<content>`+carouselOf(` as="$hero"`, three+inside)+outside+`</content>`, nil)
	mustContain(t, html,
		slide+` aria-label="1 / 3" aria-current="true"><p>a</p></li>`, slide+` aria-label="2 / 3" aria-current="false"><p>b</p></li>`, slide+` aria-label="3 / 3" aria-current="false"><p>c</p></li>`,
		`<p data-kitwork-bind="text: (current + 1) + &#39; / &#39; + count">1 / 3</p>`,
		`<p data-kitwork-bind="text: [($hero.count), &#39; &#39;, ($hero.playing)].join(&#39;&#39;)">3 false</p>`)
	mustNotContain(t, html, `data-kitwork-scope`)

	// a slide the author names keeps its name, the others theirs
	named := `<list><item alt="Cà phê sữa"><text>a</text></item><item><text>b</text></item></list>`
	html = carouselQuiet(t, "", `<content>`+carouselOf(``, named)+`</content>`, nil)
	mustContain(t, html, `<li class="flex-column" aria-label="Cà phê sữa" aria-roledescription="trang chiếu" aria-current="true"><p>a</p></li>`, slide+` aria-label="2 / 2" aria-current="false"><p>b</p></li>`)
	if n := strings.Count(html, `aria-label=`); n != 3 {
		t.Errorf("aria-label written %d times, want 3 (the host's, the alt, 2 / 2):\n%s", n, html)
	}

	for _, c := range []struct {
		name, duration, list string
		data                 map[string]any
		count                int // the slides the server paints, and the count it starts with
	}{
		{"a for", ``, `<list><item for="mon of mons"><text>{{ mon }}</text></item></list>`, carouselDishes, 3},
		{"a for with a key", ``, `<list><item for="mon, i of mons; key: mon"><text>{{ mon }}</text></item></list>`, carouselDishes, 3},
		{"written out beside a for", ``, `<list><item><text>a</text></item><item for="mon of mons"><text>{{ mon }}</text></item><item><text>z</text></item></list>`, carouselDishes, 5},
		{"two fors", ``, `<list><item for="mon of mons"><text>{{ mon }}</text></item><item for="mon of mons"><text>{{ mon }}</text></item></list>`, carouselDishes, 6},
		{"a for over nothing", ``, `<list><item><text>a</text></item><item for="mon of mons"><text>{{ mon }}</text></item></list>`, map[string]any{"mons": []any{}}, 1},
		{"after duration", `5s`, `<list><item><text>a</text></item><item for="mon of mons"><text>{{ mon }}</text></item></list>`, carouselDishes, 4},
		{"an if that holds", ``, `<list><item><text>a</text></item><item if="promo"><text>b</text></item><item><text>c</text></item></list>`, map[string]any{"promo": true}, 3},
		{"an if that does not", ``, `<list><item><text>a</text></item><item if="promo"><text>b</text></item><item><text>c</text></item></list>`, map[string]any{"promo": false}, 2},
		{"an if beside a for", ``, `<list><item for="mon, i of mons" if="i != 1"><text>{{ mon }}</text></item></list>`, carouselDishes, 2},
		{"an if beside a for, by the row", ``, `<list><item for="mon of mons" if="mon != 'Bạc xỉu'"><text>{{ mon }}</text></item></list>`, carouselDishes, 2},
		{"empty beside the rows", ``, `<list><item for="mon of mons"><text>{{ mon }}</text></item><empty><text>Chưa có món</text></empty></list>`, carouselDishes, 3},
		{"empty when there are none", ``, `<list><item for="mon of mons"><text>{{ mon }}</text></item><empty><text>Chưa có món</text></empty></list>`, map[string]any{"mons": []any{}}, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			words, scope, stop, playing := ` as="$hero"`, fmt.Sprintf("count: %d", c.count), "", "false"
			if c.duration != "" {
				words += ` duration="` + c.duration + `"`
				scope, stop, playing = "duration: 5000; "+scope, carouselStop, "true"
			}
			html := carouselQuiet(t, "", `<content>`+carouselOf(words, c.list+inside+stop)+outside+`</content>`, c.data)
			mustContain(t, html,
				`<div class="flex-column carousel" data-kitwork-component="carousel: $hero" role="region" aria-roledescription="trình chiếu" aria-label="Trình chiếu" data-kitwork-scope="`+scope+`">`,
				fmt.Sprintf(`&#39; / &#39; + count">1 / %d</p>`, c.count),
				fmt.Sprintf(`join(&#39;&#39;)">%d %s</p>`, c.count, playing))
			// the rows the server paints are the count, each a slide, none named by a place; the empty
			// the list shows instead is no slide
			if got := strings.Count(html, slide+`>`); got != c.count {
				t.Errorf("%d slides painted, want %d:\n%s", got, c.count, html)
			}
			if got := strings.Count(html, `<li `); got != c.count+strings.Count(html, `Chưa có món`) {
				t.Errorf("%d items painted for %d slides:\n%s", got, c.count, html)
			}
			if got := strings.Count(html, `aria-label=`); got != 1 {
				t.Errorf("aria-label written %d times, want only the host's:\n%s", got, html)
			}
		})
	}

	// a carousel a for repeats counts each row's slides, read where the host stands
	rows := `<content>` + carouselOf(` for="group of groups"`, `<list><item for="photo of group.photos"><text>{{ photo }}</text></item></list>`+inside) + `</content>`
	groups := []any{map[string]any{"photos": []any{"a", "b"}}, map[string]any{"photos": []any{"c", "d", "e"}}}
	html = carouselQuiet(t, "", rows, map[string]any{"groups": groups})
	mustContain(t, html, `aria-label="Trình chiếu" data-kitwork-scope="count: 2">`, `aria-label="Trình chiếu" data-kitwork-scope="count: 3">`,
		`&#39; / &#39; + count">1 / 2</p>`, `&#39; / &#39; + count">1 / 3</p>`)
}

// A <button> whose click is go(…) of a place the page knows is a dot: the server writes the place as
// its value and whether it is the first slide's as aria-current (the component marks the current one as
// it moves), aria-controls names the track, and its click becomes go(Number($this.value)), which the
// browser runs (a for over the page's data is the server's). A literal place, a for's index, a place
// computed from it (go(i + 1)) and the page's data are dots; go(…) of the component's own names
// (current, count) moves like next() and is no dot, nor is next(), previous(), nor a click that does
// more than go(…). A dot under an author's id controls that id.
// CONTROL: the buttons that are no dot keep their click as written, with no value and no aria-controls.
// The dot's value, aria-current, aria-controls and rewritten click, each removed, the component's names
// taken for a place, and two arguments taken for one, each turned this red (run 18/10/2026).
func TestCarouselDots(t *testing.T) {
	const dot = `aria-controls="kitwork-carousel-1" data-kitwork-event="click: go(Number($this.value))" value="%s" aria-current="%s">`
	three := `<list><item><text>a</text></item><item><text>b</text></item><item><text>c</text></item></list>`
	for _, c := range []struct{ name, button, values string }{
		{"a literal place", `<button click="go(2)">Ba</button>`, "2"},
		{"the first slide", `<button click="go(0)">Một</button>`, "0"},
		{"spaces and a semicolon", `<button click=" go( 1 ); ">Hai</button>`, "1"},
		{"a for's index", `<button for="mon, i of mons" click="go(i)">{{ mon }}</button>`, "0 1 2"},
		{"a place from the index", `<button for="mon, i of mons" click="go(i + 1)">{{ mon }}</button>`, "1 2 3"},
		{"the page's data", `<button click="go(start)">Đầu</button>`, "1"},
		{"a dot with its look", `<button for="mon, i of mons" click="go(i)" padding="1" active:color="brand"><icon value="point-fill" alt="{{ mon }}" /></button>`, "0 1 2"},
	} {
		t.Run(c.name, func(t *testing.T) {
			data := map[string]any{"mons": carouselDishes["mons"], "start": 1.0}
			html := carouselQuiet(t, "", `<content>`+carouselOf(``, three+c.button)+`</content>`, data)
			values := strings.Fields(c.values)
			for _, v := range values {
				current := "false"
				if v == "0" {
					current = "true"
				}
				mustContain(t, html, fmt.Sprintf(dot, v, current))
			}
			if n := strings.Count(html, `aria-controls=`); n != len(values) {
				t.Errorf("%d dots written, want %d:\n%s", n, len(values), html)
			}
			mustNotContain(t, html, `click: go(i`, `click: go(2)`, `click: go(start)`)
		})
	}

	// the author's id is the one the dots control
	html := carouselQuiet(t, "", `<content>`+carouselOf(``, `<list id="mon-noi-bat"><item><text>a</text></item></list><button click="go(0)">Một</button>`)+`</content>`, nil)
	mustContain(t, html, `type="button" aria-controls="mon-noi-bat" data-kitwork-event="click: go(Number($this.value))" value="0" aria-current="true">Một</button>`)

	// CONTROL: buttons that move without being a dot
	for _, click := range []string{`go(current + 1)`, `go(count - 1)`, `next()`, `previous()`, `go(1) || next()`, `go(1, 2)`} {
		t.Run(click, func(t *testing.T) {
			html := carouselQuiet(t, "", `<content>`+carouselOf(``, three+`<button click="`+click+`">Đi</button>`)+`</content>`, nil)
			mustContain(t, html, carouselButton+` data-kitwork-event="click: `+click+`">Đi</button>`)
			mustNotContain(t, html, `aria-controls`, `value=`)
			if n := strings.Count(html, `aria-current`); n != 3 {
				t.Errorf("aria-current written %d times, want 3 (the slides only, not the button):\n%s", n, html)
			}
		})
	}
}

// A dot is drawn by active: (aria-current, which the component moves); selected: is the tab's
// (aria-selected) and never holds on a dot, so it is told at the attribute, with the word to write, and
// the page renders as written. value on a dot is the engine's, written from go(…): an error there.
// CONTROL: active: on a dot, selected: on a button that is no dot (next()) and value on one are quiet;
// the value check and the selected: warning, each disabled, turned this red (run 18/10/2026).
func TestCarouselDotWrittenWrong(t *testing.T) {
	selected := `<content>` + carouselOf(``, carouselSlides+`<button click="go(1)" selected:weight="bold">Hai</button>`) + `</content>`
	html := compileWarns(t, content(selected), where("content.ui", selected, `selected:weight=`),
		`selected:weight: một chấm của carousel là trang đang hiện khi go(…) trỏ tới nó, và vẽ bằng active: (aria-current), không phải selected: (của tab); viết active:weight`)
	mustContain(t, html, `selected:weight-bold" type="button" aria-controls="kitwork-carousel-1" data-kitwork-event="click: go(Number($this.value))" value="1" aria-current="false">Hai</button>`)

	value := `<content>` + carouselOf(``, carouselSlides+`<button click="go(1)" value="1">Hai</button>`) + `</content>`
	mustFailExactly(t, content(value), where("content.ui", value, `value=`)+`value của một chấm là trang go(…) trỏ tới, engine viết từ go(1): bỏ value`)
	value = `<content>` + carouselOf(``, carouselSlides+`<button for="mon, i of mons" click="go(i)" value="{{ i }}">{{ mon }}</button>`) + `</content>`
	mustFailExactly(t, content(value), where("content.ui", value, `value=`)+`value của một chấm là trang go(…) trỏ tới, engine viết từ go(i): bỏ value`)

	// CONTROL
	carouselQuiet(t, "", `<content>`+carouselOf(``, carouselSlides+`<button click="go(1)" active:weight="bold">Hai</button>`+
		`<button click="next()" selected:weight="bold" value="sau">Sau</button>`)+`</content>`, nil)
}

// What the carousel does not take is an error at its place, in the author's words: no <list> right
// inside the host (none, or one wrapped in a box), two, flex, grid or columns written on the track (with
// a prefix too), a track with no slide (empty, or only the <empty> the list shows when it has none), a
// fragment among the slides; on the host, the words other carousels use (each with what to write here,
// with a prefix too), the words other libraries give the transition, a role or aria-* it writes itself,
// alt with a prefix, a transition it does not take (a prefix, no value, data, CSS's, another word, a
// slide with a direction: a carousel has no place), a duration it does not take (a prefix, no value,
// data, negative, zero, without its unit, longer than a minute, no duration at all); a region or a tag
// it is not written on.
// CONTROL: each written right compiles (the tests above); -abc is no negative time but no time at all.
// Each check of carouselHost, carouselTrack and carouselDuration and the region check of componentCall,
// disabled in turn, turned its rows red, the one of no list by a panic (run 18/10/2026).
func TestCarouselWrittenWrongIsAnError(t *testing.T) {
	const (
		noList     = `carousel cần một <list> ngay bên trong, mỗi <item> là một trang chiếu: <list><item>…</item><item>…</item></list>`
		noSlide    = `dải trang của carousel chưa có trang nào: mỗi <item> trong <list><item>…</item><item>…</item></list> là một trang`
		row        = `dải trang của carousel là một hàng engine đặt (cuộn ngang, dừng ở mỗi trang): bỏ NAME; độ rộng của mỗi trang là width trên <item>`
		fragment   = `mỗi trang của carousel là một <item> viết thẳng trong <list> (for và if đặt trên <item>): bỏ <fragment>`
		role       = `vai trò của carousel là việc của engine (role="region", trang chiếu đánh số): bỏ NAME; tên của nó là alt (alt="Món nổi bật")`
		effect     = `carousel không có NAME: cách một trang nhường trang sau là transition (slide, fade hay none)`
		direction  = `transition="VALUE": trang chiếu trượt theo dòng chữ của trang, trang sau đến từ cuối dòng; transition chỉ là một chữ: slide, fade hay none`
		noDuration = `duration cần một thời lượng: mỗi trang ở lại bao lâu (duration="5s"); không tự chạy thì bỏ duration`
		negative   = `duration không nhận thời lượng âm (VALUE): viết một thời lượng như duration="5s"`
		zero       = `duration="VALUE": không tự chạy thì bỏ duration`
		unitless   = `duration="VALUE" thiếu đơn vị: viết duration="WRITE"`
		notTime    = `duration="VALUE" không phải một thời lượng: viết duration="5s" hay duration="800ms"`
		tooLong    = `duration="VALUE" dài hơn 60 giây: mỗi trang ở lại tới khi người xem chuyển thì bỏ duration`
	)
	type wrong struct{ name, source, needle, says string }
	slide := `<item><text>a</text></item>`
	cases := []wrong{
		{"no list", carouselOf(``, `<text>a</text>`), `<carousel`, noList},
		{"a list wrapped in a box", carouselOf(``, `<box>`+carouselSlides+`</box>`), `<carousel`, noList},
		{"a track with no slide", carouselOf(``, `<list></list>`), `<list`, noSlide},
		{"a track with only empty", carouselOf(``, `<list><empty><text>Chưa có món</text></empty></list>`), `<list`, noSlide},
		{"a fragment of slides", carouselOf(``, `<list>`+slide+`<fragment for="mon of mons">`+slide+slide+`</fragment></list>`), `<fragment`, fragment},
		{"a fragment written out", carouselOf(``, `<list><fragment>`+slide+`</fragment></list>`), `<fragment`, fragment},
		{"alt by width", carouselOf(` medium:alt="Món"`, carouselSlides), `medium:alt=`, `alt của carousel không nhận tiền tố: nó là tên của carousel`},
	}
	for _, written := range []string{`flex="row"`, `medium:flex="column"`, `grid="3"`, `columns="2"`} {
		name := strings.SplitN(written, "=", 2)[0]
		cases = append(cases, wrong{"the track written " + name, carouselOf(``, `<list `+written+`>`+slide+`</list>`), written, strings.Replace(row, "NAME", name, 1)})
	}
	strangers := map[string]string{
		"autoplay": `tự chạy là duration: mỗi trang ở lại bao lâu (duration="5s")`,
		"interval": `mỗi trang ở lại bao lâu là duration (duration="5s")`,
		"timeout":  `mỗi trang ở lại bao lâu là duration (duration="5s")`,
		"delay":    `mỗi trang ở lại bao lâu là duration (duration="5s")`,
		"speed":    `nhanh chậm của lúc chuyển là việc của engine; mỗi trang ở lại bao lâu là duration (duration="5s")`,
		"loop":     `next() ở trang cuối đã quay về trang đầu, previous() ở trang đầu về trang cuối: bỏ loop`,
		"infinite": `next() ở trang cuối đã quay về trang đầu, previous() ở trang đầu về trang cuối: bỏ infinite`,
		"index":    `trang đang hiện là current (current="0" là trang đầu)`,
		"active":   `trang đang hiện là current (current="0" là trang đầu)`,
	}
	for _, written := range []string{`autoplay="true"`, `autoplay`, `medium:autoplay="true"`, `interval="5000"`, `timeout="5s"`, `delay="1s"`,
		`speed="300"`, `loop="true"`, `infinite`, `index="0"`, `active="1"`} {
		name := strings.TrimPrefix(strings.SplitN(written, "=", 2)[0], "medium:")
		cases = append(cases, wrong{"the stranger " + written, carouselOf(` `+written, carouselSlides), written, "carousel không có " + name + ": " + strangers[name]})
	}
	for _, written := range []string{`animation="fade"`, `animate="slide"`, `motion="none"`, `effect="fade"`, `medium:effect="fade"`} {
		name := strings.TrimPrefix(strings.SplitN(written, "=", 2)[0], "medium:")
		cases = append(cases, wrong{"the stranger " + written, carouselOf(` `+written, carouselSlides), written, strings.Replace(effect, "NAME", name, 1)})
	}
	for _, written := range []string{`role="region"`, `aria-live="polite"`, `aria-label="Món nổi bật"`, `aria-roledescription="carousel"`} {
		name := strings.SplitN(written, "=", 2)[0]
		cases = append(cases, wrong{"written " + name, carouselOf(` `+written, carouselSlides), written, strings.Replace(role, "NAME", name, 1)})
	}
	for _, c := range []struct{ written, says string }{
		{`medium:transition="fade"`, `transition của carousel không nhận tiền tố: nó được quyết một lần khi trang dựng`},
		{`hover:transition="fade"`, `transition của carousel không nhận tiền tố: nó được quyết một lần khi trang dựng`},
		{`transition`, `transition của carousel cần một giá trị: slide, fade, none`},
		{`transition=" "`, `transition của carousel cần một giá trị: slide, fade, none`},
		{`transition="{{ effect }}"`, `transition của carousel là chữ cố định (slide, fade, none), không có {{ }}: nó được quyết một lần khi trang dựng`},
		{`transition="0.3s"`, `transition="0.3s" là cách viết của CSS; transition của .ui là kiểu hiệu ứng: slide (trượt ngang tới trang sau), fade (hiện dần, mờ dần) hay none (ngay); nhanh chậm là việc của engine`},
		{`count="3"`, `carousel không có count: count là số trang, engine đếm từ các <item> của dải: bỏ count`},
		{`playing="true"`, `carousel không có playing: carousel tự chạy khi có duration (duration="5s"); không tự chạy thì bỏ duration: bỏ playing`},
		{`current="-1"`, `current là trang nó bắt đầu, đếm từ 0 (current="0" là trang đầu), không phải "-1"`},
		{`current="first"`, `current là trang nó bắt đầu, đếm từ 0 (current="0" là trang đầu), không phải "first"`},
		{`current="2"`, `current="2" quá số trang: dải có 2 trang, đếm từ 0 (trang cuối là current="1")`},
		{`transition="zoom"`, `transition của carousel là slide, fade hay none, không phải "zoom"`},
		{`transition="Fade"`, `transition của carousel là slide, fade hay none, không phải "Fade"`},
		{`transition="fade-in"`, `transition="fade-in": fade đã gồm hiện dần lúc vào và mờ dần lúc ra; viết transition="fade"`},
		{`transition="slide-left"`, strings.Replace(direction, "VALUE", "slide-left", 1)},
		{`transition="slide-right"`, strings.Replace(direction, "VALUE", "slide-right", 1)},
		{`transition="Slide-Up"`, strings.Replace(direction, "VALUE", "Slide-Up", 1)},
		{`transition=" slide up "`, strings.Replace(direction, "VALUE", "slide up", 1)},
		{`medium:duration="5s"`, `duration của carousel không nhận tiền tố: nó được quyết một lần khi trang dựng`},
		{`duration`, noDuration},
		{`duration=""`, noDuration},
		{`duration="  "`, noDuration},
		{`duration="{{ seconds }}s"`, `duration của carousel là một thời lượng cố định (duration="5s"), không có {{ }}: nó được quyết một lần khi trang dựng`},
		{`duration="-5s"`, strings.Replace(negative, "VALUE", "-5s", 1)},
		{`duration="-5"`, strings.Replace(negative, "VALUE", "-5", 1)},
		{`duration="-abc"`, strings.Replace(notTime, "VALUE", "-abc", 1)},
		{`duration="0"`, strings.Replace(zero, "VALUE", "0", 1)},
		{`duration="0.0"`, strings.Replace(zero, "VALUE", "0.0", 1)},
		{`duration="0s"`, strings.Replace(zero, "VALUE", "0s", 1)},
		{`duration="0ms"`, strings.Replace(zero, "VALUE", "0ms", 1)},
		{`duration="5"`, strings.NewReplacer("VALUE", "5", "WRITE", "5s").Replace(unitless)},
		{`duration="2.5"`, strings.NewReplacer("VALUE", "2.5", "WRITE", "2.5s").Replace(unitless)},
		{`duration="800"`, strings.NewReplacer("VALUE", "800", "WRITE", "800ms").Replace(unitless)},
		{`duration="61s"`, strings.Replace(tooLong, "VALUE", "61s", 1)},
		{`duration="60001ms"`, strings.Replace(tooLong, "VALUE", "60001ms", 1)},
		{`duration="1` + strings.Repeat("0", 400) + `s"`, strings.Replace(tooLong, "VALUE", "1"+strings.Repeat("0", 400)+"s", 1)},
		{`duration="fast"`, strings.Replace(notTime, "VALUE", "fast", 1)},
		{`duration="5 s"`, `duration="5 s" có khoảng trắng trước đơn vị, .ui viết số liền đơn vị: viết duration="5s"`},
		{`duration="5sec"`, `duration="5sec" có đơn vị sec, .ui viết là s: viết duration="5s"`},
		{`duration="5S"`, `duration="5S" có đơn vị S, .ui viết là s: viết duration="5s"`},
		{`duration="true"`, strings.Replace(notTime, "VALUE", "true", 1)},
	} {
		cases = append(cases, wrong{c.written, carouselOf(` `+c.written, carouselSlides+carouselStop), c.written, c.says})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			source := `<content>` + c.source + `</content>`
			mustFailExactly(t, content(source), where("content.ui", source, c.needle)+c.says)
		})
	}

	// two lists: the second is told where the first is
	source := `<content>` + carouselOf(``, carouselSlides+"\n  "+carouselSlides) + `</content>`
	first := strings.TrimSuffix(where("content.ui", source, `<list`), ": ")
	mustFailExactly(t, content(source), "content.ui:2:3: carousel có một dải trang: <list> ở "+first+" đã là dải của nó; gộp các trang vào một <list>")

}

// A carousel that plays by itself faster than a slide is read is told at its duration, under 2 seconds,
// and renders as written; the duration reaches the instance in milliseconds, the seconds read as the
// decimal they are written in (4.03s is 4030).
// CONTROL: 2s, 2000ms, 4.03s, 60s and 60000ms are quiet; the warning disabled, the duration not handed
// to the instance, or seconds read as milliseconds each turned this red (run 18/10/2026).
func TestCarouselDurationUnderTwoSecondsIsAWarning(t *testing.T) {
	for _, c := range []struct{ written, ms string }{{"1s", "1000"}, {"1999ms", "1999"}, {"800ms", "800"}, {"1.5s", "1500"}} {
		source := `<content>` + carouselOf(` duration="`+c.written+`"`, carouselSlides+carouselStop) + `</content>`
		html := compileWarns(t, content(source), where("content.ui", source, `duration=`),
			`duration="`+c.written+`" dưới 2 giây: người xem đọc không kịp một trang; viết duration từ "2s"`)
		mustContain(t, html, `data-kitwork-scope="duration: `+c.ms+`">`)
	}
	// CONTROL
	for _, c := range []struct{ written, ms string }{{"2s", "2000"}, {"2000ms", "2000"}, {"4.03s", "4030"}, {"60s", "60000"}, {"60000ms", "60000"}} {
		html := carouselQuiet(t, "", `<content>`+carouselOf(` duration="`+c.written+`"`, carouselSlides+carouselStop)+`</content>`, nil)
		mustContain(t, html, `data-kitwork-scope="duration: `+c.ms+`">`)
	}
}

// Content that moves by itself needs a way to stop it (WCAG 2.2.2): a carousel written with a duration
// and no click on the page that calls its pause() or toggle() (a button inside its host, or anywhere on
// the page through its alias, a region's file too) is told at its duration, once, and renders as
// written. play() stops nothing; the toggle() of a component nested inside the host is that component's;
// another carousel's pause() stops that one.
// CONTROL: toggle() or pause() inside, $hero.pause() or $hero.toggle() elsewhere, and a carousel that
// only plays when asked (no duration) are quiet. pause() not counted, the alias not walked or not known,
// play() or another alias counted, a nested component left the carousel's, any call taken for pause(),
// and checkCarousels not called, each turned this red (run 18/10/2026).
func TestCarouselThatPlaysNeedsAStop(t *testing.T) {
	const says = `carousel tự chạy (duration="5s") mà trang không có cách dừng: nội dung tự chuyển cần một nút dừng (WCAG 2.2.2); viết <button click="toggle()"> trong <carousel>, hay gọi $…pause() ở chỗ khác`
	for _, c := range []struct{ name, inside, outside string }{
		{"nothing stops it", ``, ``},
		{"only next and a dot", `<button click="next()">Sau</button><button click="go(0)">Một</button>`, ``},
		{"play() inside", `<button click="play()">Chạy</button>`, ``},
		{"play() through the alias", ``, `<button click="$hero.play()">Chạy</button>`},
		{"a nested dropdown's toggle()", `<dropdown><button click="toggle()">Mở</button><popover><text>a</text></popover></dropdown>`, ``},
		{"another carousel's pause()", ``, carouselOf(` as="$other" duration="5s"`, carouselSlides) + `<button click="$other.pause()">Dừng</button>`},
	} {
		t.Run(c.name, func(t *testing.T) {
			source := `<content>` + carouselOf(` as="$hero" duration="5s"`, carouselSlides+c.inside) + c.outside + `</content>`
			html, warned := carouselPage(t, "", source, nil)
			mustContain(t, html, `data-kitwork-component="carousel: $hero" role="region" aria-roledescription="trình chiếu" aria-label="Trình chiếu" data-kitwork-scope="duration: 5000">`)
			at, found := where("content.ui", source, `duration=`), 0
			for _, w := range warned {
				if strings.Contains(w, "WCAG 2.2.2") {
					if w != at+says {
						t.Errorf("warning:\n got  %q\n want %q", w, at+says)
					}
					found++
				}
			}
			if found != 1 {
				t.Errorf("want the carousel's warning once, got:\n%s", strings.Join(warned, "\n"))
			}
		})
	}
	// CONTROL
	for _, c := range []struct{ name, inside, outside string }{
		{"toggle() inside", carouselStop, ``},
		{"pause() inside", `<box flex="row"><button click="pause()">Dừng</button></box>`, ``},
		{"pause() through the alias", ``, `<button click="$hero.pause()">Dừng</button>`},
		{"toggle() through the alias", ``, `<box><button click="$hero.toggle()">Dừng</button></box>`},
	} {
		t.Run("CONTROL "+c.name, func(t *testing.T) {
			carouselQuiet(t, "", `<content>`+carouselOf(` as="$hero" duration="5s"`, carouselSlides+c.inside)+c.outside+`</content>`, nil)
		})
	}
	compileQuiet(t, map[string]string{
		"/site/view.ui":    `<view><header /><content /></view>`,
		"/site/header.ui":  `<header><button click="$hero.toggle()">Dừng</button></header>`,
		"/site/content.ui": `<content>` + carouselOf(` as="$hero" duration="5s"`, carouselSlides) + `</content>`,
	})
	carouselQuiet(t, "", `<content>`+carouselOf(` as="$hero"`, carouselSlides)+`</content>`, nil)
}

// current written as a number is the slide it starts on, as a number: the server's first paint counts
// from it ((current + 1) + ' / ' + count is "2 / 3", never "11 / 3", the text "1" plus 1), the instance
// starts there (data-kitwork-scope current: 1), and the dot of that slide is the one marked.
// CONTROL: written from data it stays an input of the call, read where the call stands.
func TestCarouselStartsWhereCurrentSays(t *testing.T) {
	source := `<content>` + carouselOf(` current="1"`, `<list><item for="mon of mons"><text>{{ mon }}</text></item></list>`+
		`<button for="mon, i of mons" click="go(i)"><text>{{ mon }}</text></button>`+
		`<text>{{ (current + 1) + ' / ' + count }}</text>`) + `</content>`
	html := carouselQuiet(t, "", source, carouselDishes)
	mustContain(t, html,
		`data-kitwork-scope="current: 1; count: 3"`,
		`>2 / 3</p>`,
		`value="0" aria-current="false"`, `value="1" aria-current="true"`, `value="2" aria-current="false"`,
	)
	if strings.Contains(html, "11 / 3") {
		t.Errorf("the first paint added to the text of current:\n%s", html)
	}

	fromData := `<content>` + carouselOf(` current="{{ start }}"`, carouselSlides+`<text>{{ current + 1 }}</text>`) + `</content>`
	html = carouselQuiet(t, "", fromData, map[string]any{"start": 1.0})
	mustContain(t, html, `current: 1`, `>2</p>`)
}
