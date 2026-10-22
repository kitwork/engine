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
func TestUIRateLimitBy(t *testing.T) {
	for _, c := range []struct{ name, route, want string }{
		{"user", `router.get(({ response }) => response.text("x")).limit({ rate: 5, per: "1m", by: "user" });`,
			"by: \"user\" đếm theo người dùng của phiên đã kiểm, mà engine chưa có phiên đã kiểm"},
		{"browser", `router.get(({ response }) => response.text("x")).limit({ rate: 5, per: "1m", by: "browser" });`,
			"by: \"browser\" lấy từ header hay cookie client tự gửi"},
		{"folder browser", `router.ratelimit({ browser: 10, minute: 1 });`,
			"by: \"browser\" lấy từ header hay cookie client tự gửi"},
		{"unknown", `router.get(({ response }) => response.text("x")).limit({ rate: 5, per: "1m", by: "cookie" });`,
			"by: \"cookie\" không có"},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := NewTenant(writeSite(t, map[string]string{
				"router.work":   `import * as router from "kitwork/router";`,
				"a/router.work": "import * as router from \"kitwork/router\";\n" + c.route + "\n",
			}), "localhost").Run()
			if err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), "dòng 2") {
				t.Fatalf("run error = %v\nwant %q at line 2", err, c.want)
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
