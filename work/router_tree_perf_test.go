package work

import "testing"

var routeMatchBenchmarkResult *RouteMatch

func benchmarkRouteTree() *RouteTree {
	root := &RouteNode{seg: ""}
	users := &RouteNode{seg: "users", parent: root}
	user := &RouteNode{
		seg:     "{user}",
		matcher: &segMatcher{name: "user", kind: segPlain},
		parent:  users,
	}
	rootChildren := []*RouteNode{users}
	userChildren := []*RouteNode{user}
	emptyChildren := []*RouteNode{}
	root.children.Store(&rootChildren)
	users.children.Store(&userChildren)
	user.children.Store(&emptyChildren)
	root.built.Store(true)
	users.built.Store(true)
	user.built.Store(true)
	return &RouteTree{root: root}
}

func BenchmarkPreparedRouteResolve(b *testing.B) {
	tree := benchmarkRouteTree()
	b.ReportAllocs()
	for iteration := 0; iteration < b.N; iteration++ {
		routeMatchBenchmarkResult = tree.Resolve("/users/quoc")
	}
}

func TestPreparedRouteResolveAllocationBudget(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation budgets are not comparable under race instrumentation")
	}
	tree := benchmarkRouteTree()
	allocations := testing.AllocsPerRun(1000, func() {
		routeMatchBenchmarkResult = tree.Resolve("/users/quoc")
	})
	if allocations > 8 {
		t.Fatalf("route resolve allocations = %.2f, budget is 8", allocations)
	}
	if routeMatchBenchmarkResult == nil ||
		!routeMatchBenchmarkResult.Found ||
		routeMatchBenchmarkResult.Params["user"] != "quoc" {
		t.Fatalf("route result = %#v", routeMatchBenchmarkResult)
	}
}
