package work

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
	"time"
)

// TestShoppingMigrationAppCanary is opt-in because it opens the local
// migration artifact and builds its 100k-document projection on first use.
func TestShoppingMigrationAppCanary(t *testing.T) {
	root := os.Getenv("KITDB_SHOPPING_CANARY_ROOT")
	if root == "" {
		t.Skip("KITDB_SHOPPING_CANARY_ROOT is not set")
	}
	started := time.Now()
	tenant := NewTenant(root, "shopping.local")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	defer tenant.Close()
	t.Logf("shopping tenant loaded in %s", time.Since(started))

	firstSearch := time.Now()
	folded := requestShoppingMigrationSearch(t, tenant, "http://shopping.local/?q=ban%20phim%20logitech")
	firstDuration := time.Since(firstSearch)
	secondSearch := time.Now()
	accented := requestShoppingMigrationSearch(t, tenant, "http://shopping.local/?q=b%C3%A0n%20ph%C3%ADm%20logitech")
	secondDuration := time.Since(secondSearch)
	if len(folded.Results) == 0 || len(accented.Results) == 0 {
		t.Fatalf("shopping search returned no rows: folded=%d accented=%d", len(folded.Results), len(accented.Results))
	}
	if !reflect.DeepEqual(shoppingResultIDs(folded.Results), shoppingResultIDs(accented.Results)) {
		t.Fatalf("accent folding changed ranked identifiers")
	}
	for _, result := range folded.Results {
		if result["_score"] == nil || result["_snippet"] == nil || result["_key"] == nil {
			t.Fatalf("shopping search result lacks projection metadata: %#v", result)
		}
	}
	if manager := tenant.searchManager; manager == nil {
		t.Fatal("shopping search manager was not created")
	} else if stats := manager.Stats(); stats.Commits > 1 {
		t.Fatalf("two stable searches published %d generations, want at most 1", stats.Commits)
	} else {
		t.Logf("shopping search: first=%s second=%s results=%d stats=%+v", firstDuration, secondDuration, len(folded.Results), stats)
	}
}

type shoppingMigrationResponse struct {
	OK       bool             `json:"ok"`
	Database string           `json:"database"`
	Query    string           `json:"query"`
	Results  []map[string]any `json:"results"`
}

func requestShoppingMigrationSearch(t *testing.T, tenant *Tenant, target string) shoppingMigrationResponse {
	t.Helper()
	recorder := httptest.NewRecorder()
	tenant.Serve(recorder, httptest.NewRequest(http.MethodGet, target, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("shopping route status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response shoppingMigrationResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode shopping response: %v; body=%s", err, recorder.Body.String())
	}
	if !response.OK || response.Database != "shopping_shadow" {
		t.Fatalf("unexpected shopping response: %#v", response)
	}
	return response
}

func shoppingResultIDs(results []map[string]any) []string {
	identifiers := make([]string, len(results))
	for position, result := range results {
		identifiers[position], _ = result["_key"].(string)
	}
	return identifiers
}
