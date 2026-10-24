package work

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// In router.work a rate limit counts ip (the default) or global; user is a boot error until the engine
// has checked sessions; browser and anything else read from the client are errors (KITWORK_ROUTER §4,
// 20/10/2026).
//
// 24/10/2026: the folder's limit is router.limit({ rate, per, by }); router.ratelimit, its old name, is a
// compile error naming it (compiler/router_words.go). STAGE 2 (router.limit runs at the folder): the folder
// case becomes router.limit({ rate: 10, per: "1m", by: "browser" }), refused at line 2 like the route's.
// CONTROL (24/10/2026, -overlay with ratelimit taken out of the compiler's removed words): the folder case went
// red, "router.work:2:8: router.ratelimit không có; router có get, …".
func TestUIRateLimitBy(t *testing.T) {
	for _, c := range []struct{ name, route, want, at string }{
		{"user", `router.get(({ response }) => response.text("x")).limit({ rate: 5, per: "1m", by: "user" });`,
			"by: \"user\" đếm theo người dùng của phiên đã kiểm, mà engine chưa có phiên đã kiểm", "dòng 2"},
		{"browser", `router.get(({ response }) => response.text("x")).limit({ rate: 5, per: "1m", by: "browser" });`,
			"by: \"browser\" lấy từ header hay cookie client tự gửi", "dòng 2"},
		{"folder, old name", `router.ratelimit({ browser: 10, minute: 1 });`,
			"router.ratelimit đã đổi tên (24/10/2026): giới hạn tần suất của nhánh là router.limit({ rate, per, by })", "router.work:2:8: "},
		// 24/10/2026: a by of no kind at all is refused by the strict reader of { rate, per, by }, at the call
		{"unknown", `router.get(({ response }) => response.text("x")).limit({ rate: 5, per: "1m", by: "cookie" });`,
			`router.get(…).limit({ by: "cookie" }): by là "ip" (mặc định) hay "global"`, "router.work:2:49: "},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := NewTenant(writeSite(t, map[string]string{
				"router.work":   `import * as router from "kitwork/router";`,
				"a/router.work": "import * as router from \"kitwork/router\";\n" + c.route + "\n",
			}), "localhost").Run()
			if err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), c.at) {
				t.Fatalf("run error = %v\nwant %q at %s", err, c.want, c.at)
			}
		})
	}

	tn := NewTenant(writeSite(t, map[string]string{
		"router.work": `import * as router from "kitwork/router";`,
		"all/router.work": "import * as router from \"kitwork/router\";\n" +
			"router.get(({ response }) => response.text(\"x\")).limit({ rate: 2, per: \"1m\", by: \"global\" });\n",
	}), "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	codes := []int{}
	for _, ip := range []string{"192.0.2.1:1", "192.0.2.2:1", "192.0.2.3:1"} {
		req := httptest.NewRequest(http.MethodGet, "http://localhost/all", nil)
		req.RemoteAddr = ip
		rec := httptest.NewRecorder()
		tn.Serve(rec, req)
		codes = append(codes, rec.Code)
	}
	if codes[0] != 200 || codes[1] != 200 || codes[2] != http.StatusTooManyRequests {
		t.Fatalf("global counts everyone together: %v", codes)
	}
}

// A template site keeps what its rate limits count, with one line per file at boot saying the client
// can change it.
func TestTemplateRateLimitByWarnsOnce(t *testing.T) {
	var tn *Tenant
	output := captureBootOutput(t, func() {
		tn = NewTenant(writeSite(t, map[string]string{
			"filesystem.kitwork": "",
			"router.kitwork.js":  `import { router } from "kitwork";`,
			"a/router.kitwork.js": "import { router } from \"kitwork\";\n" +
				"router.ratelimit({ browser: 50, minute: 1 });\n" +
				"router.get((ctx) => ctx.text(\"x\")).limit({ rate: 2, per: \"1m\", type: \"user\" });\n",
		}), "localhost")
		if err := tn.Run(); err != nil {
			t.Fatalf("run: %v", err)
		}
	})
	if got := strings.Count(output, "đếm theo header hay cookie client tự gửi"); got != 1 || !strings.Contains(output, "a/router.kitwork.js") && !strings.Contains(output, `a\router.kitwork.js`) {
		t.Fatalf("warned %d times:\n%s", got, output)
	}
	codes := []int{}
	for i := 0; i < 3; i++ {
		codes = append(codes, serveGet(tn, "/a", nil).Code)
	}
	if codes[2] != http.StatusTooManyRequests {
		t.Fatalf("the template site's limit still counts: %v", codes)
	}
}
