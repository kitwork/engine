package js

import (
	"strings"
	"testing"
)

func TestEditorIsShippedWhenUsed(t *testing.T) {
	out := deliver(`<div class="editor" data-kitwork-component="editor: $body"><div contenteditable="true" role="textbox"></div></div>`)
	if !strings.Contains(out, `/_kitwork/components/editor.`) {
		t.Fatalf("editor was not injected for markup that uses it:\n%s", out)
	}
	if src := ModulesJS([]string{"editor"}); !strings.Contains(src, `component("editor", `) || strings.Contains(src, `component("editor@`) {
		t.Error("the file must register the bare name, and only it")
	}
}

// (The template editor's browser proof, v1.0.0 on a .kitwork.html page's data-kit-component="editor",
// went with that editor on 26/10/2026: the Kitwork UI editor's proofs are editor_v2_browser_test.go and
// editor_browser_test.go.)
