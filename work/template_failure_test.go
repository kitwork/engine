package work

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A {{ expression }} of a legacy .kitwork.html page whose value is a failure (a method that is not a
// function) writes its message in the page while developing, where the author reads it, and nothing in
// production (14/10/2026): a visitor never reads an engine's message, as a 500 shows them only the
// status and the request id. In production the message goes to the site's log instead, with the
// expression, once for each failure.
//
// CONTROL: the expressions around it print the same in both, and while developing the log has no such
// line.
func TestTemplateExpressionFailureIsShownOnlyWhileDeveloping(t *testing.T) {
	files := map[string]string{
		"index.kitwork.html": "<main>{{ @page }}</main>",
		"router.kitwork.js":  "import { router } from \"kitwork\";\nrouter.get((ctx) => ctx.bind({ item: { n: 3, name: \"cà phê\" } }));\n",
		"page.kitwork.html":  "<p>[{{ item.name }}|{{ item.n() }}|{{ item.name.upper() }}]</p>",
	}
	const message = "item.n không phải hàm (là số): không gọi được"
	for _, developing := range []bool{true, false} {
		saved := AllowLocal
		AllowLocal = developing
		root := t.TempDir()
		for name, content := range files {
			full := filepath.Join(root, "acme", "localhost", filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		tenant := NewTenant(root, "localhost")
		if err := tenant.Run(); err != nil {
			t.Fatal(err)
		}
		var logged bytes.Buffer
		savedLog := slog.Default()
		slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
		recorder := httptest.NewRecorder()
		tenant.Serve(recorder, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
		slog.SetDefault(savedLog)
		tenant.Close()
		AllowLocal = saved

		body, record := recorder.Body.String(), logged.String()
		if recorder.Code != http.StatusOK {
			t.Fatalf("developing %v: GET / = %d\n%s", developing, recorder.Code, body)
		}
		if developing {
			if !strings.Contains(body, "[cà phê|"+message+"|CÀ PHÊ]") {
				t.Errorf("while developing the page says what failed where the value stands:\n%s", body)
			}
			if strings.Contains(record, "Kitwork template expression failed") {
				t.Errorf("CONTROL: while developing the page says it, the log does not:\n%s", record)
			}
			continue
		}
		if !strings.Contains(body, "[cà phê||CÀ PHÊ]") || strings.Contains(body, "không phải hàm") {
			t.Errorf("in production the page writes nothing where the failed value stands:\n%s", body)
		}
		for _, field := range []string{`msg="Kitwork template expression failed"`, "site=localhost", `expression=item.n()`, message} {
			if !strings.Contains(record, field) {
				t.Errorf("in production the site's log has %q:\n%s", field, record)
			}
		}
		if strings.Count(record, "Kitwork template expression failed") != 1 {
			t.Errorf("one log line for the one failed expression:\n%s", record)
		}
	}
}
