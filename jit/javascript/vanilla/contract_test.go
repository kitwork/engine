package vanilla

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

var (
	externalScriptRE  = regexp.MustCompile(`(?is)<script\b[^>]*\bsrc\s*=\s*(?:"([^"]+)"|'([^']+)')[^>]*>`)
	publicControlRE   = regexp.MustCompile(`(?i)\bkit\s*(?:\.\s*(?:start|destroy|use|mount|unmount)\b|\[\s*["'](?:start|destroy|use|mount|unmount)["']\s*\])`)
	globalKitTargetRE = regexp.MustCompile(`(?m)\b(?:window|globalThis|self|global|root)\s*\.\s*kit\b`)
)

func TestKitJSHasOneSmallBrowserContract(t *testing.T) {
	source := readVanillaFile(t, "kit.js")
	text := string(source)
	code := javascriptWithoutComments(text)
	tokens := javascriptIdentifiers(text)

	for _, forbidden := range []string{
		"eval", "import", "require", "WeakRef", "FinalizationRegistry",
	} {
		if indexOfToken(tokens, forbidden) >= 0 {
			t.Fatalf("kit.js contains forbidden JavaScript token %q", forbidden)
		}
	}
	for index := 0; index+1 < len(tokens); index++ {
		if tokens[index] == "new" && tokens[index+1] == "Function" {
			t.Fatal("kit.js contains the Function constructor")
		}
	}
	if match := publicControlRE.FindString(code); match != "" {
		t.Fatalf("kit.js exposes a runtime control on the public kit object: %q", match)
	}
	if got := globalKitAssignmentCount(code); got != 1 {
		t.Fatalf("kit.js global kit assignment count = %d, want exactly one", got)
	}
	for _, required := range []string{
		"cacheLimit: 256",
		"core.compiled.size >= core.cacheLimit",
		"core.compiled.delete(",
		"cleanupOwners < 1",
		"document.defaultView.MutationObserver",
		"cleanupObserver.disconnect()",
	} {
		if !strings.Contains(code, required) {
			t.Fatalf("kit.js lost bounded compile-cache contract %q", required)
		}
	}
}

func globalKitAssignmentCount(source string) int {
	count := 0
	for _, location := range globalKitTargetRE.FindAllStringIndex(source, -1) {
		index := location[1]
		for index < len(source) && (source[index] == ' ' || source[index] == '\t' || source[index] == '\r' || source[index] == '\n') {
			index++
		}
		if index < len(source) && source[index] == '=' && (index+1 == len(source) || source[index+1] != '=') {
			count++
		}
	}
	return count
}

func TestExamplesUseOnlyTheStandaloneRuntime(t *testing.T) {
	examples := []struct {
		name    string
		runtime string
	}{
		{name: "counter.html", runtime: "../kit.js"},
		{name: "dialog.html", runtime: "../kit.js"},
		{name: "dropdown.html", runtime: "../kit.js"},
		{name: "form.html", runtime: "../kit.js"},
		{name: "list.html", runtime: "../kit.js"},
		{name: "hydrate-home.html", runtime: "../hydrate.kit.js"},
		{name: "hydrate-next.html", runtime: "../hydrate.kit.js"},
	}
	for _, example := range examples {
		example := example
		t.Run(strings.TrimSuffix(example.name, filepath.Ext(example.name)), func(t *testing.T) {
			source := readVanillaFile(t, "examples", example.name)
			lower := strings.ToLower(string(source))
			matches := externalScriptRE.FindAllStringSubmatch(string(source), -1)
			if len(matches) != 1 {
				t.Fatalf("%s external script count = %d, want one", example.name, len(matches))
			}
			got := matches[0][1]
			if got == "" {
				got = matches[0][2]
			}
			if got != example.runtime {
				t.Fatalf("%s external runtime = %q, want %s", example.name, got, example.runtime)
			}
			for _, marker := range []string{
				"data-kit-app",
				"data-kit-hydrate",
				"data-kit-plan",
				"data-kitwork-plan",
				"__kitjs_plan__",
			} {
				if strings.Contains(lower, marker) {
					t.Fatalf("%s contains server/runtime marker %q", example.name, marker)
				}
			}
			if example.runtime == "../hydrate.kit.js" && strings.Contains(lower, "<style") {
				t.Fatalf("%s contains custom CSS instead of Tailwind utilities", example.name)
			}
		})
	}
}

func TestHydrateExamplesShareTheClosedComponentDefinition(t *testing.T) {
	home := string(readVanillaFile(t, "examples", "hydrate-home.html"))
	next := string(readVanillaFile(t, "examples", "hydrate-next.html"))
	definition := `kit.component("hydrate-demo", {
      count: 0,
      note: ""
    });`
	if !strings.Contains(home, definition) || !strings.Contains(next, definition) {
		t.Fatal("Hydrate examples must carry the same component definition for direct loads")
	}
}

func javascriptWithoutComments(source string) string {
	var clean strings.Builder
	clean.Grow(len(source))
	for index := 0; index < len(source); {
		switch {
		case source[index] == '/' && index+1 < len(source) && source[index+1] == '/':
			clean.WriteString("  ")
			index += 2
			for index < len(source) && source[index] != '\n' {
				clean.WriteByte(' ')
				index++
			}
		case source[index] == '/' && index+1 < len(source) && source[index+1] == '*':
			clean.WriteString("  ")
			index += 2
			for index+1 < len(source) && !(source[index] == '*' && source[index+1] == '/') {
				if source[index] == '\n' {
					clean.WriteByte('\n')
				} else {
					clean.WriteByte(' ')
				}
				index++
			}
			if index+1 < len(source) {
				clean.WriteString("  ")
				index += 2
			}
		case source[index] == '\'' || source[index] == '"' || source[index] == '`':
			quote := source[index]
			clean.WriteByte(source[index])
			index++
			for index < len(source) {
				clean.WriteByte(source[index])
				if source[index] == '\\' && index+1 < len(source) {
					index++
					clean.WriteByte(source[index])
				} else if source[index] == quote {
					index++
					break
				}
				index++
			}
		default:
			clean.WriteByte(source[index])
			index++
		}
	}
	return clean.String()
}

func readVanillaFile(t *testing.T, path ...string) []byte {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve vanilla test directory")
	}
	parts := append([]string{filepath.Dir(filename)}, path...)
	source, err := os.ReadFile(filepath.Join(parts...))
	if err != nil {
		t.Fatal(err)
	}
	return source
}

// javascriptIdentifiers returns identifiers outside comments and string literals.
// It is deliberately small: this gate only needs to reject dependency/evaluation
// primitives, while the browser test proves the executable public contract.
func javascriptIdentifiers(source string) []string {
	identifiers := make([]string, 0, len(source)/12)
	for index := 0; index < len(source); {
		switch {
		case source[index] == '/' && index+1 < len(source) && source[index+1] == '/':
			index += 2
			for index < len(source) && source[index] != '\n' {
				index++
			}
		case source[index] == '/' && index+1 < len(source) && source[index+1] == '*':
			index += 2
			for index+1 < len(source) && !(source[index] == '*' && source[index+1] == '/') {
				index++
			}
			if index+1 < len(source) {
				index += 2
			}
		case source[index] == '\'' || source[index] == '"' || source[index] == '`':
			quote := source[index]
			index++
			for index < len(source) {
				if source[index] == '\\' {
					index += 2
					continue
				}
				if source[index] == quote {
					index++
					break
				}
				index++
			}
		case isJSIdentifierStart(source[index]):
			start := index
			index++
			for index < len(source) && isJSIdentifierPart(source[index]) {
				index++
			}
			identifiers = append(identifiers, source[start:index])
		default:
			index++
		}
	}
	return identifiers
}

func isJSIdentifierStart(value byte) bool {
	return value == '$' || value == '_' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func isJSIdentifierPart(value byte) bool {
	return isJSIdentifierStart(value) || value >= '0' && value <= '9'
}

func indexOfToken(tokens []string, want string) int {
	for index, token := range tokens {
		if token == want {
			return index
		}
	}
	return -1
}
