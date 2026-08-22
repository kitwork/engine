package search

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestCommitAdoptsPublishedManifestOnDirectorySyncFailure(t *testing.T) {
	directory := t.TempDir()
	schema := crashTestSchema(t)
	writer, err := NewIndexWriter(directory, schema, WriterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Add(context.Background(), Document{
		ID: "old", Fields: map[string]string{"text": "old visible"},
	}); err != nil {
		t.Fatal(err)
	}
	base, err := writer.Commit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	syncErr := errors.New("injected directory sync failure")
	writer, err = NewIndexWriter(directory, schema, WriterOptions{
		directorySync: func(string) error { return syncErr },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Add(context.Background(), Document{
		ID: "new", Fields: map[string]string{"text": "new visible"},
	}); err != nil {
		t.Fatal(err)
	}
	info, err := writer.Commit(context.Background())
	if !errors.Is(err, syncErr) || !errors.Is(err, ErrDurabilityUncertain) {
		_ = writer.Close()
		t.Fatalf("commit error = %v, want durability uncertainty wrapping %v", err, syncErr)
	}
	if info.Generation != base.Generation+1 || info.Manifest == "" || writer.committed.generation != info.Generation {
		_ = writer.Close()
		t.Fatalf("published commit was not adopted: info=%#v committed=%d", info, writer.committed.generation)
	}
	assertCrashHits(t, writer.snapshot, "old", 1)
	assertCrashHits(t, writer.snapshot, "new", 1)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	reopened := verifyCrashedIndex(t, directory, schema, info.Generation)
	assertCrashHits(t, reopened, "new", 1)
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReplaceAllAdoptsPublishedManifestOnDirectorySyncFailure(t *testing.T) {
	directory := t.TempDir()
	schema := crashTestSchema(t)
	writer, err := NewIndexWriter(directory, schema, WriterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Add(context.Background(), Document{
		ID: "old", Fields: map[string]string{"text": "old visible"},
	}); err != nil {
		t.Fatal(err)
	}
	base, err := writer.Commit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	syncErr := errors.New("injected replacement directory sync failure")
	writer, err = NewIndexWriter(directory, schema, WriterOptions{
		directorySync: func(string) error { return syncErr },
	})
	if err != nil {
		t.Fatal(err)
	}
	info, err := writer.ReplaceAll(context.Background(), []Document{
		{ID: "new", Fields: map[string]string{"text": "new visible"}},
	})
	if !errors.Is(err, syncErr) || !errors.Is(err, ErrDurabilityUncertain) {
		_ = writer.Close()
		t.Fatalf("replacement error = %v, want durability uncertainty wrapping %v", err, syncErr)
	}
	if info.Generation != base.Generation+1 || info.Documents != 1 || info.PhysicalDocuments != 1 || writer.replacement != nil {
		_ = writer.Close()
		t.Fatalf("published replacement was not adopted: info=%#v replacement=%t", info, writer.replacement != nil)
	}
	assertCrashHits(t, writer.snapshot, "old", 0)
	assertCrashHits(t, writer.snapshot, "new", 1)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	reopened := verifyCrashedIndex(t, directory, schema, info.Generation)
	assertCrashHits(t, reopened, "old", 0)
	assertCrashHits(t, reopened, "new", 1)
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCompactionAdoptsPublishedManifestOnDirectorySyncFailure(t *testing.T) {
	directory := t.TempDir()
	schema := crashTestSchema(t)
	writer, err := NewIndexWriter(directory, schema, WriterOptions{
		Segment: BuildOptions{MaxDocuments: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	for document := 0; document < 2; document++ {
		if err := writer.Add(context.Background(), Document{
			ID: fmt.Sprintf("doc-%d", document), Fields: map[string]string{"text": "compact visible"},
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Commit(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	base := writer.currentInfoForCrashTest(t)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	syncErr := errors.New("injected directory sync failure")
	writer, err = NewIndexWriter(directory, schema, WriterOptions{
		directorySync: func(string) error { return syncErr },
	})
	if err != nil {
		t.Fatal(err)
	}
	info, compacted, err := writer.Compact(context.Background(), CompactOptions{Force: true})
	if !errors.Is(err, syncErr) || !errors.Is(err, ErrDurabilityUncertain) || !compacted {
		_ = writer.Close()
		t.Fatalf("compact result = %#v, %t, %v; want published sync error", info, compacted, err)
	}
	if info.Generation != base.Generation+1 || info.Segments != 1 || writer.committed.generation != info.Generation {
		_ = writer.Close()
		t.Fatalf("published compaction was not adopted: info=%#v committed=%d", info, writer.committed.generation)
	}
	assertCrashHits(t, writer.snapshot, "compact", 2)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	reopened := verifyCrashedIndex(t, directory, schema, info.Generation)
	if reopened.Info().Segments != 1 {
		_ = reopened.Close()
		t.Fatalf("reopened compacted segments = %d, want 1", reopened.Info().Segments)
	}
	assertCrashHits(t, reopened, "compact", 2)
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}

const (
	crashHelperEnvironment = "KITWORK_SEARCH_CRASH_HELPER"
	crashHelperDirectory   = "KITWORK_SEARCH_CRASH_DIRECTORY"
	crashHelperMode        = "KITWORK_SEARCH_CRASH_MODE"
	crashHelperPoint       = "KITWORK_SEARCH_CRASH_POINT"
	crashHelperReady       = "KITWORK_SEARCH_CRASH_READY"
)

func TestCommitCrashConsistency(t *testing.T) {
	testWriterCrashPoints(t, func(t *testing.T, directory string, point writerFaultPoint) {
		schema := crashTestSchema(t)
		writer, err := NewIndexWriter(directory, schema, WriterOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := writer.Add(context.Background(), Document{
			ID: "old", Fields: map[string]string{"text": "old common"},
		}); err != nil {
			_ = writer.Close()
			t.Fatal(err)
		}
		base, err := writer.Commit(context.Background())
		if err != nil {
			_ = writer.Close()
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}

		runWriterCrashHelper(t, directory, "commit", point)
		wantGeneration := base.Generation
		wantNew := 0
		if point == writerFaultAfterManifest {
			wantGeneration++
			wantNew = 1
		}
		index := verifyCrashedIndex(t, directory, schema, wantGeneration)
		assertCrashHits(t, index, "old", 1)
		assertCrashHits(t, index, "new", wantNew)
		_ = index.Close()

		reopened, err := NewIndexWriter(directory, schema, WriterOptions{})
		if err != nil {
			t.Fatalf("writer did not reopen after crash: %v", err)
		}
		garbage, err := reopened.GarbageCollect(context.Background(), GarbageCollectOptions{})
		if err != nil {
			_ = reopened.Close()
			t.Fatal(err)
		}
		if point == writerFaultBeforeManifest && (garbage.RemovedSegments == 0 || garbage.RemovedSidecars == 0) {
			_ = reopened.Close()
			t.Fatalf("pre-manifest crash orphans were not reclaimed: %#v", garbage)
		}
		if err := reopened.Close(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestReplaceAllCrashConsistency(t *testing.T) {
	testWriterCrashPoints(t, func(t *testing.T, directory string, point writerFaultPoint) {
		schema := crashTestSchema(t)
		writer, err := NewIndexWriter(directory, schema, WriterOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := writer.Add(context.Background(), Document{
			ID: "old", Fields: map[string]string{"text": "old common"},
		}); err != nil {
			_ = writer.Close()
			t.Fatal(err)
		}
		base, err := writer.Commit(context.Background())
		if err != nil {
			_ = writer.Close()
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}

		runWriterCrashHelper(t, directory, "replace", point)
		wantGeneration := base.Generation
		wantOld, wantNew := 1, 0
		if point == writerFaultAfterManifest {
			wantGeneration++
			wantOld, wantNew = 0, 1
		}
		index := verifyCrashedIndex(t, directory, schema, wantGeneration)
		assertCrashHits(t, index, "old", wantOld)
		assertCrashHits(t, index, "new", wantNew)
		_ = index.Close()

		reopened, err := NewIndexWriter(directory, schema, WriterOptions{})
		if err != nil {
			t.Fatalf("writer did not reopen after replacement crash: %v", err)
		}
		garbage, err := reopened.GarbageCollect(context.Background(), GarbageCollectOptions{})
		if err != nil {
			_ = reopened.Close()
			t.Fatal(err)
		}
		if point == writerFaultBeforeManifest && (garbage.RemovedSegments == 0 || garbage.RemovedSidecars == 0) {
			_ = reopened.Close()
			t.Fatalf("pre-manifest replacement orphans were not reclaimed: %#v", garbage)
		}
		if err := reopened.Close(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestCompactionCrashConsistency(t *testing.T) {
	testWriterCrashPoints(t, func(t *testing.T, directory string, point writerFaultPoint) {
		schema := crashTestSchema(t)
		writer, err := NewIndexWriter(directory, schema, WriterOptions{
			Segment: BuildOptions{MaxDocuments: 1},
		})
		if err != nil {
			t.Fatal(err)
		}
		for document := 0; document < 2; document++ {
			if err := writer.Add(context.Background(), Document{
				ID:     fmt.Sprintf("doc-%d", document),
				Fields: map[string]string{"text": "compact common"},
			}); err != nil {
				_ = writer.Close()
				t.Fatal(err)
			}
			if _, err := writer.Commit(context.Background()); err != nil {
				_ = writer.Close()
				t.Fatal(err)
			}
		}
		base := writer.currentInfoForCrashTest(t)
		if base.Segments != 2 {
			_ = writer.Close()
			t.Fatalf("compaction fixture segments = %d", base.Segments)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}

		runWriterCrashHelper(t, directory, "compact", point)
		wantGeneration := base.Generation
		wantSegments := 2
		if point == writerFaultAfterManifest {
			wantGeneration++
			wantSegments = 1
		}
		index := verifyCrashedIndex(t, directory, schema, wantGeneration)
		if info := index.Info(); info.Segments != wantSegments || info.Documents != 2 {
			_ = index.Close()
			t.Fatalf("compaction crash info = %#v, want segments=%d documents=2", info, wantSegments)
		}
		assertCrashHits(t, index, "common", 2)
		_ = index.Close()

		reopened, err := NewIndexWriter(directory, schema, WriterOptions{})
		if err != nil {
			t.Fatalf("writer did not reopen after compaction crash: %v", err)
		}
		garbage, err := reopened.GarbageCollect(context.Background(), GarbageCollectOptions{})
		if err != nil {
			_ = reopened.Close()
			t.Fatal(err)
		}
		if point == writerFaultBeforeManifest && (garbage.RemovedSegments == 0 || garbage.RemovedSidecars == 0) {
			_ = reopened.Close()
			t.Fatalf("pre-manifest compact orphans were not reclaimed: %#v", garbage)
		}
		if err := reopened.Close(); err != nil {
			t.Fatal(err)
		}
	})
}

func testWriterCrashPoints(
	t *testing.T,
	test func(*testing.T, string, writerFaultPoint),
) {
	t.Helper()
	for _, point := range []writerFaultPoint{writerFaultBeforeManifest, writerFaultAfterManifest} {
		point := point
		t.Run(crashFaultPointName(point), func(t *testing.T) {
			test(t, t.TempDir(), point)
		})
	}
}

func runWriterCrashHelper(t *testing.T, directory, mode string, point writerFaultPoint) {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestWriterCrashPublicationHelper$")
	command.Env = append(os.Environ(),
		crashHelperEnvironment+"=1",
		crashHelperDirectory+"="+directory,
		crashHelperMode+"="+mode,
		crashHelperPoint+"="+crashFaultPointName(point),
	)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	running := true
	defer func() {
		if running {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()
	ready := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if strings.TrimSpace(scanner.Text()) == crashHelperReady {
				ready <- true
				return
			}
		}
		ready <- false
	}()
	select {
	case ok := <-ready:
		if !ok {
			_ = command.Wait()
			running = false
			t.Fatalf("crash helper exited before fault point: %s", stderr.String())
		}
	case <-time.After(10 * time.Second):
		_ = command.Process.Kill()
		_ = command.Wait()
		running = false
		t.Fatalf("crash helper did not reach fault point: %s", stderr.String())
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()
	running = false
}

func TestWriterCrashPublicationHelper(t *testing.T) {
	if os.Getenv(crashHelperEnvironment) != "1" {
		return
	}
	directory := os.Getenv(crashHelperDirectory)
	mode := os.Getenv(crashHelperMode)
	point, ok := parseCrashFaultPoint(os.Getenv(crashHelperPoint))
	if directory == "" || !ok {
		t.Fatal("invalid crash helper configuration")
	}
	schema := crashTestSchema(t)
	writer, err := NewIndexWriter(directory, schema, WriterOptions{
		fault: func(hit writerFaultPoint) {
			if hit != point {
				return
			}
			_, _ = fmt.Fprintln(os.Stdout, crashHelperReady)
			select {}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	switch mode {
	case "commit":
		if err := writer.Add(context.Background(), Document{
			ID: "new", Fields: map[string]string{"text": "new common"},
		}); err != nil {
			t.Fatal(err)
		}
		_, err = writer.Commit(context.Background())
	case "replace":
		_, err = writer.ReplaceAll(context.Background(), []Document{
			{ID: "new", Fields: map[string]string{"text": "new common"}},
		})
	case "compact":
		_, _, err = writer.Compact(context.Background(), CompactOptions{
			Force: true, MaximumInputDocuments: 100,
		})
	default:
		t.Fatalf("unknown crash helper mode %q", mode)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Fatal("crash helper operation completed without reaching its fault point")
}

func crashTestSchema(t testing.TB) Schema {
	t.Helper()
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func verifyCrashedIndex(t testing.TB, directory string, schema Schema, generation uint64) *Index {
	t.Helper()
	index, err := OpenIndex(directory, schema)
	if err != nil {
		t.Fatalf("open index after crash: %v", err)
	}
	if info := index.Info(); info.Generation != generation {
		_ = index.Close()
		t.Fatalf("generation after crash = %d, want %d", info.Generation, generation)
	}
	if err := index.Verify(context.Background()); err != nil {
		_ = index.Close()
		t.Fatalf("verify index after crash: %v", err)
	}
	return index
}

func assertCrashHits(t testing.TB, index *Index, query string, want int) {
	t.Helper()
	hits, err := index.Search(context.Background(), MatchQuery{Field: "text", Text: query}, SearchOptions{Limit: 10})
	if err != nil || len(hits) != want {
		t.Fatalf("crash query %q hits = %#v, %v; want %d", query, hits, err, want)
	}
}

func crashFaultPointName(point writerFaultPoint) string {
	switch point {
	case writerFaultBeforeManifest:
		return "before-manifest"
	case writerFaultAfterManifest:
		return "after-manifest"
	default:
		return "unknown"
	}
}

func parseCrashFaultPoint(name string) (writerFaultPoint, bool) {
	switch name {
	case "before-manifest":
		return writerFaultBeforeManifest, true
	case "after-manifest":
		return writerFaultAfterManifest, true
	default:
		return 0, false
	}
}

func (writer *IndexWriter) currentInfoForCrashTest(t testing.TB) IndexInfo {
	t.Helper()
	info, err := manifestIndexInfo(writer.directory, writer.committed)
	if err != nil {
		t.Fatal(err)
	}
	return info
}
