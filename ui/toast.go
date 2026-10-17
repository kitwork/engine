package ui

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// The toast (decisions of 17/10/2026, docs/components/toast.md) is one of Kitwork's components, written
// on a <box>: a stack of short notices at an edge of the screen. The author draws the stack and ONE
// notice, in markup, the way terminal's bar is drawn; the component gives the behaviour (the list, the
// time, the keyboard, what a screen reader hears); the engine writes the wiring:
//
//	<box component="toast" as="$toast" place="bottom-end" gap="2">
//	  <card for="notice of notices; key: notice.id" flex="row" gap="3" padding="3">
//	    <icon show="notice.tone == 'danger'" value="alert-circle" color="danger" />
//	    <box gap="1">
//	      <text show="notice.title" weight="bold">{{ notice.title }}</text>
//	      <text>{{ notice.message }}</text>
//	      <button show="notice.action.click" click="act(notice.id)">{{ notice.action.label }}</button>
//	    </box>
//	    <button click="dismiss(notice.id)"><icon value="x" alt="Đóng thông báo" /></button>
//	  </card>
//	</box>
//
//   - The host is the stack: fixed at the edge or corner place names (six, bottom-end when not written;
//     the classes toast and toast-<place>, reboot.css), role="region" named "Thông báo (F8)" (the page's
//     language; alt changes the first words), tabindex="-1" so F8 can bring the focus in. place and limit
//     (how many show at once, 1 to 10, 3 when not written) go to the instance in data-kitwork-scope.
//   - The notice is the host's one direct child written for="<name> of notices; key: <name>.id": the
//     blueprint of every notice. It is the first live for of .ui: written once, as data-kitwork-for, the
//     kernel makes a row of it for each notice and keeps it by its key; the server paints none (nothing
//     was said yet), and reboot.css never draws the blueprint. Inside it what reads the notice is bound,
//     as inside any component the browser runs, a link's value too (the toast checks the address); the
//     blueprint carries data-leaving, bound to the notice's leaving (its fade out, reboot.css).
//   - Last in the host, two regions only a screen reader hears, polite and assertive, empty, each
//     saying the line added to it alone (aria-atomic="false"): the component says each notice there
//     in its own words.
//
// What does not fit is told where it is written: a host with no notice to draw (the empty host of
// before 17/10/2026 among them), two, one not right under the host, a for over another list, a key
// that is not the notice's id, no dismiss button with a name or one a show can hide, a field the
// notice does not have, a component inside the notice, a second toast on the page, if, for or show on
// the host, a place or a limit it does not take; a call through the alias of a function the toast
// does not have, or of options it would ignore (checkToastCalls), an id that is no name (a number is
// the toast's own), and a call whose notice the blueprint cannot draw (a button or a link, a title, a
// tone).

// toastPlaces are where a stack stands on the screen, the first when place is not written.
var toastPlaces = []string{"bottom-end", "bottom", "bottom-start", "top-end", "top", "top-start"}

// toastFields are what a notice has, as the blueprint reads it: notice.<field>.
var toastFields = []string{"id", "message", "title", "tone", "loading", "action", "count", "leaving"}

// toastActionFields are what a notice's action has: notice.action.<field>.
var toastActionFields = []string{"label", "click", "link"}

// toastFieldHints are the fields of other libraries' notices, each with the toast's own word.
var toastFieldHints = map[string]string{
	"text": "câu là notice.message", "description": "câu là notice.message", "body": "câu là notice.message",
	"content": "câu là notice.message", "type": "loại là notice.tone", "variant": "loại là notice.tone",
	"kind": "loại là notice.tone", "status": "loại là notice.tone", "heading": "tiêu đề là notice.title",
	"actions": "nút là notice.action", "key": "tên của thông báo là notice.id",
}

// toastMethods are what the toast does, through its alias or inside its host.
var toastMethods = []string{"show", "info", "success", "warning", "danger", "loading", "update", "dismiss", "clear", "act"}

// toastTones are the tones of a notice: the four colours of the palette that say something.
var toastTones = []string{"info", "success", "warning", "danger"}

// toastOptions are the keys a call's options take.
var toastOptions = []string{"tone", "title", "duration", "action", "id", "loading"}

// toastOptionHints are the keys of other libraries, each with the toast's own word.
var toastOptionHints = map[string]string{
	"type": "tone", "variant": "tone", "kind": "tone", "status": "tone", "color": "tone", "level": "tone",
	"timeout": "duration", "delay": "duration", "time": "duration", "autoclose": "duration", "autoClose": "duration",
	"heading": "title", "header": "title", "actions": "action", "button": "action", "key": "id", "name": "id",
}

// toastActionHints are the keys of a button in other libraries, each with the toast's word.
var toastActionHints = map[string]string{
	"text": "label", "title": "label", "name": "label", "onClick": "click", "onclick": "click", "do": "click",
	"then": "click", "handler": "click", "run": "click", "href": "link", "url": "link", "to": "link",
}

// toastLongest is the longest a duration may be written: a notice meant to stay longer stays until
// closed (duration: 0). toastShortest is the least a person reads in, toastWithAction the least a
// keyboard needs to reach a notice's button (F8, then Tab: WCAG 2.2.1).
const (
	toastLongest    = 60000
	toastShortest   = 2000
	toastWithAction = 8000
)

// toastName is an id the author gives a notice: a name that starts with a letter, then letters, digits,
// '-' and '_'. The toast numbers the notices given none (1, 2, 3…): an id of digits would meet one.
var toastName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)

// toastScope is the page's toast: what ctx.toast points to inside its host.
type toastScope struct {
	node     *Node
	alias    string
	aliases  *aliasFrame
	place    string
	limit    int
	label    string // the region's name before " (F8)": alt, else the page's word
	template *Node  // the blueprint of a notice: the host's direct child with for="… of notices"
	item     string // its loop variable (notice)
	reads    map[string]bool
	acts     bool // a <button click="act(notice.id)"> in the blueprint
	links    bool // a <link value="{{ notice.action.link }}"> in the blueprint
	toneTold bool

	// transition is how a notice comes and goes: slide (the default), fade or none (transition.go)
	transition string
}

// inputs are what the kernel seeds the instance with before its init: where the stack stands and how
// many notices it shows at once.
func (t *toastScope) inputs() []input {
	place, limit := quoteString(t.place), strconv.Itoa(t.limit)
	return []input{
		{name: "place", in: &interpolation{parts: []part{{text: t.place}}}, source: place},
		{name: "limit", in: &interpolation{parts: []part{{text: limit}}}, source: limit},
	}
}

// toastHost reads the toast's own words off its host — place, limit, alt — and finds the blueprint of
// a notice inside it; the host then goes on as the host of a component without a UI.
func (c *compiler) toastHost(n *Node, component *Attr) (*Node, *toastScope, error) {
	if n.Tag != "box" {
		return nil, nil, errAttr(n, component, "toast đặt trên <box>: chồng thông báo là một khung; viết <box component=\"toast\" as=\"$toast\">…</box>")
	}
	if c.toast != nil {
		at := attrNamed(c.toast.node, "component")
		return nil, nil, errAttr(n, component, "trang đã có một toast ở %s:%d:%d: mỗi trang một chồng thông báo (một phím F8, một chỗ cho thông báo)", c.toast.node.File, at.Line, at.Column)
	}
	scope := &toastScope{node: n, place: toastPlaces[0], limit: 3, transition: transitions[0], reads: map[string]bool{}}
	host := *n
	host.Attrs = nil
	for i := range n.Attrs {
		a := &n.Attrs[i]
		chain, base := "", a.Name
		if j := strings.LastIndex(a.Name, ":"); j >= 0 && !isEvent(a.Name) {
			chain, base = a.Name[:j], a.Name[j+1:]
		}
		if base == "transition" {
			transition, err := transitionOf(n, a, chain, "toast")
			if err != nil {
				return nil, nil, err
			}
			scope.transition = transition
			continue
		}
		if transitionStrangers[base] {
			return nil, nil, errAttr(n, a, "toast không có %s: hiệu ứng hiện và ẩn là transition (slide, fade hay none)", base)
		}
		switch base {
		case "if", "for", "show":
			return nil, nil, errAttr(n, a, "toast luôn có mặt và có một trên mỗi trang: bỏ %s trên thẻ chủ (ẩn hiện từng thông báo là việc của toast)", base)
		case "persist", "remember":
			return nil, nil, errAttr(n, a, "toast không giữ gì qua các lần ghé: thông báo là chuyện của lúc này; bỏ %s", base)
		case "role", "aria-live", "aria-label", "aria-atomic":
			return nil, nil, errAttr(n, a, "vai trò của toast là việc của engine (role=\"region\", hai vùng đọc cho trình đọc màn hình): bỏ %s; tên của chồng là alt (alt=\"Tin nhắn\")", base)
		case "place", "limit", "alt":
		default:
			host.Attrs = append(host.Attrs, *a)
			continue
		}
		value := strings.TrimSpace(a.Value)
		switch {
		case chain != "":
			return nil, nil, errAttr(n, a, "%s của toast không nhận tiền tố: nó được quyết một lần khi trang dựng", base)
		case a.Bare || value == "":
			return nil, nil, errAttr(n, a, "%s của toast cần một giá trị", base)
		case strings.Contains(value, "{{"):
			return nil, nil, errAttr(n, a, "%s của toast là chữ cố định, không có {{ }}: nó được quyết một lần khi trang dựng", base)
		}
		switch base {
		case "place":
			if !containsName(toastPlaces, value) {
				return nil, nil, errAttr(n, a, "place của toast là một mép hay góc màn hình: %s; không phải %q", strings.Join(toastPlaces, ", "), value)
			}
			scope.place = value
		case "limit":
			limit, err := strconv.Atoi(value)
			if err != nil || limit < 1 || limit > 10 {
				return nil, nil, errAttr(n, a, "limit là số thông báo hiện cùng lúc, từ 1 đến 10 (mặc định 3), không phải %q", a.Value)
			}
			scope.limit = limit
		case "alt":
			scope.label = value
		}
	}
	if err := c.toastBlueprint(n, scope); err != nil {
		return nil, nil, err
	}
	c.toast = scope
	return &host, scope, nil
}

// toastBlueprintHint is how the notice is written, for a message.
const toastBlueprintHint = `<card for="notice of notices; key: notice.id">…<button click="dismiss(notice.id)"><icon value="x" alt="Đóng thông báo" /></button></card>`

// toastBlueprint finds the blueprint of a notice among the host's children and reads what it draws.
func (c *compiler) toastBlueprint(n *Node, scope *toastScope) error {
	for _, ch := range n.Elements() {
		a := attrNamed(ch, "for")
		if a.Name != "for" {
			continue
		}
		each, err := parseFor(a.Value, ch, a)
		if err != nil {
			return err
		}
		list := strings.TrimSpace(each.list.source)
		if list != "notices" {
			hint := ""
			if list == "items" {
				hint = " (items là tên cũ)"
			}
			return errAttr(ch, a, "danh sách của toast là notices%s: viết for=\"%s of notices; key: %s.id\"", hint, each.item, each.item)
		}
		if first := scope.template; first != nil {
			return errAttr(ch, a, "toast có hai mẫu thông báo (%s:%d:%d và %s:%d:%d): mỗi toast một mẫu", first.File, first.Line, first.Column, ch.File, ch.Line, ch.Column)
		}
		if each.key == nil || strings.TrimSpace(each.key.source) != each.item+".id" {
			return errAttr(ch, a, "mẫu thông báo cần key: %s.id: mỗi thông báo giữ chỗ của nó khi chồng đổi (focus không mất); viết for=\"%s of notices; key: %s.id\"", each.item, each.item, each.item)
		}
		if attrNamed(ch, "component").Name == "component" {
			return errAttr(ch, attrNamed(ch, "component"), "mẫu thông báo không mang component: một phần tử, một component")
		}
		if !containerHosts[ch.Tag] || ch.Tag == "item" {
			return errAt(ch, "mẫu thông báo là một khung: <card for=\"%s of notices; key: %s.id\">, hay <box>, <section>; không phải <%s>", each.item, each.item, ch.Tag)
		}
		scope.template, scope.item = ch, each.item
	}
	if scope.template == nil {
		if deep := toastDeepTemplate(n.Children); deep != nil {
			return errAttr(deep, attrNamed(deep, "for"), "mẫu thông báo là con trực tiếp của thẻ chủ toast: đưa phần tử có for=\"… of notices\" lên ngay dưới <box component=\"toast\">")
		}
		return errAt(n, "toast cần một mẫu thông báo bên trong thẻ chủ: %s; từ 17/10/2026 người viết vẽ thông báo, toast không còn giao diện dựng sẵn", toastBlueprintHint)
	}
	if deep := toastDeepTemplate(scope.template.Children); deep != nil {
		return errAttr(deep, attrNamed(deep, "for"), "mẫu thông báo không lồng một mẫu thông báo khác")
	}
	dismiss := false
	// hides is the nearest element around (the notice itself among them) that a show can hide
	var walk func(nodes []*Node, hides *Node) error
	walk = func(nodes []*Node, hides *Node) error {
		for _, ch := range nodes {
			if ch.IsText() {
				if err := c.toastReads(ch, nil, scope); err != nil {
					return err
				}
				continue
			}
			if a := attrNamed(ch, "component"); a.Name == "component" {
				return errAttr(ch, a, "mẫu thông báo không chứa component: một phần tử, một component; đặt nó ngoài toast")
			}
			for i := range ch.Attrs {
				if err := c.toastReads(ch, &ch.Attrs[i], scope); err != nil {
					return err
				}
			}
			if a := attrNamed(ch, "click"); ch.Tag == "button" && a.Name == "click" {
				if e, err := compileExpr(a.Value, ch, a); err == nil {
					switch toastItemCall(e.ir, scope.item) {
					case "dismiss":
						if !spokenName(ch) {
							return errAt(ch, "nút đóng của thông báo cần tên đọc được: <button click=\"dismiss(%s.id)\"><icon value=\"x\" alt=\"Đóng thông báo\" /></button>", scope.item)
						}
						if show := toastShowOf(ch); show != nil {
							return errAttr(ch, show, "nút đóng của thông báo luôn hiện: bỏ %s ở đây; thông báo lỗi ở lại tới khi đóng, nút đóng ẩn là thông báo không đóng được", show.Name)
						}
						if hides != nil {
							show := toastShowOf(hides)
							return errAttr(hides, show, "nút đóng của thông báo (%s:%d:%d) luôn hiện: bỏ %s trên khung bọc nó; thông báo lỗi ở lại tới khi đóng, nút đóng ẩn là thông báo không đóng được", ch.File, ch.Line, ch.Column, show.Name)
						}
						dismiss = true
					case "act":
						scope.acts = true
					}
				}
			}
			if a := attrNamed(ch, "value"); ch.Tag == "link" && a.Name == "value" && strings.Contains(a.Value, scope.item+".action.link") {
				scope.links = true
			}
			inner := hides
			if toastShowOf(ch) != nil {
				inner = ch
			}
			if err := walk(ch.Children, inner); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk([]*Node{scope.template}, nil); err != nil {
		return err
	}
	if !dismiss {
		return errAt(scope.template, "mỗi thông báo cần một nút đóng: <button click=\"dismiss(%s.id)\"><icon value=\"x\" alt=\"Đóng thông báo\" /></button> (thông báo lỗi ở lại tới khi đóng)", scope.item)
	}
	if !scope.reads["message"] {
		c.warn(scope.template.File, scope.template.Line, scope.template.Column, fmt.Sprintf("mẫu thông báo không hiện %s.message: thông báo sẽ không có chữ; viết <text>{{ %s.message }}</text>", scope.item, scope.item))
	}
	return nil
}

// toastShowOf is a show written on n, with or without a prefix (medium:show), nil for none: the
// dismiss button is never hidden, nor anything around it in the notice.
func toastShowOf(n *Node) *Attr {
	for i := range n.Attrs {
		a := &n.Attrs[i]
		if a.Name == "show" || strings.HasSuffix(a.Name, ":show") && !isEvent(a.Name) {
			return a
		}
	}
	return nil
}

// toastDeepTemplate is an element below nodes (not right under the host) whose for runs over notices.
func toastDeepTemplate(nodes []*Node) *Node {
	for _, ch := range nodes {
		if ch.IsText() {
			continue
		}
		if a := attrNamed(ch, "for"); a.Name == "for" {
			if each, err := parseFor(a.Value, ch, a); err == nil && strings.TrimSpace(each.list.source) == "notices" {
				return ch
			}
		}
		if found := toastDeepTemplate(ch.Children); found != nil {
			return found
		}
	}
	return nil
}

// toastReads checks what one attribute (nil: a run of text) of the blueprint reads of the notice: a
// field it has, a field of its action; and records the fields read.
func (c *compiler) toastReads(n *Node, a *Attr, scope *toastScope) error {
	var exprs []*expr
	if a == nil {
		if !strings.Contains(n.Text, "{{") {
			return nil
		}
		in, err := parseInterpolation(n.Text, n, nil)
		if err != nil {
			return nil // the compile of the text reports it
		}
		for _, p := range in.parts {
			if p.e != nil {
				exprs = append(exprs, p.e)
			}
		}
	} else {
		exprs = attrExpressions(n, a)
	}
	for _, e := range exprs {
		var problem string
		walkIR(e.ir, func(node []any) {
			if problem != "" || len(node) != 3 || node[0] != "." {
				return
			}
			field, _ := node[2].(string)
			if ref, ok := node[1].([]any); ok && len(ref) == 2 && ref[0] == "$" && ref[1] == scope.item {
				scope.reads[field] = true
				if !containsName(toastFields, field) {
					problem = toastFieldProblem(scope.item, field)
				}
				return
			}
			if inner, ok := node[1].([]any); ok && len(inner) == 3 && inner[0] == "." && inner[2] == "action" {
				if ref, ok := inner[1].([]any); ok && len(ref) == 2 && ref[0] == "$" && ref[1] == scope.item && !containsName(toastActionFields, field) {
					hint := ""
					if word, ok := toastActionHints[field]; ok {
						hint = ": viết " + scope.item + ".action." + word
					}
					problem = fmt.Sprintf("%s.action không có %s%s (nút của thông báo có %s)", scope.item, field, hint, strings.Join(toastActionFields, ", "))
				}
			}
		})
		if problem != "" {
			if a == nil {
				return errAt(n, "%s", problem)
			}
			return errAttr(n, a, "%s", problem)
		}
	}
	return nil
}

func toastFieldProblem(item, field string) string {
	if hint, ok := toastFieldHints[field]; ok {
		return fmt.Sprintf("%s không có %s: %s", item, field, strings.Replace(hint, "notice.", item+".", 1))
	}
	return fmt.Sprintf("%s không có %s; một thông báo có %s%s", item, field, strings.Join(toastFields, ", "), nearestWords(field, toastFields))
}

// walkIR calls fn on every node of an expression's IR, outermost first.
func walkIR(ir any, fn func([]any)) {
	node, ok := ir.([]any)
	if !ok || len(node) == 0 {
		return
	}
	if _, op := node[0].(string); op {
		if node[0] == "#" {
			return
		}
		fn(node)
		for _, child := range node[1:] {
			walkIR(child, fn)
		}
		return
	}
	for _, child := range node {
		walkIR(child, fn)
	}
}

// toastItemCall reads a click that calls the toast with the notice's id — dismiss(notice.id),
// act(notice.id) — into the name of what it calls, "" for anything else.
func toastItemCall(ir any, item string) string {
	node, ok := ir.([]any)
	if !ok || len(node) != 3 || node[0] != "call" {
		return ""
	}
	callee, ok := node[1].([]any)
	if !ok || len(callee) != 2 || callee[0] != "$" {
		return ""
	}
	args, ok := node[2].([]any)
	if !ok || len(args) != 1 {
		return ""
	}
	id, ok := args[0].([]any)
	if !ok || len(id) != 3 || id[0] != "." || id[2] != "id" {
		return ""
	}
	if ref, ok := id[1].([]any); !ok || len(ref) != 2 || ref[0] != "$" || ref[1] != item {
		return ""
	}
	name, _ := callee[1].(string)
	return name
}

// toastRegion is the name of the stack, as a screen reader lists it among the page's regions: the
// author's alt, else the page's word, then the key that reaches it.
func (c *compiler) toastRegion(scope *toastScope) string {
	label := scope.label
	if label == "" {
		label = "Thông báo"
		if language := strings.ToLower(c.site.Language); language != "" && !strings.HasPrefix(language, "vi") {
			label = "Notifications"
		}
	}
	return label + " (F8)"
}

// toastClasses are the classes of the host: the stack, its place and its transition (reboot.css).
func (t *toastScope) classes() []string {
	return append([]string{"toast", "toast-" + t.place}, transitionClasses(t.transition)...)
}

// toastRegions are the two regions a screen reader hears, last in the host: polite for every notice
// but a danger, assertive for that one; empty until the component says a notice there. Each says the
// line added to it, alone: a status or an alert is atomic unless it says otherwise, and would say
// every line it holds again at each one added ("Đang lưu… Đã lưu.").
const toastRegions = `<span class="reader" role="status" aria-atomic="false"></span><span class="reader" role="alert" aria-atomic="false"></span>`

// toastChildren compiles what is inside the host — the blueprint of a notice and whatever the author
// writes around it — then the two regions.
func (c *compiler) toastChildren(n *Node, cx ctx) ([]op, error) {
	children, err := c.children(n, cx)
	if err != nil {
		return nil, err
	}
	return append(children, &staticOp{s: toastRegions}), nil
}

// glassAround is the material written on a frame around (a glass: any value but none), nil for none:
// its backdrop-filter places what is fixed inside it by the frame, not by the screen.
func glassAround(p *paint) *paintIntent {
	for ; p != nil; p = p.parent {
		for _, intent := range p.written {
			if intent.name != "material" || intent.attr == nil || intent.node == nil {
				continue
			}
			for _, value := range intent.values {
				if value != "none" {
					return intent
				}
			}
			if intent.values == nil {
				return intent // from data: a glass at least sometimes
			}
		}
	}
	return nil
}

// toastFor is the blueprint's for as the kernel reads it (data-kitwork-for): written once, never a row
// painted by the server.
func toastFor(each *forSpec) string {
	head := each.item
	if each.index != "" {
		head += ", " + each.index
	}
	return head + " of " + strings.TrimSpace(each.list.source) + "; key: " + strings.TrimSpace(each.key.source)
}

// aliasExpr is an expression that reads an alias, with where it is written: the calls through the
// toast's alias are checked once every as of the page is known (checkToastCalls).
type aliasExpr struct {
	e     *expr
	frame *aliasFrame
	node  *Node
	attr  *Attr
}

// aliasExprKey is where an expression that reads an alias is written: the same expression read twice
// there (a host's attributes are read where the call stands and again inside) is kept once.
type aliasExprKey struct {
	node   *Node
	attr   *Attr
	source string
}

// noteAliasExpr keeps an expression that reads an alias, once for where it is written.
func (c *compiler) noteAliasExpr(n *Node, a *Attr, e *expr, frame *aliasFrame) {
	key := aliasExprKey{n, a, e.source}
	if c.aliasExprSeen[key] {
		return
	}
	if c.aliasExprSeen == nil {
		c.aliasExprSeen = map[aliasExprKey]bool{}
	}
	c.aliasExprSeen[key] = true
	c.aliasExprs = append(c.aliasExprs, aliasExpr{e: e, frame: frame, node: n, attr: a})
}

// toastParams is how many parameters a lambda's list names, however the IR holds it.
func toastParams(params any) int {
	switch list := params.(type) {
	case []string:
		return len(list)
	case []any:
		return len(list)
	}
	return 0
}

// checkToastCalls checks, once every as of the page is known, each call through the toast's alias: a
// function it has, options it reads, and a notice its blueprint can draw.
func (c *compiler) checkToastCalls() error {
	if c.toast == nil {
		return nil
	}
	nonInfo := false
	var firstTone aliasExpr
	for _, use := range c.aliasExprs {
		var err error
		walkIR(use.e.ir, func(node []any) {
			if err != nil {
				return
			}
			switch {
			case len(node) == 4 && node[0] == "()":
				alias, ok := toastAlias(node[1], use.frame)
				if !ok {
					return
				}
				method, _ := node[2].(string)
				args, _ := node[3].([]any)
				var tone bool
				tone, err = c.toastCall(use, alias, method, args)
				if tone && !nonInfo {
					nonInfo, firstTone = true, use
				}
			case len(node) == 3 && node[0] == ".":
				alias, ok := toastAlias(node[1], use.frame)
				if !ok {
					return
				}
				name, _ := node[2].(string)
				err = toastRead(use, alias, name)
			}
		})
		if err != nil {
			return err
		}
	}
	if nonInfo && !c.toast.reads["tone"] {
		t := c.toast.template
		c.warn(t.File, t.Line, t.Column, fmt.Sprintf("mẫu thông báo không đổi theo %s.tone, mà %s:%d:%d gọi một thông báo không phải info: thông báo lỗi trông như thông báo thường; viết mỗi tone một icon (<icon show=\"%s.tone == 'danger'\" …>)",
			c.toast.item, firstTone.node.File, toastLine(firstTone), toastColumn(firstTone), c.toast.item))
	}
	return nil
}

func toastLine(use aliasExpr) int {
	if use.attr != nil {
		return use.attr.Line
	}
	return use.node.Line
}

func toastColumn(use aliasExpr) int {
	if use.attr != nil {
		return use.attr.Column
	}
	return use.node.Column
}

// toastAlias reads $alias where an expression reads it, when the alias names the page's toast.
func toastAlias(ir any, frame *aliasFrame) (string, bool) {
	ref, ok := ir.([]any)
	if !ok || len(ref) != 2 || ref[0] != "$" {
		return "", false
	}
	alias, _ := ref[1].(string)
	if !aliasName(alias) || frame == nil {
		return "", false
	}
	def := frame.defOf(alias)
	return alias, def != nil && def.kitwork != nil && def.name == "toast"
}

func toastError(use aliasExpr, format string, args ...any) error {
	if use.attr != nil {
		return errAttr(use.node, use.attr, format, args...)
	}
	return errAt(use.node, format, args...)
}

// toastRead checks a name read off the toast's alias, not called: its state.
func toastRead(use aliasExpr, alias, name string) error {
	switch name {
	case "notices", "waiting", "paused":
		return nil
	case "message", "visible":
		return toastError(use, "%s.%s là chữ của trang template: thông báo đang hiện là %s.notices (mỗi thông báo có message, title, tone…)", alias, name, alias)
	}
	if containsName(toastMethods, name) {
		return nil
	}
	return toastError(use, "toast không có %s; trạng thái của toast là notices, waiting, paused%s", name, nearestWords(name, []string{"notices", "waiting", "paused"}))
}

// toastCall checks one call through the toast's alias and reports whether it says a notice of another
// tone than info.
func (c *compiler) toastCall(use aliasExpr, alias, method string, args []any) (bool, error) {
	switch method {
	case "error":
		return false, toastError(use, "toast không có error: màu của bảng là danger, viết %s.danger(…)", alias)
	case "hide":
		return false, toastError(use, "hide là chữ của trang template: viết %s.clear() (đóng mọi thông báo) hay %s.dismiss(id) (đóng một)", alias, alias)
	case "close", "remove", "dismissAll":
		word := "dismiss(id)"
		if method == "dismissAll" {
			word = "clear()"
		}
		return false, toastError(use, "toast không có %s: viết %s.%s", method, alias, word)
	case "promise", "custom", "push", "add", "notify", "open":
		return false, toastError(use, "toast không có %s: hiện một thông báo là %s.show(câu, options) (hay info, success, warning, danger, loading)", method, alias)
	}
	if !containsName(toastMethods, method) {
		return false, toastError(use, "toast không có %s; hàm của toast là %s%s", method, strings.Join(toastMethods, ", "), nearestWords(method, toastMethods))
	}
	var options any
	switch method {
	case "show", "info", "success", "warning", "danger", "loading":
		if len(args) == 0 {
			return false, toastError(use, "%s.%s cần câu thông báo: %s.%s('Đã lưu.')", alias, method, alias, method)
		}
		if text, ok := toastLiteral(args[0]); ok {
			if s, isString := text.(string); isString && strings.TrimSpace(s) == "" {
				return false, toastError(use, "%s.%s(''): thông báo cần một câu; câu rỗng không hiện gì", alias, method)
			}
		}
		if len(args) > 1 {
			options = args[1]
		}
		if len(args) > 2 {
			return false, toastError(use, "%s.%s nhận câu và options: %s.%s('Đã lưu.', { title: '…' })", alias, method, alias, method)
		}
	case "update":
		if len(args) < 2 {
			return false, toastError(use, "%s.update nhận id và câu mới: %s.update('save', 'Đã lưu.', { tone: 'success' })", alias, alias)
		}
		if len(args) > 2 {
			options = args[2]
		}
	default:
		return false, nil
	}
	tone := method == "success" || method == "warning" || method == "danger"
	object, ok := options.([]any)
	if !ok || len(object) != 2 || object[0] != "{}" {
		return tone, nil // options from data: the toast reads them as it runs
	}
	pairs, _ := object[1].([]any)
	var duration *float64
	hasAction := false
	for _, p := range pairs {
		pair, ok := p.([]any)
		if !ok || len(pair) != 2 {
			continue
		}
		key, _ := pair[0].(string)
		value := pair[1]
		switch key {
		case "tone":
			literal, isLiteral := toastLiteral(value)
			if !isLiteral {
				tone = true
				continue
			}
			name, _ := literal.(string)
			switch {
			case method == "success" || method == "warning" || method == "danger" || method == "info":
				return false, toastError(use, "%s đã là tone; bỏ tone, hay gọi %s.%s(…)", method, alias, name)
			case name == "error":
				return false, toastError(use, "tone: 'error' không có: màu của bảng là danger, viết tone: 'danger' (hay %s.danger(…))", alias)
			case !containsName(toastTones, name):
				return false, toastError(use, "tone của thông báo là %s, không phải %q", strings.Join(toastTones, ", "), name)
			}
			if name != "info" {
				tone = true
			}
		case "title":
			if !c.toast.reads["title"] {
				c.warn(use.node.File, toastLine(use), toastColumn(use), fmt.Sprintf("thông báo có title mà mẫu thông báo (%s:%d:%d) không đọc %s.title: tiêu đề không hiện; viết <text show=\"%s.title\" weight=\"bold\">{{ %s.title }}</text> trong mẫu",
					c.toast.template.File, c.toast.template.Line, c.toast.template.Column, c.toast.item, c.toast.item, c.toast.item))
			}
		case "duration":
			ms, err := toastDuration(use, value)
			if err != nil {
				return false, err
			}
			duration = ms
		case "action":
			has, err := c.toastAction(use, value)
			if err != nil {
				return false, err
			}
			hasAction = hasAction || has
		case "id":
			if literal, ok := toastLiteral(value); ok {
				switch v := literal.(type) {
				case string:
					if !toastName.MatchString(v) {
						return false, toastError(use, "id của thông báo là một tên bắt đầu bằng chữ cái (chữ, số, - và _), không phải %q", v)
					}
				case float64:
					return false, toastError(use, "id của thông báo là một tên bắt đầu bằng chữ cái (chữ, số, - và _): số là của toast (1, 2, 3…), một số người viết đặt sẽ trùng nó")
				default:
					return false, toastError(use, "id của thông báo là một tên bắt đầu bằng chữ cái (chữ, số, - và _)")
				}
			}
		case "loading":
		case "message", "text", "description", "body", "content":
			return false, toastError(use, "options không có %s: câu là đối số đầu, %s.%s('Đã lưu.', { … })", key, alias, method)
		default:
			if word, ok := toastOptionHints[key]; ok {
				return false, toastError(use, "options của toast không có %s: viết %s", key, word)
			}
			return false, toastError(use, "options của toast không có %s; options có %s%s", key, strings.Join(toastOptions, ", "), nearestWords(key, toastOptions))
		}
	}
	if duration != nil && *duration > 0 {
		switch {
		case hasAction && *duration < toastWithAction:
			c.warn(use.node.File, toastLine(use), toastColumn(use), "thông báo có nút mà duration dưới 8 giây: người dùng bàn phím không kịp tới nút (WCAG 2.2.1); viết duration từ \"8s\", hay bỏ duration")
		case *duration < toastShortest:
			c.warn(use.node.File, toastLine(use), toastColumn(use), "duration dưới 2 giây: người xem đọc không kịp; viết duration từ \"2s\", hay bỏ duration (mỗi tone có thời gian của nó)")
		}
	}
	return tone, nil
}

// toastLiteral is the value of a literal written out (a string, a number, true, false, a negative
// number), and whether it is one.
func toastLiteral(ir any) (any, bool) {
	node, ok := ir.([]any)
	if !ok || len(node) != 2 {
		return nil, false
	}
	switch node[0] {
	case "#":
		return node[1], true
	case "u-":
		if inner, ok := toastLiteral(node[1]); ok {
			if number, ok := inner.(float64); ok {
				return -number, true
			}
		}
	}
	return nil, false
}

// toastDuration reads a duration written out: 0 stays, "4s" and "800ms" are times; it returns nil for a
// value from data.
func toastDuration(use aliasExpr, ir any) (*float64, error) {
	literal, ok := toastLiteral(ir)
	if !ok {
		return nil, nil
	}
	switch v := literal.(type) {
	case float64:
		if v == 0 {
			zero := 0.0
			return &zero, nil
		}
		if v < 0 {
			return nil, toastError(use, "duration không âm: viết một thời lượng như duration: '%s', hay 0 để thông báo ở lại tới khi đóng", durationSuggestion(strconv.FormatFloat(-v, 'f', -1, 64)))
		}
		return nil, toastError(use, "duration: %s thiếu đơn vị: viết duration: '%s' (0 là ở lại tới khi đóng)", strconv.FormatFloat(v, 'f', -1, 64), durationSuggestion(strconv.FormatFloat(v, 'f', -1, 64)))
	case string:
		value := strings.TrimSpace(v)
		m := durationRe.FindStringSubmatch(value)
		if m == nil {
			if strings.HasPrefix(value, "-") {
				return nil, toastError(use, "duration không âm: viết một thời lượng như duration: '4s', hay 0 để thông báo ở lại tới khi đóng")
			}
			if unitlessRe.MatchString(value) {
				if number, _ := strconv.ParseFloat(value, 64); number == 0 {
					return nil, toastError(use, "duration: '%s': thông báo ở lại tới khi đóng thì viết duration: 0", v)
				}
				return nil, toastError(use, "duration: '%s' thiếu đơn vị: viết duration: '%s'", v, durationSuggestion(value))
			}
			return nil, toastError(use, "duration: '%s' không phải một thời lượng: viết duration: '4s' hay '800ms' (0 là ở lại tới khi đóng)", v)
		}
		number, _ := strconv.ParseFloat(m[1], 64)
		ms := number
		if m[2] == "s" {
			ms = number * 1000
		}
		if ms > toastLongest {
			return nil, toastError(use, "duration: '%s' dài hơn 60 giây: thông báo ở lại tới khi đóng thì viết duration: 0", v)
		}
		return &ms, nil
	}
	return nil, toastError(use, "duration là một thời lượng có đơn vị ('4s', '800ms') hay 0 (ở lại tới khi đóng)")
}

// toastAction checks an action written out: a label, and a click that is an arrow function of nothing
// or a link the link rule takes, one of the two. It reports whether there is one, and cross-checks it
// with the blueprint, which must draw it.
func (c *compiler) toastAction(use aliasExpr, ir any) (bool, error) {
	if literal, ok := toastLiteral(ir); ok && literal == nil {
		return false, nil // action: null removes the button (update)
	}
	object, ok := ir.([]any)
	if !ok || len(object) != 2 || object[0] != "{}" {
		return true, nil // from data: the toast checks it as it runs
	}
	pairs, _ := object[1].([]any)
	var label, click, link any
	for _, p := range pairs {
		pair, ok := p.([]any)
		if !ok || len(pair) != 2 {
			continue
		}
		key, _ := pair[0].(string)
		switch key {
		case "label":
			label = pair[1]
		case "click":
			click = pair[1]
		case "link":
			link = pair[1]
		default:
			if word, ok := toastActionHints[key]; ok {
				return false, toastError(use, "action không có %s: viết %s (action là { label, click } hay { label, link })", key, word)
			}
			return false, toastError(use, "action không có %s: action là { label: 'Hoàn tác', click: () => $notes.restore() } hay { label: 'Xem giỏ', link: '/gio-hang' }", key)
		}
	}
	switch {
	case label == nil:
		return false, toastError(use, "action cần label: chữ trên nút ({ label: 'Hoàn tác', click: () => … })")
	case click == nil && link == nil:
		return false, toastError(use, "action cần click (một hàm mũi tên: click: () => $notes.restore()) hay link (một trang: link: '/gio-hang')")
	case click != nil && link != nil:
		return false, toastError(use, "action có cả click và link: một nút làm một việc; viết một trong hai")
	}
	if click != nil {
		fn, ok := click.([]any)
		if !ok || len(fn) < 3 || fn[0] != "=>" {
			return false, toastError(use, "click của action là một hàm mũi tên viết ngay tại chỗ: click: () => $notes.restore()")
		}
		if toastParams(fn[1]) > 0 {
			return false, toastError(use, "click của action không nhận tham số: toast gọi nó không đối số; viết click: () => …")
		}
		if !c.toast.acts {
			t := c.toast.template
			return false, toastError(use, "thông báo có nút (action.click) mà mẫu thông báo (%s:%d:%d) không có <button click=\"act(%s.id)\">: nút sẽ không hiện; viết <button show=\"%s.action.click\" click=\"act(%s.id)\">{{ %s.action.label }}</button> trong mẫu",
				t.File, t.Line, t.Column, c.toast.item, c.toast.item, c.toast.item, c.toast.item)
		}
	}
	if link != nil {
		if literal, ok := toastLiteral(link); ok {
			value, isString := literal.(string)
			if !isString || strings.TrimSpace(value) == "" || !safeLink(value) {
				return false, toastError(use, "link của action chỉ đi tới đường dẫn trong site (/…, ./…, #…), http:, https:, mailto: hoặc tel:")
			}
		}
		if !c.toast.links {
			t := c.toast.template
			return false, toastError(use, "thông báo có link (action.link) mà mẫu thông báo (%s:%d:%d) không có <link value=\"{{ %s.action.link }}\">: link sẽ không hiện; viết <link show=\"%s.action.link\" value=\"{{ %s.action.link }}\">{{ %s.action.label }}</link> trong mẫu",
				t.File, t.Line, t.Column, c.toast.item, c.toast.item, c.toast.item, c.toast.item)
		}
	}
	return true, nil
}
