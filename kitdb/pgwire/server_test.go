package pgwire

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"
)

func TestPostgresParameterCountIgnoresLiteralsIdentifiersAndComments(t *testing.T) {
	query := `SELECT '$99', "$88", value
FROM products
WHERE first = $2 AND second = $7
-- $100
/* $101 */`
	if got := postgresParameterCount(query); got != 7 {
		t.Fatalf("postgresParameterCount() = %d, want 7", got)
	}
}

func TestCopyAdmissionIsBoundedAndMeasured(t *testing.T) {
	metrics := &CopyMetrics{}
	scheduler := newCopyAdmissionScheduler(1, 1, 1, 1, metrics)
	firstRelease, err := scheduler.acquire(context.Background(), CopyAdmission{Key: "tenant/products", Weight: 1})
	if err != nil {
		t.Fatal(err)
	}
	waitContext, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := scheduler.acquire(waitContext, CopyAdmission{Key: "tenant/products", Weight: 1}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("second COPY exceeded the admission limit")
	}
	snapshot := metrics.Snapshot()
	if snapshot.Active != 1 || snapshot.Peak != 1 || snapshot.Queued != 0 ||
		snapshot.PeakQueued != 1 || snapshot.Acquired != 1 || snapshot.WaitTimeouts != 1 {
		t.Fatalf("waiting COPY metrics = %#v", snapshot)
	}

	firstRelease(123, true, nil)
	secondRelease, err := scheduler.acquire(context.Background(), CopyAdmission{Key: "tenant/orders", Weight: 1})
	if err != nil {
		t.Fatal(err)
	}
	secondRelease(7, true, errors.New("copy failed"))
	snapshot = metrics.Snapshot()
	if snapshot.Active != 0 || snapshot.Peak != 1 || snapshot.Acquired != 2 ||
		snapshot.Completed != 1 || snapshot.Failed != 1 || snapshot.Bytes != 130 ||
		snapshot.WaitNanoseconds == 0 {
		t.Fatalf("completed COPY metrics = %#v", snapshot)
	}
}

func TestAdmissionDefaultsFitConnectionBudget(t *testing.T) {
	server := (Server{MaxConnections: 4}).withDefaults()
	if server.MaxConcurrentQueries != 4 || server.MaxConcurrentQueriesPerKey != 4 ||
		server.MaxQueuedQueries != 4 || server.MaxQueuedQueriesPerKey != 4 || server.QueryMetrics == nil ||
		server.MaxConcurrentCopies != 2 || server.MaxConcurrentCopiesPerKey != 1 ||
		server.MaxQueuedCopies != 4 || server.MaxQueuedCopiesPerKey != 4 || server.CopyMetrics == nil {
		t.Fatalf("admission defaults = %#v", server)
	}
}

func TestQueryAdmissionBoundsExecutionPerKey(t *testing.T) {
	metrics := &QueryMetrics{}
	server := (Server{
		MaxConnections: 4, MaxConcurrentQueries: 2,
		MaxConcurrentQueriesPerKey: 1,
		MaxQueuedQueries:           4,
		MaxQueuedQueriesPerKey:     2,
		QueryTimeout:               2 * time.Second,
		QueryMetrics:               metrics,
	}).withDefaults()
	runtime := &serverRuntime{server: server}
	runtime.queryAdmission = newCopyAdmissionScheduler(
		server.MaxConcurrentQueries,
		server.MaxConcurrentQueriesPerKey,
		server.MaxQueuedQueries,
		server.MaxQueuedQueriesPerKey,
		&metrics.admission,
	)

	started := make(chan string, 3)
	completed := make(chan admissionQueryResult, 3)
	run := func(label, key string, release <-chan struct{}) {
		wire := &wireConnection{
			runtime: runtime,
			session: &blockingQuerySession{
				key: key, label: label, started: started, release: release,
			},
			cancel: &cancelSlot{},
		}
		_, err := wire.runQuery(context.Background(), "SELECT 1", nil)
		completed <- admissionQueryResult{label: label, err: err}
	}
	releaseA1 := make(chan struct{})
	releaseA2 := make(chan struct{})
	releaseB := make(chan struct{})
	go run("a1", "tenant/a", releaseA1)
	waitForQueryStart(t, started, "a1")
	go run("a2", "tenant/a", releaseA2)
	waitForQueryQueue(t, metrics, 1)
	go run("b", "tenant/b", releaseB)
	waitForQueryStart(t, started, "b")

	snapshot := metrics.Snapshot()
	if snapshot.Active != 2 || snapshot.Peak != 2 || snapshot.Queued != 1 ||
		snapshot.PeakQueued != 1 || snapshot.Acquired != 2 {
		t.Fatalf("active query metrics = %#v", snapshot)
	}
	close(releaseB)
	waitForQueryResult(t, completed, "b")
	select {
	case label := <-started:
		t.Fatalf("same-key query started while first remained active: %s", label)
	case <-time.After(10 * time.Millisecond):
	}
	close(releaseA1)
	waitForQueryStart(t, started, "a2")
	close(releaseA2)
	waitForQueryResults(t, completed, "a1", "a2")

	snapshot = metrics.Snapshot()
	if snapshot.Active != 0 || snapshot.Queued != 0 || snapshot.Peak != 2 ||
		snapshot.PeakQueued != 1 || snapshot.Acquired != 3 ||
		snapshot.Completed != 3 || snapshot.Failed != 0 || snapshot.WaitNanoseconds == 0 {
		t.Fatalf("completed query metrics = %#v", snapshot)
	}
}

type blockingQuerySession struct {
	key     string
	label   string
	started chan<- string
	release <-chan struct{}
}

type admissionQueryResult struct {
	label string
	err   error
}

func (session *blockingQuerySession) QueryAdmission() QueryAdmission {
	return QueryAdmission{Key: session.key, Weight: 1}
}

func (session *blockingQuerySession) Execute(
	ctx context.Context,
	_ string,
	_ []Parameter,
) (Result, error) {
	select {
	case session.started <- session.label:
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
	select {
	case <-session.release:
		return Result{CommandTag: "SELECT 1"}, nil
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
}

func (session *blockingQuerySession) Close() error { return nil }

func waitForQueryStart(t *testing.T, started <-chan string, want string) {
	t.Helper()
	select {
	case got := <-started:
		if got != want {
			t.Fatalf("query start = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("query %q did not start", want)
	}
}

func waitForQueryResult(t *testing.T, completed <-chan admissionQueryResult, want string) {
	t.Helper()
	select {
	case result := <-completed:
		if result.label != want || result.err != nil {
			t.Fatalf("query result = %+v, want label %q without error", result, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("query %q did not complete", want)
	}
}

func waitForQueryResults(t *testing.T, completed <-chan admissionQueryResult, wants ...string) {
	t.Helper()
	remaining := make(map[string]struct{}, len(wants))
	for _, want := range wants {
		remaining[want] = struct{}{}
	}
	deadline := time.After(time.Second)
	for len(remaining) != 0 {
		select {
		case result := <-completed:
			if result.err != nil {
				t.Fatalf("query %q failed: %v", result.label, result.err)
			}
			if _, found := remaining[result.label]; !found {
				t.Fatalf("unexpected query result %q", result.label)
			}
			delete(remaining, result.label)
		case <-deadline:
			t.Fatalf("queries did not complete: %v", remaining)
		}
	}
}

func waitForQueryQueue(t *testing.T, metrics *QueryMetrics, queued int64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if metrics.Snapshot().Queued == queued {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("query queue = %d, want %d", metrics.Snapshot().Queued, queued)
}

func TestCopyAdmissionIsFairAcrossKeys(t *testing.T) {
	metrics := &CopyMetrics{}
	scheduler := newCopyAdmissionScheduler(2, 1, 8, 4, metrics)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	firstA, err := scheduler.acquire(ctx, CopyAdmission{Key: "tenant/a", Weight: 1})
	if err != nil {
		t.Fatal(err)
	}
	type acquireResult struct {
		release func(int64, bool, error)
		err     error
	}
	nextA := make(chan acquireResult, 1)
	go func() {
		release, acquireErr := scheduler.acquire(ctx, CopyAdmission{Key: "tenant/a", Weight: 1})
		nextA <- acquireResult{release: release, err: acquireErr}
	}()
	waitForCopyQueue(t, metrics, 1)

	firstB, err := scheduler.acquire(ctx, CopyAdmission{Key: "tenant/b", Weight: 1})
	if err != nil {
		t.Fatal(err)
	}
	nextC := make(chan acquireResult, 1)
	go func() {
		release, acquireErr := scheduler.acquire(ctx, CopyAdmission{Key: "tenant/c", Weight: 1})
		nextC <- acquireResult{release: release, err: acquireErr}
	}()
	waitForCopyQueue(t, metrics, 2)

	firstA(1, true, nil)
	var admittedC acquireResult
	select {
	case admittedC = <-nextC:
		if admittedC.err != nil {
			t.Fatal(admittedC.err)
		}
	case admittedA := <-nextA:
		if admittedA.release != nil {
			admittedA.release(0, false, errors.New("unexpected admission"))
		}
		t.Fatal("one key reacquired before an unserved key")
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	firstB(1, true, nil)
	var admittedA acquireResult
	select {
	case admittedA = <-nextA:
		if admittedA.err != nil {
			t.Fatal(admittedA.err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	admittedC.release(1, true, nil)
	admittedA.release(1, true, nil)
	snapshot := metrics.Snapshot()
	if snapshot.Active != 0 || snapshot.Queued != 0 || snapshot.Peak != 2 ||
		snapshot.PeakQueued != 2 || snapshot.Acquired != 4 || snapshot.Completed != 4 {
		t.Fatalf("fair COPY metrics = %#v", snapshot)
	}
}

func TestCopyAdmissionHonorsWeights(t *testing.T) {
	metrics := &CopyMetrics{}
	scheduler := newCopyAdmissionScheduler(1, 1, 8, 4, metrics)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	blocker, err := scheduler.acquire(ctx, CopyAdmission{Key: "blocker", Weight: 1})
	if err != nil {
		t.Fatal(err)
	}
	type weightedResult struct {
		label   string
		release func(int64, bool, error)
		err     error
	}
	granted := make(chan weightedResult, 6)
	enqueue := func(label, key string, weight int, queued int64) {
		t.Helper()
		go func() {
			release, acquireErr := scheduler.acquire(ctx, CopyAdmission{Key: key, Weight: weight})
			granted <- weightedResult{label: label, release: release, err: acquireErr}
		}()
		waitForCopyQueue(t, metrics, queued)
	}
	enqueue("a1", "tenant/a", 2, 1)
	enqueue("a2", "tenant/a", 2, 2)
	enqueue("a3", "tenant/a", 2, 3)
	enqueue("a4", "tenant/a", 2, 4)
	enqueue("b1", "tenant/b", 1, 5)
	enqueue("b2", "tenant/b", 1, 6)

	blocker(1, true, nil)
	for _, want := range []string{"a1", "b1", "a2", "b2", "a3", "a4"} {
		select {
		case result := <-granted:
			if result.err != nil {
				t.Fatal(result.err)
			}
			if result.label != want {
				t.Fatalf("weighted COPY order got %q, want %q", result.label, want)
			}
			result.release(1, true, nil)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	snapshot := metrics.Snapshot()
	if snapshot.Active != 0 || snapshot.Queued != 0 || snapshot.Acquired != 7 || snapshot.Completed != 7 {
		t.Fatalf("weighted COPY metrics = %#v", snapshot)
	}
}

func TestCopyAdmissionRejectsBoundedQueue(t *testing.T) {
	metrics := &CopyMetrics{}
	scheduler := newCopyAdmissionScheduler(1, 1, 1, 1, metrics)
	first, err := scheduler.acquire(context.Background(), CopyAdmission{Key: "tenant/a", Weight: 1})
	if err != nil {
		t.Fatal(err)
	}
	waitContext, cancel := context.WithCancel(context.Background())
	waiting := make(chan error, 1)
	go func() {
		_, acquireErr := scheduler.acquire(waitContext, CopyAdmission{Key: "tenant/a", Weight: 1})
		waiting <- acquireErr
	}()
	waitForCopyQueue(t, metrics, 1)
	if _, err := scheduler.acquire(context.Background(), CopyAdmission{Key: "tenant/b", Weight: 1}); !errors.Is(err, errCopyAdmissionQueueFull) {
		t.Fatalf("full COPY queue error = %v", err)
	}
	cancel()
	if err := <-waiting; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled COPY waiter error = %v", err)
	}
	first(1, true, nil)
	snapshot := metrics.Snapshot()
	if snapshot.Active != 0 || snapshot.Queued != 0 || snapshot.Rejected != 1 || snapshot.WaitTimeouts != 1 {
		t.Fatalf("bounded COPY queue metrics = %#v", snapshot)
	}
}

func TestCopyAdmissionConcurrentDrain(t *testing.T) {
	const copies = 64
	metrics := &CopyMetrics{}
	scheduler := newCopyAdmissionScheduler(4, 1, copies, copies, metrics)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := make(chan struct{})
	errorsFound := make(chan error, copies)
	activeByKey := make(map[string]int)
	var activeMu sync.Mutex
	var workers sync.WaitGroup
	for index := 0; index < copies; index++ {
		key := fmt.Sprintf("tenant/database-%d", index%8)
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			release, err := scheduler.acquire(ctx, CopyAdmission{Key: key, Weight: 1})
			if err != nil {
				errorsFound <- err
				return
			}
			activeMu.Lock()
			activeByKey[key]++
			if activeByKey[key] != 1 {
				errorsFound <- fmt.Errorf("COPY key %q active=%d", key, activeByKey[key])
			}
			activeMu.Unlock()
			time.Sleep(100 * time.Microsecond)
			activeMu.Lock()
			activeByKey[key]--
			activeMu.Unlock()
			release(1, true, nil)
		}()
	}
	close(start)
	workers.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Error(err)
	}
	snapshot := metrics.Snapshot()
	if snapshot.Active != 0 || snapshot.Queued != 0 || snapshot.Peak > 4 ||
		snapshot.Acquired != copies || snapshot.Completed != copies || snapshot.Failed != 0 {
		t.Fatalf("concurrent COPY drain metrics = %#v", snapshot)
	}
}

func waitForCopyQueue(t *testing.T, metrics *CopyMetrics, queued int64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if metrics.Snapshot().Queued == queued {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("COPY queue = %d, want %d", metrics.Snapshot().Queued, queued)
}

func TestCopyInCandidateDoesNotGateOrdinaryQueries(t *testing.T) {
	for _, query := range []string{"COPY products FROM STDIN", " copy\nproducts from stdin", "CoPy products"} {
		if !copyInCandidate(query) {
			t.Fatalf("copyInCandidate(%q) = false", query)
		}
	}
	for _, query := range []string{"SELECT 1", "copyright", "COPYRIGHT products"} {
		if copyInCandidate(query) {
			t.Fatalf("copyInCandidate(%q) = true", query)
		}
	}
}

func TestDecoderRejectsUnterminatedCString(t *testing.T) {
	decoder := decoder{data: []byte("kitdb")}
	if _, err := decoder.cstring(); err == nil {
		t.Fatal("cstring accepted an unterminated value")
	}
}

func TestBinaryResultFieldSupportsKitDBScalars(t *testing.T) {
	oid := make([]byte, 4)
	binary.BigEndian.PutUint32(oid, 520778552)
	float8 := make([]byte, 8)
	binary.BigEndian.PutUint64(float8, math.Float64bits(12.5))
	numeric, err := EncodeNumericBinary("12.50", -1)
	if err != nil {
		t.Fatal(err)
	}
	date, _ := EncodeDateBinary("2000-01-02")
	clock, _ := EncodeTimeBinary("01:02:03.4")
	timestamp, _ := EncodeTimestampBinary("2000-01-01 00:00:00", false)
	timestamptz, _ := EncodeTimestampBinary("2000-01-01 00:00:00+00", true)
	interval, _ := EncodeIntervalBinary("2 mons 3 days 04:05:06")
	uuid, _ := EncodeUUIDBinary("a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11")

	tests := []struct {
		name string
		oid  uint32
		text string
		want []byte
	}{
		{name: "text", oid: OIDText, text: "KIT-1", want: []byte("KIT-1")},
		{name: "varchar", oid: OIDVarchar, text: "KIT-1", want: []byte("KIT-1")},
		{name: "bpchar", oid: OIDBPChar, text: "A   ", want: []byte("A   ")},
		{name: "boolean", oid: OIDBool, text: "t", want: []byte{1}},
		{name: "oid", oid: OIDOID, text: "520778552", want: oid},
		{name: "float8", oid: OIDFloat8, text: "12.5", want: float8},
		{name: "numeric", oid: OIDNumeric, text: "12.50", want: numeric},
		{name: "date", oid: OIDDate, text: "2000-01-02", want: date},
		{name: "time", oid: OIDTime, text: "01:02:03.4", want: clock},
		{name: "timestamp", oid: OIDTimestamp, text: "2000-01-01 00:00:00", want: timestamp},
		{name: "timestamptz", oid: OIDTimestampTZ, text: "2000-01-01 00:00:00+00", want: timestamptz},
		{name: "interval", oid: OIDInterval, text: "2 mons 3 days 04:05:06", want: interval},
		{name: "uuid", oid: OIDUUID, text: "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", want: uuid},
		{name: "json", oid: OIDJSON, text: `{"sku":"KIT-1"}`, want: []byte(`{"sku":"KIT-1"}`)},
		{name: "jsonb", oid: OIDJSONB, text: `{"sku":"KIT-1"}`, want: append([]byte{1}, []byte(`{"sku":"KIT-1"}`)...)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := binaryResultField(
				Field{Data: []byte(test.text)},
				Column{DataTypeOID: test.oid},
			)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got.Data, test.want) || got.Null {
				t.Fatalf("binary result = %x, null=%v; want %x", got.Data, got.Null, test.want)
			}
		})
	}
}
