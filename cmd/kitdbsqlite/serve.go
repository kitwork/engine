package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/kitwork/engine/internal/benchclock"
	kitdb "github.com/kitwork/engine/kitdb/sqlite"
)

//go:embed page.html
var page []byte

// indexState reports, per searchable table, whether its first index is built.
type indexState struct {
	mu     sync.Mutex
	tables map[string]string
}

func (state *indexState) set(table, status string) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.tables[table] = status
}

func (state *indexState) snapshot() map[string]string {
	state.mu.Lock()
	defer state.mu.Unlock()
	copied := make(map[string]string, len(state.tables))
	for table, status := range state.tables {
		copied[table] = status
	}
	return copied
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func writeError(response http.ResponseWriter, err error) {
	writeJSON(response, http.StatusBadRequest, map[string]string{"error": err.Error()})
}

// serve starts the page at once and builds each search index in the
// background, so the page can say how far the first build is.
func (t *tool) serve(ctx context.Context, listen string) error {
	state := &indexState{tables: map[string]string{}}
	for _, table := range t.search {
		state.set(table.Table, "đang dựng chỉ mục")
		go func(table string) {
			start := benchclock.Now()
			_, err := t.find(ctx, table, "a", "all", 1)
			if err != nil {
				state.set(table, "lỗi: "+err.Error())
				return
			}
			state.set(table, fmt.Sprintf("sẵn sàng (dựng %s)", since(start)))
			fmt.Printf("chỉ mục của %s sẵn sàng sau %s\n", table, since(start))
		}(table.Table)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = response.Write(page)
	})
	mux.HandleFunc("GET /api/status", func(response http.ResponseWriter, request *http.Request) {
		var size int64
		if info, err := os.Stat(t.database.Path()); err == nil {
			size = info.Size()
		}
		tables := []map[string]any{}
		for _, table := range t.search {
			columns := []string{}
			for _, column := range table.Columns {
				columns = append(columns, column.Name)
			}
			tables = append(tables, map[string]any{"table": table.Table, "columns": columns})
		}
		writeJSON(response, http.StatusOK, map[string]any{
			"path": t.database.Path(), "bytes": size, "revision": t.database.Revision(),
			"search": tables, "indexes": state.snapshot(),
		})
	})
	mux.HandleFunc("GET /api/search", func(response http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		limit, _ := strconv.Atoi(query.Get("limit"))
		result, err := t.find(request.Context(), query.Get("table"), query.Get("q"), query.Get("mode"), limit)
		if err != nil {
			writeError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, result)
	})
	mux.HandleFunc("POST /api/sql", func(response http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(io.LimitReader(request.Body, 1<<20))
		if err != nil {
			writeError(response, err)
			return
		}
		result, err := t.run(request.Context(), string(body))
		if err != nil {
			writeError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, result)
	})
	mux.HandleFunc("GET /api/history", func(response http.ResponseWriter, request *http.Request) {
		type change struct {
			Operation string `json:"operation"`
			Table     string `json:"table"`
			RowID     int64  `json:"rowid"`
			Statement string `json:"statement,omitempty"`
			By        string `json:"by"`
		}
		type commit struct {
			Revision uint64   `json:"revision"`
			At       string   `json:"at"`
			Changes  int      `json:"changes"`
			First    []change `json:"first"`
		}
		var commits []commit
		after := uint64(0)
		if revision := t.database.Revision(); revision > 20 {
			after = revision - 20
		}
		err := t.database.Changes(request.Context(), after, func(entry kitdb.Commit) error {
			item := commit{Revision: entry.Revision, At: entry.At.Local().Format("2006-01-02 15:04:05.000"), Changes: len(entry.Changes)}
			for position, value := range entry.Changes {
				if position == 3 {
					break
				}
				item.First = append(item.First, change{operationName(value.Operation), value.Table, value.RowID, value.Statement, value.By})
			}
			commits = append([]commit{item}, commits...)
			return nil
		})
		if err != nil {
			writeError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, commits)
	})

	listener, err := net.Listen("tcp", listen)
	if err != nil {
		return err
	}
	fmt.Printf("mở http://%s (chỉ trên máy này: trang chạy được mọi câu ghi)\n", listener.Addr())
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	return server.Serve(listener)
}
