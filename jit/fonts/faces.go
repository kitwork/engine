package fonts

// The @font-face of any family a site names — embedded, or downloaded into the cache by Ensure —
// for what a page draws with it.

import (
	"strconv"
	"strings"
)

// Need is what a page draws with a family: FaceCSS ships the faces for it and no others.
type Need struct {
	// Weights are the weights the page sets; none means 400. A variable family ships one face per
	// subset whatever they are, its whole weight range; a static family ships the face of each weight
	// asked for, or the nearest one it has (the face the browser would draw it from).
	Weights []int
	// Italic: the page has italic text, so the italic faces ship too.
	Italic bool
	// Subsets are the scripts the page is written in ("latin", "latin-ext", "vietnamese"); none ships
	// every subset the family has. A face without a unicode-range draws every letter and always
	// ships; the numbered slices of a family ship with the subset they are cut from ("japanese",
	// "emoji").
	Subsets []string
}

// slicedSubsets are the subsets Google cuts into numbered slices, with no comment naming them: the CJK
// scripts and emoji.
var slicedSubsets = map[string]bool{
	"japanese": true, "korean": true, "chinese-simplified": true, "chinese-traditional": true, "chinese-hongkong": true,
	"emoji": true,
}

func (n Need) ships(r rule) bool {
	if r.style == "italic" && !n.Italic {
		return false
	}
	if len(n.Subsets) == 0 || r.unicode == "" {
		return true
	}
	group, sliced := sliceOf(r.subset)
	for _, s := range n.Subsets {
		if s == r.subset || sliced && (s == group || group == "" && slicedSubsets[s]) {
			return true
		}
	}
	return false
}

// sliceOf reads a slice's subset word: "japanese-slice-3" is a slice of japanese, "slice-3" one whose
// subset was not told apart (it ships with any sliced subset); not a slice otherwise.
func sliceOf(subset string) (group string, sliced bool) {
	i := strings.LastIndex(subset, "slice-")
	if i < 0 || i > 0 && subset[i-1] != '-' {
		return "", false
	}
	if n := subset[i+len("slice-"):]; n == "" || strings.Trim(n, "0123456789") != "" {
		return "", false
	}
	if i == 0 {
		return "", true
	}
	return subset[:i-1], true
}

// rule is one @font-face as a page gets it.
type rule struct {
	style   string
	weight  [2]int // lightest, heaviest (equal for one weight)
	stretch string
	subset  string
	unicode string
	src     string // the served URL: /_kitwork/fonts/<family>-<face>.<hash>.woff2
}

func (r rule) draws(weight int) bool { return weight >= r.weight[0] && weight <= r.weight[1] }

// rulesOf is every face of a family as it is served, in Google's order: the embedded faces, or the
// faces of the family in the cache (read from disk if this process has not yet — never downloaded:
// that is Ensure's).
func rulesOf(fam Family) ([]rule, error) {
	if fam.Embedded {
		var out []rule
		for _, f := range catalog[fam.Slug].faces {
			src := routePath(f.file)
			if src == "" {
				continue
			}
			out = append(out, rule{style: f.style, weight: [2]int{f.weight, max(f.weight, f.through)},
				subset: f.subset, unicode: f.unicode, src: src})
		}
		return out, nil
	}
	read, err := cachedFamilyOf(fam)
	if err != nil {
		return nil, err
	}
	out := make([]rule, 0, len(read.files))
	for _, f := range read.files {
		out = append(out, rule{style: f.face.Style, weight: f.face.Weight, stretch: f.face.Stretch,
			subset: f.face.Subset, unicode: f.face.Unicode, src: f.path})
	}
	return out, nil
}

// FaceCSS is the @font-face block of a family for what a page draws with it (Need), served from
// RoutePrefix: an embedded family, or one Ensure has brought into the cache (an error, in Vietnamese,
// when it is neither). A variable family is one face per subset (and style) with its weight range —
// font-weight: 1 1000 — so every weight the page sets is that one file; a static family is the faces
// of the weights asked for. A family with no upright style (Molle) ships its italic faces whatever
// Need.Italic says: the browser draws upright text from them too. font-display: swap; the
// unicode-range is Google's, so the browser fetches a subset only when the text has a letter in it.
//
//	@font-face{font-family:'Itim';font-style:normal;font-weight:400;font-display:swap;src:url(/_kitwork/fonts/itim-400-latin.<hash>.woff2) format('woff2');unicode-range:U+0000-00FF, …}
func FaceCSS(name string, need Need) (string, error) {
	fam, ok := Find(name)
	if !ok {
		return "", unknownFamily(name)
	}
	picked, err := shipped(fam, need)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, r := range picked {
		b.WriteString("@font-face{font-family:'" + fam.Name + "';font-style:" + r.style + ";font-weight:" + strconv.Itoa(r.weight[0]))
		if r.weight[1] != r.weight[0] {
			b.WriteString(" " + strconv.Itoa(r.weight[1]))
		}
		if r.stretch != "" {
			b.WriteString(";font-stretch:" + r.stretch)
		}
		b.WriteString(";font-display:swap;src:url(" + r.src + ") format('woff2')")
		if r.unicode != "" {
			b.WriteString(";unicode-range:" + r.unicode)
		}
		b.WriteString("}")
	}
	return b.String(), nil
}

// shipped are the faces FaceCSS writes for need, in Google's order.
func shipped(fam Family, need Need) ([]rule, error) {
	rules, err := rulesOf(fam)
	if err != nil {
		return nil, err
	}
	upright := false
	for _, r := range rules {
		upright = upright || r.style != "italic"
	}
	if !upright {
		need.Italic = true
	}
	var picked []rule
	if fam.Variable() {
		lightest, heaviest := fam.WeightRange()
		type key struct{ style, subset, stretch string }
		at := map[key]int{}
		for _, r := range rules {
			if !need.ships(r) {
				continue
			}
			k := key{r.style, r.subset, r.stretch}
			if i, twice := at[k]; twice {
				// a variable family vendored one face per weight (Outfit) is one file per subset: the
				// face of weight 400 is the one named, as Faces names it
				if r.draws(400) && !picked[i].draws(400) {
					picked[i] = r
				}
				continue
			}
			at[k] = len(picked)
			picked = append(picked, r)
		}
		for i := range picked {
			if picked[i].weight[0] == picked[i].weight[1] {
				picked[i].weight = [2]int{lightest, heaviest}
			}
		}
		return picked, nil
	}
	weights := need.Weights
	if len(weights) == 0 {
		weights = []int{400}
	}
	var have []int
	for _, r := range rules {
		if r.weight[0] == r.weight[1] {
			have = mergeWeights(have, []int{r.weight[0]})
		}
	}
	ship := map[int]bool{}
	for _, w := range weights {
		if nearest, ok := nearestWeight(have, w); ok {
			ship[nearest] = true
		}
	}
	for _, r := range rules {
		if r.weight[0] == r.weight[1] && ship[r.weight[0]] && need.ships(r) {
			picked = append(picked, r)
		}
	}
	return picked, nil
}

// nearestWeight is the weight of have the browser draws want from, in the order CSS font matching
// tries them (CSS Fonts 4, §5.2 font-weight): want itself; for a want of 400–500, the weights above it
// up to 500 (lightest first), then those below it (heaviest first), then those above 500 (lightest
// first); for a want under 400, those below it (heaviest first), then those above (lightest first);
// for a want over 500, those above it (lightest first), then those below (heaviest first).
func nearestWeight(have []int, want int) (int, bool) {
	below, above, upTo500 := 0, 0, 0 // the heaviest under want, the lightest over it, the lightest over it up to 500
	for _, w := range have {
		switch {
		case w == want:
			return w, true
		case w < want:
			if below == 0 || w > below {
				below = w
			}
		default:
			if above == 0 || w < above {
				above = w
			}
			if w <= 500 && (upTo500 == 0 || w < upTo500) {
				upTo500 = w
			}
		}
	}
	var order []int
	switch {
	case want >= 400 && want <= 500:
		order = []int{upTo500, below, above}
	case want < 400:
		order = []int{below, above}
	default:
		order = []int{above, below}
	}
	for _, w := range order {
		if w != 0 {
			return w, true
		}
	}
	return 0, false
}

// PreloadFace is the <link rel="preload"> of the face a page draws first with a family, chosen among
// the faces FaceCSS ships for the same Need — so it is always one the stylesheet names: upright (unless
// the family has no upright style), the face that draws the page's first weight (400 when it names
// none), latin when the page has latin. "" when the family is not ready (FaceCSS's error says why) or
// nothing ships.
func PreloadFace(name string, need Need) string {
	fam, ok := Find(name)
	if !ok {
		return ""
	}
	faces, err := shipped(fam, need)
	if err != nil || len(faces) == 0 {
		return ""
	}
	want := 400
	if len(need.Weights) > 0 {
		want = need.Weights[0]
	}
	var have []int
	for _, r := range faces {
		if r.weight[0] == r.weight[1] {
			have = mergeWeights(have, []int{r.weight[0]})
		}
	}
	if !fam.Variable() {
		if nearest, ok := nearestWeight(have, want); ok {
			want = nearest // a static family draws want from the nearest face it ships
		}
	}
	best, score := 0, -1
	for i, r := range faces {
		s := 0
		if r.style == "normal" {
			s += 4
		}
		if r.draws(want) {
			s += 2
		}
		if r.subset == "latin" {
			s++
		}
		if s > score {
			best, score = i, s
		}
	}
	return `<link rel="preload" as="font" type="font/woff2" href="` + faces[best].src + `" crossorigin>`
}
