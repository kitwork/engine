package sql

import (
	"fmt"
	"strings"
)

// Cursor is the bounded token reader shared by KitDB statement parsers. It
// owns parser position so adapters do not maintain subtly different peek/take
// and lookahead rules.
type Cursor struct {
	tokens   []Token
	position int
}

// NewCursor creates a cursor at one validated token boundary. Tokens are
// copied so a caller cannot mutate parser input after construction.
func NewCursor(tokens []Token, position int) (*Cursor, error) {
	if len(tokens) == 0 || tokens[len(tokens)-1].Kind != TokenEOF {
		return nil, fmt.Errorf("kitdb SQL: token stream has no EOF marker")
	}
	if position < 0 || position >= len(tokens) {
		return nil, fmt.Errorf("kitdb SQL: token position %d is out of range", position)
	}
	return &Cursor{tokens: append([]Token(nil), tokens...), position: position}, nil
}

func (cursor *Cursor) Position() int {
	if cursor == nil {
		return 0
	}
	return cursor.position
}

func (cursor *Cursor) Len() int {
	if cursor == nil {
		return 0
	}
	return len(cursor.tokens)
}

func (cursor *Cursor) Peek() Token {
	if cursor == nil || cursor.position < 0 || cursor.position >= len(cursor.tokens) {
		return Token{Kind: TokenEOF}
	}
	return cursor.tokens[cursor.position]
}

func (cursor *Cursor) Take() Token {
	token := cursor.Peek()
	if cursor != nil && cursor.position < len(cursor.tokens) {
		cursor.position++
	}
	return token
}

// At returns one token without moving the cursor.
func (cursor *Cursor) At(position int) (Token, bool) {
	if cursor == nil || position < 0 || position >= len(cursor.tokens) {
		return Token{Kind: TokenEOF}, false
	}
	return cursor.tokens[position], true
}

// Slice returns a caller-owned token range without changing parser position.
func (cursor *Cursor) Slice(start, end int) ([]Token, bool) {
	if cursor == nil || start < 0 || end < start || end > len(cursor.tokens) {
		return nil, false
	}
	return append([]Token(nil), cursor.tokens[start:end]...), true
}

// Advance moves over exactly count tokens or leaves the cursor unchanged when
// that range would escape the token stream.
func (cursor *Cursor) Advance(count int) bool {
	if cursor == nil || count < 0 || cursor.position+count >= len(cursor.tokens) {
		return false
	}
	cursor.position += count
	return true
}

// Restore returns to an earlier parser checkpoint. It is intended for bounded
// syntax alternatives, not arbitrary token-stream mutation.
func (cursor *Cursor) Restore(position int) bool {
	if cursor == nil || position < 0 || position >= len(cursor.tokens) {
		return false
	}
	cursor.position = position
	return true
}

func (cursor *Cursor) AcceptKeyword(keyword string) bool {
	token := cursor.Peek()
	if token.Kind == TokenIdentifier && strings.EqualFold(token.Text, keyword) {
		cursor.position++
		return true
	}
	return false
}

func (cursor *Cursor) ExpectKeyword(keyword string) error {
	if !cursor.AcceptKeyword(keyword) {
		return fmt.Errorf("kitdb SQL: expected %s, got %q", strings.ToUpper(keyword), cursor.Peek().Text)
	}
	return nil
}

func (cursor *Cursor) AcceptSymbol(symbol string) bool {
	token := cursor.Peek()
	if token.Kind == TokenSymbol && token.Text == symbol {
		cursor.position++
		return true
	}
	return false
}

func (cursor *Cursor) ExpectSymbol(symbol string) error {
	if !cursor.AcceptSymbol(symbol) {
		return fmt.Errorf("kitdb SQL: expected %q, got %q", symbol, cursor.Peek().Text)
	}
	return nil
}
