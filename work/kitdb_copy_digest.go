package work

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/kitwork/engine/value"
)

// The digest of a copy of a KitDB file (kitdb_copy.go): every row in one canonical form, made once from what
// KitDB holds and once from what SQLite reads back, by two paths that share no conversion (a decimal is
// fixed-point text made from the digits of the text KitDB stored on one side, from the integer SQLite stored
// on the other), so equal digests say the copy is the data.
//
//   - a field is 0x00 for NULL, else 0x01, its length (uvarint) and its bytes: the text of a TEXT column, a
//     whole number in base 10, a float as Go writes it shortest ('g', -1), a boolean as true or false, a
//     decimal as fixed-point text with exactly its scale's digits after the point (4.50 at scale 2), a BLOB
//     as it is;
//   - a row is the SHA-256 of its fields in the target's column order, and its key the fields of its primary
//     key;
//   - a table's digest starts from the SHA-256 of its name and its columns, then chains its rows sorted by
//     key: h = SHA-256(h || row), sorted in Go on both sides (the order SQLite reads them in is not used);
//   - the digest of what one SQLite file receives is the SHA-256 of its tables' digests, by name.

// kitDBCopyDigestVersion begins every digest: another canonical form is another version.
const kitDBCopyDigestVersion = "kitwork-kitdb-copy/v1"

// kitDBCopyClass is how a column's value is stored in SQLite and written in the canonical form.
type kitDBCopyClass int

const (
	kitDBCopyText kitDBCopyClass = iota
	kitDBCopyJSON
	kitDBCopyInteger
	kitDBCopyFloat
	kitDBCopyBool
	kitDBCopyDecimal
	kitDBCopyBytes
)

func (class kitDBCopyClass) String() string {
	switch class {
	case kitDBCopyJSON:
		return "json"
	case kitDBCopyInteger:
		return "integer"
	case kitDBCopyFloat:
		return "float"
	case kitDBCopyBool:
		return "boolean"
	case kitDBCopyDecimal:
		return "decimal"
	case kitDBCopyBytes:
		return "bytes"
	}
	return "text"
}

// kitDBCopyColumn is one column of a table the copy writes, as the target file has it.
type kitDBCopyColumn struct {
	name    string
	class   kitDBCopyClass
	scale   int // of a decimal
	notNull bool
	// spec is the column .shared/schema/ declares; nil for a column of an engine table
	spec *ColumnSpec
}

// kitDBCopyTable is one table the copy writes: its columns in the target's order, the positions of its
// primary key in key order, and the rows read from KitDB, converted.
type kitDBCopyTable struct {
	name    string
	columns []kitDBCopyColumn
	primary []int
	// merge: the rows the target already holds win, and only the keys it lacks are copied (kitwork_definitions
	// and kitwork_schedules, which boot syncs from the app's own files)
	merge bool
	// declared is the table as .shared/schema/ declares it; nil for an engine table
	declared *kitDBCopyDeclared
	rows     []kitDBCopyRow
}

// kitDBCopyField is one field in the canonical form: null, or its bytes.
type kitDBCopyField struct {
	null bool
	data []byte
}

// kitDBCopyRow is one row: its key and its hash in the canonical form, the text of its key for a message, and
// its values as SQLite takes them (nil for a row read back).
type kitDBCopyRow struct {
	key    []byte
	label  string
	hash   [32]byte
	values []any
}

func appendKitDBCopyField(out []byte, field kitDBCopyField) []byte {
	if field.null {
		return append(out, 0)
	}
	out = append(out, 1)
	out = binary.AppendUvarint(out, uint64(len(field.data)))
	return append(out, field.data...)
}

// newKitDBCopyRow is the row of fields, in the table's column order.
func (table *kitDBCopyTable) newRow(fields []kitDBCopyField, values []any) kitDBCopyRow {
	var encoded, key []byte
	for _, field := range fields {
		encoded = appendKitDBCopyField(encoded, field)
	}
	labels := make([]string, 0, len(table.primary))
	for _, position := range table.primary {
		key = appendKitDBCopyField(key, fields[position])
		labels = append(labels, string(fields[position].data))
	}
	return kitDBCopyRow{key: key, label: strings.Join(labels, "/"), hash: sha256.Sum256(encoded), values: values}
}

// digest is the digest of rows as rows of the table.
func (table *kitDBCopyTable) digest(rows []kitDBCopyRow) [32]byte {
	seed := sha256.New()
	seed.Write([]byte(kitDBCopyDigestVersion))
	seed.Write([]byte{0})
	seed.Write([]byte(table.name))
	seed.Write([]byte{0})
	for _, column := range table.columns {
		fmt.Fprintf(seed, "%s:%s:%d", column.name, column.class, column.scale)
		seed.Write([]byte{0})
	}
	var chain [32]byte
	copy(chain[:], seed.Sum(nil))
	sorted := append([]kitDBCopyRow(nil), rows...)
	sort.Slice(sorted, func(left, right int) bool { return bytes.Compare(sorted[left].key, sorted[right].key) < 0 })
	link := make([]byte, 64)
	for _, row := range sorted {
		copy(link, chain[:])
		copy(link[32:], row.hash[:])
		chain = sha256.Sum256(link)
	}
	return chain
}

// kitDBCopyFileDigest is the digest of what one SQLite file receives: its tables' digests, by name.
func kitDBCopyFileDigest(digests map[string][32]byte) string {
	names := make([]string, 0, len(digests))
	for name := range digests {
		names = append(names, name)
	}
	sort.Strings(names)
	sum := sha256.New()
	sum.Write([]byte(kitDBCopyDigestVersion))
	sum.Write([]byte{0})
	for _, name := range names {
		digest := digests[name]
		sum.Write([]byte(name))
		sum.Write([]byte{0})
		sum.Write(digest[:])
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// kitDBCopyDifferent lists, by key, the rows of want and got that differ (a row in one only, or with other
// fields), at most limit of them: the keys only, never a value.
func kitDBCopyDifferent(want, got []kitDBCopyRow, limit int) []string {
	held := make(map[string]kitDBCopyRow, len(got))
	for _, row := range got {
		held[string(row.key)] = row
	}
	var keys []string
	for _, row := range want {
		other, found := held[string(row.key)]
		delete(held, string(row.key))
		if !found || other.hash != row.hash {
			keys = append(keys, row.label)
		}
	}
	for _, row := range held {
		keys = append(keys, row.label)
	}
	sort.Strings(keys)
	if len(keys) > limit {
		keys = append(keys[:limit], fmt.Sprintf("… và %d dòng khác", len(keys)-limit))
	}
	return keys
}

// kitDBCopyLargestInteger is the largest whole number a value of the VM holds exactly.
const kitDBCopyLargestInteger = 1<<53 - 1

// kitDBCopyValue is a value read from KitDB as SQLite stores it in column, with its canonical field; problem
// says why it has none (never quoting the value).
func kitDBCopyValue(column kitDBCopyColumn, item value.Value) (stored any, field kitDBCopyField, problem string) {
	if item.K == value.Nil {
		if column.notNull {
			return nil, kitDBCopyField{}, "trống, mà cột không nhận null"
		}
		return nil, kitDBCopyField{null: true}, ""
	}
	wrong := func() (any, kitDBCopyField, string) {
		return nil, kitDBCopyField{}, fmt.Sprintf("KitDB giữ một giá trị kiểu %s, cột %s không nhận kiểu đó", item.K, column.class)
	}
	switch column.class {
	case kitDBCopyText:
		if item.K != value.String {
			return wrong()
		}
		return item.String(), kitDBCopyField{data: []byte(item.String())}, ""
	case kitDBCopyJSON:
		// stored as coerceWrite writes a json column: a string as it is, anything else as its JSON
		text := item.String()
		if item.K != value.String {
			if item.K != value.Map && item.K != value.Array && item.K != value.Number && item.K != value.Bool {
				return wrong()
			}
			encoded, err := json.Marshal(item)
			if err != nil {
				return wrong()
			}
			text = string(encoded)
		}
		return text, kitDBCopyField{data: []byte(text)}, ""
	case kitDBCopyInteger:
		if item.K != value.Number || math.IsNaN(item.N) || math.IsInf(item.N, 0) || item.N != math.Trunc(item.N) ||
			math.Abs(item.N) > kitDBCopyLargestInteger {
			return wrong()
		}
		integer := int64(item.N)
		return integer, kitDBCopyField{data: []byte(strconv.FormatInt(integer, 10))}, ""
	case kitDBCopyFloat:
		if item.K != value.Number || math.IsNaN(item.N) || math.IsInf(item.N, 0) {
			return wrong()
		}
		return item.N, kitDBCopyField{data: []byte(strconv.FormatFloat(item.N, 'g', -1, 64))}, ""
	case kitDBCopyBool:
		truth := false
		switch {
		case item.K == value.Bool:
			truth = item.N != 0
		case item.K == value.Number && (item.N == 0 || item.N == 1):
			truth = item.N == 1
		default:
			return wrong()
		}
		if truth {
			return int64(1), kitDBCopyField{data: []byte("true")}, ""
		}
		return int64(0), kitDBCopyField{data: []byte("false")}, ""
	case kitDBCopyDecimal:
		return kitDBCopyDecimalValue(column, item)
	case kitDBCopyBytes:
		if item.K != value.Bytes {
			return wrong()
		}
		data := bytes.Clone(item.Bytes())
		return data, kitDBCopyField{data: data}, ""
	}
	return wrong()
}

// kitDBCopyDecimalValue is a decimal KitDB kept (its text, or a number of the VM) as the integer SQLite keeps
// at the column's scale, exact: a value that would have to be rounded, or that has more than 15 digits, has
// none. Its canonical field is made from the stored text's digits, never from the integer; and the integer is
// held against the one the engine's own write gives (sqliteWrite), which rounds with floats.
func kitDBCopyDecimalValue(column kitDBCopyColumn, item value.Value) (any, kitDBCopyField, string) {
	number, ok := kitDBDecimal(item)
	if !ok {
		return nil, kitDBCopyField{}, "không phải số thập phân"
	}
	digits := "0"
	if number.digits != "" {
		shift := number.exponent + int64(column.scale) - int64(len(number.digits))
		if shift < 0 {
			return nil, kitDBCopyField{}, fmt.Sprintf("có nhiều chữ số sau dấu phẩy hơn %d của decimal(…, %d): chép phải làm tròn", column.scale, column.scale)
		}
		if int64(len(number.digits))+shift > 15 {
			return nil, kitDBCopyField{}, "vượt 15 chữ số của decimal"
		}
		digits = number.digits + strings.Repeat("0", int(shift))
	}
	magnitude, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || magnitude > sqliteDecimalLimit {
		return nil, kitDBCopyField{}, "vượt 15 chữ số của decimal"
	}
	negative := number.sign < 0 && magnitude != 0
	scaled := magnitude
	if negative {
		scaled = -magnitude
	}
	if column.spec != nil {
		engine, message := sqliteWrite(column.spec, item)
		if message != "" || engine.K != value.Number || engine.N != float64(scaled) {
			return nil, kitDBCopyField{}, "số nguyên của decimal không khớp số engine ghi cho cùng giá trị"
		}
	}
	return scaled, kitDBCopyField{data: []byte(kitDBCopyFixed(negative, digits, column.scale))}, ""
}

// kitDBCopyFixed is a decimal as fixed-point text: its sign, its digits as a whole number, and scale of them
// after the point (450 at scale 2 is 4.50).
func kitDBCopyFixed(negative bool, digits string, scale int) string {
	if len(digits) <= scale {
		digits = strings.Repeat("0", scale-len(digits)+1) + digits
	}
	text := digits
	if scale > 0 {
		text = digits[:len(digits)-scale] + "." + digits[len(digits)-scale:]
	}
	if negative {
		text = "-" + text
	}
	return text
}

// kitDBCopyRead is a value SQLite read back from column, in the canonical form; problem says it is not of the
// type the column stores (a value SQLite's affinity turned into another type).
func kitDBCopyRead(column kitDBCopyColumn, stored any) (kitDBCopyField, string) {
	if stored == nil {
		return kitDBCopyField{null: true}, ""
	}
	wrong := func() (kitDBCopyField, string) {
		return kitDBCopyField{}, fmt.Sprintf("SQLite giữ một giá trị kiểu %T, cột %s không giữ kiểu đó", stored, column.class)
	}
	switch column.class {
	case kitDBCopyText, kitDBCopyJSON:
		text, ok := stored.(string)
		if !ok {
			return wrong()
		}
		return kitDBCopyField{data: []byte(text)}, ""
	case kitDBCopyInteger:
		integer, ok := stored.(int64)
		if !ok {
			return wrong()
		}
		return kitDBCopyField{data: []byte(strconv.FormatInt(integer, 10))}, ""
	case kitDBCopyFloat:
		number, ok := stored.(float64)
		if !ok {
			return wrong()
		}
		return kitDBCopyField{data: []byte(strconv.FormatFloat(number, 'g', -1, 64))}, ""
	case kitDBCopyBool:
		integer, ok := stored.(int64)
		if !ok || (integer != 0 && integer != 1) {
			return wrong()
		}
		return kitDBCopyField{data: []byte(strconv.FormatBool(integer == 1))}, ""
	case kitDBCopyDecimal:
		integer, ok := stored.(int64)
		if !ok {
			return wrong()
		}
		magnitude := uint64(integer)
		if integer < 0 {
			magnitude = uint64(-(integer + 1)) + 1
		}
		return kitDBCopyField{data: []byte(kitDBCopyFixed(integer < 0, strconv.FormatUint(magnitude, 10), column.scale))}, ""
	case kitDBCopyBytes:
		data, ok := stored.([]byte)
		if !ok {
			return wrong()
		}
		return kitDBCopyField{data: bytes.Clone(data)}, ""
	}
	return wrong()
}
