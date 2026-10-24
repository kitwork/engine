package work

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	queuestore "github.com/kitwork/engine/utilities/queue"
	"github.com/kitwork/engine/value"
)

// ── Queue worker: claim, run, settle (KITWORK_QUEUE.md mục 6, 11) ───────────────────────────────────
//
// One poller per app claims what is due, as much as the queue's parallel leaves room for, each claim a
// lease with a token; the attempt runs in a pooled VM under its .timeout(); the outcome is written by the
// token's holder only. An attempt past its time is failed (TIMEOUT) the moment its time is up, before it
// has stopped: its token stops holding the run then, so a late success of it changes nothing (fencing).
// A heartbeat moves the leases of the attempts running here forward and stops an attempt whose run was
// cancelled. A lease that ran out with no attempt here behind it (a process that stopped) gives the run
// back. The poller never runs JavaScript itself.
//
// The schedules of the app run on the same worker, a second one of kind schedule (KITWORK_SCHEDULE.md
// mục 9: "chung bảng kitwork_runs và chung cơ chế lease, fencing, thử lại với queue"): its poller stores
// the run of each due time first (schedule_runtime.go), then claims and runs as the queue's does.

// queueLeaseTTL is how long a claim holds its run without a heartbeat. A var so tests can shrink it.
var queueLeaseTTL = 30 * time.Second

// queuePollInterval is how often the worker looks at its clock for a run due or a lease run out; a send
// wakes it at once.
var queuePollInterval = 250 * time.Millisecond

// queueBackoff is how long a run waits after its attempt-th attempt failed: 1s, 2s, 4s… up to 30 minutes,
// each give or take a fifth (jitter), so runs that failed together do not come back together. A var so
// tests can shorten it.
var queueBackoff = func(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 12 {
		attempt = 12
	}
	wait := time.Second << uint(attempt-1)
	if wait > 30*time.Minute {
		wait = 30 * time.Minute
	}
	return time.Duration(float64(wait) * (0.8 + 0.4*rand.Float64()))
}

// queueLogLimit is the most a run's log keeps; past it the oldest lines go.
const queueLogLimit = 64 << 10

// QueueSettled, when set, hears each attempt the worker settles: the app, the run, and the status the run
// took ("" when the attempt no longer held it). A test's way to wait for the worker without sleeping.
// ScheduleSettled is the same for the runs of the app's schedules.
var (
	QueueSettled    func(identity, id, status string)
	ScheduleSettled func(identity, id, status string)
)

// StartQueueWorker writes the app's job definitions into kitwork_definitions and starts the worker when
// there is a job to run. Called again (hot reload), it stops the running worker first.
func (t *Tenant) StartQueueWorker() { t.startWorker(t.queueWorker()) }

// startWorker starts worker, the queue's or the schedules', stopping it first when it runs.
func (t *Tenant) startWorker(worker *queueRuntime) {
	if worker == nil {
		return
	}
	worker.lifecycle.Lock()
	defer worker.lifecycle.Unlock()

	worker.mu.Lock()
	stopQueueWorkerNoLock(worker)
	worker.mu.Unlock()
	worker.wg.Wait()

	worker.mu.Lock()
	defer worker.mu.Unlock()
	if worker.closed {
		return
	}
	if err := t.startQueueRuntime(worker); err != nil {
		fmt.Printf("%s worker disabled: %v\n", worker.label(), err)
	}
}

// StopQueueWorker halts claiming and waits for the attempts running here to finish.
func (t *Tenant) StopQueueWorker() { t.stopWorker(t.queueWorker()) }

// stopWorker halts worker's claiming and waits for its attempts.
func (t *Tenant) stopWorker(worker *queueRuntime) {
	if worker == nil {
		return
	}
	worker.lifecycle.Lock()
	defer worker.lifecycle.Unlock()

	worker.mu.Lock()
	stopQueueWorkerNoLock(worker)
	worker.mu.Unlock()
	worker.wg.Wait()
}

// stopQueueWorkerNoLock cancels the poller and the heartbeat. The caller holds worker.mu; the wait for the
// attempts happens OUTSIDE the lock, because an ending attempt takes that lock to leave.
func stopQueueWorkerNoLock(worker *queueRuntime) {
	worker.running = false
	for _, cancel := range worker.cancels {
		close(cancel)
	}
	worker.cancels = nil
}

// startQueueRuntime syncs the definitions and starts the poller and the heartbeat. Runs under worker.mu.
func (t *Tenant) startQueueRuntime(worker *queueRuntime) error {
	definitions := make([]queuestore.Definition, 0, len(worker.handlers))
	names := make([]string, 0, len(worker.handlers))
	for _, handler := range worker.handlers {
		definitions = append(definitions, queuestore.Definition{
			Name: handler.Name, Source: handler.Source, Retry: handler.Retry, Timeout: handler.TimeoutText,
			TimeoutMS: int(handler.Timeout / time.Millisecond), ContentHash: handler.ContentHash,
		})
		names = append(names, handler.Name)
	}
	place := worker.runs.Load()
	store := t.runsStoreAt(worker.kind, place)
	kept, err := store.Sync(definitions, worker.unloaded, time.Now())
	if err != nil {
		return fmt.Errorf("kitwork_definitions: %w", err)
	}
	withheld := make(map[string]bool, len(kept)+len(worker.unloaded.Names))
	for _, name := range kept {
		withheld[name] = true
	}
	for name := range worker.unloaded.Names {
		withheld[name] = true
	}
	worker.withheld = withheld
	if len(kept) > 0 {
		fmt.Printf("%s %s %s chưa nạp được (file lỗi): các lần chạy của nó chờ, không thử, tới khi file nạp lại\n", worker.label(), worker.noun(), strings.Join(kept, ", "))
	}
	if worker.book != nil {
		if err := t.syncSchedules(worker, store); err != nil {
			return fmt.Errorf("kitwork_schedules: %w", err)
		}
	}
	if len(worker.handlers) == 0 {
		return nil
	}
	worker.running = true
	// the first look and the first reclaim are now: what the file holds is not known yet
	worker.next, worker.reclaimAt = time.Time{}, time.Time{}
	identity := t.appID()

	poll := make(chan struct{})
	worker.cancels = append(worker.cancels, poll)
	// the worker's writes wait for another writer of a file of the catalogue while it runs; stopping it (a
	// load, the app closing) closes poll and ends the wait
	running := queuestore.New(t.runsDatabase(worker.kind, place, poll), worker.kind)
	worker.wg.Add(1)
	go func() {
		defer worker.wg.Done()
		t.queuePoller(worker, poll, running)
	}()

	beat := make(chan struct{})
	worker.cancels = append(worker.cancels, beat)
	worker.wg.Add(1)
	go func() {
		defer worker.wg.Done()
		t.queueHeartbeat(worker, beat, running)
	}()

	if worker.book != nil {
		fmt.Printf("[Schedule] đồng hồ chạy (kitwork_runs của %s) — %d lịch cho %q: %s\n",
			placeName(place), len(names), identity, strings.Join(names, ", "))
		return nil
	}
	fmt.Printf("[Queue] worker up (kitwork_runs của %s) — %d job cho %q: %s\n",
		placeName(place), len(names), identity, strings.Join(names, ", "))
	return nil
}

// queuePoller looks at its clock every tick (queuePollInterval) and at every wake, and reads kitwork_runs only
// when a run it knows of is due (next) or a lease it knows of ran out (reclaimAt).
func (t *Tenant) queuePoller(worker *queueRuntime, cancel chan struct{}, store *queuestore.Store) {
	ticker := time.NewTicker(queuePollInterval)
	defer ticker.Stop()
	tick := 0
	for {
		if t.appRuntime.Closed() {
			return // the app is closing: its worker closes next, and nothing is claimed in between
		}
		now := time.Now()
		if worker.book != nil {
			// the run of each due time is stored before anything claims it
			t.dispatchSchedules(worker, store)
		}
		if worker.takeReclaim(now) {
			t.queueReclaim(worker, store, now)
		}
		t.claimAndRunQueue(worker, store, now)
		if tick%queuePruneTicks == queuePruneTicks-1 {
			worker.mu.Lock()
			keep := worker.keep
			worker.mu.Unlock()
			if _, err := store.Prune(now.Add(-keep)); err != nil {
				fmt.Printf("%s keep: %v\n", worker.label(), err)
			}
		}
		tick++
		worker.polls.Add(1)
		select {
		case <-cancel:
			return
		case <-ticker.C:
		case <-worker.wake:
		}
	}
}

// queuePruneTicks is how many ticks of the poller apart the ended runs past keep are deleted: about a
// minute at the default interval. A var so tests can prune at once.
var queuePruneTicks = 240

// queueReclaim gives back the runs whose lease ran out with no attempt here behind them, and notes when the
// next lease no attempt here holds runs out.
func (t *Tenant) queueReclaim(worker *queueRuntime, store *queuestore.Store, now time.Time) {
	skip := map[string]bool{}
	for _, attempt := range worker.held() {
		skip[attempt.id] = true
	}
	count, next, err := store.Reclaim(now, skip)
	switch {
	case err != nil:
		fmt.Printf("%s reclaim: %v\n", worker.label(), err)
		worker.noteReclaim(now.Add(queueLeaseTTL))
		return
	case !next.IsZero():
		worker.noteReclaim(next)
	}
	if count > 0 {
		worker.noteDue(now)
	}
}

// queueHeartbeat moves the leases of the attempts running here forward, and stops an attempt whose run
// was cancelled from a process that does not run it.
func (t *Tenant) queueHeartbeat(worker *queueRuntime, cancel chan struct{}, store *queuestore.Store) {
	interval := queueLeaseTTL / 3
	if interval < 100*time.Millisecond {
		interval = 100 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-cancel:
			return
		case <-ticker.C:
			for _, attempt := range worker.held() {
				status, holds, err := store.Extend(attempt.id, attempt.token, time.Now().Add(queueLeaseTTL))
				if err == nil && holds && status == queuestore.Cancelling {
					attempt.stop(errQueueCancelled)
				}
			}
		}
	}
}

// claimAndRunQueue claims only what the queue has room for, when a run may be due, then starts each claim.
func (t *Tenant) claimAndRunQueue(worker *queueRuntime, store *queuestore.Store, now time.Time) {
	room := worker.capacity()
	if room < 1 || !worker.takeDue(now) {
		return
	}
	claims, next, err := store.Claim(now, queueLeaseTTL, room, worker.withheldJobs())
	if err != nil {
		fmt.Printf("%s claim: %v\n", worker.label(), err)
		worker.noteDue(now.Add(queuePollInterval))
		return
	}
	if next.IsZero() {
		next = queueNever
	}
	worker.noteDue(next)
	for _, claim := range claims {
		handler := worker.handlerFor(claim.Name)
		ctx, stop := context.WithCancelCause(context.Background())
		attempt := &queueAttempt{id: claim.ID, token: claim.Token, stop: stop}
		if !worker.startAttempt(attempt) {
			// closing: the lease runs out and the run comes back to whoever runs next
			stop(nil)
			continue
		}
		go func(claim queuestore.Claim, handler *QueueHandler) {
			defer worker.endAttempt(attempt)
			defer stop(nil)
			t.runQueueAttempt(ctx, worker, store, claim, handler)
		}(claim, handler)
	}
}

// runQueueAttempt runs one attempt and settles it.
func (t *Tenant) runQueueAttempt(ctx context.Context, worker *queueRuntime, store *queuestore.Store, claim queuestore.Claim, handler *QueueHandler) {
	log := &queueLog{}
	log.text.WriteString(claim.Log)
	// fail records a failed attempt; a run with attempts left is pending again until retryAt, which the
	// worker notes so it looks then. KitDB keeps only valid UTF-8: a message that is not (the text of a
	// page in another encoding) would make the settle fail, and the run come back after its lease.
	fail := func(code, message string, retryAt, now time.Time) {
		code, message = strings.ToValidUTF8(code, string(utf8.RuneError)), strings.ToValidUTF8(message, string(utf8.RuneError))
		status, err := store.Fail(claim.ID, claim.Token, code, message, log.String(), retryAt, now)
		if err == nil && status == queuestore.Pending {
			worker.noteDue(retryAt)
		}
		t.settleQueue(worker, claim.ID, status, err)
	}
	if handler == nil {
		// no file declares the job (renamed or removed since the run was sent): no attempt mends that, so
		// the run ends failed, saying so, rather than coming back for ever. A job whose file did not load
		// is withheld, never claimed (store.Claim), so it does not end here.
		message := fmt.Sprintf("không có job %s trong .shared/queue/ của app: job đã đổi tên hay đã xoá sau khi gửi", claim.Name)
		if worker.book != nil {
			message = fmt.Sprintf("không có lịch %s trong .schedule/ của app: lịch đã đổi tên hay đã xoá sau khi tới giờ", claim.Name)
		}
		log.attemptFailed(claim.Attempt, value.NotFoundCode, message)
		fail(value.NotFoundCode, message, time.Time{}, time.Now())
		return
	}
	if timeout := time.Duration(claim.TimeoutMS) * time.Millisecond; timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	done := make(chan error, 1)
	go func() {
		_, err := t.ExecuteContext(ctx, handler.Bytecode.Program, handler.Callback, []value.Value{t.attemptContext(worker, claim, log)})
		done <- err
	}()
	var runErr error
	finished := false
	select {
	case runErr = <-done:
		finished = true
	case <-ctx.Done():
	}
	if !finished {
		// the attempt is past its time or cancelled: it is settled now, and stops at its VM's next
		// checkpoint; whatever it does after, its lease no longer holds the run
		defer func() { <-done }()
	}

	now := time.Now()
	switch {
	case finished && runErr == nil:
		won, err := store.Complete(claim.ID, claim.Token, log.String(), now)
		status := queuestore.Completed
		if err == nil && !won {
			fmt.Printf("%s %s %s: lần thử %d chạy xong sau khi mất lease; kết quả bỏ\n", worker.label(), claim.Name, claim.ID, claim.Attempt)
			status = ""
		}
		t.settleQueue(worker, claim.ID, status, err)
	case errors.Is(context.Cause(ctx), errQueueCancelled):
		log.write(fmt.Sprintf("lần thử %d: dừng vì lần chạy bị huỷ", claim.Attempt))
		fail("", "", now, now)
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		message := fmt.Sprintf("hết giờ: lần thử %d chạy quá %s", claim.Attempt, time.Duration(claim.TimeoutMS)*time.Millisecond)
		if handler.TimeoutText != "" {
			message += fmt.Sprintf(" (.timeout(%q))", handler.TimeoutText)
		}
		log.attemptFailed(claim.Attempt, value.TimeoutCode, message)
		fail(value.TimeoutCode, message, now.Add(queueBackoff(claim.Attempt)), now)
	default:
		code, message := queueFailure(runErr)
		log.attemptFailed(claim.Attempt, code, message)
		fail(code, message, now.Add(queueBackoff(claim.Attempt)), now)
	}
}

// settleQueue reports a settle: an error on the server's output, and the status to QueueSettled. A settle
// that could not be written leaves the run running under its lease, which no heartbeat moves any more: the
// worker gives it back when the lease runs out.
func (t *Tenant) settleQueue(worker *queueRuntime, id, status string, err error) {
	if err != nil {
		fmt.Printf("%s %s: không ghi được kết quả: %v\n", worker.label(), id, err)
		status = ""
		worker.noteReclaim(time.Now().Add(queueLeaseTTL))
	}
	hook := QueueSettled
	if worker.book != nil {
		hook = ScheduleSettled
	}
	if hook != nil {
		hook(t.appID(), id, status)
	}
}

// queueFailure is an attempt's failure as a run keeps it: the code of the closed set, and the message with
// where it happened (the first line; the stack stays on the server).
func queueFailure(runErr error) (string, string) {
	message, _, _ := strings.Cut(runErr.Error(), "\n")
	failure, _ := value.FailureFrom(value.FailureOfError("", runErr))
	return failure.Code, message
}

// queueLog is a run's log: what its attempts wrote with context.job.log(…), and a line for each attempt that
// failed. It keeps the last queueLogLimit bytes.
type queueLog struct {
	mu   sync.Mutex
	text strings.Builder
}

// write adds a line, cleaned to valid UTF-8 (KitDB stores nothing else: a log it refused would leave the run
// unsettled).
func (l *queueLog) write(line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.text.WriteString(strings.ToValidUTF8(line, string(utf8.RuneError)))
	l.text.WriteByte('\n')
	if l.text.Len() > queueLogLimit {
		kept := l.text.String()
		kept = kept[len(kept)-queueLogLimit:]
		if cut := strings.IndexByte(kept, '\n'); cut >= 0 {
			kept = kept[cut+1:]
		}
		l.text.Reset()
		l.text.WriteString(kept)
	}
}

func (l *queueLog) attemptFailed(attempt int, code, message string) {
	l.write(fmt.Sprintf("lần thử %d: %s %s", attempt, code, message))
}

func (l *queueLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.text.String()
}

// attemptContext is the context the handler of the claimed run gets: a job's, or a schedule's.
func (t *Tenant) attemptContext(worker *queueRuntime, claim queuestore.Claim, log *queueLog) value.Value {
	if worker.book != nil {
		return t.scheduleContext(claim, log)
	}
	return t.queueContext(claim, log)
}

// queueContext is the context a job's handler gets (mục 5): context.data(), the data sent, and context.job,
// the run and this attempt.
func (t *Tenant) queueContext(claim queuestore.Claim, log *queueLog) value.Value {
	job := value.New(map[string]value.Value{
		"id":             value.New(claim.ID),
		"name":           value.New(claim.Name),
		"attempt":        value.New(claim.Attempt),
		"maxAttempts":    value.New(claim.MaxAttempts),
		"isFinalAttempt": value.New(claim.Attempt >= claim.MaxAttempts),
		"createdAt":      value.New(claim.CreatedAt),
		"startedAt":      value.New(claim.StartedAt),
		"log":            logFunction(log),
	})
	return value.Value{K: value.Proxy, V: &jobContext{data: claim.Data, job: job}}
}

// logFunction is context.job.log(…): it writes its arguments, joined by a space, as one line of the run's
// log.
func logFunction(log *queueLog) value.Value {
	return value.NewFunc(func(args ...value.Value) value.Value {
		parts := make([]string, len(args))
		for i, a := range args {
			parts[i] = a.Text()
		}
		log.write(strings.Join(parts, " "))
		return value.Value{K: value.Nil}
	})
}

// jobContext is the context of a job: data() and job, and an error naming the new spelling for each word
// of the handler argument it replaced (job.payload, job.log, job.attempt…).
type jobContext struct {
	data string
	job  value.Value
}

// jobContextRemoved are the words of the old handler argument, each now under context.job, and payload.
var jobContextRemoved = map[string]bool{"log": true, "attempt": true, "maxAttempts": true, "isFinalAttempt": true, "id": true, "name": true}

func (c *jobContext) OnGet(key string) value.Value {
	switch {
	case key == "data":
		return value.NewFunc(func(...value.Value) value.Value { return decodeJobData(c.data) })
	case key == "job":
		return c.job
	case key == "payload":
		return value.InvalidFailure(value.InvalidArgumentCode, "context.payload đã bỏ: dữ liệu đã gửi là context.data()")
	case jobContextRemoved[key]:
		return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("context.%s đã bỏ: context.job.%s", key, key))
	}
	return value.Value{K: value.Nil}
}

func (c *jobContext) OnCompare(_ string, _ value.Value) value.Value { return value.NewNil() }

func (c *jobContext) OnInvoke(method string, args ...value.Value) value.Value {
	if method == "data" {
		return decodeJobData(c.data)
	}
	read := c.OnGet(method)
	if read.IsError {
		return read
	}
	return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("context.%s không phải hàm: context của job có data() và job", method))
}
