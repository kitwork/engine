package fonts

// Google's family list, embedded: router.fonts names any Google font, and the compiler checks the
// name, the language and the weights against this list without the network. google_catalog.py
// writes google_catalog.json from https://fonts.google.com/metadata/fonts.

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode"
)

// DefaultBody and DefaultCode are the typefaces of a site that names none in router.fonts: Google
// Sans Flex for text (variable: opsz 6–144, wght 1–1000) and Google Sans Code for code (wght 300–800,
// at its default MONO 1, so every letter is one width). Both are embedded (latin, latin-ext,
// vietnamese), so the default needs no network and no cache; a Kitwork UI page reads them for a role
// its site leaves out. JetBrains Mono, the code face before, stays embedded for a site that names it.
const (
	DefaultBody = "Google Sans Flex"
	DefaultCode = "Google Sans Code"
)

//go:embed google_catalog.json
var googleCatalog []byte

// Axis is one axis of a variable font: its tag (wght, opsz, wdth…) and its range.
type Axis struct {
	Tag string
	Min float64
	Max float64
}

// Family is a Google font as Kitwork serves it.
type Family struct {
	Name     string // "Google Sans Flex", as Google spells it — the CSS font-family
	Slug     string // "google-sans-flex": lowercase, hyphens — the cache folder and the served names
	Category string // Sans Serif, Serif, Display, Handwriting or Monospace
	// Subsets are the scripts it covers ("latin", "latin-ext", "vietnamese"…, without Google's
	// "menu"). For an embedded family these are the subsets embedded, which is what a page gets.
	Subsets []string
	Weights []int // the weights of its static upright styles, ascending
	Italics []int // the weights that have an italic, ascending; none for an embedded family that ships no italic
	// Axes are the axes of the files Kitwork serves: the weight axis, and the optical size axis with it
	// (opsz, wght) — the only ones cssRequest and vendor.py ask Google for. Google's other axes (wdth,
	// slnt, GRAD, ROND…) stay at their defaults in those files, so they are not listed. nil for a
	// static family.
	Axes []Axis
	// OpenSource: only an open-source family is downloaded (Ensure).
	OpenSource bool
	// Embedded: the family is in the binary (DefaultBody, DefaultCode, JetBrains Mono, Outfit, Fira
	// Code) and needs neither the network nor the cache.
	Embedded bool
}

// Variable reports whether the family is a variable font on the weight axis: one file per subset
// draws every weight of the range.
func (f Family) Variable() bool {
	_, ok := f.axis("wght")
	return ok
}

func (f Family) axis(tag string) (Axis, bool) {
	for _, a := range f.Axes {
		if a.Tag == tag {
			return a, true
		}
	}
	return Axis{}, false
}

// WeightRange is the lightest and the heaviest weight the family draws: its weight axis when it is
// variable, else its lightest and heaviest static style.
func (f Family) WeightRange() (lightest, heaviest int) {
	if a, ok := f.axis("wght"); ok {
		return int(a.Min), int(a.Max)
	}
	all := append(append([]int(nil), f.Weights...), f.Italics...)
	if len(all) == 0 {
		return 0, 0
	}
	sort.Ints(all)
	return all[0], all[len(all)-1]
}

// HasWeight reports whether the family draws a weight itself: a weight on its axis, or one of its
// static styles. A weight it lacks is synthesized by the browser (a smeared bold) — what the compiler
// reports for a static family.
func (f Family) HasWeight(weight int) bool {
	if a, ok := f.axis("wght"); ok {
		return float64(weight) >= a.Min && float64(weight) <= a.Max
	}
	for _, w := range f.Weights {
		if w == weight {
			return true
		}
	}
	for _, w := range f.Italics {
		if w == weight {
			return true
		}
	}
	return false
}

// Covers reports whether the family covers a subset ("vietnamese"): a page in that language gets
// every letter from it, not from the fallback.
func (f Family) Covers(subset string) bool {
	for _, s := range f.Subsets {
		if s == subset {
			return true
		}
	}
	return false
}

// Fallback is the generic stack written after the family, by its category: what the page draws with
// until the face arrives, and for any letter the family lacks.
func (f Family) Fallback() string {
	return CategoryFallback(f.Category)
}

// CategoryFallback is the generic font stack of a Google category.
func CategoryFallback(category string) string {
	switch category {
	case "Serif":
		return "ui-serif, serif"
	case "Monospace":
		return "ui-monospace, monospace"
	case "Handwriting":
		return "cursive"
	case "Display":
		return "system-ui, sans-serif"
	}
	return "ui-sans-serif, system-ui, sans-serif"
}

// clone is a copy that shares no slice with the catalog.
func (f *Family) clone() Family {
	c := *f
	c.Subsets = append([]string(nil), f.Subsets...)
	c.Weights = append([]int(nil), f.Weights...)
	c.Italics = append([]int(nil), f.Italics...)
	c.Axes = append([]Axis(nil), f.Axes...)
	return c
}

var (
	googleOnce   sync.Once
	googleList   []*Family          // by name
	googleByKey  map[string]*Family // familyKey(name) → family
	googleBySlug map[string]*Family
	googleKeys   []string // familyKey of googleList[i], for Suggest
	googleErr    error
)

// nameRe and slugRe are what a family's name and slug must be — the slug is a folder on disk (the
// cache) and a word in a served name, so a catalog entry that is not is refused when it is read.
var (
	nameRe   = regexp.MustCompile(`^[A-Za-z0-9]+( [A-Za-z0-9]+)*$`)
	slugRe   = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	subsetRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	axisRe   = regexp.MustCompile(`^[A-Za-z0-9]{4}$`)
)

// google reads the catalog once, the first time a family is asked for.
func google() {
	googleOnce.Do(func() {
		list, err := parseGoogleCatalog(googleCatalog)
		if err != nil {
			googleErr = err
			list = nil
		}
		setGoogle(list)
	})
}

// setGoogle indexes a family list (google; a test adds a family through it).
func setGoogle(list []*Family) {
	googleList = list
	googleByKey = make(map[string]*Family, len(list))
	googleBySlug = make(map[string]*Family, len(list))
	googleKeys = make([]string, len(list))
	for i, f := range list {
		key := familyKey(f.Name)
		googleByKey[key] = f
		googleBySlug[f.Slug] = f
		googleKeys[i] = key
	}
}

// Slug is a family name as a folder and a served name: lowercase, words joined by hyphens
// ("Google Sans Flex" → "google-sans-flex").
func Slug(name string) string {
	return strings.ToLower(strings.Join(strings.Fields(name), "-"))
}

// familyKey is a name with case and spacing ignored, so "google sans flex", "GoogleSansFlex",
// "google-sans-flex" and "Google+Sans+Flex" are one key; quotes around it are dropped.
func familyKey(name string) string {
	name = strings.Trim(strings.TrimSpace(name), `"'`)
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == ' ' || r == '-' || r == '_' || r == '+' || unicode.IsSpace(r):
		default:
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

// parseGoogleCatalog reads google_catalog.json: one family per line as [name, category, subsets,
// weights, italics, axes, open source], indexes into the categories and subsets lists, the trailing
// parts left out when empty (open source when 1).
func parseGoogleCatalog(data []byte) ([]*Family, error) {
	var doc struct {
		Categories []string `json:"categories"`
		Subsets    []string `json:"subsets"`
		Families   [][]any  `json:"families"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	for _, s := range doc.Subsets {
		if !subsetRe.MatchString(s) {
			return nil, fmt.Errorf("subset %q", s)
		}
	}
	list := make([]*Family, 0, len(doc.Families))
	for _, row := range doc.Families {
		f, err := googleRow(row, doc.Categories, doc.Subsets)
		if err != nil {
			return nil, err
		}
		list = append(list, f)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	// The embedded families say what a page gets: the subsets and italics vendored.
	for slug, vendored := range catalog {
		for _, f := range list {
			if f.Slug != slug || f.Name != vendored.name {
				continue
			}
			f.Embedded = true
			have := map[string]bool{}
			italic := false
			for _, face := range vendored.faces {
				have[face.subset] = true
				italic = italic || face.style == "italic"
			}
			var subsets []string
			for _, s := range f.Subsets {
				if have[s] {
					subsets = append(subsets, s)
				}
			}
			f.Subsets = subsets
			if !italic {
				f.Italics = nil
			}
		}
	}
	return list, nil
}

func googleRow(row []any, categories, subsets []string) (*Family, error) {
	bad := func(what string) error {
		return fmt.Errorf("family %v: %s", row, what)
	}
	if len(row) < 4 {
		return nil, bad("short row")
	}
	name, ok := row[0].(string)
	if !ok || !nameRe.MatchString(name) {
		return nil, bad("name")
	}
	f := &Family{Name: name, Slug: Slug(name), OpenSource: true}
	if !slugRe.MatchString(f.Slug) {
		return nil, bad("slug")
	}
	index := func(v any, size int) (int, bool) {
		n, ok := v.(float64)
		if !ok || n != float64(int(n)) || n < 0 || int(n) >= size {
			return 0, false
		}
		return int(n), true
	}
	category, ok := index(row[1], len(categories))
	if !ok {
		return nil, bad("category")
	}
	f.Category = categories[category]
	list, ok := row[2].([]any)
	if !ok {
		return nil, bad("subsets")
	}
	for _, v := range list {
		i, ok := index(v, len(subsets))
		if !ok {
			return nil, bad("subset")
		}
		f.Subsets = append(f.Subsets, subsets[i])
	}
	weights := func(v any) ([]int, error) {
		list, ok := v.([]any)
		if !ok {
			return nil, bad("weights")
		}
		var out []int
		for _, w := range list {
			n, ok := index(w, 1001)
			if !ok || n < 1 {
				return nil, bad("weight")
			}
			out = append(out, n)
		}
		sort.Ints(out)
		return out, nil
	}
	var err error
	if f.Weights, err = weights(row[3]); err != nil {
		return nil, err
	}
	if len(row) > 4 {
		if f.Italics, err = weights(row[4]); err != nil {
			return nil, err
		}
	}
	if len(row) > 5 {
		axes, ok := row[5].([]any)
		if !ok {
			return nil, bad("axes")
		}
		for _, v := range axes {
			a, ok := v.([]any)
			if !ok || len(a) != 3 {
				return nil, bad("axis")
			}
			tag, ok1 := a[0].(string)
			low, ok2 := a[1].(float64)
			high, ok3 := a[2].(float64)
			if !ok1 || !ok2 || !ok3 || !axisRe.MatchString(tag) || low > high {
				return nil, bad("axis")
			}
			f.Axes = append(f.Axes, Axis{Tag: tag, Min: low, Max: high})
		}
	}
	if len(row) > 6 {
		open, ok := row[6].(float64)
		if !ok {
			return nil, bad("open source")
		}
		f.OpenSource = open == 1
	}
	f.Axes = servedAxes(f.Axes)
	if f.Variable() {
		if low, high := f.WeightRange(); low < 1 || high > 1000 {
			return nil, bad("weight axis")
		}
	}
	return f, nil
}

// servedAxes are the axes of a Google family that the files Kitwork serves draw: wght, and opsz along
// with it (cssRequest asks for opsz only inside a weight range) — nil when there is no weight axis.
func servedAxes(axes []Axis) []Axis {
	weight := false
	for _, a := range axes {
		weight = weight || a.Tag == "wght"
	}
	if !weight {
		return nil
	}
	var out []Axis
	for _, a := range axes {
		if a.Tag == "wght" || a.Tag == "opsz" {
			out = append(out, a)
		}
	}
	return out
}

// errCatalog is what Find says when the embedded catalog could not be read (a build that broke
// google_catalog.json; the tests read it).
var errCatalog = errors.New("google_catalog.json")

// Find is the Google family a name means, case and spacing ignored: "google sans flex",
// "Google Sans Flex", "GoogleSansFlex" and the slug "google-sans-flex" are one family. Quotes around
// the name are dropped. false for a name Google does not have (Suggest names the closest).
func Find(name string) (Family, bool) {
	google()
	if f := googleByKey[familyKey(name)]; f != nil {
		return f.clone(), true
	}
	return Family{}, false
}

// Suggest is up to three Google families whose names are closest to an unknown one — by edit
// distance on the name with case and spacing ignored, a name that starts with what was typed counting
// as one edit ("google sans" → Google Sans, Google Sans Code, Google Sans Flex). With a subset
// ("vietnamese"), a family that covers it ranks as one edit closer than one that does not. nil when
// nothing is close.
func Suggest(name, subset string) []string {
	google()
	typed := familyKey(name)
	if typed == "" {
		return nil
	}
	limit := max(2, len([]rune(typed))/3)
	type candidate struct {
		name           string
		rank, distance int
	}
	var found []candidate
	for i, f := range googleList {
		key := googleKeys[i]
		d := editDistance(typed, key, limit+1)
		if len(typed) >= 4 && strings.HasPrefix(key, typed) && d > 1 {
			d = 1
		}
		if d > limit {
			continue
		}
		rank := d
		if subset != "" && f.Covers(subset) {
			rank--
		}
		found = append(found, candidate{f.Name, rank, d})
	}
	sort.Slice(found, func(i, j int) bool {
		a, b := found[i], found[j]
		if a.rank != b.rank {
			return a.rank < b.rank
		}
		if a.distance != b.distance {
			return a.distance < b.distance
		}
		return a.name < b.name
	})
	var out []string
	for _, c := range found {
		if len(out) == 3 {
			break
		}
		out = append(out, c.name)
	}
	return out
}

// editDistance is the Levenshtein distance between a and b, or stop when it is at least that (the
// rest of the table cannot bring it back under).
func editDistance(a, b string, stop int) int {
	ra, rb := []rune(a), []rune(b)
	if d := len(ra) - len(rb); d >= stop || -d >= stop {
		return stop
	}
	previous := make([]int, len(rb)+1)
	current := make([]int, len(rb)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		current[0] = i
		lowest := current[0]
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
			lowest = min(lowest, current[j])
		}
		if lowest >= stop {
			return stop
		}
		previous, current = current, previous
	}
	return min(previous[len(rb)], stop)
}

// unknownFamily is the error for a name Google does not have, with the closest names.
func unknownFamily(name string) error {
	google()
	if googleErr != nil {
		return fmt.Errorf("danh sách họ chữ Google Fonts trong Kitwork bị hỏng (%w: %v) — hãy dựng lại Kitwork", errCatalog, googleErr)
	}
	message := fmt.Sprintf("họ chữ %q không có trong Google Fonts", strings.TrimSpace(name))
	if suggestions := Suggest(name, ""); len(suggestions) > 0 {
		quoted := make([]string, len(suggestions))
		for i, s := range suggestions {
			quoted[i] = fmt.Sprintf("%q", s)
		}
		message += " — có phải ý là " + strings.Join(quoted, ", ") + "?"
	}
	return errors.New(message)
}
