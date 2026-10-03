package js

import (
	"strings"
	"testing"
)

func TestCodeEditorIsShippedWhenUsed(t *testing.T) {
	out := deliver(`<div data-kit-component="code-editor"><textarea data-kit-element="field"></textarea></div>`)
	if !strings.Contains(out, `/kit/components/code-editor@`) {
		t.Fatalf("code-editor was not injected for markup that uses it:\n%s", out)
	}
	if !strings.Contains(ModulesJS([]string{"code-editor"}), `component("code-editor@v1.0.0"`) {
		t.Error("versioned registration missing")
	}
}

// The keys an editor owes its author, the caret as numbers, the gutter from the grammar — and the
// way out, because an editor that takes Tab for good traps a keyboard user inside it.
func TestBrowserCodeEditorIndentsAndLetsYouLeave(t *testing.T) {
	runBrowserProof(t, "code-editor", []string{"code-editor"}, `
<div data-kit-scope="{ value: 'a\nb\nc' }">
  <div data-kit-component="code-editor">
    <div id="gutter" data-kit-element="gutter" style="height:60px;overflow:hidden">
      <div data-kit-for="n of value.split('\n').map((line, i) => i + 1)" data-kit-element="number" data-kit-text="n" style="height:20px"></div>
    </div>
    <textarea id="field" data-kit-element="field" data-kit-model="value" data-tab-size="2" wrap="off" style="height:60px"></textarea>
    <span id="where" data-kit-text="'Ln ' + line + ', Col ' + column"></span>
  </div>
</div>
<script>
(async function () {
  await wait(80);
  var field = document.getElementById("field");
  var where = function () { return document.getElementById("where").textContent; };
  var numbers = function () { return [].slice.call(document.querySelectorAll('[data-kit-element="number"]')).map(function (n) { return n.textContent; }).join(","); };
  function at(start, end) { field.setSelectionRange(start, end === undefined ? start : end); field.dispatchEvent(new Event("select", { bubbles: true })); }

  // The gutter is the grammar over the value.
  assert(numbers() === "1,2,3", "one number per line, got " + numbers());

  // The caret, as numbers the markup prints.
  focusIn(field); at(4); await wait(30);
  assert(where() === "Ln 3, Col 1", "the caret at the start of the third line, got " + where());

  // Tab indents by data-tab-size, and the model hears it.
  var tab = key(field, "Tab"); await wait(40);
  assert(tab.defaultPrevented && field.value === "a\nb\n  c", "Tab inserts two spaces, got " + JSON.stringify(field.value));
  assert(where() === "Ln 3, Col 3", "the caret moves past the indent, got " + where());

  // A selection over several lines indents them all, and Shift+Tab takes it back.
  at(0, field.value.length); key(field, "Tab"); await wait(40);
  assert(field.value === "  a\n  b\n    c", "Tab over a block indents every line, got " + JSON.stringify(field.value));
  at(0, field.value.length); key(field, "Tab", { shiftKey: true }); await wait(40);
  assert(field.value === "a\nb\n  c", "Shift+Tab outdents every line, got " + JSON.stringify(field.value));

  // Enter carries the current line's indentation.
  at(field.value.length); key(field, "Enter"); await wait(40);
  assert(field.value === "a\nb\n  c\n  " && where() === "Ln 4, Col 3", "Enter keeps the indent, got " + JSON.stringify(field.value) + " at " + where());
  assert(numbers() === "1,2,3,4", "the gutter grows with the text, got " + numbers());

  // The way out: Escape, then Tab, is not taken — focus is free to leave.
  key(field, "Escape");
  var leave = key(field, "Tab"); await wait(20);
  assert(!leave.defaultPrevented, "after Escape the next Tab must leave the editor, not indent");
  var again = key(field, "Tab"); await wait(20);
  assert(again.defaultPrevented, "only ONE Tab is released; the one after indents again");

  // The gutter follows the field when it scrolls.
  field.value = "1\n2\n3\n4\n5\n6\n7\n8\n9"; field.dispatchEvent(new Event("input", { bubbles: true })); await wait(40);
  field.scrollTop = 40; field.dispatchEvent(new Event("scroll")); await wait(30);
  assert(document.getElementById("gutter").scrollTop === field.scrollTop && field.scrollTop > 0, "the gutter scrolls with the field, " + document.getElementById("gutter").scrollTop + " vs " + field.scrollTop);

  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
</script>`)
}
