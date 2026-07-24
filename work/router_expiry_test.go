package work

import (
	"testing"
	"time"

	"github.com/kitwork/engine/value"
)

// Boundary + rolling expiry specs resolve to the right next moment, from a fixed `now`.
func TestExpiryResolvers(t *testing.T) {
	now := time.Date(2026, 7, 8, 14, 30, 0, 0, time.UTC) // Wed 2026-07-08 14:30

	at := func(spec string) time.Time {
		r := parseExpiry(value.New(spec))
		if r == nil {
			t.Fatalf("%q → nil resolver", spec)
		}
		return now.Add(r(now))
	}

	// rolling duration
	if got := at("5m"); !got.Equal(now.Add(5 * time.Minute)) {
		t.Errorf(`"5m" → %v`, got)
	}
	// daily HH:MM: 03:00 already passed today → tomorrow 03:00
	if d := at("daily 03:00"); d.Hour() != 3 || d.Minute() != 0 || d.Day() != 9 {
		t.Errorf(`"daily 03:00" → %v (want 07-09 03:00)`, d)
	}
	// daily HH:MM still ahead today → today
	if d := at("daily 20:00"); d.Hour() != 20 || d.Day() != 8 {
		t.Errorf(`"daily 20:00" → %v (want 07-08 20:00)`, d)
	}
	// bare HH:MM behaves like daily
	if b := at("03:00"); b.Hour() != 3 || !b.After(now) {
		t.Errorf(`"03:00" → %v`, b)
	}
	// nextday is ALWAYS tomorrow
	if nd := at("nextday 03:00"); nd.Hour() != 3 || nd.Day() != 9 {
		t.Errorf(`"nextday 03:00" → %v`, nd)
	}
	// hourly → next :00 (within the hour)
	if h := at("hourly"); h.Minute() != 0 || !h.After(now) || h.Sub(now) > time.Hour {
		t.Errorf(`"hourly" → %v`, h)
	}
	// weekly → next Monday 00:00
	if w := at("weekly"); w.Weekday() != time.Monday || w.Hour() != 0 || !w.After(now) {
		t.Errorf(`"weekly" → %v`, w)
	}
	// weekly with weekday + time
	if wf := at("weekly fri 09:00"); wf.Weekday() != time.Friday || wf.Hour() != 9 {
		t.Errorf(`"weekly fri 09:00" → %v`, wf)
	}
	// monthly → 1st of next month
	if m := at("monthly"); m.Day() != 1 || m.Month() != time.August {
		t.Errorf(`"monthly" → %v`, m)
	}
	// unparseable → no resolver (fail-safe: no caching)
	if parseExpiry(value.New("garbage-spec")) != nil {
		t.Error(`"garbage-spec" should yield a nil resolver`)
	}
}
