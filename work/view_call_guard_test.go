package work

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The run after a job goes through the guards of the branch and of router.view again, with the cookies the
// job set or removed, as the browser will send them next (KITWORK_ROUTER_VIEW.md câu 5f, 5g, 28/10/2026): a
// guard that sends the page away answers { seq, redirect } and the page follows; a guard that refuses answers
// { seq, reload } and the page opens its URL again, to the guard's answer; the data of the run after the job
// never reaches a page its guard no longer lets through.
func guardedViewSite(t *testing.T) *Tenant {
	t.Helper()
	tn := NewTenant(writeSite(t, map[string]string{
		"router.work":     `import * as router from "kitwork/router";`,
		"view.ui":         "<view><content /></view>",
		"desk/content.ui": `<content><text>{{ name }}</text><text>{{ secret }}</text><input value="{{ note }}" /></content>`,
		"desk/router.work": `import * as router from "kitwork/router";

const gate = (context) => {
  if (context.cookie("session") !== "in") {
    return context.redirect("/login");
  }
  return context.cookie("role") !== "guest";
};

router.view((context) => {
  var note = "";
  const name = context.cookie("name") ?? "";
  const secret = "only for the signed in";
  const logout = () => { context.cookie("session", null); };
  const demote = () => { context.cookie("role", "guest"); };
  const rename = () => { context.cookie("name", "Binh"); };
  return context.bind({ note, name, secret, logout, demote, rename });
}).guard(gate);
`,
	}), "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	return tn
}

func guardedCall(t *testing.T, tn *Tenant, job string) (map[string]any, *httptest.ResponseRecorder) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "http://localhost/desk?"+ViewCallParameter+"="+job, strings.NewReader(callBody{Seq: 5, Args: []any{}, Page: vars("note", "")}.String()))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", "session=in; name=An")
	rec := httptest.NewRecorder()
	tn.Serve(rec, req)
	var answer map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
		t.Fatalf("%s answered no JSON: %d %q", job, rec.Code, rec.Body.String())
	}
	return answer, rec
}

func TestRunAfterAJobGoesThroughTheGuards(t *testing.T) {
	tn := guardedViewSite(t)

	// the guard lets the page on: the run after the job reads the cookie the job set
	answer, rec := guardedCall(t, tn, "rename")
	if rec.Code != http.StatusOK || instanceOf(answer, pagePlace)["name"] != "Binh" {
		t.Errorf("rename: the run after the job should read the cookie it set (name Binh): %d %s", rec.Code, rec.Body.String())
	}

	// the job signs out: the guard sends the page to /login, with the cookie removed, and no data of the page
	answer, rec = guardedCall(t, tn, "logout")
	if answer["redirect"] != "/login" || answer["seq"] != float64(5) || answer[pagePlace] != nil {
		t.Errorf("logout: the guard of the run after the job should send the page to /login: %d %s", rec.Code, rec.Body.String())
	}
	if cookie := rec.Header().Get("Set-Cookie"); !strings.Contains(cookie, "session=") || !strings.Contains(cookie, "Max-Age=0") {
		t.Errorf("logout: the cookie the job removed should go with the answer, got %q", cookie)
	}

	// the job takes the page away from the caller: the guard refuses, the page reloads, no data of the page
	answer, rec = guardedCall(t, tn, "demote")
	if answer["reload"] != true || answer[pagePlace] != nil || strings.Contains(rec.Body.String(), "only for") {
		t.Errorf("demote: the guard of the run after the job refuses, the page should reload: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Header().Get("Set-Cookie"), "role=guest") {
		t.Errorf("demote: the cookie the job set should go with the answer, got %q", rec.Header().Get("Set-Cookie"))
	}
}
