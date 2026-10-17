package ui

import (
	"strings"
	"testing"
)

// A live show beside a width that shows the element — show="$files.selected == 'b'" medium:show="true",
// a tab's panel on a phone, side by side with the others from medium — compiles to the hidden attribute
// the data drives and the width's class (medium:show-true): below medium the data decides, from medium
// the element is shown whatever it says (reboot.css hides it in the show layer, where the width's class
// outranks that; work's TestBrowserWidthShowsWhatALiveShowHides draws it). On a frame the author hears
// nothing. On a text, a link of words or a field, which have no display of their own to give back, the
// width does nothing, and the author hears so at the attribute.
// CONTROL: a text whose show is written (show="false" medium:show="true") is shown from medium by its
// classes alone, and is not told.
func TestWidthShowsWhatALiveShowHides(t *testing.T) {
	tabs := `<box component="tabs" as="$files" alt="Files"><button click="select('a')">a</button><button click="select('b')">b</button></box>`
	frames := tabsCompile(t, `<content>`+tabs+`<box show="$files.selected == 'a'" medium:show="true"><text>a</text></box><box show="$files.selected == 'b'" medium:show="true"><text>b</text></box></content>`)
	mustContain(t, frames.Render(Request{Path: "/"}),
		`<div class="flex-column medium:show-true" id="kitwork-tabpanel-1-1" role="tabpanel" aria-labelledby="kitwork-tab-1-1" tabindex="0" data-kitwork-bind="show: $files.selected == &#39;a&#39;">`,
		`<div class="flex-column medium:show-true" id="kitwork-tabpanel-1-2" role="tabpanel" aria-labelledby="kitwork-tab-1-2" tabindex="0" data-kitwork-bind="show: $files.selected == &#39;b&#39;" hidden>`)
	if w := frames.Warnings(); len(w) != 0 {
		t.Errorf("a frame shown by a width beside its live show is told nothing, got %q", w)
	}

	text := `<content>` + tabs + `<box show="$files.selected == 'a'"><text>a</text></box><text show="$files.selected == 'b'" medium:show="true">b</text></content>`
	at := where("content.ui", text, `medium:show="true">b`)
	w := tabsCompile(t, text).Warnings()
	if len(w) != 1 || !strings.HasPrefix(w[0].String(), at) ||
		!strings.Contains(w[0].String(), `medium:show="true" bên cạnh show từ dữ liệu không hiện lại một <text>`) {
		t.Errorf("want one warning at %s that the width does nothing on a text, got %q", at, w)
	}

	// what the element compiles to decides, not its tag: a link of words and a field have no display of
	// their own and are told; an image, an icon, a badge, a button, a link holding elements or written
	// with flex, and a text with lines have one, and are not (work's
	// TestBrowserWidthShowWarnsWhereItStaysHidden holds this to what a browser does)
	live := `show="$files.selected == 'b'" medium:show="true"`
	for _, c := range []struct {
		markup string
		told   bool
	}{
		{`<link value="/x" SHOW>x</link>`, true},
		{`<input name="q" alt="Tìm" SHOW />`, true},
		{`<text heading="2" SHOW>b</text>`, true},
		{`<image value="data:image/png;base64,iVBORw0KGgo=" alt="b" SHOW />`, false},
		{`<icon value="coffee" SHOW />`, false},
		{`<badge SHOW><text>b</text></badge>`, false},
		{`<button click="$files.select('a')" SHOW>b</button>`, false},
		{`<link value="/x" SHOW><icon value="coffee" /><text>x</text></link>`, false},
		{`<link value="/x" flex="row" SHOW>x</link>`, false},
		{`<text lines="2" SHOW>b</text>`, false},
	} {
		source := `<content>` + tabs + `<box show="$files.selected == 'a'"><text>a</text></box>` + strings.Replace(c.markup, "SHOW", live, 1) + `</content>`
		w := tabsCompile(t, source).Warnings()
		tag := c.markup[1:strings.IndexAny(c.markup, " >")]
		switch at := where("content.ui", source, `medium:show="true"`); {
		case c.told && (len(w) != 1 || !strings.HasPrefix(w[0].String(), at) || !strings.Contains(w[0].String(), `không hiện lại một <`+tag+`>`)):
			t.Errorf("%s: want one warning at %s, got %q", c.markup, at, w)
		case !c.told && len(w) != 0:
			t.Errorf("%s has a display of its own: no warning, got %q", c.markup, w)
		}
	}

	// CONTROL: a show written out is classes alone, which show a text from medium
	written := tabsCompile(t, `<content><text show="false" medium:show="true">b</text></content>`)
	mustContain(t, written.Render(Request{Path: "/"}), `<p class="show-false medium:show-true">b</p>`)
	if w := written.Warnings(); len(w) != 0 {
		t.Errorf("a text whose show is written is told nothing, got %q", w)
	}
}
