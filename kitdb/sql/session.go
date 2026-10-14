package sql

import (
	"errors"
	"strconv"
	"strings"
)

// ParseKillSession keeps runtime control separate from data/schema SQL.
func ParseKillSession(source string) (int64, bool, error) {
	if !ContainsFold(source, "kill") {
		return 0, false, nil
	}
	tokens, err := Lex(source)
	invalid := errors.New("invalid KILL SESSION statement")
	if err != nil {
		return 0, true, invalid
	}
	cursor, err := NewCursor(tokens, 0)
	if err != nil {
		return 0, true, invalid
	}
	first := cursor.Take()
	if first.Kind != TokenIdentifier || !strings.EqualFold(first.Text, "kill") {
		return 0, false, nil
	}
	if !cursor.AcceptKeyword("session") {
		return 0, true, invalid
	}
	token := cursor.Take()
	id, err := strconv.ParseInt(token.Text, 10, 64)
	if token.Kind != TokenNumber || err != nil || id <= 0 {
		return 0, true, invalid
	}
	cursor.AcceptSymbol(";")
	if cursor.Peek().Kind != TokenEOF {
		return 0, true, invalid
	}
	return id, true, nil
}
