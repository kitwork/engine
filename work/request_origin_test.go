package work

import (
	"net/http"
	"strings"
	"testing"
)

// A Kitwork UI route takes a request that may change something (not GET, HEAD, OPTIONS) only from the
// site's own pages or from no browser: a page of another site, cross-site or same-site (a sibling
// tenant under the same domain), is refused with 403 before the handler runs, and so is a browser
// that sends no Sec-Fetch-Site and an Origin that is not the Host. Same-origin passes, an Origin that
// is the Host passes, a request with neither header (a webhook) passes. GET, HEAD and OPTIONS are not
// checked.
//
// CONTROL: with the check taken out of serveTree, every refused request reached its handler and
// answered 200, and this test failed; with crossOriginRefusal refusing every request that is not GET,
// HEAD or OPTIONS, the same-origin and the webhook requests answered 403 and this test failed.
func TestCrossOriginRequestsToUIRoutes(t *testing.T) {
	tn := NewTenant(writeSite(t, map[string]string{
		"router.work": `import * as router from "kitwork/router";`,
		"orders/router.work": "import * as router from \"kitwork/router\";\n" +
			"router.get(({ response }) => response.json({ ran: \"get\" }));\n" +
			"router.post(({ response }) => response.json({ ran: \"post\" }));\n" +
			"router.put(({ response }) => response.json({ ran: \"put\" }));\n" +
			"router.patch(({ response }) => response.json({ ran: \"patch\" }));\n" +
			"router.delete(({ response }) => response.json({ ran: \"delete\" }));\n",
	}), "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	previous := AllowLocal
	defer func() { AllowLocal = previous }()
	AllowLocal = false

	refused := []struct {
		name   string
		header map[string]string
	}{
		{"cross-site", map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"}},
		{"same-site, a sibling tenant", map[string]string{"Sec-Fetch-Site": "same-site", "Origin": "http://other.localhost"}},
		{"an old browser, another Origin", map[string]string{"Origin": "https://evil.example"}},
		{"an old browser, Origin null", map[string]string{"Origin": "null"}},
		{"an old browser, another port", map[string]string{"Origin": "http://localhost:8081"}},
	}
	for _, c := range refused {
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
			rec := sendRequest(tn, method, "/orders", urlencoded, "email=a", c.header)
			if rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), "ran") {
				t.Errorf("%s %s: %d %q, want 403 and no handler", c.name, method, rec.Code, rec.Body.String())
			}
			if !strings.HasPrefix(rec.Body.String(), "Forbidden\nRequest ID: ") || rec.Header().Get("Cache-Control") != "no-store" {
				t.Errorf("%s %s: a visitor reads %q, Cache-Control %q", c.name, method, rec.Body.String(), rec.Header().Get("Cache-Control"))
			}
		}
	}

	allowed := []struct {
		name   string
		header map[string]string
	}{
		{"same-origin", map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": "http://localhost"}},
		{"an old browser, Origin is the Host", map[string]string{"Origin": "http://localhost"}},
		{"no browser (a webhook)", nil},
	}
	for _, c := range allowed {
		rec := sendRequest(tn, http.MethodPost, "/orders", urlencoded, "email=a", c.header)
		if rec.Code != http.StatusOK || rec.Body.String() != `{"ran":"post"}` {
			t.Errorf("%s POST: %d %q, want the handler", c.name, rec.Code, rec.Body.String())
		}
	}

	crossSite := map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"}
	if rec := sendRequest(tn, http.MethodGet, "/orders", "", "", crossSite); rec.Code != http.StatusOK || rec.Body.String() != `{"ran":"get"}` {
		t.Errorf("cross-site GET: %d %q, want the handler", rec.Code, rec.Body.String())
	}
	if rec := sendRequest(tn, http.MethodHead, "/orders", "", "", crossSite); rec.Code != http.StatusOK {
		t.Errorf("cross-site HEAD: %d, want 200", rec.Code)
	}
	if rec := sendRequest(tn, http.MethodOptions, "/orders", "", "", crossSite); rec.Code != http.StatusNoContent || rec.Header().Get("Allow") == "" {
		t.Errorf("cross-site OPTIONS: %d Allow %q, want 204 with Allow", rec.Code, rec.Header().Get("Allow"))
	}
}

// While developing (AllowLocal) the refusal says what refused it: the method and the path, the header
// that told, and that router.work takes such a request only from its own site's pages or from no
// browser.
//
// CONTROL: with crossOriginRefusal giving the same text for both cases (the Sec-Fetch-Site one), the
// old browser's refusal did not name its Origin and Host, and this test failed.
func TestCrossOriginRefusalSaysWhy(t *testing.T) {
	tn := NewTenant(writeSite(t, map[string]string{
		"router.work": `import * as router from "kitwork/router";`,
		"orders/router.work": "import * as router from \"kitwork/router\";\n" +
			"router.post(({ response }) => response.json({ ran: true }));\n",
	}), "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	previous := AllowLocal
	defer func() { AllowLocal = previous }()
	AllowLocal = true

	rec := sendRequest(tn, http.MethodPost, "/orders", "", "", map[string]string{"Sec-Fetch-Site": "same-site"})
	want := "POST /orders bị từ chối: request đến từ trang của một site khác (Sec-Fetch-Site: same-site). Route trong router.work chỉ nhận POST từ trang của chính site này, hay từ máy gọi thẳng không qua trình duyệt (webhook); chưa có cách mở cho site khác"
	if rec.Code != http.StatusForbidden || rec.Body.String() != want {
		t.Errorf("same-site: %d\n%s\nwant\n%s", rec.Code, rec.Body.String(), want)
	}
	rec = sendRequest(tn, http.MethodPost, "/orders", "", "", map[string]string{"Origin": "https://evil.example"})
	want = `POST /orders bị từ chối: request đến từ trang của một site khác (Origin "https://evil.example" không phải Host "localhost"). Route trong router.work chỉ nhận POST từ trang của chính site này, hay từ máy gọi thẳng không qua trình duyệt (webhook); chưa có cách mở cho site khác`
	if rec.Code != http.StatusForbidden || rec.Body.String() != want {
		t.Errorf("old browser: %d\n%s\nwant\n%s", rec.Code, rec.Body.String(), want)
	}
}

// A template site (router.kitwork.js) is not checked: a POST from another site's page reaches its
// handler, as it always did.
//
// CONTROL: with the check applied to every route (uiRoute dropped from serveTree's condition), the
// template site's POST answered 403 and this test failed.
func TestCrossOriginLeavesTemplateSites(t *testing.T) {
	tn := NewTenant(writeSite(t, map[string]string{
		"filesystem.kitwork": "",
		"router.kitwork.js":  `import { router } from "kitwork";`,
		"orders/router.kitwork.js": "import { router } from \"kitwork\";\n" +
			"router.post((ctx) => ctx.json({ ran: true }));\n",
	}), "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, header := range []map[string]string{
		{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"},
		{"Sec-Fetch-Site": "same-site", "Origin": "http://other.localhost"},
		{"Origin": "https://evil.example"},
	} {
		if rec := sendRequest(tn, http.MethodPost, "/orders", urlencoded, "email=a", header); rec.Code != http.StatusOK || rec.Body.String() != `{"ran":true}` {
			t.Errorf("template site POST %v: %d %q, want the handler", header, rec.Code, rec.Body.String())
		}
	}
}
