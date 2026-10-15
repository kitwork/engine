package ui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Inside a Kitwork component the kernel runs (appearance, dropdown) the page is live before the page
// itself is: the component's state and methods are in the kernel's component file, so what reads only
// the component's names runs in the browser. The engine writes it in the grouped web form
// (KITWORK_UI §11): data-kitwork-bind for the values that follow the component's state,
// data-kitwork-class for the classes it turns on and off, data-kitwork-event for the events. What
// reads only the page's data stays as the server printed it; a mix of the two waits for the page to
// run on the client (step 2) and is reported, not half done.

// frame is one level of what a bare name means where an expression stands: a component instance
// (the names it declares), the rows of a for (its loop variables) or the inputs a layout call passes.
// Names resolve through the chain nearest first, then reach the route's view (KITWORK_UI §13), the way
// the kernel resolves them.
type frame struct {
	// component is the instance this frame is, nil for the rows of a for and a layout's inputs
	component *componentDef
	// what names a frame that is no component in a message: "" for the rows of a for
	what  string
	names map[string]bool
	// file is the component's own .ui (site-relative): inside it the component's names win without
	// question; anywhere else (what the author writes inside a host) a name the component and an outer
	// level both declare is a warning, and the nearest wins (host.go)
	file string
	// client: the kernel runs the component, so its names are live in the browser
	client bool
	parent *frame
}

func componentFrame(def *componentDef, parent *frame) *frame {
	f := &frame{component: def, names: def.names(), client: def.delivered(), parent: parent}
	if def.root != nil {
		f.file = def.root.File
	}
	return f
}

func rowFrame(each *forSpec, parent *frame) *frame {
	names := map[string]bool{each.item: true}
	if each.index != "" {
		names[each.index] = true
	}
	return &frame{names: names, parent: parent}
}

// inputFrame is what a layout call passes in: inside the layout's file those names are the call's
// values, whatever a level around declares, the way the server binds them (render.go). They are the
// server's: a layout has no instance in the browser to hold them.
func inputFrame(layout string, inputs []binding, parent *frame) *frame {
	names := make(map[string]bool, len(inputs))
	for _, b := range inputs {
		names[b.name] = true
	}
	return &frame{what: "đầu vào của " + layout, names: names, parent: parent}
}

// declaring is the nearest frame that declares name, nil when only the page can.
func (f *frame) declaring(name string) *frame {
	for s := f; s != nil; s = s.parent {
		if s.names[name] {
			return s
		}
	}
	return nil
}

// live is the nearest component the browser runs: nil when nothing around an element is live yet.
func (f *frame) live() *frame {
	for s := f; s != nil; s = s.parent {
		if s.client {
			return s
		}
	}
	return nil
}

// label names a frame in a message.
func (f *frame) label() string {
	if f.component != nil {
		return f.component.name
	}
	if f.what != "" {
		return f.what
	}
	return "for"
}

// within says where an expression stands, for a message: "bên trong dropdown" inside a component the
// kernel runs, "ngoài mọi component" outside every one (where only what reads a component through its
// alias runs in the browser).
func (f *frame) within() string {
	if live := f.live(); live != nil {
		return "bên trong " + live.label()
	}
	for s := f; s != nil; s = s.parent {
		if s.component != nil {
			return "bên trong " + s.label() // a component whose logic has not reached the browser yet
		}
	}
	return "ngoài mọi component"
}

// readsLive reports whether an interpolation reads a name a component the kernel runs declares (an
// alias alone is not one).
func (f *frame) readsLive(in *interpolation) bool {
	for _, p := range in.parts {
		if p.e == nil {
			continue
		}
		for _, name := range usedNames(p.e.ir) {
			if d := f.declaring(name); d != nil && d.client {
				return true
			}
		}
	}
	return false
}

// inComponent reports whether a component, live or not, is around: nil frames and rows and a layout's
// inputs only are the page.
func (f *frame) inComponent() bool {
	for s := f; s != nil; s = s.parent {
		if s.component != nil {
			return true
		}
	}
	return false
}

// clientGlobals are the names the kernel's expressions read without a scope.
var clientGlobals = map[string]bool{
	"Math": true, "Date": true, "JSON": true, "Number": true, "String": true, "Boolean": true,
	"Array": true, "Object": true, "parseInt": true, "parseFloat": true, "encodeURIComponent": true,
	"decodeURIComponent": true, "isNaN": true, "isFinite": true, "true": true, "false": true, "null": true,
}

// onClient reports whether an expression runs in the browser: it reads the names of a component the
// kernel runs, or a component through its alias ($install.copied, inside a component or outside every
// one: the kernel keeps the page's handles), and nothing the server alone knows (the page's data, a
// loop variable over it, a component whose logic has not reached the browser yet). An expression of
// the server alone stays on the server; one that mixes a live component's names with the page's is an
// error. An alias read with the page's data ($cart.add(product, 1)) stays the server's, as it was.
// That the component an alias names is one the browser runs is checked once every as of the page is
// known (compile, wiredAliases).
func (f *frame) onClient(e *expr, n *Node, a *Attr) (bool, error) {
	own, alias := false, false
	var page []string
	for _, name := range usedNames(e.ir) {
		switch d := f.declaring(name); {
		case d != nil && d.client:
			own = true
		case aliasName(name):
			alias = true // the page's handle on a component instance, known on the client
		case strings.HasPrefix(name, "$") && name != "$", clientGlobals[name]:
			// $event and the kernel's other handles, known on the client
		default:
			page = append(page, name)
		}
	}
	if !own && !alias {
		return false, nil
	}
	if len(page) > 0 {
		if !own {
			return false, nil
		}
		sort.Strings(page)
		message := f.within() + " biểu thức " + e.source + " đọc cả tên của component lẫn " + strings.Join(page, ", ") +
			" của trang; phần chạy ở trình duyệt mới chỉ có tên của component (trang chạy ở client là bước 2)"
		if a != nil {
			return false, errAttr(n, a, "%s", message)
		}
		return false, errAt(n, "%s", message)
	}
	return true, nil
}

// ofServerComponent reports whether every name an expression reads that a frame declares belongs to
// a component whose logic has not reached the browser yet (a site component, step 4): its event is
// that component's, not the page's, and waits for it.
func (f *frame) ofServerComponent(e *expr) bool {
	found := false
	for _, name := range usedNames(e.ir) {
		d := f.declaring(name)
		if d == nil || d.component == nil {
			continue
		}
		if d.client {
			return false
		}
		found = true
	}
	return found
}

// usedNames lists the scope names an expression reads or writes (a lambda's parameters excluded).
func usedNames(ir any) []string {
	seen := map[string]bool{}
	var out []string
	add := func(name string) {
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	var walk func(node any, bound map[string]bool)
	walk = func(node any, bound map[string]bool) {
		parts, ok := node.([]any)
		if !ok || len(parts) == 0 {
			return
		}
		op, isNode := parts[0].(string)
		if !isNode {
			// a list of nodes, not a node: a call's arguments, an array's items, an object's entries.
			// Every one is read; read as a node, the first was taken for its operator and lost
			// ($cart.add(product, 1) read as $cart alone)
			for _, p := range parts {
				walk(p, bound)
			}
			return
		}
		switch op {
		case "#":
			return
		case "$":
			if len(parts) > 1 {
				if name, ok := parts[1].(string); ok && !bound[name] {
					add(name)
				}
			}
			return
		case "=", "++", "--":
			if len(parts) > 1 {
				if name, ok := parts[1].(string); ok && !bound[name] {
					add(name)
				}
			}
			for _, p := range parts[2:] {
				walk(p, bound)
			}
			return
		case "=$", "++$", "--$":
			add("$")
			for _, p := range parts[2:] {
				walk(p, bound)
			}
			return
		case "=>":
			if len(parts) < 3 {
				return
			}
			inner := map[string]bool{}
			for name := range bound {
				inner[name] = true
			}
			if params, ok := parts[1].([]any); ok {
				for _, p := range params {
					if name, ok := p.(string); ok {
						inner[name] = true
					}
				}
			}
			walk(parts[2], inner)
			return
		}
		for _, p := range parts[1:] {
			walk(p, bound)
		}
	}
	walk(ir, map[string]bool{})
	return out
}

// wireEvents turns the events of an element inside a component into data-kitwork-event pairs: the
// part after ':' in the .ui name becomes '.' on the wire (keydown:escape → keydown.escape), and a list
// of events (pointer-enter,focus-in:once) becomes one pair per event.
func (s *frame) wireEvents(n *Node, events []Attr) ([]string, error) {
	var pairs []string
	for i := range events {
		a := &events[i]
		e, err := eventExpression(n, a)
		if err != nil {
			return nil, err
		}
		if s.ofServerComponent(e) {
			continue // the event of a component whose logic reaches the browser in step 4
		}
		live := s.live()
		if live != nil && hasTopLevelSemicolon(e.source) {
			return nil, errAttr(n, a, "%s có nhiều câu lệnh; %s mỗi sự kiện gọi một việc (gộp nhiều câu thành một hàm là bước 2)", a.Name, s.within())
		}
		client, err := s.onClient(e, n, a)
		if err != nil {
			return nil, err
		}
		if !client && live == nil {
			// outside every component an event reaches the browser only through an alias: any other
			// waits for the page to run on the client (KITWORK_UI_DIAGNOSTICS, "Chữ gõ sai đang biến
			// mất im lặng"); a name of a component on the page is told at compile (strayNames)
			continue
		}
		if live == nil && hasTopLevelSemicolon(e.source) {
			return nil, errAttr(n, a, "%s có nhiều câu lệnh; %s mỗi sự kiện gọi một việc (gộp nhiều câu thành một hàm là bước 2)", a.Name, s.within())
		}
		if !client {
			return nil, errAttr(n, a, "bên trong %s sự kiện %s chỉ gọi tên của component (%s); sự kiện của trang chạy ở client là bước 2", live.label(), a.Name, strings.Join(sortedNames(live.names), ", "))
		}
		head, modifiers, _ := strings.Cut(a.Name, ":")
		for _, event := range strings.Split(head, ",") {
			name := strings.TrimSpace(event)
			if modifiers != "" {
				name += "." + modifiers
			}
			pairs = append(pairs, name+": "+e.source)
		}
	}
	return pairs, nil
}

// eventExpression reads an event attribute: its handler, and what follows the ':' of its name, the
// modifiers the kernel runs it through (input:once.debounce(300)). The kernel refuses a handler whose
// modifiers it cannot make sense of, so a modifier it would refuse (click:debounse(300)) is an error
// here, where it is written, inside a component or not: a typo never vanishes.
func eventExpression(n *Node, a *Attr) (*expr, error) {
	e, err := compileExpr(a.Value, n, a)
	if err != nil {
		return nil, err
	}
	head, modifiers, _ := strings.Cut(a.Name, ":")
	for _, event := range strings.Split(head, ",") {
		if problem := modifierProblem(strings.TrimSpace(event), modifiers); problem != "" {
			return nil, errAttr(n, a, "%s: %s", a.Name, problem)
		}
	}
	return e, nil
}

// domEvents are the DOM events the kernel listens for, by the .ui name of each (jit/hydrate
// groupedEventTypes); "" for one the kernel raises itself.
var domEvents = map[string]string{
	"click": "click", "double-click": "dblclick", "context-menu": "contextmenu", "click-outside": "click",
	"keydown": "keydown", "keyup": "keyup", "input": "input", "change": "change", "submit": "submit",
	"focus-in": "focusin", "focus-out": "focusout",
	"pointer-down": "pointerdown", "pointer-up": "pointerup", "pointer-enter": "pointerenter", "pointer-leave": "pointerleave",
	"scroll": "scroll", "load": "load", "error": "error", "toggle": "toggle",
	"intersect-enter": "", "intersect-leave": "", "ready": "",
}

// modifierProblem says what the kernel would refuse in the modifiers of one event, "" when it runs them
// (the rules of jit/hydrate checkModifierPipeline and checkIntersectModifiers, which check the same
// handler on the wire): modifiers follow the ':' joined by '.', each known, none twice.
func modifierProblem(event, modifiers string) string {
	if modifiers == "" {
		return ""
	}
	list := splitModifiers(modifiers)
	for _, m := range list {
		if m == "" {
			return "phần đi kèm rỗng: các phần đi kèm nối nhau bằng một dấu chấm (keydown:escape, input:once.debounce(300))"
		}
	}
	switch event {
	case "ready":
		return "ready chạy một lần khi component bắt đầu sống ở trình duyệt, không có phần đi kèm"
	case "intersect-enter", "intersect-leave":
		seen := map[string]bool{}
		for _, m := range list {
			name := m
			if strings.HasPrefix(m, "margin(") {
				if !strings.HasSuffix(m, ")") || m == "margin()" {
					return m + " cần một khoảng lề CSS, như margin(-20%)"
				}
				name = "margin"
			}
			switch name {
			case "once", "half", "full", "margin":
			default:
				return "không có phần đi kèm " + m + " cho " + event + " (nhận once, half, full, margin(…))"
			}
			if seen[name] {
				return "phần đi kèm " + name + " viết hai lần"
			}
			seen[name] = true
		}
		if seen["half"] && seen["full"] {
			return "half và full là hai ngưỡng (nửa phần tử, cả phần tử); chọn một"
		}
		return ""
	case "click-outside":
		for _, m := range list {
			if m == "outside" {
				return "click-outside đã nghe ở bên ngoài: không viết thêm outside"
			}
		}
		return pipelineProblem("click", append([]string{"outside"}, list...))
	}
	dom, known := domEvents[event]
	if !known {
		dom = event
	}
	return pipelineProblem(dom, list)
}

// splitModifiers cuts modifiers at the dots outside parentheses: once.debounce(300), margin(-2.5rem).
func splitModifiers(s string) []string {
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
		case '.':
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return append(out, s[start:])
}

// keyComboWords are the words a key combination may hold besides its one key (mod is Control, or
// Command on macOS).
var keyComboWords = map[string]bool{"mod": true, "ctrl": true, "meta": true, "shift": true, "alt": true}

// pipelineProblem is the kernel's modifier pipeline for one DOM event: where it listens (window,
// document, outside, self), which key (escape, enter, a combination), what it does to the event
// (prevent, stop), how often (debounce(n), throttle(n), once).
func pipelineProblem(event string, modifiers []string) string {
	seen := map[string]bool{}
	timing := 0
	for _, m := range modifiers {
		name := m
		if open := strings.IndexByte(m, '('); open > 0 && strings.HasSuffix(m, ")") && (m[:open] == "debounce" || m[:open] == "throttle") {
			name = m[:open]
			timing++
			// digits only, as the kernel reads them: debounce(+300) disables the handler there
			ms, err := strconv.Atoi(m[open+1 : len(m)-1])
			if err != nil || ms < 1 || ms > 60000 || !digits(m[open+1:len(m)-1]) {
				return name + "(" + m[open+1:len(m)-1] + "): thời gian chờ là số mili giây từ 1 đến 60000"
			}
		}
		if seen[name] {
			return "phần đi kèm " + name + " viết hai lần"
		}
		seen[name] = true
		if strings.Contains(name, "+") {
			if event != "keydown" && event != "keyup" {
				return name + " là tổ hợp phím, đặt trên keydown hoặc keyup"
			}
			key := ""
			parts := map[string]bool{}
			for _, part := range strings.Split(name, "+") {
				switch {
				case part == "":
					return name + " có một phần rỗng: viết như mod+k"
				case keyComboWords[part]:
					if parts[part] {
						return name + " lặp lại " + part
					}
					parts[part] = true
				case key != "":
					return name + " có hai phím (" + key + " và " + part + "); một tổ hợp bấm một phím"
				default:
					key = part
				}
			}
			if key == "" {
				return name + " chưa có phím: viết như mod+k"
			}
			if parts["mod"] && (parts["ctrl"] || parts["meta"]) {
				return name + " trộn mod với ctrl hay meta: mod đã là phím của nền tảng (Control, Command trên macOS), viết như mod+" + key
			}
			seen["combo"] = true
			continue
		}
		switch name {
		case "window", "document", "outside", "self", "escape", "enter", "prevent", "stop", "once", "debounce", "throttle":
		default:
			return "không có phần đi kèm " + m + " (nhận window, document, outside, self, escape, enter, tổ hợp phím như mod+k, prevent, stop, debounce(…), throttle(…), once)"
		}
	}
	switch {
	case seen["combo"] && (seen["escape"] || seen["enter"]):
		return "tổ hợp phím đã là bộ lọc phím, không đi cùng escape hay enter"
	case (seen["escape"] || seen["enter"]) && event != "keydown" && event != "keyup":
		return "escape và enter lọc một phím, đặt trên keydown hoặc keyup"
	case seen["escape"] && seen["enter"]:
		return "escape và enter không cùng là bộ lọc của một sự kiện"
	case seen["outside"] && event != "click" && event != "dblclick" && event != "pointerdown" && event != "pointerup" && event != "focusin":
		return "outside chỉ đi với click, double-click, pointer-down, pointer-up và focus-in"
	case seen["self"] && (seen["outside"] || seen["window"] || seen["document"]):
		return "self giữ sự kiện ở chính phần tử, không đi cùng outside, window hay document"
	case seen["window"] && seen["document"]:
		return "window và document là hai chỗ nghe; chọn một"
	case timing > 1:
		return "debounce và throttle không cùng hẹn giờ một sự kiện"
	}
	return ""
}

// bindPairs are the data-kitwork-bind pairs of an element inside a component: each value the server
// rendered from the component's state is kept up to date by the kernel. show is one of them: the
// kernel hides with the hidden attribute, the way the server's first paint does (render.go).
func (s *frame) bindPairs(n *Node, o *elemOp) ([]string, error) {
	var pairs []string
	for _, d := range o.dynAttrs {
		source, client, err := s.interpolationOnClient(n, d.in)
		if err != nil {
			return nil, err
		}
		if client {
			pairs = append(pairs, d.name+": "+source)
		}
	}
	if o.showExpr != nil {
		client, err := s.onClient(o.showExpr, n, nil)
		if err != nil {
			return nil, err
		}
		if client {
			pairs = append(pairs, "show: "+o.showExpr.source)
		}
	}
	for _, e := range []*expr{o.ifExpr} {
		if e == nil {
			continue
		}
		if client, err := s.onClient(e, n, nil); err != nil {
			return nil, err
		} else if client {
			return nil, errAt(n, "%s if theo trạng thái của component chưa chạy ở trình duyệt; dùng show", s.within())
		}
	}
	if o.forEach != nil {
		if client, err := s.onClient(o.forEach.list, n, nil); err != nil {
			return nil, err
		} else if client {
			return nil, errAt(n, "%s for theo trạng thái của component chưa chạy ở trình duyệt", s.within())
		}
	}
	return pairs, nil
}

// classPairs are the data-kitwork-class pairs of an element inside a component: a class that follows
// the component's state is written out once for every value its expression spells out, as
// `<the class>: <when it is on>`, and the kernel turns that one class on or off. A class two values
// both make is one pair whose condition names both, so no pair undoes another.
func (s *frame) classPairs(n *Node, o *elemOp) ([]string, error) {
	var order []string
	when := map[string][]string{}
	for _, d := range o.dynClasses {
		source, client, err := s.interpolationOnClient(n, d.in)
		if err != nil {
			return nil, err
		}
		if !client {
			continue
		}
		values := interpolationCandidates(d.in)
		// named as the author wrote it (value="{{ mode }}" on an icon), not by the class it makes
		name, value := d.name, source
		if attrName, attrValue, ok := strings.Cut(d.written, `="`); ok {
			name, value = attrName, strings.TrimSuffix(attrValue, `"`)
		}
		if values == nil {
			return nil, errAt(n, "%s %s=\"%s\" đổi theo trạng thái, nên phải viết ra mọi giá trị (vd. {{ open ? 'x' : 'y' }})", s.within(), name, value)
		}
		for _, value := range values {
			class, _ := d.classOf(value)
			if class == "" {
				continue
			}
			if !classPairName(class) {
				return nil, errAt(n, "%s class %s đổi theo trạng thái, nhưng tên có tiền tố (hover:…) hay ký tự ([ ]) nên trình duyệt chưa bật/tắt được", s.within(), class)
			}
			if _, seen := when[class]; !seen {
				order = append(order, class)
			}
			when[class] = append(when[class], "("+source+") == "+quoteString(value))
		}
	}
	pairs := make([]string, 0, len(order))
	for _, class := range order {
		pairs = append(pairs, class+": "+strings.Join(when[class], " || "))
	}
	return pairs, nil
}

// textPair is the bind pair for an element whose whole content is one value that follows the
// component's state ({{ mode }}); text mixed with other content cannot follow it yet.
func (s *frame) textPair(n *Node, children []op) (string, error) {
	for _, ch := range children {
		t, ok := ch.(*textOp)
		if !ok {
			continue
		}
		source, client, err := s.interpolationOnClient(n, t.in)
		if err != nil {
			return "", err
		}
		if !client {
			continue
		}
		if len(children) != 1 {
			return "", errAt(n, "%s chữ đổi theo trạng thái (%s) phải đứng một mình trong thẻ của nó; bọc nó trong <text>", s.within(), source)
		}
		return "text: " + source, nil
	}
	return "", nil
}

// classPairName reports whether a class can be the name of a data-kitwork-class pair. The grouped form
// cuts a pair at its first ':', and a name is letters, digits, '-', '.', '_', '+', '/' and flat
// parentheses (KITWORK_UI §11; jit/hydrate pairNameError is the reader's twin). So a variant class
// (hover:background-brand) cannot be turned on and off this way; an alpha one (background-paper/80) and
// a glass (material-glass/60, the thin glass since 13/10/2026) can.
func classPairName(class string) bool {
	if class == "" {
		return false
	}
	open := false
	for i := 0; i < len(class); i++ {
		switch c := class[i]; {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '.', c == '_', c == '+', c == '/':
		case c == '(' && !open:
			open = true
		case c == ')' && open:
			open = false
		default:
			return false
		}
	}
	return !open
}

// interpolationOnClient is the expression an interpolation becomes on the client — one {{ }} as it is,
// text around it joined with + — and whether it runs there.
func (s *frame) interpolationOnClient(n *Node, in *interpolation) (string, bool, error) {
	if in == nil {
		return "", false, nil
	}
	var parts []string
	client := false
	for _, p := range in.parts {
		if p.e == nil {
			if p.text != "" {
				parts = append(parts, quoteString(p.text))
			}
			continue
		}
		on, err := s.onClient(p.e, n, nil)
		if err != nil {
			return "", false, err
		}
		client = client || on
		parts = append(parts, "("+p.e.source+")")
	}
	if e := in.single(); e != nil {
		return e.source, client, nil
	}
	return strings.Join(parts, " + "), client, nil
}

// quoteString writes a string literal for an expression: single quotes, the way the grammar's strings
// are written in markup.
func quoteString(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}

// hasTopLevelSemicolon reports a ';' outside strings and brackets: two statements, which the grouped
// form would read as two pairs.
func hasTopLevelSemicolon(source string) bool {
	depth := 0
	var quote byte
	for i := 0; i < len(source); i++ {
		c := source[i]
		if quote != 0 {
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"', '`':
			quote = c
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case ';':
			if depth == 0 {
				return true
			}
		}
	}
	return false
}

func sortedNames(names map[string]bool) []string {
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// runtimePairs are the grouped attributes of an element inside a component the kernel runs (or of
// its host).
type runtimePairs struct {
	binds, classes, events []string
}

// runtimeAttributes reads the runtime pairs of an element inside a component the kernel runs (or of
// its host): text is added by the caller once the children are compiled. A popover shows while the
// component around it is open: one that declares open and close, run by the kernel. Outside every
// component the kernel runs, only what reads a component through its alias and nothing else of the
// page runs in the browser ($install.copy(), {{ $install.copied ? 'Copied' : 'Copy' }}: decision T2,
// 12/10/2026); any other event reaches no browser yet (KITWORK_UI_DIAGNOSTICS, "Chữ gõ sai đang biến
// mất im lặng"), but what is written is still read: an expression that does not parse is an error,
// never dropped unread (wireEvents reads every event first).
func (c *compiler) runtimeAttributes(n *Node, p *attrPlan, o *elemOp, scope *frame) (runtimePairs, error) {
	var pairs runtimePairs
	if n.Tag == "popover" {
		owner := scope.declaring("open")
		if owner == nil || owner.component == nil || !owner.client || !owner.names["close"] {
			return pairs, errAt(n, "popover hiện khi component chứa nó mở: đặt nó trong thẻ chủ của một component có open và close (<box component=\"dropdown\">…<popover>…</popover></box>)")
		}
	}
	var err error
	if pairs.events, err = scope.wireEvents(n, p.events); err != nil {
		return pairs, err
	}
	if pairs.binds, err = scope.bindPairs(n, o); err != nil {
		return pairs, err
	}
	if pairs.classes, err = scope.classPairs(n, o); err != nil {
		return pairs, err
	}
	if n.Tag == "popover" {
		pairs.binds = append(pairs.binds, "show: open") // a popover shows while the component around it is open
	}
	return pairs, nil
}

// withText adds the text pair of an element whose children are compiled, then writes the runtime
// attributes onto its start tag. Every alias they read is noted with where it is read from (aliases),
// for compile to check that its component is one the browser runs (wiredAliases).
func (c *compiler) withText(n *Node, o *elemOp, scope *frame, pairs runtimePairs, aliases *aliasFrame) error {
	text, err := scope.textPair(n, o.children)
	if err != nil {
		return err
	}
	if text != "" {
		pairs.binds = append(pairs.binds, text)
	}
	for _, list := range [][]string{pairs.binds, pairs.classes, pairs.events} {
		for _, pair := range list {
			_, source, _ := strings.Cut(pair, ": ")
			c.noteWired(n, source, aliases)
		}
	}
	var w strings.Builder
	writeRuntime(&w, pairs)
	o.staticAttrs += w.String()
	return nil
}

// noteWired notes the aliases an expression written for the kernel reads (source), as read at n.
func (c *compiler) noteWired(n *Node, source string, aliases *aliasFrame) {
	if !strings.Contains(source, "$") {
		return
	}
	e, err := compileExpr(source, n, nil)
	if err != nil {
		return
	}
	for _, name := range usedNames(e.ir) {
		if aliasName(name) {
			c.wired = append(c.wired, aliasUse{name: name, frame: aliases, node: n})
		}
	}
}

// wiredAliases checks, once every as of the page is known, that each alias an expression written for
// the kernel reads names a component the browser runs: the alias of a site component, whose logic
// reaches the browser in step 4, would read as nothing there and the page would show what it did not
// mean (a count of 0, a button that does nothing).
func (c *compiler) wiredAliases() error {
	for _, use := range c.wired {
		def := use.frame.defOf(use.name)
		if def == nil || def.delivered() {
			continue // an alias no host sets is errUse's to tell
		}
		return errAt(use.node, "%s là alias của %s, mà %s chưa chạy ở trình duyệt (logic của component của site tới trình duyệt ở bước 4): ở đây chưa đọc hay gọi được nó qua alias", use.name, def.name, def.name)
	}
	return nil
}

// strayNames tells, once every component of the page is known, a bare name read outside every
// component that no row or layout input around declares and the route's view does not either, while a
// component called on this page declares it: copy() beside a terminal, outside its host, where it
// means nothing (decision T2, 12/10/2026: copy() and copied outside a component that gives them are a
// compile error). Told only where the route's own router.work was read: without it what the page is
// given (Request.Scope: a view elsewhere, a test's data) is not known here, and nothing is told.
func (c *compiler) strayNames() error {
	if c.view == nil {
		return nil
	}
	helpers := formatHelpers()
	names := make([]string, 0, len(c.called))
	for name := range c.called {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, use := range c.strays {
		if c.view[use.name] || helpers[use.name] != nil {
			continue
		}
		for _, component := range names {
			if _, declared := c.called[component].state[use.name]; !declared {
				continue
			}
			message := fmt.Sprintf(`%s là tên của %s, mà ở đây nằm ngoài thẻ chủ của nó nên không đọc hay gọi được gì: đặt chỗ này bên trong thẻ chủ của %s, hoặc đặt as="$tên" trên thẻ chủ rồi viết $tên.%s`,
				use.name, component, component, use.name)
			if use.attr != nil {
				return errAttr(use.node, use.attr, "%s", message)
			}
			return errAt(use.node, "%s", message)
		}
	}
	return nil
}

// writeRuntime writes the runtime pairs in the order they sit on a tag: data-kitwork-bind,
// data-kitwork-class, data-kitwork-event.
func writeRuntime(w *strings.Builder, pairs runtimePairs) {
	if len(pairs.binds) > 0 {
		writeAttr(w, "data-kitwork-bind", strings.Join(pairs.binds, "; "))
	}
	if len(pairs.classes) > 0 {
		writeAttr(w, "data-kitwork-class", strings.Join(pairs.classes, "; "))
	}
	if len(pairs.events) > 0 {
		writeAttr(w, "data-kitwork-event", strings.Join(pairs.events, "; "))
	}
}
