package compiler

import (
	"fmt"
	"strings"
)

// kitwork/routine (KITWORK_ROUTINE.md): go() starts and goes on, wait() starts and waits, or waits for what go()
// started; cancel() asks one to stop; retry(n) and timeout(…) come before them.
//
//	import * as routine from "kitwork/routine";
//	const [users, products] = routine.wait(() => loadUsers(), () => loadProducts());
//	routine.go(() => sendWelcome(email));
//
// The resolver refuses here, where they are written, the go of before (go(fn) bare, import { go } from
// "kitwork", kitwork().go), a word the module has not, a function of go that uses the request (it runs after
// the request is over), and a wait that mixes functions with handles or puts rules on a handle. The module
// refuses the same when it runs (work/routine.go), for a name the compiler could not follow. Every check
// only ever fails: a program that writes none of them resolves and compiles byte for byte as before.

// RoutineGoMoved is the error of go(fn) written bare (mục 2): go is a word of kitwork/routine, no longer a
// global.
const RoutineGoMoved = "`go` nằm trong `kitwork/routine`: `import * as routine from \"kitwork/routine\"` rồi `routine.go(fn)`"

// RoutineGoRemoved is the error of go read off "kitwork" (import { go } from "kitwork", kitwork().go), which
// ran a function in the background until 22/10/2026.
const RoutineGoRemoved = `go của "kitwork" đã bỏ: ` + RoutineGoMoved

// RoutineWaitMixed is the error of a wait given functions and handles at once (mục 4).
const RoutineWaitMixed = "routine.wait nhận các hàm hay các handle, không trộn hai loại: routine.go(hàm) trước rồi routine.wait(a, b)"

// RoutineRulesOnHandle is the error of retry or timeout before a wait of handles (mục 5).
const RoutineRulesOnHandle = "routine.retry(…) và routine.timeout(…) chỉ áp cho việc mà lời gọi bắt đầu: handle đã chạy với luật của nó từ lúc go(); viết routine.retry(2).go(fn) rồi routine.wait(handle)"

// routineGoRequest is the error of a function of go that uses the request (mục 3).
const routineGoRequest = "hàm của routine.go dùng %s của request: `go` chạy sau khi request xong: lấy giá trị cần trước rồi đưa vào"

// routineWords are the words of kitwork/routine.
var routineWords = map[string]bool{"go": true, "wait": true, "cancel": true, "retry": true, "timeout": true}

// routineRequestNames are the names a handler gives the request, as its parameters.
var routineRequestNames = map[string]bool{"context": true, "request": true, "response": true}

// RoutineUnknownWord is the error of a word kitwork/routine has not (routine.run, routine.spawn).
func RoutineUnknownWord(word string) string {
	return fmt.Sprintf("routine.%s không có: kitwork/routine có go, wait, cancel, retry, timeout; không có run, spawn, async, await (KITWORK_ROUTINE.md mục 1)", word)
}

// RoutineArgumentNotAFunction is the error of an argument of go or wait that is not a function (nor, for
// wait, a handle).
func RoutineArgumentNotAFunction(word, given string, position int) string {
	if word == "wait" {
		return fmt.Sprintf("routine.wait nhận hàm hay handle của routine.go, không nhận %s (đối số thứ %d): viết routine.wait(() => …)", given, position)
	}
	return fmt.Sprintf("routine.%s nhận hàm, không nhận %s (đối số thứ %d): viết routine.%s(() => …)", word, given, position, word)
}

// markRoutine marks a declared variable as the module kitwork/routine (import * as routine from
// "kitwork/routine", import { routine } from "kitwork"), a word of it bound by name (import { go } from
// "kitwork/routine"), or a const holding what a go() gave (a handle, or handles taken apart).
func (r *resolver) markRoutine(node *VarStatement, name *Identifier, declared *binding) {
	if declared == nil {
		return
	}
	if node.Pattern == nil {
		if module, ok := kitworkModulePath(node.Value); ok {
			declared.routineModule = declared.routineModule ||
				(node.DestructMode == DestructNone && module == "routine") ||
				(node.DestructMode == DestructObject && module == "" && name.Value == "routine")
			if node.DestructMode == DestructObject && module == "routine" && routineWords[name.Value] {
				declared.routineWord = name.Value
			}
		} else if member, isMember := node.Value.(*MemberExpression); isMember && member.Property != nil {
			if module, ok := kitworkModulePath(member.Object); ok && module == "routine" && routineWords[member.Property.Value] {
				declared.routineWord = member.Property.Value // import { go as start } from "kitwork/routine"
			}
		}
	}
	if node.Token.Kind == Const && r.routineStart(node.Value) {
		declared.routineHandle = true
	}
	// ({ request, response }) => …: the names a destructured parameter gives are read off it as it enters
	// (objectParameter), under the pattern as written, a name no source declares
	if node.Pattern != nil {
		if source, isName := node.Value.(*Identifier); isName && strings.HasPrefix(source.Value, "{") {
			if found := r.lookup(source.Value); found != nil && found.kind == bindingParameter {
				declared.parameterPart = true
			}
		}
	}
}

// routineDeclaration refuses an import of go off "kitwork" (import { go } from "kitwork", const { go } =
// kitwork()), and of a word kitwork/routine has not (import { run } from "kitwork/routine", const { run } =
// routine).
func (r *resolver) routineDeclaration(node *VarStatement) {
	refuse := func(word *Identifier, module string) bool {
		switch {
		case module == "" && word.Value == "go":
			r.fail(fmt.Errorf("%s: %s", r.where(word.Token), RoutineGoRemoved))
			return true
		case module == "routine" && !routineWords[word.Value]:
			r.fail(fmt.Errorf("%s: %s", r.where(word.Token), RoutineUnknownWord(word.Value)))
			return true
		}
		return false
	}
	// the words the declaration takes off a module: a pattern's keys, or the names an import lowers to
	module, isModule := kitworkModulePath(node.Value)
	if namespace, isName := node.Value.(*Identifier); isName {
		if found := r.lookup(namespace.Value); found != nil && found.routineModule {
			module, isModule = "routine", true
		}
	}
	var words []*Identifier
	switch {
	case node.Pattern != nil && !node.Pattern.Array && isModule:
		for _, element := range node.Pattern.Elements {
			words = append(words, &Identifier{Token: element.KeyToken, Value: element.Key})
		}
	case node.Pattern == nil && isModule && node.DestructMode == DestructObject:
		words = node.Names
	case node.Pattern == nil:
		if member, isMember := node.Value.(*MemberExpression); isMember && member.Property != nil {
			if inner, ok := kitworkModulePath(member.Object); ok && inner == "routine" {
				refuse(member.Property, "routine")
			}
		}
		return
	}
	for _, word := range words {
		if refuse(word, module) {
			return
		}
	}
}

// routineMember refuses, where it is written, kitwork().go (import { go as start } from "kitwork" and
// import * as go from "kitwork/go" lower to it) and a word read off the module that it has not (routine.run).
func (r *resolver) routineMember(object Expression, property *Identifier) bool {
	if property == nil {
		return false
	}
	if isKitworkCall(object) && property.Value == "go" {
		r.fail(fmt.Errorf("%s: %s", r.where(property.Token), RoutineGoRemoved))
		return true
	}
	if isModule, _ := r.routineChain(object); isModule && !routineWords[property.Value] {
		r.fail(fmt.Errorf("%s: %s", r.where(property.Token), RoutineUnknownWord(property.Value)))
		return true
	}
	return false
}

// routineChain reports e as the module kitwork/routine, or a builder of it (routine.retry(2), retry(2) of a
// word imported by name); rules is true for a builder, which carries retry or timeout.
func (r *resolver) routineChain(e Expression) (isModule, rules bool) {
	switch node := e.(type) {
	case *Identifier:
		found := r.lookup(node.Value)
		return found != nil && found.routineModule, false
	case *MemberExpression:
		module, ok := kitworkModulePath(node)
		return ok && module == "routine", false
	case *MethodCallExpression:
		if node.Method != nil && (node.Method.Value == "retry" || node.Method.Value == "timeout") {
			if isModule, _ := r.routineChain(node.Object); isModule {
				return true, true
			}
		}
	case *CallExpression:
		if callee, isName := node.Function.(*Identifier); isName {
			if found := r.lookup(callee.Value); found != nil && (found.routineWord == "retry" || found.routineWord == "timeout") {
				return true, true
			}
		}
	}
	return false, false
}

// routineStart reports e as a go() of the module: what it gives is a handle, or handles.
func (r *resolver) routineStart(e Expression) bool {
	switch node := e.(type) {
	case *MethodCallExpression:
		if node.Method == nil || node.Method.Value != "go" {
			return false
		}
		isModule, _ := r.routineChain(node.Object)
		return isModule
	case *CallExpression:
		callee, isName := node.Function.(*Identifier)
		if !isName {
			return false
		}
		found := r.lookup(callee.Value)
		return found != nil && found.routineWord == "go"
	}
	return false
}

// routineHandleArgument reports e as certainly a handle: a go() written there, or a const that holds one.
func (r *resolver) routineHandleArgument(e Expression) bool {
	if name, isName := e.(*Identifier); isName {
		found := r.lookup(name.Value)
		return found != nil && found.routineHandle
	}
	return r.routineStart(e)
}

// routineCall checks a call of the module's words written after a dot (routine.go(…), routine.retry(2).wait(…),
// kitwork().go(…)); true when it failed.
func (r *resolver) routineCall(node *MethodCallExpression) bool {
	if node.Method == nil {
		return false
	}
	if r.routineMember(node.Object, node.Method) {
		return true
	}
	isModule, rules := r.routineChain(node.Object)
	if !isModule {
		return false
	}
	return r.routineArguments(node.Method.Value, node.Method.Token, rules, node.Arguments)
}

// routineNamedCall checks a call of a word imported by name (go(fn) after import { go } from
// "kitwork/routine"); true when it failed.
func (r *resolver) routineNamedCall(node *CallExpression) bool {
	callee, isName := node.Function.(*Identifier)
	if !isName {
		return false
	}
	found := r.lookup(callee.Value)
	if found == nil || found.routineWord == "" {
		return false
	}
	return r.routineArguments(found.routineWord, callee.Token, false, node.Arguments)
}

// routineArguments checks the arguments of go, wait and cancel, and marks each function handed to go, whose
// use of the request reference() refuses; true when it failed.
func (r *resolver) routineArguments(word string, at Token, rules bool, arguments []Expression) bool {
	fail := func(token Token, message string) bool {
		r.fail(fmt.Errorf("%s: %s", r.where(token), message))
		return true
	}
	switch word {
	case "go":
		if len(arguments) == 0 {
			return fail(at, "routine.go nhận ít nhất một hàm: routine.go(() => …)")
		}
		for index, argument := range arguments {
			if literal, isFunction := argument.(*FunctionLiteral); isFunction {
				if r.routineGo == nil {
					r.routineGo = map[*FunctionLiteral]bool{}
				}
				r.routineGo[literal] = true
				continue
			}
			if r.routineHandleArgument(argument) {
				return fail(at, "routine.go nhận hàm, không nhận handle: handle đã chạy, đợi nó bằng routine.wait(handle)")
			}
			if certainlyNotAFunction(argument) {
				return fail(at, RoutineArgumentNotAFunction("go", "giá trị tính xong trước khi routine chạy", index+1))
			}
		}
	case "wait":
		if len(arguments) == 0 {
			return fail(at, "routine.wait nhận ít nhất một hàm hay một handle: routine.wait(() => …), routine.wait(handle)")
		}
		functions, handles := false, false
		for index, argument := range arguments {
			switch {
			case isFunctionLiteral(argument):
				functions = true
			case r.routineHandleArgument(argument):
				handles = true
			case certainlyNotAFunction(argument):
				return fail(at, RoutineArgumentNotAFunction("wait", "giá trị tính xong trước khi routine chạy", index+1))
			}
		}
		if functions && handles {
			return fail(at, RoutineWaitMixed)
		}
		if handles && rules {
			return fail(at, RoutineRulesOnHandle)
		}
	case "cancel":
		if rules {
			return fail(at, "routine.cancel không đi sau retry hay timeout: cancel chỉ xin dừng, routine.cancel(handle)")
		}
		if len(arguments) == 0 {
			return fail(at, "routine.cancel nhận handle của routine.go: const task = routine.go(fn); routine.cancel(task)")
		}
		for _, argument := range arguments {
			if isFunctionLiteral(argument) {
				return fail(at, "routine.cancel nhận handle của routine.go, không nhận hàm: const task = routine.go(fn); routine.cancel(task)")
			}
		}
	}
	return false
}

func isFunctionLiteral(e Expression) bool {
	_, ok := e.(*FunctionLiteral)
	return ok
}

// routineRequest refuses name, read in a function handed to go, when it is the request a handler around
// was given (mục 3): a parameter of a function outside the outermost such function, named context,
// request or response, or one of those names a destructured parameter gives (({ request }) => …). A
// variable of another kind is not the request's by its name (const response = http.get(url).send(), a
// const request of the file): the compiler leaves it, and the copy a routine makes refuses an object of
// the request whatever holds it when the routine runs (work/routine_snapshot.go).
func (r *resolver) routineRequest(name *Identifier, found *binding) bool {
	if len(r.goFunctions) == 0 || found == nil || !routineRequestNames[name.Value] ||
		(found.kind != bindingParameter && !found.parameterPart) {
		return false
	}
	outermost := r.goFunctions[0]
	for scope := found.scope.function; scope != nil; scope = scope.parent {
		if scope == outermost {
			return false
		}
	}
	r.fail(fmt.Errorf("%s: "+routineGoRequest, r.where(name.Token), name.Value))
	return true
}

// checkBareGo makes go(fn) written bare a compile error (mục 2): until 22/10/2026 the language proposed it
// as a global, and no global answers it any more. A go the scope of the call declares (import { go } from
// "kitwork/routine", const go = …) is that one; a go another file of the bundle declares is that file's,
// and leaves this call bare. A program that writes a global go itself (go = …) keeps it.
func (c *Compiler) checkBareGo(n *CallExpression) error {
	callee, ok := n.Function.(*Identifier)
	if !ok || callee.Value != "go" || c.variable(callee) != nil || (c.scopes != nil && c.scopes.writesGo) {
		return nil
	}
	return fmt.Errorf("%s: %s", locationText(c.getSourceLocation(callee.Token.Source, callee.Token.Position)), RoutineGoMoved)
}
