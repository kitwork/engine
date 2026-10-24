package work

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	queuestore "github.com/kitwork/engine/utilities/queue"
)

// heldUpstream is a server whose n-th request (from 1) is answered only once release(n) is called; arrived
// hears each request's number as it comes in. A job's attempt blocked in http.get is an attempt blocked in
// a host call, which its VM's deadline cannot stop: the call runs on until it returns.
type heldUpstream struct {
	server  *httptest.Server
	arrived chan int
	gates   []chan struct{}
}

func holdUpstream(t *testing.T, requests int) *heldUpstream {
	t.Helper()
	reachLoopback(t)
	held := &heldUpstream{arrived: make(chan int, requests), gates: make([]chan struct{}, requests)}
	for index := range held.gates {
		held.gates[index] = make(chan struct{})
	}
	var count int32
	held.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(atomic.AddInt32(&count, 1))
		if n > requests {
			w.WriteHeader(http.StatusTeapot)
			return
		}
		held.arrived <- n
		<-held.gates[n-1]
		w.Write([]byte("ok"))
	}))
	t.Cleanup(func() {
		for _, gate := range held.gates {
			select {
			case <-gate:
			default:
				close(gate)
			}
		}
		held.server.Close()
	})
	return held
}

func (h *heldUpstream) release(n int) { close(h.gates[n-1]) }

// wait waits for the n-th request to come in.
func (h *heldUpstream) wait(t *testing.T, n int) {
	t.Helper()
	select {
	case got := <-h.arrived:
		if got != n {
			t.Fatalf("request %d came in, want %d", got, n)
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("request %d never came in", n)
	}
}

// heldJob is a job whose every attempt asks upstream once and logs what it got.
func heldJob(upstream, rules string) string {
	return `import * as queue from "kitwork/queue";
import * as http from "kitwork/http";
export const held = queue.job((context) => {
  const answer = http.get("` + upstream + `/x").timeout("30s").send();
  context.job.log("lần " + context.job.attempt + " xong " + answer.status);
})` + rules + `;
`
}

const heldRoute = `import * as router from "kitwork/router";
import { held } from "app/.shared/queue/held.work";
router.post((context) => context.json(held.send({})));
`

// An attempt past its .timeout() is settled at once, and its retry is claimed while the first still runs
// in a host call. Each is its own entry of the worker: both count against parallel while both run, and
// when the first ends it takes only itself out, so the retry keeps its lease (the heartbeat, reclaim's
// skip) and completes. Before, the entries were by run id: the first, ending, took the retry's out, and
// the retry lost its lease while healthy. CONTROL: the retry completing at attempt 2 is what the run
// records.
func TestQueueLingeringAttemptKeepsTheRetrysEntry(t *testing.T) {
	upstream := holdUpstream(t, 2)
	watch := watchQueue(t)
	q := newQueueApp(t, map[string]string{
		".shared/queue/held.work":    heldJob(upstream.server.URL, `.retry(3).timeout("300ms")`),
		"localhost/send/router.work": heldRoute,
	})
	worker := q.app.queueWorker()
	id := q.serve(t, http.MethodPost, "/send", url.Values{})["id"].(string)
	upstream.wait(t, 1)
	watch.until(t, id, "pending") // attempt 1 past its 300ms, settled; its VM is still in http.get
	upstream.wait(t, 2)           // attempt 2 runs too

	held := worker.held()
	if len(held) != 2 || held[0].id != id || held[1].id != id || held[0].token == held[1].token {
		t.Fatalf("while both attempts run the worker holds %d entries, want the two attempts of %s", len(held), id)
	}
	if room := worker.capacity(); room != queueDefaultParallel-2 {
		t.Fatalf("while both attempts run the room is %d, want parallel %d less 2", room, queueDefaultParallel)
	}
	second := q.run(t, id)
	upstream.release(1)
	if !waitFor(10*time.Second, func() bool { return len(worker.held()) < 2 }) {
		t.Fatal("attempt 1 never ended after its host call returned")
	}
	if held := worker.held(); len(held) != 1 || held[0].id != id {
		t.Fatalf("after attempt 1 ended the worker holds %d entries, want attempt 2 of %s still held", len(held), id)
	}
	upstream.release(2)
	watch.until(t, id, "completed")
	if run := q.run(t, id); second.Attempt != 2 || run.Attempt != 2 || !strings.Contains(run.Log, "lần 2 xong 200") || strings.Contains(run.Log, "lần 1 xong") {
		t.Fatalf("run = %+v, want completed by attempt 2 alone", run)
	}
}

// An attempt whose lease was taken from it while it ran (a heartbeat that did not reach the file, a pause
// longer than the lease) never reports success: the run went back and its retry holds the lease now, so
// when the first attempt returns, its success is refused (fencing, mục 6, 11) and the retry's outcome is
// the run's. CONTROL: the retry, which holds the lease, completes the run.
func TestQueueLateSuccessOfALostLeaseIsRefused(t *testing.T) {
	upstream := holdUpstream(t, 2)
	watch := watchQueue(t)
	q := newQueueApp(t, map[string]string{
		".shared/queue/held.work":    heldJob(upstream.server.URL, `.retry(1)`),
		"localhost/send/router.work": heldRoute,
	})
	id := q.serve(t, http.MethodPost, "/send", url.Values{})["id"].(string)
	upstream.wait(t, 1)
	// the lease of attempt 1 ran out with no heartbeat: the run goes back
	if err := (appRuns{tenant: q.app}).Write(func(db queuestore.SQL) error {
		_, err := db.Exec(`UPDATE kitwork_runs SET lease_until = '2000-01-01T00:00:00.000Z' WHERE id = $1`, id)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if count, _, err := q.app.queueStore().Reclaim(time.Now(), nil); err != nil || count != 1 {
		t.Fatalf("reclaim = %d (%v), want the run given back", count, err)
	}
	q.app.wakeQueueWorker()
	upstream.wait(t, 2) // attempt 2 holds the lease, attempt 1 still runs
	upstream.release(1)
	if seen := watch.until(t, id, ""); len(seen) != 1 {
		t.Fatalf("attempt 1 settled as %v, want refused", seen)
	}
	upstream.release(2)
	watch.until(t, id, "completed")
	if run := q.run(t, id); run.Attempt != 2 || run.Log != "lần 2 xong 200\n" {
		t.Fatalf("run = %+v, want completed by attempt 2, attempt 1's success refused", run)
	}
}

// An attempt still running when the app closes records its outcome: the app runtime drains the worker
// before it closes any resource, so the app's database and kitwork_runs are still there when the attempt
// ends. Before, the resources went first and the settle failed ("app runtime is closed"); after a restart
// the run sat in running until its lease ran out, then ran again (its email twice). CONTROL: the restart
// finds the run completed by the attempt of before, not run again. The app's resources close in a map's
// order, so the test closes four apps: before the drain, the worker went first in about one round of three.
func TestQueueAttemptRunningAtCloseRecordsItsOutcome(t *testing.T) {
	const rounds = 4
	upstream := holdUpstream(t, rounds)
	watch := watchQueue(t)
	for round := 1; round <= rounds; round++ {
		q := newQueueApp(t, map[string]string{
			".shared/queue/held.work":    heldJob(upstream.server.URL, `.retry(1)`),
			"localhost/send/router.work": heldRoute,
		})
		worker := q.app.queueWorker()
		id := q.serve(t, http.MethodPost, "/send", url.Values{})["id"].(string)
		upstream.wait(t, round)

		closed := make(chan struct{})
		go func() {
			q.close()
			close(closed)
		}()
		if !waitFor(10*time.Second, func() bool {
			worker.mu.Lock()
			defer worker.mu.Unlock()
			return worker.closed
		}) {
			t.Fatalf("round %d: the app's close never reached the worker", round)
		}
		select {
		case <-closed:
			t.Fatalf("round %d: the app closed while an attempt still ran", round)
		default:
		}
		upstream.release(round)
		watch.until(t, id, "completed")
		<-closed

		again := bootQueueApp(t, q.root)
		if run := again.run(t, id); run.Status != "completed" || run.Attempt != 1 || run.Log != "lần 1 xong 200\n" {
			t.Fatalf("round %d: after the restart run = %+v, want completed by its attempt of before", round, run)
		}
		again.close()
	}
}
