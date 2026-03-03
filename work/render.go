package work

import (
	"fmt"
	"html"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kitwork/engine/value"
)

// ----------------------------------------------------------------------------
// RENDER SERVICE & FLUENT API
// ----------------------------------------------------------------------------

func (w *KitWork) Render(args ...value.Value) *Render {
	r := &Render{
		tenant: w.tenant,
		layout: make(map[string]string),
	}
	if len(args) > 0 {
		r.Template(args...)
	}
	return r
}

type Render struct {
	tenant *Tenant
	path   string // Thư mục gốc, ví dụ: /pages/home
	page   string // Thư mục trang con, ví dụ: contact/profile
	layout map[string]string
}

func (r *Render) Layout(val value.Value) *Render {
	if r.layout == nil {
		r.layout = make(map[string]string)
	}

	if val.IsString() {
		path := r.tenant.joinPath(val.String())
		layouts, err := os.ReadDir(path)
		if err == nil {
			for _, layout := range layouts {
				if layout.IsDir() {
					continue
				}
				name := layout.Name()
				// Lưu cả tên có đuôi và không đuôi để dễ truy cập
				r.layout[name] = filepath.Join(path, name)
				if ext := filepath.Ext(name); ext != "" {
					r.layout[strings.TrimSuffix(name, ext)] = filepath.Join(path, name)
				}
			}
		}
		return r
	}

	if val.IsMap() {
		for k, v := range val.Map() {
			r.layout[k] = v.String()
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

func (r *Render) getIndexPath() string {
	// r.index bây giờ chỉ lưu tên file, r.path lưu thư mục
	return r.tenant.joinPath(path.Join(r.path, "index.html"))
}

func (r *Render) getPagePath() string {
	// Kết quả: path + page_name + page.html
	return r.tenant.joinPath(path.Join(r.path, r.page, "page.html"))
}

func (r *Render) getNotFoundPath() string {
	// Kết quả: path + page_name + page.html
	return r.tenant.joinPath(path.Join(r.path, "notfound.html"))
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

	// 2. GIAI ĐOẠN BIND: Render dữ liệu vào template "phẳng" đã ráp nối xong
	scope := make(map[string]value.Value)

	valData := value.New(data)
	scope["$"] = valData
	if valData.IsMap() {
		for k, v := range valData.Map() {
			scope[k] = v
		}
	}

	// Parse và Eval một lần duy nhất cho toàn bộ cây mẫu
	tokens := specializeTokens(fullTemplate)
	prog := parse(tokens)
	return eval(prog, data, scope)
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
			switch cmd {
			case "page", "body", "content":
				// Nạp trang con động
				pagePath := r.getPagePath()
				if raw, err := os.ReadFile(pagePath); err == nil {
					sb.WriteString(r.assemble(string(raw), filepath.Dir(pagePath), depth+1))
				} else {
					// Fallback sang notfound
					nfPath := r.getNotFoundPath()
					if raw, err := os.ReadFile(nfPath); err == nil {
						sb.WriteString(r.assemble(string(raw), filepath.Dir(nfPath), depth+1))
					} else {
						sb.WriteString(fmt.Sprintf("<!-- 404: %v -->", pagePath))
					}
				}

			case "layout", "include":
				if len(parts) < 2 {
					sb.WriteString(t)
					continue
				}
				fname := strings.Trim(parts[1], `"'`)
				if !strings.HasSuffix(fname, ".html") {
					fname += ".html"
				}

				found := false
				// A. Thử tìm trong layout map (từ r.layout)
				if pathVal, ok := r.layout[fname]; ok {
					if raw, err := os.ReadFile(pathVal); err == nil {
						sb.WriteString(r.assemble(string(raw), filepath.Dir(pathVal), depth+1))
						found = true
					}
				} else if pathVal, ok := r.layout[strings.TrimSuffix(fname, ".html")]; ok {
					if raw, err := os.ReadFile(pathVal); err == nil {
						sb.WriteString(r.assemble(string(raw), filepath.Dir(pathVal), depth+1))
						found = true
					}
				}

				// B. Nếu chưa thấy, thử tìm file tương đối trong thư mục hiện tại
				if !found {
					fullPath := filepath.Join(currentDir, fname)
					if raw, err := os.ReadFile(fullPath); err == nil {
						sb.WriteString(r.assemble(string(raw), filepath.Dir(fullPath), depth+1))
						found = true
					}
				}

				// C. Cuối cùng thử tìm trong thư mục views global
				if !found {
					globalPath := r.tenant.joinPath("views", fname)
					if raw, err := os.ReadFile(globalPath); err == nil {
						sb.WriteString(r.assemble(string(raw), filepath.Dir(globalPath), depth+1))
						found = true
					}
				}

				if !found {
					sb.WriteString(fmt.Sprintf("<!-- Missing partial: %v -->", fname))
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

func (r *Render) Bind(data value.Value) value.Value {
	return value.New(r.tmpl(data))
}

// Render service entry point
// kitwork().render(...) -> Template
// kitwork().render.file(...) -> Service call

// HTML renders a raw template string with data
func (r *Render) HTML(tmpl string, data any) string {
	viewDir := r.tenant.joinPath("views")
	return engineRender(tmpl, data, viewDir, viewDir)
}

// File renders a file from the 'views' directory
func (r *Render) File(name string, data any) string {
	path := r.tenant.joinPath("views", name)
	if filepath.Ext(path) == "" {
		path += ".html"
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return "Render Error: file not found at " + path
	}

	viewDir := filepath.Dir(path)
	globalDir := r.tenant.joinPath("views")

	return engineRender(string(content), data, viewDir, globalDir)
}

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
					case "if", "else", "elseif", "end", "for", "let", "include", "layout":
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
	if last {
		for i := len(s) - 1; i >= 0; i-- {
			if s[i] == ')' {
				level++
			}
			if s[i] == '(' {
				level--
			}
			if level == 0 && checkFn(i) {
				return i
			}
		}
	} else {
		for i := 0; i < len(s); i++ {
			if s[i] == '(' {
				level++
			}
			if s[i] == ')' {
				level--
			}
			if level == 0 && checkFn(i) {
				return i
			}
		}
	}
	return -1
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
