// Package columnar is an experimental, blocked-column projection.
// It is derived data, not KitDB's canonical record or transaction format.
package columnar

import "fmt"

const BatchRows = 1024
const MaximumColumns = 128
const MaximumGroupPayloadBytes = 64 << 20

type Kind byte

const (
	Integer Kind = 1
	Float   Kind = 2
	Boolean Kind = 3
	Text    Kind = 4
)

type Field struct {
	Tag  uint32
	Kind Kind
}

// ColumnStatistics describes one column inside one immutable block. IntegerSum
// is a signed two's-complement 128-bit value: high*2^64 + low.
type ColumnStatistics struct {
	Field          Field
	Nulls          uint32
	HasValue       bool
	IntegerMin     int64
	IntegerMax     int64
	FloatMin       float64
	FloatMax       float64
	IntegerSumHigh int64
	IntegerSumLow  uint64
}

type BlockStatistics struct {
	Columns      []ColumnStatistics
	Rows         int
	EncodedBytes int64
}

type BlockAction byte

const (
	ScanBlock BlockAction = iota
	SkipBlock
	UseBlockStatistics
)

type ScanReport struct {
	Rows                 uint64
	RowsScanned          uint64
	BlocksScanned        uint64
	RowsSkipped          uint64
	BlocksSkipped        uint64
	RowsFromStatistics   uint64
	BlocksFromStatistics uint64
	// I/O counters cover successful block reads during this scan. They exclude
	// Reader.Open metadata and cannot distinguish OS-cache hits from disk I/O.
	BlockHeadersRead       uint64
	BlockHeaderBytesRead   uint64
	ColumnPayloadsRead     uint64
	ColumnPayloadBytesRead uint64
}

// BlockRange selects complete encoded groups inside one columnar section.
// Callers derive ranges from checksummed projection metadata; ScanBlockRanges
// still validates every boundary and requested payload before trusting it.
type BlockRange struct {
	Offset int64
	Length int64
}

type Vector struct {
	Field    Field
	Valid    []byte
	Integers []int64
	Floats   []float64
	Texts    []string
}
type Batch struct {
	Columns []Vector
	Rows    int
}

func NewBatch(fields []Field) (*Batch, error) {
	if err := validateFields(fields); err != nil {
		return nil, err
	}
	b := &Batch{Columns: make([]Vector, len(fields))}
	for i, field := range fields {
		b.Columns[i] = Vector{Field: field, Valid: make([]byte, BatchRows)}
		switch field.Kind {
		case Float:
			b.Columns[i].Floats = make([]float64, BatchRows)
		case Text:
			b.Columns[i].Texts = make([]string, BatchRows)
		default:
			b.Columns[i].Integers = make([]int64, BatchRows)
		}
	}
	return b, nil
}

func validateFields(fields []Field) error {
	if len(fields) == 0 || len(fields) > MaximumColumns {
		return fmt.Errorf("columnar: field count must be 1..%d", MaximumColumns)
	}
	seen := make(map[uint32]bool, len(fields))
	for _, field := range fields {
		if field.Tag == 0 || seen[field.Tag] || field.Kind < Integer || field.Kind > Text {
			return fmt.Errorf("columnar: invalid field")
		}
		seen[field.Tag] = true
	}
	return nil
}

func (b *Batch) Reset() {
	b.Rows = 0
	for i := range b.Columns {
		clear(b.Columns[i].Valid)
		clear(b.Columns[i].Texts)
	}
}
