// canary runs a bounded long-lived probe against either a synthetic Kitwork
// runtime or an existing HTTP deployment.
//
//	go run ./cmd/canary --duration=24h --bundle=canary.zip --heap
//	go run ./cmd/canary --url=https://example.com/health --duration=24h
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"
)

func main() {
	config := canaryConfig{}
	flag.StringVar(&config.URL, "url", "", "external URL; empty starts the synthetic Kitwork canary")
	flag.DurationVar(&config.Duration, "duration", time.Minute, "total canary duration")
	flag.DurationVar(&config.Interval, "interval", 100*time.Millisecond, "delay between requests per worker")
	flag.DurationVar(&config.RequestTimeout, "request-timeout", 5*time.Second, "per-request timeout")
	flag.DurationVar(&config.ReloadEvery, "reload-every", 10*time.Second, "synthetic source rewrite interval")
	flag.DurationVar(&config.ReportEvery, "report-every", 10*time.Second, "progress report interval; 0 disables it")
	flag.IntVar(&config.Workers, "workers", 4, "concurrent request workers")
	flag.IntVar(&config.ExpectedStatus, "status", 200, "expected HTTP status")
	flag.StringVar(&config.Contains, "contains", "", "optional response substring for external mode")
	flag.Float64Var(&config.MaxErrorRate, "max-error-rate", 0, "maximum accepted failure ratio from 0 to 1")
	flag.StringVar(&config.BundlePath, "bundle", "", "optional synthetic diagnostic ZIP path")
	flag.BoolVar(&config.IncludeHeap, "heap", false, "include heap.pprof in the diagnostic bundle")
	flag.StringVar(&config.ReportPath, "json", "", "optional final JSON report path")
	flag.BoolVar(&config.Quiet, "quiet", false, "suppress periodic human-readable reports")
	flag.Parse()

	if err := config.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, "canary:", err)
		os.Exit(2)
	}

	signalContext, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stopSignals()
	ctx, cancel := context.WithTimeout(signalContext, config.Duration)
	defer cancel()

	summary, err := executeCanary(ctx, config)
	if reportErr := writeCanaryReport(config.ReportPath, summary); reportErr != nil && err == nil {
		err = reportErr
	}
	printCanarySummary(summary)
	if err != nil {
		fmt.Fprintln(os.Stderr, "canary:", err)
		os.Exit(1)
	}
}
