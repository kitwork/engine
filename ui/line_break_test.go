package ui

import (
	"strings"
	"testing"
)

// A .ui file reads the same whatever ends its lines: LF, CRLF (what git writes on Windows under
// core.autocrlf) or CR alone. The parser reads each as one line break, as a browser's HTML input
// stream does, so a code block, a terminal's lines and the commands its copy() takes are what the
// author wrote, with no carriage return left before </code> or </p> (a browser would read it as one
// more line):
//   - CONTROL: the page from LF holds what a reader must see: the terminal's lines, the block's text;
//   - the page from CRLF, and from CR alone, is that page byte for byte.
func TestLineBreaksOfEveryKindReadAlike(t *testing.T) {
	source := `<content gap="4">
  <box component="terminal">
    <code language="powershell">
      Get-ChildItem
    </code>
    <text>
      content.ui
    </text>
    <code language="powershell">kitwork dev</code>
  </box>
  <code language="go">
    // a comment
    x := 1
  </code>
</content>`
	lf := codePage(t, nil, source).Render(Request{Path: "/"})
	if got := terminalText(t, lf); got != "PS> Get-ChildItem\ncontent.ui\nPS> kitwork dev\n" {
		t.Fatalf("CONTROL: the terminal's lines from LF: %q", got)
	}
	if got := codeText(t, lf, "go"); got != "// a comment\nx := 1" {
		t.Fatalf("CONTROL: the code block from LF: %q", got)
	}
	for name, ending := range map[string]string{"CRLF": "\r\n", "CR": "\r"} {
		written := strings.ReplaceAll(source, "\n", ending)
		if strings.Count(written, ending) != strings.Count(source, "\n") || (name == "CR" && strings.Contains(written, "\n")) {
			t.Fatalf("%s: the source is not written with %q", name, ending)
		}
		if got := codePage(t, nil, written).Render(Request{Path: "/"}); got != lf {
			t.Errorf("%s: the page differs from LF's:\n got  %q\n want %q", name, got, lf)
		}
	}
}
