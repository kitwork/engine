package search

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

type containerState struct {
	Revision int `json:"revision"`
}

// containerHistory is a deterministic source: initial documents, then one
// batch of latest-per-identifier changes per revision.
type containerHistory struct {
	initial   []Document
	revisions [][]Change
}

func newContainerHistory(seed uint64, documents, revisions int) containerHistory {
	random := rand.New(rand.NewPCG(seed, 27))
	var history containerHistory
	live := map[string]bool{}
	for number := 0; number < documents; number++ {
		document := Document{ID: fmt.Sprintf("p%04d", number), Fields: liveFields(random)}
		history.initial = append(history.initial, document)
		live[document.ID] = true
	}
	next := documents
	for revision := 0; revision < revisions; revision++ {
		changes := map[string]Change{}
		var order []string
		for count := 1 + random.IntN(12); count > 0; count-- {
			var id string
			switch roll := random.IntN(10); {
			case roll < 5:
				id = fmt.Sprintf("p%04d", random.IntN(next))
				changes[id] = Change{ID: id, Fields: liveFields(random)}
			case roll < 7:
				id = fmt.Sprintf("p%04d", next)
				next++
				changes[id] = Change{ID: id, Fields: liveFields(random)}
			default:
				id = fmt.Sprintf("p%04d", random.IntN(next))
				changes[id] = Change{ID: id, Delete: true}
			}
			order = append(order, id)
		}
		var batch []Change
		emitted := map[string]bool{}
		for position := len(order) - 1; position >= 0; position-- {
			if !emitted[order[position]] {
				emitted[order[position]] = true
				batch = append(batch, changes[order[position]])
			}
		}
		sort.Slice(batch, func(i, j int) bool { return batch[i].ID < batch[j].ID })
		history.revisions = append(history.revisions, batch)
	}
	return history
}

// stateAt is the source at revision: identifier → fields.
func (history containerHistory) stateAt(revision int) map[string]map[string]string {
	state := map[string]map[string]string{}
	for _, document := range history.initial {
		state[document.ID] = document.Fields
	}
	for _, batch := range history.revisions[:revision] {
		for _, change := range batch {
			if change.Delete {
				delete(state, change.ID)
			} else {
				state[change.ID] = change.Fields
			}
		}
	}
	return state
}

// compareWithSource checks index against an index rebuilt from the source at
// revision, numbered in the order the container holds its documents.
func compareWithSource(t *testing.T, label string, schema Schema, index *Index, history containerHistory, revision int) {
	t.Helper()
	state := history.stateAt(revision)
	order := liveIdentifiersInOrder(t, index)
	if len(order) != len(state) {
		t.Fatalf("%s: %d live documents, the source has %d", label, len(order), len(state))
	}
	documents := make([]Document, 0, len(order))
	for _, identifier := range order {
		fields, exists := state[identifier]
		if !exists {
			t.Fatalf("%s: the index holds %q, which the source does not", label, identifier)
		}
		documents = append(documents, Document{ID: identifier, Fields: fields})
	}
	compareLiveIndexes(t, label, index, rebuiltLiveIndex(t, schema, documents))
}

func replaceFromHistory(t *testing.T, container *Container, name string, schema Schema, history containerHistory, options BuildOptions) {
	t.Helper()
	if err := container.Replace(context.Background(), name, schema, containerState{}, options, func(add func(Document) error) error {
		for _, document := range history.initial {
			if err := add(document); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestContainerMatchesARebuiltIndexAfterEveryFold(t *testing.T) {
	ctx := context.Background()
	schema := productSchema(t)
	path := filepath.Join(t.TempDir(), "main.db.search")
	container, err := OpenContainer(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer container.Close()
	history := newContainerHistory(1, 300, 60)
	options := BuildOptions{MaxDocuments: 10}
	replaceFromHistory(t, container, "products", schema, history, options)
	check := func(revision int) {
		index, state, err := container.Index("products", schema)
		if err != nil {
			t.Fatal(err)
		}
		defer index.Close()
		if !strings.Contains(string(state), fmt.Sprintf(`"revision":%d`, revision)) {
			t.Fatalf("revision %d: state %s", revision, state)
		}
		if len(index.segments) > containerMaximumSegments {
			t.Fatalf("revision %d: %d segments", revision, len(index.segments))
		}
		compareWithSource(t, fmt.Sprintf("revision %d", revision), schema, index, history, revision)
	}
	check(0)
	for revision := 1; revision <= len(history.revisions); revision++ {
		if err := container.Fold(ctx, "products", schema, containerState{Revision: revision}, options, history.revisions[revision-1]); err != nil {
			t.Fatal(err)
		}
		check(revision)
	}
	if info := container.Info(); info.Number < 60 {
		t.Fatalf("only %d generations were published", info.Number)
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Fatalf("the index is %d files, not one", len(entries))
	}
}

func TestContainerKeepsIndexesApartAndReopens(t *testing.T) {
	ctx := context.Background()
	schema := productSchema(t)
	path := filepath.Join(t.TempDir(), "main.db.search")
	container, err := OpenContainer(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	first, second := newContainerHistory(2, 120, 10), newContainerHistory(3, 80, 10)
	replaceFromHistory(t, container, "products", schema, first, BuildOptions{MaxDocuments: 16})
	replaceFromHistory(t, container, "posts", schema, second, BuildOptions{MaxDocuments: 16})
	for revision := 1; revision <= 10; revision++ {
		if err := container.Fold(ctx, "products", schema, containerState{Revision: revision}, BuildOptions{}, first.revisions[revision-1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := container.Close(); err != nil {
		t.Fatal(err)
	}
	container, err = OpenContainer(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer container.Close()
	products, _, err := container.Index("products", schema)
	if err != nil {
		t.Fatal(err)
	}
	defer products.Close()
	compareWithSource(t, "products after reopen", schema, products, first, 10)
	posts, _, err := container.Index("posts", schema)
	if err != nil {
		t.Fatal(err)
	}
	defer posts.Close()
	compareWithSource(t, "posts, never folded", schema, posts, second, 0)
	if _, _, err := container.Index("missing", schema); !errors.Is(err, ErrIndexNotFound) {
		t.Fatalf("missing index: %v", err)
	}
}

func TestContainerCompactionWaitsForOpenIndexesAndShrinks(t *testing.T) {
	ctx := context.Background()
	schema := productSchema(t)
	path := filepath.Join(t.TempDir(), "main.db.search")
	container, err := OpenContainer(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer container.Close()
	history := newContainerHistory(4, 200, 40)
	replaceFromHistory(t, container, "products", schema, history, BuildOptions{MaxDocuments: 25})
	for revision := 1; revision <= 40; revision++ {
		if err := container.Fold(ctx, "products", schema, containerState{Revision: revision}, BuildOptions{}, history.revisions[revision-1]); err != nil {
			t.Fatal(err)
		}
	}
	if info := container.Info(); info.ObsoleteBytes <= info.LiveBytes {
		t.Fatalf("40 folds left too few obsolete bytes to compact: %+v", info)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	held, _, err := container.Index("products", schema)
	if err != nil {
		t.Fatal(err)
	}
	compaction, err := container.PrepareCompaction(ctx)
	if err != nil {
		t.Fatal(err)
	}
	compacted, _, err := compaction.Index("products", schema)
	if err != nil {
		t.Fatal(err)
	}
	committed := make(chan error, 1)
	go func() { committed <- compaction.Commit(ctx) }()
	select {
	case err := <-committed:
		t.Fatalf("compaction committed while an index of the old file was open: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	compareWithSource(t, "old index during compaction", schema, held, history, 40)
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-committed; err != nil {
		t.Fatal(err)
	}
	compareWithSource(t, "compacted index", schema, compacted, history, 40)
	compacted.Close()
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() >= before.Size() {
		t.Fatalf("compaction left %d bytes of %d", after.Size(), before.Size())
	}
	// The moved file keeps serving folds and reopens.
	if err := container.Fold(ctx, "products", schema, containerState{Revision: 40}, BuildOptions{}, []Change{{ID: "p0000", Delete: true}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".compact"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the compacted file is still beside the container: %v", err)
	}
}

func TestLiveOverAContainerRebasesAcrossGenerations(t *testing.T) {
	ctx := context.Background()
	schema := productSchema(t)
	container, err := OpenContainer(ctx, filepath.Join(t.TempDir(), "main.db.search"))
	if err != nil {
		t.Fatal(err)
	}
	defer container.Close()
	history := newContainerHistory(5, 200, 30)
	replaceFromHistory(t, container, "products", schema, history, BuildOptions{MaxDocuments: 32})
	base, _, err := container.Index("products", schema)
	if err != nil {
		t.Fatal(err)
	}
	live, err := NewLive(base, 0, LiveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	for revision := 1; revision <= 30; revision++ {
		if err := live.Apply(ctx, uint64(revision), history.revisions[revision-1]); err != nil {
			t.Fatal(err)
		}
		view, err := live.View(ctx, uint64(revision))
		if err != nil {
			t.Fatal(err)
		}
		compareWithSource(t, fmt.Sprintf("live revision %d", revision), schema, view.Index, history, revision)
		view.Close()
		if revision%10 == 0 {
			changes, err := live.Changes(uint64(revision))
			if err != nil {
				t.Fatal(err)
			}
			if err := container.Fold(ctx, "products", schema, containerState{Revision: revision}, BuildOptions{}, changes); err != nil {
				t.Fatal(err)
			}
			next, _, err := container.Index("products", schema)
			if err != nil {
				t.Fatal(err)
			}
			if err := live.Rebase(next, uint64(revision)); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// TestContainerFoldChild folds forever; the crash test kills it.
func TestContainerFoldChild(t *testing.T) {
	path := os.Getenv("KITWORK_SEARCH_CONTAINER_CHILD")
	if path == "" {
		t.Skip("runs as the child of TestKilledFoldLeavesAWholeGeneration")
	}
	ctx := context.Background()
	schema := productSchema(t)
	container, err := OpenContainer(ctx, path)
	if err != nil {
		os.Exit(3)
	}
	history := newContainerHistory(6, 150, 400)
	_, state, err := container.Index("products", schema)
	if err != nil {
		os.Exit(4)
	}
	revision := 0
	fmt.Sscanf(string(state), `{"revision":%d}`, &revision)
	fmt.Println("ready")
	for revision++; revision <= len(history.revisions); revision++ {
		if err := container.Fold(ctx, "products", schema, containerState{Revision: revision}, BuildOptions{}, history.revisions[revision-1]); err != nil {
			os.Exit(5)
		}
	}
	os.Exit(0)
}

func TestKilledFoldLeavesAWholeGeneration(t *testing.T) {
	if testing.Short() {
		t.Skip("starts and kills child processes")
	}
	ctx := context.Background()
	schema := productSchema(t)
	path := filepath.Join(t.TempDir(), "main.db.search")
	container, err := OpenContainer(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	history := newContainerHistory(6, 150, 400)
	replaceFromHistory(t, container, "products", schema, history, BuildOptions{MaxDocuments: 20})
	if err := container.Close(); err != nil {
		t.Fatal(err)
	}
	previous := 0
	for round := 0; round < 4; round++ {
		child := exec.Command(os.Args[0], "-test.run=^TestContainerFoldChild$", "-test.count=1")
		child.Env = append(os.Environ(), "KITWORK_SEARCH_CONTAINER_CHILD="+path)
		output, err := child.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		buffer := make([]byte, 16)
		if count, err := output.Read(buffer); err != nil || !strings.Contains(string(buffer[:count]), "ready") {
			_ = child.Process.Kill()
			t.Fatalf("child did not start: %q %v", buffer[:count], err)
		}
		time.Sleep(time.Duration(80+round*90) * time.Millisecond)
		_ = child.Process.Kill()
		_ = child.Wait()

		container, err := OpenContainer(ctx, path)
		if err != nil {
			t.Fatalf("round %d: reopen after kill: %v", round, err)
		}
		index, state, err := container.Index("products", schema)
		if err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		revision := 0
		fmt.Sscanf(string(state), `{"revision":%d}`, &revision)
		if revision <= previous && revision < len(history.revisions) {
			t.Fatalf("round %d: the child published nothing (revision %d)", round, revision)
		}
		previous = revision
		compareWithSource(t, "after kill at revision "+strconv.Itoa(revision), schema, index, history, revision)
		index.Close()
		if err := container.Close(); err != nil {
			t.Fatal(err)
		}
		t.Logf("round %d: whole generation at revision %d", round, revision)
	}
}
