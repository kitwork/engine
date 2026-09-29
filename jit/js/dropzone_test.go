package js

import (
	"strings"
	"testing"
)

func TestDropzoneIsShippedWhenUsed(t *testing.T) {
	out := Render(`<div data-kit-component="dropzone"><input type="file" data-kit-element="picker"></div>`)
	if !strings.Contains(out, `components=component%3Adropzone`) {
		t.Fatalf("dropzone was not injected for markup that uses it:\n%s", out)
	}
	if !strings.Contains(ModulesJS([]string{"dropzone"}), `component("dropzone@v1.0.0"`) {
		t.Error("versioned registration missing")
	}
}

// Real DragEvents carrying real Files, in a real browser: the drag state surviving child crossings,
// the drop judged by the input's own accept, the size and count rules, and the picker taking the
// same path.
func TestBrowserDropzoneJudgesDropsByTheInputsOwnRules(t *testing.T) {
	runBrowserProof(t, "dropzone", []string{"dropzone"}, `
<div id="zone" data-kit-component="dropzone" data-kit-class="dragging ? 'is-dragging' : ''" style="padding:40px">
  <p id="inner">Drop files here</p>
  <input id="picker" data-kit-element="picker" type="file" multiple accept="image/*,.pdf" data-max-bytes="1000" data-limit="3">
  <button id="browse" type="button" data-kit-click="$element.picker.click()">Browse</button>
  <ul><li data-kit-for="file of files" data-kit-key="file.name" data-kit-text="file.name"></li></ul>
  <p data-kit-for="item of rejected" data-kit-key="item.name" data-kit-element="refused" data-kit-text="item.name + ': ' + item.reason"></p>
  <button id="clear" data-kit-click="clear()">clear</button>
</div>
<script>
(async function () {
  await wait(80);
  var zone = document.getElementById("zone"), inner = document.getElementById("inner");
  function transfer(files) { var dt = new DataTransfer(); files.forEach(function (f) { dt.items.add(f); }); return dt; }
  function file(name, type, bytes) { return new File([new Uint8Array(bytes)], name, { type: type }); }
  function drag(target, type, dt) { var e = new DragEvent(type, { dataTransfer: dt, bubbles: true, cancelable: true }); target.dispatchEvent(e); return e; }
  var listed = function () { return [].slice.call(zone.querySelectorAll("li")).map(function (li) { return li.textContent; }).join(","); };
  var refused = function () { return [].slice.call(zone.querySelectorAll('[data-kit-element="refused"]')).map(function (p) { return p.textContent; }).join(" | "); };

  // The drag state survives the pointer crossing a child: enter zone, enter child, leave zone.
  var dt = transfer([file("a.png", "image/png", 10)]);
  var over = drag(zone, "dragenter", dt); drag(inner, "dragenter", dt); drag(zone, "dragleave", dt); await wait(30);
  assert(over.defaultPrevented && zone.className.indexOf("is-dragging") >= 0, "still dragging after crossing into a child");
  assert(drag(zone, "dragover", dt).defaultPrevented, "dragover is refused its default, or the browser would not allow the drop");
  drag(inner, "dragleave", dt); await wait(30);
  assert(zone.className.indexOf("is-dragging") < 0, "leaving the last element ends the drag");

  // A drop judged by the input's own accept, data-max-bytes and duplicates.
  var batch = transfer([file("a.png", "image/png", 10), file("b.pdf", "application/pdf", 10), file("c.exe", "application/x-msdownload", 10), file("big.png", "image/png", 5000), file("a.png", "image/png", 10)]);
  drag(zone, "dragenter", batch); var dropped = drag(zone, "drop", batch); await wait(40);
  assert(dropped.defaultPrevented, "the drop is taken, not opened by the browser");
  assert(listed() === "a.png,b.pdf", "an image and a pdf pass accept=\"image/*,.pdf\", got " + listed());
  assert(refused() === "c.exe: not an accepted type | big.png: larger than 1 KB | a.png: already added", "each refusal says why, got " + refused());
  assert(zone.className.indexOf("is-dragging") < 0, "a drop ends the drag");

  // data-limit="3": the fourth file is over the limit.
  var more = transfer([file("d.png", "image/png", 10), file("e.png", "image/png", 10)]);
  drag(zone, "drop", more); await wait(40);
  assert(listed() === "a.png,b.pdf,d.png" && refused() === "e.png: over the limit of 3", "the limit holds, got " + listed() + " / " + refused());

  // The picker takes the same path and the same rules.
  document.getElementById("clear").click(); await wait(30);
  var picker = document.getElementById("picker");
  picker.files = transfer([file("f.pdf", "application/pdf", 10), file("g.txt", "text/plain", 10)]).files;
  picker.dispatchEvent(new Event("change", { bubbles: true })); await wait(40);
  assert(listed() === "f.pdf" && refused() === "g.txt: not an accepted type", "the picker is judged like a drop, got " + listed() + " / " + refused());

  // CONTROL: a drag that carries no files (text being dragged) is none of the zone's business.
  var text = new DataTransfer(); text.setData("text/plain", "hello");
  assert(!drag(zone, "dragover", text).defaultPrevented, "a drag without files is left alone");

  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
</script>`)
}
