package ui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	logic "github.com/kitwork/engine/compiler"
)

// What a page is given (KITWORK_UI §4, 24/10/2026): content.ui, the frame view.ui around it and the regions
// that frame calls read exactly the names of the object its route's context.view({ … }) is given; a name
// read there that nothing on the page declares and the route does not give is the compile error "router.get
// không trả về x", at the read. notfound.ui and its frame are given nothing (context.notFound(), an address no
// folder answers), error.ui and its frame only message (context.error(…), work/router_ui.go respondUI). A
// read of a name the server and the browser give every page is no such name: a format helper (money, date,
// initials), a global of the expressions (Math, JSON…), undefined, NaN and Infinity. Nor is a name a
// component of the page declares: outside its host strayNames tells it, inside another component it is left.
//
// What a route gives is read from its router.work as it is written (viewNames): when the compiler cannot see
// it, it says nothing. Only in a Kitwork UI site (Site.RouterWork) is a folder with no router file known to
// give its page nothing, and notfound.ui and error.ui known at all; elsewhere (a test's page and its Scope, a
// folder with a template's router.kitwork.js) a page with no router.work of its own is told nothing.

// The pages a route folder draws (work/router_ui.go uiOwnPage, uiNotFoundPage, uiErrorPage).
const (
	pageContent  = ""
	pageNotFound = "notfound"
	pageError    = "error"
)

// routerFile is the router file of a Kitwork UI folder; templateRouterFile a template site's.
const (
	routerFile         = "router.work"
	templateRouterFile = "router.kitwork.js"
)

// routeView is what a folder's router.work gives its page, as the compiler reads it (viewNames).
type routeView struct {
	// names are the keys of every object a .view({ … }) of the file is handed; nil when that is not known here
	names map[string]bool
	// file is the router.work read, "" when the folder has none
	file string
	// get says the file declares router.get; views is how many .view(…) it calls with an object or a value
	get   bool
	views int
	// view says the file declares router.view (28/10/2026); jobs are the names of its bind that are functions,
	// called through the bridge, and vars the vars at the top of its function (view_bridge.go)
	view bool
	jobs map[string]bool
	vars map[string]bool
}

// pageGiven is what one page is given: the route's view for content.ui, nothing for notfound.ui, message for
// error.ui; names nil when that is not known here.
type pageGiven struct {
	routeView
	page string
	// routerless: a folder of a Kitwork UI site with no router file at all, which gives its page nothing
	routerless bool
}

// given is what the page of routeDir is given, page being which page it is (pageContent, pageNotFound,
// pageError).
func (s *Site) given(routeDir, page string) pageGiven {
	g := pageGiven{routeView: s.viewNames(routeDir), page: page}
	switch page {
	case pageNotFound:
		g.names = nil
		if s.RouterWork {
			g.names = map[string]bool{}
		}
	case pageError:
		g.names = nil
		if s.RouterWork {
			g.names = map[string]bool{"message": true}
		}
	default:
		if g.file == "" && s.RouterWork && !s.exists(filepath.Join(routeDir, templateRouterFile)) {
			g.names, g.routerless = map[string]bool{}, true
		}
	}
	return g
}

// literalNames are words an expression reads like names that are values of the language, given by nothing:
// the server and the kernel read them where they stand.
var literalNames = map[string]bool{"undefined": true, "NaN": true, "Infinity": true}

// viewNames are the names the route's view gives the page: the keys of every object its router.work hands
// a .view({ … }), in any handler or function of the file (23/10/2026: the page reads exactly those; router.view,
// whose function's and file's names the page read, is gone). The compiler reads them to tell a name the page
// reads and is not given (notGiven), to warn of a name that a component and the view both declare where markup
// cannot tell which one it reads (the component's wins), and to tell a name read outside the component that
// declares it (strayNames).
//
// When the file does not say for certain what the page is given, the names are not known (nil) and nothing
// is told: a router.work it cannot read (the engine reports it where the route runs); a .view(…) handed
// anything but one object written there whose keys are plain names (.view(data), .view({ ...data }),
// .view({ [key]: x }), .view({ a }, { b })); view read off anything without being called there (const show =
// context.view, const { view } = context, context["view"]); code of another file of the app, a name the file
// imports, what it gave or what a global or Kitwork made of it (kept or not: Object.assign({}, { page })),
// called with an argument (page(context), makePage()(context), const handle = makePage(); handle(context),
// const handlers = { page }; handlers.page(context)), handed to a router
// line or a function of the file (router.get(page), router.get(makePage()), run(makePage(), context)), or read
// where the reading does not follow it (returned: const pick = () => page; assigned): that code may call
// .view itself. Called with nothing (listNavigation()), handed to Kitwork (richtext(sample())), to a global, or
// to what the engine handed the file (context.view({ navigation, items: list() })), it is data. The compiler of
// router.work refuses most of the rest already (compiler/router_rules.go); this reading stays as careful where
// it does not.
func (s *Site) viewNames(routeDir string) routeView {
	file := filepath.Join(routeDir, routerFile)
	if !s.exists(file) {
		return routeView{}
	}
	view := routeView{file: file}
	source, err := s.read(file)
	if err != nil {
		return view
	}
	parser := logic.NewParser(logic.NewLexer(string(source)))
	program := parser.ParseProgram()
	if len(parser.Errors()) > 0 {
		return view
	}
	// imported are the names bound to code of another file of the app (an import of "kitwork" is no such
	// statement: the parser lowers it to a const)
	imported := map[string]bool{}
	for _, statement := range program.Statements {
		if module, ok := statement.(*logic.ImportStatement); ok {
			for _, spec := range module.Names {
				imported[spec.Local] = true
			}
			if module.Default != nil {
				imported[module.Default.Value] = true
			}
		}
		if word := routerLineWord(statement); word == "get" || word == "view" {
			view.get = true
			if word == "view" {
				view.view = true
			}
		}
	}
	// kitwork are the names an import of Kitwork binds; engine the parameters of the file's functions (the
	// context a handler is handed, a row of a list), unless the file also declares the name (a function of the
	// file may hold it), and what the file reads off them (derived: const request = context.request); foreign
	// what code of another file gave (const handle = makePage(), and what keeps it)
	kitwork, parameters, declared := map[string]bool{}, map[string]bool{}, map[string]bool{}
	derived, foreign := map[string]bool{}, map[string]bool{}
	eachNode(program.Statements, func(node logic.Node) {
		switch n := node.(type) {
		case *logic.VarStatement:
			for _, name := range n.Names {
				if n.Import != logic.ImportNone {
					kitwork[name.Value] = true
				} else {
					declared[name.Value] = true
				}
			}
		case *logic.FunctionLiteral:
			for _, parameter := range n.Parameters {
				parameters[parameter.Value] = true
			}
		}
	})
	engine := func(name string) bool { return derived[name] || parameters[name] && !declared[name] }
	// fromAnotherFile reports a value that may be code of another file: a name it imports, what keeps one
	// (foreign), and what a global or Kitwork makes of one, which may hand it back (Object.assign({}, { page }),
	// Object.values(helpers)[0])
	var fromAnotherFile func(e logic.Expression) bool
	givesBack := func(callee logic.Expression, arguments []logic.Expression) bool {
		if root := calleeRoot(callee); !scriptGlobals[root] && !kitwork[root] {
			return false
		}
		for _, argument := range arguments {
			if fromAnotherFile(argument) {
				return true
			}
		}
		return false
	}
	fromAnotherFile = func(e logic.Expression) bool {
		return flowsFrom(e, true, func(name *logic.Identifier) bool { return imported[name.Value] || foreign[name.Value] }, givesBack)
	}
	for changed := true; changed; {
		changed = false
		eachNode(program.Statements, func(node logic.Node) {
			n, ok := node.(*logic.VarStatement)
			if !ok || n.Import != logic.ImportNone {
				return
			}
			bound := foreign
			if !fromAnotherFile(n.Value) {
				if !rootedIn(n.Value, false, engine) {
					return
				}
				bound = derived
			}
			for _, name := range n.Names {
				if !bound[name.Value] {
					bound[name.Value], changed = true, true
				}
			}
		})
	}
	// handsOn reports a call that hands what another file gave to code that may call it with the context: a
	// router line (router.get(makePage())), a function of the file (run(makePage(), context)); handed to Kitwork
	// (richtext(sample())), to a global, or to what the engine handed (context.view({ items: list() })), it is
	// data
	handsOn := func(callee logic.Expression, arguments []logic.Expression) bool {
		root := calleeRoot(callee)
		for _, argument := range arguments {
			if !fromAnotherFile(argument) {
				continue
			}
			if root == "" || root == "router" || foreign[root] || !kitwork[root] && !scriptGlobals[root] && !engine(root) {
				return true
			}
		}
		return false
	}
	// followed are the names whose value the reading goes after where it stands: what a const keeps (foreign),
	// what a call calls (called with an argument, or with nothing) and is handed (handsOn); and the names whose
	// value goes nowhere, used up where it is read (a condition, the operand of !, typeof or -, a key read, a
	// part of a template literal). A name imported from another file read anywhere else (returned, a handler's
	// body, assigned) goes where the reading does not follow it
	followed := map[*logic.Identifier]bool{}
	follow := func(e logic.Expression) {
		flowsFrom(e, true, func(name *logic.Identifier) bool {
			followed[name] = true
			return false
		}, nil)
	}
	eachNode(program.Statements, func(node logic.Node) {
		switch n := node.(type) {
		case *logic.VarStatement:
			if n.Import == logic.ImportNone {
				follow(n.Value)
			}
		case *logic.CallExpression:
			follow(n.Function)
			for _, argument := range n.Arguments {
				follow(argument)
			}
		case *logic.MethodCallExpression:
			follow(n.Object)
			for _, argument := range n.Arguments {
				follow(argument)
			}
		case *logic.PrefixExpression:
			follow(n.Right)
		case *logic.TernaryExpression:
			follow(n.Condition)
		case *logic.IfExpression:
			follow(n.Condition)
		case *logic.IndexExpression:
			follow(n.Index)
		case *logic.TemplateLiteral:
			for _, part := range n.Parts {
				follow(part)
			}
		}
	})
	names, known := map[string]bool{}, true
	// called are the .view members read as what a call calls: (context.view)({ … })
	called := map[logic.Node]bool{}
	read := func(arguments []logic.Expression) {
		if len(arguments) == 0 {
			return // .view() gives the page nothing
		}
		view.views++
		object, ok := arguments[0].(*logic.ObjectLiteral)
		if !ok || len(arguments) > 1 {
			// a value, or a second object (the engine draws the page with the last object it is handed,
			// work/view.go; the compiler of router.work refuses it)
			known = false
			return
		}
		for _, entry := range object.Entries {
			switch key := entry.Key.(type) {
			case *logic.Identifier:
				if !entry.IsSpread {
					names[key.Value] = true
					continue
				}
			case *logic.Literal:
				if !entry.IsSpread && key.Value.IsString() {
					names[key.Value.Text()] = true
					continue
				}
			}
			known = false
		}
	}
	eachNode(program.Statements, func(node logic.Node) {
		switch n := node.(type) {
		case *logic.VarStatement:
			if n.Import != logic.ImportNone {
				for _, name := range n.Names {
					if name.Value == "view" {
						known = false // import { view } from "kitwork/router"
					}
				}
			}
			if patternReads(n.Pattern, "view") {
				known = false
			}
		case *logic.CallExpression:
			if len(n.Arguments) > 0 && fromAnotherFile(n.Function) || handsOn(n.Function, n.Arguments) {
				known = false // makePage()(context), handle(context), router.get(makePage())
			}
			if member := viewMember(n.Function); member != nil && !isIdentifier(member.Object, "router") {
				called[member] = true
				read(n.Arguments)
			}
		case *logic.MethodCallExpression:
			if len(n.Arguments) > 0 && fromAnotherFile(n.Object) || handsOn(n.Object, n.Arguments) {
				known = false // helpers.page(context), router.get(handlers.page)
			}
			// .view({ … }) answers a router.get, .bind({ … }) a router.view (28/10/2026); router.view(fn) is
			// the door itself, not an answer
			if n.Method != nil && (n.Method.Value == "view" || n.Method.Value == "bind") && !isIdentifier(n.Object, "router") {
				read(n.Arguments)
			}
		case *logic.MemberExpression:
			if n.Property != nil && n.Property.Value == "view" && !called[n] && !isIdentifier(n.Object, "router") {
				known = false
			}
		case *logic.IndexExpression:
			if literal, ok := n.Index.(*logic.Literal); ok && literal.Value.IsString() && literal.Value.Text() == "view" {
				known = false
			}
		case *logic.Identifier:
			if imported[n.Value] && !followed[n] {
				known = false // const pick = () => page, handle = page
			}
		}
	})
	if known {
		view.names = names
	}
	if view.view {
		view.jobs, view.vars = viewBridgeNames(program.Statements)
	}
	return view
}

// rootedIn reports whether a value comes from a name of the set: the name, what is read or called off it
// (handle.page, makePage(), list().length), either side of a choice (a ? b : c, a || b), and, with literals, an
// object or a list that holds one ({ page: makePage() }).
func rootedIn(e logic.Expression, literals bool, set func(string) bool) bool {
	return flowsFrom(e, literals, func(name *logic.Identifier) bool { return set(name.Value) }, nil)
}

// flowsFrom visits the names a value comes from, as rootedIn reads them, until visit answers true, and
// reports whether it did; through, when not nil, is asked at each call on the way (its callee and arguments)
// and answers true for a call that gives back what it is handed.
func flowsFrom(e logic.Expression, literals bool, visit func(*logic.Identifier) bool, through func(logic.Expression, []logic.Expression) bool) bool {
	switch node := e.(type) {
	case *logic.Identifier:
		return visit(node)
	case *logic.CallExpression:
		return through != nil && through(node.Function, node.Arguments) || flowsFrom(node.Function, literals, visit, through)
	case *logic.MethodCallExpression:
		return through != nil && through(node.Object, node.Arguments) || flowsFrom(node.Object, literals, visit, through)
	case *logic.MemberExpression:
		return flowsFrom(node.Object, literals, visit, through)
	case *logic.IndexExpression:
		return flowsFrom(node.Left, literals, visit, through)
	case *logic.ChainExpression:
		return flowsFrom(node.Expression, literals, visit, through)
	case *logic.TernaryExpression:
		return flowsFrom(node.Consequence, literals, visit, through) || flowsFrom(node.Alternative, literals, visit, through)
	case *logic.InfixExpression:
		return flowsFrom(node.Left, literals, visit, through) || flowsFrom(node.Right, literals, visit, through)
	case *logic.SpreadExpression:
		return literals && flowsFrom(node.Value, literals, visit, through)
	case *logic.ObjectLiteral:
		if literals {
			for _, entry := range node.Entries {
				if flowsFrom(entry.Value, literals, visit, through) {
					return true
				}
			}
		}
	case *logic.ArrayLiteral:
		if literals {
			for _, element := range node.Elements {
				if flowsFrom(element, literals, visit, through) {
					return true
				}
			}
		}
	}
	return false
}

// calleeRoot is the name a call is made through: run of run(…), context of context.title("…").view(…), router of
// router.get(…).guard(…); "" for anything else (a function written there).
func calleeRoot(e logic.Expression) string {
	switch node := e.(type) {
	case *logic.Identifier:
		return node.Value
	case *logic.CallExpression:
		return calleeRoot(node.Function)
	case *logic.MethodCallExpression:
		return calleeRoot(node.Object)
	case *logic.MemberExpression:
		return calleeRoot(node.Object)
	case *logic.IndexExpression:
		return calleeRoot(node.Left)
	case *logic.ChainExpression:
		return calleeRoot(node.Expression)
	}
	return ""
}

// scriptGlobals are the globals of a .work file that take a value without running code of the app on it.
var scriptGlobals = map[string]bool{
	"safe": true, "Math": true, "JSON": true, "Date": true, "Number": true, "String": true, "Boolean": true,
	"Array": true, "Object": true, "parseInt": true, "parseFloat": true, "encodeURIComponent": true,
	"decodeURIComponent": true, "isNaN": true, "isFinite": true,
}

// viewMember is the .view a call calls when what it calls is one ((context.view)(…)), nil otherwise.
func viewMember(e logic.Expression) *logic.MemberExpression {
	if chain, ok := e.(*logic.ChainExpression); ok {
		e = chain.Expression
	}
	if member, ok := e.(*logic.MemberExpression); ok && member.Property != nil && member.Property.Value == "view" {
		return member
	}
	return nil
}

// patternReads reports whether a destructuring pattern reads key, nested patterns included.
func patternReads(pattern *logic.BindingPattern, key string) bool {
	if pattern == nil {
		return false
	}
	for _, element := range pattern.Elements {
		if !pattern.Array && element.Key == key || patternReads(element.Pattern, key) {
			return true
		}
	}
	return false
}

// routerLineWord is the word a line of router.work begins with, router.<word>(…) and whatever is chained after
// it; "" for any other statement.
func routerLineWord(statement logic.Statement) string {
	line, ok := statement.(*logic.ExpressionStatement)
	if !ok {
		return ""
	}
	current := line.Expression
	for {
		switch node := current.(type) {
		case *logic.ChainExpression:
			current = node.Expression
		case *logic.MethodCallExpression:
			if isIdentifier(node.Object, "router") && node.Method != nil {
				return node.Method.Value
			}
			current = node.Object
		case *logic.CallExpression:
			if member, ok := node.Function.(*logic.MemberExpression); ok && isIdentifier(member.Object, "router") && member.Property != nil {
				return member.Property.Value
			}
			current = node.Function
		case *logic.MemberExpression:
			current = node.Object
		case *logic.IndexExpression:
			current = node.Left
		default:
			return ""
		}
	}
}

// notGiven tells, once every component of the page is known, a bare name the page reads that nothing around
// it declares, no component of the page declares, and the page is not given: "router.get không trả về x",
// at the read. Told only where what the page is given is known (pageGiven).
func (c *compiler) notGiven() error {
	if c.view == nil {
		return nil
	}
	helpers := formatHelpers()
	for _, use := range c.unowned {
		if c.view[use.name] || helpers[use.name] != nil || literalNames[use.name] || c.declaredOnPage(use.name) {
			continue
		}
		message := c.notGivenMessage(use.name)
		if use.attr != nil {
			return errAttr(use.node, use.attr, "%s", message)
		}
		return errAt(use.node, "%s", message)
	}
	return nil
}

// declaredOnPage reports whether a component called on this page declares the name.
func (c *compiler) declaredOnPage(name string) bool {
	for _, def := range c.called {
		if _, declared := def.state[name]; declared {
			return true
		}
	}
	return false
}

// notGivenMessage is the error of a name the page reads and is not given, saying what the page is given.
func (c *compiler) notGivenMessage(name string) string {
	g := c.given
	switch g.page {
	case pageNotFound:
		return fmt.Sprintf("trang không tìm thấy không được đưa tên nào, nên %s không đọc ra gì: notfound.ui, khung view.ui và các vùng quanh nó vẽ cho context.notFound() hay cho địa chỉ không thư mục nào khớp, không kèm object nào; phần động của khung là component (KITWORK_UI.md mục 4)", name)
	case pageError:
		return fmt.Sprintf("trang lỗi chỉ được đưa message, câu của context.error(…), nên %s không đọc ra gì: error.ui, khung view.ui và các vùng quanh nó không có tên nào khác%s", name, nearestGiven(name, g.names))
	}
	route := rel(c.site, g.file)
	given := givenList(g.names)
	switch {
	case g.routerless:
		return fmt.Sprintf("router.get không trả về %s: thư mục %s không có router.work, nên không có router.get và trang không được đưa tên nào; đưa %s cho trang bằng router.get((context) => context.view({ %s })) trong router.work của thư mục", name, c.route(), name, name)
	case !g.get && len(g.names) == 0:
		return fmt.Sprintf("router.get không trả về %s: %s không có router.get, nên trang mở bằng GET không được đưa tên nào; đưa %s cho trang bằng router.get((context) => context.view({ %s }))", name, route, name, name)
	case !g.get:
		return fmt.Sprintf("router.get không trả về %s: %s không có router.get, nên trang mở bằng GET không được đưa tên nào, và context.view({ … }) của cửa khác trong file cũng không có %s; tên có: %s%s", name, route, name, given, nearestGiven(name, g.names))
	case g.views == 0:
		return fmt.Sprintf("router.get không trả về %s: router.get trong %s không gọi context.view({ … }), nên trang không được đưa tên nào; đưa %s cho trang bằng context.view({ %s })", name, route, name, name)
	case len(g.names) == 0:
		return fmt.Sprintf("router.get không trả về %s: trang đọc %s mà object của context.view({ … }) trong %s không có tên nào; thêm %s vào object đó", name, name, route, name)
	}
	return fmt.Sprintf("router.get không trả về %s: trang đọc %s mà object của context.view({ … }) trong %s không có; tên có: %s%s", name, name, route, given, nearestGiven(name, g.names))
}

// givenList is the names given, sorted, as a message lists them.
func givenList(names map[string]bool) string {
	list := make([]string, 0, len(names))
	for name := range names {
		list = append(list, name)
	}
	sort.Strings(list)
	return strings.Join(list, ", ")
}

// nearestGiven is "; ý là y?" for the name given nearest to one read: the same name in other case first, then
// the fewest edits, two at most (one for a name of four letters or fewer); "" when none is that near.
func nearestGiven(written string, names map[string]bool) string {
	list := make([]string, 0, len(names))
	for name := range names {
		list = append(list, name)
	}
	sort.Strings(list)
	for _, name := range list {
		if strings.EqualFold(name, written) {
			return "; ý là " + name + "?"
		}
	}
	limit := 2
	if utf8.RuneCountInString(written) <= 4 {
		limit = 1
	}
	best, nearest := limit+1, ""
	for _, name := range list {
		if d := editDistance(written, name, limit); d < best {
			best, nearest = d, name
		}
	}
	if nearest == "" {
		return ""
	}
	return "; ý là " + nearest + "?"
}

// readAt is where a read stands for its error: the attribute that holds it, or, in a run of text, the {{ that
// opens the expression, where parseInterpolation found it (the text's start when that is not the run's).
func readAt(n *Node, a *Attr, e *expr) *Node {
	if a != nil || !n.IsText() || e.brace < 0 || !strings.HasPrefix(n.Text[min(e.brace, len(n.Text)):], "{{") {
		return n
	}
	line, column := n.Line, n.Column
	for _, r := range n.Text[:e.brace] {
		if r == '\n' {
			line, column = line+1, 1
			continue
		}
		column++
	}
	return &Node{File: n.File, Line: line, Column: column}
}

// eachNode visits every statement and expression of the statements, outer first: blocks, functions' bodies
// and the defaults of a pattern included.
func eachNode(statements []logic.Statement, visit func(logic.Node)) {
	for _, statement := range statements {
		eachStatementNode(statement, visit)
	}
}

func eachStatementNode(statement logic.Statement, visit func(logic.Node)) {
	switch node := statement.(type) {
	case *logic.VarStatement:
		visit(node)
		eachPatternNode(node.Pattern, visit)
		eachExpressionNode(node.Value, visit)
	case *logic.ExpressionStatement:
		eachExpressionNode(node.Expression, visit)
	case *logic.ReturnStatement:
		eachExpressionNode(node.ReturnValue, visit)
	case *logic.BlockStatement:
		if node != nil {
			eachNode(node.Statements, visit)
		}
	case *logic.GroupStatement:
		eachNode(node.Statements, visit)
	case *logic.SwitchStatement:
		eachExpressionNode(node.Discriminant, visit)
		for _, each := range node.Cases {
			eachExpressionNode(each.Test, visit)
			eachNode(each.Statements, visit)
		}
	case *logic.ForStatement:
		eachExpressionNode(node.Iterable, visit)
		eachStatementNode(node.Body, visit)
	case *logic.ForRangeStatement:
		eachStatementNode(node.Init, visit)
		eachExpressionNode(node.Cond, visit)
		eachExpressionNode(node.Update, visit)
		eachStatementNode(node.Body, visit)
	case *logic.DeferStatement:
		eachExpressionNode(node.Fn, visit)
	case *logic.SpawnStatement:
		eachExpressionNode(node.Fn, visit)
	case logic.Expression:
		eachExpressionNode(node, visit)
	}
}

func eachPatternNode(pattern *logic.BindingPattern, visit func(logic.Node)) {
	if pattern == nil {
		return
	}
	for _, element := range pattern.Elements {
		eachExpressionNode(element.Default, visit)
		eachPatternNode(element.Pattern, visit)
	}
}

func eachExpressionNode(e logic.Expression, visit func(logic.Node)) {
	if e == nil {
		return
	}
	visit(e)
	switch node := e.(type) {
	case *logic.PrefixExpression:
		eachExpressionNode(node.Right, visit)
	case *logic.InfixExpression:
		eachExpressionNode(node.Left, visit)
		eachExpressionNode(node.Right, visit)
	case *logic.IfExpression:
		eachExpressionNode(node.Condition, visit)
		eachStatementNode(node.Consequence, visit)
		eachStatementNode(node.Alternative, visit)
	case *logic.TernaryExpression:
		eachExpressionNode(node.Condition, visit)
		eachExpressionNode(node.Consequence, visit)
		eachExpressionNode(node.Alternative, visit)
	case *logic.CallExpression:
		eachExpressionNode(node.Function, visit)
		for _, argument := range node.Arguments {
			eachExpressionNode(argument, visit)
		}
	case *logic.MethodCallExpression:
		eachExpressionNode(node.Object, visit)
		for _, argument := range node.Arguments {
			eachExpressionNode(argument, visit)
		}
	case *logic.MemberExpression:
		eachExpressionNode(node.Object, visit)
	case *logic.IndexExpression:
		eachExpressionNode(node.Left, visit)
		eachExpressionNode(node.Index, visit)
	case *logic.ChainExpression:
		eachExpressionNode(node.Expression, visit)
	case *logic.AssignmentExpression:
		eachExpressionNode(node.Value, visit)
	case *logic.ArrayLiteral:
		for _, element := range node.Elements {
			eachExpressionNode(element, visit)
		}
	case *logic.ObjectLiteral:
		for _, entry := range node.Entries {
			eachExpressionNode(entry.Value, visit)
		}
	case *logic.SpreadExpression:
		eachExpressionNode(node.Value, visit)
	case *logic.TemplateLiteral:
		for _, part := range node.Parts {
			eachExpressionNode(part, visit)
		}
	case *logic.FunctionLiteral:
		eachStatementNode(node.Body, visit)
	}
}
