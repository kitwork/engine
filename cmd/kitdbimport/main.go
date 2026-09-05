package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/kitwork/engine/kitdb/buildinfo"
	"github.com/kitwork/engine/kitdb/pgimport"
)

func main() {
	version := flag.Bool("version", false, "print binary build information and exit")
	var (
		action      = flag.String("action", "import", "action: import, status, verify, cancel, or forget")
		urlValue    = flag.String("url", "", "KitDB PostgreSQL URL (sslmode=disable on the local profile)")
		fileValue   = flag.String("file", "", "seekable CSV or JSONL source file")
		formatValue = flag.String("format", "", "source format: csv or jsonl (inferred from extension when empty)")
		tableValue  = flag.String("table", "", "target KitDB struct/table")
		columns     = flag.String("columns", "", "comma-separated target columns; optional with -header CSV")
		importID    = flag.String("id", "", "stable import ID; generated deterministically when empty")
		sourceID    = flag.String("source-id", "", "stable logical source ID; generated from the absolute path when empty")
		header      = flag.Bool("header", false, "read and validate/infer the first CSV row as column names")
		delimiter   = flag.String("delimiter", ",", "one-rune CSV delimiter")
		nullMarker  = flag.String("null", `\N`, "CSV field spelling treated as SQL NULL (empty is allowed)")
		chunkRows   = flag.Int("chunk-rows", pgimport.DefaultChunkRows, "maximum source records in one atomic COPY")
		chunkBytes  = flag.Int64("chunk-bytes", pgimport.DefaultChunkBytes, "maximum source bytes in one atomic COPY")
		maxChunks   = flag.Int("max-chunks", 0, "stop successfully after this many chunks; zero means through EOF")
		timeout     = flag.Duration("timeout", 0, "whole-import timeout; zero uses no additional deadline")
		confirm     = flag.String("confirm-forget", "", "repeat the import ID to authorize forgetting a terminal checkpoint")
	)
	flag.Parse()
	if *version {
		printJSON(buildinfo.Current())
		return
	}

	delimiterRunes := []rune(*delimiter)
	if len(delimiterRunes) != 1 {
		fatalf("-delimiter must contain exactly one rune")
	}
	columnList := make([]string, 0)
	if strings.TrimSpace(*columns) != "" {
		for _, column := range strings.Split(*columns, ",") {
			columnList = append(columnList, strings.TrimSpace(column))
		}
	}

	ctx := context.Background()
	var cancel context.CancelFunc
	if *timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, *timeout)
		defer cancel()
	}
	config := pgimport.Config{
		URL: *urlValue, File: *fileValue, Format: *formatValue, Table: *tableValue,
		Columns: columnList, ImportID: *importID, SourceID: *sourceID,
		Header: *header, Delimiter: delimiterRunes[0], Null: *nullMarker, NullSet: true,
		ChunkRows: *chunkRows, ChunkBytes: *chunkBytes, MaxChunks: *maxChunks,
		Progress: func(progress pgimport.Progress) {
			fmt.Fprintf(
				os.Stderr,
				"kitdbimport: chunk=%d chunk_rows=%d total_rows=%d offset=%d complete=%t transaction=%d\n",
				progress.Chunk, progress.ChunkRows, progress.Rows, progress.Offset,
				progress.Complete, progress.Transaction,
			)
		},
	}

	switch strings.ToLower(strings.TrimSpace(*action)) {
	case "import":
		report, err := pgimport.Run(ctx, config)
		if err != nil {
			fatalf("%v", err)
		}
		printJSON(report)
	case "verify":
		verification, err := pgimport.Verify(ctx, config)
		if err != nil {
			fatalf("%v", err)
		}
		printJSON(verification)
	case "status", "cancel", "forget":
		if strings.TrimSpace(*urlValue) == "" {
			fatalf("-url is required for %s", *action)
		}
		if strings.TrimSpace(*importID) == "" {
			fatalf("-id is required for %s", *action)
		}
		database, err := sql.Open("postgres", *urlValue)
		if err != nil {
			fatalf("open database: %v", err)
		}
		defer database.Close()
		database.SetMaxOpenConns(2)
		database.SetMaxIdleConns(1)
		switch strings.ToLower(strings.TrimSpace(*action)) {
		case "status":
			status, found, err := pgimport.QueryStatus(ctx, database, *importID)
			if err != nil {
				fatalf("%v", err)
			}
			printJSON(struct {
				Found  bool            `json:"found"`
				Status pgimport.Status `json:"status"`
			}{Found: found, Status: status})
		case "cancel":
			status, err := pgimport.Cancel(ctx, database, *importID)
			if err != nil {
				fatalf("%v", err)
			}
			printJSON(status)
		case "forget":
			if *confirm != *importID {
				fatalf("-confirm-forget must exactly match -id")
			}
			forgotten, err := pgimport.Forget(ctx, database, *importID)
			if err != nil {
				fatalf("%v", err)
			}
			printJSON(struct {
				ImportID  string `json:"import_id"`
				Forgotten bool   `json:"forgotten"`
			}{ImportID: *importID, Forgotten: forgotten})
		}
	default:
		fatalf("-action must be import, status, verify, cancel, or forget")
	}
}

func printJSON(input any) {
	encoded, err := json.MarshalIndent(input, "", "  ")
	if err != nil {
		fatalf("encode report: %v", err)
	}
	fmt.Println(string(encoded))
}

func fatalf(format string, values ...any) {
	fmt.Fprintf(os.Stderr, "kitdbimport: "+format+"\n", values...)
	os.Exit(1)
}
