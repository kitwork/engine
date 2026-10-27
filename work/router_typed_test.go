package work

import (
	"strings"
	"testing"
)

// Locks two behaviours: a {slug[string]} typed segment matches any non-numeric segment (not just
// digits), and a handler that takes the response apart, ({ request, response }) =>
// response.view({...}), draws the resolved folder's page. (A template site's page until 27/10/2026.)
func TestTreeTypedSegmentsAndResponseView(t *testing.T) {
	hit := scaffold(t, map[string]string{
		"router.work": `import * as router from "kitwork/router";`,
		"view.ui":     `<view><content /></view>`,
		"notfound.ui": `<content><text>404</text></content>`,
		// {slug[string]} — matches any segment; the handler answers with response.view({...})
		"blog/{slug[string]}/content.ui": `<content><text>slug: {{ slug }}</text></content>`,
		"blog/{slug[string]}/router.work": `import * as router from "kitwork/router";` + "\n" +
			`router.get(({ request, response }) => response.view({ slug: request.params("slug") }));`,
		// {n[number]} — matches only digits
		"num/{n[number]}/router.work": `import * as router from "kitwork/router";` + "\n" +
			`router.get((context) => context.json({ n: context.params("n") }));`,
	})

	if rec := hit("/blog/hello-world"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "slug: hello-world") {
		t.Fatalf("/blog/hello-world: code=%d body=%q — want 200 with 'slug: hello-world' ({slug[string]} + response.view)", rec.Code, rec.Body.String())
	}
	if rec := hit("/num/42"); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"n":42`) { // a [number] segment is a number
		t.Fatalf("/num/42: code=%d body=%q — want 200 with n=42", rec.Code, rec.Body.String())
	}
	if rec := hit("/num/abc"); rec.Code != 404 {
		t.Fatalf("/num/abc: code=%d — want 404 (number type rejects non-digits)", rec.Code)
	}
}
