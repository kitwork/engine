package compiler

import (
	"fmt"
	"strings"
)

// environment of "kitwork" (KITWORK_ENVIRONMENT.md, 22/10/2026): an app reads its environment by name,
//
//	import { environment } from "kitwork";
//	const stripe = environment.require("STRIPE_SECRET");     // missing: the boot fails
//	const port = environment.PORT || 8080;                    // "8080" in .env is 8080
//
// and the compiler sees every name it reads (mục 3), which keeps the whole set out of any program's reach and
// lets the boot check each require before a request comes. The resolver refuses here, where they are written:
//   - any use of environment but environment.NAME and environment.require("NAME"): a write, another name for
//     the whole (const e = environment), handing it to a function (Object.keys(environment)), going through it
//     (for … of), an index (environment[key]), taking it apart (const { PORT } = environment);
//   - a word of the module "kitwork" read by [ ] with a name the compiler cannot read, or with "environment"
//     (kitwork()[name], k[name] with k the module), by which environment would reach a program whole;
//   - environment.require of a name not written as a string;
//   - env, its name until 22/10/2026, in a file of an app (import { env } from "kitwork", kitwork().env); a
//     manifest compiles with no app and keeps its import { env } (mục 9.8);
//   - a key not named PUBLIC_… that goes to the page: anywhere in the object a .view({ … }) hands it, however
//     deep, or held by a variable of a function a .view(…) runs, written there or passed by its name, whose
//     every variable is the page's (mục 6).
//
// A const or a let of the program's top level that holds such a key, an import of one from a module of the
// app included, is not the page's either: the engine leaves it out of what a router.view hands its page
// (Bytecode.EnvironmentServerOnly). Every check only ever fails: a program that writes none of them resolves
// and compiles byte for byte as before.

// EnvironmentRenamed is the error of env read off "kitwork" in a file of an app.
const EnvironmentRenamed = "`env` đã đổi tên: import { environment } from \"kitwork\", rồi environment.KEY, environment.require(\"KEY\") (KITWORK_ENVIRONMENT.md mục 11)"

// EnvironmentByName is the error of environment used other than by the name of a key.
const EnvironmentByName = `environment chỉ đọc theo tên: viết environment.TÊN hay environment.require("TÊN"); không đặt tên khác cho cả bộ, không truyền đi, không duyệt, không tách, không đọc bằng [ ] (KITWORK_ENVIRONMENT.md mục 3)`

// KitworkByName is the error of a word of the module "kitwork" read by [ ] with a name the compiler cannot
// read: environment would reach the program that way, past every check of a key's name.
const KitworkByName = `module "kitwork" chỉ đọc theo tên: viết kitwork.TÊN, hay import { TÊN } from "kitwork"; đọc bằng [ ] thì compiler không thấy tên, environment đi qua mà không ai kiểm (KITWORK_ENVIRONMENT.md mục 3)`

// EnvironmentRequireLiteral is the error of environment.require given anything but one key written as a string.
const EnvironmentRequireLiteral = `environment.require nhận đúng một tên khoá viết thẳng là chuỗi: environment.require("STRIPE_SECRET"); boot kiểm mọi khoá require theo đúng tên đó (KITWORK_ENVIRONMENT.md mục 3)`

// EnvironmentReadOnly is the error of a write to environment.
func EnvironmentReadOnly(key string) string {
	if key == "" {
		return `environment chỉ đọc: không gán vào environment; giá trị đặt trong .env của app (KITWORK_ENVIRONMENT.md mục 3)`
	}
	return fmt.Sprintf(`environment chỉ đọc: không gán environment.%s; giá trị đặt trong .env của app (KITWORK_ENVIRONMENT.md mục 3)`, key)
}

// EnvironmentToBrowser is the error of a key not named PUBLIC_… that would go down to the browser.
func EnvironmentToBrowser(key string) string {
	return fmt.Sprintf("khoá `%s` sẽ đi xuống trình duyệt; khoá công khai đặt tên `PUBLIC_…`, khoá bí mật dùng ở server rồi chỉ đưa trang kết quả (KITWORK_ENVIRONMENT.md mục 6)", key)
}

// EnvironmentPublicPrefix begins the name of a key a page may receive.
const EnvironmentPublicPrefix = "PUBLIC_"

// NameUse is a key of the environment a program names, and where: environment.require("STRIPE_SECRET") at
// router.work:3:35.
type NameUse struct {
	Name   string
	File   string
	Line   int
	Column int
}

// Place is file:line:column, as compile errors write it.
func (u NameUse) Place() string {
	if u.File == "" {
		return fmt.Sprintf("%d:%d", u.Line, u.Column)
	}
	return fmt.Sprintf("%s:%d:%d", u.File, u.Line, u.Column)
}

// markEnvironment marks a declared variable as environment itself (import { environment } from "kitwork",
// import { environment as e } from "kitwork", const { environment } = kitwork, kitwork namespaced), as the
// module "kitwork" itself (import * as kitwork from "kitwork", const k = kitwork(), a const of either), with
// what it holds of keys not named PUBLIC_… (environmentHolds), and a const with the function it is declared
// with.
func (r *resolver) markEnvironment(node *VarStatement, name *Identifier, declared *binding) {
	if declared == nil {
		return
	}
	if node.Pattern == nil {
		if module, ok := kitworkModulePath(node.Value); ok {
			switch {
			case module == "" && node.DestructMode == DestructObject && name.Value == "environment":
				declared.kitworkEnvironment = true
			case module == "" && node.DestructMode == DestructNone:
				declared.kitworkRoot = true
			case module == "environment" && node.DestructMode == DestructNone:
				declared.kitworkEnvironment = true
			}
		}
		if !holdsOne(node) {
			return
		}
		declared.environmentHeld = r.environmentHolds(node.Value)
		if node.Token.Kind == Const {
			if source, isName := node.Value.(*Identifier); isName {
				// const k2 = k, another name for the module: read as the module is
				if found := r.lookup(source.Value); found != nil && found.kitworkRoot {
					declared.kitworkRoot = true
				}
			}
			if function, isFunction := node.Value.(*FunctionLiteral); isFunction {
				declared.function = function
			}
		}
		return
	}
	// const { environment } = kitwork, kitwork the module itself
	if source, isName := node.Value.(*Identifier); isName && !node.Pattern.Array {
		if found := r.lookup(source.Value); found != nil && found.kitworkRoot {
			for _, element := range node.Pattern.Elements {
				if element.Key == "environment" && element.Name == name {
					declared.kitworkEnvironment = true
				}
			}
		}
	}
}

// environmentDeclaration refuses env taken off "kitwork" in a file of an app: import { env } from "kitwork"
// (const { env } = kitwork()), import { env as e } from "kitwork" and import * as e from "kitwork/env"
// (const e = kitwork().env), const { env } = kitwork with kitwork the module.
func (r *resolver) environmentDeclaration(node *VarStatement) {
	if !r.inApp {
		return
	}
	if node.Pattern != nil {
		source, isName := node.Value.(*Identifier)
		if !isName || node.Pattern.Array {
			return
		}
		if found := r.lookup(source.Value); found == nil || !found.kitworkRoot {
			return
		}
		for _, element := range node.Pattern.Elements {
			if element.Key == "env" {
				r.fail(fmt.Errorf("%s: %s", r.where(element.KeyToken), EnvironmentRenamed))
				return
			}
		}
		return
	}
	module, ok := kitworkModulePath(node.Value)
	if !ok {
		return
	}
	switch {
	case module == "" && node.DestructMode == DestructObject:
		for _, name := range node.Names {
			if name.Value == "env" {
				r.fail(fmt.Errorf("%s: %s", r.where(name.Token), EnvironmentRenamed))
				return
			}
		}
	case module == "env" && len(node.Names) > 0:
		r.fail(fmt.Errorf("%s: %s", r.where(node.Names[0].Token), EnvironmentRenamed))
	}
}

// environmentObject reports e as environment: a name bound to it, environment of the module "kitwork"
// (kitwork.environment, kitwork().environment).
func (r *resolver) environmentObject(e Expression) bool {
	switch node := e.(type) {
	case *Identifier:
		found := r.lookup(node.Value)
		return found != nil && found.kitworkEnvironment
	case *MemberExpression:
		return node.Property != nil && node.Property.Value == "environment" && r.kitworkModule(node.Object)
	}
	return false
}

// kitworkModule reports e as the module "kitwork" itself: kitwork(), or a name bound to it.
func (r *resolver) kitworkModule(e Expression) bool {
	if isKitworkCall(e) {
		return true
	}
	if name, isName := e.(*Identifier); isName {
		found := r.lookup(name.Value)
		return found != nil && found.kitworkRoot
	}
	return false
}

// useEnvironment resolves environment where it is read by name: the next reference of its binding is let
// through.
func (r *resolver) useEnvironment(object Expression) {
	switch node := object.(type) {
	case *Identifier:
		r.environmentUse = true
		r.reference(node, false)
		r.environmentUse = false
	case *MemberExpression:
		r.expression(node.Object) // kitwork(), or the name of the module
	}
}

// environmentMember reads environment.NAME, and refuses environment of "kitwork" read whole
// (kitwork.environment, kitwork().environment) and env of "kitwork" in a file of an app.
func (r *resolver) environmentMember(node *MemberExpression) bool {
	if node.Property == nil {
		return false
	}
	if r.environmentObject(node.Object) {
		r.useEnvironment(node.Object)
		return true
	}
	if !r.kitworkModule(node.Object) {
		return false
	}
	switch {
	case node.Property.Value == "environment":
		r.fail(fmt.Errorf("%s: %s", r.where(node.Property.Token), EnvironmentByName))
		return true
	case node.Property.Value == "env" && r.inApp:
		r.fail(fmt.Errorf("%s: %s", r.where(node.Property.Token), EnvironmentRenamed))
		return true
	}
	return false
}

// environmentCall reads environment.require("NAME"), which the boot checks (resolution.requires), and
// environment.NAME(…), which calls a value: NOT_A_FUNCTION when it runs (mục 3).
func (r *resolver) environmentCall(node *MethodCallExpression) bool {
	if node.Method == nil || !r.environmentObject(node.Object) {
		return false
	}
	r.useEnvironment(node.Object)
	if node.Method.Value != "require" {
		for _, argument := range node.Arguments {
			r.expression(argument)
		}
		return true
	}
	key, literal := environmentKey(node)
	if !literal {
		at := node.Method.Token
		if len(node.Arguments) > 0 {
			at = expressionToken(node.Arguments[0])
		}
		r.fail(fmt.Errorf("%s: %s", r.where(at), EnvironmentRequireLiteral))
		return true
	}
	location := r.compiler.getSourceLocation(node.Arguments[0].(*Literal).Token.Source, node.Arguments[0].(*Literal).Token.Position)
	r.result.requires = append(r.result.requires, NameUse{
		Name: key, File: location.File, Line: int(location.Line), Column: int(location.Column),
	})
	return true
}

// environmentKey is the key of environment.require("KEY"): literal is false unless it has one argument, a
// string written as it is.
func environmentKey(node *MethodCallExpression) (key string, literal bool) {
	if len(node.Arguments) != 1 {
		return "", false
	}
	argument, isLiteral := node.Arguments[0].(*Literal)
	if !isLiteral || !argument.Value.IsString() {
		return "", false
	}
	return argument.Value.Text(), true
}

// environmentTarget refuses a write to environment: environment.KEY = x, environment.KEY++, environment[k] = x.
func (r *resolver) environmentTarget(target Expression) bool {
	switch node := target.(type) {
	case *MemberExpression:
		if r.environmentObject(node.Object) && node.Property != nil {
			r.fail(fmt.Errorf("%s: %s", r.where(node.Property.Token), EnvironmentReadOnly(node.Property.Value)))
			return true
		}
	case *IndexExpression:
		if r.environmentObject(node.Left) {
			r.fail(fmt.Errorf("%s: %s", r.where(node.Token), EnvironmentReadOnly("")))
			return true
		}
	}
	return false
}

// environmentHeld is what a value holds of keys not named PUBLIC_…: key is the first such key anywhere in
// it, "" for none; members, for an object whose every key is written as a name (an object literal, what a
// module of the app exports), is what each member holds, so that config.host holds only what host does.
// members is nil for any other value, a member of which holds what the whole does.
type environmentHeld struct {
	key     string
	members map[string]environmentHeld
}

// member is what a member of the value holds: its own when the value is an object known by its names, the
// whole's otherwise (an item of an array, a member of a key read off environment).
func (held environmentHeld) member(name string) environmentHeld {
	if held.members != nil {
		return held.members[name]
	}
	return environmentHeld{key: held.key}
}

// holdsOne reports a declaration of one name with a value, by const or let: what the value holds is what
// the name holds.
func holdsOne(node *VarStatement) bool {
	return node.Pattern == nil && node.DestructMode == DestructNone && len(node.Names) == 1 &&
		node.Value != nil && (node.Token.Kind == Const || node.Token.Kind == Let)
}

// holdEnvironment reads again, once the value of a declaration is resolved, what its name holds: a module
// of the app is a function resolved only then, so what an import of it holds is known only then
// (import { stripe } from "app/.shared/keys.work" is const stripe = __kw_mod_1.stripe).
func (r *resolver) holdEnvironment(node *VarStatement) {
	if r.err != nil || !holdsOne(node) {
		return
	}
	declared := r.result.slots[node.Names[0]]
	if declared == nil {
		return
	}
	if held := r.environmentHolds(node.Value); held.key != "" || held.members != nil {
		declared.environmentHeld = held
	}
}

// environmentHolds is what a value holds of keys not named PUBLIC_…, as the compiler sees it written: a read
// (environment.STRIPE_SECRET, environment.require("STRIPE_SECRET")), one with a default (||, ??, a ? b : c),
// text made with it (+, a template), an object or an array with one anywhere in it (a spread too), a member
// or an item of such a value, a variable that holds one, what a function run at once returns (a module of
// the app: import { stripe } from "app/.shared/keys.work"). Nothing for any other value: a call and a
// comparison make something else of a key (environment.SECRET != null is a yes or a no).
func (r *resolver) environmentHolds(e Expression) environmentHeld {
	switch node := e.(type) {
	case *MemberExpression:
		if node.Property == nil {
			return environmentHeld{}
		}
		if r.environmentObject(node.Object) {
			if strings.HasPrefix(node.Property.Value, EnvironmentPublicPrefix) {
				return environmentHeld{}
			}
			return environmentHeld{key: node.Property.Value}
		}
		return r.environmentHolds(node.Object).member(node.Property.Value)
	case *IndexExpression:
		whole := r.environmentHolds(node.Left)
		if key, isLiteral := node.Index.(*Literal); isLiteral && key.Value.IsString() {
			return whole.member(key.Value.Text())
		}
		return environmentHeld{key: whole.key}
	case *MethodCallExpression:
		if node.Method != nil && node.Method.Value == "require" && r.environmentObject(node.Object) {
			if key, literal := environmentKey(node); literal && !strings.HasPrefix(key, EnvironmentPublicPrefix) {
				return environmentHeld{key: key}
			}
		}
	case *CallExpression:
		if function, isFunction := node.Function.(*FunctionLiteral); isFunction && len(node.Arguments) == 0 {
			return r.environmentReturned(function)
		}
	case *InfixExpression:
		switch node.Operator {
		case "||", "??", "+":
			return r.environmentFirst(node.Left, node.Right)
		}
	case *TernaryExpression:
		return r.environmentFirst(node.Consequence, node.Alternative)
	case *ChainExpression:
		return r.environmentHolds(node.Expression)
	case *SpreadExpression:
		return r.environmentHolds(node.Value)
	case *Identifier:
		if found := r.resolvedBinding(node); found != nil {
			return found.environmentHeld
		}
	case *TemplateLiteral:
		return r.environmentFirst(node.Parts...)
	case *ArrayLiteral:
		return r.environmentFirst(node.Elements...)
	case *ObjectLiteral:
		return r.environmentObjectHolds(node)
	}
	return environmentHeld{}
}

// environmentFirst is the first key any of the values holds; what each member holds is not kept.
func (r *resolver) environmentFirst(values ...Expression) environmentHeld {
	for _, value := range values {
		if key := r.environmentHolds(value).key; key != "" {
			return environmentHeld{key: key}
		}
	}
	return environmentHeld{}
}

// environmentObjectHolds is what an object literal holds: the first key any entry holds, and each member by
// its name while every entry that holds a key has its name written ({ [name]: environment.SECRET } is an
// object of unknown members).
func (r *resolver) environmentObjectHolds(node *ObjectLiteral) environmentHeld {
	held := environmentHeld{members: map[string]environmentHeld{}}
	for _, entry := range node.Entries {
		inner := r.environmentHolds(entry.Value)
		if held.key == "" {
			held.key = inner.key
		}
		if held.members == nil {
			continue
		}
		if entry.IsSpread {
			if inner.members == nil {
				if inner.key != "" {
					held.members = nil
				}
				continue
			}
			for name, member := range inner.members {
				held.members[name] = member
			}
			continue
		}
		name, _, named := objectKeyName(entry.Key)
		if !named {
			if inner.key != "" {
				held.members = nil
			}
			continue
		}
		held.members[name] = inner
	}
	if held.key == "" {
		return environmentHeld{}
	}
	return held
}

// environmentReturned is what a function run at once returns (const __kw_mod_1 = (() => { …; return { stripe:
// stripe }; })(), each module of the bundle), read off the return that ends its body once it is resolved.
func (r *resolver) environmentReturned(function *FunctionLiteral) environmentHeld {
	if r.functionScopes[function] == nil || function.Body == nil || len(function.Body.Statements) == 0 {
		return environmentHeld{}
	}
	returned, isReturn := function.Body.Statements[len(function.Body.Statements)-1].(*ReturnStatement)
	if !isReturn || returned.ReturnValue == nil {
		return environmentHeld{}
	}
	return r.environmentHolds(returned.ReturnValue)
}

// resolvedBinding is the variable a name reads: the one the resolver gave it, else the one in scope now.
func (r *resolver) resolvedBinding(name *Identifier) *binding {
	if found := r.result.slots[name]; found != nil {
		return found
	}
	return r.lookup(name.Value)
}

// environmentView refuses a key not named PUBLIC_… that a .view(…) hands its page (mục 6), once the call is
// resolved: anything in what it is given, however deep (.view({ config: { stripe: environment.STRIPE_SECRET } }),
// .view(data) with data such an object), or what a variable of the function it runs holds (router.view((context)
// => { const stripe = environment.STRIPE_SECRET; }), or router.view(page) with page a const of such a
// function), every variable of which the page reads.
func (r *resolver) environmentView(node *MethodCallExpression) {
	if r.err != nil || node.Method == nil || node.Method.Value != "view" {
		return
	}
	for _, argument := range node.Arguments {
		if function := r.viewFunction(argument); function != nil {
			for _, declared := range function.scope.order {
				if key := declared.environmentHeld.key; key != "" && declared.kind != bindingParameter {
					r.fail(fmt.Errorf("%s: %s", r.where(declared.token), EnvironmentToBrowser(key)))
					return
				}
			}
			continue
		}
		if given, isObject := argument.(*ObjectLiteral); isObject {
			for _, entry := range given.Entries {
				if key := r.environmentHolds(entry.Value).key; key != "" {
					r.fail(fmt.Errorf("%s: %s", r.where(expressionToken(entry.Value)), EnvironmentToBrowser(key)))
					return
				}
			}
			continue
		}
		if key := r.environmentHolds(argument).key; key != "" {
			r.fail(fmt.Errorf("%s: %s", r.where(expressionToken(argument)), EnvironmentToBrowser(key)))
			return
		}
	}
}

// viewFunction is the scope of the function a .view(…) runs: written there, or a const declared with it and
// passed by its name. nil for anything else, or a function not resolved yet.
func (r *resolver) viewFunction(argument Expression) *functionScope {
	switch given := argument.(type) {
	case *FunctionLiteral:
		return r.functionScopes[given]
	case *Identifier:
		if declared := r.resolvedBinding(given); declared != nil && declared.function != nil {
			return r.functionScopes[declared.function]
		}
	}
	return nil
}

// environmentIndex refuses a word of the module "kitwork" read by [ ] (kitwork()[name], k[name] with k the
// module): a name the compiler cannot read, or "environment", by which environment would reach a program
// whole, past every check of a key's name; "env" in a file of an app is its old name.
func (r *resolver) environmentIndex(node *IndexExpression) bool {
	if !r.kitworkModule(node.Left) {
		return false
	}
	word, literal := "", false
	if key, isLiteral := node.Index.(*Literal); isLiteral && key.Value.IsString() {
		word, literal = key.Value.Text(), true
	}
	switch {
	case !literal:
		r.fail(fmt.Errorf("%s: %s", r.where(node.Token), KitworkByName))
	case word == "environment":
		r.fail(fmt.Errorf("%s: %s", r.where(node.Token), EnvironmentByName))
	case word == "env" && r.inApp:
		r.fail(fmt.Errorf("%s: %s", r.where(node.Token), EnvironmentRenamed))
	default:
		return false
	}
	return true
}

// environmentServerOnly lists the consts and lets of the program's top level that hold a key not named
// PUBLIC_…, imports of one from a module of the app included: the page of a router.view does not read them
// (Bytecode.EnvironmentServerOnly).
func environmentServerOnly(result *resolution) []string {
	var names []string
	for _, declared := range result.bindings {
		// the bundler's own names (__kw_mod_1, a module of the app) are never the page's anyway
		if declared.environmentHeld.key != "" && declared.scope.function.literal == nil && !declared.scope.block &&
			!strings.HasPrefix(declared.name, "__") {
			names = append(names, declared.name)
		}
	}
	return names
}
