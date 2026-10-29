package work

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// The page and every component the browser runs are instances of one data block (29/10/2026,
// docs/KITWORK_BRIDGE.md mục 3): on /shop, a page with a var and a const of its own that holds the cart, the
// block holds the page's data under "page" and the cart's under its place, which component that place is, the
// jobs and vars of the page (the component "page") and of the cart, and the revisions of both apart, under bridge.
// There is one block, and no block of the components beside it.
//
// CONTROL (by hand, 29/10/2026): with the block written without the instances the render drew, the cart's data,
// place and revisions were missing (red); with the page's data left out, "page" was missing (red).
func TestOneBlockHoldsThePageAndItsInstances(t *testing.T) {
	tn := ordersSite(t)
	postCall(tn, "/shop", "shop-cart.checkout", callBody{Seq: 1, Args: []any{}, Page: vars("note", ""), At: placesOf(t, tn, "/shop")[0],
		Mine: vars("items", []any{map[string]any{"id": 1, "name": "trà"}})}.String())
	body := serveGet(tn, "/shop", nil).Body.String()
	if n := strings.Count(body, "data-kitwork-page"); n != 1 || strings.Contains(body, "data-kitwork-components") {
		t.Fatalf("the page should carry one data block, and no block of its components (%d):\n%.3000s", n, body)
	}
	m := regexp.MustCompile(`data-kitwork-page>(.*?)</script>`).FindStringSubmatch(body)
	var block map[string]json.RawMessage
	if m == nil || json.Unmarshal([]byte(m[1]), &block) != nil {
		t.Fatalf("no data block:\n%.2000s", body)
	}
	at := placesOf(t, tn, "/shop")[0]
	var page, cart map[string]any
	var components map[string]string
	var jobs, vars map[string][]string
	var bridge struct {
		Revs     map[string]map[string]string `json:"revs"`
		Defaults map[string]map[string]any    `json:"defaults"`
		Stamp    float64                      `json:"stamp"`
	}
	for key, into := range map[string]any{"page": &page, at: &cart, "components": &components, "jobs": &jobs, "vars": &vars, "bridge": &bridge} {
		if json.Unmarshal(block[key], into) != nil {
			t.Fatalf("the block should hold %q: %s", key, m[1])
		}
	}
	if page["count"] != float64(1) || page["note"] != "" {
		t.Errorf("the instance page should hold the page's data: %v", page)
	}
	if items, ok := cart["items"].([]any); !ok || len(items) != 0 {
		t.Errorf("the cart's place should hold its data as the page drew it: %v", cart)
	}
	if components[at] != "shop-cart" {
		t.Errorf("the block should say the place %s is the cart: %v", at, components)
	}
	if strings.Join(jobs["page"], ",") != "" || strings.Join(vars["page"], ",") != "note" ||
		strings.Join(jobs["shop-cart"], ",") != "add,checkout" || strings.Join(vars["shop-cart"], ",") != "items" {
		t.Errorf("jobs %v, vars %v", jobs, vars)
	}
	if bridge.Revs["page"]["count"] == "" || bridge.Revs["page"]["note"] != "" || bridge.Stamp <= 0 {
		t.Errorf("the page's revisions and stamp should stand under bridge, a var's aside: %+v", bridge)
	}
	if bridge.Defaults["shop-cart"] == nil {
		t.Errorf("the cart's starting values should stand under bridge, for the kernel: %+v", bridge)
	}
}
