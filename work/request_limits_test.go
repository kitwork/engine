package work

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func postBody(tn *Tenant, path, body string, header map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "http://localhost"+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	tn.Serve(rec, req)
	return rec
}

// Every read of a request body has a ceiling, 1 MiB by default (KITWORK_ROUTER §5, 20/10/2026): past it
// the request answers 413 and reads no further; request.json({ limit }) of router.work reads more, up to
// the host's ceiling; reading twice gives the same value. A template site keeps request.json as a
// property, under the same ceiling.
func TestRequestBodyCeiling(t *testing.T) {
	big := `{"text":"` + strings.Repeat("a", 3<<19) + `"}` // 1.5 MiB
	small := `{"text":"chào"}`
	ui := NewTenant(writeSite(t, map[string]string{
		"router.work": `import * as router from "kitwork/router";`,
		"body/router.work": "import * as router from \"kitwork/router\";\n" +
			"router.post(({ request, response }) => response.json({ first: request.body().length, again: request.body().length }));\n",
		"json/router.work": "import * as router from \"kitwork/router\";\n" +
			"router.post(({ request, response }) => response.json({ size: request.json().text.length }));\n",
		"wide/router.work": "import * as router from \"kitwork/router\";\n" +
			"router.post(({ request, response }) => response.json({ size: request.json({ limit: \"4mb\" }).text.length }));\n",
		"huge/router.work": "import * as router from \"kitwork/router\";\n" +
			"router.post(({ request, response }) => response.json({ size: request.json({ limit: \"4gb\" }) }));\n",
	}), "localhost")
	if err := ui.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if r := postBody(ui, "/body", small, nil); r.Code != http.StatusOK || r.Body.String() != `{"again":15,"first":15}` {
		t.Errorf("/body small: %d %q", r.Code, r.Body.String())
	}
	if r := postBody(ui, "/body", big, nil); r.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("/body past 1 MiB: %d %.120q, want 413", r.Code, r.Body.String())
	}
	if r := postBody(ui, "/json", big, nil); r.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("/json past 1 MiB: %d, want 413", r.Code)
	}
	if r := postBody(ui, "/wide", big, nil); r.Code != http.StatusOK || r.Body.String() != `{"size":1572864}` {
		t.Errorf("/wide with limit 4mb: %d %.120q", r.Code, r.Body.String())
	}
	if r := postBody(ui, "/huge", small, nil); r.Code != http.StatusInternalServerError {
		t.Errorf("/huge asks past the host: %d %q, want the request to fail", r.Code, r.Body.String())
	}

	// a body that says it is too long is not read at all
	req := httptest.NewRequest(http.MethodPost, "http://localhost/body", strings.NewReader(small))
	req.ContentLength = 2 << 20
	rec := httptest.NewRecorder()
	ui.Serve(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("Content-Length past the ceiling: %d, want 413", rec.Code)
	}

	template := NewTenant(writeSite(t, map[string]string{
		"filesystem.kitwork": "",
		"router.kitwork.js":  `import { router } from "kitwork";`,
		// kiturl.com's shape: request.json read as a property
		"shorten/router.kitwork.js": "import { router } from \"kitwork\";\n" +
			"router.post((ctx) => { const body = ctx.request.json || {}; return ctx.json({ text: body.text }); });\n",
		"limit/router.kitwork.js": "import { router } from \"kitwork\";\n" +
			"router.post((ctx) => ctx.json(ctx.jsonBodyLimit(8192)));\n",
		"raw/router.kitwork.js": "import { router } from \"kitwork\";\n" +
			"router.post((ctx) => ctx.json({ size: ctx.body().length }));\n",
	}), "localhost")
	if err := template.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if r := postBody(template, "/shorten", small, nil); r.Body.String() != `{"text":"chào"}` {
		t.Errorf("template request.json property: %d %q", r.Code, r.Body.String())
	}
	if r := postBody(template, "/limit", small, nil); r.Body.String() != `{"text":"chào"}` {
		t.Errorf("template jsonBodyLimit: %d %q", r.Code, r.Body.String())
	}
	if r := postBody(template, "/raw", big, nil); r.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("template body past 1 MiB: %d, want 413", r.Code)
	}
}

// The client's address and scheme believe a proxy's headers only when the app trusts its proxy: one
// rule for request.ip, request.scheme, request.secure and the rate limits.
func TestRequestTrustsTheProxyOnlyWhenTold(t *testing.T) {
	tn := NewTenant(writeSite(t, map[string]string{
		"router.work": `import * as router from "kitwork/router";`,
		"who/router.work": "import * as router from \"kitwork/router\";\n" +
			"router.get(({ request, response }) => response.json([request.ip, request.scheme, request.secure]));\n",
	}), "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	ask := func() string {
		req := httptest.NewRequest(http.MethodGet, "http://localhost/who", nil)
		req.RemoteAddr = "192.0.2.7:4321"
		req.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")
		req.Header.Set("X-Forwarded-Proto", "https")
		rec := httptest.NewRecorder()
		tn.Serve(rec, req)
		return rec.Body.String()
	}
	previous := TrustProxyHeaders
	defer func() { TrustProxyHeaders = previous }()

	TrustProxyHeaders = false
	if got := ask(); got != `["192.0.2.7","http",false]` {
		t.Errorf("no trusted proxy: %s", got)
	}
	TrustProxyHeaders = true
	if got := ask(); got != `["203.0.113.9","https",true]` {
		t.Errorf("trusted proxy: %s", got)
	}
}

func TestParseByteSize(t *testing.T) {
	for text, want := range map[string]int64{"4mb": 4 << 20, "512kb": 512 << 10, "1gb": 1 << 30, "100": 100, " 2MB ": 2 << 20} {
		if got, err := ParseByteSizeOf(text); err != nil || got != want {
			t.Errorf("%q = %d, %v; want %d", text, got, err, want)
		}
	}
	for _, bad := range []string{"", "lots", "-1mb", "0"} {
		if _, err := ParseByteSizeOf(bad); err == nil {
			t.Errorf("%q read as a size", bad)
		}
	}
}
