package sql

import (
	"strings"
	"testing"
)

func TestLexProducesDecodedTokensAndSourceSpans(t *testing.T) {
	source := `SELECT "display""name", price, :merchant FROM [shopping] WHERE name = 'Highland''s' AND price >= 10.5;`
	tokens, err := Lex(source)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		kind TokenKind
		text string
	}{
		{TokenIdentifier, "SELECT"},
		{TokenIdentifier, `display"name`},
		{TokenSymbol, ","},
		{TokenIdentifier, "price"},
		{TokenSymbol, ","},
		{TokenPlaceholder, ":merchant"},
		{TokenIdentifier, "FROM"},
		{TokenIdentifier, "shopping"},
		{TokenIdentifier, "WHERE"},
		{TokenIdentifier, "name"},
		{TokenSymbol, "="},
		{TokenString, "Highland's"},
		{TokenIdentifier, "AND"},
		{TokenIdentifier, "price"},
		{TokenSymbol, ">="},
		{TokenNumber, "10.5"},
		{TokenSymbol, ";"},
		{TokenEOF, ""},
	}
	if len(tokens) != len(want) {
		t.Fatalf("Lex() returned %d tokens, want %d: %#v", len(tokens), len(want), tokens)
	}
	previousEnd := 0
	for index, expected := range want {
		token := tokens[index]
		if token.Kind != expected.kind || token.Text != expected.text {
			t.Fatalf("token %d = %#v, want kind=%d text=%q", index, token, expected.kind, expected.text)
		}
		if token.Start < previousEnd || token.End < token.Start || token.End > len(source) {
			t.Fatalf("token %d has invalid span [%d,%d)", index, token.Start, token.End)
		}
		previousEnd = token.End
	}
	if eof := tokens[len(tokens)-1]; eof.Start != len(source) || eof.End != len(source) {
		t.Fatalf("EOF span = [%d,%d), want %d", eof.Start, eof.End, len(source))
	}
}

func TestLexSkipsCommentsWithoutJoiningTokens(t *testing.T) {
	tokens, err := Lex("SELECT/* block */name -- line\nFROM shopping")
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if token.Kind != TokenEOF {
			got = append(got, token.Text)
		}
	}
	if strings.Join(got, "|") != "SELECT|name|FROM|shopping" {
		t.Fatalf("tokens = %q", got)
	}
}

func TestLexPreservesBoundedErrorContract(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{"empty", "", "statement is empty"},
		{"utf8", string([]byte{'S', 0xff}), "invalid UTF-8 at byte 1"},
		{"comment", "/*", "unterminated block comment at byte 0"},
		{"string", "'value", "unterminated string at byte 0"},
		{"identifier", `""`, "invalid quoted identifier at byte 0"},
		{"parameter", ":", "empty named parameter at byte 0"},
		{"symbol", "|", `unsupported character '|' at byte 0`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Lex(test.source)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Lex(%q) error = %v, want %q", test.source, err, test.want)
			}
		})
	}
}

func TestLexBoundsStatementBytesAndTokens(t *testing.T) {
	if _, err := Lex(strings.Repeat("x", MaximumStatementBytes+1)); err == nil || !strings.Contains(err.Error(), "statement exceeds 1048576 bytes") {
		t.Fatalf("byte bound error = %v", err)
	}
	if _, err := Lex(strings.Repeat("x ", MaximumStatementTokens+1)); err == nil || !strings.Contains(err.Error(), "statement exceeds 4096 tokens") {
		t.Fatalf("token bound error = %v", err)
	}
}
