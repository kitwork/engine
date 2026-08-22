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
	active := pool.Stats()
	if active.Active != 1 || active.Created != 1 || active.Acquired != 1 || active.Released != 0 {
		t.Fatalf("active pool stats = %+v", active)
	}
	pool.Release(vm)
	if got := pool.Active(); got != 0 {
		t.Fatalf("expected no active VMs after release, got %d", got)
	}
	released := pool.Stats()
	if released.Active != 0 || released.Created != 1 || released.Acquired != 1 || released.Released != 1 {
		t.Fatalf("released pool stats = %+v", released)
	}
}
