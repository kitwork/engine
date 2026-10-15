package core

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/kitwork/engine/work"
)

// A request for a site whose generation did not prepare (its router does not compile), and one for a
// host with no site, used to answer the error itself — the compile error, the absolute path of the
// folder it looked for — to any visitor, in production too. Production now answers the status and the
// request id (X-Request-ID); the log record keeps the error under that id, at warning level for a host
// with no site, which any client can send. While developing (AllowLocal) the answer is the error.
// CONTROL: a site that compiles answers 200.
func TestSiteThatCouldNotAnswerShowsVisitorsOnlyTheRequestID(t *testing.T) {
	const compileError = "compile router router.kitwork.js"
	savedLocal, savedLog := work.AllowLocal, slog.Default()
	t.Cleanup(func() {
		work.AllowLocal = savedLocal
		slog.SetDefault(savedLog)
	})
	for _, developing := range []bool{false, true} {
		work.AllowLocal = developing
		var logged bytes.Buffer
		slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))

		root := t.TempDir()
		routerFile := writeTreeTenant(t, root, "unused")
		if err := os.WriteFile(routerFile, []byte(invalidGenerationRouter), 0o644); err != nil {
			t.Fatal(err)
		}
		engine := New(root, 0, false, "")
		ask := func(target, id string) *httptest.ResponseRecorder {
			logged.Reset()
			request := httptest.NewRequest(http.MethodGet, target, nil)
			request.Header.Set("X-Request-ID", id)
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			return response
		}

		broken := ask("http://localhost/", "req-boot")
		record := logged.String()
		if broken.Code != http.StatusNotFound || broken.Header().Get("X-Request-ID") != "req-boot" {
			t.Errorf("developing=%v: a site that did not boot = %d, X-Request-ID %q", developing, broken.Code, broken.Header().Get("X-Request-ID"))
		}
		if developing && !strings.Contains(broken.Body.String(), compileError) {
			t.Errorf("developing: the answer lacks the compile error: %q", broken.Body.String())
		}
		if !developing && broken.Body.String() != "Not Found\nRequest ID: req-boot\n" {
			t.Errorf("production: a site that did not boot answered %q, want the status and the request id only", broken.Body.String())
		}
		if !strings.Contains(record, "level=ERROR") || !strings.Contains(record, "request_id=req-boot") || !strings.Contains(record, compileError) {
			t.Errorf("developing=%v: the log record lacks the request id or the compile error:\n%s", developing, record)
		}

		nowhere := ask("http://nowhere.example/", "req-nowhere")
		record = logged.String()
		if !developing && (nowhere.Code != http.StatusNotFound || nowhere.Body.String() != "Not Found\nRequest ID: req-nowhere\n") {
			t.Errorf("production: a host with no site answered %d %q, want the status and the request id only", nowhere.Code, nowhere.Body.String())
		}
		if !strings.Contains(record, "level=WARN") || !strings.Contains(record, "request_id=req-nowhere") || !strings.Contains(record, "nowhere.example") {
			t.Errorf("developing=%v: the record of a host with no site:\n%s", developing, record)
		}

		// CONTROL: the site, once it compiles, answers
		writeRouterBody(t, routerFile, "booted")
		engine.Close()
		engine = New(root, 0, false, "")
		if fine := ask("http://localhost/", "req-fine"); fine.Code != http.StatusOK || fine.Body.String() != "booted" {
			t.Errorf("CONTROL developing=%v: a site that compiles = %d %q", developing, fine.Code, fine.Body.String())
		}
		engine.Close()
	}
}
