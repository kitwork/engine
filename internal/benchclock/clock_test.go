package benchclock

import (
	"testing"
	"time"
)

func TestElapsed(t *testing.T) {
	start := Now()
	if elapsed := Since(start); elapsed < 0 {
		t.Fatalf("clock moved backwards: %v", elapsed)
	}
	time.Sleep(5 * time.Millisecond)
	if elapsed := Since(start); elapsed <= 0 {
		t.Fatalf("clock did not advance: %v", elapsed)
	}
}
