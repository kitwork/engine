package work

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A call names its job in the page's URL, POST /todo?kitwork-call=add (KITWORK_ROUTER_VIEW.md câu 7b,
// 28/10/2026), and needs no header: the desktop's shell passes a request's method, URL and body and no header
// at all, Android's Content-Type and Accept only. The engine takes the name out of the URL before anything
// reads it: the view's function reads the page's own URL, its own query as it was written. The job's name in a
// header is no call.
func urlViewSite(t *testing.T) *Tenant {
	t.Helper()
	tn := NewTenant(writeSite(t, map[string]string{
		"router.work":      `import * as router from "kitwork/router";`,
		"view.ui":          "<view><content /></view>",
		"board/content.ui": `<content><text>{{ seen }}</text><text>{{ tab }}</text></content>`,
		"board/router.work": `import * as router from "kitwork/router";

router.view((context) => {
  var seen = "";
  var tab = "";
  const look = () => {
    seen = context.query("kitwork-call") || "none";
    tab = context.query("tab") || "none";
  };
  return context.bind({ seen, tab, look });
});
`,
	}), "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	return tn
}

func TestACallNamesItsJobInTheURL(t *testing.T) {
	tn := patchTodoSite(t)

	// as the desktop's shell sends it: method, URL and body, no header at all
	req := httptest.NewRequest(http.MethodPost, "http://localhost/todo?kitwork-call=add", strings.NewReader(`{"args":[],"page":{"title":"Mua sữa"},"seq":1}`))
	rec := httptest.NewRecorder()
	tn.Serve(rec, req)
	var answer map[string]any
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &answer) != nil || answer[pagePlace] == nil {
		t.Fatalf("a call naming its job in the URL, with no header, should be answered with what changed: %d %q", rec.Code, rec.Body.String())
	}

	// the job's name in a header, and none in the URL, is no call
	req = httptest.NewRequest(http.MethodPost, "http://localhost/todo", strings.NewReader(`{"args":[],"page":{"title":"x"},"seq":2}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Kitwork-Call", "add")
	rec = httptest.NewRecorder()
	tn.Serve(rec, req)
	if rec.Code == http.StatusOK && strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Errorf("the job's name in a header should be no call: %d %q", rec.Code, rec.Body.String())
	}

	// a name that is no job's is the request's own error
	req = httptest.NewRequest(http.MethodPost, "http://localhost/todo?kitwork-call=a%20b", strings.NewReader(`{}`))
	rec = httptest.NewRecorder()
	tn.Serve(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("a name that is no job's should answer 400: %d %q", rec.Code, rec.Body.String())
	}
}

func TestTheViewReadsThePagesOwnURL(t *testing.T) {
	tn := urlViewSite(t)
	req := httptest.NewRequest(http.MethodPost, "http://localhost/board?tab=2&kitwork-call=look", strings.NewReader(`{"args":[],"page":{"seen":"","tab":""},"seq":1}`))
	rec := httptest.NewRecorder()
	tn.Serve(rec, req)
	var answer struct {
		Patch map[string]any `json:"page"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &answer) != nil {
		t.Fatalf("look: %d %q", rec.Code, rec.Body.String())
	}
	if answer.Patch["seen"] != "none" || answer.Patch["tab"] != "2" {
		t.Errorf("the job should read the page's own URL, its query and not the job's name: %s", rec.Body.String())
	}
}
