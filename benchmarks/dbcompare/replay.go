package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strings"
	"time"
)

func replayQueries(source, output, names string, updated, profile bool, repetitions, cache int, timeout time.Duration) error {
	return replayQueriesMode(source, output, names, "kitdb-row", updated, profile, repetitions, cache, timeout)
}

func replayQueriesMode(source, output, names, mode string, updated, profile bool, repetitions, cache int, timeout time.Duration) (returnErr error) {
	if mode != "kitdb-row" && mode != "kitdb-batch" && mode != "kitdb-analytics" {
		return fmt.Errorf("invalid replay mode %q", mode)
	}
	if repetitions < 1 || repetitions > 100 || cache < 1 || cache > 1024 || timeout <= 0 {
		return fmt.Errorf("invalid replay limits")
	}
	encoded, err := os.ReadFile(filepath.Join(source, "workload.json"))
	if err != nil {
		return err
	}
	var w workload
	if err := json.Unmarshal(encoded, &w); err != nil {
		return err
	}
	if w.Rows < 128 || w.Rows > 1000000 || w.Warmups < 0 || w.Warmups > 100 {
		return fmt.Errorf("invalid replay fixture size")
	}
	requested := make(map[string]bool)
	for _, name := range strings.Split(names, ",") {
		requested[name] = true
	}
	var selected []queryCase
	for _, q := range cases(w.Rows, updated) {
		if requested[q.Name] {
			selected = append(selected, q)
			delete(requested, q.Name)
		}
	}
	if len(requested) != 0 {
		return fmt.Errorf("unknown replay queries: %v", requested)
	}
	w.Queries, w.Repetitions, w.CacheMiB = selected, repetitions, cache
	if output == "" {
		output = filepath.Join(".artifacts", "dbcompare-replay-"+time.Now().Format("20060102-150405"))
	}
	if err := os.MkdirAll(filepath.Dir(output), 0700); err != nil {
		return err
	}
	if err := os.Mkdir(output, 0700); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	previousProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previousProcs)
	dbPath := filepath.Join(source, "kitdb", "data.kitdb")
	if _, err := os.Stat(dbPath); err != nil {
		return err
	}
	db, err := openKit(dbPath, mode, cache, false)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, db.close()) }()
	if profile {
		cpu, err := os.OpenFile(filepath.Join(output, "cpu.pprof"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		if err := pprof.StartCPUProfile(cpu); err != nil {
			cpu.Close()
			return err
		}
		defer func() {
			pprof.StopCPUProfile()
			returnErr = errors.Join(returnErr, cpu.Close())
		}()
	}
	clock := "Go monotonic time"
	if runtime.GOOS == "windows" {
		clock = "QPC"
	}
	r := report{Engine: mode, Version: runtime.Version(), Rows: w.Rows,
		Settings: fmt.Sprintf("read-only SQL replay on an existing synthetic fixture; no DDL/DML/refresh; GOMAXPROCS=1; %d warmups; %d MiB page cache; %s/%s; %s; updated=%t; profiled=%t", w.Warmups, cache, runtime.GOOS, runtime.GOARCH, clock, updated, profile)}
	err = querySuite(ctx, db, w, &r)
	if err != nil {
		r.Error = err.Error()
	}
	if e := writeJSON(filepath.Join(output, "results.json"), r); e != nil {
		return errors.Join(err, e)
	}
	if profile {
		allocs, e := os.OpenFile(filepath.Join(output, "allocs.pprof"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return errors.Join(err, e)
		}
		e = pprof.Lookup("allocs").WriteTo(allocs, 0)
		if e = errors.Join(e, allocs.Close()); e != nil {
			return errors.Join(err, e)
		}
	}
	fmt.Fprintf(os.Stderr, "Replay results: %s\n", filepath.Join(output, "results.json"))
	return err
}
