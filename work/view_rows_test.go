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

// A refresh runs the page with the vars the tab holds, as a job does (28/10/2026): a search's results, which
// read the query the tab typed, come for that query, not for an empty one, when another tab adds a word.
func TestARefreshRunsWithTheTabsVars(t *testing.T) {
	tn := NewTenant(writeSite(t, map[string]string{
		"router.work":  `import * as router from "kitwork/router";`,
		"view.ui":      "<view><content /></view>",
		"s/content.ui": `<content><input value="{{ query }}" /><list><item for="word of found; key: word"><text>{{ word }}</text></item></list></content>`,
		"s/router.work": `import * as router from "kitwork/router";
import { datastore } from "kitwork/storage";

const list = datastore("words").segment("all");

router.view((context) => {
  var query = "";
  const words = list.get() ?? [];
  const found = words.filter((word) => word.includes(query));
  const add = (word) => { list.set(words.concat([word])); };
  return context.bind({ query, found, add });
});
`,
	}), "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, word := range []string{"apple", "banana", "cherry", "blueberry"} {
		postCall(tn, "/s", "add", `{"args":["`+word+`"],"vars":{"query":""},"seq":1}`)
	}
	rec := httptest.NewRecorder()
	tn.Serve(rec, httptest.NewRequest(http.MethodPost, "http://localhost/s?"+ViewRefreshParameter+"=1", strings.NewReader(`{"seq":2,"revs":{},"vars":{"query":"b"}}`)))
	var answer struct {
		Patch map[string]any `json:"patch"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &answer) != nil {
		t.Fatalf("refresh: %d %q", rec.Code, rec.Body.String())
	}
	found, _ := answer.Patch["found"].([]any)
	if len(found) != 2 || found[0] != "banana" || found[1] != "blueberry" {
		t.Fatalf("a refresh should run with the tab's query (b): banana and blueberry, got %s", rec.Body.String())
	}
	if _, sent := answer.Patch["query"]; sent {
		t.Errorf("a var is the tab's own, never sent back: %s", rec.Body.String())
	}
}
