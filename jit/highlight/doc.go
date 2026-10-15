// Package highlight reads source code into tokens on the server, so a page shows
// coloured code with no JavaScript in the browser.
//
// # What it gives
//
//	tokens, err := highlight.Tokenize("javascript", source)
//	for _, token := range tokens {
//		_ = token.Kind.String() // "keyword", "string", "regular-expression", …
//		_ = token.Text          // a slice of source
//	}
//
// Tokenize takes a language= value and the code. Languages lists the values,
// Has checks one, Suggest says what to write instead of a wrong one ("js" →
// javascript), and an unknown name comes back as an *UnknownLanguageError whose
// text is the author's message: `ngôn ngữ "js" chưa có; viết javascript`. A
// source over MaximumSourceSize (one mebibyte) comes back as one plain token
// with a *SourceTooLargeError, so one code block cannot exhaust the host's memory.
//
// Token kinds are a closed set (Kinds): plain, keyword, string, number, comment,
// function, type, constant, property, variable, operator, punctuation, tag,
// attribute, regular-expression, inserted, deleted, heading, prompt. A kind says
// what the text is, never its colour: the .ui compiler turns each kind into a
// class whose colour comes from the site's roles, native turns it into a text
// attribute. prompt belongs to the terminal component; no tokenizer emits it.
//
// # The contract every language keeps
//
//   - Lossless: the tokens' texts joined give back the source byte for byte, for
//     any input: invalid UTF-8, NUL bytes, CRLF, unterminated strings and
//     comments, ten megabytes of one character.
//   - Whole characters: for valid UTF-8 input, every token is valid UTF-8; no
//     boundary splits a character (a span or an AttributedString range would
//     otherwise cut one in half).
//   - Merged: two neighbouring tokens never share a kind, and no token is empty.
//   - Whitespace is plain: a token made only of whitespace is plain.
//   - Never panics, runs in linear time, never recurses without bound. No
//     backtracking regular expressions: tokenizers are hand-written scanners.
//
// Most of this holds by construction, not by care. A tokenizer never builds a
// token's text: it moves a scanner forward by saying "up to here is a <kind>",
// and the scanner slices the source, merges kinds, turns whitespace plain and
// emits whatever the tokenizer left unread as plain. Tokenize also recovers a
// panic into plain text and an error, but tests and the fuzz target call
// tokenizers unguarded, so a panic fails there.
//
// # Adding a language
//
// One file, its tests, one registry line:
//
//  1. <language>.go holds func tokenize<Language>(scan *scanner). Today every
//     language has this file as a stub that emits one plain token: replace its
//     body, keep its name.
//  2. <language>_test.go holds a table for checkExact (and checkContains for long
//     sources), and testdata/<language>/ holds at least one realistic sample. The
//     shared tests then run the contract over every sample in every language,
//     the fuzz target seeds from it, and the benchmark repeats it to ~100 KB.
//  3. The registry in tokenize.go maps the full name to the function.
//
// A tokenizer is a guarded loop over the scanner (a sketch, not the real json):
//
//	func tokenizeSketch(scan *scanner) {
//		for loop := scan.loop(); loop.next(); {
//			switch character := scan.peek(); {
//			case isWhitespace(character):
//				scan.emit(Plain, scanWhitespace(scan.source, scan.position))
//			case character == '"':
//				scan.emit(String, scanQuoted(scan.source, scan.position+1, doubleQuoted))
//			case isDecimalDigit(character):
//				scan.emit(Number, scanNumber(scan.source, scan.position))
//			default:
//				scan.emitCharacter(Punctuation)
//			}
//		}
//	}
//
// The scanner: done, peek, peekAt, startsWith, rest read; emit(kind, end),
// emitLength, emitCharacter, emitRest move; embed(tokenizer, end) hands a region
// to another language (css in a <style>, an expression in {{ }}); previous
// reports the last non-blank token (is this '/' a division?); loop guards a loop
// so a branch that forgets to emit costs one plain character instead of a hang.
//
// The helpers in lexical.go read one thing and return where it ends:
// scanWhitespace, scanToLineEnd, scanNewline, scanUntil, scanBlockComment,
// scanQuoted (escapes, doubled quotes, single or multiple lines), scanNumber
// (0x, 0b, 0o, 1_000, 2.5e-3, suffixes), scanIdentifier (any script's letters,
// plus the language's extra bytes), matchLongest (operators), wordSet (keywords).
//
// Rules that keep a tokenizer linear and safe: emit up to what a helper
// returned rather than reading the same text twice; never search ahead for a
// closer and then emit only the opener (that is quadratic on "<<<<"); never call
// a tokenizer from itself — nest with embed, whose depth is bounded, or track
// nesting with a counter; never panic on an index (byteAt and peek return 0 past
// the end).
//
// # The template highlighter
//
// highlight.go is the older half of this package: Render and Code colour the
// <code data-kit-highlight="…"> slots of .kitwork.html pages with utility classes
// and its own small grammars. It does not use Tokenize yet and keeps its own
// names (it still accepts "js" and "golang").
package highlight
