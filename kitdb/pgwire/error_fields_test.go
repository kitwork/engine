package pgwire

import (
	"bufio"
	"bytes"
	"errors"
	"testing"
)

// An error's schema, table and constraint go in fields s, t and n of the ErrorResponse, as PostgreSQL sends them with
// a broken constraint (a client reads n to know which); an error without them sends none.
//
// CONTROL (08/11/2026): with the fields not written, "fields map['C':\"23514\" 'M':… 'S':\"ERROR\" 'V':\"ERROR\"], want n
// notes_text_check".
func TestErrorFields(t *testing.T) {
	fields := func(err error) map[byte]string {
		var buffer bytes.Buffer
		writer := bufio.NewWriter(&buffer)
		if write := writeError(writer, err); write != nil {
			t.Fatal(write)
		}
		if err := writer.Flush(); err != nil {
			t.Fatal(err)
		}
		message := buffer.Bytes()
		if len(message) < 5 || message[0] != 'E' {
			t.Fatalf("not an ErrorResponse: %q", message)
		}
		found := map[byte]string{}
		for body := message[5:]; len(body) > 0 && body[0] != 0; {
			end := bytes.IndexByte(body[1:], 0)
			found[body[0]] = string(body[1 : end+1])
			body = body[end+2:]
		}
		return found
	}
	got := fields(&Error{Code: "23514", Message: `new row for relation "notes" violates check constraint "notes_text_check"`,
		Schema: "public", Table: "notes", Constraint: "notes_text_check"})
	if got['C'] != "23514" || got['s'] != "public" || got['t'] != "notes" || got['n'] != "notes_text_check" {
		t.Fatalf("fields %q, want n notes_text_check", got)
	}
	for _, err := range []error{NewError("42601", "syntax error"), errors.New("plain")} {
		if got := fields(err); got['s'] != "" || got['t'] != "" || got['n'] != "" || got['M'] == "" {
			t.Fatalf("%v: fields %q, want no s, t or n", err, got)
		}
	}
}
