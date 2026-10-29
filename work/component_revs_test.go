package work

import (
	"encoding/json"
	"net/http"
	"regexp"
	"testing"
)

// An instance's data has revisions as a page's does (29/10/2026): the components block carries those of each
// instance by place, its consts and inputs, not its vars nor its jobs; a call sends them back. The field of the
// word picker sets query in the browser, so the run before the search already reads it and finds what the run
// after finds: only the revisions say the tab still shows what the page was drawn with, and the results come
// whole. A call that changes nothing the tab holds brings nothing, which needs the page's revisions and the
// server's to agree.
//
// CONTROL (by hand, 29/10/2026): with serveComponentCall not reading scopeRevs, the search brought nothing
// (red); with the block written without revisions, the block lacked them (red); with the page hashing an
// instance's data otherwise than the server, the call that changed nothing brought found whole (red).
func TestAnInstanceGetsWhatItsTabHoldsOlder(t *testing.T) {
	tn := pickerSite(t)
	for _, word := range []string{"apple", "apricot", "banana"} {
		postCall(tn, "/pick", "word-picker.add", callBody{Seq: 1, Args: []any{word}, Page: vars(), At: placesOf(t, tn, "/pick")[0], Mine: vars("query", "")}.String())
	}
	page := serveGet(tn, "/pick", nil).Body.String()
	m := regexp.MustCompile(`data-kitwork-page>(.*?)</script>`).FindStringSubmatch(page)
	var block struct {
		Bridge struct {
			Revs map[string]map[string]string `json:"revs"`
		} `json:"bridge"`
	}
	if m == nil || json.Unmarshal([]byte(m[1]), &block) != nil {
		t.Fatalf("the page should carry its data block:\n%.2000s", page)
	}
	drawn := block.Bridge.Revs[placesOf(t, tn, "/pick")[0]]
	if drawn["found"] == "" || drawn["query"] != "" || drawn["search"] != "" {
		t.Fatalf("the block should carry the revisions of the instance's consts, not of its vars nor jobs: %s", m[1])
	}

	at := placesOf(t, tn, "/pick")[0]
	call := func(query string, revs map[string]string) (map[string]any, string) {
		r := postCall(tn, "/pick", "word-picker.search", callBody{Seq: 2, Args: []any{query}, Page: vars(), At: at, Mine: vars("query", query),
			Revs: map[string]map[string]string{at: revs}}.String())
		if r.Code != http.StatusOK {
			t.Fatalf("search: %d %s", r.Code, r.Body.String())
		}
		return componentAnswer(t, r.Body.String()), r.Body.String()
	}

	// typed "ap" in the field: the tab shows found as drawn, for an empty query
	answer, body := call("ap", drawn)
	patch, revs := instanceOf(answer, at), revsOf(answer, at)
	if patch["found"] != float64(2) || revs["found"] == nil || revs["found"] == drawn["found"] {
		t.Fatalf("a tab holding found as drawn should get it whole, with its new revision: %s", body)
	}
	if _, has := revs["query"]; has {
		t.Errorf("a var has no revision: %s", body)
	}

	// the same again, holding what the answer brought: nothing
	if answer, body := call("ap", map[string]string{"found": revs["found"].(string)}); answer[at] != nil || revsOf(answer, at) != nil {
		t.Errorf("a tab that holds the instance as it is should get nothing of it: %s", body)
	}
	// the query the page was drawn with, holding the block's revisions: nothing, the page and the server agree
	if answer, body := call("", drawn); answer[at] != nil || revsOf(answer, at) != nil {
		t.Errorf("a tab that holds the instance as drawn should get nothing of it: the page's revisions should be the server's: %s", body)
	}
}
