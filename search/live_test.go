package search

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var liveWords = []string{
	"áo", "thun", "nam", "nữ", "đồng", "hồ", "điện", "thoại", "sữa", "rửa", "mặt", "bình", "giữ", "nhiệt",
	"kem", "chống", "nắng", "ốp", "lưng", "tai", "nghe", "cotton", "xanh", "đỏ", "trắng", "giày", "dép", "túi",
}

func liveText(random *rand.Rand, minimum, maximum int) string {
	words := make([]string, minimum+random.IntN(maximum-minimum+1))
	for position := range words {
		words[position] = liveWords[random.IntN(len(liveWords))]
	}
	return strings.Join(words, " ")
}

func liveFields(random *rand.Rand) map[string]string {
	return map[string]string{"title": liveText(random, 1, 5), "body": liveText(random, 3, 16)}
}

func liveQueries() []struct {
	name  string
	query MatchQuery
} {
	both := []string{"title", "body"}
	return []struct {
		name  string
		query MatchQuery
	}{
		{"title and", MatchQuery{Field: "title", Text: "áo thun"}},
		{"fields and", MatchQuery{Fields: both, Text: "ao thun"}},
		{"body single", MatchQuery{Field: "body", Text: "nhiet"}},
		{"fields any", MatchQuery{Fields: both, Text: "điện thoại kem", Operator: QueryAny}},
		{"title any", MatchQuery{Field: "title", Text: "dong ho", Operator: QueryAny}},
		{"phrase", MatchQuery{Field: "title", Phrase: "áo thun"}},
		{"prefix", MatchQuery{Fields: both, Prefix: "th"}},
	}
}

// writeLiveIndex commits documents, in order, into a new index directory.
func writeLiveIndex(t *testing.T, directory string, schema Schema, documents []Document, segment uint32) *IndexWriter {
	t.Helper()
	writer, err := NewIndexWriter(directory, schema, WriterOptions{Segment: BuildOptions{MaxDocuments: segment}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, document := range documents {
		if err := writer.Add(ctx, document); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := writer.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return writer
}

// liveIdentifiersInOrder lists live identifiers in physical (ordinal) order.
func liveIdentifiersInOrder(t *testing.T, index *Index) []string {
	t.Helper()
	var identifiers []string
	for position, segment := range index.segments {
		for document := uint32(0); document < segment.header.documentN; document++ {
			if index.deletions[position].Contains(document) {
				continue
			}
			identifier, err := segment.documentIdentifier(document)
			if err != nil {
				t.Fatal(err)
			}
			identifiers = append(identifiers, identifier)
		}
	}
	return identifiers
}

type liveHistory struct {
	baseOrder []string
	revisions [][]Change // revisions[r-1] is revision r
}

// stateAt replays the history and returns the documents a rebuilt index at
// revision holds, in the order a live view numbers them: surviving base
// documents in base order, then each changed document at its latest change.
func (history liveHistory) stateAt(base map[string]map[string]string, revision int) []Document {
	state := make(map[string]map[string]string, len(base))
	for identifier, fields := range base {
		state[identifier] = fields
	}
	var changed []liveChange
	for r := 1; r <= revision; r++ {
		for _, change := range history.revisions[r-1] {
			changed = append(changed, liveChange{revision: uint64(r), change: change})
			if change.Delete {
				delete(state, change.ID)
			} else {
				state[change.ID] = change.Fields
			}
		}
	}
	touched := make(map[string]bool)
	for _, entry := range changed {
		touched[entry.change.ID] = true
	}
	var documents []Document
	for _, identifier := range history.baseOrder {
		if !touched[identifier] {
			documents = append(documents, Document{ID: identifier, Fields: state[identifier]})
		}
	}
	for _, entry := range latestLiveChanges(changed) {
		if !entry.change.Delete {
			documents = append(documents, Document{ID: entry.change.ID, Fields: entry.change.Fields})
		}
	}
	return documents
}

func compareLiveIndexes(t *testing.T, label string, got, want *Index) {
	t.Helper()
	ctx := context.Background()
	if got.Info().Documents != want.Info().Documents {
		t.Fatalf("%s: %d live documents, rebuilt index has %d", label, got.Info().Documents, want.Info().Documents)
	}
	for _, query := range liveQueries() {
		for _, limit := range []int{7, 1000} {
			gotHits, gotErr := got.Search(ctx, query.query, SearchOptions{Limit: limit})
			wantHits, wantErr := want.Search(ctx, query.query, SearchOptions{Limit: limit})
			if (gotErr != nil) != (wantErr != nil) {
				t.Fatalf("%s %s: error %v, rebuilt %v", label, query.name, gotErr, wantErr)
			}
			if len(gotHits) != len(wantHits) {
				t.Fatalf("%s %s limit %d: %d hits, rebuilt index has %d", label, query.name, limit, len(gotHits), len(wantHits))
			}
			for position := range gotHits {
				g, w := gotHits[position], wantHits[position]
				if g.ID != w.ID || math.Abs(g.Score-w.Score) > 1e-9*math.Max(1, math.Abs(w.Score)) {
					t.Fatalf("%s %s limit %d hit %d: %s %.12f, rebuilt %s %.12f",
						label, query.name, limit, position, g.ID, g.Score, w.ID, w.Score)
				}
			}
		}
		if query.query.Phrase == "" && query.query.Prefix == "" && query.query.Operator == QueryAll {
			gotCount, err := got.Count(ctx, query.query, nil)
			if err != nil {
				t.Fatal(err)
			}
			wantCount, err := want.Count(ctx, query.query, nil)
			if err != nil {
				t.Fatal(err)
			}
			if gotCount != wantCount {
				t.Fatalf("%s %s: count %d, rebuilt %d", label, query.name, gotCount, wantCount)
			}
		}
	}
}

func rebuiltLiveIndex(t *testing.T, schema Schema, documents []Document) *Index {
	t.Helper()
	directory := t.TempDir()
	writer := writeLiveIndex(t, directory, schema, documents, 1<<20)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	index, err := OpenIndex(directory, schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = index.Close() })
	return index
}

// newLiveFixture commits a base with several segments, tombstones and updated
// documents, then derives a history of random changes after it.
func newLiveFixture(t *testing.T, seed uint64, revisions int) (Schema, string, *IndexWriter, *Index, map[string]map[string]string, liveHistory) {
	t.Helper()
	schema := productSchema(t)
	random := rand.New(rand.NewPCG(seed, 2026))
	ctx := context.Background()
	directory := filepath.Join(t.TempDir(), "base")
	state := make(map[string]map[string]string)
	var documents []Document
	for number := 0; number < 300; number++ {
		document := Document{ID: fmt.Sprintf("p%03d", number), Fields: liveFields(random)}
		documents = append(documents, document)
		state[document.ID] = document.Fields
	}
	writer := writeLiveIndex(t, directory, schema, documents, 64)
	for number := 0; number < 15; number++ {
		identifier := fmt.Sprintf("p%03d", random.IntN(300))
		if _, err := writer.Delete(ctx, identifier); err != nil && !errors.Is(err, ErrPendingDocument) {
			t.Fatal(err)
		}
		delete(state, identifier)
	}
	for number := 0; number < 10; number++ {
		identifier := fmt.Sprintf("p%03d", random.IntN(300))
		if _, live := state[identifier]; !live {
			continue
		}
		fields := liveFields(random)
		if err := writer.Upsert(ctx, Document{ID: identifier, Fields: fields}); err != nil {
			if errors.Is(err, ErrPendingDocument) {
				continue
			}
			t.Fatal(err)
		}
		state[identifier] = fields
	}
	if _, err := writer.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	base, err := OpenIndex(directory, schema)
	if err != nil {
		t.Fatal(err)
	}
	if base.Info().Deleted == 0 || len(base.segments) < 3 {
		t.Fatalf("fixture base needs tombstones and several segments: %+v", base.Info())
	}

	history := liveHistory{baseOrder: liveIdentifiersInOrder(t, base)}
	current := make(map[string]bool, len(state))
	for identifier := range state {
		current[identifier] = true
	}
	next := 0
	for revision := 1; revision <= revisions; revision++ {
		var changes []Change
		for count := 1 + random.IntN(4); count > 0; count-- {
			var liveIDs []string
			for identifier := range current {
				liveIDs = append(liveIDs, identifier)
			}
			sortStrings(liveIDs)
			switch roll := random.IntN(100); {
			case roll < 45 && len(liveIDs) > 0:
				changes = append(changes, Change{ID: liveIDs[random.IntN(len(liveIDs))], Fields: liveFields(random)})
			case roll < 70:
				identifier := fmt.Sprintf("n%03d", next)
				next++
				changes = append(changes, Change{ID: identifier, Fields: liveFields(random)})
				current[identifier] = true
			case roll < 90 && len(liveIDs) > 0:
				identifier := liveIDs[random.IntN(len(liveIDs))]
				changes = append(changes, Change{ID: identifier, Delete: true})
				delete(current, identifier)
			default:
				identifier := fmt.Sprintf("p%03d", random.IntN(300))
				if current[identifier] {
					changes = append(changes, Change{ID: identifier, Delete: true})
					delete(current, identifier)
				} else {
					changes = append(changes, Change{ID: identifier, Fields: liveFields(random)})
					current[identifier] = true
				}
			}
		}
		if revision%11 == 0 {
			changes = nil // a revision that touched no searchable document
		}
		history.revisions = append(history.revisions, changes)
	}
	return schema, directory, writer, base, state, history
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

func TestLiveViewMatchesRebuiltIndexAtEveryRevision(t *testing.T) {
	ctx := context.Background()
	for _, seed := range []uint64{1, 2, 3} {
		schema, _, writer, base, state, history := newLiveFixture(t, seed, 40)
		live, err := NewLive(base, 0, LiveOptions{})
		if err != nil {
			t.Fatal(err)
		}
		check := func(revision int) {
			view, err := live.View(ctx, uint64(revision))
			if err != nil {
				t.Fatal(err)
			}
			defer view.Close()
			compareLiveIndexes(t, fmt.Sprintf("seed %d revision %d", seed, revision), view.Index,
				rebuiltLiveIndex(t, schema, history.stateAt(state, revision)))
		}
		check(0)
		for revision := 1; revision <= len(history.revisions); revision++ {
			if err := live.Apply(ctx, uint64(revision), history.revisions[revision-1]); err != nil {
				t.Fatal(err)
			}
			check(revision)
			if revision > 3 && revision%7 == 0 {
				check(revision - 3) // an older revision still answers exactly
			}
		}
		if err := live.Close(); err != nil {
			t.Fatal(err)
		}
		if !base.closed.Load() {
			t.Fatal("closing Live with no open view left its base open")
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// Control: the same view without tombstones for replaced base documents must
// disagree with the rebuilt index, or the comparison above proves nothing.
func TestLiveViewWithoutReplacementTombstonesDisagrees(t *testing.T) {
	ctx := context.Background()
	schema, _, writer, base, state, history := newLiveFixture(t, 1, 40)
	defer writer.Close()
	live, err := NewLive(base, 0, LiveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	var changes []liveChange
	for revision, revisionChanges := range history.revisions {
		if err := live.Apply(ctx, uint64(revision+1), revisionChanges); err != nil {
			t.Fatal(err)
		}
	}
	changes = live.changes
	piece, err := buildLivePiece(ctx, schema, live.options, changes)
	if err != nil {
		t.Fatal(err)
	}
	untombstoned, err := assembleLiveIndex(base, make([]map[uint32]struct{}, len(base.segments)),
		[]*livePiece{piece}, make([]map[uint32]struct{}, 1))
	if err != nil {
		t.Fatal(err)
	}
	defer untombstoned.Close()
	want := rebuiltLiveIndex(t, schema, history.stateAt(state, len(history.revisions)))
	if untombstoned.Info().Documents == want.Info().Documents {
		t.Fatal("control kept the rebuilt document count; the history changed no base document")
	}
	differs := false
	for _, query := range liveQueries() {
		gotHits, _ := untombstoned.Search(ctx, query.query, SearchOptions{Limit: 1000})
		wantHits, _ := want.Search(ctx, query.query, SearchOptions{Limit: 1000})
		if len(gotHits) != len(wantHits) {
			differs = true
			break
		}
		for position := range gotHits {
			if gotHits[position].ID != wantHits[position].ID || gotHits[position].Score != wantHits[position].Score {
				differs = true
			}
		}
	}
	if !differs {
		t.Fatal("control without tombstones matched the rebuilt index; the comparison is not sensitive")
	}
}

func TestLiveRebaseKeepsOpenViewsAndDropsFoldedChanges(t *testing.T) {
	ctx := context.Background()
	schema, directory, writer, base, state, history := newLiveFixture(t, 4, 40)
	live, err := NewLive(base, 0, LiveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	for revision := 1; revision <= 30; revision++ {
		if err := live.Apply(ctx, uint64(revision), history.revisions[revision-1]); err != nil {
			t.Fatal(err)
		}
	}
	held, err := live.View(ctx, 25)
	if err != nil {
		t.Fatal(err)
	}

	folded, err := live.Changes(30)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range folded {
		if change.Delete {
			if _, err := writer.Delete(ctx, change.ID); err != nil {
				t.Fatal(err)
			}
		} else if err := writer.Upsert(ctx, Document{ID: change.ID, Fields: change.Fields}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := writer.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := OpenIndex(directory, schema)
	if err != nil {
		t.Fatal(err)
	}
	for revision := 31; revision <= 40; revision++ {
		if err := live.Apply(ctx, uint64(revision), history.revisions[revision-1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := live.Rebase(next, 30); err != nil {
		t.Fatal(err)
	}
	if live.BaseRevision() != 30 || live.Pending() != countChanges(history.revisions[30:]) {
		t.Fatalf("rebase kept base %d and %d changes", live.BaseRevision(), live.Pending())
	}

	// The view taken before Rebase still answers revision 25 from the old base.
	compareLiveIndexes(t, "held revision 25", held.Index, rebuiltLiveIndex(t, schema, history.stateAt(state, 25)))
	if base.closed.Load() {
		t.Fatal("rebase closed the old base while a view used it")
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	if !base.closed.Load() {
		t.Fatal("closing the last view of the old base left it open")
	}

	if _, err := live.View(ctx, 29); !errors.Is(err, ErrRevisionUnavailable) {
		t.Fatalf("view before the new base: %v", err)
	}
	// After Rebase the base order changed (Upsert appended documents), so the
	// rebuilt index is compared by content and score, ordered as the new base
	// numbers its documents.
	rebased := liveHistory{baseOrder: liveIdentifiersInOrder(t, next), revisions: history.revisions[30:]}
	rebasedState := make(map[string]map[string]string)
	for _, document := range history.stateAt(state, 30) {
		rebasedState[document.ID] = document.Fields
	}
	for revision := 30; revision <= 40; revision++ {
		view, err := live.View(ctx, uint64(revision))
		if err != nil {
			t.Fatal(err)
		}
		compareLiveIndexes(t, fmt.Sprintf("rebased revision %d", revision), view.Index,
			rebuiltLiveIndex(t, schema, rebased.stateAt(rebasedState, revision-30)))
		if err := view.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func countChanges(revisions [][]Change) int {
	total := 0
	for _, changes := range revisions {
		total += len(changes)
	}
	return total
}

func TestLiveRevisionRules(t *testing.T) {
	ctx := context.Background()
	schema := productSchema(t)
	directory := t.TempDir()
	writer := writeLiveIndex(t, directory, schema, []Document{{ID: "a", Fields: map[string]string{"title": "áo thun"}}}, 64)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	base, err := OpenIndex(directory, schema)
	if err != nil {
		t.Fatal(err)
	}
	live, err := NewLive(base, 5, LiveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	if err := live.Apply(ctx, 5, nil); err == nil {
		t.Fatal("applied a revision equal to the base revision")
	}
	if _, err := live.View(ctx, 4); !errors.Is(err, ErrRevisionUnavailable) {
		t.Fatalf("view before base: %v", err)
	}
	if _, err := live.View(ctx, 6); !errors.Is(err, ErrRevisionAhead) {
		t.Fatalf("view after latest: %v", err)
	}
	if err := live.Apply(ctx, 6, []Change{{ID: "b", Fields: map[string]string{"price": "1"}}}); err == nil {
		t.Fatal("applied a document with an unknown field")
	}
	if live.Revision() != 5 || live.Pending() != 0 {
		t.Fatalf("a rejected apply changed Live: revision %d, %d pending", live.Revision(), live.Pending())
	}
	if err := live.Apply(ctx, 7, nil); err != nil {
		t.Fatal(err)
	}
	fields := map[string]string{"title": "áo khoác"}
	if err := live.Apply(ctx, 8, []Change{{ID: "a", Fields: fields}}); err != nil {
		t.Fatal(err)
	}
	fields["title"] = "giày" // the caller's map is not shared with Live
	for revision, want := range map[uint64]uint64{6: 1, 7: 1, 8: 0} {
		view, err := live.View(ctx, revision)
		if err != nil {
			t.Fatal(err)
		}
		count, err := view.Count(ctx, MatchQuery{Field: "title", Text: "thun"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if count != want {
			t.Fatalf("revision %d: %d documents with thun, want %d", revision, count, want)
		}
		view.Close()
	}
	view, err := live.View(ctx, 8)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	if count, _ := view.Count(ctx, MatchQuery{Field: "title", Text: "ao khoac"}, nil); count != 1 {
		t.Fatalf("revision 8 lost the applied document: %d", count)
	}
}

func TestLiveConcurrentApplyAndViews(t *testing.T) {
	ctx := context.Background()
	_, _, writer, base, _, history := newLiveFixture(t, 5, 60)
	defer writer.Close()
	live, err := NewLive(base, 0, LiveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	var group sync.WaitGroup
	failures := make(chan error, 16)
	stop := make(chan struct{})
	for reader := 0; reader < 4; reader++ {
		group.Add(1)
		go func(reader int) {
			defer group.Done()
			for iteration := 0; ; iteration++ {
				select {
				case <-stop:
					return
				default:
				}
				revision := live.Revision()
				if reader%2 == 1 && revision > 0 {
					revision -= uint64(iteration) % (revision + 1)
				}
				view, err := live.View(ctx, revision)
				if err != nil {
					failures <- err
					return
				}
				if _, err := view.Search(ctx, MatchQuery{Fields: []string{"title", "body"}, Text: "ao thun"}, SearchOptions{Limit: 5}); err != nil {
					failures <- err
				}
				view.Close()
			}
		}(reader)
	}
	for revision := 1; revision <= len(history.revisions); revision++ {
		if err := live.Apply(ctx, uint64(revision), history.revisions[revision-1]); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	group.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
}

func TestLiveViewsShareAndBoundTheirPieces(t *testing.T) {
	ctx := context.Background()
	_, _, writer, base, _, history := newLiveFixture(t, 6, 40)
	defer writer.Close()
	live, err := NewLive(base, 0, LiveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	var previous []*livePiece
	for revision := 1; revision <= len(history.revisions); revision++ {
		if err := live.Apply(ctx, uint64(revision), history.revisions[revision-1]); err != nil {
			t.Fatal(err)
		}
		view, err := live.View(ctx, uint64(revision))
		if err != nil {
			t.Fatal(err)
		}
		view.Close()
		live.mu.Lock()
		pieces, sequence := live.pieces, live.sequence
		live.mu.Unlock()
		if len(pieces) > maximumLivePieces {
			t.Fatalf("revision %d: %d pieces", revision, len(pieces))
		}
		if sequence > 0 && pieceCoverage(pieces) != sequence {
			t.Fatalf("revision %d: pieces cover %d of %d changes", revision, pieceCoverage(pieces), sequence)
		}
		// unless they were merged, the earlier pieces are the very same ones
		if len(pieces) > len(previous) {
			for position := range previous {
				if pieces[position] != previous[position] {
					t.Fatalf("revision %d: piece %d was rebuilt", revision, position)
				}
			}
		}
		previous = pieces
	}
}
