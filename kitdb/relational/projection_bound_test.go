package relational

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

// Preserve the pre-binding conversion rules as an independent regression oracle.
func referenceProjectionRead(field kitdbsql.Field, item any) any {
	if item == nil {
		return nil
	}
	if temporal, ok := item.(exactTemporal); ok {
		return temporal.text
	}
	info, found := kitdbsql.LookupKind(field.Kind)
	if found {
		switch info.Family {
		case kitdbsql.FamilyInteger, kitdbsql.FamilySystem:
			if v, err := integerValue(item); err == nil {
				return v
			}
		case kitdbsql.FamilyFloat:
			if v, err := floatValue(item); err == nil {
				return v
			}
		case kitdbsql.FamilyBoolean:
			if v, err := booleanValue(item); err == nil {
				return v
			}
		}
	}
	switch field.Kind {
	case "bool":
		if v, err := booleanValue(item); err == nil {
			return v
		}
	case "json", "jsonb", "array", "vector":
		if text, ok := item.(string); ok {
			decoder := json.NewDecoder(strings.NewReader(text))
			decoder.UseNumber()
			var value any
			if decoder.Decode(&value) == nil {
				return normalizeJSONNumber(value)
			}
		}
	}
	return item
}

func TestBoundProjectionReadCompatibility(t *testing.T) {
	kinds := []string{"unknown", " BIGINT ", "BOOL", "JSON", "json ", ""}
	for _, kind := range kitdbsql.AllTypes() {
		kinds = append(kinds, kind.Kind)
	}
	values := []any{nil, true, false, int8(2), int64(1), ^uint64(0),
		float32(1.5), float64(0), json.Number("2"), "1", "false", "invalid",
		`{"n":2,"nested":[1,2]}`, `[1,2]`, []byte{1, 2}, map[string]any{"x": int64(1)},
		exactTemporal{text: "2026-10-22"}, exactDecimal("1.25")}
	for _, kind := range kinds {
		t.Run(kind, func(t *testing.T) {
			field := kitdbsql.Field{Name: "v", Kind: kind}
			schema := kitdbsql.Schema{Fields: []kitdbsql.Field{field}}
			_, projection, err := bindProjection(schema, []kitdbsql.Projection{{Name: "v"}})
			if err != nil {
				t.Fatal(err)
			}
			for _, value := range values {
				want := referenceProjectionRead(field, value)
				got := projectRow(map[string]any{"v": value}, projection)[0]
				if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(readField(field, value), want) {
					t.Fatalf("input=%#v (%T) prepared=%#v want=%#v", value, value, got, want)
				}
			}
		})
	}
}

func TestBoundProjectionCapturesBinding(t *testing.T) {
	schema := kitdbsql.Schema{Fields: []kitdbsql.Field{
		{Name: "id", Kind: "bigint", Aliases: []string{"old_id"}},
		{Name: "flag", Kind: "bool"},
	}}
	columns, projection, err := bindProjection(schema, []kitdbsql.Projection{
		{Name: "old_id", Alias: "item"}, {Name: "ID", Alias: "item"}, {Name: "flag"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if columns[0].Name != "item" || columns[1].Name != "item" || columns[2].Kind != "bool" {
		t.Fatalf("columns=%+v", columns)
	}
	// The descriptor owns its canonical name/mode; it does not consult a later
	// mutable schema container or retain a pointer to a field for every cell.
	schema.Fields[0].Name = "changed"
	schema.Fields[0].Kind = "text"
	schema.Fields[1].Kind = "text"
	if got := projectRow(map[string]any{"id": "42", "flag": "true"}, projection); !reflect.DeepEqual(got, []any{int64(42), int64(42), true}) {
		t.Fatalf("binding changed: %#v", got)
	}
	if _, _, err := bindProjection(schema, []kitdbsql.Projection{{Name: "missing"}}); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestBoundProjectionSQLPaths(t *testing.T) {
	ctx := context.Background()
	engine, err := Open(filepath.Join(t.TempDir(), "projection.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := engine.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, query := range []string{
		`CREATE TABLE items(id BIGINT PRIMARY KEY, name TEXT, enabled BOOLEAN, score DOUBLE PRECISION, payload JSONB)`,
		`INSERT INTO items VALUES(1,'one',TRUE,1.5,'{"n":2}'),(2,'two',NULL,2.5,NULL),(3,NULL,FALSE,NULL,'[1,2]')`,
	} {
		if _, err := engine.Execute(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, query string
		stream      bool
		labels      []string
		rows        [][]any
	}{
		{"star", `SELECT * FROM items WHERE id=1`, true, []string{"id", "name", "enabled", "score", "payload"},
			[][]any{{int64(1), "one", true, 1.5, map[string]any{"n": int64(2)}}}},
		{"alias_duplicate_null", `SELECT ID AS item,id AS item,enabled,score,payload FROM items WHERE id=2`, true,
			[]string{"item", "item", "enabled", "score", "payload"}, [][]any{{int64(2), int64(2), nil, 2.5, nil}}},
		{"boolean_json", `SELECT enabled,payload FROM items WHERE id=3`, true,
			[]string{"enabled", "payload"}, [][]any{{false, []any{int64(1), int64(2)}}}},
		{"derived", `SELECT * FROM (SELECT id,name,enabled,score,payload FROM items) AS picked WHERE id=1`, false,
			[]string{"id", "name", "enabled", "score", "payload"}, [][]any{{int64(1), "one", true, 1.5, map[string]any{"n": int64(2)}}}},
		{"cte", `WITH picked AS (SELECT id,enabled FROM items) SELECT id AS item,enabled FROM picked WHERE id=3`, false,
			[]string{"item", "enabled"}, [][]any{{int64(3), false}}},
		{"distinct_offset", `SELECT DISTINCT id FROM items ORDER BY id LIMIT 2 OFFSET 1`, false,
			[]string{"id"}, [][]any{{int64(2)}, {int64(3)}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := engine.Execute(ctx, tc.query)
			if err != nil {
				t.Fatal(err)
			}
			labels := make([]string, len(result.Columns))
			for i, column := range result.Columns {
				labels[i] = column.Name
			}
			if !reflect.DeepEqual(labels, tc.labels) || !reflect.DeepEqual(result.Rows, tc.rows) {
				t.Fatalf("labels=%v rows=%#v want=%#v", labels, result.Rows, tc.rows)
			}
			stream, handled, err := engine.openSelectRowStream(ctx, tc.query, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !tc.stream {
				if handled || stream != nil {
					t.Fatal("materializing query unexpectedly streamed")
				}
				return
			}
			if !handled || stream == nil {
				t.Fatal("expected row stream")
			}
			defer stream.Close()
			var rows [][]any
			for {
				row, err := stream.Next()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				rows = append(rows, row)
			}
			if !reflect.DeepEqual(stream.Columns(), result.Columns) || !reflect.DeepEqual(rows, result.Rows) {
				t.Fatalf("stream mismatch: columns=%+v rows=%#v", stream.Columns(), rows)
			}
		})
	}
}
