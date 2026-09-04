package relational

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/kitwork/engine/internal/snapshotfile"
	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

// A relational Engine owns at most one warm reader per projection kind. The
// serialized-directory bound keeps an active tenant from retaining an
// arbitrarily large projection manifest; larger snapshots remain usable but
// are opened per query.
const maximumCachedProjectionDirectoryBytes = 2 << 20

type projectionCacheAccess struct {
	hit    bool
	miss   bool
	bypass bool
}

type projectionCacheEntry struct {
	path       string
	file       *snapshotfile.Reader
	manifest   projectionManifest
	references int
	retired    bool
}

// ProjectionCacheStats reports only process-local reader residency. Directory
// bytes cover the decoded snapshot-file directory retained by the reader, not
// KCOL/search payload pages or operating-system page-cache residency.
type ProjectionCacheStats struct {
	Entries        int
	ActiveLeases   int
	DirectoryBytes int64
}

// ProjectionCacheTrim reports reader metadata released by an explicit trim.
// Projection files remain complete, immutable, and reusable on the next query.
type ProjectionCacheTrim struct {
	Entries        int
	DirectoryBytes int64
}

type projectionReaderCache struct {
	mu      sync.Mutex
	entries map[string]*projectionCacheEntry
}

type projectionLease struct {
	cache  *projectionReaderCache
	entry  *projectionCacheEntry
	file   *snapshotfile.Reader
	table  projectionTable
	access projectionCacheAccess
}

func (lease *projectionLease) close() error {
	if lease == nil {
		return nil
	}
	if lease.entry != nil {
		entry := lease.entry
		lease.entry = nil
		lease.file = nil
		return lease.cache.release(entry)
	}
	file := lease.file
	lease.file = nil
	if file == nil {
		return nil
	}
	return file.Close()
}

func (cache *projectionReaderCache) release(entry *projectionCacheEntry) error {
	if cache == nil || entry == nil {
		return nil
	}
	cache.mu.Lock()
	if entry.references <= 0 {
		cache.mu.Unlock()
		return fmt.Errorf("kitdb: projection cache lease underflow")
	}
	entry.references--
	var file *snapshotfile.Reader
	if entry.retired && entry.references == 0 {
		file = entry.file
		entry.file = nil
	}
	cache.mu.Unlock()
	if file != nil {
		return file.Close()
	}
	return nil
}

func (cache *projectionReaderCache) invalidate(kind string) error {
	cache.mu.Lock()
	entry := cache.entries[kind]
	if entry == nil {
		cache.mu.Unlock()
		return nil
	}
	if entry.references != 0 {
		cache.mu.Unlock()
		return fmt.Errorf("kitdb: %s projection cache readers did not drain", kind)
	}
	delete(cache.entries, kind)
	entry.retired = true
	file := entry.file
	entry.file = nil
	cache.mu.Unlock()
	if file != nil {
		return file.Close()
	}
	return nil
}

func (cache *projectionReaderCache) close() error {
	cache.mu.Lock()
	var files []*snapshotfile.Reader
	var result error
	for kind, entry := range cache.entries {
		if entry.references != 0 {
			result = errors.Join(result, fmt.Errorf("kitdb: %s projection cache readers did not drain", kind))
			continue
		}
		delete(cache.entries, kind)
		entry.retired = true
		if entry.file != nil {
			files = append(files, entry.file)
			entry.file = nil
		}
	}
	cache.mu.Unlock()
	for _, file := range files {
		result = errors.Join(result, file.Close())
	}
	return result
}

func (cache *projectionReaderCache) stats() ProjectionCacheStats {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	var stats ProjectionCacheStats
	for _, entry := range cache.entries {
		if entry == nil || entry.retired || entry.file == nil {
			continue
		}
		stats.Entries++
		stats.ActiveLeases += entry.references
		stats.DirectoryBytes += int64(entry.file.DirectoryBytes())
	}
	return stats
}

func (cache *projectionReaderCache) trim() (ProjectionCacheTrim, error) {
	cache.mu.Lock()
	for kind, entry := range cache.entries {
		if entry.references != 0 {
			cache.mu.Unlock()
			return ProjectionCacheTrim{}, fmt.Errorf(
				"kitdb: %s projection cache readers did not drain", kind,
			)
		}
	}
	files := make([]*snapshotfile.Reader, 0, len(cache.entries))
	var trimmed ProjectionCacheTrim
	for kind, entry := range cache.entries {
		delete(cache.entries, kind)
		entry.retired = true
		if entry.file != nil {
			trimmed.Entries++
			trimmed.DirectoryBytes += int64(entry.file.DirectoryBytes())
			files = append(files, entry.file)
			entry.file = nil
		}
	}
	cache.mu.Unlock()

	var result error
	for _, file := range files {
		result = errors.Join(result, file.Close())
	}
	return trimmed, result
}

func loadProjection(path string) (*snapshotfile.Reader, projectionManifest, error) {
	file, err := snapshotfile.Open(path)
	if err != nil {
		return nil, projectionManifest{}, err
	}
	var manifest projectionManifest
	if err := file.Metadata(&manifest); err != nil {
		_ = file.Close()
		return nil, projectionManifest{}, err
	}
	return file, manifest, nil
}

func projectionTableFor(
	manifest projectionManifest,
	kind string,
	cursor kitdbengine.HistoryCursor,
	catalog string,
	schema kitdbsql.Schema,
	generation uint64,
	epoch uint64,
) (projectionTable, error) {
	table, found := manifest.Tables[schema.ID]
	if manifest.Version != 1 || manifest.Kind != kind || manifest.Cursor != cursor ||
		manifest.Catalog != catalog || !found || table.Hash != schema.Hash ||
		table.Generation != generation || table.Epoch != epoch {
		return projectionTable{}, fmt.Errorf("kitdb: %s snapshot is missing or stale; call RefreshProjections", kind)
	}
	return table, nil
}

// acquireProjection is called while projectionMu is read-locked. Publication
// takes the write lock, drains all leases, closes the old handle, then changes
// the on-disk generation. Refcounts additionally make replacement safe if an
// independently published immutable generation is observed by concurrent
// readers.
func (engine *Engine) acquireProjection(
	path string,
	kind string,
	cursor kitdbengine.HistoryCursor,
	catalog string,
	schema kitdbsql.Schema,
	generation uint64,
	epoch uint64,
) (projectionLease, error) {
	cache := &engine.projectionCache
	cache.mu.Lock()
	entry := cache.entries[kind]
	if entry != nil && entry.path == path && !entry.retired {
		if table, err := projectionTableFor(entry.manifest, kind, cursor, catalog, schema, generation, epoch); err == nil {
			entry.references++
			cache.mu.Unlock()
			return projectionLease{
				cache: cache, entry: entry, file: entry.file, table: table,
				access: projectionCacheAccess{hit: true},
			}, nil
		}
	}
	cache.mu.Unlock()

	file, manifest, err := loadProjection(path)
	if err != nil {
		return projectionLease{}, err
	}
	table, err := projectionTableFor(manifest, kind, cursor, catalog, schema, generation, epoch)
	if err != nil {
		_ = file.Close()
		return projectionLease{}, err
	}
	access := projectionCacheAccess{miss: true}
	if file.DirectoryBytes() > maximumCachedProjectionDirectoryBytes {
		access.bypass = true
		return projectionLease{file: file, table: table, access: access}, nil
	}

	candidate := &projectionCacheEntry{path: path, file: file, manifest: manifest, references: 1}
	cache.mu.Lock()
	entry = cache.entries[kind]
	if entry != nil && entry.path == path && !entry.retired {
		if currentTable, currentErr := projectionTableFor(entry.manifest, kind, cursor, catalog, schema, generation, epoch); currentErr == nil {
			entry.references++
			cache.mu.Unlock()
			if closeErr := file.Close(); closeErr != nil {
				_ = cache.release(entry)
				return projectionLease{}, closeErr
			}
			return projectionLease{
				cache: cache, entry: entry, file: entry.file, table: currentTable, access: access,
			}, nil
		}
	}
	if cache.entries == nil {
		cache.entries = make(map[string]*projectionCacheEntry, 2)
	}
	cache.entries[kind] = candidate
	var retired *snapshotfile.Reader
	if entry != nil {
		entry.retired = true
		if entry.references == 0 {
			retired = entry.file
			entry.file = nil
		}
	}
	cache.mu.Unlock()
	if retired != nil {
		if closeErr := retired.Close(); closeErr != nil {
			_ = cache.release(candidate)
			return projectionLease{}, closeErr
		}
	}
	return projectionLease{cache: cache, entry: candidate, file: file, table: table, access: access}, nil
}

type projectionPublisher interface {
	Publish(context.Context, any) error
}

func (engine *Engine) publishProjection(
	ctx context.Context,
	kind string,
	publisher projectionPublisher,
	manifest projectionManifest,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	engine.projectionMu.Lock()
	defer engine.projectionMu.Unlock()
	if err := engine.projectionCache.invalidate(kind); err != nil {
		return err
	}
	return publisher.Publish(ctx, manifest)
}

// ProjectionCacheStats returns the bounded readers currently retained by this
// relational engine. It performs no projection I/O and does not open a missing
// snapshot.
func (engine *Engine) ProjectionCacheStats() ProjectionCacheStats {
	if engine == nil {
		return ProjectionCacheStats{}
	}
	return engine.projectionCache.stats()
}

// TrimProjectionCache releases every warm projection reader without changing
// canonical KROW data or projection files. The publication gate waits for
// active projection queries, so a successful trim cannot invalidate an
// in-flight reader.
func (engine *Engine) TrimProjectionCache() (ProjectionCacheTrim, error) {
	if engine == nil {
		return ProjectionCacheTrim{}, fmt.Errorf("kitdb: projection engine is nil")
	}
	engine.projectionMu.Lock()
	defer engine.projectionMu.Unlock()
	return engine.projectionCache.trim()
}

func (stats *ExecutionStats) observeProjectionCache(access projectionCacheAccess) {
	if stats == nil {
		return
	}
	if access.hit {
		stats.ProjectionCacheHits++
	}
	if access.miss {
		stats.ProjectionCacheMisses++
	}
	if access.bypass {
		stats.ProjectionCacheBypasses++
	}
}
