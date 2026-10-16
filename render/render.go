package render

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	jitcss "github.com/kitwork/engine/jit/css"
	fonts "github.com/kitwork/engine/jit/fonts"
	jithighlight "github.com/kitwork/engine/jit/highlight"
	hydrate "github.com/kitwork/engine/jit/hydrate"
	icons "github.com/kitwork/engine/jit/icons"
	jitjs "github.com/kitwork/engine/jit/js"
	logo "github.com/kitwork/engine/jit/logo"
	jitmanifest "github.com/kitwork/engine/jit/manifest"
	material "github.com/kitwork/engine/jit/material"
	theme "github.com/kitwork/engine/jit/theme"
	"github.com/kitwork/engine/utilities/minifier"
	"github.com/kitwork/engine/value"
)

// Config is everything the render engine needs — no *Tenant, no HTTP. Build a render with New(),
// then Bind(data) for a page or HTML(tmpl, data) for a raw string template (e.g. an email).
type Config struct {
	Base             string            // template root — the anchor every path resolves against
	JitConfig        *jitcss.Config    // JIT-CSS config (brand colors, keyframes…); nil = defaults
	Directory        string            // sub-root under Base (tree uses "."; legacy used "views"/"app")
	Path             string            // the folder whose page/index/slots resolve, walked up
	Page             string            // explicit page override (usually "" — derived from Path)
	Index            string            // explicit shell filename override
	Notfound         string            // notfound filename (default "notfound")
	NotfoundMode     bool              // render the notfound page for {{ @page }}
	JitCSS           bool              // inline the minimal JIT CSS for the page's classes
	Global           value.Value       // data merged into every render
	Minify           []string          // explicit minify content types
	MinifySet        bool              // whether Minify was set explicitly
	DefaultMinify    bool              // minify when not set explicitly (caller passes !AllowLocal)
	ThemeMode        string            // appearance pre-paint: "" = auto-scan, "force" = always, "off" = never
	Drive            bool              // router.reload(false): Drive on every page — off by default
	HighlightTheme   string            // folder-scoped default for {{ src.highlight() }} (router.highlight)
	ManifestPath     string            // where router.manifest() publishes, for the <head> link
	HighlightPalette map[string]string // per-role overrides from router.highlight({ role: … })
	SiteComponents   jitjs.Site        // the tenant's own _components/<name>.js, frozen at generation
	Source           Source            // immutable template source; nil reads the live filesystem
	// ExpressionFailed hears a {{ expression }} whose value is a failure (a method that is not a
	// function, a value an error left), with the expression as the engine reads it and the failure's
	// message, and the page writes nothing in its place. nil writes the message where the value would
	// stand. The engine passes nil while developing (AllowLocal) and, in production, a hook that logs
	// it: a visitor never reads an engine's message in a page (14/10/2026), as a 500 shows them only
	// the status and the request id. It hears a {{ layout "name" }} whose file cannot be read the same
	// way, and with it set a page with neither its page nor a not-found page carries no <!-- 404: path -->
	// comment, which names a path of the server (16/10/2026).
	ExpressionFailed func(expression, message string)
}

func New(c Config) *Render {
	return &Render{
		base: c.Base, jitConfig: c.JitConfig, directory: c.Directory, path: c.Path,
		page: c.Page, index: c.Index, notfound: c.Notfound, notfoundMode: c.NotfoundMode,
		jitCSS: c.JitCSS, global: c.Global, minify: c.Minify, minifySet: c.MinifySet,
		defaultMinify: c.DefaultMinify, themeMode: c.ThemeMode, drive: c.Drive,
		highlightTheme:   c.HighlightTheme,
		manifestPath:     c.ManifestPath,
		highlightPalette: c.HighlightPalette,
		siteComponents:   c.SiteComponents,
		source:           c.Source,
		expressionFailed: c.ExpressionFailed,
	}
}

type Render struct {
	base                 string         // template root — the anchor every path resolves against
	jitConfig            *jitcss.Config // JIT-CSS config; nil = defaults
	directory            string
	path                 string // Thư mục gốc, ví dụ: /pages/home
	page                 string // Thư mục trang con, ví dụ: contact/profile
	index                string // File shell chính, mặc định là index
	layout               Layout
	global               value.Value // Dữ liệu dùng chung cho mọi bản render
	notfound             string
	notfoundMode         bool              // render the notfound page for {{ @page }}
	jitCSS               bool              // inject server-side Tailwind/utility CSS for the page's classes
	minify               []string          // content types to minify on the final HTML output
	minifySet            bool              // whether minify was set explicitly (else default by environment)
	defaultMinify        bool              // minify default when not explicit (injected — replaces AllowLocal)
	themeMode            string            // appearance pre-paint mode (see Config.ThemeMode)
	drive                bool              // Kitwork Drive, opt-in per site (see Config.Drive)
	highlightTheme       string            // folder-scoped default theme for {{ src.highlight() }}
	manifestPath         string            // router.manifest() output path, linked from <head>
	highlightPalette     map[string]string // per-role class overrides for the highlight pass
	siteComponents       jitjs.Site
	source               Source                           // immutable generation snapshot; nil = live filesystem
	expressionFailed     func(expression, message string) // see Config.ExpressionFailed
	program              *node
	prepareError         string
	templateFailed       bool   // prepareError is the template's own text failing, not a missing shell
	missingPage          string // a {{ @page }} with neither its page nor a not-found page: the page looked for (NoPageError)
	presentationPrepared bool
	minifyPrepared       bool
}

type Layout struct {
	header   string
	navbar   string
	footer   string
	head     string
	sidebar  string
	tabbar   string
	subbar   string
	toolbar  string
	titlebar string
}

func (r *Render) New(dir ...string) *Render {
	newRender := *r
	if len(dir) > 0 && dir[0] != "" {
		newRender.directory = dir[0]
	}
	return &newRender
}

// resolve joins paths against the render's base directory — the decoupled replacement for the
// tenant's path resolver, so the engine needs no *Tenant to locate template files.
func (r *Render) resolve(paths ...string) string {
	return filepath.Join(append([]string{r.base}, paths...)...)
}

func (r *Render) readFile(filename string) ([]byte, error) {
	if r.source != nil {
		return r.source.ReadFile(filename)
	}
	return os.ReadFile(filename)
}

func (r *Render) fileExists(filename string) bool {
	if r.source != nil {
		return r.source.Exists(filename)
	}
	return fileExists(filename)
}

func (r *Render) shouldMinify() bool {
	if r.minifySet {
		return len(r.minify) > 0
	}
	return r.defaultMinify
}

func (r *Render) getIndexPath() string {
	// Explicit index override: keep the old direct-file / directory behavior.
	if r.index != "" {
		path1 := r.pathJoin(r.path, r.index, r.getfile("index"))
		if r.fileExists(path1) {
			return path1
		}
		return r.pathJoin(r.path, r.getfile(r.index))
	}

	// NESTED SHELL: walk UP from the page's folder to the nearest index.kitwork.html.
	// e.g. page /docs/routing → app/docs/routing/index → app/docs/index (found) →
	// app/index. A section gets its own shell just by having its own index file.
	// (A few os.Stat — cheap; the template read+parse below dominates cost anyway.)
	folder := path.Join("/", r.path, r.page)
	for {
		candidate := r.pathJoin(folder, r.getfile("index"))
		if r.fileExists(candidate) {
			return candidate
		}
		if folder == "/" || folder == "." || folder == "" {
			break
		}
		folder = path.Dir(folder)
	}
	return r.pathJoin("", r.getfile("index")) // root <dir>/index.kitwork.html
}

func (r *Render) getPagePath() string {
	// Kết quả: path + page_name + page.kitwork.html
	return r.pathJoin(r.path, r.page, r.getfile("page"))
}

func (r *Render) getfile(name string) string {

	if filepath.Ext(name) == "" {
		return name + ".kitwork.html"
	}
	return name
}

func (r *Render) getNotFoundPath() string {
	name := r.notfound
	if name == "" {
		name = "notfound"
	}

	// Explicit absolute path (e.g. .notfound("/errors/404")): resolve from the render root only,
	// no walk-up — the caller pinned it deliberately.
	if strings.HasPrefix(name, "/") {
		name = strings.TrimPrefix(name, "/")
		if p := r.pathJoin("", name, r.getfile("index")); r.fileExists(p) {
			return p // directory form: <name>/index.kitwork.html
		}
		return r.pathJoin("", r.getfile(name)) // direct file: <name>.kitwork.html
	}

	// Otherwise: walk UP from the page's folder to the NEAREST notfound — the same nested
	// resolution the shell (index) uses. So /docs/routing falls back to docs/notfound, then the
	// root notfound. No declaration needed; .notfound("name") only changes the filename to look for.
	folder := path.Join("/", r.path, r.page)
	for {
		if p := r.pathJoin(folder, r.getfile(name)); r.fileExists(p) {
			return p // direct file: <folder>/notfound.kitwork.html
		}
		if p := r.pathJoin(folder, name, r.getfile("index")); r.fileExists(p) {
			return p // directory form: <folder>/notfound/index.kitwork.html
		}
		if folder == "/" || folder == "." || folder == "" {
			break
		}
		folder = path.Dir(folder)
	}
	return r.pathJoin("", r.getfile(name)) // root fallback: <root>/notfound.kitwork.html
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

func (r *Render) pathJoin(vals ...string) string {
	path := path.Join(vals...)
	return r.resolve(r.dir(), path)
}

func (r *Render) dir() string {
	if r.directory == "" {
		r.directory = "views"
	}
	return r.directory
}

// tmpl renders data; failed says the template's own text did not prepare, and the text is then why;
// noShell says no shell (index.kitwork.html) stands above the page to put it in, and the text then
// names the shell looked for (NoShellError); missingPage names the page a {{ @page }} looked for when
// neither it nor a not-found page is there, and the page renders with nothing in its place (NoPageError).
func (r *Render) tmpl(data any) (rendered string, failed bool, noShell bool, missingPage string) {
	// 1. GIAI ĐOẠN ASSEMBLY: Ráp nối các file mẫu thành một template lớn duy nhất
	// Bắt đầu từ file Shell (index.html)
	program := r.program
	prepareError := r.prepareError
	templateFailed := r.templateFailed
	missingPage = r.missingPage
	presentationPrepared := r.presentationPrepared
	minifyPrepared := r.minifyPrepared
	if program == nil && prepareError == "" {
		program, presentationPrepared, minifyPrepared, prepareError, templateFailed, missingPage = r.compileTemplate(false)
	}
	if prepareError != "" {
		// the one failure of compileTemplate that is not the template's own text is a shell it could not read
		return prepareError, templateFailed, !templateFailed, ""
	}

	// Đệ quy nạp các thành phần lồng nhau (layouts, includes, page)

	// 2. GIAI ĐOẠN BIND: Render dữ liệu vào các biến
	scope := make(map[string]value.Value)
	if r.source != nil {
		scope["__template_source"] = value.New(r.source)
	}

	// A. Nạp dữ liệu Global (Nếu có)
	if !r.global.IsBlank() && r.global.IsMap() {
		for k, v := range r.global.Map() {
			scope[k] = v
		}
	}

	// B. Nạp dữ liệu cụ thể của Request ($)
	valData := value.New(data)
	scope["$"] = valData
	if valData.IsMap() {
		for k, v := range valData.Map() {
			scope[k] = v // Ghi đè Global nếu trùng key
		}
	}

	// Folder-scoped default for a bare {{ src.highlight() }}. A page/site may still
	// override per call with {{ src.highlight("mono") }}; unset falls back to the
	// engine default palette. It rides the scope, so it stays per-render/tenant.
	if r.highlightTheme != "" {
		scope["$highlight"] = value.New(r.highlightTheme)
	}

	// Parse và Eval một lần duy nhất cho toàn bộ cây mẫu
	out := evalWith(program, data, scope, r.expressionFailed)
	if !presentationPrepared {
		out = r.applyStaticPresentation(out)
	}

	if r.shouldMinify() {
		if !minifyPrepared {
			out = minifier.HTML(out)
			if r.themeMode != "off" {
				out = theme.Canonicalize(out)
			}
		}
	}
	return out, false, false, missingPage
}

// applyStaticPresentation performs source-driven JIT work. Prepared renders run
// it once while building the immutable generation; live or dynamic-attribute
// renders retain the exact request-time behavior.
func (r *Render) applyStaticPresentation(out string) string {
	// 2. JIT highlight: fill author-declared code slots
	// (<code data-kitwork-highlight="go">). It MUST run before the JIT CSS pass:
	// it introduces color classes, and that pass generates CSS for exactly the
	// classes present in the document. A page with no slot costs one substring
	// search. router.highlight("tokyo-night") picks the palette.
	out = jithighlight.Render(out, r.highlightTheme, r.highlightPalette)

	// Link the web app manifest. Unlike every other pass this one reaches into
	// <head>, because a manifest with no <link> does nothing — an author-placed
	// <link data-kitwork-generated="manifest"> is filled in place, otherwise the link
	// is injected. A site that declared no manifest is untouched.
	out = jitmanifest.Render(out, r.manifestPath)

	// Name the engine in <head>: <meta name="generator" content="Kitwork …"> (generator.go).
	out = withGenerator(out)

	// The bar between pages: a site that moves without a reload gets the progressbar component on every
	// page that has none (progressbar.go). Before the CSS pass, which styles its classes, and before
	// the JavaScript pass, which delivers its component.
	if r.drive {
		out = withProgressbar(out)
		// and the reader that says the new page's title to a screen reader (reader.go)
		out = withReader(out)
	}

	// 3. JIT CSS (opt-in via .jit()): sinh CSS tối thiểu cho đúng các class trang dùng
	// (Tailwind + hệ industrial), nhét <style> trước </head>. Thay CDN client-side;
	// cache theo tập class nên gần như miễn phí sau lần đầu.
	if r.jitCSS {
		if css := jitcss.GenerateJITCached(out, r.jitConfig); css != "" {
			// Prepend @keyframes + :root vars + reduced-motion for ONLY the animations the page
			// actually uses (jit animate emit-only-used). No-op when nothing animates.
			if kf := jitcss.UsedKeyframes(css, r.jitConfig); kf != "" {
				css = kf + "\n" + css
			}
			style := "<style data-kitwork-generated=\"css\">\n" + css + "</style>"
			if i := strings.LastIndex(out, "</head>"); i >= 0 {
				out = out[:i] + style + out[i:]
			} else {
				out = style + out
			}
		}
	}

	// 3d. JIT material: inject <style data-kitwork-generated="material"> with CSS for ONLY the UI
	// material families (.button/.btn, .card, …) the page uses (jit/material). No-op otherwise.
	out = material.Render(out)

	// 3c. JIT icons: scan for `<i class="icon-x">` (and the icons a data-kitwork-class toggles in) and
	// inject a per-page <style data-kitwork-generated="icons"> with CSS-mask rules for ONLY the icons
	// used (jit/icons) — a cheap no-op when none are present. A Kitwork UI page links that CSS as
	// /_kitwork/icons.<hash>.css instead (PresentHead). No Font Awesome, no CDN, no payload; there is
	// no shared icon stylesheet (the /jiticons route of router.icons() went on 09/07/2026).
	out = icons.Render(out)

	// 3f. JIT logos: brand logos (Simple Icons) via <i class="logo-x"> — inline like icons (jit/logo),
	// /_kitwork/logo.<hash>.css on a Kitwork UI page; the /jitlogo route of router.logo() is gone.
	out = logo.Render(out)

	// 3e. JavaScript for kernel pages: the kernel, Drive when the site asked for it, and one file per
	// component (data-kit-component, or the engine's data-kitwork-component) and directive file the
	// page uses — each named by its content, so each is cached once and for good (jit/js
	// delivery.go). A page that uses no grammar — neither data-kit-* nor the eight grouped
	// data-kitwork-* directives — gets nothing.
	out = jitjs.Deliver(out, r.siteComponents, jitjs.Options{Drive: r.drive})

	// 3h. hydrate: verify every authored expression (compile-time linting, printed while developing).
	// It reads the page and never changes it; the wire ships the SOURCE and the client parses it
	// (no eval).
	hydrate.Check(out, jitjs.ComponentNames(r.siteComponents))
	// Drive is a site-wide choice (router.reload(false), the default of a Kitwork UI site): the page
	// carries Drive's file only when the site asked for it, so the file being there is the answer and
	// no flag is written for it.

	// 3g. JIT fonts (jitfonts): self-hosted Google Fonts. Scan for the font FAMILIES the page uses
	// (a `font-family: <Name>` value or a `font-<slug>` class) → inject preload links + ONE
	// <style data-kitwork-generated="fonts"> with @font-face (subset woff2 served from /_kitwork/fonts/) for ONLY
	// those families. No Google at runtime, no third-party CDN; a cheap no-op when none are used.
	out = fonts.Render(out)

	// 3i. Appearance pre-paint (jit/theme): swap <script data-kitwork-generated="appearance"> (or the
	// older "theme" marker) for a synchronous pre-paint that applies the saved or system light/dark
	// choice before first paint (no flash). Pairs with the kit.appearance service (kit.theme is the same
	// object) that the appearance and theme components read.
	switch r.themeMode {
	case "off": // router.themes({ prepaint: false }) — no pre-paint even when the scan would find usage
	case "force":
		out = theme.Force(out) // the site declared its dark (css darkMode / themes) — always inject, scan or no scan
	default:
		out = theme.Render(out)
	}

	return out
}

// assemble thực hiện quét template và nạp các thành phần thô một cách đệ quy
func (r *Render) compileTemplate(preparePresentation bool) (
	program *node,
	presentationPrepared bool,
	minifyPrepared bool,
	prepareError string,
	templateFailed bool,
	missingPage string,
) {
	defer func() {
		if recovered := recover(); recovered != nil {
			program = nil
			presentationPrepared = false
			minifyPrepared = false
			prepareError = fmt.Sprintf("template parse error: %v", recovered)
			templateFailed = true
			missingPage = ""
		}
	}()
	indexPath := r.getIndexPath()
	shellRaw, err := r.readFile(indexPath)
	if err != nil {
		// no shell: not the template's own text (templateFailed false), and the text names the shell looked
		// for, an absolute path on the server (NoShellError)
		return nil, false, false, fmt.Sprintf("[Error reading index: %v]", indexPath), false, ""
	}
	var notes assembly
	fullTemplate := r.assemble(string(shellRaw), indexPath, 0, &notes)
	if notes.failure != "" {
		return nil, false, false, notes.failure, true, ""
	}
	if preparePresentation && !hasDynamicPresentation(fullTemplate) {
		fullTemplate = r.applyStaticPresentation(fullTemplate)
		presentationPrepared = true
		if r.shouldMinify() {
			if minified, ok := minifier.TemplateHTML(fullTemplate); ok {
				fullTemplate = minified
				if r.themeMode != "off" {
					fullTemplate = theme.Canonicalize(fullTemplate)
				}
				minifyPrepared = true
			}
		}
	}
	return parse(specializeTokens(fullTemplate)), presentationPrepared, minifyPrepared, "", false, notes.missingPage
}

// Prepare assembles and parses this render path once. The returned Render is
// immutable and safe for concurrent Bind calls.
func (r *Render) Prepare() *Render {
	if r == nil {
		return nil
	}
	prepared := *r
	prepared.program, prepared.presentationPrepared, prepared.minifyPrepared, prepared.prepareError,
		prepared.templateFailed, prepared.missingPage = prepared.compileTemplate(true)
	return &prepared
}

// Present finishes a whole HTML document built elsewhere exactly like a template page: CSS for the
// classes it uses, icons, fonts, delivered scripts, appearance pre-paint, then minification when the site
// minifies. Nothing in the engine calls it: a Kitwork UI page builds its head once from its skeleton
// (PresentHead) and links its own stylesheets (engine/ui/stylesheet), with none of the template
// passes' CSS. It stays as the control two tests compare with: what these passes would make of the same
// document (TestUIPageRunsNoPassPerRequest, TestPresentHeadTakesTheStylesheetsOut).
func (r *Render) Present(document string) string {
	if r == nil {
		return document
	}
	out := r.applyStaticPresentation(document)
	if r.shouldMinify() {
		out = minifier.HTML(out)
		if r.themeMode != "off" {
			out = theme.Canonicalize(out)
		}
	}
	return out
}

// PresentationPrepared reports whether source-driven presentation work was
// frozen into this immutable render generation.
func (r *Render) PresentationPrepared() bool {
	return r != nil && r.presentationPrepared
}

func hasDynamicKitJSAttribute(template string) bool {
	_, err := kitJSStaticScanSource(template)
	return err != nil
}

func kitAttributeName(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return strings.HasPrefix(name, "data-kit-") || strings.HasPrefix(name, "data-kitwork-")
}

func htmlTemplateSpace(char byte) bool {
	return char == ' ' || char == '\t' || char == '\r' || char == '\n' || char == '\f'
}

type kitJSTemplateTokenKind uint8

const (
	kitJSTemplateOutput kitJSTemplateTokenKind = iota
	kitJSTemplateControl
	kitJSTemplateFragment
)

type kitJSTemplateHTMLMode uint8

const (
	kitJSHTMLData kitJSTemplateHTMLMode = iota
	kitJSHTMLTagOpen
	kitJSHTMLTagName
	kitJSHTMLBeforeAttribute
	kitJSHTMLAttributeName
	kitJSHTMLAfterAttributeName
	kitJSHTMLBeforeAttributeValue
	kitJSHTMLUnquotedAttributeValue
	kitJSHTMLQuotedAttributeValue
	kitJSHTMLDeclaration
	kitJSHTMLComment
	kitJSHTMLRawText
	kitJSHTMLRawCandidate
)

type kitJSTemplateHTMLContext struct {
	mode         kitJSTemplateHTMLMode
	tagName      string
	attribute    string
	quote        byte
	closing      bool
	selfClosing  bool
	rawName      string
	rawCandidate string
	declaration  string
	commentTail  string
}

func classifyKitJSTemplateToken(content string) kitJSTemplateTokenKind {
	content = strings.TrimSpace(content)
	if content == "" {
		return kitJSTemplateControl
	}
	fields := strings.Fields(content)
	command := fields[0]
	switch command {
	case "if", "else", "elseif", "end", "for", "let", "capture":
		return kitJSTemplateControl
	case "layout":
		return kitJSTemplateFragment
	}
	if strings.HasPrefix(command, "@") || kitJSTemplateRawCall(content) {
		return kitJSTemplateFragment
	}
	return kitJSTemplateOutput
}

func kitJSTemplateRawCall(content string) bool {
	if !strings.HasPrefix(content, "raw") {
		return false
	}
	rest := content[len("raw"):]
	if rest != "" && (rest[0] == '_' || rest[0] >= 'a' && rest[0] <= 'z' || rest[0] >= 'A' && rest[0] <= 'Z' || rest[0] >= '0' && rest[0] <= '9') {
		return false
	}
	rest = strings.TrimLeftFunc(rest, func(char rune) bool {
		return char == ' ' || char == '\t' || char == '\r' || char == '\n' || char == '\f'
	})
	return strings.HasPrefix(rest, "(")
}

func (context *kitJSTemplateHTMLContext) rejectToken(kind kitJSTemplateTokenKind) string {
	if kind == kitJSTemplateFragment {
		return "raw or deferred template fragments can synthesize elements and attributes"
	}
	switch context.mode {
	case kitJSHTMLData, kitJSHTMLRawText:
		return ""
	case kitJSHTMLBeforeAttribute:
		if kind == kitJSTemplateControl {
			return ""
		}
		return "template output in a start tag can synthesize attributes"
	case kitJSHTMLQuotedAttributeValue:
		if !kitAttributeName(context.attribute) {
			return ""
		}
		return "template tokens cannot change a data-kit-* or data-kitwork-* attribute value"
	case kitJSHTMLComment:
		return "template tokens inside HTML comments can change the scanner boundary"
	case kitJSHTMLRawCandidate:
		return "template tokens can synthesize a raw-text closing tag"
	default:
		return "template tokens cannot synthesize tag names, attribute names, or unquoted attribute fragments"
	}
}

func (context *kitJSTemplateHTMLContext) advance(source string) {
	for index := 0; index < len(source); index++ {
		context.advanceByte(source[index])
	}
}

func (context *kitJSTemplateHTMLContext) advanceGap(length int) {
	for index := 0; index < length; index++ {
		context.advanceByte(' ')
	}
}

func (context *kitJSTemplateHTMLContext) advanceByte(char byte) {
	switch context.mode {
	case kitJSHTMLData:
		if char == '<' {
			context.mode = kitJSHTMLTagOpen
			context.tagName = ""
			context.attribute = ""
			context.closing = false
			context.selfClosing = false
		}
	case kitJSHTMLTagOpen:
		switch {
		case char == '!':
			context.mode = kitJSHTMLDeclaration
			context.declaration = "!"
		case char == '?':
			context.mode = kitJSHTMLDeclaration
			context.declaration = "?"
		case char == '/':
			context.closing = true
			context.mode = kitJSHTMLTagName
		case htmlTemplateTagNameStart(char):
			context.tagName = string(htmlTemplateLower(char))
			context.mode = kitJSHTMLTagName
		default:
			context.mode = kitJSHTMLData
		}
	case kitJSHTMLTagName:
		switch {
		case htmlTemplateTagNamePart(char):
			context.tagName += string(htmlTemplateLower(char))
		case htmlTemplateSpace(char):
			if context.closing {
				context.mode = kitJSHTMLDeclaration
			} else {
				context.mode = kitJSHTMLBeforeAttribute
			}
		case char == '>':
			context.finishTag()
		case char == '/':
			context.selfClosing = true
			context.mode = kitJSHTMLBeforeAttribute
		default:
			context.mode = kitJSHTMLBeforeAttribute
			context.advanceByte(char)
		}
	case kitJSHTMLBeforeAttribute:
		switch {
		case htmlTemplateSpace(char):
		case char == '>':
			context.finishTag()
		case char == '/':
			context.selfClosing = true
		default:
			context.attribute = string(htmlTemplateLower(char))
			context.mode = kitJSHTMLAttributeName
		}
	case kitJSHTMLAttributeName:
		switch {
		case htmlTemplateSpace(char):
			context.mode = kitJSHTMLAfterAttributeName
		case char == '=':
			context.mode = kitJSHTMLBeforeAttributeValue
		case char == '>':
			context.finishTag()
		case char == '/':
			context.attribute = ""
			context.selfClosing = true
			context.mode = kitJSHTMLBeforeAttribute
		default:
			context.attribute += string(htmlTemplateLower(char))
		}
	case kitJSHTMLAfterAttributeName:
		switch {
		case htmlTemplateSpace(char):
		case char == '=':
			context.mode = kitJSHTMLBeforeAttributeValue
		case char == '>':
			context.finishTag()
		case char == '/':
			context.attribute = ""
			context.selfClosing = true
			context.mode = kitJSHTMLBeforeAttribute
		default:
			context.attribute = string(htmlTemplateLower(char))
			context.mode = kitJSHTMLAttributeName
		}
	case kitJSHTMLBeforeAttributeValue:
		switch {
		case htmlTemplateSpace(char):
		case char == '"' || char == '\'':
			context.quote = char
			context.mode = kitJSHTMLQuotedAttributeValue
		case char == '>':
			context.finishTag()
		default:
			context.mode = kitJSHTMLUnquotedAttributeValue
		}
	case kitJSHTMLUnquotedAttributeValue:
		switch {
		case htmlTemplateSpace(char):
			context.attribute = ""
			context.mode = kitJSHTMLBeforeAttribute
		case char == '>':
			context.finishTag()
		}
	case kitJSHTMLQuotedAttributeValue:
		if char == context.quote {
			context.quote = 0
			context.attribute = ""
			context.mode = kitJSHTMLBeforeAttribute
		}
	case kitJSHTMLDeclaration:
		if context.declaration == "!" && char == '-' {
			context.declaration = "!-"
		} else if context.declaration == "!-" && char == '-' {
			context.declaration = "!--"
			context.commentTail = ""
			context.mode = kitJSHTMLComment
		} else if char == '>' {
			context.mode = kitJSHTMLData
		} else {
			context.declaration = "?"
		}
	case kitJSHTMLComment:
		context.commentTail += string(char)
		if len(context.commentTail) > 3 {
			context.commentTail = context.commentTail[len(context.commentTail)-3:]
		}
		if context.commentTail == "-->" {
			context.commentTail = ""
			context.mode = kitJSHTMLData
		}
	case kitJSHTMLRawText:
		if char == '<' {
			context.rawCandidate = "<"
			context.mode = kitJSHTMLRawCandidate
		}
	case kitJSHTMLRawCandidate:
		if char == '<' {
			context.rawCandidate = "<"
			return
		}
		context.rawCandidate += string(htmlTemplateLower(char))
		if char == '>' {
			if rawTemplateClose(context.rawCandidate, context.rawName) {
				context.rawName = ""
				context.mode = kitJSHTMLData
			} else {
				context.mode = kitJSHTMLRawText
			}
			context.rawCandidate = ""
		} else if len(context.rawCandidate) > 64 {
			context.rawCandidate = ""
			context.mode = kitJSHTMLRawText
		}
	}
}

func (context *kitJSTemplateHTMLContext) finishTag() {
	if !context.closing && !context.selfClosing && rawTemplateElement(context.tagName) {
		context.rawName = context.tagName
		context.mode = kitJSHTMLRawText
	} else {
		context.mode = kitJSHTMLData
	}
	context.tagName = ""
	context.attribute = ""
	context.quote = 0
	context.closing = false
	context.selfClosing = false
}

func rawTemplateElement(name string) bool {
	switch name {
	case "script", "style", "textarea", "title", "xmp", "iframe", "noembed", "noframes", "plaintext":
		return true
	default:
		return false
	}
}

func rawTemplateClose(candidate, name string) bool {
	candidate = strings.TrimSpace(candidate)
	if len(candidate) < 4 || !strings.HasPrefix(candidate, "</") || candidate[len(candidate)-1] != '>' {
		return false
	}
	inside := strings.TrimSpace(candidate[2 : len(candidate)-1])
	fields := strings.Fields(inside)
	return len(fields) > 0 && strings.EqualFold(fields[0], name)
}

func htmlTemplateTagNameStart(char byte) bool {
	return char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z'
}

func htmlTemplateTagNamePart(char byte) bool {
	return htmlTemplateTagNameStart(char) || char >= '0' && char <= '9' || char == '-' || char == ':'
}

func htmlTemplateLower(char byte) byte {
	if char >= 'A' && char <= 'Z' {
		return char + ('a' - 'A')
	}
	return char
}

// kitJSStaticScanSource removes Kitwork template control/interpolation tokens
// before the HTML scanner runs. The assembled template remains the sole module
// authority: conditional branches remain as a safe static superset, while
// tokens that could synthesize tags, attributes, or reserved values fail the
// generation scan instead of being mistaken for authored static HTML.
func kitJSStaticScanSource(template string) ([]byte, error) {
	var output strings.Builder
	output.Grow(len(template))
	context := kitJSTemplateHTMLContext{mode: kitJSHTMLData}
	for offset := 0; offset < len(template); {
		relative := strings.Index(template[offset:], "{{")
		if relative < 0 {
			remainder := template[offset:]
			output.WriteString(remainder)
			context.advance(remainder)
			break
		}
		start := offset + relative
		static := template[offset:start]
		output.WriteString(static)
		context.advance(static)
		closeRelative := strings.Index(template[start+2:], "}}")
		if closeRelative < 0 {
			return nil, fmt.Errorf("unterminated template token at byte %d", start)
		}
		end := start + 2 + closeRelative + 2
		content := template[start+2 : end-2]
		if reason := context.rejectToken(classifyKitJSTemplateToken(content)); reason != "" {
			return nil, fmt.Errorf("unsafe template token at byte %d: %s", start, reason)
		}
		for index := start; index < end; index++ {
			output.WriteByte(' ')
		}
		context.advanceGap(end - start)
		offset = end
	}
	return []byte(output.String()), nil
}

// hasDynamicPresentation catches server template expressions that can change
// authored attributes or style declarations. Those renders keep the legacy
// request-time scan so a data-driven class/font/action is never omitted.
func hasDynamicPresentation(template string) bool {
	for offset := 0; offset < len(template); {
		relative := strings.Index(template[offset:], "{{")
		if relative < 0 {
			return false
		}
		index := offset + relative
		before := template[:index]
		open := strings.LastIndex(before, "<")
		if open > strings.LastIndex(before, ">") {
			segment := strings.TrimSpace(before[open+1:])
			equal := strings.LastIndex(segment, "=")
			if equal < 0 {
				return true
			}
			nameEnd := equal
			for nameEnd > 0 && (segment[nameEnd-1] == ' ' || segment[nameEnd-1] == '\t' ||
				segment[nameEnd-1] == '\r' || segment[nameEnd-1] == '\n') {
				nameEnd--
			}
			nameStart := nameEnd
			for nameStart > 0 && isAttributeNameByte(segment[nameStart-1]) {
				nameStart--
			}
			name := strings.ToLower(segment[nameStart:nameEnd])
			if name == "class" || name == "style" ||
				strings.HasPrefix(name, "data-kit-") ||
				strings.HasPrefix(name, "data-kitwork-") {
				return true
			}
		}
		lower := strings.ToLower(before)
		if strings.LastIndex(lower, "<style") > strings.LastIndex(lower, "</style>") {
			return true
		}
		// An expression inside a highlight slot is code the page binds — a
		// component catalogue printing its own demo. The highlighter must see the
		// evaluated text, not the expression, so the page renders at request time.
		if codeOpen := strings.LastIndex(lower, "<code"); codeOpen > strings.LastIndex(lower, "</code>") {
			tag := lower[codeOpen:]
			if end := strings.IndexByte(tag, '>'); end >= 0 {
				tag = tag[:end]
			}
			if strings.Contains(tag, "data-kit-highlight") || strings.Contains(tag, "data-kitwork-highlight") {
				return true
			}
		}
		offset = index + 2
	}
	return false
}

func isAttributeNameByte(char byte) bool {
	return char >= 'a' && char <= 'z' ||
		char >= 'A' && char <= 'Z' ||
		char >= '0' && char <= '9' ||
		char == '-' || char == '_' || char == ':'
}

// PreparationError reports assembly or parse failure captured by Prepare.
func (r *Render) PreparationError() error {
	if r == nil || r.prepareError == "" {
		return nil
	}
	return fmt.Errorf("%s", r.prepareError)
}

// TemplateError is PreparationError when the failure is in the template's own text (a {{ if }} whose
// condition cannot be read, a template that does not parse); nil when it prepared, or when its one
// failure is that there is no shell (index.kitwork.html) to put a page in, as for a site that serves no
// template.
func (r *Render) TemplateError() error {
	if r == nil || !r.templateFailed {
		return nil
	}
	return r.PreparationError()
}

// assembly is what assemble finds on its way through a template and the files it names.
type assembly struct {
	failure     string // the first {{ if }} whose condition cannot be read: file:line:column: why
	missingPage string // the first {{ @page }} with neither its page nor a not-found page: the page looked for
}

// assemble puts together the template of file (content) and the files it names. A {{ if }} whose
// condition cannot be read sets notes.failure, the first one only, to its place and why
// (file:line:column: …), and the template does not prepare, as a script that does not compile. A
// {{ @page }} with neither its page nor a not-found page sets notes.missingPage (NoPageError).
func (r *Render) assemble(content string, file string, depth int, notes *assembly) string {
	if depth > 10 { // Giới hạn đệ quy để tránh treo hệ thống
		return "<!-- Error: Template recursion too deep -->"
	}
	currentDir := filepath.Dir(file)

	tokens := specializeTokens(content)
	var sb strings.Builder

	for index, t := range tokens {
		if strings.HasPrefix(t, "{{") && strings.HasSuffix(t, "}}") {
			tag := strings.TrimSpace(t[2 : len(t)-2])
			parts := strings.Fields(tag)
			if len(parts) == 0 {
				sb.WriteString(t)
				continue
			}

			cmd := parts[0]
			// Layout-slot token: `@navbar` (preferred) or legacy `_navbar_`. Normalize `@x` → `_x_`
			// so both forms hit the slot handling below; anything else falls through to the Bind
			// stage. `base` is the bare slot name ("navbar"), used to find the partial file — which
			// may be the clean `navbar.kitwork.html` OR the legacy `_navbar_.kitwork.html`.
			if len(cmd) > 1 && cmd[0] == '@' {
				cmd = "_" + cmd[1:] + "_"
			}
			base := strings.Trim(cmd, "_")
			switch cmd {
			case "_page_":
				// Nạp trang con động. notfoundMode → nạp thẳng trang not-found
				// (không phải trang của path), để path chưa đăng ký luôn ra 404 page.
				pagePath := r.getPagePath()
				if r.notfoundMode {
					pagePath = r.getNotFoundPath()
				}
				if raw, err := r.readFile(pagePath); err == nil {

					sb.WriteString(r.assemble(string(raw), pagePath, depth+1, notes))
				} else {

					nfPath := r.getNotFoundPath()
					if raw, err := r.readFile(nfPath); err == nil {

						sb.WriteString(r.assemble(string(raw), nfPath, depth+1, notes))
					} else {
						// neither: the page renders with nothing here, and Page says which page it looked for
						// (NoPageError), for the server to log or to fail the request with
						if notes.missingPage == "" {
							notes.missingPage = pagePath
						}
						if r.expressionFailed == nil {
							// while developing, the page looked for: an absolute path of the server, which no page
							// carries in production (Config.ExpressionFailed is set there), minified or not
							sb.WriteString(fmt.Sprintf("<!-- 404: %v -->", pagePath))
						}
					}

				}

			case "_header_", "_navbar_", "_footer_", "_head_", "_sidebar_", "_toolbar_", "_tabbar_", "_subbar_", "_titlebar_":
				found := false

				// A. Thử tìm trong Layout Map (ưu tiên nạp từ RAM nếu có)
				var pathVal string
				switch cmd {
				case "_header_":
					pathVal = r.layout.header
				case "_navbar_":
					pathVal = r.layout.navbar
				case "_footer_":
					pathVal = r.layout.footer
				case "_head_":
					pathVal = r.layout.head
				case "_sidebar_":
					pathVal = r.layout.sidebar
				case "_toolbar_":
					pathVal = r.layout.toolbar
				case "_tabbar_":
					pathVal = r.layout.tabbar
				case "_subbar_":
					pathVal = r.layout.subbar
				case "_titlebar_":
					pathVal = r.layout.titlebar
				}
				if pathVal != "" {
					if raw, err := r.readFile(pathVal); err == nil {
						sb.WriteString(r.assemble(string(raw), pathVal, depth+1, notes))
						found = true
					}
				}

				// B. Walk UP from the current dir to the render root, so a nested-section
				// shell (e.g. app/docs/index) finds shared partials that live higher up
				// (app/_navbar_) — the same walk-up that resolves the shell itself. This
				// is what makes a render work with NO layout map (zero-config).
				if !found {
					root := filepath.Clean(r.resolve(r.dir()))
					dir := filepath.Clean(currentDir)
					for {
						for _, fname := range slotFiles(base) {
							fullPath := filepath.Join(dir, fname)
							if raw, err := r.readFile(fullPath); err == nil {
								sb.WriteString(r.assemble(string(raw), fullPath, depth+1, notes))
								found = true
								break
							}
						}
						if found {
							break
						}
						parent := filepath.Dir(dir)
						if dir == root || parent == dir {
							break
						}
						dir = parent
					}
				}

				// C. Cuối cùng thử tìm trong thư mục views global
				if !found {
					for _, fname := range slotFiles(base) {
						globalPath := r.resolve("views", fname)
						if raw, err := r.readFile(globalPath); err == nil {
							sb.WriteString(r.assemble(string(raw), globalPath, depth+1, notes))
							found = true
							break
						}
					}
				}

				if !found {
					sb.WriteString(fmt.Sprintf("<!-- Missing: %v -->", base+".kitwork.html"))
				}

			case "layout":
				if len(parts) < 2 {
					sb.WriteString(t)
					continue
				}
				name := strings.Trim(parts[1], `"'`)
				if !strings.HasSuffix(name, ".html") {
					name += ".html"
				}
				includePath := filepath.Join(currentDir, filepath.FromSlash(name))
				raw, err := r.readFile(includePath)
				if err != nil {
					includePath = r.resolve("views", filepath.FromSlash(name))
					raw, err = r.readFile(includePath)
				}
				if err != nil {
					layoutFailed(r.expressionFailed, &sb, tag, err)
					continue
				}
				sb.WriteString(r.assemble(string(raw), includePath, depth+1, notes))

			default:
				// Các tag khác như if, for, biến... giữ nguyên để giai đoạn Bind xử lý
				if cmd == "if" && notes.failure == "" {
					condition, at := ifCondition(t)
					if _, unreadable := compileCondition(condition); unreadable != nil {
						notes.failure = r.templateName(file) + ":" + sourceLocation(tokens, index, at+unreadable.at) + ": " + unreadable.describe(condition)
					}
				}
				sb.WriteString(t)
			}
		} else {
			// Text bình thường
			sb.WriteString(t)
		}
	}
	return sb.String()
}

// templateName is how a template file is named in a message: relative to the render's base, as the
// compiler names a script relative to its entry's folder, never the machine's absolute path; its name
// alone when it has no path from there.
func (r *Render) templateName(file string) string {
	relative, err := filepath.Rel(r.base, file)
	if err != nil || r.base == "" || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return filepath.Base(file)
	}
	return filepath.ToSlash(relative)
}

// slotFiles returns the partial-file candidates for a layout slot, newest convention first:
// "navbar.kitwork.html" (clean — matches the @navbar token) then the legacy
// "_navbar_.kitwork.html". The first that exists on disk wins.
func slotFiles(base string) []string {
	return []string{base + ".kitwork.html", "_" + base + "_.kitwork.html"}
}

func (r *Render) Has(name string) bool {
	base := strings.Trim(name, "_") // accept "sidebar", "_sidebar_" or "@sidebar"-style input
	base = strings.TrimPrefix(base, "@")
	for _, fname := range slotFiles(base) {
		if r.fileExists(r.pathJoin(r.path, fname)) {
			return true
		}
	}
	return false
}

func (r *Render) Exists(name string) bool {
	// Trường hợp 1: Kiểm tra thư mục con chứa page.kitwork.html (Ví dụ: routing/page.kitwork.html)
	path1 := r.pathJoin(r.path, name, r.getfile("page"))
	if r.fileExists(path1) {
		return true
	}

	// Trường hợp 2: Kiểm tra file trực tiếp (Ví dụ: routing.kitwork.html)
	path2 := r.pathJoin(r.path, r.getfile(name))
	if r.fileExists(path2) {
		return true
	}

	return false
}

// Bind renders data. A template whose own text does not prepare (TemplateError) gives a failure that
// says why, never a page: the request that asked for it fails as a page that does not compile, the
// message whole in the log and, while developing, in the answer (16/10/2026: the message was the page,
// in production too). Its code is INVALID_ARGUMENT, a page named that the engine cannot prepare; no
// program reads it, for the request ends there. With no shell (index.kitwork.html) to put the page in,
// the page is the text of NoShellError, which names a path of the server: a server answering visitors
// renders with Page, which gives that case apart.
func (r *Render) Bind(data value.Value) value.Value {
	page, _ := r.bind(data)
	return page
}

func (r *Render) bind(data value.Value) (value.Value, error) {
	rendered, failed, noShell, missingPage := r.tmpl(data)
	if failed {
		return value.InvalidFailure(value.InvalidArgumentCode, rendered), nil
	}
	if noShell {
		return value.New(rendered), &NoShellError{text: rendered}
	}
	if missingPage != "" {
		return value.New(rendered), &NoPageError{page: missingPage}
	}
	return value.New(rendered), nil
}

// NoPageError is a page that rendered with nothing where its {{ @page }} stands: neither the page looked
// for nor a not-found page is there, at an unknown path of a site with no not-found page or at a page a
// handler chose that does not exist. The page carries no path of the server in production (while
// developing a <!-- 404: path --> comment stands there); the text names the page looked for, an absolute
// path on the server, for the log and, while developing, the answer (16/10/2026: in production a chosen
// page that does not exist answered 200 with nothing in it, and nothing was said).
type NoPageError struct{ page string }

func (e *NoPageError) Error() string {
	return "không có trang " + e.page + ", cũng không có trang not-found"
}

// NoShellError is why a page did not render: no shell (index.kitwork.html) stands above it to put it
// in, as in a site that serves no template, at an unknown path or at a page a handler chose. Its text
// names the shell looked for, an absolute path on the server, for the log and, while developing, the
// answer; a visitor in production never reads it (16/10/2026: it was the whole page of every such 404,
// in production too).
type NoShellError struct{ text string }

func (e *NoShellError) Error() string { return e.text }

// BindPage renders like Bind but with a per-request page override and notfound flag, on a COPY —
// so callers never mutate the shared render or touch its unexported fields (page = "" keeps the
// render's own page). Used by the tree view lifecycle.
func (r *Render) BindPage(page string, notfoundMode bool, data value.Value) value.Value {
	rendered, _ := r.Page(page, notfoundMode, data)
	return rendered
}

// Page renders as BindPage does and gives apart a page that has no shell to stand in: err is then a
// *NoShellError, and the value is its text, the page BindPage gives. A page with neither its page nor a
// not-found page gives a *NoPageError, and the value is the page with nothing in its place.
func (r *Render) Page(page string, notfoundMode bool, data value.Value) (value.Value, error) {
	rc := *r
	if page != "" && page != rc.page {
		rc.page = page
		rc.program = nil
		rc.prepareError = ""
		rc.presentationPrepared = false
		rc.minifyPrepared = false
	}
	if notfoundMode != rc.notfoundMode {
		rc.program = nil
		rc.prepareError = ""
		rc.presentationPrepared = false
		rc.minifyPrepared = false
	}
	rc.notfoundMode = notfoundMode
	return rc.bind(data)
}

// Render service entry point
// kitwork().render(...) -> Template
// kitwork().render.file(...) -> Service call

// HTML renders a raw template string with data
func (r *Render) HTML(tmpl string, data any) string {
	viewDir := r.resolve("views")
	return engineRenderWithSource(tmpl, data, viewDir, viewDir, r.source, r.expressionFailed)
}

// File renders a file from the 'views' directory
// func (r *Render) File(name string, data any) string {
// 	path := r.resolve("views", name)
// 	if filepath.Ext(path) == "" {
// 		path += ".html"
// 	}

// 	content, err := os.ReadFile(path)
// 	if err != nil {
// 		return "Render Error: file not found at " + path
// 	}

// 	viewDir := filepath.Dir(path)
// 	globalDir := r.resolve("views")

// 	return engineRender(string(content), data, viewDir, globalDir)
// }

// ----------------------------------------------------------------------------
// TEMPLATE ENGINE CORE
// ----------------------------------------------------------------------------

func engineRender(tmpl string, data any, viewDir string, globalDir string) string {
	return engineRenderWithSource(tmpl, data, viewDir, globalDir, nil, nil)
}

func engineRenderWithSource(tmpl string, data any, viewDir string, globalDir string, source Source, failed func(expression, message string)) string {
	tokens := specializeTokens(tmpl)
	node := parse(tokens)

	initialScope := make(map[string]value.Value)
	valData := value.New(data)
	initialScope["$"] = valData
	initialScope["__view_dir"] = value.New(viewDir)
	initialScope["__global_view_dir"] = value.New(globalDir)
	if source != nil {
		initialScope["__template_source"] = value.New(source)
	}

	if valData.IsMap() {
		for k, v := range valData.Map() {
			initialScope[k] = v
		}
	}

	return evalWith(node, data, initialScope, failed)
}

func readScopedTemplate(scope *renderScope, filename string) ([]byte, error) {
	if sourceValue, ok := scope.get("__template_source"); ok {
		if source, valid := sourceValue.V.(Source); valid && source != nil {
			return source.ReadFile(filename)
		}
	}
	return os.ReadFile(filename)
}

type nodeType int

const (
	nodeRoot nodeType = iota
	nodeText
	nodeVar
	nodeIf
	nodeRange
	nodeLet
	nodePartial
	nodeCapture
)

type node struct {
	typ  nodeType
	val  string // Variable name or Condition
	expr *expression
	// failure is why the condition of a {{ if }} cannot be read (compileCondition), with where it
	// stands in the template; the node renders it as a failed expression (conditionFailed)
	failure     string
	raw         bool
	keyVar      string // "i" in range i, v := list
	valVar      string // "v" in range i, v := list
	children    []*node
	alt         []*node // Else block
	parsingElse bool    // Parsing state
	staticBytes int
}

func specializeTokens(tmpl string) []string {
	var tokens []string
	start := 0
	for {
		open := strings.Index(tmpl[start:], "{{")
		if open == -1 {
			tokens = append(tokens, tmpl[start:])
			break
		}
		if open > 0 {
			tokens = append(tokens, tmpl[start:start+open])
		}

		close := strings.Index(tmpl[start+open:], "}}")
		if close == -1 {
			tokens = append(tokens, tmpl[start+open:])
			break
		}

		tagContent := tmpl[start+open+2 : start+open+close]
		tokens = append(tokens, "{{"+tagContent+"}}")
		start += open + close + 2
	}
	var clean []string
	for _, t := range tokens {
		if t != "" {
			// Nếu là tag {{ ... }}
			if strings.HasPrefix(t, "{{") && strings.HasSuffix(t, "}}") {
				content := strings.TrimSpace(t[2 : len(t)-2])
				parts := strings.Fields(content)
				if len(parts) > 0 {
					cmd := parts[0]
					switch cmd {
					case "if", "else", "elseif", "end", "for", "let":
						clean = append(clean, t)
						continue
					}
				}
				// Nếu không phải lệnh đặc biệt, coi như in biến
				clean = append(clean, t)
			} else {
				// Text thuần
				clean = append(clean, t)
			}
		}
	}
	return clean
}

func parse(tokens []string) *node {
	root := &node{typ: nodeRoot}
	stack := []*node{root}

	for index, t := range tokens {
		current := stack[len(stack)-1]

		if strings.HasPrefix(t, "{{") && strings.HasSuffix(t, "}}") {
			content := strings.TrimSpace(t[2 : len(t)-2])
			parts := strings.Fields(content)

			if len(parts) == 0 {
				continue
			}

			cmd := parts[0]

			switch cmd {
			case "if":
				// the whole condition, one expression (condition.go); one that cannot be read fails where
				// it stands when it renders. A prepared template never gets here with one: assemble
				// refuses it first, naming its file.
				condition, at := ifCondition(t)
				n := &node{typ: nodeIf, val: condition}
				if compiled, failure := compileCondition(condition); failure != nil {
					n.failure = sourceLocation(tokens, index, at+failure.at) + ": " + failure.describe(condition)
				} else {
					n.expr = compiled
				}
				addChild(current, n)
				stack = append(stack, n)

			case "for":
				n := &node{typ: nodeRange}
				if inIdx := indexOf(parts, "in"); inIdx > -1 {
					varsPart := strings.Join(parts[1:inIdx], "")
					if strings.HasPrefix(varsPart, "(") && strings.HasSuffix(varsPart, ")") {
						inner := varsPart[1 : len(varsPart)-1]
						subParts := strings.Split(inner, ",")
						if len(subParts) > 1 {
							n.keyVar = subParts[0]
							n.valVar = subParts[1]
						} else {
							n.valVar = subParts[0]
						}
					} else {
						n.valVar = parts[1]
					}
					n.val = parts[inIdx+1]
				} else {
					n.val = parts[1]
				}
				n.expr = compileExpression(n.val)
				addChild(current, n)
				stack = append(stack, n)

			case "let":
				if len(parts) >= 4 && parts[2] == "=" {
					// The right-hand side is everything after the first '=', so a
					// string literal with spaces (e.g. a lead paragraph) survives
					// intact instead of being truncated to its first token.
					rhs := strings.TrimSpace(content[strings.Index(content, "=")+1:])
					n := &node{
						typ:    nodeLet,
						keyVar: parts[1],
						val:    rhs,
						expr:   compileExpression(rhs),
					}
					addChild(current, n)
				}

			case "capture":
				// {{ capture name }} ... {{ end }} renders the inner markup live
				// AND binds `name` to that region's exact source text, so a code
				// panel can echo `{{ name }}` (HTML-escaped) with no drift.
				if len(parts) >= 2 {
					n := &node{typ: nodeCapture, keyVar: parts[1]}
					addChild(current, n)
					stack = append(stack, n)
				}

			case "else":
				if current.typ == nodeIf {
					current.parsingElse = true
				}

			case "layout":
				if len(parts) > 1 {
					n := &node{typ: nodePartial, val: strings.Trim(parts[1], `"'`)}
					addChild(current, n)
				}

			case "end":
				if len(stack) > 1 {
					stack = stack[:len(stack)-1]
				}

			default:
				expr, raw := compileOutputExpression(content)
				n := &node{typ: nodeVar, val: content, expr: expr, raw: raw}
				addChild(current, n)
			}
		} else {
			n := &node{typ: nodeText, val: t}
			addChild(current, n)
		}
	}
	root.staticBytes = countStaticBytes(root)
	return root
}

func countStaticBytes(n *node) int {
	if n == nil {
		return 0
	}
	if n.typ == nodeText {
		return len(n.val)
	}
	total := 0
	for _, child := range n.children {
		total += countStaticBytes(child)
	}
	for _, child := range n.alt {
		total += countStaticBytes(child)
	}
	return total
}

func addChild(parent, child *node) {
	if parent.parsingElse {
		parent.alt = append(parent.alt, child)
	} else {
		parent.children = append(parent.children, child)
	}
}

// reconstructSource rebuilds the authored template source of a capture region.
// Demo markup is static text, so this is the exact authored HTML; interpolation
// and control nodes are re-emitted in their `{{ ... }}` form for completeness.
func reconstructSource(nodes []*node) string {
	var b strings.Builder
	for _, child := range nodes {
		switch child.typ {
		case nodeText:
			b.WriteString(child.val)
		case nodeVar:
			b.WriteString("{{ ")
			b.WriteString(child.val)
			b.WriteString(" }}")
		default:
			b.WriteString(reconstructSource(child.children))
		}
	}
	return b.String()
}

// dedentSource trims blank edge lines and removes the common leading indentation
// so a captured region prints cleanly regardless of its nesting in the template.
func dedentSource(source string) string {
	source = strings.Trim(source, "\r\n")
	lines := strings.Split(source, "\n")
	indent := -1
	for _, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed == "" {
			continue
		}
		lead := len(line) - len(trimmed)
		if indent < 0 || lead < indent {
			indent = lead
		}
	}
	if indent <= 0 {
		return source
	}
	for i, line := range lines {
		if len(line) >= indent {
			lines[i] = line[indent:]
		} else {
			lines[i] = strings.TrimLeft(line, " \t")
		}
	}
	return strings.Join(lines, "\n")
}

func indexOf(parts []string, target string) int {
	for i, p := range parts {
		if p == target {
			return i
		}
	}
	return -1
}

func eval(n *node, data any, scope map[string]value.Value) string {
	return evalWith(n, data, scope, nil)
}

// evalWith is eval with the hook of the render for an expression that failed (Config.ExpressionFailed).
func evalWith(n *node, data any, scope map[string]value.Value, failed func(expression, message string)) (out string) {
	var sb strings.Builder
	if n != nil {
		sb.Grow(n.staticBytes)
	}
	rootScope := renderScope{values: scope, failed: failed}
	current := value.New(data)
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("[Render Panic] %v\n", r)
			out = ""
		}
	}()
	evalInto(n, current, &rootScope, &sb)
	return sb.String()
}

func evalInto(
	n *node,
	data value.Value,
	scope *renderScope,
	output *strings.Builder,
) {
	if n == nil {
		return
	}
	switch n.typ {
	case nodeRoot:
		renderChildrenInto(n.children, data, scope, output)

	case nodeText:
		output.WriteString(n.val)

	case nodeVar:
		resolved := resolveExpression(n.expr, data, scope)
		if resolved.K == value.Invalid {
			if failed := scope.failure(); failed != nil {
				// production: nothing in the page where the value would stand, the message to the log
				failed(expressionSource(n.expr), resolved.Text())
				return
			}
		}
		writeResolvedValue(output, resolved, !n.raw)

	case nodeIf:
		if n.failure != "" {
			conditionFailed(n, n.failure, scope, output)
			return
		}
		condition := resolveExpression(n.expr, data, scope)
		if condition.K == value.Invalid {
			conditionFailed(n, condition.Text(), scope, output)
			return
		}

		if condition.Truthy() {
			renderChildrenInto(n.children, data, scope, output)
		} else {
			renderChildrenInto(n.alt, data, scope, output)
		}

	case nodeRange:
		val := resolveExpression(n.expr, data, scope)
		if val.IsArray() {
			arr := val.Array()
			newScope := renderScope{parent: scope}
			for i, item := range arr {
				newScope.reset(scope)
				if n.keyVar != "" {
					newScope.bind(n.keyVar, value.New(i))
				}
				if n.valVar != "" {
					newScope.bind(n.valVar, item)
				}
				renderChildrenInto(n.children, item, &newScope, output)
			}
		} else if val.IsMap() {
			m := val.Map()
			newScope := renderScope{parent: scope}
			for k, v := range m {
				newScope.reset(scope)
				if n.keyVar != "" {
					newScope.bind(n.keyVar, value.New(k))
				}
				if n.valVar != "" {
					newScope.bind(n.valVar, v)
				}
				renderChildrenInto(n.children, v, &newScope, output)
			}
		}

	case nodeLet:
		val := resolveExpression(n.expr, data, scope)
		scope.set(n.keyVar, val)

	case nodeCapture:
		// Bind the region's exact authored source (dedented) so a later
		// `{{ name }}` prints it HTML-escaped, then render the region live so
		// the JIT presentation pass scans the demo like any other markup.
		scope.set(n.keyVar, value.New(dedentSource(reconstructSource(n.children))))
		renderChildrenInto(n.children, data, scope, output)

	case nodePartial:
		viewDir := ""
		if v, ok := scope.get("__view_dir"); ok {
			viewDir = v.String()
		}
		fname := n.val
		if !strings.HasSuffix(fname, ".html") {
			fname += ".html"
		}

		// Thử tìm trong __layouts map trước (ưu tiên Fluent Layouts)
		if lMapVal, ok := scope.get("__layouts"); ok && lMapVal.IsMap() {
			if pathVal, ok := lMapVal.Map()[fname]; ok {
				content, err := readScopedTemplate(scope, pathVal.String())
				if err == nil {
					tokens := specializeTokens(string(content))
					prog := parse(tokens)
					newScope := renderScope{parent: scope}
					newScope.bind("__view_dir", value.New(filepath.Dir(pathVal.String())))
					evalInto(prog, data, &newScope, output)
					return
				}
			}
			// Thử tìm theo tên không đuôi
			nameOnly := strings.TrimSuffix(fname, ".html")
			if pathVal, ok := lMapVal.Map()[nameOnly]; ok {
				content, err := readScopedTemplate(scope, pathVal.String())
				if err == nil {
					tokens := specializeTokens(string(content))
					prog := parse(tokens)
					newScope := renderScope{parent: scope}
					newScope.bind("__view_dir", value.New(filepath.Dir(pathVal.String())))
					evalInto(prog, data, &newScope, output)
					return
				}
			}
		}

		fullPath := filepath.Join(viewDir, fname)
		content, err := readScopedTemplate(scope, fullPath)
		if err != nil {
			if globalVal, ok := scope.get("__global_view_dir"); ok {
				fallbackDir := globalVal.String()
				fullPath = filepath.Join(fallbackDir, fname)
				content, err = readScopedTemplate(scope, fullPath)
			}
			if err != nil {
				layoutFailed(scope.failure(), output, `layout "`+n.val+`"`, err)
				return
			}
		}

		tokens := specializeTokens(string(content))
		prog := parse(tokens)
		newScope := renderScope{parent: scope}
		newScope.bind("__view_dir", value.New(filepath.Dir(fullPath)))

		evalInto(prog, data, &newScope, output)
	}
}

// conditionFailed is a {{ if }} whose condition failed, cannot be read (failure) or met a failure (a
// method that is not a function, "a" - 1), as a {{ expression }} that fails: neither branch renders;
// while developing the message stands in their place, in production the page writes nothing there and
// the message goes to the log (Config.ExpressionFailed). A script's run ends at such a failure; until
// 16/10/2026 a failed condition was false and its else branch rendered, with nothing said.
func conditionFailed(n *node, message string, scope *renderScope, output *strings.Builder) {
	if failed := scope.failure(); failed != nil {
		failed("if "+n.val, message)
		return
	}
	writeEscapedString(output, message)
}

// layoutFailed is a {{ layout "name" }} whose file cannot be read, as a {{ expression }} that fails:
// while developing the error stands in its place, as it always did (read from the disk, it names the
// absolute path looked for); in production the page writes nothing there and the error goes to the log
// (Config.ExpressionFailed), for a page never carries a path of the server or a Go error (16/10/2026:
// "[Error: …]" was in the page, in production too).
func layoutFailed(failed func(expression, message string), output *strings.Builder, tag string, err error) {
	if failed != nil {
		failed(tag, err.Error())
		return
	}
	output.WriteString(fmt.Sprintf("[Error: %v]", err))
}

func writeResolvedValue(output *strings.Builder, resolved value.Value, escape bool) {
	escape = escape && !resolved.Raw // a Raw-marked (already-safe) value is never re-escaped
	if text, ok := resolved.V.(string); ok {
		if escape {
			writeEscapedString(output, text)
		} else {
			output.WriteString(text)
		}
		return
	}

	var local [128]byte
	rendered := resolved.Append(local[:0])
	if escape {
		writeEscapedBytes(output, rendered)
	} else {
		_, _ = output.Write(rendered)
	}
}

func writeEscapedString(output *strings.Builder, source string) {
	start := 0
	for index := 0; index < len(source); index++ {
		replacement := htmlReplacement(source[index])
		if replacement == "" {
			continue
		}
		output.WriteString(source[start:index])
		output.WriteString(replacement)
		start = index + 1
	}
	output.WriteString(source[start:])
}

func writeEscapedBytes(output *strings.Builder, source []byte) {
	start := 0
	for index := 0; index < len(source); index++ {
		replacement := htmlReplacement(source[index])
		if replacement == "" {
			continue
		}
		_, _ = output.Write(source[start:index])
		output.WriteString(replacement)
		start = index + 1
	}
	_, _ = output.Write(source[start:])
}

func htmlReplacement(character byte) string {
	switch character {
	case '&':
		return "&amp;"
	case '\'':
		return "&#39;"
	case '<':
		return "&lt;"
	case '>':
		return "&gt;"
	case '"':
		return "&#34;"
	default:
		return ""
	}
}

func renderChildrenInto(
	nodes []*node,
	data value.Value,
	scope *renderScope,
	output *strings.Builder,
) {
	for _, n := range nodes {
		evalInto(n, data, scope, output)
	}
}

func findSplitIndex(s string, checkFn func(int) bool, last bool) int {
	level := 0
	inDoubleQuote := false
	inSingleQuote := false
	matchedIdx := -1

	for i := 0; i < len(s); i++ {
		// Skip escaped characters
		if s[i] == '\\' && i+1 < len(s) {
			i++
			continue
		}

		if s[i] == '"' && !inSingleQuote {
			inDoubleQuote = !inDoubleQuote
			continue
		}
		if s[i] == '\'' && !inDoubleQuote {
			inSingleQuote = !inSingleQuote
			continue
		}

		if inDoubleQuote || inSingleQuote {
			continue // Skip everything inside string literals
		}

		if s[i] == '(' {
			level++
		} else if s[i] == ')' {
			level--
		}

		if level == 0 && checkFn(i) {
			if !last {
				return i
			}
			matchedIdx = i
		}
	}
	return matchedIdx
}

func traverse(current value.Value, parts []string) value.Value {
	for _, part := range parts {
		if current.IsNil() {
			return current
		}
		res := current.Get(part)
		if res.IsNil() {
			if nested, ok := current.V.(value.Value); ok {
				current = nested
				res = current.Get(part)
			}
		}
		current = res
		if current.IsNil() {
			return current
		}
	}
	return current
}
