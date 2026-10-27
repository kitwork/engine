package work

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// M0 of the Native Bridge RFC: the "In-Memory Route Mapper" IS the tree router. A native shell
// serves a tenant by handing tenant.Serve a synthetic request and a recorder — no socket, no
// localhost, no engine changes. This test proves the recorder path is byte-identical to real HTTP
// for every response class a shell needs: the HTML page, the client kernel, a static asset and a
// JSON api. kitwork://app is the custom scheme WebView traffic arrives on.
func TestNativeInMemoryServe(t *testing.T) {
	tmp, err := os.MkdirTemp("", "kitwork-native-m0-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	dir := filepath.Join(tmp, "test", "app.local")
	write := func(rel, content string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("router.work", `import * as router from "kitwork/router";`+"\n"+`router.meta({ title: "native" });`)
	write("view.ui", `<view><content /></view>`)
	write("content.ui", `<content><dropdown><button click="open = !open">menu</button></dropdown></content>`)
	write("notfound.ui", `<content><text>404</text></content>`)
	write("assets/logo.svg", `<svg xmlns="http://www.w3.org/2000/svg"/>`)
	write("api/router.work",
		`import * as router from "kitwork/router";`+"\n"+`router.get((context) => context.json({ native: true }));`)

	tenant := NewTenant(tmp, "app.local")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}

	// The shell's whole route mapper — no TCP anywhere.
	inMemory := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "kitwork://app"+path, nil)
		req.Host = "app.local"
		rec := httptest.NewRecorder()
		tenant.Serve(rec, req)
		return rec
	}

	// The same tenant over REAL HTTP, as the reference.
	server := httptest.NewServer(http.HandlerFunc(tenant.Serve))
	defer server.Close()
	overHTTP := func(path string) (int, string, []byte) {
		resp, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header.Get("Content-Type"), body
	}

	// The client kernel is a file named by its hash, so its path is read off the page (production
	// drops the quotes around src).
	kernel := ""
	if found := regexp.MustCompile(`src="?(/_kitwork/kernel\.[0-9a-f]+\.js)`).FindStringSubmatch(inMemory("/").Body.String()); found != nil {
		kernel = found[1]
	}
	if kernel == "" {
		t.Fatal("the home page carries no kernel file")
	}
	for _, path := range []string{"/", kernel, "/assets/logo.svg", "/api", "/nowhere"} {
		rec := inMemory(path)
		status, contentType, body := overHTTP(path)
		if rec.Code != status {
			t.Errorf("%s: status recorder=%d http=%d", path, rec.Code, status)
		}
		if got := rec.Header().Get("Content-Type"); got != contentType {
			t.Errorf("%s: content-type recorder=%q http=%q", path, got, contentType)
		}
		if rec.Body.String() != string(body) {
			t.Errorf("%s: body differs (recorder %db vs http %db)", path, rec.Body.Len(), len(body))
		}
	}

	// Sanity on the interesting bytes: page carries the kernel reference + directive; api is json.
	home := inMemory("/").Body.String()
	if !strings.Contains(home, "/_kitwork/kernel.") || !strings.Contains(home, `data-kitwork-event="click: open = !open"`) {
		t.Errorf("home over kitwork:// lost its kernel/directives: %s", home)
	}
	if api := inMemory("/api").Body.String(); !strings.Contains(api, `"native":true`) {
		t.Errorf("api over kitwork://: %s", api)
	}
}
