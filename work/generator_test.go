package work

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kitwork/engine/version"
)

// A template page (.kitwork.html) names the engine in <head> like a Kitwork UI page does.
func TestTemplatePageNamesTheGenerator(t *testing.T) {
	tenant, _ := writeTestSite(t, "", `<main>x</main>`)
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	tenant.Serve(recorder, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
	body := recorder.Body.String()
	if !strings.Contains(body, "Kitwork "+version.Number) {
		t.Fatalf("GET / = %d is missing the generator Kitwork %s\n%.400s", recorder.Code, version.Number, body)
	}
	// production minifies away the optional <head>/</head>; what comes before the page's content is
	// the head
	if strings.Index(body, "Kitwork "+version.Number) > strings.Index(body, "<main>") {
		t.Errorf("the generator belongs in <head>, before the page's content:\n%s", body)
	}
}
