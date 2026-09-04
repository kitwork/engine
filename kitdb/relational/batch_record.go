package relational

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"math"
	"unicode/utf8"

	kitdbengine "github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/kitdb/columnar"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

type batchDecoder struct {
	schema    kitdbsql.Schema
	batch     *columnar.Batch
	positions map[uint32]int
	tags      map[uint32]struct{}
}

func newBatchDecoder(schema kitdbsql.Schema, fields []columnar.Field) (*batchDecoder, error) {
	return newBatchDecoderCapacity(schema, fields, columnar.BatchRows)
}

func newBatchDecoderCapacity(schema kitdbsql.Schema, fields []columnar.Field, rows int) (*batchDecoder, error) {
	if rows < 1 || rows > columnarChunkRows {
		return nil, fmt.Errorf("kitdb: invalid batch capacity")
	}
	batch, err := columnar.NewBatch(fields)
	if err != nil {
		return nil, err
	}
	if rows != columnar.BatchRows {
		for index := range batch.Columns {
			batch.Columns[index].Valid = make([]byte, rows)
			switch batch.Columns[index].Field.Kind {
			case columnar.Float:
				batch.Columns[index].Floats = make([]float64, rows)
				batch.Columns[index].Integers = nil
				batch.Columns[index].Texts = nil
			case columnar.Text:
				batch.Columns[index].Texts = make([]string, rows)
				batch.Columns[index].Integers = nil
				batch.Columns[index].Floats = nil
			default:
				batch.Columns[index].Integers = make([]int64, rows)
				batch.Columns[index].Floats = nil
				batch.Columns[index].Texts = nil
			}
		}
	}
	d := &batchDecoder{schema: schema, batch: batch, positions: make(map[uint32]int), tags: make(map[uint32]struct{})}
	for i, field := range fields {
		d.positions[field.Tag] = i
		d.tags[field.Tag] = struct{}{}
	}
	return d, nil
}

// append validates the KROW envelope and walks TLVs without constructing a row
// map. The existing legacy decoder remains the compatibility fallback.
func (d *batchDecoder) append(encoded []byte) error {
	if len(d.batch.Columns) == 0 || d.batch.Rows >= len(d.batch.Columns[0].Valid) {
		return fmt.Errorf("kitdb: batch is full")
	}
	row := d.batch.Rows
	if len(encoded) > documentLimit {
		return fmt.Errorf("kitdb: stored row too large")
	}
	if len(encoded) < 4 || !bytes.Equal(encoded[:4], rowMagic[:]) {
		decoded, err := decodeProjectedRow(d.schema, encoded, d.tags)
		if err != nil {
			return err
		}
		for _, field := range d.schema.Fields {
			position, selected := d.positions[field.Tag]
			if !selected {
				continue
			}
			if err := setBatchValue(&d.batch.Columns[position], row, decoded.values[field.Name]); err != nil {
				return err
			}
		}
		d.batch.Rows++
		return nil
	}
	if len(encoded) < rowHeaderSize || encoded[4] != binaryRowVersion || encoded[5] != 0 || binary.LittleEndian.Uint16(encoded[6:]) != rowHeaderSize {
		return fmt.Errorf("kitdb: invalid KROW batch header")
	}
	count := binary.LittleEndian.Uint32(encoded[8:])
	body := encoded[rowHeaderSize:]
	if count > rowFieldLimit || crc32.Checksum(body, rowCRCTable) != binary.LittleEndian.Uint32(encoded[12:]) {
		return fmt.Errorf("kitdb: invalid KROW batch count/checksum")
	}
	offset, previous := 0, uint64(0)
	for range count {
		tag, err := consumeUvarint(body, &offset)
		if err != nil || tag == 0 || tag <= previous || tag > math.MaxUint32 || offset >= len(body) {
			return fmt.Errorf("kitdb: invalid KROW batch tag")
		}
		previous = tag
		kind := body[offset]
		offset++
		length, err := consumeUvarint(body, &offset)
		if err != nil || kind == 0 || length > uint64(len(body)-offset) {
			return fmt.Errorf("kitdb: invalid KROW batch payload")
		}
		payload := body[offset : offset+int(length)]
		offset += int(length)
		position, selected := d.positions[uint32(tag)]
		if !selected {
			continue
		}
		v := &d.batch.Columns[position]
		if kind == valueNil {
			if len(payload) != 0 {
				return fmt.Errorf("kitdb: invalid null")
			}
			continue
		}
		switch {
		case (v.Field.Kind == columnar.Integer || v.Field.Kind == columnar.Boolean) && kind == valueInteger:
			number, width := binary.Varint(payload)
			var canonical [10]byte
			if width <= 0 || width != len(payload) || binary.PutVarint(canonical[:], number) != width {
				return fmt.Errorf("kitdb: invalid integer")
			}
			v.Integers[row] = number
			if v.Field.Kind == columnar.Boolean && (number < 0 || number > 1) {
				return fmt.Errorf("kitdb: invalid boolean")
			}
		case v.Field.Kind == columnar.Float && kind == valueNumber:
			if len(payload) != 8 {
				return fmt.Errorf("kitdb: invalid float")
			}
			value := math.Float64frombits(binary.LittleEndian.Uint64(payload))
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return fmt.Errorf("kitdb: non-finite float")
			}
			v.Floats[row] = value
		case v.Field.Kind == columnar.Boolean && kind == valueBool:
			if len(payload) != 1 || payload[0] > 1 {
				return fmt.Errorf("kitdb: invalid boolean")
			}
			v.Integers[row] = int64(payload[0])
		case v.Field.Kind == columnar.Text && kind == valueString:
			if !utf8.Valid(payload) {
				return fmt.Errorf("kitdb: invalid UTF-8 text")
			}
			v.Texts[row] = string(payload)
		default:
			// Mixed legacy encoders can store integer-valued floats. Preserve the
			// existing scalar conversion without allocating maps for normal KROW.
			nodes := 0
			value, err := decodeValue(kind, payload, 1, &nodes)
			if err != nil {
				return err
			}
			if err := setBatchValue(v, row, value); err != nil {
				return err
			}
		}
		v.Valid[row] = 1
	}
	if offset != len(body) {
		return fmt.Errorf("kitdb: trailing KROW batch bytes")
	}
	d.batch.Rows++
	return nil
}

func setBatchValue(v *columnar.Vector, row int, value any) error {
	if value == nil {
		return nil
	}
	var err error
	switch v.Field.Kind {
	case columnar.Integer:
		v.Integers[row], err = integerValue(value)
	case columnar.Float:
		v.Floats[row], err = floatValue(value)
		if math.IsNaN(v.Floats[row]) || math.IsInf(v.Floats[row], 0) {
			return fmt.Errorf("kitdb: non-finite batch value")
		}
	case columnar.Boolean:
		b, err := booleanValue(value)
		if err != nil {
			return err
		}
		if b {
			v.Integers[row] = 1
		} else {
			v.Integers[row] = 0
		}
	case columnar.Text:
		v.Texts[row], err = stringValue(value)
	}
	if err == nil {
		v.Valid[row] = 1
	}
	return err
}

func stringValue(value any) (string, error) {
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("expected text, got %T", value)
	}
	if !utf8.ValidString(text) {
		return "", fmt.Errorf("text is not valid UTF-8")
	}
	return text, nil
}

func scanSnapshotBatches(ctx context.Context, snapshot *kitdbengine.Snapshot, schema kitdbsql.Schema, generation uint64, fields []columnar.Field, observe bool, visit func(*columnar.Batch) error) (rows uint64, stats kitdbengine.CursorStats, err error) {
	prefix, err := rowPrefix(schema, generation)
	if err != nil {
		return 0, stats, err
	}
	cursor, err := snapshot.Cursor(kitdbengine.RangeOptions{Prefix: prefix})
	if err != nil {
		return 0, stats, err
	}
	defer func() {
		if observe {
			stats = cursor.Stats()
		}
		cursor.Close()
	}()
	decoder, err := newBatchDecoder(schema, fields)
	if err != nil {
		return 0, stats, err
	}
	for cursor.Next() {
		if rows&255 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, stats, err
			}
		}
		if err := decoder.append(cursor.Value()); err != nil {
			return 0, stats, err
		}
		rows++
		if decoder.batch.Rows == columnar.BatchRows {
			if err := visit(decoder.batch); err != nil {
				return 0, stats, err
			}
			decoder.batch.Reset()
		}
	}
	if err := cursor.Err(); err != nil {
		return 0, stats, err
	}
	if decoder.batch.Rows != 0 {
		if err := visit(decoder.batch); err != nil {
			return 0, stats, err
		}
	}
	return rows, stats, nil
}
