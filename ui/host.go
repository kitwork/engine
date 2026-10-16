package ui

import (
	"fmt"
	"path"
	"strings"
)

// A component — a name with logic — is written component="name" on a tag (KITWORK_UI §13). A piece
// with only a .ui is a layout, called by its tag (layout.go); written component= it is an error that
// says so. Two shapes:
//
//   - With a UI (cart.ui with its cart.work, Kitwork's toast): the file's one root is the host, and the
//     call writes that same tag, empty — <box component="cart" as="$cart" />. The two are one element:
//     the file owns the look and the behaviour; the call adds only its own words (component, as, if,
//     show, for with its key, span and place, with width prefixes) and inputs, which are every other
//     attribute and must be names the component declares or its .ui reads.
//   - Without a UI (dropdown, appearance, a site component that has only a .work): the host is the
//     author's tag and its attributes stay the tag's; what is written inside is the component's UI.
//     An attribute that is none of the tag's own is an input; one the tag and the component both
//     claim is an error. The terminal is one of these with a rule of its own: its own word (typing)
//     leaves the host first, and each <code> inside is a command with a prompt before it
//     (terminal.go). A component's inside is never a terminal's: its code is its own.
//
// Which side reads a name: if, show, for (and its key), as and the values of inputs are read where
// the call stands, and may not read the component's own names. Everything else written on a host
// without a UI, and inside it, reads the component's names first, then outward; a name the component
// and an outer level (a component around it, a row, the route's view) both declare is a warning
// there: the nearest wins. Inside a component's own .ui its names win without question.

// componentCall compiles an element that carries component=.
func (c *compiler) componentCall(n *Node, cx ctx, outer *callSite) (op, error) {
	attr := attrNamed(n, "component")
	info := catalog[n.Tag] // element() sends a tag outside the vocabulary to layoutCall
	if info.region && len(n.SignificantChildren()) == 0 {
		return c.region(n, cx) // refuses the attribute: an empty region call takes source, if and show
	}
	if reason := forbiddenHost(n.Tag); reason != "" {
		return nil, errAttr(n, attr, "component không đặt trên <%s>: %s", n.Tag, reason)
	}
	def, err := c.resolveComponent(n, attr)
	if err != nil {
		return nil, err
	}
	if def.layout() {
		return nil, c.layoutAsAComponent(n, attr, def)
	}
	c.called[def.name] = def // its names, read outside its host, are told (strayNames)
	if def.root != nil {
		if err := checkComponentRoot(def); err != nil {
			return nil, err
		}
	}
	if info.region && def.hasUI() {
		return nil, errAttr(n, attr, "%s có giao diện (%s) nên không đặt trên vùng <%s>: giao diện của vùng là file của vùng; đặt %s trên một thẻ bên trong",
			def.name, def.what(c.site), n.Tag, def.name)
	}
	// a component with a rule for what is inside it (the terminal): its own words are read here and
	// leave the host, which then goes on as the host of a component without a UI
	var term *terminalSpec
	if def.kitwork != nil && def.kitwork.around {
		if info.region {
			return nil, errAttr(n, attr, "%s không đặt trên vùng <%s>: đặt nó trên một thẻ bên trong (%s)", def.name, n.Tag, hostList(def.kitwork.hosts))
		}
		var err error
		if n, term, err = c.terminalHost(n); err != nil {
			return nil, err
		}
	}
	if def.hasUI() {
		if root := def.rootTag(); n.Tag != root {
			return nil, errAttr(n, attr, "%s dựng một <%s>: viết %s", def.what(c.site), root, suggestion(root, def, n))
		}
		if def.kitwork != nil {
			return c.kitworkUI(n, cx, def)
		}
		if len(n.SignificantChildren()) > 0 {
			return nil, errAt(n, "%s có giao diện (%s) nên thẻ gọi để rỗng: nội dung con không phải slot; đổi giao diện thì sửa %s",
				def.name, def.what(c.site), path.Base(def.what(c.site)))
		}
	} else {
		if len(n.SignificantChildren()) == 0 {
			return nil, errAt(n, "%s không có giao diện: nội dung bên trong thẻ là giao diện của nó (<%s component=\"%s\">…</%s>)", def.name, n.Tag, def.name, n.Tag)
		}
		if def.kitwork != nil && !def.kitwork.hosts[n.Tag] && !info.region {
			return nil, errAttr(n, attr, "%s đặt trên %s, không trên <%s>", def.name, hostList(def.kitwork.hosts), n.Tag)
		}
	}
	site := &callSite{def: def, state: def.state}
	host := *n // the host as the author wrote it, without the call's words (a component without a UI)
	host.Attrs = nil
	var extra []Attr // static show on a call of a component with a UI: merged into the root
	var asAttr, forAttr *Attr
	var outside []outsideExpr
	for i := range n.Attrs {
		a := &n.Attrs[i]
		chain, base := "", a.Name
		if j := strings.LastIndex(a.Name, ":"); j >= 0 && !isEvent(a.Name) {
			chain, base = a.Name[:j], a.Name[j+1:]
		}
		// bare, only a true-or-false word the host tag keeps for itself reads as "true" (compile.go lint):
		// an input is data the call passes, and has none
		if a.Bare && !(booleanAttributes[base] && !def.hasUI() && (presentation(base, n.Tag) || tagWord(n.Tag, base))) {
			return nil, errAttr(n, a, "%s cần một giá trị", a.Name)
		}
		if chain != "" && (base == "component" || base == "as" || base == "for" || base == "if") {
			return nil, errAttr(n, a, "%s không nhận tiền tố", base)
		}
		switch {
		case base == "component":
		case base == "as":
			asAttr = a
		case base == "for":
			each, err := parseFor(a.Value, n, a)
			if err != nil {
				return nil, err
			}
			site.forEach, forAttr = each, a
			outside = append(outside, outsideExpr{a, "for", each.list})
			if each.key != nil {
				outside = append(outside, outsideExpr{a, "key", each.key})
			}
		case base == "if":
			e, err := compileExpr(a.Value, n, a)
			if err != nil {
				return nil, err
			}
			site.ifExpr = e
			outside = append(outside, outsideExpr{a, "if", e})
		case base == "show" && chain == "" && !literalBoolean(a.Value):
			e, err := compileExpr(a.Value, n, a)
			if err != nil {
				return nil, err
			}
			site.showExpr = e
			outside = append(outside, outsideExpr{a, "show", e})
		case base == "show":
			// true/false, plain or with a width prefix: classes on the host
			if def.hasUI() {
				moved, err := c.callShow(n, a, chain)
				if err != nil {
					return nil, err
				}
				extra = append(extra, moved)
			} else {
				host.Attrs = append(host.Attrs, *a)
			}
		case (base == "span" || base == "place") && def.hasUI():
			made, err := c.placement(n, a, chain, base, def.rootTag())
			if err != nil {
				return nil, err
			}
			site.classes = append(site.classes, made...)
			if base == "place" && len(made) > 0 {
				site.places = append(site.places, c.placeWidth(chain)) // the root floats there (contrast.go)
			}
		case def.hasUI():
			name := camel(a.Name)
			if chain != "" || !c.takesInput(def, name) {
				return nil, c.notAnInput(n, a, def)
			}
			in, err := c.inputValue(n, a, site)
			if err != nil {
				return nil, err
			}
			outside = append(outside, inputExprs(a, in)...)
		default:
			// a component without a UI: the tag's own words stay the tag's
			name := camel(a.Name)
			if chain != "" || isEvent(a.Name) || presentation(base, n.Tag) || tagWord(n.Tag, a.Name) || a.Name == "align" {
				if _, declared := def.state[name]; declared && chain == "" {
					return nil, errAttr(n, a, "%s là thuộc tính của <%s> và cũng là tên %s khai: một chữ một nghĩa trên một thẻ; đổi tên trong %s",
						a.Name, n.Tag, def.name, logicFileName(c, def))
				}
				host.Attrs = append(host.Attrs, *a)
				continue
			}
			if a.Name == "source" {
				return nil, errAttr(n, a, "source chỉ dành cho vùng; chỉ rõ nguồn của component bằng đường dẫn: component=\".components/%s\"", def.name)
			}
			if _, declared := def.state[name]; !declared {
				return nil, errAttr(n, a, "%s không phải thuộc tính của <%s>, và %s không khai %s", a.Name, n.Tag, def.name, name)
			}
			in, err := c.inputValue(n, a, site)
			if err != nil {
				return nil, err
			}
			outside = append(outside, inputExprs(a, in)...)
		}
	}
	if outer != nil && outer.ifExpr != nil {
		// a region root that is a host: the region's own if comes first
		if site.ifExpr != nil {
			site.ifExpr = &expr{source: "(" + outer.ifExpr.source + ") && (" + site.ifExpr.source + ")", ir: []any{"&&", outer.ifExpr.ir, site.ifExpr.ir}}
		} else {
			site.ifExpr = outer.ifExpr
		}
	}

	// what the call reads is read where it stands, never the component's own names
	for _, o := range outside {
		if err := c.checkNames(n, o.attr, o.e, cx.scope, cx.aliases); err != nil {
			return nil, err
		}
		for _, name := range usedNames(o.e.ir) {
			if _, own := def.state[name]; own {
				return nil, outsideError(c, n, o, def, name)
			}
		}
	}
	// live for and if on a host: the kernel keeps a for's row and a component's instance in one place
	// for now, so neither repeats nor removes a host by live state yet
	if err := c.liveCall(n, cx, site, def, forAttr); err != nil {
		return nil, err
	}
	if site.showExpr != nil {
		client, err := cx.scope.onClient(site.showExpr, n, attrNamed(n, "show"))
		if err != nil {
			return nil, err
		}
		if client {
			site.showBind = "show: " + site.showExpr.source
		}
	}

	// as names one instance: page-wide, unique, never once per row
	if asAttr != nil {
		alias := strings.TrimSpace(asAttr.Value)
		if forAttr != nil {
			return nil, errAttr(n, asAttr, "as không đặt trên thẻ có for: mỗi hàng là một instance, mà alias chỉ trỏ một")
		}
		if err := c.registerAlias(n, asAttr, alias, cx, def); err != nil {
			return nil, err
		}
		site.alias = alias
	}
	if def.delivered() {
		for _, b := range site.bindings {
			source, client, err := cx.scope.interpolationOnClient(n, b.in)
			if err != nil {
				return nil, err
			}
			if !client {
				source = ""
			}
			c.noteWired(n, source, cx.aliases) // the kernel reads it as the instance starts
			site.inputs = append(site.inputs, input{name: b.name, in: b.in, source: source})
		}
	}

	// the frames inside: the row of the call's for, then the instance
	inner := cx.scope
	if site.forEach != nil {
		inner = rowFrame(site.forEach, inner)
	}
	inner = componentFrame(def, inner)
	inside := cx
	inside.scope = inner
	inside.terminal = nil // a component's code is its own, never a command of a terminal around it
	if forAttr != nil {
		inside.forAt = &forSite{node: n, attr: forAttr}
	}

	if def.hasUI() {
		root := *def.root
		root.Attrs = append(append([]Attr(nil), def.root.Attrs...), extra...)
		// an alias written inside a component's .ui is its instance's own, unseen by the page
		rootCtx := ctx{inline: cx.inline, file: root.File, breadcrumb: cx.breadcrumb, kept: cx.kept, scope: inner, forAt: inside.forAt,
			aliases: &aliasFrame{owner: def.name, set: map[string]string{}, parent: cx.aliases}, pageBackground: cx.pageBackground, paint: cx.paint,
			onScreen: cx.onScreen, lazyAround: cx.lazyAround}
		return c.element(&root, rootCtx, site)
	}
	// what is written on the host and inside it reads the component's names first
	if err := c.checkReads(&host, inside, nil); err != nil {
		return nil, err
	}
	if term != nil {
		site.terminal = term
		// the parameter the kernel seeds the instance with, before its init (data-kitwork-scope)
		site.inputs = append(site.inputs, term.inputs()...)
	}
	return c.dispatch(&host, info, inside, site)
}

// outsideExpr is one expression a call reads where it stands.
type outsideExpr struct {
	attr *Attr
	word string // for, key, if, show, or the input's name
	e    *expr
}

func inputExprs(a *Attr, in *interpolation) []outsideExpr {
	var out []outsideExpr
	for _, p := range in.parts {
		if p.e != nil {
			out = append(out, outsideExpr{a, camel(a.Name), p.e})
		}
	}
	return out
}

// outsideError explains that a word of the call read a name of the component it calls.
func outsideError(c *compiler, n *Node, o outsideExpr, def *componentDef, name string) error {
	where := "đặt nó lên phần con"
	if def.hasUI() {
		where = "đặt nó trong " + path.Base(def.what(c.site))
	}
	switch o.word {
	case "if", "for", "key":
		return errAttr(n, o.attr, "%s đọc bên ngoài thẻ chủ: có thẻ chủ hay không, lặp mấy lần quyết trước khi %s có; %s là của %s", o.word, def.name, name, def.name)
	case "show":
		return errAttr(n, o.attr, "show đọc bên ngoài thẻ chủ; %s là của %s: %s", name, def.name, where)
	}
	return errAttr(n, o.attr, "giá trị của %s đọc bên ngoài thẻ chủ; %s là của %s, không phải của chỗ gọi", o.word, name, def.name)
}

// liveCall refuses a for or an if on a host that reads a component the browser runs: the kernel
// keeps a for's row and a component's instance in one state slot, so it cannot yet repeat or remove
// a host by live state (KITWORK_UI §20).
func (c *compiler) liveCall(n *Node, cx ctx, site *callSite, def *componentDef, forAttr *Attr) error {
	check := func(e *expr, a *Attr, word string) error {
		if e == nil {
			return nil
		}
		client, err := cx.scope.onClient(e, n, a)
		if err != nil || !client {
			return err
		}
		if def.instance() {
			return errAttr(n, a, "%s trên thẻ chủ của %s đọc trạng thái sống (%s): kernel chưa tách được hàng của for khỏi instance của component, nên chưa lặp hay bỏ một thẻ chủ theo trạng thái sống; dùng show trên phần con, hoặc đặt %s trên một thẻ bọc không phải thẻ chủ",
				word, def.name, e.source, word)
		}
		if word == "if" {
			return errAttr(n, a, "bên trong %s if theo trạng thái của component chưa chạy ở trình duyệt; dùng show", cx.scope.live().label())
		}
		return errAttr(n, a, "bên trong %s for theo trạng thái của component chưa chạy ở trình duyệt", cx.scope.live().label())
	}
	if err := check(site.ifExpr, attrNamed(n, "if"), "if"); err != nil {
		return err
	}
	if site.forEach != nil {
		return check(site.forEach.list, forAttr, "for")
	}
	return nil
}

// takesInput reports whether name can be an input of a component with a UI: a name it declares or
// one its .ui reads.
func (c *compiler) takesInput(def *componentDef, name string) bool {
	if _, declared := def.state[name]; declared {
		return true
	}
	return c.uiReads(def, map[string]bool{})[name]
}

// notAnInput explains an attribute at the call of a component with a UI that is none of the call's
// words and no input.
func (c *compiler) notAnInput(n *Node, a *Attr, def *componentDef) error {
	file := path.Base(def.what(c.site))
	words := "component, as, if, show, for, span, place và dữ liệu nó đọc"
	base := a.Name[strings.LastIndex(a.Name, ":")+1:]
	known := isEvent(a.Name) || presentation(base, def.rootTag()) || tagWord(def.rootTag(), base) || base == "align"
	if known {
		// a typo in the value is that error first (question 1): background="papr" is told of its colour
		if err := c.readValue(n, a); err != nil {
			return err
		}
	}
	switch {
	case isEvent(a.Name):
		return errAttr(n, a, "sự kiện %s đặt trong %s: hành vi của %s nằm ở đó; chỗ gọi nhận %s", a.Name, file, def.name, words)
	case presentation(base, def.rootTag()) || base == "align":
		return errAttr(n, a, "trình bày của %s nằm trong %s; chỗ gọi nhận %s", def.name, file, words)
	case known:
		return errAttr(n, a, "%s là thuộc tính của <%s>, gốc của %s: viết nó trong %s; chỗ gọi nhận %s", a.Name, def.rootTag(), def.name, file, words)
	}
	return errAttr(n, a, "%s không khai và %s không đọc %s; chỗ gọi nhận %s", def.name, file, camel(a.Name), words)
}

// inputValue reads one input: its value is read where the call stands.
func (c *compiler) inputValue(n *Node, a *Attr, site *callSite) (*interpolation, error) {
	in, err := parseInterpolation(a.Value, n, a)
	if err != nil {
		return nil, err
	}
	site.bindings = append(site.bindings, binding{name: camel(a.Name), in: in})
	return in, nil
}

// placement is span or place written at the call of a component with a UI or a layout, with its
// prefixes: the classes the merged root gets. The value is read the way the same attribute on an
// element is (classes.go, class): a word the stylesheet refuses is an error at the call, what it only
// warns of is a warning there.
func (c *compiler) placement(n *Node, a *Attr, chain, base, rootTag string) ([]string, error) {
	pre := prefix{keep: true}
	if chain != "" {
		var err error
		if pre, err = c.prefixOf(n, a, chain, base); err != nil { // out of order: a warning (compile.go)
			return nil, err
		}
		if !pre.keep {
			return nil, nil
		}
		if len(pre.states) > 0 {
			return nil, errAttr(n, a, "%s ở chỗ gọi nhận tiền tố độ rộng hay đích, không nhận tiền tố trạng thái", base)
		}
	}
	class, err := c.class(n, a, &element{tag: rootTag}, pre, base, a.Value)
	if err != nil || class == "" {
		return nil, err
	}
	return []string{class}, nil
}

// callShow checks a show written true or false at a call, plain or with a prefix, before it moves onto
// the root of another file: what is wrong with it is reported at the call, where it is written; a
// prefix out of order is a warning there (prefixOf, compile.go). It returns the attribute the root
// takes, its prefix in order, so the root reads it without telling the author a second time, from a
// file they did not write it in.
func (c *compiler) callShow(n *Node, a *Attr, chain string) (Attr, error) {
	if chain == "" {
		return *a, nil
	}
	if !literalBoolean(a.Value) {
		return *a, errAttr(n, a, "bản đầu chưa cho tiền tố đi cùng biểu thức; %s chỉ nhận \"true\" hoặc \"false\"", a.Name)
	}
	pre, err := c.prefixOf(n, a, chain, "show")
	if err != nil {
		return *a, err
	}
	if len(pre.states) > 0 {
		return *a, errAttr(n, a, "%s: ẩn hiện theo trạng thái chưa được hỗ trợ", a.Name)
	}
	moved := *a
	moved.Name = orderedPrefix(chain) + ":show"
	return moved, nil
}

// presentation reports whether name is a presentation attribute (of any tag: the stylesheet says
// which values a tag takes).
func presentation(name, _ string) bool { return presentationName(name) }

func logicFileName(c *compiler, def *componentDef) string {
	if def.logic != "" {
		return rel(c.site, def.logic)
	}
	return "component"
}

// kitworkUI compiles a call of one of Kitwork's components that bring their own UI.
func (c *compiler) kitworkUI(n *Node, cx ctx, def *componentDef) (op, error) {
	switch def.name {
	case "toast":
		return c.toast(n, cx)
	case "progressbar":
		return c.progressbar(n, cx)
	}
	return nil, errAt(n, "%s là component của Kitwork mà engine chưa dựng được", def.name)
}

// forbiddenHost is why a tag cannot carry component=, "" when it can.
func forbiddenHost(tag string) string {
	switch tag {
	case "page":
		return "page là khung của trang; component đặt trên một thẻ bên trong"
	case "content":
		return "content là nội dung của route; component đặt trên một thẻ bên trong"
	case "popover", "option", "empty":
		return tag + " là một phần của thẻ chứa nó, không đứng riêng"
	case "fragment":
		return "fragment không dựng phần tử nào"
	case "input", "image", "icon", "select", "textarea":
		return tag + " không có nội dung con, và phần tử của nó do Kitwork quyết"
	case "code", "quote":
		return tag + " chỉ chứa chữ"
	case "accordion", "accordions", "breadcrumb", "reader":
		return tag + " là phần tử của Kitwork, tự là instance; component= trên nó chỉ dành cho component mở rộng nó (" + tag + ".define), mà engine chưa đọc"
	}
	return ""
}

func hostList(hosts map[string]bool) string {
	names := sortedNames(hosts)
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = "<" + name + ">"
	}
	return strings.Join(quoted, ", ")
}

// suggestion writes the call of a component the way it is written: its host tag, component=, the
// alias the author wrote, then the rest.
func suggestion(tag string, def *componentDef, n *Node) string {
	var b strings.Builder
	b.WriteString("<" + tag + " component=\"" + def.name + "\"")
	rest := false
	for _, a := range n.Attrs {
		switch a.Name {
		case "as":
			b.WriteString(" as=\"" + a.Value + "\"")
		case "component":
		default:
			rest = true
		}
	}
	if rest {
		b.WriteString(" …")
	}
	if def.hasUI() {
		b.WriteString(" />")
	} else {
		b.WriteString(">…</" + tag + ">")
	}
	return b.String()
}

// registerAlias records an as where it is written: page-wide in page.ui, a region file or the content,
// the instance's own inside a component's .ui. An as inside a for would name every row.
func (c *compiler) registerAlias(n *Node, a *Attr, alias string, cx ctx, def *componentDef) error {
	if err := aliasWritten(n, a, alias); err != nil {
		return err
	}
	if f := cx.forAt; f != nil {
		if f.node.File == n.File {
			return errAttr(n, a, "as nằm trong for (%s:%d:%d): mỗi hàng là một instance, mà alias chỉ trỏ một", f.node.File, f.attr.Line, f.attr.Column)
		}
		return errAttr(f.node, f.attr, "%s:%d:%d đặt as=\"%s\", mà chỗ gọi này lặp bằng for: mỗi hàng là một instance, mà alias chỉ trỏ một", n.File, a.Line, a.Column, alias)
	}
	frame := cx.aliases
	if frame == nil {
		return nil
	}
	if first, ok := frame.set[alias]; ok {
		where := "trên trang"
		if frame.owner != "" {
			where = "trong " + frame.owner
		}
		return errAttr(n, a, "alias %s đã đặt ở %s; alias là duy nhất %s", alias, first, where)
	}
	frame.set[alias] = fmt.Sprintf("%s:%d:%d", n.File, a.Line, a.Column)
	if frame.defs == nil {
		frame.defs = map[string]*componentDef{}
	}
	frame.defs[alias] = def // what the alias reaches: a component the browser runs, or not yet (wiredAliases)
	return nil
}

// aliasWritten checks the value of an as: a name the kernel keeps says so (aliasName refuses those
// too, so it is asked first), then the shape of an alias.
func aliasWritten(n *Node, a *Attr, alias string) error {
	if reservedAliases[alias] {
		return errAttr(n, a, "%s là tên engine giữ chỗ; đặt alias khác", alias)
	}
	if !aliasName(alias) {
		return errAttr(n, a, "as là $ và một tên bắt đầu bằng chữ (as=\"$account\"), không phải %q", a.Value)
	}
	return nil
}

// reservedAliases are the names the kernel keeps for itself.
var reservedAliases = map[string]bool{"$": true, "$this": true, "$el": true, "$host": true, "$root": true, "$event": true, "$element": true}

// aliasName reports whether s is an alias as the kernel reads one: $, a letter, then letters, digits
// or _, and not a name the kernel keeps.
func aliasName(s string) bool {
	if len(s) < 2 || s[0] != '$' || reservedAliases[s] {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case i > 1 && (c >= '0' && c <= '9' || c == '_'):
		default:
			return false
		}
	}
	return true
}

// checkReads checks what an element reads where it stands: its attributes (except the call's
// words, which componentCall reads where the call stands) and its own runs of text.
func (c *compiler) checkReads(n *Node, cx ctx, skip map[string]bool) error {
	for i := range n.Attrs {
		a := &n.Attrs[i]
		if a.Name == "component" || a.Name == "as" || skip[a.Name] {
			continue
		}
		for _, e := range attrExpressions(n, a) {
			if err := c.checkNames(n, a, e, cx.scope, cx.aliases); err != nil {
				return err
			}
		}
	}
	for _, ch := range n.Children {
		if !ch.IsText() || !strings.Contains(ch.Text, "{{") {
			continue
		}
		in, err := parseInterpolation(ch.Text, ch, nil)
		if err != nil {
			continue // the compile of the text reports it
		}
		for _, p := range in.parts {
			if p.e == nil {
				continue
			}
			if err := c.checkNames(ch, nil, p.e, cx.scope, cx.aliases); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkNames checks the names one expression reads. An alias is recorded, to be found once every as
// of the page is known. A name a component declares, read in what the author wrote inside its host
// (not in its own .ui), and declared by an outer level too, is a warning: the nearest wins, on the
// server (render.go puts the component's state over the scope) as in the kernel, so the page renders
// as written, but markup reads one and looks like the other.
func (c *compiler) checkNames(n *Node, a *Attr, e *expr, scope *frame, aliases *aliasFrame) error {
	for _, name := range usedNames(e.ir) {
		if strings.HasPrefix(name, "$") {
			if aliasName(name) && aliases != nil {
				c.aliasUses = append(c.aliasUses, aliasUse{name: name, frame: aliases, node: n, attr: a})
			}
			continue
		}
		owner := scope.declaring(name)
		if owner == nil && !scope.inComponent() && !clientGlobals[name] {
			// outside every component and declared by nothing around: the view's, or the name of a
			// component on the page read outside its host (copy() beside a terminal): strayNames tells
			c.strays = append(c.strays, aliasUse{name: name, node: n, attr: a})
		}
		if owner == nil || owner.component == nil || owner.file != "" && owner.file == n.File {
			continue // the page's, a row's, or read inside the component's own .ui
		}
		line, column := n.Line, n.Column
		if a != nil {
			line, column = a.Line, a.Column
		}
		if outer := owner.parent.declaring(name); outer != nil {
			c.warn(n.File, line, column, fmt.Sprintf("%s ở đây là của %s, mà %s bao ngoài cũng khai %s: một tên một nghĩa — gọi qua alias ($tên.%s) hoặc đổi tên",
				name, owner.label(), outer.label(), name, name))
			continue
		}
		if c.view[name] {
			c.warn(n.File, line, column, fmt.Sprintf("%s ở đây là của %s, mà view của route %s (router.work) cũng khai %s: một tên một nghĩa — gọi qua alias ($tên.%s) hoặc đổi tên trong view",
				name, owner.label(), c.route(), name, name))
		}
	}
	return nil
}

// route is the route being compiled, as its folder under the site.
func (c *compiler) route() string {
	r := rel(c.site, c.routeDir)
	if r == "." || r == "" {
		return "/"
	}
	return "/" + r
}

// errUse is the error of an alias read where no host in reach sets it.
func (c *compiler) errUse(use aliasUse) error {
	message := fmt.Sprintf("%s chưa có trên trang này (route %s): không thẻ nào trong tầm đặt as=\"%s\"; alias đặt trong page.ui, vùng hay content.ui thì cả trang gọi được",
		use.name, c.route(), use.name)
	if use.attr != nil {
		return errAttr(use.node, use.attr, "%s", message)
	}
	return errAt(use.node, "%s", message)
}
