package compiler

import "fmt"

// kitwork/router in router.work (KITWORK_ROUTER.md, 23/10/2026): a page is the answer of a method,
// router.get((context) => context.view({ … })), and it reads exactly the names of that object. router.view,
// the folder's page fed by every name its function and its file declared, is gone: refused here, where it
// is written in a router.work (router.view(…), const { view } = router, kitwork().router.view); a name the
// compiler cannot follow, and a template site's router.kitwork.js, are refused when the router runs
// (work/router_ui.go), in the same words.

// RouterFile is the name of a Kitwork UI site's router file.
const RouterFile = "router.work"

// RouterViewRemoved is the error of router.view, the page of a folder until 23/10/2026.
const RouterViewRemoved = `router.view đã bỏ: trang là câu trả lời của router.get, router.get((context) => context.title("…").view({ … })); trang đọc đúng những tên trong object của view, không còn mọi tên của hàm và của file (KITWORK_ROUTER.md mục 1, 6)`

// markRouter marks a declared variable as the module kitwork/router itself (import * as router from
// "kitwork/router", import { router } from "kitwork", import { router as r } from "kitwork").
func (r *resolver) markRouter(node *VarStatement, name *Identifier, declared *binding) {
	if declared == nil || node.Pattern != nil {
		return
	}
	if module, ok := kitworkModulePath(node.Value); ok {
		declared.routerModule = (node.DestructMode == DestructNone && module == "router") ||
			(node.DestructMode == DestructObject && module == "" && name.Value == "router")
	}
}

// routerDeclaration refuses view taken apart from the router in a router.work (const { view } = router,
// import { view } from "kitwork/router"), and the router taken off the module "kitwork" (const { router } =
// kitwork(), const { router } = k with k the module): the router comes only through its import line.
func (r *resolver) routerDeclaration(node *VarStatement) {
	if !r.inRouterFile {
		return
	}
	if node.Pattern != nil && !node.Pattern.Array && r.routerCalls != nil && r.kitworkModule(node.Value) {
		if element := patternElement(node.Pattern, "router"); element != nil {
			written := "kitwork()"
			if name, isName := node.Value.(*Identifier); isName {
				written = name.Value
			}
			r.fail(fmt.Errorf("%s: %s", r.where(element.KeyToken), RouterImportForm("const { router } = "+written, "")))
			return
		}
	}
	if node.Pattern != nil {
		namespace, isName := node.Value.(*Identifier)
		if !isName || node.Pattern.Array {
			return
		}
		if found := r.lookup(namespace.Value); found == nil || !found.routerModule {
			return
		}
		for _, element := range node.Pattern.Elements {
			if element.Key == "view" {
				r.fail(fmt.Errorf("%s: %s", r.where(element.KeyToken), RouterViewRemoved))
				return
			}
		}
		return
	}
	if module, ok := kitworkModulePath(node.Value); ok && module == "router" && node.DestructMode == DestructObject {
		for _, name := range node.Names {
			if name.Value == "view" {
				r.fail(fmt.Errorf("%s: %s", r.where(name.Token), RouterViewRemoved))
				return
			}
		}
	}
	if member, isMember := node.Value.(*MemberExpression); isMember && member.Property != nil && member.Property.Value == "view" {
		if module, ok := kitworkModulePath(member.Object); ok && module == "router" {
			r.fail(fmt.Errorf("%s: %s", r.where(member.Property.Token), RouterViewRemoved))
		}
	}
}

// routerMember refuses, where it is written in a router.work, router.view read off the router (router.view(…),
// kitwork().router.view). In a router.work or a router config.work read by readRouterFile it refuses the
// router read off the module "kitwork" (kitwork().router, k.router) but in the import line itself, and a word
// read off the router without a call (router.get, helper(router.get)); called is false for that read, true for
// the word of a call.
func (r *resolver) routerMember(object Expression, property *Identifier, called bool) bool {
	if !r.inRouterFile || property == nil {
		return false
	}
	if r.routerCalls != nil && property.Value == "router" && !r.importValue && r.kitworkModule(object) {
		written := "kitwork().router"
		if name, isName := object.(*Identifier); isName {
			written = name.Value + ".router"
		}
		r.fail(fmt.Errorf("%s: %s", r.where(property.Token), RouterImportForm(written, "")))
		return true
	}
	isModule := false
	if module, ok := kitworkModulePath(object); ok {
		isModule = module == "router"
	} else if name, isName := object.(*Identifier); isName {
		found := r.lookup(name.Value)
		isModule = found != nil && found.routerModule
	}
	if !isModule {
		return false
	}
	if property.Value == "view" {
		r.fail(fmt.Errorf("%s: %s", r.where(property.Token), RouterViewRemoved))
		return true
	}
	if r.routerCalls == nil || called {
		return false
	}
	if name, isName := object.(*Identifier); isName {
		r.fail(fmt.Errorf("%s: %s", r.where(property.Token), RouterAsValue(name.Value+"."+property.Value)))
		return true
	}
	return false
}

// routerIndex refuses, in a router.work or a router config.work read by readRouterFile, the router read off the
// module "kitwork" by [ ] (kitwork()["router"], k["router"]), as routerMember refuses kitwork().router: the
// router comes only through its import line. A name in [ ] is refused for every word of the module already
// (environmentIndex).
func (r *resolver) routerIndex(node *IndexExpression) bool {
	if !r.inRouterFile || r.routerCalls == nil || !r.kitworkModule(node.Left) {
		return false
	}
	key, isLiteral := node.Index.(*Literal)
	if !isLiteral || !key.Value.IsString() || key.Value.Text() != "router" {
		return false
	}
	written := `kitwork()["router"]`
	if name, isName := node.Left.(*Identifier); isName {
		written = name.Value + `["router"]`
	}
	r.fail(fmt.Errorf("%s: %s", r.where(key.Token), RouterImportForm(written, "")))
	return true
}

// routerCall lets through the router.<word>(…) a line of a router.work or a router config.work begins with
// (readRouterFile), reading the router and the call's arguments; the same call anywhere else (in a function,
// an if, a loop, or as part of another expression) is refused. A call of the router under another name, or
// written out as kitwork().router, is refused as it is read (routerMember).
func (r *resolver) routerCall(node *MethodCallExpression) bool {
	if !r.inRouterFile || node.Method == nil {
		return false
	}
	if r.routerMember(node.Object, node.Method, true) {
		return true
	}
	if r.routerCalls == nil {
		return false
	}
	name, isName := node.Object.(*Identifier)
	if !isName {
		return false
	}
	if found := r.lookup(name.Value); found == nil || !found.routerModule {
		return false
	}
	if !r.routerCalls.Heads[node] {
		r.fail(fmt.Errorf("%s: %s", r.where(node.Method.Token), RouterNotAtTop(node.Method.Value)))
		return true
	}
	r.routerUse = true
	r.reference(name, false)
	r.routerUse = false
	for _, argument := range node.Arguments {
		r.expression(argument)
	}
	return true
}

// routerRead refuses the router read as a value in a router.work or a router config.work (const r = router,
// helper(router), router["get"]): it stands only at the head of a line's call (routerCall).
func (r *resolver) routerRead(name *Identifier, found *binding) bool {
	if !found.routerModule || r.routerCalls == nil || r.routerUse {
		return false
	}
	r.fail(fmt.Errorf("%s: %s", r.where(name.Token), RouterAsValue(name.Value)))
	return true
}
