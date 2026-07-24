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
	pool.Release(vm)
}
