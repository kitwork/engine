package js

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The editor's reading of a paste and a drop is a file of its own, a part (parts/editor-paste.js,
// 21/10/2026, PartAsset): served at /_kitwork/parts/editor-paste.<hash>.js by its exact current name
// only, never linked by a page, and named in the editor's delivered file with its hash where the
// editor's source names it without one. The template editor (v1.0.0) has no part.
// CONTROL (run 21/10/2026, copies of delivery.go through -overlay, fix in the scratchpad): with
// withParts handing the source back as it is, "the editor's file names the part with its hash" failed;
// with AssetAt answering no part, "served by its exact name" failed.
func TestEditorPastePartIsDeliveredByName(t *testing.T) {
	part := PartAsset("editor-paste")
	if part == nil || part.Role != "part" || !regexp.MustCompile(`^/_kitwork/parts/editor-paste\.[0-9a-f]{16}\.js$`).MatchString(part.Path) {
		t.Fatalf("the part: %+v", part)
	}
	if !strings.Contains(string(part.Body), `Symbol.for("kitwork:editor-paste")`) {
		t.Errorf("the part does not define its function under its symbol")
	}
	if !strings.Contains(ComponentSource("editor", ""), `"/_kitwork/parts/editor-paste.js"`) {
		t.Errorf("the editor's source names no part")
	}
	editor := componentAsset("editor", nil)
	if body := string(editor.Body); !strings.Contains(body, `"`+part.Path+`"`) || strings.Contains(body, `"/_kitwork/parts/editor-paste.js"`) {
		t.Errorf("the editor's file names the part with its hash: want %s in it", part.Path)
	}
	if asset, exact := AssetAt(part.Path, nil); asset != part || !exact {
		t.Errorf("served by its exact name: %s gave %v, exact %v", part.Path, asset, exact)
	}
	for _, path := range []string{"/_kitwork/parts/editor-paste.0123456789abcdef.js", "/_kitwork/parts/editor-paste.js", "/kit/parts/editor-paste.0123456789abcdef.js",
		"/_kitwork/parts/nothing.0123456789abcdef.js", "/_kitwork/parts/Editor-paste.0123456789abcdef.js", "/_kitwork/parts/../components/editor.0123456789abcdef.js"} {
		if asset, _ := AssetAt(path, nil); asset != nil {
			t.Errorf("%s is answered with %s: only the part's exact name is", path, asset.Path)
		}
	}
	page := Deliver(`<html><head></head><body><div data-kitwork-component="editor: $body"></div></body></html>`, nil, Options{})
	if !strings.Contains(page, editor.Path) || strings.Contains(page, "/parts/") {
		t.Errorf("a page with an editor links the editor and never its part:\n%s", page)
	}
	if v1 := componentAsset("editor@v1.0.0", nil); v1 == nil || strings.Contains(string(v1.Body), "/parts/") {
		t.Errorf("the template editor names a part")
	}
	if PartAsset("../components/editor/v2.0.0") != nil || PartAsset("nothing") != nil {
		t.Errorf("a part that is not one is answered")
	}
}

// The editor loads its part at the page's first paste or drop and never before: not with the page, not
// for a command or typing. A paste is taken at once (the browser builds nothing) and done once the file
// is here, at the caret; from the second on it is done as it happens. A drop first loads it too. When the
// file cannot load (offline, or a page of another build whose part's name no longer answers), a paste is
// its plain words at the caret.
// CONTROL (run 21/10/2026, copies of v2.0.0.js through -overlay, fix in the scratchpad): with the editor
// asking for its part at ready, "nothing of the part before a paste" failed; with bring() handing nothing
// when the file does not load (no plain words), "a paste whose part cannot load is its plain words"
// failed (the field unchanged).
func TestBrowserEditorLoadsItsPastePart(t *testing.T) {
	part := PartAsset("editor-paste").Path
	// the paste part by its name: the place part (parts/editor-place.js) comes at the first focus in the
	// editor, which a command gives the area
	const fetched = `function partTag() { return document.querySelector('script[src*="/_kitwork/parts/editor-paste."]'); }
function partFetched() { return performance.getEntriesByType("resource").filter(function (e) { return e.name.indexOf("/_kitwork/parts/editor-paste.") >= 0; }).length; }
async function until(test) { for (var i = 0; i < 150 && !test(); i++) await wait(20); return test(); }
`
	runEditorPartProof(t, "editor paste part", false, editorV2Start("<p>hello world</p>", "")+editorProof(fetched+`
  assert(!partTag() && !partFetched() && !window[Symbol.for("kitwork:editor-paste")], "nothing of the part before a paste");
  selectWord("world"); press($("bold")); caretAt("world", true); type("!"); beforeinput("insertParagraph"); type("hai");
  assert(!partTag() && !partFetched(), "a command and typing load nothing");
  var before = field.value, event = pasted({ "text/html": "<p><b>đậm</b> dán</p>", "text/plain": "đậm dán" });
  assert(event.defaultPrevented && field.value === before, "a paste is taken at once and waits for its part, got " + JSON.stringify(field.value));
  assert(await until(function () { return field.value !== before; }), "the paste is done once its part is here");
  assert(field.value === "<p>hello <strong>world!</strong></p>\n<p>hai<strong>đậm</strong> dán</p>", "at the caret, read onto the grammar, got " + JSON.stringify(field.value));
  assert(partFetched() === 1 && partTag().getAttribute("src") === `+jsString(part)+`, "the part, by its name with its hash, once, got " + (partTag() && partTag().getAttribute("src")));
  pasted({ "text/plain": " ba" });
  assert(field.value === "<p>hello <strong>world!</strong></p>\n<p>hai<strong>đậm</strong> dán ba</p>" && partFetched() === 1, "the second paste is done as it happens, got " + JSON.stringify(field.value));
`))
	runEditorPartProof(t, "editor paste part, a drop first", false, editorV2Start("<p>hello world</p>", "")+editorProof(fetched+`
  var text = find("world")[0], spot = document.createRange(); spot.setStart(text, text.length); var box = spot.getBoundingClientRect();
  var transfer = new DataTransfer(); transfer.setData("text/html", "<p>thả</p>"); transfer.setData("text/plain", "thả");
  var drop = new DragEvent("drop", { dataTransfer: transfer, clientX: box.left + 1, clientY: box.top + box.height / 2, bubbles: true, cancelable: true });
  area.dispatchEvent(drop);
  assert(drop.defaultPrevented && await until(function () { return field.value !== "<p>hello world</p>"; }), "a drop first loads the part and is done, got " + JSON.stringify(field.value));
  assert(field.value === "<p>hello worldthả</p>" && partFetched() === 1, "where it was let go, got " + JSON.stringify(field.value));
`))
	runEditorPartProof(t, "editor paste part refused", true, editorV2Start("<p>hello world</p>", "")+editorProof(fetched+`
  caretAt("world", true);
  pasted({ "text/html": "<p><b>đậm</b> dán</p>", "text/plain": " đậm dán" });
  assert(await until(function () { return field.value !== "<p>hello world</p>"; }), "a paste whose part cannot load is its plain words, got " + JSON.stringify(field.value));
  assert(field.value === "<p>hello world đậm dán</p>", "at the caret, got " + JSON.stringify(field.value));
`))
}

// runEditorPartProof is runEditorProof without the part loaded with the page (the editor loads it
// itself), from a server that answers the part with 404 when refuse is true.
func runEditorPartProof(t *testing.T, label string, refuse bool, body string) {
	t.Helper()
	runEditorPartServed(t, label, func(path string) bool { return refuse && strings.HasPrefix(path, AssetPrefix+"parts/") }, body)
}

// runEditorPartServed is runEditorPartProof from a server that answers a request with 404 where refuse
// says so (it is asked for each request, in turn).
func runEditorPartServed(t *testing.T, label string, refuse func(path string) bool, body string) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping " + label + " browser proof in short mode")
	}
	browser := findHeadlessBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	body = strings.Replace(body, "<script>", "<script>"+editorScript+editorV2Script, 1)
	page := "<!doctype html><html><head></head><body>" + editorStyle + body + "</body></html>"
	page = strings.Replace(page, "<script>", "<script>"+proofScriptHead, 1)
	delivered := deliverTestPage(t, page, nil, nil)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if refuse(request.URL.Path) {
			http.NotFound(response, request)
			return
		}
		if delivered.serveFiles(response, request) {
			return
		}
		if request.URL.Path != "/" {
			http.NotFound(response, request)
			return
		}
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = response.Write([]byte(delivered.html))
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args := []string{"--headless=new", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage", "--disable-background-networking",
		"--disable-default-apps", "--disable-extensions", "--disable-sync", "--metrics-recording-only", "--no-first-run",
		"--window-size=1000,800", "--run-all-compositor-stages-before-draw", "--user-data-dir=" + filepath.Join(t.TempDir(), "p"),
		"--virtual-time-budget=20000", "--dump-dom", server.URL + "/"}
	if runtime.GOOS == "windows" {
		args = append([]string{"--disable-background-mode", "--disable-breakpad", "--disable-crashpad-for-testing"}, args...)
	}
	output, err := exec.CommandContext(ctx, browser, args...).CombinedOutput()
	if i := bytes.Index(output, []byte("data-kit-test-error=")); i >= 0 {
		t.Fatalf("%s proof failed: %s", label, output[i:min(i+400, len(output))])
	}
	if !bytes.Contains(output, []byte(`data-kit-test="passed"`)) {
		t.Fatalf("%s proof did not pass (err=%v):\n%.2000s", label, err, output)
	}
}
