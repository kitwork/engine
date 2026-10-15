package ui

import (
	"html"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/kitwork/engine/jit/highlight"
	"github.com/kitwork/engine/ui/stylesheet"
)

// The terminal (decisions of 12/10/2026) is one of Kitwork's components, written on a container the
// author chooses. The author writes the whole look in markup; the component gives behaviour:
//
//	<box component="terminal" typing="3s" background="ink-10" radius="medium">
//	  <box flex="row" align="center" gap="3" padding-x="4" padding-y="2" background="ink-20">
//	    <text size="small" font="code">bash</text>
//	    <button click="copy()">{{ copied ? 'Copied' : 'Copy' }}</button>
//	  </box>
//	  <box padding="4">
//	    <code language="bash">git clone https://github.com/kitwork/engine.git</code>
//	    <text color="ink-30">Cloning into 'engine'...</text>
//	    <code language="bash">cd engine</code>
//	  </box>
//	</box>
//
// What is inside the host is markup of the page, compiled as anywhere else, with one rule: every
// <code> inside a terminal is a command, wherever it sits in the host — in a bar the author draws too —
// except inside a component nested in it, whose code is its own. Before each command the engine writes
// a prompt chosen from the command's language (terminalPrompts: "$ " for bash and for a command that
// names none), as <span class="code-prompt">, muted and never selected (stylesheet.CodeLook), outside
// the <code>: a block is <pre><span class="code-prompt">$ </span><code>…</code></pre>, a command in a
// line is <span><span class="code-prompt">$ </span><code>…</code></span>. A command keeps the colours
// of its language (code.go). A <text> is whatever the author wants it to be (what a command printed, a
// title): never copied. At the end of the host the engine writes one empty polite status region
// (terminalStatus), where the component says what a copy did; the author writes no status markup.
//
// The host keeps the attributes of its tag (background, padding, radius, if, for, as…), as the host of
// any component without a UI does (host.go); an id is not taken on a component's host yet (an error),
// so a link to a terminal names an element around it. typing is the terminal's own, read when the page
// compiles: the total time typing every command takes ("3s", "800ms"), handed to the kernel as
// data-kitwork-scope="typing: 3000" (milliseconds). shell is gone: the prompt follows each command's
// language, so shell="…" is an error that says so.
//
// The browser runs the kernel's terminal component (jit/js/components/terminal): copy() puts the
// commands on the clipboard (kit.clipboard), one a line, and copied is true for two seconds after;
// with typing it types the commands out while the terminal is on screen. The whole text is in the HTML
// from the first byte and typing never moves it: what is not typed yet is only painted transparent
// (the CSS Custom Highlight API), so a screen reader, a reader without JavaScript and find in page have
// every line at once.

// terminalPrompts are the prompts written before a command, by the command's language; a language
// not here, and a command that names none, takes "$ ".
var terminalPrompts = map[string]string{
	"bash":       "$ ",
	"powershell": "PS> ",
	"python":     ">>> ",
	"sql":        "sql> ",
}

// terminalPrompt is the prompt written before a command in language.
func terminalPrompt(language string) string {
	if prompt, ok := terminalPrompts[language]; ok {
		return prompt
	}
	return "$ "
}

// typingLongest is the longest typing that runs without a word from the author: motion that starts by
// itself and lasts longer than five seconds needs a way to pause it (WCAG 2.2.2 Pause, Stop, Hide), so
// a longer one renders as written and the author hears of it while developing.
const typingLongest = 5000

// terminalSpec is what a terminal's own attributes said: how long typing every command takes, in
// milliseconds (0: no typing).
type terminalSpec struct {
	typing float64
}

// inputs are the parameters the kernel seeds the instance with before its init (data-kitwork-scope).
func (t *terminalSpec) inputs() []input {
	if t.typing <= 0 {
		return nil
	}
	ms := strconv.FormatFloat(t.typing, 'f', -1, 64)
	return []input{{name: "typing", in: &interpolation{parts: []part{{text: ms}}}, source: ms}}
}

// terminalScope is the terminal whose commands the <code> elements being compiled are (ctx.terminal):
// it counts them, so a terminal without one is told.
type terminalScope struct {
	commands int
}

// terminalHost reads the terminal's own words off its host — typing, fixed when the page compiles —
// and returns the host without them, to go on as the host of a component without a UI.
func (c *compiler) terminalHost(n *Node) (*Node, *terminalSpec, error) {
	if len(n.SignificantChildren()) == 0 {
		return nil, nil, errAt(n, "terminal cần ít nhất một lệnh bên trong: <%s component=\"terminal\"><code>lệnh</code></%s> (mỗi <code> bên trong là một lệnh)", n.Tag, n.Tag)
	}
	spec := &terminalSpec{}
	host := *n
	host.Attrs = nil
	for i := range n.Attrs {
		a := &n.Attrs[i]
		base := a.Name
		if !isEvent(a.Name) {
			base = a.Name[strings.LastIndex(a.Name, ":")+1:]
		}
		switch {
		case base == "shell":
			return nil, nil, errAttr(n, a, "%s", shellRemoved(a))
		case base == "typing" && base != a.Name:
			return nil, nil, errAttr(n, a, "typing của terminal không nhận tiền tố: nó được quyết một lần khi trang dựng")
		case base == "typing":
			typing, err := c.terminalTyping(n, a)
			if err != nil {
				return nil, nil, err
			}
			spec.typing = typing
		default:
			host.Attrs = append(host.Attrs, *a)
		}
	}
	return &host, spec, nil
}

// shellRemoved is the error of shell= on a terminal: the word is gone (12/10/2026) and the prompt now
// follows each command's language, so a page that still writes it is told what to write instead of
// silently showing another prompt.
func shellRemoved(a *Attr) string {
	language := "bash"
	if value := strings.TrimSpace(a.Value); !a.Bare && !strings.Contains(value, "{{") && highlight.Has(value) {
		language = value
	}
	return `shell đã bỏ: dấu nhắc nay theo language của từng <code> ("$ " cho bash và cho <code> không viết language, ` +
		`"PS> " cho powershell, ">>> " cho python, "sql> " cho sql); bỏ shell và viết <code language="` + language + `">…</code>`
}

var (
	// durationRe is a duration typing takes: a number of seconds or milliseconds, written with its unit
	durationRe = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)(ms|s)$`)
	// unitlessRe is a number written without a unit
	unitlessRe = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?$`)
)

// terminalTyping reads typing=: the total time typing every command takes, "3s" or "800ms", in
// milliseconds. Anything else is an error that says what to write (typing="3s"); longer than five
// seconds is told while developing and renders as written.
func (c *compiler) terminalTyping(n *Node, a *Attr) (float64, error) {
	value := strings.TrimSpace(a.Value)
	switch {
	case a.Bare || value == "":
		return 0, errAttr(n, a, `typing cần một thời lượng: viết typing="3s" (tổng thời gian gõ mọi lệnh); không gõ thì bỏ typing`)
	case strings.Contains(value, "{{"):
		return 0, errAttr(n, a, `typing của terminal là một thời lượng cố định (typing="3s"), không có {{ }}: nó được quyết một lần khi trang dựng`)
	case value == "true" || value == "false":
		return 0, errAttr(n, a, `typing nay là tổng thời gian gõ mọi lệnh, không phải %s: viết typing="3s" hoặc typing="800ms"; không gõ thì bỏ typing`, value)
	case strings.HasPrefix(value, "-") && (durationRe.MatchString(value[1:]) || unitlessRe.MatchString(value[1:])):
		return 0, errAttr(n, a, `typing không nhận thời lượng âm (%s): viết một thời lượng như typing="%s"`, value, durationSuggestion(value[1:]))
	case unitlessRe.MatchString(value):
		if number, _ := strconv.ParseFloat(value, 64); number == 0 {
			return 0, errAttr(n, a, `typing="%s" là không giây: không gõ thì bỏ typing; gõ thì viết một thời lượng như typing="3s"`, a.Value)
		}
		return 0, errAttr(n, a, `typing="%s" thiếu đơn vị: viết typing="%s"`, a.Value, durationSuggestion(value))
	}
	m := durationRe.FindStringSubmatch(value)
	if m == nil {
		return 0, errAttr(n, a, `typing="%s" không phải một thời lượng: viết typing="3s" hoặc typing="800ms" (tổng thời gian gõ mọi lệnh)`, a.Value)
	}
	number, err := strconv.ParseFloat(m[1], 64)
	if err != nil || math.IsInf(number, 0) {
		return 0, errAttr(n, a, `typing="%s" không phải một thời lượng: viết typing="3s" hoặc typing="800ms" (tổng thời gian gõ mọi lệnh)`, a.Value)
	}
	ms := number
	if m[2] == "s" {
		ms = number * 1000
	}
	if ms == 0 {
		return 0, errAttr(n, a, `typing="%s" là không giây: không gõ thì bỏ typing; gõ thì viết một thời lượng như typing="3s"`, a.Value)
	}
	if ms > typingLongest {
		c.warn(n.File, a.Line, a.Column, `typing="`+a.Value+`" gõ lâu hơn 5 giây: chuyển động tự chạy quá 5 giây cần một cách để dừng (WCAG 2.2.2 Pause, Stop, Hide); viết typing="5s" hoặc ngắn hơn`)
	}
	return ms, nil
}

// durationSuggestion is the duration to write for a value written without its unit, or with one after
// a minus: 3 → 3s, 800 → 800ms (a number that large is milliseconds), -3s → 3s.
func durationSuggestion(value string) string {
	if durationRe.MatchString(value) {
		return value
	}
	if number, err := strconv.ParseFloat(value, 64); err == nil && number >= 100 {
		return value + "ms"
	}
	return value + "s"
}

// terminalChildren compiles what is inside a terminal's host: the author's markup, each <code> a
// command (command, below), then the polite status region. cx is the host's context.
func (c *compiler) terminalChildren(n *Node, cx ctx) ([]op, error) {
	scope := &terminalScope{}
	cx.terminal = scope
	children, err := c.children(n, cx)
	if err != nil {
		return nil, err
	}
	if scope.commands == 0 {
		return nil, errAt(n, "terminal cần ít nhất một lệnh: một <code> bên trong (mỗi <code> trong terminal là một lệnh, trừ <code> của một component lồng bên trong)")
	}
	return append(children, &staticOp{s: terminalStatus}), nil
}

// terminalStatus is the terminal's polite status region: seen by a screen reader, not by the eye (the
// class reader, reboot.css's), empty until the component says what a copy did. It is in the HTML from
// the first byte, so a screen reader hears it change.
const terminalStatus = `<span class="reader" role="status"></span>`

// command compiles a <code> inside a terminal: the code as anywhere else (code.go), with the prompt of
// its language before it, outside the <code>, so neither a selection nor a copy takes it. The prompt's
// rule (muted, never selected) goes in the page's stylesheet and its colour is measured on what lies
// around the command, like any text.
func (c *compiler) command(n *Node, cx ctx, site *callSite) (op, error) {
	compiled, err := c.code(n, cx, site)
	if err != nil {
		return nil, err
	}
	o, ok := compiled.(*elemOp)
	if !ok {
		return compiled, nil
	}
	cx.terminal.commands++
	language := codeLanguage{node: n}
	var text op = &staticOp{s: html.EscapeString(terminalPrompt(""))}
	if a := attrNamed(n, "language"); a.Name == "language" {
		language.attr = a
		value := strings.TrimSpace(a.Value)
		if strings.Contains(value, "{{") {
			in, err := parseInterpolation(a.Value, n, a)
			if err != nil {
				return nil, err
			}
			language.in = in
			text = &promptOp{language: in}
		} else {
			language.name = value
			text = &staticOp{s: html.EscapeString(terminalPrompt(value))}
		}
	}
	around := "pre"
	if cx.inline {
		around = "span"
	}
	if err := c.codeKinds(language, &paint{parent: cx.paint, node: n, tag: n.Tag, html: around}, []highlight.Kind{highlight.Prompt}); err != nil {
		return nil, err
	}
	prompt := fold(&elemOp{tag: "span", staticClass: []string{stylesheet.CodeClass("prompt")}, children: []op{text}})
	if !cx.inline {
		// a block: the prompt inside the <pre>, on the command's line
		o.children = append([]op{prompt}, o.children...)
		return o, nil
	}
	// in a line: the prompt and the command in one <span>, which takes what the <code> was given (its
	// look, for, if, show, id) so the prompt comes and goes with it; the <code> keeps its language
	wrapper := *o
	wrapper.tag = "span"
	wrapper.staticClass, wrapper.dynClasses = nil, nil
	wrapper.children = []op{prompt, o}
	o.staticAttrs, o.dynAttrs = "", nil
	o.forEach, o.ifExpr, o.showExpr, o.callerIf, o.callerShow = nil, nil, nil, nil, nil
	o.bindings, o.state, o.inputs = nil, nil, nil
	var own []string
	for _, class := range o.staticClass {
		if strings.HasPrefix(class, "language-") {
			own = append(own, class)
		} else {
			wrapper.staticClass = append(wrapper.staticClass, class)
		}
	}
	var dynamic []dynClass
	for _, d := range o.dynClasses {
		if d.language {
			dynamic = append(dynamic, d)
		} else {
			wrapper.dynClasses = append(wrapper.dynClasses, d)
		}
	}
	o.staticClass, o.dynClasses = own, dynamic
	return &wrapper, nil
}

// promptOp writes the prompt of a command whose language comes from {{ }}, read when the page renders.
type promptOp struct {
	language *interpolation
}

func (o *promptOp) render(r *run, scope map[string]any) {
	r.b.WriteString(html.EscapeString(terminalPrompt(strings.TrimSpace(stringify(o.language.value(scope))))))
}
