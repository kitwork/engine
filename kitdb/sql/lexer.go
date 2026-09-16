package sql

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MaximumStatementBytes  = 1 << 20
	MaximumStatementTokens = 4096
)

// TokenKind identifies one lexical SQL unit without assigning dialect
// semantics. Keywords remain identifiers until the parser binds them in
// context.
type TokenKind uint8

const (
	TokenEOF TokenKind = iota
	TokenIdentifier
	TokenString
	TokenNumber
	TokenPlaceholder
	TokenSymbol
)

// Token retains its decoded text and half-open byte range in the original SQL
// source. Spans let later parser and binder stages report precise diagnostics
// without retaining a second lexer-specific position table.
type Token struct {
	Kind  TokenKind
	Text  string
	Start int
	End   int
}

// Lex tokenizes one bounded KitDB SQL statement. It performs no keyword or
// schema interpretation and is therefore safe to share across embedded,
// PostgreSQL-wire, and Kitwork frontends.
func Lex(source string) ([]Token, error) {
	if len(source) == 0 {
		return nil, fmt.Errorf("kitdb SQL: statement is empty")
	}
	if len(source) > MaximumStatementBytes {
		return nil, fmt.Errorf("kitdb SQL: statement exceeds %d bytes", MaximumStatementBytes)
	}
	tokens := make([]Token, 0, 32)
	appendToken := func(kind TokenKind, text string, start, end int) error {
		if len(tokens) >= MaximumStatementTokens {
			return fmt.Errorf("kitdb SQL: statement exceeds %d tokens", MaximumStatementTokens)
		}
		tokens = append(tokens, Token{Kind: kind, Text: text, Start: start, End: end})
		return nil
	}
	for offset := 0; offset < len(source); {
		r, size := utf8.DecodeRuneInString(source[offset:])
		if r == utf8.RuneError && size == 1 {
			return nil, fmt.Errorf("kitdb SQL: invalid UTF-8 at byte %d", offset)
		}
		if unicode.IsSpace(r) {
			offset += size
			continue
		}
		if strings.HasPrefix(source[offset:], "--") {
			offset += 2
			for offset < len(source) && source[offset] != '\n' && source[offset] != '\r' {
				offset++
			}
			continue
		}
		if strings.HasPrefix(source[offset:], "/*") {
			end := strings.Index(source[offset+2:], "*/")
			if end < 0 {
				return nil, fmt.Errorf("kitdb SQL: unterminated block comment at byte %d", offset)
			}
			offset += end + 4
			continue
		}
		if r == '\'' {
			start := offset
			offset++
			var text strings.Builder
			closed := false
			for offset < len(source) {
				if source[offset] == '\'' {
					if offset+1 < len(source) && source[offset+1] == '\'' {
						text.WriteByte('\'')
						offset += 2
						continue
					}
					offset++
					closed = true
					break
				}
				text.WriteByte(source[offset])
				offset++
			}
			if !closed {
				return nil, fmt.Errorf("kitdb SQL: unterminated string at byte %d", start)
			}
			if err := appendToken(TokenString, text.String(), start, offset); err != nil {
				return nil, err
			}
			continue
		}
		if r == '"' || r == '`' || r == '[' {
			start := offset
			closeByte := byte(r)
			if r == '[' {
				closeByte = ']'
			}
			offset++
			var text strings.Builder
			closed := false
			for offset < len(source) {
				if source[offset] == closeByte {
					if closeByte != ']' && offset+1 < len(source) && source[offset+1] == closeByte {
						text.WriteByte(closeByte)
						offset += 2
						continue
					}
					offset++
					closed = true
					break
				}
				text.WriteByte(source[offset])
				offset++
			}
			if !closed || text.Len() == 0 {
				return nil, fmt.Errorf("kitdb SQL: invalid quoted identifier at byte %d", start)
			}
			if err := appendToken(TokenIdentifier, text.String(), start, offset); err != nil {
				return nil, err
			}
			continue
		}
		if strings.HasPrefix(source[offset:], "::") {
			if err := appendToken(TokenSymbol, "::", offset, offset+2); err != nil {
				return nil, err
			}
			offset += 2
			continue
		}
		if r == '?' || r == ':' || r == '@' || r == '$' {
			start := offset
			offset += size
			for offset < len(source) {
				next, nextSize := utf8.DecodeRuneInString(source[offset:])
				if !unicode.IsLetter(next) && !unicode.IsDigit(next) && next != '_' {
					break
				}
				offset += nextSize
			}
			if r != '?' && offset == start+size {
				return nil, fmt.Errorf("kitdb SQL: empty named parameter at byte %d", start)
			}
			if err := appendToken(TokenPlaceholder, source[start:offset], start, offset); err != nil {
				return nil, err
			}
			continue
		}
		if unicode.IsLetter(r) || r == '_' {
			start := offset
			offset += size
			for offset < len(source) {
				next, nextSize := utf8.DecodeRuneInString(source[offset:])
				if !unicode.IsLetter(next) && !unicode.IsDigit(next) && next != '_' {
					break
				}
				offset += nextSize
			}
			if err := appendToken(TokenIdentifier, source[start:offset], start, offset); err != nil {
				return nil, err
			}
			continue
		}
		if unicode.IsDigit(r) || r == '.' && offset+1 < len(source) && source[offset+1] >= '0' && source[offset+1] <= '9' {
			start := offset
			dot := r == '.'
			offset += size
			for offset < len(source) {
				next := source[offset]
				if next == '.' && !dot {
					dot = true
					offset++
					continue
				}
				if next < '0' || next > '9' {
					break
				}
				offset++
			}
			if offset < len(source) && (source[offset] == 'e' || source[offset] == 'E') {
				exponent := offset
				offset++
				if offset < len(source) && (source[offset] == '+' || source[offset] == '-') {
					offset++
				}
				digits := offset
				for offset < len(source) && source[offset] >= '0' && source[offset] <= '9' {
					offset++
				}
				if digits == offset {
					return nil, fmt.Errorf("kitdb SQL: invalid numeric exponent at byte %d", exponent)
				}
			}
			if err := appendToken(TokenNumber, source[start:offset], start, offset); err != nil {
				return nil, err
			}
			continue
		}
		if r == '|' {
			if !strings.HasPrefix(source[offset:], "||") {
				return nil, fmt.Errorf("kitdb SQL: unsupported character %q at byte %d", r, offset)
			}
			if err := appendToken(TokenSymbol, "||", offset, offset+2); err != nil {
				return nil, err
			}
			offset += 2
			continue
		}
		if strings.ContainsRune("(),.*;+/-/%", r) {
			start := offset
			offset += size
			if err := appendToken(TokenSymbol, string(r), start, offset); err != nil {
				return nil, err
			}
			continue
		}
		if strings.ContainsRune("=!<>", r) {
			start := offset
			operator := string(r)
			offset += size
			if offset < len(source) {
				next := source[offset]
				if next == '=' || (r == '<' && next == '>') {
					operator += string(next)
					offset++
				}
			}
			if err := appendToken(TokenSymbol, operator, start, offset); err != nil {
				return nil, err
			}
			continue
		}
		return nil, fmt.Errorf("kitdb SQL: unsupported character %q at byte %d", r, offset)
	}
	tokens = append(tokens, Token{Kind: TokenEOF, Start: len(source), End: len(source)})
	return tokens, nil
}
