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
// import { view } from "kitwork/router").
func (r *resolver) routerDeclaration(node *VarStatement) {
	if !r.inRouterFile {
		return
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
// kitwork().router.view).
func (r *resolver) routerMember(object Expression, property *Identifier) bool {
	if !r.inRouterFile || property == nil || property.Value != "view" {
		return false
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
	r.fail(fmt.Errorf("%s: %s", r.where(property.Token), RouterViewRemoved))
	return true
}
