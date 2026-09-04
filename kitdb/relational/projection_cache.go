package relational

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/kitwork/engine/internal/snapshotfile"
	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
	"github.com/kitwork/engine/search"
)

// A relational Engine owns at most one warm reader per projection kind. The
// serialized-directory bound keeps an active tenant from retaining an
// arbitrarily large projection manifest; larger snapshots remain usable but
// are opened per query.
const maximumCachedProjectionDirectoryBytes = 2 << 20

// MaximumSearchReaderCacheBytes is the per-Engine ceiling for deterministic
// packed-search reader memory reservations.
const MaximumSearchReaderCacheBytes = int64(8 << 30)

type projectionCacheAccess struct {
	hit    bool
	miss   bool
	bypass bool
}

type projectionCacheEntry struct {
	path                string
	file                *snapshotfile.Reader
	manifest            projectionManifest
	searchIndexes       map[string]*projectionSearchIndex
	searchCapacityBytes int64
	references          int
	retired             bool
}

type projectionSearchIndex struct {
	ready    chan struct{}
	index    *search.Index
	capacity int64
	err      error
	bypass   bool
}

// ProjectionCacheStats reports only process-local reader residency. Directory
// bytes cover the decoded snapshot-file directory retained by the reader, not
// KCOL/search payload pages or operating-system page-cache residency.
type ProjectionCacheStats struct {
	Entries                   int
	ActiveLeases              int
	DirectoryBytes            int64
	SearchReaders             int
	SearchReaderResidentBytes int64
	SearchReaderCapacityBytes int64
}

// ProjectionCacheTrim reports reader metadata released by an explicit trim.
// Projection files remain complete, immutable, and reusable on the next query.
type ProjectionCacheTrim struct {
	Entries                   int
	DirectoryBytes            int64
	SearchReaders             int
	SearchReaderResidentBytes int64
	SearchReaderCapacityBytes int64
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

type projectionSearchLease struct {
	index  *search.Index
	access projectionCacheAccess
	cached bool
}

func (lease *projectionSearchLease) close() error {
	if lease == nil || lease.index == nil || lease.cached {
		return nil
	}
	index := lease.index
	lease.index = nil
	return index.Close()
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
	var resources projectionEntryResources
	if entry.retired && entry.references == 0 {
		resources = detachProjectionEntryResources(entry)
	}
	cache.mu.Unlock()
	return resources.close()
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
	resources := detachProjectionEntryResources(entry)
	cache.mu.Unlock()
	return resources.close()
}

func (cache *projectionReaderCache) close() error {
	cache.mu.Lock()
	var resources []projectionEntryResources
	var result error
	for kind, entry := range cache.entries {
		if entry.references != 0 {
			result = errors.Join(result, fmt.Errorf("kitdb: %s projection cache readers did not drain", kind))
			continue
		}
		delete(cache.entries, kind)
		entry.retired = true
		resources = append(resources, detachProjectionEntryResources(entry))
	}
	cache.mu.Unlock()
	for _, resource := range resources {
		result = errors.Join(result, resource.close())
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
		stats.SearchReaderCapacityBytes += entry.searchCapacityBytes
		for _, reader := range entry.searchIndexes {
			if reader.index == nil {
				continue
			}
			stats.SearchReaders++
			stats.SearchReaderResidentBytes += reader.index.ResidentBytes()
		}
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
	resources := make([]projectionEntryResources, 0, len(cache.entries))
	var trimmed ProjectionCacheTrim
	for kind, entry := range cache.entries {
		delete(cache.entries, kind)
		stats := projectionEntryCacheStats(entry)
		entry.retired = true
		trimmed.Entries += stats.Entries
		trimmed.DirectoryBytes += stats.DirectoryBytes
		trimmed.SearchReaders += stats.SearchReaders
		trimmed.SearchReaderResidentBytes += stats.SearchReaderResidentBytes
		trimmed.SearchReaderCapacityBytes += stats.SearchReaderCapacityBytes
		resources = append(resources, detachProjectionEntryResources(entry))
	}
	cache.mu.Unlock()

	var result error
	for _, resource := range resources {
		result = errors.Join(result, resource.close())
	}
	return trimmed, result
}

type projectionEntryResources struct {
	file    *snapshotfile.Reader
	indexes []*search.Index
}

func detachProjectionEntryResources(entry *projectionCacheEntry) projectionEntryResources {
	if entry == nil {
		return projectionEntryResources{}
	}
	resources := projectionEntryResources{file: entry.file}
	entry.file = nil
	for _, reader := range entry.searchIndexes {
		if reader.index != nil {
			resources.indexes = append(resources.indexes, reader.index)
			reader.index = nil
		}
	}
	entry.searchIndexes = nil
	entry.searchCapacityBytes = 0
	return resources
}

func (resources projectionEntryResources) close() error {
	var result error
	for _, index := range resources.indexes {
		result = errors.Join(result, index.Close())
	}
	if resources.file != nil {
		result = errors.Join(result, resources.file.Close())
	}
	return result
}

func projectionEntryCacheStats(entry *projectionCacheEntry) ProjectionCacheStats {
	if entry == nil || entry.retired || entry.file == nil {
		return ProjectionCacheStats{}
	}
	stats := ProjectionCacheStats{
		Entries:                   1,
		ActiveLeases:              entry.references,
		DirectoryBytes:            int64(entry.file.DirectoryBytes()),
		SearchReaderCapacityBytes: entry.searchCapacityBytes,
	}
	for _, reader := range entry.searchIndexes {
		if reader.index == nil {
			continue
		}
		stats.SearchReaders++
		stats.SearchReaderResidentBytes += reader.index.ResidentBytes()
	}
	return stats
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
	var retired projectionEntryResources
	if entry != nil {
		entry.retired = true
		if entry.references == 0 {
			retired = detachProjectionEntryResources(entry)
		}
	}
	cache.mu.Unlock()
	if closeErr := retired.close(); closeErr != nil {
		_ = cache.release(candidate)
		return projectionLease{}, closeErr
	}
	return projectionLease{cache: cache, entry: candidate, file: file, table: table, access: access}, nil
}

// acquireSearchIndex optionally retains one immutable reader per table. The
// caller's parent projection lease keeps the borrowed container alive.
func (lease *projectionLease) acquireSearchIndex(
	ctx context.Context,
	table string,
	schema search.Schema,
	maximumBytes int64,
) (projectionSearchLease, error) {
	if lease == nil || lease.file == nil || ctx == nil {
		return projectionSearchLease{}, fmt.Errorf("kitdb: invalid search projection lease/context")
	}
	if err := ctx.Err(); err != nil {
		return projectionSearchLease{}, err
	}
	if lease.entry == nil || lease.cache == nil || maximumBytes == 0 {
		return lease.openUncachedSearchIndex(ctx, table, schema)
	}

	cache, entry := lease.cache, lease.entry
	cache.mu.Lock()
	if entry.retired || entry.file == nil {
		cache.mu.Unlock()
		return projectionSearchLease{}, fmt.Errorf("kitdb: search projection cache entry is retired")
	}
	if loading := entry.searchIndexes[table]; loading != nil {
		ready := loading.ready
		cache.mu.Unlock()
		select {
		case <-ready:
		case <-ctx.Done():
			return projectionSearchLease{}, ctx.Err()
		}
		if loading.bypass {
			return lease.openUncachedSearchIndex(ctx, table, schema)
		}
		if loading.err != nil {
			return projectionSearchLease{}, loading.err
		}
		if loading.index == nil {
			return projectionSearchLease{}, fmt.Errorf("kitdb: search projection reader completed without an index")
		}
		return projectionSearchLease{
			index: loading.index, cached: true, access: projectionCacheAccess{hit: true},
		}, nil
	}
	if entry.searchIndexes == nil {
		entry.searchIndexes = make(map[string]*projectionSearchIndex)
	}
	loading := &projectionSearchIndex{ready: make(chan struct{})}
	entry.searchIndexes[table] = loading
	cache.mu.Unlock()

	section, err := lease.file.Section(table)
	if err != nil {
		lease.failSearchIndexLoad(table, loading, err, false)
		return projectionSearchLease{}, err
	}
	info, err := search.InspectSnapshot(ctx, section, schema)
	if err != nil {
		lease.failSearchIndexLoad(table, loading, err, false)
		return projectionSearchLease{}, err
	}

	cache.mu.Lock()
	remaining := maximumBytes - entry.searchCapacityBytes
	bypass := info.ReaderCapacityBytes <= 0 || info.ReaderCapacityBytes > remaining
	if bypass {
		delete(entry.searchIndexes, table)
		loading.bypass = true
		close(loading.ready)
		cache.mu.Unlock()
		return lease.openUncachedSearchIndex(ctx, table, schema)
	}
	entry.searchCapacityBytes += info.ReaderCapacityBytes
	loading.capacity = info.ReaderCapacityBytes
	cache.mu.Unlock()

	index, err := search.OpenSnapshotContext(ctx, section, schema)
	if err != nil {
		lease.failSearchIndexLoad(table, loading, err, true)
		return projectionSearchLease{}, err
	}
	if resident := index.ResidentBytes(); resident > info.ReaderCapacityBytes {
		_ = index.Close()
		err = fmt.Errorf(
			"kitdb: search reader residency %d exceeds its reservation %d",
			resident, info.ReaderCapacityBytes,
		)
		lease.failSearchIndexLoad(table, loading, err, true)
		return projectionSearchLease{}, err
	}
	cache.mu.Lock()
	loading.index = index
	close(loading.ready)
	cache.mu.Unlock()
	return projectionSearchLease{
		index: index, cached: true, access: projectionCacheAccess{miss: true},
	}, nil
}

func (lease *projectionLease) failSearchIndexLoad(
	table string,
	loading *projectionSearchIndex,
	err error,
	releaseCapacity bool,
) {
	cache, entry := lease.cache, lease.entry
	cache.mu.Lock()
	if entry.searchIndexes[table] == loading {
		delete(entry.searchIndexes, table)
		if releaseCapacity {
			entry.searchCapacityBytes -= loading.capacity
			if entry.searchCapacityBytes < 0 {
				entry.searchCapacityBytes = 0
			}
		}
	}
	loading.err = err
	close(loading.ready)
	cache.mu.Unlock()
}

func (lease *projectionLease) openUncachedSearchIndex(
	ctx context.Context,
	table string,
	schema search.Schema,
) (projectionSearchLease, error) {
	section, err := lease.file.Section(table)
	if err != nil {
		return projectionSearchLease{}, err
	}
	index, err := search.OpenSnapshotContext(ctx, section, schema)
	if err != nil {
		return projectionSearchLease{}, err
	}
	return projectionSearchLease{
		index: index, access: projectionCacheAccess{miss: true, bypass: true},
	}, nil
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

func (stats *ExecutionStats) observeSearchReaderCache(access projectionCacheAccess) {
	if stats == nil {
		return
	}
	if access.hit {
		stats.SearchReaderCacheHits++
	}
	if access.miss {
		stats.SearchReaderCacheMisses++
	}
	if access.bypass {
		stats.SearchReaderCacheBypasses++
	}
}
