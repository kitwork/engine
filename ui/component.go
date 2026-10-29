package ui

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"

	logic "github.com/kitwork/engine/compiler"
)

// A reusable piece of a site is one of two things (KITWORK_UI §13), and the author's one test is
// whether it has logic:
//
//   - a layout piece has only a .ui (product-card.ui, order-table.ui): no state, no instance. It is
//     called by its tag name, <product-card for="product of products; key: product.id" />, and reads
//     the context where it is called (layout.go);
//   - a component has logic, a component.define("name", …) of the site or the identity or one of
//     Kitwork's own (appearance, dropdown, toast, progressbar, terminal, tabs, network, carousel,
//     countdown). It is written as its tag too: <shop-cart as="$cart" />, <dropdown>…</dropdown>
//     (host.go).
//
// A name with a dash is the site's, one without is Kitwork's (KITWORK_UI §13, 18/10/2026), so the two
// never meet: a file of the site named like a tag of Kitwork is an error. The site's are found by
// their name from the folder of the file that writes them (never from the route, so a line in
// header.ui means the same on every page): the look (<name>.ui) from that folder up to the site root,
// then .components/ of the identity; the logic in components.work or <name>.work at the site root,
// then .components/<name>.work. A path, written source="…" at the call, names one place for both
// halves: ./x or ../x from the writing file, .components/x for the identity.
//
// A component is a scope boundary (KITWORK_UI §17): inside it a bare name is the component's own, and
// only names it does not declare reach the page around it. Without this, cart.ui's {{ total() }}
// would print the page's total() when the page happens to declare one. A layout piece is no boundary.
//
// The engine does not run component.define yet, so the names are read from its source: every name the
// body declares, with its starting value when that value is written out (false, "", [], { … }) and
// nothing otherwise (a function, a value computed from a call). That is the component as it is before
// anything happened, which is what the server prints.

// componentDef is a name as a page resolves it: a component, or a layout piece (a .ui and no logic).
type componentDef struct {
	// name is the last segment of the value: the name on the wire and in messages
	name string
	// ui is its .ui file (absolute), "" when it has none; root is that file's one root element, which
	// is the host: the call writes the same tag, empty
	ui   string
	root *Node
	// logic is the file whose component.define("name", …) it is, "" when it has none of its own
	logic string
	// state is what it declares, with the starting values the source spells out; for a component whose
	// function answers context.bind({ … }), the names of that object alone (component_live.go)
	state map[string]any
	// bound, jobs, vars, inputs and required are what the logic's context.bind({ … }) and
	// context.attributes() say (component_live.go): bound false for a function that answers no bind
	bound    bool
	jobs     map[string]bool
	vars     map[string]bool
	inputs   map[string]any
	required map[string]bool
	// readOnly are its jobs that change nothing but the instance's vars (Page.ReadOnlyJob)
	readOnly map[string]bool
	// kitwork is one of Kitwork's own components (appearance, dropdown, toast, progressbar, terminal)
	kitwork *kitworkComponent
}

// hasUI reports whether the component brings its own look: then the call writes its root tag, empty.
func (d *componentDef) hasUI() bool {
	return d.root != nil || d.kitwork != nil && d.kitwork.root != ""
}

// hostTag is the element of .ui a call of the component builds on: the root of its own UI, Kitwork's
// choice for one of Kitwork's (a button for appearance), a box for a site component without a UI.
func (d *componentDef) hostTag() string {
	switch {
	case d.root != nil:
		return d.root.Tag
	case d.kitwork != nil:
		return d.kitwork.hostTag()
	}
	return "box"
}

// rootTag is the tag the component's own UI is built on, "" for a component without a UI.
func (d *componentDef) rootTag() string {
	if d.root != nil {
		return d.root.Tag
	}
	if d.kitwork != nil {
		return d.kitwork.root
	}
	return ""
}

// instance reports whether the component has names of its own (a .work, or Kitwork's contract): an
// instance is a scope boundary and is what as names.
func (d *componentDef) instance() bool { return d.logic != "" || d.kitwork != nil }

// layout reports whether the name is a layout piece: a .ui with no logic, called by its tag.
func (d *componentDef) layout() bool { return !d.instance() }

// logicPlace says where a component's logic is, for a message: "components.work",
// ".components/cart.work", "component của Kitwork".
func (d *componentDef) logicPlace(s *Site) string {
	if d.logic == "" {
		return "component của Kitwork"
	}
	if r := rel(s, d.logic); !strings.HasPrefix(r, "../") {
		return r
	}
	return ".components/" + filepath.Base(d.logic)
}

// delivered reports whether the browser runs the component: its host carries data-kitwork-component
// and its names are live there. One of Kitwork's is a file of the kernel; a site component whose bind
// holds a job or a var is registered by call.js, its jobs called through the bridge (component_live.go).
// Any other site or identity component's logic does not reach the browser: its host is not marked (the
// kernel would report a component it cannot find), and its names cover the page's on the server.
func (d *componentDef) delivered() bool { return d.kitwork != nil || d.live() }

// names are the names the component declares.
func (d *componentDef) names() map[string]bool {
	out := make(map[string]bool, len(d.state))
	for name := range d.state {
		out[name] = true
	}
	return out
}

// source says where the component comes from, for the rule that one name has one source on a page.
func (d *componentDef) source(s *Site) string {
	switch {
	case d.ui != "" && d.logic != "":
		return rel(s, d.ui) + " + " + rel(s, d.logic)
	case d.ui != "":
		return rel(s, d.ui)
	case d.logic != "":
		return rel(s, d.logic)
	}
	return "kitwork/components/" + d.name
}

// what names where a component's look comes from, for a message: "cart.ui", "toast của Kitwork".
func (d *componentDef) what(s *Site) string {
	if d.ui != "" {
		return rel(s, d.ui)
	}
	if d.kitwork != nil {
		return d.name + " của Kitwork"
	}
	return d.name
}

// componentName reports whether s is a component name: lowercase words joined by '-'.
func componentName(s string) bool {
	if s == "" || s[0] < 'a' || s[0] > 'z' || strings.HasSuffix(s, "-") || strings.Contains(s, "--") {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

// siteName reports whether a tag names a piece of the site: a component name with a dash (shop-cart).
// A name without one is Kitwork's.
func siteName(s string) bool { return componentName(s) && strings.Contains(s, "-") }

// callsComponent reports whether an element as written calls a component — one of Kitwork's tags
// (<tabs>) or a piece of the site with logic (<shop-cart>) — so that what is inside it is that
// component's: a scan of its own tags (the tabs' buttons, a toast's notice) stops there.
func (c *compiler) callsComponent(n *Node) bool {
	if n.IsText() {
		return false
	}
	if kitworkComponents[n.Tag] != nil {
		return true
	}
	if _, known := catalog[n.Tag]; known || !siteName(n.Tag) || len(n.SignificantChildren()) == 0 {
		return false // an empty tag of the site is a layout piece: what it holds is in its file
	}
	def, _ := c.siteLogic(n, n.Tag)
	return def != nil
}

// componentAttribute is a component attribute the author wrote on n, with or without a prefix
// (medium:component), nil for none: since 18/10/2026 every one is an error (componentWritten).
func componentAttribute(n *Node) *Attr {
	for i := range n.Attrs {
		if a := &n.Attrs[i]; a.Name == "component" || strings.HasSuffix(a.Name, ":component") {
			return a
		}
	}
	return nil
}

// rootOf is the tag an element as written builds, as a parent's rule for its children reads it: its own
// for a tag of Kitwork, the box or button one of Kitwork's components builds, the box a component of the
// site written with content builds, and for an empty tag of the site, what the root of its file builds.
// file names the piece, for a message ("" for a tag of Kitwork). A tag that names nothing is its own:
// layoutCall says what is wrong with it.
func (c *compiler) rootOf(n *Node) (root, file string) {
	if k := kitworkComponents[n.Tag]; k != nil {
		return k.hostTag(), ""
	}
	if _, known := catalog[n.Tag]; known || !siteName(n.Tag) {
		return n.Tag, ""
	}
	if len(n.SignificantChildren()) > 0 {
		if def, _ := c.siteLogic(n, n.Tag); def != nil {
			return def.hostTag(), def.name + " (" + def.logicPlace(c.site) + ")"
		}
		return n.Tag, ""
	}
	var def *componentDef
	if a := attrNamed(n, "source"); a.Name == "source" {
		def, _ = c.layoutSource(n, a)
	} else {
		def, _ = c.layoutNamed(n, n.Tag)
	}
	if def == nil || def.root == nil {
		return n.Tag, ""
	}
	if root := def.root; kitworkComponents[root.Tag] != nil || c.callsComponent(root) {
		tag, _ := c.rootOf(root) // a piece whose root is a component: the element that component builds
		return tag, rel(c.site, def.ui)
	}
	return def.root.Tag, rel(c.site, def.ui)
}

// writingDir is the folder of the file that writes n: where the names it writes are looked up from.
func (c *compiler) writingDir(n *Node) string {
	if n.File == "" {
		return c.routeDir
	}
	return filepath.Join(c.site.Root, filepath.FromSlash(path.Dir(n.File)))
}

// identityDir is .components/ of the identity the site belongs to.
func (s *Site) identityDir() string {
	return filepath.Join(filepath.Dir(filepath.Clean(s.Root)), ".components")
}

// resolveComponent finds the component n's component attribute names: one of Kitwork's, or one of the
// site's, by its logic.
func (c *compiler) resolveComponent(n *Node, a *Attr) (*componentDef, error) {
	value, err := pieceValue(n, a, a.Name)
	if err != nil {
		return nil, err
	}
	def, err := c.resolve(n, a, value)
	if err != nil {
		return nil, err
	}
	if def == nil {
		return nil, errAttr(n, a, "không tìm thấy component %s: không có component.define(\"%s\", …) (gốc site: components.work hoặc %s.work, rồi .components/%s.work), và Kitwork không có component tên này",
			value, value, value, value)
	}
	// one name, one source on a page: the browser and Drive know a component by its name. A layout piece
	// is built into the page on the server and never named on the wire, so its tag calls do not come
	// here (layoutNamed): nearer wins.
	source := def.source(c.site)
	if first, ok := c.sources[def.name]; ok && first.source != source {
		return nil, errAttr(n, a, "component %s trên trang này đến từ hai nguồn: %s (%s) và %s; một tên một nguồn trong một trang",
			def.name, first.source, first.at, source)
	} else if !ok {
		c.sources[def.name] = componentSource{source: source, at: fmt.Sprintf("%s:%d:%d", n.File, a.Line, a.Column)}
	}
	return def, nil
}

// pieceValue is the value of a component or source attribute: one static name or path.
func pieceValue(n *Node, a *Attr, word string) (string, error) {
	value := strings.TrimSpace(a.Value)
	switch {
	case strings.Contains(value, "{{"):
		return "", errAttr(n, a, "%s là một tên cố định, không có {{ }}: engine tìm nó khi dựng trang", word)
	case value == "":
		return "", errAttr(n, a, "%s cần tên của file (%s=\"product-card-gon\")", word, word)
	case strings.ContainsAny(value, " ,;\t\n"):
		return "", errAttr(n, a, "%s nhận một tên, không phải %q", word, value)
	}
	return value, nil
}

// resolve finds the component a name written in n names: one of Kitwork's, the logic of one of the site, or
// nil when no component has that name (a layout piece, found by layoutNamed, or nothing).
func (c *compiler) resolve(n *Node, a *Attr, value string) (*componentDef, error) {
	key := "component\x00" + value
	if def, ok := c.components[key]; ok {
		return def, nil
	}
	def, err := c.componentNamed(n, a, value)
	if err != nil || def == nil {
		return nil, err
	}
	if def.logic != "" {
		c.files[def.logic] = true
	}
	c.components[key] = def
	return def, nil
}

// componentSource is where a page first took a component name from.
type componentSource struct {
	source string
	at     string
}

// componentNamed finds a component by its name.
func (c *compiler) componentNamed(n *Node, a *Attr, name string) (*componentDef, error) {
	if !componentName(name) {
		return nil, errAttr(n, a, "tên component viết thường, các từ nối bằng gạch ngang (product-card), không phải %q", name)
	}
	if err := reservedComponentName(n, a, name); err != nil {
		return nil, err
	}
	s := c.site
	root := filepath.Clean(s.Root)
	if kit := kitworkComponents[name]; kit != nil {
		// Kitwork's: a name without a dash is never the site's, so logic of the site under it is never
		// read (its .ui is told by namedLikeATag)
		if kit.planned {
			return nil, errAttr(n, a, "<%s> là component của Kitwork mà engine chưa dựng được", name)
		}
		logicFile, _, err := s.componentLogic(name, filepath.Join(root, "components.work"), filepath.Join(root, name+".work"),
			filepath.Join(s.identityDir(), name+".work"))
		if err != nil {
			return nil, err
		}
		if logicFile != "" {
			return nil, &Error{File: rel(s, logicFile), Line: 1, Column: 1, Message: fmt.Sprintf(
				"component.define(\"%s\", …) đặt tên trùng thẻ <%s> của Kitwork: <%s> luôn là component của Kitwork nên logic này không bao giờ được đọc; tên component của site có gạch ngang (shop-%s)",
				name, name, name, name)}
		}
		return &componentDef{name: name, kitwork: kit, state: kit.state}, nil
	}
	if !siteName(name) {
		return nil, nil // a name without a dash is Kitwork's, and Kitwork has none of it: the caller says so
	}
	// the site's: its logic alone (28/10/2026). Its look is what is written inside its tag, or a layout piece
	// whose root is its tag, found as any layout is (layout.go, componentPiece)
	logicFile, names, err := s.componentLogic(name, filepath.Join(root, "components.work"), filepath.Join(root, name+".work"),
		filepath.Join(s.identityDir(), name+".work"))
	if err != nil || logicFile == "" {
		return nil, err // nothing has this name: the caller says so in its own words
	}
	def := &componentDef{name: name, logic: logicFile}
	def.take(names)
	return def, nil
}

// siteLogic is the component of the site a tag names from where n is written: its logic, nil when the name
// has none (a layout piece, or nothing).
func (c *compiler) siteLogic(n *Node, name string) (*componentDef, error) {
	if !siteName(name) {
		return nil, nil
	}
	return c.resolve(n, &Attr{Name: "component", Value: name, Line: n.Line, Column: n.Column}, name)
}

// reservedComponentName refuses a name that is a tag: the core vocabulary, a region or a Kitwork
// element is written as its tag, never as a component.
func reservedComponentName(n *Node, a *Attr, name string) error {
	switch info, ok := catalog[name]; {
	case ok && info.region:
		return errAttr(n, a, "%s là vùng, không phải component: <%s /> lấy %s.ui, hoặc viết <%s …>nội dung</%s> tại chỗ", name, name, name, name, name)
	case name == "accordion", name == "accordions", name == "breadcrumb", name == "popover", name == "reader", name == "icon", name == "image", plannedElements[name]:
		return errAttr(n, a, "%s là phần tử của Kitwork, không phải component: viết <%s …>…</%s>", name, name, name)
	case ok:
		return errAttr(n, a, "%s là thẻ lõi của Kitwork, không phải component: viết <%s …>", name, name)
	}
	return nil
}

// componentLogic finds the logic of the component <name> in the first of files that defines it: a
// <name>.work holds the component <name> (the name in define is the file's name), components.work
// may hold several. It returns the file and the names the define declares; "" when none defines it.
func (s *Site) componentLogic(name string, files ...string) (string, *logicNames, error) {
	for _, file := range files {
		if !s.exists(file) {
			continue
		}
		source, err := s.read(file)
		if err != nil {
			return "", nil, err
		}
		names, found, err := componentNames(string(source), name)
		if err != nil {
			return "", nil, &Error{File: rel(s, file), Line: 1, Column: 1, Message: err.Error()}
		}
		if found {
			if problem := names.problem; problem != nil && names.live() {
				// a name of a bind the page would draw wrong (component_live.go)
				line, column := lineColumn(string(source), problem.position)
				return "", nil, &Error{File: rel(s, file), Line: line, Column: column, Message: problem.message}
			}
			return file, names, nil
		}
		if filepath.Base(file) != "components.work" {
			return "", nil, &Error{File: rel(s, file), Line: 1, Column: 1,
				Message: fmt.Sprintf("file này không có component.define(\"%s\", …): tên trong define trùng tên file", name)}
		}
	}
	return "", nil, nil
}

// componentNames parses a logic file and returns the names component.define(name, …) declares at the
// top of its body, or binds when it answers context.bind({ … }) (component_live.go). found is false when
// the file defines no component of that name.
func componentNames(source, name string) (*logicNames, bool, error) {
	parser := logic.NewParser(logic.NewLexer(source))
	program := parser.ParseProgram()
	if errs := parser.Errors(); len(errs) > 0 {
		return nil, false, fmt.Errorf("logic của component không đọc được: %s", errs[0])
	}
	for _, statement := range program.Statements {
		fn := defineFunction(statement, name)
		if fn == nil || fn.Body == nil {
			continue
		}
		if names, ok := boundNames(fn); ok {
			names.readOnly = map[string]bool{}
			for _, job := range logic.ReadOnlyJobs(fn) {
				if names.jobs[job] {
					names.readOnly[job] = true
				}
			}
			return names, true, nil
		}
		return &logicNames{state: declaredNames(fn.Body.Statements)}, true, nil
	}
	return nil, false, nil
}

// declaredNames are the names a list of statements declares at its top, with the starting value of
// each when the source spells it out.
func declaredNames(statements []logic.Statement) map[string]any {
	state := map[string]any{}
	declaring := false // the parser reads `var x = 1` as a bare `var` and then `x = 1`
	for _, s := range statements {
		switch s := s.(type) {
		case *logic.VarStatement:
			for _, id := range s.Names {
				state[id.Value] = nil
			}
			if s.DestructMode == logic.DestructNone && len(s.Names) == 1 {
				state[s.Names[0].Value], _ = writtenValue(s.Value)
			}
		case *logic.GroupStatement:
			for name, v := range declaredNames(s.Statements) {
				state[name] = v
			}
		case *logic.ImportStatement:
			for _, spec := range s.Names {
				state[spec.Local] = nil
			}
			if s.Default != nil {
				state[s.Default.Value] = nil
			}
		case *logic.ExpressionStatement:
			if id, ok := s.Expression.(*logic.Identifier); ok && id.Value == "var" {
				declaring = true
				continue
			}
			if assign, ok := s.Expression.(*logic.AssignmentExpression); ok && declaring {
				if id, ok := assign.Name.(*logic.Identifier); ok {
					state[id.Value], _ = writtenValue(assign.Value)
				}
			}
		}
		declaring = false
	}
	return state
}

// defineFunction returns the function of component.define("<name>", () => { … }) when the statement is that
// call.
func defineFunction(statement logic.Statement, name string) *logic.FunctionLiteral {
	expression, ok := statement.(*logic.ExpressionStatement)
	if !ok {
		return nil
	}
	arguments := callArguments(expression.Expression, "component", "define")
	if len(arguments) < 2 {
		return nil
	}
	if literal, ok := arguments[0].(*logic.Literal); !ok || !literal.Value.IsString() || literal.Value.Text() != name {
		return nil
	}
	function, _ := arguments[1].(*logic.FunctionLiteral)
	return function
}

// callArguments returns the arguments of <object>.<method>(…) when e is that call; object "" takes
// any receiver (context.view, context.title(…).view).
func callArguments(e logic.Expression, object, method string) []logic.Expression {
	switch call := e.(type) {
	case *logic.MethodCallExpression:
		if call.Method == nil || call.Method.Value != method || object != "" && !isIdentifier(call.Object, object) {
			return nil
		}
		return call.Arguments
	case *logic.CallExpression:
		member, ok := call.Function.(*logic.MemberExpression)
		if !ok || member.Property == nil || member.Property.Value != method || object != "" && !isIdentifier(member.Object, object) {
			return nil
		}
		return call.Arguments
	}
	return nil
}

func isIdentifier(e logic.Expression, name string) bool {
	id, ok := e.(*logic.Identifier)
	return ok && id.Value == name
}

// writtenValue is the value an expression spells out: a literal, or an array or object of them.
// Anything that has to run (a call, a function, a name) is not known before the component runs.
func writtenValue(e logic.Expression) (any, bool) {
	switch e := e.(type) {
	case *logic.Literal:
		return e.Value.Interface(), true
	case *logic.PrefixExpression:
		if e.Operator == "-" {
			if v, ok := writtenValue(e.Right); ok {
				if n, ok := v.(float64); ok {
					return -n, true
				}
			}
		}
	case *logic.ArrayLiteral:
		out := make([]any, 0, len(e.Elements))
		for _, element := range e.Elements {
			v, ok := writtenValue(element)
			if !ok {
				return nil, false
			}
			out = append(out, v)
		}
		return out, true
	case *logic.ObjectLiteral:
		out := make(map[string]any, len(e.Entries))
		for _, entry := range e.Entries {
			if entry.IsSpread {
				return nil, false
			}
			key := ""
			switch k := entry.Key.(type) {
			case *logic.Identifier:
				key = k.Value
			case *logic.Literal:
				key = k.Value.Text()
			default:
				return nil, false
			}
			v, ok := writtenValue(entry.Value)
			if !ok {
				return nil, false
			}
			out[strings.TrimSpace(key)] = v
		}
		return out, true
	}
	return nil, false
}

// uiReads are the names a .ui (a layout piece's, a component's) reads from where it is called: every
// name its expressions use that is not a loop variable of the file, with what the layout pieces it
// calls read in turn (minus what each call passes in), minus what a component it calls declares. An
// attribute at a call of the piece is an input when it is one of these (or a name the component
// declares).
func (c *compiler) uiReads(def *componentDef, visiting map[string]bool) map[string]bool {
	if def.root == nil {
		return nil
	}
	if cached, ok := c.reads[def.ui]; ok {
		return cached
	}
	if visiting[def.ui] {
		return nil
	}
	visiting[def.ui] = true
	out := map[string]bool{}
	bound := map[string]bool{}
	var walk func(n *Node)
	walk = func(n *Node) {
		if n.IsText() {
			for _, name := range textNames(n) {
				out[name] = true
			}
			return
		}
		if _, known := catalog[n.Tag]; !known && siteName(n.Tag) {
			// a piece of the site it calls: what a layout reads comes from here, except what the call
			// passes; what a component written in place reads, minus what it declares
			if logic, _ := c.siteLogic(n, n.Tag); logic != nil && len(n.SignificantChildren()) > 0 {
				outer := out
				out = map[string]bool{}
				for i := range n.Attrs {
					for _, e := range attrExpressions(n, &n.Attrs[i]) {
						for _, name := range usedNames(e.ir) {
							outer[name] = true
						}
					}
				}
				for _, ch := range n.Children {
					walk(ch)
				}
				for name := range out {
					if _, declared := logic.state[name]; !declared {
						outer[name] = true
					}
				}
				out = outer
				return
			}
			var nested *componentDef
			if a := attrNamed(n, "source"); a.Name == "source" {
				nested, _ = c.layoutSource(n, a)
			} else {
				nested, _ = c.layoutNamed(n, n.Tag)
			}
			if nested != nil && nested.layout() {
				passed := map[string]bool{}
				for _, a := range n.Attrs {
					passed[camel(a.Name)] = true
				}
				for name := range c.uiReads(nested, visiting) {
					if !passed[name] {
						out[name] = true
					}
				}
			}
		}
		for i := range n.Attrs {
			a := &n.Attrs[i]
			if a.Name == "for" {
				if f, err := parseFor(a.Value, n, a); err == nil {
					bound[f.item] = true
					if f.index != "" {
						bound[f.index] = true
					}
				}
			}
			for _, e := range attrExpressions(n, a) {
				for _, name := range usedNames(e.ir) {
					out[name] = true
				}
			}
		}
		for _, ch := range n.Children {
			walk(ch)
		}
	}
	walk(def.root)
	for name := range bound {
		delete(out, name)
	}
	delete(visiting, def.ui)
	c.reads[def.ui] = out
	return out
}

// attrExpressions are the expressions one attribute holds: for its list and key, if, show and an
// event as a whole, any other value its {{ }}. An attribute that does not compile gives none: the
// compile of the element reports it.
func attrExpressions(n *Node, a *Attr) []*expr {
	name := a.Name
	if i := strings.LastIndex(name, ":"); i >= 0 && !isEvent(name) {
		name = name[i+1:]
	}
	switch {
	case a.Bare:
		return nil
	case name == "for":
		f, err := parseFor(a.Value, n, a)
		if err != nil {
			return nil
		}
		out := []*expr{f.list}
		if f.key != nil {
			out = append(out, f.key)
		}
		return out
	case name == "if", name == "show" && !literalBoolean(a.Value), isEvent(a.Name) && !(a.Name == "scroll" && scrollWord(a.Value)):
		e, err := compileExpr(a.Value, n, a)
		if err != nil {
			return nil
		}
		return []*expr{e}
	case strings.Contains(a.Value, "{{"):
		in, err := parseInterpolation(a.Value, n, a)
		if err != nil {
			return nil
		}
		var out []*expr
		for _, p := range in.parts {
			if p.e != nil {
				out = append(out, p.e)
			}
		}
		return out
	}
	return nil
}

// textNames are the names the {{ }} of a run of text read.
func textNames(n *Node) []string {
	if !strings.Contains(n.Text, "{{") {
		return nil
	}
	in, err := parseInterpolation(n.Text, n, nil)
	if err != nil {
		return nil
	}
	var out []string
	for _, p := range in.parts {
		if p.e != nil {
			out = append(out, usedNames(p.e.ir)...)
		}
	}
	return out
}

func literalBoolean(v string) bool {
	v = strings.TrimSpace(v)
	return v == "true" || v == "false"
}

func scrollWord(v string) bool {
	v = strings.TrimSpace(v)
	return v == "x" || v == "y"
}
