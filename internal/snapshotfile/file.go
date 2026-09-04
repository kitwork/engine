// Package snapshotfile provides experimental containers with immutable sections
// for rebuildable projections. It is not a transaction log or a backup format.
package snapshotfile

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
)

const headerSize = 40
const maximumDirectory = 8 << 20
const maximumEntries = 16384

var checksum = crc32.MakeTable(crc32.Castagnoli)

type Entry struct {
	Name    string
	Offset  int64
	Length  int64
	Extents []Extent `json:",omitempty"`
}

type directory struct {
	Metadata json.RawMessage
	Entries  []Entry
}

type Writer struct {
	file    *os.File
	path    string
	entries []Entry
	names   map[string]bool
	failed  bool
}

func Create(path string) (*Writer, error) {
	file, err := os.CreateTemp(filepath.Dir(path), ".projection-*")
	if err != nil {
		return nil, err
	}
	w := &Writer{file: file, path: path, names: make(map[string]bool)}
	if _, err := file.Write(make([]byte, headerSize)); err != nil {
		w.Close()
		return nil, err
	}
	return w, nil
}

// Add streams one entry; an unsuccessful callback poisons the unpublished file.
func (w *Writer) Add(name string, write func(io.Writer) error) error {
	if w.file == nil || w.failed || write == nil || len(name) == 0 || len(name) > 512 || w.names[name] || len(w.entries) >= maximumEntries {
		return fmt.Errorf("snapshot: invalid entry or closed/failed writer")
	}
	start, err := w.file.Seek(0, io.SeekCurrent)
	if err != nil {
		w.failed = true
		return err
	}
	if err := write(w.file); err != nil {
		w.failed = true
		return err
	}
	end, err := w.file.Seek(0, io.SeekCurrent)
	if err != nil {
		w.failed = true
		return err
	}
	w.entries = append(w.entries, Entry{Name: name, Offset: start, Length: end - start})
	w.names[name] = true
	return nil
}

func (w *Writer) Publish(ctx context.Context, metadata any) error {
	if ctx == nil {
		return fmt.Errorf("snapshot: nil publication context")
	}
	if w.file == nil || w.failed {
		return fmt.Errorf("snapshot: closed/failed writer")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	meta, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	data, err := json.Marshal(directory{Metadata: meta, Entries: w.entries})
	if err != nil {
		return err
	}
	if len(data) > maximumDirectory {
		return fmt.Errorf("snapshot: directory exceeds %d bytes", maximumDirectory)
	}
	w.failed = true // Publication cannot be retried after an I/O failure.
	offset, err := w.file.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	if _, err := w.file.Write(data); err != nil {
		return err
	}
	header := make([]byte, headerSize)
	copy(header, "KSNAP001")
	binary.LittleEndian.PutUint64(header[8:], uint64(offset))
	binary.LittleEndian.PutUint64(header[16:], uint64(len(data)))
	binary.LittleEndian.PutUint32(header[24:], crc32.Checksum(data, checksum))
	binary.LittleEndian.PutUint32(header[36:], crc32.Checksum(header[:36], checksum))
	if _, err := w.file.WriteAt(header, 0); err != nil {
		return err
	}
	if err := w.file.Sync(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	name := w.file.Name()
	if err := w.file.Close(); err != nil {
		return err
	}
	w.file = nil
	if err := os.Rename(name, w.path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return syncParent(filepath.Dir(w.path))
}

func (w *Writer) Close() error {
	if w == nil || w.file == nil {
		return nil
	}
	name := w.file.Name()
	err := w.file.Close()
	w.file = nil
	_ = os.Remove(name)
	return err
}

type Reader struct {
	file           *os.File
	directory      directory
	directoryBytes int
	entries        map[string]Entry
	generation     *generationState
	sections       map[string]*extentReader
}

func Open(path string) (*Reader, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	r, err := open(file)
	if err != nil {
		_ = file.Close()
	}
	return r, err
}

func open(file *os.File) (*Reader, error) {
	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !stat.Mode().IsRegular() || stat.Size() < headerSize {
		return nil, fmt.Errorf("snapshot: not a regular snapshot file")
	}
	header := make([]byte, headerSize)
	if _, err := file.ReadAt(header, 0); err != nil {
		return nil, err
	}
	if string(header[:8]) != "KSNAP001" {
		return openGeneration(file, stat)
	}
	if string(header[:8]) != "KSNAP001" || crc32.Checksum(header[:36], checksum) != binary.LittleEndian.Uint32(header[36:]) {
		return nil, fmt.Errorf("snapshot: invalid header/checksum")
	}
	offset, length := binary.LittleEndian.Uint64(header[8:]), binary.LittleEndian.Uint64(header[16:])
	if offset < headerSize || offset > uint64(stat.Size()) || length > maximumDirectory || length != uint64(stat.Size())-offset {
		return nil, fmt.Errorf("snapshot: invalid directory bounds")
	}
	data := make([]byte, int(length))
	if _, err := file.ReadAt(data, int64(offset)); err != nil {
		return nil, err
	}
	if crc32.Checksum(data, checksum) != binary.LittleEndian.Uint32(header[24:]) {
		return nil, fmt.Errorf("snapshot: directory checksum mismatch")
	}
	r := &Reader{file: file, directoryBytes: len(data), entries: make(map[string]Entry)}
	if err := json.Unmarshal(data, &r.directory); err != nil {
		return nil, err
	}
	if len(r.directory.Entries) > maximumEntries {
		return nil, fmt.Errorf("snapshot: too many entries")
	}
	end := int64(headerSize)
	for _, entry := range r.directory.Entries {
		if len(entry.Name) == 0 || len(entry.Name) > 512 || entry.Offset != end || entry.Length < 0 || entry.Length > int64(offset)-end {
			return nil, fmt.Errorf("snapshot: invalid entry bounds")
		}
		if _, exists := r.entries[entry.Name]; exists {
			return nil, fmt.Errorf("snapshot: duplicate entry")
		}
		r.entries[entry.Name] = entry
		end += entry.Length
	}
	if end != int64(offset) {
		return nil, fmt.Errorf("snapshot: unaccounted payload")
	}
	return r, nil
}

func (r *Reader) Metadata(target any) error { return json.Unmarshal(r.directory.Metadata, target) }

// DirectoryBytes is the verified serialized directory retained by this reader.
// Owners can use it to bound long-lived metadata caches without knowing the
// snapshot format's internal JSON representation.
func (r *Reader) DirectoryBytes() int {
	if r == nil {
		return 0
	}
	return r.directoryBytes
}

// Section borrows the container handle. Close the container after all readers drain.
func (r *Reader) Section(name string) (*io.SectionReader, error) {
	entry, exists := r.entries[name]
	if !exists {
		return nil, fmt.Errorf("snapshot: missing entry %q", name)
	}
	if r.generation != nil {
		if len(entry.Extents) == 1 {
			return io.NewSectionReader(r.file, entry.Extents[0].Offset, entry.Length), nil
		}
		return io.NewSectionReader(r.sections[name], 0, entry.Length), nil
	}
	return io.NewSectionReader(r.file, entry.Offset, entry.Length), nil
}

func (r *Reader) Close() error { return r.file.Close() }

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func Copy(ctx context.Context, dst io.Writer, src io.Reader) error {
	if ctx == nil {
		return fmt.Errorf("snapshot: nil copy context")
	}
	_, err := io.CopyBuffer(dst, contextReader{ctx, src}, make([]byte, 64<<10))
	return err
}
