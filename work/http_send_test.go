package work

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kitwork/engine/internal/loopback"
)

// reachLoopback lets a test's httptest server (127.0.0.1) through the SSRF guard: since 22/10/2026
// AllowLocal no longer turns the guard off.
func reachLoopback(t *testing.T) {
	t.Helper()
	t.Cleanup(loopback.Open())
}

// echoUpstream answers every request with what it saw: method, path, query, the headers a test reads,
// and the body.
func echoUpstream(t *testing.T) (*httptest.Server, *int32) {
	t.Helper()
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"method": r.Method, "path": r.URL.Path, "query": r.URL.RawQuery,
			"authorization": r.Header.Get("Authorization"), "accept": r.Header.Get("Accept"),
			"contentType": r.Header.Get("Content-Type"), "body": string(body),
		})
	}))
	t.Cleanup(server.Close)
	return server, &hits
}

// The spec's one-sentence example (KITWORK_HTTP.md) and its router example (mục 10) run verbatim in a
// router.work handler: headers, query, timeout, the body in .send(order), json() of the answer; and
// with the upstream gone the same code answers 502 with Kitwork's sentence, which names the request
// without its query.
func TestHTTPSpecExamplesRunInARoute(t *testing.T) {
	reachLoopback(t)
	upstream, _ := echoUpstream(t)
	tn := NewTenant(writeSite(t, map[string]string{
		"router.work": `import * as router from "kitwork/router";`,
		"orders/router.work": `import * as http from "kitwork/http";
import * as router from "kitwork/router";

const token = "demo-token";

router.post((context) => {
  const order = context.request.json();
  const upstream = http.post("` + upstream.URL + `/orders")
    .headers({ Authorization: "Bearer " + token, Accept: "application/json" })
    .query({ source: "kitwork" })
    .timeout("10s")
    .send(order);

  if (!upstream.ok) {
    return context.status(502).json({ error: upstream.error });
  }
  return context.json(upstream.json());
});
`,
		"users/router.work": `import * as http from "kitwork/http";
import * as router from "kitwork/router";

router.get(({ response }) => {
  const upstream = http.get("` + upstream.URL + `/users")
    .query({ page: 1, limit: 20 })
    .header("Accept", "application/json")
    .timeout("5s")
    .retry(2)
    .send();

  if (!upstream.ok) {
    return response.status(upstream.code === "TIMEOUT" ? 504 : 502).json({ error: upstream.error });
  }
  return response.json(upstream.json());
});
`,
	}), "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}

	order := `{"item":"coffee","quantity":2}`
	rec := sendRequest(tn, http.MethodPost, "/orders", "application/json", order, nil)
	var echoed map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &echoed); rec.Code != http.StatusOK || err != nil {
		t.Fatalf("POST /orders = %d %s", rec.Code, rec.Body.String())
	}
	for key, want := range map[string]string{
		"method": "POST", "path": "/orders", "query": "source=kitwork", "authorization": "Bearer demo-token",
		"accept": "application/json", "contentType": "application/json", "body": order,
	} {
		if echoed[key] != want {
			t.Errorf("the upstream saw %s = %v, want %q", key, echoed[key], want)
		}
	}
	rec = sendRequest(tn, http.MethodGet, "/users", "", "", nil)
	if body := rec.Body.String(); rec.Code != http.StatusOK || !strings.Contains(body, "limit=20") || !strings.Contains(body, "page=1") {
		t.Fatalf("GET /users = %d %s", rec.Code, rec.Body.String())
	}

	upstream.Close()
	rec = sendRequest(tn, http.MethodPost, "/orders", "application/json", order, nil)
	var failed struct{ Error string }
	if err := json.Unmarshal(rec.Body.Bytes(), &failed); rec.Code != http.StatusBadGateway || err != nil {
		t.Fatalf("POST /orders without its upstream = %d %s, want 502", rec.Code, rec.Body.String())
	}
	if !strings.Contains(failed.Error, "/orders") || strings.Contains(failed.Error, "source=kitwork") {
		t.Errorf("the error %q should name the request without its query", failed.Error)
	}
	if rec = sendRequest(tn, http.MethodGet, "/users", "", "", nil); rec.Code != http.StatusBadGateway {
		t.Errorf("GET /users without its upstream = %d %s, want 502", rec.Code, rec.Body.String())
	}
}

// The old spellings fail in a program with a message naming the new one, and a request described and
// kept is never sent (it was sent at the end of its statement until 22/10/2026). import { http } from
// "kitwork" stays the same module.
func TestHTTPOldSpellingsFailInAProgram(t *testing.T) {
	reachLoopback(t)
	upstream, _ := echoUpstream(t)
	var kept int32
	keptServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { atomic.AddInt32(&kept, 1) }))
	defer keptServer.Close()
	u := upstream.URL
	tn := NewTenant(writeSite(t, map[string]string{
		"router.work": `import * as http from "kitwork/http";
import { http as same } from "kitwork";
import * as router from "kitwork/router";

router.get((context) => {
  const later = http.get("` + keptServer.URL + `/kept").timeout("5s");
  const reasons = [
    safe(() => fetch("` + u + `")).error.message,
    safe(() => http.cache("1m").get("` + u + `")).error.message,
    safe(() => http.get("` + u + `").status).error.message,
    safe(() => http.post("` + u + `", { a: 1 })).error.message,
    safe(() => http.get("` + u + `").timeout(5000)).error.message,
    safe(() => http.get("` + u + `").send().body).error.message,
    safe(() => http.get("` + u + `").send({ page: 1 })).error.message,
    safe(() => http.get("` + u + `").base64).error.message,
    safe(() => http.get("` + u + `").send().error()).error.message
  ];
  return context.json({ reasons: reasons, same: same.get("` + u + `/same").send().json().path });
});
`,
	}), "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	rec := sendRequest(tn, http.MethodGet, "/", "", "", nil)
	var got struct {
		Reasons []string
		Same    string
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); rec.Code != http.StatusOK || err != nil || len(got.Reasons) != 9 {
		t.Fatalf("GET / = %d %s", rec.Code, rec.Body.String())
	}
	for i, says := range []string{
		`fetch đã bỏ: viết http.get(url).send()`,
		`http.cache(…) đã bỏ: bổ nghĩa đứng sau method, viết http.get(url).cache(…).send()`,
		`http.get(…) chưa gửi nên chưa có status: kết quả là của .send(), viết const upstream = http.get(url).send(); rồi đọc upstream.status`,
		`thân đi trong .send(data): http.post(url).send(data)`,
		`.timeout("5s")`,
		`.body không có: đọc thân bằng .bytes(), .text() hay .json()`,
		`không nhận thân: tham số URL viết bằng .query(`,
		`http.get(…) chưa gửi nên chưa có base64: kết quả là của .send(), viết const upstream = http.get(url).send(); rồi đọc upstream.bytes()`,
		`.error không phải hàm`,
	} {
		if !strings.Contains(got.Reasons[i], says) {
			t.Errorf("reason %d = %q, want it to say %q", i, got.Reasons[i], says)
		}
	}
	if got.Same != "/same" {
		t.Errorf(`import { http } from "kitwork" sent to %q, want /same`, got.Same)
	}
	if n := atomic.LoadInt32(&kept); n != 0 {
		t.Errorf("a request described and never sent reached the network %d times", n)
	}
}

// A route's function that returns a request it never sent fails naming .send(): until 22/10/2026 the
// request went out at the return, and since then the route answered 200 with nothing, and nothing went
// out. The router's .error() reads the failure; nothing reaches the upstream.
func TestHTTPReturnedUnsentRequestFails(t *testing.T) {
	reachLoopback(t)
	upstream, hits := echoUpstream(t)
	tn := NewTenant(writeSite(t, map[string]string{
		"router.work": `import * as router from "kitwork/router";`,
		"returned/router.work": `import * as http from "kitwork/http";
import * as router from "kitwork/router";

router.get(() => http.post("` + upstream.URL + `/orders").header("Accept", "application/json"))
  .error((context, error) => context.status(500).json({ code: error.code, message: error.message }));
`,
		"plain/router.work": `import * as http from "kitwork/http";
import * as router from "kitwork/router";

router.get(() => http.get("` + upstream.URL + `/feed"));
`,
	}), "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	rec := sendRequest(tn, http.MethodGet, "/returned", "", "", nil)
	var got struct{ Code, Message string }
	if err := json.Unmarshal(rec.Body.Bytes(), &got); rec.Code != http.StatusInternalServerError || err != nil {
		t.Fatalf("GET /returned = %d %s, want the .error() handler's 500", rec.Code, rec.Body.String())
	}
	if got.Code != "INVALID_ARGUMENT" || !strings.Contains(got.Message, "trả về http.post(…) chưa gửi") ||
		!strings.Contains(got.Message, "const upstream = http.post(url).send()") {
		t.Errorf("the failure = %+v, want INVALID_ARGUMENT naming http.post(url).send()", got)
	}
	if rec := sendRequest(tn, http.MethodGet, "/plain", "", "", nil); rec.Code != http.StatusInternalServerError {
		t.Errorf("GET /plain = %d %q, want 500, not an empty 200", rec.Code, rec.Body.String())
	}
	if n := atomic.LoadInt32(hits); n != 0 {
		t.Errorf("a request returned unsent reached the upstream %d times", n)
	}
}

// A self-call ("/path") reaches the site that made it: it carries the site's domain as its Host, and
// skips the SSRF guard (no loopback opened here).
func TestHTTPSelfCallReachesItsOwnSite(t *testing.T) {
	tn := NewTenant(writeSite(t, map[string]string{
		"router.work": `import * as http from "kitwork/http";
import * as router from "kitwork/router";

router.get((context) => {
  const self = http.get("/echo").query({ n: 7 }).send();
  return context.json({ ok: self.ok, status: self.status, echo: self.json() });
});
`,
		"echo/router.work": `import * as router from "kitwork/router";

router.get((context) => context.json({ host: context.request.host, n: context.request.query("n") }));
`,
	}), "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(tn.Serve))
	defer server.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	saved := ServerPort
	ServerPort, _ = strconv.Atoi(port)
	defer func() { ServerPort = saved }()

	rec := sendRequest(tn, http.MethodGet, "/", "", "", nil)
	var got struct {
		Ok     bool
		Status int
		Echo   struct{ Host, N string }
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); rec.Code != http.StatusOK || err != nil {
		t.Fatalf("GET / = %d %s", rec.Code, rec.Body.String())
	}
	if !got.Ok || got.Status != 200 || got.Echo.N != "7" || got.Echo.Host != "localhost" {
		t.Fatalf("the self-call = %+v, want ok, 200, n 7, Host localhost (the site's domain)", got)
	}
}

// The SSRF guard stays on for every absolute URL while developing (AllowLocal, ALLOW_LOCAL=true): until
// 22/10/2026 it turned the guard off for every request. A request to a loopback server is DENIED, and so
// is router.proxy to it; only a self-call ("/path") skips the guard.
func TestHTTPGuardStaysOnWhileDeveloping(t *testing.T) {
	saved := AllowLocal
	AllowLocal = true
	defer func() { AllowLocal = saved }()
	upstream, hits := echoUpstream(t)
	tn := NewTenant(writeSite(t, map[string]string{
		"router.work": `import * as http from "kitwork/http";
import * as router from "kitwork/router";

router.get((context) => {
  const upstream = http.get("` + upstream.URL + `/x").send();
  return context.json({ ok: upstream.ok, status: upstream.status, code: upstream.code });
});
`,
		"mirror/router.work": `import * as router from "kitwork/router";
router.proxy("` + upstream.URL + `/logo.png");
`,
	}), "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if rec := sendRequest(tn, http.MethodGet, "/", "", "", nil); rec.Code != http.StatusOK ||
		strings.TrimSpace(rec.Body.String()) != `{"code":"DENIED","ok":false,"status":null}` {
		t.Errorf("GET / = %d %s, want DENIED", rec.Code, rec.Body.String())
	}
	if rec := sendRequest(tn, http.MethodGet, "/mirror", "", "", nil); rec.Code != http.StatusBadGateway {
		t.Errorf("router.proxy to loopback = %d %s, want 502", rec.Code, rec.Body.String())
	}
	if n := atomic.LoadInt32(hits); n != 0 {
		t.Errorf("the guard let %d requests through", n)
	}
}
