package work

// Kitwork UI pages: a site whose router is router.work describes its interface in *.ui files. A folder's
// page is the answer of its router.get, context.view({ … }) (serveUIAnswer); router.colors and
// router.fonts declare the site's design tokens, and router.appearance the light/dark mode its pages show
// first. Each page is compiled once per generation by the ui package, its head with it: its stylesheets
// are files of their own, one per role (/_kitwork/reboot.<hash>.css, colors.<hash>.css, fonts.<hash>.css,
// icons.<hash>.css, page.<hash>.css, engine/ui/stylesheet); a request only runs the
// handler (the names of the object its view is given are what the page reads) and fills the prepared page
// with them.

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

	"github.com/kitwork/engine/jit/fonts"
	"github.com/kitwork/engine/jit/hydrate"
	"github.com/kitwork/engine/jit/icons"
	jitjs "github.com/kitwork/engine/jit/js"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/ui"
	"github.com/kitwork/engine/ui/stylesheet"
	"github.com/kitwork/engine/value"
)

// View is router.view, the door of a screen (28/10/2026, KITWORK_ROUTER_VIEW.md): its function runs each time
// the page is drawn and answers context.bind({ … }); the page reads exactly the names of that object, and a
// function in it is a job of the page, called through the bridge (view_call.go):
//
//	router.view((context) => context.title("Việc cần làm").bind({ todos, add }));
//
// It is the folder's GET; a POST naming a job (?kitwork-call=…) runs the same function again, then the job.
func (f *FolderRouter) View(args ...value.Value) *FolderMethod {
	m := f.declare("router.view", "GET", args...)
	m.view = true
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
	return f, nil
}

// Appearance is the light/dark mode a page shows before the visitor chooses one.
//
//	router.appearance("dark");
//
// "system" (the default) follows the device; "light" and "dark" fix it. Inherited down the folders
// like router.transition: a branch may set its own, "system" included. The visitor's own choice (the
// appearance component, kitwork.appearance) always wins over it: a .ui page writes the mode on <html> as
// data-kitwork-appearance only for the pre-paint and kitwork.appearance to fall back on when the visitor
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

func sortedNames(m map[string]value.Value) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
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

// systemSans and systemMono are the system stacks after a .ui page's typefaces, body and code: the
// faces every platform has, when the page's own is not there yet or not at all.
const (
	systemSans = "ui-sans-serif, system-ui, -apple-system, Segoe UI, Roboto, sans-serif"
	systemMono = "ui-monospace, SFMono-Regular, Menlo, monospace"
)

// quoteFamily quotes a family name that contains spaces, as CSS requires: Plus Jakarta Sans is
// only a valid font-family value written as "Plus Jakarta Sans".
func quoteFamily(name string) string {
	if strings.ContainsAny(name, " ") && !strings.HasPrefix(name, "'") && !strings.HasPrefix(name, "\"") {
		return "'" + name + "'"
	}
	return name
}

// fontStack is a role's font-family on a .ui page: its typeface (fontFamily), then the system stack
// of that role, as a template page's font-body and font-code are. A Google family is written as Google
// spells it, the name its @font-face carries, however router.fonts wrote it ("google sans code").
func fontStack(role, family string) string {
	fallback := systemSans
	if role == "code" {
		fallback = systemMono
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
	presentations map[string]*uiPresentation
	// weights are the font weights the site's pages use, and code whether any of them sets text in the
	// code face, gathered when every page compiles (compileAll), so the pages share one fonts file
	weights []int
	code    bool

	// stylesheets are the pages' stylesheets by their name under /_kitwork/ without the extension,
	// <role>.<hash> (reboot.<hash>, colors.<hash>, fonts.<hash>, icons.<hash>, page.<hash>):
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
	presentation *uiPresentation
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
// /_kitwork/fonts.<hash>.css, /_kitwork/icons.<hash>.css,
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
		presentations: map[string]*uiPresentation{},
		stylesheets:   map[string]pageStylesheet{},
		scripts:       map[string]engineScript{},
	}
}

// logWarnings prints, while developing, the warnings of a page just compiled (ui.Page.Warnings): each
// once per generation, "cảnh báo: file:line:col: message", so a view.ui every route shares says its
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
func routeLabel(relative string, kind string) string {
	route := "/" + strings.Trim(relative, "/")
	switch kind {
	case uiNotFoundPage:
		return "không tìm thấy ở " + route
	case uiErrorPage:
		return "trang lỗi ở " + route
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

// pageScriptRole is the role of a page's program, /_kitwork/page.<hash>.js (28/10/2026, ui/page_program.go).
const pageScriptRole = "page"

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

// setRoute records a route's colours and typefaces, and its presentation: the site's, with whether the
// route has a dark look (its colour chain declares a dark value, or its appearance is "dark"), so
// every page of the route pre-paints the appearance before anything shows.
func (u *uiSite) setRoute(relative string, node *RouteNode, site uiPresentation) {
	if u != nil {
		style := routeStyleOf(node)
		u.styles[relative] = style
		site.dark = style.err == nil && style.palette.Dark()
		u.presentations[relative] = &site
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
	for _, kind := range []string{uiOwnPage, uiNotFoundPage, uiErrorPage} {
		for _, relative := range routes {
			if kind == uiOwnPage && !u.hasPage(relative) {
				continue
			}
			if page := u.compile(relative, kind); page.err == nil && page.page != nil {
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
	href, css := u.dataIcons.file(strings.Join(set, " "), func() []byte { return compiled.presentation.iconsStylesheet(set) })
	if href == "" {
		return compiled.head
	}
	if compiled.iconsInline {
		return compiled.headBefore + inlineStyle("icons", css) + compiled.headAfter
	}
	return compiled.headBefore + stylesheetLink(href, "icons") + compiled.headAfter
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
// (the pre-paint, the scripts, its icons), then after the pre-paint the font preloads (first,
// so the faces are asked for before the browser reads any CSS: until the fix of 14/10/2026 they came last, after
// a page block that can be past the first window) and its stylesheets in the cascade's order —
// reboot, colors, fonts, icons, page —, before the scripts. How each part is delivered is the
// route's router.bundle (router_ui_bundle.go): by default ("auto") the page's own CSS (page, icons)
// is written in the page, and the site's (reboot, colors, fonts) too when the page then fits the
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
	built := presentation.head(page.Skeleton())
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
	for _, sheet := range built.Stylesheets {
		if part := (cssPart{role: sheet.Name, generated: sheet.Generated, css: []byte(sheet.CSS)}); sheet.Name == "icons" && len(part.css) > 0 {
			own = &part
		}
	}
	rules := cssPart{role: "page", generated: "css", css: page.Stylesheet().CSS}
	scripts := ""
	if page.UsesScrolled() {
		// the flag the scrolled: rules read, only for a page that can carry one; no kernel needed
		scripts = `<script data-kitwork-generated="scrolled" src="` + scrolledScript().path + `" defer></script>`
	}
	scripts = u.pageScripts(compiled.style.bundle.javascript, &built, scripts)
	if program := page.Script(); program != nil {
		// a page that runs in the kernel with a program (ui/page_program.go): page.<hash>.js, kept for good; it
		// registers the program under its id, so it may run before or after call.js
		scripts += `<script data-kitwork-generated="page" src="` + u.storeScript(pageScriptRole, program) + `" defer></script>`
	}
	// the font preloads first, so the browser asks for the faces before it reads the CSS (a page block
	// can be past the first window)
	preloads := fontPreloads(compiled.style, page.UsesCode())
	// "auto" weighs the head production sends: the warnings file below is development's only
	fits := func(links string) bool { return u.fitsInitialWindow(compiled, built.Insert(preloads+links+scripts)) }
	links, icons, iconsInline := u.pageCSS(compiled.style.bundle.css, site, own, rules, fits)
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

// The pages a route folder answers with: its own (content.ui), the nearest notfound.ui, the nearest
// error.ui (20/10/2026: error(…) in router.work draws it with 500, as notFound() draws notfound.ui).
const (
	uiOwnPage      = ""
	uiNotFoundPage = "notfound"
	uiErrorPage    = "error"
)

// page returns the compiled page of a route folder, or its not-found or error page, with its head. A
// page no route listed (compileAll) is compiled on first use the same way. A nil page with no error
// means the site has no notfound.ui (or error.ui) for that place.
func (u *uiSite) page(relative string, kind string) (*uiCompiled, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	compiled := u.compile(relative, kind)
	if compiled.err == nil && compiled.page != nil && compiled.head == "" {
		compiled.head = u.buildHead(compiled)
	}
	return compiled, compiled.err
}

// compile compiles a page once, without its head; u.mu is held.
func (u *uiSite) compile(relative string, kind string) *uiCompiled {
	key := relative
	if kind != uiOwnPage {
		key += "\x00" + kind
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
	case kind == uiNotFoundPage:
		compiled.page, compiled.err = u.siteFor(relative).CompileNotFound(u.dir(relative))
	case kind == uiErrorPage:
		compiled.page, compiled.err = u.siteFor(relative).CompileError(u.dir(relative))
	default:
		compiled.page, compiled.err = u.siteFor(relative).Compile(u.dir(relative))
	}
	u.logWarnings(compiled.page, routeLabel(relative, kind))
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
	return t.hasUIPage(n, uiNotFoundPage)
}

// hasUIPage reports whether this folder has a page of that kind to show, its own or the nearest above.
func (t *Tenant) hasUIPage(n *RouteNode, kind string) bool {
	plan := t.renderPlan()
	if n == nil || plan == nil || plan.ui == nil {
		return false
	}
	compiled, err := plan.ui.page(n.relPath(), kind)
	return compiled.page != nil || err != nil
}

// respondUI renders the Kitwork UI page of a folder (or its not-found page) into the response.
func (t *Tenant) respondUI(reqRouter *Router, node *RouteNode, scope map[string]any) {
	plan := t.renderPlan()
	if plan == nil || plan.ui == nil || node == nil {
		reqRouter.response.Text(value.New("Service Unavailable"), http.StatusServiceUnavailable)
		return
	}
	notFound := reqRouter.isNotfound
	kind := uiOwnPage
	status := http.StatusOK
	if notFound {
		kind = uiNotFoundPage
		status = http.StatusNotFound
	} else if reqRouter.response.uiPage == uiErrorPage {
		// error(…): the nearest error.ui with 500, reading the message the handler gave (answerUIPage)
		kind = uiErrorPage
		status = http.StatusInternalServerError
		scope = map[string]any{"message": reqRouter.response.uiMessage}
	} else if code := reqRouter.response.Code(); code >= 100 && code <= 999 {
		// the status the handler set with context.status(503) (14/10/2026: until then the page always
		// answered 200), so a handler that read a failure with safe(() => …) can say so
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
	}

	reqRouter.uiAnswered = true
	compiled, err := plan.ui.page(node.relPath(), kind)
	if err != nil {
		// file:line:column and what is wrong while developing; in production the status and the request id
		t.recordFailure(reqRouter, "page", err)
		reqRouter.fail(err, http.StatusInternalServerError)
		return
	}
	if compiled.page == nil {
		if kind == uiErrorPage {
			reqRouter.response.Text(value.New(errorText(reqRouter.response.uiMessage)), http.StatusInternalServerError)
			return
		}
		reqRouter.response.Text(value.New("Not Found"), http.StatusNotFound)
		return
	}

	r := reqRouter.request
	request := ui.Request{
		Path:        r.URL.Path,
		Origin:      requestOrigin(r),
		Scope:       scope,
		Method:      r.Method,
		Title:       metaText(meta, "title"),
		Description: metaText(meta, "description"),
		Language:    metaText(meta, "language"),
		Transition:  reqRouter.transition,
		Appearance:  routeAppearance(node),
		Favicon:     folderFavicon(node),
	}
	if reqRouter.viewSnapshot != nil && compiled.page != nil && compiled.page.Live() {
		snapshot := reqRouter.viewSnapshot()
		request.Revs = viewRevs(snapshot)     // the revisions of its data, for its calls (view_patch.go)
		t.keepViewRows(node.folder, snapshot) // its lists as drawn, for a refresh by row (view_rows.go)
		request.Stamp = reqRouter.viewStamp   // the moment its data was read (view_call.go)
		if node.folder != nil {
			request.Tickets = t.viewTickets(reqRouter, node.folder) // what it is told of (view_changes.go)
		}
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
	// each component of the site the page draws runs for this request (component_call.go)
	components := newComponentRenderer(t, reqRouter)
	request.Component = components.run
	document := compiled.page.Document(request)
	if err := components.failed(); err != nil {
		t.recordFailure(reqRouter, "component", err)
		reqRouter.fail(err, http.StatusInternalServerError)
		return
	}
	reqRouter.response.HTML(value.New(document), status)
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
	return requestScheme(r) + "://" + r.Host
}

// cycleFailure is the error of a page value that holds itself, at the handler that gave it:
// "router.work:4:12: dữ liệu trang cart: object tự tham chiếu (vòng ở self, quay về gốc)".
func cycleFailure(handler *value.Lambda, name string, cycle *value.WalkError) error {
	place := "context.view"
	if handler != nil && handler.SourceFile != "" {
		place = fmt.Sprintf("%s:%d:%d", handler.SourceFile, handler.SourceLine, handler.SourceColumn)
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
// is kept as fatal, and ends the request as a failed handler does (serveUIAnswer).
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
				if diagnostic, ok := runtime.DiagnosticFrom(out); ok && diagnostic.Code != runtime.DiagnosticRuntimeError && !c.fatal.Failed() {
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
