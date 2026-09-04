package kitdb

// CursorStats describes successful main-snapshot work performed by one or
// more point lookups or range cursors. It is query-local evidence, not a
// process-global cache metric.
type CursorStats struct {
	PageAccesses             uint64
	PageCacheHits            uint64
	PageCacheMisses          uint64
	PageCacheBypasses        uint64
	PagesRead                uint64
	PageBytesRead            uint64
	PageRecordsDecoded       uint64
	GenerationEntriesVisited uint64
	OverlayEntriesVisited    uint64
}

// Add combines independent lookup/cursor observations.
func (stats *CursorStats) Add(other CursorStats) {
	if stats == nil {
		return
	}
	stats.PageAccesses += other.PageAccesses
	stats.PageCacheHits += other.PageCacheHits
	stats.PageCacheMisses += other.PageCacheMisses
	stats.PageCacheBypasses += other.PageCacheBypasses
	stats.PagesRead += other.PagesRead
	stats.PageBytesRead += other.PageBytesRead
	stats.PageRecordsDecoded += other.PageRecordsDecoded
	stats.GenerationEntriesVisited += other.GenerationEntriesVisited
	stats.OverlayEntriesVisited += other.OverlayEntriesVisited
}
