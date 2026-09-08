package work

import (
	"regexp"
	"strings"
	"testing"
)

// ruleFor pulls one emitted CSS rule out of a rendered page.
func ruleFor(t *testing.T, body, class string) string {
	t.Helper()
	re := regexp.MustCompile(`\.` + regexp.QuoteMeta(class) + `\s*\{[^}]*\}`)
	m := re.FindString(body)
	if m == "" {
		t.Fatalf("no CSS rule emitted for .%s", class)
	}
	return strings.ReplaceAll(m, " ", "")
}

const highlightShell = `<!doctype html><html data-kit-app="v1"><head></head><body>{{ @page }}</body></html>`

// A named theme has to reach colour resolution, or it would style the syntax
// and leave the frame on another palette — the split this whole design removes.
func TestHighlightThemeDrivesTheTerminalTokens(t *testing.T) {
	page := `<div class="bg-terminal"><code data-kitwork-highlight="go">package main</code></div>`

	dark := scaffold(t, map[string]string{
		"filesystem.kitwork": "",
		"index.kitwork.html": highlightShell,
		"page.kitwork.html":  page,
		"router.kitwork.js":  `import { router } from "kitwork";` + "\n" + `router.css({}).highlight("tokyo-night");`,
	})("/").Body.String()

	light := scaffold(t, map[string]string{
		"filesystem.kitwork": "",
		"index.kitwork.html": highlightShell,
		"page.kitwork.html":  page,
		"router.kitwork.js":  `import { router } from "kitwork";` + "\n" + `router.css({}).highlight("classic");`,
	})("/").Body.String()

	darkRule, lightRule := ruleFor(t, dark, "bg-terminal"), ruleFor(t, light, "bg-terminal")
	if darkRule == lightRule {
		t.Fatalf("two different themes emitted the same frame colour: %s", darkRule)
	}
	if !strings.Contains(darkRule, "#1a1b26") {
		t.Fatalf("tokyo-night frame did not resolve: %s", darkRule)
	}
}


// router.highlight({ keyword: … }) tunes one role without restating the theme.
func TestHighlightMapOverridesOneRole(t *testing.T) {
	body := scaffold(t, map[string]string{
		"filesystem.kitwork": "",
		"index.kitwork.html": highlightShell,
		"page.kitwork.html":  `<div class="text-terminal-keyword bg-terminal"></div>`,
		"router.kitwork.js": `import { router } from "kitwork";` + "\n" +
			`router.css({}).highlight({ theme: "tokyo-night", keyword: "#ff1234" });`,
	})("/").Body.String()

	if rule := ruleFor(t, body, "text-terminal-keyword"); !strings.Contains(rule, "#ff1234") {
		t.Fatalf("the per-role override did not reach the CSS: %s", rule)
	}
	if rule := ruleFor(t, body, "bg-terminal"); !strings.Contains(rule, "#1a1b26") {
		t.Fatalf("overriding one role changed the rest of the theme: %s", rule)
	}
}

// The tokenizer must emit token classes, not baked colours — that is what makes
// a theme change a variable swap instead of a re-render of every page.
func TestHighlightEmitsTokenClassesNotBakedHex(t *testing.T) {
	body := scaffold(t, map[string]string{
		"filesystem.kitwork": "",
		"index.kitwork.html": highlightShell,
		"page.kitwork.html":  `<code data-kitwork-highlight="go">package main</code>`,
		"router.kitwork.js":  `import { router } from "kitwork";` + "\n" + `router.css({}).highlight("tokyo-night");`,
	})("/").Body.String()

	// Match the class, not the whole attribute: a role may carry weight or any
	// other utility alongside its colour.
	if !strings.Contains(body, `text-terminal-keyword`) {
		t.Fatalf("the tokenizer did not emit a token class.\n%s", body)
	}
	if strings.Contains(body, "text-[#") {
		t.Fatal("the tokenizer still emits baked arbitrary-hex classes")
	}
}
