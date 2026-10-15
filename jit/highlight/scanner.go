package highlight

import (
	"strings"
	"unicode/utf8"
)

// output is the token list every scanner of one Tokenize call writes into, the
// embedded ones included. Offsets here are absolute: into output.source.
type output struct {
	source string
	tokens []Token

	// Where the last two tokens start, so a merge can re-slice instead of
	// concatenating (linear, no copy) and previous can find its token.
	lastStart       int
	beforeLastStart int
}

// add appends source[start:end] as kind, keeping the contract as it goes: a
// whitespace-only run is plain whatever the tokenizer called it, and a run of
// the same kind as the token before it extends that token.
func (out *output) add(kind Kind, start, end int) {
	text := out.source[start:end]
	if kind != Plain && isAllWhitespace(text) {
		kind = Plain
	}
	if count := len(out.tokens); count > 0 && out.tokens[count-1].Kind == kind {
		out.tokens[count-1].Text = out.source[out.lastStart:end]
		return
	}
	if len(out.tokens) == cap(out.tokens) {
		out.grow(end)
	}
	out.tokens = append(out.tokens, Token{Kind: kind, Text: text})
	out.beforeLastStart, out.lastStart = out.lastStart, start
}

// grow makes room for the token that ends at end and, judging by how many
// tokens the source has given so far, for the rest of the source too. Append's
// own growth (a quarter at a time) would allocate about five times the final
// list on input that makes a token of every byte; this sizes it from the
// density so far, at least a quarter more (so growth stays geometric) and never
// more than one token per byte left, which no input can exceed.
func (out *output) grow(end int) {
	count := len(out.tokens)
	remaining := len(out.source) - end
	capacity := count + count/4 + 16
	if end > 0 {
		capacity = max(capacity, count+int(float64(count)*float64(remaining)/float64(end)*1.125))
	}
	capacity = min(capacity, count+1+remaining)
	grown := make([]Token, count, capacity)
	copy(grown, out.tokens)
	out.tokens = grown
}

// scanner walks one source (or, embedded, one region of it) from left to right.
// A tokenizer reads with peek/startsWith and the scan* helpers, and moves only by
// emitting: every emit says "from here up to end is a <kind>". Emits never go
// backwards and never past the end, which is why tokens always join back into
// the source.
type scanner struct {
	source   string // this scanner's text: the whole source, or an embedded region
	position int    // the first byte not yet emitted
	base     int    // where source starts inside output.source
	depth    int    // how many embeds deep this scanner is
	out      *output
}

// maximumEmbedDepth bounds embedding. A region deeper than this is emitted as
// plain text instead of being tokenized, so no input can nest its way into a
// stack overflow, which is the one failure recover cannot catch.
const maximumEmbedDepth = 32

// done reports whether everything has been emitted.
func (scan *scanner) done() bool { return scan.position >= len(scan.source) }

// peek is the byte at the position, or 0 at the end. Source may hold real NUL
// bytes, so a tokenizer checks done() rather than trusting 0.
func (scan *scanner) peek() byte { return byteAt(scan.source, scan.position) }

// peekAt is the byte offset bytes ahead of the position, or 0 past the end.
func (scan *scanner) peekAt(offset int) byte { return byteAt(scan.source, scan.position+offset) }

// startsWith reports whether the unread text begins with prefix.
func (scan *scanner) startsWith(prefix string) bool {
	return strings.HasPrefix(scan.source[scan.position:], prefix)
}

// rest is the unread text.
func (scan *scanner) rest() string { return scan.source[scan.position:] }

// emit marks source[position:end] as kind and moves to end. An end at or before
// the position emits nothing; an end past the source stops at the source's end.
func (scan *scanner) emit(kind Kind, end int) {
	if end > len(scan.source) {
		end = len(scan.source)
	}
	if end <= scan.position {
		return
	}
	scan.out.add(kind, scan.base+scan.position, scan.base+end)
	scan.position = end
}

// emitLength marks the next length bytes as kind.
func (scan *scanner) emitLength(kind Kind, length int) { scan.emit(kind, scan.position+length) }

// emitCharacter marks the next character as kind: one UTF-8 sequence, or one
// byte when the bytes there are not valid UTF-8.
func (scan *scanner) emitCharacter(kind Kind) {
	if scan.done() {
		return
	}
	_, size := utf8.DecodeRuneInString(scan.source[scan.position:])
	scan.emit(kind, scan.position+size)
}

// emitRest marks everything unread as kind.
func (scan *scanner) emitRest(kind Kind) { scan.emit(kind, len(scan.source)) }

// embed tokenizes source[position:end] with another tokenizer (css inside a
// <style>, an expression inside {{ }}) and moves to end. The embedded tokenizer
// sees only that region; what it leaves unread is plain. Its tokens merge with
// the surrounding ones like any others.
func (scan *scanner) embed(tokenize tokenizer, end int) {
	if end > len(scan.source) {
		end = len(scan.source)
	}
	if end <= scan.position {
		return
	}
	if scan.depth >= maximumEmbedDepth {
		scan.emit(Plain, end)
		return
	}
	region := &scanner{
		source: scan.source[scan.position:end],
		base:   scan.base + scan.position,
		depth:  scan.depth + 1,
		out:    scan.out,
	}
	tokenize(region)
	region.emitRest(Plain)
	scan.position = end
}

// previous is the last token before the position that is not only whitespace,
// cut to end at its last non-whitespace byte: (Keyword, "return"), (Plain, "x"),
// (Punctuation, ")"). It is what a tokenizer asks when a '/' could start a
// regular expression or divide. At the start of this scanner's region it is
// (Plain, ""); it never reaches back past the region.
//
// Plain runs merge, so "a b" may come back as one plain text; only its last
// byte is reliable as "the last thing written".
func (scan *scanner) previous() (Kind, string) {
	index := scan.position - 1
	for index >= 0 && isWhitespace(scan.source[index]) {
		index--
	}
	if index < 0 {
		return Plain, ""
	}
	absolute := scan.base + index
	tokens := scan.out.tokens
	kind, start := tokens[len(tokens)-1].Kind, scan.out.lastStart
	if start > absolute {
		// The last token is whitespace only (it starts after the byte), so the
		// byte sits in the token before it: whitespace-only tokens are plain and
		// plain tokens never follow plain ones.
		kind, start = tokens[len(tokens)-2].Kind, scan.out.beforeLastStart
	}
	if start < scan.base {
		start = scan.base
	}
	return kind, scan.out.source[start : absolute+1]
}

// loop guards one tokenizer loop against spinning:
//
//	for loop := scan.loop(); loop.next(); {
//		...
//	}
//
// next reports whether there is text left. If the iteration before consumed
// nothing, next first emits one character as plain, so a branch that forgets to
// emit costs one plain character instead of hanging the server.
func (scan *scanner) loop() scanLoop { return scanLoop{scan: scan, last: -1} }

// scanLoop is the state of one guarded loop; nested loops each take their own.
type scanLoop struct {
	scan *scanner
	last int
}

func (loop *scanLoop) next() bool {
	if loop.scan.position == loop.last {
		loop.scan.emitCharacter(Plain)
	}
	loop.last = loop.scan.position
	return !loop.scan.done()
}
