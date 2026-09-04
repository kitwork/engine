package search

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestIndexUpdateDeleteAndSnapshotIsolation(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	writer, err := NewIndexWriter(directory, schema, WriterOptions{Segment: BuildOptions{MaxDocuments: 2}})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	for _, document := range []Document{
		{ID: "doc-a", Fields: map[string]string{"text": "alpha common"}},
		{ID: "doc-b", Fields: map[string]string{"text": "beta common common"}},
		{ID: "doc-c", Fields: map[string]string{"text": "gamma common"}},
	} {
		if err := writer.Add(context.Background(), document); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := writer.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	old, err := OpenIndex(directory, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()

	if err := writer.Update(context.Background(), Document{
		ID: "doc-a", Fields: map[string]string{"text": "delta delta common"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Update(context.Background(), Document{ID: "doc-a"}); !errors.Is(err, ErrPendingDocument) {
		t.Fatalf("second pending update error = %v", err)
	}
	deleted, err := writer.Delete(context.Background(), "doc-b")
	if err != nil || !deleted {
		t.Fatalf("delete doc-b = %v, %v", deleted, err)
	}
	deleted, err = writer.Delete(context.Background(), "doc-b")
	if err != nil || deleted {
		t.Fatalf("repeat delete doc-b = %v, %v", deleted, err)
	}
	if err := writer.Update(context.Background(), Document{ID: "missing"}); !errors.Is(err, ErrDocumentNotFound) {
		t.Fatalf("missing update error = %v", err)
	}
	info, err := writer.Commit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Documents != 2 || info.PhysicalDocuments != 4 || info.Deleted != 2 {
		t.Fatalf("mutation commit info = %#v", info)
	}

	for _, query := range []string{"alpha", "beta"} {
		hits, err := old.Search(context.Background(), MatchQuery{Field: "text", Text: query}, SearchOptions{})
		if err != nil || len(hits) != 1 {
			t.Fatalf("old snapshot query %q = %#v, %v", query, hits, err)
		}
	}
	current, err := OpenIndex(directory, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	if err := current.Verify(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"alpha", "beta"} {
		hits, err := current.Search(context.Background(), MatchQuery{Field: "text", Text: query}, SearchOptions{})
		if err != nil || len(hits) != 0 {
			t.Fatalf("new snapshot query %q = %#v, %v", query, hits, err)
		}
	}
	hits, err := current.Search(context.Background(), MatchQuery{Field: "text", Text: "delta common"}, SearchOptions{})
	if err != nil || len(hits) != 1 || hits[0].ID != "doc-a" {
		t.Fatalf("updated query hits = %#v, %v", hits, err)
	}

	rebuilt := buildTestSegment(t, schema, []Document{
		{ID: "doc-a", Fields: map[string]string{"text": "delta delta common"}},
		{ID: "doc-c", Fields: map[string]string{"text": "gamma common"}},
	})
	defer rebuilt.Close()
	want, err := rebuilt.Search(context.Background(), MatchQuery{Field: "text", Text: "delta common"}, SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(want) != 1 || math.Abs(hits[0].Score-want[0].Score) > 1e-12 {
		t.Fatalf("tombstone score = %.15f, rebuilt score = %#v", hits[0].Score, want)
	}
}

func TestIndexSearchAfterPreservesBM25OrderAcrossSegments(t *testing.T) {
	schema, err := NewSchema(
		Text("name", StandardAnalyzer(), Boost(3)),
		Text("description", StandardAnalyzer()),
	)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	writer, err := NewIndexWriter(directory, schema, WriterOptions{
		Segment: BuildOptions{MaxDocuments: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	for index, document := range []Document{
		{ID: "a", Fields: map[string]string{"name": "alpha alpha", "description": "common"}},
		{ID: "b", Fields: map[string]string{"name": "alpha", "description": "common common"}},
		{ID: "c", Fields: map[string]string{"name": "alpha", "description": "common"}},
		{ID: "d", Fields: map[string]string{"name": "common", "description": "alpha"}},
		{ID: "e", Fields: map[string]string{"name": "alpha alpha alpha", "description": "common"}},
		{ID: "f", Fields: map[string]string{"name": "alpha", "description": "common"}},
		{ID: "g", Fields: map[string]string{"name": "alpha", "description": "common common common"}},
	} {
		if err := writer.Add(context.Background(), document); err != nil {
			t.Fatalf("add document %d: %v", index, err)
		}
	}
	if _, err := writer.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	index, err := OpenIndex(directory, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	query := MatchQuery{Fields: []string{"name", "description"}, Text: "alpha common"}
	want, err := index.Search(context.Background(), query, SearchOptions{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var got []Hit
	var after *SearchAfter
	for {
		page, err := index.Search(context.Background(), query, SearchOptions{Limit: 2, After: after})
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		got = append(got, page...)
		last := page[len(page)-1]
		after = &SearchAfter{Score: last.Score, Ordinal: last.Ordinal}
	}
	if len(got) != len(want) {
		t.Fatalf("paged hits = %d, want %d", len(got), len(want))
	}
	for position := range want {
		if got[position].ID != want[position].ID || got[position].Score != want[position].Score ||
			got[position].Ordinal != want[position].Ordinal {
			t.Fatalf("paged hit %d = %#v, want %#v", position, got[position], want[position])
		}
	}
	if _, err := index.Search(context.Background(), MatchQuery{
		Fields: []string{"name", "description"}, Text: "alpha", Operator: QueryAny,
	}, SearchOptions{After: &SearchAfter{}}); err == nil {
		t.Fatal("QueryAny SEARCH AFTER unexpectedly succeeded")
	}
}

func TestIndexUpsertIsReplaySafe(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	writer, err := NewIndexWriter(directory, schema, WriterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	if err := writer.Upsert(context.Background(), Document{
		ID: "product/1", Fields: map[string]string{"text": "old keyboard"},
	}); err != nil {
		t.Fatal(err)
	}
	if info, err := writer.Commit(context.Background()); err != nil || info.Documents != 1 {
		t.Fatalf("initial upsert commit = %#v, %v", info, err)
	}

	updated := Document{ID: "product/1", Fields: map[string]string{"text": "new mechanical keyboard"}}
	for replay := 0; replay < 2; replay++ {
		if err := writer.Upsert(context.Background(), updated); err != nil {
			t.Fatalf("upsert replay %d: %v", replay, err)
		}
		info, err := writer.Commit(context.Background())
		if err != nil {
			t.Fatalf("commit replay %d: %v", replay, err)
		}
		if info.Documents != 1 {
			t.Fatalf("documents after replay %d = %d, want 1", replay, info.Documents)
		}
	}

	index, err := OpenIndex(directory, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	if err := index.Verify(context.Background()); err != nil {
		t.Fatal(err)
	}
	oldHits, err := index.Search(context.Background(), MatchQuery{
		Field: "text", Text: "old",
	}, SearchOptions{})
	if err != nil || len(oldHits) != 0 {
		t.Fatalf("old hits after replay = %#v, %v", oldHits, err)
	}
	newHits, err := index.Search(context.Background(), MatchQuery{
		Field: "text", Text: "mechanical keyboard",
	}, SearchOptions{})
	if err != nil || len(newHits) != 1 || newHits[0].ID != updated.ID {
		t.Fatalf("new hits after replay = %#v, %v", newHits, err)
	}
}

func TestIndexSearchAnyMergesSegments(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	writer, err := NewIndexWriter(directory, schema, WriterOptions{Segment: BuildOptions{MaxDocuments: 1}})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	for _, document := range []Document{
		{ID: "doc-a", Fields: map[string]string{"text": "alpha"}},
		{ID: "doc-b", Fields: map[string]string{"text": "beta"}},
		{ID: "doc-c", Fields: map[string]string{"text": "alpha beta"}},
	} {
		if err := writer.Add(context.Background(), document); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Commit(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	index, err := OpenIndex(directory, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()

	hits, err := index.Search(context.Background(), MatchQuery{
		Field: "text", Text: "alpha beta", Operator: QueryAny,
	}, SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 3 || hits[0].ID != "doc-c" {
		t.Fatalf("hits = %#v", hits)
	}
}

func TestIndexSearchPrefixMergesSegments(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	writer, err := NewIndexWriter(directory, schema, WriterOptions{Segment: BuildOptions{MaxDocuments: 1}})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	for _, document := range []Document{
		{ID: "doc-a", Fields: map[string]string{"text": "alpha"}},
		{ID: "doc-b", Fields: map[string]string{"text": "alphabet"}},
		{ID: "doc-c", Fields: map[string]string{"text": "alpine"}},
		{ID: "doc-d", Fields: map[string]string{"text": "beta"}},
	} {
		if err := writer.Add(context.Background(), document); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Commit(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	index, err := OpenIndex(directory, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()

	hits, err := index.Search(context.Background(), MatchQuery{
		Field: "text", Prefix: "alp",
	}, SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 3 || hits[0].ID != "doc-a" || hits[1].ID != "doc-b" || hits[2].ID != "doc-c" {
		t.Fatalf("hits = %#v", hits)
	}
}

func TestIndexRejectsCorruptIdentifierAndDeletionSidecars(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	writer, err := NewIndexWriter(directory, schema, WriterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Add(context.Background(), Document{ID: "doc", Fields: map[string]string{"text": "alpha"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	identifiers, err := filepath.Glob(filepath.Join(directory, "*.ki"))
	if err != nil || len(identifiers) != 1 {
		t.Fatalf("identifier sidecars = %#v, %v", identifiers, err)
	}
	corruptByte(t, identifiers[0], identifierIndexHeaderSize)
	if err := writer.Update(context.Background(), Document{ID: "doc", Fields: map[string]string{"text": "beta"}}); !errors.Is(err, ErrCorruptSegment) && !errors.Is(err, ErrCorruptIndex) {
		t.Fatalf("corrupt identifier update error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	directory = t.TempDir()
	writer, err = NewIndexWriter(directory, schema, WriterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Add(context.Background(), Document{ID: "doc", Fields: map[string]string{"text": "alpha"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if deleted, err := writer.Delete(context.Background(), "doc"); err != nil || !deleted {
		t.Fatalf("delete = %v, %v", deleted, err)
	}
	if _, err := writer.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	deletions, err := filepath.Glob(filepath.Join(directory, "*.kd"))
	if err != nil || len(deletions) != 1 {
		t.Fatalf("deletion sidecars = %#v, %v", deletions, err)
	}
	corruptByte(t, deletions[0], deletionHeaderSize)
	if _, err := OpenIndex(directory, schema); !errors.Is(err, ErrCorruptIndex) {
		t.Fatalf("corrupt deletion open error = %v", err)
	}
}

func TestIndexReadsLegacyManifestAndWritesVersionTwo(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	builder, err := NewBuilder(schema, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	builder.version = segmentVersionV1
	if err := builder.Add(Document{ID: "legacy", Fields: map[string]string{"text": "legacy alpha"}}); err != nil {
		t.Fatal(err)
	}
	segmentName := "segment-legacy.ks"
	segmentInfo, err := builder.Write(context.Background(), filepath.Join(directory, segmentName))
	if err != nil {
		t.Fatal(err)
	}
	legacy := marshalLegacyManifestForTest(t, indexManifest{
		generation: 1, schemaHash: schema.fingerprint,
		segments: []manifestSegment{{name: segmentName, documents: 1, bytes: uint64(segmentInfo.Bytes)}},
	})
	if err := os.WriteFile(filepath.Join(directory, manifestFilename(1)), legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	index, err := OpenIndex(directory, schema)
	if err != nil {
		t.Fatal(err)
	}
	if hits, err := index.Search(context.Background(), MatchQuery{Field: "text", Text: "legacy"}, SearchOptions{}); err != nil || len(hits) != 1 {
		t.Fatalf("legacy hits = %#v, %v", hits, err)
	}
	if err := index.Close(); err != nil {
		t.Fatal(err)
	}
	writer, err := NewIndexWriter(directory, schema, WriterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Add(context.Background(), Document{ID: "modern", Fields: map[string]string{"text": "modern beta"}}); err != nil {
		t.Fatal(err)
	}
	info, err := writer.Commit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	info, merged, err := writer.Compact(context.Background(), CompactOptions{Force: true})
	if err != nil || !merged {
		t.Fatalf("compact legacy segment = %#v, %v, %v", info, merged, err)
	}
	if len(writer.committed.segments) != 1 {
		t.Fatalf("compacted segment count = %d", len(writer.committed.segments))
	}
	upgraded, err := OpenSegment(filepath.Join(directory, writer.committed.segments[0].name), schema)
	if err != nil {
		t.Fatal(err)
	}
	if upgraded.header.version != segmentVersion {
		_ = upgraded.Close()
		t.Fatalf("compacted segment version = %d, want %d", upgraded.header.version, segmentVersion)
	}
	if err := upgraded.Close(); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(info.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if version := binary.LittleEndian.Uint32(data[8:12]); version != manifestVersion {
		t.Fatalf("committed manifest version = %d", version)
	}
}

func TestIndexCompactPreservesSearchAndPurgesTombstones(t *testing.T) {
	schema, err := NewSchema(
		Text("title", StandardAnalyzer()),
		Text("description", StandardAnalyzer(), Boost(0.5)),
	)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	writer, err := NewIndexWriter(directory, schema, WriterOptions{Segment: BuildOptions{MaxDocuments: 2}})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	for document := 0; document < 6; document++ {
		if err := writer.Add(context.Background(), Document{
			ID: fmt.Sprintf("doc-%d", document),
			Fields: map[string]string{
				"title":       fmt.Sprintf("common product %d", document),
				"description": "cotton common durable",
			},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := writer.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := writer.Update(context.Background(), Document{
		ID: "doc-1", Fields: map[string]string{
			"title": "common updated product", "description": "cotton premium",
		},
	}); err != nil {
		t.Fatal(err)
	}
	if deleted, err := writer.Delete(context.Background(), "doc-2"); err != nil || !deleted {
		t.Fatalf("delete doc-2 = %v, %v", deleted, err)
	}
	beforeInfo, err := writer.Commit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if beforeInfo.Documents != 5 || beforeInfo.PhysicalDocuments != 7 || beforeInfo.Deleted != 2 {
		t.Fatalf("before compact info = %#v", beforeInfo)
	}
	before, err := OpenIndex(directory, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer before.Close()
	want, err := before.Search(context.Background(), MatchQuery{Field: "title", Text: "common product"}, SearchOptions{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}

	afterInfo, merged, err := writer.Compact(context.Background(), CompactOptions{
		Force: true, MaximumSegments: 1, MaximumInputSegments: 8, MaximumInputDocuments: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !merged || afterInfo.Segments != 1 || afterInfo.Documents != 5 ||
		afterInfo.PhysicalDocuments != 5 || afterInfo.Deleted != 0 {
		t.Fatalf("after compact info = %#v, merged = %v", afterInfo, merged)
	}
	after, err := OpenIndex(directory, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer after.Close()
	if err := after.Verify(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := after.Search(context.Background(), MatchQuery{Field: "title", Text: "common product"}, SearchOptions{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	assertHitScoresEqual(t, got, want)
	if hits, err := after.Search(context.Background(), MatchQuery{Field: "title", Text: "updated"}, SearchOptions{}); err != nil || len(hits) != 1 || hits[0].ID != "doc-1" {
		t.Fatalf("compacted update hits = %#v, %v", hits, err)
	}
	if hits, err := after.Search(context.Background(), MatchQuery{Field: "title", Text: "2"}, SearchOptions{}); err != nil || len(hits) != 0 {
		t.Fatalf("compacted deleted hits = %#v, %v", hits, err)
	}
	if hits, err := before.Search(context.Background(), MatchQuery{Field: "title", Text: "updated"}, SearchOptions{}); err != nil || len(hits) != 1 {
		t.Fatalf("pre-compact snapshot hits = %#v, %v", hits, err)
	}
}

func TestIndexCompactCanRemoveFullyDeletedSegment(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	writer, err := NewIndexWriter(directory, schema, WriterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := writer.Add(context.Background(), Document{ID: "only", Fields: map[string]string{"text": "alpha"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if deleted, err := writer.Delete(context.Background(), "only"); err != nil || !deleted {
		t.Fatalf("delete only = %v, %v", deleted, err)
	}
	if _, err := writer.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	info, merged, err := writer.Compact(context.Background(), CompactOptions{Force: true, MaximumInputDocuments: 10})
	if err != nil {
		t.Fatal(err)
	}
	if !merged || info.Segments != 0 || info.Documents != 0 || info.PhysicalDocuments != 0 || info.Deleted != 0 {
		t.Fatalf("empty compact info = %#v, merged = %v", info, merged)
	}
	index, err := OpenIndex(directory, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	if hits, err := index.Search(context.Background(), MatchQuery{Field: "text", Text: "alpha"}, SearchOptions{}); err != nil || len(hits) != 0 {
		t.Fatalf("empty compact hits = %#v, %v", hits, err)
	}
}

func TestIndexGarbageCollectionHonorsGenerationDrain(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	writer, err := NewIndexWriter(directory, schema, WriterOptions{Segment: BuildOptions{MaxDocuments: 1}})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	for _, document := range []Document{
		{ID: "doc-a", Fields: map[string]string{"text": "alpha common"}},
		{ID: "doc-b", Fields: map[string]string{"text": "beta common"}},
	} {
		if err := writer.Add(context.Background(), document); err != nil {
			t.Fatal(err)
		}
	}
	first, err := writer.Commit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Update(context.Background(), Document{ID: "doc-a", Fields: map[string]string{"text": "alpha updated"}}); err != nil {
		t.Fatal(err)
	}
	second, err := writer.Commit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	third, merged, err := writer.Compact(context.Background(), CompactOptions{
		Force: true, MaximumInputSegments: 8, MaximumInputDocuments: 100,
	})
	if err != nil || !merged {
		t.Fatalf("compact = %#v, %v, %v", third, merged, err)
	}

	orphan := filepath.Join(directory, "segment-orphan.ks")
	temporary := filepath.Join(directory, "segment-incomplete.ks.tmp-0123456789abcdef")
	unknown := filepath.Join(directory, "operator-notes.txt")
	for _, path := range []string{orphan, temporary, unknown} {
		if err := os.WriteFile(path, []byte("garbage"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	safe, err := writer.GarbageCollect(context.Background(), GarbageCollectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if safe.RemovedManifests != 0 || safe.RemovedSegments != 1 || safe.RemovedTemporary != 1 {
		t.Fatalf("safe garbage collection = %#v", safe)
	}
	for _, manifest := range []string{first.Manifest, second.Manifest, third.Manifest} {
		if _, err := os.Stat(manifest); err != nil {
			t.Fatalf("safe collection removed manifest %s: %v", manifest, err)
		}
	}
	if _, err := os.Stat(unknown); err != nil {
		t.Fatalf("safe collection removed unknown file: %v", err)
	}

	pruned, err := writer.GarbageCollect(context.Background(), GarbageCollectOptions{
		OldestRetainedGeneration: third.Generation,
	})
	if err != nil {
		t.Fatal(err)
	}
	if pruned.RemovedManifests != 2 || pruned.RemovedSegments < 1 || pruned.RemovedSidecars < 1 || pruned.ReclaimedBytes <= 0 {
		t.Fatalf("pruned garbage collection = %#v", pruned)
	}
	for _, manifest := range []string{first.Manifest, second.Manifest} {
		if _, err := os.Stat(manifest); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("obsolete manifest still exists %s: %v", manifest, err)
		}
	}
	if _, err := os.Stat(third.Manifest); err != nil {
		t.Fatalf("latest manifest missing: %v", err)
	}
	index, err := OpenIndex(directory, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	if err := index.Verify(context.Background()); err != nil {
		t.Fatal(err)
	}
	hits, err := index.Search(context.Background(), MatchQuery{Field: "text", Text: "updated"}, SearchOptions{})
	if err != nil || len(hits) != 1 || hits[0].ID != "doc-a" {
		t.Fatalf("post-GC hits = %#v, %v", hits, err)
	}
	if _, err := writer.GarbageCollect(context.Background(), GarbageCollectOptions{
		OldestRetainedGeneration: third.Generation + 1,
	}); err == nil {
		t.Fatal("garbage collection accepted a future generation")
	}
}

func TestIndexWriterFlushCommitAndSnapshotIsolation(t *testing.T) {
	schema, err := NewSchema(Text("text", VietnameseAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	if _, err := OpenIndex(directory, schema); !errors.Is(err, ErrIndexNotFound) {
		t.Fatalf("open uncommitted index error = %v", err)
	}

	writer, err := NewIndexWriter(directory, schema, WriterOptions{
		Segment: BuildOptions{MaxDocuments: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	documents := []Document{
		{ID: "product-0", Fields: map[string]string{"text": "ao thun cotton"}},
		{ID: "product-1", Fields: map[string]string{"text": "ao khoac"}},
		{ID: "product-2", Fields: map[string]string{"text": "quan cotton"}},
		{ID: "product-3", Fields: map[string]string{"text": "giay nam"}},
		{ID: "product-4", Fields: map[string]string{"text": "ao cotton nam"}},
	}
	for _, document := range documents {
		if err := writer.Add(context.Background(), document); err != nil {
			t.Fatal(err)
		}
	}
	firstInfo, err := writer.Commit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if firstInfo.Generation != 1 || firstInfo.Segments != 3 || firstInfo.Documents != uint64(len(documents)) || firstInfo.Bytes <= 0 {
		t.Fatalf("first commit info = %#v", firstInfo)
	}

	first, err := OpenIndex(directory, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if err := first.Verify(context.Background()); err != nil {
		t.Fatal(err)
	}
	hits, err := first.Search(context.Background(), MatchQuery{Field: "text", Text: "ao cotton"}, SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].ID != "product-0" || hits[1].ID != "product-4" {
		t.Fatalf("first snapshot hits = %#v", hits)
	}

	if err := writer.Add(context.Background(), Document{
		ID: "product-5", Fields: map[string]string{"text": "snapshot moi"},
	}); err != nil {
		t.Fatal(err)
	}
	secondInfo, err := writer.Commit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if secondInfo.Generation != 2 || secondInfo.Segments != 4 || secondInfo.Documents != 6 {
		t.Fatalf("second commit info = %#v", secondInfo)
	}
	if hits, err := first.Search(context.Background(), MatchQuery{Field: "text", Text: "snapshot"}, SearchOptions{}); err != nil || len(hits) != 0 {
		t.Fatalf("old snapshot hits = %#v, err = %v", hits, err)
	}

	second, err := OpenIndex(directory, schema)
	if err != nil {
		t.Fatal(err)
	}
	hits, err = second.Search(context.Background(), MatchQuery{Field: "text", Text: "snapshot"}, SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ID != "product-5" || hits[0].Ordinal != 5 {
		t.Fatalf("new snapshot hits = %#v", hits)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}

	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	resumed, err := NewIndexWriter(directory, schema, WriterOptions{Segment: BuildOptions{MaxDocuments: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if err := resumed.Add(context.Background(), Document{
		ID: "product-6", Fields: map[string]string{"text": "resumed writer"},
	}); err != nil {
		t.Fatal(err)
	}
	thirdInfo, err := resumed.Commit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := resumed.Close(); err != nil {
		t.Fatal(err)
	}
	if thirdInfo.Generation != 3 || thirdInfo.Documents != 7 {
		t.Fatalf("resumed commit info = %#v", thirdInfo)
	}
}

func TestIndexWriterReplaceAllKeepsOldSnapshots(t *testing.T) {
	schema, err := NewSchema(Text("text", VietnameseAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	writer, err := NewIndexWriter(directory, schema, WriterOptions{
		Segment: BuildOptions{MaxDocuments: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	for _, document := range []Document{
		{ID: "old-a", Fields: map[string]string{"text": "du lieu cu alpha"}},
		{ID: "old-b", Fields: map[string]string{"text": "du lieu cu beta"}},
	} {
		if err := writer.Add(context.Background(), document); err != nil {
			t.Fatal(err)
		}
	}
	base, err := writer.Commit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	old, err := OpenIndex(directory, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()

	replaced, err := writer.ReplaceAll(context.Background(), []Document{
		{ID: "new-a", Fields: map[string]string{"text": "du lieu moi gamma"}},
		{ID: "new-b", Fields: map[string]string{"text": "du lieu moi delta"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if replaced.Generation != base.Generation+1 || replaced.Segments != 2 ||
		replaced.Documents != 2 || replaced.PhysicalDocuments != 2 || replaced.Deleted != 0 {
		t.Fatalf("replacement info = %#v", replaced)
	}
	if hits, err := old.Search(context.Background(), MatchQuery{Field: "text", Text: "cu"}, SearchOptions{}); err != nil || len(hits) != 2 {
		t.Fatalf("old snapshot old hits = %#v, %v", hits, err)
	}
	if hits, err := old.Search(context.Background(), MatchQuery{Field: "text", Text: "moi"}, SearchOptions{}); err != nil || len(hits) != 0 {
		t.Fatalf("old snapshot replacement hits = %#v, %v", hits, err)
	}

	current, err := OpenIndex(directory, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	if hits, err := current.Search(context.Background(), MatchQuery{Field: "text", Text: "cu"}, SearchOptions{}); err != nil || len(hits) != 0 {
		t.Fatalf("replacement old hits = %#v, %v", hits, err)
	}
	if hits, err := current.Search(context.Background(), MatchQuery{Field: "text", Text: "moi"}, SearchOptions{}); err != nil || len(hits) != 2 || hits[0].ID != "new-a" || hits[1].ID != "new-b" {
		t.Fatalf("replacement new hits = %#v, %v", hits, err)
	}

	empty, err := writer.ReplaceAll(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if empty.Generation != replaced.Generation+1 || empty.Segments != 0 || empty.Documents != 0 || empty.Bytes != 0 {
		t.Fatalf("empty replacement info = %#v", empty)
	}
	if hits, err := current.Search(context.Background(), MatchQuery{Field: "text", Text: "moi"}, SearchOptions{}); err != nil || len(hits) != 2 {
		t.Fatalf("pre-empty snapshot hits = %#v, %v", hits, err)
	}
	latest, err := OpenIndex(directory, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer latest.Close()
	if hits, err := latest.Search(context.Background(), MatchQuery{Field: "text", Text: "moi"}, SearchOptions{}); err != nil || len(hits) != 0 {
		t.Fatalf("empty replacement hits = %#v, %v", hits, err)
	}
}

func TestIndexWriterReplaceAllRollsBackFailedBuild(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	writer, err := NewIndexWriter(directory, schema, WriterOptions{
		Segment: BuildOptions{MaxDocuments: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := writer.Add(context.Background(), Document{ID: "stable", Fields: map[string]string{"text": "stable source"}}); err != nil {
		t.Fatal(err)
	}
	base, err := writer.Commit(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	_, err = writer.ReplaceAll(context.Background(), []Document{
		{ID: "temporary", Fields: map[string]string{"text": "temporary replacement"}},
		{ID: "", Fields: map[string]string{"text": "invalid replacement"}},
	})
	if err == nil {
		t.Fatal("invalid replacement was accepted")
	}
	if writer.committed.generation != base.Generation || writer.hasPendingChanges() || writer.replacement != nil {
		t.Fatalf("failed replacement leaked state: generation=%d pending=%t replacement=%t", writer.committed.generation, writer.hasPendingChanges(), writer.replacement != nil)
	}
	segments, err := filepath.Glob(filepath.Join(directory, "*.ks"))
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 1 {
		t.Fatalf("failed replacement left segment artifacts: %#v", segments)
	}
	if hits, err := writer.snapshot.Search(context.Background(), MatchQuery{Field: "text", Text: "stable"}, SearchOptions{}); err != nil || len(hits) != 1 || hits[0].ID != "stable" {
		t.Fatalf("stable snapshot after failed replacement = %#v, %v", hits, err)
	}
	if hits, err := writer.snapshot.Search(context.Background(), MatchQuery{Field: "text", Text: "temporary"}, SearchOptions{}); err != nil || len(hits) != 0 {
		t.Fatalf("temporary replacement became visible = %#v, %v", hits, err)
	}

	if err := writer.Add(context.Background(), Document{ID: "after", Fields: map[string]string{"text": "writer recovered"}}); err != nil {
		t.Fatal(err)
	}
	info, err := writer.Commit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Generation != base.Generation+1 || info.Documents != 2 {
		t.Fatalf("post-rollback commit info = %#v", info)
	}
}

func TestIndexWriterReplaceAllRejectsPendingChangesAndDuplicateIDs(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	writer, err := NewIndexWriter(t.TempDir(), schema, WriterOptions{Segment: BuildOptions{MaxDocuments: 1}})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := writer.Add(context.Background(), Document{ID: "pending", Fields: map[string]string{"text": "pending source"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.ReplaceAll(context.Background(), []Document{{ID: "replacement"}}); !errors.Is(err, ErrPendingChanges) {
		t.Fatalf("replacement with pending changes error = %v", err)
	}
	if _, err := writer.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}

	_, err = writer.ReplaceAll(context.Background(), []Document{
		{ID: "duplicate", Fields: map[string]string{"text": "first"}},
		{ID: "duplicate", Fields: map[string]string{"text": "second"}},
	})
	if !errors.Is(err, ErrPendingDocument) {
		t.Fatalf("duplicate replacement error = %v", err)
	}
	if hits, err := writer.snapshot.Search(context.Background(), MatchQuery{Field: "text", Text: "pending"}, SearchOptions{}); err != nil || len(hits) != 1 || hits[0].ID != "pending" {
		t.Fatalf("committed state after duplicate replacement = %#v, %v", hits, err)
	}
}

func TestIndexWriterStreamingReplacementPublishesWithBoundedPendingIDs(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	writer, err := NewIndexWriter(directory, schema, WriterOptions{
		Segment: BuildOptions{MaxDocuments: 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := writer.Add(context.Background(), Document{ID: "old", Fields: map[string]string{"text": "old snapshot"}}); err != nil {
		t.Fatal(err)
	}
	base, err := writer.Commit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	old, err := OpenIndex(directory, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()

	replacement, err := writer.BeginReplacement()
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Abort()
	for document := 0; document < 100; document++ {
		if err := replacement.Add(context.Background(), Document{
			ID: fmt.Sprintf("new-%03d", document),
			Fields: map[string]string{
				"text": fmt.Sprintf("replacement common token-%03d", document),
			},
		}); err != nil {
			t.Fatal(err)
		}
		if len(writer.pendingIDs) != 0 {
			t.Fatalf("streaming replacement retained %d global pending identifiers", len(writer.pendingIDs))
		}
		if writer.builder.DocumentCount() > 3 || len(writer.builder.identifierSet) > 3 {
			t.Fatalf("replacement builder escaped its bound: documents=%d identifiers=%d", writer.builder.DocumentCount(), len(writer.builder.identifierSet))
		}
	}
	beforePublish, err := OpenIndex(directory, schema)
	if err != nil {
		t.Fatal(err)
	}
	if hits, err := beforePublish.Search(context.Background(), MatchQuery{Field: "text", Text: "replacement"}, SearchOptions{}); err != nil || len(hits) != 0 {
		_ = beforePublish.Close()
		t.Fatalf("uncommitted replacement became visible = %#v, %v", hits, err)
	}
	if err := beforePublish.Close(); err != nil {
		t.Fatal(err)
	}

	info, err := replacement.Commit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Generation != base.Generation+1 || info.Documents != 100 || info.PhysicalDocuments != 100 || info.Segments != 34 {
		t.Fatalf("streaming replacement info = %#v", info)
	}
	if writer.replacement != nil || replacement.active || replacement.writer != nil {
		t.Fatal("committed replacement still owns the writer")
	}
	if err := replacement.Add(context.Background(), Document{ID: "closed"}); !errors.Is(err, ErrClosed) {
		t.Fatalf("committed replacement add error = %v", err)
	}
	if hits, err := old.Search(context.Background(), MatchQuery{Field: "text", Text: "old"}, SearchOptions{}); err != nil || len(hits) != 1 {
		t.Fatalf("old snapshot after streaming replacement = %#v, %v", hits, err)
	}
	current, err := OpenIndex(directory, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	if hits, err := current.Search(context.Background(), MatchQuery{Field: "text", Text: "replacement"}, SearchOptions{Limit: 20}); err != nil || len(hits) != 20 {
		t.Fatalf("streaming replacement hits = %d, %v", len(hits), err)
	}
}

func TestIndexWriterStreamingReplacementCompactsBeyondManifestSegmentLimit(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	writer, err := NewIndexWriter(directory, schema, WriterOptions{
		Segment: BuildOptions{MaxDocuments: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	replacement, err := writer.BeginReplacement()
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Abort()
	documentCount := manifestMaximumSegments + 44
	for document := 0; document < documentCount; document++ {
		if err := replacement.Add(context.Background(), Document{
			ID: fmt.Sprintf("document-%03d", document),
			Fields: map[string]string{
				"text": fmt.Sprintf("common token-%03d", document),
			},
		}); err != nil {
			t.Fatalf("add document %d: %v", document, err)
		}
	}
	info, err := replacement.Commit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Documents != uint64(documentCount) || info.Segments > replacementCompactionFinalTargetSegments {
		t.Fatalf("compacted replacement info = %#v", info)
	}

	index, err := OpenIndex(directory, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	if err := index.Verify(context.Background()); err != nil {
		t.Fatal(err)
	}
	hits, err := index.Search(context.Background(), MatchQuery{Field: "text", Text: "common"}, SearchOptions{Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 200 {
		t.Fatalf("compacted replacement hits = %d, want 200", len(hits))
	}
	segments, err := filepath.Glob(filepath.Join(directory, "*.ks"))
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != info.Segments {
		t.Fatalf("segment artifacts = %d, manifest segments = %d", len(segments), info.Segments)
	}
}

func TestIndexWriterStreamingReplacementDetectsDuplicateAfterPendingMerge(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	writer, err := NewIndexWriter(t.TempDir(), schema, WriterOptions{
		Segment: BuildOptions{MaxDocuments: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	replacement, err := writer.BeginReplacement()
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Abort()
	for _, document := range []Document{
		{ID: "duplicate", Fields: map[string]string{"text": "first"}},
		{ID: "duplicate", Fields: map[string]string{"text": "second"}},
	} {
		if err := replacement.Add(context.Background(), document); err != nil {
			t.Fatal(err)
		}
	}
	if err := replacement.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := writer.compactPendingReplacement(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if len(writer.pending) != 1 {
		t.Fatalf("pending segments after merge = %d", len(writer.pending))
	}
	if _, err := replacement.Commit(context.Background()); !errors.Is(err, ErrPendingDocument) {
		t.Fatalf("merged duplicate commit error = %v", err)
	}
}

func TestIndexWriterStreamingReplacementRetryAbortAndDuplicateValidation(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	writer, err := NewIndexWriter(directory, schema, WriterOptions{
		Segment: BuildOptions{MaxDocuments: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := writer.Add(context.Background(), Document{ID: "stable", Fields: map[string]string{"text": "stable source"}}); err != nil {
		t.Fatal(err)
	}
	base, err := writer.Commit(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	replacement, err := writer.BeginReplacement()
	if err != nil {
		t.Fatal(err)
	}
	for _, document := range []Document{
		{ID: "first", Fields: map[string]string{"text": "replacement first"}},
		{ID: "second", Fields: map[string]string{"text": "replacement second"}},
		{ID: "third", Fields: map[string]string{"text": "replacement third"}},
	} {
		if err := replacement.Add(context.Background(), document); err != nil {
			t.Fatal(err)
		}
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := replacement.Commit(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled replacement commit error = %v", err)
	}
	if !replacement.active || writer.replacement != replacement {
		t.Fatal("pre-publication cancellation closed the replacement")
	}
	info, err := replacement.Commit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Generation != base.Generation+1 || info.Documents != 3 {
		t.Fatalf("retried replacement info = %#v", info)
	}

	duplicate, err := writer.BeginReplacement()
	if err != nil {
		t.Fatal(err)
	}
	for _, document := range []Document{
		{ID: "duplicate", Fields: map[string]string{"text": "first segment"}},
		{ID: "other", Fields: map[string]string{"text": "first segment"}},
		{ID: "third", Fields: map[string]string{"text": "second segment"}},
		{ID: "duplicate", Fields: map[string]string{"text": "second segment"}},
	} {
		if err := duplicate.Add(context.Background(), document); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := duplicate.Commit(context.Background()); !errors.Is(err, ErrPendingDocument) {
		t.Fatalf("cross-segment duplicate commit error = %v", err)
	}
	if !duplicate.active || writer.replacement != duplicate {
		t.Fatal("duplicate validation closed a pre-publication replacement")
	}
	if err := duplicate.Abort(); err != nil {
		t.Fatal(err)
	}
	if err := duplicate.Abort(); err != nil {
		t.Fatalf("idempotent replacement abort = %v", err)
	}
	if writer.replacement != nil || writer.hasPendingChanges() {
		t.Fatal("aborted replacement retained writer state")
	}
	current, err := OpenIndex(directory, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	if current.Info().Generation != info.Generation {
		t.Fatalf("failed replacement changed generation to %d, want %d", current.Info().Generation, info.Generation)
	}
	if hits, err := current.Search(context.Background(), MatchQuery{Field: "text", Text: "replacement"}, SearchOptions{}); err != nil || len(hits) != 3 {
		t.Fatalf("committed replacement after aborted duplicate = %#v, %v", hits, err)
	}
}

func TestStreamingReplacementExclusivelyOwnsWriter(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	writer, err := NewIndexWriter(t.TempDir(), schema, WriterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := writer.Add(context.Background(), Document{ID: "committed", Fields: map[string]string{"text": "committed"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	replacement, err := writer.BeginReplacement()
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Abort()

	if _, err := writer.BeginReplacement(); !errors.Is(err, ErrReplacementActive) {
		t.Fatalf("second replacement error = %v", err)
	}
	if err := writer.Add(context.Background(), Document{ID: "direct"}); !errors.Is(err, ErrReplacementActive) {
		t.Fatalf("direct add error = %v", err)
	}
	if err := writer.Update(context.Background(), Document{ID: "committed"}); !errors.Is(err, ErrReplacementActive) {
		t.Fatalf("direct update error = %v", err)
	}
	if _, err := writer.Delete(context.Background(), "committed"); !errors.Is(err, ErrReplacementActive) {
		t.Fatalf("direct delete error = %v", err)
	}
	if err := writer.Flush(context.Background()); !errors.Is(err, ErrReplacementActive) {
		t.Fatalf("direct flush error = %v", err)
	}
	if _, err := writer.Commit(context.Background()); !errors.Is(err, ErrReplacementActive) {
		t.Fatalf("direct commit error = %v", err)
	}
	if _, _, err := writer.Compact(context.Background(), CompactOptions{Force: true}); !errors.Is(err, ErrReplacementActive) {
		t.Fatalf("direct compact error = %v", err)
	}
	if _, err := writer.GarbageCollect(context.Background(), GarbageCollectOptions{}); !errors.Is(err, ErrReplacementActive) {
		t.Fatalf("direct garbage collection error = %v", err)
	}
	if err := replacement.Abort(); err != nil {
		t.Fatal(err)
	}
	if err := writer.Add(context.Background(), Document{ID: "after", Fields: map[string]string{"text": "after abort"}}); err != nil {
		t.Fatalf("writer did not recover after abort: %v", err)
	}
}

func TestStreamingReplacementRejectsCorruptPendingIdentifierIndex(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	writer, err := NewIndexWriter(directory, schema, WriterOptions{Segment: BuildOptions{MaxDocuments: 2}})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	replacement, err := writer.BeginReplacement()
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Abort()
	for _, identifier := range []string{"one", "two", "three", "four"} {
		if err := replacement.Add(context.Background(), Document{
			ID: identifier, Fields: map[string]string{"text": identifier},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := replacement.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	identifiers, err := filepath.Glob(filepath.Join(directory, "*.ki"))
	if err != nil || len(identifiers) != 2 {
		t.Fatalf("pending identifier indexes = %#v, %v", identifiers, err)
	}
	corruptByte(t, identifiers[0], identifierIndexHeaderSize)
	if _, err := replacement.Commit(context.Background()); !errors.Is(err, ErrCorruptIndex) {
		t.Fatalf("corrupt pending identifier commit error = %v", err)
	}
	if !replacement.active || writer.replacement != replacement {
		t.Fatal("corrupt pre-publication replacement was closed")
	}
	if err := replacement.Abort(); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenIndex(directory, schema); !errors.Is(err, ErrIndexNotFound) {
		t.Fatalf("corrupt replacement changed the committed index: %v", err)
	}
}

func TestIndexBM25MatchesOneSegmentStatistics(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	documents := []Document{
		{ID: "doc-0", Fields: map[string]string{"text": "rare common common"}},
		{ID: "doc-1", Fields: map[string]string{"text": "common filler filler filler"}},
		{ID: "doc-2", Fields: map[string]string{"text": "rare common"}},
		{ID: "doc-3", Fields: map[string]string{"text": "common"}},
		{ID: "doc-4", Fields: map[string]string{"text": "common filler"}},
		{ID: "doc-5", Fields: map[string]string{"text": "common common"}},
	}

	builder, err := NewBuilder(schema, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, document := range documents {
		if err := builder.Add(document); err != nil {
			t.Fatal(err)
		}
	}
	singlePath := filepath.Join(t.TempDir(), "single.ks")
	if _, err := builder.Write(context.Background(), singlePath); err != nil {
		t.Fatal(err)
	}
	single, err := OpenSegment(singlePath, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer single.Close()

	directory := t.TempDir()
	writer, err := NewIndexWriter(directory, schema, WriterOptions{Segment: BuildOptions{MaxDocuments: 2}})
	if err != nil {
		t.Fatal(err)
	}
	for _, document := range documents {
		if err := writer.Add(context.Background(), document); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := writer.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	index, err := OpenIndex(directory, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()

	query := MatchQuery{Field: "text", Text: "rare common"}
	want, err := single.Search(context.Background(), query, SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := index.Search(context.Background(), query, SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("multi-segment hits = %d, one-segment hits = %d", len(got), len(want))
	}
	for position := range want {
		if got[position].ID != want[position].ID || got[position].Ordinal != want[position].Ordinal || math.Abs(got[position].Score-want[position].Score) > 1e-12 {
			t.Fatalf("hit %d: multi = %#v, one = %#v", position, got[position], want[position])
		}
	}
}

func TestIndexWriterCloseDiscardsUncommittedSegments(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	writer, err := NewIndexWriter(directory, schema, WriterOptions{Segment: BuildOptions{MaxDocuments: 1}})
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 2; index++ {
		if err := writer.Add(context.Background(), Document{
			ID: fmt.Sprintf("doc-%d", index), Fields: map[string]string{"text": "pending"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	segments, err := filepath.Glob(filepath.Join(directory, "*.ks"))
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 2 {
		t.Fatalf("pending segment count = %d", len(segments))
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	segments, err = filepath.Glob(filepath.Join(directory, "*.ks"))
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 0 {
		t.Fatalf("discard left segments: %#v", segments)
	}
	if _, err := OpenIndex(directory, schema); !errors.Is(err, ErrIndexNotFound) {
		t.Fatalf("discarded index open error = %v", err)
	}
	if err := writer.Add(context.Background(), Document{ID: "closed"}); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed writer add error = %v", err)
	}
}

func TestIndexBlockMaxUsesGlobalThreshold(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	writer, err := NewIndexWriter(directory, schema, WriterOptions{
		Segment: BuildOptions{MaxDocuments: 1024, FlushThresholdBytes: 64 << 20},
	})
	if err != nil {
		t.Fatal(err)
	}
	highScore := strings.TrimSpace(strings.Repeat("alpha beta ", 8))
	lowScore := "alpha beta " + strings.Repeat("padding ", 126)
	for document := 0; document < 4096; document++ {
		text := lowScore
		if document >= 1024 && document < 1024+postingBlockDocuments {
			text = highScore
		}
		if err := writer.Add(context.Background(), Document{
			ID: fmt.Sprintf("doc-%05d", document), Fields: map[string]string{"text": text},
		}); err != nil {
			_ = writer.Close()
			t.Fatal(err)
		}
	}
	if _, err := writer.Commit(context.Background()); err != nil {
		_ = writer.Close()
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	index, err := OpenIndex(directory, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()

	prepared, err := prepareMatchQuery(
		context.Background(), schema, MatchQuery{Field: "text", Text: "alpha beta"}, SearchOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	optimizedStats := blockMaxStatistics{}
	prepared.statistics = &optimizedStats
	optimized, err := index.searchMatch(context.Background(), prepared)
	if err != nil {
		t.Fatal(err)
	}
	exhaustiveStats := blockMaxStatistics{}
	prepared.blockMax = false
	prepared.statistics = &exhaustiveStats
	exhaustive, err := index.searchMatch(context.Background(), prepared)
	if err != nil {
		t.Fatal(err)
	}
	assertOrderedHitsEqual(t, optimized, exhaustive)
	if optimizedStats.leadBlocksDecoded > 3 || optimizedStats.leadBlocksPruned < 14 || optimizedStats.segmentsPruned < 2 {
		t.Fatalf("index block-max work = %#v", optimizedStats)
	}
	if exhaustiveStats.leadBlocksDecoded != 32 || exhaustiveStats.leadBlocksPruned != 0 || exhaustiveStats.segmentsPruned != 0 {
		t.Fatalf("index exhaustive work = %#v", exhaustiveStats)
	}
}

func TestEmptyIndexAndLatestManifestCorruption(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	writer, err := NewIndexWriter(directory, schema, WriterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	info, err := writer.Commit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Generation != 1 || info.Segments != 0 || info.Documents != 0 {
		t.Fatalf("empty commit info = %#v", info)
	}
	empty, err := OpenIndex(directory, schema)
	if err != nil {
		t.Fatal(err)
	}
	if hits, err := empty.Search(context.Background(), MatchQuery{Field: "text", Text: "anything"}, SearchOptions{}); err != nil || len(hits) != 0 {
		t.Fatalf("empty index hits = %#v, err = %v", hits, err)
	}
	if err := empty.Verify(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := empty.Close(); err != nil {
		t.Fatal(err)
	}

	if err := writer.Add(context.Background(), Document{ID: "doc", Fields: map[string]string{"text": "committed"}}); err != nil {
		t.Fatal(err)
	}
	info, err = writer.Commit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	manifestData, err := os.ReadFile(info.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestData[len(manifestData)-1] ^= 0xff
	if err := os.WriteFile(info.Manifest, manifestData, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenIndex(directory, schema); !errors.Is(err, ErrCorruptIndex) {
		t.Fatalf("latest corrupt manifest error = %v", err)
	}
}

func TestIndexCanceledAndConcurrentSearch(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	writer, err := NewIndexWriter(directory, schema, WriterOptions{Segment: BuildOptions{MaxDocuments: 100}})
	if err != nil {
		t.Fatal(err)
	}
	for document := 0; document < 1000; document++ {
		if err := writer.Add(context.Background(), Document{
			ID: fmt.Sprintf("doc-%04d", document), Fields: map[string]string{"text": "alpha beta gamma"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := writer.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	index, err := OpenIndex(directory, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := index.Search(canceled, MatchQuery{Field: "text", Text: "alpha"}, SearchOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled index search error = %v", err)
	}

	var wait sync.WaitGroup
	errorsFound := make(chan error, 8)
	for worker := 0; worker < 8; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for iteration := 0; iteration < 10; iteration++ {
				hits, err := index.Search(context.Background(), MatchQuery{Field: "text", Text: "alpha beta"}, SearchOptions{})
				if err != nil {
					errorsFound <- err
					return
				}
				if len(hits) != defaultSearchLimit || hits[0].ID != "doc-0000" {
					errorsFound <- fmt.Errorf("unexpected hits: %#v", hits)
					return
				}
			}
		}()
	}
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Fatal(err)
	}
}

func TestManifestBoundsAndSchemaMismatch(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	writer, err := NewIndexWriter(directory, schema, WriterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Add(context.Background(), Document{ID: "doc", Fields: map[string]string{"text": "alpha"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	other, err := NewSchema(Text("other", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenIndex(directory, other); !errors.Is(err, ErrSchemaMismatch) {
		t.Fatalf("index schema mismatch error = %v", err)
	}

	tooMany := indexManifest{
		generation: 1, schemaHash: schema.fingerprint,
		segments: make([]manifestSegment, manifestMaximumSegments+1),
	}
	if _, err := marshalManifest(tooMany); !errors.Is(err, ErrTooManySegments) {
		t.Fatalf("oversized manifest error = %v", err)
	}
}

func TestIndexIgnoresOrphanAndTemporaryFiles(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	writer, err := NewIndexWriter(directory, schema, WriterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Add(context.Background(), Document{ID: "committed", Fields: map[string]string{"text": "visible"}}); err != nil {
		t.Fatal(err)
	}
	info, err := writer.Commit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	orphan, err := NewBuilder(schema, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := orphan.Add(Document{ID: "orphan", Fields: map[string]string{"text": "hidden"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := orphan.Write(context.Background(), filepath.Join(directory, "segment-orphan.ks")); err != nil {
		t.Fatal(err)
	}
	temporaryManifest := filepath.Join(directory, manifestFilename(info.Generation+1)+".tmp-incomplete")
	if err := os.WriteFile(temporaryManifest, []byte("incomplete"), 0o600); err != nil {
		t.Fatal(err)
	}

	index, err := OpenIndex(directory, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	if got := index.Info(); got.Generation != info.Generation || got.Documents != 1 || got.Segments != 1 {
		t.Fatalf("index adopted uncommitted files: %#v", got)
	}
	if hits, err := index.Search(context.Background(), MatchQuery{Field: "text", Text: "hidden"}, SearchOptions{}); err != nil || len(hits) != 0 {
		t.Fatalf("orphan hits = %#v, err = %v", hits, err)
	}
}

func TestIndexReportsMissingCommittedSegment(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	writer, err := NewIndexWriter(directory, schema, WriterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Add(context.Background(), Document{ID: "doc", Fields: map[string]string{"text": "alpha"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	segments, err := filepath.Glob(filepath.Join(directory, "*.ks"))
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 1 {
		t.Fatalf("segment count = %d", len(segments))
	}
	if err := os.Remove(segments[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenIndex(directory, schema); !errors.Is(err, ErrCorruptIndex) || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing committed segment error = %v", err)
	}
}

func buildTestSegment(t *testing.T, schema Schema, documents []Document) *Segment {
	t.Helper()
	builder, err := NewBuilder(schema, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, document := range documents {
		if err := builder.Add(document); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "test.ks")
	if _, err := builder.Write(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	segment, err := OpenSegment(path, schema)
	if err != nil {
		t.Fatal(err)
	}
	return segment
}

func corruptByte(t *testing.T, path string, offset int64) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var value [1]byte
	if _, err := file.ReadAt(value[:], offset); err != nil {
		t.Fatal(err)
	}
	value[0] ^= 0xff
	if _, err := file.WriteAt(value[:], offset); err != nil {
		t.Fatal(err)
	}
	if err := file.Sync(); err != nil {
		t.Fatal(err)
	}
}

func marshalLegacyManifestForTest(t *testing.T, manifest indexManifest) []byte {
	t.Helper()
	body := make([]byte, 0, 64)
	for _, segment := range manifest.segments {
		base := len(body)
		body = append(body, make([]byte, manifestV1EntrySize)...)
		binary.LittleEndian.PutUint16(body[base:base+2], uint16(len(segment.name)))
		binary.LittleEndian.PutUint32(body[base+4:base+8], segment.documents)
		binary.LittleEndian.PutUint64(body[base+8:base+16], segment.bytes)
		body = append(body, segment.name...)
	}
	data := make([]byte, manifestHeaderSize, manifestHeaderSize+len(body))
	copy(data[:8], manifestMagic[:])
	binary.LittleEndian.PutUint32(data[8:12], manifestLegacyVersion)
	binary.LittleEndian.PutUint32(data[12:16], manifestHeaderSize)
	binary.LittleEndian.PutUint64(data[16:24], uint64(manifestHeaderSize+len(body)))
	copy(data[24:56], manifest.schemaHash[:])
	binary.LittleEndian.PutUint64(data[56:64], manifest.generation)
	binary.LittleEndian.PutUint32(data[64:68], uint32(len(manifest.segments)))
	binary.LittleEndian.PutUint32(data[72:76], crc32.Checksum(body, crcTable))
	binary.LittleEndian.PutUint32(data[manifestHeaderCRCOffset:manifestHeaderCRCOffset+4], crc32.Checksum(data, crcTable))
	return append(data, body...)
}

func assertHitScoresEqual(t *testing.T, got, want []Hit) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("hit counts differ: got %d, want %d", len(got), len(want))
	}
	wantScores := make(map[string]float64, len(want))
	for _, hit := range want {
		wantScores[hit.ID] = hit.Score
	}
	for _, hit := range got {
		score, exists := wantScores[hit.ID]
		if !exists || math.Abs(hit.Score-score) > 1e-12 {
			t.Fatalf("hit %q score = %.15f, want %.15f (exists %v)", hit.ID, hit.Score, score, exists)
		}
	}
}
