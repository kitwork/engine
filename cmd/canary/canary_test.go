package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCanaryConfigValidation(t *testing.T) {
	valid := canaryConfig{
		Duration:       time.Second,
		Interval:       time.Millisecond,
		RequestTimeout: time.Second,
		ReloadEvery:    time.Second,
		Workers:        1,
		ExpectedStatus: http.StatusOK,
	}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	invalid := valid
	invalid.Workers = 0
	if err := invalid.Validate(); err == nil {
		t.Fatal("zero workers were accepted")
	}
	invalid = valid
	invalid.URL = "https://example.com"
	invalid.BundlePath = "private.zip"
	if err := invalid.Validate(); err == nil {
		t.Fatal("external diagnostic bundle was accepted")
	}
	invalid = valid
	invalid.IncludeHeap = true
	if err := invalid.Validate(); err == nil {
		t.Fatal("heap profile without a diagnostic bundle was accepted")
	}
	invalid = valid
	invalid.URL = "example.com/health"
	if err := invalid.Validate(); err == nil {
		t.Fatal("relative external URL was accepted")
	}
}

func TestExternalCanary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte("canary-ok"))
	}))
	defer server.Close()
	config := canaryConfig{
		URL:            server.URL,
		Duration:       150 * time.Millisecond,
		Interval:       5 * time.Millisecond,
		RequestTimeout: time.Second,
		ReloadEvery:    time.Second,
		Workers:        2,
		ExpectedStatus: http.StatusOK,
		Contains:       "canary-ok",
		MaxErrorRate:   0,
		Quiet:          true,
	}
	ctx, cancel := context.WithTimeout(context.Background(), config.Duration)
	defer cancel()
	summary, err := executeCanary(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Mode != "external" || summary.Requests == 0 || summary.Failures != 0 {
		t.Fatalf("external summary = %+v", summary)
	}
	if summary.Requests != summary.Successes+summary.Failures {
		t.Fatalf("external request accounting = %+v", summary)
	}
}

func TestCancelledCanaryFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte("canary-ok"))
	}))
	defer server.Close()
	config := canaryConfig{
		URL:            server.URL,
		Duration:       time.Second,
		Interval:       time.Millisecond,
		RequestTimeout: time.Second,
		Workers:        1,
		ExpectedStatus: http.StatusOK,
		Contains:       "canary-ok",
		MaxErrorRate:   0,
		Quiet:          true,
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	summary, err := executeCanary(ctx, config)
	if err == nil || summary.Success || summary.Failure == "" {
		t.Fatalf("cancelled summary = %+v, err = %v", summary, err)
	}
}

func TestSyntheticCanarySmoke(t *testing.T) {
	config := canaryConfig{
		Duration:       1500 * time.Millisecond,
		Interval:       20 * time.Millisecond,
		RequestTimeout: time.Second,
		ReloadEvery:    250 * time.Millisecond,
		Workers:        2,
		ExpectedStatus: http.StatusOK,
		MaxErrorRate:   0,
		Quiet:          true,
	}
	ctx, cancel := context.WithTimeout(context.Background(), config.Duration)
	defer cancel()
	summary, err := executeCanary(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Mode != "synthetic" ||
		summary.Requests == 0 ||
		summary.Failures != 0 ||
		summary.ReloadWrites == 0 ||
		!summary.DrainHealthy ||
		summary.Diagnostics == nil {
		t.Fatalf("synthetic summary = %+v", summary)
	}
	if summary.Diagnostics.Health.LoadedApps != 0 ||
		summary.Diagnostics.Health.LoadedSites != 0 ||
		summary.Diagnostics.Health.ActiveGenerations != 0 ||
		summary.Diagnostics.Health.ActiveGenerationLeases != 0 ||
		summary.Diagnostics.Health.VMPool.Active != 0 ||
		summary.Diagnostics.Health.Requests.Inflight != 0 {
		t.Fatalf("synthetic diagnostics retained active work: %+v", summary.Diagnostics.Health)
	}
	if summary.Requests != summary.Successes+summary.Failures {
		t.Fatalf("synthetic request accounting = %+v", summary)
	}
}

func TestPublicCanaryTargetRedactsURLSecrets(t *testing.T) {
	target := &canaryTarget{
		mode: "external",
		url:  "https://user:secret@example.com/health?token=private#details",
	}
	if got := publicCanaryTarget(target); got != "https://example.com/health" {
		t.Fatalf("public target = %q", got)
	}
}

func TestSyntheticCanaryBodyVerification(t *testing.T) {
	if !verifySyntheticCanaryBody([]byte("generation-42:11,12,13,14:native-ok")) {
		t.Fatal("valid synthetic response was rejected")
	}
	for _, invalid := range []string{
		"generation-x:11,12,13,14:native-ok",
		"generation-1:wrong",
		"11,12,13,14:native-ok",
	} {
		if verifySyntheticCanaryBody([]byte(invalid)) {
			t.Fatalf("invalid synthetic response %q was accepted", invalid)
		}
	}
}
