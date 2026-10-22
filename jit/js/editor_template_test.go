package js

import (
	"strings"
	"testing"
)

// A template page keeps its editor (20/10/2026, runtime.go templateVersions). The newest editor
// (v2.0.0) is .ui's: toggle('bold'), link(), parts the engine finds and marks. A .kitwork.html page
// that writes data-kit-component="editor" with no version (kitwork.io's components/editor page) still
// gets v1.0.0, the file whose run('bold'), isActive() and beginLink() it calls, under the editor's one
// name; the engine's host, data-kitwork-component="editor: $body", gets the newest; a template page that
// names a version gets that one. The names the page's check reads for the editor are both versions',
// so neither page is told of a name its own file has.
// CONTROL: with templateVersions empty the template page got the newest file, and this test failed, as
// did TestBrowserEditorEditsFromTheMarkupsButtons (the template editor's browser proof, which names no
// component beside its markup); with ComponentNames reading the newest alone the names row failed (run
// 20/10/2026 on overlaid copies of runtime.go and component_names.go).
func TestTemplatePageKeepsItsEditor(t *testing.T) {
	newest, template := componentAsset("editor", nil), componentAsset("editor@v1.0.0", nil)
	if newest == nil || template == nil {
		t.Fatalf("the catalogue has no editor: newest %v, v1.0.0 %v", newest, template)
	}
	if newest.Path == template.Path {
		t.Skip("the editor's newest version is v1.0.0: nothing to keep apart")
	}
	for _, c := range []struct{ name, page, want, not string }{
		{"a template page", `<div data-kit-component="editor"><div data-kit-element="area" contenteditable="true"></div></div>`, template.Path, newest.Path},
		{"a .ui page", `<div class="editor" data-kitwork-component="editor: $body" data-kitwork-scope="empty: true"><div class="richtext editor-area" role="textbox"></div></div>`, newest.Path, template.Path},
		{"a template page that names the newest", `<div data-kit-component="editor@` + strings.TrimSuffix(findLatestComponentVersion("editor"), ".js") + `"></div>`, newest.Path, template.Path},
	} {
		html := deliver(`<html><head></head><body>` + c.page + `</body></html>`)
		if !strings.Contains(html, c.want) || strings.Contains(html, c.not) {
			t.Errorf("%s: want %s and not %s:\n%s", c.name, c.want, c.not, html)
		}
	}
	names, ok := ComponentNames(nil)("editor")
	if !ok {
		t.Fatal("the editor's names cannot be read")
	}
	for _, version := range []string{"", "@v1.0.0"} {
		own, known := declaredNamesOf(componentSource("editor"+version, nil))
		if !known {
			t.Fatalf("editor%s: names cannot be read", version)
		}
		for _, name := range own {
			if !containsString(names, name) {
				t.Errorf("editor%s declares %s, which the page's check does not read: %v", version, name, names)
			}
		}
	}
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
