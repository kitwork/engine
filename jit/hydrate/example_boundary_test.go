package hydrate

import (
	"strings"
	"testing"
)

// A printed example goes from the scan, and its tags stay balanced: the inside of a <pre> goes up to
// the </pre> that closes it, the inside of a <code> up to its </code>. A terminal's command is a <pre>
// holding a prompt and a <code>; cut at the first </code>, it left <pre></code></pre>, one close too
// many, and every boundary after it was counted one level too deep.
func TestExamplesLeaveTheTagsBalanced(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{`<pre><span class="code-prompt">$ </span><code class="language-bash">npm i kitwork</code></pre>`, `<pre></pre>`},
		{`<pre><code>x</code></pre><p><code>y</code></p>`, `<pre></pre><p><code></code></p>`},
		{`<pre data-kit-text="a"><code>b</code></pre>`, `<pre data-kit-text="a"></pre>`},
		{"<pre>\n<code>a\nb</code>\n</pre><pre><code>c</code></pre>", `<pre></pre><pre></pre>`},
	} {
		if got := withoutExamples(c.in); got != c.want {
			t.Errorf("withoutExamples(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// A component's boundary ends with its host. What follows it (a sibling) is the page's again, so a
// name read there that nothing declares is said at the page, and a name the closed component has is
// never borrowed from it.
func TestAClosedComponentIsNotTheBoundaryOfItsSiblings(t *testing.T) {
	names := func(component string) ([]string, bool) {
		switch component {
		case "terminal":
			return []string{"typing", "copied", "copy"}, true
		case "progressbar":
			return []string{"active", "value"}, true
		}
		return nil, false
	}
	terminal := `<div data-kitwork-component="terminal" data-kitwork-scope="typing: 3000"><pre><span class="code-prompt">$ </span><code>ls</code></pre><span class="reader" role="status"></span></div>`
	progressbar := `<div data-kitwork-component="progressbar" role="progressbar"></div>`
	read := `<p data-kitwork-bind="text: copied ? 'yes' : 'no'"></p>`

	after := strings.Join(undeclaredWarningsWith(`<body>`+terminal+read+`</body>`, names), "\n")
	if !strings.Contains(after, `reads "copied", which no scope above it declares`) {
		t.Errorf("copied read after a closed terminal is the page's, and nothing there declares it; got:\n%s", after)
	}
	after = strings.Join(undeclaredWarningsWith(`<body>`+progressbar+read+`</body>`, names), "\n")
	if strings.Contains(after, "component progressbar") || !strings.Contains(after, `reads "copied", which no scope above it declares`) {
		t.Errorf("copied read after a closed progressbar is the page's, not the progressbar's; got:\n%s", after)
	}
	// control: the same read alone on the page is said the same way, and inside the terminal it is
	// the terminal's and fine
	alone := strings.Join(undeclaredWarningsWith(`<body>`+read+`</body>`, names), "\n")
	if !strings.Contains(alone, `reads "copied", which no scope above it declares`) {
		t.Errorf("control: copied alone on the page is said; got:\n%s", alone)
	}
	inside := strings.Replace(terminal, `<span class="reader"`, read+`<span class="reader"`, 1)
	if got := undeclaredWarningsWith(`<body>`+inside+`</body>`, names); len(got) != 0 {
		t.Errorf("control: copied inside the terminal is its own; got:\n%s", strings.Join(got, "\n"))
	}
}

// A terminal written right — its copy button after two commands, a progressbar before it on the page —
// says nothing: the button reads the terminal's names.
func TestATerminalButtonAfterItsCommandsIsSilent(t *testing.T) {
	names := func(component string) ([]string, bool) {
		switch component {
		case "terminal":
			return []string{"typing", "copied", "copy"}, true
		case "progressbar":
			return []string{"active", "value"}, true
		}
		return nil, false
	}
	command := func(text string) string {
		return `<pre><span class="code-prompt">$ </span><code class="language-bash">` + text + `</code></pre>`
	}
	button := `<button type="button" data-kitwork-bind="text: copied ? 'Copied' : 'Copy'" data-kitwork-event="click: copy()">Copy</button>`
	page := func(commands ...string) string {
		return `<html><body><div data-kitwork-component="progressbar" role="progressbar"></div>` +
			`<div data-kitwork-component="terminal">` + strings.Join(commands, "") + button +
			`<span class="reader" role="status"></span></div></body></html>`
	}
	for _, html := range []string{page(command("npm i kitwork")), page(command("npm i kitwork"), command("print(1)")), page(command("a"), command("b"), command("c"))} {
		if got := authoringMistakesWith(html, names); len(got) != 0 {
			t.Errorf("a terminal written right was reported:\n%s\nin %s", strings.Join(got, "\n"), html)
		}
	}
	// control: the same button misspelling the terminal's name is said, after two commands too
	misspelt := strings.Replace(page(command("a"), command("b")), "click: copy()", "click: cpy()", 1)
	if got := strings.Join(authoringMistakesWith(misspelt, names), "\n"); !strings.Contains(got, `reads "cpy", which component terminal does not have`) {
		t.Errorf("control: a misspelt name after two commands is the terminal's to lack; got:\n%s", got)
	}
}
