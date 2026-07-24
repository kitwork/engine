package app_test

import (
	"testing"

	"github.com/kitwork/engine/app"
)

func TestAppPackage(t *testing.T) {
	scope := app.NewScope("test_app", "example.com", "/tmp/app", nil)
	if scope.AppID() != "test_app" {
		t.Fatalf("Expected appID test_app, got %s", scope.AppID())
	}

	pool := app.NewPool()
	vm := pool.Acquire()
	if vm == nil {
		t.Fatal("Acquire VM failed")
	}
	pool.Release(vm)

	lc := app.NewLifecycle()
	if lc.Status() != "initialized" {
		t.Fatalf("Expected status initialized, got %s", lc.Status())
	}
	lc.Boot()
	if lc.Status() != "running" {
		t.Fatalf("Expected status running, got %s", lc.Status())
	}
}
