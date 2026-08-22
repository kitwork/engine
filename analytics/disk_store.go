package analytics

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const (
	segmentFilePrefix = "segment-"
	segmentFileSuffix = ".analytics"
	manifestFilename  = "manifest.json"
	manifestVersion   = uint32(1)
)

type diskManifest struct {
	Version    uint32   `json:"version"`
	SchemaHash string   `json:"schema_hash"`
	NextID     uint64   `json:"next_id"`
	Segments   []string `json:"segments"`
}

// DiskStore keeps an analytics store durable across restarts.
type DiskStore struct {
	mu         sync.Mutex
	directory  string
	schema     Schema
	schemaHash [32]byte
	store      *Store
	manifest   diskManifest
}

// OpenDiskStore opens or creates one durable analytics store directory.
func OpenDiskStore(directory string, schema Schema) (*DiskStore, error) {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	hash := schema.Fingerprint()
	store := NewStore(schema)
	disk := &DiskStore{
		directory:  directory,
		schema:     schema,
		schemaHash: hash,
		store:      store,
		manifest: diskManifest{
			Version:    manifestVersion,
			SchemaHash: hex.EncodeToString(hash[:]),
			NextID:     1,
			Segments:   nil,
		},
	}

	manifest, exists, err := readDiskManifest(filepath.Join(directory, manifestFilename))
	if err != nil {
		return nil, err
	}
	if exists {
		if err := disk.loadManifest(manifest); err != nil {
			return nil, err
		}
		return disk, nil
	}

	segments, nextID, err := disk.scanExistingSegments()
	if err != nil {
		return nil, err
	}
	disk.manifest.Segments = segments
	disk.manifest.NextID = nextID
	if len(segments) != 0 {
		if err := disk.persistManifest(); err != nil {
			return nil, err
		}
	}
	return disk, nil
}

// Close is present for symmetry. The current durable store owns no extra OS handle.
func (disk *DiskStore) Close() error {
	return nil
}

// Schema returns the durable store schema.
func (disk *DiskStore) Schema() Schema { return disk.schema }

// Rows returns the total row count.
func (disk *DiskStore) Rows() int { return disk.store.Rows() }

// Segments returns the number of visible immutable segments.
func (disk *DiskStore) Segments() int { return disk.store.Segments() }

// Scan returns rows that match the predicates.
func (disk *DiskStore) Scan(ctx context.Context, predicates ...Predicate) ([]Row, error) {
	return disk.store.Scan(ctx, predicates...)
}

// Count returns the number of matching rows.
func (disk *DiskStore) Count(ctx context.Context, predicates ...Predicate) (int64, error) {
	return disk.store.Count(ctx, predicates...)
}

// GroupBy computes grouped aggregates from the durable snapshot.
func (disk *DiskStore) GroupBy(ctx context.Context, groupColumns []string, predicates []Predicate, aggregates ...Aggregate) ([]GroupResult, error) {
	return disk.store.GroupBy(ctx, groupColumns, predicates, aggregates...)
}

// Append adds one immutable segment and publishes it to disk.
func (disk *DiskStore) Append(rows []Row) error {
	if len(rows) == 0 {
		return nil
	}
	builder := NewBuilder(disk.schema)
	if err := builder.AddRows(rows); err != nil {
		return err
	}
	segment, err := builder.Build()
	if err != nil {
		return err
	}

	disk.mu.Lock()
	defer disk.mu.Unlock()

	previousManifest := disk.manifest
	name := segmentFileName(disk.manifest.NextID)
	path := filepath.Join(disk.directory, name)
	if err := writeSegmentFile(path, segment); err != nil {
		return err
	}
	disk.manifest.NextID++
	disk.manifest.Segments = append(disk.manifest.Segments, name)
	if err := disk.persistManifestLocked(); err != nil {
		disk.manifest = previousManifest
		_ = os.Remove(path)
		return err
	}
	disk.store.appendSegment(segment)
	return nil
}

// ReplaceAll swaps the visible snapshot and writes a fresh durable segment.
func (disk *DiskStore) ReplaceAll(rows []Row) error {
	builder := NewBuilder(disk.schema)
	if err := builder.AddRows(rows); err != nil {
		return err
	}
	segment, err := builder.Build()
	if err != nil {
		return err
	}

	disk.mu.Lock()
	defer disk.mu.Unlock()

	previousManifest := disk.manifest
	name := segmentFileName(disk.manifest.NextID)
	path := filepath.Join(disk.directory, name)
	if err := writeSegmentFile(path, segment); err != nil {
		return err
	}
	disk.manifest.NextID++
	disk.manifest.Segments = []string{name}
	if err := disk.persistManifestLocked(); err != nil {
		disk.manifest = previousManifest
		_ = os.Remove(path)
		return err
	}
	disk.store.replaceSegments([]*Segment{segment})
	return nil
}

// Compact rewrites the visible rows into one fresh durable segment.
func (disk *DiskStore) Compact(ctx context.Context) error {
	rows, err := disk.store.Scan(ctx)
	if err != nil {
		return err
	}
	return disk.ReplaceAll(rows)
}

func (disk *DiskStore) loadManifest(manifest diskManifest) error {
	if manifest.Version != manifestVersion {
		return fmt.Errorf("%w: version %d, want %d", ErrCorruptManifest, manifest.Version, manifestVersion)
	}
	if manifest.SchemaHash != hex.EncodeToString(disk.schemaHash[:]) {
		return fmt.Errorf("%w: schema fingerprint mismatch", ErrInvalidSchema)
	}
	segments := make([]*Segment, 0, len(manifest.Segments))
	for _, name := range manifest.Segments {
		segment, err := loadSegmentFile(filepath.Join(disk.directory, name), disk.schema)
		if err != nil {
			return err
		}
		segments = append(segments, segment)
	}
	if manifest.NextID == 0 {
		manifest.NextID = 1
	}
	disk.store.replaceSegments(segments)
	disk.manifest = manifest
	return nil
}

func (disk *DiskStore) persistManifest() error {
	disk.mu.Lock()
	defer disk.mu.Unlock()
	return disk.persistManifestLocked()
}

func (disk *DiskStore) persistManifestLocked() error {
	manifestPath := filepath.Join(disk.directory, manifestFilename)
	temporary, err := os.CreateTemp(disk.directory, manifestFilename+".*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()

	encoded, err := json.MarshalIndent(disk.manifest, "", "  ")
	if err != nil {
		return err
	}
	if _, err := temporary.Write(encoded); err != nil {
		return err
	}
	if _, err := temporary.Write([]byte{'\n'}); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, manifestPath); err != nil {
		return err
	}
	return nil
}

func (disk *DiskStore) scanExistingSegments() ([]string, uint64, error) {
	entries, err := os.ReadDir(disk.directory)
	if err != nil {
		return nil, 0, err
	}
	segments := make([]string, 0)
	var nextID uint64 = 1
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, segmentFilePrefix) || !strings.HasSuffix(name, segmentFileSuffix) {
			continue
		}
		if _, ok := parseSegmentSequence(name); !ok {
			continue
		}
		segments = append(segments, name)
		if sequence, ok := parseSegmentSequence(name); ok && sequence >= nextID {
			nextID = sequence + 1
		}
	}
	sort.Strings(segments)
	loaded := make([]*Segment, 0, len(segments))
	for _, name := range segments {
		segment, err := loadSegmentFile(filepath.Join(disk.directory, name), disk.schema)
		if err != nil {
			return nil, 0, err
		}
		loaded = append(loaded, segment)
	}
	disk.store.replaceSegments(loaded)
	return segments, nextID, nil
}

func segmentFileName(sequence uint64) string {
	return fmt.Sprintf("%s%020d%s", segmentFilePrefix, sequence, segmentFileSuffix)
}

func parseSegmentSequence(name string) (uint64, bool) {
	if !strings.HasPrefix(name, segmentFilePrefix) || !strings.HasSuffix(name, segmentFileSuffix) {
		return 0, false
	}
	number := strings.TrimSuffix(strings.TrimPrefix(name, segmentFilePrefix), segmentFileSuffix)
	sequence, err := strconv.ParseUint(number, 10, 64)
	if err != nil {
		return 0, false
	}
	return sequence, true
}

func readDiskManifest(path string) (diskManifest, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return diskManifest{}, false, nil
		}
		return diskManifest{}, false, err
	}
	var manifest diskManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return diskManifest{}, false, fmt.Errorf("%w: decode manifest: %v", ErrCorruptManifest, err)
	}
	return manifest, true, nil
}
