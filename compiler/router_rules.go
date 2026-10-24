package compiler

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
)

// The rules of router.work and of the router's config.work (KITWORK_ROUTER.md mục 3, 4, 11; KITWORK_UI.md
// mục 4; 24/10/2026), read from the file as the author wrote it, before anything is bundled into it
// (readRouterFile), as a folder's config.work is read (folder_config.go):
//
//   - one way to the router: the line import * as router from "kitwork/router", named router, once;
//   - each line a call that begins router.<word>(…), the word one of the closed vocabulary (router_words.go):
//     a door chaining only guard, limit, cache, persist; a folder call chaining nothing, a call whose nearest
//     folder wins once per file; no door in a config.work, no router.timezone outside the app's config.work;
//   - in router.work, no const get, post, put or patch at the top level, and context.view handed an object
//     written in place, never view taken apart.
//
// The resolver then refuses the router anywhere but at the head of one of these lines (router.go). Any other
// .work file refuses kitwork/router altogether (refuseRouterImport). A template site's router.kitwork.js, and
// a source compiled without a file's name, keep every old way.

// RouterConfigFile is the name of the file a folder's rules are split out into.
const RouterConfigFile = "config.work"

// routerFileKind is what a file is to the rules of the router.
type routerFileKind uint8

const (
	notRouterFile routerFileKind = iota
	routerWorkFile
	routerConfigFile
)

// RouterCalls are the lines of a router.work or a router config.work: Heads are the router.<word>(…) each line
// begins with, which the resolver lets through; every other use of the router is refused.
type RouterCalls struct {
	Heads map[*MethodCallExpression]bool
}

// RouterImportOutside is the error of kitwork/router in a .work file that is neither a router.work nor a
// config.work of a site: a job, a schedule, a library, a schema, the manifest.
const RouterImportOutside = `kitwork/router chỉ có trong router.work và config.work của site: file này không khai cửa hay luật của nhánh; router.get(…), router.guard(…) viết trong router.work, còn hàm dùng chung nhận context của người gọi (KITWORK_ROUTER.md mục 3)`

// RouterImportForm is the error of a way to the router other than the one line; written is what the file
// wrote, uses the calls it meant.
func RouterImportForm(written, uses string) string {
	if uses == "" {
		uses = "router.get(…), router.guard(…)"
	}
	return fmt.Sprintf(`%s: router chỉ đến qua đúng một dòng import * as router from "kitwork/router", tên router, một lần mỗi file; rồi %s (KITWORK_ROUTER.md mục 3)`,
		written, uses)
}

// RouterImportTwice is the error of the router's import line written a second time.
func RouterImportTwice(first, second int) string {
	return fmt.Sprintf(`import * as router from "kitwork/router" viết hai lần, dòng %d và dòng %d: một file một dòng import đó`, first, second)
}

// RouterAsValue is the error of the router read as a value; written is what was read (router, router.get).
func RouterAsValue(written string) string {
	return fmt.Sprintf("%s đọc làm giá trị: router chỉ đứng đầu một lời gọi ở cấp file, router.get(…), router.guard(…), để danh sách cửa đọc được bằng phân tích tĩnh (KITWORK_ROUTER.md mục 3)", written)
}

// RouterNotAtTop is the error of router.<word>(…) written anywhere but as a line of its own at the file's top
// level.
func RouterNotAtTop(word string) string {
	return fmt.Sprintf("router.%s(…) chỉ viết ở cấp file, một dòng; không trong if, vòng lặp hay thân hàm, không làm phần của một biểu thức khác (KITWORK_ROUTER.md mục 3)", word)
}

// RouterConstDoor is the error of const get, post, put or patch at the top level of a router.work: it looks
// like a door and opens none.
func RouterConstDoor(keyword, name string) string {
	return fmt.Sprintf("%s %s ở cấp file của router.work: cửa khai bằng router.%s(…); %s %s trông như cửa mà không mở gì, đặt tên khác cho hàm rồi router.%s(tên_đó) (KITWORK_ROUTER.md mục 3)",
		keyword, name, name, keyword, name, name)
}

// ContextViewObject is the error of context.view(…) handed anything but an object written in place with plain
// keys; written is the call as the file wrote it.
func ContextViewObject(written string) string {
	return fmt.Sprintf("%s: context.view nhận một object viết tại chỗ, khoá là tên viết sẵn, để trang biết từng tên nó nhận: context.view({ products }) (KITWORK_UI.md mục 4)", written)
}

// ContextViewApart is the error of view taken apart from context in a router.work or a router config.work;
// written is how.
func ContextViewApart(written string) string {
	return fmt.Sprintf("%s: view tách khỏi context; gọi thẳng context.view({ … }) trên context, để trang biết từng tên nó nhận (KITWORK_UI.md mục 4)", written)
}

// routerConstDoors are the names a router.work may not declare at its top level.
var routerConstDoors = map[string]bool{"get": true, "post": true, "put": true, "patch": true}

// routerFileOf is what the file at absolute is to the rules of the router: a router.work, or a config.work of
// the app's folder, a site or a branch (not of .shared/queue/ or .schedule/, and never in a folder that is no
// route: one whose name begins with a dot or an underscore).
func routerFileOf(appRoot, absolute string) routerFileKind {
	switch filepath.Base(absolute) {
	case RouterFile:
		return routerWorkFile
	case RouterConfigFile:
		if folderOf(appRoot, absolute) != "" {
			return notRouterFile
		}
		if relative, ok := appRelativePath(appRoot, absolute); ok {
			segments := strings.Split(relative, "/")
			for _, segment := range segments[:len(segments)-1] {
				if strings.HasPrefix(segment, ".") || strings.HasPrefix(segment, "_") {
					return notRouterFile
				}
			}
		}
		return routerConfigFile
	}
	return notRouterFile
}

// appRelativePath is absolute from the app's folder, with forward slashes; false when the folder is not known
// or the file is not in it.
func appRelativePath(appRoot, absolute string) (string, bool) {
	if appRoot == "" {
		return "", false
	}
	root, err := filepath.Abs(appRoot)
	if err != nil {
		root = appRoot
	}
	relative, err := filepath.Rel(root, absolute)
	if err != nil {
		return "", false
	}
	relative = filepath.ToSlash(relative)
	if relative == ".." || strings.HasPrefix(relative, "../") {
		return "", false
	}
	return relative, true
}

// routerDepth is how many names the file's path has from the app's folder: 1 for app/config.work, 2 for
// app/shop.com/config.work (a site's root, or a branch when the app's folder is the site's), 3 or more in a
// branch of a site; 0 when the app's folder is not known.
func routerDepth(appRoot, absolute string) int {
	relative, ok := appRelativePath(appRoot, absolute)
	if !ok {
		return 0
	}
	return strings.Count(relative, "/") + 1
}

// routerFailure is an error found in a router file, kept with its place until the first in the file is chosen.
type routerFailure struct {
	at      Token
	message string
}

// routerReader reads a router.work or a router config.work as the author wrote it.
type routerReader struct {
	kind  routerFileKind
	depth int
	text  []byte
	// namespace is the name the router's import line binds ("router"); "" when the file has none. declared
	// are the names the file declares at its top level, imports among them.
	namespace string
	declared  map[string]bool
	failures  []routerFailure
	calls     *RouterCalls
}

func (reader *routerReader) fail(at Token, message string) {
	reader.failures = append(reader.failures, routerFailure{at: at, message: message})
}

func (reader *routerReader) lineOf(token Token) int {
	position := int(token.Position)
	if position < 0 || position > len(reader.text) {
		return 0
	}
	return bytes.Count(reader.text[:position], []byte{'\n'}) + 1
}

// first is the error of the failure written first in the file; nil when there is none.
func firstFailure(failures []routerFailure, text []byte) error {
	if len(failures) == 0 {
		return nil
	}
	first := failures[0]
	for _, failure := range failures[1:] {
		if failure.at.Position < first.at.Position {
			first = failure
		}
	}
	return fmt.Errorf("%s: %s", textLocation(text, first.at), first.message)
}

// readRouterFile reads the top level of a router.work or a router config.work, before any import is bundled
// into it: the router's import line, each line that calls the router, the const of a door (in a router.work),
// and every context.view (in both: the two files are two halves of one folder's scope, KITWORK_ROUTER.md mục 4,
// so a guard of config.work hands view an object written in place as one of router.work does). depth is
// routerDepth's; text is the file's source, for the places errors give. The first error in the file is the one
// returned.
func readRouterFile(prog *Program, kind routerFileKind, depth int, text []byte) (*RouterCalls, error) {
	reader := &routerReader{kind: kind, depth: depth, text: text, calls: &RouterCalls{Heads: map[*MethodCallExpression]bool{}}}
	statements := topLevelStatements(prog.Statements)
	reader.declared = map[string]bool{}
	for _, statement := range statements {
		if node, ok := statement.(*VarStatement); ok {
			for _, name := range node.Names {
				reader.declared[name.Value] = true
			}
		}
	}
	reader.imports(statements)
	reader.lines(statements)
	reader.views(prog.Statements)
	if err := firstFailure(reader.failures, text); err != nil {
		return nil, err
	}
	return reader.calls, nil
}

// importOf is the module of "kitwork" an import's statement takes ("" for "kitwork" itself, "router" for
// "kitwork/router"), and the names a named import takes from it.
func importOf(node *VarStatement) (module string, imported []string) {
	switch {
	case node.Import == ImportNamed && node.DestructMode == DestructNone:
		// import { a as b } from "kitwork/x" is const b = kitwork().x.a
		member, isMember := node.Value.(*MemberExpression)
		if !isMember || member.Property == nil {
			return "", nil
		}
		module, _ = kitworkModulePath(member.Object)
		return module, []string{member.Property.Value}
	case node.Import == ImportNamed:
		module, _ = kitworkModulePath(node.Value)
		for _, name := range node.Names {
			imported = append(imported, name.Value)
		}
		return module, imported
	}
	module, _ = kitworkModulePath(node.Value)
	return module, nil
}

// importWritten is an import's statement as the author wrote it.
func importWritten(node *VarStatement, module string, imported []string) string {
	specifier := `"kitwork"`
	if module != "" {
		specifier = `"kitwork/` + module + `"`
	}
	switch node.Import {
	case ImportNamespace:
		return fmt.Sprintf("import * as %s from %s", node.Names[0].Value, specifier)
	case ImportDefault:
		return fmt.Sprintf("import %s from %s", node.Names[0].Value, specifier)
	}
	if node.DestructMode == DestructNone && len(imported) == 1 && len(node.Names) == 1 && imported[0] != node.Names[0].Value {
		return fmt.Sprintf("import { %s as %s } from %s", imported[0], node.Names[0].Value, specifier)
	}
	return fmt.Sprintf("import { %s } from %s", strings.Join(imported, ", "), specifier)
}

// imports reads the file's imports of "kitwork": the one line of the router, and every other way to it.
func (reader *routerReader) imports(statements []Statement) {
	var first *VarStatement
	for _, statement := range statements {
		node, ok := statement.(*VarStatement)
		if !ok || node.Import == ImportNone {
			continue
		}
		module, imported := importOf(node)
		switch {
		case module == "router" && node.Import == ImportNamespace && node.Names[0].Value == "router":
			if first != nil {
				reader.fail(node.Token, RouterImportTwice(reader.lineOf(first.Token), reader.lineOf(node.Token)))
				continue
			}
			first = node
			reader.namespace = "router"
		case module == "router" && node.Import == ImportNamed && containsWord(imported, "view"):
			reader.fail(node.Token, RouterViewRemoved)
		case module == "router" && node.Import == ImportNamed:
			uses := make([]string, len(imported))
			for i, name := range imported {
				uses[i] = "router." + name + "(…)"
			}
			reader.fail(node.Token, RouterImportForm(importWritten(node, module, imported), strings.Join(uses, ", ")))
		case module == "router":
			reader.fail(node.Token, RouterImportForm(importWritten(node, module, imported), ""))
		case module == "" && node.Import == ImportNamed && containsWord(imported, "router"):
			reader.fail(node.Token, RouterImportForm(importWritten(node, module, imported), ""))
		}
	}
}

func containsWord(words []string, word string) bool {
	for _, each := range words {
		if each == word {
			return true
		}
	}
	return false
}

// isRouter reports e as the router: the name of its import line, or kitwork().router written out (refused
// where it is read, router.go).
func (reader *routerReader) isRouter(e Expression) bool {
	if name, isName := e.(*Identifier); isName {
		return reader.namespace != "" && name.Value == reader.namespace
	}
	module, ok := kitworkModulePath(e)
	return ok && module == "router"
}

// lines reads each statement of the top level: a line that calls the router, and a const or let of the file.
func (reader *routerReader) lines(statements []Statement) {
	once := map[string]Token{}
	for _, statement := range statements {
		switch node := statement.(type) {
		case *VarStatement:
			if node.Import != ImportNone {
				continue
			}
			if reader.isRouter(node.Value) {
				// const r = router, const { get } = router read the router as a value; view taken apart is
				// said as router.view. The resolver refuses kitwork().router itself (routerMember).
				if element := patternElement(node.Pattern, "view"); element != nil {
					reader.fail(element.KeyToken, RouterViewRemoved)
				} else if name, isName := node.Value.(*Identifier); isName {
					reader.fail(name.Token, RouterAsValue(name.Value))
				}
				continue
			}
			if reader.kind != routerWorkFile {
				continue
			}
			for _, name := range node.Names {
				if routerConstDoors[name.Value] {
					reader.fail(name.Token, RouterConstDoor(declarationKeyword(node), name.Value))
				}
			}
		case *ExpressionStatement:
			reader.readLine(node.Expression, once)
		}
	}
}

// declarationKeyword is the word a declaration is written with.
func declarationKeyword(node *VarStatement) string {
	switch {
	case node.Function:
		return "function"
	case node.Token.Kind == Let:
		return "let"
	}
	return "const"
}

// patternElement is the element of a pattern, nested ones included, that reads key; nil when none does.
func patternElement(pattern *BindingPattern, key string) *BindingElement {
	if pattern == nil || pattern.Array {
		return nil
	}
	for i := range pattern.Elements {
		element := &pattern.Elements[i]
		if element.Key == key {
			return element
		}
		if found := patternElement(element.Pattern, key); found != nil {
			return found
		}
	}
	return nil
}

// routerLink is one link of a line after the router.<word>(…) it begins with.
type routerLink struct {
	at      Token
	word    string
	written string
	call    bool
}

// readLine reads a line: when it begins with router.<word>(…), the word and what is chained after it. Any other
// line is not the router's (the resolver refuses the router read in it).
func (reader *routerReader) readLine(expression Expression, once map[string]Token) {
	var links []routerLink
	current := expression
	for {
		switch node := current.(type) {
		case *ChainExpression:
			current = node.Expression
			continue
		case *MethodCallExpression:
			if node.Method == nil {
				return
			}
			if name, isName := node.Object.(*Identifier); isName && reader.namespace != "" && name.Value == reader.namespace {
				for left, right := 0, len(links)-1; left < right; left, right = left+1, right-1 {
					links[left], links[right] = links[right], links[left]
				}
				reader.call(node, links, once)
				return
			}
			if name, isName := node.Object.(*Identifier); isName && reader.namespace == "" && name.Value == "router" && !reader.declared["router"] {
				// router written with no import line (and no name of the file's): the spelling before 19/10/2026
				reader.fail(name.Token, RouterImportForm("router."+node.Method.Value+"(…) không có dòng import", ""))
				return
			}
			links = append(links, routerLink{at: node.Method.Token, word: node.Method.Value, written: "." + node.Method.Value + "(…)", call: true})
			current = node.Object
		case *MemberExpression:
			if node.Property == nil {
				return
			}
			links = append(links, routerLink{at: node.Property.Token, word: node.Property.Value, written: "." + node.Property.Value})
			current = node.Object
		case *CallExpression:
			links = append(links, routerLink{at: node.Token, written: "(…)"})
			current = node.Function
		case *IndexExpression:
			links = append(links, routerLink{at: node.Token, written: "[…]"})
			current = node.Left
		default:
			return
		}
	}
}

// call reads the router.<word>(…) a line begins with and what the line chains after it.
func (reader *routerReader) call(head *MethodCallExpression, links []routerLink, once map[string]Token) {
	word, at := head.Method.Value, head.Method.Token
	if removed, ok := RouterRemovedWord(word); ok {
		reader.fail(at, removed)
		return
	}
	known, ok := RouterWordOf(word)
	if !ok {
		reader.fail(at, RouterUnknownWord(word, reader.kind == routerConfigFile))
		return
	}
	reader.calls.Heads[head] = true
	if known.Kind != RouterFolderCall {
		if reader.kind == routerConfigFile {
			reader.fail(at, RouterDoorInConfig(word))
			return
		}
		reader.routeRules(word, links)
		return
	}
	if len(links) > 0 {
		reader.fail(links[0].at, RouterCallChained(word, links[0].written))
		return
	}
	if where := reader.wrongLevel(known); where != "" {
		reader.fail(at, RouterWrongLevel(word, where))
		return
	}
	if known.Inheritance == RouterNearestWins {
		if first, twice := once[word]; twice {
			reader.fail(at, RouterCallTwice(word, fmt.Sprintf("dòng %d", reader.lineOf(first)), fmt.Sprintf("dòng %d", reader.lineOf(at))))
			return
		}
		once[word] = at
	}
}

// routeRules reads what a door chains: guard and limit as often as written, cache and persist once.
func (reader *routerReader) routeRules(door string, links []routerLink) {
	seen := map[string]Token{}
	for _, link := range links {
		if !link.call {
			reader.fail(link.at, RouteRuleNotACall(door, link.written))
			return
		}
		if removed, ok := RouteRuleRemoved(door, link.word); ok {
			reader.fail(link.at, removed)
			return
		}
		rule, ok := RouteRuleOf(link.word)
		if !ok {
			reader.fail(link.at, RouteRuleUnknown(door, link.word))
			return
		}
		if rule.Repeats {
			continue
		}
		if first, twice := seen[link.word]; twice {
			reader.fail(link.at, RouteRuleTwice(door, link.word, textLocation(reader.text, first), textLocation(reader.text, link.at)))
			return
		}
		seen[link.word] = link.at
	}
}

// wrongLevel is the place, in the author's words, where a folder call stands that its level does not take,
// as far as the file's name and path tell; "" when it may stand there or that is not known here (the engine
// checks the rest as it runs the folders). router.work is never the app's; a config.work of the app's folder
// may be a site's too (one site in the app's folder), so only a file deeper than it is surely not the app's,
// and only one deeper than a site's root surely a branch's.
func (reader *routerReader) wrongLevel(known RouterWord) string {
	switch {
	case reader.kind == routerWorkFile && !known.Takes(RouterSite|RouterBranch):
		return "router.work"
	case reader.kind == routerConfigFile && reader.depth >= 2 && !known.Takes(RouterSite|RouterBranch):
		return "config.work của một site hay một nhánh"
	case reader.depth >= 3 && !known.Takes(RouterBranch):
		return routerFileName(reader.kind) + " của một nhánh"
	}
	return ""
}

// routerFileName is the name of a router file of kind.
func routerFileName(kind routerFileKind) string {
	if kind == routerConfigFile {
		return RouterConfigFile
	}
	return RouterFile
}

// views reads every .view(…) of a router.work or a router config.work, functions' bodies included, whatever it
// is called on (the .ui compiler reads them all, ui/given.go viewNames): handed an object written in place with
// plain keys, or nothing. view taken apart from context is read by contextViews.
func (reader *routerReader) views(statements []Statement) {
	eachNode(statements, func(node Node) bool {
		switch n := node.(type) {
		case *VarStatement:
			if n.Import != ImportNone {
				return false
			}
		case *MethodCallExpression:
			if n.Method != nil && n.Method.Value == "view" && len(n.Arguments) > 0 && !reader.isRouter(n.Object) {
				reader.viewArgument(n)
			}
		}
		return true
	})
	reader.contextViews(statements)
}

// contextModifiers are the calls on context that give it back (KITWORK_UI.md mục 4: every modifier before the
// answer), so view read after one is still context's: (context.title("…").view)(…).
var contextModifiers = map[string]bool{
	"status": true, "type": true, "header": true, "headers": true, "cookie": true,
	"title": true, "description": true, "image": true, "canonical": true, "robots": true, "meta": true,
}

// routerHanded is a function a router line hands the context to, and the place of the parameter that gets it.
type routerHanded struct {
	function *FunctionLiteral
	index    int
}

// contextViews refuses view taken apart from context in a router.work or a router config.work:
// (context.view)(…), context["view"], const { view } = context, ({ view }) => …, typeof context.view. The
// context is what the file hands a function as its first argument through a router line (router.get(fn),
// .guard(fn), router.guard(fn), or a top-level const of a function handed by name, router.get(handle)),
// followed into a top-level function the file calls with it (render(context)), into a name bound to it (const
// c = context, ({ response }) => …), and past a modifier (context.title("…")). view read off anything else
// (settings.view, a row's view column, items.map(({ view }) => view)) is data, and stays.
func (reader *routerReader) contextViews(statements []Statement) {
	top := topLevelStatements(statements)
	functions := map[string]*FunctionLiteral{}
	for _, statement := range top {
		node, ok := statement.(*VarStatement)
		if !ok || node.Import != ImportNone || node.Pattern != nil || len(node.Names) != 1 {
			continue
		}
		if function, isFunction := node.Value.(*FunctionLiteral); isFunction {
			functions[node.Names[0].Value] = function
		}
	}
	var pending []routerHanded
	seen := map[routerHanded]bool{}
	hand := func(argument Expression, index int) {
		var function *FunctionLiteral
		switch node := argument.(type) {
		case *FunctionLiteral:
			function = node
		case *Identifier:
			function = functions[node.Value]
		}
		if function == nil || index >= len(function.Parameters) {
			return
		}
		each := routerHanded{function: function, index: index}
		if !seen[each] {
			seen[each] = true
			pending = append(pending, each)
		}
	}
	for _, statement := range top {
		if line, ok := statement.(*ExpressionStatement); ok {
			for _, call := range reader.lineCalls(line.Expression) {
				for _, argument := range call.Arguments {
					hand(argument, 0)
				}
			}
		}
	}
	// a call that hands the context on: render(context), ((c) => …)(context)
	called := func(call *CallExpression, names map[string]bool) {
		for index, argument := range call.Arguments {
			if contextRooted(argument, names) {
				hand(call.Function, index)
			}
		}
	}
	for len(pending) > 0 {
		each := pending[0]
		pending = pending[1:]
		names := map[string]bool{each.function.Parameters[each.index].Value: true}
		reader.contextWalk(each.function.Body, names, called)
	}
}

// lineCalls are the calls of a router line, the router.<word>(…) it begins with and each one chained after it;
// nil for a line that does not begin with one (readLine refused it, or it is not the router's).
func (reader *routerReader) lineCalls(expression Expression) []*MethodCallExpression {
	var calls []*MethodCallExpression
	current := expression
	for {
		switch node := current.(type) {
		case *ChainExpression:
			current = node.Expression
		case *MethodCallExpression:
			calls = append(calls, node)
			if reader.calls.Heads[node] {
				return calls
			}
			current = node.Object
		case *MemberExpression:
			current = node.Object
		case *CallExpression:
			current = node.Function
		case *IndexExpression:
			current = node.Left
		default:
			return nil
		}
	}
}

// contextWalk reads node where names are the names that hold the context, refusing view taken apart from
// them; called hears each call, so the context is followed into the function it is handed to. A function's
// parameter of the same name is the function's own; a const or let of it, its own from there on.
func (reader *routerReader) contextWalk(node Node, names map[string]bool, called func(*CallExpression, map[string]bool)) {
	switch n := node.(type) {
	case *FunctionLiteral:
		inner := map[string]bool{}
		for name := range names {
			inner[name] = true
		}
		for _, parameter := range n.Parameters {
			delete(inner, parameter.Value)
		}
		if len(inner) > 0 && n.Body != nil {
			reader.contextWalk(n.Body, inner, called)
		}
		return
	case *VarStatement:
		if n.Import != ImportNone {
			return
		}
		rooted := contextRooted(n.Value, names)
		for _, name := range n.Names {
			delete(names, name.Value)
		}
		switch {
		case n.Pattern != nil && rooted:
			if element := contextPatternView(n.Pattern); element != nil {
				reader.fail(element.KeyToken, ContextViewApart("{ view }"))
			}
			for _, element := range n.Pattern.Elements {
				if element.Key == "response" && element.Name != nil {
					names[element.Name.Value] = true
				}
			}
		case n.Pattern == nil && rooted && len(n.Names) == 1:
			names[n.Names[0].Value] = true
		}
	case *MemberExpression:
		if n.Property != nil && n.Property.Value == "view" && contextRooted(n.Object, names) {
			reader.fail(n.Property.Token, ContextViewApart(receiverText(n.Object)+".view"))
		}
	case *IndexExpression:
		if literal, isLiteral := n.Index.(*Literal); isLiteral && literal.Value.IsString() && literal.Value.Text() == "view" &&
			contextRooted(n.Left, names) {
			reader.fail(literal.Token, ContextViewApart(receiverText(n.Left)+`["view"]`))
		}
	case *CallExpression:
		called(n, names)
	}
	for _, child := range nodeChildren(node) {
		reader.contextWalk(child, names, called)
	}
}

// contextRooted reports e as the context: a name that holds it, its response (context.response), or what a
// modifier of it gives back (context.title("…")).
func contextRooted(e Expression, names map[string]bool) bool {
	switch node := e.(type) {
	case *Identifier:
		return names[node.Value]
	case *ChainExpression:
		return contextRooted(node.Expression, names)
	case *MemberExpression:
		return node.Property != nil && node.Property.Value == "response" && contextRooted(node.Object, names)
	case *MethodCallExpression:
		return node.Method != nil && contextModifiers[node.Method.Value] && contextRooted(node.Object, names)
	}
	return false
}

// contextPatternView is the element of a pattern taking the context apart that reads view: { view }, or
// { response: { view } }; nil when none does.
func contextPatternView(pattern *BindingPattern) *BindingElement {
	if pattern == nil || pattern.Array {
		return nil
	}
	for i := range pattern.Elements {
		element := &pattern.Elements[i]
		if element.Key == "view" {
			return element
		}
		if element.Key == "response" && element.Pattern != nil && !element.Pattern.Array {
			for j := range element.Pattern.Elements {
				if element.Pattern.Elements[j].Key == "view" {
					return &element.Pattern.Elements[j]
				}
			}
		}
	}
	return nil
}

// viewArgument checks what a .view(…) is handed: one object written in place whose keys are plain names. A
// second argument is refused at it: the engine draws the page with the last object it is handed (work/view.go),
// so the names of the first would read nothing, and the page could not be told which it is given (ui/given.go).
func (reader *routerReader) viewArgument(call *MethodCallExpression) {
	receiver := receiverText(call.Object)
	if len(call.Arguments) > 1 {
		extra := call.Arguments[1]
		reader.fail(Token{Position: startPosition(extra), Source: getNodeSource(extra)},
			ContextViewObject(fmt.Sprintf("%s.view({ … }, …)", receiver)))
	}
	argument := call.Arguments[0]
	object, isObject := argument.(*ObjectLiteral)
	if !isObject {
		written := "…"
		if name, isName := argument.(*Identifier); isName {
			written = name.Value
		}
		reader.fail(Token{Position: startPosition(argument), Source: getNodeSource(argument)},
			ContextViewObject(fmt.Sprintf("%s.view(%s)", receiver, written)))
		return
	}
	for _, entry := range object.Entries {
		if entry.IsSpread {
			written := "…"
			if name, isName := entry.Value.(*Identifier); isName {
				written = name.Value
			}
			reader.fail(Token{Position: startPosition(entry.Value), Source: getNodeSource(entry.Value)},
				ContextViewObject(fmt.Sprintf("%s.view({ ...%s })", receiver, written)))
			return
		}
		switch key := entry.Key.(type) {
		case *Identifier:
			continue
		case *Literal:
			if key.Value.IsString() {
				continue
			}
		}
		reader.fail(Token{Position: startPosition(entry.Key), Source: getNodeSource(entry.Key)},
			ContextViewObject(fmt.Sprintf("%s.view({ %s: … })", receiver, entry.Key.String())))
		return
	}
}

// receiverText is the object a word is read off, as an error writes it: its name, or … for anything else.
func receiverText(e Expression) string {
	if name, isName := e.(*Identifier); isName {
		return name.Value
	}
	return "…"
}

// eachNode visits every node of the statements, functions' bodies included, outer first; visit returning
// false skips what the node holds.
func eachNode(statements []Statement, visit func(Node) bool) {
	stack := make([]Node, 0, len(statements))
	for i := len(statements) - 1; i >= 0; i-- {
		if statements[i] != nil {
			stack = append(stack, statements[i])
		}
	}
	for len(stack) > 0 {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if !visit(node) {
			continue
		}
		children := nodeChildren(node)
		for i := len(children) - 1; i >= 0; i-- {
			stack = append(stack, children[i])
		}
	}
}

// refuseRouterImport refuses kitwork/router in a .work file that is neither a router.work nor a router
// config.work, as the author wrote it (a job, a schedule, a library alone or bundled, a schema, a config.work
// of .shared/queue/ or .schedule/, the manifest): an import of it in any form, import { router } from
// "kitwork", and kitwork().router or the router read off the module "kitwork" (import * as k from "kitwork",
// const k = kitwork(), a const of either; k.router, k["router"], const { router } = k).
func refuseRouterImport(prog *Program, text []byte) error {
	var failures []routerFailure
	roots := map[string]bool{}
	for _, statement := range topLevelStatements(prog.Statements) {
		node, ok := statement.(*VarStatement)
		if !ok || node.Import == ImportNone {
			continue
		}
		module, imported := importOf(node)
		switch {
		case module == "router", module == "" && node.Import == ImportNamed && containsWord(imported, "router"):
			failures = append(failures, routerFailure{at: node.Token, message: RouterImportOutside})
		case module == "" && node.Import == ImportNamespace:
			roots[node.Names[0].Value] = true
		}
	}
	kitworkModule := func(e Expression) bool {
		if isKitworkCall(e) {
			return true
		}
		name, isName := e.(*Identifier)
		return isName && roots[name.Value]
	}
	eachNode(prog.Statements, func(node Node) bool {
		switch n := node.(type) {
		case *VarStatement:
			if n.Import != ImportNone {
				return false
			}
			if n.Pattern != nil && kitworkModule(n.Value) {
				if element := patternElement(n.Pattern, "router"); element != nil {
					failures = append(failures, routerFailure{at: element.KeyToken, message: RouterImportOutside})
				}
			}
			// const k = kitwork(), const k2 = k: another name of the module (markEnvironment reads it so too)
			if n.Pattern == nil && n.DestructMode == DestructNone && len(n.Names) == 1 && kitworkModule(n.Value) {
				roots[n.Names[0].Value] = true
			}
		case *MemberExpression:
			if n.Property != nil && n.Property.Value == "router" && kitworkModule(n.Object) {
				failures = append(failures, routerFailure{at: n.Property.Token, message: RouterImportOutside})
			}
		case *IndexExpression:
			if key, isLiteral := n.Index.(*Literal); isLiteral && key.Value.IsString() && key.Value.Text() == "router" && kitworkModule(n.Left) {
				failures = append(failures, routerFailure{at: key.Token, message: RouterImportOutside})
			}
		}
		return true
	})
	return firstFailure(failures, text)
}
