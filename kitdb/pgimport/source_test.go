package pgimport

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestCSVSourceStreamsHeaderQuotedNewlineAndNull(t *testing.T) {
	path := filepath.Join(t.TempDir(), "items.csv")
	contents := "id,title,note\r\n1,\"line one\nline two\",\\N\r\n"
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	source, err := openCSVSource(path, nil, true, ',', `\N`, true)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if got := source.Columns(); !equalColumnNames(got, []string{"id", "title", "note"}) {
		t.Fatalf("CSV columns = %#v", got)
	}
	record, err := source.Next()
	if err != nil {
		t.Fatal(err)
	}
	if record.values[0] != "1" || record.values[1] != "line one\nline two" || record.values[2] != nil {
		t.Fatalf("CSV record = %#v", record.values)
	}
	if _, err := source.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("CSV EOF = %v", err)
	}
	if source.Offset() != uint64(len(contents)) || record.end != uint64(len(contents)) {
		t.Fatalf("CSV offsets = record:%d source:%d want:%d", record.end, source.Offset(), len(contents))
	}
}

func TestJSONLSourceCanonicalValuesAndOffsets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "items.jsonl")
	contents := "\n{\"id\":\"one\",\"price\":12,\"active\":true,\"meta\":{\"b\":2,\"a\":1}}\n" +
		"{\"id\":\"two\",\"active\":false}\n"
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	source, err := openJSONLSource(path, []string{"id", "price", "active", "meta"})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	first, err := source.Next()
	if err != nil {
		t.Fatal(err)
	}
	if first.values[0] != "one" || first.values[1] != "12" || first.values[2] != true ||
		first.values[3] != `{"a":1,"b":2}` {
		t.Fatalf("first JSONL record = %#v", first.values)
	}
	second, err := source.Next()
	if err != nil {
		t.Fatal(err)
	}
	if second.values[0] != "two" || second.values[1] != nil || second.values[2] != false || second.values[3] != nil {
		t.Fatalf("second JSONL record = %#v", second.values)
	}
	if _, err := source.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("JSONL EOF = %v", err)
	}
	if source.Offset() != uint64(len(contents)) || second.end != uint64(len(contents)) {
		t.Fatalf("JSONL offsets = record:%d source:%d want:%d", second.end, source.Offset(), len(contents))
	}
}
