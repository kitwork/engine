package render

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	jitcss "github.com/kitwork/engine/jit/css"
	fonts "github.com/kitwork/engine/jit/fonts"
	hydrate "github.com/kitwork/engine/jit/hydrate"
	icons "github.com/kitwork/engine/jit/icons"
	jitjs "github.com/kitwork/engine/jit/js"
	logo "github.com/kitwork/engine/jit/logo"
	material "github.com/kitwork/engine/jit/material"
	theme "github.com/kitwork/engine/jit/theme"
	"github.com/kitwork/engine/utilities/minifier"
	"github.com/kitwork/engine/value"
)

// Config is everything the render engine needs — no *Tenant, no HTTP. Build a render with New(),
// then Bind(data) for a page or HTML(tmpl, data) for a raw string template (e.g. an email).
type Config struct {
	Base          string         // template root — the anchor every path resolves against
	JitConfig     *jitcss.Config // JIT-CSS config (brand colors, keyframes…); nil = defaults
	Directory     string         // sub-root under Base (tree uses "."; legacy used "views"/"app")
	Path          string         // the folder whose page/index/slots resolve, walked up
	Page          string         // explicit page override (usually "" — derived from Path)
	Index         string         // explicit shell filename override
	Notfound      string         // notfound filename (default "notfound")
	NotfoundMode  bool           // render the notfound page for {{ @page }}
	JitCSS        bool           // inline the minimal JIT CSS for the page's classes
	Global        value.Value    // data merged into every render
	Minify        []string       // explicit minify content types
	MinifySet     bool           // whether Minify was set explicitly
	DefaultMinify bool           // minify when not set explicitly (caller passes !AllowLocal)
	ThemeMode     string         // theme pre-paint: "" = auto-scan, "force" = always, "off" = never
	Source        Source         // immutable template source; nil reads the live filesystem
}

func New(c Config) *Render {
	return &Render{
		base: c.Base, jitConfig: c.JitConfig, directory: c.Directory, path: c.Path,
		page: c.Page, index: c.Index, notfound: c.Notfound, notfoundMode: c.NotfoundMode,
		jitCSS: c.JitCSS, global: c.Global, minify: c.Minify, minifySet: c.MinifySet,
		defaultMinify: c.DefaultMinify, themeMode: c.ThemeMode,
		source: c.Source,
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
	notfoundMode         bool     // render the notfound page for {{ @page }}
	jitCSS               bool     // inject server-side Tailwind/utility CSS for the page's classes
	minify               []string // content types to minify on the final HTML output
	minifySet            bool     // whether minify was set explicitly (else default by environment)
	defaultMinify        bool     // minify default when not explicit (injected — replaces AllowLocal)
	themeMode            string   // theme pre-paint mode (see Config.ThemeMode)
	source               Source   // immutable generation snapshot; nil = live filesystem
	program              *node
	prepareError         string
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

func (r *Render) tmpl(data any) string {
	// 1. GIAI ĐOẠN ASSEMBLY: Ráp nối các file mẫu thành một template lớn duy nhất
	// Bắt đầu từ file Shell (index.html)
	program := r.program
	prepareError := r.prepareError
	presentationPrepared := r.presentationPrepared
	minifyPrepared := r.minifyPrepared
	if program == nil && prepareError == "" {
		program, presentationPrepared, minifyPrepared, prepareError = r.compileTemplate(false)
	}
	if prepareError != "" {
		return prepareError
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

	// Parse và Eval một lần duy nhất cho toàn bộ cây mẫu
	out := eval(program, data, scope)
	if !presentationPrepared {
		out = r.applyStaticPresentation(out)
	}

	// Hydrate pre-render evaluates request data, so it deliberately remains on
	// the request path even when the static presentation was generation-prepared.
	out = hydrate.PreRender(out)

	if r.shouldMinify() {
		if !minifyPrepared {
			out = minifier.HTML(out)
		}
	}
	return out
}

// applyStaticPresentation performs source-driven JIT work. Prepared renders run
// it once while building the immutable generation; live or dynamic-attribute
// renders retain the exact request-time behavior.
func (r *Render) applyStaticPresentation(out string) string {
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
			style := "<style data-kitwork-jit=\"css\">\n" + css + "</style>"
			if i := strings.LastIndex(out, "</head>"); i >= 0 {
				out = out[:i] + style + out[i:]
			} else {
				out = style + out
			}
		}
	}

	// 3d. JIT material: inject <style data-kitwork-jit="material"> with CSS for ONLY the UI
	// material families (.button/.btn, .card, …) the page uses (jit/material). No-op otherwise.
	out = material.Render(out)

	// 3c. JIT icons. DEFAULT (inline): scan for `<i class="icon-x">` and inject a per-page
	// <style data-kitwork-jit="icons"> with CSS-mask rules for ONLY the icons used (jit/icons) — a
	// cheap no-op when none are present. SERVICE mode: if the tenant declared router.icons(), a
	// shared cached stylesheet is served at iconRoute instead, so we skip the inline pass and
	// auto-link that stylesheet (same guards as router.jit(): a LIVE isIcons route, idempotent if a
	// link is already present). Sovereign + minimal either way: no Font Awesome, no CDN, no payload.
	out = icons.Render(out)

	// 3f. JIT logos: brand logos (Simple Icons) via <i class="logo-x"> — same inline/service model
	// as icons (jit/logo). router.logo() switches to the shared cached /jitlogo stylesheet.
	out = logo.Render(out)

	// 3e. jitjs. DEFAULT (inline): inject a per-page <script data-kitwork-jit="js"> with the core
	// dispatcher + ONLY the data-kitwork-action verbs the page uses (jit/js); Drive re-runs it on
	// swap (mergeHead). SERVICE: if the tenant declared router.jitjs(), one shared cached runtime is
	// served at jitjsRoute, so we skip inlining and auto-inject <script src> (same guards as
	// router.icons()). A cheap no-op when no verbs are used.
	out = jitjs.Render(out)

	// 3h. hydrate (frontend bytecode VM): on a page that opts in via the data-kitwork-hydrate root
	// marker, verify every authored expression (compile-time linting) and inject the kernel runtime
	// reference — only-used. The wire ships the SOURCE; the client parses it (no eval). Then PreRender
	// runs the SAME Go walker over data-kit-text/show to bake initial values into the HTML: no flash,
	// correct with JS off, indexable. Both are marker-gated no-ops on ordinary pages.
	out = hydrate.Render(out)

	// 3g. JIT fonts (jitfonts): self-hosted Google Fonts. Scan for the font FAMILIES the page uses
	// (a `font-family: <Name>` value or a `font-<slug>` class) → inject preload links + ONE
	// <style data-kitwork-jit="fonts"> with @font-face (subset woff2 served from /jitfonts) for ONLY
	// those families. No Google at runtime, no third-party CDN; a cheap no-op when none are used.
	out = fonts.Render(out)

	// 3i. JIT theme: swap <script data-kitwork-jit="theme"> for a synchronous pre-paint that applies
	// the saved/OS theme before first paint (no flash). Pairs with the jitjs theme toggle verb.
	switch r.themeMode {
	case "off": // router.jittheme(false) — no pre-paint even when the scan would find usage
	case "force":
		out = theme.Force(out) // router.jittheme(true) — always inject, scan or no scan
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
) {
	defer func() {
		if recovered := recover(); recovered != nil {
			program = nil
			presentationPrepared = false
			minifyPrepared = false
			prepareError = fmt.Sprintf("template parse error: %v", recovered)
		}
	}()
	indexPath := r.getIndexPath()
	shellRaw, err := r.readFile(indexPath)
	if err != nil {
		return nil, false, false, fmt.Sprintf("[Error reading index: %v]", indexPath)
	}
	fullTemplate := r.assemble(string(shellRaw), filepath.Dir(indexPath), 0)
	if preparePresentation && !hasDynamicPresentation(fullTemplate) {
		fullTemplate = r.applyStaticPresentation(fullTemplate)
		presentationPrepared = true
		if r.shouldMinify() {
			if minified, ok := minifier.TemplateHTML(fullTemplate); ok {
				fullTemplate = minified
				minifyPrepared = true
			}
		}
	}
	return parse(specializeTokens(fullTemplate)), presentationPrepared, minifyPrepared, ""
}

// Prepare assembles and parses this render path once. The returned Render is
// immutable and safe for concurrent Bind calls.
func (r *Render) Prepare() *Render {
	if r == nil {
		return nil
	}
	prepared := *r
	prepared.program, prepared.presentationPrepared, prepared.minifyPrepared, prepared.prepareError =
		prepared.compileTemplate(true)
	return &prepared
}

// PresentationPrepared reports whether source-driven presentation work was
// frozen into this immutable render generation.
func (r *Render) PresentationPrepared() bool {
	return r != nil && r.presentationPrepared
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

func (r *Render) assemble(content string, currentDir string, depth int) string {
	if depth > 10 { // Giới hạn đệ quy để tránh treo hệ thống
		return "<!-- Error: Template recursion too deep -->"
	}

	tokens := specializeTokens(content)
	var sb strings.Builder

	for _, t := range tokens {
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

					sb.WriteString(r.assemble(string(raw), filepath.Dir(pagePath), depth+1))
				} else {

					nfPath := r.getNotFoundPath()
					if raw, err := r.readFile(nfPath); err == nil {

						sb.WriteString(r.assemble(string(raw), filepath.Dir(nfPath), depth+1))
					} else {
						sb.WriteString(fmt.Sprintf("<!-- 404: %v -->", pagePath))
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
						sb.WriteString(r.assemble(string(raw), filepath.Dir(pathVal), depth+1))
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
								sb.WriteString(r.assemble(string(raw), filepath.Dir(fullPath), depth+1))
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
							sb.WriteString(r.assemble(string(raw), filepath.Dir(globalPath), depth+1))
							found = true
							break
						}
					}
				}

				if !found {
					sb.WriteString(fmt.Sprintf("<!-- Missing: %v -->", base+".kitwork.html"))
				}

			case "include", "layout":
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
					sb.WriteString(fmt.Sprintf("[Error: %v]", err))
					continue
				}
				sb.WriteString(r.assemble(string(raw), filepath.Dir(includePath), depth+1))

			default:
				// Các tag khác như if, for, biến... giữ nguyên để giai đoạn Bind xử lý
				sb.WriteString(t)
			}
		} else {
			// Text bình thường
			sb.WriteString(t)
		}
	}
	return sb.String()
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

func (r *Render) Bind(data value.Value) value.Value {
	return value.New(r.tmpl(data))
}

// BindPage renders like Bind but with a per-request page override and notfound flag, on a COPY —
// so callers never mutate the shared render or touch its unexported fields (page = "" keeps the
// render's own page). Used by the tree view lifecycle.
func (r *Render) BindPage(page string, notfoundMode bool, data value.Value) value.Value {
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
	return rc.Bind(data)
}

// Render service entry point
// kitwork().render(...) -> Template
// kitwork().render.file(...) -> Service call

// HTML renders a raw template string with data
func (r *Render) HTML(tmpl string, data any) string {
	viewDir := r.resolve("views")
	return engineRenderWithSource(tmpl, data, viewDir, viewDir, r.source)
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
	return engineRenderWithSource(tmpl, data, viewDir, globalDir, nil)
}

func engineRenderWithSource(tmpl string, data any, viewDir string, globalDir string, source Source) string {
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

	return eval(node, data, initialScope)
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
)

type node struct {
	typ         nodeType
	val         string   // Variable name or Condition
	args        []string // Arguments for comparison
	expr        *expression
	condition   ifCondition
	raw         bool
	keyVar      string // "i" in range i, v := list
	valVar      string // "v" in range i, v := list
	children    []*node
	alt         []*node // Else block
	parsingElse bool    // Parsing state
	staticBytes int
}

type ifCondition struct {
	modulo       bool
	moduloBy     int
	moduloTarget int
	operator     string
	targetRaw    string
	targetNumber float64
	targetIsNum  bool
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

	for _, t := range tokens {
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
				n := &node{typ: nodeIf, val: parts[1], expr: compileExpression(parts[1])}
				if len(parts) > 2 {
					n.args = parts[2:]
					n.condition = compileIfCondition(n.args)
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
					n := &node{
						typ:    nodeLet,
						keyVar: parts[1],
						val:    parts[3],
						expr:   compileExpression(parts[3]),
					}
					addChild(current, n)
				}

			case "else":
				if current.typ == nodeIf {
					current.parsingElse = true
				}

			case "include", "layout":
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

func compileIfCondition(args []string) ifCondition {
	if len(args) >= 4 && args[0] == "%" {
		var moduloBy float64
		var moduloTarget float64
		_, _ = fmt.Sscanf(args[1], "%f", &moduloBy)
		_, _ = fmt.Sscanf(args[3], "%f", &moduloTarget)
		return ifCondition{
			modulo:       true,
			moduloBy:     int(moduloBy),
			moduloTarget: int(moduloTarget),
			operator:     args[2],
		}
	}
	if len(args) < 2 {
		return ifCondition{}
	}

	targetRaw := strings.Trim(args[1], `"'`)
	var targetNumber float64
	parsed, _ := fmt.Sscanf(targetRaw, "%f", &targetNumber)
	return ifCondition{
		operator:     args[0],
		targetRaw:    targetRaw,
		targetNumber: targetNumber,
		targetIsNum:  parsed == 1,
	}
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

func indexOf(parts []string, target string) int {
	for i, p := range parts {
		if p == target {
			return i
		}
	}
	return -1
}

func eval(n *node, data any, scope map[string]value.Value) (out string) {
	var sb strings.Builder
	if n != nil {
		sb.Grow(n.staticBytes)
	}
	rootScope := renderScope{values: scope}
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
		writeResolvedValue(output, resolveExpression(n.expr, data, scope), !n.raw)

	case nodeIf:
		val := resolveExpression(n.expr, data, scope)
		truthy := val.Truthy()
		condition := n.condition

		if condition.modulo {
			truthy = false
			if val.IsNumeric() {
				if condition.moduloBy != 0 {
					current := int(val.Float())
					remainder := current % condition.moduloBy
					switch condition.operator {
					case "==":
						truthy = remainder == condition.moduloTarget
					case "!=":
						truthy = remainder != condition.moduloTarget
					}
				}
			}
		} else if condition.operator != "" {
			if val.IsNumeric() {
				if condition.targetIsNum {
					current := val.Float()
					switch condition.operator {
					case "==":
						truthy = current == condition.targetNumber
					case "!=":
						truthy = current != condition.targetNumber
					case ">":
						truthy = current > condition.targetNumber
					case "<":
						truthy = current < condition.targetNumber
					case ">=":
						truthy = current >= condition.targetNumber
					case "<=":
						truthy = current <= condition.targetNumber
					}
				}
			} else {
				switch condition.operator {
				case "==":
					truthy = val.String() == condition.targetRaw
				case "!=":
					truthy = val.String() != condition.targetRaw
				}
			}
		}

		if truthy {
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
				output.WriteString(fmt.Sprintf("[Error: %v]", err))
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

func writeResolvedValue(output *strings.Builder, resolved value.Value, escape bool) {
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
