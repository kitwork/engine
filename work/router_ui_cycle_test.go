package work

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kitwork/engine/value"
)

// A value a .ui page would read that holds itself is the view's error, naming the view's place, the
// name and the cycle. Before 14/10/2026 the converter went round it to its depth bound, and an object
// with two keys back to itself doubled at every level: 2⁶⁵ conversions, a request that never ended.
func TestViewScopeOfAValueThatHoldsItselfIsTheViewsError(t *testing.T) {
	data := map[string]value.Value{"a": value.New(1)}
	cart := value.New(data)
	data["self"] = cart
	data["again"] = cart
	view := &value.Lambda{SourceFile: "router.kitwork.js", SourceLine: 4, SourceColumn: 22}

	done := make(chan struct{})
	var scope map[string]any
	var err error
	go func() {
		defer close(done)
		scope, err = viewScope(nil, view, map[string]value.Value{"cart": cart})
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("viewScope of an object with two keys back to itself did not end")
	}
	self := "router.kitwork.js:4:22: dữ liệu trang cart: object tự tham chiếu (vòng ở self, quay về gốc)"
	again := "router.kitwork.js:4:22: dữ liệu trang cart: object tự tham chiếu (vòng ở again, quay về gốc)"
	if err == nil || (err.Error() != self && err.Error() != again) || scope != nil {
		t.Fatalf("viewScope = %v, %v; want the error %q", scope, err, self)
	}

	// the same object under two names, or twice in a list, does not hold itself
	shared := value.New(map[string]value.Value{"n": value.New(1)})
	scope, err = viewScope(nil, nil, map[string]value.Value{
		"pair": value.New(map[string]value.Value{"p": shared, "q": value.New([]value.Value{shared, shared})}),
	})
	if err != nil {
		t.Fatalf("shared object: %v", err)
	}
	pair, _ := scope["pair"].(map[string]any)
	list, _ := pair["q"].([]any)
	if p, _ := pair["p"].(map[string]any); p["n"] != float64(1) || len(list) != 2 || list[1].(map[string]any)["n"] != float64(1) {
		t.Fatalf("shared object converted to %#v", scope["pair"])
	}
}

// The same through a site: a view's object that holds itself is the page's 500, saying where; and a
// page that puts its scope into itself ({{ x = $ }}) and hands it to a view function gets the failure
// as the argument, where until 14/10/2026 turning it into a value overflowed Go's stack and stopped
// the host, every site on it.
func TestUIPageOfAValueThatHoldsItself(t *testing.T) {
	tenant := buildUISite(t, map[string]string{
		"localhost/router.work": `
import { router } from "kitwork";
router.view((ctx) => {
  const cart = { a: 1 };
  cart.self = cart;
  cart.again = cart;
});
`,
		"localhost/content.ui": `<content><text>{{ cart.a }}</text></content>`,
		"localhost/scope/router.work": `
import { router } from "kitwork";
router.view((ctx) => {
  function shown(v) { return "đã gọi"; }
});
`,
		"localhost/scope/content.ui": `<content><text>{{ x = $ }}</text><text>{{ shown(x) }}</text></content>`,
	})
	rec := serveUI(t, tenant, "http://localhost/")
	body := rec.Body.String()
	if rec.Code != http.StatusInternalServerError || !strings.Contains(body, "dữ liệu trang cart: object tự tham chiếu (vòng ở ") ||
		!strings.Contains(body, "router.work:") {
		t.Fatalf("GET / = %d\n%s", rec.Code, body)
	}
	rec = serveUI(t, tenant, "http://localhost/scope")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "đã gọi") {
		t.Fatalf("GET /scope = %d\n%.2000s", rec.Code, rec.Body.String())
	}
}
