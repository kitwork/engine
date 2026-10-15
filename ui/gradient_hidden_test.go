package ui

import (
	"strings"
	"testing"
)

// Gradient text is clipped to the letters of the element that writes it, and the letters inside inherit
// its transparent fill (-webkit-text-fill-color): an element between that paints a ground of its own —
// a background written at any condition, a glass, a card's paper, a pinned frame's, a layout's frame in
// another file — covers the clipped gradient, and its text is not drawn at all (review of 12/10/2026:
// Edge painted 0 letter pixels for the first case, and the page compiled with no word). A compile error
// at the gradient text, which goes into a <text> of its own. CONTROL: text that writes a colour of its
// own for every condition takes its fill back (its colour class); a frame that holds no text, or paints
// only a see-through colour, hides nothing — a neutral button's and a field's fills are translucent ink
// (decision F1, 13/10/2026), and Edge draws the gradient's letters through them; gradient text on the
// <text> inside a ground reads.
func TestGradientTextHiddenUnderAGround(t *testing.T) {
	for _, c := range []struct {
		files        map[string]string
		needle, says string
	}{
		{content(`<content><box color="headline"><box background="paper" padding="8"><text size="huge" weight="bold">MMMMMMMM</text></box></box></content>`), `color=`,
			`color="headline" là chữ gradient: chữ bên trong thừa hưởng nét tô trong suốt của nó (-webkit-text-fill-color), và <box> bên trong có nền background="paper" phủ lên gradient, nên chữ của <box> không hiện. ` +
				`Để chữ gradient trên một <text> riêng không bọc phần tử có nền (<text color="headline">…</text>), hoặc cho <box> màu chữ riêng (color="ink")`},
		// the ground the engine gives a pinned frame
		{content(`<content><box color="headline"><box pin="top"><text>Ghim</text></box></box></content>`), `color=`,
			`và <box> bên trong có nền canvas engine cho <box> phủ lên gradient, nên chữ của <box> không hiện`},
		// the surface of its kind
		{content(`<content><box color="headline"><card><text>Thẻ</text></card></box></content>`), `color=`,
			`và <card> bên trong có nền paper của <card> phủ lên gradient`},
		{content(`<content><box color="headline"><box material="glass"><text>Kính</text></box></box></content>`), `color=`,
			`và <box> bên trong có kính (material="glass") phủ lên gradient`},
		{content(`<content><box color="headline"><box hover:background="paper"><text>Một</text></box></box></content>`), `color=`,
			`và <box> bên trong có nền hover:background="paper" phủ lên gradient`},
		// a background from data is a declared token, which may well be solid
		{content(`<content><box color="headline"><box background="{{ tone }}"><text>Dữ liệu</text></box></box></content>`), `color=`,
			`và <box> bên trong có nền background="{{ tone }}" phủ lên gradient`},
		// a colour of its own only from medium leaves its letters transparent below medium
		{content(`<content><box color="headline"><button background="paper" medium:color="ink">Gửi</button></box></content>`), `color=`,
			`nên chữ của <button> không hiện`},
		// the gradient an arm of a {{ }} renders
		{content(`<content><box color="{{ on ? 'headline' : 'ink' }}"><button background="paper">Gửi</button></box></content>`), `color=`,
			`color="{{ on ? 'headline' : 'ink' }}" (nhánh headline) là chữ gradient: chữ bên trong thừa hưởng nét tô trong suốt`},
		// a layout in its own file paints the ground: told at the gradient text that wraps its call
		{map[string]string{
			"/site/content.ui":   `<content><box color="nebula"><price-tag /></box></content>`,
			"/site/price-tag.ui": `<box background="paper" padding="2"><text>9.000đ</text></box>`,
		}, `color=`, `color="nebula" là chữ gradient: chữ bên trong thừa hưởng nét tô trong suốt của nó (-webkit-text-fill-color), và <box> bên trong có nền background="paper" phủ lên gradient`},
	} {
		mustFail(t, c.files, where("content.ui", c.files["/site/content.ui"], c.needle), c.says)
	}
	for _, source := range []string{
		`<content><box color="headline"><box background="paper" padding="8"><text color="ink">Kitwork</text></box></box></content>`,
		`<content><box color="headline"><button color="ink">Gửi</button></box></content>`,
		`<content><box color="headline"><box background="line" height="1"></box><text heading="1">Kitwork</text></box></content>`,
		`<content><box color="headline"><box background="brand/10" padding="2"><text heading="1">Kitwork</text></box></box></content>`,
		// a neutral button's and a field's fills are translucent: the letters show through them
		`<content><box color="headline"><button size="huge">MMMMMMMM</button></box></content>`,
		`<content><box color="headline"><field><label>Tên</label><input name="q" /></field></box></content>`,
		`<content><button><text color="headline">Gửi</text></button></content>`,
		`<content><box background="paper"><text color="headline">Kitwork</text></box></content>`,
	} {
		mustCompile(t, content(source))
	}
}

// A gradient a {{ }} spells out renders its class as a written one does (color-headline from {{ on ?
// 'headline' : 'ink' }}), so the three refusals hold for it too (review of 12/10/2026: each compiled with
// no word and rendered the very CSS the error exists to prevent): gradient text on an element with a
// background or a card's surface, gradient text on an icon, a glass over a gradient background. The
// message names the arm. CONTROL: arms that are colours compile, gradient text from an arm on a <text>
// of its own compiles, and a colour from data alone (its domain the declared tokens) is no gradient.
func TestGradientWrittenInATernaryIsRefused(t *testing.T) {
	for _, c := range []struct{ source, needle, says string }{
		{`<content><box background="paper" color="{{ on ? 'headline' : 'ink' }}"><text>A</text></box></content>`, `color=`,
			`color="{{ on ? 'headline' : 'ink' }}" (nhánh headline) là chữ gradient: gradient được cắt theo nét chữ (background-clip:text), nên background="paper" trên cùng phần tử cũng bị cắt theo nét chữ, không còn là nền. ` +
				`Để chữ gradient trên một <text> riêng bên trong: <box background="paper"><text color="{{ on ? 'headline' : 'ink' }}">…</text></box>`},
		{`<content><card color="{{ on ? 'headline' : 'ink' }}"><text>A</text></card></content>`, `color=`,
			`(nhánh headline) là chữ gradient: gradient được cắt theo nét chữ (background-clip:text), nên nền paper của <card> cũng bị cắt`},
		{`<content><icon value="star" alt="Sao" color="{{ on ? 'nebula' : 'ink' }}" /></content>`, `color=`,
			`color="{{ on ? 'nebula' : 'ink' }}" (nhánh nebula) trên <icon>: icon là một mặt nạ tô bằng nền của nó, chữ gradient không tô được icon (icon biến mất); tô icon bằng gradient là background="{{ on ? 'nebula' : 'ink' }}"`},
		{`<content><box material="glass" background="{{ on ? 'glow' : 'paper' }}"><text>A</text></box></content>`, `material=`,
			`material="glass" đi cùng background="{{ on ? 'glow' : 'paper' }}" (nhánh glow): kính làm từ màu nền của khung, gradient không làm kính được`},
	} {
		mustFail(t, content(c.source), where("content.ui", c.source, c.needle), c.says)
	}
	for _, source := range []string{
		`<content><box background="paper" color="{{ on ? 'ink' : 'ink-30' }}"><text>A</text></box></content>`,
		`<content><box background="paper"><text color="{{ on ? 'headline' : 'ink' }}">A</text></box></content>`,
		`<content><box background="paper" color="{{ tone }}"><text>A</text></box></content>`,
		`<content><box material="glass" background="{{ on ? 'paper' : 'panel' }}"><text>A</text></box></content>`,
	} {
		mustCompile(t, content(source))
	}
}

// A colour background written for a later condition takes back the gradient of its own element only
// (stylesheet.Rules.TakeBack): the class it shares with a switch elsewhere on the page keeps its bytes,
// so the switch keeps its thumb, reboot.css's background-image (review of 12/10/2026: the thumb was
// gone once any element of the page had a gradient). A gradient a {{ }} may render is taken back too.
func TestAColourTakesBackOnlyItsElementsGradient(t *testing.T) {
	s := memorySite("", content(`<content>
  <box id="hero" background="glow" medium:background="paper"><text>Quán</text></box>
  <box id="band" background="{{ on ? 'tint' : 'canvas' }}" medium:background="panel"><text>Dải</text></box>
  <field><label>Bật</label><input type="switch" name="on" medium:background="paper" /></field>
</content>`))
	s.Reload = true
	page, err := s.Compile("/site")
	if err != nil {
		t.Fatal(err)
	}
	css := string(page.Stylesheet().CSS)
	for _, line := range []string{
		".medium\\:background-paper{background-color:var(--paper)}\n.medium\\:background-paper:where(.background-glow){background-image:none}\n",
		".medium\\:background-panel{background-color:var(--panel)}\n.medium\\:background-panel:where(.background-tint){background-image:none}\n",
	} {
		if !strings.Contains(css, "\n"+line) {
			t.Errorf("the page's stylesheet misses\n%s\nin\n%s", line, css)
		}
	}
	if strings.Contains(css, "background-color:var(--paper);background-image:none") || strings.Contains(css, "background-color:var(--panel);background-image:none") {
		t.Errorf("a colour class takes images away from every element that carries it:\n%s", css)
	}
	html := page.Render(Request{Path: "/", Scope: map[string]any{"on": true}})
	if !strings.Contains(html, `background-glow medium:background-paper`) || !strings.Contains(html, `medium:background-panel background-tint`) {
		t.Errorf("the elements carry the classes as written:\n%s", html)
	}
}
