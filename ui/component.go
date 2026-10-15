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
//     Kitwork's own (appearance, dropdown, toast, progressbar, terminal). It is written component="name"
//     on a tag: <box component="cart" as="$cart" />, <box component="dropdown">…</box> (host.go).
//
// Both are found by their name from the folder of the file that writes them (never from the route, so
// a line in header.ui means the same on every page): the look (<name>.ui) from that folder up to the
// site root, then .components/ of the identity; the logic in components.work or <name>.work at the
// site root, then .components/<name>.work, then Kitwork's own. A path names one place for both halves:
// ./x or ../x from the writing file, .components/x for the identity, kitwork/components/x for Kitwork.
// Writing one in the other's form is an error that says how to write it.
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
	// state is what it declares, with the starting values the source spells out
	state map[string]any
	// kitwork is one of Kitwork's own components (appearance, dropdown, toast, progressbar, terminal)
	kitwork *kitworkComponent
}

// hasUI reports whether the component brings its own look: then the call writes its root tag, empty.
func (d *componentDef) hasUI() bool {
	return d.root != nil || d.kitwork != nil && d.kitwork.root != ""
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

// delivered reports whether the browser runs the component: the kernel has its file, so its host
// carries data-kitwork-component and its names are live there. A site or identity component's logic
// does not reach the browser until the engine runs component.define (step 4): its host is not marked
// (the kernel would report a component it cannot find), and its names cover the page's on the server.
func (d *componentDef) delivered() bool { return d.kitwork != nil }

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

// resolveComponent finds what the value of n's component attribute names. It may be a layout piece:
// the call reports that (componentCall), since only the call knows how it was written.
func (c *compiler) resolveComponent(n *Node, a *Attr) (*componentDef, error) {
	value, err := pieceValue(n, a, "component")
	if err != nil {
		return nil, err
	}
	def, err := c.resolve(n, a, value)
	if err != nil {
		return nil, err
	}
	if def == nil {
		if strings.Contains(value, "/") {
			return nil, errAttr(n, a, "không tìm thấy component %s ở %s: không có %s.ui hay component.define(\"%s\", …) ở đó",
				path.Base(value), value, path.Base(value), path.Base(value))
		}
		return nil, errAttr(n, a, "không tìm thấy component %s: không có %s.ui (từ thư mục của %s lên gốc site, rồi .components/), không có component.define(\"%s\", …) (gốc site: components.work hoặc %s.work, rồi .components/%s.work), và Kitwork không có component tên này",
			value, value, path.Base(n.File), value, value, value)
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
	case value == "" && word == "component":
		return "", errAttr(n, a, "component cần tên của component (component=\"cart\")")
	case value == "":
		return "", errAttr(n, a, "%s cần tên của file (%s=\"product-card-gon\")", word, word)
	case strings.ContainsAny(value, " ,;\t\n") && word == "component":
		return "", errAttr(n, a, "component nhận một tên, không phải %q: một phần tử một component, component thứ hai đặt trên một thẻ lồng bên trong", value)
	case strings.ContainsAny(value, " ,;\t\n"):
		return "", errAttr(n, a, "%s nhận một tên, không phải %q", word, value)
	}
	return value, nil
}

// resolve finds what a name or a path written in n means from the folder of the file that writes n:
// a component (logic, with or without a .ui), a layout piece (a .ui and no logic), or nil when nothing
// has that name. The .ui of either is parsed; what its root may be is the call's to check.
func (c *compiler) resolve(n *Node, a *Attr, value string) (*componentDef, error) {
	dir := c.writingDir(n)
	key := dir + "\x00" + value
	if def, ok := c.components[key]; ok {
		return def, nil
	}
	var def *componentDef
	var err error
	if strings.Contains(value, "/") {
		def, err = c.componentAtPath(n, a, dir, value)
	} else {
		def, err = c.componentNamed(n, a, dir, value)
	}
	if err != nil || def == nil {
		return nil, err
	}
	if def.ui != "" {
		c.files[def.ui] = true
		nodes, err := c.site.parseFile(def.ui)
		if err != nil {
			return nil, err
		}
		if def.root, err = singleRoot(nodes, rel(c.site, def.ui)); err != nil {
			return nil, err
		}
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

// checkComponentRoot checks the root of a component's .ui: it is the host, so it carries neither
// the call's words (if, for: whether and how often there is a host is the call's) nor a component
// of its own (one element, one instance), and it is not a region (a region is the page's).
func checkComponentRoot(def *componentDef) error {
	root := def.root
	for i := range root.Attrs {
		a := &root.Attrs[i]
		switch a.Name {
		case "if", "for":
			return errAttr(root, a, "gốc của %s không mang %s: có thẻ chủ hay không, lặp mấy lần là việc của chỗ gọi (<%s component=\"%s\" %s=\"…\" />)",
				path.Base(root.File), a.Name, root.Tag, def.name, a.Name)
		case "component":
			return errAttr(root, a, "gốc của %s đã là thẻ chủ của %s: một phần tử một component; đặt %s trên một thẻ bên trong",
				path.Base(root.File), def.name, strings.TrimSpace(a.Value))
		case "as":
			return errAttr(root, a, "as đặt ở chỗ gọi (<%s component=\"%s\" as=\"…\" />), không trên gốc của %s", root.Tag, def.name, path.Base(root.File))
		}
	}
	if _, ok := catalog[root.Tag]; !ok {
		return errAt(root, "gốc của %s là <%s>, không phải thẻ của Kitwork: thẻ chủ của component là một thẻ như box, card, list; bọc <%s /> trong thẻ đó",
			path.Base(root.File), root.Tag, root.Tag)
	}
	if info, ok := catalog[root.Tag]; ok && info.region {
		return errAt(root, "gốc của %s là vùng <%s>: vùng là của trang (<%s /> lấy %s.ui); component có giao diện dựng trên một thẻ như box, card, list",
			path.Base(root.File), root.Tag, root.Tag, root.Tag)
	}
	if reason := forbiddenHost(root.Tag); reason != "" {
		return errAt(root, "gốc của %s là <%s>, mà %s", path.Base(root.File), root.Tag, reason)
	}
	return nil
}

// componentNamed finds a component by its name from the folder dir.
func (c *compiler) componentNamed(n *Node, a *Attr, dir, name string) (*componentDef, error) {
	if !componentName(name) {
		return nil, errAttr(n, a, "tên component viết thường, các từ nối bằng gạch ngang (product-card), không phải %q", name)
	}
	if err := reservedComponentName(n, a, name); err != nil {
		return nil, err
	}
	s := c.site
	root := filepath.Clean(s.Root)

	// a .ui nearer than the site root: a branch's own look
	branch := ""
	for _, folder := range s.upward(dir) {
		if folder == root || !strings.HasPrefix(folder, root+string(filepath.Separator)) {
			break
		}
		if candidate := filepath.Join(folder, name+".ui"); s.exists(candidate) {
			branch = candidate
			if err := strayLogic(s, candidate, name); err != nil {
				return nil, err
			}
			break
		}
	}

	def := &componentDef{name: name}
	logicFile, state, err := s.componentLogic(name, filepath.Join(root, "components.work"), filepath.Join(root, name+".work"),
		filepath.Join(s.identityDir(), name+".work"))
	if err != nil {
		return nil, err
	}
	tierUI := ""
	for _, candidate := range []string{filepath.Join(root, name+".ui"), filepath.Join(s.identityDir(), name+".ui")} {
		if s.exists(candidate) {
			tierUI = candidate
			break
		}
	}
	kit := kitworkComponents[name]
	switch {
	case logicFile != "":
		// a site or identity .ui pairs with site or identity logic
		def.logic, def.state, def.ui = logicFile, state, tierUI
	case tierUI != "":
		if kit != nil && kit.root != "" {
			return nil, &Error{File: rel(s, tierUI), Line: 1, Column: 1, Message: fmt.Sprintf(
				"%s.ui đổi giao diện %s của Kitwork: engine chưa dựng %s từ file của site; bỏ file này hoặc đặt tên khác", name, name, name)}
		}
		// a .ui named like one of Kitwork's components without a UI never pairs with it: it is a piece
		// of the site's own, and the nearer name wins
		def.ui = tierUI
	case kit != nil && !kit.planned:
		def.kitwork, def.state = kit, kit.state
	case kit != nil:
		if branch == "" {
			return nil, errAttr(n, a, "%s là component của Kitwork mà engine chưa dựng được", name)
		}
	}
	if branch != "" {
		if def.instance() && def.ui == "" && (def.kitwork == nil || def.kitwork.root == "") {
			// whether a component has a look is fixed where it is defined, not by a branch
			defined := "của Kitwork"
			if def.logic != "" {
				defined = "ở " + rel(s, def.logic)
			}
			return nil, &Error{File: rel(s, branch), Line: 1, Column: 1, Message: fmt.Sprintf(
				"%s là component không có giao diện (%s): nội dung bên trong thẻ chủ là giao diện của nó, nên %s ở nhánh không cho nó giao diện; chỗ gọi %s:%d:%d sẽ hỏng. Đặt tên khác cho file này",
				name, defined, filepath.Base(branch), n.File, a.Line, a.Column)}
		}
		if def.kitwork != nil {
			return nil, &Error{File: rel(s, branch), Line: 1, Column: 1, Message: fmt.Sprintf(
				"%s.ui đổi giao diện %s của Kitwork: engine chưa dựng %s từ file của site; bỏ file này hoặc đặt tên khác", name, name, name)}
		}
		def.ui = branch
	}
	if def.ui == "" && !def.instance() {
		return nil, nil // nothing has this name: the caller says so in its own words
	}
	return def, nil
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

// componentAtPath finds the component a path names: one place for both halves.
func (c *compiler) componentAtPath(n *Node, a *Attr, dir, value string) (*componentDef, error) {
	s := c.site
	root := filepath.Clean(s.Root)
	segments := strings.Split(value, "/")
	name := segments[len(segments)-1]
	if !componentName(name) {
		return nil, errAttr(n, a, "đường dẫn component kết thúc bằng tên component (viết thường, nối bằng gạch ngang), không phải %q", name)
	}
	if err := reservedComponentName(n, a, name); err != nil {
		return nil, err
	}
	def := &componentDef{name: name}
	switch {
	case strings.HasPrefix(value, "kitwork/components/") && len(segments) == 3:
		kit := kitworkComponents[name]
		if kit == nil {
			return nil, errAttr(n, a, "Kitwork không có component %s", name)
		}
		if kit.planned {
			return nil, errAttr(n, a, "%s là component của Kitwork mà engine chưa dựng được", name)
		}
		def.kitwork, def.state = kit, kit.state
		return def, nil
	case strings.HasPrefix(value, ".components/") && len(segments) == 2:
		identity := s.identityDir()
		var err error
		def.logic, def.state, err = s.componentLogic(name, filepath.Join(identity, name+".work"))
		if err != nil {
			return nil, err
		}
		if candidate := filepath.Join(identity, name+".ui"); s.exists(candidate) {
			def.ui = candidate
		}
	case segments[0] == "." || segments[0] == "..":
		for _, segment := range segments[:len(segments)-1] {
			if segment != "." && segment != ".." && !componentName(segment) {
				return nil, errAttr(n, a, "đường dẫn component chỉ có tên thư mục viết thường, . và .., không có %q", segment)
			}
		}
		target := filepath.Clean(filepath.Join(dir, filepath.FromSlash(value)))
		folder := filepath.Dir(target)
		if folder != root && !strings.HasPrefix(folder, root+string(filepath.Separator)) {
			return nil, errAttr(n, a, "đường dẫn %s ra ngoài site; component của identity viết .components/%s", value, name)
		}
		if candidate := target + ".ui"; s.exists(candidate) {
			def.ui = candidate
		}
		if folder == root {
			// logic is defined at the site root only: a path to the root takes the root's logic
			var err error
			def.logic, def.state, err = s.componentLogic(name, filepath.Join(root, "components.work"), filepath.Join(root, name+".work"))
			if err != nil {
				return nil, err
			}
		}
	default:
		return nil, errAttr(n, a, "đường dẫn component bắt đầu bằng ./ hoặc ../ (từ file đang viết), .components/ (của identity) hoặc kitwork/components/ (của Kitwork), không phải %q", value)
	}
	if def.ui == "" && def.logic == "" {
		return nil, nil // nothing at that place: the caller says so in its own words
	}
	return def, nil
}

// componentLogic finds the logic of the component <name> in the first of files that defines it: a
// <name>.work holds the component <name> (the name in define is the file's name), components.work
// may hold several. It returns the file and the names the define declares; "" when none defines it.
func (s *Site) componentLogic(name string, files ...string) (string, map[string]any, error) {
	for _, file := range files {
		if !s.exists(file) {
			continue
		}
		source, err := s.read(file)
		if err != nil {
			return "", nil, err
		}
		state, found, err := componentNames(string(source), name)
		if err != nil {
			return "", nil, &Error{File: rel(s, file), Line: 1, Column: 1, Message: err.Error()}
		}
		if found {
			return file, state, nil
		}
		if filepath.Base(file) != "components.work" {
			return "", nil, &Error{File: rel(s, file), Line: 1, Column: 1,
				Message: fmt.Sprintf("file này không có component.define(\"%s\", …): tên trong define trùng tên file", name)}
		}
	}
	return "", nil, nil
}

// componentNames parses a logic file and returns the names component.define(name, …) declares at the
// top of its body. found is false when the file defines no component of that name.
func componentNames(source, name string) (map[string]any, bool, error) {
	parser := logic.NewParser(logic.NewLexer(source))
	program := parser.ParseProgram()
	if errs := parser.Errors(); len(errs) > 0 {
		return nil, false, fmt.Errorf("logic của component không đọc được: %s", errs[0])
	}
	for _, statement := range program.Statements {
		body := defineBody(statement, name)
		if body == nil {
			continue
		}
		return declaredNames(body.Statements), true, nil
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

// defineBody returns the body of component.define("<name>", () => { … }) when the statement is that call.
func defineBody(statement logic.Statement, name string) *logic.BlockStatement {
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
	if function, ok := arguments[1].(*logic.FunctionLiteral); ok {
		return function.Body
	}
	return nil
}

// callArguments returns the arguments of <object>.<method>(…) when e is that call; object "" takes
// any receiver (router.view, router.reload(false).view).
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

// viewNames are the names the route's view gives the page: what its router.work declares at the top
// (its imports among them) and what router.view's function declares. The compiler reads them to
// warn of a name that a component and the view both declare where markup cannot tell which one it
// reads (the component's wins). A router.work it cannot read gives none (the engine reports it where
// the route runs).
func (s *Site) viewNames(routeDir string) (map[string]bool, string) {
	file := filepath.Join(routeDir, "router.work")
	if !s.exists(file) {
		return nil, ""
	}
	source, err := s.read(file)
	if err != nil {
		return nil, ""
	}
	parser := logic.NewParser(logic.NewLexer(string(source)))
	program := parser.ParseProgram()
	if len(parser.Errors()) > 0 {
		return nil, file
	}
	names := map[string]bool{}
	for name := range declaredNames(program.Statements) {
		names[name] = true
	}
	for _, statement := range program.Statements {
		expression, ok := statement.(*logic.ExpressionStatement)
		if !ok {
			continue
		}
		for _, argument := range callArguments(expression.Expression, "", "view") {
			if function, ok := argument.(*logic.FunctionLiteral); ok && function.Body != nil {
				for name := range declaredNames(function.Body.Statements) {
					names[name] = true
				}
			}
		}
	}
	return names, file
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
		if _, known := catalog[n.Tag]; !known {
			// a layout piece it calls: what that file reads comes from here, except what the call passes
			var nested *componentDef
			if a := attrNamed(n, "source"); a.Name == "source" {
				nested, _ = c.layoutSource(n, a)
			} else if componentName(n.Tag) && !plannedElements[n.Tag] {
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
			if a.Name == "component" {
				if nested, err := c.resolveComponent(n, a); err == nil {
					inner := c.uiReads(nested, visiting)
					for name := range inner {
						if _, declared := nested.state[name]; !declared {
							out[name] = true
						}
					}
				}
				continue
			}
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
