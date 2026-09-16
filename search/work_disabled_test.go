//go:build !searchwork

package search

import (
	"context"
	"reflect"
	"testing"
)

func TestPostingWorkDisabledIsEmpty(t *testing.T) {
	if size := reflect.TypeOf(postingWork{}).Size(); size != 0 {
		t.Fatalf("disabled work retains %d bytes", size)
	}
	ctx := context.Background()
	if frequencyWorkContext(ctx) != ctx {
		t.Fatal("disabled work changed context")
	}
	if allocated := testing.AllocsPerRun(100, func() {
		work := postingWorkFromContext(ctx)
		work.add(workAdvanceCalls, 1)
		work.varint(1, false)
	}); allocated != 0 {
		t.Fatalf("disabled work allocated %f", allocated)
	}
}
