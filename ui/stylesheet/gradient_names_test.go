package stylesheet

import (
	"strings"
	"testing"
)

// A gradient's name shares one namespace with colours and the standard set, however its letters are
// written, and never takes a word that means something else where a colour stands (review of 12/10/2026):
// a site gradient named none made background="none", an error everywhere else, paint the gradient; one
// named fadetop meant something else than the standard fade-top, though background="fadetop" is told it
// is fade-top. Nor does a name end in -<digits> (operators of 13/10/2026): hero-2 would read as hero
// lighter by 2%, so it is refused with the name to write, the hyphens before the digits dropped.
// CONTROL: names that only hold a standard name's words among others (fade-topper, top-fade, none-left)
// are the site's, so are names with digits that are no step (hero2, hero-x2, h2o-glow), and
// background="none" stays an error on a palette without such a gradient.
func TestGradientNameIsNoKeywordNorAStandardSpelledOtherwise(t *testing.T) {
	hero := Gradient{Type: "linear", Direction: "to-end", Stops: []Stop{{Color: "ink", At: 0}, {Color: "brand", At: 100}}}
	for name, want := range map[string]string{
		"none":         `router.gradients: "none" là một từ khoá, không phải tên: background="none" hay color="none" không bao giờ là một gradient; đặt tên khác`,
		"auto":         `router.gradients: "auto" là một từ khoá, không phải tên`,
		"inherit":      `router.gradients: "inherit" là một từ khoá, không phải tên`,
		"current":      `router.gradients: "current" là một từ khoá, không phải tên`,
		"currentcolor": `router.gradients: "currentcolor" là một từ khoá, không phải tên`,
		"unset":        `router.gradients: "unset" là một từ khoá, không phải tên`,
		"fadetop":      `router.gradients: "fadetop" là cách viết khác của gradient chuẩn fade-top: background="fadetop" là lỗi gợi fade-top, không là gradient riêng; đặt tên khác`,
		"scrimtop":     `router.gradients: "scrimtop" là cách viết khác của gradient chuẩn scrim-top`,
		"fade-to-p":    `router.gradients: "fade-to-p" là cách viết khác của gradient chuẩn fade-top`,
		"head-line":    `router.gradients: "head-line" là cách viết khác của gradient chuẩn headline`,
		// in .ui, X-N is X lighter (operators of 13/10/2026): hero-2 would read as hero lighter by 2%
		"hero-2":       `router.gradients: "hero-2" kết thúc bằng -2, mà trong giao diện X-N là X nhạt hơn (hero-2 sẽ đọc là hero nhạt 2%): viết "hero2"`,
		"card-glow-10": `router.gradients: "card-glow-10" kết thúc bằng -10, mà trong giao diện X-N là X nhạt hơn (card-glow-10 sẽ đọc là card-glow nhạt 10%): viết "card-glow10"`,
		"hero-2-3":     `router.gradients: "hero-2-3" kết thúc bằng -3, mà trong giao diện X-N là X nhạt hơn (hero-2-3 sẽ đọc là hero-2 nhạt 3%): viết "hero23"`,
	} {
		if err := CheckGradient(name, hero); err == nil || !strings.HasPrefix(err.Error(), want) {
			t.Errorf("%s:\n got  %v\n want %s…", name, err, want)
		}
	}
	p, _ := NewPalette(false)
	if _, err := p.WithGradients(map[string]Gradient{"none": hero}); err == nil || !strings.Contains(err.Error(), `"none" là một từ khoá`) {
		t.Errorf("router.gradients({ none: … }) reaches the palette: %v", err)
	}
	if _, err := p.WithGradients(map[string]Gradient{"hero-2": hero}); err == nil || !strings.Contains(err.Error(), `"hero-2" kết thúc bằng -2`) {
		t.Errorf("router.gradients({ \"hero-2\": … }) reaches the palette: %v", err)
	}
	for _, name := range []string{"fade-topper", "top-fade", "none-left", "hero", "hero2", "hero-x2", "h2o-glow"} {
		if err := CheckGradient(name, hero); err != nil {
			t.Errorf("control: %s is a name of the site's own: %v", name, err)
		}
	}
	if _, err := NewTable(p).Class(nil, Intent{Name: "background", Value: "none"}); err == nil || !strings.Contains(err.Error(), `không có màu "none"`) {
		t.Errorf("control: background=\"none\" is an error: %v", err)
	}
}
