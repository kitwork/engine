package javascript

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The dropzone browser proof drops synthetic files onto the host and picks
// through the native input: the list follows, limits refuse with a reason,
// the input's FileList mirrors what was kept, remove and clear empty both.
// remove() and clear() are invoked through data-kit-click on purpose: a
// method a directive calls receives an action proxy as `this`, and the
// instance data must still be reachable from it.
func TestBrowserDropzoneComponentKeepsAcceptedFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping dropzone component browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := composer.ComposeStandalone([]ComponentRef{{Name: "dropzone", Version: "1.0.0"}}, false)
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/dropzone.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(bundle.JavaScript)
		case "/dropzone.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(dropzoneComponentDocument))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	runVanillaBrowser(t, browser, server.URL+"/dropzone.html")
}

var dropzoneComponentDocument = fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>KitJS dropzone component</title></head><body>
  <div id="zone" data-kit-component="dropzone@1.0.0" data-kit-scope="accept: 'image/*,.pdf', max: 1000, limit: 3">
    <input id="picker" type="file" data-dropzone-input>
    <button id="browse" type="button" data-dropzone-browse>Choose</button>
    <output id="dragging" data-kit-text="dragging ? 'over' : 'idle'"></output>
    <ul id="names"><template data-kit-for="file of files"><li data-kit-text="file.name"></li></template></ul>
    <ul id="rejected"><template data-kit-for="item of rejected"><li data-kit-text="item.name + ':' + item.reason"></li></template></ul>
    <button id="remove-a" type="button" data-kit-click="remove('a.png')">Remove a</button>
    <button id="clear" type="button" data-kit-click="clear()">Clear</button>
  </div>
  <script src="/dropzone.js"></script><script>
%s
__runStandaloneKitTest(async function () {
  var waitFor = __kitTestWaitFor;
  var assert = __kitTestAssert;
  var zone = document.getElementById("zone");
  var picker = document.getElementById("picker");
  function text(id) {
    var node = document.getElementById(id);
    if (node.tagName === "UL") return Array.prototype.map.call(node.querySelectorAll("li"), function (li) { return li.textContent.trim(); }).join(",");
    return node.textContent.trim();
  }
  function file(name, size, type) { return new File([new Uint8Array(size)], name, { type: type }); }
  function transfer(files) { var t = new DataTransfer(); files.forEach(function (f) { t.items.add(f); }); return t; }

  await waitFor(function () { return text("dragging") === "idle" && text("names") === ""; }, "dropzone did not start idle and empty");
  assert(picker.multiple === true, "multiple was not applied to the input");
  assert(picker.getAttribute("accept") === "image/*,.pdf", "accept was not applied to the input");

  zone.dispatchEvent(new DragEvent("dragenter", { bubbles: true, cancelable: true }));
  await waitFor(function () { return text("dragging") === "over"; }, "dragenter did not mark dragging");
  zone.dispatchEvent(new DragEvent("dragleave", { bubbles: true }));
  await waitFor(function () { return text("dragging") === "idle"; }, "dragleave did not clear dragging");

  var drop = new DragEvent("drop", { bubbles: true, cancelable: true, dataTransfer: transfer([
    file("a.png", 10, "image/png"), file("big.png", 5000, "image/png"), file("notes.txt", 10, "text/plain"), file("b.pdf", 10, "application/pdf")
  ]) });
  zone.dispatchEvent(drop);
  await waitFor(function () { return text("names") === "a.png,b.pdf"; }, "drop did not keep the accepted files: " + text("names"));
  assert(text("rejected") === "big.png:size,notes.txt:type", "limits did not refuse with reasons: " + text("rejected"));
  assert(picker.files.length === 2 && picker.files[0].name === "a.png", "the input's FileList did not mirror the kept files");
  assert(text("dragging") === "idle", "drop left dragging on");

  picker.files = transfer([file("c.png", 10, "image/png"), file("d.png", 10, "image/png")]).files;
  picker.dispatchEvent(new Event("change", { bubbles: true }));
  await waitFor(function () { return text("names") === "a.png,b.pdf,c.png"; }, "change did not append up to the limit: " + text("names"));
  assert(text("rejected") === "d.png:count", "the count limit did not refuse the fourth file: " + text("rejected"));

  document.getElementById("remove-a").click();
  await waitFor(function () { return text("names") === "b.pdf,c.png"; }, "remove did not drop the named file");
  assert(picker.files.length === 2 && picker.files[0].name === "b.pdf", "remove did not update the input's FileList");

  document.getElementById("clear").click();
  await waitFor(function () { return text("names") === "" && text("rejected") === ""; }, "clear did not empty the lists");
  assert(picker.files.length === 0, "clear did not empty the input");
});
  </script>
</body></html>`, browserHarness)
