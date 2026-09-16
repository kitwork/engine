package relational

import (
	"container/list"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

const (
	defaultResultCacheBytes         int64 = 16 << 20
	defaultResultCacheEntries             = 1_024
	defaultMaximumCachedResultBytes       = 1 << 20
	maximumResultCacheBytes         int64 = 1 << 40
	maximumResultCacheEntries             = 1_000_000
)

// QueryCacheOptions bounds the optional transaction-aware result cache.
// Durations are maximum ages, never the sole invalidation mechanism: every
// lookup must also match the source database's latest committed transaction.
type QueryCacheOptions struct {
	Select         time.Duration
	Search         time.Duration
	Analytics      time.Duration
	MaximumBytes   int64
	MaximumEntries int
	MaximumResult  int64
}

// QueryCacheStats contains fixed-cardinality process-local counters.
type QueryCacheStats struct {
	Enabled   bool
	Entries   int
	Bytes     int64
	Hits      uint64
	Misses    uint64
	Bypasses  uint64
	Evictions uint64
}

type resultCacheEntry struct {
	key         [32]byte
	transaction uint64
	expiresAt   time.Time
	result      Result
	bytes       int64
	element     *list.Element
}

type queryResultCache struct {
	options   QueryCacheOptions
	mu        sync.Mutex
	entries   map[[32]byte]*resultCacheEntry
	lru       list.List
	bytes     int64
	hits      atomic.Uint64
	misses    atomic.Uint64
	bypasses  atomic.Uint64
	evictions atomic.Uint64
}

func normalizeQueryCacheOptions(options QueryCacheOptions) (QueryCacheOptions, error) {
	if options.Select < 0 || options.Search < 0 || options.Analytics < 0 {
		return QueryCacheOptions{}, fmt.Errorf("kitdb: query cache durations cannot be negative")
	}
	enabled := options.Select > 0 || options.Search > 0 || options.Analytics > 0
	if !enabled {
		return QueryCacheOptions{}, nil
	}
	if options.MaximumBytes == 0 {
		options.MaximumBytes = defaultResultCacheBytes
	}
	if options.MaximumEntries == 0 {
		options.MaximumEntries = defaultResultCacheEntries
	}
	if options.MaximumResult == 0 {
		options.MaximumResult = defaultMaximumCachedResultBytes
	}
	if options.MaximumBytes < 1 || options.MaximumBytes > maximumResultCacheBytes {
		return QueryCacheOptions{}, fmt.Errorf("kitdb: query cache memory must be between 1 byte and %d bytes", maximumResultCacheBytes)
	}
	if options.MaximumEntries < 1 || options.MaximumEntries > maximumResultCacheEntries {
		return QueryCacheOptions{}, fmt.Errorf("kitdb: query cache entries must be between 1 and %d", maximumResultCacheEntries)
	}
	if options.MaximumResult < 1 || options.MaximumResult > options.MaximumBytes {
		return QueryCacheOptions{}, fmt.Errorf("kitdb: maximum cached result must be between 1 byte and the query cache memory limit")
	}
	return options, nil
}

func newQueryResultCache(options QueryCacheOptions) *queryResultCache {
	if options.Select == 0 && options.Search == 0 && options.Analytics == 0 {
		return nil
	}
	return &queryResultCache{options: options, entries: make(map[[32]byte]*resultCacheEntry)}
}

func (cache *queryResultCache) duration(statement *kitdbsql.ParsedStatement) time.Duration {
	if cache == nil || statement == nil || statement.Kind != kitdbsql.StatementSelect || statement.Select == nil {
		return 0
	}
	if !cacheableSelect(statement.Select) {
		return 0
	}
	if selectTreeHasSearch(statement.Select) {
		return cache.options.Search
	}
	if selectTreeHasAnalytics(statement.Select) {
		return cache.options.Analytics
	}
	return cache.options.Select
}

func selectTreeHasAnalytics(plan *kitdbsql.SelectStatement) bool {
	if plan == nil {
		return false
	}
	if len(plan.GroupBy) != 0 || plan.Having != nil {
		return true
	}
	for _, projection := range plan.Projection {
		if projection.Aggregate != "" {
			return true
		}
	}
	if selectTreeHasAnalytics(plan.Source) {
		return true
	}
	for _, common := range plan.CommonTables {
		if selectTreeHasAnalytics(common.Select) {
			return true
		}
	}
	for _, operation := range plan.SetOperations {
		if selectTreeHasAnalytics(operation.Select) {
			return true
		}
	}
	return false
}

func selectTreeHasSearch(plan *kitdbsql.SelectStatement) bool {
	if plan == nil {
		return false
	}
	if plan.Search != nil || selectTreeHasSearch(plan.Source) {
		return true
	}
	for _, common := range plan.CommonTables {
		if selectTreeHasSearch(common.Select) {
			return true
		}
	}
	for _, operation := range plan.SetOperations {
		if selectTreeHasSearch(operation.Select) {
			return true
		}
	}
	return false
}

func cacheableSelect(plan *kitdbsql.SelectStatement) bool {
	if plan == nil {
		return true
	}
	for _, condition := range plan.Conditions {
		if volatileLiteral(condition.Value) {
			return false
		}
	}
	if volatileExpression(plan.Predicate) || volatileExpression(plan.Having) || volatileLiteral(plan.After) {
		return false
	}
	for _, projection := range plan.Projection {
		if volatileExpression(projection.Expression) {
			return false
		}
	}
	for _, order := range plan.Order {
		if volatileExpression(order.Expression) {
			return false
		}
	}
	if plan.Search != nil && volatileLiteral(plan.Search.Query) {
		return false
	}
	if !cacheableSelect(plan.Source) {
		return false
	}
	for _, common := range plan.CommonTables {
		if !cacheableSelect(common.Select) {
			return false
		}
	}
	for _, operation := range plan.SetOperations {
		if !cacheableSelect(operation.Select) {
			return false
		}
	}
	return true
}

func volatileExpression(expression *kitdbsql.ExpressionPlan) bool {
	if expression == nil {
		return false
	}
	if volatileLiteral(expression.Literal) {
		return true
	}
	switch expression.Operator {
	case "now", "current_timestamp", "current_date", "current_time", "localtimestamp", "random",
		"nextval", "currval", "setval", "lastval":
		return true
	}
	for index := range expression.Arguments {
		if volatileExpression(&expression.Arguments[index]) {
			return true
		}
	}
	return false
}

func volatileLiteral(literal kitdbsql.Literal) bool {
	switch literal.Kind {
	case kitdbsql.LiteralCurrentTimestamp, kitdbsql.LiteralCurrentDate,
		kitdbsql.LiteralCurrentTime, kitdbsql.LiteralLocalTimestamp:
		return true
	}
	return false
}

func queryResultCacheKey(source string, parameters []any) [32]byte {
	hash := sha256.New()
	_, _ = hash.Write([]byte(source))
	var scratch [8]byte
	for _, parameter := range parameters {
		_, _ = hash.Write([]byte{0})
		_, _ = fmt.Fprintf(hash, "%T:", parameter)
		switch value := parameter.(type) {
		case nil:
		case []byte:
			binary.LittleEndian.PutUint64(scratch[:], uint64(len(value)))
			_, _ = hash.Write(scratch[:])
			_, _ = hash.Write(value)
		case time.Time:
			binary.LittleEndian.PutUint64(scratch[:], uint64(value.UnixNano()))
			_, _ = hash.Write(scratch[:])
		default:
			_, _ = fmt.Fprintf(hash, "%#v", value)
		}
	}
	var key [32]byte
	copy(key[:], hash.Sum(nil))
	return key
}

func (cache *queryResultCache) get(key [32]byte, transaction uint64, now time.Time) (Result, bool) {
	if cache == nil {
		return Result{}, false
	}
	cache.mu.Lock()
	entry := cache.entries[key]
	if entry == nil || entry.transaction != transaction || !now.Before(entry.expiresAt) {
		if entry != nil {
			cache.removeLocked(entry)
		}
		cache.mu.Unlock()
		cache.misses.Add(1)
		return Result{}, false
	}
	cache.lru.MoveToFront(entry.element)
	result := cloneCachedResult(entry.result)
	cache.mu.Unlock()
	cache.hits.Add(1)
	return result, true
}

func (cache *queryResultCache) put(key [32]byte, transaction uint64, expiresAt time.Time, result Result) {
	if cache == nil {
		return
	}
	cloned := cloneCachedResult(result)
	bytes := cachedResultBytes(cloned)
	if bytes > cache.options.MaximumResult || bytes > cache.options.MaximumBytes {
		cache.bypasses.Add(1)
		return
	}
	cache.mu.Lock()
	if current := cache.entries[key]; current != nil {
		cache.removeLocked(current)
	}
	entry := &resultCacheEntry{key: key, transaction: transaction, expiresAt: expiresAt, result: cloned, bytes: bytes}
	entry.element = cache.lru.PushFront(entry)
	cache.entries[key] = entry
	cache.bytes += bytes
	for len(cache.entries) > cache.options.MaximumEntries || cache.bytes > cache.options.MaximumBytes {
		oldest := cache.lru.Back()
		if oldest == nil {
			break
		}
		cache.removeLocked(oldest.Value.(*resultCacheEntry))
		cache.evictions.Add(1)
	}
	cache.mu.Unlock()
}

func (cache *queryResultCache) removeLocked(entry *resultCacheEntry) {
	delete(cache.entries, entry.key)
	cache.bytes -= entry.bytes
	cache.lru.Remove(entry.element)
}

func (cache *queryResultCache) stats() QueryCacheStats {
	if cache == nil {
		return QueryCacheStats{}
	}
	cache.mu.Lock()
	stats := QueryCacheStats{Enabled: true, Entries: len(cache.entries), Bytes: cache.bytes}
	cache.mu.Unlock()
	stats.Hits = cache.hits.Load()
	stats.Misses = cache.misses.Load()
	stats.Bypasses = cache.bypasses.Load()
	stats.Evictions = cache.evictions.Load()
	return stats
}

func cloneCachedResult(source Result) Result {
	result := source
	if source.Execution != nil {
		execution := *source.Execution
		result.Execution = &execution
	}
	result.Columns = append([]Column(nil), source.Columns...)
	for index := range result.Columns {
		if source.Columns[index].TimePrecision != nil {
			value := *source.Columns[index].TimePrecision
			result.Columns[index].TimePrecision = &value
		}
		if source.Columns[index].TextLength != nil {
			value := *source.Columns[index].TextLength
			result.Columns[index].TextLength = &value
		}
	}
	result.Rows = make([][]any, len(source.Rows))
	for rowIndex, row := range source.Rows {
		result.Rows[rowIndex] = append([]any(nil), row...)
		for columnIndex, item := range result.Rows[rowIndex] {
			if data, ok := item.([]byte); ok {
				result.Rows[rowIndex][columnIndex] = append([]byte(nil), data...)
			}
		}
	}
	return result
}

func cachedResultBytes(result Result) int64 {
	bytes := int64(len(result.CommandTag) + len(result.Columns)*64 + len(result.Rows)*24)
	for _, column := range result.Columns {
		bytes += int64(len(column.Name) + len(column.Kind))
	}
	for _, row := range result.Rows {
		bytes += int64(len(row) * 16)
		for _, item := range row {
			switch value := item.(type) {
			case string:
				bytes += int64(len(value))
			case []byte:
				bytes += int64(len(value))
			default:
				bytes += 8
			}
			if bytes < 0 || bytes > math.MaxInt64/2 {
				return math.MaxInt64
			}
		}
	}
	return bytes
}
