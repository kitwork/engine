package work

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

func TestKitSQLScalarExpressionsMatchSQLite(t *testing.T) {
	oracle, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer oracle.Close()

	for index, expression := range []string{
		`1 + 2 * 3`,
		`(1 + 2) * 3`,
		`5 / 2`,
		`5.0 / 2`,
		`10 / 0`,
		`11 % 4`,
		`'kit' || 'db'`,
		`COALESCE(NULL, NULL, 'ready')`,
		`NULLIF('same', 'same')`,
		`LOWER('KiTDB')`,
		`UPPER('kitdb')`,
		`TRIM('  data  ')`,
		`LENGTH('Nguyễn')`,
		`ABS(-7)`,
		`ROUND(12.345, 2)`,
		`CASE WHEN 3 BETWEEN 1 AND 4 THEN 'inside' ELSE 'outside' END`,
		`CASE 'b' WHEN 'a' THEN 1 WHEN 'b' THEN 2 ELSE 3 END`,
		`NULL AND 0`,
		`NULL OR 1`,
		`NOT (NULL OR 0)`,
		`3 IN (1, 3, NULL)`,
		`4 NOT IN (1, 3, NULL)`,
	} {
		t.Run(fmt.Sprintf("%02d", index), func(t *testing.T) {
			statement, err := parseKitSQL("SELECT "+expression+" AS result", kitSQLBindings{
				named: map[string]value.Value{},
			})
			if err != nil {
				t.Fatalf("KitDB parse %q: %v", expression, err)
			}
			actualResult := executeKitDBRemoteScalar(statement)
			actual := normalizeKitSQLDifferentialValue(
				actualResult.rows[0][0], actualResult.columns[0].kind,
			)
			var expected any
			if err := oracle.QueryRow("SELECT " + expression).Scan(&expected); err != nil {
				t.Fatalf("SQLite query %q: %v", expression, err)
			}
			if bytes, ok := expected.([]byte); ok {
				expected = string(bytes)
			}
			if !reflect.DeepEqual(actual, expected) {
				t.Fatalf("expression %q: KitDB=%#v SQLite=%#v", expression, actual, expected)
			}
		})
	}
}

func TestKitSQLExpressionBounds(t *testing.T) {
	deep := strings.Repeat("(", kitSQLExpressionDepthLimit+1) + "1" +
		strings.Repeat(")", kitSQLExpressionDepthLimit+1)
	if _, err := parseKitSQL("SELECT "+deep, kitSQLBindings{named: map[string]value.Value{}}); err == nil ||
		!strings.Contains(err.Error(), "depth") {
		t.Fatalf("deep expression error = %v", err)
	}

	items := make([]string, kitSQLExpressionSetLimit+1)
	for index := range items {
		items[index] = fmt.Sprint(index)
	}
	set := "SELECT 1 IN (" + strings.Join(items, ",") + ")"
	if _, err := parseKitSQL(set, kitSQLBindings{named: map[string]value.Value{}}); err == nil ||
		!strings.Contains(err.Error(), "IN exceeds") {
		t.Fatalf("wide IN expression error = %v", err)
	}
}

func normalizeKitSQLDifferentialValue(item value.Value, kind string) any {
	if item.IsNil() {
		return nil
	}
	switch item.K {
	case value.Bool:
		if item.N != 0 {
			return int64(1)
		}
		return int64(0)
	case value.Number:
		if storageClass(kind) == "INTEGER" {
			return int64(item.N)
		}
		return item.N
	case value.Bytes:
		return string(item.Bytes())
	default:
		return item.Interface()
	}
}
