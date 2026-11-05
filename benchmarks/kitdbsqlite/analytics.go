package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kitwork/engine/kitdb/analytics"
	"github.com/kitwork/engine/kitdb/columnar"
)

// analyticsColumns are the columns of shopping the analytic queries of RESULTS_2026-10-27.md read: the integers
// they filter, group and sum, the merchant they group by.
var analyticsColumns = []string{"merchant", "id", "shop", "category", "price", "pricing", "discount", "sold", "stock",
	"review", "official"}

// cmdAnalytics builds KCOL on SQLite (kitdb/analytics, docs/KITWORK_KITDB.md mục 6 of the Kitwork repository) for
// shopping's analytic columns, from one read transaction of the file opened read-only, into directory, and reads
// every block of it back. The file is never written. Evidence for mục 6 and 11, bước 2 (05/11/2026).
func cmdAnalytics(path, directory string) {
	ctx := context.Background()
	if _, err := os.Stat(filepath.Join(directory, "shopping.json")); err == nil {
		panic(directory + " holds a copy already")
	}
	db, err := sql.Open("sqlite", "file:"+toSlash(path)+"?mode=ro&_pragma=busy_timeout(10000)&_pragma=cache_size(-262144)")
	must(err)
	defer db.Close()
	db.SetMaxOpenConns(1)
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	must(err)
	defer tx.Rollback()

	start := time.Now()
	manifest, err := analytics.Build(ctx, tx, directory, "shopping", analyticsColumns)
	must(err)
	fmt.Printf("built %d rows of %d columns at revision %d: %s, %s\n", manifest.Rows, len(manifest.Columns),
		manifest.Revision, duration(time.Since(start)), gigabytes(manifest.Bytes))

	copy, err := analytics.Open(directory, "shopping")
	must(err)
	defer copy.Close()
	for _, columns := range [][]string{{"price"}, {"merchant", "price"}, analyticsColumns} {
		start = time.Now()
		var rows, sum int64
		report, err := copy.Scan(ctx, columns, nil, func(batch *columnar.Batch) error {
			rows += int64(batch.Rows)
			last := batch.Columns[len(batch.Columns)-1]
			if last.Field.Kind == columnar.Integer {
				for row := range batch.Rows {
					sum += last.Integers[row]
				}
			}
			return nil
		})
		must(err)
		fmt.Printf("scanned %v: %d rows, %s read, %s\n", columns, rows,
			gigabytes(int64(report.ColumnPayloadBytesRead+report.BlockHeaderBytesRead)), duration(time.Since(start)))
		_ = sum
	}
}
