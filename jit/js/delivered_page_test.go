package js

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// testPage is a hand-written test page served the way the engine serves a real one: Deliver gives
// it its files (/kit/kernel.<hash>.js, /kit/drive…, a file per directive and component), and the
// browser loads exactly those bytes from exactly those names. Until 03/10 the browser proofs got one
// composed string at /kit.js instead — a composition production had stopped serving, which is how a
// directive file could be missing from every proof without one of them noticing.
type testPage struct {
	html  string
	files map[string]string // a page's own scripts, as files
	site  Site
}

// The tag hand-written pages used to load the composed runtime with. Scripts BEFORE it ran before
// the kernel existed and stay inline, so they still do; scripts AFTER it ran with the kernel loaded
// and not yet started, so they become deferred files that follow the kernel's — deferred scripts
// run in order, before DOMContentLoaded, which is when the kernel starts.
var legacyRuntimeTag = regexp.MustCompile(`<script src="/kit\.js"(?: defer)?></script>\s*`)
var pageInlineScript = regexp.MustCompile(`(?s)<script>(.*?)</script>`)
var pageExternalScript = regexp.MustCompile(`<script src="(/[^"]+)"></script>`)

// deliverTestPage prepares page for a browser proof. components lists what the proof needs even if
// the markup does not name it (a component a script inserts later); everything the markup names
// comes through Deliver like on a real page. The kernel carries the native bridge, as it does in a
// desktop or mobile shell, and Drive's file is there but not switched on — what the composed
// runtime used to give every proof.
func deliverTestPage(t *testing.T, page string, components []string, site Site) *testPage {
	t.Helper()
	previous := Native
	Native = true
	t.Cleanup(func() { Native = previous })

	if !strings.Contains(page, "</head>") {
		if i := strings.Index(page, "<body"); i >= 0 {
			page = page[:i] + "<head></head>" + page[i:]
		} else {
			page = "<head></head>" + page
		}
	}
	before, after := "", page
	if at := legacyRuntimeTag.FindStringIndex(page); at != nil {
		before, after = page[:at[0]], page[at[1]:]
	}
	p := &testPage{files: make(map[string]string), site: site}
	after = pageInlineScript.ReplaceAllStringFunc(after, func(tag string) string {
		path := fmt.Sprintf("/test-scripts/%d.js", len(p.files))
		p.files[path] = pageInlineScript.FindStringSubmatch(tag)[1]
		return `<script src="` + path + `" defer></script>`
	})
	after = pageExternalScript.ReplaceAllString(after, `<script src="$1" defer></script>`)

	html := Deliver(before+after, site, Options{})
	tag := func(asset *Asset) string { return `<script src="` + asset.Path + `" defer></script>` }
	kernel := KernelAsset()
	if !strings.Contains(html, kernel.Path) {
		html = strings.Replace(html, "</head>", `<script data-kitwork-jit="runtime" src="`+kernel.Path+`" defer></script></head>`, 1)
	}
	// Drive's file right after the kernel's, as Deliver places it — present, not switched on.
	kernelEnd := strings.Index(html, kernel.Path)
	kernelEnd += strings.Index(html[kernelEnd:], "</script>") + len("</script>")
	html = html[:kernelEnd] + tag(DriveAsset()) + html[kernelEnd:]
	for _, key := range ModuleKeysFor(components, site) {
		_, name, _ := strings.Cut(key, ":")
		if asset := componentAsset(name, site); asset != nil && !strings.Contains(html, asset.Path) {
			html = strings.Replace(html, "</head>", tag(asset)+"</head>", 1)
		}
	}
	p.html = html
	return p
}

// serveFiles answers the page's JavaScript — its /kit/ files and its own scripts — and reports
// whether it did. The page itself is the caller's to serve, with whatever headers its proof needs.
func (p *testPage) serveFiles(response http.ResponseWriter, request *http.Request) bool {
	if source, ok := p.files[request.URL.Path]; ok {
		response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		_, _ = response.Write([]byte(source))
		return true
	}
	if !strings.HasPrefix(request.URL.Path, AssetPrefix) {
		return false
	}
	asset, _ := AssetAt(request.URL.Path, p.site)
	if asset == nil {
		http.NotFound(response, request)
		return true
	}
	response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	_, _ = response.Write(asset.Body)
	return true
}

// ModulesJS is the named components' files, joined — the bytes a page that names them downloads
// under /kit/components/. Tests read registrations out of it.
func ModulesJS(names []string) string {
	var b strings.Builder
	for _, key := range moduleKeys(names, nil) {
		_, name, _ := strings.Cut(key, ":")
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(wrapComponent(componentSource(name, nil)))
	}
	return b.String()
}
