package sqlite

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Anchors (KITWORK_KITDB.md mục 5, câu 13.9). <file>.history/ holds:
//
//   - <rev>-<at>.base: the file's pages at rev, byte for byte, a SQLite file; the start of a chain;
//   - <rev>-<at>.pages: the pages that changed since the anchor its header names, the one before it in its chain;
//   - pages.hashes: a hash of every page of the newest anchor, the next .pages is taken against;
//   - <rev>-<at>.db: an anchor written before 05/11/2026, a VACUUM INTO copy, read by Restore until Retain lets it go;
//   - fold: the marker of a fold of the oldest anchors into the base, while it runs.
//
// An anchor is the file as it was, history and all: the history of the days before it stays in its pages until
// Retain folds it into the base and the live file has let go of it too, so a change's images can sit on disk about
// twice app.history({ days }) (decided 05/11/2026).

// ErrCorruptAnchor means an anchor does not read back as it was written: a .pages whose checksum does not match, a
// chain that does not reach its base, a fold that cannot be finished.
var ErrCorruptAnchor = errors.New("kitdb: anchor is corrupt")

const (
	pagesMagic     = "kitwork pages 1\n"
	hashesMagic    = "kitwork hashes1\n"
	foldMagic      = "kitwork fold 1"
	hashesName     = "pages.hashes"
	foldName       = "fold"
	pageHashSize   = 16
	anchorHeadSize = 16 + 8 + 4 + 4 // magic, a revision, the page size, the page count
)

// anchorDirectory holds the anchors of the file.
func (database *Database) anchorDirectory() string { return database.path + ".history" }

type anchorKind int

const (
	anchorWhole anchorKind = iota // <rev>-<at>.db, a VACUUM INTO copy written before 05/11/2026
	anchorBase                    // <rev>-<at>.base, the file's pages at rev
	anchorPages                   // <rev>-<at>.pages, the pages changed since the anchor previous names
)

var anchorExtensions = map[string]anchorKind{".db": anchorWhole, ".base": anchorBase, ".pages": anchorPages}

type anchor struct {
	path     string
	revision uint64
	at       int64
	kind     anchorKind
	previous uint64 // anchorPages: the revision of the anchor its pages are taken against
	broken   bool   // anchorPages: a header that does not read
}

func anchorName(revision uint64, at int64, kind anchorKind) string {
	for extension, known := range anchorExtensions {
		if known == kind {
			return fmt.Sprintf("%020d-%020d%s", revision, at, extension)
		}
	}
	panic("kitdb: unknown anchor kind")
}

// listAnchors lists every anchor file of the directory, oldest first, whether or not its chain reaches a base.
func (database *Database) listAnchors() ([]anchor, error) {
	directory := database.anchorDirectory()
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var anchors []anchor
	for _, entry := range entries {
		extension := filepath.Ext(entry.Name())
		kind, known := anchorExtensions[extension]
		if entry.IsDir() || !known {
			continue
		}
		var revision uint64
		var at int64
		if _, err := fmt.Sscanf(entry.Name(), "%020d-%020d", &revision, &at); err != nil ||
			anchorName(revision, at, kind) != entry.Name() {
			continue
		}
		item := anchor{path: filepath.Join(directory, entry.Name()), revision: revision, at: at, kind: kind}
		if kind == anchorPages {
			header, err := readAnchorHead(item.path, pagesMagic)
			item.previous, item.broken = header.revision, err != nil
		}
		anchors = append(anchors, item)
	}
	// at one revision a base comes last, so the newest anchor at or before a moment is the base a fold cut short left
	// beside the .pages it folded
	rank := map[anchorKind]int{anchorPages: 0, anchorWhole: 1, anchorBase: 2}
	sort.Slice(anchors, func(i, j int) bool {
		if anchors[i].revision != anchors[j].revision {
			return anchors[i].revision < anchors[j].revision
		}
		return rank[anchors[i].kind] < rank[anchors[j].kind]
	})
	return anchors, nil
}

// anchors lists the anchors a restore can start from, oldest first: every whole copy and base, and every .pages whose
// chain of anchors before it reaches a base.
func (database *Database) anchors() ([]anchor, error) {
	all, err := database.listAnchors()
	if err != nil {
		return nil, err
	}
	return chainedAnchors(all), nil
}

func chainedAnchors(all []anchor) []anchor {
	chained := map[uint64]bool{} // the revisions a .pages can be taken against
	var anchors []anchor
	for _, item := range all { // oldest first, and a .pages is taken against an older revision
		switch item.kind {
		case anchorWhole:
			anchors = append(anchors, item)
		case anchorBase:
			chained[item.revision] = true
			anchors = append(anchors, item)
		case anchorPages:
			if !item.broken && item.previous < item.revision && chained[item.previous] {
				chained[item.revision] = true
				anchors = append(anchors, item)
			}
		}
	}
	return anchors
}

// chain is what restoring item writes, in order: a whole copy or a base, then the .pages up to item.
func chain(anchors []anchor, item anchor) ([]anchor, error) {
	bases, pages := map[uint64]anchor{}, map[uint64]anchor{}
	for _, known := range anchors {
		switch known.kind {
		case anchorBase:
			bases[known.revision] = known
		case anchorPages:
			pages[known.revision] = known
		}
	}
	links := []anchor{item}
	for links[0].kind == anchorPages {
		previous := links[0].previous
		if base, found := bases[previous]; found { // a fold cut short can leave a base and a .pages of one revision
			links = append([]anchor{base}, links...)
			break
		}
		before, found := pages[previous]
		if !found || previous >= links[0].revision {
			return nil, fmt.Errorf("%w: %s is taken against revision %d, which no anchor holds", ErrCorruptAnchor,
				filepath.Base(links[0].path), previous)
		}
		links = append([]anchor{before}, links...)
	}
	return links, nil
}

// Anchor writes an anchor of the file as of its latest commit and returns its revision. Restore starts from the
// newest anchor at or before the moment it is asked for, so anchors bound how much history a restore replays.
func (database *Database) Anchor(ctx context.Context) (uint64, error) {
	if err := database.ensureOpen(); err != nil {
		return 0, err
	}
	database.anchoring.Lock()
	defer database.anchoring.Unlock()
	return database.writeAnchor(ctx)
}

// OldestAnchor is the moment of the oldest anchor a restore can start from, the earliest moment RestoreTable reaches;
// found is false while there is none, as when a table's first anchor is still written in the background. It says
// where ErrNoAnchor ends (09/11/2026).
func (database *Database) OldestAnchor() (at time.Time, found bool, err error) {
	if err := database.ensureOpen(); err != nil {
		return time.Time{}, false, err
	}
	anchors, err := database.anchors()
	if err != nil || len(anchors) == 0 {
		return time.Time{}, false, err
	}
	return time.Unix(0, anchors[0].at).UTC(), true, nil
}

// anchorSnapshot is the file as of a commit, held by a read transaction of its own: the pages an anchor copies.
type anchorSnapshot struct {
	source                            *sql.DB
	tx                                *sql.Tx
	revision, at, pageSize, pageCount int64
}

// openSnapshot starts a read transaction on the file and reads in it its latest commit, which fixes what the
// transaction sees: whatever the writer commits after, the pages read through it are the file as of that commit.
func (database *Database) openSnapshot(ctx context.Context) (*anchorSnapshot, error) {
	source, err := sql.Open("sqlite", dataSourceName(database.path, "mode=ro"))
	if err != nil {
		return nil, err
	}
	source.SetMaxOpenConns(1)
	snapshot := &anchorSnapshot{source: source}
	snapshot.tx, err = source.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err == nil {
		err = snapshot.tx.QueryRowContext(ctx, `SELECT rev, at FROM kitwork_history ORDER BY rev DESC LIMIT 1`).Scan(&snapshot.revision, &snapshot.at)
		if errors.Is(err, sql.ErrNoRows) {
			err = nil
		}
	}
	if err == nil {
		err = snapshot.tx.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&snapshot.pageSize)
	}
	if err == nil {
		err = snapshot.tx.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&snapshot.pageCount)
	}
	if err != nil {
		snapshot.close()
		return nil, err
	}
	return snapshot, nil
}

func (snapshot *anchorSnapshot) close() {
	if snapshot.tx != nil {
		_ = snapshot.tx.Rollback()
	}
	_ = snapshot.source.Close()
}

// writeAnchor writes an anchor as of the file's latest commit (writeAnchorOf). The caller holds database.anchoring.
func (database *Database) writeAnchor(ctx context.Context) (uint64, error) {
	snapshot, err := database.openSnapshot(ctx)
	if err != nil {
		return 0, err
	}
	defer snapshot.close()
	return database.writeAnchorOf(ctx, snapshot)
}

// writeAnchorOf writes an anchor of the snapshot, unless an anchor at or after its commit is there already. It reads
// every page through sqlite_dbpage in the snapshot's read transaction, so the writer goes on, and hashes each. Against
// the hashes of the newest anchor it keeps the pages that changed (.pages); without them (the first anchor, a file
// whose anchors were whole copies, hashes whose anchor is gone or of another page size, hashes that do not read) it
// keeps every page (.base). The hashes of the pages it read replace pages.hashes once the anchor is in place. The
// caller holds database.anchoring.
func (database *Database) writeAnchorOf(ctx context.Context, snapshot *anchorSnapshot) (uint64, error) {
	directory := database.anchorDirectory()
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return 0, err
	}
	tx, revision, at, pageSize, pageCount := snapshot.tx, snapshot.revision, snapshot.at, snapshot.pageSize, snapshot.pageCount
	anchors, err := database.anchors()
	if err != nil {
		return 0, err
	}
	if len(anchors) > 0 && anchors[len(anchors)-1].revision >= uint64(revision) {
		return anchors[len(anchors)-1].revision, nil // nothing committed since the newest anchor
	}

	// the hashes the pages are taken against, when they are of an anchor still chained
	var against *hashesReader
	if reader, err := openHashes(filepath.Join(directory, hashesName)); err == nil {
		chained := false
		for _, item := range anchors {
			chained = chained || item.kind != anchorWhole && item.revision == reader.revision
		}
		if chained && reader.pageSize == uint32(pageSize) && reader.revision < uint64(revision) {
			against = reader
		} else {
			reader.close()
		}
	}
	kind := anchorBase
	if against != nil {
		kind = anchorPages
		defer against.close()
	}
	stamp := time.Now().UnixNano()
	anchorTemporary := filepath.Join(directory, fmt.Sprintf("anchor-%d.tmp", stamp))
	hashesTemporary := filepath.Join(directory, fmt.Sprintf("hashes-%d.tmp", stamp))
	written := false
	defer func() {
		if !written {
			_ = os.Remove(anchorTemporary)
			_ = os.Remove(hashesTemporary)
		}
	}()
	output, err := newAnchorWriter(anchorTemporary, kind, against, uint32(pageSize), uint32(pageCount))
	if err != nil {
		return 0, err
	}
	defer output.abandon()
	hashes, err := newAnchorWriter(hashesTemporary, -1, nil, uint32(pageSize), uint32(pageCount))
	if err != nil {
		return 0, err
	}
	defer hashes.abandon()
	hashes.head(hashesMagic, uint64(revision))

	rows, err := tx.QueryContext(ctx, `SELECT pgno, data FROM sqlite_dbpage`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	next := int64(1)
	empty := make([]byte, pageSize)
	var hashesFault error // pages.hashes does not read: no ground for a .pages
	page := func(number int64, data []byte) error {
		sum := sha256.Sum256(data)
		hashes.write(sum[:pageHashSize])
		if kind == anchorBase {
			output.write(data)
			return nil
		}
		if number <= int64(against.pageCount) {
			old, err := against.next()
			if err != nil {
				hashesFault = err
				return err
			}
			if bytes.Equal(old, sum[:pageHashSize]) {
				return nil
			}
		}
		output.writeNumber(uint32(number))
		output.write(data)
		return nil
	}
	scan := func() error {
		for rows.Next() {
			var number int64
			var data sql.RawBytes
			if err := rows.Scan(&number, &data); err != nil {
				return err
			}
			if number < next || number > pageCount || int64(len(data)) != pageSize {
				return fmt.Errorf("kitdb: sqlite_dbpage gave page %d of %d bytes after page %d of %d", number, len(data), next-1, pageCount)
			}
			for ; next < number; next++ { // a page sqlite_dbpage does not give holds nothing
				if err := page(next, empty); err != nil {
					return err
				}
			}
			if err := page(number, data); err != nil {
				return err
			}
			next++
			if next%4096 == 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for ; next <= pageCount; next++ {
			if err := page(next, empty); err != nil {
				return err
			}
		}
		rows.Close()
		return nil
	}
	scanErr := scan()
	if scanErr != nil && hashesFault == nil {
		return 0, scanErr
	}
	if against != nil && hashesFault == nil {
		hashesFault = against.finish()
	}
	if hashesFault != nil {
		// hashes that do not read are no ground for pages: they go, and the anchor keeps every page
		_ = output.abandon()
		_ = hashes.abandon()
		_ = os.Remove(anchorTemporary)
		_ = os.Remove(hashesTemporary)
		against.close()
		if err := os.Remove(filepath.Join(directory, hashesName)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return 0, err
		}
		written = true                               // nothing of this attempt is left
		return database.writeAnchorOf(ctx, snapshot) // the same pages, read again in the same transaction
	}
	if against != nil {
		against.close() // pages.hashes is replaced below, which Windows refuses while it is open
	}
	if err := output.finish(); err != nil {
		return 0, err
	}
	if err := hashes.finish(); err != nil {
		return 0, err
	}
	final := filepath.Join(directory, anchorName(uint64(revision), at, kind))
	if err := os.Rename(anchorTemporary, final); err != nil {
		return 0, err
	}
	written = true
	// The anchor is in place before its hashes: hashes of an anchor not written would chain the next .pages to
	// nothing, while hashes left behind by an anchor written chain it to an older one, which Restore follows.
	database.noteAnchor(uint64(revision))
	if err := os.Rename(hashesTemporary, filepath.Join(directory, hashesName)); err != nil {
		_ = os.Remove(hashesTemporary)
		return uint64(revision), fmt.Errorf("kitdb: anchor %d is written, its page hashes are not: %w", revision, err)
	}
	return uint64(revision), nil
}

// noteAnchor keeps revision as the newest anchor's when it is newer.
func (database *Database) noteAnchor(revision uint64) {
	for {
		known := database.anchored.Load()
		if known >= revision || database.anchored.CompareAndSwap(known, revision) {
			return
		}
	}
}

// anchorWhenStarted anchors the file in the background (anchorLater) when a table's history starts after its newest
// anchor (KITWORK_KITDB.md câu 13.11): the largest recorded of kitwork_tables among the tables whose history is on (the
// revision of a table's Tracked change, from which its changes are in the history) is past the newest anchor written
// or being written. A restore of the table to a moment from there on then starts from an anchor that crosses no start.
// recorded 1 is a table recorded from the file's first commit, a file's table of before (adopt), which waits for
// Retain's anchor as the file of before did. The writer calls it after a commit that changed what the history follows,
// with its own connection; Open, with the readers.
func (database *Database) anchorWhenStarted(ctx context.Context, querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}) error {
	var held int
	if err := querier.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name = 'kitwork_tables'`).Scan(&held); err != nil || held == 0 {
		return err
	}
	var started int64
	if err := querier.QueryRowContext(ctx, `SELECT coalesce(max(recorded), 0) FROM kitwork_tables WHERE history = 1 AND recorded > 1`).Scan(&started); err != nil {
		return err
	}
	if started == 0 || database.anchored.Load() >= uint64(started) {
		return nil
	}
	return database.anchorLater()
}

// anchorLater writes an anchor of the file as of its latest commit without holding up the caller: the read transaction
// the pages are copied in starts now, so the anchor is at this commit whatever the writer commits after, and a
// goroutine of its own writes them, after the anchor or the Retain running. Close stops it; Open starts again the
// anchor of a table's start it did not write (anchorWhenStarted).
func (database *Database) anchorLater() error {
	ctx := database.backgroundContext
	snapshot, err := database.openSnapshot(ctx)
	if err != nil {
		return err
	}
	database.noteAnchor(uint64(snapshot.revision))
	database.background.Add(1)
	go func() {
		defer database.background.Done()
		defer snapshot.close()
		database.anchoring.Lock()
		defer database.anchoring.Unlock()
		if ctx.Err() != nil {
			return
		}
		if _, err := database.writeAnchorOf(ctx, snapshot); err != nil && ctx.Err() == nil {
			database.backgroundMutex.Lock()
			if database.backgroundErr == nil {
				database.backgroundErr = fmt.Errorf("kitdb: the anchor at revision %d, written in the background: %w", snapshot.revision, err)
			}
			database.backgroundMutex.Unlock()
		}
	}()
	return nil
}

// anchorWriter writes a .base (the pages as they are), a .pages (each page after its number, a zero number ending
// them, then the SHA-256 of everything before) or pages.hashes (kind -1: a hash a page, then the SHA-256).
type anchorWriter struct {
	file      *os.File
	buffer    *bufio.Writer
	sum       hash.Hash
	kind      anchorKind
	pageSize  uint32
	pageCount uint32
	err       error
	closed    bool
}

func newAnchorWriter(path string, kind anchorKind, against *hashesReader, pageSize, pageCount uint32) (*anchorWriter, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	writer := &anchorWriter{file: file, buffer: bufio.NewWriterSize(file, 1<<20), sum: sha256.New(), kind: kind}
	writer.pageSize, writer.pageCount = pageSize, pageCount
	if kind == anchorPages {
		writer.head(pagesMagic, against.revision)
	}
	return writer, nil
}

func (writer *anchorWriter) head(magic string, revision uint64) {
	head := make([]byte, 0, anchorHeadSize)
	head = append(head, magic...)
	head = binary.BigEndian.AppendUint64(head, revision)
	head = binary.BigEndian.AppendUint32(head, writer.pageSize)
	head = binary.BigEndian.AppendUint32(head, writer.pageCount)
	writer.write(head)
}

func (writer *anchorWriter) writeNumber(number uint32) {
	writer.write(binary.BigEndian.AppendUint32(nil, number))
}

func (writer *anchorWriter) write(data []byte) {
	if writer.err != nil {
		return
	}
	if writer.kind != anchorBase {
		writer.sum.Write(data)
	}
	_, writer.err = writer.buffer.Write(data)
}

// finish ends the file (a .pages its zero number and checksum, pages.hashes its checksum), syncs and closes it.
func (writer *anchorWriter) finish() error {
	if writer.kind == anchorPages {
		writer.writeNumber(0)
	}
	if writer.kind != anchorBase && writer.err == nil {
		_, writer.err = writer.buffer.Write(writer.sum.Sum(nil))
	}
	if writer.err == nil {
		writer.err = writer.buffer.Flush()
	}
	if writer.err == nil {
		writer.err = writer.file.Sync()
	}
	writer.closed = true
	return errors.Join(writer.err, writer.file.Close())
}

func (writer *anchorWriter) abandon() error {
	if writer.closed {
		return nil
	}
	writer.closed = true
	return writer.file.Close()
}

// hashesReader reads pages.hashes a hash at a time; finish checks its checksum.
type hashesReader struct {
	file      *os.File
	buffer    *bufio.Reader
	sum       hash.Hash
	revision  uint64
	pageSize  uint32
	pageCount uint32
	read      uint32
	hash      [pageHashSize]byte
}

func openHashes(path string) (*hashesReader, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	reader := &hashesReader{file: file, buffer: bufio.NewReaderSize(file, 1<<20), sum: sha256.New()}
	head := make([]byte, anchorHeadSize)
	if _, err := io.ReadFull(reader.buffer, head); err != nil {
		file.Close()
		return nil, err
	}
	parsed, err := parseAnchorHead(head, hashesMagic)
	if err != nil {
		file.Close()
		return nil, err
	}
	reader.sum.Write(head)
	reader.revision, reader.pageSize, reader.pageCount = parsed.revision, parsed.pageSize, parsed.pageCount
	return reader, nil
}

func (reader *hashesReader) next() ([]byte, error) {
	if reader.read >= reader.pageCount {
		return nil, fmt.Errorf("%w: pages.hashes holds %d pages", ErrCorruptAnchor, reader.pageCount)
	}
	if _, err := io.ReadFull(reader.buffer, reader.hash[:]); err != nil {
		return nil, fmt.Errorf("%w: pages.hashes: %v", ErrCorruptAnchor, err)
	}
	reader.sum.Write(reader.hash[:])
	reader.read++
	return reader.hash[:], nil
}

// finish reads the hashes left and checks the checksum.
func (reader *hashesReader) finish() error {
	for reader.read < reader.pageCount {
		if _, err := reader.next(); err != nil {
			return err
		}
	}
	return readChecksum(reader.buffer, reader.sum, "pages.hashes")
}

func (reader *hashesReader) close() { _ = reader.file.Close() }

type anchorHead struct {
	revision  uint64
	pageSize  uint32
	pageCount uint32
}

func parseAnchorHead(head []byte, magic string) (anchorHead, error) {
	if len(head) != anchorHeadSize || string(head[:len(magic)]) != magic {
		return anchorHead{}, fmt.Errorf("%w: not %q", ErrCorruptAnchor, strings.TrimSpace(magic))
	}
	parsed := anchorHead{
		revision:  binary.BigEndian.Uint64(head[16:]),
		pageSize:  binary.BigEndian.Uint32(head[24:]),
		pageCount: binary.BigEndian.Uint32(head[28:]),
	}
	if parsed.pageSize < 512 || parsed.pageSize > 65536 || parsed.pageSize&(parsed.pageSize-1) != 0 {
		return anchorHead{}, fmt.Errorf("%w: page size %d", ErrCorruptAnchor, parsed.pageSize)
	}
	return parsed, nil
}

func readAnchorHead(path, magic string) (anchorHead, error) {
	file, err := os.Open(path)
	if err != nil {
		return anchorHead{}, err
	}
	defer file.Close()
	head := make([]byte, anchorHeadSize)
	if _, err := io.ReadFull(file, head); err != nil {
		return anchorHead{}, fmt.Errorf("%w: %s: %v", ErrCorruptAnchor, filepath.Base(path), err)
	}
	return parseAnchorHead(head, magic)
}

func readChecksum(reader *bufio.Reader, sum hash.Hash, name string) error {
	stored := make([]byte, sha256.Size)
	if _, err := io.ReadFull(reader, stored); err != nil {
		return fmt.Errorf("%w: %s ends early: %v", ErrCorruptAnchor, name, err)
	}
	if !bytes.Equal(stored, sum.Sum(nil)) {
		return fmt.Errorf("%w: the checksum of %s does not match", ErrCorruptAnchor, name)
	}
	if _, err := reader.ReadByte(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: %s goes on after its checksum", ErrCorruptAnchor, name)
	}
	return nil
}

// readPages reads a .pages through, handing each page to page in order (page must not keep the slice; nil only
// checks), and checks the checksum at its end. A page handed over before a bad checksum shows is in a file the
// caller then throws away, or was checked by an earlier read.
func readPages(path string, page func(number uint32, data []byte) error) (anchorHead, error) {
	file, err := os.Open(path)
	if err != nil {
		return anchorHead{}, err
	}
	defer file.Close()
	name := filepath.Base(path)
	reader := bufio.NewReaderSize(file, 1<<20)
	sum := sha256.New()
	head := make([]byte, anchorHeadSize)
	if _, err := io.ReadFull(reader, head); err != nil {
		return anchorHead{}, fmt.Errorf("%w: %s: %v", ErrCorruptAnchor, name, err)
	}
	parsed, err := parseAnchorHead(head, pagesMagic)
	if err != nil {
		return anchorHead{}, err
	}
	sum.Write(head)
	data := make([]byte, parsed.pageSize)
	number := make([]byte, 4)
	last := uint32(0)
	for {
		if _, err := io.ReadFull(reader, number); err != nil {
			return anchorHead{}, fmt.Errorf("%w: %s ends early: %v", ErrCorruptAnchor, name, err)
		}
		sum.Write(number)
		current := binary.BigEndian.Uint32(number)
		if current == 0 {
			break
		}
		if current <= last || current > parsed.pageCount {
			return anchorHead{}, fmt.Errorf("%w: %s holds page %d after page %d of %d", ErrCorruptAnchor, name, current, last, parsed.pageCount)
		}
		if _, err := io.ReadFull(reader, data); err != nil {
			return anchorHead{}, fmt.Errorf("%w: %s ends early: %v", ErrCorruptAnchor, name, err)
		}
		sum.Write(data)
		if page != nil {
			if err := page(current, data); err != nil {
				return anchorHead{}, err
			}
		}
		last = current
	}
	return parsed, readChecksum(reader, sum, name)
}

// basePageSize is the page size a SQLite file's header gives.
func basePageSize(path string) (uint32, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	head := make([]byte, 100)
	if _, err := io.ReadFull(file, head); err != nil || string(head[:16]) != "SQLite format 3\x00" {
		return 0, fmt.Errorf("%w: %s is not a SQLite file", ErrCorruptAnchor, filepath.Base(path))
	}
	size := uint32(binary.BigEndian.Uint16(head[16:]))
	if size == 1 {
		size = 65536
	}
	return size, nil
}

// corruptAnchor names the anchor file that does not read back as it was written.
type corruptAnchor struct {
	path string
	err  error // wraps ErrCorruptAnchor
}

func (fault *corruptAnchor) Error() string { return fault.err.Error() }
func (fault *corruptAnchor) Unwrap() error { return fault.err }

// checkChain reads each .pages of links after the first through and checks its checksum and page size against the
// base's. A file that does not read back is a *corruptAnchor naming it.
func checkChain(links []anchor) error {
	if len(links) < 2 {
		return nil
	}
	size, err := basePageSize(links[0].path)
	if err != nil {
		return &corruptAnchor{path: links[0].path, err: err}
	}
	for _, link := range links[1:] {
		head, err := readPages(link.path, nil)
		if err == nil && head.pageSize != size {
			err = fmt.Errorf("%w: %s holds pages of %d bytes, %s of %d", ErrCorruptAnchor, filepath.Base(link.path),
				head.pageSize, filepath.Base(links[0].path), size)
		}
		if errors.Is(err, ErrCorruptAnchor) {
			return &corruptAnchor{path: link.path, err: err}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// writeChain writes each .pages of links after the first into the file at target, in order, and cuts it to the
// page count of the last. The chain is checked through before a byte of it is written.
func writeChain(target string, links []anchor) error {
	if len(links) < 2 {
		return nil
	}
	if err := checkChain(links); err != nil {
		return err
	}
	output, err := os.OpenFile(target, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	var last anchorHead
	for _, link := range links[1:] {
		last, err = readPages(link.path, func(number uint32, data []byte) error {
			_, err := output.WriteAt(data, int64(number-1)*int64(len(data)))
			return err
		})
		if err != nil {
			_ = output.Close()
			return err
		}
	}
	err = output.Truncate(int64(last.pageCount) * int64(last.pageSize))
	if err == nil {
		err = output.Sync()
	}
	return errors.Join(err, output.Close())
}

// setAside renames an anchor file that does not read back to <name>.corrupt: out of every chain, kept to look at.
func setAside(path string) error {
	if err := os.Rename(path, path+".corrupt"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// restoreAnchor writes to target, which must not exist, the file as it was at item.
func restoreAnchor(anchors []anchor, item anchor, target string) error {
	links, err := chain(anchors, item)
	if err != nil {
		return err
	}
	if err := copyFile(links[0].path, target); err != nil {
		return err
	}
	if err := writeChain(target, links); err != nil {
		_ = os.Remove(target)
		return err
	}
	return nil
}

// fold writes the chain of start into its base, so the base becomes the file as it was at start and takes start's
// name, and removes the .pages it wrote. The marker naming the base, its new name and the .pages is in place before a
// page is written. The caller holds database.anchoring and database.anchorFiles.
func (database *Database) fold(anchors []anchor, start anchor) error {
	links, err := chain(anchors, start)
	if err != nil || len(links) < 2 {
		return err
	}
	if links[0].kind != anchorBase {
		return fmt.Errorf("%w: %s starts the chain of %s", ErrCorruptAnchor, filepath.Base(links[0].path), filepath.Base(start.path))
	}
	// A chain that does not read back gets no marker and the base no page: the file at fault is set aside, the
	// anchors chained through it drop out, and the next Retain keeps an older anchor or a newer one.
	if err := checkChain(links); err != nil {
		var fault *corruptAnchor
		if errors.As(err, &fault) {
			return errors.Join(err, setAside(fault.path))
		}
		return err
	}
	lines := []string{foldMagic, filepath.Base(links[0].path), anchorName(start.revision, start.at, anchorBase)}
	for _, link := range links[1:] {
		lines = append(lines, filepath.Base(link.path))
	}
	directory := database.anchorDirectory()
	temporary := filepath.Join(directory, fmt.Sprintf("fold-%d.tmp", time.Now().UnixNano()))
	if err := writeSynced(temporary, []byte(strings.Join(lines, "\n")+"\n")); err != nil {
		return err
	}
	if err := os.Rename(temporary, filepath.Join(directory, foldName)); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return database.finishFold()
}

// finishFold carries out the fold its marker names, from the start or from where it was cut short, and removes the
// marker; without a marker it does nothing. Writing a chain again over a base that holds part or all of it leaves the
// base as the chain's last anchor, since each page ends as the last .pages that holds it, or as the base when none
// does and no .pages reached it.
func (database *Database) finishFold() error {
	directory := database.anchorDirectory()
	marker := filepath.Join(directory, foldName)
	content, err := os.ReadFile(marker)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	lines := strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
	if len(lines) < 4 || lines[0] != foldMagic {
		return fmt.Errorf("%w: the fold marker does not read", ErrCorruptAnchor)
	}
	for _, name := range lines[1:] {
		if name != filepath.Base(name) || strings.ContainsAny(name, `/\`) {
			return fmt.Errorf("%w: the fold marker names %q", ErrCorruptAnchor, name)
		}
	}
	source, target := filepath.Join(directory, lines[1]), filepath.Join(directory, lines[2])
	if _, err := os.Stat(source); err == nil {
		links := []anchor{{path: source, kind: anchorBase}}
		for _, name := range lines[3:] {
			links = append(links, anchor{path: filepath.Join(directory, name), kind: anchorPages})
		}
		if err := writeChain(source, links); err != nil {
			var fault *corruptAnchor
			if !errors.As(err, &fault) {
				return fmt.Errorf("fold into %s: %w", lines[1], err)
			}
			// A .pages the fold had checked does not read back now. The base may hold part of the chain under its
			// old name, so it goes aside with the file at fault and the marker goes: the anchors chained through
			// them drop out, and the next anchor is a base.
			return errors.Join(fmt.Errorf("fold into %s: %w", lines[1], err), setAside(fault.path), setAside(source),
				os.Remove(marker))
		}
		if err := os.Rename(source, target); err != nil {
			return err
		}
	} else if _, err := os.Stat(target); err != nil {
		return fmt.Errorf("%w: the fold's base is neither %s nor %s", ErrCorruptAnchor, lines[1], lines[2])
	}
	for _, name := range lines[3:] {
		if err := os.Remove(filepath.Join(directory, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return os.Remove(marker)
}

// cleanAnchorDirectory finishes a fold cut short and removes what an anchor, its hashes or a fold left half written.
func (database *Database) cleanAnchorDirectory() error {
	entries, err := os.ReadDir(database.anchorDirectory())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".tmp") {
			if err := os.Remove(filepath.Join(database.anchorDirectory(), entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	// a fold that cannot be finished has set aside what it could not trust: the file opens without it
	if err := database.finishFold(); err != nil && !errors.Is(err, ErrCorruptAnchor) {
		return err
	}
	return nil
}

func writeSynced(path string, content []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, err = file.Write(content)
	if err == nil {
		err = file.Sync()
	}
	return errors.Join(err, file.Close())
}
