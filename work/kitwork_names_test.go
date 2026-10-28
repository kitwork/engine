package work

import (
	"regexp"
	"testing"
)

// A page and every file of the engine's it loads name the runtime kitwork and nothing else (28/10/2026): no
// window.kit or kit.<x>, no data-k or data-kit-* attribute, no <!--kit-…--> marker. The todo page of the bridge
// carries the most of them: its program's marks, its list's template and markers, its data block, call.js.
// A file's comments may tell the history ("data-kit-key until 26/10/2026"): only its code is read.
//
// CONTROL (by hand, 28/10/2026): with ui/page_program.go marking an element data-k="n" again, this test went
// red, naming the page's data-k=.
func TestAPageAndItsFilesNameOnlyKitwork(t *testing.T) {
	tn := patchTodoSite(t)
	page := serveGet(tn, "/todo", nil).Body.String()
	files := regexp.MustCompile(`src="?(/_kitwork/[^" >]+\.js)`).FindAllStringSubmatch(page, -1)
	if len(files) < 3 {
		t.Fatalf("the todo page should load the kernel, call.js and its program, found %d files\n%.2000s", len(files), page)
	}
	old := regexp.MustCompile(`window\.kit[^w]|\bkit\.[a-z]|\sdata-k=|data-k-|data-kit-|<!--kit-`)
	check := func(name, text string) {
		t.Helper()
		if at := old.FindStringIndex(text); at != nil {
			from, to := max(at[0]-80, 0), min(at[1]+80, len(text))
			t.Errorf("%s names %q, an old name of kitwork's: …%s…", name, text[at[0]:at[1]], text[from:to])
		}
	}
	check("/todo", page)
	comments := regexp.MustCompile(`/\*[\s\S]*?\*/|(?m)(?:^|\s)//.*$`)
	for _, file := range files {
		check(file[1], comments.ReplaceAllString(serveGet(tn, file[1], nil).Body.String(), ""))
	}
}
