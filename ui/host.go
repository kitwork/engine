package ui

import (
	"fmt"
	"path"
	"strings"

	"github.com/kitwork/engine/ui/stylesheet"
)

// A component — a name with logic — is written as its tag (KITWORK_UI §13, 18/10/2026): one of
// Kitwork's by a name without a dash (<tabs>, <carousel>), one of the site's by a name with one
// (<shop-cart>, a layout's form). The tag builds one element, the host: the compiler gives it the
// host's tag of .ui (componentDef.hostTag) and a component attribute naming what it calls (asHost),
// and componentCall goes on from there; component= written by the author is an error that says which
// tag to write (componentWritten). Two shapes:
//
//   - With a UI (shop-cart.ui with its shop-cart.work, Kitwork's progressbar): the file's one root is
//     the host, and the call is empty — <shop-cart as="$cart" />. The two are one element: the file
//     owns the look and the behaviour; the call adds only its own words (as, if, show, for with its
//     key, span and place, with width prefixes) and inputs, which are every other attribute and must
//     be names the component declares or its .ui reads.
//   - Without a UI (dropdown, appearance, a site component that has only a .work): the host is a box
//     (a button for appearance) and the attributes written on the tag are the box's; what is written
//     inside is the component's UI. An attribute that is none of the box's own is an input; one the
//     box and the component both claim is an error. The terminal is one of these with a rule of its
//     own: its own word (typing) leaves the host first, and each <code> inside is a command with a
//     prompt before it (terminal.go). A component's inside is never a terminal's: its code is its own.
//
// Which side reads a name: if, show, for (and its key), as and the values of inputs are read where
// the call stands, and may not read the component's own names. Everything else written on a host
// without a UI, and inside it, reads the component's names first, then outward; a name the component
// and an outer level (a component around it, a row, the route's view) both declare is a warning
// there: the nearest wins. Inside a component's own .ui its names win without question.

// kitworkCall compiles one of Kitwork's components written as its tag: <tabs as="$files">…</tabs>.
func (c *compiler) kitworkCall(n *Node, kit *kitworkComponent, cx ctx) (op, error) {
	if kit.planned {
		return nil, errAt(n, "<%s> là component của Kitwork mà engine chưa dựng được", n.Tag)
	}
	if err := c.namedLikeATag(n); err != nil {
		return nil, err
	}
	if a := attrNamed(n, "source"); a.Name == "source" {
		return nil, errAttr(n, a, "source lấy một file của site; <%s> là của Kitwork, không có source", n.Tag)
	}
	return c.componentCall(asHost(n, kit.hostTag(), n.Tag), cx)
}

// asHost is the host a component's tag builds on: the same element under the host's tag of .ui, with
// the tag the author wrote as a component attribute first, where the tag stands. componentCall finds
// the component by it, or by the source written beside it, and the page carries the component's name
// as data-kitwork-component. The node as written is left as it is.
func asHost(n *Node, tag, component string) *Node {
	host := *n
	host.Tag = tag
	host.Attrs = append([]Attr{{Name: "component", Value: component, Line: n.Line, Column: n.Column}}, n.Attrs...)
	return &host
}

// called is a component's call as the author wrote it, for a message: its tag (<tabs>, <shop-cart>,
// <my-row> with a source). n is the host it builds on (asHost).
func called(n *Node) string {
	return "<" + attrNamed(n, "component").Value + ">"
}

// tagOf is an element's tag as the author wrote it, for a message: a component's tag (<dropdown>) for
// the host it built (a box, a button), the element's own tag otherwise.
func (c *compiler) tagOf(n *Node) string {
	if tag, ok := c.hostTags[n]; ok {
		return tag
	}
	return n.Tag
}

// componentWritten is the error of component= written by the author: since 18/10/2026 a component is
// written as its tag (KITWORK_UI §13), and the message writes the tag. A name without a dash that
// Kitwork has (a component, an element, a core tag) is that tag; a site's name takes a dash, so one
// without is told to take one.
func componentWritten(n *Node, a *Attr) error {
	value := strings.TrimSpace(a.Value)
	name := path.Base(value)
	if !componentName(name) || strings.Contains(value, "{{") {
		// no name a tag could take: {{ }}, two names, capitals, nothing
		return errAttr(n, a, "component= đã bỏ: component là một thẻ, tên của nó là tên thẻ, viết thường, cố định (<tabs>, <shop-cart>)")
	}
	tag := name
	kit := kitworkComponents[name]
	if _, kitworkTag := catalog[name]; kit == nil && !kitworkTag && !plannedElements[name] && !siteName(name) {
		tag = "shop-" + name
	}
	call := "<" + tag
	if strings.Contains(value, "/") && !strings.HasPrefix(value, "kitwork/") {
		call += ` source="` + strings.TrimSuffix(value, name) + tag + `"` // the file renamed with the name
	}
	if alias, ok := n.Attr("as"); ok {
		call += ` as="` + alias + `"`
	}
	inside := len(n.SignificantChildren()) > 0
	if !inside {
		call += " />"
	} else {
		call += ">…</" + tag + ">"
	}
	message := fmt.Sprintf("component= đã bỏ: component là một thẻ, viết %s", call)
	if info, ok := catalog[n.Tag]; ok && (info.region || n.Tag == "page") {
		message += fmt.Sprintf(" bên trong <%s>", writtenAs(n.Tag)) // a region is no component's host: it holds one
	} else if _, chosen := catalog[n.Tag]; chosen && kit != nil && kit.root == "" && n.Tag != kit.hostTag() {
		message += fmt.Sprintf(" (<%s> dựng một <%s>; cần <%s> thì đặt nó bên trong)", name, kit.hostTag(), n.Tag)
	}
	if tag != name {
		message += fmt.Sprintf("; tên component của site có gạch ngang, tên không gạch ngang là của Kitwork: đổi tên %s thành %s", name, tag)
	}
	return errAttr(n, a, "%s", message)
}

// componentCall compiles the host of a component (asHost): an element whose component attribute is
// the tag the author wrote, and names what it calls unless a source does.
func (c *compiler) componentCall(n *Node, cx ctx) (op, error) {
	attr := attrNamed(n, "component")
	info := catalog[n.Tag] // a host's tag: box, button, the root of the component's UI
	written := attr.Value
	c.hostTags[n] = written
	find := attr
	if source := attrNamed(n, "source"); source.Name == "source" {
		find = source // a component of the site the call names by its file (layoutCall checked it)
	}
	def, err := c.resolveComponent(n, find)
	if err != nil {
		return nil, err
	}
	c.called[def.name] = def // its names, read outside its host, are told (strayNames)
	if def.root != nil {
		if err := c.checkComponentRoot(def); err != nil {
			return nil, err
		}
	}
	// a component with a rule for what is inside it (the terminal): its own words are read here and
	// leave the host, which then goes on as the host of a component without a UI
	var term *terminalSpec
	if def.kitwork != nil && def.kitwork.around {
		var err error
		if n, term, err = c.terminalHost(n); err != nil {
			return nil, err
		}
	}
	// the tabs' own word (persist) leaves the host the same way: the engine reads it into the name the
	// choice is kept under once it knows the tabs (tabs.go)
	persist := ""
	if def.kitwork != nil && def.name == "tabs" {
		var err error
		if n, persist, err = c.tabsWords(n); err != nil {
			return nil, err
		}
	}
	// the toast's own words (place, limit, alt) leave the host the same way, and the blueprint of a
	// notice is found inside it before anything compiles (toast.go)
	var toast *toastScope
	if def.kitwork != nil && def.name == "toast" {
		var err error
		if n, toast, err = c.toastHost(n, attr); err != nil {
			return nil, err
		}
		// a lazy frame around places the stack, fixed, by itself and not by the screen (15/10/2026); a
		// glass frame does too (its backdrop-filter)
		c.tellLazyAround(cx, "<toast>", n, "một khung lazy giữ phần cố định (position:fixed) bên trong theo chính nó, không theo màn hình, nên ngăn thông báo nằm ngoài màn hình")
		if glass := glassAround(cx.paint); glass != nil {
			c.warn(n.File, attr.Line, attr.Column, fmt.Sprintf("toast trong khung kính (material ở %s:%d:%d) bám theo khung đó, không theo màn hình: kính giữ phần cố định bên trong theo chính nó; đặt toast ngoài khung kính, như trong overlay.ui",
				glass.node.File, glass.attr.Line, glass.attr.Column))
		}
	}
	// the network's own words (place, delay, duration) leave the host the same way (network.go); placed,
	// it is fixed to the screen like the toast, and a frame around that holds what is fixed is told the
	// same way
	var network *networkSpec
	if def.kitwork != nil && def.name == "network" {
		var err error
		if n, network, err = c.networkHost(n); err != nil {
			return nil, err
		}
		if network.place != "" {
			c.tellLazyAround(cx, "<network place=\""+network.place+"\">", n, "một khung lazy giữ phần cố định (position:fixed) bên trong theo chính nó, không theo màn hình, nên ngăn thông báo nằm ngoài màn hình")
			if glass := glassAround(cx.paint); glass != nil {
				c.warn(n.File, attr.Line, attr.Column, fmt.Sprintf("network có place trong khung kính (material ở %s:%d:%d) bám theo khung đó, không theo màn hình: kính giữ phần cố định bên trong theo chính nó; đặt network ngoài khung kính, như trong overlay.ui",
					glass.node.File, glass.attr.Line, glass.attr.Column))
			}
		}
	}
	// the carousel's own words (alt, transition, duration) leave the host the same way, and its track
	// is found before anything compiles (carousel.go)
	var carousel *carouselScope
	if def.kitwork != nil && def.name == "carousel" {
		var err error
		if n, carousel, err = c.carouselHost(n, attr); err != nil {
			return nil, err
		}
	}
	// the countdown's own word (duration) leaves the host the same way, and the <time> that holds the time
	// left is found before anything compiles (countdown.go)
	var countdown *countdownScope
	if def.kitwork != nil && def.name == "countdown" {
		var err error
		if n, countdown, err = c.countdownHost(n, attr); err != nil {
			return nil, err
		}
	}
	// the editor's parts (its writing area, toolbars, the link's popover) are found before anything
	// compiles, and the words it refuses told; its own word (format) leaves the host (editor.go)
	var editor *editorScope
	if def.kitwork != nil && def.name == "editor" {
		var err error
		if n, editor, err = c.editorHost(n, attr, cx); err != nil {
			return nil, err
		}
	}
	if def.hasUI() {
		if def.kitwork != nil {
			return c.kitworkUI(n, cx, def)
		}
		if len(n.SignificantChildren()) > 0 {
			return nil, errAt(n, "%s có giao diện (%s) nên thẻ gọi để rỗng: nội dung con không phải slot; đổi giao diện thì sửa %s",
				called(n), def.what(c.site), path.Base(def.what(c.site)))
		}
	} else if len(n.SignificantChildren()) == 0 && (def.kitwork == nil || !def.kitwork.empty) {
		return nil, errAt(n, "%s không có giao diện: nội dung bên trong thẻ là giao diện của nó (%s…</%s>)", called(n), called(n), written)
	}
	site := &callSite{def: def, state: def.state}
	if carousel != nil {
		site.carousel = carousel
		site.classes = append(site.classes, carousel.classes()...)
		site.inputs = append(site.inputs, carousel.inputs()...)
		site.state = carousel.startState(site.state)
		c.carousels = append(c.carousels, carousel)
		if carousel.countFrom != "" {
			// slides a for repeats: how many is the page's data, read where the call stands, as an input
			// the author could have written (count="{{ 2 + (mons).length }}")
			in, err := parseInterpolation("{{ "+carousel.countFrom+" }}", n, attr)
			if err != nil {
				return nil, err
			}
			site.bindings = append(site.bindings, binding{name: "count", in: in})
		}
	}
	if countdown != nil {
		site.inputs = append(site.inputs, countdown.inputs()...) // the duration, in milliseconds
		site.state = countdown.startState(site.state)            // all of it left, not playing
		c.countdowns = append(c.countdowns, countdown)
	}
	if editor != nil {
		// the box its hidden field is placed against, and its first state, read where the call stands
		// for each request: the counts of its value cleaned, unsaved, open, address (editor.go)
		site.classes = append(site.classes, stylesheet.EditorClass)
		site.bindings = append(site.bindings, editor.bindings()...)
	}
	if network != nil {
		site.network = network
		site.classes = append(site.classes, network.classes()...)
		site.inputs = append(site.inputs, network.inputs()...) // delay and duration, as written (network.go)
		if network.place != "" {
			site.places = append(site.places, 0) // fixed over whatever the page holds there, at every width (contrast.go)
		}
	}
	c.hostTags[n] = written // the component's own words may have left the host (terminal, tabs, toast…)
	host := *n              // the host as the author wrote it, without the call's words (a component without a UI)
	host.Attrs = nil
	c.hostTags[&host] = written
	if def.kitwork != nil && def.kitwork.empty && len(n.SignificantChildren()) == 0 && !writesAttribute(n, "show") {
		// written empty, it is heard and never seen: it takes no room in the page's flow (a gap of a
		// row around it, a line of a list), and the kernel still starts it
		host.Attrs = append(host.Attrs, Attr{Name: "show", Value: "false", Line: attr.Line, Column: attr.Column})
	}
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
		if a.Bare && a.Value == "" {
			// a word of the box a site component builds: lint did not know the tag (it does one of Kitwork's)
			a.Value = "true"
			c.warn(n.File, a.Line, a.Column, fmt.Sprintf("thuộc tính đúng/sai luôn có giá trị: viết %s=\"true\" (viết trơn đọc là true)", a.Name))
		}
		if chain != "" && (base == "as" || base == "for" || base == "if" || base == "source") {
			return nil, errAttr(n, a, "%s không nhận tiền tố", base)
		}
		switch {
		case base == "component" && i > 0:
			return nil, componentWritten(n, a) // the author's own, beside the tag
		case base == "component":
		case base == "source" && def.kitwork == nil:
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
				if def.takes(name) && chain == "" {
					return nil, errAttr(n, a, "%s là thuộc tính của <%s> (%s dựng một <%s>) và cũng là tên %s khai: một chữ một nghĩa trên một thẻ; đổi tên trong %s",
						a.Name, n.Tag, called(n), n.Tag, def.name, logicFileName(c, def))
				}
				host.Attrs = append(host.Attrs, *a)
				continue
			}
			if !def.takes(name) {
				if message := c.webWord(n, a); message != "" {
					// a word of the web (onoffline, href, class) is told the .ui word, as on any tag (compile.go)
					return nil, errAttr(n, a, "%s", message)
				}
				return nil, errAttr(n, a, "%s không phải thuộc tính của <%s> (%s dựng một <%s>), và %s không khai %s", a.Name, n.Tag, called(n), n.Tag, def.name, name)
			}
			in, err := c.inputValue(n, a, site)
			if err != nil {
				return nil, err
			}
			outside = append(outside, inputExprs(a, in)...)
		}
	}
	// what the call reads is read where it stands, never the component's own names
	for _, o := range outside {
		if err := c.checkNames(n, o.attr, o.e, callScope(o, site, cx.scope), cx.aliases); err != nil {
			return nil, err
		}
		for _, name := range usedNames(o.e.ir) {
			if _, own := def.state[name]; own {
				return nil, outsideError(c, n, o, def, name)
			}
		}
		if err := c.liveInput(n, o, callScope(o, site, cx.scope), def); err != nil {
			return nil, err
		}
	}
	// live for and if on a host: the kernel keeps a for's row and a component's instance in one place
	// for now, so neither repeats nor removes a host by live state yet
	if err := c.liveCall(n, cx, site, def, forAttr); err != nil {
		return nil, err
	}
	// a component whose jobs go through the bridge: what it requires, and its place on the page
	// (component_live.go)
	passed := make(map[string]bool, len(site.bindings))
	for _, b := range site.bindings {
		passed[b.name] = true
	}
	if err := c.missingInputs(n, def, passed); err != nil {
		return nil, err
	}
	if err := c.liveSiteCall(n, cx, site); err != nil {
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
			if _, bound := def.state[b.name]; def.live() && !bound {
				continue // an input the look does not read stays the server's (KITWORK_COMPONENTS.md mục 5)
			}
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
	inner.countdown = countdown // nil for any other component (countdown.go)
	c.programOff = true         // what is inside a component of Kitwork is the kernel's: no program yet (page_program.go)
	inside := cx
	inside.scope = inner
	inside.terminal = nil // a component's code is its own, never a command of a terminal around it
	inside.tabs = nil     // nor is its button a tab of a tabs around it

	// nor is it a notice of a toast around it, nor a part of a carousel around it
	inside.toast, inside.toastRow = nil, false
	inside.carousel = nil
	if carousel != nil {
		carousel.alias, inside.carousel = site.alias, carousel
	}
	// nor is its <time> the time left of a countdown around it
	inside.countdown = nil
	if countdown != nil {
		countdown.alias, countdown.aliases, inside.countdown = site.alias, cx.aliases, countdown
	}
	// nor is its <textarea> the writing area of an editor around it, nor its button one of its commands
	inner.editor, inside.editor = editor, editor // nil for any other component (editor.go)
	if editor != nil {
		editor.alias, editor.aliases = site.alias, cx.aliases
	}
	if toast != nil {
		// the stack: its blueprint of a notice is one of the children, its place and limit go in (toast.go)
		toast.alias, toast.aliases = site.alias, inside.aliases
		site.toast, inside.toast = toast, toast
		site.classes = append(site.classes, toast.classes()...)
		site.inputs = append(site.inputs, toast.inputs()...)
		if site.alias == "" {
			c.warn(n.File, attr.Line, attr.Column, `toast không có as: trang không gọi được show; viết <toast as="$toast"> rồi gọi $toast.success('Đã lưu.') ở bất cứ đâu`)
		}
	}
	if forAttr != nil {
		inside.forAt = &forSite{node: n, attr: forAttr}
	}
	if def.kitwork != nil && def.name == "tabs" {
		// the tablist: its tabs by name, the first chosen unless the call chose one (tabs.go)
		tabs, err := c.tabsHost(n, site, inside, persist)
		if err != nil {
			return nil, err
		}
		site.tabs, inside.tabs = tabs, tabs
	}
	if site.alias != "" && def.delivered() && cx.aliases != nil {
		// read on the server as the instance starts: $files.selected before the kernel runs — page-wide
		// for the page's aliases (page.go), inside the instance for one in a component's own .ui
		start := aliasStart{alias: site.alias, state: site.state, bindings: site.bindings}
		if cx.aliases.owner == "" {
			c.aliasStarts = append(c.aliasStarts, start)
		} else {
			cx.aliases.starts = append(cx.aliases.starts, start)
		}
	}

	if def.hasUI() {
		root := *def.root
		root.Attrs = append(append([]Attr(nil), def.root.Attrs...), extra...)
		// an alias written inside a component's .ui is its instance's own, unseen by the page
		rootCtx := ctx{inline: cx.inline, file: root.File, breadcrumb: cx.breadcrumb, kept: cx.kept, scope: inner, forAt: inside.forAt,
			aliases: &aliasFrame{owner: def.name, set: map[string]string{}, parent: cx.aliases}, pageBackground: cx.pageBackground, paint: cx.paint,
			onScreen: cx.onScreen, lazyAround: cx.lazyAround}
		made, err := c.element(&root, rootCtx, site)
		if o, ok := made.(*elemOp); ok && err == nil {
			// its own aliases read as they start, in the instance's names (a root folded to static HTML
			// reads none)
			o.aliases = rootCtx.aliases.starts
		}
		return made, err
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

// callScope is where a word of a call reads: the row of the call's own for, for every word but that for's
// list, which is read where the call stands. The page renders it so: if, show, the key and every input once a
// row, the row's names bound (render.go, renderOne).
func callScope(o outsideExpr, site *callSite, scope *frame) *frame {
	if site.forEach == nil || o.word == "for" {
		return scope
	}
	return rowFrame(site.forEach, scope)
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
		return errAttr(n, o.attr, "%s đọc ở chỗ gọi, bên ngoài %s: có %s hay không, lặp mấy lần quyết trước khi nó có; %s là của %s", o.word, called(n), called(n), name, def.name)
	case "show":
		return errAttr(n, o.attr, "show đọc ở chỗ gọi, bên ngoài %s; %s là của %s: %s", called(n), name, def.name, where)
	}
	return errAttr(n, o.attr, "giá trị của %s đọc ở chỗ gọi, bên ngoài %s; %s là của %s, không phải của chỗ gọi", o.word, called(n), name, def.name)
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
			return errAttr(n, a, "%s trên %s đọc trạng thái sống (%s): kernel chưa tách được hàng của for khỏi instance của component, nên chưa lặp hay bỏ một component theo trạng thái sống; dùng show trên phần con, hoặc đặt %s trên một <box> bọc ngoài",
				word, called(n), e.source, word)
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
	if def.takes(name) {
		return true
	}
	return c.uiReads(def, map[string]bool{})[name]
}

// notAnInput explains an attribute at the call of a component with a UI that is none of the call's
// words and no input.
func (c *compiler) notAnInput(n *Node, a *Attr, def *componentDef) error {
	file := path.Base(def.what(c.site))
	words := "as, if, show, for, span, place và dữ liệu nó đọc"
	base := a.Name[strings.LastIndex(a.Name, ":")+1:]
	// a side or a corner named with start or end is told its name of today, as on any element
	if message := c.renamedSide(n, a, strings.TrimSuffix(strings.TrimSuffix(a.Name, base), ":"), base); message != "" {
		return errAttr(n, a, "%s", message)
	}
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
	case "progressbar":
		return c.progressbar(n, cx)
	}
	return nil, errAt(n, "%s là component của Kitwork mà engine chưa dựng được", def.name)
}

// forbiddenHost is why a tag cannot be the root of a component's .ui (the host), "" when it can.
func forbiddenHost(tag string) string {
	switch tag {
	case "page":
		return "page là khung của trang"
	case "content":
		return "content là nội dung của route"
	case "popover", "option", "empty":
		return tag + " là một phần của thẻ chứa nó, không đứng riêng"
	case "fragment":
		return "fragment không dựng phần tử nào"
	case "input", "image", "icon", "select", "textarea", "richtext":
		return tag + " không có nội dung con, và phần tử của nó do Kitwork quyết"
	case "code", "quote":
		return tag + " chỉ chứa chữ"
	case "accordion", "accordions", "breadcrumb", "reader":
		return tag + " là phần tử của Kitwork, tự là instance"
	}
	return ""
}

// registerAlias records an as where it is written: page-wide in view.ui, a region file or the content,
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
				c.noteAliasExpr(n, a, e, aliases) // a call through the toast's alias is checked (toast.go)
			}
			continue
		}
		owner := scope.declaring(name)
		if owner == nil && !clientGlobals[name] {
			// declared by nothing around, in a component or not: what the page is given, or notGiven tells
			// (given.go); outside every component it may be the name of a component on the page read
			// outside its host (copy() beside a terminal): strayNames tells
			c.unowned = append(c.unowned, aliasUse{name: name, node: readAt(n, a, e), attr: a})
			if !scope.inComponent() {
				c.strays = append(c.strays, aliasUse{name: name, node: n, attr: a})
			}
		}
		if owner == nil || owner.component == nil || owner.file != "" && owner.file == n.File {
			continue // the page's, a row's, or read inside the component's own .ui
		}
		line, column := n.Line, n.Column
		if a != nil {
			line, column = a.Line, a.Column
		}
		if outer := owner.parent.declaring(name); outer != nil {
			if c.editorMenuOwn(n, name) {
				continue // the toggle() that opens a dropdown of an editor's toolbar, the engine's reading (editor.go)
			}
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
	message := fmt.Sprintf("%s chưa có trên trang này (route %s): không thẻ nào trong tầm đặt as=\"%s\"; alias đặt trong view.ui, vùng hay content.ui thì cả trang gọi được",
		use.name, c.route(), use.name)
	if use.attr != nil {
		return errAttr(use.node, use.attr, "%s", message)
	}
	return errAt(use.node, "%s", message)
}

// componentEventsHere refuses an event only a Kitwork component sends (componentEvents) on any tag
// but that component's host: <box offline="…"> would wait for an event nothing ever sends there. A
// component of the site with that component's name is the site's, and sends none of them either. The
// host it says to write carries the event alone, with its modifiers (click,offline:once → offline:once),
// written empty for a component that may be (<network offline="…" />), open for one that holds what
// the author draws (<countdown finish="…">).
func (c *compiler) componentEventsHere(n *Node, events []Attr, site *callSite) error {
	for i := range events {
		a := &events[i]
		head, modifiers, _ := strings.Cut(a.Name, ":")
		for _, event := range strings.Split(head, ",") {
			event = strings.TrimSpace(event)
			owner := componentEvents[event]
			if owner == "" {
				continue
			}
			if site != nil && site.def != nil && site.def.kitwork != nil && site.def.name == owner {
				continue // a site's logic under Kitwork's name is refused at its file (component.go)
			}
			written := event
			if modifiers != "" {
				written += ":" + modifiers
			}
			end := " />"
			if k := kitworkComponents[owner]; k != nil && !k.empty {
				end = ">"
			}
			return errAttr(n, a, "%s là sự kiện của component %s, chỉ thẻ của nó nghe được: <%s %s=\"…\"%s",
				event, owner, owner, written, end)
		}
	}
	return nil
}
