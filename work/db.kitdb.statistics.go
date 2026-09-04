package work

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"math"
	"math/bits"
	"sort"

	kitdbengine "github.com/kitwork/engine/kitdb"
	requestscope "github.com/kitwork/engine/request"
	"github.com/kitwork/engine/utilities/query"
	"github.com/kitwork/engine/value"
)

const (
	kitDBStatisticsNamespace       byte = 0x04
	kitDBStatisticsVersion         byte = 1
	kitDBStatisticsMaximumBytes         = 1 << 20
	kitDBStatisticsMaximumIndexes       = 64
	kitDBStatisticsMaximumPrefixes      = 8
	kitDBStatisticsSketchBits           = 10
	kitDBStatisticsSketchRegisters      = 1 << kitDBStatisticsSketchBits
)

var (
	kitDBStatisticsMagic      = [4]byte{'K', 'S', 'T', 'A'}
	kitDBStatisticsCRC        = crc32.MakeTable(crc32.Castagnoli)
	kitDBStatisticsDirtyValue = []byte("KITDB-STATISTICS-DIRTY\x01")
)

type kitDBStatistics struct {
	StructID            string                 `json:"structId"`
	SchemaHash          string                 `json:"schemaHash"`
	AnalyzedTransaction uint64                 `json:"analyzedTransaction"`
	Rows                uint64                 `json:"rows"`
	Indexes             []kitDBIndexStatistics `json:"indexes,omitempty"`
}

type kitDBIndexStatistics struct {
	Signature        string   `json:"signature"`
	Name             string   `json:"name"`
	Fields           []string `json:"fields"`
	Entries          uint64   `json:"entries"`
	DistinctPrefixes []uint64 `json:"distinctPrefixes"`
}

type kitDBStatisticsSketch [kitDBStatisticsSketchRegisters]byte

func (sketch *kitDBStatisticsSketch) add(hash uint64) {
	// FNV is fast and deterministic for the tuple stream, but its high bits are
	// not sufficiently avalanche-distributed for direct HLL bucket selection.
	hash ^= hash >> 30
	hash *= 0xbf58476d1ce4e5b9
	hash ^= hash >> 27
	hash *= 0x94d049bb133111eb
	hash ^= hash >> 31
	index := hash >> (64 - kitDBStatisticsSketchBits)
	rank := bits.LeadingZeros64(hash<<kitDBStatisticsSketchBits) + 1
	maximum := 64 - kitDBStatisticsSketchBits + 1
	if rank > maximum {
		rank = maximum
	}
	if byte(rank) > sketch[index] {
		sketch[index] = byte(rank)
	}
}

func (sketch *kitDBStatisticsSketch) estimate(limit uint64) uint64 {
	if limit == 0 {
		return 0
	}
	sum := 0.0
	zeros := 0
	for _, register := range sketch {
		sum += math.Ldexp(1, -int(register))
		if register == 0 {
			zeros++
		}
	}
	m := float64(kitDBStatisticsSketchRegisters)
	estimate := 0.7213 / (1 + 1.079/m) * m * m / sum
	if estimate <= 2.5*m && zeros != 0 {
		estimate = m * math.Log(m/float64(zeros))
	}
	if estimate < 1 {
		estimate = 1
	}
	result := uint64(math.Round(estimate))
	if result > limit {
		return limit
	}
	return result
}

type kitDBIndexStatisticsBuilder struct {
	index    indexDef
	entries  uint64
	sketches []kitDBStatisticsSketch
}

func kitDBStatisticsKey(definition *StructDef) ([]byte, error) {
	identity := stableSchemaID("statistics", definition.ID)
	return kitDBFixedKey(kitDBStatisticsNamespace, definition.ID, identity)
}

func kitDBStatisticsDirtyKey(definition *StructDef) ([]byte, error) {
	identity := stableSchemaID("statistics-dirty", definition.ID)
	return kitDBFixedKey(kitDBStatisticsNamespace, definition.ID, identity)
}

func kitDBContentRevisionKey(definition *StructDef) ([]byte, error) {
	identity := stableSchemaID("content-revision", definition.ID)
	return kitDBFixedKey(kitDBStatisticsNamespace, definition.ID, identity)
}

func loadKitDBContentRevision(
	reader kitDBReader,
	definition *StructDef,
) (uint64, bool, error) {
	key, err := kitDBContentRevisionKey(definition)
	if err != nil {
		return 0, false, err
	}
	encoded, found, err := reader.Get(key)
	if err != nil || !found {
		return 0, found, err
	}
	if len(encoded) != 8 {
		return 0, false, fmt.Errorf("kitdb: invalid content revision")
	}
	return binary.BigEndian.Uint64(encoded), true, nil
}

func bumpKitDBContentRevision(
	transaction *kitDBRecordTransaction,
	definition *StructDef,
) error {
	key, err := kitDBContentRevisionKey(definition)
	if err != nil {
		return err
	}
	if transaction.hasPendingMutation(key) {
		return nil
	}
	revision, found, err := loadKitDBContentRevision(transaction, definition)
	if err != nil {
		return err
	}
	if found && revision == math.MaxUint64 {
		return fmt.Errorf("kitdb: content revision space is exhausted")
	}
	revision++
	return transaction.Put(key, binary.BigEndian.AppendUint64(nil, revision))
}

func encodeKitDBStatistics(statistics kitDBStatistics) ([]byte, error) {
	payload, err := json.Marshal(statistics)
	if err != nil {
		return nil, fmt.Errorf("kitdb: encode statistics: %w", err)
	}
	if len(payload) > kitDBStatisticsMaximumBytes-16 {
		return nil, fmt.Errorf("kitdb: statistics exceed %d bytes", kitDBStatisticsMaximumBytes)
	}
	encoded := make([]byte, 12, 12+len(payload)+4)
	copy(encoded[:4], kitDBStatisticsMagic[:])
	encoded[4] = kitDBStatisticsVersion
	binary.LittleEndian.PutUint32(encoded[8:12], uint32(len(payload)))
	encoded = append(encoded, payload...)
	encoded = binary.LittleEndian.AppendUint32(encoded, crc32.Checksum(encoded, kitDBStatisticsCRC))
	return encoded, nil
}

func decodeKitDBStatistics(encoded []byte) (kitDBStatistics, error) {
	if len(encoded) < 16 || len(encoded) > kitDBStatisticsMaximumBytes ||
		!bytes.Equal(encoded[:4], kitDBStatisticsMagic[:]) {
		return kitDBStatistics{}, fmt.Errorf("kitdb: invalid statistics envelope")
	}
	if encoded[4] != kitDBStatisticsVersion {
		return kitDBStatistics{}, fmt.Errorf("kitdb: unsupported statistics version %d", encoded[4])
	}
	if encoded[5] != 0 || encoded[6] != 0 || encoded[7] != 0 {
		return kitDBStatistics{}, fmt.Errorf("kitdb: nonzero reserved statistics bytes")
	}
	payloadLength := int(binary.LittleEndian.Uint32(encoded[8:12]))
	if payloadLength != len(encoded)-16 {
		return kitDBStatistics{}, fmt.Errorf("kitdb: invalid statistics payload length")
	}
	storedChecksum := binary.LittleEndian.Uint32(encoded[len(encoded)-4:])
	if crc32.Checksum(encoded[:len(encoded)-4], kitDBStatisticsCRC) != storedChecksum {
		return kitDBStatistics{}, fmt.Errorf("kitdb: statistics checksum mismatch")
	}
	statistics := kitDBStatistics{}
	if err := json.Unmarshal(encoded[12:len(encoded)-4], &statistics); err != nil {
		return kitDBStatistics{}, fmt.Errorf("kitdb: decode statistics: %w", err)
	}
	if statistics.StructID == "" || statistics.SchemaHash == "" ||
		len(statistics.Indexes) > kitDBStatisticsMaximumIndexes {
		return kitDBStatistics{}, fmt.Errorf("kitdb: invalid statistics payload")
	}
	seen := make(map[string]struct{}, len(statistics.Indexes))
	for _, index := range statistics.Indexes {
		if index.Signature == "" || index.Name == "" || len(index.Fields) == 0 ||
			len(index.Fields) > kitDBStatisticsMaximumPrefixes ||
			len(index.DistinctPrefixes) != len(index.Fields) {
			return kitDBStatistics{}, fmt.Errorf("kitdb: invalid index statistics payload")
		}
		if _, duplicate := seen[index.Signature]; duplicate {
			return kitDBStatistics{}, fmt.Errorf("kitdb: duplicate index statistics")
		}
		seen[index.Signature] = struct{}{}
		for position, distinct := range index.DistinctPrefixes {
			if distinct > index.Entries || (position > 0 && distinct < index.DistinctPrefixes[position-1]) {
				return kitDBStatistics{}, fmt.Errorf("kitdb: invalid distinct-prefix statistics")
			}
		}
	}
	return statistics, nil
}

func loadKitDBStatistics(
	reader kitDBReader,
	definition *StructDef,
) (*kitDBStatistics, bool, bool, error) {
	key, err := kitDBStatisticsKey(definition)
	if err != nil {
		return nil, false, false, err
	}
	encoded, found, err := reader.Get(key)
	if err != nil {
		return nil, false, false, err
	}
	var statistics *kitDBStatistics
	if found {
		decoded, decodeErr := decodeKitDBStatistics(encoded)
		if decodeErr != nil {
			return nil, false, false, decodeErr
		}
		statistics = &decoded
	}
	dirty, err := loadKitDBStatisticsDirty(reader, definition)
	if err != nil {
		return nil, false, false, err
	}
	return statistics, found, dirty, nil
}

func loadKitDBStatisticsDirty(reader kitDBReader, definition *StructDef) (bool, error) {
	key, err := kitDBStatisticsDirtyKey(definition)
	if err != nil {
		return false, err
	}
	encoded, found, err := reader.Get(key)
	if err != nil || !found {
		return false, err
	}
	if !bytes.Equal(encoded, kitDBStatisticsDirtyValue) {
		return false, fmt.Errorf("kitdb: invalid statistics dirty marker")
	}
	return true, nil
}

func kitDBStatisticsState(
	statistics *kitDBStatistics,
	found bool,
	dirty bool,
	definition *StructDef,
) string {
	if !found || statistics == nil {
		return "missing"
	}
	if statistics.StructID != definition.ID || statistics.SchemaHash != definition.Hash {
		return "schema_changed"
	}
	if dirty {
		return "stale"
	}
	return "current"
}

func (t *SchemaTable) loadKitDBStatistics(reader kitDBReader) error {
	statistics, found, dirty, err := loadKitDBStatistics(reader, t.definition)
	if err != nil {
		return err
	}
	t.statistics = statistics
	t.statisticsState = kitDBStatisticsState(statistics, found, dirty, t.definition)
	return nil
}

func (t *SchemaTable) Analyze(args ...value.Value) value.Value {
	if v, ok := t.ready(); !ok {
		return v
	}
	if len(args) != 0 {
		return value.Value{K: value.Invalid, V: "db.analyze: does not accept arguments"}
	}
	if t.engine != "kitdb" {
		return value.Value{K: value.Invalid, V: "db.analyze: available only for KitDB"}
	}
	if t.transaction != nil {
		return value.Value{K: value.Invalid, V: "db.analyze: cannot run inside db.transaction()"}
	}
	ctx := context.Background()
	if t.scope != nil {
		ctx = t.scope.Context()
	}
	statistics, committed, err := t.analyzeKitDB(ctx)
	if err != nil {
		return kitDBError(err)
	}
	t.statistics = &statistics
	t.statisticsState = "current"
	return value.New(map[string]value.Value{
		"struct":      value.New(t.table),
		"rows":        value.New(statistics.Rows),
		"indexes":     value.New(len(statistics.Indexes)),
		"transaction": value.New(fmt.Sprintf("%d", committed)),
		"status":      value.New("current"),
	})
}

func (t *SchemaTable) analyzeKitDB(ctx context.Context) (kitDBStatistics, uint64, error) {
	transaction, err := beginKitDBRecordTransaction(t.tenant, t.scope, t.dbName, false)
	if err != nil {
		return kitDBStatistics{}, 0, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = transaction.Rollback()
		}
	}()
	if err := validateKitDBWriteDefinition(transaction.managed.database, t.definition); err != nil {
		return kitDBStatistics{}, 0, err
	}
	previous, previousFound, previousDirty, err := loadKitDBStatistics(transaction, t.definition)
	if err != nil {
		return kitDBStatistics{}, 0, err
	}
	analysisTable := *t
	analysisTable.transaction = transaction
	if err := analysisTable.ensureKitDBStruct(); err != nil {
		return kitDBStatistics{}, 0, err
	}
	statistics, err := collectKitDBStatistics(ctx, &analysisTable, transaction)
	if err != nil {
		return kitDBStatistics{}, 0, err
	}
	if previousFound && !previousDirty && previous != nil &&
		previous.StructID == t.definition.ID {
		// A catalog-only change may require fresh index statistics without
		// changing the row snapshot observed by search projections.
		statistics.AnalyzedTransaction = previous.AnalyzedTransaction
	}
	encoded, err := encodeKitDBStatistics(statistics)
	if err != nil {
		return kitDBStatistics{}, 0, err
	}
	key, err := kitDBStatisticsKey(t.definition)
	if err != nil {
		return kitDBStatistics{}, 0, err
	}
	if err := transaction.Put(key, encoded); err != nil {
		return kitDBStatistics{}, 0, err
	}
	dirtyKey, err := kitDBStatisticsDirtyKey(t.definition)
	if err != nil {
		return kitDBStatistics{}, 0, err
	}
	if dirty, err := loadKitDBStatisticsDirty(transaction, t.definition); err != nil {
		return kitDBStatistics{}, 0, err
	} else if dirty {
		if err := transaction.Delete(dirtyKey); err != nil {
			return kitDBStatistics{}, 0, err
		}
	}
	published, err := transaction.CommitContext(ctx)
	if err != nil {
		return kitDBStatistics{}, 0, err
	}
	committed = true
	return statistics, published, nil
}

func collectKitDBStatistics(
	ctx context.Context,
	table *SchemaTable,
	reader *kitDBRecordTransaction,
) (kitDBStatistics, error) {
	// Statistics and PRAGMA output describe user-declared indexes. The hidden
	// ordered-primary path is a physical access contract, not schema metadata.
	indexes := collectIndexes(table.definition.Name, table.definition.columns)
	active := indexes[:0]
	for _, index := range indexes {
		if _, inactive := table.inactiveIndexes[kitDBIndexSignature(index)]; inactive {
			continue
		}
		active = append(active, index)
	}
	indexes = active
	if len(indexes) > kitDBStatisticsMaximumIndexes {
		return kitDBStatistics{}, fmt.Errorf(
			"kitdb: ANALYZE supports at most %d active indexes per struct",
			kitDBStatisticsMaximumIndexes,
		)
	}
	builders := make([]kitDBIndexStatisticsBuilder, len(indexes))
	for position, index := range indexes {
		if len(index.columns) > kitDBStatisticsMaximumPrefixes {
			return kitDBStatistics{}, fmt.Errorf(
				"kitdb: ANALYZE index %q exceeds %d fields",
				index.name, kitDBStatisticsMaximumPrefixes,
			)
		}
		builders[position] = kitDBIndexStatisticsBuilder{
			index: index, sketches: make([]kitDBStatisticsSketch, len(index.columns)),
		}
	}
	statistics := kitDBStatistics{
		StructID: table.definition.ID, SchemaHash: table.definition.Hash,
		AnalyzedTransaction: reader.base,
	}
	prefix, err := kitDBRowPrefix(table.definition)
	if err != nil {
		return kitDBStatistics{}, err
	}
	access := kitDBAccessPlan{
		kind: kitDBAccessScan, options: kitdbengine.RangeOptions{Prefix: prefix},
	}
	err = table.scanKitDBRowsFrom(reader, query.ExecutionPlan{}, access, func(row kitDBStoredRow) (bool, error) {
		if err := contextError(ctx); err != nil {
			return false, err
		}
		statistics.Rows++
		for position := range builders {
			builder := &builders[position]
			if len(builder.index.filter) != 0 &&
				!kitDBPartialIndexMatches(table.definition, row.values, builder.index.filter) {
				continue
			}
			builder.entries++
			hash := uint64(14695981039346656037)
			for fieldPosition, field := range builder.index.columns {
				item, found := row.values[field]
				if !found {
					item = value.NewNil()
				}
				definitionField, found := kitDBField(table.definition, field)
				if !found {
					return false, fmt.Errorf("kitdb: index field %q disappeared", field)
				}
				component, err := kitDBOrderedFieldScalarComponent(definitionField, item)
				if err != nil {
					return false, err
				}
				for _, next := range component {
					hash ^= uint64(next)
					hash *= 1099511628211
				}
				builder.sketches[fieldPosition].add(hash)
			}
		}
		return false, nil
	})
	if err != nil {
		return kitDBStatistics{}, err
	}
	statistics.Indexes = make([]kitDBIndexStatistics, len(builders))
	for position, builder := range builders {
		index := kitDBIndexStatistics{
			Signature: kitDBIndexSignature(builder.index), Name: builder.index.name,
			Fields: append([]string(nil), builder.index.columns...), Entries: builder.entries,
			DistinctPrefixes: make([]uint64, len(builder.sketches)),
		}
		for prefixPosition := range builder.sketches {
			index.DistinctPrefixes[prefixPosition] = builder.sketches[prefixPosition].estimate(builder.entries)
		}
		statistics.Indexes[position] = index
	}
	sort.Slice(statistics.Indexes, func(left, right int) bool {
		return statistics.Indexes[left].Name < statistics.Indexes[right].Name
	})
	return statistics, nil
}

func (t *SchemaTable) estimateKitDBAccess(
	access kitDBAccessPlan,
	plan query.ExecutionPlan,
) kitDBAccessPlan {
	state := t.currentKitDBStatisticsState()
	access.statisticsState = state
	if t.statistics != nil {
		access.statisticsTransaction = t.statistics.AnalyzedTransaction
	}
	if state != "current" || t.statistics == nil {
		return access
	}

	switch access.kind {
	case kitDBAccessPrimary, kitDBAccessUnique:
		access.hasEstimate = true
		if t.statistics.Rows == 0 {
			access.estimatedRows = 0
			access.estimateKind = "exact"
		} else {
			access.estimatedRows = 1
			access.estimateKind = "upper_bound"
		}
		return access
	case kitDBAccessScan:
		access.hasEstimate = true
		access.estimatedRows = t.statistics.Rows
		if kitDBPlanHasFilter(plan) {
			access.estimateKind = "upper_bound"
		} else {
			access.estimateKind = "exact"
		}
		return access
	}

	var indexStatistics *kitDBIndexStatistics
	for position := range t.statistics.Indexes {
		candidate := &t.statistics.Indexes[position]
		if candidate.Signature == access.indexSignature {
			indexStatistics = candidate
			break
		}
	}
	if indexStatistics == nil {
		return access
	}
	access.hasEstimate = true
	access.estimatedRows = indexStatistics.Entries
	access.estimateKind = "exact"
	if access.equalityPrefix > 0 && access.equalityPrefix <= len(indexStatistics.DistinctPrefixes) {
		distinct := indexStatistics.DistinctPrefixes[access.equalityPrefix-1]
		if distinct != 0 {
			access.estimatedRows = (indexStatistics.Entries + distinct - 1) / distinct
			access.estimateKind = "approximate"
		}
	}
	if access.rangeField != "" && access.estimateKind == "exact" {
		access.estimateKind = "upper_bound"
	}
	return access
}

func (t *SchemaTable) currentKitDBStatisticsState() string {
	state := t.statisticsState
	if state == "" {
		state = "missing"
	}
	if t.transaction != nil && t.statistics != nil {
		dirty, err := loadKitDBStatisticsDirty(t.transaction, t.definition)
		if err != nil {
			return "unavailable"
		}
		if dirty {
			return "transaction_changed"
		}
	}
	return state
}

func (t *SchemaTable) invalidateKitDBStatistics(transaction *kitDBRecordTransaction) error {
	if transaction == nil || t.definition == nil {
		return fmt.Errorf("kitdb: statistics invalidation is unavailable")
	}
	if err := bumpKitDBContentRevision(transaction, t.definition); err != nil {
		return err
	}
	statisticsKey, err := kitDBStatisticsKey(t.definition)
	if err != nil {
		return err
	}
	if _, found, err := transaction.Get(statisticsKey); err != nil {
		return err
	} else if !found {
		return nil
	}
	dirtyKey, err := kitDBStatisticsDirtyKey(t.definition)
	if err != nil {
		return err
	}
	if dirty, err := loadKitDBStatisticsDirty(transaction, t.definition); err != nil {
		return err
	} else if dirty {
		return nil
	}
	return transaction.Put(dirtyKey, kitDBStatisticsDirtyValue)
}

func (t *SchemaTable) markKitDBStatisticsStale() {
	if t.statistics != nil {
		t.statisticsState = "stale"
	}
}

func kitDBAccessEstimateBetter(candidate, current kitDBAccessPlan) bool {
	if candidate.hasEstimate != current.hasEstimate {
		return candidate.hasEstimate
	}
	return candidate.hasEstimate && candidate.estimatedRows < current.estimatedRows
}

func executeKitDBRemoteAnalyze(
	ctx context.Context,
	scope *requestscope.Scope,
	database *dbProxy,
	statement kitSQLStatement,
) (kitDBRemoteResult, error) {
	if database.transaction != nil {
		return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: ANALYZE cannot run inside a record transaction")
	}
	names := []string{}
	if statement.table != "" {
		name, _, err := kitDBRemoteDefinition(database, statement.table)
		if err != nil {
			return kitDBRemoteResult{}, err
		}
		names = append(names, name)
	} else {
		if err := database.refreshKitDBCatalogDefinitions(); err != nil {
			return kitDBRemoteResult{}, err
		}
		_, definitions := database.schemaSnapshot()
		for name := range definitions {
			names = append(names, name)
		}
		sort.Strings(names)
	}
	for _, name := range names {
		if err := contextError(ctx); err != nil {
			return kitDBRemoteResult{}, err
		}
		table, err := kitDBRemoteTable(database, scope, name)
		if err != nil {
			return kitDBRemoteResult{}, err
		}
		if err := ensureKitDBRemoteTableReady(table); err != nil {
			return kitDBRemoteResult{}, err
		}
		if _, _, err := table.analyzeKitDB(ctx); err != nil {
			return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: ANALYZE %s: %w", name, err)
		}
	}
	return kitDBRemoteResult{affected: int64(len(names))}, nil
}

func executeKitDBRemoteStatistics(
	scope *requestscope.Scope,
	database *dbProxy,
	tableName string,
) (kitDBRemoteResult, error) {
	table, err := kitDBRemoteTable(database, scope, tableName)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := ensureKitDBRemoteTableReady(table); err != nil {
		return kitDBRemoteResult{}, err
	}
	columns := []kitDBRemoteColumn{
		{name: "kind", kind: "text"},
		{name: "name", kind: "text"},
		{name: "fields", kind: "json"},
		{name: "rows", kind: "integer"},
		{name: "entries", kind: "integer"},
		{name: "distinct_prefixes", kind: "json"},
		{name: "status", kind: "text"},
		{name: "analyzed_transaction", kind: "text"},
	}
	state := table.currentKitDBStatisticsState()
	if table.statistics == nil {
		return kitDBRemoteResult{columns: columns, rows: [][]value.Value{{
			value.New("table"), value.New(table.table), value.New("[]"), value.NewNil(),
			value.NewNil(), value.New("[]"), value.New(state), value.NewNil(),
		}}}, nil
	}
	transaction := value.New(fmt.Sprintf("%d", table.statistics.AnalyzedTransaction))
	rows := [][]value.Value{{
		value.New("table"), value.New(table.table), value.New("[]"), value.New(table.statistics.Rows),
		value.New(table.statistics.Rows), value.New("[]"), value.New(state), transaction,
	}}
	for _, index := range table.statistics.Indexes {
		fields, _ := json.Marshal(index.Fields)
		distinct, _ := json.Marshal(index.DistinctPrefixes)
		rows = append(rows, []value.Value{
			value.New("index"), value.New(index.Name), value.New(string(fields)), value.New(table.statistics.Rows),
			value.New(index.Entries), value.New(string(distinct)), value.New(state), transaction,
		})
	}
	return kitDBRemoteResult{columns: columns, rows: rows}, nil
}
