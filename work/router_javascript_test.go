package work

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strings"
	"testing"

	jitjs "github.com/kitwork/engine/jit/js"
	"github.com/kitwork/engine/site"
)

// servedScript is a script file the engine delivers, under its one prefix /_kitwork/ (/kit/ is
// answered by nothing since 26/10/2026).
var servedScript = regexp.MustCompile(`src="?(/_kitwork/[^"\s>]+)`)

func homeOf(t *testing.T, tenant *Tenant) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	tenant.Serve(recorder, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET / = %d", recorder.Code)
	}
	return recorder.Body.String()
}

// Nothing can carry a version pin or a pre-render switch any more: the site's presentation and the
// JavaScript delivery options have no field for one. (A version written
// in a template page's own markup named one until 26/10/2026; nothing does now.)
func TestNothingCarriesAVersionPinOrPrerender(t *testing.T) {
	for _, carrier := range []any{site.PresentationSnapshot{}, jitjs.Options{}} {
		kind := reflect.TypeOf(carrier)
		for i := 0; i < kind.NumField(); i++ {
			field := kind.Field(i)
			name := strings.ToLower(field.Name)
			if strings.Contains(name, "version") || strings.Contains(name, "prerender") || strings.Contains(name, "pin") {
				t.Errorf("%s still has %s: router.javascript() and its pins are gone", kind, field.Name)
			}
		}
		if kind.NumField() == 0 {
			t.Errorf("control: %s has no fields, the check proves nothing", kind)
		}
	}
	// control: the check does find a field that carries one
	type pinned struct{ ComponentVersions map[string]string }
	if field, ok := reflect.TypeOf(pinned{}).FieldByName("ComponentVersions"); !ok || !strings.Contains(strings.ToLower(field.Name), "version") {
		t.Error("control: the check must recognise a version field")
	}
}

// Each file the page names is served, cached for good; an old hash still gets today's file, not kept.
// The site loads every page whole (router.reload(true)), so the page names the kernel and its two
// components and nothing of Drive.
func TestKitFilesAreServedImmutable(t *testing.T) {
	tenant := buildUISiteMode(t, map[string]string{
		"localhost/router.work": "import * as router from \"kitwork/router\";\nrouter.reload(true);\n",
		"localhost/view.ui":     `<view><content /></view>`,
		"localhost/content.ui": `<content>
  <tabs as="$files" alt="Files" flex="row"><button click="select('a')">a</button><button click="select('b')">b</button></tabs>
  <box show="$files.selected == 'a'"><text>a</text></box>
  <box show="$files.selected == 'b'"><text>b</text></box>
  <dropdown><button click="toggle()" expanded="{{ open }}">Mở</button></dropdown>
</content>`,
	}, false)
	t.Cleanup(tenant.Close)
	out := homeOf(t, tenant)
	paths := servedScript.FindAllStringSubmatch(out, -1)
	if len(paths) != 3 {
		t.Fatalf("want kernel, tabs and dropdown, got %v in:\n%s", paths, out)
	}
	for _, m := range paths {
		recorder := httptest.NewRecorder()
		tenant.Serve(recorder, httptest.NewRequest(http.MethodGet, "http://localhost"+m[1], nil))
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Header().Get("Cache-Control"), "immutable") || recorder.Body.Len() == 0 {
			t.Errorf("%s: status %d, cache %q, %d bytes", m[1], recorder.Code, recorder.Header().Get("Cache-Control"), recorder.Body.Len())
		}
		again := httptest.NewRequest(http.MethodGet, "http://localhost"+m[1], nil)
		again.Header.Set("If-None-Match", recorder.Header().Get("ETag"))
		revalidated := httptest.NewRecorder()
		tenant.Serve(revalidated, again)
		if revalidated.Code != http.StatusNotModified {
			t.Errorf("%s: a matching ETag is a 304, got %d", m[1], revalidated.Code)
		}
	}
	// (A site's own _components file, answered by its own site only, went on 26/10/2026.)
	// An old hash — HTML cached before a deploy — keeps its JavaScript, but the file is not kept
	// under a name that is not its own.
	wrong := paths[0][1][:strings.LastIndex(paths[0][1], ".")-16] + "0000000000000000.js"
	recorder := httptest.NewRecorder()
	tenant.Serve(recorder, httptest.NewRequest(http.MethodGet, "http://localhost"+wrong, nil))
	if recorder.Code != http.StatusOK || recorder.Header().Get("Cache-Control") != "no-cache" || recorder.Body.Len() == 0 {
		t.Errorf("%s: want today's file, no-cache; got %d %q", wrong, recorder.Code, recorder.Header().Get("Cache-Control"))
	}
	// A name that means nothing is a 404.
	recorder = httptest.NewRecorder()
	tenant.Serve(recorder, httptest.NewRequest(http.MethodGet, "http://localhost/_kitwork/components/nonesuch.0000000000000000.js", nil))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("an unknown component: want 404, got %d", recorder.Code)
	}
}
