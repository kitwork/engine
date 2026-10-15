package highlight

import (
	"fmt"
	"sort"
)

// Token is one run of source and what it is. Text is always a slice of the
// source passed to Tokenize, so tokens cost no copy of the code.
type Token struct {
	Kind Kind
	Text string
}

// tokenizer reads a whole source through a scanner. It only ever says "up to
// here is a <kind>"; the scanner cuts the text, so a tokenizer cannot lose,
// duplicate or reorder a byte. Whatever it leaves unread becomes plain text.
type tokenizer func(scan *scanner)

// registry maps each language= value to its tokenizer. Full names only: a short
// name is not a second way to say the same thing, it is a miss with a
// suggestion (see Suggest). One line per language.
//
// Filled in init rather than as a literal so a tokenizer may look another one up
// here (html embedding css) without an initialization cycle.
var registry map[string]tokenizer

func init() {
	registry = map[string]tokenizer{
		"bash":       tokenizeBash,
		"css":        tokenizeCSS,
		"diff":       tokenizeDiff,
		"go":         tokenizeGo,
		"html":       tokenizeHTML,
		"javascript": tokenizeJavaScript,
		"json":       tokenizeJSON,
		"powershell": tokenizePowerShell,
		"python":     tokenizePython,
		"sql":        tokenizeSQL,
		"text":       tokenizeText,
		"typescript": tokenizeTypeScript,
		"ui":         tokenizeUI,
		"work":       tokenizeWork,
		"yaml":       tokenizeYAML,
	}
}

// Tokenize splits source into tokens of one language.
//
// For a known language the tokens always join back into source byte for byte,
// whatever the input (invalid UTF-8, NUL bytes, unterminated strings), adjacent
// tokens never share a kind, and a token that is only whitespace is plain.
//
// An unknown language returns the whole source as one plain token together with
// an *UnknownLanguageError whose text names the fix, so a caller can either
// show the code uncoloured or refuse the page.
//
// A source longer than MaximumSourceSize also comes back as one plain token,
// with a *SourceTooLargeError.
//
// It never panics. A tokenizer bug that would panic instead comes back as the
// source in one plain token and an error naming the language; the tests and the
// fuzz target call tokenizers unguarded, so such a bug fails there first.
func Tokenize(language, source string) ([]Token, error) {
	tokenize, known := registry[language]
	if !known {
		return wholeSource(source), unknownLanguage(language)
	}
	if len(source) > MaximumSourceSize {
		return wholeSource(source), &SourceTooLargeError{Language: language, Size: len(source), Limit: MaximumSourceSize}
	}
	return guarded(language, tokenize, source)
}

// MaximumSourceSize is the longest source Tokenize colours, in bytes: one
// mebibyte, about 25,000 lines of code, far more than a page shows. The token
// list is linear in the source but holds 24 bytes per token, and running out of
// memory is the one failure no recover catches; one tenant's code block must not
// take down the process every tenant shares.
const MaximumSourceSize = 1 << 20

// SourceTooLargeError is what Tokenize returns for a source longer than
// MaximumSourceSize, together with the source as one plain token.
type SourceTooLargeError struct {
	Language string
	Size     int // bytes in the source
	Limit    int // MaximumSourceSize
}

func (failure *SourceTooLargeError) Error() string {
	return fmt.Sprintf("khối mã %s dài %d byte, quá giới hạn %d byte; khối mã hiện thành chữ thường", failure.Language, failure.Size, failure.Limit)
}

// Languages lists every language= value, sorted.
func Languages() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Has reports whether language is a language= value, exactly as written.
func Has(language string) bool {
	_, known := registry[language]
	return known
}

// guarded runs a tokenizer and turns a panic into plain text and an error.
func guarded(language string, tokenize tokenizer, source string) (tokens []Token, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			tokens = wholeSource(source)
			err = fmt.Errorf("bộ tách từ của ngôn ngữ %q gặp lỗi nội bộ (%v); khối mã hiện thành chữ thường", language, recovered)
		}
	}()
	return tokenizeWith(tokenize, source), nil
}

// tokenizeWith runs a tokenizer over the whole source with no guard. Tests and
// the fuzz target use it so a panic surfaces instead of being absorbed.
func tokenizeWith(tokenize tokenizer, source string) []Token {
	if source == "" {
		return []Token{}
	}
	// Code averages three to five bytes a token, so a quarter of the source is
	// room for most of it; output.grow takes it from there.
	out := &output{source: source, tokens: make([]Token, 0, len(source)/4+16)}
	scan := &scanner{source: source, out: out}
	tokenize(scan)
	scan.emitRest(Plain)
	if cap(out.tokens) > 2*len(out.tokens)+64 {
		// Few long tokens (a plain text, a diff): keep only what they use.
		return append([]Token(nil), out.tokens...)
	}
	return out.tokens
}

// wholeSource is the source as one plain token: what text produces, and what an
// unknown language or a failed tokenizer falls back to.
func wholeSource(source string) []Token {
	if source == "" {
		return []Token{}
	}
	return []Token{{Kind: Plain, Text: source}}
}
