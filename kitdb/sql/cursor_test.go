package sql

import "testing"

func TestCursorOwnsBoundedTokenNavigation(t *testing.T) {
	tokens, err := Lex("SELECT products.*")
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := NewCursor(tokens, 1)
	if err != nil {
		t.Fatal(err)
	}
	if cursor.Position() != 1 || cursor.Peek().Text != "products" {
		t.Fatalf("initial cursor = position %d token %#v", cursor.Position(), cursor.Peek())
	}
	if token, found := cursor.At(2); !found || token.Text != "." {
		t.Fatalf("At(2) = %#v, %v", token, found)
	}
	if slice, found := cursor.Slice(1, 4); !found || len(slice) != 3 || slice[2].Text != "*" {
		t.Fatalf("Slice(1,4) = %#v, %v", slice, found)
	}
	if !cursor.Advance(3) || cursor.Peek().Kind != TokenEOF {
		t.Fatalf("cursor after Advance = position %d token %#v", cursor.Position(), cursor.Peek())
	}
	if !cursor.Restore(1) || cursor.Peek().Text != "products" {
		t.Fatalf("cursor after Restore = position %d token %#v", cursor.Position(), cursor.Peek())
	}
	if cursor.Restore(len(tokens)) {
		t.Fatal("cursor restored beyond token stream")
	}
	if !cursor.Advance(3) || cursor.Peek().Kind != TokenEOF {
		t.Fatalf("cursor after second Advance = position %d token %#v", cursor.Position(), cursor.Peek())
	}
	if cursor.Advance(1) {
		t.Fatal("cursor advanced beyond EOF")
	}
}

func TestCursorKeywordAndSymbolErrorsMatchSQLContract(t *testing.T) {
	tokens, err := Lex("query value")
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := NewCursor(tokens, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !cursor.AcceptKeyword("QUERY") {
		t.Fatal("case-insensitive keyword was not accepted")
	}
	if err := cursor.ExpectKeyword("plan"); err == nil || err.Error() != `kitdb SQL: expected PLAN, got "value"` {
		t.Fatalf("ExpectKeyword error = %v", err)
	}
	if err := cursor.ExpectSymbol("("); err == nil || err.Error() != `kitdb SQL: expected "(", got "value"` {
		t.Fatalf("ExpectSymbol error = %v", err)
	}
}

func TestNewCursorRejectsInvalidStream(t *testing.T) {
	if _, err := NewCursor(nil, 0); err == nil {
		t.Fatal("empty token stream was accepted")
	}
	if _, err := NewCursor([]Token{{Kind: TokenIdentifier, Text: "select"}}, 0); err == nil {
		t.Fatal("token stream without EOF was accepted")
	}
	tokens, err := Lex("SELECT 1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewCursor(tokens, len(tokens)); err == nil {
		t.Fatal("out-of-range cursor position was accepted")
	}
}
