package analytics

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kitwork/engine/internal/snapshotfile"
)

// fileFormat names the metadata of a File's generation.
const fileFormat = "kitwork-analytics/1"

// readHandles is how many handles of the system each generation reads with: queries scan at once.
const readHandles = 8

// moveAttempts is how many times a compaction tries to move its file over the path once every reader of the old one
// is done, another program (a scanner of the system) holding it a moment.
var moveAttempts = 10

// errClosed is an operation on a File already closed.
var errClosed = errors.New("analytics: the file of the copies is closed")

// File is every table's copy of one database file, in one file beside it (Path: <file>.analytics; KITWORK_KITDB.md
// mục 3 and 6). It is a KSNAP002 container (internal/snapshotfile), as <file>.search is: a generation names, for each
// table, its manifest and the section holding its blocks. A build or a fold appends a generation, the table's new
// section and every other table's section referenced as it is, then switches the root: a crash leaves the generation
// before. A table read keeps the generation it was opened from readable until it is closed.
//
// The bytes no generation reads any more stay in the file until it is compacted (Compact): the live sections are
// copied into <file>.analytics.compact, the tables move to it (Moved), and once every reader of the old file is done
// the copy moves over the path (Windows refuses to replace a file still open).
//
// One process writes a File: the one holding the database's writer lock; a second one appending fails, the file
// having moved under it.
type File struct {
	// Moved, when set, is called once the current generation changed (a build, a fold, a compaction), outside the
	// File's locks: the owner opens the tables it keeps again, so the generations before close.
	Moved func()

	path    string
	publish sync.Mutex // one publisher: a build, a fold or a compaction

	mutex       sync.Mutex
	current     *generation
	live        string                   // the file current reads: path, or path+".compact" while a compaction could not move it
	generations map[*generation]struct{} // the generations open
	changed     chan struct{}            // closed, and made again, when a generation closes
	closed      bool
}

// fileMetadata is what a generation of a File says.
type fileMetadata struct {
	Format string               `json:"format"`
	Next   uint64               `json:"next"` // the number of the next section
	Tables map[string]*Manifest `json:"tables"`
}

// generation is one opened generation of a File. The File holds one reference while it is current, each copy opened
// from it one more; the last release closes its handles.
type generation struct {
	file       *File
	reader     *snapshotfile.Reader
	metadata   fileMetadata
	from       string // the file it reads; guarded by file.mutex
	references atomic.Int64
}

func (g *generation) acquire() { g.references.Add(1) }

func (g *generation) release() {
	if g.references.Add(-1) == 0 {
		_ = g.reader.Close()
		g.file.closedGeneration(g)
	}
}

// OpenFile opens the copies' file at path (Path of a database file). There may be none yet: the first build makes
// it. What the File cannot read is derived data and goes, to be built again: a file that does not open as one (said
// in the log), a compaction a process ended before moving, and the directory of copies the first KCOL engine on
// SQLite wrote (05/11/2026: one .kcol and one .json per table), whose own files alone are removed.
func OpenFile(path string) (*File, error) {
	path = filepath.Clean(path)
	f := &File{path: path, live: path, generations: map[*generation]struct{}{}, changed: make(chan struct{})}
	if info, err := os.Lstat(path + ".compact"); err == nil && info.Mode().IsRegular() {
		if err := os.Remove(path + ".compact"); err != nil {
			return nil, err
		}
	}
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return f, nil
	case err != nil:
		return nil, err
	case info.IsDir():
		return f, removeDirectoryOfCopies(path)
	case !info.Mode().IsRegular():
		return nil, fmt.Errorf("analytics: %s is not a regular file", path)
	}
	g, err := f.openGeneration(path)
	if err != nil {
		slog.Warn("analytics: the copies' file does not open; it is removed and the copies built again", "file", path, "error", err)
		if err := os.Remove(path); err != nil {
			return nil, err
		}
		return f, nil
	}
	f.current = g
	return f, nil
}

// removeDirectoryOfCopies removes the directory of copies the first KCOL engine on SQLite wrote at path (05/11/2026):
// its main parts (<table>-<revision>-<stamp>.kcol), manifests (<table>.json) and what a build cut short left (.tmp).
// A file of another name is not the engine's: nothing is removed, and the File is not built over it.
func removeDirectoryOfCopies(path string) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.Type().IsRegular() || !(strings.HasSuffix(name, ".kcol") || strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".tmp")) {
			return fmt.Errorf("analytics: %s is a directory holding %q, not a copy: the copies' file needs its name", path, name)
		}
	}
	for _, entry := range entries {
		if err := os.Remove(filepath.Join(path, entry.Name())); err != nil {
			return err
		}
	}
	return os.Remove(path)
}

// openGeneration opens the newest generation of the file from; the caller holds no lock of the File.
func (f *File) openGeneration(from string) (*generation, error) {
	reader, err := snapshotfile.OpenRenamable(from, readHandles)
	if err != nil {
		return nil, err
	}
	var metadata fileMetadata
	if err := reader.Metadata(&metadata); err != nil || metadata.Format != fileFormat {
		_ = reader.Close()
		return nil, fmt.Errorf("analytics: %s is not a %s file", from, fileFormat)
	}
	if metadata.Tables == nil {
		metadata.Tables = map[string]*Manifest{}
	}
	for name, manifest := range metadata.Tables {
		if manifest == nil || manifest.Table != name || reader.SectionExtents(manifest.Section) == 0 && manifest.Bytes != 0 {
			_ = reader.Close()
			return nil, fmt.Errorf("analytics: %s names table %q without its section", from, name)
		}
	}
	g := &generation{file: f, reader: reader, metadata: metadata, from: from}
	g.references.Store(1)
	f.mutex.Lock()
	f.generations[g] = struct{}{}
	f.mutex.Unlock()
	return g, nil
}

func (f *File) closedGeneration(g *generation) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	delete(f.generations, g)
	close(f.changed)
	f.changed = make(chan struct{})
}

// Table opens table's copy as the current generation holds it: nil and no error when it holds none.
func (f *File) Table(table string) (*Table, error) {
	f.mutex.Lock()
	g := f.current
	if g == nil || f.closed || g.metadata.Tables[table] == nil {
		f.mutex.Unlock()
		return nil, nil
	}
	g.acquire()
	f.mutex.Unlock()
	copy, err := openCopy(g, g.metadata.Tables[table])
	if err != nil {
		g.release()
		return nil, err
	}
	return newTable(copy), nil
}

// Tables are the names of the tables the current generation holds a copy of.
func (f *File) Tables() []string {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	if f.current == nil {
		return nil
	}
	return sortedKeys(f.current.metadata.Tables)
}

// Info is the current generation: how many bytes the file holds, and how many of them no generation reads.
func (f *File) Info() snapshotfile.GenerationInfo {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	if f.current == nil {
		return snapshotfile.GenerationInfo{}
	}
	return f.current.reader.GenerationInfo()
}

// SectionExtents is how many ranges of the file table's section is made of; 0 when there is none.
func (f *File) SectionExtents(table string) int {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	if f.current == nil || f.current.metadata.Tables[table] == nil {
		return 0
	}
	return f.current.reader.SectionExtents(f.current.metadata.Tables[table].Section)
}

// NeedsCompaction says the file holds more bytes no generation reads than ones it does (and over 1 MiB), or that its
// sections are made of so many ranges that a few more folds would pass the directory's budget.
func (f *File) NeedsCompaction() bool {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	g := f.current
	if g == nil {
		return false
	}
	if g.reader.NeedsCompaction() {
		return true
	}
	extents := 0
	for _, manifest := range g.metadata.Tables {
		extents += g.reader.SectionExtents(manifest.Section)
	}
	return extents > snapshotfile.MaximumExtents/4
}

// publishLocked appends a generation in which table's section is what write writes, under the manifest it returns,
// and every other table's section is referenced as it is; it then becomes current. write is given the generation it
// appends to (nil for the file's first), which it may reference. f.publish is held.
func (f *File) publishLocked(ctx context.Context, table string, write func(output io.Writer, base *generation) (*Manifest, error)) (*Manifest, error) {
	f.mutex.Lock()
	if f.closed {
		f.mutex.Unlock()
		return nil, errClosed
	}
	base, live := f.current, f.live
	if base != nil {
		base.acquire()
	}
	f.mutex.Unlock()
	metadata := fileMetadata{Format: fileFormat, Next: 1, Tables: map[string]*Manifest{}}
	var writer *snapshotfile.GenerationWriter
	var err error
	if base == nil {
		if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
			return nil, err
		}
		writer, err = snapshotfile.CreateGeneration(f.path)
	} else {
		defer base.release()
		metadata.Next = base.metadata.Next
		maps.Copy(metadata.Tables, base.metadata.Tables)
		writer, err = snapshotfile.AppendGeneration(live, base.reader)
	}
	if err != nil {
		return nil, err
	}
	defer writer.Close()
	section := strconv.FormatUint(metadata.Next, 10) + ".kcol"
	metadata.Next++
	var manifest *Manifest
	if err := writer.Add(section, func(output io.Writer) error {
		var err error
		manifest, err = write(output, base)
		return err
	}); err != nil {
		return nil, err
	}
	manifest.Table, manifest.Section = table, section
	metadata.Tables[table] = manifest
	for _, name := range sortedKeys(metadata.Tables) {
		other := metadata.Tables[name]
		if name == table {
			continue
		}
		if err := writer.Add(other.Section, func(output io.Writer) error {
			return snapshotfile.Reference(output, base.reader, other.Section, 0, other.Bytes)
		}); err != nil {
			return nil, err
		}
	}
	if err := writer.Publish(ctx, metadata); err != nil {
		return nil, err
	}
	next, err := f.openGeneration(live)
	if err != nil {
		return nil, err
	}
	f.swap(next, live)
	return manifest, nil
}

// swap makes next, read from live, the current generation.
func (f *File) swap(next *generation, live string) {
	f.mutex.Lock()
	previous := f.current
	if f.closed {
		previous = next // a Close while it was written: nothing keeps it
	} else {
		f.current, f.live = next, live
	}
	f.mutex.Unlock()
	if previous != nil {
		previous.release()
	}
}

func (f *File) moved() {
	if f.Moved != nil {
		f.Moved()
	}
}

// Compact copies the live sections into a fresh file and moves it over the path once every reader of the file before
// is done, waiting for them until ctx ends (the copies are then read from <file>.analytics.compact, which the next
// compaction moves, or the next start removes).
func (f *File) Compact(ctx context.Context) error {
	f.publish.Lock()
	defer f.publish.Unlock()
	f.mutex.Lock()
	base, old := f.current, f.live
	if base == nil || f.closed {
		f.mutex.Unlock()
		return nil
	}
	base.acquire()
	f.mutex.Unlock()
	target := f.path + ".compact"
	if old == target {
		target = f.path // a compaction before could not move: the file at path is no generation's any more
	}
	err := func() error {
		defer base.release()
		writer, err := snapshotfile.CreateGeneration(target)
		if err != nil {
			return err
		}
		defer writer.Close()
		for _, name := range sortedKeys(base.metadata.Tables) {
			manifest := base.metadata.Tables[name]
			section, err := base.reader.Section(manifest.Section)
			if err != nil {
				return err
			}
			if err := writer.Add(manifest.Section, func(output io.Writer) error {
				return snapshotfile.Copy(ctx, output, section)
			}); err != nil {
				return err
			}
		}
		return writer.Publish(ctx, base.metadata)
	}()
	if err != nil {
		if target != f.path {
			_ = os.Remove(target)
		}
		return err
	}
	next, err := f.openGeneration(target)
	if err != nil {
		return err
	}
	f.swap(next, target)
	f.moved()
	if err := f.waitClosed(ctx, old); err != nil {
		return err
	}
	if target == f.path {
		return os.Remove(old) // the .compact the copies were read from while it could not move
	}
	var moveErr error
	for attempt := range moveAttempts {
		if moveErr = os.Rename(target, f.path); moveErr == nil {
			break
		}
		time.Sleep(time.Duration(attempt+1) * 50 * time.Millisecond)
	}
	if moveErr != nil {
		return fmt.Errorf("analytics: the compacted copies stay in %s: %w", target, moveErr)
	}
	f.mutex.Lock()
	if f.live == target {
		f.live = f.path
	}
	for g := range f.generations {
		if g.from == target {
			g.from = f.path
		}
	}
	f.mutex.Unlock()
	return nil
}

// waitClosed waits until no generation reads the file from.
func (f *File) waitClosed(ctx context.Context, from string) error {
	for {
		f.mutex.Lock()
		reading := false
		for g := range f.generations {
			reading = reading || g.from == from
		}
		if !reading {
			f.mutex.Unlock()
			return nil
		}
		changed := f.changed
		f.mutex.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Close lets the current generation go; the tables still read keep theirs until closed.
func (f *File) Close() error {
	f.mutex.Lock()
	if f.closed {
		f.mutex.Unlock()
		return nil
	}
	f.closed = true
	current := f.current
	f.current = nil
	f.mutex.Unlock()
	if current != nil {
		current.release()
	}
	return nil
}

func sortedKeys(tables map[string]*Manifest) []string {
	names := make([]string, 0, len(tables))
	for name := range tables {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
