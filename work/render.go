package work

import (
	"fmt"
	"html"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	jitcss "github.com/kitwork/engine/jit/css"
	"github.com/kitwork/engine/modules/minifier"
	"github.com/kitwork/engine/value"
)

// ----------------------------------------------------------------------------
// RENDER SERVICE & FLUENT API
// ----------------------------------------------------------------------------

func (w *KitWork) Render() *Render {

	return NewRender(w.tenant)
}

func NewRender(tenant *Tenant) *Render {
	return &Render{
		tenant: tenant,
	}
}

type Render struct {
	tenant    *Tenant
	directory string
	path      string // Thư mục gốc, ví dụ: /pages/home
	page      string // Thư mục trang con, ví dụ: contact/profile
	index     string // File shell chính, mặc định là index
	layout    Layout
	global    value.Value // Dữ liệu dùng chung cho mọi bản render
	notfound  string
	// notfoundMode: render the notfound page for {{_page_}} (not the path's own page).
	// Set by ctx.view on a router.notfound("*") route so an unregistered path always
	// shows the not-found page — never a page that merely happens to exist on disk.
	notfoundMode bool

	jitCSS    bool     // .jit(): inject server-side Tailwind/utility CSS for the page's classes
	minify    []string // .minify(): content types to minify on the final HTML output
	minifySet bool     // whether minify was set explicitly (else default by environment)
}

type Layout struct {
	navbar  string
	footer  string
	head    string
	sidebar string
	tabbar  string
	subbar  string
	toolbar string
}

func (r *Render) New(dir ...string) *Render {
	newRender := *r
	if len(dir) > 0 && dir[0] != "" {
		newRender.directory = dir[0]
	}
	return &newRender
}

func (r *Render) Global(val value.Value) *Render {
	r.global = val
	return r
}

// Jit enables the server-side Tailwind/utility JIT for this render: a <style> with the
// minimal CSS for the page's classes is injected before </head>. Opt-in — `render.jit()`
// (or `render.jit(false)` to disable). Replaces the client-side Tailwind CDN.
func (r *Render) Jit(vals ...value.Value) *Render {
	r.jitCSS = len(vals) == 0 || vals[0].Truthy()
	return r
}

// Minify configures minification of the final HTML output (collapse whitespace, drop comments,
// minify inline CSS/JS/JSON/SVG). Calling it marks the choice EXPLICIT, overriding the
// environment default (see shouldMinify):
//
//	render.minify()              // on, all types
//	render.minify(false)         // off
//	render.minify("css", "js")   // on, explicit subset by type name
func (r *Render) Minify(vals ...value.Value) *Render {
	r.minifySet = true
	switch {
	case len(vals) == 0:
		r.minify = minifier.AllTypes
	case vals[0].IsBool():
		if vals[0].Truthy() {
			r.minify = minifier.AllTypes
		} else {
			r.minify = nil
		}
	default:
		r.minify = nil
		for _, v := range vals {
			r.minify = append(r.minify, v.String())
		}
	}
	return r
}

// shouldMinify decides whether to minify this render. If .minify() / context({minify}) set it
// explicitly, honor that. Otherwise default BY ENVIRONMENT: OFF on local dev (AllowLocal) for
// readable output, ON for a real server for compact output.
func (r *Render) shouldMinify() bool {
	if r.minifySet {
		return len(r.minify) > 0
	}
	return !AllowLocal
}

// CSS returns the tenant's site-wide JIT CSS as a STRING — the same minified, mtime-cached
// stylesheet router.jit() serves at /jitcss. Use it to serve the CSS from YOUR OWN handler
// with full control over path, caching and headers:
//
//	router.get("/styles.css").static("1h").handle((req, res) => res.css(render.css()))
//	router.get("/styles.css").cache("1h").handle((req, res) => res.css(render.css()))
//
// Pair with res.css(...) / ctx.css(...) so the response goes out as text/css.
func (r *Render) CSS() value.Value {
	css, _ := tenantJITCSS(r.tenant)
	return value.New(css)
}

func (r *Render) Layout(val value.Value) *Render {

	if val.IsString() {
		path := r.tenant.resolve(val.String())
		layouts, err := os.ReadDir(path)
		if err == nil {
			for _, layout := range layouts {
				if layout.IsDir() {
					continue
				}
				name := layout.Name()
				// Lưu cả tên có đuôi và không đuôi để dễ truy cập
				baseName := strings.TrimSuffix(name, ".kitwork.html")
				fullPath := filepath.Join(path, name)
				switch baseName {
				case "navbar":
					r.layout.navbar = fullPath
				case "footer":
					r.layout.footer = fullPath
				case "head":
					r.layout.head = fullPath
				case "sidebar":
					r.layout.sidebar = fullPath
				case "toolbar":
					r.layout.toolbar = fullPath
				case "tabbar":
					r.layout.tabbar = fullPath
				case "subbar":
					r.layout.subbar = fullPath
				}
			}
		}
		return r
	}

	if val.IsMap() {
		for k, v := range val.Map() {
			pathVal := r.tenant.resolve(r.dir(), r.getfile(v.String()))
			if k == "navbar" {
				r.layout.navbar = pathVal
			} else if k == "footer" {
				r.layout.footer = pathVal
			} else if k == "head" {
				r.layout.head = pathVal
			} else if k == "sidebar" {
				r.layout.sidebar = pathVal
			} else if k == "toolbar" {
				r.layout.toolbar = pathVal
			} else if k == "tabbar" {
				r.layout.tabbar = pathVal
			} else if k == "subbar" {
				r.layout.subbar = pathVal
			}
		}
	}
	return r
}

func (r *Render) Template(vals ...value.Value) *Render {
	if len(vals) == 0 {
		return r
	}

	arg := vals[0].String()
	if arg == "" {
		return r
	}

	// Nếu truyền vào file (có extension), tách lấy thư mục
	if filepath.Ext(arg) != "" {
		r.path = filepath.Dir(arg)
	} else {
		r.path = arg
	}

	return r
}

func (r *Render) Index(vals ...value.Value) *Render {
	newRender := *r
	if len(vals) > 0 {
		newRender.index = vals[0].String()
	}
	return &newRender
}

func (r *Render) getIndexPath() string {
	// Explicit index override: keep the old direct-file / directory behavior.
	if r.index != "" {
		path1 := r.pathJoin(r.path, r.index, r.getfile("index"))
		if _, err := os.Stat(path1); err == nil {
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
		if _, err := os.Stat(candidate); err == nil {
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
		if p := r.pathJoin("", name, r.getfile("index")); fileExists(p) {
			return p // directory form: <name>/index.kitwork.html
		}
		return r.pathJoin("", r.getfile(name)) // direct file: <name>.kitwork.html
	}

	// Otherwise: walk UP from the page's folder to the NEAREST notfound — the same nested
	// resolution the shell (index) uses. So /docs/routing falls back to docs/notfound, then the
	// root notfound. No declaration needed; .notfound("name") only changes the filename to look for.
	folder := path.Join("/", r.path, r.page)
	for {
		if p := r.pathJoin(folder, r.getfile(name)); fileExists(p) {
			return p // direct file: <folder>/notfound.kitwork.html
		}
		if p := r.pathJoin(folder, name, r.getfile("index")); fileExists(p) {
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
	return r.tenant.resolve(r.dir(), path)
}

func (r *Render) Directory(vals ...value.Value) *Render {
	if len(vals) > 0 {
		r.directory = vals[0].String()
	}
	return r
}

func (r *Render) dir() string {
	if r.directory == "" {
		r.directory = "views"
	}
	return r.directory
}

func (r *Render) Path(vals ...value.Value) *Render {
	newRender := *r
	if len(vals) > 0 {
		newRender.path = vals[0].String()
	}
	return &newRender
}

func (r *Render) NotFound(vals ...value.Value) *Render {
	if len(vals) > 0 {
		r.notfound = vals[0].String()
	} else {
		r.notfound = "notfound"
	}
	return r
}

func (r *Render) Page(vals ...value.Value) *Render {
	if len(vals) > 0 {
		r.page = vals[0].String()
	}
	return r
}

func (r *Render) tmpl(data any) string {
	// 1. GIAI ĐOẠN ASSEMBLY: Ráp nối các file mẫu thành một template lớn duy nhất
	// Bắt đầu từ file Shell (index.html)
	indexPath := r.getIndexPath()
	shellRaw, err := os.ReadFile(indexPath)
	if err != nil {
		return fmt.Sprintf("[Error reading index: %v]", indexPath)
	}

	// Đệ quy nạp các thành phần lồng nhau (layouts, includes, page)
	fullTemplate := r.assemble(string(shellRaw), filepath.Dir(indexPath), 0)

	// 2. GIAI ĐOẠN BIND: Render dữ liệu vào các biến
	scope := make(map[string]value.Value)

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
	tokens := specializeTokens(fullTemplate)
	prog := parse(tokens)
	out := eval(prog, data, scope)

	// 3. JIT CSS (opt-in via .jit()): sinh CSS tối thiểu cho đúng các class trang dùng
	// (Tailwind + hệ industrial), nhét <style> trước </head>. Thay CDN client-side;
	// cache theo tập class nên gần như miễn phí sau lần đầu.
	if r.jitCSS {
		if css := jitcss.GenerateJITCached(out); css != "" {
			if strings.Contains(css, "animation:") {
				css = jitcss.AnimKeyframes + "\n" + css // keyframes for animate-* utilities
			}
			style := "<style data-kitwork-jit>\n" + css + "</style>"
			if i := strings.LastIndex(out, "</head>"); i >= 0 {
				out = out[:i] + style + out[i:]
			} else {
				out = style + out
			}
		}
	}

	// 3b. JIT service mode (router.jit()): link the shared, cached stylesheet once — no
	// <head> edit needed. Two guards keep the <link> honest: (a) only inject when a LIVE jit
	// route still serves jitRoute, so a router.get(jitRoute) that shadowed it never leaves
	// pages pointing at a non-CSS response; (b) skip when the page already carries a
	// stylesheet link to jitRoute — idempotent for hand-placed links, without the false
	// match an unrelated <a href="…"> would cause.
	if r.tenant != nil && r.tenant.jitInject && r.tenant.jitRoute != "" && r.tenant.routes != nil {
		if rt, _ := r.tenant.routes.Match("GET", r.tenant.jitRoute); rt != nil && rt.isJIT {
			route := r.tenant.jitRoute
			already := strings.Contains(out, `rel="stylesheet" href="`+route+`"`) ||
				strings.Contains(out, `rel='stylesheet' href='`+route+`'`)
			if !already {
				link := `<link rel="stylesheet" href="` + route + `">`
				if i := strings.LastIndex(out, "</head>"); i >= 0 {
					out = out[:i] + link + out[i:]
				} else {
					out = link + out
				}
			}
		}
	}

	// 4. Minify (opt-in via .minify()): gọn HTML + CSS/JS nội tuyến (giữ nguyên
	// pre/textarea/script). HTML minify tự lan vào <style>/<script> bên trong.
	if r.shouldMinify() {
		out = minifier.HTML(out)
	}
	return out
}

// assemble thực hiện quét template và nạp các thành phần thô một cách đệ quy
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
				if raw, err := os.ReadFile(pagePath); err == nil {

					sb.WriteString(r.assemble(string(raw), filepath.Dir(pagePath), depth+1))
				} else {

					nfPath := r.getNotFoundPath()
					if raw, err := os.ReadFile(nfPath); err == nil {

						sb.WriteString(r.assemble(string(raw), filepath.Dir(nfPath), depth+1))
					} else {
						sb.WriteString(fmt.Sprintf("<!-- 404: %v -->", pagePath))
					}

				}

			case "_navbar_", "_footer_", "_head_", "_sidebar_", "_toolbar_", "_tabbar_", "_subbar_":
				found := false

				// A. Thử tìm trong Layout Map (ưu tiên nạp từ RAM nếu có)
				var pathVal string
				switch cmd {
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
				}
				if pathVal != "" {
					if raw, err := os.ReadFile(pathVal); err == nil {
						sb.WriteString(r.assemble(string(raw), filepath.Dir(pathVal), depth+1))
						found = true
					}
				}

				// B. Walk UP from the current dir to the render root, so a nested-section
				// shell (e.g. app/docs/index) finds shared partials that live higher up
				// (app/_navbar_) — the same walk-up that resolves the shell itself. This
				// is what makes a render work with NO layout map (zero-config).
				if !found {
					root := filepath.Clean(r.tenant.resolve(r.dir()))
					dir := filepath.Clean(currentDir)
					for {
						for _, fname := range slotFiles(base) {
							fullPath := filepath.Join(dir, fname)
							if raw, err := os.ReadFile(fullPath); err == nil {
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
						globalPath := r.tenant.resolve("views", fname)
						if raw, err := os.ReadFile(globalPath); err == nil {
							sb.WriteString(r.assemble(string(raw), filepath.Dir(globalPath), depth+1))
							found = true
							break
						}
					}
				}

				if !found {
					sb.WriteString(fmt.Sprintf("<!-- Missing: %v -->", base+".kitwork.html"))
				}

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
		if _, err := os.Stat(r.pathJoin(r.path, fname)); err == nil {
			return true
		}
	}
	return false
}

func (r *Render) Exists(name string) bool {
	// Trường hợp 1: Kiểm tra thư mục con chứa page.kitwork.html (Ví dụ: routing/page.kitwork.html)
	path1 := r.pathJoin(r.path, name, r.getfile("page"))
	if _, err := os.Stat(path1); err == nil {
		return true
	}

	// Trường hợp 2: Kiểm tra file trực tiếp (Ví dụ: routing.kitwork.html)
	path2 := r.pathJoin(r.path, r.getfile(name))
	if _, err := os.Stat(path2); err == nil {
		return true
	}

	return false
}

func (r *Render) Bind(data value.Value) value.Value {
	return value.New(r.tmpl(data))
}

// Render service entry point
// kitwork().render(...) -> Template
// kitwork().render.file(...) -> Service call

// HTML renders a raw template string with data
func (r *Render) HTML(tmpl string, data any) string {
	viewDir := r.tenant.resolve("views")
	return engineRender(tmpl, data, viewDir, viewDir)
}

// File renders a file from the 'views' directory
// func (r *Render) File(name string, data any) string {
// 	path := r.tenant.resolve("views", name)
// 	if filepath.Ext(path) == "" {
// 		path += ".html"
// 	}

// 	content, err := os.ReadFile(path)
// 	if err != nil {
// 		return "Render Error: file not found at " + path
// 	}

// 	viewDir := filepath.Dir(path)
// 	globalDir := r.tenant.resolve("views")

// 	return engineRender(string(content), data, viewDir, globalDir)
// }

// ----------------------------------------------------------------------------
// TEMPLATE ENGINE CORE
// ----------------------------------------------------------------------------

func engineRender(tmpl string, data any, viewDir string, globalDir string) string {
	tokens := specializeTokens(tmpl)
	node := parse(tokens)

	initialScope := make(map[string]value.Value)
	valData := value.New(data)
	initialScope["$"] = valData
	initialScope["__view_dir"] = value.New(viewDir)
	initialScope["__global_view_dir"] = value.New(globalDir)

	if valData.IsMap() {
		for k, v := range valData.Map() {
			initialScope[k] = v
		}
	}

	return eval(node, data, initialScope)
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
	keyVar      string   // "i" in range i, v := list
	valVar      string   // "v" in range i, v := list
	children    []*node
	alt         []*node // Else block
	parsingElse bool    // Parsing state
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
				n := &node{typ: nodeIf, val: parts[1]}
				if len(parts) > 2 {
					n.args = parts[2:]
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
				addChild(current, n)
				stack = append(stack, n)

			case "let":
				if len(parts) >= 4 && parts[2] == "=" {
					n := &node{typ: nodeLet, keyVar: parts[1], val: parts[3]}
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
				n := &node{typ: nodeVar, val: content}
				addChild(current, n)
			}
		} else {
			n := &node{typ: nodeText, val: t}
			addChild(current, n)
		}
	}
	return root
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
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("[Render Panic] %v\n", r)
			out = ""
		}
	}()

	var sb strings.Builder

	switch n.typ {
	case nodeRoot:
		return renderChildren(n.children, data, scope)

	case nodeText:
		return n.val

	case nodeVar:
		return resolveVar(n.val, data, scope)

	case nodeIf:
		var truthy bool
		var val value.Value

		if len(n.args) >= 4 && n.args[0] == "%" {
			val = resolveValue(n.val, data, scope)
			if val.IsNumeric() {
				var modBy float64
				var target float64
				fmt.Sscanf(n.args[1], "%f", &modBy)
				fmt.Sscanf(n.args[3], "%f", &target)
				op := n.args[2]
				if modBy != 0 {
					current := int(val.Float())
					rem := current % int(modBy)
					switch op {
					case "==":
						truthy = (rem == int(target))
					case "!=":
						truthy = (rem != int(target))
					}
				}
			}
		} else {
			val = resolveValue(n.val, data, scope)
			truthy = val.Truthy()
			if len(n.args) >= 2 {
				op := n.args[0]
				targetRaw := strings.Trim(n.args[1], `"'`)
				if val.IsNumeric() {
					var targetNum float64
					if _, err := fmt.Sscanf(targetRaw, "%f", &targetNum); err == nil {
						currentNum := val.Float()
						switch op {
						case "==":
							truthy = (currentNum == targetNum)
						case "!=":
							truthy = (currentNum != targetNum)
						case ">":
							truthy = (currentNum > targetNum)
						case "<":
							truthy = (currentNum < targetNum)
						case ">=":
							truthy = (currentNum >= targetNum)
						case "<=":
							truthy = (currentNum <= targetNum)
						}
					}
				} else {
					strVal := val.String()
					switch op {
					case "==":
						truthy = (strVal == targetRaw)
					case "!=":
						truthy = (strVal != targetRaw)
					}
				}
			}
		}

		if truthy {
			sb.WriteString(renderChildren(n.children, data, scope))
		} else {
			sb.WriteString(renderChildren(n.alt, data, scope))
		}

	case nodeRange:
		val := resolveValue(n.val, data, scope)
		if val.IsArray() {
			arr := val.Array()
			for i, item := range arr {
				newScope := copyMap(scope)
				if n.keyVar != "" {
					newScope[n.keyVar] = value.New(i)
				}
				if n.valVar != "" {
					newScope[n.valVar] = item
				}
				sb.WriteString(renderChildren(n.children, item, newScope))
			}
		} else if val.IsMap() {
			m := val.Map()
			for k, v := range m {
				newScope := copyMap(scope)
				if n.keyVar != "" {
					newScope[n.keyVar] = value.New(k)
				}
				if n.valVar != "" {
					newScope[n.valVar] = v
				}
				sb.WriteString(renderChildren(n.children, v, newScope))
			}
		}

	case nodeLet:
		val := resolveValue(n.val, data, scope)
		if scope != nil {
			scope[n.keyVar] = val
		}

	case nodePartial:
		viewDir := ""
		if v, ok := scope["__view_dir"]; ok {
			viewDir = v.String()
		}
		fname := n.val
		if !strings.HasSuffix(fname, ".html") {
			fname += ".html"
		}

		// Thử tìm trong __layouts map trước (ưu tiên Fluent Layouts)
		if lMapVal, ok := scope["__layouts"]; ok && lMapVal.IsMap() {
			if pathVal, ok := lMapVal.Map()[fname]; ok {
				content, err := os.ReadFile(pathVal.String())
				if err == nil {
					tokens := specializeTokens(string(content))
					prog := parse(tokens)
					newScope := copyMap(scope)
					newScope["__view_dir"] = value.New(filepath.Dir(pathVal.String()))
					return eval(prog, data, newScope)
				}
			}
			// Thử tìm theo tên không đuôi
			nameOnly := strings.TrimSuffix(fname, ".html")
			if pathVal, ok := lMapVal.Map()[nameOnly]; ok {
				content, err := os.ReadFile(pathVal.String())
				if err == nil {
					tokens := specializeTokens(string(content))
					prog := parse(tokens)
					newScope := copyMap(scope)
					newScope["__view_dir"] = value.New(filepath.Dir(pathVal.String()))
					return eval(prog, data, newScope)
				}
			}
		}

		fullPath := filepath.Join(viewDir, fname)
		content, err := os.ReadFile(fullPath)
		if err != nil {
			if globalVal, ok := scope["__global_view_dir"]; ok {
				fallbackDir := globalVal.String()
				fullPath = filepath.Join(fallbackDir, fname)
				content, err = os.ReadFile(fullPath)
			}
			if err != nil {
				return fmt.Sprintf("[Error: %v]", err)
			}
		}

		tokens := specializeTokens(string(content))
		prog := parse(tokens)
		newScope := copyMap(scope)
		newScope["__view_dir"] = value.New(filepath.Dir(fullPath))

		return eval(prog, data, newScope)
	}
	return sb.String()
}

func renderChildren(nodes []*node, data any, scope map[string]value.Value) string {
	var sb strings.Builder
	for _, n := range nodes {
		sb.WriteString(eval(n, data, scope))
	}
	return sb.String()
}

func copyMap(src map[string]value.Value) map[string]value.Value {
	dst := make(map[string]value.Value)
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func resolveVar(rawKey string, data any, scope map[string]value.Value) string {
	if strings.HasPrefix(rawKey, "raw(") && strings.HasSuffix(rawKey, ")") {
		innerKey := rawKey[4 : len(rawKey)-1]
		val := resolveValue(innerKey, data, scope)
		return val.String()
	}

	if rawKey == "$" || strings.HasPrefix(rawKey, "$.") {
		return html.EscapeString(resolveValue(rawKey, data, scope).String())
	}

	val := resolveValue(rawKey, data, scope)
	return html.EscapeString(val.String())
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

func resolveValue(path string, data any, scope map[string]value.Value) value.Value {
	path = strings.TrimSpace(path)
	if (strings.HasPrefix(path, `"`) && strings.HasSuffix(path, `"`)) ||
		(strings.HasPrefix(path, `'`) && strings.HasSuffix(path, `'`)) {
		return value.New(path[1 : len(path)-1])
	}
	if val, err := strconv.ParseFloat(path, 64); err == nil {
		return value.New(val)
	}

	// 1. Ternary (cond ? true : false)
	qIdx := findSplitIndex(path, func(i int) bool { return path[i] == '?' }, false)
	if qIdx > -1 && (qIdx+1 >= len(path) || path[qIdx+1] != '?') {
		remainder := path[qIdx+1:]
		cIdxRel := findSplitIndex(remainder, func(i int) bool { return remainder[i] == ':' }, false)
		if cIdxRel > -1 {
			cIdx := qIdx + 1 + cIdxRel
			cond := resolveValue(path[:qIdx], data, scope)
			if cond.Truthy() {
				return resolveValue(path[qIdx+1:cIdx], data, scope)
			}
			return resolveValue(path[cIdx+1:], data, scope)
		}
	}

	// 2. Logic & Null Coalescing
	if idx := findSplitIndex(path, func(i int) bool {
		return path[i] == '?' && i+1 < len(path) && path[i+1] == '?'
	}, false); idx > -1 {
		left := resolveValue(path[:idx], data, scope)
		if left.IsBlank() {
			return resolveValue(path[idx+2:], data, scope)
		}
		return left
	}

	if idx := findSplitIndex(path, func(i int) bool {
		return path[i] == '|' && i+1 < len(path) && path[i+1] == '|'
	}, false); idx > -1 {
		left := resolveValue(path[:idx], data, scope)
		if !left.Truthy() {
			return resolveValue(path[idx+2:], data, scope)
		}
		return left
	}

	if idx := findSplitIndex(path, func(i int) bool {
		return path[i] == '&' && i+1 < len(path) && path[i+1] == '&'
	}, false); idx > -1 {
		left := resolveValue(path[:idx], data, scope)
		if left.Truthy() {
			return resolveValue(path[idx+2:], data, scope)
		}
		return left
	}

	// 3. Comparisons & Basic Arithmetic
	ops := []string{"==", "!=", ">=", "<=", ">", "<", "+", "-", "*", "/", "%"}
	for _, op := range ops {
		if idx := findSplitIndex(path, func(i int) bool {
			return strings.HasPrefix(path[i:], op)
		}, true); idx > 0 {
			left := resolveValue(path[:idx], data, scope)
			right := resolveValue(path[idx+len(op):], data, scope)
			switch op {
			case "==":
				return value.New(left.Equal(right))
			case "!=":
				return value.New(!left.Equal(right))
			case ">=":
				return value.New(left.GreaterEqual(right))
			case "<=":
				return value.New(left.LessEqual(right))
			case ">":
				return value.New(left.Greater(right))
			case "<":
				return value.New(left.Less(right))
			case "+":
				return left.Add(right)
			case "-":
				return left.Sub(right)
			case "*":
				return left.Mul(right)
			case "/":
				return left.Div(right)
			case "%":
				return left.Mod(right)
			}
		}
	}

	// 4. variable lookup
	var current value.Value
	if v, ok := data.(value.Value); ok {
		current = v
	} else {
		current = value.New(data)
	}

	if path == "." {
		return current
	}
	if strings.HasPrefix(path, ".") {
		return traverse(current, strings.Split(strings.TrimPrefix(path, "."), "."))
	}

	parts := strings.Split(path, ".")
	if val, ok := scope[parts[0]]; ok {
		if len(parts) > 1 {
			return traverse(val, parts[1:])
		}
		return val
	}

	res := traverse(current, parts)
	if !res.IsNil() {
		return res
	}
	if strings.HasPrefix(parts[0], "$") {
		parts[0] = strings.TrimPrefix(parts[0], "$")
		return traverse(current, parts)
	}
	return res
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
