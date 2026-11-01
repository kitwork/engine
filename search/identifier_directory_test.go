package search

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestIdentifierDirectoryMatchesIdentifierIndex(t *testing.T) {
	ctx := context.Background()
	schema := productSchema(t)
	directory := t.TempDir()
	var documents []Document
	for number := 0; number < 1000; number++ {
		documents = append(documents, Document{ID: fmt.Sprintf("item-%d", number*7), Fields: map[string]string{"title": "áo"}})
	}
	writer := writeLiveIndex(t, directory, schema, documents, 1<<20)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	index, err := OpenIndex(directory, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	entry := index.entries[0]
	lookup, err := openIdentifierIndex(filepath.Join(directory, entry.identifierName), schema.fingerprint, entry.documents, entry.identifierBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer lookup.Close()
	loaded, err := loadIdentifierDirectory(ctx, lookup)
	if err != nil {
		t.Fatal(err)
	}
	segment := index.segments[0]
	absent := 0
	for number := 0; number < 7000; number++ {
		identifier := fmt.Sprintf("item-%d", number)
		gotDocument, gotFound, err := loaded.Find(ctx, segment, identifier)
		if err != nil {
			t.Fatal(err)
		}
		wantDocument, wantFound, err := lookup.Find(ctx, segment, identifier)
		if err != nil {
			t.Fatal(err)
		}
		if gotFound != wantFound || gotDocument != wantDocument {
			t.Fatalf("%s: directory %d %v, identifier index %d %v", identifier, gotDocument, gotFound, wantDocument, wantFound)
		}
		if number%7 == 0 && !gotFound {
			t.Fatalf("%s is in the segment but was not found", identifier)
		}
		if !gotFound && loaded.mayContain(identifierHash(identifier)) {
			absent++
		}
	}
	// 6,000 absent identifiers at 10 bits per document: about 1% pass the filter.
	if absent > 300 {
		t.Fatalf("%d of 6000 absent identifiers passed the Bloom filter", absent)
	}

	path := filepath.Join(directory, entry.identifierName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[identifierIndexHeaderSize+5] ^= 0xff
	corrupt := filepath.Join(t.TempDir(), "corrupt.ki")
	if err := os.WriteFile(corrupt, data, 0o600); err != nil {
		t.Fatal(err)
	}
	damaged, err := openIdentifierIndex(corrupt, schema.fingerprint, entry.documents, entry.identifierBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer damaged.Close()
	if _, err := loadIdentifierDirectory(ctx, damaged); err == nil {
		t.Fatal("a damaged identifier index loaded")
	}
}
