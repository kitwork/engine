// Package highlight is the JIT syntax highlighter: a source-driven scan that
// fills author-declared code slots.
//
// An author marks a code block and names its language:
//
//	<pre><code data-kit-highlight="go">package main</code></pre>
//
// The author writes the short prefix; the filled block carries the long one
// (data-kitwork-highlight), matching the house source-vs-emitted convention.
// The browser kernel validates data-kit-* against its own directive list, so a
// server-only name must not survive into the delivered document.
//
// Render finds those blocks, tokenizes the text content, and wraps each token in
// a color class. Nothing else in the document is touched, and a block that was
// never marked is never rewritten — the engine fills a slot the author opened,
// it does not decide on its own that some code should be colored.
//
// Idempotent by construction. A slot holds plain text; a filled block holds
// elements. Render skips any block whose content already contains a raw '<', so
// a second pass over its own output finds nothing to do. No "already processed"
// flag is needed, and none can be lost by a cache, a morph, or a re-render.
//
// Safe by construction. Every text run is escaped and only `<span class="...">`
// is emitted, so code can never inject markup. Class names come from a fixed
// palette, never from the document.
//
// Because it emits utility classes, it MUST run before the JIT CSS pass so the
// classes it introduces are part of the set that pass generates CSS for.
package highlight

import (
	"html"
	"strings"

	jitcss "github.com/kitwork/engine/jit/css"
)

// Palette maps a token kind to the utility class that colors it. Built-in
// themes use arbitrary-hex classes so a site needs no configuration; a site that
// wants its own tokens can pass its own class names instead.
type Palette struct {
	Comment  string
	Keyword  string
	String   string
	Number   string
	Method   string
	Punct    string
	Property string
	Text     string

	// Splitting these out is what stops one role becoming a catch-all. Before
	// they existed, 25 of the 40 spans on this project's own home page were
	// punctuation — every bracket, dot, colon and operator the same colour,
	// which is close to not highlighting at all.
	Operator string
	Variable string

	// A shell line is not an expression: its first word is a command, a dash
	// word is a flag, and the rest are arguments. Colouring them as keywords and
	// identifiers says something untrue about the text.
	Command  string
	Flag     string
	Argument string
	Prompt   string
}

// A theme is ONE table covering both halves of a code block: the chrome around
// it (ground, title bar, window dots) and the syntax inside it. Editor themes
// are packaged exactly this way — a VS Code theme file carries `colors` for the
// interface and `tokenColors` for the code — so one name drives both here too.
var palettes = map[string]map[string]string{
	"tokyonight": {
		"DEFAULT": "#1a1b26", "bar": "#16161e", "line": "#292e42", "text": "#c0caf5",
		"comment": "#565f89", "red": "#f7768e", "yellow": "#e0af68", "green": "#9ece6a",
		"keyword": "#bb9af7", "method": "#7aa2f7", "string": "#9ece6a",
		"number": "#ff9e64", "punctuation": "#7dcfff", "property": "#73daca",
		"operator": "#89ddff", "variable": "#c0caf5", "command": "#7aa2f7", "flag": "#bb9af7", "argument": "#9ece6a", "prompt": "#565f89",
	},
	"classic": {
		"DEFAULT": "#1e1e1e", "bar": "#181818", "line": "#2d2d2d", "text": "#d4d4d4",
		"comment": "#6a9955", "red": "#f48771", "yellow": "#cca700", "green": "#89d185",
		"keyword": "#c586c0", "method": "#dcdcaa", "string": "#ce9178",
		"number": "#b5cea8", "punctuation": "#d4d4d4", "property": "#9cdcfe",
		"operator": "#d4d4d4", "variable": "#9cdcfe", "command": "#dcdcaa", "flag": "#c586c0", "argument": "#ce9178", "prompt": "#6a9955",
	},
	"mono": {
		"DEFAULT": "#17181c", "bar": "#111216", "line": "#2a2c33", "text": "#d1d5db",
		"comment": "#6b7280", "red": "#9ca3af", "yellow": "#9ca3af", "green": "#9ca3af",
		"keyword": "#e5e7eb", "method": "#f3f4f6", "string": "#d1d5db",
		"number": "#d1d5db", "punctuation": "#9ca3af", "property": "#d1d5db",
		"operator": "#9ca3af", "variable": "#d1d5db", "command": "#f3f4f6", "flag": "#e5e7eb", "argument": "#d1d5db", "prompt": "#6b7280",
	},
}

// DefaultTheme is what an unnamed or unknown theme falls back to.
const DefaultTheme = "tokyonight"

// tokenClasses is what the tokenizer emits: utility classes over the terminal
// token family, NOT baked colours. The theme decides what each one resolves to,
// so changing themes re-skins HTML that is already rendered instead of forcing
// every page through the renderer again.
var tokenClasses = Palette{
	Comment:  "text-terminal-comment",
	Keyword:  "text-terminal-keyword font-bold",
	String:   "text-terminal-string",
	Number:   "text-terminal-number",
	Method:   "text-terminal-method",
	Punct:    "text-terminal-punctuation",
	Property: "text-terminal-property",
	Text:     "text-terminal-text",
	Operator: "text-terminal-operator",
	Variable: "text-terminal-variable",
	Command:  "text-terminal-command font-bold",
	Flag:     "text-terminal-flag",
	Argument: "text-terminal-argument",
	Prompt:   "text-terminal-prompt",
}

// Registering the palette is what lets a page write bg-terminal or
// text-terminal-keyword with no colour block of its own. A site that DOES
// declare terminal in router.css() still wins: the config is consulted first.
func init() {
	jitcss.RegisterHighlightPalette(func(theme, role string) (string, bool) {
		table, ok := palettes[ThemeKey(theme)]
		if !ok {
			table = palettes[DefaultTheme]
		}
		hex, ok := table[role]
		return hex, ok
	})
}

// ThemeKey normalizes a theme name so router.highlight("tokyo-night"),
// "tokyo night" and "TokyoNight" all select the same palette. The canonical
// spelling is kebab-case, matching every other identifier in the project.
func ThemeKey(name string) string {
	var builder strings.Builder
	for index := 0; index < len(name); index++ {
		char := name[index]
		switch {
		case char >= 'A' && char <= 'Z':
			builder.WriteByte(char + 32)
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9':
			builder.WriteByte(char)
		}
	}
	return builder.String()
}

// PaletteFor returns the class set the tokenizer emits. It no longer varies by
// theme: the class names are fixed and the THEME decides what they resolve to,
// which is the whole reason a theme change no longer requires a re-render.
func PaletteFor(string) Palette { return tokenClasses }

// KnownTheme reports whether a name selects a built-in palette. Callers use it
// to reject a typo at config time instead of silently serving the default.
func KnownTheme(name string) bool {
	_, ok := palettes[ThemeKey(name)]
	return ok
}

// language describes one grammar as data. Adding a language is a table entry,
// not new code — deliberately, so the set stays small and reviewable.
type language struct {
	keywords    map[string]struct{}
	properties  map[string]struct{}
	lineComment string
	blockOpen   string
	blockClose  string
	quotes      string
	methods     bool // color identifier( as a call
	shell       bool // a line is command + flags + arguments, not an expression
}

func words(list ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(list))
	for _, word := range list {
		set[word] = struct{}{}
	}
	return set
}

var goLanguage = language{
	keywords: words("break", "case", "chan", "const", "continue", "default", "defer",
		"else", "fallthrough", "for", "func", "go", "goto", "if", "import", "interface",
		"map", "package", "range", "return", "select", "struct", "switch", "type", "var",
		"nil", "true", "false", "iota"),
	properties: words("string", "int", "int8", "int16", "int32", "int64", "uint", "uint8",
		"uint16", "uint32", "uint64", "byte", "rune", "float32", "float64", "bool", "error",
		"any", "make", "new", "len", "cap", "append", "copy", "delete", "panic", "recover"),
	lineComment: "//", blockOpen: "/*", blockClose: "*/", quotes: "\"'`", methods: true,
}

var javascriptLanguage = language{
	keywords: words("const", "let", "var", "function", "return", "if", "else", "for", "of",
		"in", "import", "export", "from", "default", "class", "extends", "new", "await",
		"async", "true", "false", "null", "undefined", "typeof", "instanceof", "delete",
		"switch", "case", "break", "continue", "throw", "try", "catch", "finally", "do",
		"while", "yield", "static", "get", "set"),
	lineComment: "//", blockOpen: "/*", blockClose: "*/", quotes: "\"'`", methods: true,
}

// kitworkLanguage is the subset this engine actually runs: no while, no do, no
// try/catch. Keeping them OUT of the keyword table is deliberate — code that
// uses them reads as plain text rather than as valid language, which is a quiet
// hint at the boundary rather than a lie about it.
var kitworkLanguage = language{
	keywords: words("const", "let", "function", "return", "if", "else", "for", "of",
		"import", "export", "from", "default", "new", "true", "false", "null", "undefined",
		"typeof", "switch", "case", "break", "continue"),
	lineComment: "//", blockOpen: "/*", blockClose: "*/", quotes: "\"'`", methods: true,
}

var jsonLanguage = language{
	keywords: words("true", "false", "null"),
	quotes:   "\"", methods: false,
}

var bashLanguage = language{
	keywords: words("if", "then", "else", "fi", "for", "in", "do", "done", "while", "case",
		"esac", "function", "return", "export", "local", "echo", "cd", "set", "source"),
	lineComment: "#", quotes: "\"'", methods: false, shell: true,
}

var sqlLanguage = language{
	keywords: words("SELECT", "FROM", "WHERE", "INSERT", "INTO", "VALUES", "UPDATE", "SET",
		"DELETE", "CREATE", "TABLE", "INDEX", "UNIQUE", "PRIMARY", "KEY", "FOREIGN",
		"REFERENCES", "JOIN", "LEFT", "RIGHT", "INNER", "OUTER", "ON", "GROUP", "BY",
		"ORDER", "LIMIT", "OFFSET", "AND", "OR", "NOT", "NULL", "AS", "DISTINCT", "ALTER",
		"DROP", "ADD", "COLUMN", "BEGIN", "COMMIT", "ROLLBACK", "RETURNING"),
	lineComment: "--", blockOpen: "/*", blockClose: "*/", quotes: "'\"", methods: false,
}

var cssLanguage = language{
	keywords:    words("import", "media", "supports", "keyframes", "font-face", "root"),
	lineComment: "", blockOpen: "/*", blockClose: "*/", quotes: "\"'", methods: false,
}

// languages is the closed set. An unknown name is not an error: the block is
// escaped and left uncolored, which is strictly better than guessing wrong.
var languages = map[string]language{
	"go":         goLanguage,
	"golang":     goLanguage,
	"js":         javascriptLanguage,
	"javascript": javascriptLanguage,
	"kitwork":    kitworkLanguage,
	"kitworkjs":  kitworkLanguage,
	"json":       jsonLanguage,
	"bash":       bashLanguage,
	"sh":         bashLanguage,
	"shell":      bashLanguage,
	"sql":        sqlLanguage,
	"css":        cssLanguage,
}

func isSpace(char byte) bool {
	return char == ' ' || char == '\t' || char == '\n' || char == '\r' || char == '\f'
}

func isDigit(char byte) bool { return char >= '0' && char <= '9' }

func isWordStart(char byte) bool {
	return char == '_' || char == '$' || char == '-' ||
		(char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z')
}

func isWord(char byte) bool { return isWordStart(char) || isDigit(char) }

// delimiters are the structural characters — brackets, separators, the dot of a
// member access. Everything else in a punctuation run is an operator, which is
// the distinction that stops one colour swallowing a quarter of every listing.
// isShellSeparator reports the characters that end one shell word or one
// command. Used to bound a path token and to reopen the command slot.
func isShellSeparator(char byte) bool {
	switch char {
	case '|', '&', ';', '<', '>', '(', ')':
		return true
	}
	return false
}

func isDelimiter(char byte) bool {
	switch char {
	case '(', ')', '[', ']', '{', '}', ',', ';', '.', ':':
		return true
	}
	return false
}

// isOperatorRun reports whether a punctuation run is entirely non-delimiter, so
// a mixed run like "})" stays structural rather than being called an operator.
func isOperatorRun(run string) bool {
	if run == "" {
		return false
	}
	for index := 0; index < len(run); index++ {
		if isDelimiter(run[index]) {
			return false
		}
	}
	return true
}

func span(builder *strings.Builder, class, content string) {
	if class == "" {
		builder.WriteString(content)
		return
	}
	builder.WriteString(`<span class="`)
	builder.WriteString(class)
	builder.WriteString(`">`)
	builder.WriteString(content)
	builder.WriteString(`</span>`)
}

// overrideClass turns one router.highlight({ role: value }) entry into the class
// the tokenizer emits. Three forms, because a site means three different things:
//
//	"#ff0000"                 a literal colour   → text-[#ff0000]
//	"brand"                   one of ITS tokens  → text-brand, and themeable with it
//	"text-pink-400 font-bold" spelled out        → used verbatim
//
// Naming a token is the interesting one: the role then follows whatever that
// token does, including a [data-theme] swap, without the theme knowing about it.
func overrideClass(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "#") {
		return "text-[" + value + "]"
	}
	// Already a utility (or several): a space or a known prefix means the site
	// spelled out exactly what it wants.
	if strings.ContainsAny(value, " 	") {
		return value
	}
	for _, prefix := range [...]string{"text-", "font-", "decoration-", "drop-shadow-"} {
		if strings.HasPrefix(value, prefix) {
			return value
		}
	}
	return "text-" + value
}

// PaletteWithOverrides applies a site's per-role overrides on top of the class
// set the tokenizer emits.
func PaletteWithOverrides(base Palette, overrides map[string]string) Palette {
	if len(overrides) == 0 {
		return base
	}
	set := func(target *string, role string) {
		if value, ok := overrides[role]; ok {
			if class := overrideClass(value); class != "" {
				*target = class
			}
		}
	}
	set(&base.Comment, "comment")
	set(&base.Keyword, "keyword")
	set(&base.String, "string")
	set(&base.Number, "number")
	set(&base.Method, "method")
	set(&base.Punct, "punctuation")
	set(&base.Property, "property")
	set(&base.Text, "text")
	set(&base.Operator, "operator")
	set(&base.Variable, "variable")
	set(&base.Command, "command")
	set(&base.Flag, "flag")
	set(&base.Argument, "argument")
	set(&base.Prompt, "prompt")
	return base
}

// Code tokenizes one block of source and returns HTML. Exported so a template
// method and this scan share exactly one implementation.
func Code(source, languageName string, palette Palette) string {
	lang, known := languages[ThemeKey(languageName)]
	if !known {
		return html.EscapeString(source)
	}

	var builder strings.Builder
	builder.Grow(len(source) * 3)
	length := len(source)

	// Shell state. A shell listing is read line by line: the first word of a
	// line names the program, everything after it is flags and arguments. That
	// resets at every newline, which is why it cannot live in the language table.
	commandTaken := false

	for index := 0; index < length; {
		char := source[index]

		// Whitespace passes through untouched so indentation survives exactly.
		if isSpace(char) {
			start := index
			for index < length && isSpace(source[index]) {
				index++
			}
			run := source[start:index]
			if strings.IndexByte(run, 10) >= 0 { // a newline ends the shell line
				commandTaken = false
			}
			builder.WriteString(run)
			continue
		}

		// Line comment.
		if lang.lineComment != "" && strings.HasPrefix(source[index:], lang.lineComment) {
			end := index
			// Stop before the carriage return as well, or a CRLF file leaves
			// the \r inside the comment token. The browser then normalizes that
			// \r to a line break of its own and the block renders one blank
			// line too many.
			for end < length && source[end] != '\n' && source[end] != '\r' {
				end++
			}
			span(&builder, palette.Comment, html.EscapeString(source[index:end]))
			index = end
			continue
		}

		// Block comment. An unterminated block runs to the end of the source
		// rather than dropping the tail.
		if lang.blockOpen != "" && strings.HasPrefix(source[index:], lang.blockOpen) {
			end := strings.Index(source[index+len(lang.blockOpen):], lang.blockClose)
			if end < 0 {
				end = length
			} else {
				end = index + len(lang.blockOpen) + end + len(lang.blockClose)
			}
			span(&builder, palette.Comment, html.EscapeString(source[index:end]))
			index = end
			continue
		}

		// String. Backslash escapes are respected so a quote inside a string
		// does not end it early.
		if strings.IndexByte(lang.quotes, char) >= 0 {
			end := index + 1
			for end < length {
				if source[end] == '\\' && end+1 < length {
					end += 2
					continue
				}
				if source[end] == char {
					end++
					break
				}
				end++
			}
			span(&builder, palette.String, html.EscapeString(source[index:end]))
			index = end
			continue
		}

		// Number.
		if isDigit(char) {
			end := index
			for end < length && (isDigit(source[end]) || source[end] == '.' ||
				source[end] == 'x' || source[end] == '_' ||
				(source[end] >= 'a' && source[end] <= 'f') ||
				(source[end] >= 'A' && source[end] <= 'F')) {
				end++
			}
			span(&builder, palette.Number, html.EscapeString(source[index:end]))
			index = end
			continue
		}

		// A path is one shell word. Left to the generic branches, "./run.sh" came
		// apart into punctuation, "run", punctuation, "sh" — four tokens for one
		// program name.
		if lang.shell && (char == '.' || char == '/') {
			end := index
			for end < length && !isSpace(source[end]) && !isShellSeparator(source[end]) {
				end++
			}
			if end > index {
				class := palette.Argument
				if !commandTaken {
					class = palette.Command
					commandTaken = true
				}
				span(&builder, class, html.EscapeString(source[index:end]))
				index = end
				continue
			}
		}

		// Word: keyword, known property, call, or plain identifier.
		if isWordStart(char) {
			end := index
			for end < length && isWord(source[end]) {
				end++
			}
			// A shell word runs to whitespace: "mysite.com" and
			// "http://localhost:8080/" are ONE argument each, not five tokens
			// broken at every dot and slash. "=" stays outside so VAR=1 keeps its
			// assignment shape and --flag=value still shows the flag.
			if lang.shell {
				for end < length && !isSpace(source[end]) && !isShellSeparator(source[end]) &&
					source[end] != '=' && source[end] != '#' &&
					strings.IndexByte(lang.quotes, source[end]) < 0 {
					end++
				}
			}
			word := source[index:end]
			class := ""
			if lang.shell {
				// VAR=1 ./run.sh — an assignment prefix is not the program being
				// run, so it must not consume the command slot.
				assignment := end < length && source[end] == '=' &&
					(end+1 >= length || source[end+1] != '=')
				switch {
				case word == "$" || word == "%" || word == "❯":
					class = palette.Prompt
				case strings.HasPrefix(word, "-"):
					class = palette.Flag
				case assignment:
					// Consume NAME=VALUE together. Leaving the value to the next
					// pass meant "ALLOW_LOCAL=true go run ." coloured `true` as
					// the command, when the command is `go`.
					span(&builder, palette.Variable, html.EscapeString(word))
					span(&builder, palette.Operator, "=")
					index = end + 1
					valueEnd := index
					for valueEnd < length && !isSpace(source[valueEnd]) &&
						!isShellSeparator(source[valueEnd]) {
						valueEnd++
					}
					if valueEnd > index {
						span(&builder, palette.Argument, html.EscapeString(source[index:valueEnd]))
						index = valueEnd
					}
					continue
				case !commandTaken:
					class = palette.Command
					commandTaken = true
				default:
					class = palette.Argument
				}
				span(&builder, class, html.EscapeString(word))
				index = end
				continue
			}
			if _, ok := lang.keywords[word]; ok {
				class = palette.Keyword
			} else if _, ok := lang.keywords[strings.ToUpper(word)]; ok && lang.lineComment == "--" {
				class = palette.Keyword // SQL keywords are case-insensitive
			} else if _, ok := lang.properties[word]; ok {
				class = palette.Property
			} else {
				probe := end
				for probe < length && isSpace(source[probe]) {
					probe++
				}
				switch {
				case lang.methods && probe < length && source[probe] == '(':
					class = palette.Method
				case probe < length && source[probe] == ':' &&
					(probe+1 >= length || source[probe+1] != ':'):
					// A key in an object literal. Without this every option name
					// in a config block rendered as undifferentiated body text.
					class = palette.Property
				default:
					class = palette.Variable
				}
			}
			span(&builder, class, html.EscapeString(word))
			index = end
			continue
		}

		// Punctuation run.
		start := index
		for index < length && !isSpace(source[index]) && !isWordStart(source[index]) &&
			!isDigit(source[index]) && strings.IndexByte(lang.quotes, source[index]) < 0 {
			if lang.lineComment != "" && strings.HasPrefix(source[index:], lang.lineComment) {
				break
			}
			if lang.blockOpen != "" && strings.HasPrefix(source[index:], lang.blockOpen) {
				break
			}
			index++
		}
		if index == start {
			index++ // never stall
		}
		run := source[start:index]
		class := palette.Punct
		if isOperatorRun(run) {
			class = palette.Operator
		}
		// `a | b`, `a && b`, `a; b` — everything after a separator is a new
		// command line, so the command slot reopens.
		if lang.shell && strings.ContainsAny(run, "|&;") {
			commandTaken = false
		}
		span(&builder, class, html.EscapeString(run))
	}

	return builder.String()
}

const (
	markerShort = "data-kit-highlight"
	markerLong  = "data-kitwork-highlight"
)

// Render fills every author-declared highlight slot in an HTML document. A
// document with no slot is returned unchanged after one scan for the marker, so
// the common case costs a substring search.
func Render(source, theme string, overrides map[string]string) string {
	// Both markers end in this suffix, so one search covers the short and long
	// prefixes without matching either one alone.
	if !strings.Contains(source, "-highlight") {
		return source
	}
	palette := PaletteWithOverrides(PaletteFor(theme), overrides)

	var builder strings.Builder
	builder.Grow(len(source) + len(source)/2)
	rest := source

	for {
		open := strings.Index(rest, "<code")
		if open < 0 {
			break
		}
		tagEnd := strings.IndexByte(rest[open:], '>')
		if tagEnd < 0 {
			break
		}
		tagEnd += open
		tag := rest[open : tagEnd+1]

		languageName, marked := markedLanguage(tag)
		if !marked {
			builder.WriteString(rest[:tagEnd+1])
			rest = rest[tagEnd+1:]
			continue
		}

		closeAt := strings.Index(rest[tagEnd+1:], "</code>")
		if closeAt < 0 {
			break
		}
		closeAt += tagEnd + 1
		content := rest[tagEnd+1 : closeAt]

		// The author writes the short prefix; the engine emits the long one. That
		// is the house convention (source vs engine-emitted), and it earns its
		// keep twice over here: the browser kernel validates data-kit-* against
		// its own directive list and would reject a server-only name, while
		// data-kitwork-* is its engine namespace and is left alone.
		builder.WriteString(rest[:open])
		builder.WriteString(promoteMarker(tag))
		// A slot holds text. Content that already carries elements is output
		// from an earlier pass and is left exactly as it is.
		if strings.IndexByte(content, '<') >= 0 {
			builder.WriteString(content)
		} else {
			builder.WriteString(Code(html.UnescapeString(content), languageName, palette))
		}
		builder.WriteString("</code>")
		rest = rest[closeAt+len("</code>"):]
	}

	builder.WriteString(rest)
	return builder.String()
}

// promoteMarker rewrites the author's data-kit-highlight into the engine's
// data-kitwork-highlight on one open tag, leaving every other attribute alone.
// A tag already carrying the long form is returned unchanged.
func promoteMarker(tag string) string {
	if strings.Contains(tag, markerLong) {
		return tag
	}
	at := strings.Index(tag, markerShort)
	if at < 0 {
		return tag
	}
	return tag[:at] + markerLong + tag[at+len(markerShort):]
}

// markedLanguage reads the highlight attribute off one <code ...> open tag.
// Both prefixes are accepted, matching how jit/theme reads its own marker.
func markedLanguage(tag string) (string, bool) {
	for _, marker := range [...]string{markerLong, markerShort} {
		at := strings.Index(tag, marker)
		if at < 0 {
			continue
		}
		rest := tag[at+len(marker):]
		rest = strings.TrimLeft(rest, " \t\r\n")
		if !strings.HasPrefix(rest, "=") {
			continue
		}
		rest = strings.TrimLeft(rest[1:], " \t\r\n")
		if rest == "" {
			continue
		}
		quote := rest[0]
		if quote != '"' && quote != '\'' {
			end := 0
			for end < len(rest) && !isSpace(rest[end]) && rest[end] != '>' {
				end++
			}
			return rest[:end], true
		}
		end := strings.IndexByte(rest[1:], quote)
		if end < 0 {
			continue
		}
		return rest[1 : 1+end], true
	}
	return "", false
}
