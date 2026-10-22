package ui

import (
	"strings"
	"testing"
)

// twiceSays is the error of an attribute or an event written a second time on one element: at the
// second, naming where the first is. Each needle starts where its attribute, or its event in a list,
// is written.
func twiceSays(file, source, first, second, what string) string {
	firstAt := strings.TrimSuffix(where(file, source, first), ": ")
	return where(file, source, second) + what + " (lần đầu ở " + firstAt + "): giữ một"
}

// An attribute written twice on one element is an error at the second, naming the first (17/10/2026).
// It used to pass: on the host of a component the last one won (transition="fade" transition="none"
// was none, place="top" place="bottom" bottom, delay="1s" delay="2s" 2000), on a tag both were kept
// (gap="2" gap="3" wrote class="gap-2 gap-3" and the stylesheet's order decided). Every file is read
// the same way (content.ui, page.ui, a layout's file, the call of a layout), on one line or across
// lines, and a bare attribute is one too.
func TestAttributeWrittenTwiceIsAnError(t *testing.T) {
	for _, c := range []struct{ name, file, source, first, second, what string }{
		{"a tag", "content.ui", `<content><box gap="2" gap="3">x</box></content>`,
			`gap="2"`, `gap="3"`, "gap viết hai lần trên <box>"},
		{"across lines", "content.ui", "<content>\n  <box gap=\"2\"\n       padding=\"4\"\n       gap=\"3\">x</box>\n</content>",
			`gap="2"`, `gap="3"`, "gap viết hai lần trên <box>"},
		{"the same prefix", "content.ui", `<content><box medium:gap="2" medium:gap="3">x</box></content>`,
			`medium:gap="2"`, `medium:gap="3"`, "medium:gap viết hai lần trên <box>"},
		{"a component's transition", "content.ui", `<content>` + networkWithNotice(` transition="fade" transition="none"`) + `</content>`,
			`transition="fade"`, `transition="none"`, "transition viết hai lần trên <box>"},
		{"a component's place", "content.ui", `<content>` + networkWithNotice(` place="top" place="bottom"`) + `</content>`,
			`place="top"`, `place="bottom"`, "place viết hai lần trên <box>"},
		{"a component's delay", "content.ui", `<content>` + networkWithNotice(` delay="1s" delay="2s"`) + `</content>`,
			`delay="1s"`, `delay="2s"`, "delay viết hai lần trên <box>"},
		{"the call of a layout", "content.ui", `<content><product-card title="a" title="b" /></content>`,
			`title="a"`, `title="b"`, "title viết hai lần trên <product-card>"},
		{"a layout's file", "product-card.ui", `<card gap="2" gap="3"><text>{{ title }}</text></card>`,
			`gap="2"`, `gap="3"`, "gap viết hai lần trên <card>"},
		{"page.ui", "page.ui", "<page>\n  <box gap=\"2\" gap=\"3\"><text>x</text></box>\n  <content />\n</page>",
			`gap="2"`, `gap="3"`, "gap viết hai lần trên <box>"},
		{"bare", "content.ui", `<content><input type="checkbox" alt="Nhận tin" required required /></content>`,
			`required `, `required />`, "required viết hai lần trên <input>"},
		// a scroll area is no event: scroll="y" then scroll="track()" is scroll twice, told as an attribute
		{"a scroll area, then a scroll event", "content.ui", `<content><box scroll="y" scroll="track()">x</box></content>`,
			`scroll="y"`, `scroll="track()"`, "scroll viết hai lần trên <box>"},
	} {
		t.Run(c.name, func(t *testing.T) {
			files := map[string]string{
				"/site/content.ui":      `<content><product-card title="Cà phê" /></content>`,
				"/site/product-card.ui": `<card><text>{{ title }}</text></card>`,
			}
			files["/site/"+c.file] = c.source
			mustFailExactly(t, files, twiceSays(c.file, c.source, c.first, c.second, c.what))
		})
	}
}

// The same event twice on one element is the rule of KITWORK_UI.md §11 ("Một sự kiện xuất hiện hai lần
// trên cùng phần tử là lỗi"), told as an attribute written twice is: the event is its name with what
// follows the ':', the pair the kernel listens for, wherever it is written (alone, in a list, twice in
// one list). It used to reach the wire as two pairs of one name (click: toggle(); click: open()), the
// pair the server's check of data-kitwork-event refuses. The error is at the event, inside a list too.
func TestEventWrittenTwiceIsAnError(t *testing.T) {
	host := func(events string) string {
		return `<content><box component="dropdown"` + events + `><text>x</text></box></content>`
	}
	for _, c := range []struct{ name, events, first, second, what string }{
		{"twice alone", ` click="toggle()" click="open()"`,
			`click="toggle()"`, `click="open()"`, "sự kiện click viết hai lần trên <box>"},
		{"twice in one list", ` click,click="toggle()"`,
			`click,click`, `click="toggle()"`, "sự kiện click viết hai lần trên <box>"},
		{"alone, then in a list", ` click="toggle()" pointer-enter,click="open()"`,
			`click="toggle()"`, `click="open()"`, "sự kiện click viết hai lần trên <box>"},
		{"in a list, then alone", ` pointer-enter,focus-in="open()" focus-in="close()"`,
			`focus-in="open()"`, `focus-in="close()"`, "sự kiện focus-in viết hai lần trên <box>"},
		{"with the same filter", ` keydown:escape="close()" keydown:escape="toggle()"`,
			`keydown:escape="close()"`, `keydown:escape="toggle()"`, "sự kiện keydown:escape viết hai lần trên <box>"},
		{"a list with the same modifiers", ` pointer-enter,focus-in:once="open()" focus-in:once="close()"`,
			`focus-in:once="open()"`, `focus-in:once="close()"`, "sự kiện focus-in:once viết hai lần trên <box>"},
	} {
		t.Run(c.name, func(t *testing.T) {
			source := host(c.events)
			mustFailExactly(t, content(source), twiceSays("content.ui", source, c.first, c.second, c.what))
		})
	}
}

// CONTROL: what only looks the same compiles. A name with another prefix is another attribute, an
// attribute is the element's own (the same one on two elements), and an event with another filter is
// another pair (keydown:escape beside keydown:enter, as the kitwork example's menu writes it); two lists
// of different events share nothing.
func TestWrittenOnceEachCompiles(t *testing.T) {
	html := mustCompile(t, content(`<content><box gap="2" medium:gap="3" large+hover:gap="4"><box gap="2">x</box></box></content>`))
	mustContain(t, html, `class="flex-column gap-2 medium:gap-3 large+hover:gap-4"`, `class="flex-column gap-2">x`)
	html = mustCompile(t, content(`<content><box component="dropdown" keydown:escape="close()" keydown:enter="toggle()"`+
		` pointer-enter,focus-in="open()" pointer-leave,focus-out="close()"><text>x</text></box></content>`))
	mustContain(t, html, `data-kitwork-event="keydown.escape: close(); keydown.enter: toggle(); pointer-enter: open(); focus-in: open(); pointer-leave: close(); focus-out: close()"`)
}
