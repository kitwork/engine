package relational

var queryCacheStatusColumns = []Column{
	{Name: "enabled", Kind: "bool"},
	{Name: "entries", Kind: "bigint"},
	{Name: "bytes", Kind: "bigint"},
	{Name: "hits", Kind: "bigint"},
	{Name: "misses", Kind: "bigint"},
	{Name: "bypasses", Kind: "bigint"},
	{Name: "evictions", Kind: "bigint"},
}

func (transaction *Transaction) executeQueryCacheStatus() Result {
	stats := transaction.engine.QueryCacheStats()
	return Result{
		Columns: append([]Column(nil), queryCacheStatusColumns...),
		Rows: [][]any{{
			stats.Enabled,
			boundedUnsignedInteger(stats.Entries),
			stats.Bytes,
			boundedUnsignedInteger(stats.Hits),
			boundedUnsignedInteger(stats.Misses),
			boundedUnsignedInteger(stats.Bypasses),
			boundedUnsignedInteger(stats.Evictions),
		}},
		CommandTag: "SELECT 1",
	}
}

func boundedUnsignedInteger[T ~int | ~uint64](value T) int64 {
	if uint64(value) > uint64(^uint64(0)>>1) {
		return int64(^uint64(0) >> 1)
	}
	return int64(value)
}
