package work

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"testing"
)

// Calls of one page's jobs at the same time (28/10/2026): the todo page's view reads the list, its job writes
// the list read with a row added. Two calls that run their view before either writes keep one row of the two:
// twenty adds at once kept five. The engine runs one call of a page's jobs at a time, from the run before the
// job to the run after it, so each job writes on what the one before it wrote.
func TestCallsOfOnePageAtTheSameTimeLoseNothing(t *testing.T) {
	tn := patchTodoSite(t)
	const n = 20
	var wg sync.WaitGroup
	start := make(chan struct{})
	codes := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			r := postCall(tn, "/todo", "add", callBody{Seq: 1, Args: []any{}, Page: vars("title", "R"+strconv.Itoa(i))}.String())
			codes[i] = r.Code
		}(i)
	}
	close(start)
	wg.Wait()
	for i, code := range codes {
		if code != http.StatusOK {
			t.Fatalf("call %d answered %d", i, code)
		}
	}
	page := serveGet(tn, "/todo", nil).Body.String()
	m := regexp.MustCompile(`data-kitwork-page>(.*?)</script>`).FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("no data block:\n%.2000s", page)
	}
	var block struct {
		State struct {
			Todos []struct {
				ID    float64 `json:"id"`
				Title string  `json:"title"`
			} `json:"todos"`
		} `json:"page"`
	}
	if err := json.Unmarshal([]byte(m[1]), &block); err != nil {
		t.Fatal(err)
	}
	titles, ids := map[string]bool{}, map[float64]bool{}
	for _, todo := range block.State.Todos {
		titles[todo.Title] = true
		ids[todo.ID] = true
	}
	if len(block.State.Todos) != n || len(titles) != n || len(ids) != n {
		t.Fatalf("%d adds at once should keep %d rows with %d ids, kept %d rows, %d titles, %d ids", n, n, n,
			len(block.State.Todos), len(titles), len(ids))
	}
}
