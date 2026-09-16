package search

import (
	"bytes"
	"context"
	"io"
	"reflect"
	"testing"
)

func TestPackedSnapshotMatchesSegmentedSearch(t *testing.T) {
	schema, err := NewSchema(Text("name", VietnameseAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	w, err := NewIndexWriter(directory, schema, WriterOptions{Segment: BuildOptions{MaxDocuments: 2}})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	ctx := context.Background()
	for _, doc := range []Document{
		{ID: "shopee/1", Fields: map[string]string{"name": "b\u00e0n ph\u00edm logitech"}},
		{ID: "shopee/2", Fields: map[string]string{"name": "logitech keyboard"}},
		{ID: "other/3", Fields: map[string]string{"name": "logitech mouse"}},
		{ID: "other/4", Fields: map[string]string{"name": "b\u00e0n ph\u00edm c\u01a1"}},
	} {
		if err := w.Add(ctx, doc); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	index, err := OpenIndex(directory, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	var output bytes.Buffer
	if err := index.WriteSnapshot(ctx, &output); err != nil {
		t.Fatal(err)
	}
	data := output.Bytes()
	packed, err := OpenSnapshot(io.NewSectionReader(bytes.NewReader(data), 0, int64(len(data))), schema)
	if err != nil {
		t.Fatal(err)
	}
	defer packed.Close()
	if packed.Info().Segments < 2 {
		t.Fatal("expected multiple packed segments")
	}
	inspected, err := InspectSnapshot(ctx, io.NewSectionReader(bytes.NewReader(data), 0, int64(len(data))), schema)
	if err != nil {
		t.Fatal(err)
	}
	if inspected.Generation != packed.Info().Generation || inspected.Segments != packed.Info().Segments ||
		inspected.Documents != packed.Info().Documents || inspected.Bytes != int64(len(data)) ||
		inspected.ReaderCapacityBytes < packed.ResidentBytes() {
		t.Fatalf("inspect = %+v, open = %+v", inspected, packed.Info())
	}
	if err := packed.Verify(ctx); err != nil {
		t.Fatal(err)
	}
	for _, query := range []MatchQuery{{Text: "logitech"}, {Text: "ban phim"}, {Text: "logitech", IdentifierPrefix: "shopee/"}} {
		query.Fields = []string{"name"}
		want, err := index.Search(ctx, query, SearchOptions{Limit: 3})
		if err != nil {
			t.Fatal(err)
		}
		got, err := packed.Search(ctx, query, SearchOptions{Limit: 3})
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("packed = %+v, want %+v, err=%v", got, want, err)
		}
		if len(got) != 0 {
			after := &SearchAfter{Score: got[0].Score, Ordinal: got[0].Ordinal}
			want, err = index.Search(ctx, query, SearchOptions{Limit: 3, After: after})
			if err != nil {
				t.Fatal(err)
			}
			got, err = packed.Search(ctx, query, SearchOptions{Limit: 3, After: after})
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatal("packed pagination mismatch", err)
			}
		}
	}
	if resident := packed.ResidentBytes(); resident <= 0 || resident > inspected.ReaderCapacityBytes {
		t.Fatalf("resident bytes = %d, capacity = %d", resident, inspected.ReaderCapacityBytes)
	}
	data[20] ^= 1
	if _, err := OpenSnapshot(io.NewSectionReader(bytes.NewReader(data), 0, int64(len(data))), schema); err == nil {
		t.Fatal("corrupt packed manifest accepted")
	}
	if _, err := InspectSnapshot(ctx, io.NewSectionReader(bytes.NewReader(data), 0, int64(len(data))), schema); err == nil {
		t.Fatal("inspect accepted corrupt packed manifest")
	}
}

func TestInspectSnapshotHonorsCancellation(t *testing.T) {
	schema, err := NewSchema(Text("name", VietnameseAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := InspectSnapshot(ctx, io.NewSectionReader(bytes.NewReader(nil), 0, 0), schema); err != context.Canceled {
		t.Fatalf("InspectSnapshot cancellation = %v", err)
	}
}

func TestManagerWriteSnapshotLeasesCommittedGeneration(t *testing.T) {
	schema, err := NewSchema(Text("name", VietnameseAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(t.TempDir(), ManagerOptions{
		DisableAutoCompact: true,
		Writer:             WriterOptions{Segment: BuildOptions{MaxDocuments: 2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	ctx := context.Background()
	documents := []Document{
		{ID: "shop/1", Fields: map[string]string{"name": "bàn phím logitech"}},
		{ID: "shop/2", Fields: map[string]string{"name": "chuột logitech"}},
		{ID: "shop/3", Fields: map[string]string{"name": "bàn phím cơ"}},
	}
	if _, err := manager.UpsertMany(ctx, "shop", schema, documents); err != nil {
		t.Fatal(err)
	}
	want, err := manager.Search(ctx, "shop", schema, MatchQuery{
		Fields: []string{"name"}, Text: "ban phim logitech",
	}, SearchOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	info, err := manager.WriteSnapshot(ctx, "shop", schema, &output)
	if err != nil {
		t.Fatal(err)
	}
	if info.Generation == 0 || info.Documents != uint64(len(documents)) || info.Deleted != 0 {
		t.Fatalf("snapshot source = %+v", info)
	}
	packed, err := OpenSnapshot(
		io.NewSectionReader(bytes.NewReader(output.Bytes()), 0, int64(output.Len())), schema,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer packed.Close()
	got, err := packed.Search(ctx, MatchQuery{
		Fields: []string{"name"}, Text: "ban phim logitech",
	}, SearchOptions{Limit: 10})
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("packed hits = %+v, want %+v, err = %v", got, want, err)
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := manager.WriteSnapshot(canceled, "shop", schema, &bytes.Buffer{}); err != context.Canceled {
		t.Fatalf("canceled snapshot = %v", err)
	}
	if _, err := manager.WriteSnapshot(ctx, "missing", schema, &bytes.Buffer{}); err != ErrIndexNotFound {
		t.Fatalf("missing snapshot = %v", err)
	}
	if _, err := manager.WriteSnapshot(ctx, "shop", schema, nil); err == nil {
		t.Fatal("nil snapshot output accepted")
	}
}

func TestManagerWriteSnapshotRejectsTombstones(t *testing.T) {
	schema, err := NewSchema(Text("name", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(t.TempDir(), ManagerOptions{DisableAutoCompact: true})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	ctx := context.Background()
	if _, err := manager.Add(ctx, "shop", schema, Document{
		ID: "1", Fields: map[string]string{"name": "keyboard"},
	}); err != nil {
		t.Fatal(err)
	}
	if deleted, _, err := manager.Delete(ctx, "shop", schema, "1"); err != nil || !deleted {
		t.Fatalf("delete = %v, %v", deleted, err)
	}
	if _, err := manager.WriteSnapshot(ctx, "shop", schema, &bytes.Buffer{}); err == nil {
		t.Fatal("snapshot accepted tombstones")
	}
}
