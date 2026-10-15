package stylesheet

import (
	"strings"
	"testing"
)

// skeleton's band in the middle is more ink than its sides (ink-82 between ink-88): darker than them in
// the light mode, where ink is dark, and lighter in the dark mode, where ink is light. That is what its
// description says (gradients.go, KITWORK_UI.md, the /colors page of the kitwork example); the review of
// 12/10/2026 found "its lighter band" there, true in the dark mode only (oklab L 0.832 between 0.873 on
// the kitwork palette in light). On every palette the set was designed on. The operators of 13/10/2026
// moved its stops from ink.12 and ink.18 to ink-88 and ink-82, the same colours: its image is the one
// written before, byte for byte (oldSkeleton, written down from the stylesheet before the change), and
// its samples are Kit Design's #d6dde4 at the sides and #c7cfd8 at the band in light, #1e2b38 and
// #263444 in dark, as they were.
func TestSkeletonBandIsMoreInk(t *testing.T) {
	for name, p := range designPalettes(t) {
		for _, m := range p.Modes() {
			samples, ok := p.GradientSamples("skeleton", m)
			if !ok {
				t.Fatalf("%s: skeleton has no samples", name)
			}
			side, band := Luminance(samples[0].Painted()), Luminance(samples[5].Painted())
			if darker := band < side; darker != (m == LightMode) {
				t.Errorf("%s, %s: the band's luminance %.4f against the sides' %.4f: want darker in light, lighter in dark", name, m.Word(), band, side)
			}
		}
	}
	const oldSkeleton = "linear-gradient(to right,color-mix(in oklab,var(--ink) 12%,var(--canvas)) 0%,color-mix(in oklab,var(--ink) 12%,var(--canvas)) 35%," +
		"color-mix(in oklab,var(--ink) 18%,var(--canvas)) 50%,color-mix(in oklab,var(--ink) 12%,var(--canvas)) 65%,color-mix(in oklab,var(--ink) 12%,var(--canvas)) 100%)"
	p, _ := NewPalette(true)
	css := pageCSS(t, NewTable(p), Intent{Name: "background", Value: "skeleton"})
	if !strings.Contains(css, ".background-skeleton{background-image:"+oldSkeleton+";") {
		t.Errorf("skeleton's image is no longer the one of ink.12 and ink.18:\n%s\nwant\n%s", css, oldSkeleton)
	}
	for _, c := range []struct {
		m          Mode
		side, band string
	}{{LightMode, "#d6dde4", "#c7cfd8"}, {DarkMode, "#1e2b38", "#263444"}} {
		samples, _ := p.GradientSamples("skeleton", c.m)
		if samples[0].Hex() != c.side || samples[5].Hex() != c.band {
			t.Errorf("Kit Design's skeleton in %s: side %s, band %s; want %s and %s as before", c.m.Word(), samples[0].Hex(), samples[5].Hex(), c.side, c.band)
		}
	}
}
