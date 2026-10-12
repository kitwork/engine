package kitsql

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type streamTestBody struct {
	io.Reader
	closed bool
}

func (body *streamTestBody) Close() error { body.closed = true; return nil }

type streamTestTransport func(*http.Request) (*http.Response, error)

func (transport streamTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestStreamClientRejectsMalformedFramesAndClosesBody(t *testing.T) {
	header := `{"version":1,"type":"columns","columns":[{"name":"id"}]}` + "\n"
	row := `{"version":1,"type":"row","row":[{"type":"integer","value":"1"}]}` + "\n"
	for _, test := range []struct {
		name, payload, want string
	}{
		{"missing complete", header + row, "unexpected EOF"},
		{"truncated row", header + `{"version":1,"type":"row"`, "unexpected EOF"},
		{"duplicate columns", header + header, "unexpected stream frame"},
		{"wrong width", header + `{"version":1,"type":"row","row":[]}`, "row width"},
		{"wrong version", header + `{"version":2,"type":"complete"}`, "unsupported stream version"},
		{"wrong count", header + row + `{"version":1,"type":"complete","rows":2}`, "completion frame"},
		{"negative count", header + `{"version":1,"type":"complete","rows":-1}`, "completion frame"},
		{"mixed row", header + `{"version":1,"type":"row","rows":1,"row":[{"type":"integer","value":"1"}]}`, "malformed row frame"},
		{"mixed complete", header + `{"version":1,"type":"complete","error":{"code":"x","message":"x"}}`, "completion frame"},
		{"missing error", header + `{"version":1,"type":"error"}`, "malformed stream error"},
		{"bad value", header + `{"version":1,"type":"row","row":[{"type":"integer","value":"bad"}]}`, "invalid integer"},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := &streamTestBody{Reader: strings.NewReader(test.payload)}
			connection := &connection{connector: &connector{
				endpoint: "https://example.test" + Path,
				client: &http.Client{Transport: streamTestTransport(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {StreamMediaType}}, Body: body}, nil
				})},
			}}
			rows, err := connection.QueryContext(context.Background(), "SELECT id", nil)
			if err == nil {
				for {
					err = rows.Next(make([]driver.Value, 1))
					if err != nil {
						break
					}
				}
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
			if !body.closed {
				t.Fatal("failed stream retained its response body")
			}
		})
	}
}

func TestStreamClientValidCompletionAndEarlyClose(t *testing.T) {
	for _, early := range []bool{false, true} {
		body := &streamTestBody{Reader: strings.NewReader(`{"version":1,"type":"row","row":[{"type":"text","value":"ok"}]}
{"version":1,"type":"complete","rows":1}`)}
		rows := &streamRows{columns: []Column{{Name: "name"}}, decoder: json.NewDecoder(body), body: body}
		values := make([]driver.Value, 1)
		if err := rows.Next(values); err != nil || values[0] != "ok" {
			t.Fatalf("row = %v, %v", values, err)
		}
		if early {
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
		} else if err := rows.Next(values); !errors.Is(err, io.EOF) {
			t.Fatalf("completion = %v", err)
		}
		if !body.closed {
			t.Fatal("body not closed")
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		if err := rows.Next(values); !errors.Is(err, io.EOF) {
			t.Fatalf("closed cursor = %v", err)
		}
	}
}

func TestCompatibilityResponseRejectsTrailingJSON(t *testing.T) {
	_, err := decodeResponse(&http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"version":1} {"version":1}`))})
	if err == nil {
		t.Fatal("accepted trailing response JSON")
	}
}
