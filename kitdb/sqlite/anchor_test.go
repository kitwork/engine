package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// anchorFixture is a file with one base and a .pages for every anchor taken after it, and the rows at every revision.
type anchorFixture struct {
	path     string
	database *Database
	recorded map[uint64]string
	anchored []uint64 // the revisions of the .pages, in order
}

func (fixture *anchorFixture) write(t *testing.T, query string, arguments ...any) uint64 {
	t.Helper()
	revision, err := fixture.database.Write(context.Background(), "t", func(tx *Transaction) error {
		_, err := tx.Exec(query, arguments...)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	_, fixture.recorded[revision] = liveDigest(t, fixture.database)
	return revision
}

func (fixture *anchorFixture) anchor(t *testing.T) uint64 {
	t.Helper()
	revision, err := fixture.database.Anchor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	fixture.anchored = append(fixture.anchored, revision)
	return revision
}

// reopen closes the file and opens it again, as a process that stopped and started.
func (fixture *anchorFixture) reopen(t *testing.T) {
	t.Helper()
	if err := fixture.database.Close(); err != nil {
		t.Fatal(err)
	}
	fixture.database = openTestDatabase(t, fixture.path)
}

// newAnchorFixture writes a table of rows no later write touches and anchors, then anchors after each of count
// rounds of writes on notes (anchored).
func newAnchorFixture(t *testing.T, count int) *anchorFixture {
	t.Helper()
	ctx := context.Background()
	fixture := &anchorFixture{path: filepath.Join(t.TempDir(), "kitwork.db"), recorded: map[uint64]string{}}
	fixture.database = openTestDatabase(t, fixture.path)
	if _, err := fixture.database.Migrate(ctx, "boot", `CREATE TABLE ballast (id INTEGER PRIMARY KEY, body TEXT)`,
		`CREATE TABLE notes (id INTEGER PRIMARY KEY, title TEXT)`); err != nil {
		t.Fatal(err)
	}
	fixture.write(t, `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 4000)
		INSERT INTO ballast (id, body) SELECT i, printf('%0500d', i) FROM n`)
	if _, err := fixture.database.Anchor(ctx); err != nil {
		t.Fatal(err)
	}
	for round := range count {
		for note := range 4 {
			id := round*4 + note + 1
			fixture.write(t, `INSERT INTO notes (id, title) VALUES (?, ?)`, id, fmt.Sprintf("note %d", id))
		}
		fixture.write(t, `UPDATE notes SET title = title || '.' WHERE id % 3 = ?`, round%3)
		fixture.write(t, `DELETE FROM notes WHERE id = ?`, round*4+1)
		fixture.anchor(t)
	}
	return fixture
}

// restoreDigest restores the moment of revision and returns its rows' digest.
func (fixture *anchorFixture) restoreDigest(t *testing.T, revision uint64) (string, error) {
	t.Helper()
	moments := revisionMoments(t, fixture.database)
	target := filepath.Join(t.TempDir(), fmt.Sprintf("restored-%d.db", revision))
	restored, err := fixture.database.Restore(context.Background(), moments[revision], target)
	if err != nil {
		return "", err
	}
	if restored != revision {
		t.Fatalf("restoring the moment of revision %d reached %d", revision, restored)
	}
	return fileDigest(t, target), nil
}

// anchorAlone writes item's chain to a file of its own, with no history replayed after it, and reads the revision
// its history ends at, its integrity_check and its rows' digest.
func anchorAlone(t *testing.T, anchors []anchor, item anchor) (uint64, string, string) {
	t.Helper()
	target := filepath.Join(t.TempDir(), "anchor.db")
	if err := restoreAnchor(anchors, item, target); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dataSourceName(target, "mode=ro"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var top int64
	var check string
	if err := db.QueryRow(`SELECT coalesce(max(rev), 0) FROM kitwork_history`).Scan(&top); err != nil {
		t.Fatalf("anchor %d: %v", item.revision, err)
	}
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&check); err != nil {
		t.Fatalf("anchor %d: %v", item.revision, err)
	}
	return uint64(top), check, digest(t, func(query string, arguments ...any) (*sql.Rows, error) { return db.Query(query, arguments...) })
}

func anchorFiles(t *testing.T, database *Database) string {
	t.Helper()
	entries, err := os.ReadDir(database.anchorDirectory())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		name := entry.Name()
		if dash := strings.IndexByte(name, '-'); dash == 20 { // <rev>-<at>.<kind>: the revision and the kind
			var revision uint64
			fmt.Sscanf(name[:20], "%d", &revision)
			name = fmt.Sprintf("%d%s", revision, filepath.Ext(name))
			if strings.HasSuffix(entry.Name(), ".corrupt") {
				name = fmt.Sprintf("%d%s.corrupt", revision, filepath.Ext(strings.TrimSuffix(entry.Name(), ".corrupt")))
			}
		}
		names = append(names, name)
	}
	return strings.Join(names, " ")
}

// TestPageAnchorsRestoreEveryRevision: the anchor a new file gets is a base, every later one a .pages holding only
// the pages changed since the one before, and a restore at the moment of every revision, from the base and the
// .pages of its chain and the history after, reads the rows that revision had. The table no write touches stays out
// of every .pages.
//
// CONTROL (05/11/2026): with every page kept as unchanged, "anchor 2 alone: history up to 0, integrity_check "ok",
// the rows of its revision false" (a restore through it still read right, since the history it holds stopped at 0
// and the replay ran from there); with every page kept as changed, "anchor 8.pages holds 1004 of 1004 pages, want
// fewer than half".
func TestPageAnchorsRestoreEveryRevision(t *testing.T) {
	fixture := newAnchorFixture(t, 5)
	anchors, err := fixture.database.anchors()
	if err != nil {
		t.Fatal(err)
	}
	if len(anchors) != 7 || anchors[0].kind != anchorBase || anchors[0].revision != 0 {
		t.Fatalf("anchors: %s, want the base Open wrote and six .pages", anchorFiles(t, fixture.database))
	}
	for _, item := range anchors[2:] { // after the one that took in the ballast
		count := 0
		head, err := readPages(item.path, func(uint32, []byte) error { count++; return nil })
		if err != nil {
			t.Fatal(err)
		}
		if item.kind != anchorPages || count*2 >= int(head.pageCount) {
			t.Fatalf("anchor %d%s holds %d of %d pages, want fewer than half", item.revision, filepath.Ext(item.path),
				count, head.pageCount)
		}
	}
	// each anchor alone is the file at its revision, before any history is replayed over it
	for _, item := range anchors[1:] {
		top, check, got := anchorAlone(t, anchors, item)
		if top != item.revision || check != "ok" || got != fixture.recorded[item.revision] {
			t.Fatalf("anchor %d alone: history up to %d, integrity_check %q, the rows of its revision %v", item.revision,
				top, check, got == fixture.recorded[item.revision])
		}
	}
	for revision, want := range fixture.recorded {
		got, err := fixture.restoreDigest(t, revision)
		if err != nil {
			t.Fatalf("restore revision %d: %v", revision, err)
		}
		if got != want {
			t.Fatalf("revision %d restored to different rows", revision)
		}
	}
	// nothing committed since the newest anchor: no new one
	if revision, err := fixture.database.Anchor(context.Background()); err != nil || revision != fixture.anchored[len(fixture.anchored)-1] {
		t.Fatalf("an anchor with nothing new gave revision %d (%v)", revision, err)
	}
	if got := len(strings.Fields(anchorFiles(t, fixture.database))); got != 8 {
		t.Fatalf("anchor files: %s, want seven anchors and pages.hashes", anchorFiles(t, fixture.database))
	}
}

// TestRetainFoldsAChainIntoItsBase: when the oldest anchor kept is a .pages, Retain writes its chain into the base,
// which takes its name, and the .pages before go; the anchors after it chain to the base; every moment from it on
// restores, a moment before it is ErrPruned.
//
// CONTROL (05/11/2026): with the base renamed but no page written into it, "revision 17 restored to different rows
// (kitdb: history is corrupt: revision 14 follows 0)"; with pages.hashes left open while it is replaced (as first
// written), "kitdb: anchor 2 is written, its page hashes are not: rename …: Access is denied.", every .pages then
// taken against the base and dropped by the fold.
func TestRetainFoldsAChainIntoItsBase(t *testing.T) {
	ctx := context.Background()
	fixture := newAnchorFixture(t, 4)
	moments := revisionMoments(t, fixture.database)
	start := fixture.anchored[1]
	kept, err := fixture.database.Retain(ctx, moments[start].Add(time.Hour), Retention{Keep: time.Hour, Every: 1000 * time.Hour})
	if err != nil || kept != start {
		t.Fatalf("Retain kept from revision %d (%v), want %d", kept, err, start)
	}
	want := fmt.Sprintf("%d.base %d.pages %d.pages pages.hashes", start, fixture.anchored[2], fixture.anchored[3])
	if got := anchorFiles(t, fixture.database); got != want {
		t.Fatalf("anchors: %s, want %s", got, want)
	}
	for revision, digest := range fixture.recorded {
		got, err := fixture.restoreDigest(t, revision)
		if revision < start {
			if !errors.Is(err, ErrPruned) {
				t.Fatalf("restoring revision %d before the kept anchor: %v, want ErrPruned", revision, err)
			}
			continue
		}
		if err != nil || got != digest {
			t.Fatalf("revision %d restored to different rows (%v)", revision, err)
		}
	}
	// the next anchor chains to what the fold left
	fixture.write(t, `INSERT INTO notes (id, title) VALUES (1000, 'after the fold')`)
	revision := fixture.anchor(t)
	if got, err := fixture.restoreDigest(t, revision); err != nil || got != fixture.recorded[revision] {
		t.Fatalf("revision %d after the fold restored to different rows (%v)", revision, err)
	}
}

// TestFoldCutShortIsFinishedAtOpen: a fold whose process stopped, after writing part of its chain into the base or
// after renaming the base, is finished by the next Open; every kept moment restores.
//
// CONTROL (05/11/2026): with Open not finishing a fold, "partway: anchors: 0.base 2.pages 8.pages 14.pages 20.pages
// fold pages.hashes, want 8.base 14.pages 20.pages pages.hashes".
func TestFoldCutShortIsFinishedAtOpen(t *testing.T) {
	for _, renamed := range []bool{false, true} {
		name := map[bool]string{false: "partway", true: "renamed"}[renamed]
		t.Run(name, func(t *testing.T) {
			fixture := newAnchorFixture(t, 3)
			anchors, err := fixture.database.anchors()
			if err != nil {
				t.Fatal(err)
			}
			base, first, second := anchors[0], anchors[1], anchors[2]
			target := filepath.Join(fixture.database.anchorDirectory(), anchorName(second.revision, second.at, anchorBase))
			marker := strings.Join([]string{foldMagic, filepath.Base(base.path), filepath.Base(target),
				filepath.Base(first.path), filepath.Base(second.path)}, "\n") + "\n"
			if err := os.WriteFile(filepath.Join(fixture.database.anchorDirectory(), foldName), []byte(marker), 0o644); err != nil {
				t.Fatal(err)
			}
			if renamed {
				if err := writeChain(base.path, []anchor{base, first, second}); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(base.path, target); err != nil {
					t.Fatal(err)
				}
			} else if err := writeChain(base.path, []anchor{base, first}); err != nil {
				t.Fatal(err)
			}
			fixture.reopen(t)
			want := fmt.Sprintf("%d.base %d.pages %d.pages pages.hashes", second.revision, fixture.anchored[1], fixture.anchored[2])
			if got := anchorFiles(t, fixture.database); got != want {
				t.Fatalf("anchors: %s, want %s", got, want)
			}
			for revision, digest := range fixture.recorded {
				if revision < second.revision {
					continue // still in the history, but no anchor holds a moment before the base
				}
				if got, err := fixture.restoreDigest(t, revision); err != nil || got != digest {
					t.Fatalf("revision %d restored to different rows (%v)", revision, err)
				}
			}
		})
	}
}

// TestCorruptPagesAreSetAside: a .pages that does not read back is never written into anything. A restore through it
// starts from the anchor before it and replays more of the history; a fold through it writes nothing into the base
// and sets the file aside, and the next anchor, with no chain to take its pages against, is a base.
//
// CONTROL (05/11/2026): with the checksum not checked, "a fold through a corrupt .pages: <nil>, want
// ErrCorruptAnchor"; with the chain not checked before the fold writes, "the fold changed the base (<nil>)"; with the
// file not set aside, "anchors: 0.base 2.pages 8.pages 14.pages 20.pages pages.hashes, want 2.pages set aside"; with
// no fallback to an older anchor, "revision 20 restored to different rows (kitdb: anchor is corrupt: the checksum of
// …-….pages does not match)".
func TestCorruptPagesAreSetAside(t *testing.T) {
	ctx := context.Background()
	fixture := newAnchorFixture(t, 3)
	anchors, err := fixture.database.anchors()
	if err != nil {
		t.Fatal(err)
	}
	base, first := anchors[0], anchors[1]
	content, err := os.ReadFile(first.path)
	if err != nil {
		t.Fatal(err)
	}
	content[anchorHeadSize+4+200] ^= 0x5a // inside the first page it holds
	if err := os.WriteFile(first.path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	for revision, digest := range fixture.recorded {
		if got, err := fixture.restoreDigest(t, revision); err != nil || got != digest {
			t.Fatalf("revision %d restored to different rows (%v)", revision, err)
		}
	}
	before, err := os.ReadFile(base.path)
	if err != nil {
		t.Fatal(err)
	}
	moments := revisionMoments(t, fixture.database)
	_, err = fixture.database.Retain(ctx, moments[fixture.anchored[1]].Add(time.Hour), Retention{Keep: time.Hour, Every: 1000 * time.Hour})
	if !errors.Is(err, ErrCorruptAnchor) {
		t.Fatalf("a fold through a corrupt .pages: %v, want ErrCorruptAnchor", err)
	}
	if after, err := os.ReadFile(base.path); err != nil || !bytes.Equal(before, after) {
		t.Fatalf("the fold changed the base (%v)", err)
	}
	if got := anchorFiles(t, fixture.database); !strings.Contains(got, fmt.Sprintf("%d.pages.corrupt", first.revision)) ||
		strings.Contains(got, fmt.Sprintf(" %d.pages ", first.revision)) {
		t.Fatalf("anchors: %s, want %d.pages set aside", got, first.revision)
	}
	if chained, _ := fixture.database.anchors(); len(chained) != 1 || chained[0].kind != anchorBase {
		t.Fatalf("anchors a restore can start from: %d, want the base alone", len(chained))
	}
	fixture.write(t, `INSERT INTO notes (id, title) VALUES (1000, 'after')`)
	revision := fixture.anchor(t)
	if chained, _ := fixture.database.anchors(); chained[len(chained)-1].kind != anchorBase || chained[len(chained)-1].revision != revision {
		t.Fatalf("the anchor after the corrupt chain: %s, want a base", anchorFiles(t, fixture.database))
	}
	if got, err := fixture.restoreDigest(t, revision); err != nil || got != fixture.recorded[revision] {
		t.Fatalf("revision %d restored to different rows (%v)", revision, err)
	}
}

// TestHashesThatDoNotReadGiveABase: pages.hashes cut or changed, or of an anchor no longer there, is no ground for a
// .pages: the next anchor keeps every page.
//
// CONTROL (05/11/2026): with the hashes' checksum not checked, "changed: the next anchor is 15.pages, want a base".
func TestHashesThatDoNotReadGiveABase(t *testing.T) {
	for _, damage := range []string{"cut", "changed", "orphaned"} {
		t.Run(damage, func(t *testing.T) {
			fixture := newAnchorFixture(t, 2)
			hashes := filepath.Join(fixture.database.anchorDirectory(), hashesName)
			content, err := os.ReadFile(hashes)
			if err != nil {
				t.Fatal(err)
			}
			switch damage {
			case "cut":
				content = content[:len(content)-40]
			case "changed":
				content[anchorHeadSize+3] ^= 0x01
			case "orphaned":
				anchors, _ := fixture.database.anchors()
				if err := os.Remove(anchors[len(anchors)-1].path); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(hashes, content, 0o644); err != nil {
				t.Fatal(err)
			}
			fixture.write(t, `INSERT INTO notes (id, title) VALUES (1000, 'after')`)
			revision := fixture.anchor(t)
			anchors, _ := fixture.database.anchors()
			if newest := anchors[len(anchors)-1]; newest.kind != anchorBase || newest.revision != revision {
				t.Fatalf("the next anchor is %d%s, want a base", newest.revision, filepath.Ext(newest.path))
			}
			fixture.write(t, `INSERT INTO notes (id, title) VALUES (1001, 'after the base')`)
			revision = fixture.anchor(t)
			for _, check := range []uint64{revision - 1, revision} {
				if got, err := fixture.restoreDigest(t, check); err != nil || got != fixture.recorded[check] {
					t.Fatalf("revision %d restored to different rows (%v)", check, err)
				}
			}
		})
	}
}

// TestWholeAnchorsStillRestoreAndAgeOut: a file whose anchors were written before 05/11/2026, whole VACUUM INTO
// copies holding only their last commit, still restores from them; its next anchor is a base, and Retain lets the
// whole copies go like any anchor before the oldest kept.
//
// CONTROL (05/11/2026): with .db no longer listed, "anchors: 8.dbx 9.dbx 10.base 11.pages pages.hashes, want 8.db
// 9.db 10.base 11.pages pages.hashes".
func TestWholeAnchorsStillRestoreAndAgeOut(t *testing.T) {
	ctx := context.Background()
	fixture := newAnchorFixture(t, 1)
	// the anchors as an engine before 05/11/2026 left them: a whole copy of the latest commit, nothing else
	whole := func() uint64 {
		t.Helper()
		directory := fixture.database.anchorDirectory()
		temporary := filepath.Join(directory, "whole.tmp")
		source, err := sql.Open("sqlite", dataSourceName(fixture.path, "mode=ro"))
		if err != nil {
			t.Fatal(err)
		}
		defer source.Close()
		if _, err := source.Exec(`VACUUM INTO ?`, filepath.ToSlash(temporary)); err != nil {
			t.Fatal(err)
		}
		copied, err := sql.Open("sqlite", dataSourceName(temporary, "_pragma=journal_mode(DELETE)"))
		if err != nil {
			t.Fatal(err)
		}
		var revision, at int64
		if err := copied.QueryRow(`SELECT max(rev), max(at) FROM kitwork_history`).Scan(&revision, &at); err != nil {
			t.Fatal(err)
		}
		if _, err := copied.Exec(`DELETE FROM kitwork_history WHERE rev < ?`, revision); err != nil {
			t.Fatal(err)
		}
		copied.Close()
		if err := os.Rename(temporary, filepath.Join(directory, anchorName(uint64(revision), at, anchorWhole))); err != nil {
			t.Fatal(err)
		}
		return uint64(revision)
	}
	entries, _ := os.ReadDir(fixture.database.anchorDirectory())
	for _, entry := range entries {
		if err := os.Remove(filepath.Join(fixture.database.anchorDirectory(), entry.Name())); err != nil {
			t.Fatal(err)
		}
	}
	first := whole()
	fixture.write(t, `INSERT INTO notes (id, title) VALUES (100, 'x')`)
	second := whole()
	fixture.write(t, `INSERT INTO notes (id, title) VALUES (101, 'y')`)
	fixture.reopen(t)
	for _, revision := range []uint64{first, second, second + 1} {
		if got, err := fixture.restoreDigest(t, revision); err != nil || got != fixture.recorded[revision] {
			t.Fatalf("revision %d restored to different rows (%v)", revision, err)
		}
	}
	based := fixture.anchor(t)
	fixture.write(t, `INSERT INTO notes (id, title) VALUES (102, 'z')`)
	paged := fixture.anchor(t)
	want := fmt.Sprintf("%d.db %d.db %d.base %d.pages pages.hashes", first, second, based, paged)
	if got := anchorFiles(t, fixture.database); got != want {
		t.Fatalf("anchors: %s, want %s", got, want)
	}
	moments := revisionMoments(t, fixture.database)
	kept, err := fixture.database.Retain(ctx, moments[based].Add(time.Hour), Retention{Keep: time.Hour, Every: 1000 * time.Hour})
	if err != nil || kept != based {
		t.Fatalf("Retain kept from revision %d (%v), want %d", kept, err, based)
	}
	want = fmt.Sprintf("%d.base %d.pages pages.hashes", based, paged)
	if got := anchorFiles(t, fixture.database); got != want {
		t.Fatalf("anchors: %s, want %s", got, want)
	}
	for _, revision := range []uint64{based, based + 1, paged} {
		if got, err := fixture.restoreDigest(t, revision); err != nil || got != fixture.recorded[revision] {
			t.Fatalf("revision %d restored to different rows (%v)", revision, err)
		}
	}
}

// TestPageAnchorsUnderAConcurrentWorkload: anchors taken while sixteen writers commit are each the file at one
// revision, its history ending there, as a replay of that revision from the first base gives it.
//
// CONTROL (05/11/2026): with every page kept as unchanged, "anchor 2 holds the history up to 0, integrity_check "ok"".
func TestPageAnchorsUnderAConcurrentWorkload(t *testing.T) {
	ctx := context.Background()
	fixture := newAnchorFixture(t, 1)
	if _, err := fixture.database.Migrate(ctx, "boot", productsSchema); err != nil {
		t.Fatal(err)
	}
	// the writers go on until three anchors were taken while they ran (at most 300 writes each)
	var taken atomic.Int64
	var writers sync.WaitGroup
	for worker := range 16 {
		writers.Go(func() {
			random := rand.New(rand.NewPCG(uint64(worker), 3))
			for count := 0; count < 300 && taken.Load() < 3; count++ {
				_, _ = fixture.database.Write(ctx, "w", randomWrite(random, fmt.Sprintf("w%d", worker)))
			}
		})
	}
	done := make(chan struct{})
	go func() { writers.Wait(); close(done) }()
	for running := true; running; {
		select {
		case <-done:
			running = false
		default:
			before := fixture.database.Revision()
			if _, err := fixture.database.Anchor(ctx); err != nil {
				t.Fatal(err)
			}
			if fixture.database.Revision() != before {
				taken.Add(1) // the writers committed while it ran
			}
		}
	}
	if _, err := fixture.database.Anchor(ctx); err != nil {
		t.Fatal(err)
	}
	anchors, err := fixture.database.anchors()
	if err != nil {
		t.Fatal(err)
	}
	if taken.Load() < 3 {
		t.Fatalf("%d anchors were taken while the writers committed, want 3", taken.Load())
	}
	// Each anchor alone is the file at one revision: the history it holds ends there, it passes integrity_check, and
	// its rows are those a restore of that moment from the first base, replaying every commit since, gives.
	aside := t.TempDir()
	for _, item := range anchors[1:] {
		top, check, got := anchorAlone(t, anchors, item)
		if top != item.revision || check != "ok" {
			t.Fatalf("anchor %d holds the history up to %d, integrity_check %q", item.revision, top, check)
		}
		// the anchors after the first base set aside, a restore replays from it
		for _, other := range anchors[1:] {
			if err := os.Rename(other.path, filepath.Join(aside, filepath.Base(other.path))); err != nil {
				t.Fatal(err)
			}
		}
		moments := revisionMoments(t, fixture.database)
		replayed := filepath.Join(t.TempDir(), "replayed.db")
		if _, err := fixture.database.Restore(ctx, moments[item.revision], replayed); err != nil {
			t.Fatal(err)
		}
		for _, other := range anchors[1:] {
			if err := os.Rename(filepath.Join(aside, filepath.Base(other.path)), other.path); err != nil {
				t.Fatal(err)
			}
		}
		if want := fileDigest(t, replayed); got != want {
			t.Fatalf("anchor %d reads different rows from the replay of its revision", item.revision)
		}
	}
	// and a restore of the last moment, through the newest anchor's chain and what came after
	last := fixture.database.Revision()
	_, fixture.recorded[last] = liveDigest(t, fixture.database)
	if got, err := fixture.restoreDigest(t, last); err != nil || got != fixture.recorded[last] {
		t.Fatalf("the last revision %d restored to different rows (%v)", last, err)
	}
}

// OldestAnchor is the moment of the oldest anchor, none while a file has none: a table's first anchor, held here as an
// anchor running holds it, is there only once written.
//
// CONTROL (09/11/2026): with the newest anchor given in place of the oldest, "the oldest anchor at …, want the first's".
func TestOldestAnchor(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "kitwork.db"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.Migrate(ctx, "boot", `CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT)`); err != nil {
		t.Fatal(err)
	}
	func() {
		database.anchoring.Lock()
		defer database.anchoring.Unlock()
		if _, err := database.SetHistory(ctx, "t", "notes", true); err != nil {
			t.Fatal(err)
		}
		if _, found, err := database.OldestAnchor(); err != nil || found {
			t.Fatalf("OldestAnchor while the first is written: found %v (%v), want none", found, err)
		}
	}()
	database.background.Wait()
	first, found, err := database.OldestAnchor()
	if err != nil || !found {
		t.Fatalf("OldestAnchor once the first is written: found %v (%v)", found, err)
	}
	time.Sleep(5 * time.Millisecond)
	mustWrite(t, database, `INSERT INTO notes (body) VALUES ('a')`)
	if _, err := database.Anchor(ctx); err != nil {
		t.Fatal(err)
	}
	if oldest, _, _ := database.OldestAnchor(); !oldest.Equal(first) {
		t.Fatalf("the oldest anchor at %v, want the first's, %v", oldest, first)
	}
}

// A file that has rows and no anchor, opened with the history of every table (as the old engine opens a file of
// before), gets its first anchor in the background, from a snapshot taken as Open returns: Open does not wait for its
// pages (27.6 GB for shopping), a write goes through while they are written, and the anchor is of the revision the file
// had at Open, not of the write after it. A file made now still gets its anchor before Open returns.
//
// CONTROL (09/11/2026): with Open writing the anchor before it returns, as before, "an anchor while the background one
// is held: [00000000000000000003-….base pages.hashes], want none yet".
func TestOpenAnchorsAFileInTheBackground(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "kitwork.db")
	first, err := Open(ctx, path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Migrate(ctx, "boot", `CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT)`); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, first, `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 200) INSERT INTO notes (body) SELECT 'note ' || i FROM n`)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	release := make(chan struct{})
	started := make(chan struct{}, 1)
	anchorStarting = func() {
		started <- struct{}{}
		<-release
	}
	t.Cleanup(func() { anchorStarting = nil })
	database, err := Open(ctx, path, Options{HistoryOfEveryTable: true})
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	opened := database.Revision()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("no anchor started in the background")
	}
	anchorFiles := func() []string {
		entries, _ := filepath.Glob(filepath.Join(path+".history", "*"))
		var names []string
		for _, entry := range entries {
			names = append(names, filepath.Base(entry))
		}
		return names
	}
	if names := anchorFiles(); len(names) != 0 {
		close(release)
		t.Fatalf("an anchor while the background one is held: %v, want none yet", names)
	}
	mustWrite(t, database, `INSERT INTO notes (body) VALUES ('while the anchor is written')`)
	close(release)
	database.background.Wait()
	anchors, err := database.anchors()
	if err != nil || len(anchors) != 1 || anchors[0].revision != opened || anchors[0].kind != anchorBase {
		t.Fatalf("anchors after the background one: %+v (%v), want one .base of revision %d", anchors, err, opened)
	}
}
