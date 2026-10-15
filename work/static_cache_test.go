package work

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A site's static files carry a Cache-Control and validators (14/10/2026): until then
// /assets/logo.svg had no Cache-Control, so the browser guessed how long to keep it. A name that
// carries the hash of its bytes is kept a year and never asked again; any other name is kept an hour
// in production and asked again on every use while developing; an ETag and Last-Modified make the
// question a 304 without the bytes. Served from a router.assets folder, and the same from a site that
// declares none (any safe file).
//
// CONTROLS: logo.20261014.svg spells eight hex digits that are a date, not the hash of its bytes, and
// app.0000000000000000.js and app.abcdef0123456789.js a hash that is not their own: none is kept a
// year. A request whose validator does not match gets the bytes.
func TestStaticFilesCarryCacheControlAndValidators(t *testing.T) {
	script := "console.log('kitwork');\n"
	sum := sha256.Sum256([]byte(script))
	hashed := "app." + hex.EncodeToString(sum[:])[:16] + ".js"
	assets := map[string]string{
		"localhost/assets/logo.svg":                `<svg xmlns="http://www.w3.org/2000/svg"/>`,
		"localhost/assets/logo.20261014.svg":       `<svg xmlns="http://www.w3.org/2000/svg"><g/></svg>`,
		"localhost/assets/" + hashed:               script,
		"localhost/assets/app.0000000000000000.js": script,
		"localhost/assets/app.abcdef0123456789.js": script,
	}
	for _, site := range []struct {
		label  string
		router string // what the root router.work adds
	}{
		{"a site with no router.assets", ""},
		{"a router.assets folder", "\nrouter.assets(\"assets\");"},
	} {
		for _, developing := range []bool{false, true} {
			files := withRootRouter(t, sitePalette+site.router, assets)
			tenant := buildUISiteMode(t, files, developing)
			ask := func(path string, header map[string]string) *httptest.ResponseRecorder {
				request := httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil)
				for name, value := range header {
					request.Header.Set(name, value)
				}
				recorder := httptest.NewRecorder()
				tenant.Serve(recorder, request)
				return recorder
			}
			lifetime := "public, max-age=3600"
			if developing {
				lifetime = "no-cache"
			}
			for path, want := range map[string]string{
				"/assets/logo.svg":                lifetime,
				"/assets/logo.20261014.svg":       lifetime, // CONTROL: a date is not a hash
				"/assets/app.0000000000000000.js": lifetime, // CONTROL: a hash, not this file's
				"/assets/app.abcdef0123456789.js": lifetime, // CONTROL: the same, read and compared
				"/assets/" + hashed:               "public, max-age=31536000, immutable",
			} {
				answer := ask(path, nil)
				if answer.Code != http.StatusOK {
					t.Fatalf("%s, developing %v: %s = %d", site.label, developing, path, answer.Code)
				}
				if got := answer.Header().Get("Cache-Control"); got != want {
					t.Errorf("%s, developing %v: %s Cache-Control = %q, want %q", site.label, developing, path, got, want)
				}
				etag, modified := answer.Header().Get("ETag"), answer.Header().Get("Last-Modified")
				if !strings.HasPrefix(etag, `W/"`) || modified == "" {
					t.Fatalf("%s: %s has ETag %q and Last-Modified %q, want both", site.label, path, etag, modified)
				}
				if again := ask(path, map[string]string{"If-None-Match": etag}); again.Code != http.StatusNotModified || again.Body.Len() != 0 {
					t.Errorf("%s: %s with its ETag = %d (%d bytes), want 304 and no bytes", site.label, path, again.Code, again.Body.Len())
				}
				if again := ask(path, map[string]string{"If-Modified-Since": modified}); again.Code != http.StatusNotModified {
					t.Errorf("%s: %s with its Last-Modified = %d, want 304", site.label, path, again.Code)
				}
				// CONTROL: another validator gets the file
				if other := ask(path, map[string]string{"If-None-Match": `"0-0"`}); other.Code != http.StatusOK || other.Body.Len() == 0 {
					t.Errorf("%s: %s with another ETag = %d, want the file", site.label, path, other.Code)
				}
			}
		}
	}
}
