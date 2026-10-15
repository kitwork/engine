package work

import (
	"fmt"
	"path"
	"strings"

	"github.com/kitwork/engine/jit/highlight"
	"github.com/kitwork/engine/ui/stylesheet"
	"github.com/kitwork/engine/value"
)

// uiHighlight is router.highlight in router.work (decision of 13/10/2026): the colour of each kind of
// code token on the site's .ui pages.
//
//	router.highlight({
//	  keyword: { light: "#7c3aed", dark: "#bb9af7" },
//	  string: "success",
//	  comment: "ink-30",
//	  "regular-expression": "success+20",
//	  inserted: "info"
//	});
//
// A key is a kind (stylesheet.HighlightKinds: every kind of jit/highlight but plain), a hyphenated one in
// quotes. A value is a colour as color= takes it, or the kind's own colour in each mode, { light, dark },
// each side "#rgb", "#rrggbb" or a named colour, as router.colors writes a token; inserted and deleted
// take an accent, the line's fill made from it as a badge's is. A kind left out keeps its engine colour.
// Like router.gradients, a branch's kinds are its folder's and the ones below, over the site's, the
// nearest folder winning per kind, and several calls in one file add up; each kind remembers the call
// that set it. What can be read from the call alone is checked at once and stops the boot: one object,
// keys that are kinds (the template's keys are told what they are here), values a string or a pair of
// two strings, no lone #hex, no pair for inserted or deleted. Whether a colour is one the route's palette
// names, an accent, or no gradient is answered by the route's pages, as a router.colors value is; and a
// colour short of contrast on the page's grounds is told at the call (stylesheet.Palette.HighlightWarning).
func (f *FolderRouter) uiHighlight(args ...value.Value) (*FolderRouter, error) {
	if f.callSite == nil {
		return f, fmt.Errorf("router.highlight viết ở cấp đầu của router.work, chạy một lần khi site khởi động: trong một hàm chạy lúc phục vụ (router.view) nó không đổi được màu của trang")
	}
	example := `router.highlight({ keyword: "brand", string: "success", comment: "ink-30" })`
	if len(args) == 1 && args[0].IsString() {
		name := strings.TrimSpace(args[0].String())
		if highlight.KnownTheme(name) {
			return f, fmt.Errorf("router.highlight(%q): %s là bộ màu của trang template, mã màu cố định cho một nền tối; khối mã của trang .ui tô theo bảng màu của site, đọc trên nền của trang ở cả sáng và tối. Đổi màu loại nào thì viết loại ấy: %s", name, name, example)
		}
		return f, fmt.Errorf("router.highlight(%q): router.highlight nhận một object màu theo loại chữ, không nhận một chữ: %s", name, example)
	}
	if len(args) != 1 || !args[0].IsMap() {
		return f, fmt.Errorf(`router.highlight nhận một object màu theo loại chữ: router.highlight({ keyword: "brand", comment: "ink-30" })`)
	}
	declared := args[0].Map()
	if err := highlightKeysError(declared); err != nil {
		return f, err
	}
	for _, kind := range stylesheet.HighlightKinds() {
		v, ok := declared[kind]
		if !ok {
			continue
		}
		c, err := highlightOf(kind, v)
		if err != nil {
			return f, err
		}
		if err := stylesheet.CheckHighlight(kind, c); err != nil {
			return f, err
		}
	}
	f.highlight = addDeclarations(f.highlight, declared)
	if f.highlightAt == nil {
		f.highlightAt = map[string]stylesheet.Source{}
	}
	place := f.place()
	for kind := range declared {
		f.highlightAt[kind] = place
	}
	return f, nil
}

// highlightKeysError gathers every key of one call that is no kind into one error, in the order of the
// keys: a key of the template's router.highlight says what it is on a .ui page (legacyHighlightKeys), any
// other what the kinds are (stylesheet.CheckHighlightKey: plain, another spelling, a short name, a typo).
// One key is told alone; several are named together, each with its sentence, a sentence several keys
// share (red, yellow, green) said once. A key is written as in code (stylesheet.HighlightKey: a hyphenated
// one in quotes). nil when every key is a kind.
func highlightKeysError(declared map[string]value.Value) error {
	type unread struct{ key, legacy, problem string }
	var keys []unread
	for _, key := range sortedNames(declared) {
		v := declared[key]
		if say, ok := legacyHighlightKeys[key]; ok {
			keys = append(keys, unread{key: key, legacy: say(legacyValue(v))})
			continue
		}
		written := "…"
		if v.IsString() {
			written = `"` + strings.TrimSpace(v.String()) + `"`
		}
		if _, problem := stylesheet.CheckHighlightKey(key, written); problem != "" {
			keys = append(keys, unread{key: key, problem: problem})
		}
	}
	switch {
	case len(keys) == 0:
		return nil
	case len(keys) == 1 && keys[0].legacy == "":
		return fmt.Errorf("router.highlight({ %s: … }): %s", stylesheet.HighlightKey(keys[0].key), keys[0].problem)
	}
	var names, sentences []string
	said := map[string]bool{}
	for _, k := range keys {
		key := stylesheet.HighlightKey(k.key)
		names = append(names, key)
		switch {
		case k.legacy == "":
			sentences = append(sentences, key+": "+k.problem+".")
		case !said[k.legacy]:
			said[k.legacy] = true
			sentences = append(sentences, k.legacy)
		}
	}
	return fmt.Errorf("router.highlight({ %s }): trang .ui không có khoá %s. %s", strings.Join(names, ", "), strings.Join(names, ", "), strings.Join(sentences, " "))
}

// legacyValue is a value of the template's router.highlight as a .ui kind would take it: a colour
// word as written; X-dark, the template's colour of X in dark, as X, which a .ui page turns with the
// mode itself; a #hex as a pair, one mode's colour; anything else as … .
func legacyValue(v value.Value) string {
	if !v.IsString() {
		return "…"
	}
	s := strings.TrimSpace(v.String())
	switch {
	case strings.HasPrefix(s, "#"):
		return `{ light: "` + s + `", dark: "#…" }`
	case strings.HasSuffix(s, "-dark") && len(s) > len("-dark"):
		stem := strings.TrimSuffix(s, "-dark")
		return `"` + stem + `" (` + s + ` là màu của ` + stem + ` ở chế độ tối của trang template; ở trang .ui ` + stem + ` tự đổi theo chế độ)`
	}
	return `"` + s + `"`
}

// legacyHighlightKeys are the keys of the template's router.highlight that are no kind of .ui, each with
// what it is there and what a .ui page writes instead, the value written given as it would be here.
var legacyHighlightKeys = map[string]func(written string) string{
	"theme": func(string) string {
		return "theme là bộ màu có tên của trang template (tokyonight, classic, mono), mã màu cố định cho một nền tối; trang .ui tô mã theo bảng màu của site ở cả sáng và tối và chưa có bộ màu có tên: bỏ theme, viết màu từng loại cần đổi."
	},
	"method": func(v string) string {
		return "method là tên gọi hàm ở trang template; ở trang .ui đó là function: " + stylesheet.HighlightKey("function") + ": " + v + "."
	},
	"command": func(v string) string {
		return "command là tên lệnh của dòng shell ở trang template; ở trang .ui đó là function (tên được gọi hay tên lệnh): " + stylesheet.HighlightKey("function") + ": " + v + "."
	},
	"flag": func(v string) string {
		return "flag là cờ dòng lệnh (-x, --long) ở trang template; ở trang .ui đó là attribute (tên thuộc tính trong markup hay cờ dòng lệnh): attribute: " + v + "."
	},
	"argument": func(string) string {
		return "argument là đối số của lệnh ở trang template; ở trang .ui đối số là chữ thường, theo màu chữ của <code>: viết color= trên <code> hay trên thẻ chủ terminal."
	},
	"text": func(string) string {
		return "text là chữ của khung ở trang template; ở trang .ui chữ thường của khối mã theo màu chữ của <code>: viết color= trên <code> hay trên thẻ chủ terminal."
	},
	"DEFAULT": func(string) string {
		return "DEFAULT là nền của khung terminal ở trang template; ở trang .ui khung là markup của người viết: viết background= trên thẻ chủ terminal."
	},
	"bar": func(string) string {
		return "bar là thanh tiêu đề của khung ở trang template; ở trang .ui thanh là một <box> của người viết: viết background= trên nó."
	},
	"line": func(string) string {
		return "line là đường kẻ của khung ở trang template; ở trang .ui viết border= trên phần tử của người viết."
	},
	"red":    windowDots,
	"yellow": windowDots,
	"green":  windowDots,
}

func windowDots(string) string {
	return `red, yellow, green là ba chấm cửa sổ ở trang template; ở trang .ui ba chấm là markup của người viết: background="danger", "warning", "success" trên từng chấm.`
}

// highlightOf reads one kind's value of router.highlight as the stylesheet takes it, refusing a shape
// that is not one: a string, a colour as color= takes it; or an object of light and dark only, each a
// string, not empty. Whether each reads as a colour is stylesheet.CheckHighlight's.
func highlightOf(kind string, v value.Value) (stylesheet.HighlightColour, error) {
	var c stylesheet.HighlightColour
	at := "router.highlight({ " + stylesheet.HighlightKey(kind) + ": … }): "
	if v.IsString() {
		c.Color = strings.TrimSpace(v.String())
		return c, nil
	}
	if !v.IsMap() {
		return c, fmt.Errorf(`%smàu của %s là một chữ (brand, ink-30, violet) hay một cặp { light: "#7c3aed", dark: "#bb9af7" }`, at, kind)
	}
	pair := v.Map()
	for _, key := range sortedNames(pair) {
		if key != "light" && key != "dark" {
			return c, fmt.Errorf(`router.highlight({ %s: { %s: … } }): một cặp màu chỉ nhận light và dark, không có "%s"`, stylesheet.HighlightKey(kind), key, key)
		}
		if !pair[key].IsString() {
			return c, fmt.Errorf(`%s%s của cặp màu là một chữ, "#rrggbb" hay một màu có tên ({ light: "#0550ae", dark: "#79c0ff" })`, at, key)
		}
	}
	if light, ok := pair["light"]; ok {
		c.Pair.Light = strings.TrimSpace(light.String())
	}
	if dark, ok := pair["dark"]; ok {
		c.Pair.Dark = strings.TrimSpace(dark.String())
	}
	if c.Pair == (stylesheet.Color{}) {
		return c, fmt.Errorf(`%scặp màu trống, viết { light: "#0550ae", dark: "#79c0ff" }`, at)
	}
	return c, nil
}

// place is where the call being made is written: the router file relative to the site root, forward
// slashes (a branch's router.work is mon/{slug}/router.work; a file it imports, by the path the import
// takes from it), with the line and column the VM gives the call — those of its boot errors.
func (f *FolderRouter) place() stylesheet.Source {
	loc := f.callSite()
	file := loc.File
	if file == "" {
		file = UIRouterFileName
	}
	if f.node != nil {
		file = path.Join(f.node.relPath(), file)
	}
	return stylesheet.Source{File: file, Line: int(loc.Line), Column: int(loc.Column)}
}

// withRouteHighlight is a route's palette with the router.highlight() of every folder from the site root
// down to the route, the nearest last, each kind with the call that set it
// (stylesheet.Palette.WithHighlight: the nearest wins per kind, each colour checked against the route's
// palette and measured on its grounds).
func withRouteHighlight(palette *stylesheet.Palette, node *RouteNode) (*stylesheet.Palette, error) {
	var folders []*FolderRouter
	for n := node; n != nil; n = n.parent {
		if n.folder != nil && len(n.folder.highlight) > 0 {
			folders = append(folders, n.folder)
		}
	}
	chain := make([]map[string]stylesheet.HighlightColour, 0, len(folders))
	for i := len(folders) - 1; i >= 0; i-- {
		layer := map[string]stylesheet.HighlightColour{}
		for kind, v := range folders[i].highlight {
			c, err := highlightOf(kind, v) // checked when declared: it reads
			if err != nil {
				return nil, err
			}
			c.Source = folders[i].highlightAt[kind]
			layer[kind] = c
		}
		chain = append(chain, layer)
	}
	return palette.WithHighlight(chain...)
}
