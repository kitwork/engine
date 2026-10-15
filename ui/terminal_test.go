package ui

import (
	"html"
	"regexp"
	"strings"
	"testing"

	"github.com/kitwork/engine/jit/hydrate"
)

// terminalPage compiles one content.ui (Kit Design's palette) and returns the page, failing on a
// compile error.
func terminalPage(t *testing.T, source string) *Page {
	t.Helper()
	return codePage(t, nil, source)
}

// terminalError is the compile error of one content.ui.
func terminalError(t *testing.T, source string) string {
	t.Helper()
	s := memorySite("", map[string]string{"/site/content.ui": source})
	s.Reload = true
	_, err := s.Compile("/site")
	if err == nil {
		t.Fatalf("want a compile error for\n%s", source)
	}
	return err.Error()
}

// terminalWarnings are the warnings of one content.ui's compile, each a line.
func terminalWarnings(page *Page) string {
	var lines []string
	for _, w := range page.Warnings() {
		lines = append(lines, w.String())
	}
	return strings.Join(lines, "\n")
}

var terminalHostRe = regexp.MustCompile(`<([a-z]+)[^>]*data-kitwork-component="terminal[^"]*"[^>]*>`)

// terminalInner is the HTML inside the first terminal's host of a body.
func terminalInner(t *testing.T, body string) string {
	t.Helper()
	at := terminalHostRe.FindStringSubmatchIndex(body)
	if at == nil {
		t.Fatalf("no terminal in\n%s", body)
	}
	tag := body[at[2]:at[3]]
	tags := regexp.MustCompile(`<(/?)` + tag + `\b[^>]*>`)
	depth, from := 1, at[1]
	for {
		m := tags.FindStringSubmatchIndex(body[from:])
		if m == nil {
			t.Fatalf("the terminal is not closed:\n%s", body)
		}
		if body[from+m[2]:from+m[3]] == "/" {
			depth--
		} else {
			depth++
		}
		if depth == 0 {
			return body[at[1] : from+m[0]]
		}
		from += m[1]
	}
}

// terminalText is the text of the first terminal of a body as a reader without JavaScript reads it:
// a line for each block (a command's <pre>, a <p>), tags left out, characters unescaped.
func terminalText(t *testing.T, body string) string {
	t.Helper()
	inner := strings.NewReplacer("</pre>", "</pre>\n", "</p>", "</p>\n").Replace(terminalInner(t, body))
	return html.UnescapeString(codeTags.ReplaceAllString(inner, ""))
}

// The terminal is what the author wrote: the host keeps its tag and its words with the kernel's
// component and its typing (milliseconds), everything inside is the page's markup as anywhere else,
// and the engine adds only a prompt before each command — inside its <pre>, outside the <code>, so
// neither a selection nor a copy takes it — and an empty polite status region last. No window, no bar,
// no class of its own. The whole page, byte for byte; the full text is in the HTML, every prompt,
// command and line the author wrote, before any script; the prompt's rule is in the page's stylesheet.
func TestTerminalIsWhatTheAuthorWrote(t *testing.T) {
	page := terminalPage(t, `<content><box component="terminal" typing="3s" background="panel" radius="medium">
  <box flex="row" align="center" gap="3" padding-x="4" padding-y="2">
    <text size="small" font="code">bash</text>
  </box>
  <box padding="4">
    <code language="bash">npm install kitwork</code>
    <text color="ink-30">added 1 package in 2s</text>
    <code>kitwork dev</code>
  </box>
</box></content>`)
	body := page.Render(Request{Path: "/"})
	want := `<body class="flex-column"><main class="flex-column">` +
		`<div class="flex-column background-panel radius-medium" data-kitwork-component="terminal" data-kitwork-scope="typing: 3000">` +
		`<div class="flex-row align-center gap-3 padding-x-4 padding-y-2"><p class="size-small font-code">bash</p></div>` +
		`<div class="flex-column padding-4">` +
		`<pre><span class="code-prompt">$ </span><code class="language-bash"><span class="code-function">npm</span> install kitwork</code></pre>` +
		`<p class="color-ink-30">added 1 package in 2s</p>` +
		`<pre><span class="code-prompt">$ </span><code>kitwork dev</code></pre>` +
		`</div>` +
		`<span class="reader" role="status"></span>` +
		`</div></main></body>`
	if body != want {
		t.Errorf("the terminal's page:\n got  %s\n want %s", body, want)
	}
	if got := terminalText(t, body); got != "bash\n$ npm install kitwork\nadded 1 package in 2s\n$ kitwork dev\n" {
		t.Errorf("what a reader without JavaScript reads: %q", got)
	}
	css := string(page.Stylesheet().CSS)
	// the text around the command at 70%: the prompt follows what the author writes on the terminal
	mustContain(t, css, ".code-prompt{color:color-mix(in oklab,currentColor 70%,transparent);-webkit-user-select:none;user-select:none}")
	mustNotContain(t, css, "terminal")
	if !page.UsesCode() {
		t.Error("a terminal shows code: its page ships the code face")
	}
	if warned := terminalWarnings(page); warned != "" {
		t.Errorf("a terminal on Kit Design's palette tells nothing:\n%s", warned)
	}
	// the skeleton has the prompt and the status region, as every render does
	mustContain(t, page.Skeleton(), `<span class="code-prompt">$ </span>`, `<span class="reader" role="status"></span>`)
}

// The prompt before a command follows the command's language: "$ " for bash and for a command that
// names none, "PS> " for powershell, ">>> " for python, "sql> " for sql, "$ " for any other. A command
// keeps the colours of its language. A language from data decides the prompt when the page renders. In
// a line the prompt and the command are one <span>, which takes what the <code> was given (its look,
// its for), so the prompt comes and goes with the command. CONTROL: a <code> outside a terminal has no
// prompt.
func TestTerminalPromptFollowsTheLanguage(t *testing.T) {
	for _, c := range []struct{ language, prompt, class string }{
		{`language="bash"`, "$ ", ` class="language-bash"`},
		{``, "$ ", ``},
		{`language="powershell"`, "PS&gt; ", ` class="language-powershell"`},
		{`language="python"`, "&gt;&gt;&gt; ", ` class="language-python"`},
		{`language="sql"`, "sql&gt; ", ` class="language-sql"`},
		{`language="go"`, "$ ", ` class="language-go"`},
		{`language="text"`, "$ ", ` class="language-text"`},
	} {
		command := `select 1`
		body := terminalPage(t, `<content><box component="terminal"><code `+c.language+`>`+command+`</code></box></content>`).Render(Request{Path: "/"})
		coloured := command
		if name := strings.TrimSuffix(strings.TrimPrefix(c.language, `language="`), `"`); name != "" {
			var err error
			if coloured, _, err = highlightHTML(name, command); err != nil {
				t.Fatal(err)
			}
		}
		mustContain(t, body, `<pre><span class="code-prompt">`+c.prompt+`</span><code`+c.class+`>`+coloured+`</code></pre>`)
	}
	// from data: the prompt of the value the page renders with
	page := terminalPage(t, `<content><box component="terminal"><code language="{{ language }}">ls</code></box></content>`)
	for language, prompt := range map[string]string{"powershell": "PS&gt; ", "python": "&gt;&gt;&gt; ", "bash": "$ ", "go": "$ "} {
		body := page.Render(Request{Path: "/", Scope: map[string]any{"language": language}})
		mustContain(t, body, `<pre><span class="code-prompt">`+prompt+`</span><code class="language-`+language+`">`)
	}
	// in a line: one span, its look and its for with it
	inline := terminalPage(t, `<content><box component="terminal"><text>Chạy <code language="powershell" for="command of commands" color="brand+15">{{ command }}</code> rồi xem</text></box></content>`)
	body := inline.Render(Request{Path: "/", Scope: map[string]any{"commands": []any{"ls", "dir"}}})
	ls, _, _ := highlightHTML("powershell", "ls")
	dir, _, _ := highlightHTML("powershell", "dir")
	mustContain(t, body, `<p>Chạy <span class="color-brand+15"><span class="code-prompt">PS&gt; </span><code class="language-powershell">`+ls+`</code></span>`+
		`<span class="color-brand+15"><span class="code-prompt">PS&gt; </span><code class="language-powershell">`+dir+`</code></span> rồi xem</p>`)
	// CONTROL: outside a terminal
	outside := terminalPage(t, `<content><code language="powershell">ls</code><text>Chạy <code language="bash">ls</code></text></content>`).Render(Request{Path: "/"})
	if strings.Contains(outside, "code-prompt") {
		t.Errorf("a <code> outside a terminal has no prompt:\n%s", outside)
	}
}

// Every <code> inside a terminal is a command, wherever it sits in the host — in a bar the author
// draws too — except inside a component nested in it, whose code is its own: a dropdown's <code> has
// no prompt, a nested terminal's has its own. A terminal whose only <code> is a nested component's has
// no command, and says so; so does an empty host.
func TestTerminalCommandsAreItsOwnCode(t *testing.T) {
	page := terminalPage(t, `<content><box component="terminal">
  <box flex="row"><code>git status</code><text>bash</text></box>
  <box component="dropdown">
    <button click="toggle()">Mở</button>
    <popover><code>not a command</code></popover>
  </box>
  <box component="terminal"><code language="python">print(1)</code></box>
  <code language="sql">select 1</code>
</box></content>`)
	body := page.Render(Request{Path: "/"})
	inner := terminalInner(t, body)
	if got := strings.Count(inner, `class="code-prompt"`); got != 3 {
		t.Errorf("three commands have a prompt (the bar's, the nested terminal's, the last), got %d:\n%s", got, inner)
	}
	mustContain(t, inner,
		`<div class="flex-row"><pre><span class="code-prompt">$ </span><code>git status</code></pre><p>bash</p></div>`,
		`<pre><code>not a command</code></pre>`,
		`<span class="code-prompt">&gt;&gt;&gt; </span><code class="language-python">`,
		`<span class="code-prompt">sql&gt; </span><code class="language-sql">`)
	cases := []struct{ source, needle, says string }{
		{`<content><box component="terminal"><box component="dropdown"><button click="toggle()">Mở</button><popover><code>ls</code></popover></box></box></content>`, `<box`,
			`terminal cần ít nhất một lệnh: một <code> bên trong (mỗi <code> trong terminal là một lệnh, trừ <code> của một component lồng bên trong)`},
		{`<content><box component="terminal"><text>chỉ có chữ</text></box></content>`, `<box`,
			`terminal cần ít nhất một lệnh: một <code> bên trong (mỗi <code> trong terminal là một lệnh, trừ <code> của một component lồng bên trong)`},
		{`<content><box component="terminal"></box></content>`, `<box`,
			`terminal cần ít nhất một lệnh bên trong: <box component="terminal"><code>lệnh</code></box> (mỗi <code> bên trong là một lệnh)`},
	}
	for _, c := range cases {
		if got, want := terminalError(t, c.source), where("content.ui", c.source, c.needle)+c.says; got != want {
			t.Errorf("%s\n got  %s\n want %s", c.source, got, want)
		}
	}
}

// shell is gone (12/10/2026): the prompt follows each command's language, so a terminal that still
// writes shell — any value, bare, with a prefix — is an error that says what to write instead, the
// language of the shell it named when there is one. Nothing changes silently.
func TestTerminalShellIsRemoved(t *testing.T) {
	says := func(language string) string {
		return `shell đã bỏ: dấu nhắc nay theo language của từng <code> ("$ " cho bash và cho <code> không viết language, ` +
			`"PS> " cho powershell, ">>> " cho python, "sql> " cho sql); bỏ shell và viết <code language="` + language + `">…</code>`
	}
	for attribute, language := range map[string]string{
		`shell="bash"`:              "bash",
		`shell="powershell"`:        "powershell",
		`shell="zsh"`:               "bash",
		`shell="{{ shell }}"`:       "bash",
		`shell`:                     "bash",
		`medium:shell="powershell"`: "powershell",
	} {
		source := `<content><box component="terminal" ` + attribute + `><code>ls</code></box></content>`
		if got, want := terminalError(t, source), where("content.ui", source, attribute)+says(language); got != want {
			t.Errorf("%s:\n got  %s\n want %s", attribute, got, want)
		}
	}
}

// typing is the total time typing every command takes, a duration with its unit: the instance's
// parameter in milliseconds, written for the kernel to seed it before its init; not written, nothing
// is written and nothing types. Up to five seconds says nothing; longer renders as written and the
// author hears WCAG 2.2.2 while developing. Anything else is an error with what to write: a word of
// the old true-or-false, a bare typing, a number without its unit, a negative, zero, a non-duration, a
// value from data, a prefix.
func TestTerminalTypingIsADuration(t *testing.T) {
	terminal := func(attribute string) string {
		return `<content><box component="terminal" ` + attribute + `><code>kitwork dev</code></box></content>`
	}
	for attribute, scope := range map[string]string{
		`typing="3s"`:    ` data-kitwork-scope="typing: 3000"`,
		`typing="800ms"`: ` data-kitwork-scope="typing: 800"`,
		`typing="1.5s"`:  ` data-kitwork-scope="typing: 1500"`,
		`typing="5s"`:    ` data-kitwork-scope="typing: 5000"`,
		``:               ``,
	} {
		page := terminalPage(t, terminal(attribute))
		mustContain(t, page.Render(Request{Path: "/"}), `<div class="flex-column" data-kitwork-component="terminal"`+scope+`>`)
		if warned := terminalWarnings(page); warned != "" {
			t.Errorf("%s says nothing:\n%s", attribute, warned)
		}
	}
	long := terminalPage(t, terminal(`typing="8s"`))
	mustContain(t, long.Render(Request{Path: "/"}), ` data-kitwork-scope="typing: 8000"`)
	if got, want := terminalWarnings(long), where("content.ui", terminal(`typing="8s"`), "typing")+
		`typing="8s" gõ lâu hơn 5 giây: chuyển động tự chạy quá 5 giây cần một cách để dừng (WCAG 2.2.2 Pause, Stop, Hide); viết typing="5s" hoặc ngắn hơn`; got != want {
		t.Errorf("typing longer than five seconds:\n got  %s\n want %s", got, want)
	}
	for attribute, says := range map[string]string{
		`typing="true"`:         `typing nay là tổng thời gian gõ mọi lệnh, không phải true: viết typing="3s" hoặc typing="800ms"; không gõ thì bỏ typing`,
		`typing="false"`:        `typing nay là tổng thời gian gõ mọi lệnh, không phải false: viết typing="3s" hoặc typing="800ms"; không gõ thì bỏ typing`,
		`typing`:                `typing cần một thời lượng: viết typing="3s" (tổng thời gian gõ mọi lệnh); không gõ thì bỏ typing`,
		`typing="3"`:            `typing="3" thiếu đơn vị: viết typing="3s"`,
		`typing="800"`:          `typing="800" thiếu đơn vị: viết typing="800ms"`,
		`typing="-2s"`:          `typing không nhận thời lượng âm (-2s): viết một thời lượng như typing="2s"`,
		`typing="-3"`:           `typing không nhận thời lượng âm (-3): viết một thời lượng như typing="3s"`,
		`typing="0s"`:           `typing="0s" là không giây: không gõ thì bỏ typing; gõ thì viết một thời lượng như typing="3s"`,
		`typing="0"`:            `typing="0" là không giây: không gõ thì bỏ typing; gõ thì viết một thời lượng như typing="3s"`,
		`typing="fast"`:         `typing="fast" không phải một thời lượng: viết typing="3s" hoặc typing="800ms" (tổng thời gian gõ mọi lệnh)`,
		`typing="3 s"`:          `typing="3 s" không phải một thời lượng: viết typing="3s" hoặc typing="800ms" (tổng thời gian gõ mọi lệnh)`,
		`typing="{{ moving }}"`: `typing của terminal là một thời lượng cố định (typing="3s"), không có {{ }}: nó được quyết một lần khi trang dựng`,
		`medium:typing="3s"`:    `typing của terminal không nhận tiền tố: nó được quyết một lần khi trang dựng`,
	} {
		source := terminal(attribute)
		if got, want := terminalError(t, source), where("content.ui", source, attribute)+says; got != want {
			t.Errorf("%s:\n got  %s\n want %s", attribute, got, want)
		}
	}
}

// copy() and copied are the terminal's names: a bare copy() inside the host is the nearest terminal's,
// written for the kernel on the button the author draws; copied is false when the page is built. The
// host's alias is the handle the rest of the page reaches it by. Every expression the kernel reads here
// is one its grammar takes. CONTROL: inside a component that does not give copy() (a dropdown), copy()
// is the error it always was.
func TestTerminalGivesCopyAndCopied(t *testing.T) {
	page := terminalPage(t, `<content><box component="terminal" as="$install">
  <box flex="row"><button click="copy()">{{ copied ? 'Đã chép' : 'Chép' }}</button></box>
  <code>kitwork dev</code>
</box></content>`)
	body := page.Render(Request{Path: "/"})
	mustContain(t, body,
		`data-kitwork-component="terminal: $install"`,
		`data-kitwork-bind="text: copied ? &#39;Đã chép&#39; : &#39;Chép&#39;" data-kitwork-event="click: copy()">Chép</button>`)
	for _, expression := range []string{"copy()", "copied ? 'Đã chép' : 'Chép'", "$install.copy()", "$install.copied"} {
		if _, err := hydrate.Compile(expression); err != nil {
			t.Errorf("%s: %v", expression, err)
		}
	}
	source := `<content><box component="dropdown"><button click="copy()">Chép</button><popover><text>x</text></popover></box></content>`
	if got, want := terminalError(t, source), where("content.ui", source, "click=")+
		`bên trong dropdown sự kiện click chỉ gọi tên của component (close, open, toggle); sự kiện của trang chạy ở client là bước 2`; got != want {
		t.Errorf("CONTROL: copy() where no component gives it:\n got  %s\n want %s", got, want)
	}
}

// The prompt is the text around its command at 70% (review of 12/10/2026: a fixed ink-30 was 1.83:1 on
// a dark terminal whatever the author wrote), and it is measured on what lies around its command like
// any text, told at the command's language (or the command when it names none) with advice the author
// can follow: a colour for the text around, or another ground. CONTROLS: on Kit Design's palette, on a
// light ground, it tells nothing; on a host painted ink with color="paper" (the decision's dark
// terminal) the prompt reads.
func TestTerminalPromptIsMeasured(t *testing.T) {
	light := `<content><box component="terminal"><code language="bash">ls</code></box></content>`
	contrastPasses(t, darkPalette(t), content(light))
	paper := `<content><box component="terminal" background="ink" color="paper"><code>ls</code><code>pwd</code></box></content>`
	if told := contrastCompile(t, darkPalette(t), content(paper)); strings.Contains(told, "prompt") {
		t.Errorf("CONTROL: paper on ink, the prompt reads:\n%s", told)
	}
	dark := `<content><box component="terminal" background="ink"><code language="bash">ls</code><code>pwd</code></box></content>`
	told := contrastCompile(t, darkPalette(t), content(dark))
	for _, want := range []string{
		where("content.ui", dark, "language=") + "chữ prompt (ink 70%) trong khối mã trên nền ink chỉ ",
		where("content.ui", dark, "<code>pwd") + "chữ prompt (ink 70%) trong khối mã trên nền ink chỉ ",
		"Dấu nhắc là màu chữ quanh lệnh ở 70%: viết color trên thẻ chủ (hay phần tử bao lệnh) một màu tương phản mạnh hơn với nền ink, hoặc đổi background.",
	} {
		if !strings.Contains(told, want) {
			t.Errorf("a host painted ink: want %q in\n%s", want, told)
		}
	}
	if strings.Contains(told, "khai lại ink") {
		t.Errorf("ink on ink is never told to declare ink anew (it moves both):\n%s", told)
	}
}

// The host keeps the words of its tag and of a call, as the host of any component without a UI: its
// presentation, as, if and for read where it stands. A card hosts it too. CONTROL: what is inside reads
// the row of the for.
func TestTerminalHostKeepsItsWords(t *testing.T) {
	page := terminalPage(t, `<content>
  <box component="terminal" as="$install" background="panel" padding-x="2"><code>kitwork dev</code></box>
  <card component="terminal" for="step of steps" if="steps"><code language="bash">{{ step }}</code></card>
</content>`)
	body := page.Render(Request{Path: "/", Scope: map[string]any{"steps": []any{"go build", "go test"}}})
	mustContain(t, body,
		`<div class="flex-column background-panel padding-x-2" data-kitwork-component="terminal: $install">`,
		`<div class="card flex-column" data-kitwork-component="terminal">`)
	if got := strings.Count(body, `<div class="card flex-column"`); got != 2 {
		t.Errorf("a for on the host repeats the terminal: %d", got)
	}
	mustContain(t, body, `<code class="language-bash"><span class="code-function">go</span> build</code>`, `<code class="language-bash"><span class="code-function">go</span> test</code>`)
	if none := page.Render(Request{Path: "/"}); strings.Contains(none, `class="card flex-column"`) {
		t.Errorf("if on the host leaves it out:\n%s", none)
	}
}
