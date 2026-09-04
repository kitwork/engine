package search

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestManagerBatchesMutationsAndPersists(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	manager, err := NewManager(directory, ManagerOptions{
		MaxOpenIndexes: 4, MaxConcurrentSearches: 16, MaxConcurrentSearchesPerIndex: 8,
		MutationQueueSize: 128, MutationBatchSize: 64, MutationBatchDelay: 50 * time.Millisecond,
		DisableAutoCompact: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	const documents = 64
	start := make(chan struct{})
	errorsFound := make(chan error, documents)
	var wait sync.WaitGroup
	for document := 0; document < documents; document++ {
		document := document
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := manager.Add(context.Background(), "tenant-a", schema, Document{
				ID:     fmt.Sprintf("doc-%03d", document),
				Fields: map[string]string{"text": "common durable product"},
			})
			if err != nil {
				errorsFound <- err
			}
		}()
	}
	close(start)
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Fatal(err)
	}
	stats := manager.Stats()
	if stats.OpenIndexes != 1 || stats.Commits < 1 || stats.Commits >= documents || stats.QueuedMutations != 0 {
		t.Fatalf("manager stats after batch = %#v", stats)
	}
	indexStats, exists := manager.IndexStats("tenant-a")
	if !exists || indexStats.Generation == 0 || indexStats.Documents != documents ||
		indexStats.QueuedMutations != 0 || indexStats.PendingMutations != 0 || indexStats.Failed {
		t.Fatalf("managed index stats = %#v, exists = %v", indexStats, exists)
	}
	info, err := manager.Info(context.Background(), "tenant-a", schema)
	if err != nil || info.Generation != indexStats.Generation || info.Documents != documents {
		t.Fatalf("managed info = %#v, %v", info, err)
	}
	hits, err := manager.Search(context.Background(), "tenant-a", schema,
		MatchQuery{Field: "text", Text: "common product"}, SearchOptions{Limit: documents})
	if err != nil || len(hits) != documents {
		t.Fatalf("batched hits = %d, %v", len(hits), err)
	}
	if _, err := manager.Update(context.Background(), "tenant-a", schema, Document{
		ID: "doc-000", Fields: map[string]string{"text": "updated premium"},
	}); err != nil {
		t.Fatal(err)
	}
	deleted, _, err := manager.Delete(context.Background(), "tenant-a", schema, "doc-001")
	if err != nil || !deleted {
		t.Fatalf("managed delete = %v, %v", deleted, err)
	}
	if hits, err := manager.Search(context.Background(), "tenant-a", schema,
		MatchQuery{Field: "text", Text: "updated"}, SearchOptions{}); err != nil || len(hits) != 1 || hits[0].ID != "doc-000" {
		t.Fatalf("updated hits = %#v, %v", hits, err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewManager(directory, ManagerOptions{DisableAutoCompact: true})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	hits, err = reopened.Search(context.Background(), "tenant-a", schema,
		MatchQuery{Field: "text", Text: "common"}, SearchOptions{Limit: documents})
	if err != nil || len(hits) != documents-2 {
		t.Fatalf("reopened hits = %d, %v", len(hits), err)
	}
}

func TestManagerBatchesReplaySafeProjectionMutations(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	manager, err := NewManager(directory, ManagerOptions{
		MutationQueueSize: 32, MutationBatchSize: 16, MutationBatchDelay: 100 * time.Millisecond,
		DisableAutoCompact: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	documents := []Document{
		{ID: "product/1", Fields: map[string]string{"text": "old keyboard"}},
		{ID: "product/2", Fields: map[string]string{"text": "wireless mouse"}},
		{ID: "product/3", Fields: map[string]string{"text": "usb headset"}},
	}
	if info, err := manager.UpsertMany(context.Background(), "tenant", schema, documents); err != nil || info.Documents != 3 {
		_ = manager.Close()
		t.Fatalf("initial upsert batch = %#v, %v", info, err)
	}
	if commits := manager.Stats().Commits; commits != 1 {
		_ = manager.Close()
		t.Fatalf("initial batch commits = %d, want 1", commits)
	}

	replayed := []Document{
		{ID: "product/1", Fields: map[string]string{"text": "mechanical keyboard"}},
		{ID: "product/2", Fields: map[string]string{"text": "silent mouse"}},
		{ID: "product/4", Fields: map[string]string{"text": "web camera"}},
	}
	for replay := 0; replay < 2; replay++ {
		info, err := manager.UpsertMany(context.Background(), "tenant", schema, replayed)
		if err != nil || info.Documents != 4 {
			_ = manager.Close()
			t.Fatalf("upsert replay %d = %#v, %v", replay, info, err)
		}
	}
	if commits := manager.Stats().Commits; commits != 3 {
		_ = manager.Close()
		t.Fatalf("commits after replay = %d, want 3", commits)
	}
	if info, err := manager.Mutate(context.Background(), "tenant", schema, MutationBatch{
		Upserts: []Document{{ID: "product/4", Fields: map[string]string{"text": "better web camera"}}},
		Deletes: []string{"product/3", "missing"},
	}); err != nil || info.Documents != 3 {
		_ = manager.Close()
		t.Fatalf("mixed mutation batch = %#v, %v", info, err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewManager(directory, ManagerOptions{DisableAutoCompact: true})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	for query, want := range map[string]string{
		"mechanical": "product/1",
		"silent":     "product/2",
		"camera":     "product/4",
	} {
		hits, err := reopened.Search(context.Background(), "tenant", schema,
			MatchQuery{Field: "text", Text: query}, SearchOptions{})
		if err != nil || len(hits) != 1 || hits[0].ID != want {
			t.Fatalf("reopened query %q = %#v, %v", query, hits, err)
		}
	}
	for _, query := range []string{"old", "wireless", "headset"} {
		hits, err := reopened.Search(context.Background(), "tenant", schema,
			MatchQuery{Field: "text", Text: query}, SearchOptions{})
		if err != nil || len(hits) != 0 {
			t.Fatalf("stale query %q = %#v, %v", query, hits, err)
		}
	}
}

func TestManagerProjectionCheckpointPersistsAndReplaces(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	manager, err := NewManager(directory, ManagerOptions{DisableAutoCompact: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.WriteCheckpoint(context.Background(), "tenant", schema, []byte("before-index")); !errors.Is(err, ErrIndexNotFound) {
		_ = manager.Close()
		t.Fatalf("checkpoint without generation = %v, want ErrIndexNotFound", err)
	}
	if _, err := manager.Add(context.Background(), "tenant", schema, Document{
		ID: "doc", Fields: map[string]string{"text": "durable"},
	}); err != nil {
		_ = manager.Close()
		t.Fatal(err)
	}
	for _, payload := range [][]byte{[]byte("source-tx-1"), []byte("source-tx-2")} {
		if err := manager.WriteCheckpoint(context.Background(), "tenant", schema, payload); err != nil {
			_ = manager.Close()
			t.Fatal(err)
		}
	}
	checkpoint, err := manager.ReadCheckpoint(context.Background(), "tenant", schema)
	if err != nil || string(checkpoint) != "source-tx-2" {
		_ = manager.Close()
		t.Fatalf("live checkpoint = %q, %v", checkpoint, err)
	}
	checkpoint[0] = 'X'
	again, err := manager.ReadCheckpoint(context.Background(), "tenant", schema)
	if err != nil || string(again) != "source-tx-2" {
		_ = manager.Close()
		t.Fatalf("checkpoint alias = %q, %v", again, err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(
		managedIndexDirectory(directory, "tenant"),
		managedCheckpointFilename+".tmp-0011223344556677",
	)
	if err := os.WriteFile(orphan, []byte("unpublished"), 0o600); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewManager(directory, ManagerOptions{DisableAutoCompact: true})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	checkpoint, err = reopened.ReadCheckpoint(context.Background(), "tenant", schema)
	if err != nil || string(checkpoint) != "source-tx-2" {
		t.Fatalf("reopened checkpoint = %q, %v", checkpoint, err)
	}
	if _, err := os.Stat(orphan); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphan checkpoint staging remains: %v", err)
	}
}

func TestManagerOpenReclaimsUnpublishedArtifacts(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	directory := managedIndexDirectory(root, "tenant")
	writer, err := NewIndexWriter(directory, schema, WriterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Add(context.Background(), Document{
		ID: "published", Fields: map[string]string{"text": "durable document"},
	}); err != nil {
		_ = writer.Close()
		t.Fatal(err)
	}
	committed, err := writer.Commit(context.Background())
	if err != nil {
		_ = writer.Close()
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	orphans := make([]string, 0, 2)
	for _, suffix := range []string{".ks", ".ki"} {
		name, err := newArtifactFilename("segment", committed.Generation+1, suffix)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, []byte("unpublished"), 0o644); err != nil {
			t.Fatal(err)
		}
		orphans = append(orphans, path)
	}

	manager, err := NewManager(root, ManagerOptions{DisableAutoCompact: true})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	hits, err := manager.Search(context.Background(), "tenant", schema,
		MatchQuery{Field: "text", Text: "durable"}, SearchOptions{})
	if err != nil || len(hits) != 1 || hits[0].ID != "published" {
		t.Fatalf("search after orphan recovery = %#v, %v", hits, err)
	}
	for _, path := range orphans {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("unpublished artifact %q remains: %v", filepath.Base(path), err)
		}
	}
}

func TestManagerOpenReclaimsInitialUnpublishedArtifacts(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	directory := managedIndexDirectory(root, "tenant")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	orphans := make([]string, 0, 2)
	for _, suffix := range []string{".ks", ".ki"} {
		name, err := newArtifactFilename("segment", 1, suffix)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, []byte("unpublished"), 0o600); err != nil {
			t.Fatal(err)
		}
		orphans = append(orphans, path)
	}

	manager, err := NewManager(root, ManagerOptions{DisableAutoCompact: true})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	hits, err := manager.Search(context.Background(), "tenant", schema,
		MatchQuery{Field: "text", Text: "anything"}, SearchOptions{})
	if err != nil || len(hits) != 0 {
		t.Fatalf("empty search after initial orphan recovery = %#v, %v", hits, err)
	}
	for _, path := range orphans {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("initial unpublished artifact %q remains: %v", filepath.Base(path), err)
		}
	}
	manifests, err := filepath.Glob(filepath.Join(directory, "*.km"))
	if err != nil {
		t.Fatal(err)
	}
	if len(manifests) != 0 {
		t.Fatalf("initial orphan recovery published manifests: %#v", manifests)
	}
}

func TestManagerSnapshotLeaseControlsGenerationGC(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(t.TempDir(), ManagerOptions{
		DisableAutoCompact: true, AutoGarbageCollect: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	first, err := manager.Add(context.Background(), "tenant", schema, Document{
		ID: "doc", Fields: map[string]string{"text": "alpha old"},
	})
	if err != nil {
		t.Fatal(err)
	}
	managed, releaseManaged, err := manager.managed(context.Background(), "tenant", schema)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseManaged()
	old, release, err := managed.acquireSnapshot()
	if err != nil || old == nil {
		t.Fatalf("acquire old snapshot = %#v, %v", old, err)
	}
	second, err := manager.Update(context.Background(), "tenant", schema, Document{
		ID: "doc", Fields: map[string]string{"text": "beta new"},
	})
	if err != nil {
		release()
		t.Fatal(err)
	}
	if second.Generation <= first.Generation {
		release()
		t.Fatalf("generations did not advance: first=%d second=%d", first.Generation, second.Generation)
	}
	oldHits, err := old.index.Search(context.Background(), MatchQuery{Field: "text", Text: "alpha"}, SearchOptions{})
	if err != nil || len(oldHits) != 1 {
		release()
		t.Fatalf("leased old hits = %#v, %v", oldHits, err)
	}
	if err := manager.Maintain(context.Background(), "tenant", schema); err != nil {
		release()
		t.Fatal(err)
	}
	if _, err := os.Stat(first.Manifest); err != nil {
		release()
		t.Fatalf("GC removed leased manifest: %v", err)
	}
	release()
	if err := manager.Maintain(context.Background(), "tenant", schema); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(first.Manifest); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("drained manifest still exists: %v", err)
	}
	hits, err := manager.Search(context.Background(), "tenant", schema,
		MatchQuery{Field: "text", Text: "beta"}, SearchOptions{})
	if err != nil || len(hits) != 1 || hits[0].ID != "doc" {
		t.Fatalf("current hits after GC = %#v, %v", hits, err)
	}
}

func TestManagerBoundsConcurrentSearches(t *testing.T) {
	analyzer := &managerBlockingAnalyzer{entered: make(chan struct{}), release: make(chan struct{})}
	schema, err := NewSchema(Text("text", analyzer))
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(t.TempDir(), ManagerOptions{
		MaxConcurrentSearches: 1, MaxConcurrentSearchesPerIndex: 1,
		DisableAutoCompact: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if _, err := manager.Add(context.Background(), "tenant", schema, Document{
		ID: "doc", Fields: map[string]string{"text": "match"},
	}); err != nil {
		t.Fatal(err)
	}
	firstDone := make(chan error, 1)
	go func() {
		_, err := manager.Search(context.Background(), "tenant", schema,
			MatchQuery{Field: "text", Text: "hold"}, SearchOptions{})
		firstDone <- err
	}()
	select {
	case <-analyzer.entered:
	case <-time.After(time.Second):
		t.Fatal("first search did not enter analyzer")
	}
	deadline, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := manager.Search(deadline, "tenant", schema,
		MatchQuery{Field: "text", Text: "match"}, SearchOptions{}); !errors.Is(err, context.DeadlineExceeded) {
		close(analyzer.release)
		t.Fatalf("bounded search error = %v", err)
	}
	if stats := manager.Stats(); stats.ActiveSearches != 1 {
		close(analyzer.release)
		t.Fatalf("active searches = %#v", stats)
	}
	close(analyzer.release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
}

func TestManagerNoisyTenantCannotReserveGlobalSearchSlots(t *testing.T) {
	analyzer := &managerBlockingAnalyzer{entered: make(chan struct{}), release: make(chan struct{})}
	schema, err := NewSchema(Text("text", analyzer))
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(t.TempDir(), ManagerOptions{
		MaxConcurrentSearches: 2, MaxConcurrentSearchesPerIndex: 1,
		DisableAutoCompact: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	for _, tenant := range []string{"tenant-a", "tenant-b"} {
		if _, err := manager.Add(context.Background(), tenant, schema, Document{
			ID: "doc", Fields: map[string]string{"text": "match"},
		}); err != nil {
			t.Fatal(err)
		}
	}

	firstDone := make(chan error, 1)
	go func() {
		_, err := manager.Search(context.Background(), "tenant-a", schema,
			MatchQuery{Field: "text", Text: "hold"}, SearchOptions{})
		firstDone <- err
	}()
	select {
	case <-analyzer.entered:
	case <-time.After(time.Second):
		t.Fatal("first tenant-a search did not enter analyzer")
	}

	const waitingSearches = 8
	started := make(chan struct{}, waitingSearches)
	waitingDone := make(chan error, waitingSearches)
	for search := 0; search < waitingSearches; search++ {
		go func() {
			started <- struct{}{}
			_, err := manager.Search(context.Background(), "tenant-a", schema,
				MatchQuery{Field: "text", Text: "hold"}, SearchOptions{})
			waitingDone <- err
		}()
	}
	for search := 0; search < waitingSearches; search++ {
		<-started
	}
	time.Sleep(20 * time.Millisecond)

	deadline, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	hits, searchErr := manager.Search(deadline, "tenant-b", schema,
		MatchQuery{Field: "text", Text: "match"}, SearchOptions{})
	cancel()
	close(analyzer.release)
	if searchErr != nil || len(hits) != 1 || hits[0].ID != "doc" {
		t.Fatalf("tenant-b was starved by tenant-a: hits=%#v err=%v", hits, searchErr)
	}
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	for search := 0; search < waitingSearches; search++ {
		if err := <-waitingDone; err != nil {
			t.Fatal(err)
		}
	}
}

func TestManagerSearchTelemetrySeparatesWaitingAndExecution(t *testing.T) {
	analyzer := &managerBlockingAnalyzer{entered: make(chan struct{}), release: make(chan struct{})}
	schema, err := NewSchema(Text("text", analyzer))
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(t.TempDir(), ManagerOptions{
		MaxConcurrentSearches: 1, MaxConcurrentSearchesPerIndex: 1,
		DisableAutoCompact: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if _, err := manager.Add(context.Background(), "tenant", schema, Document{
		ID: "doc", Fields: map[string]string{"text": "match"},
	}); err != nil {
		t.Fatal(err)
	}

	firstDone := make(chan error, 1)
	go func() {
		_, err := manager.Search(context.Background(), "tenant", schema,
			MatchQuery{Field: "text", Text: "hold"}, SearchOptions{})
		firstDone <- err
	}()
	select {
	case <-analyzer.entered:
	case <-time.After(time.Second):
		t.Fatal("first search did not enter execution")
	}

	waitingContext, cancelWaiting := context.WithCancel(context.Background())
	secondDone := make(chan error, 1)
	go func() {
		_, err := manager.Search(waitingContext, "tenant", schema,
			MatchQuery{Field: "text", Text: "match"}, SearchOptions{})
		secondDone <- err
	}()
	waitForManagerSearchState(t, manager, 1, 1)

	stats := manager.Stats()
	if stats.Searches.Started != 2 || stats.Searches.Completed != 0 ||
		stats.Searches.MaxActive != 1 || stats.Searches.MaxWaiting < 1 {
		close(analyzer.release)
		t.Fatalf("in-flight search telemetry = %#v", stats.Searches)
	}
	indexStats, exists := manager.IndexStats("tenant")
	if !exists || indexStats.ActiveSearches != 1 || indexStats.WaitingSearches != 1 ||
		indexStats.Searches.MaxActive != 1 {
		close(analyzer.release)
		t.Fatalf("in-flight index telemetry = %#v, exists = %v", indexStats, exists)
	}

	cancelWaiting()
	if err := <-secondDone; !errors.Is(err, context.Canceled) {
		close(analyzer.release)
		t.Fatalf("waiting search cancellation = %v", err)
	}
	close(analyzer.release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}

	stats = manager.Stats()
	if stats.ActiveSearches != 0 || stats.WaitingSearches != 0 ||
		stats.Searches.Started != 2 || stats.Searches.Completed != 2 ||
		stats.Searches.Succeeded != 1 || stats.Searches.Canceled != 1 || stats.Searches.Failed != 0 ||
		stats.Searches.TotalLatency.Count != 2 || stats.Searches.QueueLatency.Count != 2 ||
		stats.Searches.ExecutionLatency.Count != 1 {
		t.Fatalf("completed search telemetry = %#v", stats)
	}
	if len(stats.Searches.TotalLatency.Buckets) != searchLatencyBucketCount {
		t.Fatalf("latency buckets = %d", len(stats.Searches.TotalLatency.Buckets))
	}
	stats.Searches.TotalLatency.Buckets[0].Count = ^uint64(0)
	if manager.Stats().Searches.TotalLatency.Buckets[0].Count == ^uint64(0) {
		t.Fatal("search telemetry snapshot exposed mutable histogram state")
	}
}

func TestManagerRejectsSearchWhenTenantAdmissionQueueIsFull(t *testing.T) {
	analyzer := &managerBlockingAnalyzer{entered: make(chan struct{}), release: make(chan struct{})}
	schema, err := NewSchema(Text("text", analyzer))
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(t.TempDir(), ManagerOptions{
		MaxConcurrentSearches: 1, MaxConcurrentSearchesPerIndex: 1,
		MaxQueuedSearches: 2, MaxQueuedSearchesPerIndex: 1,
		DisableAutoCompact: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if _, err := manager.Add(context.Background(), "tenant", schema, Document{
		ID: "doc", Fields: map[string]string{"text": "match"},
	}); err != nil {
		t.Fatal(err)
	}

	firstDone := make(chan error, 1)
	go func() {
		_, err := manager.Search(context.Background(), "tenant", schema,
			MatchQuery{Field: "text", Text: "hold"}, SearchOptions{})
		firstDone <- err
	}()
	select {
	case <-analyzer.entered:
	case <-time.After(time.Second):
		t.Fatal("active search did not enter execution")
	}

	waitingContext, cancelWaiting := context.WithCancel(context.Background())
	secondDone := make(chan error, 1)
	go func() {
		_, err := manager.Search(waitingContext, "tenant", schema,
			MatchQuery{Field: "text", Text: "match"}, SearchOptions{})
		secondDone <- err
	}()
	waitForManagerSearchState(t, manager, 1, 1)

	overloadContext, cancelOverload := context.WithTimeout(context.Background(), 100*time.Millisecond)
	_, overloadErr := manager.Search(overloadContext, "tenant", schema,
		MatchQuery{Field: "text", Text: "match"}, SearchOptions{})
	cancelOverload()
	if !errors.Is(overloadErr, ErrSearchOverloaded) {
		cancelWaiting()
		close(analyzer.release)
		t.Fatalf("full tenant queue error = %v", overloadErr)
	}

	cancelWaiting()
	if err := <-secondDone; !errors.Is(err, context.Canceled) {
		close(analyzer.release)
		t.Fatalf("waiting search cancellation = %v", err)
	}
	close(analyzer.release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	stats := manager.Stats().Searches
	if stats.Started != 3 || stats.Completed != 3 || stats.Succeeded != 1 ||
		stats.Canceled != 1 || stats.Overloaded != 1 || stats.Failed != 0 ||
		stats.Active != 0 || stats.Waiting != 0 || stats.MaxWaiting != 1 ||
		stats.QueueLatency.Count != 2 || stats.ExecutionLatency.Count != 1 {
		t.Fatalf("overload telemetry = %#v", stats)
	}
}

func TestManagerSearchesRemainConsistentAcrossConcurrentUpdates(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	const documents = 32
	manager, err := NewManager(t.TempDir(), ManagerOptions{
		MaxConcurrentSearches: 8, MaxConcurrentSearchesPerIndex: 4,
		MutationQueueSize: 64, MutationBatchSize: 64, MutationBatchDelay: 2 * time.Millisecond,
		DisableAutoCompact: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	seedErrors := make(chan error, documents)
	var seedWait sync.WaitGroup
	for document := 0; document < documents; document++ {
		document := document
		seedWait.Add(1)
		go func() {
			defer seedWait.Done()
			_, err := manager.Add(context.Background(), "tenant", schema, Document{
				ID:     fmt.Sprintf("doc-%02d", document),
				Fields: map[string]string{"text": fmt.Sprintf("common version0 product%d", document)},
			})
			if err != nil {
				seedErrors <- err
			}
		}()
	}
	seedWait.Wait()
	close(seedErrors)
	for err := range seedErrors {
		t.Fatal(err)
	}

	const readers = 12
	const searchesPerReader = 100
	start := make(chan struct{})
	searchErrors := make(chan error, readers)
	var searchWait sync.WaitGroup
	for reader := 0; reader < readers; reader++ {
		searchWait.Add(1)
		go func() {
			defer searchWait.Done()
			<-start
			for range searchesPerReader {
				hits, err := manager.Search(context.Background(), "tenant", schema,
					MatchQuery{Field: "text", Text: "common"}, SearchOptions{Limit: documents})
				if err != nil {
					searchErrors <- err
					return
				}
				if err := validateSnapshotIdentifiers(hits, documents); err != nil {
					searchErrors <- err
					return
				}
			}
		}()
	}
	close(start)

	const updateRounds = 8
	for round := 1; round <= updateRounds; round++ {
		updateErrors := make(chan error, documents)
		var updateWait sync.WaitGroup
		for document := 0; document < documents; document++ {
			document := document
			updateWait.Add(1)
			go func() {
				defer updateWait.Done()
				_, err := manager.Update(context.Background(), "tenant", schema, Document{
					ID:     fmt.Sprintf("doc-%02d", document),
					Fields: map[string]string{"text": fmt.Sprintf("common version%d product%d", round, document)},
				})
				if err != nil {
					updateErrors <- err
				}
			}()
		}
		updateWait.Wait()
		close(updateErrors)
		for err := range updateErrors {
			t.Fatal(err)
		}
	}

	searchWait.Wait()
	close(searchErrors)
	for err := range searchErrors {
		t.Fatal(err)
	}
	finalHits, err := manager.Search(context.Background(), "tenant", schema,
		MatchQuery{Field: "text", Text: "version8"}, SearchOptions{Limit: documents})
	if err != nil {
		t.Fatal(err)
	}
	if err := validateSnapshotIdentifiers(finalHits, documents); err != nil {
		t.Fatal(err)
	}
	stats, exists := manager.IndexStats("tenant")
	if !exists || stats.Documents != documents || stats.PhysicalDocuments <= documents ||
		stats.Deleted == 0 || stats.Searches.Failed != 0 || stats.Searches.Canceled != 0 ||
		stats.Searches.Overloaded != 0 ||
		stats.Searches.MaxActive > 4 {
		t.Fatalf("concurrent publication stats = %#v, exists = %v", stats, exists)
	}
}

func TestManagerCapacityConfinementAndSchemaIsolation(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	manager, err := NewManager(root, ManagerOptions{MaxOpenIndexes: 1, DisableAutoCompact: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Search(context.Background(), "../../tenant-a", schema,
		MatchQuery{Field: "text", Text: "empty"}, SearchOptions{}); err != nil {
		t.Fatal(err)
	}
	path := managedIndexDirectory(root, "../../tenant-a")
	relative, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(relative, "..") || strings.Contains(path, "tenant-a") {
		t.Fatalf("managed path escaped root: path=%q relative=%q err=%v", path, relative, err)
	}
	if _, err := manager.Search(context.Background(), "tenant-b", schema,
		MatchQuery{Field: "text", Text: "empty"}, SearchOptions{}); !errors.Is(err, ErrManagerCapacity) {
		t.Fatalf("capacity error = %v", err)
	}
	otherSchema, err := NewSchema(Text("other", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Search(context.Background(), "../../tenant-a", otherSchema,
		MatchQuery{Field: "other", Text: "empty"}, SearchOptions{}); !errors.Is(err, ErrSchemaMismatch) {
		t.Fatalf("schema isolation error = %v", err)
	}
	if err := manager.CloseIndex("../../tenant-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Add(context.Background(), "tenant-b", schema, Document{
		ID: "doc", Fields: map[string]string{"text": "opened after eviction"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Add(context.Background(), "tenant-c", schema, Document{ID: "closed"}); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed manager add error = %v", err)
	}
}

func TestManagerCloseIndexSerializesConcurrentReopen(t *testing.T) {
	analyzer := &managerCloseBlockingAnalyzer{
		entered: make(chan struct{}), release: make(chan struct{}),
	}
	schema, err := NewSchema(Text("text", analyzer))
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(t.TempDir(), ManagerOptions{DisableAutoCompact: true})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if _, err := manager.Add(context.Background(), "tenant", schema, Document{
		ID: "doc", Fields: map[string]string{"text": "match"},
	}); err != nil {
		t.Fatal(err)
	}

	activeDone := make(chan error, 1)
	go func() {
		_, searchErr := manager.Search(context.Background(), "tenant", schema,
			MatchQuery{Field: "text", Text: "hold"}, SearchOptions{})
		activeDone <- searchErr
	}()
	select {
	case <-analyzer.entered:
	case <-time.After(time.Second):
		t.Fatal("active search did not hold its snapshot")
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- manager.CloseIndex("tenant") }()
	deadline := time.Now().Add(time.Second)
	for {
		manager.mu.Lock()
		entry := manager.indexes["tenant"]
		closing := entry != nil && entry.closing != nil
		manager.mu.Unlock()
		if closing {
			break
		}
		if time.Now().After(deadline) {
			close(analyzer.release)
			t.Fatal("index did not enter closing state")
		}
		time.Sleep(time.Millisecond)
	}

	type searchResult struct {
		hits []Hit
		err  error
	}
	reopened := make(chan searchResult, 1)
	go func() {
		hits, searchErr := manager.Search(context.Background(), "tenant", schema,
			MatchQuery{Field: "text", Text: "match"}, SearchOptions{})
		reopened <- searchResult{hits: hits, err: searchErr}
	}()
	select {
	case result := <-reopened:
		close(analyzer.release)
		t.Fatalf("reopen borrowed the closing index: hits=%#v err=%v", result.hits, result.err)
	case <-time.After(25 * time.Millisecond):
	}

	close(analyzer.release)
	if err := <-activeDone; err != nil {
		t.Fatalf("drained search error = %v", err)
	}
	if err := <-closeDone; err != nil {
		t.Fatal(err)
	}
	result := <-reopened
	if result.err != nil || len(result.hits) != 1 || result.hits[0].ID != "doc" {
		t.Fatalf("reopened search = %#v, %v", result.hits, result.err)
	}
	if stats := manager.Stats(); stats.OpenIndexes != 1 {
		t.Fatalf("reopened manager stats = %#v", stats)
	}
}

func TestManagerCloseDrainsAcceptedMutation(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	manager, err := NewManager(directory, ManagerOptions{
		MutationBatchDelay: 5 * time.Second, DisableAutoCompact: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	managed, releaseManaged, err := manager.managed(context.Background(), "tenant", schema)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseManaged()
	addDone := make(chan error, 1)
	go func() {
		_, err := manager.Add(context.Background(), "tenant", schema, Document{
			ID: "accepted", Fields: map[string]string{"text": "durable shutdown"},
		})
		addDone <- err
	}()
	deadline := time.Now().Add(time.Second)
	for managed.pendingMutations.Load() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if managed.pendingMutations.Load() != 1 {
		t.Fatal("mutation was not accepted before shutdown")
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-addDone; err != nil {
		t.Fatalf("accepted mutation failed during close: %v", err)
	}
	index, err := OpenIndex(managed.directory, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	hits, err := index.Search(context.Background(), MatchQuery{Field: "text", Text: "shutdown"}, SearchOptions{})
	if err != nil || len(hits) != 1 || hits[0].ID != "accepted" {
		t.Fatalf("shutdown persisted hits = %#v, %v", hits, err)
	}
}

func TestManagerAutomaticallyCompactsByPolicy(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(t.TempDir(), ManagerOptions{
		Writer: WriterOptions{Segment: BuildOptions{MaxDocuments: 1}},
		Compact: CompactOptions{
			MaximumSegments: 2, MaximumInputSegments: 4, MaximumInputDocuments: 100,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	for document := 0; document < 5; document++ {
		if _, err := manager.Add(context.Background(), "tenant", schema, Document{
			ID: fmt.Sprintf("doc-%d", document), Fields: map[string]string{"text": "common compact"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := manager.Maintain(context.Background(), "tenant", schema); err != nil {
		t.Fatal(err)
	}
	stats, exists := manager.IndexStats("tenant")
	if !exists || stats.Segments > 2 || stats.Documents != 5 || stats.Deleted != 0 {
		t.Fatalf("auto-compact stats = %#v, exists = %v", stats, exists)
	}
	if manager.Stats().Compactions == 0 {
		t.Fatal("auto-compaction did not publish a generation")
	}
	hits, err := manager.Search(context.Background(), "tenant", schema,
		MatchQuery{Field: "text", Text: "common compact"}, SearchOptions{Limit: 10})
	if err != nil || len(hits) != 5 {
		t.Fatalf("auto-compact hits = %#v, %v", hits, err)
	}
}

func TestManagerConcurrentTenantIsolation(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	const tenants = 8
	const documents = 16
	manager, err := NewManager(t.TempDir(), ManagerOptions{
		MaxOpenIndexes: tenants, MaxConcurrentSearches: 8, MaxConcurrentSearchesPerIndex: 2,
		MutationQueueSize: 64, MutationBatchSize: 16, MutationBatchDelay: 10 * time.Millisecond,
		DisableAutoCompact: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	errorsFound := make(chan error, tenants*documents+tenants)
	var wait sync.WaitGroup
	for tenant := 0; tenant < tenants; tenant++ {
		for document := 0; document < documents; document++ {
			tenant, document := tenant, document
			wait.Add(1)
			go func() {
				defer wait.Done()
				key := fmt.Sprintf("tenant-%d", tenant)
				_, err := manager.Add(context.Background(), key, schema, Document{
					ID:     fmt.Sprintf("doc-%d", document),
					Fields: map[string]string{"text": fmt.Sprintf("tenant%dunique common", tenant)},
				})
				if err != nil {
					errorsFound <- err
				}
			}()
		}
	}
	wait.Wait()
	for tenant := 0; tenant < tenants; tenant++ {
		tenant := tenant
		wait.Add(1)
		go func() {
			defer wait.Done()
			key := fmt.Sprintf("tenant-%d", tenant)
			for iteration := 0; iteration < 10; iteration++ {
				hits, err := manager.Search(context.Background(), key, schema,
					MatchQuery{Field: "text", Text: fmt.Sprintf("tenant%dunique", tenant)},
					SearchOptions{Limit: documents})
				if err != nil || len(hits) != documents {
					errorsFound <- fmt.Errorf("tenant %d isolation hits=%d: %w", tenant, len(hits), err)
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
	if stats := manager.Stats(); stats.OpenIndexes != tenants || stats.ActiveSearches != 0 || stats.QueuedMutations != 0 {
		t.Fatalf("tenant isolation stats = %#v", stats)
	}
}

func TestManagerMutationBatchDeadlineMarksWriterUnavailable(t *testing.T) {
	analyzer := &managerMutationBlockingAnalyzer{
		entered: make(chan struct{}),
	}
	schema, err := NewSchema(Text("text", analyzer))
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(t.TempDir(), ManagerOptions{
		MutationQueueSize: 4, MutationBatchSize: 2, MutationBatchDelay: time.Second,
		CommitTimeout: 40 * time.Millisecond, DisableAutoCompact: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	managed, releaseManaged, err := manager.managed(context.Background(), "tenant", schema)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseManaged()
	firstDone := make(chan error, 1)
	go func() {
		_, err := manager.Add(context.Background(), "tenant", schema, Document{
			ID: "first", Fields: map[string]string{"text": "normal"},
		})
		firstDone <- err
	}()
	deadline := time.Now().Add(time.Second)
	for managed.pendingMutations.Load() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if managed.pendingMutations.Load() != 1 {
		t.Fatal("first mutation was not accepted")
	}
	secondDone := make(chan error, 1)
	go func() {
		_, err := manager.Add(context.Background(), "tenant", schema, Document{
			ID: "slow", Fields: map[string]string{"text": "slow"},
		})
		secondDone <- err
	}()
	select {
	case <-analyzer.entered:
	case <-time.After(time.Second):
		t.Fatal("mutation did not enter analyzer")
	}
	firstErr := <-firstDone
	secondErr := <-secondDone
	if !errors.Is(firstErr, ErrIndexUnavailable) || !errors.Is(firstErr, context.DeadlineExceeded) {
		t.Fatalf("applied mutation deadline error = %v", firstErr)
	}
	if !errors.Is(secondErr, context.DeadlineExceeded) {
		t.Fatalf("canceled analyzer error = %v", secondErr)
	}
	if _, err := manager.Add(context.Background(), "tenant", schema, Document{ID: "later"}); !errors.Is(err, ErrIndexUnavailable) {
		t.Fatalf("failed writer accepted another mutation: %v", err)
	}
	stats, exists := manager.IndexStats("tenant")
	if !exists || !stats.Failed || !strings.Contains(stats.Failure, context.DeadlineExceeded.Error()) {
		t.Fatalf("failed index stats = %#v, exists = %v", stats, exists)
	}
}

func TestManagerDefaultsFitSmallMutationQueue(t *testing.T) {
	manager, err := NewManager(t.TempDir(), ManagerOptions{
		MutationQueueSize: 1, DisableAutoCompact: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if manager.options.MutationBatchSize != 1 {
		t.Fatalf("default mutation batch = %d, want 1", manager.options.MutationBatchSize)
	}
	if manager.options.MaxConcurrentSearches != defaultManagerConcurrentSearches(runtime.GOMAXPROCS(0)) ||
		manager.options.MaxQueuedSearches != defaultManagerQueuedSearches ||
		manager.options.MaxQueuedSearchesPerIndex != defaultManagerQueuedPerIndex ||
		manager.options.MaxConcurrentReplacements != defaultManagerReplacements ||
		manager.options.ReplacementTimeout != defaultManagerReplacementTimeout {
		t.Fatalf("default search admission = %#v", manager.options.ManagerOptions)
	}
}

func TestDefaultManagerConcurrentSearchesTracksCPUWithoutOversubscription(t *testing.T) {
	tests := []struct {
		processors int
		want       int
	}{
		{processors: 1, want: 4},
		{processors: 2, want: 4},
		{processors: 4, want: 4},
		{processors: 16, want: 16},
		{processors: maximumManagerConcurrentSearches, want: maximumManagerConcurrentSearches},
	}
	for _, test := range tests {
		if got := defaultManagerConcurrentSearches(test.processors); got != test.want {
			t.Errorf("default search slots for %d processors = %d, want %d", test.processors, got, test.want)
		}
	}
}

func TestManagerQuarantinesDurabilityUncertainGeneration(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	syncErr := errors.New("injected directory sync failure")
	manager, err := NewManager(root, ManagerOptions{
		Writer: WriterOptions{
			directorySync: func(string) error { return syncErr },
		},
		DisableAutoCompact: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	info, err := manager.Add(context.Background(), "tenant", schema, Document{
		ID: "published", Fields: map[string]string{"text": "durable boundary"},
	})
	if !errors.Is(err, ErrIndexUnavailable) || !errors.Is(err, ErrDurabilityUncertain) || !errors.Is(err, syncErr) {
		t.Fatalf("managed commit error = %v, want unavailable durability uncertainty", err)
	}
	if info.Generation != 1 || info.Manifest == "" {
		t.Fatalf("managed published info = %#v, want generation 1", info)
	}
	if _, err := manager.Add(context.Background(), "tenant", schema, Document{ID: "retry"}); !errors.Is(err, ErrIndexUnavailable) || !errors.Is(err, ErrDurabilityUncertain) {
		t.Fatalf("quarantined writer accepted another mutation: %v", err)
	}
	stats, exists := manager.IndexStats("tenant")
	if !exists || !stats.Failed || !strings.Contains(stats.Failure, ErrDurabilityUncertain.Error()) {
		t.Fatalf("durability failure stats = %#v, exists = %v", stats, exists)
	}

	index, err := OpenIndex(managedIndexDirectory(root, "tenant"), schema)
	if err != nil {
		t.Fatal(err)
	}
	hits, err := index.Search(context.Background(), MatchQuery{Field: "text", Text: "boundary"}, SearchOptions{})
	if err != nil || len(hits) != 1 || hits[0].ID != "published" {
		_ = index.Close()
		t.Fatalf("visible uncertain generation hits = %#v, %v", hits, err)
	}
	if err := index.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestManagerOwnsAcceptedMutationPayload(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(t.TempDir(), ManagerOptions{
		MutationBatchDelay: 100 * time.Millisecond, DisableAutoCompact: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	managed, releaseManaged, err := manager.managed(context.Background(), "tenant", schema)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseManaged()
	document := Document{ID: "doc", Fields: map[string]string{"text": "original value"}}
	addDone := make(chan error, 1)
	go func() {
		_, err := manager.Add(context.Background(), "tenant", schema, document)
		addDone <- err
	}()
	deadline := time.Now().Add(time.Second)
	for managed.pendingMutations.Load() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if managed.pendingMutations.Load() != 1 {
		t.Fatal("mutation was not accepted")
	}
	document.Fields["text"] = "changed by caller"
	if err := <-addDone; err != nil {
		t.Fatal(err)
	}
	hits, err := manager.Search(context.Background(), "tenant", schema,
		MatchQuery{Field: "text", Text: "original"}, SearchOptions{})
	if err != nil || len(hits) != 1 || hits[0].ID != "doc" {
		t.Fatalf("owned payload hits = %#v, %v", hits, err)
	}
	if hits, err := manager.Search(context.Background(), "tenant", schema,
		MatchQuery{Field: "text", Text: "changed"}, SearchOptions{}); err != nil || len(hits) != 0 {
		t.Fatalf("caller mutation leaked into index: %#v, %v", hits, err)
	}
}

func TestManagerBoundsPendingMutationBytes(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	manager, err := NewManager(directory, ManagerOptions{
		MutationBatchDelay:      5 * time.Second,
		MaxPendingMutationBytes: 512, MaxPendingMutationBytesPerIndex: 512,
		DisableAutoCompact: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	managed, releaseManaged, err := manager.managed(context.Background(), "tenant", schema)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseManaged()
	firstDone := make(chan error, 1)
	go func() {
		_, err := manager.Add(context.Background(), "tenant", schema, Document{
			ID: "first", Fields: map[string]string{"text": strings.Repeat("a", 80)},
		})
		firstDone <- err
	}()
	deadline := time.Now().Add(time.Second)
	for managed.pendingMutations.Load() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if managed.pendingMutations.Load() != 1 || manager.Stats().PendingMutationBytes == 0 {
		t.Fatalf("first mutation did not reserve bytes: %#v", manager.Stats())
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	_, err = manager.Add(waitCtx, "tenant", schema, Document{
		ID: "second", Fields: map[string]string{"text": strings.Repeat("b", 80)},
	})
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("byte-budget wait error = %v", err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-firstDone; err != nil {
		t.Fatalf("accepted mutation failed during byte-budget shutdown: %v", err)
	}
	if stats := manager.Stats(); stats.PendingMutationBytes != 0 || stats.QueuedMutations != 0 {
		t.Fatalf("mutation budget leaked after close: %#v", stats)
	}

	tooSmall, err := NewManager(t.TempDir(), ManagerOptions{
		MaxPendingMutationBytes: 256, MaxPendingMutationBytesPerIndex: 256,
		DisableAutoCompact: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer tooSmall.Close()
	if _, err := tooSmall.Add(context.Background(), "tenant", schema, Document{
		ID: "large", Fields: map[string]string{"text": strings.Repeat("x", 80)},
	}); !errors.Is(err, ErrMutationTooLarge) {
		t.Fatalf("oversized mutation error = %v", err)
	}
}

func TestManagerBoundsConcurrentMutationBatches(t *testing.T) {
	analyzer := &managerWriterBlockingAnalyzer{
		enteredA: make(chan struct{}), enteredB: make(chan struct{}),
		releaseA: make(chan struct{}), releaseB: make(chan struct{}),
	}
	schema, err := NewSchema(Text("text", analyzer))
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(t.TempDir(), ManagerOptions{
		MaxOpenIndexes: 2, MaxConcurrentMutationBatches: 1,
		DisableAutoCompact: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		analyzer.releaseAll()
		_ = manager.Close()
	}()

	firstDone := make(chan error, 1)
	go func() {
		_, err := manager.Add(context.Background(), "tenant-a", schema, Document{
			ID: "a", Fields: map[string]string{"text": "hold-a"},
		})
		firstDone <- err
	}()
	select {
	case <-analyzer.enteredA:
	case <-time.After(time.Second):
		t.Fatal("first mutation batch did not enter analyzer")
	}
	secondDone := make(chan error, 1)
	go func() {
		_, err := manager.Add(context.Background(), "tenant-b", schema, Document{
			ID: "b", Fields: map[string]string{"text": "hold-b"},
		})
		secondDone <- err
	}()
	select {
	case <-analyzer.enteredB:
		t.Fatal("second tenant bypassed the global mutation-batch limit")
	case <-time.After(40 * time.Millisecond):
	}
	if stats := manager.Stats(); stats.ActiveMutationBatches != 1 {
		t.Fatalf("active mutation batches = %#v", stats)
	}
	analyzer.releaseFirst()
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	select {
	case <-analyzer.enteredB:
	case <-time.After(time.Second):
		t.Fatal("second mutation batch did not acquire released capacity")
	}
	analyzer.releaseSecond()
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
}

func TestManagerStreamingReplacementPublishesAtomically(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	manager, err := NewManager(directory, ManagerOptions{
		Writer:             WriterOptions{Segment: BuildOptions{MaxDocuments: 1}},
		DisableAutoCompact: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Add(context.Background(), "tenant", schema, Document{
		ID: "old", Fields: map[string]string{"text": "previous catalog"},
	}); err != nil {
		t.Fatal(err)
	}

	replacement, err := manager.BeginReplacement(context.Background(), "tenant", schema)
	if err != nil {
		t.Fatal(err)
	}
	for _, document := range []Document{
		{ID: "new-a", Fields: map[string]string{"text": "fresh catalog alpha"}},
		{ID: "new-b", Fields: map[string]string{"text": "fresh catalog beta"}},
	} {
		if err := replacement.Add(context.Background(), document); err != nil {
			_ = replacement.Abort()
			t.Fatal(err)
		}
	}
	if _, err := manager.Add(context.Background(), "tenant", schema, Document{ID: "blocked"}); !errors.Is(err, ErrReplacementActive) {
		_ = replacement.Abort()
		t.Fatalf("ordinary mutation during replacement = %v", err)
	}
	if err := manager.Maintain(context.Background(), "tenant", schema); !errors.Is(err, ErrReplacementActive) {
		_ = replacement.Abort()
		t.Fatalf("maintenance during replacement = %v", err)
	}
	oldHits, err := manager.Search(context.Background(), "tenant", schema,
		MatchQuery{Field: "text", Text: "previous"}, SearchOptions{})
	if err != nil || len(oldHits) != 1 || oldHits[0].ID != "old" {
		_ = replacement.Abort()
		t.Fatalf("old snapshot during replacement = %#v, %v", oldHits, err)
	}
	newHits, err := manager.Search(context.Background(), "tenant", schema,
		MatchQuery{Field: "text", Text: "fresh"}, SearchOptions{})
	if err != nil || len(newHits) != 0 {
		_ = replacement.Abort()
		t.Fatalf("uncommitted replacement became visible = %#v, %v", newHits, err)
	}
	stats, exists := manager.IndexStats("tenant")
	if !exists || !stats.ReplacementActive || stats.PendingReplacementDocs != 0 ||
		stats.PendingReplacementBytes != 0 || stats.PendingWriteBytes != 0 {
		_ = replacement.Abort()
		t.Fatalf("active replacement stats = %#v, exists=%v", stats, exists)
	}

	info, err := replacement.Commit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Documents != 2 || info.Segments != 2 {
		t.Fatalf("replacement info = %#v", info)
	}
	if err := replacement.Add(context.Background(), Document{ID: "late"}); !errors.Is(err, ErrClosed) {
		t.Fatalf("completed replacement add = %v", err)
	}
	if _, err := replacement.Commit(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("completed replacement commit = %v", err)
	}
	if _, err := manager.Add(context.Background(), "tenant", schema, Document{
		ID: "after", Fields: map[string]string{"text": "fresh catalog gamma"},
	}); err != nil {
		t.Fatalf("mutation immediately after commit = %v", err)
	}
	if err := replacement.Abort(); err != nil {
		t.Fatalf("idempotent abort after commit = %v", err)
	}
	oldHits, err = manager.Search(context.Background(), "tenant", schema,
		MatchQuery{Field: "text", Text: "previous"}, SearchOptions{})
	if err != nil || len(oldHits) != 0 {
		t.Fatalf("replaced document remained visible = %#v, %v", oldHits, err)
	}
	newHits, err = manager.Search(context.Background(), "tenant", schema,
		MatchQuery{Field: "text", Text: "fresh"}, SearchOptions{Limit: 10})
	if err != nil || len(newHits) != 3 {
		t.Fatalf("published replacement hits = %#v, %v", newHits, err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewManager(directory, ManagerOptions{DisableAutoCompact: true})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	newHits, err = reopened.Search(context.Background(), "tenant", schema,
		MatchQuery{Field: "text", Text: "fresh"}, SearchOptions{Limit: 10})
	if err != nil || len(newHits) != 3 {
		t.Fatalf("reopened replacement hits = %#v, %v", newHits, err)
	}
}

func TestManagerReplacementBackpressureAndCancellation(t *testing.T) {
	analyzer := &managerMutationBlockingAnalyzer{entered: make(chan struct{})}
	schema, err := NewSchema(Text("text", analyzer))
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(t.TempDir(), ManagerOptions{
		MutationQueueSize: 1, DisableAutoCompact: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if _, err := manager.Add(context.Background(), "tenant", schema, Document{
		ID: "old", Fields: map[string]string{"text": "stable old"},
	}); err != nil {
		t.Fatal(err)
	}
	sessionCtx, cancelSession := context.WithCancel(context.Background())
	replacement, err := manager.BeginReplacement(sessionCtx, "tenant", schema)
	if err != nil {
		cancelSession()
		t.Fatal(err)
	}
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- replacement.Add(context.Background(), Document{
			ID: "slow", Fields: map[string]string{"text": "slow"},
		})
	}()
	select {
	case <-analyzer.entered:
	case <-time.After(time.Second):
		cancelSession()
		t.Fatal("replacement did not enter analyzer")
	}
	stats, exists := manager.IndexStats("tenant")
	if !exists || stats.PendingReplacementDocs != 1 || stats.PendingReplacementBytes == 0 ||
		manager.Stats().PendingReplacementBytes == 0 {
		cancelSession()
		t.Fatalf("replacement admission stats = %#v, exists=%v", stats, exists)
	}
	waitCtx, cancelWait := context.WithTimeout(context.Background(), 30*time.Millisecond)
	err = replacement.Add(waitCtx, Document{ID: "queued", Fields: map[string]string{"text": "queued"}})
	cancelWait()
	if !errors.Is(err, context.DeadlineExceeded) {
		cancelSession()
		t.Fatalf("replacement queue backpressure error = %v", err)
	}
	cancelSession()
	if err := <-firstDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled replacement add = %v", err)
	}
	waitForReplacementInactive(t, manager, "tenant")
	if err := replacement.Abort(); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Add(context.Background(), "tenant", schema, Document{
		ID: "after", Fields: map[string]string{"text": "recovered write"},
	}); err != nil {
		t.Fatalf("writer did not recover after replacement cancellation: %v", err)
	}
	hits, err := manager.Search(context.Background(), "tenant", schema,
		MatchQuery{Field: "text", Text: "stable"}, SearchOptions{})
	if err != nil || len(hits) != 1 || hits[0].ID != "old" {
		t.Fatalf("cancellation changed committed snapshot = %#v, %v", hits, err)
	}
}

func TestManagerReplacementDetectsCrossSegmentDuplicateAndAborts(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(t.TempDir(), ManagerOptions{
		Writer: WriterOptions{Segment: BuildOptions{MaxDocuments: 1}}, DisableAutoCompact: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	replacement, err := manager.BeginReplacement(context.Background(), "tenant", schema)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"first", "second"} {
		if err := replacement.Add(context.Background(), Document{
			ID: "duplicate", Fields: map[string]string{"text": text},
		}); err != nil {
			_ = replacement.Abort()
			t.Fatal(err)
		}
	}
	if _, err := replacement.Commit(context.Background()); !errors.Is(err, ErrPendingDocument) {
		_ = replacement.Abort()
		t.Fatalf("cross-segment duplicate commit = %v", err)
	}
	if _, err := manager.Add(context.Background(), "tenant", schema, Document{ID: "blocked"}); !errors.Is(err, ErrReplacementActive) {
		_ = replacement.Abort()
		t.Fatalf("failed commit released replacement ownership: %v", err)
	}
	if err := replacement.Abort(); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Add(context.Background(), "tenant", schema, Document{
		ID: "valid", Fields: map[string]string{"text": "usable after abort"},
	}); err != nil {
		t.Fatalf("writer unusable after duplicate abort: %v", err)
	}
}

func TestManagerBoundsConcurrentReplacements(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(t.TempDir(), ManagerOptions{
		MaxOpenIndexes: 2, MaxConcurrentReplacements: 1, DisableAutoCompact: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	first, err := manager.BeginReplacement(context.Background(), "tenant-a", schema)
	if err != nil {
		t.Fatal(err)
	}
	if stats := manager.Stats(); stats.ActiveReplacements != 1 {
		_ = first.Abort()
		t.Fatalf("active replacements = %#v", stats)
	}
	deadline, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	_, err = manager.BeginReplacement(deadline, "tenant-b", schema)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		_ = first.Abort()
		t.Fatalf("second replacement capacity error = %v", err)
	}
	if err := first.Abort(); err != nil {
		t.Fatal(err)
	}
	waitForReplacementInactive(t, manager, "tenant-b")
	second, err := manager.BeginReplacement(context.Background(), "tenant-b", schema)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Abort(); err != nil {
		t.Fatal(err)
	}
}

func TestManagerCloseAbortsStreamingReplacement(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	manager, err := NewManager(root, ManagerOptions{DisableAutoCompact: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Add(context.Background(), "tenant", schema, Document{
		ID: "old", Fields: map[string]string{"text": "committed old"},
	}); err != nil {
		t.Fatal(err)
	}
	replacement, err := manager.BeginReplacement(context.Background(), "tenant", schema)
	if err != nil {
		t.Fatal(err)
	}
	if err := replacement.Add(context.Background(), Document{
		ID: "new", Fields: map[string]string{"text": "uncommitted new"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := replacement.Commit(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("commit after manager close = %v", err)
	}
	index, err := OpenIndex(managedIndexDirectory(root, "tenant"), schema)
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	oldHits, err := index.Search(context.Background(), MatchQuery{Field: "text", Text: "committed"}, SearchOptions{})
	if err != nil || len(oldHits) != 1 || oldHits[0].ID != "old" {
		t.Fatalf("old snapshot after close = %#v, %v", oldHits, err)
	}
	newHits, err := index.Search(context.Background(), MatchQuery{Field: "text", Text: "uncommitted"}, SearchOptions{})
	if err != nil || len(newHits) != 0 {
		t.Fatalf("aborted replacement survived close = %#v, %v", newHits, err)
	}
}

func TestManagerQuarantinesUncertainStreamingReplacement(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	directory := managedIndexDirectory(root, "tenant")
	seed, err := NewIndexWriter(directory, schema, WriterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.Add(context.Background(), Document{
		ID: "old", Fields: map[string]string{"text": "stable old"},
	}); err != nil {
		_ = seed.Close()
		t.Fatal(err)
	}
	if _, err := seed.Commit(context.Background()); err != nil {
		_ = seed.Close()
		t.Fatal(err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}

	syncErr := errors.New("injected replacement directory sync failure")
	manager, err := NewManager(root, ManagerOptions{
		Writer:             WriterOptions{directorySync: func(string) error { return syncErr }},
		DisableAutoCompact: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	replacement, err := manager.BeginReplacement(context.Background(), "tenant", schema)
	if err != nil {
		t.Fatal(err)
	}
	if err := replacement.Add(context.Background(), Document{
		ID: "new", Fields: map[string]string{"text": "published uncertain"},
	}); err != nil {
		_ = replacement.Abort()
		t.Fatal(err)
	}
	info, err := replacement.Commit(context.Background())
	if !errors.Is(err, ErrIndexUnavailable) || !errors.Is(err, ErrDurabilityUncertain) || !errors.Is(err, syncErr) {
		t.Fatalf("uncertain replacement commit = %v", err)
	}
	if info.Generation != 2 || info.Documents != 1 {
		t.Fatalf("uncertain replacement info = %#v", info)
	}
	if _, err := manager.Add(context.Background(), "tenant", schema, Document{ID: "blocked"}); !errors.Is(err, ErrIndexUnavailable) || !errors.Is(err, ErrDurabilityUncertain) {
		t.Fatalf("uncertain replacement did not quarantine writer: %v", err)
	}
	stats, exists := manager.IndexStats("tenant")
	if !exists || stats.ReplacementActive || !stats.Failed || !strings.Contains(stats.Failure, syncErr.Error()) {
		t.Fatalf("uncertain replacement stats = %#v, exists=%v", stats, exists)
	}

	index, err := OpenIndex(directory, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	hits, err := index.Search(context.Background(), MatchQuery{Field: "text", Text: "uncertain"}, SearchOptions{})
	if err != nil || len(hits) != 1 || hits[0].ID != "new" {
		t.Fatalf("published uncertain replacement = %#v, %v", hits, err)
	}
}

func waitForReplacementInactive(t *testing.T, manager *Manager, key string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		stats, exists := manager.IndexStats(key)
		if exists && !stats.ReplacementActive && stats.PendingReplacementDocs == 0 &&
			stats.PendingReplacementBytes == 0 && stats.PendingWriteBytes == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	stats, _ := manager.IndexStats(key)
	t.Fatalf("replacement did not drain: %#v, manager=%#v", stats, manager.Stats())
}

type managerBlockingAnalyzer struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

type managerCloseBlockingAnalyzer struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

type managerMutationBlockingAnalyzer struct {
	entered chan struct{}
	once    sync.Once
}

type managerWriterBlockingAnalyzer struct {
	enteredA chan struct{}
	enteredB chan struct{}
	releaseA chan struct{}
	releaseB chan struct{}
	enterA   sync.Once
	enterB   sync.Once
	freeA    sync.Once
	freeB    sync.Once
}

func (analyzer *managerWriterBlockingAnalyzer) Identifier() string {
	return "manager-writer-blocking-v1"
}

func (analyzer *managerWriterBlockingAnalyzer) Analyze(ctx context.Context, text string, emit func(Token) bool) error {
	var release <-chan struct{}
	switch text {
	case "hold-a":
		analyzer.enterA.Do(func() { close(analyzer.enteredA) })
		release = analyzer.releaseA
	case "hold-b":
		analyzer.enterB.Do(func() { close(analyzer.enteredB) })
		release = analyzer.releaseB
	default:
		return StandardAnalyzer().Analyze(ctx, text, emit)
	}
	select {
	case <-release:
	case <-ctx.Done():
		return ctx.Err()
	}
	return StandardAnalyzer().Analyze(ctx, text, emit)
}

func (analyzer *managerWriterBlockingAnalyzer) releaseFirst() {
	analyzer.freeA.Do(func() { close(analyzer.releaseA) })
}

func (analyzer *managerWriterBlockingAnalyzer) releaseSecond() {
	analyzer.freeB.Do(func() { close(analyzer.releaseB) })
}

func (analyzer *managerWriterBlockingAnalyzer) releaseAll() {
	analyzer.releaseFirst()
	analyzer.releaseSecond()
}

func (analyzer *managerMutationBlockingAnalyzer) Identifier() string {
	return "manager-mutation-blocking-v1"
}

func (analyzer *managerMutationBlockingAnalyzer) Analyze(ctx context.Context, text string, emit func(Token) bool) error {
	if text == "slow" {
		analyzer.once.Do(func() { close(analyzer.entered) })
		<-ctx.Done()
		return ctx.Err()
	}
	return StandardAnalyzer().Analyze(ctx, text, emit)
}

func (analyzer *managerBlockingAnalyzer) Identifier() string {
	return "manager-blocking-v1"
}

func (analyzer *managerBlockingAnalyzer) Analyze(ctx context.Context, text string, emit func(Token) bool) error {
	if text == "hold" {
		analyzer.once.Do(func() { close(analyzer.entered) })
		select {
		case <-analyzer.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return StandardAnalyzer().Analyze(ctx, text, emit)
}

func (analyzer *managerCloseBlockingAnalyzer) Identifier() string {
	return "manager-close-blocking-v1"
}

func (analyzer *managerCloseBlockingAnalyzer) Analyze(ctx context.Context, text string, emit func(Token) bool) error {
	if text == "hold" {
		analyzer.once.Do(func() { close(analyzer.entered) })
		// Keep one snapshot leased until the test has started both CloseIndex
		// and the racing reopen operation.
		<-analyzer.release
	}
	return StandardAnalyzer().Analyze(ctx, text, emit)
}

func waitForManagerSearchState(t *testing.T, manager *Manager, active, waiting int64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		stats := manager.Stats()
		if stats.ActiveSearches == active && stats.WaitingSearches == waiting {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("search state = %#v, want active=%d waiting=%d", manager.Stats(), active, waiting)
}

func validateSnapshotIdentifiers(hits []Hit, expected int) error {
	if len(hits) != expected {
		return fmt.Errorf("snapshot returned %d hits, want %d", len(hits), expected)
	}
	seen := make(map[string]struct{}, len(hits))
	for _, hit := range hits {
		if !strings.HasPrefix(hit.ID, "doc-") {
			return fmt.Errorf("snapshot returned unexpected identifier %q", hit.ID)
		}
		if _, exists := seen[hit.ID]; exists {
			return fmt.Errorf("snapshot returned duplicate identifier %q", hit.ID)
		}
		seen[hit.ID] = struct{}{}
	}
	return nil
}
