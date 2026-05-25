package work

import (
	"testing"
)

func TestRouteTrie(t *testing.T) {
	trie := NewRoutes()

	r1 := &Router{Path: "/"}
	r2 := &Router{Path: "/users"}
	r3 := &Router{Path: "/users/:id"}
	r4 := &Router{Path: "/users/:id/posts/:postId"}
	r5 := &Router{Path: "/assets/*"}
	r6 := &Router{Path: "/profile/:name?"}

	trie.Insert("GET", "/", r1)
	trie.Insert("GET", "/users", r2)
	trie.Insert("GET", "/users/:id", r3)
	trie.Insert("GET", "/users/:id/posts/:postId", r4)
	trie.Insert("GET", "/assets/*", r5)
	trie.Insert("GET", "/profile/:name?", r6)

	// Test case 1: Exact static match
	matched, params := trie.Match("GET", "/")
	if matched != r1 || len(params) != 0 {
		t.Errorf("expected r1, got %v with params %v", matched, params)
	}

	// Test case 2: Static match
	matched, params = trie.Match("GET", "/users")
	if matched != r2 || len(params) != 0 {
		t.Errorf("expected r2, got %v with params %v", matched, params)
	}

	// Test case 3: Dynamic parameter match
	matched, params = trie.Match("GET", "/users/123")
	if matched != r3 || params["id"] != "123" {
		t.Errorf("expected r3, got %v with params %v", matched, params)
	}

	// Test case 4: Nested dynamic parameter match
	matched, params = trie.Match("GET", "/users/123/posts/456")
	if matched != r4 || params["id"] != "123" || params["postId"] != "456" {
		t.Errorf("expected r4, got %v with params %v", matched, params)
	}

	// Test case 5: Wildcard match
	matched, params = trie.Match("GET", "/assets/images/logo.png")
	if matched != r5 {
		t.Errorf("expected r5, got %v", matched)
	}

	// Test case 6: Optional parameter present
	matched, params = trie.Match("GET", "/profile/john")
	if matched != r6 || params["name"] != "john" {
		t.Errorf("expected r6, got %v with params %v", matched, params)
	}

	// Test case 7: Optional parameter missing
	matched, params = trie.Match("GET", "/profile")
	if matched != r6 || params["name"] != "" {
		t.Errorf("expected r6, got %v with params %v", matched, params)
	}

	// Test case 8: Wildcard match root level fallback
	r7 := &Router{Path: "/*"}
	trie.Insert("GET", "/*", r7)
	matched, params = trie.Match("GET", "/other-page")
	if matched != r7 {
		t.Errorf("expected r7 (wildcard root), got %v", matched)
	}

	// Test case 9: Root path matches /* when no exact / is registered
	trie2 := NewRoutes()
	trie2.Insert("GET", "/*", r7)
	matched, params = trie2.Match("GET", "/")
	if matched != r7 {
		t.Errorf("expected r7, got %v", matched)
	}
}
