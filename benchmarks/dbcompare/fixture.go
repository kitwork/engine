package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"strings"
)

const schemaSQL = `CREATE TABLE products (id INTEGER PRIMARY KEY, merchant INTEGER NOT NULL, name TEXT NOT NULL, price BIGINT, rating FLOAT, enabled BOOLEAN NOT NULL, payload TEXT NOT NULL)`
const indexSQL = `CREATE INDEX products_merchant_id ON products (merchant, id)`

type queryCase struct {
	Name  string  `json:"name"`
	SQL   string  `json:"sql"`
	Want  [][]any `json:"want"`
	Batch bool    `json:"batch"`
}

type workload struct {
	Rows           int         `json:"rows"`
	Batch          int         `json:"batch"`
	CheckpointRows int         `json:"checkpoint_rows"`
	Repetitions    int         `json:"repetitions"`
	Warmups        int         `json:"warmups"`
	CacheMiB       int         `json:"cache_mib"`
	Schema         []string    `json:"schema"`
	Inserts        string      `json:"inserts"`
	Queries        []queryCase `json:"queries"`
	Update         string      `json:"update"`
	AfterUpdate    queryCase   `json:"after_update"`
}

type product struct {
	id, merchant  int
	name          string
	price, rating any
	enabled       bool
	payload       string
}

func fixtureProduct(i int) product {
	p := product{i, i % 64, fmt.Sprintf("product %d", i%997), int64(i * 37 % 10000), float64(i%20) / 4, i%2 == 0, ""}
	if i%17 == 0 {
		p.price = nil
	}
	if i%19 == 0 {
		p.rating = nil
	}
	p.payload = strings.Repeat("product description ", 12) + fmt.Sprintf("%08x", uint32(i)*2654435761)
	return p
}

func sqlNumber(v any) string {
	if v == nil {
		return "NULL"
	}
	return fmt.Sprint(v)
}

type totals struct {
	rows, prices, ratings int64
	sum, ratingSum        float64
	min, maxRating        any
}

func (a *totals) add(p product) {
	a.rows++
	if p.price != nil {
		v := p.price.(int64)
		a.prices++
		a.sum += float64(v)
		if a.min == nil || v < a.min.(int64) {
			a.min = v
		}
	}
	if p.rating != nil {
		v := p.rating.(float64)
		a.ratings++
		a.ratingSum += v
		if a.maxRating == nil || v > a.maxRating.(float64) {
			a.maxRating = v
		}
	}
}

func (a totals) sumValue() any {
	if a.prices == 0 {
		return nil
	}
	return a.sum
}
func (a totals) avgValue() any {
	if a.ratings == 0 {
		return nil
	}
	return a.ratingSum / float64(a.ratings)
}

func cases(n int, changed bool) []queryCase {
	var all, filtered, nulls totals
	groups := make([]totals, 64)
	var lookup, page [][]any
	for i := 0; i < n; i++ {
		p := fixtureProduct(i)
		if changed && i == n/2 {
			p.price = int64(99999)
		}
		all.add(p)
		groups[p.merchant].add(p)
		if p.price == nil {
			nulls.add(p)
		} else if v := p.price.(int64); v >= 2500 && v < 7500 && p.enabled {
			filtered.add(p)
		}
		if i == n/2 {
			lookup = append(lookup, []any{p.id, p.name, p.price, p.merchant})
		}
		if p.merchant == 7 && i >= n/2 && len(page) < 100 {
			page = append(page, []any{p.id, p.name, p.price, p.merchant})
		}
	}
	var grouped [][]any
	for merchant, a := range groups {
		if a.rows > 0 {
			grouped = append(grouped, []any{merchant, a.rows, a.sumValue()})
		}
	}
	return []queryCase{
		{"lookup", fmt.Sprintf("SELECT id, name, price, merchant FROM products WHERE id = %d", n/2), lookup, false},
		{"indexed_page", fmt.Sprintf("SELECT id, name, price, merchant FROM products WHERE merchant = 7 AND id >= %d ORDER BY id LIMIT 100", n/2), page, false},
		{"count_all", "SELECT COUNT(*) FROM products", [][]any{{all.rows}}, false},
		{"aggregate_all", "SELECT COUNT(*), COUNT(price), SUM(price), AVG(rating), MIN(price), MAX(rating) FROM products", [][]any{{all.rows, all.prices, all.sumValue(), all.avgValue(), all.min, all.maxRating}}, true},
		{"aggregate_filter", "SELECT COUNT(*), SUM(price), AVG(rating) FROM products WHERE price >= 2500 AND price < 7500 AND enabled = true", [][]any{{filtered.rows, filtered.sumValue(), filtered.avgValue()}}, true},
		{"aggregate_null", "SELECT COUNT(*), SUM(price) FROM products WHERE price IS NULL", [][]any{{nulls.rows, nulls.sumValue()}}, true},
		{"group_merchant", "SELECT merchant, COUNT(*), SUM(price) FROM products GROUP BY merchant ORDER BY merchant", grouped, true},
	}
}

func writeJSON(path string, v any) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	e := json.NewEncoder(f)
	e.SetIndent("", "  ")
	err = e.Encode(v)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func createWorkload(dir string, n, batch, checkpoint, repetitions, cache int) (workload, error) {
	w := workload{n, batch, checkpoint, repetitions, 2, cache, []string{schemaSQL, indexSQL}, filepath.Join(dir, "inserts.sql"), cases(n, false), fmt.Sprintf("UPDATE products SET price = 99999 WHERE id = %d", n/2), cases(n, true)[3]}
	f, err := os.OpenFile(w.Inserts, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return w, err
	}
	out := bufio.NewWriter(f)
	for start := 0; start < n; start += batch {
		var sql strings.Builder
		sql.WriteString("INSERT INTO products (id,merchant,name,price,rating,enabled,payload) VALUES ")
		for i := start; i < min(start+batch, n); i++ {
			p := fixtureProduct(i)
			if i != start {
				sql.WriteByte(',')
			}
			fmt.Fprintf(&sql, "(%d,%d,'%s',%s,%s,%t,'%s')", p.id, p.merchant, p.name, sqlNumber(p.price), sqlNumber(p.rating), p.enabled, p.payload)
		}
		if _, err = fmt.Fprintln(out, sql.String()); err != nil {
			break
		}
	}
	if flushErr := out.Flush(); err == nil {
		err = flushErr
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = writeJSON(filepath.Join(dir, "workload.json"), w)
	}
	return w, err
}

func numeric(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case float64:
		return x, true
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	default:
		return 0, false
	}
}

func checkRows(got, want [][]any) error {
	if len(got) != len(want) {
		return fmt.Errorf("row count %d, want %d", len(got), len(want))
	}
	for i := range want {
		if len(got[i]) != len(want[i]) {
			return fmt.Errorf("row %d width mismatch", i)
		}
		for j, expected := range want[i] {
			actual := got[i][j]
			// Drivers expose exact NUMERIC aggregates as decimal text. Compare
			// that text to numeric expectations without discarding integer bits.
			if text, ok := actual.(string); ok && numericTextMatches(text, expected) {
				continue
			}
			if text, ok := expected.(string); ok && numericTextMatches(text, actual) {
				continue
			}
			a, na := numeric(actual)
			b, nb := numeric(expected)
			if na && nb && !math.IsNaN(a) && !math.IsInf(a, 0) && math.Abs(a-b) <= 1e-9*math.Max(1, math.Abs(b)) {
				continue
			}
			if !na && !nb && actual == expected {
				continue
			}
			return fmt.Errorf("row %d column %d: got %v (%T), want %v (%T)", i, j, actual, actual, expected, expected)
		}
	}
	return nil
}

func numericTextMatches(text string, value any) bool {
	decimal, ok := new(big.Rat).SetString(text)
	if !ok {
		return false
	}
	var expected big.Rat
	switch number := value.(type) {
	case int:
		expected.SetInt64(int64(number))
	case int64:
		expected.SetInt64(number)
	case float64:
		if expected.SetFloat64(number) == nil {
			return false
		}
		if math.Trunc(number) != number {
			actual, _ := decimal.Float64()
			return !math.IsInf(actual, 0) && math.Abs(actual-number) <= 1e-9*math.Max(1, math.Abs(number))
		}
	default:
		return false
	}
	return decimal.Cmp(&expected) == 0
}
