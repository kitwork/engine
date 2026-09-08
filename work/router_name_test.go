package work

import (
	"reflect"
	"strings"
	"testing"
)

// The VM reaches a Go method by lowercasing its name (value.membersFor), so a
// method called Css is what makes router.css(...) resolve. These tests assert
// the NAME is reachable, not merely that a Go method exists — renaming the Go
// method without keeping the lowercase spelling would silently break every site.
func routerMethodNames(t *testing.T) map[string]reflect.Method {
	t.Helper()
	routerType := reflect.TypeOf(&FolderRouter{})
	names := make(map[string]reflect.Method, routerType.NumMethod())
	for index := 0; index < routerType.NumMethod(); index++ {
		method := routerType.Method(index)
		key := strings.ToLower(method.Name)
		if _, exists := names[key]; !exists {
			names[key] = method
		}
	}
	return names
}

func TestRouterExposesCurrentDesignAndDeliveryNames(t *testing.T) {
	names := routerMethodNames(t)
	for _, want := range []string{"css", "javascript"} {
		if _, ok := names[want]; !ok {
			t.Fatalf("router.%s() does not resolve: no exported method lowercases to %q", want, want)
		}
	}
}

// The old spellings stay reachable: dozens of live sites still call them, and a
// rename that breaks them is a rename that cannot ship.
func TestRouterKeepsDeprecatedJitAliases(t *testing.T) {
	names := routerMethodNames(t)
	for _, want := range []string{"jitcss", "jitjs"} {
		if _, ok := names[want]; !ok {
			t.Fatalf("router.%s() stopped resolving; existing sites would break", want)
		}
	}
}

// Both spellings must reach the same implementation, or the alias is a fork
// waiting to drift.
func TestCssAliasMatchesJitcssSignature(t *testing.T) {
	names := routerMethodNames(t)
	css, jitcss := names["css"], names["jitcss"]
	if css.Type != jitcss.Type {
		t.Fatalf("router.css and router.jitcss have different signatures: %s vs %s", css.Type, jitcss.Type)
	}
	javascript, jitjs := names["javascript"], names["jitjs"]
	if javascript.Type != jitjs.Type {
		t.Fatalf("router.javascript and router.jitjs have different signatures: %s vs %s", javascript.Type, jitjs.Type)
	}
}

// The house rule is no abbreviations. "css" is the language's own name; "js" is
// a shortening of JavaScript, so it must not become a reachable router name.
func TestRouterDoesNotExposeAbbreviatedJs(t *testing.T) {
	if _, ok := routerMethodNames(t)["js"]; ok {
		t.Fatal("router.js() resolves — the abbreviated spelling is back; the name is router.javascript()")
	}
}
