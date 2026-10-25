package compiler

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// A guard does not draw a page, and answers only so (25/10/2026, KITWORK_ROUTER.md mục 4): it lets the request
// on by returning nothing (or true), refuses it with false (403), or answers with context.redirect(…) or
// context.status(…).json(…). Any other answer of the context in a guard of a router.work or a router config.work
// (view, html, text, svg, css, file, notFound, error, a template response's words, json with no status before it in
// its chain) is a compile error wherever the compiler can follow the guard; a value written in the guard
// function's own return (a string, a number, an object, an array, a function, behind ?:, ||, && or ?? too) is one
// too, and any other value it returns fails as it runs. The compiler follows the guard into a function written in
// place, a top-level const of a function handed by name or through another name (const gate = draw, export
// default gate), a function imported by name from another file of the app, through the files that export it on,
// and every function of those files the guard hands the context to (check(context)). A call through a name
// declared where it is written (a local const, a parameter, a loop's name) is not followed: that is not the
// file's function of the name; nor is a name the program assigns (let gate = …; gate = …), whose first value is
// not surely the one the router gets. A guard it cannot follow (one a call makes, one read off an object, one that
// calls a function of its own) is refused by the engine as it runs, in the same words (work/view.go,
// work/router_serve.go). A template site's router.kitwork.js keeps its guards' answers.

// guardMay is what a guard may do, as its errors end.
const guardMay = "cho đi tiếp thì không trả gì, chặn thì trả false (403), hay trả lời bằng context.redirect(…), context.status(…).json(…) (KITWORK_ROUTER.md mục 4)"

// ContextViewInGuard is the error of a guard that draws a page; written is the call as the file wrote it.
func ContextViewInGuard(written string) string {
	return fmt.Sprintf("%s trong guard: guard không vẽ trang; %s", written, guardMay)
}

// ContextAnswerInGuard is the error of a guard that answers other than it may; written is the answer as the file
// wrote it (context.text(…), context.json(…) không có status).
func ContextAnswerInGuard(written string) string {
	return fmt.Sprintf("%s trong guard: guard chỉ %s", written, guardMay)
}

// GuardReturnsValue is the error of a guard that returns a value; kind is what it returns (một chuỗi, một object…).
func GuardReturnsValue(kind string) string {
	return fmt.Sprintf("guard trả về %s: guard chỉ %s", kind, guardMay)
}

// guardRefusedAnswers are the answers of the context a guard may not give, view aside (its own words): every one
// but redirect, and json after a status the guard wrote. The words of a template's response a router.work guard
// sees over a template's folder (send, string, bytes…) are among them. The VM reads a member's name in any case
// (context.Text is context.text), so the names are folded: lowercase here, and lowercased where they are read.
var guardRefusedAnswers = map[string]bool{
	"html": true, "text": true, "svg": true, "css": true, "file": true, "notfound": true, "error": true,
	"send": true, "string": true, "bytes": true, "render": true, "directory": true, "helloworld": true,
	"errorstring": true, "return": true,
}

// routerPlace is where a router file stands: the app's folder and the file, absolute; empty for a source
// compiled without them, whose imports are not followed.
type routerPlace struct {
	appRoot, file string
}

// guardFile is a file a guard is read in: the router file itself, or a file one of its guards comes from.
type guardFile struct {
	// name is the file as an error writes it (from the router file's folder, as the bundler writes it); text is
	// its source, dir its folder ("" when imports from it are not followed).
	name string
	text []byte
	dir  string
	// functions are the file's top-level const of a function by name; aliases its top-level const of another
	// name (const gate = draw, and export default gate, which the parser writes const __kw_default = gate);
	// imports the names it imports from another file; exports its own name of each name it exports.
	functions map[string]*FunctionLiteral
	aliases   map[string]string
	imports   map[string]guardImport
	exports   map[string]string
}

// guardImport is a name imported from another file: the import, and the name in that file's exports
// ("default" for import x from …).
type guardImport struct {
	statement *ImportStatement
	imported  string
}

func newGuardFile(name string, text []byte, dir string, prog *Program) *guardFile {
	file := &guardFile{name: name, text: text, dir: dir, functions: map[string]*FunctionLiteral{},
		aliases: map[string]string{}, imports: map[string]guardImport{}, exports: map[string]string{}}
	for _, statement := range topLevelStatements(prog.Statements) {
		switch node := statement.(type) {
		case *VarStatement:
			if node.Import != ImportNone || node.Pattern != nil || len(node.Names) != 1 {
				continue
			}
			switch value := node.Value.(type) {
			case *FunctionLiteral:
				file.functions[node.Names[0].Value] = value
			case *Identifier:
				file.aliases[node.Names[0].Value] = value.Value
			}
		case *ImportStatement:
			if node.Default != nil {
				file.imports[node.Default.Value] = guardImport{statement: node, imported: "default"}
			}
			for _, spec := range node.Names {
				file.imports[spec.Local] = guardImport{statement: node, imported: spec.Imported}
			}
		}
	}
	// a name the program assigns is not surely its first value: not followed, left to the engine as it runs
	for name := range reassigned(prog.Statements) {
		delete(file.functions, name)
		delete(file.aliases, name)
	}
	for _, name := range prog.Exports {
		file.exports[name] = name
	}
	for _, alias := range prog.ExportAliases {
		file.exports[alias.Exported] = alias.Local
	}
	if prog.HasDefault {
		file.exports["default"] = DefaultExportName
	}
	return file
}

// guardReader follows the guards of a router file into the functions they run.
type guardReader struct {
	reader *routerReader
	// resolver finds the file an import names as the bundler does; files are the files read, by absolute
	// path, nil for one that is not (the bundler says why when it bundles it).
	resolver *bundler
	entry    *guardFile
	files    map[string]*guardFile
	seen     map[routerHanded]bool
	// returned are the guard functions whose returns were read, once each
	returned map[*FunctionLiteral]bool
	// anchor is the guard as the router file hands it: where an error found in another file is told from.
	anchor Token
}

// guardViews reads every guard a line of the file hands the router, router.guard(…) and .guard(…) after a
// door, one function or an array of them, and refuses context.view in what it runs.
func (reader *routerReader) guardViews(prog *Program, place routerPlace) {
	dir := ""
	if place.file != "" {
		dir = filepath.Dir(place.file)
	}
	appRoot := place.appRoot
	if appRoot != "" {
		if absolute, err := filepath.Abs(appRoot); err == nil {
			appRoot = absolute
		}
	}
	guards := &guardReader{
		reader:   reader,
		resolver: &bundler{appRoot: appRoot, debugRoot: dir, sources: map[string]string{}},
		entry:    newGuardFile(filepath.Base(place.file), reader.text, dir, prog),
		files:    map[string]*guardFile{},
		seen:     map[routerHanded]bool{},
		returned: map[*FunctionLiteral]bool{},
	}
	for _, statement := range topLevelStatements(prog.Statements) {
		line, ok := statement.(*ExpressionStatement)
		if !ok {
			continue
		}
		for _, call := range reader.lineCalls(line.Expression) {
			if call.Method == nil || call.Method.Value != "guard" {
				continue
			}
			for _, argument := range call.Arguments {
				handed := []Expression{argument}
				if array, isArray := argument.(*ArrayLiteral); isArray {
					handed = array.Elements
				}
				for _, guard := range handed {
					// told from the guard's name, or from the word guard when it is written in place
					guards.anchor = call.Method.Token
					if name, isName := guard.(*Identifier); isName {
						guards.anchor = name.Token
					}
					guards.hand(guards.entry, guard, 0, nil, true)
				}
			}
		}
	}
}

// hand reads the function callee is in file as one handed the context as its index-th argument: an answer of the
// context it may not give is refused, and a call handing the context on is followed. outer are the names declared
// where the call is written (contextWalk), so a call through one of them is not followed into the file's function
// of that name; a function written in place there sees them too. guard is the function a router line hands: what
// it returns as written is read too, whatever it takes (a function it calls returns to it, not to the router).
//
// A json stands on the status written in its own chain, context.status(401).json(…), the one form of the rule: a
// status on a line of its own or written by another function is no compile-time status, since whether it runs
// before the json is what only the run tells (the engine, refuseGuardAnswer, takes a status the guard wrote before
// the json, wherever it wrote it).
func (guards *guardReader) hand(file *guardFile, callee Expression, index int, outer map[string]bool, guard bool) {
	if name, isName := callee.(*Identifier); isName && outer[name.Value] {
		return
	}
	file, function := guards.function(file, callee)
	if function == nil || function.Body == nil {
		return
	}
	if guard && !guards.returned[function] {
		guards.returned[function] = true
		guards.returns(file, function)
	}
	// a parameter the parser could not read (function gate(a.b)) is left for the compiler to refuse
	if index >= len(function.Parameters) || function.Parameters[index] == nil {
		return
	}
	step := routerHanded{function: function, index: index}
	if guards.seen[step] {
		return
	}
	guards.seen[step] = true
	// a function written in place sees the names declared where it is written; any other, only its own
	declared := map[string]bool(nil)
	if _, inPlace := callee.(*FunctionLiteral); inPlace {
		declared = outer
	}
	names := map[string]bool{function.Parameters[index].Value: true}
	contextWalk(function.Body, names, withParameters(declared, function), func(node Node, names, declared map[string]bool) {
		switch n := node.(type) {
		case *MethodCallExpression:
			if n.Method == nil || !contextRooted(n.Object, names) {
				return
			}
			written := n.Method.Value
			switch method := strings.ToLower(written); {
			case method == "view":
				guards.fail(file, n.Method.Token, ContextViewInGuard(receiverText(n.Object)+"."+written+"(…)"))
			case guardRefusedAnswers[method]:
				guards.fail(file, n.Method.Token, ContextAnswerInGuard(receiverText(n.Object)+"."+written+"(…)"))
			case method == "json" && !statusInChain(n.Object):
				guards.fail(file, n.Method.Token, ContextAnswerInGuard(receiverText(n.Object)+"."+written+"(…) không có status trong chuỗi"))
			}
		case *CallExpression:
			for at, argument := range n.Arguments {
				if contextRooted(argument, names) {
					guards.hand(file, n.Function, at, declared, false)
				}
			}
		}
	})
}

// statusInChain reports a status written in the chain of edits an answer stands on: context.status(401).json(…),
// context.status(401).header(…).json(…).
func statusInChain(e Expression) bool {
	for depth := 0; depth < 1024; depth++ {
		switch node := e.(type) {
		case *ChainExpression:
			e = node.Expression
		case *MethodCallExpression:
			if node.Method == nil || !contextModifiers[strings.ToLower(node.Method.Value)] {
				return false
			}
			if strings.EqualFold(node.Method.Value, "status") {
				return true
			}
			e = node.Object
		default:
			return false
		}
	}
	return false
}

// returns refuses a value the guard function returns as written: a string, a number, a template, an object, an
// array, a function. A boolean, null and nothing are its answers (go on, 403); a name or a call is read as it runs
// (work/router_serve.go). The functions written in it return to them, not to the router.
func (guards *guardReader) returns(file *guardFile, function *FunctionLiteral) {
	var visit func(Node)
	visit = func(node Node) {
		switch n := node.(type) {
		case *FunctionLiteral:
			if n != function {
				return
			}
		case *ReturnStatement:
			if kind, at := returnedKind(n.ReturnValue); kind != "" {
				guards.fail(file, Token{Position: startPosition(at), Source: getNodeSource(at)}, GuardReturnsValue(kind))
			}
		}
		for _, child := range nodeChildren(node) {
			visit(child)
		}
	}
	visit(function)
}

// returnedKind is what a value written in a return may be, as an error says it, and the value written: a literal
// on its own, or one a ternary or a logical operator may give (cond ? "đóng" : true, ok || "đóng"), arithmetic and
// a sign (always a number, + a number or a string), typeof (a string); "" for a value a guard may return (a
// boolean, null) or one only its run tells (a name, a call).
func returnedKind(e Expression) (string, Expression) {
	switch node := e.(type) {
	case *Literal:
		switch {
		case node.Value.IsString():
			return "một chuỗi", node
		case node.Value.IsNumber():
			return "một số", node
		}
	case *TemplateLiteral:
		return "một chuỗi", node
	case *ObjectLiteral:
		return "một object", node
	case *ArrayLiteral:
		return "một mảng", node
	case *FunctionLiteral:
		return "một hàm", node
	case *TernaryExpression:
		if kind, at := returnedKind(node.Consequence); kind != "" {
			return kind, at
		}
		return returnedKind(node.Alternative)
	case *PrefixExpression:
		switch node.Operator {
		case "-", "+":
			return "một số", node
		case "typeof":
			return "một chuỗi", node
		}
	case *InfixExpression:
		switch node.Operator {
		case "||", "&&", "??":
			if kind, at := returnedKind(node.Right); kind != "" {
				return kind, at
			}
			return returnedKind(node.Left)
		case "-", "*", "/", "%", "**":
			return "một số", node
		case "+":
			left, _ := returnedKind(node.Left)
			right, _ := returnedKind(node.Right)
			switch {
			case left == "một chuỗi" || right == "một chuỗi":
				return "một chuỗi", node
			case left == "một số" && right == "một số":
				return "một số", node
			}
			// + of two names or calls gives a number or a string, never a boolean or null
			return "một số hay một chuỗi", node
		}
	}
	return "", nil
}

// function is the function callee is, and the file it is written in: one written there, or one a name of file
// stands for (resolve); nil when it is none the compiler can follow (a function a call makes, one read off an
// object, a name the file does not declare).
func (guards *guardReader) function(file *guardFile, callee Expression) (*guardFile, *FunctionLiteral) {
	switch node := callee.(type) {
	case *FunctionLiteral:
		return file, node
	case *Identifier:
		return guards.resolve(file, node.Value)
	}
	return nil, nil
}

// resolve is the function name stands for in file, and the file it is written in: a top-level const of a
// function, through a const of another name (const gate = draw, export default gate), and a name imported
// from another file through the files that export it on. A name met twice in one file ends the search, so a
// cycle of names or of re-exports ends it too (the bundler then says import cycle).
func (guards *guardReader) resolve(file *guardFile, name string) (*guardFile, *FunctionLiteral) {
	type step struct {
		file *guardFile
		name string
	}
	visited := map[step]bool{}
	for file != nil && !visited[step{file, name}] {
		visited[step{file, name}] = true
		if function := file.functions[name]; function != nil {
			return file, function
		}
		if other, isAlias := file.aliases[name]; isAlias {
			name = other
			continue
		}
		imported, isImported := file.imports[name]
		if !isImported {
			return nil, nil
		}
		module := guards.load(file, imported.statement)
		if module == nil {
			return nil, nil
		}
		local, exported := module.exports[imported.imported]
		if !exported {
			return nil, nil
		}
		file, name = module, local
	}
	return nil, nil
}

// load reads the file an import of from names, once; nil when it cannot be read or parsed (the bundler says
// why when it bundles it).
func (guards *guardReader) load(from *guardFile, statement *ImportStatement) *guardFile {
	if from.dir == "" {
		return nil
	}
	path, err := guards.resolver.resolve(statement, from.dir)
	if err != nil {
		return nil
	}
	if file, read := guards.files[path]; read {
		return file
	}
	guards.files[path] = nil
	text, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	name := guards.resolver.sourceName(path)
	prog, err := parseProgramSource(string(text), name)
	if err != nil {
		return nil
	}
	file := newGuardFile(name, text, filepath.Dir(path), prog)
	guards.files[path] = file
	return file
}

// fail refuses at, in file: an error of the router file at its place, one of another file at that file's place
// and told from the guard that runs it.
func (guards *guardReader) fail(file *guardFile, at Token, message string) {
	if file == guards.entry {
		guards.reader.fail(at, message)
		return
	}
	guards.reader.failures = append(guards.reader.failures, routerFailure{
		at:      guards.anchor,
		where:   textLocation(file.text, at),
		message: fmt.Sprintf("%s (guard ở %s)", message, textLocation(guards.reader.text, guards.anchor)),
	})
}
