package javascript

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestExpressionValidatorMatchesBrowserGrammar(t *testing.T) {
	t.Parallel()
	accepted := []struct {
		mode   string
		source string
	}{
		{"binding", `items.map((item, index) => ({name: item.name ?? "unknown", index: index}))`},
		{"binding", `(left ?? right) || fallback`},
		{"binding", `record["safe"] === null ? [] : [record.value]`},
		{"binding", `!!open`},
		{"binding", `user?.profile.name ?? "Guest"`},
		{"binding", `records?.[index]?.value`},
		{"binding", `handler?.(payload)?.result`},
		{"binding", `root?.branch().leaf?.[key]`},
		{"binding", `(root?.branch).leaf`},
		{"action", `count = count + 1; save({count: count,})`},
		{"action", `$dialog.open(() => count = count + 1)`},
		{"action", `$dialog?.open?.()`},
		{"action", `++count; count++; --other; other--`},
		{"action", `run();`},
	}
	for _, test := range accepted {
		if err := validateExpression(test.source, test.mode); err != nil {
			t.Errorf("validateExpression(%q, %q) = %v", test.source, test.mode, err)
		}
	}

	rejected := []struct {
		mode   string
		source string
	}{
		{"binding", ``},
		{"binding", `count = 1`},
		{"binding", `$dialog.visible`},
		{"binding", `left ?? right || fallback`},
		{"binding", `object.constructor`},
		{"binding", `run(1,)`},
		{"binding", `[1,]`},
		{"binding", `value++`},
		{"binding", `++value`},
		{"binding", `value--`},
		{"binding", `--value`},
		{"binding", `value => value`},
		{"action", `$dialog = value`},
		{"action", `object.field = value`},
		{"action", `object?.field = value`},
		{"action", `object.field++`},
		{"action", `++object.field`},
		{"action", `object?.field++`},
		{"action", `++object?.field`},
		{"action", `(value)++`},
		{"action", `++(value)`},
		{"action", `$dialog++`},
		{"action", `++$dialog`},
		{"action", `run()++`},
		{"action", `1++`},
		{"action", `++value++`},
		{"action", `object?.`},
		{"action", `object?.[key`},
		{"action", `object?.(value,)`},
		{"action", `count += 1`},
		{"action", `count << 1`},
		{"action", `count & 1`},
		{"action", `delete count`},
		{"action", `new Counter()`},
		{"action", `first;; second`},
		{"binding", `"\u0061"`},
	}
	for _, test := range rejected {
		if err := validateExpression(test.source, test.mode); err == nil {
			t.Errorf("validateExpression(%q, %q) unexpectedly succeeded", test.source, test.mode)
		}
	}
}

func TestExpressionValidatorAcceptsBrowserReadCorpus(t *testing.T) {
	t.Parallel()
	for _, test := range closedExpressionReadCases {
		if err := validateExpression(test.Source, "binding"); err != nil {
			t.Errorf("browser read case %q (%q) = %v", test.ID, test.Source, err)
		}
	}
}

func TestExpressionValidatorUsesECMAScriptWhitespace(t *testing.T) {
	t.Parallel()
	for _, source := range []string{"\u00a0value\u00a0", "\ufeffvalue\u2028", "\u2003value\u2029"} {
		if err := validateExpression(source, "binding"); err != nil {
			t.Errorf("ECMAScript-trimmed expression %q = %v", source, err)
		}
	}
	if err := validateExpression("\u0085value", "binding"); err == nil {
		t.Fatal("NEL must not be treated as ECMAScript whitespace")
	}
	if err := validateExpression("left\u00a0+ right", "binding"); err == nil {
		t.Fatal("lexer must not treat internal NBSP as expression whitespace")
	}
	if err := validateForExpression("\u00a0item\u2003of\u2028items\ufeff"); err != nil {
		t.Fatalf("ECMAScript for whitespace = %v", err)
	}
	if err := validateForExpression("\u0085item of items"); err == nil {
		t.Fatal("for grammar must reject NEL as whitespace")
	}
}

func TestExpressionValidatorDirectiveSpecificGrammar(t *testing.T) {
	t.Parallel()
	for _, source := range []string{
		`value: count; aria-label: label`,
		`{"aria-label": label, disabled: !open}`,
		`title: condition ? "a:b" : "c"`,
	} {
		if err := validateBindExpression(source); err != nil {
			t.Errorf("validateBindExpression(%q) = %v", source, err)
		}
	}
	for _, source := range []string{
		``, `onclick: run`, `style: value`, `data-kit-x: value`, `value`, `{}`,
	} {
		if err := validateBindExpression(source); err == nil {
			t.Errorf("validateBindExpression(%q) unexpectedly succeeded", source)
		}
	}

	for _, source := range []string{
		`width: progress + '%'; opacity: visible ? 1 : 0; margin-left: offset + 'px';`,
		`-webkit-line-clamp: count; --Accent_1: tone; --accent_1: other`,
		`content: condition ? "a:b;c" : "d"`,
		`transform: make({x: 1, y: [2, 3]})`,
		`width: count &lt; 10 ? '1px' : '2px'`,
	} {
		if err := validateStyleExpression(source); err != nil {
			t.Errorf("validateStyleExpression(%q) = %v", source, err)
		}
	}
	for _, source := range []string{
		``, ` `, `{width: progress}`, `width: progress, opacity: alpha`,
		`; width: value`, `width: value;;`, `width: value; ; opacity: alpha`,
		`width`, `width:`, `width: value; opacity`, `width: count = 1`,
		`width: "unfinished`, `width: (value`, `width: value]`,
		`"width": value`, `Width: value`, `font--size: value`, `---tone: value`, `--9tone: value`,
		`width: first; width: second`, `--Tone: first; --Tone: second`,
		`css-text: value`, `csstext: value`, `behavior: value`, `-moz-binding: value`,
		`--kit-tone: value`, `--KITWORK-tone: value`,
		`margin: value`, `padding: value`, `border: value`, `background: value`, `transition: value`,
		`animation-range: value`, `border-inline: value`, `-webkit-mask: value`,
		`all: value`, `corner-shape: value`, `timeline-trigger: value`, `white-space: value`,
	} {
		if err := validateStyleExpression(source); err == nil {
			t.Errorf("validateStyleExpression(%q) unexpectedly succeeded", source)
		}
	}

	for _, source := range []string{`field`, `field_value`, `for`} {
		if err := validateModelExpression(source); err != nil {
			t.Errorf("validateModelExpression(%q) = %v", source, err)
		}
	}
	for _, source := range []string{``, `$field`, `field$value`, `deep.field`, `constructor`, `field-name`} {
		if err := validateModelExpression(source); err == nil {
			t.Errorf("validateModelExpression(%q) unexpectedly succeeded", source)
		}
	}

	for _, source := range []string{`item of items`, `item, index of items.filter((entry) => entry.open)`} {
		if err := validateForExpression(source); err != nil {
			t.Errorf("validateForExpression(%q) = %v", source, err)
		}
	}
	for _, source := range []string{`item in items`, `$item of items`, `item, item of items`, `for of items`, `item of`} {
		if err := validateForExpression(source); err == nil {
			t.Errorf("validateForExpression(%q) unexpectedly succeeded", source)
		}
	}
}

func TestExpressionValidatorStyleShorthandDenylist(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"all", "animation", "animation-range", "background", "background-position",
		"border", "border-block", "border-inline", "border-radius", "column-rule",
		"columns", "container", "flex", "font", "gap", "grid", "inset",
		"list-style", "margin", "marker", "mask", "offset", "outline", "overflow",
		"overscroll-behavior", "padding", "place-content", "position-try", "row-rule",
		"rule", "scroll-margin", "scroll-padding", "scroll-timeline", "text-box",
		"text-decoration", "text-wrap", "timeline-trigger", "transition", "view-timeline",
		"white-space", "-webkit-animation", "-webkit-border-radius", "-webkit-mask",
		"-webkit-transition",
	} {
		err := validateStyleExpression(name + ": value")
		if err == nil || !strings.Contains(err.Error(), "shorthand style property") {
			t.Errorf("style shorthand %q error = %v", name, err)
		}
	}

	for _, name := range []string{"width", "opacity", "transform", "margin-left"} {
		if err := validateStyleExpression(name + ": value"); err != nil {
			t.Errorf("style longhand %q = %v", name, err)
		}
	}

	for name := range expressionStyleShorthandNames {
		err := validateStyleName(name)
		if err == nil || !strings.Contains(err.Error(), "shorthand style property") {
			t.Errorf("static style shorthand %q error = %v", name, err)
		}
	}
}

func TestExpressionValidatorStyleDiagnosticsMatchRuntime(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		source string
		want   string
	}{
		{`{width: value}`, `style map cannot use outer braces`},
		{`width: first; width: second`, `duplicate style property "width"`},
		{`Width: value`, `invalid style property name "Width"`},
		{`css-text: value`, `unsafe style property name "css-text"`},
		{`margin: value`, `shorthand style property "margin" is not supported`},
		{`width:`, `empty style expression for "width"`},
	} {
		err := validateStyleExpression(test.source)
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("validateStyleExpression(%q) error = %v, want %q", test.source, err, test.want)
		}
	}
}

func TestExpressionValidatorStyleBudgetsUseDecodedUTF16(t *testing.T) {
	t.Parallel()
	if got := expressionUTF16Length("a😀"); got != 3 {
		t.Fatalf("expressionUTF16Length(a+astral) = %d, want 3", got)
	}

	const prefix = `content: "`
	const suffix = `"`
	// The decoded source is exactly 16,384 UTF-16 units: the ASCII framing
	// and one ASCII payload unit consume 12, and each entity becomes one
	// astral rune occupying two units.
	exact := prefix + "x" + strings.Repeat("&#x1F600;", (styleSourceLimit-12)/2) + suffix
	if err := validateStyleExpression(exact); err != nil {
		t.Fatalf("exact style source limit = %v", err)
	}
	over := prefix + "x" + strings.Repeat("&#x1F600;", (styleSourceLimit-12)/2+1) + suffix
	if err := validateStyleExpression(over); err == nil || !strings.Contains(err.Error(), "UTF-16") {
		t.Fatalf("overlong style source error = %v", err)
	}

	entries := make([]string, styleEntryLimit+1)
	for index := range entries {
		entries[index] = fmt.Sprintf("--value%d: value", index)
	}
	if err := validateStyleExpression(strings.Join(entries[:styleEntryLimit], ";")); err != nil {
		t.Fatalf("exact style entry limit = %v", err)
	}
	if err := validateStyleExpression(strings.Join(entries, ";")); err == nil || !strings.Contains(err.Error(), "entries") {
		t.Fatalf("style entry overflow error = %v", err)
	}
}

func TestExpressionValidatorBudgets(t *testing.T) {
	t.Parallel()
	deep := strings.Repeat("(", expressionDepthLimit+1) + "value" + strings.Repeat(")", expressionDepthLimit+1)
	if err := validateExpression(deep, "binding"); err == nil || !strings.Contains(err.Error(), "nesting") {
		t.Fatalf("deep expression error = %v", err)
	}

	large := strings.Repeat("value+", expressionNodeLimit/2) + "value"
	if err := validateExpression(large, "binding"); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("large expression error = %v", err)
	}

	largeOptionalChain := "value" + strings.Repeat("?.field", expressionNodeLimit-1)
	if err := validateExpression(largeOptionalChain, "binding"); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("large optional chain error = %v", err)
	}
}

func TestScanHTMLValidatesAuthoredExpressions(t *testing.T) {
	t.Parallel()
	valid := `<main data-kit-scope="count: 0; items: []; user: null">
  <output data-kit-text="count &lt; 10 ? count : 10"></output>
  <output data-kit-text="user?.profile.name ?? 'Guest'"></output>
  <output data-kit-show="!!user"></output>
  <button data-kit-click:prevent="count++; ++count"></button>
  <input data-kit-model="count" data-kit-bind="disabled: count === 10">
	<div data-kit-style="width: count + '%'; opacity: user ? 1 : 0; margin-left: count + 'px'"></div>
  <template data-kit-for="item, index of items" data-kit-key="item.id"></template>
</main>`
	result, err := ScanHTML([]byte(valid))
	if err != nil {
		t.Fatalf("ScanHTML(valid) = %v", err)
	}
	if !result.NeedsRuntime {
		t.Fatal("valid directives did not select the runtime")
	}

	invalid := []string{
		`<p data-kit-text="count = 1"></p>`,
		`<p data-kit-text="value++"></p>`,
		`<button data-kit-click="user.count++"></button>`,
		`<button data-kit-click="user?.count = 1"></button>`,
		`<p data-kit-text="user?."></p>`,
		`<div data-kit-bind="onclick: run"></div>`,
		`<div data-kit-style="{width: count}"></div>`,
		`<div data-kit-style="width: count; width: 10"></div>`,
		`<div data-kit-style="width: count = 1"></div>`,
		`<div data-kit-style="margin: count + 'px'"></div>`,
		`<div data-kit-style></div>`,
		`<input data-kit-model="$field">`,
		`<input data-kit-model="deep.value">`,
		`<template data-kit-for="item in items"></template>`,
		`<template data-kit-for="item of items" data-kit-key="item.constructor"></template>`,
		`<p data-kit-text="left &amp;amp;&amp;amp; right"></p>`,
	}
	for _, source := range invalid {
		if _, err := ScanHTML([]byte(source)); !errors.Is(err, ErrInvalidExpressionUse) {
			t.Errorf("ScanHTML(%q) error = %v, want ErrInvalidExpressionUse", source, err)
		}
	}

	ignored := `<section data-kit-ignore><p data-kit-text="count ="></p></section>`
	if _, err := ScanHTML([]byte(ignored)); err != nil {
		t.Fatalf("ScanHTML(ignored invalid expression) = %v", err)
	}
}
