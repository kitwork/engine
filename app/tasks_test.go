package app_test

import (
	"errors"
	goruntime "runtime"
	"testing"

	"github.com/kitwork/engine/app"
)

// StartWithin accepts count tasks at once or none: past the limit it refuses now (no line to wait in), and
// once tasks end there is room again; a closing app accepts none. Running counts what has not ended.
//
// CONTROL: with StartWithin not checking the limit, the third task was accepted: "past the limit: <nil>";
// with done not counting down, the room never came back: "after two ended: app task limit reached".
func TestTaskGroupStartWithinRefusesPastTheLimit(t *testing.T) {
	runtime := app.NewRuntime("tasks")
	tasks := runtime.Tasks()
	_, done, err := tasks.StartWithin(2, 3)
	if err != nil || tasks.Running() != 2 {
		t.Fatalf("two of three: %v, running %d", err, tasks.Running())
	}
	if _, _, err := tasks.StartWithin(2, 3); !errors.Is(err, app.ErrTaskLimit) || tasks.Running() != 2 {
		t.Fatalf("past the limit: %v, running %d; want ErrTaskLimit and nothing accepted", err, tasks.Running())
	}
	done()
	done()
	if _, more, err := tasks.StartWithin(3, 3); err != nil || tasks.Running() != 3 {
		t.Fatalf("after two ended: %v, running %d", err, tasks.Running())
	} else {
		more()
		more()
		more()
	}
	_, many, err := tasks.StartWithin(1000, 0)
	if err != nil {
		t.Fatalf("no limit: %v", err)
	}
	closed := make(chan struct{})
	go func() {
		runtime.Close()
		close(closed)
	}()
	for !tasks.Closing() {
		goruntime.Gosched()
	}
	if _, _, err := tasks.StartWithin(1, 0); !errors.Is(err, app.ErrTasksClosing) {
		t.Fatalf("a closing app accepted a task: %v", err)
	}
	for range 1000 {
		many()
	}
	<-closed
	if tasks.Running() != 0 {
		t.Fatalf("%d tasks counted after the app closed", tasks.Running())
	}
}
