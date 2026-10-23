package queue

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// A schedule's due time is one run, stored once (KITWORK_SCHEDULE.md mục 5, 9): Dispatch stores it and
// moves the schedule's LastAt; the same due time again, or one before LastAt, stores nothing (Held); a run
// of the schedule that has not ended passes the next due time over (Busy) and still moves LastAt. The
// state survives the file being opened again. CONTROL: the first due time and the one after the run ended
// are stored, each its own run, so a Held or a Busy is the rule's doing, not a Dispatch that stores
// nothing.
func TestScheduleDispatchStoresEachDueTimeOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "application.kitdb")
	file := openFile(t, path)
	store := New(file, "schedule")
	queue := New(file, "queue")

	slot := func(minute int) time.Time { return epoch.Add(time.Duration(minute) * time.Minute) }
	run := func(at time.Time) NewRun {
		return NewRun{Name: "nhip", Data: `{"at":"` + Stamp(at) + `"}`, Key: Stamp(at), MaxAttempts: 1}
	}
	if _, _, err := store.Dispatch(slot(1), run(slot(1)), true, slot(1)); !errors.Is(err, ErrNoSchedule) {
		t.Fatalf("a schedule never synced dispatched: %v", err)
	}
	if err := store.SyncSchedules([]ScheduleState{{Name: "nhip", Rule: `every("1m")`, DeclaredAt: epoch, LastAt: epoch}}, nil, epoch); err != nil {
		t.Fatal(err)
	}

	first, outcome, err := store.Dispatch(slot(1), run(slot(1)), true, slot(1))
	if err != nil || outcome != Dispatched || first.Key != Stamp(slot(1)) || first.Status != Pending {
		t.Fatalf("the first due time = %+v %q %v, want dispatched", first, outcome, err)
	}
	if _, outcome, _ := store.Dispatch(slot(1), run(slot(1)), true, slot(1)); outcome != Held {
		t.Fatalf("the same due time again = %q, want held", outcome)
	}
	if _, outcome, _ := store.Dispatch(slot(2), run(slot(2)), true, slot(2)); outcome != Busy {
		t.Fatalf("a due time while the first run is pending = %q, want busy", outcome)
	}
	// the due time passed over is handled: it is never stored, even once the run ended
	claimed, _, err := store.Claim(slot(2), time.Minute, 1, nil)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim = %v %v", claimed, err)
	}
	if won, err := store.Complete(claimed[0].ID, claimed[0].Token, "", slot(2)); !won || err != nil {
		t.Fatalf("complete = %v %v", won, err)
	}
	if _, outcome, _ := store.Dispatch(slot(2), run(slot(2)), true, slot(2)); outcome != Held {
		t.Fatalf("the due time passed over, again = %q, want held", outcome)
	}
	third, outcome, err := store.Dispatch(slot(3), run(slot(3)), true, slot(3))
	if err != nil || outcome != Dispatched || third.ID == first.ID {
		t.Fatalf("the due time after the run ended = %+v %q %v, want a run of its own", third, outcome, err)
	}
	// a once() is never passed over: skipBusy false stores it beside a run not ended
	if err := store.SyncSchedules([]ScheduleState{{Name: "nhip", Rule: `every("1m")`, DeclaredAt: epoch, LastAt: slot(3)}}, nil, epoch); err != nil {
		t.Fatal(err)
	}
	if _, outcome, _ := store.Dispatch(slot(4), run(slot(4)), false, slot(4)); outcome != Dispatched {
		t.Fatalf("not skipping busy = %q, want dispatched", outcome)
	}
	// the runs are the schedule's kind: the queue of the same file does not see them
	if runs, _ := queue.Runs(Filter{}, 10, 0); len(runs) != 0 {
		t.Fatalf("the queue reads %d runs of the schedule", len(runs))
	}
	file.close()

	reopened := openFile(t, path)
	t.Cleanup(reopened.close)
	again := New(reopened, "schedule")
	states, err := again.ScheduleStates()
	if err != nil {
		t.Fatal(err)
	}
	state := states["nhip"]
	if !state.LastAt.Equal(slot(4)) || state.Rule != `every("1m")` || !state.DeclaredAt.Equal(epoch) || !state.OnceAt.IsZero() {
		t.Fatalf("the state read again = %+v", state)
	}
	if runs, _ := again.Runs(Filter{Name: "nhip"}, 10, 0); len(runs) != 3 {
		t.Fatalf("nhip has %d runs, want 3: the first, the third, the fourth", len(runs))
	}
	if err := again.SyncSchedules(nil, []string{"nhip"}, epoch); err != nil {
		t.Fatal(err)
	}
	if states, _ := again.ScheduleStates(); len(states) != 0 {
		t.Fatalf("a schedule removed keeps its state: %+v", states)
	}
}
