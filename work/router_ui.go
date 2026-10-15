package work

// Kitwork UI pages: a site whose router is router.work describes its interface in *.ui files.
// router.view declares a folder's page; router.colors and router.fonts the site's design tokens, and
// router.appearance the light/dark mode its pages show first. Each page is compiled once per
// generation by the ui package, its head with it: its stylesheets are files of their own, one per role
// (/_kitwork/reboot.<hash>.css, colors.<hash>.css, fonts.<hash>.css, icons.<hash>.css, logo.<hash>.css,
// page.<hash>.css, engine/ui/stylesheet); a request only runs the view (its const and var become the
// names the page reads) and fills the prepared page with them.

import (
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	jitcss "github.com/kitwork/engine/jit/css"
	"github.com/kitwork/engine/jit/fonts"
	"github.com/kitwork/engine/jit/hydrate"
	"github.com/kitwork/engine/jit/icons"
	jitjs "github.com/kitwork/engine/jit/js"
	"github.com/kitwork/engine/render"
	"github.com/kitwork/engine/runtime"
	sitepkg "github.com/kitwork/engine/site"
	"github.com/kitwork/engine/ui"
	"github.com/kitwork/engine/ui/stylesheet"
	"github.com/kitwork/engine/value"
)

// View declares the page of this folder in Kitwork UI: its content.ui, inside the nearest page.ui,
// filled with the names the view declares.
//
//	router.view((ctx) => {
//	  const product = findProduct(ctx.params("slug"));
//	  if (!product) return ctx.notFound();
//	  ctx.title(product.name);
//	});
func (f *FolderRouter) View(args ...value.Value) *FolderMethod {
	m := f.declare("GET")
	m.uiView = true
	if len(args) > 0 && args[0].K == value.Func {
		m.handle = lambdaOf(args[0])
	}
	return m
}

// Transition says how <content /> changes when a link moves to a page of this folder (and its
// sub-folders) without a reload; header, footer and the progressbar stay still.
//
//	router.reload(false).transition("fade");
//
// "none" (the default) swaps at once, as a browser does. "fade" fades the old content into the new.
// "slide" slides it sideways, forward on a link and back on the Back button, the way a phone's
// screens move. Named by what the person sees, each with a counterpart on a native target. A person
// who asked their system for less motion gets "none" whatever the site says.
func (f *FolderRouter) Transition(args ...value.Value) (*FolderRouter, error) {
	if len(args) != 1 || !args[0].IsString() {
		return f, fmt.Errorf(`router.transition expects "none", "fade" or "slide"`)
	}
	switch name := args[0].String(); name {
	case "none", "fade", "slide":
		if f != nil {
			f.transition = name
		}
		return f, nil
	default:
		return f, fmt.Errorf(`router.transition("%s"): the transitions are "none", "fade" and "slide"`, name)
	}
}

// Colors declares the site's palette in Kitwork UI's renderer-neutral form: each token the interface
// names (background="canvas", color="brand") and its colour. The web target turns it into its CSS
// configuration.
//
//	router.colors({
//	  brand: "#c8742c",
//	  canvas: { light: "#f6f3ee", dark: "#141110" },
//	  "chart.muted": "#6b5d52"
//	});
//
// A token written chart.muted in the interface is the colour named "chart.muted" here: a role name is
// the site's to declare. Secondary text needs none, it is ink lighter (color="ink-30"). A
// { light, dark } pair gives the site a dark look: the dark value re-values the same token, a token
// with no dark value of its own is derived (router.themes' formula), and every page it reaches
// pre-paints the visitor's light/dark before the first frame. In the interface an accent grows only
// its on rung, the label written on it (brand.on); lighter and stronger are operators the page writes
// (brand-85, brand+15), never tokens, so a name never ends in -<digits> (chart-2 would read as chart
// lighter by 2%). The legacy template pages still take every rung jitcss grows (applyPalette).
//
// At the site root the palette is the site's. Declared in a branch (quan-ly/router.work) it re-values
// tokens for that folder and the ones below only, over the site's — the nearest folder wins per token
// (routeCSS). Several calls in one file add up, a later token over an earlier one.
func (f *FolderRouter) Colors(args ...value.Value) (*FolderRouter, error) {
	if len(args) != 1 || !args[0].IsMap() {
		return f, fmt.Errorf(`router.colors expects one object of tokens: router.colors({ brand: "#c8742c", canvas: { light: "#f6f3ee", dark: "#141110" } })`)
	}
	// a pair reads light and dark only: a mistyped key (drak) would leave the token without its dark
	for token, v := range args[0].Map() {
		if !v.IsMap() {
			continue
		}
		for key := range v.Map() {
			if key != "light" && key != "dark" {
				return f, fmt.Errorf(`router.colors({ %s: { %s: … } }): một cặp màu chỉ nhận light và dark, không có "%s"`, token, key, key)
			}
		}
	}
	if f == nil {
		return f, nil
	}
	f.colors = addDeclarations(f.colors, args[0].Map())
	f.publishSitePalette()
	return f, nil
}

// Fonts declares the site's typefaces by role: body for running text, code for code and figures.
//
//	router.fonts({ body: "Outfit", code: "Fira Code" });
//
// A role left out is set in the engine's default, embedded: Google Sans Flex for body, Google Sans
// Code for code (fonts.DefaultBody, fonts.DefaultCode). Root and branches merge as router.colors does:
// a branch's role replaces the site's for that branch, the roles it does not write stay the site's.
func (f *FolderRouter) Fonts(args ...value.Value) (*FolderRouter, error) {
	if len(args) != 1 || !args[0].IsMap() {
		return f, fmt.Errorf(`router.fonts expects one object of roles: router.fonts({ body: "Outfit", code: "Fira Code" })`)
	}
	// two roles, each a typeface's name: any other key or value would be dropped without a word
	for role, v := range args[0].Map() {
		if role != "body" && role != "code" {
			return f, fmt.Errorf(`router.fonts({ %s: … }): không có vai trò "%s"; router.fonts nhận body (chữ của trang) và code (mã và con số)`, role, role)
		}
		if !v.IsString() {
			return f, fmt.Errorf(`router.fonts({ %s: … }): %s là tên một kiểu chữ, viết trong dấu nháy: router.fonts({ %s: "Outfit" })`, role, role, role)
		}
	}
	if f == nil {
		return f, nil
	}
	f.fonts = addDeclarations(f.fonts, args[0].Map())
	f.publishSitePalette()
	return f, nil
}

// Appearance is the light/dark mode a page shows before the visitor chooses one.
//
//	router.appearance("dark");
//
// "system" (the default) follows the device; "light" and "dark" fix it. Inherited down the folders
// like router.transition: a branch may set its own, "system" included. The visitor's own choice (the
// appearance component, kit.appearance) always wins over it: a .ui page writes the mode on <html> as
// data-kitwork-appearance only for the pre-paint and kit.appearance to fall back on when the visitor
// has stored none. A "dark" route whose colours have no dark value gets its dark derived from the
// tokens, so it is dark rather than unchanged (routeCSS).
func (f *FolderRouter) Appearance(args ...value.Value) (*FolderRouter, error) {
	if len(args) == 1 && args[0].IsString() {
		switch mode := args[0].String(); mode {
		case "system", "light", "dark":
			if f != nil {
				f.appearance = mode
			}
			return f, nil
		default:
			return f, fmt.Errorf(`router.appearance("%s"): the modes are "system", "light" and "dark"`, mode)
		}
	}
	if len(args) == 1 && args[0].IsMap() {
		return f, fmt.Errorf(`router.appearance takes the mode, "system", "light" or "dark": colours are router.colors({ … }) and typefaces router.fonts({ … })`)
	}
	return f, fmt.Errorf(`router.appearance expects "system", "light" or "dark"`)
}

// Contrast says how a .ui page's pair of colours short of WCAG 2.2 AA contrast is told (ui/contrast.go).
//
//	router.contrast("strict");
//
// "warning" (the default) lets the page render as written and tells the author while developing: the
// terminal once per compile, the browser's console on every load. "strict" makes every such pair a
// compile error: the page answers it instead of rendering. Inherited down the folders like
// router.appearance: a branch may say its own. The engine's own defaults pass in both, so a site that
// writes no colour never shows unreadable text either way.
func (f *FolderRouter) Contrast(args ...value.Value) (*FolderRouter, error) {
	if len(args) != 1 || !args[0].IsString() {
		return f, fmt.Errorf(`router.contrast nhận một chữ, "warning" (mặc định) hoặc "strict": router.contrast("strict")`)
	}
	switch mode := args[0].String(); mode {
	case ui.ContrastWarning, ui.ContrastStrict:
		if f != nil {
			f.contrast = mode
		}
		return f, nil
	default:
		return f, ui.ContrastModeError(mode)
	}
}

// routeContrast is the router.contrast() of the nearest folder from the route up to the site root that
// sets one; "" (the default, "warning") when none does.
func routeContrast(node *RouteNode) string {
	for n := node; n != nil; n = n.parent {
		if n.folder != nil && n.folder.contrast != "" {
			return n.folder.contrast
		}
	}
	return ""
}

// Theme is the old name of router.colors() and router.fonts() together: router.theme({ colors, fonts
// }) applies its colors through router.colors and its fonts through router.fonts, at the root and in a
// branch alike, and the boot says so once for the file that calls it (removedMethodWarnings), so a
// site moves when it is ready.
func (f *FolderRouter) Theme(cfg value.Value) *FolderRouter {
	if f == nil {
		return f
	}
	f.removed(renamedTheme)
	if !cfg.IsMap() {
		return f
	}
	if colors, ok := cfg.Map()["colors"]; ok && colors.IsMap() {
		f.colors = addDeclarations(f.colors, colors.Map())
	}
	if fonts, ok := cfg.Map()["fonts"]; ok && fonts.IsMap() {
		f.fonts = addDeclarations(f.fonts, fonts.Map())
	}
	f.publishSitePalette()
	return f
}

// addDeclarations adds one call's tokens (or roles) to what the file declared before: a later value
// replaces an earlier one, a name it does not write stays.
func addDeclarations(into map[string]value.Value, declared map[string]value.Value) map[string]value.Value {
	if into == nil {
		into = make(map[string]value.Value, len(declared))
	}
	for name, v := range declared {
		into[name] = v
	}
	return into
}

// publishSitePalette hands the site root's colours and typefaces to the presentation as the site's CSS
// configuration. It is rebuilt whole from the defaults on every call, so router.colors and router.fonts
// may come in either order and as many times as the file likes. A branch keeps its own on the folder
// (routeCSS). router.css() is a template site's whole configuration: on one site the later wins.
func (f *FolderRouter) publishSitePalette() {
	if f == nil || f.tenant == nil || f.node == nil || f.node.parent != nil {
		return
	}
	config := buildJitcssConfig(value.Value{K: value.Map, V: map[string]value.Value{}})
	if config == nil {
		return
	}
	if applyPalette(config, f.colors, f.fonts) {
		f.tenant.presentation().SetThemes([]string{"dark"})
	}
	f.tenant.presentation().SetJITConfig(config)
}

// applyPalette writes one folder's router.colors() and router.fonts() into a CSS configuration: each
// colour, its dark rung (<token>-dark, which re-values the token under .dark) when it has one, and the
// typefaces. It reports whether any dark value was declared. Tokens are written in name order, so the
// CSS is the same on every boot.
//
// An accent (brand, success, warning… every colour but the surfaces and the text) is a family: from
// its one hue the engine grows deep, soft, wash and on (jitcss.Palette) unless the palette writes the
// rung itself. A grown rung is a token like any other, so a badge's soft fill and deep text get a dark
// value of their own instead of staying a light pastel on a dark page.
func applyPalette(config *jitcss.Config, colors, fonts map[string]value.Value) bool {
	dark := false
	declared := map[string]bool{}
	for name := range colors {
		declared[strings.ReplaceAll(name, ".", "-")] = true
	}
	for _, name := range sortedNames(colors) {
		v := colors[name]
		light, night := "", ""
		switch {
		case v.IsString():
			light = v.String()
		case v.IsMap():
			if c, ok := v.Map()["light"]; ok && c.IsString() {
				light = c.String()
			}
			if c, ok := v.Map()["dark"]; ok && c.IsString() {
				night = c.String()
			}
		}
		token := strings.ReplaceAll(name, ".", "-")
		if light != "" {
			setColor(config, token, light)
		}
		if night != "" {
			dark = true
			setColor(config, token+"-dark", night)
		}
		if light == "" || !accentToken(token) {
			continue
		}
		grown := jitcss.Palette(light)
		for _, rung := range sortedRungs(grown) {
			if rung == "DEFAULT" || declared[token+"-"+rung] {
				continue // the hue itself is the token; a rung the palette writes is its own
			}
			setColor(config, token+"-"+rung, grown[rung])
		}
	}
	for _, role := range sortedNames(fonts) {
		v := fonts[role]
		if !v.IsString() || strings.TrimSpace(v.String()) == "" {
			continue
		}
		fallback := jitcss.DefaultConfig.FontFamily["sans"]
		if role == "code" {
			fallback = jitcss.DefaultConfig.FontFamily["mono"]
		}
		config.FontFamily[role] = quoteFamily(strings.TrimSpace(v.String())) + ", " + fallback
	}
	return dark
}

func setColor(config *jitcss.Config, token, hex string) {
	config.Colors[token] = jitcss.Hex(hex)
	if !contains(config.Order, token) {
		config.Order = append(config.Order, token)
	}
}

func sortedNames(m map[string]value.Value) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedRungs(m map[string]string) []string {
	rungs := make([]string, 0, len(m))
	for rung := range m {
		rungs = append(rungs, rung)
	}
	sort.Strings(rungs)
	return rungs
}

// accentToken reports whether a colour is an accent that grows a family: every colour except the
// surfaces (canvas, panel, paper, line) and the text (ink and its rungs).
func accentToken(token string) bool {
	switch token {
	case "canvas", "panel", "paper", "line", "ink":
		return false
	}
	return !strings.HasPrefix(token, "ink-")
}

// routeAppearance is the router.appearance() of the nearest folder from the route up to the site root
// that sets one; "system" when none does.
func routeAppearance(node *RouteNode) string {
	for n := node; n != nil; n = n.parent {
		if n.folder != nil && n.folder.appearance != "" {
			return n.folder.appearance
		}
	}
	return "system"
}

// routeCSS is one route's CSS configuration, and whether its pages have a dark look (the render plan
// then pre-paints every one of them). It is the site's, with the router.colors() and router.fonts() of
// every folder between the site root and the route merged over it, the nearest last, and the dark look
// derived again from the merged tokens: a branch that changes brand gets a dark brand of its own, and
// a dark pair written only in a branch gives that branch a dark look. A route whose appearance is
// "dark" (mode) gets one too, derived from the tokens, when nothing declared one. A route that changes
// nothing shares the site's configuration as it is.
func routeCSS(site *jitcss.Config, node *RouteNode, mode string) (*jitcss.Config, bool) {
	var chain []*FolderRouter
	for n := node; n != nil && n.parent != nil; n = n.parent {
		if n.folder != nil && (len(n.folder.colors) > 0 || len(n.folder.fonts) > 0) {
			chain = append(chain, n.folder)
		}
	}
	siteDark := site != nil && (site.Themes["dark"] != nil || site.DarkByClass)
	deriveDark := mode == "dark" && !siteDark
	if len(chain) == 0 && !deriveDark {
		return site, siteDark
	}
	var merged *jitcss.Config
	if site != nil {
		merged = sitepkg.CloneJITConfig(site)
	} else {
		merged = buildJitcssConfig(value.Value{K: value.Map, V: map[string]value.Value{}})
	}
	var modes []string
	for m := range merged.Themes {
		modes = append(modes, m)
	}
	merged.Themes = nil
	dark := deriveDark
	for i := len(chain) - 1; i >= 0; i-- {
		if applyPalette(merged, chain[i].colors, chain[i].fonts) {
			dark = true
		}
	}
	if dark && !contains(modes, "dark") {
		modes = append(modes, "dark")
	}
	sort.Strings(modes)
	jitcss.DeriveThemes(merged, modes...)
	return merged, contains(modes, "dark") || merged.DarkByClass
}

// routeStyle is what a route's .ui pages are styled with: its colour chain (router.colors of the site
// and of every branch down to the route, over Kit Design's defaults, ui/stylesheet) and its typefaces
// (router.fonts the same way: the nearest folder's role wins), and how a pair of them short of contrast
// is told (router.contrast of the nearest folder that says). language is the route's router.language,
// which tells the compiler which way its lines run (a left or right attribute on a page written right
// to left is a warning).
type routeStyle struct {
	palette  *stylesheet.Palette
	err      error // a router.colors the palette refuses: every page of the route answers it
	body     string
	code     string
	contrast string
	language string
	// bundle is how the route's pages deliver their CSS and their JavaScript (router.bundle of the
	// nearest folder that names each part, router_ui_bundle.go)
	bundle bundleChoice
	// document is what every response of the route writes besides its request's own path, origin and
	// data (routeDocument), for the page router.bundle "auto" weighs (fitsInitialWindow)
	document ui.Request
}

// routeStyleOf reads a route's colours and typefaces down its folders. A route whose appearance is
// "dark" gets a dark look derived from the tokens when nothing declared one (router.appearance).
func routeStyleOf(node *RouteNode) routeStyle {
	var folders []*FolderRouter
	for n := node; n != nil; n = n.parent {
		if n.folder != nil && (len(n.folder.colors) > 0 || len(n.folder.fonts) > 0) {
			folders = append(folders, n.folder)
		}
	}
	var style routeStyle
	chain := make([]map[string]stylesheet.Color, 0, len(folders))
	for i := len(folders) - 1; i >= 0; i-- {
		f := folders[i]
		if len(f.colors) > 0 {
			chain = append(chain, paletteColors(f.colors))
		}
		for role, v := range f.fonts {
			if !v.IsString() || strings.TrimSpace(v.String()) == "" {
				continue
			}
			switch role {
			case "body":
				style.body = strings.TrimSpace(v.String())
			case "code":
				style.code = strings.TrimSpace(v.String())
			}
		}
	}
	style.palette, style.err = stylesheet.NewPalette(routeAppearance(node) == "dark", chain...)
	if style.err == nil {
		// router.gradients down the folders, checked against this palette (router_ui_gradients.go)
		style.palette, style.err = withRouteGradients(style.palette, node)
	}
	if style.err == nil {
		// router.highlight down the folders, checked against this palette with its gradients
		// (router_ui_highlight.go)
		style.palette, style.err = withRouteHighlight(style.palette, node)
	}
	style.contrast = routeContrast(node)
	style.language = routeLanguage(node)
	style.bundle = routeBundle(node)
	style.document = routeDocument(node)
	return style
}

// routeDocument is what respondUI writes for every page of a route whatever the request: router.meta's
// title (else its site), description and language merged down the folders, the router.transition of
// the nearest folder that sets one, router.appearance and router.favicon, as respondUI reads them. A
// request's own title (ctx.title…) and its data add to it; a compile cannot know them.
func routeDocument(node *RouteNode) ui.Request {
	var chain []*FolderRouter
	for n := node; n != nil; n = n.parent {
		if n.folder != nil {
			chain = append(chain, n.folder)
		}
	}
	meta := map[string]value.Value{}
	transition := ""
	for i := len(chain) - 1; i >= 0; i-- { // root to leaf, as router_serve merges them
		for k, v := range chain[i].meta {
			meta[k] = v
		}
		if chain[i].transition != "" {
			transition = chain[i].transition
		}
	}
	document := ui.Request{
		Title:       metaText(meta, "title"),
		Description: metaText(meta, "description"),
		Language:    metaText(meta, "language"),
		Transition:  transition,
		Appearance:  routeAppearance(node),
		Favicon:     folderFavicon(node),
	}
	if document.Title == "" {
		document.Title = metaText(meta, "site")
	}
	return document
}

// routeLanguage is the router.language() (or router.meta({ language })) of the nearest folder from the
// route up to the site root that says one; "" when none does. A request's own ctx language comes after
// the page compiled and does not reach it.
func routeLanguage(node *RouteNode) string {
	for n := node; n != nil; n = n.parent {
		if n.folder == nil {
			continue
		}
		if v, ok := n.folder.meta["language"]; ok && v.IsString() {
			return v.String()
		}
	}
	return ""
}

// paletteColors is one folder's router.colors() as the palette reads it: a colour, or a { light, dark }
// pair.
func paletteColors(colors map[string]value.Value) map[string]stylesheet.Color {
	out := make(map[string]stylesheet.Color, len(colors))
	for name, v := range colors {
		var c stylesheet.Color
		switch {
		case v.IsString():
			c.Light = v.String()
		case v.IsMap():
			if light, ok := v.Map()["light"]; ok && light.IsString() {
				c.Light = light.String()
			}
			if dark, ok := v.Map()["dark"]; ok && dark.IsString() {
				c.Dark = dark.String()
			}
		}
		out[name] = c
	}
	return out
}

// fontFamily is the typeface a .ui page sets a role in: the router.fonts name, or the engine's
// default for a role the site leaves out — Google Sans Flex for body, Google Sans Code for code
// (fonts.DefaultBody, fonts.DefaultCode), both embedded.
func fontFamily(role, declared string) string {
	switch {
	case declared != "":
		return declared
	case role == "code":
		return fonts.DefaultCode
	}
	return fonts.DefaultBody
}

// fontStack is a role's font-family on a .ui page: its typeface (fontFamily), then the system stack
// of that role, as a template page's font-body and font-code are. A Google family is written as Google
// spells it, the name its @font-face carries, however router.fonts wrote it ("google sans code").
func fontStack(role, family string) string {
	fallback := jitcss.DefaultConfig.FontFamily["sans"]
	if role == "code" {
		fallback = jitcss.DefaultConfig.FontFamily["mono"]
	}
	family = fontFamily(role, family)
	if found, ok := fonts.Find(family); ok {
		family = found.Name
	}
	return quoteFamily(family) + ", " + fallback
}

// shippedFamilies are the embedded typefaces whose faces a .ui page's fonts file carries, each once:
// the body's, and the code's when code says text is set in it. A family the binary does not carry
// (router.fonts may name any Google font) ships nothing yet: its name stays in the stack, the system
// stack draws it.
func shippedFamilies(style routeStyle, code bool) []fonts.Family {
	roles := [][2]string{{"body", style.body}}
	if code {
		roles = append(roles, [2]string{"code", style.code})
	}
	var out []fonts.Family
	seen := map[string]bool{}
	for _, role := range roles {
		family, ok := fonts.Find(fontFamily(role[0], role[1]))
		if !ok || !family.Embedded || seen[family.Slug] {
			continue
		}
		seen[family.Slug] = true
		out = append(out, family)
	}
	return out
}

// fontsFile is fonts.<hash>.css of a .ui page: --font-body and --font-code, which reboot.css and the
// page's font="code" read, and the @font-face of each embedded typeface it ships (shippedFamilies) for
// the weights given — the weights the site's pages use, so every page of a site shares one file
// (fonts.FaceCSS: a variable family is one face per subset whatever the weights). The code face ships
// when code is true, a page of the site setting text in it; a face is only fetched when text needs it.
func fontsFile(style routeStyle, weights []int, code bool) []byte {
	var b strings.Builder
	b.WriteString(":root{--font-body:" + fontStack("body", style.body) + ";--font-code:" + fontStack("code", style.code) + "}\n")
	for _, family := range shippedFamilies(style, code) {
		if faces, err := fonts.FaceCSS(family.Name, fonts.Need{Weights: weights}); err == nil && faces != "" {
			b.WriteString(faces + "\n")
		}
	}
	return []byte(b.String())
}

// fontPreloads are the <link rel="preload"> of the faces a page draws first, each once: the body's
// always, the code's only on a page that sets code — the face of running text's weight, latin
// (fonts.PreloadFace).
func fontPreloads(style routeStyle, code bool) string {
	var b strings.Builder
	for _, family := range shippedFamilies(style, code) {
		b.WriteString(fonts.PreloadFace(family.Name, fonts.Need{}))
	}
	return b.String()
}

// uiSite is one generation's Kitwork UI pages, kept for the generation's lifetime (any source change
// prepares a new generation). Every page is compiled while the generation is prepared (compileAll),
// before it answers a request, and its head is built then, once (KITWORK_UI §18): its stylesheets —
// reboot, its colours, its fonts, its icons, its own rules — are files in this generation's store,
// named by their role and their bytes, and the rest is the head every response writes as it is. A
// request runs none of the passes and writes no CSS but the icons only its data named.
type uiSite struct {
	site *ui.Site
	// development is AllowLocal when the generation was prepared: a page's warnings (ui.Page.Warnings)
	// are printed once (logWarnings) and linked as warnings.<hash>.js for the browser's console
	// (warningsScript). In production neither happens: nothing in the page, nothing per request.
	development bool
	// origin is the site's ("https://" and its domain), for the page router.bundle "auto" weighs
	// (weighedDocument): a response's own is its request's
	origin string
	// logged are the heads of the warnings this generation has printed (ui.Warning.Head), each said
	// once (logWarnings); under mu
	logged map[string]bool
	mu     sync.Mutex
	pages  map[string]*uiCompiled
	// styles are each route's colours and typefaces (routeStyleOf); presentations each route's
	// presentation (its appearance, Drive, components…), which builds the rest of the head. Written
	// while the render plan is built, before any page compiles; a route not listed compiles with the
	// site's own.
	styles        map[string]routeStyle
	presentations map[string]*render.Render
	// weights are the font weights the site's pages use, and code whether any of them sets text in the
	// code face, gathered when every page compiles (compileAll), so the pages share one fonts file
	weights []int
	code    bool

	// stylesheets are the pages' stylesheets by their name under /_kitwork/ without the extension,
	// <role>.<hash> (reboot.<hash>, colors.<hash>, fonts.<hash>, icons.<hash>, logo.<hash>, page.<hash>):
	// two pages whose CSS of one role is the same share that file — reboot for every page, the colours
	// of every page of one colour chain. Filled as pages compile; read by every request for a
	// stylesheet, hence a lock of their own.
	stylesheetsMu sync.RWMutex
	stylesheets   map[string]pageStylesheet
	// scripts are the generation's own scripts, under the same lock: the pages' warnings files while
	// developing, warnings.<hash> (warningsScript), and the bundles of router.bundle javascript "split",
	// bundle.<hash> (pageScripts), in production too
	scripts map[string]engineScript

	// dataIcons are the icons files of responses whose data named icons their page could not list:
	// one per distinct set, the recently used ones (dataIconsBound).
	dataIcons dataIconsStore
}

type uiCompiled struct {
	page *ui.Page
	err  error
	// head is what the page's <head> carries after its title: the pre-paint, the links to its
	// stylesheets, its scripts and font preloads, built once. headBefore and headAfter are the same
	// head around its icons link, for a response whose data named icons of its own (iconsHead).
	head                  string
	headBefore, headAfter string
	// icons are the names of the icons the page shows whatever its data: what its icons file holds.
	icons []string
	// presentation is the route's, for the CSS of icons only a request's data names.
	presentation *render.Render
	style        routeStyle
	// route is the path of the page's route ("/", "/docs/{slug}"), for the one render without data that
	// router.bundle "auto" weighs (fitsInitialWindow)
	route string
	// iconsInline says the page writes its icons in a <style> (router.bundle css other than false), so a
	// response whose data named icons of its own writes that set as a block too (iconsHead)
	iconsInline bool
}

// dataIconsBound is how many distinct sets of icons from data a generation keeps a file for: the
// recently used ones. The name of a set it let go answers like any name it never made (the engine's
// 404), and the next response that needs that set builds its file again, under the same name.
const dataIconsBound = 256

// dataIconsStore keeps the icons files made for responses, by set and by name, the most recently used
// first, at most bound of them. Safe for concurrent use.
type dataIconsStore struct {
	mu     sync.Mutex
	bound  int
	order  *list.List // of *dataIconsFile, the most recently used at the front
	bySet  map[string]*list.Element
	byName map[string]*list.Element
}

type dataIconsFile struct {
	set, name, href string
	sheet           pageStylesheet
}

// href is the URL of a set's icons file, building it the first time with build; "" when build makes
// no CSS. Using a set makes it the most recently used.
func (d *dataIconsStore) href(set string, build func() []byte) string {
	href, _ := d.file(set, build)
	return href
}

// file is a set's icons file, its URL and its CSS (a page that writes its icons inline writes the
// CSS, router.bundle), building it the first time with build; "" and nil when build makes no CSS.
// Using a set makes it the most recently used.
func (d *dataIconsStore) file(set string, build func() []byte) (string, []byte) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if e, ok := d.bySet[set]; ok {
		d.order.MoveToFront(e)
		file := e.Value.(*dataIconsFile)
		return file.href, file.sheet.body
	}
	css := build()
	if len(css) == 0 {
		return "", nil
	}
	if d.order == nil {
		d.order, d.bySet, d.byName = list.New(), map[string]*list.Element{}, map[string]*list.Element{}
	}
	if d.bound <= 0 {
		d.bound = dataIconsBound
	}
	sum := sha256.Sum256(css)
	digest := hex.EncodeToString(sum[:])
	name := "icons." + digest[:pageStylesheetHash]
	file := &dataIconsFile{set: set, name: name, href: jitjs.AssetPrefix + name + pageStylesheetSuffix,
		sheet: pageStylesheet{body: css, digest: digest}}
	e := d.order.PushFront(file)
	d.bySet[set] = e
	d.byName[name] = e
	for d.order.Len() > d.bound {
		last := d.order.Back()
		old := last.Value.(*dataIconsFile)
		d.order.Remove(last)
		delete(d.bySet, old.set)
		if d.byName[old.name] == last {
			delete(d.byName, old.name)
		}
	}
	return file.href, file.sheet.body
}

// stylesheet is the file a name names, if the store still keeps it; serving it makes it the most
// recently used.
func (d *dataIconsStore) stylesheet(name string) (pageStylesheet, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	e, ok := d.byName[name]
	if !ok {
		return pageStylesheet{}, false
	}
	d.order.MoveToFront(e)
	return e.Value.(*dataIconsFile).sheet, true
}

// pageStylesheet is one page stylesheet: its bytes and their whole SHA-256 (the ETag).
type pageStylesheet struct {
	body   []byte
	digest string
}

// A page stylesheet's name is <role>.<hash>.css at the top of the engine's prefix — one file per role
// on a page, so no folder: /_kitwork/reboot.<hash>.css, /_kitwork/colors.<hash>.css,
// /_kitwork/fonts.<hash>.css, /_kitwork/icons.<hash>.css, /_kitwork/logo.<hash>.css,
// /_kitwork/page.<hash>.css (the woff2 files are /_kitwork/fonts/…, a folder: "fonts." and "fonts/"
// are two names).
const (
	pageStylesheetSuffix = ".css"
	// pageStylesheetHash is how much of the SHA-256 the name carries, as for the engine's scripts.
	pageStylesheetHash = 16
)

// pageStylesheetName is the <role>.<hash> a request path names when it is a page stylesheet's
// spelling, /_kitwork/<name>.css with no folder in it; false for anything else. Whether that name is a
// file is the store's to say.
func pageStylesheetName(p string) (string, bool) {
	rest, ok := strings.CutPrefix(p, jitjs.AssetPrefix)
	if !ok || !strings.HasSuffix(rest, pageStylesheetSuffix) || strings.ContainsAny(rest, `/\`) {
		return "", false
	}
	return strings.TrimSuffix(rest, pageStylesheetSuffix), true
}

func newUISite(base string, reload bool) *uiSite {
	return &uiSite{
		site:          &ui.Site{Root: base, Reload: reload},
		development:   AllowLocal,
		pages:         map[string]*uiCompiled{},
		styles:        map[string]routeStyle{},
		presentations: map[string]*render.Render{},
		stylesheets:   map[string]pageStylesheet{},
		scripts:       map[string]engineScript{},
	}
}

// logWarnings prints, while developing, the warnings of a page just compiled (ui.Page.Warnings): each
// once per generation, "cảnh báo: file:line:col: message", so a page.ui every route shares says its
// warning once and not once a route. Once is by the warning's head (ui.Warning.Head): the line of the
// pairs short of contrast on one background, which lists each page's own places, is said for the first
// page that has it; the browser's console of every page still says that page's own (warningsScript). A
// new generation (a source changed) compiles again and says them again. In production nothing is
// printed. The places of such a line, and their count, are that page's, so the line says which page
// (route: "/", "/docs", the not-found page of a folder). u.mu is held.
func (u *uiSite) logWarnings(page *ui.Page, route string) {
	if !u.development || page == nil {
		return
	}
	for _, w := range page.Warnings() {
		head := w.Head()
		if u.logged[head] {
			continue
		}
		if u.logged == nil {
			u.logged = map[string]bool{}
		}
		u.logged[head] = true
		line := w.String()
		if head != line {
			line += " (số chỗ là của trang " + route + "; console của mỗi trang nêu chỗ của trang đó)"
		}
		fmt.Printf("cảnh báo: %s\n", line)
	}
}

// routeLabel names the page of a route folder in a line of the terminal: "/", "/docs", or the
// not-found page of one.
func routeLabel(relative string, notFound bool) string {
	route := "/" + strings.Trim(relative, "/")
	if notFound {
		return "không tìm thấy ở " + route
	}
	return route
}

// warningsScript is warnings.<hash>.js of a page with warnings, while developing: one console.warn a
// warning, "cảnh báo: file:line:col: message", so the author sees them in the browser's console on
// every load of the page — a file the head links, never an inline script and never bundled, whatever
// router.bundle says (its javascript "inline" writes the other parts in the page). It is stored like
// the page's stylesheets, by its role and its bytes; "" when the page has none.
func (u *uiSite) warningsScript(page *ui.Page) string {
	list := page.Warnings()
	if len(list) == 0 {
		return ""
	}
	var b strings.Builder
	for _, w := range list {
		message, _ := json.Marshal("cảnh báo: " + w.String()) // a JavaScript string: quotes, </script>, U+2028 escaped
		b.WriteString("console.warn(")
		b.Write(message)
		b.WriteString(");\n")
	}
	return u.storeScript(warningsRole, []byte(b.String()))
}

// warningsRole is the role of a page's warnings file, /_kitwork/warnings.<hash>.js.
const warningsRole = "warnings"

// script is the generation's script a name names (warnings.<hash> while developing, bundle.<hash> of
// router.bundle javascript "split", generationScriptName), or false when this generation made none by
// that name.
func (u *uiSite) script(name string) (engineScript, bool) {
	if u == nil {
		return engineScript{}, false
	}
	u.stylesheetsMu.RLock()
	defer u.stylesheetsMu.RUnlock()
	script, ok := u.scripts[name]
	return script, ok
}

// setRoute records a route's presentation and its colours and typefaces.
func (u *uiSite) setRoute(relative string, node *RouteNode, presentation *render.Render) {
	if u != nil {
		u.styles[relative] = routeStyleOf(node)
		u.presentations[relative] = presentation
	}
}

// compileAll compiles every page of the routes — each folder's content.ui, and the not-found page a
// miss below it shows — then gathers the font weights they use and whether any sets code, then builds
// each one's head and stylesheets, so the generation's store holds every page's stylesheets before the
// first request and the pages share one fonts file. A page a browser still has open after a restart
// asks for the same names, since a name is the bytes and the same source gives the same bytes. A
// mistake in a page is kept and answered when the page is asked for, as before. A site with no .ui
// page at all compiles nothing.
func (u *uiSite) compileAll(routes []string) {
	if u == nil {
		return
	}
	pages := false
	for _, relative := range routes {
		if u.hasPage(relative) {
			pages = true
		}
	}
	if !pages {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	var compiled []*uiCompiled
	for _, notFound := range []bool{false, true} {
		for _, relative := range routes {
			if !notFound && !u.hasPage(relative) {
				continue
			}
			if page := u.compile(relative, notFound); page.err == nil && page.page != nil {
				compiled = append(compiled, page)
			}
		}
	}
	seen := map[int]bool{}
	for _, page := range compiled {
		for _, w := range page.page.Weights() {
			seen[w] = true
		}
		u.code = u.code || page.page.UsesCode()
	}
	for w := range seen {
		u.weights = append(u.weights, w)
	}
	sort.Ints(u.weights)
	for _, page := range compiled {
		if page.head == "" {
			page.head = u.buildHead(page)
		}
	}
}

// stylesheet is the page stylesheet a name (<role>.<hash>) names, or false when this generation
// compiled no page with that CSS in that role.
func (u *uiSite) stylesheet(name string) (pageStylesheet, bool) {
	if u == nil {
		return pageStylesheet{}, false
	}
	u.stylesheetsMu.RLock()
	sheet, ok := u.stylesheets[name]
	u.stylesheetsMu.RUnlock()
	if ok {
		return sheet, true
	}
	return u.dataIcons.stylesheet(name)
}

// iconsHead is the head of one response whose data named icons the page's icons file could not list:
// the page's own icons and those, ONE set, in place of the page's (built once per distinct set,
// data-kitwork-generated="icons", so Drive trades it like the others): a block when the page writes its
// own icons as one (iconsInline, router.bundle css other than false), else one icons file it links.
// The head built once when none of those icons is one the engine has.
func (u *uiSite) iconsHead(compiled *uiCompiled, extra []string) string {
	seen := map[string]bool{}
	var set []string
	added := false
	for _, name := range compiled.icons {
		if !seen[name] && icons.Has(name) {
			seen[name] = true
			set = append(set, name)
		}
	}
	for _, name := range extra {
		if !seen[name] && icons.Has(name) {
			seen[name] = true
			set = append(set, name)
			added = true
		}
	}
	if !added || compiled.presentation == nil {
		return compiled.head
	}
	sort.Strings(set)
	href, css := u.dataIcons.file(strings.Join(set, " "), func() []byte { return compiled.presentation.IconsStylesheet(set) })
	if href == "" {
		return compiled.head
	}
	if compiled.iconsInline {
		return compiled.headBefore + inlineStyle("icons", css) + compiled.headAfter
	}
	return compiled.headBefore + render.Link(href, "icons") + compiled.headAfter
}

// store keeps one stylesheet under <role>.<hash> and returns its URL, "" for no CSS.
func (u *uiSite) store(role string, css []byte) string {
	if len(css) == 0 {
		return ""
	}
	sum := sha256.Sum256(css)
	digest := hex.EncodeToString(sum[:])
	name := role + "." + digest[:pageStylesheetHash]
	u.stylesheetsMu.Lock()
	u.stylesheets[name] = pageStylesheet{body: css, digest: digest}
	u.stylesheetsMu.Unlock()
	return jitjs.AssetPrefix + name + pageStylesheetSuffix
}

// iconsSlot holds the place of the icons link while a head is built, so a response whose data names
// icons of its own can link another file there (uiCompiled.headBefore, headAfter).
const iconsSlot = "\x00icons\x00"

// buildHead builds a compiled page's head once: the route's presentation passes over its skeleton
// (the pre-paint, the scripts, its icons and logos), then after the pre-paint the font preloads (first,
// so the faces are asked for before the browser reads any CSS: until the fix of 14/10/2026 they came last, after
// a page block that can be past the first window) and its stylesheets in the cascade's order —
// reboot, colors, fonts, icons, logo, page —, before the scripts. How each part is delivered is the
// route's router.bundle (router_ui_bundle.go): by default ("auto") the page's own CSS (page, icons,
// logo) is written in the page, and the site's (reboot, colors, fonts) too when the page then fits the
// first window, else linked as one site.<hash>.css; with router.bundle(false) each role is a file of
// its own, as before 14/10/2026. Every layered stylesheet opens with the same layer statement, so its
// rules keep their order wherever its link or block stands; the few unlayered rules (the page's focus
// ring over the palette's) rely on the order the head writes, which Drive keeps on a move (drive.js
// stageStylesheets). A page that can carry a scrolled: class gets scrolled.<hash>.js right after its
// stylesheets (deferred, like the scripts after it); Drive loads it when a move arrives at such a page
// from one without it. While developing, a page with warnings gets warnings.<hash>.js after that
// (warningsScript): its warnings in the browser's console, always a file of its own, never bundled,
// never weighed by "auto" (production's bytes decide).
func (u *uiSite) buildHead(compiled *uiCompiled) string {
	page, presentation := compiled.page, compiled.presentation
	if page == nil || presentation == nil {
		return ""
	}
	compiled.icons = page.Icons()
	built := presentation.PresentHead(page.Skeleton())
	weights := page.Weights()
	if len(u.weights) > 0 {
		seen := map[int]bool{}
		for _, w := range append(append([]int(nil), u.weights...), weights...) {
			seen[w] = true
		}
		weights = weights[:0:0]
		for w := range seen {
			weights = append(weights, w)
		}
		sort.Ints(weights)
	}
	site := siteCSSParts(compiled.style, fontsFile(compiled.style, weights, u.code || page.UsesCode()))
	var own *cssPart // the page's own icons, nil when it shows no icon
	var logos []cssPart
	for _, sheet := range built.Stylesheets {
		part := cssPart{role: sheet.Name, generated: sheet.Generated, css: []byte(sheet.CSS)}
		switch {
		case len(part.css) == 0:
		case sheet.Name == "icons":
			own = &part
		default:
			logos = append(logos, part)
		}
	}
	rules := cssPart{role: "page", generated: "css", css: page.Stylesheet().CSS}
	scripts := ""
	if page.UsesScrolled() {
		// the flag the scrolled: rules read, only for a page that can carry one; no kernel needed
		scripts = `<script data-kitwork-generated="scrolled" src="` + scrolledScript().path + `" defer></script>`
	}
	scripts = u.pageScripts(compiled.style.bundle.javascript, &built, scripts)
	// the font preloads first, so the browser asks for the faces before it reads the CSS (a page block
	// can be past the first window)
	preloads := fontPreloads(compiled.style, page.UsesCode())
	// "auto" weighs the head production sends: the warnings file below is development's only
	fits := func(links string) bool { return u.fitsInitialWindow(compiled, built.Insert(preloads+links+scripts)) }
	links, icons, iconsInline := u.pageCSS(compiled.style.bundle.css, site, own, logos, rules, fits)
	compiled.iconsInline = iconsInline
	if u.development {
		if src := u.warningsScript(page); src != "" {
			// while developing, a page with warnings says them in the browser's console on every load
			scripts += `<script data-kitwork-generated="warnings" src="` + src + `" defer></script>`
		}
	}
	whole := built.Insert(preloads + links + scripts)
	compiled.headBefore, compiled.headAfter, _ = strings.Cut(whole, iconsSlot)
	return compiled.headBefore + icons + compiled.headAfter
}

// siteFor is the site a route's pages compile against: the site itself with that route's colours, its
// router.contrast and its router.language.
func (u *uiSite) siteFor(relative string) *ui.Site {
	style, ok := u.styles[relative]
	if !ok {
		style = routeStyleOf(nil)
	}
	s := *u.site
	s.Palette, s.Contrast, s.Language = style.palette, style.contrast, style.language
	return &s
}

func (u *uiSite) dir(relative string) string {
	return filepath.Join(u.site.Root, filepath.FromSlash(relative))
}

// page returns the compiled page of a route folder, or its not-found page, with its head. A page no
// route listed (compileAll) is compiled on first use the same way. A nil page with no error means the
// site has no notfound.ui for that place.
func (u *uiSite) page(relative string, notFound bool) (*uiCompiled, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	compiled := u.compile(relative, notFound)
	if compiled.err == nil && compiled.page != nil && compiled.head == "" {
		compiled.head = u.buildHead(compiled)
	}
	return compiled, compiled.err
}

// compile compiles a page once, without its head; u.mu is held.
func (u *uiSite) compile(relative string, notFound bool) *uiCompiled {
	key := relative
	if notFound {
		key += "\x00notfound"
	}
	if compiled, ok := u.pages[key]; ok {
		return compiled
	}
	compiled := &uiCompiled{presentation: u.presentations[relative], style: u.styles[relative], route: "/" + strings.Trim(relative, "/")}
	if _, ok := u.styles[relative]; !ok {
		compiled.style = routeStyleOf(nil)
	}
	switch {
	case compiled.style.err != nil:
		compiled.err = compiled.style.err
	case notFound:
		compiled.page, compiled.err = u.siteFor(relative).CompileNotFound(u.dir(relative))
	default:
		compiled.page, compiled.err = u.siteFor(relative).Compile(u.dir(relative))
	}
	u.logWarnings(compiled.page, routeLabel(relative, notFound))
	u.pages[key] = compiled
	return compiled
}

func (u *uiSite) hasPage(relative string) bool {
	return u != nil && u.site.HasContent(u.dir(relative))
}

// folderHasUIPage reports whether a folder has a Kitwork UI page (content.ui) of its own.
func (t *Tenant) folderHasUIPage(n *RouteNode) bool {
	plan := t.renderPlan()
	return n != nil && plan != nil && plan.ui.hasPage(n.relPath())
}

// hasUINotFound reports whether a not-found reached at this folder has a notfound.ui to show.
func (t *Tenant) hasUINotFound(n *RouteNode) bool {
	plan := t.renderPlan()
	if n == nil || plan == nil || plan.ui == nil {
		return false
	}
	compiled, err := plan.ui.page(n.relPath(), true)
	return compiled.page != nil || err != nil
}

// serveUIView runs a router.view and answers with the folder's page, or with the not-found page when
// the view called ctx.notFound().
func (t *Tenant) serveUIView(vm *runtime.VM, leaf *FolderRouter, method *FolderMethod, ctxObj *Context, reqRouter *Router, node *RouteNode) {
	var scope map[string]any
	var converter *uiConverter
	var failed func() value.Value
	if method.handle != nil && leaf.bytecode != nil {
		bc := leaf.bytecode
		vm.FastResetPrepared(bc.Program)
		started := time.Now()
		result, locals := vm.ExecuteLambdaScope(method.handle, ctxObj.arguments(method.handle))
		t.recordVMExecution(bc.Program, vm, result, time.Since(started))
		if result.K == value.Invalid {
			// the view failed: the page answers that, as it answers a compile error (respondUI), and never
			// falls through to another way of serving the folder
			t.recordRuntimeFailure(reqRouter, bc, "view", result)
			// what failed in the view while developing; in production the status and the request id
			reqRouter.fail(uncaught(result.Text()), http.StatusInternalServerError)
			return
		}
		if reqRouter.response.IsSend() {
			return // the view answered by itself (redirect, json…)
		}
		var err error
		if scope, converter, err = convertViewScope(vm, method.handle, locals); err != nil {
			// a value the page would read holds itself (uiConverter): the view's error, as above
			t.recordFailure(reqRouter, "view", err)
			reqRouter.fail(err, http.StatusInternalServerError)
			return
		}
		failed = func() value.Value { return converter.fatal }
	}
	t.respondUI(reqRouter, node, scope)
	if failed != nil {
		if failure := failed(); failure.K == value.Invalid && failure.IsError {
			// a function of the view the page called hit what nothing catches (energy, cancellation):
			// the request ends as a failed view does, whatever the page wrote
			t.recordRuntimeFailure(reqRouter, leaf.bytecode, "view", failure)
			reqRouter.fail(uncaught(failure.Text()), http.StatusInternalServerError)
		}
	}
}

// respondUI renders the Kitwork UI page of a folder (or its not-found page) into the response.
func (t *Tenant) respondUI(reqRouter *Router, node *RouteNode, scope map[string]any) {
	plan := t.renderPlan()
	if plan == nil || plan.ui == nil || node == nil {
		reqRouter.response.Text(value.New("Service Unavailable"), http.StatusServiceUnavailable)
		return
	}
	notFound := reqRouter.isNotfound
	status := http.StatusOK
	if notFound {
		status = http.StatusNotFound
	} else if code := reqRouter.response.Code(); code >= 100 && code <= 999 {
		// the status the view set with ctx.status(503) (14/10/2026: until then the page always
		// answered 200), so a view that read a failure with safe(() => …) can say so
		status = code
	}

	// meta: inherited router.meta()/language() root→leaf, then what this request set (ctx.title…).
	meta := map[string]value.Value{}
	for k, v := range reqRouter.chainMeta {
		meta[k] = v
	}
	if vb := reqRouter.viewBuilder; vb != nil {
		for k, v := range vb.meta {
			meta[k] = v
		}
		vb.rendered = true // this page is the response; the template view must not render too
	}

	compiled, err := plan.ui.page(node.relPath(), notFound)
	if err != nil {
		// file:line:column and what is wrong while developing; in production the status and the request id
		t.recordFailure(reqRouter, "page", err)
		reqRouter.fail(err, http.StatusInternalServerError)
		return
	}
	if compiled.page == nil {
		reqRouter.response.Text(value.New("Not Found"), http.StatusNotFound)
		return
	}

	r := reqRouter.request
	request := ui.Request{
		Path:        r.URL.Path,
		Origin:      requestOrigin(r),
		Scope:       scope,
		Title:       metaText(meta, "title"),
		Description: metaText(meta, "description"),
		Language:    metaText(meta, "language"),
		Transition:  reqRouter.transition,
		Appearance:  routeAppearance(node),
		Favicon:     folderFavicon(node),
	}
	if request.Title == "" {
		request.Title = metaText(meta, "site")
	}
	// The head was built once, when the page compiled: the response writes it as it is, and no
	// presentation pass reads this document. Every class a value from data can make is in the page's
	// stylesheet; icons this request's data named, which the page's icons file could not list, make the
	// response carry one icons stylesheet for the page's icons and those (iconsHead): a block where the
	// page's own icons are a block (router.bundle css other than false, whose head carries the page's
	// CSS in <style> blocks), else a file. Nothing else is written per request.
	request.Head = compiled.head
	request.IconsHead = func(extra []string) string { return plan.ui.iconsHead(compiled, extra) }
	if AllowLocal {
		// while developing: a value from data that made no class (a colour no token of the palette names),
		// once per page, in the voice of the compile's warnings (logWarnings)
		request.Warn = func(message string) { fmt.Printf("cảnh báo: %s\n", message) }
	}
	reqRouter.response.HTML(value.New(compiled.page.Document(request)), status)
}

// folderFavicon is the icon a page links: router.favicon of the nearest folder that declares one.
func folderFavicon(node *RouteNode) string {
	for n := node; n != nil; n = n.parent {
		if n.folder != nil && n.folder.favicon != "" {
			return n.folder.favicon
		}
	}
	return ""
}

func metaText(meta map[string]value.Value, key string) string {
	if v, ok := meta[key]; ok && v.IsString() {
		return v.String()
	}
	return ""
}

func requestOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// viewScope is what the page reads: the names the view declared, over the names its file declared or
// imported at the top (router.work's imports are visible to the interface).
func viewScope(vm *runtime.VM, view *value.Lambda, locals map[string]value.Value) (map[string]any, error) {
	scope, _, err := convertViewScope(vm, view, locals)
	return scope, err
}

// convertViewScope is viewScope with the converter that made it: the functions of the view it hands
// the page run through it, and its fatal is what ended one of them that nothing may catch.
func convertViewScope(vm *runtime.VM, view *value.Lambda, locals map[string]value.Value) (map[string]any, *uiConverter, error) {
	c := &uiConverter{vm: vm}
	scope := make(map[string]any, len(locals)+8)
	for name, v := range locals {
		if visibleName(name) {
			if x, ok := c.convert(v, 0); ok {
				scope[name] = x
			}
			if c.cycle != nil {
				return nil, nil, cycleFailure(view, name, c.cycle)
			}
		}
	}
	for l := view; l != nil; l = l.Parent {
		// a file's variable of a block (x#3) or a loop's iteration object (#4) is not one of its names
		// (14/10/2026, block scope): the map is read as the source wrote it
		for name, v := range runtime.DeclaredNames(l.Scope) {
			if _, taken := scope[name]; taken || !visibleName(name) {
				continue
			}
			if x, ok := c.convert(v, 0); ok {
				scope[name] = x
			}
			if c.cycle != nil {
				return nil, nil, cycleFailure(view, name, c.cycle)
			}
		}
	}
	return scope, c, nil
}

// cycleFailure is the error of a page value that holds itself, at the view that gave it:
// "router.kitwork.js:4:22: dữ liệu trang cart: object tự tham chiếu (vòng ở self, quay về gốc)".
func cycleFailure(view *value.Lambda, name string, cycle *value.WalkError) error {
	place := "router.view"
	if view != nil && view.SourceFile != "" {
		place = fmt.Sprintf("%s:%d:%d", view.SourceFile, view.SourceLine, view.SourceColumn)
	}
	return fmt.Errorf("%s: dữ liệu trang %s: %s", place, name, cycle.Error())
}

// visibleName drops the bundler's own names (__kw_…) from what the page can read.
func visibleName(name string) bool { return name != "" && !strings.HasPrefix(name, "__") }

// uiConverter turns VM values into the plain values the ui package evaluates. A function becomes a
// helper the page can call ({{ money(total()) }}): it runs on the same VM, during this request.
//
// An object or array that holds itself has no plain form. The converter keeps the path it is on
// (value.Path) and stops at the first value met again on it: before, it went round to uiMaxDepth, and
// an object with two keys back to itself doubled at every level, 2⁶⁵ conversions for one request
// (14/10/2026). For page data that is the view's error (cycle); a helper's result keeps nil there.
//
// A function of the view the page calls ({{ money(x) }}) that fails gives the page nothing: a runtime
// error is not the page's to show. A failure nothing may catch (ENERGY_LIMIT, EXECUTION_CANCELLED…)
// is kept as fatal, and ends the request as a failed view does (serveUIView).
type uiConverter struct {
	vm    *runtime.VM
	path  value.Path
	cycle *value.WalkError
	fatal value.Value
}

const uiMaxDepth = 64

func (c *uiConverter) convert(v value.Value, depth int) (any, bool) {
	if depth > uiMaxDepth {
		return nil, false
	}
	if depth == 0 {
		c.path.Root(v)
	}
	switch v.K {
	case value.Nil:
		return nil, true
	case value.Bool, value.Number, value.String:
		return v.Interface(), true
	case value.Time:
		if t, ok := v.Interface().(time.Time); ok {
			return t.UTC().Format(time.RFC3339), true
		}
		return nil, true
	case value.Array:
		items := v.Array()
		out := make([]any, 0, len(items))
		for i, item := range items {
			if holds(item) && !c.enter(c.path.Index(item, i)) {
				out = append(out, nil)
				continue
			}
			x, _ := c.convert(item, depth+1)
			if holds(item) {
				c.path.Leave()
			}
			out = append(out, x)
		}
		return out, true
	case value.Map:
		fields := v.Map()
		out := make(map[string]any, len(fields))
		for name, field := range fields {
			if holds(field) && !c.enter(c.path.Key(field, name)) {
				continue
			}
			if x, ok := c.convert(field, depth+1); ok {
				out[name] = x
			}
			if holds(field) {
				c.path.Leave()
			}
		}
		return out, true
	case value.Func:
		lambda, ok := v.V.(*value.Lambda)
		if !ok || lambda == nil {
			return nil, false
		}
		return hydrate.Func(func(args []any) any {
			in := make([]value.Value, len(args))
			for i, a := range args {
				in[i] = value.New(a)
			}
			out := c.vm.ExecuteLambda(lambda, in)
			if out.K == value.Invalid {
				if diagnostic, ok := runtime.DiagnosticFrom(out); ok && diagnostic.Code != runtime.DiagnosticRuntimeError && c.fatal.ErrorVal == nil {
					c.fatal = out
				}
				return nil
			}
			x, _ := c.convert(out, 0)
			return x
		}), true
	}
	return nil, false // host objects (ctx, router, database…) are not page data
}

// holds reports whether v is an object or array: what the converter walks into.
func holds(v value.Value) bool { return v.K == value.Array || v.K == value.Map }

// enter is whether the converter walks into what c.path was just given; err is why not: a value met
// again on its own path (the first is the cycle) or one past value.MaxDepth.
func (c *uiConverter) enter(err *value.WalkError) bool {
	if err == nil {
		return true
	}
	if c.cycle == nil {
		c.cycle = err
	}
	return false
}
