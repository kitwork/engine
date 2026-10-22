package work

// The methods a folder answers, for 405 and OPTIONS in a Kitwork UI site (KITWORK_ROUTER §2,
// 20/10/2026), and HEAD answered as GET with its body dropped.

import (
	"net/http"
	"sort"
	"strings"
)

// allowedMethods is the Allow header of a folder: the methods its router file declared, GET when it
// has a page, HEAD with GET, and OPTIONS; "" when it answers nothing (a 404).
func (t *Tenant) allowedMethods(node *RouteNode, folder *FolderRouter) string {
	set := map[string]bool{}
	if folder != nil {
		for name := range folder.methods {
			set[name] = true
		}
	}
	if t.folderHasUIPage(node) || t.folderHasPage(node) {
		set[http.MethodGet] = true
	}
	if len(set) == 0 {
		return ""
	}
	if set[http.MethodGet] {
		set[http.MethodHead] = true
	}
	set[http.MethodOptions] = true
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// headOnly answers a HEAD: the headers and the status of the GET, no body.
type headOnly struct{ http.ResponseWriter }

func (h headOnly) Write(b []byte) (int, error) { return len(b), nil }
