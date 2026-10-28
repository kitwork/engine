package work

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A tab that asks for what changed while it held a list at a kept revision (view_rows.go, 28/10/2026) gets the
// rows that changed since, not the list whole: thirty rows drawn, then one added and one ticked elsewhere, and
// the refresh brings those two rows (the count, thirty left either way, does not change).
//
// CONTROL (by hand, 28/10/2026): with serveViewRefresh not calling rowsSince, this test went red: the refresh
// brought the thirty-one rows whole.
func TestARefreshBringsTheRowsThatChanged(t *testing.T) {
	tn := patchTodoSite(t)
	for i := 0; i < 30; i++ {
		callAnswer(t, tn, "add", `{"args":[],"vars":{"title":"row"},"seq":1}`)
	}
	held, _ := json.Marshal(pageRevs(t, serveGet(tn, "/todo", nil).Body.String()))
	callAnswer(t, tn, "add", `{"args":[],"vars":{"title":"new"},"seq":2}`)
	callAnswer(t, tn, "toggle", `{"args":[7,true],"vars":{"title":""},"seq":3}`)

	req := httptest.NewRequest(http.MethodPost, "http://localhost/todo?"+ViewRefreshParameter+"=1", strings.NewReader(`{"seq":4,"revs":`+string(held)+`}`))
	rec := httptest.NewRecorder()
	tn.Serve(rec, req)
	var answer struct {
		Patch map[string]any `json:"patch"`
		Lists map[string]struct {
			Set    []map[string]any `json:"set"`
			Remove []any            `json:"remove"`
		} `json:"lists"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &answer) != nil {
		t.Fatalf("refresh: %d %q", rec.Code, rec.Body.String())
	}
	if _, whole := answer.Patch["todos"]; whole {
		t.Fatalf("a tab holding a kept version should get the rows that changed, not the list whole: %.300s", rec.Body.String())
	}
	set := answer.Lists["todos"].Set
	if len(set) != 2 || set[0]["id"] != float64(7) || set[1]["id"] != float64(31) {
		t.Fatalf("the refresh should bring the row ticked (7) and the row added (31): %s", rec.Body.String())
	}
}

// pageRevs reads the revisions of a page's data block.
func pageRevs(t *testing.T, body string) map[string]string {
	t.Helper()
	start := strings.Index(body, "data-kitwork-page>")
	end := strings.Index(body[start:], "</script>")
	var block struct {
		Revs map[string]string `json:"revs"`
	}
	if start < 0 || end < 0 || json.Unmarshal([]byte(body[start+len("data-kitwork-page>"):start+end]), &block) != nil {
		t.Fatalf("no data block:\n%.1500s", body)
	}
	return block.Revs
}
