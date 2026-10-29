package work

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Each answer of a page carries the moment the server read its data (stamp, 28/10/2026), the page drawn too:
// a tab takes no data read before the data it holds (call.js). Twenty adds at once, each from a tab that holds
// the page as it was drawn: in the order of their stamps, each answer counts one row more than the one before,
// all after the page's stamp, and a refresh after them comes after them all.
func TestAnswersOfAPageAreStampedInTheOrderTheirDataWasRead(t *testing.T) {
	tn := patchTodoSite(t)
	page := serveGet(tn, "/todo", nil).Body.String()
	m := regexp.MustCompile(`data-kitwork-page>(.*?)</script>`).FindStringSubmatch(page)
	var block struct {
		Bridge struct {
			Revs  map[string]map[string]string `json:"revs"`
			Stamp float64                      `json:"stamp"`
		} `json:"bridge"`
	}
	if m == nil || json.Unmarshal([]byte(m[1]), &block) != nil || block.Bridge.Stamp <= 0 || len(block.Bridge.Revs["page"]) == 0 {
		t.Fatalf("the page's data block should carry its stamp with its revisions:\n%.2000s", page)
	}
	revs, _ := json.Marshal(block.Bridge.Revs["page"])

	const n = 20
	type answer struct {
		stamp, left float64
		body        string
	}
	answers := make([]answer, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			r := postCall(tn, "/todo", "add", `{"args":[],"vars":{"title":"R`+strconv.Itoa(i)+`"},"seq":1,"revs":`+string(revs)+`}`)
			var a struct {
				Stamp float64        `json:"stamp"`
				Patch map[string]any `json:"patch"`
			}
			json.Unmarshal(r.Body.Bytes(), &a)
			left, _ := a.Patch["left"].(float64)
			answers[i] = answer{a.Stamp, left, r.Body.String()}
		}(i)
	}
	close(start)
	wg.Wait()
	sort.Slice(answers, func(i, j int) bool { return answers[i].stamp < answers[j].stamp })
	last := block.Bridge.Stamp
	for i, a := range answers {
		if a.stamp <= last {
			t.Fatalf("answer %d: its stamp %.0f should come after %.0f: %.300s", i, a.stamp, last, a.body)
		}
		if a.left != float64(i+1) {
			t.Fatalf("in the order of their stamps, answer %d should count %d rows, counts %.0f: the data of a greater stamp was read after: %.300s", i, i+1, a.left, a.body)
		}
		last = a.stamp
	}

	req := httptest.NewRequest(http.MethodPost, "http://localhost/todo?"+ViewRefreshParameter+"=1", strings.NewReader(`{"seq":3,"revs":`+string(revs)+`}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	tn.Serve(rec, req)
	var refreshed struct {
		Stamp float64 `json:"stamp"`
	}
	if json.Unmarshal(rec.Body.Bytes(), &refreshed) != nil || refreshed.Stamp <= last {
		t.Fatalf("a refresh after the adds should come after them all (%.0f): %d %q", last, rec.Code, rec.Body.String())
	}
}
