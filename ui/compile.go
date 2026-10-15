package ui

import (
	"fmt"
	"html"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/kitwork/engine/jit/icons"
	"github.com/kitwork/engine/ui/stylesheet"
)

// Page is one route compiled for one target: the page frame, its regions and the route content,
// joined into one op tree. It is prepared once per source version and rendered per request.
type Page struct {
	root op
	// Files lists every source the page was built from, for hot reload.
	Files []string
	// skeleton is the page with every branch present, for the presentation passes (skeleton.go)
	skeleton skeletonState
	// rules is the page's stylesheet: the rule of every class any branch of its HTML can carry,
	// recorded as the page compiled (page.<hash>.css)
	rules *stylesheet.Rules
	// uses are the elements the page holds whose look sets a font weight or face of its own (badge,
	// heading, code), for the weights and faces its fonts file ships
	uses map[string]bool
	// warnings are what its compile found that works but is probably not what the author wants
	// (warning.go), in the order found; warned is every warning the page has given, those of its
	// compile and those its renders name (a value from data that made no class), so each is said once
	warnings []Warning
	warned   warnings
	// palette is the route's colour chain, for a document shown with its stylesheets inline
	palette *stylesheet.Palette
	// rightToLeft says the site's router.language, the one the compile read (Site.Language), is written
	// right to left: the document carries dir="rtl", so start and end flip in the browser exactly as the
	// warnings of left and right said they would (checkSides)
	rightToLeft bool
}

// element is what the class rules need to know about the element being compiled.
type element struct {
	tag  string // Kitwork tag
	html string // output element
}

type ctx struct {
	inline     bool     // text here becomes <span>
	flex       bool     // the parent lays out its children (flex, grid): whitespace between them is not content
	kept       bool     // outside <content />: kept across a soft navigation, so its links are re-marked
	breadcrumb bool     // inside a breadcrumb item
	pageChild  bool     // a direct child of <page>: the only place a progressbar goes
	container  []string // the classes <page container="…"> gives each region directly inside it
	// pageBackground is the background token <page> writes ("" for none, or one from data): what an
	// element pinned to an edge shows behind it when it writes none, so the content scrolling under it
	// does not show through
	pageBackground string
	accordionName  string   // the shared name for accordion children of an accordions
	scope          *frame   // what a bare name means here: the components and rows around (runtime.go)
	forAt          *forSite // the nearest for around: an alias there would name every row
	aliases        *aliasFrame
	field          *fieldIDs
	file           string
	// paint is the element around, as the contrast check reads it (contrast.go): the chain its colour
	// and its ground are read along. A region's, a layout's and a component's root take their call's.
	paint *paint
	// terminal is the terminal around whose commands every <code> here is (terminal.go): set inside a
	// terminal's host, nil outside one and inside any component nested in it
	terminal *terminalScope
}

// forSite is where a for is written: on an element, or on the call that brings a component's file in.
type forSite struct {
	node *Node
	attr *Attr
}

type fieldIDs struct {
	control string
	hint    string
}

// callSite carries what a call adds to the element it compiles: for a region, the root of its file;
// for a layout piece, the root of its .ui with the call's placement; for a component with a UI, the
// root of its .ui merged with the call; for a component without one, the host the author wrote.
type callSite struct {
	forEach  *forSpec
	ifExpr   *expr
	showExpr *expr
	classes  []string
	bindings []binding
	// the layout piece the element is the root of, or the component it is the host of; nil for a
	// region. alias is a component's as, state the names it declares with their starting values
	// (component.go), inputs the bindings the kernel seeds it with
	def    *componentDef
	alias  string
	state  map[string]any
	inputs []input
	// showBind is the host's show when it reads a live component around the host: the kernel keeps
	// it up to date
	showBind string
	// places are the widths a place written at the call starts at (0 every screen): the root floats
	// over what lies around it there (contrast.go)
	places []int
	// terminal is the host of a terminal: each <code> inside is a command, with a prompt before it
	// (terminal.go); nil for every other call
	terminal *terminalSpec
}

type compiler struct {
	site     *Site
	routeDir string
	content  *Node
	seq      map[string]int
	depth    int
	files    map[string]bool
	// layout is the page.ui the route uses (site-relative), written on <body> as data-kitwork-layout:
	// a link to a page with another page.ui is not a content swap but a whole new page (Drive)
	layout string

	// components are the components and layout pieces resolved so far, by writing folder and value (a
	// layout piece by its file); sources the source each component name took on this page (one name,
	// one source); reads what each .ui reads; nearest the nearest <name>.ui seen from a folder
	components map[string]*componentDef
	sources    map[string]componentSource
	reads      map[string]map[string]bool
	nearest    map[string]string
	// view is what the route's view declares (router.work): a name a component and the view both
	// declare cannot be read inside the component's host
	view map[string]bool
	// aliasUses are the $alias reads of the page, checked once every as is known
	aliasUses []aliasUse
	// wired are the aliases read by what is written for the kernel, checked once every as is known to
	// name a component the browser runs (runtime.go, wiredAliases); strays are the bare names read
	// outside every component that nothing around declares, checked once every component of the page
	// is known (strayNames); called are the components the page calls, by name
	wired  []aliasUse
	strays []aliasUse
	called map[string]*componentDef
	// anchors are the ids written on this page (page.ui, its regions, layouts, the content), each with
	// where it was written: an id names one place, so a second one is an error
	anchors map[string]string

	// table reads presentation attributes against the route's colour chain; rules is the page's
	// stylesheet it records into (classes.go)
	table *stylesheet.Table
	rules *stylesheet.Rules
	// uses are the elements the page holds whose look sets a font weight or face (Page.uses)
	uses map[string]bool

	// contrast are the pairs of colour and ground that fall short (contrast.go), every one of the page,
	// each once, when the route's contrast is strict; otherwise each is a warning of the page
	contrast     []*Error
	contrastSeen map[string]bool
	// highlightShown are the calls of router.highlight whose colours the page can show (code.go,
	// codeKinds): each tells its line of contrast at the end of the compile (tellHighlight)
	highlightShown map[stylesheet.Source]bool
	// hiddenText is gradient text an element under it hides (gradient.go, checkHiddenText): the first
	// one found, the page's error
	hiddenText error

	// out is the page being compiled: its warnings gather here as they are found (warn)
	out *Page
}

// aliasFrame holds the aliases set in one place: the page (page.ui, its regions and the content),
// or one instance's own .ui, whose aliases are its own and unseen by the page.
type aliasFrame struct {
	owner  string                   // the component whose .ui these aliases are in; "" for the page
	set    map[string]string        // alias → where its as is written (file:line:col)
	defs   map[string]*componentDef // alias → the component on whose host it is written
	parent *aliasFrame
}

func (f *aliasFrame) find(alias string) bool {
	for s := f; s != nil; s = s.parent {
		if _, ok := s.set[alias]; ok {
			return true
		}
	}
	return false
}

// defOf is the component an alias in reach names, nil when no host in reach sets it.
func (f *aliasFrame) defOf(alias string) *componentDef {
	for s := f; s != nil; s = s.parent {
		if _, ok := s.set[alias]; ok {
			return s.defs[alias]
		}
	}
	return nil
}

// aliasUse is one read of an alias ($cart.add(…)) and the aliases in reach where it stands.
type aliasUse struct {
	name  string
	frame *aliasFrame
	node  *Node
	attr  *Attr
}

// Compile prepares the page of the route folder routeDir: its content.ui, the nearest page.ui going
// up, and every region and component those reach (KITWORK_UI §6, §13).
func (s *Site) Compile(routeDir string) (*Page, error) {
	return s.compile(routeDir, filepath.Join(routeDir, "content.ui"))
}

// CompileNotFound prepares the not-found page seen from routeDir: the nearest notfound.ui walking
// up takes the place of content.ui inside the same page frame. It returns nil when there is none.
func (s *Site) CompileNotFound(routeDir string) (*Page, error) {
	file := s.find(routeDir, "notfound")
	if file == "" {
		return nil, nil
	}
	return s.compile(routeDir, file)
}

// HasContent reports whether routeDir has a page of its own (content.ui).
func (s *Site) HasContent(routeDir string) bool {
	return s.exists(filepath.Join(routeDir, "content.ui"))
}

func (s *Site) compile(routeDir, contentFile string) (*Page, error) {
	switch s.Contrast {
	case "", ContrastWarning, ContrastStrict:
	default:
		return nil, ContrastModeError(s.Contrast)
	}
	c := &compiler{site: s, routeDir: routeDir, files: map[string]bool{}, seq: map[string]int{},
		components: map[string]*componentDef{}, called: map[string]*componentDef{}, sources: map[string]componentSource{}, reads: map[string]map[string]bool{},
		nearest: map[string]string{}, table: stylesheet.NewTable(s.Palette), rules: &stylesheet.Rules{}, uses: map[string]bool{},
		out: &Page{palette: s.Palette, rightToLeft: rightToLeft(s.Language)}}
	view, viewFile := s.viewNames(routeDir)
	c.view = view
	if viewFile != "" {
		c.files[viewFile] = true
	}

	c.files[contentFile] = true
	nodes, err := s.parseFile(contentFile)
	if err != nil {
		return nil, err
	}
	content, err := singleRoot(nodes, rel(s, contentFile))
	if err != nil {
		return nil, err
	}
	if content.Tag != "content" {
		return nil, &Error{File: content.File, Line: content.Line, Column: content.Column,
			Message: filepath.Base(contentFile) + " phải có gốc <content>, không phải <" + content.Tag + ">"}
	}
	c.content = content

	var page *Node
	if pageFile := s.find(routeDir, "page"); pageFile != "" {
		c.files[pageFile] = true
		c.layout = filepath.ToSlash(rel(s, pageFile))
		nodes, err := s.parseFile(pageFile)
		if err != nil {
			return nil, err
		}
		if page, err = singleRoot(nodes, rel(s, pageFile)); err != nil {
			return nil, err
		}
		if page.Tag != "page" {
			return nil, &Error{File: page.File, Line: page.Line, Column: page.Column,
				Message: "page.ui phải có gốc <page>; nội dung của route đặt trong content.ui"}
		}
	} else {
		page = &Node{Tag: "page", Children: []*Node{{Tag: "content", Bare: true}}}
	}

	pageAliases := &aliasFrame{set: map[string]string{}}
	root, err := c.element(page, ctx{file: page.File, kept: true, aliases: pageAliases}, nil)
	if err != nil {
		return nil, err
	}
	for _, use := range c.aliasUses {
		if !use.frame.find(use.name) {
			return nil, c.errUse(use)
		}
	}
	if err := c.wiredAliases(); err != nil { // an alias the kernel reads names a component it runs
		return nil, err
	}
	if err := c.strayNames(); err != nil { // a component's name read outside its host (copy())
		return nil, err
	}
	if c.hiddenText != nil { // gradient text an element under it hides (gradient.go)
		return nil, c.hiddenText
	}
	// the line of each call of router.highlight whose colour falls short, at the call (code.go)
	c.tellHighlight()
	if err := c.contrastError(); err != nil { // strict: every pair short of contrast at once
		return nil, err
	}
	out := c.out
	out.root, out.rules, out.uses = root, c.rules, c.uses
	for f := range c.files {
		out.Files = append(out.Files, f)
	}
	sort.Strings(out.Files)
	return out, nil
}

func rel(s *Site, path string) string {
	r, err := filepath.Rel(s.Root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(r)
}

func errAt(n *Node, format string, args ...any) error {
	return &Error{File: n.File, Line: n.Line, Column: n.Column, Message: fmt.Sprintf(format, args...)}
}

func errAttr(n *Node, a *Attr, format string, args ...any) error {
	return &Error{File: n.File, Line: a.Line, Column: a.Column, Message: fmt.Sprintf(format, args...)}
}

// element compiles one element node.
func (c *compiler) element(n *Node, cx ctx, site *callSite) (op, error) {
	c.depth++
	defer func() { c.depth-- }()
	if c.depth > 64 {
		return nil, errAt(n, "lồng quá sâu (có thể một UI tự gọi lại chính nó)")
	}
	c.lint(n)
	info, known := catalog[n.Tag]
	if !known {
		return c.layoutCall(n, cx) // a layout piece, by its tag (layout.go)
	}
	if err := c.namedLikeATag(n); err != nil {
		return nil, err
	}
	if a := attrNamed(n, "component"); a.Name == "component" {
		return c.componentCall(n, cx, site)
	}
	if info.region && n.Tag != "page" && len(n.SignificantChildren()) == 0 {
		return c.region(n, cx)
	}
	// an element's own for: what it holds, and its own attributes, are read once per row
	if a := attrNamed(n, "for"); a.Name == "for" {
		if each, err := parseFor(a.Value, n, a); err == nil {
			cx.scope = rowFrame(each, cx.scope)
			cx.forAt = &forSite{node: n, attr: a}
		}
	}
	if err := c.checkReads(n, cx, nil); err != nil {
		return nil, err
	}
	return c.dispatch(n, info, cx, site)
}

// lint tells, once, what an element writes in a form the compiler reads but the house writes another
// way (decision D: house style is a warning, the page renders as it means): a tag name with capitals
// (<Box> is box: tag names do not tell case apart), a true-or-false attribute written bare (required is
// required="true"), and for, if, show or an event wrapped in {{ }} (they are expressions already: the
// compiler reads what the braces hold). Every element passes here before its rule: element() for the
// page, the regions' calls, the layouts' and components' roots and everything inside them, region()
// for a region's own root and summary() for an accordion's header.
func (c *compiler) lint(n *Node) {
	if n.written != "" {
		c.warn(n.File, n.Line, n.Column, fmt.Sprintf("tên thẻ <%s> viết thường: <%s> (tên thẻ không phân biệt hoa thường, nên đây là <%s>)", n.written, n.Tag, n.Tag))
	}
	_, kitworkTag := catalog[n.Tag]
	for i := range n.Attrs {
		a := &n.Attrs[i]
		if a.Bare {
			// on a tag of Kitwork a true-or-false attribute written bare reads as "true"; at the call of a
			// layout it is an input, data the call passes, and stays the call's error (layout.go), as an
			// input of a component does (host.go)
			if kitworkTag && booleanAttributes[a.Name[strings.LastIndex(a.Name, ":")+1:]] {
				a.Value = "true"
				c.warn(n.File, a.Line, a.Column, fmt.Sprintf("thuộc tính đúng/sai luôn có giá trị: viết %s=\"true\" (viết trơn đọc là true)", a.Name))
			}
			continue
		}
		if !expressionAttribute(a) {
			continue
		}
		if bare, ok := wrappedExpression(a.Value); ok {
			c.warn(n.File, a.Line, a.Column, a.Name+" đã là biểu thức, không bọc {{ }}: viết "+a.Name+"=\""+bare+"\"")
		}
	}
}

// expressionAttribute reports whether an attribute's whole value is an expression: for, if, a show
// that is no plain true or false, an event. scroll written as a word is the area that scrolls.
func expressionAttribute(a *Attr) bool {
	switch {
	case a.Bare:
		return false
	case a.Name == "for", a.Name == "if":
		return true
	case a.Name == "show":
		return !literalBoolean(a.Value)
	}
	return isEvent(a.Name) && !scrollArea(a.Name, a.Value)
}

// scrollArea reports whether scroll="…" is the area that scrolls rather than the scroll event: a word
// (x, y, or a word the stylesheet refuses with what scroll takes) or a value from data. A handler calls
// or changes something (scroll="track()"), which a word cannot.
func scrollArea(name, value string) bool {
	if name != "scroll" {
		return false
	}
	v := strings.TrimSpace(value)
	if strings.Contains(v, "{{") {
		return true
	}
	if v == "" {
		return false
	}
	for i := 0; i < len(v); i++ {
		ch := v[i]
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_') {
			return false
		}
	}
	return true
}

// anchor checks an id the author wrote (KITWORK_UI §7, "Neo: id"). An id is where a link can land
// (href="#quickstart", /docs#install), so it is a fixed word, written once in the page: an id from
// data, or one inside a for, would name every row or none, and a second element with the same id is a
// link that silently lands on the first. Ids starting with kitwork- are the engine's own (a field's
// control and hint), so an author's id never collides with one. Capitals are house style: the id is
// written as it is, and a warning names its lowercase.
func (c *compiler) anchor(n *Node, a *Attr, cx ctx) error {
	value := strings.TrimSpace(a.Value)
	switch {
	case strings.Contains(value, "{{"):
		return errAttr(n, a, "id là chữ cố định (một chỗ để link nhảy tới), không lấy từ dữ liệu")
	case cx.forAt != nil:
		at := cx.forAt.node
		return errAttr(n, a, "id \"%s\" nằm trong for (%s:%d:%d): mỗi dòng sẽ mang cùng một id; đặt id trên phần tử bao ngoài vòng lặp", value, at.File, at.Line, at.Column)
	case !anchorWord(value) && anchorWord(strings.ToLower(value)):
		c.warn(n.File, a.Line, a.Column, fmt.Sprintf("id \"%s\" có chữ hoa: id viết bằng chữ thường, số và dấu gạch nối (id=\"%s\"); link phải viết đúng hoa thường như id", value, strings.ToLower(value)))
	case !anchorWord(value):
		return errAttr(n, a, "id \"%s\" chỉ gồm chữ thường, số và dấu gạch nối, bắt đầu bằng chữ (vd. quickstart, check-before-serving)", value)
	}
	if strings.HasPrefix(strings.ToLower(value), "kitwork-") {
		return errAttr(n, a, "id bắt đầu bằng kitwork- là của engine; đặt tên khác")
	}
	where := fmt.Sprintf("%s:%d:%d", n.File, a.Line, a.Column)
	if c.anchors == nil {
		c.anchors = map[string]string{}
	}
	if first, ok := c.anchors[value]; ok {
		return errAttr(n, a, "id \"%s\" đã dùng ở %s; mỗi id chỉ một lần trong trang", value, first)
	}
	c.anchors[value] = where
	return nil
}

// anchorWord: lowercase letters and digits in words joined by single hyphens, starting with a letter.
func anchorWord(value string) bool {
	if value == "" || value[0] < 'a' || value[0] > 'z' || strings.HasSuffix(value, "-") || strings.Contains(value, "--") {
		return false
	}
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-') {
			return false
		}
	}
	return true
}

// requireName warns of a button, a link or a badge that says nothing a screen reader can read
// (KITWORK_UI §7, "Tên đọc được"): an icon is decoration unless it has an alt, so a button holding only
// an icon needs that alt — it is the button's name. A badge may carry the name itself (alt on the badge).
// It works and shows as written, but some readers cannot use it: a warning (decision D, question 4).
func (c *compiler) requireName(n *Node) {
	switch n.Tag {
	case "button", "link":
		if spokenName(n) {
			return
		}
		switch heldMedia(n) {
		case "icon":
			c.warn(n.File, n.Line, n.Column, fmt.Sprintf("<%s> chỉ có icon nên chưa có tên để trình đọc màn hình đọc: viết alt trên icon (vd. <icon value=\"x\" alt=\"Đóng\" />)", n.Tag))
			return
		case "image":
			c.warn(n.File, n.Line, n.Column, fmt.Sprintf("<%s> chỉ có ảnh nên chưa có tên để trình đọc màn hình đọc: viết alt trên ảnh, nói nó dẫn tới đâu (vd. <image value=\"/logo.svg\" alt=\"Trang chủ\" />)", n.Tag))
			return
		}
		c.warn(n.File, n.Line, n.Column, fmt.Sprintf("<%s> cần tên đọc được: chữ bên trong, hoặc icon có alt", n.Tag))
	case "badge":
		if alt, ok := n.Attr("alt"); ok && strings.TrimSpace(alt) != "" {
			return
		}
		if spokenName(n) {
			return
		}
		if what := heldMedia(n); what != "" {
			held := "icon"
			if what == "image" {
				held = "ảnh"
			}
			c.warn(n.File, n.Line, n.Column, "<badge> chỉ có "+held+" cần alt nói nó là gì (vd. <badge alt=\"Đang online\">)")
			return
		}
		c.warn(n.File, n.Line, n.Column, "<badge> cần tên đọc được: chữ bên trong, hoặc alt nói nó là gì (vd. <badge alt=\"Đang online\">)")
	}
}

// spokenName reports whether an element holds something a screen reader reads: text (written or
// from {{ }}), an icon or image with a non-empty alt, or a layout piece (whose file says it).
func spokenName(n *Node) bool {
	for _, child := range n.Children {
		switch {
		case child.Tag == "":
			if strings.TrimSpace(child.Text) != "" {
				return true
			}
		case child.Tag == "icon" || child.Tag == "image":
			if alt, ok := child.Attr("alt"); ok && strings.TrimSpace(alt) != "" {
				return true
			}
		default:
			if _, known := catalog[child.Tag]; !known {
				return true // a layout piece: its own file decides, not this check
			}
			if spokenName(child) {
				return true
			}
		}
	}
	return false
}

// heldMedia is what an element without a spoken name holds that could carry one: "icon" when it holds
// an icon, else "image" when it holds an image, else "".
func heldMedia(n *Node) string {
	found := ""
	for _, child := range n.Elements() {
		what := child.Tag
		if what != "icon" && what != "image" {
			what = heldMedia(child)
		}
		if what == "icon" {
			return "icon"
		}
		if what == "image" {
			found = "image"
		}
	}
	return found
}

// anchorAttribute checks the element's id (anchor) and returns it as an attribute ("" without one).
// Every element that carries an id passes here — the content's, a region's root, a layout's root —
// so the page's ids are checked in one place, once each.
func (c *compiler) anchorAttribute(n *Node, p *attrPlan, cx ctx) (string, error) {
	if p == nil || p.id == "" {
		return "", nil
	}
	if err := c.anchor(n, attrNamed(n, "id"), cx); err != nil {
		return "", err
	}
	var w strings.Builder
	writeAttr(&w, "id", p.id)
	return w.String(), nil
}

// offAnchor is the id of a tag the house writes none on (a code block, a breadcrumb, an accordion's
// header): the id is checked as any other and written on the element the tag becomes, so a link to it
// still lands, and a warning says where the house puts it (decision D, question 8).
func (c *compiler) offAnchor(n *Node, p *attrPlan, cx ctx, where string) (string, error) {
	anchor, err := c.anchorAttribute(n, p, cx)
	if err != nil || anchor == "" {
		return anchor, err
	}
	a := attrNamed(n, "id")
	c.warn(n.File, a.Line, a.Column, fmt.Sprintf("id không đặt trên %s; đặt trên phần tử bao quanh nó (trang vẫn ghi id ở đây, link tới #%s vẫn nhảy tới)", where, p.id))
	return anchor, nil
}

// dispatch compiles a catalog element by the rule of its tag.
func (c *compiler) dispatch(n *Node, info tagInfo, cx ctx, site *callSite) (op, error) {
	switch n.Tag {
	case "fragment":
		return c.fragment(n, cx)
	case "text":
		return c.text(n, cx, site)
	case "code":
		if cx.terminal != nil {
			return c.command(n, cx, site) // a command of the terminal around it, its prompt before it
		}
		return c.code(n, cx, site)
	case "quote":
		return c.quote(n, cx, site)
	case "accordion":
		return c.accordion(n, cx, site)
	case "breadcrumb":
		return c.breadcrumbElement(n, cx, site)
	case "reader":
		return c.reader(n, cx)
	}
	return c.generic(n, info, cx, site)
}

// region replaces an empty region tag with the root of its file (or, for <content />, with the route
// content). The call site only says where the file comes from and whether it appears. Regions are
// found from the route's folder (a branch may have its own navbar.ui), and have no .work: what a
// region does is a component on its root or inside it.
func (c *compiler) region(n *Node, cx ctx) (op, error) {
	site := &callSite{}
	for i := range n.Attrs {
		a := &n.Attrs[i]
		name := a.Name
		switch {
		case name == "source", name == "if", name == "show":
		case strings.HasSuffix(name, ":show"):
		case name == "component":
			return nil, errAttr(n, a, "<%s /> rỗng là chỗ gọi vùng, lấy %s.ui; component của vùng đặt trên gốc của file đó (<%s component=\"…\">…</%s>), hoặc trên một thẻ bên trong", n.Tag, n.Tag, n.Tag, n.Tag)
		default:
			// a word of the web, a typo in the name or in the value is that error first (question 1); a
			// known word whose value reads goes on the root of the region's file
			if message := c.webWord(n, a); message != "" {
				return nil, errAttr(n, a, "%s", message)
			}
			if name != "id" && !uiWord(name) {
				return nil, errAttr(n, a, "thuộc tính %s không có trong .ui; thẻ vùng <%s /> ở chỗ gọi chỉ nhận source, if, show", name, n.Tag)
			}
			if err := c.readValue(n, a); err != nil {
				return nil, err
			}
			return nil, errAttr(n, a, "thẻ vùng <%s /> ở chỗ gọi chỉ nhận source, if, show; %s đặt trên gốc của file %s.ui", n.Tag, name, n.Tag)
		}
	}
	var root *Node
	if n.Tag == "content" {
		root = c.content
	} else {
		name := n.Tag
		if source, ok := n.Attr("source"); ok {
			name = source
		}
		file := c.site.find(c.routeDir, name)
		if file == "" {
			return nil, errAt(n, "không tìm thấy %s.ui (tìm từ thư mục của route lên gốc site)", name)
		}
		c.files[file] = true
		nodes, err := c.site.parseFile(file)
		if err != nil {
			return nil, err
		}
		if root, err = singleRoot(nodes, rel(c.site, file)); err != nil {
			return nil, err
		}
		if root.Tag != n.Tag {
			return nil, errAt(root, "%s.ui phải có gốc <%s>, không phải <%s>", name, n.Tag, root.Tag)
		}
		if logic := strings.TrimSuffix(file, ".ui") + ".work"; c.site.exists(logic) {
			return nil, &Error{File: rel(c.site, logic), Line: 1, Column: 1, Message: fmt.Sprintf(
				"vùng không có file .work: điều %s làm là một component trên gốc của %s (<%s component=\"…\">) hoặc trên một thẻ bên trong",
				n.Tag, filepath.Base(file), n.Tag)}
		}
	}
	c.lint(root) // the root of a region's file, or the route's content, is compiled here, never through element()
	if len(root.SignificantChildren()) == 0 && root.Tag != "content" {
		// an empty region renders as written, an empty element: maybe a place kept on purpose
		c.warn(root.File, root.Line, root.Column, fmt.Sprintf("<%s> trong %s không có nội dung: trang dựng một <%s> rỗng", root.Tag, root.File, root.Tag))
	}
	if v, ok := n.Attr("if"); ok {
		e, err := compileExpr(v, n, attrNamed(n, "if"))
		if err != nil {
			return nil, err
		}
		site.ifExpr = e
	}
	// show on the call site (plain or prefixed) is merged into the root's own attributes.
	merged := *root
	merged.Attrs = append([]Attr(nil), root.Attrs...)
	for _, a := range n.Attrs {
		if a.Name == "show" || strings.HasSuffix(a.Name, ":show") {
			merged.Attrs = append(merged.Attrs, a)
		}
	}
	rootCtx := ctx{file: root.File, kept: n.Tag != "content", container: cx.container, aliases: cx.aliases, pageBackground: cx.pageBackground, paint: cx.paint}
	if a := attrNamed(&merged, "component"); a.Name == "component" {
		return c.componentCall(&merged, rootCtx, site)
	}
	if err := c.checkReads(&merged, rootCtx, nil); err != nil {
		return nil, err
	}
	info := catalog[root.Tag]
	return c.generic(&merged, info, rootCtx, site)
}

// camel turns an input's attribute name into the name the component reads: old-price → oldPrice.
func camel(name string) string {
	parts := strings.Split(name, "-")
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}

func (c *compiler) fragment(n *Node, cx ctx) (op, error) {
	o := &elemOp{fragment: true}
	hasControl := false
	for i := range n.Attrs {
		a := &n.Attrs[i]
		switch a.Name {
		case "for":
			f, err := parseFor(a.Value, n, a)
			if err != nil {
				return nil, err
			}
			o.forEach = f
			hasControl = true
		case "if":
			e, err := compileExpr(a.Value, n, a)
			if err != nil {
				return nil, err
			}
			o.ifExpr = e
			hasControl = true
		case "show", "id":
			// hiding what it holds, or a place for a link to land, needs an element: without one the page
			// would show what it hides and a link would land nowhere, so these stay errors
			return nil, errAttr(n, a, "fragment chỉ nhận for và if; %s cần một phần tử thật, dùng <box>", a.Name)
		default:
			// a word of .ui does nothing on what builds no element: the page renders without it
			done, err := c.notHere(n, a, "<fragment>", "fragment không dựng phần tử nào; cần một phần tử thật thì dùng <box>")
			if err != nil {
				return nil, err
			}
			if !done {
				return nil, errAttr(n, a, "thuộc tính %s không có trên <fragment>; fragment chỉ nhận for và if", a.Name)
			}
		}
	}
	if !hasControl {
		// it renders what it holds, as a box-less group: only one tag too many
		c.warn(n.File, n.Line, n.Column, "fragment không có for hay if thì thừa; bỏ thẻ này")
	}
	cx.pageChild = false // a progressbar is written straight in <page>, never under a condition
	children, err := c.children(n, cx)
	if err != nil {
		return nil, err
	}
	o.children = children
	return o, nil
}

// parseFor reads "x of list", "x, i of list" and an optional "; key: expr". The whole value wrapped
// in {{ }} reads as what the braces hold (lint warns of it); braces anywhere else are an error.
func parseFor(source string, n *Node, a *Attr) (*forSpec, error) {
	if bare, ok := wrappedExpression(source); ok {
		source = bare
	}
	if strings.Contains(source, "{{") {
		return nil, errAttr(n, a, "for đã là biểu thức, không bọc {{ }}: viết for=\"x of list\"")
	}
	head := source
	var key *expr
	if i := strings.Index(source, ";"); i >= 0 {
		head = source[:i]
		tail := strings.TrimSpace(source[i+1:])
		if !strings.HasPrefix(tail, "key:") {
			return nil, errAttr(n, a, "sau ; trong for chỉ có key: (for=\"x of list; key: x.id\")")
		}
		var err error
		if key, err = compileExpr(strings.TrimPrefix(tail, "key:"), n, a); err != nil {
			return nil, err
		}
	}
	parts := strings.SplitN(head, " of ", 2)
	if len(parts) != 2 {
		return nil, errAttr(n, a, "for viết là \"x of list\" (hoặc \"x, i of list\")")
	}
	names := strings.Split(parts[0], ",")
	f := &forSpec{item: strings.TrimSpace(names[0]), key: key}
	if len(names) > 1 {
		f.index = strings.TrimSpace(names[1])
	}
	if !identifier(f.item) || (f.index != "" && !identifier(f.index)) {
		return nil, errAttr(n, a, "tên biến của for không hợp lệ: %s", parts[0])
	}
	list, err := compileExpr(parts[1], n, a)
	if err != nil {
		return nil, err
	}
	f.list = list
	return f, nil
}

func identifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if r == '_' || r == '$' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (i > 0 && r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

// attrPlan is the result of reading an element's attributes.
type attrPlan struct {
	forEach    *forSpec
	ifExpr     *expr
	showExpr   *expr
	classes    []string
	show       []string // show-false, medium:show-true…: on the outer element when a tag renders as two
	regions    []string // container on <page>: the cap of each region inside it, never of <body>
	dynClasses []dynClass
	semantic   map[string]*Attr
	order      []string
	events     []Attr
	hasPlace   bool
	// pin is the first pin attribute written, with or without a prefix; pinned says one of them pins the
	// element to an edge (top, bottom, or a value from data), so it needs a background of its own
	pin    *Attr
	pinned bool
	// id is the anchor the author wrote (KITWORK_UI §7, "Neo: id"), already checked by anchor()
	id string
	// paints are the colour, ground and type the element writes, and places the widths a place it
	// writes starts at, for the contrast check (contrast.go)
	paints []*paintIntent
	places []int
	// pins are the pins it writes by the width each starts at, and hidden the widths a show written
	// "false" hides it at (nil: shown at every width), for the contrast check too
	pins   []pinAt
	hidden []bool
}

// attributes reads the attributes common to every element: control (for, if, show), events,
// presentation with prefixes. Everything else is returned in semantic for the tag's own rule.
func (c *compiler) attributes(n *Node, el *element) (*attrPlan, error) {
	p := &attrPlan{semantic: map[string]*Attr{}}

	showBase := ""
	showTarget := ""
	type widthShow struct {
		attr  *Attr
		pre   prefix
		value string
	}
	var showWidths []widthShow
	var sides []sideWritten // presentation as written, for checkSides (classes.go)

	for i := range n.Attrs {
		a := &n.Attrs[i]
		name := a.Name
		// a true-or-false attribute written bare reads as "true" (the parser gives it that value; lint warns)
		if name == "id" {
			p.id = strings.TrimSpace(a.Value) // checked in element(), where the for around is known
			continue
		}

		// event: the part before ':' is a list of event names (input:debounce(300), pointer-enter,focus-in).
		// scroll is both an event and the scroll owner: scroll="y" / scroll="x" is the area that scrolls,
		// and so is any other word, which the stylesheet then refuses (a typo never becomes a handler)
		if isEvent(name) && !scrollArea(name, a.Value) {
			if event, attribute, _ := strings.Cut(name, ":"); event == "scroll" && stylesheet.IsPresentation(attribute) {
				// scroll:shadow="small" reads as the scroll event and would do nothing: the state is scrolled:
				return nil, errAttr(n, a, "%s: scroll: là sự kiện cuộn; đổi trình bày khi trang đã cuộn là tiền tố trạng thái scrolled: (scrolled:%s=\"%s\")", name, attribute, a.Value)
			}
			p.events = append(p.events, *a)
			continue
		}

		chain, base := "", name
		if i := strings.LastIndex(name, ":"); i >= 0 {
			chain, base = name[:i], name[i+1:]
			if strings.HasPrefix(name, "bind:") {
				return nil, errAttr(n, a, "không có bind:; viết %s=\"{{ %s }}\"", strings.TrimPrefix(name, "bind:"), a.Value)
			}
		}
		var pre prefix
		if chain != "" {
			var err error
			if pre, err = c.prefixOf(n, a, chain, base); err != nil {
				return nil, err
			}
			if !pre.keep {
				continue // a branch for another target: dropped from this build
			}
		}

		switch base {
		case "for":
			if chain != "" {
				return nil, errAttr(n, a, "for không nhận tiền tố")
			}
			f, err := parseFor(a.Value, n, a)
			if err != nil {
				return nil, err
			}
			p.forEach = f
			continue
		case "if":
			if chain != "" {
				return nil, errAttr(n, a, "if không nhận tiền tố; theo độ rộng hay đích thì dùng show")
			}
			e, err := compileExpr(a.Value, n, a)
			if err != nil {
				return nil, err
			}
			p.ifExpr = e
			continue
		case "show":
			v := strings.TrimSpace(a.Value)
			literal := v == "true" || v == "false"
			switch {
			case chain == "" && !literal:
				e, err := compileExpr(v, n, a)
				if err != nil {
					return nil, err
				}
				p.showExpr = e
			case chain == "":
				showBase = v
			case !literal:
				return nil, errAttr(n, a, "bản đầu chưa cho tiền tố đi cùng biểu thức; %s chỉ nhận \"true\" hoặc \"false\"", name)
			case len(pre.states) > 0:
				return nil, errAttr(n, a, "%s: ẩn hiện theo trạng thái chưa được hỗ trợ", name)
			case len(pre.widths) > 0:
				showWidths = append(showWidths, widthShow{attr: a, pre: pre, value: v})
			default:
				showTarget = v // a kept target branch overrides the plain value
			}
			continue
		}

		if base == "place" {
			p.hasPlace = true
			p.places = append(p.places, narrowest(pre.widths))
		}
		// pin or material on a tag the house does not give them to: the value is read first (a word the
		// stylesheet refuses is that error), then the page draws what is written and a warning says where
		// the house puts it (decision D, question 1)
		misplaced := ""
		if base == "pin" {
			if !pinnable(el.tag) {
				misplaced = fmt.Sprintf("pin ghim một khung vào mép màn hình khi cuộn (box, section, card, list, header, footer…); <%s> không ghim được (trang vẫn ghim nó như đã viết)", el.tag)
			}
			if p.pin == nil {
				p.pin = a
			}
			v := strings.TrimSpace(a.Value)
			if v == "top" || v == "bottom" || strings.Contains(v, "{{") {
				p.pinned = true
			}
			p.pins = append(p.pins, pinAt{width: narrowest(pre.widths), on: v != "none"})
		}
		if base == "material" && !materialFrame(el.tag) {
			misplaced = materialRefused(el.tag) + " (trang vẫn vẽ material như đã viết)"
		}
		if !presentationName(base) {
			if chain != "" {
				if message := c.prefixedWebWord(n, a, chain, base); message != "" {
					return nil, errAttr(n, a, "%s", message) // medium:pl="4" is told padding-left, as pl="4" is
				}
				return nil, errAttr(n, a, "%s không nhận tiền tố (chỉ thuộc tính trình bày và show nhận tiền tố)", base)
			}
			p.semantic[name] = a
			p.order = append(p.order, name)
			continue
		}
		sides = append(sides, sideWritten{attr: a, name: base, width: narrowest(pre.widths), states: pre.states})
		if strings.Contains(a.Value, "{{") {
			if base == "container" && el.tag == "page" {
				return nil, errAttr(n, a, "container của <page> là một chữ cố định (%s), không đổi theo dữ liệu", widthScaleWords)
			}
			in, err := parseInterpolation(a.Value, n, a)
			if err != nil {
				return nil, err
			}
			d, err := c.dynamicClass(n, a, el, pre, base, in)
			if err != nil {
				return nil, err
			}
			if misplaced != "" {
				c.warn(n.File, a.Line, a.Column, misplaced)
			}
			p.dynClasses = append(p.dynClasses, d)
			if paintNames[base] {
				// each value it spells out is measured, even when another arm reads data (that arm is
				// unknown, skipped); a ternary's arms pair with the arms of the same test
				values, complete := interpolationLiterals(in)
				if !complete && base == "material" {
					values = stylesheet.Materials() // from data: any of them may come
				}
				pi := paintIntentOf(n, a, pre, base, values)
				pi.partial = !complete && values != nil
				pi.test, pi.arms = ternaryArms(in)
				p.paints = append(p.paints, pi)
			}
			continue
		}
		class, err := c.class(n, a, el, pre, base, a.Value)
		if err != nil {
			return nil, err
		}
		if misplaced != "" {
			c.warn(n.File, a.Line, a.Column, misplaced)
		}
		if paintNames[base] {
			p.paints = append(p.paints, paintIntentOf(n, a, pre, base, []string{a.Value}))
		}
		if class == "" {
			continue
		}
		if base == "container" && el.tag == "page" {
			// the regions line up on one width while <body>, and its background, spans the screen
			p.regions = append(p.regions, class)
			continue
		}
		p.classes = append(p.classes, class)
	}

	if p.pin != nil && p.hasPlace {
		// both are drawn as written; with place the element floats over its parent, so pin does nothing
		c.warn(n.File, p.pin.Line, p.pin.Column, "pin và place không đi cùng nhau: phần tử có place đã nổi trên cha, không nằm trong dòng chảy để ghim")
	}
	c.checkSides(n, sides) // a left or right with its start or end twin, and left or right on a site written right to left
	if err := c.checkGradients(n, el, p); err != nil {
		return nil, err // gradient text over a background, a glass over a gradient (gradient.go)
	}
	// one transition for what the element's classes animate together (scrolled:shadow="small"
	// scrolled:background="paper" fade in as one): every class with a state lands on this element
	c.rules.Transition(p.classes)
	c.takeBack(p) // a colour background for a later condition takes back this element's gradient (gradient.go)

	// show: the plain value or a kept target branch, then the width branches, each one class of the
	// show layer: show-false medium:show-true hides, then gives back whatever display the element has
	base := showBase
	if showTarget != "" {
		base = showTarget
	}
	if base == "false" && p.showExpr == nil {
		class, err := c.class(n, attrNamed(n, "show"), el, prefix{keep: true}, "show", "false")
		if err != nil {
			return nil, err
		}
		p.show = append(p.show, class)
	}
	for _, w := range showWidths {
		class, err := c.class(n, w.attr, el, w.pre, "show", w.value)
		if err != nil {
			return nil, err
		}
		p.show = append(p.show, class)
	}
	// the widths it cannot render at: its text is not measured there (contrast.go). A show from data
	// may show it, so only a written "false" hides it.
	for width := 0; width <= len(stylesheet.Widths()); width++ {
		value, at := base, -1
		for _, w := range showWidths {
			if from := narrowest(w.pre.widths); from <= width && from >= at {
				value, at = w.value, from
			}
		}
		if value == "false" {
			if p.hidden == nil {
				p.hidden = make([]bool, len(stylesheet.Widths())+1)
			}
			p.hidden[width] = true
		}
	}
	return p, nil
}

// prefixOf reads an attribute's prefix (classes.go parsePrefix). Its axes written out of order
// (hover+large:) still say one thing, so that is house style: the prefix is read in its order
// (large+hover, which the class writes anyway) and a warning says how to write it (decision D). Every
// other problem of a prefix is a word the compiler does not understand: an error.
func (c *compiler) prefixOf(n *Node, a *Attr, chain, base string) (prefix, error) {
	pre, problem := parsePrefix(chain, c.site.target())
	if problem == "" {
		return pre, nil
	}
	if ordered := orderedPrefix(chain); ordered != chain {
		again, second := parsePrefix(ordered, c.site.target())
		if second != "" {
			return pre, errAttr(n, a, "%s", second) // out of order and wrong besides: the wrong is the error
		}
		c.warn(n.File, a.Line, a.Column, fmt.Sprintf("%s: viết %s:%s", problem, ordered, base))
		return again, nil
	}
	return pre, errAttr(n, a, "%s", problem)
}

// orderedPrefix puts the groups of a prefix in the order target → width → state, each group as
// written; a group whose first word is on no axis leaves the prefix as it is (parsePrefix names it).
func orderedPrefix(chain string) string {
	groups := strings.Split(chain, "+")
	rank := func(group string) int {
		word, _, _ := strings.Cut(group, ",")
		switch {
		case targetWords[word]:
			return 0
		case widthWords[word]:
			return 1
		case stateWords[word]:
			return 2
		}
		return -1
	}
	for _, g := range groups {
		if rank(g) < 0 {
			return chain
		}
	}
	sort.SliceStable(groups, func(i, j int) bool { return rank(groups[i]) < rank(groups[j]) })
	return strings.Join(groups, "+")
}

// htmlVoid are the HTML elements written without a closing tag.
var htmlVoid = map[string]bool{"img": true, "input": true}

// pinnable reports whether pin goes on a tag: a frame, one that lays out children in the page's flow
// (box, section, card, grid, list, form, the regions…). Not <page> (the page itself scrolls), not what
// already floats (popover, overlay), not text or a control.
func pinnable(tag string) bool {
	info, ok := catalog[tag]
	if !ok || tag == "page" || tag == "popover" || tag == "overlay" {
		return false
	}
	return info.kind == kindContainer || info.kind == kindGrid
}

// materialFrame reports whether material goes on a tag (decision M): a frame, one that lays out
// children and takes a background (box, card, section, grid, list, form, the regions, popover…). Not
// <page>, behind which nothing lies to see through; not text, a control, an image or an icon.
func materialFrame(tag string) bool {
	info, ok := catalog[tag]
	return ok && tag != "page" && (info.kind == kindContainer || info.kind == kindGrid)
}

// materialRefused is why material does not go on a tag.
func materialRefused(tag string) string {
	const frames = "material là chất liệu của một khung (box, card, section, header, footer, popover…)"
	if tag == "page" {
		return frames + "; <page> là nền của cả trang, phía sau không có gì để nhìn qua: đặt material trên một vùng hay một khung bên trong"
	}
	return frames + "; <" + tag + "> không mang material được"
}

// writesAttribute reports whether an element writes an attribute, with or without a prefix.
func writesAttribute(n *Node, name string) bool {
	for _, a := range n.Attrs {
		if a.Name == name || strings.HasSuffix(a.Name, ":"+name) {
			return true
		}
	}
	return false
}

// pageBackgroundOf is the background token <page> writes, "" when it writes none or one from data.
func pageBackgroundOf(n *Node) string {
	if v, ok := n.Attr("background"); ok && !strings.Contains(v, "{{") {
		return strings.TrimSpace(v)
	}
	return ""
}

// isFlex reports whether an element lays out its children: it carries a direction or is a grid, with
// or without a width prefix.
func isFlex(classes []string) bool {
	for _, c := range classes {
		c = c[strings.LastIndexByte(c, ':')+1:]
		if c == "flex-row" || c == "flex-column" || c == "grid" {
			return true
		}
	}
	return false
}

func isEvent(name string) bool {
	head := name
	if i := strings.Index(name, ":"); i >= 0 {
		head = name[:i]
	}
	for _, e := range strings.Split(head, ",") {
		if !events[e] {
			return false
		}
	}
	return head != ""
}

// generic compiles a catalog element with the shared rules and the tag's own attributes.
func (c *compiler) generic(n *Node, info tagInfo, cx ctx, site *callSite) (op, error) {
	c.requireName(n)
	if n.Tag == "popover" {
		var err error
		if n, err = c.popoverShow(n); err != nil {
			return nil, err
		}
	}
	el := &element{tag: n.Tag, html: info.html}
	p, err := c.attributes(n, el)
	if err != nil {
		return nil, err
	}
	// <page background="cirrus">: the background goes on a layer fixed to the screen (backdrop.go)
	backdrop, err := c.pageBackdrop(n, p)
	if err != nil {
		return nil, err
	}
	o := &elemOp{tag: info.html, void: htmlVoid[info.html]}

	// The words the engine writes without any attribute (KITWORK_UI §11, "Đầu ra web: class và CSS"):
	// how each element looks before the author writes anything is reboot.css's, keyed by the HTML
	// element, so only these are classes — the .ui tag where several tags share one HTML element, the
	// direction of an element that lays out children, the parent of a placed child, and the fill of a
	// neutral button. Then the written attributes in source order, then what the parent passes down.
	classes := engineWords(n, info, cx)
	terminal := site != nil && site.terminal != nil
	neutral := neutralButton(n)
	if neutral {
		// neutral and usable as it is: a fill, never a border (border="line" draws one when written)
		for _, step := range neutralSteps {
			classes = append(classes, c.engineStateClass(step.state, "background", step.value))
		}
	}
	frame := c.paintOf(n, el, p, cx, site)
	if neutral {
		for _, step := range neutralSteps {
			var states []string
			if step.state != "" {
				states = []string{step.state}
			}
			frame.written = append(frame.written, &paintIntent{name: "background", node: n, states: states, values: []string{step.value}})
		}
	}
	if p.pinned && !writesAttribute(n, "background") && !neutral {
		// pinned to an edge, the content scrolls under it: with no background of its own it takes the
		// page's (<page background>, else canvas), or the content would show through; on a page
		// backdrop the canvas, or nothing under a glass (backdrop.go)
		if background, ok := c.pinnedGround(n, cx); ok {
			classes = append(classes, c.engineClass("background", background))
			frame.written = append(frame.written, &paintIntent{name: "background", node: n, values: []string{background}})
		}
	}
	if focus := stylesheet.LookIn("focus", frame.html, frame.classes, frame.inputType, 0).Surface; focus != "" &&
		focus != frame.look(0).Surface && !writesAttribute(n, "background") && !writesAttribute(n, "material") {
		// reboot.css deepens a field's fill to ink/8 while it has focus (a written background wins in every
		// state, page over elements): what is typed is measured on that fill too
		frame.written = append(frame.written, &paintIntent{name: "background", node: n, states: []string{"focus"}, values: []string{focus}})
	}
	c.measureOwn(n, frame)
	if n.Tag == "badge" {
		c.uses["badge"] = true
	}
	if n.Tag == "accordions" {
		cx.accordionName = fmt.Sprintf("accordions-%d", c.next("accordions"))
	}
	if containsPlaceChild(n) {
		classes = append(classes, "place-anchor")
	}
	classes = append(classes, p.classes...)
	classes = append(classes, p.show...)
	if info.region && n.Tag != "overlay" && !writesWidth(n) {
		// the cap of <page container="…">; a region that writes its own width keeps its own. overlay is
		// a layer over the screen, not a band of the page: what it holds floats, so it is not capped.
		classes = append(classes, cx.container...)
	}
	if site != nil {
		classes = append(classes, site.classes...)
	}
	o.staticClass = dedupe(classes)
	o.dynClasses = p.dynClasses
	o.forEach, o.ifExpr, o.showExpr = p.forEach, p.ifExpr, p.showExpr
	if site != nil {
		mergeCallSite(o, site)
	}

	var attrs strings.Builder
	// the host of a component the kernel runs: data-kitwork-component="dropdown", or with an alias
	// "dropdown: $account" (KITWORK_UI §11), first on its tag. A site component's host is not marked
	// yet: its logic (cart.work) does not reach the client until the engine runs component.define
	// (step 4), and the kernel reports a host whose component it cannot find; its names still cover
	// the page's on the server. A layout piece has no instance and is never marked.
	writeHost(&attrs, site)
	if err := c.tagAttributes(n, info, p, o, &attrs, cx); err != nil {
		return nil, err
	}
	scope := cx.scope
	runtime, err := c.runtimeAttributes(n, p, o, scope)
	if err != nil {
		return nil, err
	}
	if site != nil && site.showBind != "" {
		runtime.binds = append(runtime.binds, site.showBind)
	}
	if n.Tag == "page" && c.layout != "" {
		writeAttr(&attrs, "data-kitwork-layout", c.layout)
	}
	anchor, err := c.anchorAttribute(n, p, cx)
	if err != nil {
		return nil, err
	}
	o.staticAttrs = anchor + attrs.String()

	if info.kind != kindVoid {
		childCtx := cx
		childCtx.inline = info.kind == kindInline || cx.breadcrumb && n.Tag == "item"
		childCtx.flex = isFlex(o.staticClass)
		childCtx.pageChild = n.Tag == "page"
		childCtx.paint = frame
		childCtx.container = p.regions // set on <page> only: the regions directly inside it
		if n.Tag == "page" {
			childCtx.pageBackground = pageBackgroundOf(n)
		}
		childCtx.scope = scope
		if n.Tag == "field" {
			childCtx.field = &fieldIDs{control: fmt.Sprintf("kitwork-field-%d", c.next("field"))}
			if hasChild(n, "hint") {
				childCtx.field.hint = childCtx.field.control + "-hint"
			}
		}
		if n.Tag != "item" && n.Tag != "field" && n.Tag != "accordions" {
			childCtx.breadcrumb = false
		}
		if n.Tag == "list" {
			var normal, empty []*Node
			for _, ch := range n.Children {
				if ch.Tag == "empty" {
					empty = append(empty, ch)
					continue
				}
				normal = append(normal, ch)
			}
			if err := checkListChildren(n, normal); err != nil {
				return nil, err
			}
			o.children, err = c.childNodes(normal, childCtx)
			if err != nil {
				return nil, err
			}
			o.emptyOps, err = c.childNodes(empty, childCtx)
			if err != nil {
				return nil, err
			}
		} else if terminal {
			// the author's markup, each <code> a command with its prompt, then the status region (terminal.go)
			if o.children, err = c.terminalChildren(n, childCtx); err != nil {
				return nil, err
			}
		} else {
			parent := n
			if n.Tag == "page" {
				if parent, err = c.withProgressbar(n); err != nil {
					return nil, err
				}
				if parent, err = c.withReader(parent); err != nil {
					return nil, err
				}
			}
			o.children, err = c.children(parent, childCtx)
			if err != nil {
				return nil, err
			}
		}
	}
	if backdrop != nil {
		o.children = append([]op{backdrop}, o.children...) // first in <body>, behind everything (backdrop.go)
	}
	if n.Tag == "item" {
		o.countsItem = true
	}
	if n.Tag == "empty" {
		o.staticAttrs += ` role="status"`
	}
	if info.html == "ul" {
		// no markers (reboot.css): Safari drops a list's semantics without them unless it says it is one
		o.staticAttrs += ` role="list"`
	}
	if err := c.withText(n, o, scope, runtime, cx.aliases); err != nil {
		return nil, err
	}
	return fold(o), nil
}

// popoverShow reads a show written on a popover and leaves it out: a popover shows while the component
// around it is open, so show="true" says nothing more (decision D: a warning). Its value is read first,
// so an expression that does not parse is still an error, never dropped unread. show="false" or a
// condition means to keep it hidden while open, which the engine does not draw: left out, the popover
// would show what it was written to hide, so that is an error.
func (c *compiler) popoverShow(n *Node) (*Node, error) {
	a := attrNamed(n, "show")
	if a.Name != "show" {
		return n, nil
	}
	if !literalBoolean(a.Value) {
		if _, err := compileExpr(a.Value, n, a); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(a.Value) != "true" {
		return nil, errAttr(n, a, "popover hiện khi component chứa nó mở; show=\"%s\" trên popover chưa được hỗ trợ (bỏ nó thì popover vẫn hiện khi mở): ẩn hay hiện phần bên trong bằng show trên một thẻ trong popover", a.Value)
	}
	c.warn(n.File, a.Line, a.Column, "popover hiện khi component chứa nó mở; không viết show (trang bỏ show này)")
	without := *n
	without.Attrs = nil
	for _, other := range n.Attrs {
		if other.Name != "show" {
			without.Attrs = append(without.Attrs, other)
		}
	}
	return &without, nil
}

// sharedTag are the .ui tags whose HTML element another tag also makes: their look is keyed by the
// tag word as a class (reboot.css), the others' by the element itself.
var sharedTag = map[string]bool{"card": true, "badge": true, "popover": true, "field": true, "hint": true, "error": true}

// neutralSteps are the fill of a neutral button, written as the author's background would be (one
// table, one page stylesheet): translucent ink, as a system's fills are (decision F1, 13/10/2026), so
// the button shows on the grounds a page is made of — the canvas, a card's paper, the panel, a page's
// backdrop — where a solid fill vanishes on its own colour. Not on a band painted ink itself, nor one
// near it (brand+15, midnightblue): ink over ink is ink, so there the button shows no fill and no
// edge, and its author writes a background or a border (TestTranslucentFillsShowOnEveryGround keeps
// that exception in sight). ink/6 at rest, deeper under the
// pointer (ink/10) and while pressed (ink/14), fainter while disabled (ink/4), so a button that cannot
// be pressed never answers the pointer; reboot.css dims its words too. The contrast walk composites
// each over what lies behind the button. No border: border="line" draws one where the author wants it.
var neutralSteps = []struct{ state, value string }{
	{"", "ink/6"}, {"hover", "ink/10"}, {"pressed", "ink/14"}, {"disabled", "ink/4"},
}

// neutralButton reports whether a button is neutral: its author writes nothing about its fill or its
// edge, no background, material or border with or without a prefix. hover:background="panel" alone is
// a button that shows a fill only under the pointer, border="none" a bare one: both are the author's.
func neutralButton(n *Node) bool {
	return n.Tag == "button" && !writesAttribute(n, "background") && !writesAttribute(n, "material") &&
		!writesAttribute(n, "border")
}

// engineWords are the classes an element gets before its attributes: the tag word where the HTML
// element is shared, then its direction when it lays out children and flex is not written (a
// container a column; a button, a badge, a link holding elements and a breadcrumb entry a row). The
// direction is one class read by reboot.css; align and space written on the element only set the
// variables it maps onto the flex axes, so medium:flex="row" re-reads the same align.
func engineWords(n *Node, info tagInfo, cx ctx) []string {
	var out []string
	if sharedTag[n.Tag] {
		out = append(out, n.Tag)
	}
	if _, written := n.Attr("flex"); written {
		return out
	}
	switch {
	case cx.breadcrumb && n.Tag == "item":
		out = append(out, "flex-row")
	case info.kind == kindContainer:
		out = append(out, "flex-column")
	case info.kind == kindGrid:
		out = append(out, "grid")
	case n.Tag == "button", n.Tag == "badge", n.Tag == "link" && len(n.Elements()) > 0:
		out = append(out, "flex-row")
	}
	return out
}

// containsPlaceChild reports whether a child of n is placed against it: written with place="…", or a
// popover, which hangs below the start edge of what opens it even when it writes no place. A place the
// page drops (with a warning) places nothing: a fragment builds no element, so what it holds stands
// in n, and a reader takes no place.
func containsPlaceChild(n *Node) bool {
	for _, ch := range n.Elements() {
		switch ch.Tag {
		case "popover":
			return true
		case "fragment":
			if containsPlaceChild(ch) {
				return true
			}
			continue
		case "reader":
			continue
		}
		for _, a := range ch.Attrs {
			if a.Name == "place" || strings.HasSuffix(a.Name, ":place") {
				return true
			}
		}
	}
	return false
}

// writesWidth reports whether an element writes its own container or max-width, with or without a prefix.
func writesWidth(n *Node) bool {
	for _, a := range n.Attrs {
		base := a.Name[strings.LastIndex(a.Name, ":")+1:]
		if base == "container" || base == "max-width" {
			return true
		}
	}
	return false
}

func hasChild(n *Node, tag string) bool {
	for _, ch := range n.Elements() {
		if ch.Tag == tag {
			return true
		}
	}
	return false
}

func checkListChildren(n *Node, children []*Node) error {
	for _, ch := range children {
		if ch.IsText() {
			if strings.TrimSpace(ch.Text) != "" {
				return errAt(ch, "chữ không đặt thẳng trong list; bọc trong <item>")
			}
			continue
		}
		switch ch.Tag {
		case "item", "fragment":
		default:
			return errAt(ch, "con của list là item (danh sách rỗng thì thêm empty); bọc <%s> trong <item>", ch.Tag)
		}
	}
	return nil
}

func mergeCallSite(o *elemOp, site *callSite) {
	if site.forEach != nil {
		o.forEach = site.forEach
	}
	if site.def != nil {
		// a call of a layout piece or a component: the call's if and show and its inputs are read where
		// the call stands; then the inputs go in and a component's names cover the page's (the root's
		// own if and show read them)
		o.callerIf, o.callerShow = site.ifExpr, site.showExpr
		o.bindings = append(o.bindings, site.bindings...)
		o.inputs = site.inputs
		if site.state != nil {
			o.state = make(map[string]any, len(site.state))
			for name, v := range site.state {
				o.state[name] = v
			}
			for _, b := range site.bindings {
				delete(o.state, b.name) // data passed in at the call is what the component starts from
			}
		}
		return
	}
	if site.ifExpr != nil {
		if o.ifExpr != nil {
			inner := o.ifExpr
			outer := site.ifExpr
			o.ifExpr = &expr{source: "(" + outer.source + ") && (" + inner.source + ")", ir: []any{"&&", outer.ir, inner.ir}}
		} else {
			o.ifExpr = site.ifExpr
		}
	}
	if site.showExpr != nil {
		o.showExpr = site.showExpr
	}
	o.bindings = append(o.bindings, site.bindings...)
}

// next numbers generated names per kind (field ids, accordion groups); stable for one source.
func (c *compiler) next(kind string) int {
	c.seq[kind]++
	return c.seq[kind]
}

// tagAttributes writes the HTML attributes a tag's own meaning produces.
func (c *compiler) tagAttributes(n *Node, info tagInfo, p *attrPlan, o *elemOp, w *strings.Builder, cx ctx) error {
	allowed := map[string]bool{}
	for _, name := range passThrough[n.Tag] {
		allowed[name] = true
	}
	used := map[string]bool{}

	take := func(name string) *Attr {
		if a, ok := p.semantic[name]; ok {
			used[name] = true
			return a
		}
		return nil
	}

	// value="" is as missing as no value: a link to nowhere, an image of nothing, an icon without a name
	required := func(name string) *Attr {
		if a := take(name); a != nil && strings.TrimSpace(a.Value) != "" {
			return a
		}
		return nil
	}
	switch n.Tag {
	case "link":
		a := required("value")
		if a == nil {
			if h := p.semantic["href"]; h != nil {
				return errAttr(n, h, "href là chữ của web; đích của link là value: <link value=\"%s\">", strings.TrimSpace(h.Value))
			}
			return errAt(n, "link cần value (đích đến)")
		}
		in, err := parseInterpolation(a.Value, n, a)
		if err != nil {
			return err
		}
		if in.static() && !safeLink(in.literal()) {
			return errAttr(n, a, "link chỉ đi tới đường dẫn trong site (/…, ./…, #…), http:, https:, mailto: hoặc tel:")
		}
		o.href = in
		o.kept = cx.kept
		if t := take("target"); t != nil {
			return errAttr(n, t, "target là chữ của web; mở ở tab mới viết new-tab=\"true\"")
		}
		if t := take("new-tab"); t != nil {
			switch strings.TrimSpace(t.Value) {
			case "true":
				o.newTab = true
			case "false":
			default:
				return errAttr(n, t, "new-tab chỉ nhận \"true\" hoặc \"false\"")
			}
		}
		if e := take("exact"); e != nil {
			switch strings.TrimSpace(e.Value) {
			case "true":
				o.exact = true
			case "false":
			default:
				return errAttr(n, e, "exact chỉ nhận \"true\" hoặc \"false\"")
			}
		}
		if r := take("reload"); r != nil {
			switch strings.TrimSpace(r.Value) {
			case "true":
				writeAttr(w, "data-kit-reload", "true") // this link loads the next page whole
			case "false":
			default:
				return errAttr(n, r, "reload chỉ nhận \"true\" hoặc \"false\"")
			}
		}
		if cx.breadcrumb {
			o.crumbLink = true
		}
	case "image":
		a := required("value")
		if a == nil {
			if s := p.semantic["src"]; s != nil {
				return errAttr(n, s, "src là chữ của web; nguồn ảnh là value: <image value=\"%s\" …>", strings.TrimSpace(s.Value))
			}
			return errAt(n, "image cần value (nguồn ảnh)")
		}
		if !strings.Contains(a.Value, "{{") && !safeImage(strings.TrimSpace(a.Value)) {
			return errAttr(n, a, "image chỉ lấy ảnh từ đường dẫn trong site, http:, https: hoặc data:image/…")
		}
		if err := c.attrValue(n, a, "src", false, o, w); err != nil {
			return err
		}
		if len(o.dynAttrs) > 0 && o.dynAttrs[len(o.dynAttrs)-1].name == "src" {
			o.dynAttrs[len(o.dynAttrs)-1].image = true // checked again when the data fills it
		}
		if alt := take("alt"); alt == nil {
			// the image shows as written, without an alt: a screen reader reads its file name or nothing
			c.warn(n.File, n.Line, n.Column, "image cần alt (alt=\"\" nếu chỉ để trang trí)")
		} else if err := c.attrValue(n, alt, "alt", false, o, w); err != nil {
			return err
		}
		// a static image of the site that can be read now: its size, so the browser keeps its room
		// before it arrives and nothing below jumps (image.go)
		if width, height, ok := c.imageSize(strings.TrimSpace(a.Value)); ok {
			w.WriteString(` width="` + strconv.Itoa(width) + `" height="` + strconv.Itoa(height) + `"`)
		}
		w.WriteString(` loading="lazy" decoding="async"`)
	case "icon":
		a := required("value")
		if a == nil {
			return errAt(n, "icon cần value (tên icon)")
		}
		if strings.Contains(a.Value, "{{") {
			in, err := parseInterpolation(a.Value, n, a)
			if err != nil {
				return err
			}
			o.dynClasses = append(o.dynClasses, dynClass{name: "icon", icon: true, in: in,
				where: fmt.Sprintf("%s:%d:%d", n.File, a.Line, a.Column), written: a.Name + `="` + a.Value + `"`})
		} else {
			// an icon the set does not have draws nothing (jit/icons skips it): a typo never vanishes
			if name := strings.TrimSpace(a.Value); !icons.Has(name) {
				return errAttr(n, a, "không có icon %q trong bộ icon của Kitwork (Tabler)%s", name, nearestWords(name, icons.Names()))
			}
			o.staticClass = append(o.staticClass, "icon-"+a.Value)
		}
		// No alt is decoration, as alt="" is (KITWORK_UI §7, "Tên đọc được"): an icon nearly always stands
		// next to words that already say it. Where it is the only thing a button, a link or a badge says,
		// requireName refuses the element instead.
		alt := take("alt")
		if alt == nil || strings.TrimSpace(alt.Value) == "" {
			w.WriteString(` aria-hidden="true"`)
		} else {
			w.WriteString(` role="img"`)
			if err := c.attrValue(n, alt, "aria-label", false, o, w); err != nil {
				return err
			}
		}
	case "button":
		if a := take("expanded"); a != nil {
			// what this button opens is open: aria-expanded on the web, the platform's own on native
			if v := strings.TrimSpace(a.Value); v != "true" && v != "false" && !strings.Contains(v, "{{") {
				return errAttr(n, a, "expanded chỉ nhận \"true\", \"false\" hoặc {{ }}")
			}
			if err := c.attrValue(n, a, "aria-expanded", false, o, w); err != nil {
				return err
			}
		}
		typ := "button"
		if a := take("type"); a != nil {
			switch a.Value {
			case "button", "submit", "reset":
				typ = a.Value
			default:
				return errAttr(n, a, "type của button là button, submit hoặc reset")
			}
		}
		writeAttr(w, "type", typ)
		if a := take("value"); a != nil {
			if err := c.attrValue(n, a, "value", false, o, w); err != nil {
				return err
			}
		}
	case "input":
		if err := controlName(c, n, take("alt"), o, w); err != nil {
			return err
		}
		typ, _ := n.Attr("type")
		if typ == "switch" {
			used["type"] = true
			w.WriteString(` type="checkbox" role="switch"`)
		} else if a := p.semantic["type"]; a != nil && !strings.Contains(a.Value, "{{") && !inputTypes[strings.TrimSpace(a.Value)] {
			// the browser reads a type it does not know as text: a typo (type="emial") never vanishes
			return errAttr(n, a, "type của input là %s, không có %q", inputTypeWords, a.Value)
		}
		// copied to the HTML as written, and a word the browser does not know is ignored: read here
		for _, name := range []string{"inputmode", "autocomplete"} {
			if a := p.semantic[name]; a != nil && !strings.Contains(a.Value, "{{") {
				if problem := wordValueProblem(name, strings.TrimSpace(a.Value)); problem != "" {
					return errAttr(n, a, "%s", problem)
				}
			}
		}
		if a := take("value"); a != nil {
			if err := c.attrValue(n, a, "value", false, o, w); err != nil {
				return err
			}
		}
		if cx.field != nil {
			writeAttr(w, "id", cx.field.control)
			if cx.field.hint != "" {
				writeAttr(w, "aria-describedby", cx.field.hint)
			}
		}
	case "select":
		if err := controlName(c, n, take("alt"), o, w); err != nil {
			return err
		}
		if a := take("value"); a != nil {
			in, err := parseInterpolation(a.Value, n, a)
			if err != nil {
				return err
			}
			o.selectValue = in
		}
		if cx.field != nil {
			writeAttr(w, "id", cx.field.control)
			if cx.field.hint != "" {
				writeAttr(w, "aria-describedby", cx.field.hint)
			}
		}
	case "option":
		a := take("value")
		if a == nil {
			return errAt(n, "option cần value")
		}
		in, err := parseInterpolation(a.Value, n, a)
		if err != nil {
			return err
		}
		o.optionValue = in
		if err := c.attrValue(n, a, "value", false, o, w); err != nil {
			return err
		}
	case "textarea":
		if err := controlName(c, n, take("alt"), o, w); err != nil {
			return err
		}
		if a := take("value"); a != nil {
			in, err := parseInterpolation(a.Value, n, a)
			if err != nil {
				return err
			}
			o.content = in
		}
		if cx.field != nil {
			writeAttr(w, "id", cx.field.control)
		}
	case "label":
		if cx.field != nil {
			writeAttr(w, "for", cx.field.control)
		}
	case "hint":
		if cx.field != nil && cx.field.hint != "" {
			writeAttr(w, "id", cx.field.hint)
		}
	case "time":
		a := required("value")
		if a == nil {
			return errAt(n, "time cần value (thời điểm máy đọc được)")
		}
		if err := c.attrValue(n, a, "datetime", false, o, w); err != nil {
			return err
		}
	case "popover":
		// it shows when the component around it opens: closed in the page as built (a show written on it
		// was read and left out by popoverShow)
		// unplaced it hangs below the start edge of what opens it (reboot.css); a written place replaces
		// that whole
		w.WriteString(" hidden")
	case "badge", "accordions", "box", "card", "section", "list", "item":
		if a := take("alt"); a != nil {
			if err := c.attrValue(n, a, "aria-label", false, o, w); err != nil {
				return err
			}
		}
	}

	for _, name := range p.order {
		if used[name] {
			continue
		}
		a := p.semantic[name]
		if !allowed[name] {
			if done, err := c.notHere(n, a, "<"+n.Tag+">", ""); done || err != nil {
				if err != nil {
					return err
				}
				continue // a word of .ui another tag reads: no effect here, told as a warning
			}
			return errAttr(n, a, "thuộc tính %s không có trên <%s>", name, n.Tag)
		}
		if err := c.attrValue(n, a, name, booleanAttributes[name], o, w); err != nil {
			return err
		}
	}
	return nil
}

// attrValue writes an attribute that may hold {{ }}: static values are written now, dynamic ones are
// left as holes. Boolean attributes are present for "true", absent for "false".
func (c *compiler) attrValue(n *Node, a *Attr, htmlName string, boolean bool, o *elemOp, w *strings.Builder) error {
	if boolean {
		switch strings.TrimSpace(a.Value) {
		case "true":
			w.WriteString(" " + htmlName)
			return nil
		case "false":
			return nil
		}
		if !strings.Contains(a.Value, "{{") {
			return errAttr(n, a, "%s chỉ nhận \"true\", \"false\" hoặc {{ }}", a.Name)
		}
	}
	in, err := parseInterpolation(a.Value, n, a)
	if err != nil {
		return err
	}
	if in.static() {
		writeAttr(w, htmlName, in.literal())
		return nil
	}
	o.dynAttrs = append(o.dynAttrs, dynAttr{name: htmlName, in: in, boolean: boolean})
	return nil
}

// inputTypes are the types an <input> takes: HTML's, with switch (a checkbox drawn as a switch). The
// browser reads any other word as text, so a type="emial" would lose its keyboard and its checks.
var inputTypes = map[string]bool{
	"text": true, "search": true, "email": true, "url": true, "tel": true, "password": true, "number": true,
	"range": true, "date": true, "time": true, "datetime-local": true, "month": true, "week": true, "color": true,
	"checkbox": true, "radio": true, "file": true, "hidden": true, "submit": true, "reset": true, "button": true,
	"switch": true,
}

const inputTypeWords = "text, search, email, url, tel, password, number, range, date, time, datetime-local, month, week, color, checkbox, radio, file, hidden, submit, reset, button hoặc switch"

// notHere decides an attribute that the tag it is written on does not read (decision D, questions 1
// and 2). where names the place in a message (<box>, phần đầu của accordion), hint says what the place
// takes instead.
//   - A word of the web that .ui says in a word of its own (href, src, class, style, onclick, title,
//     margin, pl, font-size…) is an error naming that word: done, with the error.
//   - A word of .ui that another tag reads (value on a box, alt on a quote, gap on a reader, click on
//     a fragment) does nothing here. Its value is read first, the way the tag that reads it would, so
//     a value that does not read is that error, never a warning; then the page renders without it and
//     a warning says so: done, no error.
//   - Any other word is a typo: not done, and the caller's own error names it.
func (c *compiler) notHere(n *Node, a *Attr, where, hint string) (bool, error) {
	if message := c.webWord(n, a); message != "" {
		return true, errAttr(n, a, "%s", message)
	}
	if !uiWord(a.Name) {
		return false, nil
	}
	if err := c.readValue(n, a); err != nil {
		return true, err
	}
	message := fmt.Sprintf("%s không có tác dụng trên %s; trang hiện như không viết nó", a.Name, where)
	if hint != "" {
		message += " (" + hint + ")"
	}
	c.warn(n.File, a.Line, a.Column, message)
	return true, nil
}

// kitworkInput decides an attribute that a Kitwork element writing its own markup (reader, toast,
// progressbar) does not read, the way notHere does, except for, if, show and id: dropping those would
// put the element where it was written not to be (or hide a reader from the screen reader), so they
// stay the caller's error. drawn says the host is seen (the toast's stack, the progressbar's line, not
// the reader's clipped region): presentation or an event on it would do something, which the engine
// does not attach to a call of Kitwork's component yet (question 5), so dropping it would change the
// page; its value is read first, then it is the caller's error too.
func (c *compiler) kitworkInput(n *Node, a *Attr, drawn bool, where, hint string) (bool, error) {
	base := a.Name[strings.LastIndex(a.Name, ":")+1:]
	switch base {
	case "for", "if", "show", "id":
		return false, nil
	}
	if drawn && (isEvent(a.Name) || stylesheet.IsPresentation(base)) {
		if err := c.readValue(n, a); err != nil {
			return true, err
		}
		return false, nil
	}
	return c.notHere(n, a, where, hint)
}

// uiWord reports whether a name is a word of .ui on some tag: an event, a presentation attribute (with
// or without a prefix), show, a control (for, if, as, source, component), an attribute some tag reads
// (tagWords, passThrough), a true-or-false attribute, a progressbar's delay. id is no word here: an id
// written where nothing carries it would leave its links nowhere to land.
func uiWord(name string) bool {
	if isEvent(name) {
		return true
	}
	base := name[strings.LastIndex(name, ":")+1:]
	if base == "show" || stylesheet.IsPresentation(base) {
		return true
	}
	if base != name {
		return false // only presentation and show take a prefix
	}
	switch name {
	case "for", "if", "as", "source", "component", "delay":
		return true
	}
	if booleanAttributes[name] {
		return true
	}
	for tag := range tagWords {
		if tagWord(tag, name) {
			return true
		}
	}
	for tag := range passThrough {
		if tagWord(tag, name) {
			return true
		}
	}
	return false
}

// htmlEvents are the event names of HTML's on… attributes, each with the name .ui writes the event by.
var htmlEvents = map[string]string{
	"click": "click", "dblclick": "double-click", "contextmenu": "context-menu",
	"keydown": "keydown", "keyup": "keyup", "keypress": "keydown",
	"input": "input", "change": "change", "submit": "submit", "scroll": "scroll",
	"load": "load", "error": "error", "toggle": "toggle",
	"focus": "focus-in", "blur": "focus-out", "focusin": "focus-in", "focusout": "focus-out",
	"mouseenter": "pointer-enter", "mouseleave": "pointer-leave", "mouseover": "pointer-enter", "mouseout": "pointer-leave",
	"mousedown": "pointer-down", "mouseup": "pointer-up",
	"pointerenter": "pointer-enter", "pointerleave": "pointer-leave", "pointerdown": "pointer-down", "pointerup": "pointer-up",
}

// webWord is the error of an attribute name of the web that .ui says in a word of its own, "" for any
// other name (decision D, question 2): HTML's href, src, class, style, title and on…, and the CSS or
// Tailwind names the stylesheet knows the .ui word for (margin, pl, font-size…). title is
// answered by what names the element it is written on: the text inside, alt where the tag reads it, the
// alt of the icon or image inside a link or a button.
func (c *compiler) webWord(n *Node, a *Attr) string {
	name, value := a.Name, strings.TrimSpace(a.Value)
	if name == "title" {
		message := "title là chữ của web, .ui không có; tên đọc được là chữ bên trong phần tử"
		switch {
		case tagWord(n.Tag, "alt"):
			message += ", hoặc alt (<" + n.Tag + " alt=\"…\">)"
		case n.Tag == "link" || n.Tag == "button":
			message += ", hoặc alt trên icon hay ảnh bên trong (<icon value=\"x\" alt=\"Đóng\" />)"
		}
		return message
	}
	switch name {
	case "href":
		return fmt.Sprintf("href là chữ của web; đích của link là value: <link value=\"%s\">…</link>", value)
	case "src":
		return fmt.Sprintf("src là chữ của web; nguồn ảnh là value: <image value=\"%s\" alt=\"…\" />", value)
	case "class":
		return "class là chữ của web; .ui không có class: trình bày viết bằng thuộc tính của nó (gap, padding, background…)"
	case "style":
		return "style là chữ của web; .ui không có style: trình bày viết bằng thuộc tính của nó (gap, padding, background…)"
	}
	if strings.HasPrefix(name, "on") {
		if event, ok := htmlEvents[name[2:]]; ok {
			return fmt.Sprintf("%s là chữ của web; sự kiện viết bằng tên của nó, không có on: %s=\"%s\"", name, event, value)
		}
	}
	if !stylesheet.IsPresentation(name) && !strings.Contains(name, ":") {
		// the stylesheet's table of misnamed attributes: what .ui writes for margin, pl…
		if _, err := c.table.Class(nil, stylesheet.Intent{Name: name, Value: value}); err != nil &&
			!strings.HasSuffix(err.Error(), "không phải thuộc tính trình bày") {
			return err.Error()
		}
	}
	return ""
}

// prefixedWebWord is webWord for a word of the web written with a prefix (medium:pl="4"): what the
// bare word is told, behind its prefix, and, when the table names the .ui attribute it is, that
// attribute with the prefix kept ("Có tiền tố thì viết medium:padding-left"). "" for any other name: a
// word of .ui that takes no prefix, a typo.
func (c *compiler) prefixedWebWord(n *Node, a *Attr, chain, base string) string {
	bare := *a
	bare.Name = base
	message := c.webWord(n, &bare)
	if message == "" {
		return ""
	}
	message = chain + ":" + message
	_, err := c.table.Class(nil, stylesheet.Intent{Name: base, Value: strings.TrimSpace(a.Value)})
	if told, ok := err.(*stylesheet.Problem); ok && told.Suggestion != "" {
		message += ". Có tiền tố thì viết " + chain + ":" + told.Suggestion // the table's suggestions are presentation attributes
	}
	return message
}

// readValue reads the value of a word of .ui written where it does nothing, the way the tag that reads
// it would: an expression that does not parse, a word the stylesheet refuses, a true-or-false that is
// neither, is an error even there (a typo never vanishes behind a warning).
func (c *compiler) readValue(n *Node, a *Attr) error {
	if isEvent(a.Name) && !scrollArea(a.Name, a.Value) {
		_, err := eventExpression(n, a)
		return err
	}
	chain, base := "", a.Name
	if i := strings.LastIndex(a.Name, ":"); i >= 0 {
		chain, base = a.Name[:i], a.Name[i+1:]
	}
	pre := prefix{keep: true}
	if chain != "" {
		var err error
		if pre, err = c.prefixOf(n, a, chain, base); err != nil {
			return err
		}
	}
	value := strings.TrimSpace(a.Value)
	switch {
	case base == "for":
		_, err := parseFor(a.Value, n, a)
		return err
	case base == "if", base == "show" && !literalBoolean(value):
		_, err := compileExpr(a.Value, n, a)
		return err
	case base == "show" || stylesheet.IsPresentation(base):
		values := []string{value}
		if strings.Contains(value, "{{") {
			in, err := parseInterpolation(a.Value, n, a)
			if err != nil {
				return err
			}
			values = interpolationCandidates(in) // nil from data: nothing written to read
		}
		for _, v := range values {
			if v == "" {
				continue
			}
			_, err := c.table.Class(nil, pre.intent("box", base, v))
			if problem, ok := err.(*stylesheet.Problem); ok && problem.Warning {
				err = nil // read: what it would warn of is moot where it does nothing
			}
			if err != nil {
				return errAttr(n, a, "%s", err.Error())
			}
		}
		return nil
	case base == "as":
		if !aliasName(value) {
			return errAttr(n, a, "as là $ và một tên bắt đầu bằng chữ (as=\"$account\"), không phải %q", a.Value)
		}
		return nil
	case booleanAttributes[base] && value != "true" && value != "false" && !strings.Contains(value, "{{"):
		return errAttr(n, a, "%s chỉ nhận \"true\", \"false\" hoặc {{ }}", a.Name)
	case !strings.Contains(value, "{{"):
		if problem := wordValueProblem(base, value); problem != "" {
			return errAttr(n, a, "%s", problem)
		}
	}
	if strings.Contains(a.Value, "{{") {
		_, err := parseInterpolation(a.Value, n, a)
		return err
	}
	return nil
}

// wordValueProblem says what is wrong with the written value of a word of .ui that takes a fixed set of
// values or a number, "" when it reads or the word takes any text: readValue asks it for a word written
// where it does nothing, so a typo there is still an error (decision D, question 1), and the tag that
// reads the word asks the same.
func wordValueProblem(name, value string) string {
	switch name {
	case "heading":
		if !headingLevel(value) {
			return "heading là số tĩnh từ 1 đến 6"
		}
	case "type":
		if !inputTypes[value] {
			return fmt.Sprintf("type của input là %s; của button là button, submit hoặc reset; không có %q", inputTypeWords, value)
		}
	case "expanded":
		if value != "true" && value != "false" {
			return "expanded chỉ nhận \"true\", \"false\" hoặc {{ }}"
		}
	case "delay":
		if ms, err := strconv.Atoi(value); err != nil || ms < 0 || !digits(value) {
			return fmt.Sprintf("delay là số mili giây từ 0 đến 10000 (mặc định 120; 0 là hiện ngay), không phải %q", value)
		}
	case "inputmode": // HTML reads both without telling case apart
		if !inputModes[strings.ToLower(value)] {
			return fmt.Sprintf("inputmode là %s, không có %q", inputModeWords, value)
		}
	case "language":
		if problem := languageProblem(value); problem != "" {
			return problem
		}
	case "autocomplete":
		if !autocompleteValue(strings.ToLower(value)) {
			return fmt.Sprintf("autocomplete là on, off hoặc tên trường của HTML (email, username, current-password, new-password, one-time-code, name, tel, street-address, postal-code…, có thể kèm section-…, shipping hay billing), không có %q", value)
		}
	}
	return ""
}

// nearestWords names the words of a set closest to a word written that is not one of them, for an
// error ("; gần nhất: home, house"): at most three, those fewest edits away, within a third of the
// word's length (two at least); "" when none is that close.
func nearestWords(word string, set []string) string {
	limit := len(word) / 3
	if limit < 2 {
		limit = 2
	}
	best := limit + 1
	var near []string
	for _, candidate := range set {
		d := editDistance(word, candidate, best)
		switch {
		case d < best:
			best, near = d, []string{candidate}
		case d == best && d <= limit:
			near = append(near, candidate)
		}
	}
	if len(near) == 0 {
		return ""
	}
	if len(near) > 3 {
		near = near[:3]
	}
	return "; gần nhất: " + strings.Join(near, ", ")
}

// editDistance is the number of single-letter edits between two words (Levenshtein), or more than
// limit as soon as it is.
func editDistance(a, b string, limit int) int {
	if d := len(a) - len(b); d > limit || -d > limit {
		return limit + 1
	}
	previous := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(a); i++ {
		current := make([]int, len(b)+1)
		current[0] = i
		lowest := current[0]
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
			lowest = min(lowest, current[j])
		}
		if lowest > limit {
			return limit + 1
		}
		previous = current
	}
	return previous[len(b)]
}

// headingLevel reports whether a heading is written as its level, 1 to 6, digits only.
func headingLevel(value string) bool {
	return len(value) == 1 && value[0] >= '1' && value[0] <= '6'
}

// digits reports whether a value is written in digits only: no sign, no space, no point.
func digits(value string) bool {
	if value == "" {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}

// inputModes are the keyboards an <input> asks for (HTML inputmode); the browser ignores any other word.
var inputModes = map[string]bool{
	"none": true, "text": true, "decimal": true, "numeric": true, "tel": true, "search": true, "email": true, "url": true,
}

const inputModeWords = "none, text, decimal, numeric, tel, search, email hoặc url"

// autocompleteFields are the field names of HTML's autocomplete; autocompleteContact those a home,
// work, mobile, fax or pager may come before.
var autocompleteFields = map[string]bool{
	"name": true, "honorific-prefix": true, "given-name": true, "additional-name": true, "family-name": true,
	"honorific-suffix": true, "nickname": true, "username": true, "new-password": true, "current-password": true,
	"one-time-code": true, "organization-title": true, "organization": true, "street-address": true,
	"address-line1": true, "address-line2": true, "address-line3": true, "address-level4": true,
	"address-level3": true, "address-level2": true, "address-level1": true, "country": true, "country-name": true,
	"postal-code": true, "cc-name": true, "cc-given-name": true, "cc-additional-name": true, "cc-family-name": true,
	"cc-number": true, "cc-exp": true, "cc-exp-month": true, "cc-exp-year": true, "cc-csc": true, "cc-type": true,
	"transaction-currency": true, "transaction-amount": true, "language": true, "bday": true, "bday-day": true,
	"bday-month": true, "bday-year": true, "sex": true, "url": true, "photo": true,
}

var autocompleteContact = map[string]bool{
	"tel": true, "tel-country-code": true, "tel-national": true, "tel-area-code": true, "tel-local": true,
	"tel-extension": true, "email": true, "impp": true,
}

// autocompleteValue reports whether an autocomplete reads as HTML's: on or off alone, or its tokens in
// order — section-…, shipping or billing, home, work, mobile, fax or pager before a contact field, one
// field name, webauthn last. The browser ignores any other value, so a typo (emial) never vanishes.
func autocompleteValue(value string) bool {
	tokens := strings.Fields(value)
	if len(tokens) == 1 && (tokens[0] == "on" || tokens[0] == "off") {
		return true
	}
	if len(tokens) > 0 && tokens[len(tokens)-1] == "webauthn" {
		tokens = tokens[:len(tokens)-1]
	}
	if len(tokens) > 0 && strings.HasPrefix(tokens[0], "section-") && len(tokens[0]) > len("section-") {
		tokens = tokens[1:]
	}
	if len(tokens) > 0 && (tokens[0] == "shipping" || tokens[0] == "billing") {
		tokens = tokens[1:]
	}
	switch len(tokens) {
	case 1:
		return autocompleteFields[tokens[0]] || autocompleteContact[tokens[0]]
	case 2:
		switch tokens[0] {
		case "home", "work", "mobile", "fax", "pager":
			return autocompleteContact[tokens[1]]
		}
	}
	return false
}

func dedupe(classes []string) []string {
	seen := map[string]bool{}
	out := classes[:0:0]
	for _, c := range classes {
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	return out
}

// children compiles the content of an element, normalising whitespace the way a reader expects:
// whitespace between blocks disappears, runs of whitespace inside text become one space.
func (c *compiler) children(n *Node, cx ctx) ([]op, error) {
	return c.childNodes(n.Children, cx)
}

func (c *compiler) childNodes(nodes []*Node, cx ctx) ([]op, error) {
	var out []op
	significant := make([]*Node, 0, len(nodes))
	for _, ch := range nodes {
		significant = append(significant, ch)
	}
	first, last := -1, -1
	for i, ch := range significant {
		if !ch.IsText() || strings.TrimSpace(ch.Text) != "" {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	for i, ch := range significant {
		if ch.IsText() {
			text := ch.Text
			if strings.TrimSpace(text) == "" {
				if cx.inline && !cx.flex && i > first && i < last {
					out = append(out, &staticOp{s: " "})
				}
				continue
			}
			c.measure(cx.paint, false) // words the element around shows: against its ground (contrast.go)
			text = collapseSpace(text)
			if i == first {
				text = strings.TrimLeft(text, " ")
			}
			if i == last {
				text = strings.TrimRight(text, " ")
			}
			in, err := parseInterpolation(text, ch, nil)
			if err != nil {
				return nil, err
			}
			if in.static() {
				out = append(out, &staticOp{s: html.EscapeString(in.literal())})
			} else {
				out = append(out, &textOp{in: in})
			}
			continue
		}
		o, err := c.element(ch, cx, nil)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return mergeStatic(out), nil
}

func collapseSpace(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if !space {
				b.WriteByte(' ')
			}
			space = true
			continue
		}
		space = false
		b.WriteRune(r)
	}
	return b.String()
}

func mergeStatic(ops []op) []op {
	out := ops[:0:0]
	for _, o := range ops {
		if s, ok := o.(*staticOp); ok && len(out) > 0 {
			if prev, ok := out[len(out)-1].(*staticOp); ok {
				out[len(out)-1] = &staticOp{s: prev.s + s.s, items: prev.items + s.items}
				continue
			}
		}
		out = append(out, o)
	}
	return out
}

// fold renders an element that depends on nothing into a static string once, at compile time.
func fold(o *elemOp) op {
	if o.forEach != nil || o.ifExpr != nil || o.showExpr != nil || len(o.bindings) > 0 ||
		o.callerIf != nil || o.callerShow != nil ||
		len(o.dynClasses) > 0 || len(o.dynAttrs) > 0 || o.href != nil || o.selectValue != nil ||
		o.optionValue != nil || o.content != nil || o.crumbLink || o.crumbCurrent || o.breadcrumb ||
		o.fragment || len(o.emptyOps) > 0 {
		return o
	}
	for _, ch := range o.children {
		if _, ok := ch.(*staticOp); !ok {
			return o
		}
	}
	r := &run{}
	o.render(r, map[string]any{})
	return &staticOp{s: r.b.String(), items: r.items}
}

// text: a block of text (<p>), text inside a line (<span>), or a heading (<h1>…<h6>).
func (c *compiler) text(n *Node, cx ctx, site *callSite) (op, error) {
	tag := "p"
	if cx.inline {
		tag = "span"
	}
	if v, ok := n.Attr("strikethrough"); ok && v == "true" {
		tag = "s"
	}
	if v, ok := n.Attr("heading"); ok {
		if !headingLevel(v) { // digits only: heading="+3" would make an <h+3>
			a := attrNamed(n, "heading")
			return nil, errAttr(n, a, "heading là số tĩnh từ 1 đến 6")
		}
		tag = "h" + v
	}
	if v, ok := n.Attr("wrap"); ok && strings.TrimSpace(v) == "false" && tag != "span" {
		// a whole paragraph or heading already stands on its own lines: keeping it together means
		// nothing, and as an inline-block it would only shrink to its words. It is drawn as written.
		a := attrNamed(n, "wrap")
		c.warn(n.File, a.Line, a.Column, "wrap=\"false\" giữ một cụm chữ trong câu liền nhau: đặt trên <text> nằm trong một <text> khác (đoạn văn hay tiêu đề đã đứng riêng dòng nên ở đây không có gì để giữ liền)")
	}
	if cx.breadcrumb && len(n.Elements()) == 0 && site == nil {
		// the last entry of a breadcrumb: the current page, not a link
		o, err := c.textElement(n, cx, site, tag)
		if err != nil {
			return nil, err
		}
		if e, ok := o.(*elemOp); ok {
			e.crumbCurrent = true
		}
		return o, nil
	}
	return c.textElement(n, cx, site, tag)
}

func hasClass(classes []string, name string) bool {
	for _, c := range classes {
		if c == name {
			return true
		}
	}
	return false
}

func attrNamed(n *Node, name string) *Attr {
	for i := range n.Attrs {
		if n.Attrs[i].Name == name {
			return &n.Attrs[i]
		}
	}
	return &Attr{Line: n.Line, Column: n.Column}
}

func (c *compiler) textElement(n *Node, cx ctx, site *callSite, tag string) (op, error) {
	el := &element{tag: n.Tag, html: tag}
	p, err := c.attributes(n, el)
	if err != nil {
		return nil, err
	}
	o := &elemOp{tag: tag}
	frame := c.paintOf(n, el, p, cx, site)
	// a heading's look by its level is reboot.css's (h1 is the heading scale's medium step, semibold,
	// tracked tight, 1.25 high); size written on it is a step of the heading scale (classes.go)
	if el.intentTag() == "heading" {
		c.uses["heading"] = true
	}
	classes := append(append([]string{}, p.classes...), p.show...)
	if site != nil {
		classes = append(classes, site.classes...)
	}
	o.staticClass = dedupe(classes)
	o.dynClasses = p.dynClasses
	o.forEach, o.ifExpr, o.showExpr = p.forEach, p.ifExpr, p.showExpr
	if site != nil {
		mergeCallSite(o, site)
	}
	var attrs strings.Builder
	for _, name := range p.order {
		a := p.semantic[name]
		switch name {
		case "heading", "strikethrough":
		case "alt":
			if err := c.attrValue(n, a, "aria-label", false, o, &attrs); err != nil {
				return nil, err
			}
		default:
			if done, err := c.notHere(n, a, "<text>", ""); err != nil {
				return nil, err
			} else if !done {
				return nil, errAttr(n, a, "thuộc tính %s không có trên <text>", name)
			}
		}
	}
	runtime, err := c.runtimeAttributes(n, p, o, cx.scope)
	if err != nil {
		return nil, err
	}
	if site != nil && site.showBind != "" {
		runtime.binds = append(runtime.binds, site.showBind)
	}
	anchor, err := c.anchorAttribute(n, p, cx)
	if err != nil {
		return nil, err
	}
	o.staticAttrs = anchor + attrs.String()
	childCtx := cx
	childCtx.inline = true
	childCtx.flex = false
	childCtx.breadcrumb = false
	childCtx.paint = frame
	o.children, err = c.children(n, childCtx)
	if err != nil {
		return nil, err
	}
	if err := c.withText(n, o, cx.scope, runtime, cx.aliases); err != nil {
		return nil, err
	}
	return fold(o), nil
}

func removeClass(classes []string, name string) []string {
	out := classes[:0:0]
	for _, c := range classes {
		if c != name {
			out = append(out, c)
		}
	}
	return out
}

// quote: <q> inside a line, <blockquote> as a block (holding one or more text).
func (c *compiler) quote(n *Node, cx ctx, site *callSite) (op, error) {
	tag := "blockquote"
	if cx.inline {
		tag = "q"
	}
	el := &element{tag: "quote", html: tag}
	p, err := c.attributes(n, el)
	if err != nil {
		return nil, err
	}
	o := &elemOp{tag: tag}
	classes := []string{}
	if tag == "blockquote" {
		// a block of text that stacks: its rule on the start edge is reboot.css's
		classes = append(classes, "flex-column")
	}
	classes = append(classes, p.classes...)
	classes = append(classes, p.show...)
	if site != nil {
		classes = append(classes, site.classes...)
	}
	o.staticClass = dedupe(classes)
	o.dynClasses = p.dynClasses
	o.forEach, o.ifExpr, o.showExpr = p.forEach, p.ifExpr, p.showExpr
	if site != nil {
		mergeCallSite(o, site)
	}
	var attrs strings.Builder
	for _, name := range p.order {
		a := p.semantic[name]
		if name != "source" {
			if done, err := c.notHere(n, a, "<quote>", "quote nhận source"); err != nil {
				return nil, err
			} else if !done {
				return nil, errAttr(n, a, "thuộc tính %s không có trên <quote>", name)
			}
			continue
		}
		if err := c.attrValue(n, a, "cite", false, o, &attrs); err != nil {
			return nil, err
		}
	}
	anchor, err := c.anchorAttribute(n, p, cx)
	if err != nil {
		return nil, err
	}
	o.staticAttrs = anchor + attrs.String()
	childCtx := cx
	childCtx.paint = c.paintOf(n, el, p, cx, site)
	childCtx.inline = tag == "q"
	childCtx.flex = tag == "blockquote"
	o.children, err = c.children(n, childCtx)
	if err != nil {
		return nil, err
	}
	return fold(o), nil
}

// accordion: <details> (named by its accordions so only one stays open) whose first child is the
// header, turned into <summary>; the rest is the content. The browser does the opening.
func (c *compiler) accordion(n *Node, cx ctx, site *callSite) (op, error) {
	el := &element{tag: "accordion", html: "details"}
	p, err := c.attributes(n, el)
	if err != nil {
		return nil, err
	}
	o := &elemOp{tag: "details"}
	cx.paint = c.paintOf(n, el, p, cx, site) // the header and the content stand on the accordion
	classes := append(append([]string{}, p.classes...), p.show...)
	if site != nil {
		classes = append(classes, site.classes...)
	}
	o.staticClass = dedupe(classes)
	o.dynClasses = p.dynClasses
	o.forEach, o.ifExpr, o.showExpr = p.forEach, p.ifExpr, p.showExpr
	if site != nil {
		mergeCallSite(o, site)
	}
	var attrs strings.Builder
	if cx.accordionName != "" {
		writeAttr(&attrs, "name", cx.accordionName)
	}
	for _, name := range p.order {
		a := p.semantic[name]
		if name != "open" {
			if done, err := c.notHere(n, a, "<accordion>", "accordion nhận open"); err != nil {
				return nil, err
			} else if !done {
				return nil, errAttr(n, a, "thuộc tính %s không có trên <accordion>", name)
			}
			continue
		}
		if err := c.attrValue(n, a, "open", true, o, &attrs); err != nil {
			return nil, err
		}
	}
	anchor, err := c.anchorAttribute(n, p, cx)
	if err != nil {
		return nil, err
	}
	o.staticAttrs = anchor + attrs.String()

	elements := n.Elements()
	if len(elements) == 0 {
		return nil, errAt(n, "accordion cần phần đầu: con đầu tiên mang click=\"toggle()\"")
	}
	header := elements[0]
	if !hasToggle(header) {
		// the first child is the header whatever it writes: the browser opens <details> itself
		c.warn(header.File, header.Line, header.Column, "phần đầu của accordion (con đầu tiên) cần click=\"toggle()\" (trang vẫn lấy con đầu tiên làm phần đầu)")
	}
	for _, other := range elements[1:] {
		if hasToggle(other) {
			return nil, errAt(other, "click=\"toggle()\" chỉ đặt trên phần đầu (con đầu tiên) của accordion")
		}
	}
	summary, err := c.summary(header, cx)
	if err != nil {
		return nil, err
	}
	rest := make([]*Node, 0, len(n.Children))
	seenHeader := false
	for _, ch := range n.Children {
		if ch == header {
			seenHeader = true
			continue
		}
		if seenHeader {
			rest = append(rest, ch)
		}
	}
	childCtx := cx
	childCtx.inline = false
	childCtx.flex = false
	childCtx.accordionName = ""
	content, err := c.childNodes(rest, childCtx)
	if err != nil {
		return nil, err
	}
	o.children = append([]op{summary}, content...)
	return fold(o), nil
}

func hasToggle(n *Node) bool {
	for _, a := range n.Attrs {
		if a.Name == "click" && strings.Contains(a.Value, "toggle()") {
			return true
		}
	}
	return false
}

// summary compiles the accordion header as <summary>, keeping its layout and look.
func (c *compiler) summary(n *Node, cx ctx) (op, error) {
	info, ok := catalog[n.Tag]
	if !ok || (info.kind != kindContainer && n.Tag != "text") {
		return nil, errAt(n, "phần đầu của accordion là box hoặc text, không phải <%s>", n.Tag)
	}
	if a := attrNamed(n, "component"); a.Name == "component" {
		return nil, errAttr(n, a, "phần đầu của accordion thành <summary> của nó: component đặt trên một thẻ bên trong")
	}
	c.lint(n) // the header is compiled here, never through element()
	// its click="toggle()" is the accordion's own word, left out below (the browser opens <details>):
	// no name of the page or of a component around it, so it is read nowhere (strayNames)
	if err := c.checkReads(n, cx, map[string]bool{"click": true}); err != nil {
		return nil, err
	}
	header := *n
	header.Attrs = nil
	for _, a := range n.Attrs {
		if a.Name == "click" {
			continue // the browser opens <details> itself
		}
		header.Attrs = append(header.Attrs, a)
	}
	el := &element{tag: n.Tag, html: "summary"}
	p, err := c.attributes(&header, el)
	if err != nil {
		return nil, err
	}
	for _, name := range p.order {
		if done, err := c.notHere(n, p.semantic[name], "phần đầu của accordion", "phần đầu thành <summary> của accordion"); err != nil {
			return nil, err
		} else if !done {
			return nil, errAttr(n, p.semantic[name], "thuộc tính %s không có trên phần đầu của accordion", name)
		}
	}
	anchor, err := c.offAnchor(n, p, cx, "phần đầu của accordion (id đặt trên chính accordion)")
	if err != nil {
		return nil, err
	}
	// no marker and a pointer are reboot.css's (summary)
	var classes []string
	if n.Tag != "text" {
		if _, ok := header.Attr("flex"); !ok {
			classes = append(classes, "flex-column")
		}
	}
	classes = append(classes, p.classes...)
	classes = append(classes, p.show...)
	o := &elemOp{tag: "summary", staticClass: dedupe(classes), dynClasses: p.dynClasses, staticAttrs: anchor}
	childCtx := cx
	childCtx.paint = c.paintOf(&header, el, p, cx, nil)
	childCtx.inline = true
	childCtx.flex = isFlex(o.staticClass)
	childCtx.accordionName = ""
	o.children, err = c.children(&header, childCtx)
	if err != nil {
		return nil, err
	}
	return fold(o), nil
}

// withProgressbar checks the page's progressbar and, on a site that moves between pages without a
// reload, gives a page that writes none the one it would have written: <box component="progressbar" />
// at the top.
// The bar belongs to the whole frame, above the header, never inside a region: a page without a
// header, or one that hides it on a phone, still shows that the next page is coming.
func (c *compiler) withProgressbar(page *Node) (*Node, error) {
	// each of these renders as written: a second bar runs with the first on the same kit.progress, a bar
	// written lower still sits on the top edge (fixed), a bar on a site that reloads stays hidden
	var found *Node
	for i, ch := range page.Elements() {
		if !isProgressbar(ch) {
			continue
		}
		if found != nil {
			c.warn(ch.File, ch.Line, ch.Column, "page chỉ có một progressbar (thanh này chạy cùng thanh đầu)")
			continue
		}
		if i > 0 {
			c.warn(ch.File, ch.Line, ch.Column, fmt.Sprintf("progressbar đứng đầu page, trên <%s />: vạch nằm ở mép trên của trang", page.Elements()[0].Tag))
		}
		found = ch
	}
	if found != nil && c.site.Reload {
		c.warn(found.File, found.Line, found.Column, "router.reload(true) tải lại cả trang mỗi lần chuyển nên progressbar không bao giờ chạy; bỏ thẻ này")
	}
	if found != nil || c.site.Reload {
		return page, nil
	}
	with := *page
	bar := &Node{Tag: "box", Bare: true, File: page.File, Line: page.Line, Column: page.Column,
		Attrs: []Attr{{Name: "component", Value: "progressbar", Line: page.Line, Column: page.Column}}}
	with.Children = append([]*Node{bar}, page.Children...)
	return &with, nil
}

// isProgressbar reports whether an element is a call of the progressbar: component="progressbar",
// by its name or its path.
func isProgressbar(n *Node) bool {
	v, ok := n.Attr("component")
	return ok && path.Base(strings.TrimSpace(v)) == "progressbar"
}

// withReader gives a page that writes no <reader /> the one it would have written, on a site that
// moves between pages without a reload: such a move does not tell a screen reader that the page
// changed, so the reader says the new title. A site that reloads every page needs none, but may write
// one to say sentences of its own ($reader.say(…)).
func (c *compiler) withReader(page *Node) (*Node, error) {
	var found *Node
	for _, ch := range page.Elements() {
		if ch.Tag != "reader" {
			continue
		}
		if found != nil {
			// two live regions both render; a screen reader may say a sentence twice
			c.warn(ch.File, ch.Line, ch.Column, "page chỉ có một reader (trình đọc màn hình có thể đọc một câu hai lần)")
			continue
		}
		found = ch
	}
	if found != nil || c.site.Reload {
		return page, nil
	}
	with := *page
	with.Children = append(append([]*Node(nil), page.Children...), &Node{Tag: "reader", Bare: true, File: page.File, Line: page.Line, Column: page.Column})
	return &with, nil
}

// reader is what a screen reader says aloud: an invisible live region whose text is the reader
// component's (kit.reader). Drive says the new page's title through it after every move; a page says
// a sentence of its own through its alias.
func (c *compiler) reader(n *Node, cx ctx) (op, error) {
	if !cx.pageChild {
		// it renders where it is written and works until a soft move replaces the content around it
		c.warn(n.File, n.Line, n.Column, "reader đặt trong page.ui, là con của <page>; không đặt trong vùng hay nội dung (Drive thay nội dung khi chuyển trang nên câu đọc có thể mất)")
	}
	if len(n.SignificantChildren()) > 0 {
		return nil, errAt(n, "reader không có nội dung con; câu cần đọc gửi bằng $reader.say(…)")
	}
	alias := ""
	for i := range n.Attrs {
		a := &n.Attrs[i]
		if a.Name != "as" {
			if done, err := c.kitworkInput(n, a, false, "reader", "reader chỉ nhận as"); err != nil {
				return nil, err
			} else if !done {
				return nil, errAttr(n, a, "reader chỉ nhận as, không nhận %s", a.Name)
			}
			continue
		}
		alias = strings.TrimSpace(a.Value)
		if err := c.registerAlias(n, a, alias, cx, nil); err != nil { // the kernel's reader, always in the browser
			return nil, err
		}
	}
	return &staticOp{s: readerMarkup(alias)}, nil
}

// readerMarkup is the web form of a reader on a Kitwork UI page: the kernel's reader component on an
// invisible live region (seen by a screen reader, not by the eye: 1px and clipped, never
// display:none, which would hide it from the screen reader too). The class reader is reboot.css's, so
// the page carries no style attribute. A template page that moves without a reload gets the template
// form of the same region (render/reader.go).
func readerMarkup(alias string) string {
	host := "reader"
	if alias != "" {
		host += ": " + alias
	}
	return `<div class="reader" data-kitwork-component="` + host + `" data-kitwork-bind="text: text" aria-live="polite" aria-atomic="true"></div>`
}

// controlName is the name a field is read out by when no <label> shows one (a quantity box in a cart
// row): alt becomes aria-label (KITWORK_UI §7, "Tên đọc được").
func controlName(c *compiler, n *Node, alt *Attr, o *elemOp, w *strings.Builder) error {
	if alt == nil {
		return nil
	}
	return c.attrValue(n, alt, "aria-label", false, o, w)
}

// writeHost marks the host of a component the kernel runs: data-kitwork-component="dropdown", or
// with an alias "dropdown: $account" (KITWORK_UI §11). The alias is how the rest of the page reaches
// the instance.
func writeHost(w *strings.Builder, site *callSite) {
	if site == nil || site.def == nil || !site.def.delivered() {
		return
	}
	value := site.def.name
	if site.alias != "" {
		value += ": " + site.alias
	}
	writeAttr(w, "data-kitwork-component", value)
}

// toast is the page's notification stack, <box component="toast" as="$toast" />: an empty live
// region on the bottom edge that the toast component fills when something is said ($toast.show(…)).
// Built empty: nothing has been said yet.
func (c *compiler) toast(n *Node, cx ctx) (op, error) {
	if len(n.SignificantChildren()) > 0 {
		return nil, errAt(n, "toast không có nội dung con; thông báo gửi bằng $toast.show(…)")
	}
	site := &callSite{def: &componentDef{name: "toast", kitwork: kitworkComponents["toast"]}}
	for i := range n.Attrs {
		a := &n.Attrs[i]
		switch a.Name {
		case "component":
		case "as":
			alias := strings.TrimSpace(a.Value)
			if err := c.registerAlias(n, a, alias, cx, site.def); err != nil {
				return nil, err
			}
			site.alias = alias
		default:
			if done, err := c.kitworkInput(n, a, true, "toast", "toast chỉ nhận as; thông báo gửi bằng $toast.show(…)"); err != nil {
				return nil, err
			} else if !done {
				return nil, errAttr(n, a, "toast chỉ nhận as, không nhận %s; thông báo gửi bằng $toast.show(…)", a.Name)
			}
		}
	}
	var b strings.Builder
	b.WriteString(`<div class="toast"`) // the stack on the bottom end corner, reboot.css's
	writeHost(&b, site)
	b.WriteString(` role="status" aria-live="polite"></div>`)
	return &staticOp{s: b.String()}, nil
}

// progressThickness is how thick the line is when page.ui does not say, and the most it may be: a
// line width counts real units (px on the web, points on iOS, dp on Android), as border-width,
// lineWidth and trackThickness do, not the spacing scale. Thickness, not height: the progressbar's
// own box is 0 high so it takes no room, and the word stays right for a bar that is not horizontal.
const (
	progressThickness    = 1
	progressThickestLine = 8
)

// progressDelay is how long a move may take before the bar shows (ms) when page.ui does not say: the
// value Drive itself uses. progressLongestDelay is the longest the house writes: longer, the bar of
// most moves never shows.
const (
	progressDelay        = 120
	progressLongestDelay = 10000
)

// progressbar is the move between pages, <box component="progressbar" color="…" delay="…"
// thickness="…" />: the page's view of the work in flight that Drive reports, through the kernel's
// progressbar component (a page that has one gets no bar of Drive's own). Its UI is Kitwork's, so
// color, thickness, delay and alt are its inputs, fixed when the page is built. It sticks to the top
// edge and takes no room, so the header below it does not move.
func (c *compiler) progressbar(n *Node, cx ctx) (op, error) {
	if !cx.pageChild {
		return nil, errAt(n, "progressbar đặt trong page.ui, ở đầu <page>, trên <header />; không đặt trong vùng hay nội dung")
	}
	if len(n.SignificantChildren()) > 0 {
		return nil, errAt(n, "progressbar không có nội dung con; viết <box component=\"progressbar\" />")
	}
	color := "brand"
	label := "" // the page's language says it unless alt does (progressNameOp)
	delay := "" // Drive's own 120ms unless page.ui says otherwise
	thickness := progressThickness
	for i := range n.Attrs {
		a := &n.Attrs[i]
		switch {
		case a.Name == "component":
		case a.Bare:
			return nil, errAttr(n, a, "%s cần một giá trị", a.Name)
		case strings.Contains(a.Value, "{{"):
			return nil, errAttr(n, a, "%s của progressbar là chữ cố định, không có {{ }}", a.Name)
		case a.Name == "color":
			color = strings.TrimSpace(a.Value)
		case a.Name == "alt":
			label = a.Value
		case a.Name == "thickness":
			// how thick the line is, in px: a bar, not a band. A thicker one is drawn as written, and the
			// house says it is a band (decision D, question 3); no line at all is no number of px.
			px, err := strconv.Atoi(strings.TrimSpace(a.Value))
			if err != nil || px < 1 {
				return nil, errAttr(n, a, "thickness của progressbar là số px từ 1 đến 8 (mặc định 1), không phải %q", a.Value)
			}
			if px > progressThickestLine {
				c.warn(n.File, a.Line, a.Column, fmt.Sprintf("thickness của progressbar là số px từ 1 đến 8 (mặc định 1); %q dày hơn một vạch (trang vẽ đúng %dpx)", a.Value, px))
			}
			thickness = px
		case a.Name == "delay":
			// how long a move may take before the bar shows, in ms: a fast move never flashes it. A longer
			// wait is kept as written, and the house says the bar may then never show; no wait is below 0.
			ms, err := strconv.Atoi(strings.TrimSpace(a.Value))
			if err != nil || ms < 0 {
				return nil, errAttr(n, a, "delay là số mili giây từ 0 đến 10000 (mặc định 120; 0 là hiện ngay), không phải %q", a.Value)
			}
			if ms > progressLongestDelay {
				c.warn(n.File, a.Line, a.Column, fmt.Sprintf("delay là số mili giây từ 0 đến 10000 (mặc định 120; 0 là hiện ngay); %q lâu tới mức thanh gần như không bao giờ hiện (trang giữ đúng %dms)", a.Value, ms))
			}
			if ms != progressDelay {
				delay = strconv.Itoa(ms)
			}
		default:
			if done, err := c.kitworkInput(n, a, true, "progressbar", "progressbar chỉ nhận color, thickness, delay và alt"); err != nil {
				return nil, err
			} else if !done {
				return nil, errAttr(n, a, "progressbar chỉ nhận color (màu của vạch), thickness (độ dày), delay (chờ bao lâu mới hiện) và alt (tên đọc được), không nhận %s", a.Name)
			}
		}
	}
	// the line's thickness and colour are classes of the page's stylesheet like any written attribute.
	// color is the line's background: it is read as the color the author wrote, so a colour the palette
	// does not have is named as written (color="purple"), then made the line's background
	at := attrNamed(n, "color")
	if _, _, err := c.table.Read(nil, stylesheet.Intent{Tag: "progressbar", Name: "color", Value: color}); err != nil {
		return nil, errAttr(n, at, "%s", err.Error())
	}
	bar, err := c.class(n, at, &element{tag: "progressbar", html: "div"}, prefix{keep: true}, "background", color)
	if err != nil {
		return nil, err
	}
	line := []string{c.lineThickness(thickness), bar}
	head, tail := progressbarMarkup(line, delay)
	var name op = &staticOp{s: html.EscapeString(label)}
	if label == "" {
		name = progressNameOp{} // no alt: the name is in the language of the page being rendered
	}
	return &elemOp{fragment: true, children: []op{&staticOp{s: head}, name, &staticOp{s: tail}}}, nil
}

// progressNameOp writes the bar's name when page.ui writes no alt, in the page's language like every
// word the engine writes itself (newTabNote): Vietnamese for vi (vi-VN…) and a page that names none,
// English for any other, as a template page's bar says it (render/progressbar.go).
type progressNameOp struct{}

func (progressNameOp) render(r *run, scope map[string]any) {
	if r.language != "" && !strings.HasPrefix(strings.ToLower(r.language), "vi") {
		r.b.WriteString("Loading the next page")
		return
	}
	r.b.WriteString("Đang chuyển trang")
}

// lineThickness is the class of a progressbar's line of px pixels. One thicker than the house's
// thickest is drawn as written too (its warning is the progressbar's own): the table reads it with
// what it would warn of, and records its rule.
func (c *compiler) lineThickness(px int) string {
	value := strconv.Itoa(px)
	if px <= progressThickestLine {
		return c.engineClass("thickness", value)
	}
	class, _, err := c.table.Read(c.rules, stylesheet.Intent{Name: "thickness", Value: value})
	if err != nil {
		return ""
	}
	return class
}

// progressbarMarkup is the web form of a progressbar on a Kitwork UI page: the kernel's progressbar
// component on a host fixed to the top edge of the window that takes no room (the class progressbar,
// reboot.css's), holding the line whose classes are given (its thickness and colour). The component
// reads kit.progress, which Drive reports a move to, and puts the host under a title bar the page
// draws itself (its top getter). delay is the ms a move may take before the bar shows, "" for Drive's
// own 120. A template page that moves without a reload gets the template form of the same bar
// (render/progressbar.go). It is the markup before the value of aria-label and after it: the name is
// the caller's to write, escaped.
func progressbarMarkup(line []string, delay string) (head, tail string) {
	var b strings.Builder
	// fixed, not sticky: a sticky host cannot leave <body>, so on a short page it could not reach the
	// bottom of a title bar the page draws itself; fixed takes no room and its top getter sets the edge.
	// The directives are the grouped web form (KITWORK_UI.md §11): one attribute per kind, the pairs
	// inside. hidden is the first paint — no move is under way when the page arrives — and show takes
	// it from there.
	b.WriteString(`<div class="progressbar" data-kitwork-component="progressbar"`)
	b.WriteString(` data-kitwork-bind="show: active; aria-valuenow: value" hidden role="progressbar"`)
	if delay != "" {
		writeAttr(&b, "data-kitwork-delay", delay) // read by Drive, which decides when the move has been slow
	}
	b.WriteString(` aria-label="`)
	head = b.String()
	b.Reset()
	b.WriteString(`" aria-valuemin="0" aria-valuemax="100" data-kitwork-style="top: top">`)
	b.WriteString(`<div class="` + strings.Join(line, " ") + `" data-kitwork-style="width: value + '%'"></div></div>`)
	return head, b.String()
}

// breadcrumb: <nav aria-label> + <ol> of items, and the BreadcrumbList data for search engines.
func (c *compiler) breadcrumbElement(n *Node, cx ctx, site *callSite) (op, error) {
	el := &element{tag: "breadcrumb", html: "nav"}
	p, err := c.attributes(n, el)
	if err != nil {
		return nil, err
	}
	anchor, err := c.offAnchor(n, p, cx, "<breadcrumb>")
	if err != nil {
		return nil, err
	}
	label := "Vị trí trang"
	for _, name := range p.order {
		a := p.semantic[name]
		if name != "alt" {
			if done, err := c.notHere(n, a, "<breadcrumb>", "breadcrumb nhận alt"); err != nil {
				return nil, err
			} else if !done {
				return nil, errAttr(n, a, "thuộc tính %s không có trên <breadcrumb>", name)
			}
			continue
		}
		label = a.Value
	}
	for _, ch := range n.Children {
		if ch.IsText() {
			if strings.TrimSpace(ch.Text) != "" {
				return nil, errAt(ch, "chữ không đặt thẳng trong breadcrumb; mỗi mục là một <item>")
			}
			continue
		}
		if ch.Tag != "item" {
			return nil, errAt(ch, "con của breadcrumb chỉ là item; dấu ngăn đặt trong item")
		}
	}
	// the trail wraps and centres its entries across (reboot.css's breadcrumb)
	listClasses := append([]string{"breadcrumb", "flex-row"}, p.classes...)
	childCtx := cx
	childCtx.paint = c.paintOf(n, el, p, cx, site)
	childCtx.breadcrumb = true
	childCtx.flex = true
	items, err := c.childNodes(n.Children, childCtx)
	if err != nil {
		return nil, err
	}
	list := &elemOp{tag: "ol", staticClass: dedupe(listClasses), dynClasses: p.dynClasses, children: items, staticAttrs: ` role="list"`}
	nav := &elemOp{tag: "nav", label: label, breadcrumb: true, children: []op{list}, staticClass: p.show, staticAttrs: anchor}
	nav.forEach, nav.ifExpr, nav.showExpr = p.forEach, p.ifExpr, p.showExpr
	if site != nil {
		mergeCallSite(nav, site)
	}
	return nav, nil
}
