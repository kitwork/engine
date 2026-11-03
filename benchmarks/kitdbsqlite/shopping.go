package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"strconv"
	"strings"
)

// column is one field of shopping, in the position of its KitDB definition, with the canonical encoding the
// PostgreSQL import used for its checksum (kitdb/migrate/postgres/stream.go, removed 03/11/2026).
type column struct {
	name     string
	kind     string // integer | text | bool | decimal | array | jsonb
	nullable bool
}

var shoppingColumns = []column{
	{"merchant", "text", false}, {"id", "integer", false}, {"shop", "integer", false}, {"arrange", "integer", false},
	{"name", "text", false}, {"description", "text", false}, {"content", "text", false}, {"brand", "text", false},
	{"images", "array", false}, {"video", "text", false}, {"category", "integer", false}, {"categories", "array", false},
	{"categorization", "integer", false}, {"rating", "decimal", false}, {"review", "integer", false}, {"sold", "integer", false},
	{"selled", "integer", false}, {"attributes", "jsonb", false}, {"price", "integer", false}, {"pricing", "integer", false},
	{"discount", "integer", false}, {"official", "bool", false}, {"source", "text", false}, {"expired", "integer", false},
	{"stock", "integer", false}, {"like", "integer", false}, {"commission", "decimal", false}, {"location", "text", false},
	{"is_adult", "bool", true}, {"is_deleted", "bool", true}, {"is_detail", "bool", true},
}

// The 2026-08-28 PostgreSQL import, as its _kitdb_migration row records it. KitDB normalizes decimals on write
// (4.50 becomes 4.5), so a stream read back from KitDB does not reproduce importChecksum; copy and verify
// compare KitDB with SQLite through the same chain instead.
const (
	importID          = "shopping-full-20260828-v3"
	importSource      = "KitDB/public.shopping"
	importFingerprint = "042802ef7f3f2b10d3b4e682ccdee62248fe87d186efa10593be7ca3222e83e3"
	importRows        = 13773074
	importChecksum    = "632c9b1606a03e34558cc82473cbff6915b9d64de7772a2916b68842e4ff4375"
)

func quoted(name string) string { return `"` + name + `"` }

func columnList() string {
	names := make([]string, len(shoppingColumns))
	for i, c := range shoppingColumns {
		names[i] = quoted(c.name)
	}
	return strings.Join(names, ", ")
}

// createTable is the STRICT table of main.db. Decimals stay TEXT as KitDB returns them, which keeps the copy
// byte-comparable but compares and orders them as text (see cmdDecimal).
func createTable() string {
	types := map[string]string{"integer": "INTEGER", "text": "TEXT", "bool": "INTEGER", "decimal": "TEXT",
		"array": "TEXT", "jsonb": "TEXT"}
	var b strings.Builder
	b.WriteString("CREATE TABLE shopping (\n")
	for _, c := range shoppingColumns {
		fmt.Fprintf(&b, "\t%s %s", quoted(c.name), types[c.kind])
		if !c.nullable {
			b.WriteString(" NOT NULL")
		}
		switch c.kind {
		case "bool":
			fmt.Fprintf(&b, " CHECK (%s IN (0, 1))", quoted(c.name))
		case "array":
			fmt.Fprintf(&b, " CHECK (json_type(%s) = 'array')", quoted(c.name))
		case "jsonb":
			fmt.Fprintf(&b, " CHECK (json_valid(%s))", quoted(c.name))
		}
		b.WriteString(",\n")
	}
	b.WriteString("\tPRIMARY KEY (merchant, id)\n) STRICT")
	return b.String()
}

// chain is the import's row checksum: a seed over the import identity, then sha256(previous || fields) per row.
type chain struct{ sum [sha256.Size]byte }

func newChain() *chain {
	names := make([]string, len(shoppingColumns))
	for i, c := range shoppingColumns {
		names[i] = c.name
	}
	seed := "kitdb-postgres-migration/v1\x00" + importID + "\x00" + importSource + "\x00" + importFingerprint +
		"\x00" + strings.Join(names, "\x00")
	return &chain{sum: sha256.Sum256([]byte(seed))}
}

func writeField(h hash.Hash, field []byte) {
	if field == nil {
		h.Write([]byte{0})
		return
	}
	h.Write([]byte{1})
	var length [binary.MaxVarintLen64]byte
	h.Write(length[:binary.PutUvarint(length[:], uint64(len(field)))])
	h.Write(field)
}

func (c *chain) advance(fields [][]byte) {
	h := sha256.New()
	h.Write(c.sum[:])
	for _, f := range fields {
		writeField(h, f)
	}
	copy(c.sum[:], h.Sum(nil))
}

func (c *chain) hex() string { return hex.EncodeToString(c.sum[:]) }

func asString(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case []byte:
		return string(x), true
	}
	return "", false
}

// convert returns the value written to SQLite and its canonical form (nil is NULL) for the checksum.
func convert(c column, v any) (any, []byte, error) {
	if v == nil {
		if !c.nullable {
			return nil, nil, fmt.Errorf("column %s is NOT NULL but read NULL", c.name)
		}
		return nil, nil, nil
	}
	switch c.kind {
	case "integer":
		if x, ok := v.(int64); ok {
			return x, strconv.AppendInt(nil, x, 10), nil
		}
	case "text", "decimal":
		if s, ok := asString(v); ok {
			return s, []byte(s), nil
		}
	case "bool":
		if b, ok := v.(bool); ok {
			if b {
				return int64(1), []byte("true"), nil
			}
			return int64(0), []byte("false"), nil
		}
	case "array":
		items, ok := v.([]any)
		if !ok {
			s, isText := asString(v)
			if !isText {
				break
			}
			if err := json.Unmarshal([]byte(s), &items); err != nil {
				return nil, nil, fmt.Errorf("column %s: %w", c.name, err)
			}
		}
		texts := make([]string, len(items))
		for i, item := range items {
			s, isText := item.(string)
			if !isText {
				return nil, nil, fmt.Errorf("column %s: element %T is not text", c.name, item)
			}
			texts[i] = s
		}
		encoded, err := json.Marshal(texts)
		if err != nil {
			return nil, nil, err
		}
		return string(encoded), encoded, nil
	case "jsonb":
		var decoded any
		switch x := v.(type) {
		case map[string]any, []any:
			decoded = x
		default:
			s, isText := asString(v)
			if !isText {
				return nil, nil, fmt.Errorf("column %s: unexpected value %T", c.name, v)
			}
			if err := json.Unmarshal([]byte(s), &decoded); err != nil {
				return nil, nil, fmt.Errorf("column %s: %w", c.name, err)
			}
		}
		encoded, err := json.Marshal(decoded)
		if err != nil {
			return nil, nil, err
		}
		return string(encoded), encoded, nil
	}
	return nil, nil, fmt.Errorf("column %s (%s): unexpected value %T", c.name, c.kind, v)
}

// canonicalFromSQLite reads a stored SQLite value back into the canonical form.
func canonicalFromSQLite(c column, v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	switch c.kind {
	case "integer":
		if x, ok := v.(int64); ok {
			return strconv.AppendInt(nil, x, 10), nil
		}
	case "bool":
		if x, ok := v.(int64); ok {
			if x == 1 {
				return []byte("true"), nil
			}
			return []byte("false"), nil
		}
	default:
		if s, ok := asString(v); ok {
			return []byte(s), nil
		}
	}
	return nil, fmt.Errorf("column %s: unexpected stored value %T", c.name, v)
}
