package work

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The cache is the route's (.cache() RAM, .persist() disk), never an answer's, and keeps only what may
// be shared with everyone the guards let through (KITWORK_ROUTER §8, §9; 20/10/2026): a GET, a status
// HTTP caches (a 404 yes, a 5xx never), no Set-Cookie of the answer, no Cache-Control private or no-store.
func TestRouteCacheKeepsOnlyWhatMayBeShared(t *testing.T) {
	route := func(answer string) string {
		return "import * as router from \"kitwork/router\";\nlet runs = 0;\n" +
			"router.get(({ response }) => { runs = runs + 1; return " + answer + "; }).cache(\"1h\");\n"
	}
	tmp := writeSite(t, map[string]string{
		"router.work":         `import * as router from "kitwork/router";`,
		"page.ui":             "<page><content /></page>",
		"notfound.ui":         "<content><text>KHONG-THAY</text></content>",
		"ok/router.work":      route(`response.text("ok " + runs)`),
		"gone/router.work":    route(`response.notFound()`),
		"boom/router.work":    route(`response.error("hong " + runs)`),
		"made/router.work":    route(`response.status(201).text("made " + runs)`),
		"cookie/router.work":  route(`response.cookie("seen", "1").text("cookie " + runs)`),
		"private/router.work": route(`response.header("Cache-Control", "private").text("private " + runs)`),
		"nostore/router.work": route(`response.header("Cache-Control", "no-store").text("nostore " + runs)`),
		"post/router.work": "import * as router from \"kitwork/router\";\nlet runs = 0;\n" +
			"router.post(({ response }) => { runs = runs + 1; return response.text(\"post \" + runs); }).cache(\"1h\");\n",
		"refused/router.work": "import * as router from \"kitwork/router\";\nlet runs = 0;\n" +
			"router.get(({ response }) => { runs = runs + 1; return response.text(\"in \" + runs); }).cache(\"1h\")" +
			".guard(({ request }) => request.header(\"X-Pass\") == \"yes\");\n",
	})
	tn := NewTenant(tmp, "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	twice := func(method, path string) (*httptest.ResponseRecorder, *httptest.ResponseRecorder) {
		do := func() *httptest.ResponseRecorder {
			req := httptest.NewRequest(method, "http://localhost"+path, nil)
			rec := httptest.NewRecorder()
			tn.Serve(rec, req)
			return rec
		}
		return do(), do()
	}

	first, second := twice(http.MethodGet, "/ok")
	if first.Body.String() != "ok 1" || second.Body.String() != "ok 1" || second.Header().Get("X-Kitwork-Cache") != "hit" {
		t.Fatalf("/ok kept: %q then %q cache=%q", first.Body.String(), second.Body.String(), second.Header().Get("X-Kitwork-Cache"))
	}
	first, second = twice(http.MethodGet, "/gone")
	if first.Code != http.StatusNotFound || second.Code != http.StatusNotFound || second.Header().Get("X-Kitwork-Cache") != "hit" {
		t.Fatalf("/gone: a 404 is kept: %d then %d cache=%q", first.Code, second.Code, second.Header().Get("X-Kitwork-Cache"))
	}
	for path, again := range map[string]string{
		"/boom":    "hong 2",
		"/made":    "made 2",
		"/cookie":  "cookie 2",
		"/private": "private 2",
		"/nostore": "nostore 2",
	} {
		_, second := twice(http.MethodGet, path)
		if second.Header().Get("X-Kitwork-Cache") != "" || second.Body.String() != again {
			t.Errorf("%s was kept: second %d %q cache=%q, want the handler again (%q)", path, second.Code, second.Body.String(), second.Header().Get("X-Kitwork-Cache"), again)
		}
	}
	if _, second := twice(http.MethodPost, "/post"); second.Body.String() != "post 2" || second.Header().Get("X-Kitwork-Cache") != "" {
		t.Errorf("a POST was kept: %q cache=%q", second.Body.String(), second.Header().Get("X-Kitwork-Cache"))
	}

	// the guard stands before the read of the cache, and a guard that says no is 403
	pass := map[string]string{"X-Pass": "yes"}
	serveGet(tn, "/refused", pass)
	if r := serveGet(tn, "/refused", pass); r.Header().Get("X-Kitwork-Cache") != "hit" {
		t.Fatalf("/refused let through: not kept: %q", r.Body.String())
	}
	if r := serveGet(tn, "/refused", nil); r.Code != http.StatusForbidden || r.Header().Get("X-Kitwork-Cache") != "" {
		t.Fatalf("/refused refused: %d %q cache=%q, want 403 and no cache", r.Code, r.Body.String(), r.Header().Get("X-Kitwork-Cache"))
	}
}

// .cache() and .persist() are two tiers: a disk entry still fresh when RAM has none fills RAM again,
// for no longer than what is left of it; a read never stretches the first expiry.
func TestRoutePersistFillsRAMAgain(t *testing.T) {
	tmp := writeSite(t, map[string]string{
		"router.work": `import * as router from "kitwork/router";`,
		"menu/router.work": "import * as router from \"kitwork/router\";\nlet runs = 0;\n" +
			"router.get(({ response }) => { runs = runs + 1; return response.text(\"menu \" + runs); }).cache(200).persist(\"1h\");\n",
	})
	tn := NewTenant(tmp, "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if r := serveGet(tn, "/menu", nil); r.Body.String() != "menu 1" {
		t.Fatalf("first: %q", r.Body.String())
	}
	time.Sleep(300 * time.Millisecond) // RAM has gone, the disk has not
	if r := serveGet(tn, "/menu", nil); r.Body.String() != "menu 1" || r.Header().Get("X-Kitwork-Cache") != "hit" {
		t.Fatalf("from the disk: %q cache=%q", r.Body.String(), r.Header().Get("X-Kitwork-Cache"))
	}
	// the disk entry filled RAM: with the disk gone, RAM still answers
	if err := os.RemoveAll(filepath.Join(tmp, "test", "localhost", ".persist")); err != nil {
		t.Fatal(err)
	}
	if r := serveGet(tn, "/menu", nil); r.Body.String() != "menu 1" || r.Header().Get("X-Kitwork-Cache") != "hit" {
		t.Fatalf("RAM was not filled again: %q cache=%q", r.Body.String(), r.Header().Get("X-Kitwork-Cache"))
	}
}

func TestRefillExpiryNeverStretchesTheDisk(t *testing.T) {
	now := time.Now()
	cases := []struct {
		ram  time.Duration
		disk time.Time
		want time.Duration
	}{
		{5 * time.Minute, now.Add(time.Minute), time.Minute},
		{30 * time.Second, now.Add(time.Minute), 30 * time.Second},
		{0, now.Add(time.Minute), time.Minute},
		{time.Minute, time.Time{}, time.Minute},
	}
	for _, c := range cases {
		if got := refillExpiry(c.ram, c.disk, now); got != c.want {
			t.Errorf("refillExpiry(%v, %v left) = %v, want %v", c.ram, c.disk.Sub(now), got, c.want)
		}
	}
}
