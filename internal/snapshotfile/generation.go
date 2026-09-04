package snapshotfile

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
)

const rootSize = 4096
const generationDataStart = 2 * rootSize
const maximumExtents = 4 * maximumEntries

// Extent is an immutable physical range. Entry order, not physical order,
// defines a section's logical byte stream.
type Extent struct {
	Offset int64
	Length int64
}

type generationState struct {
	identity          [16]byte
	number            uint64
	maximumNumber     uint64
	slot              int
	directoryOffset   int64
	directoryLength   int64
	directoryChecksum uint32
	fileBytes         int64
	liveBytes         int64
}

type GenerationInfo struct {
	Number        uint64
	FileBytes     int64
	LiveBytes     int64
	ObsoleteBytes int64
}

func (r *Reader) GenerationInfo() GenerationInfo {
	if r == nil || r.generation == nil {
		return GenerationInfo{}
	}
	g := r.generation
	return GenerationInfo{Number: g.number, FileBytes: g.fileBytes, LiveBytes: g.liveBytes, ObsoleteBytes: g.fileBytes - g.liveBytes}
}

// NeedsCompaction is an explicit-refresh policy, not a hard disk quota.
// Unpublished tails and old directories count as obsolete bytes as well.
func (r *Reader) NeedsCompaction() bool {
	i := r.GenerationInfo()
	return i.Number != 0 && i.ObsoleteBytes > max(i.LiveBytes, 1<<20)
}

func openGeneration(file *os.File, stat os.FileInfo) (*Reader, error) {
	if stat.Size() < generationDataStart {
		return nil, fmt.Errorf("snapshot: truncated generation roots")
	}
	var best *Reader
	var maximum uint64
	var identity [16]byte
	var haveIdentity bool
	for slot := range 2 {
		root := make([]byte, rootSize)
		if _, err := file.ReadAt(root, int64(slot*rootSize)); err != nil {
			return nil, err
		}
		if string(root[:8]) != "KSNAP002" || crc32.Checksum(root[:rootSize-4], checksum) != binary.LittleEndian.Uint32(root[rootSize-4:]) {
			continue
		}
		g := generationState{number: binary.LittleEndian.Uint64(root[8:]), slot: slot, fileBytes: stat.Size()}
		copy(g.identity[:], root[48:64])
		if g.number == 0 || g.identity == [16]byte{} || !bytes.Equal(root[44:48], make([]byte, 4)) || !bytes.Equal(root[64:rootSize-4], make([]byte, rootSize-68)) {
			continue
		}
		if haveIdentity && identity != g.identity {
			return nil, fmt.Errorf("snapshot: root identities disagree")
		}
		identity, haveIdentity = g.identity, true
		maximum = max(maximum, g.number)
		offset, length, end := binary.LittleEndian.Uint64(root[16:]), binary.LittleEndian.Uint64(root[24:]), binary.LittleEndian.Uint64(root[32:])
		if offset < generationDataStart || offset > uint64(stat.Size()) || length == 0 || length > maximumDirectory || length > uint64(stat.Size())-offset || end != offset+length {
			continue
		}
		g.directoryOffset, g.directoryLength = int64(offset), int64(length)
		g.directoryChecksum = binary.LittleEndian.Uint32(root[40:])
		data := make([]byte, int(length))
		if _, err := file.ReadAt(data, int64(offset)); err != nil {
			continue
		}
		if crc32.Checksum(data, checksum) != g.directoryChecksum {
			continue
		}
		r := &Reader{
			file: file, directoryBytes: len(data), generation: &g,
			entries: make(map[string]Entry), sections: make(map[string]*extentReader),
		}
		if err := json.Unmarshal(data, &r.directory); err != nil {
			continue
		}
		live, err := validateGenerationDirectory(r.directory, int64(offset))
		if err != nil {
			continue
		}
		g.liveBytes = generationDataStart + int64(length) + live
		for _, entry := range r.directory.Entries {
			r.entries[entry.Name] = entry
			r.sections[entry.Name] = newExtentReader(file, entry.Extents)
		}
		if best != nil && best.generation.number == g.number {
			return nil, fmt.Errorf("snapshot: duplicate generation roots")
		}
		if best == nil || g.number > best.generation.number {
			best = r
		}
	}
	if best == nil {
		return nil, fmt.Errorf("snapshot: no valid generation root/directory")
	}
	best.generation.maximumNumber = maximum
	return best, nil
}

func validateGenerationDirectory(d directory, end int64) (int64, error) {
	if len(d.Entries) > maximumEntries {
		return 0, fmt.Errorf("snapshot: too many entries")
	}
	names := make(map[string]bool, len(d.Entries))
	var ranges []Extent
	var live int64
	for _, entry := range d.Entries {
		if len(entry.Name) == 0 || len(entry.Name) > 512 || names[entry.Name] || entry.Offset != 0 || entry.Length < 0 || len(entry.Extents) > maximumExtents-len(ranges) {
			return 0, fmt.Errorf("snapshot: invalid generation entry")
		}
		names[entry.Name] = true
		var size int64
		for _, extent := range entry.Extents {
			if extent.Offset < generationDataStart || extent.Offset > end || extent.Length <= 0 || extent.Length > end-extent.Offset || extent.Length > entry.Length-size {
				return 0, fmt.Errorf("snapshot: invalid extent bounds")
			}
			size += extent.Length
			ranges = append(ranges, extent)
		}
		if size != entry.Length || size > math.MaxInt64-live {
			return 0, fmt.Errorf("snapshot: invalid logical section length")
		}
		live += size
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].Offset < ranges[j].Offset })
	var previous int64 = generationDataStart
	for _, extent := range ranges {
		if extent.Offset < previous {
			return 0, fmt.Errorf("snapshot: overlapping extents")
		}
		previous = extent.Offset + extent.Length
	}
	return live, nil
}

type extentReader struct {
	file    *os.File
	extents []Extent
	starts  []int64
	size    int64
}

func newExtentReader(file *os.File, extents []Extent) *extentReader {
	r := &extentReader{file: file, extents: extents, starts: make([]int64, len(extents))}
	for i, extent := range extents {
		r.starts[i] = r.size
		r.size += extent.Length
	}
	return r
}

func (r *extentReader) ReadAt(p []byte, offset int64) (int, error) {
	if offset < 0 {
		return 0, fmt.Errorf("snapshot: negative section offset")
	}
	if len(p) == 0 {
		return 0, nil
	}
	if offset >= r.size {
		return 0, io.EOF
	}
	i := sort.Search(len(r.starts), func(i int) bool { return r.starts[i] > offset }) - 1
	total := 0
	for len(p) > 0 && i < len(r.extents) {
		extent := r.extents[i]
		inside := offset - r.starts[i]
		length := min(int64(len(p)), extent.Length-inside)
		n, err := r.file.ReadAt(p[:length], extent.Offset+inside)
		total += n
		offset += int64(n)
		p = p[n:]
		if err != nil {
			return total, err
		}
		if int64(n) != length {
			return total, io.ErrUnexpectedEOF
		}
		i++
	}
	if len(p) > 0 {
		return total, io.EOF
	}
	return total, nil
}

func (r *Reader) rangeExtents(name string, offset, length int64) ([]Extent, error) {
	entry, ok := r.entries[name]
	if !ok || offset < 0 || length < 0 || offset > entry.Length || length > entry.Length-offset {
		return nil, fmt.Errorf("snapshot: invalid referenced range")
	}
	if length == 0 {
		return nil, nil
	}
	if r.generation == nil {
		return []Extent{{entry.Offset + offset, length}}, nil
	}
	var result []Extent
	starts := r.sections[name].starts
	index := sort.Search(len(starts), func(i int) bool { return starts[i] > offset }) - 1
	logical := starts[index]
	for _, extent := range entry.Extents[index:] {
		if logical+extent.Length > offset && logical < offset+length {
			start := max(offset, logical)
			end := min(offset+length, logical+extent.Length)
			result = append(result, Extent{extent.Offset + start - logical, end - start})
		}
		logical += extent.Length
		if logical >= offset+length {
			break
		}
	}
	return result, nil
}

// GenerationWriter requires an exclusive publisher for this path. Readers may
// keep old immutable sections open during append. Replacement/compaction must
// drain readers before Publish (also required by Windows file ownership).
type GenerationWriter struct {
	file      *os.File
	path      string
	temporary bool
	base      *Reader
	state     generationState
	entries   []Entry
	names     map[string]bool
	extents   int
	failed    bool
	written   int64
	position  int64
	info      GenerationInfo
	fault     func(string) error // Per-writer test seam; never a process-global hook.
}

func CreateGeneration(path string) (*GenerationWriter, error) {
	file, err := os.CreateTemp(filepath.Dir(path), ".projection-*")
	if err != nil {
		return nil, err
	}
	w := &GenerationWriter{file: file, path: path, temporary: true, names: make(map[string]bool)}
	if _, err := rand.Read(w.state.identity[:]); err != nil {
		w.Close()
		return nil, err
	}
	w.state.number, w.state.slot = 1, 1
	if err := w.write(make([]byte, generationDataStart)); err != nil {
		w.Close()
		return nil, err
	}
	return w, nil
}

func AppendGeneration(path string, base *Reader) (*GenerationWriter, error) {
	if base == nil || base.generation == nil || base.generation.maximumNumber == math.MaxUint64 {
		return nil, fmt.Errorf("snapshot: base cannot append generations")
	}
	stat, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !stat.Mode().IsRegular() {
		return nil, fmt.Errorf("snapshot: append target is not regular")
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*GenerationWriter, error) { file.Close(); return nil, err }
	baseStat, err := base.file.Stat()
	if err != nil {
		return fail(err)
	}
	currentStat, err := file.Stat()
	if err != nil {
		return fail(err)
	}
	if !os.SameFile(baseStat, currentStat) || !os.SameFile(stat, currentStat) {
		return fail(fmt.Errorf("snapshot: append target changed"))
	}
	current, err := open(file)
	if err != nil {
		return fail(err)
	}
	g := current.generation
	old := base.generation
	if g == nil || g.maximumNumber == math.MaxUint64 || g.identity != old.identity || g.number != old.number || g.directoryOffset != old.directoryOffset || g.directoryChecksum != old.directoryChecksum {
		return fail(fmt.Errorf("snapshot: stale append base"))
	}
	position, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		return fail(err)
	}
	w := &GenerationWriter{file: file, path: path, base: base, names: make(map[string]bool), state: *g, position: position}
	w.state.number = g.maximumNumber + 1
	w.state.slot = 1 - g.slot
	return w, nil
}

func (w *GenerationWriter) Appending() bool      { return w != nil && !w.temporary }
func (w *GenerationWriter) BytesWritten() int64  { return w.written }
func (w *GenerationWriter) Info() GenerationInfo { return w.info }

func (w *GenerationWriter) write(data []byte) error {
	n, err := w.file.Write(data)
	w.written += int64(n)
	w.position += int64(n)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err != nil {
		w.failed = true
	}
	return err
}

func (w *GenerationWriter) point(name string) error {
	if w.fault != nil {
		return w.fault(name)
	}
	return nil
}

func (w *GenerationWriter) Add(name string, write func(io.Writer) error) error {
	if w.file == nil || w.failed || write == nil || len(name) == 0 || len(name) > 512 || w.names[name] || len(w.entries) >= maximumEntries {
		return fmt.Errorf("snapshot: invalid generation entry/writer")
	}
	out := &generationEntryWriter{owner: w, entry: Entry{Name: name}}
	defer func() { out.closed = true }()
	if err := write(out); err != nil {
		w.failed = true
		return err
	}
	if w.failed {
		return fmt.Errorf("snapshot: failed entry writer")
	}
	out.closed = true
	w.entries = append(w.entries, out.entry)
	w.names[name] = true
	if err := w.point("after-entry"); err != nil {
		w.failed = true
		return err
	}
	return nil
}

type generationEntryWriter struct {
	owner  *GenerationWriter
	entry  Entry
	closed bool
}

func (out *generationEntryWriter) addExtent(extent Extent) error {
	if extent.Length == 0 {
		return nil
	}
	if extent.Length < 0 || extent.Length > math.MaxInt64-out.entry.Length {
		return fmt.Errorf("snapshot: section length overflow")
	}
	n := len(out.entry.Extents)
	if n > 0 && out.entry.Extents[n-1].Offset+out.entry.Extents[n-1].Length == extent.Offset {
		out.entry.Extents[n-1].Length += extent.Length
	} else {
		if out.owner.extents >= maximumExtents {
			return fmt.Errorf("snapshot: extent budget exceeded")
		}
		out.entry.Extents = append(out.entry.Extents, extent)
		out.owner.extents++
	}
	out.entry.Length += extent.Length
	return nil
}

func (out *generationEntryWriter) Write(data []byte) (int, error) {
	w := out.owner
	if out.closed || w.file == nil || w.failed {
		return 0, fmt.Errorf("snapshot: closed/failed entry")
	}
	offset := w.position
	n, err := w.file.Write(data)
	w.written += int64(n)
	w.position += int64(n)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = out.addExtent(Extent{offset, int64(n)})
	}
	if err != nil {
		w.failed = true
	}
	return n, err
}

// Reference adds a logical range without reading or writing its bytes. The
// caller must verify the referenced format/checksums before calling this.
func Reference(output io.Writer, source *Reader, name string, offset, length int64) error {
	out, ok := output.(*generationEntryWriter)
	if !ok || out.closed || out.owner.failed || out.owner.file == nil || source == nil || source != out.owner.base {
		if ok && !out.closed {
			out.owner.failed = true
		}
		return fmt.Errorf("snapshot: invalid reference owner")
	}
	ranges, err := source.rangeExtents(name, offset, length)
	if err == nil {
		for _, extent := range ranges {
			if err = out.addExtent(extent); err != nil {
				break
			}
		}
	}
	if err != nil {
		out.owner.failed = true
	}
	return err
}

func (w *GenerationWriter) Publish(ctx context.Context, metadata any) error {
	if ctx == nil || w.file == nil || w.failed {
		return fmt.Errorf("snapshot: invalid generation publication")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	w.failed = true // A publication attempt is not retryable, even after sync fails.
	meta, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	d := directory{Metadata: meta, Entries: w.entries}
	offset, err := w.file.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	live, err := validateGenerationDirectory(d, offset)
	if err != nil {
		return err
	}
	data, err := json.Marshal(d)
	if err != nil {
		return err
	}
	if len(data) > maximumDirectory {
		return fmt.Errorf("snapshot: directory budget exceeded")
	}
	if err := w.write(data); err != nil {
		return err
	}
	if err := w.file.Sync(); err != nil {
		return err
	}
	if err := w.point("after-directory-sync"); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	root := make([]byte, rootSize)
	copy(root, "KSNAP002")
	binary.LittleEndian.PutUint64(root[8:], w.state.number)
	binary.LittleEndian.PutUint64(root[16:], uint64(offset))
	binary.LittleEndian.PutUint64(root[24:], uint64(len(data)))
	binary.LittleEndian.PutUint64(root[32:], uint64(offset)+uint64(len(data)))
	binary.LittleEndian.PutUint32(root[40:], crc32.Checksum(data, checksum))
	copy(root[48:64], w.state.identity[:])
	binary.LittleEndian.PutUint32(root[rootSize-4:], crc32.Checksum(root[:rootSize-4], checksum))
	n, err := w.file.WriteAt(root, int64(w.state.slot*rootSize))
	w.written += int64(n)
	if err != nil {
		return err
	}
	if n != len(root) {
		return io.ErrShortWrite
	}
	if err := w.point("after-root-write"); err != nil {
		return err
	}
	// After the root write, cancellation must not pretend publication was undone.
	if err := w.file.Sync(); err != nil {
		return err
	}
	if err := w.point("after-root-sync"); err != nil {
		return err
	}
	w.info = GenerationInfo{Number: w.state.number, FileBytes: offset + int64(len(data)), LiveBytes: generationDataStart + int64(len(data)) + live}
	w.info.ObsoleteBytes = w.info.FileBytes - w.info.LiveBytes
	name := w.file.Name()
	if err := w.file.Close(); err != nil {
		return err
	}
	w.file = nil
	if !w.temporary {
		return nil
	}
	if err := w.point("before-rename"); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, w.path); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := w.point("after-rename"); err != nil {
		return err
	}
	return syncParent(filepath.Dir(w.path))
}

func (w *GenerationWriter) Close() error {
	if w == nil || w.file == nil {
		return nil
	}
	name := w.file.Name()
	err := w.file.Close()
	w.file = nil
	if w.temporary {
		err = errors.Join(err, os.Remove(name))
	}
	// Never truncate an append tail: another reader may have pinned a root that
	// was later damaged. Safe reclamation is a drained whole-file compaction.
	return err
}
