package app_test

import (
	"testing"

	"github.com/kitwork/engine/app"
)

func TestAppPool(t *testing.T) {
	pool := app.NewPool()
	vm := pool.Acquire()
	if vm == nil {
		t.Fatal("Acquire VM failed")
	}
	if got := pool.Active(); got != 1 {
		t.Fatalf("expected one active VM, got %d", got)
	}
	pool.Release(vm)
	if got := pool.Active(); got != 0 {
		t.Fatalf("expected no active VMs after release, got %d", got)
	}
}
