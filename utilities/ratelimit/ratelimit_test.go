package ratelimit

import (
	"strconv"
	"testing"
	"time"
)

// A window that has ended is dropped as the limiter goes on: the windows of a thousand client addresses, each
// seen once, are not held for the life of the site. A window still open is kept, its count with it, so a key
// that used it up is still refused.
//
// CONTROL (validated by disabling the code, 25/10/2026): with Allow never sweeping, the limiter still held 1001
// windows two minutes later.
func TestEndedWindowsAreDropped(t *testing.T) {
	at := time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)
	limiter := New()
	limiter.now = func() time.Time { return at }
	for address := 0; address < 1000; address++ {
		if !limiter.Allow("ip|192.0.2."+strconv.Itoa(address)+"|folder:router|10/1m0s", 10, time.Minute) {
			t.Fatalf("the first request of address %d was refused", address)
		}
	}
	// an hour's window, used up: still open two minutes later
	limiter.Allow("global", 2, time.Hour)
	limiter.Allow("global", 2, time.Hour)

	at = at.Add(2 * time.Minute)
	if limiter.Allow("global", 2, time.Hour) {
		t.Errorf("a third request in an hour's window of two was let through: the open window was dropped")
	}
	limiter.mu.Lock()
	held := len(limiter.windows)
	limiter.mu.Unlock()
	if held != 1 {
		t.Errorf("the limiter holds %d windows two minutes on, want 1: the hour's window, the ended ones dropped", held)
	}
	// a key whose window was dropped starts a new one, as one that ended does
	if !limiter.Allow("ip|192.0.2.7|folder:router|10/1m0s", 10, time.Minute) {
		t.Errorf("an address whose window ended was refused")
	}
}
