package ui

import (
	"fmt"
	"path/filepath"
	"strings"
)

// A layout piece is a piece of markup with no logic (KITWORK_UI §13): product-card.ui,
// order-table.ui. It is called by its tag name, the way a region is: <order-table />,
// <product-card for="product of products; key: product.id" />. It has no state and no instance, so it
// is no scope boundary: its file reads the context where it is called (the page's data, the row of a
// for around the call, the names of a component the call sits in) and what the call passes in.
//
// The call's words are if, show, for (with its key), span, place and source. Every other attribute is
// an input: a name the file reads, its value read where the call stands (product="{{ featured }}").
// The file's one root renders as it is, with the call's placement on it, never inside a wrapper.
// Presentation, events and children at the call are errors (the look and the behaviour are the
// file's); as is a warning: there is no instance to name, so it does nothing.
//
// A layout piece is found from the folder of the file that writes the call: that folder, its parents
// up to the site root, then .components/ of the identity. (Regions are layouts too, found from the
// route's folder: compile.go.) A name with logic is a component, called by its tag the same way
// (host.go), so a layout that gains a .work keeps its calls. A piece of the site has a dash in its
// name (product-card), and a name without one is Kitwork's: a core tag, a region, a Kitwork element
// or component is always that tag, so a .ui file named like one is an error.

// layoutCall compiles an element whose tag is outside the vocabulary: a piece of the site, a layout or
// a component. A tag that names nothing is an error.
func (c *compiler) layoutCall(n *Node, cx ctx) (op, error) {
	name := n.Tag
	if plannedElements[name] {
		if err := c.namedLikeATag(n); err != nil {
			return nil, err
		}
		return nil, errAt(n, "<%s> là phần tử của Kitwork mà engine chưa dựng được", name)
	}
	if n.written != "" && !siteName(name) {
		return nil, c.unknownWritten(n)
	}
	if strings.Contains(name, "-") && !componentName(name) {
		return nil, c.dashesWrong(n)
	}
	if !siteName(name) {
		return nil, c.withoutADash(n)
	}
	var def *componentDef
	var err error
	source := attrNamed(n, "source")
	if source.Name == "source" {
		def, err = c.layoutSource(n, source)
	} else {
		def, err = c.layoutNamed(n, name)
	}
	if err != nil {
		return nil, err
	}
	if def == nil {
		if n.written != "" {
			return nil, c.unknownWritten(n)
		}
		return nil, errAt(n, "không biết thẻ <%s>: không có layout %s.ui (từ thư mục của %s lên gốc site, rồi .components/), và không có component tên này",
			name, name, filepath.Base(n.File))
	}
	if def.instance() {
		// a component of the site: its host goes on as a call of it (host.go), found by the source when
		// one is written
		return c.componentCall(asHost(n, def.hostTag(), name), cx)
	}
	if err := c.checkLayoutRoot(n, def, cx.pageChild); err != nil {
		return nil, err
	}
	if len(n.SignificantChildren()) > 0 {
		return nil, errAt(n, "%s là layout nên thẻ gọi để rỗng: nội dung con không phải slot; nội dung nằm trong %s", name, rel(c.site, def.ui))
	}

	site := &callSite{def: def}
	rootTag := def.root.Tag
	var extra []Attr // static show: on the root, like any class of the call's placement
	var forAttr *Attr
	var outside []outsideExpr
	var inputs []*Attr // the attribute of each binding, in order
	reads := c.uiReads(def, map[string]bool{})
	for i := range n.Attrs {
		a := &n.Attrs[i]
		chain, base := "", a.Name
		if j := strings.LastIndex(a.Name, ":"); j >= 0 && !isEvent(a.Name) {
			chain, base = a.Name[:j], a.Name[j+1:]
		}
		if a.Bare { // an input without a value: a layout has no true-or-false word of its own at the call
			return nil, errAttr(n, a, "%s cần một giá trị", a.Name)
		}
		if chain != "" && (base == "as" || base == "for" || base == "if" || base == "source") {
			return nil, errAttr(n, a, "%s không nhận tiền tố", base)
		}
		switch {
		case base == "source":
		case base == "as":
			// names nothing, so it does nothing, and the page renders as written; its value is still read,
			// so a mistyped alias is an error, not a word that vanishes
			if err := aliasWritten(n, a, strings.TrimSpace(a.Value)); err != nil {
				return nil, err
			}
			c.warn(n.File, a.Line, a.Column, fmt.Sprintf("as đặt tên cho một instance; %s là layout (chỉ có giao diện, không có logic) nên không có instance để gọi: bỏ as", name))
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
			moved, err := c.callShow(n, a, chain)
			if err != nil {
				return nil, err
			}
			extra = append(extra, moved)
		case base == "span" || base == "place":
			made, err := c.placement(n, a, chain, base, rootTag)
			if err != nil {
				return nil, err
			}
			site.classes = append(site.classes, made...)
			if base == "place" && len(made) > 0 {
				site.places = append(site.places, c.placeWidth(chain)) // the root floats there (contrast.go)
			}
		case isEvent(a.Name):
			return nil, errAttr(n, a, "sự kiện %s đặt trong %s: %s là layout, hành vi nằm trong file của nó; chỗ gọi nhận %s", a.Name, rel(c.site, def.ui), name, layoutWords)
		case chain == "" && reads[camel(a.Name)]:
			in, err := c.inputValue(n, a, site)
			if err != nil {
				return nil, err
			}
			inputs = append(inputs, a)
			outside = append(outside, inputExprs(a, in)...)
		case c.renamedSide(n, a, chain, base) != "":
			return nil, errAttr(n, a, "%s", c.renamedSide(n, a, chain, base))
		case chain != "" || presentation(base, rootTag) || tagWord(rootTag, base) || base == "align":
			// a typo in the value is that error first (question 1): background="papr" is told of its colour
			if presentation(base, rootTag) || tagWord(rootTag, base) || base == "align" {
				if err := c.readValue(n, a); err != nil {
					return nil, err
				}
			}
			if !presentation(base, rootTag) && base != "align" && tagWord(rootTag, base) {
				return nil, errAttr(n, a, "%s là thuộc tính của <%s>, gốc của %s: viết nó trong %s; chỗ gọi nhận %s", a.Name, rootTag, name, rel(c.site, def.ui), layoutWords)
			}
			return nil, errAttr(n, a, "trình bày của %s nằm trong %s; chỗ gọi nhận %s", name, rel(c.site, def.ui), layoutWords)
		default:
			return nil, errAttr(n, a, "%s không đọc %s; chỗ gọi nhận %s", rel(c.site, def.ui), camel(a.Name), layoutWords)
		}
	}

	// what the call reads is read where it stands
	for _, o := range outside {
		if err := c.checkNames(n, o.attr, o.e, cx.scope, cx.aliases); err != nil {
			return nil, err
		}
	}
	// inside a component the browser runs, the call's for and if are the server's alone (the kernel
	// repeats and removes nothing by live state yet); its show the kernel keeps up to date
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
	// an input is a value the server reads once: a layout has no instance in the browser to keep a live
	// value up to date, so an input read from live state would be printed once and never follow it
	for i, b := range site.bindings {
		_, client, err := cx.scope.interpolationOnClient(n, b.in)
		if err != nil {
			return nil, err
		}
		if client && cx.scope.readsLive(b.in) { // an alias alone is read on the server here, as it was
			return nil, errAttr(n, inputs[i], "giá trị của %s đọc trạng thái sống của %s: %s là layout, không có instance ở trình duyệt giữ giá trị đó, nên nó chỉ là giá trị lúc dựng trang; đọc thẳng tên của %s trong %s",
				b.name, cx.scope.live().label(), name, cx.scope.live().label(), rel(c.site, def.ui))
		}
	}

	// the frames inside: the row of the call's for, then what the call passes in
	inner := cx.scope
	if site.forEach != nil {
		inner = rowFrame(site.forEach, inner)
	}
	if len(site.bindings) > 0 {
		inner = inputFrame(name, site.bindings, inner)
	}
	root := *def.root
	root.Attrs = append(append([]Attr(nil), def.root.Attrs...), extra...)
	rootCtx := cx
	rootCtx.file = root.File
	rootCtx.scope = inner
	if forAttr != nil {
		rootCtx.forAt = &forSite{node: n, attr: forAttr}
	}
	return c.element(&root, rootCtx, site)
}

// unknownWritten is the error of a tag written with capitals that names nothing once read lowercase
// (<ProductCard> is productcard): it names the tag as written, and when the capitals stand for dashes
// (product-card) and a layout or a component has that name, how to call it.
func (c *compiler) unknownWritten(n *Node) error {
	if dashed := dashedTag(n.written); dashed != n.Tag && siteName(dashed) {
		if def, err := c.layoutNamed(n, dashed); err == nil && def != nil {
			what := "layout " + rel(c.site, def.ui)
			if def.instance() {
				what = "component " + dashed + " (" + def.logicPlace(c.site) + ")"
			}
			return errAt(n, "không biết thẻ <%s>: tên viết thường, các chữ nối bằng gạch ngang; %s gọi bằng <%s>",
				n.written, what, dashed)
		}
	}
	return errAt(n, "không biết thẻ <%s> (đọc là <%s>): không có trong từ vựng của Kitwork, không có layout %s.ui (từ thư mục của %s lên gốc site, rồi .components/), và không có component tên này",
		n.written, n.Tag, n.Tag, filepath.Base(n.File))
}

// dashedTag is a tag written with capitals in the form a layout's name takes: lowercase, a dash where
// a capital follows a lowercase letter or a digit (ProductCard → product-card).
func dashedTag(written string) string {
	var b strings.Builder
	for i := 0; i < len(written); i++ {
		ch := written[i]
		if ch >= 'A' && ch <= 'Z' {
			if i > 0 && (written[i-1] >= 'a' && written[i-1] <= 'z' || written[i-1] >= '0' && written[i-1] <= '9') {
				b.WriteByte('-')
			}
			ch += 'a' - 'A'
		}
		b.WriteByte(ch)
	}
	return b.String()
}

// withoutADash is the error of a tag outside the vocabulary whose name has no dash: such a name is
// Kitwork's, and Kitwork has no tag of it. A piece of the site in reach under that name is told to
// take a dash; with none, the tag names nothing.
func (c *compiler) withoutADash(n *Node) error {
	name := n.Tag
	file := ""
	if componentName(name) {
		file = c.nearestUI(c.writingDir(n), name)
		if file == "" {
			root := filepath.Clean(c.site.Root)
			if logic, _, err := c.site.componentLogic(name, filepath.Join(root, "components.work"), filepath.Join(root, name+".work"),
				filepath.Join(c.site.identityDir(), name+".work")); err == nil {
				file = logic
			}
		}
	}
	if file != "" && filepath.Base(file) == "components.work" {
		// a file of several components: the define is renamed, not the file
		return errAt(n, "<%s> không có gạch ngang nên là thẻ của Kitwork, mà Kitwork không có thẻ này: tên layout và component của site có gạch ngang; trong %s đổi component.define(\"%s\", …) thành component.define(\"shop-%s\", …) rồi gọi <shop-%s>",
			name, rel(c.site, file), name, name, name)
	}
	if file != "" {
		return errAt(n, "<%s> không có gạch ngang nên là thẻ của Kitwork, mà Kitwork không có thẻ này: tên layout và component của site có gạch ngang; đổi tên %s (vd. shop-%s) rồi gọi <shop-%s>",
			name, rel(c.site, file), name, name)
	}
	return errAt(n, "không biết thẻ <%s>: Kitwork không có thẻ này, và tên layout hay component của site có gạch ngang (<product-card>, <shop-cart>)", name)
}

// dashesWrong is the error of a tag of the site whose dashes are misplaced (shop--cart, shop-cart-, a
// digit after a dash is fine): a site's name is lowercase words joined by single dashes. It writes the
// name the dashes meant, and names a file in reach under the name as written.
func (c *compiler) dashesWrong(n *Node) error {
	words := []string{}
	for _, word := range strings.Split(n.Tag, "-") {
		if word != "" {
			words = append(words, word)
		}
	}
	meant := strings.Join(words, "-")
	if file := c.nearestUI(c.writingDir(n), n.Tag); file != "" {
		return errAt(n, "tên layout và component của site viết thường, các từ nối bằng một gạch ngang: đổi tên %s thành %s.ui rồi gọi <%s>", rel(c.site, file), meant, meant)
	}
	return errAt(n, "không biết thẻ <%s>: tên layout và component của site viết thường, các từ nối bằng một gạch ngang (<%s>)", n.Tag, meant)
}

// layoutWords are the words a layout call takes, for a message.
const layoutWords = "if, show, for, span, place, source và dữ liệu file đọc"

// layoutNamed finds what a name of the site (with a dash) names from the folder of the file that
// writes it. A name the site or the identity defines logic for is that component. Otherwise the
// nearest <name>.ui is a layout piece; with neither, nil.
func (c *compiler) layoutNamed(n *Node, name string) (*componentDef, error) {
	if !siteName(name) {
		return nil, nil
	}
	s := c.site
	root := filepath.Clean(s.Root)
	probe := &Attr{Name: "component", Value: name, Line: n.Line, Column: n.Column}
	logic, _, err := s.componentLogic(name, filepath.Join(root, "components.work"), filepath.Join(root, name+".work"),
		filepath.Join(s.identityDir(), name+".work"))
	if err != nil {
		return nil, err
	}
	if logic != "" {
		return c.resolve(n, probe, name) // the component, with its .ui if it has one
	}
	dir := c.writingDir(n)
	file := c.nearestUI(dir, name)
	if file == "" {
		return nil, nil
	}
	if err := strayLogic(s, file, name); err != nil {
		return nil, err
	}
	return c.layoutFile(name, file)
}

// layoutSource finds the file a call's source names: a name looked up like the tag's own, or a path
// (./x, ../x from the writing file, .components/x for the identity). A layout or a component.
func (c *compiler) layoutSource(n *Node, a *Attr) (*componentDef, error) {
	value, err := pieceValue(n, a, "source")
	if err != nil {
		return nil, err
	}
	var def *componentDef
	if strings.Contains(value, "/") {
		def, err = c.resolve(n, a, value)
	} else if siteName(value) {
		def, err = c.layoutNamed(n, value)
	} else {
		return nil, errAttr(n, a, "source là tên của một layout hay component của site (viết thường, nối bằng gạch ngang) hoặc đường dẫn tới nó, không phải %q", value)
	}
	if err != nil {
		return nil, err
	}
	if def == nil {
		return nil, errAttr(n, a, "không tìm thấy %s (source=\"%s\")", filepath.Base(value), value)
	}
	return def, nil
}

// layoutFile is the layout piece of one .ui file, parsed once per page.
func (c *compiler) layoutFile(name, file string) (*componentDef, error) {
	key := "layout\x00" + file
	if def, ok := c.components[key]; ok {
		return def, nil
	}
	c.files[file] = true
	nodes, err := c.site.parseFile(file)
	if err != nil {
		return nil, err
	}
	def := &componentDef{name: name, ui: file}
	if def.root, err = singleRoot(nodes, rel(c.site, file)); err != nil {
		return nil, err
	}
	c.components[key] = def
	return def, nil
}

// nearestUI is the nearest <name>.ui seen from dir: dir and its parents up to the site root, then
// .components/ of the identity; "" when there is none.
func (c *compiler) nearestUI(dir, name string) string {
	key := dir + "\x00" + name
	if file, ok := c.nearest[key]; ok {
		return file
	}
	s := c.site
	// upward stops at the site root, and gives a folder outside the site (a file of the identity's
	// .components/) alone: that folder first, then the site root, then the identity
	folders := append(s.upward(dir), filepath.Clean(s.Root), s.identityDir())
	file := ""
	seen := map[string]bool{}
	for _, folder := range folders {
		if seen[folder] {
			continue
		}
		seen[folder] = true
		if candidate := filepath.Join(folder, name+".ui"); s.exists(candidate) {
			file = candidate
			break
		}
	}
	c.nearest[key] = file
	return file
}

// strayLogic refuses a <name>.work beside a branch's <name>.ui: logic is defined at the site root or
// for the identity, so nothing would read it, and the piece would stay a layout without a word.
func strayLogic(s *Site, file, name string) error {
	folder := filepath.Dir(file)
	if folder == filepath.Clean(s.Root) || folder == s.identityDir() {
		return nil
	}
	logic := filepath.Join(folder, name+".work")
	if !s.exists(logic) {
		return nil
	}
	return &Error{File: rel(s, logic), Line: 1, Column: 1, Message: fmt.Sprintf(
		"%s.work không được đọc: logic của component đặt ở gốc site (components.work hoặc %s.work) hoặc .components/%s.work của identity; ở đây %s là layout",
		name, name, name, rel(s, file))}
}

// checkLayoutRoot checks the root of a layout piece: the call puts its for, if, show, span and place
// on it, so it is an element that takes them, and it does not repeat itself. A reader as the root is a
// warning, not an error: it renders, but called twice it is two readers, and inside the content Drive
// replaces it with the content (accessibility, KITWORK_UI_DIAGNOSTICS question 4). A call that stands
// anywhere but straight in <page> hears that from the reader itself (compile.go, reader), once. A
// reader takes none of the call's words, so a call that writes one of them stays an error: the reader
// would drop it.
func (c *compiler) checkLayoutRoot(call *Node, def *componentDef, pageChild bool) error {
	root := def.root
	file := filepath.Base(root.File)
	for i := range root.Attrs {
		a := &root.Attrs[i]
		switch a.Name {
		case "for":
			return errAttr(root, a, "gốc của %s không mang for: lặp mấy lần là việc của chỗ gọi (<%s for=\"…\" />)", file, def.name)
		case "component":
			return componentWritten(root, a)
		}
	}
	info, known := catalog[root.Tag]
	switch {
	case kitworkComponents[root.Tag] != nil || c.callsComponent(root):
		return errAt(root, "gốc của %s là <%s>, một component: chỗ gọi <%s /> đặt for, if, show, span, place lên chính gốc này, nên gốc không là một component; bọc <%s> trong một <box>",
			file, root.Tag, def.name, root.Tag)
	case root.Tag == "view" || root.Tag == "page":
		return frameOrPageRoot(root, file)
	case !known:
		return errAt(root, "gốc của %s là <%s>, không phải thẻ của Kitwork: chỗ gọi <%s /> đặt for, if, show, span, place lên gốc, nên gốc là một thẻ như box, card, list; bọc <%s /> trong thẻ đó",
			file, root.Tag, def.name, root.Tag)
	case info.region:
		return errAt(root, "gốc của %s là vùng <%s>: vùng gọi bằng thẻ của nó (<%s /> lấy %s.ui, source= lấy file khác); layout dựng trên một thẻ như box, card, list",
			file, root.Tag, root.Tag, root.Tag)
	case root.Tag == "fragment":
		return errAt(root, "gốc của %s là <fragment>, không dựng phần tử nào để chỗ gọi đặt for, if, show, span, place lên; dùng <box>", file)
	case root.Tag == "reader":
		for i := range call.Attrs {
			a := &call.Attrs[i]
			switch base := a.Name[strings.LastIndex(a.Name, ":")+1:]; base {
			case "for", "if", "show", "span", "place":
				return errAt(root, "gốc của %s là <reader>: reader đặt thẳng trong view.ui, một lần; reader không nhận %s mà chỗ gọi %s:%d:%d đặt lên gốc",
					file, a.Name, call.File, a.Line, a.Column)
			}
		}
		if pageChild {
			c.warn(root.File, root.Line, root.Column, fmt.Sprintf("gốc của %s là <reader>: reader đặt thẳng trong view.ui, một lần", file))
		}
	}
	return nil
}

// namedLikeATag refuses a .ui file in reach named like a tag of Kitwork (a core tag or a Kitwork
// element): that tag always means Kitwork's, so the file would never be called.
func (c *compiler) namedLikeATag(n *Node) error {
	info, known := catalog[n.Tag]
	if known && (info.region || n.Tag == "page") {
		return nil // region files are layouts called by the region's tag
	}
	file := c.nearestUI(c.writingDir(n), n.Tag)
	if file == "" {
		if kitworkComponents[n.Tag] != nil {
			return nil // logic under a Kitwork component's name is told where the component is found
		}
		return c.logicNamedLikeATag(n, known)
	}
	what := "thẻ lõi"
	switch {
	case kitworkComponents[n.Tag] != nil:
		what = "component"
	case !known || isKitworkElement(n.Tag):
		what = "phần tử"
	}
	return &Error{File: rel(c.site, file), Line: 1, Column: 1, Message: fmt.Sprintf(
		"%s đặt tên trùng %s <%s> của Kitwork: <%s> luôn là %s của Kitwork nên file này không bao giờ được gọi; đặt cho layout tên khác (viết thường, nối bằng gạch ngang) rồi gọi bằng tên đó",
		filepath.Base(file), what, n.Tag, n.Tag, what)}
}

// logicNamedLikeATag refuses a component.define of the site named like a core tag or a Kitwork element
// (card, reader): that tag always means Kitwork's, so the logic would never be read. Every element of a
// tag asks, so the answer is kept by name for the page.
func (c *compiler) logicNamedLikeATag(n *Node, known bool) error {
	logic, asked := c.logicNamed[n.Tag]
	if !asked {
		s := c.site
		root := filepath.Clean(s.Root)
		var err error
		logic, _, err = s.componentLogic(n.Tag, filepath.Join(root, "components.work"), filepath.Join(root, n.Tag+".work"),
			filepath.Join(s.identityDir(), n.Tag+".work"))
		if err != nil {
			return err
		}
		c.logicNamed[n.Tag] = logic
	}
	if logic == "" {
		return nil
	}
	what := "thẻ lõi"
	if !known || isKitworkElement(n.Tag) {
		what = "phần tử"
	}
	return &Error{File: rel(c.site, logic), Line: 1, Column: 1, Message: fmt.Sprintf(
		"component.define(\"%s\", …) đặt tên trùng %s <%s> của Kitwork: <%s> luôn là %s của Kitwork nên logic này không bao giờ được đọc; tên component của site có gạch ngang (shop-%s)",
		n.Tag, what, n.Tag, n.Tag, what, n.Tag)}
}

// isKitworkElement reports whether a tag is one of Kitwork's own elements rather than the core
// vocabulary.
func isKitworkElement(tag string) bool {
	switch tag {
	case "accordion", "accordions", "breadcrumb", "popover", "reader", "icon", "image":
		return true
	}
	return plannedElements[tag]
}
