package work

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/storage"
	"github.com/kitwork/engine/value"
)

// ── kitwork/storage (KITWORK_STORAGE.md) ──────────────────────────────────────────────────────────
//
//	import { memory, disk, datastore } from "kitwork/storage";
//	const rates = memory("rates").expire("5m").get(() => loadRates());   // read, or compute once and keep
//	datastore("flags").segment("checkout").set({ newFlow: true });
//	memory("rooms").segment(roomId).send({ event: "chat", id, data: { text } });
//	memory("rooms").segment(roomId).observe((message) => stream.send(message));   // stream.go
//
// One API for three places. A handle names a key, segment() and expire() make another handle and never
// change the one they are called on, and the action ends the chain. Each app has its own storage (one per
// app.Runtime, installed as the resource "storage"): its sites and its jobs share it, and no app sees
// another's keys, even of the same name. Every value is kept encoded (storage/codec.go), so every read is a
// value of its own and the three places give back the same value for the same write.
//
// A call of a handle is handed the VM that calls it (runtime.CallerMethods), for whether the file is still
// loading (a file only declares: get, set and the rest at its top level fail), for get(() => …), which runs
// its function on that VM with that VM's budget, and for observe(), which binds the observer to the request
// the VM runs (stream.go).

// The ceilings of the storage of one app. The spec leaves the numbers open (mục 10.2); vars so tests can
// lower them.
var (
	// storageValueLimit is the largest value, and the largest data of a message, in encoded bytes (mục 5).
	storageValueLimit = 1 << 20
	// storageMemoryKeys and storageMemoryBytes bound memory() of one app; past them the key used least
	// recently is pushed out (mục 9).
	storageMemoryKeys  = 10_000
	storageMemoryBytes = 64 << 20
	// storageObserverLimit is how many observers one app may have at once (mục 7).
	storageObserverLimit = 10_000
	// storageQueueLimit is how many messages an observer holds unread; one more closes it (mục 7).
	storageQueueLimit = 256
	// storageKeyLimit is the longest key, its name and segments together, in bytes.
	storageKeyLimit = 4096
	// storageClock is the storage's time: when a value was written, when it expires.
	storageClock = time.Now
	// storageFlightWaiting is told the key a get(() => …) waits for another request to compute (a test
	// sets it, to know a request is waiting without sleeping).
	storageFlightWaiting func(key string)
	// storageSwept is told each time disk() or datastore() reads all it holds to remove what expired (a
	// test sets it, to count the sweeps).
	storageSwept func(place string)
)

// storageSweepDue reports whether a full place may hold a value that has expired: not known yet (no sweep
// since the process began), or the soonest expiry known is past.
func storageSweepDue(known bool, next time.Time) bool {
	return !known || (!next.IsZero() && !storageClock().Before(next))
}

// storageSooner is the soonest expiry once a value expiring at expireAt is kept, when the soonest is known.
func storageSooner(known bool, next, expireAt time.Time) time.Time {
	if !known || expireAt.IsZero() {
		return next
	}
	if next.IsZero() || expireAt.Before(next) {
		return expireAt
	}
	return next
}

// storageResourceName is the resource of the app's runtime that holds its storage.
const storageResourceName = "storage"

// appStorage is the storage of one app: memory(), the single flight of get(() => …) for its three places,
// the hub of send() and observe(), the count of its open streams, and disk() and datastore() once used.
type appStorage struct {
	root    string // the app's folder: disk() and datastore() live in its .database/
	memory  *storage.MemoryStore
	flights storage.Flights
	hub     *storage.Hub
	streams streamSlots

	places placeSet
}

// Close ends every observer of the app (its streams end with their requests) and closes disk() and
// datastore().
func (s *appStorage) Close() {
	s.hub.Close()
	s.places.close()
}

// storage is the storage of t's app, installed on first use.
func (t *Tenant) storage() (*appStorage, error) {
	if t == nil || t.appRuntime == nil {
		return nil, errors.New("không có app để lưu")
	}
	if current, ok := t.appRuntime.Resource(storageResourceName).(*appStorage); ok {
		return current, nil
	}
	candidate := &appStorage{
		root:   t.resolveApp(),
		memory: storage.NewMemoryStore(storageMemoryKeys, storageMemoryBytes, func() time.Time { return storageClock() }),
		hub:    storage.NewHub(storageObserverLimit, storageQueueLimit),
	}
	resource, _, err := t.appRuntime.InstallResource(storageResourceName, candidate)
	if err != nil {
		return nil, err
	}
	current, ok := resource.(*appStorage)
	if !ok {
		return nil, fmt.Errorf("storage: app resource has unexpected type %T", resource)
	}
	return current, nil
}

// ── the module ─────────────────────────────────────────────────────────────────────────────────────

// storageModule is kitwork/storage: memory, disk and datastore, each making a handle.
type storageModule struct {
	tenant *Tenant
}

// Storage is the module: import { memory, disk, datastore } from "kitwork/storage".
func (w *KitWork) Storage() value.Value {
	return value.Value{K: value.Proxy, V: &storageModule{tenant: w.tenant}}
}

func (m *storageModule) OnGet(key string) value.Value {
	place, ok := storagePlaces[key]
	if !ok {
		return value.InvalidFailure(value.InvalidArgumentCode, compiler.StorageUnknownWord(key))
	}
	return value.NewFunc(func(args ...value.Value) value.Value { return m.open(place, args) })
}

func (m *storageModule) OnCompare(_ string, _ value.Value) value.Value { return value.NewNil() }

func (m *storageModule) OnInvoke(method string, args ...value.Value) value.Value {
	place, ok := storagePlaces[method]
	if !ok {
		return value.InvalidFailure(value.InvalidArgumentCode, compiler.StorageUnknownWord(method))
	}
	return m.open(place, args)
}

var storagePlaces = map[string]storage.Place{"memory": storage.Memory, "disk": storage.Disk, "datastore": storage.Datastore}

// open is memory(name), disk(name), datastore(name): a handle of the root name, touching nothing yet, so a
// file may make one at its top level (const flags = datastore("flags")).
func (m *storageModule) open(place storage.Place, args []value.Value) value.Value {
	if len(args) != 1 || args[0].K != value.String || args[0].Text() == "" {
		return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf(`%s(tên) nhận một tên gốc là chuỗi không rỗng: %s("rates"); phần sau của khoá đặt bằng segment(…)`, place, place))
	}
	name := args[0].Text()
	if len(name) > storageKeyLimit {
		return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("%s(tên): tên gốc dài %d byte, quá trần %d byte của một khoá", place, len(name), storageKeyLimit))
	}
	return value.Value{K: value.Proxy, V: &storageHandle{tenant: m.tenant, key: storage.Key{Place: place, Name: name}}}
}

// ── the handle ─────────────────────────────────────────────────────────────────────────────────────

// storageHandle is one key of one place, and the expiry its next write takes. It never changes: segment()
// and expire() make another.
type storageHandle struct {
	tenant     *Tenant
	key        storage.Key
	expire     func(time.Time) time.Duration // nil: the value does not expire
	expireText string
}

// storageHandleWords are the words of a handle.
var storageHandleWords = map[string]bool{
	"segment": true, "expire": true, "name": true, "key": true, "keys": true,
	"get": true, "set": true, "exists": true, "ttl": true, "delete": true, "send": true, "observe": true,
}

// storageActions are the words that touch the place: none of them runs while a file loads (mục 9).
var storageActions = map[string]bool{
	"get": true, "set": true, "exists": true, "ttl": true, "delete": true, "send": true, "observe": true,
}

func (h *storageHandle) OnGet(key string) value.Value {
	if !storageHandleWords[key] {
		return h.unknown(key)
	}
	// read by name (const { get } = handle): called alone it is still handed its caller
	return value.NewCallerFunction(func(caller any, args []value.Value) value.Value {
		vm, _ := caller.(*runtime.VM)
		return h.call(vm, key, args)
	})
}

func (h *storageHandle) OnCompare(_ string, _ value.Value) value.Value { return value.NewNil() }

// OnInvoke is a call by a host, which lends no VM.
func (h *storageHandle) OnInvoke(method string, args ...value.Value) value.Value {
	return h.call(nil, method, args)
}

// InvokeFrom is handle.<method>(…) written in a program: caller is the VM that runs it.
func (h *storageHandle) InvokeFrom(caller *runtime.VM, method string, args []value.Value) (value.Value, bool) {
	return h.call(caller, method, args), true
}

func (h *storageHandle) unknown(word string) value.Value {
	return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("%s.%s không có: handle có segment, expire, name, key, keys, get, set, exists, ttl, delete, send, observe (KITWORK_STORAGE.md mục 4–7)", h.spelling(), word))
}

// spelling is the handle as a program writes it, for a message: memory("users").segment(…).
func (h *storageHandle) spelling() string {
	spelled := fmt.Sprintf("%s(%q)", h.key.Place, h.key.Name)
	if len(h.key.Segments) > 0 {
		spelled += ".segment(…)"
	}
	return spelled
}

func (h *storageHandle) call(caller *runtime.VM, method string, args []value.Value) value.Value {
	if !storageHandleWords[method] {
		return h.unknown(method)
	}
	if storageActions[method] && caller != nil {
		if _, loading := loadingVMs.Load(caller); loading {
			return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("%s.%s ở cấp file: lúc nạp, file chỉ khai; tạo handle ở cấp file thì được (const flags = %s(%q)), còn %s gọi trong một handler, một job, một lịch hay một hàm (KITWORK_STORAGE.md mục 9)",
				h.spelling(), method, h.key.Place, h.key.Name, method))
		}
	}
	switch method {
	case "segment":
		return h.segment(args)
	case "expire":
		return h.withExpire(args)
	case "name":
		return value.New(h.key.Name)
	case "key":
		return value.New(h.key.Human())
	case "keys":
		segments := make([]value.Value, len(h.key.Segments))
		for index, segment := range h.key.Segments {
			segments[index] = value.New(segment)
		}
		return value.New(segments)
	case "get":
		return h.get(caller, args)
	case "set":
		return h.set(args)
	case "exists":
		return h.exists(args)
	case "ttl":
		return h.ttl(args)
	case "delete":
		return h.remove(args)
	case "send":
		return h.send(args)
	case "observe":
		return h.observe(caller, args)
	}
	return h.unknown(method)
}

// segment(…) is the handle of the key with segments added: each a non-empty string or a number, which is
// written as text (segment(123) is "123", mục 4).
func (h *storageHandle) segment(args []value.Value) value.Value {
	segments, failure := segmentWords(args, `users.segment("123"), users.segment(id, "profile")`, `dựng khoá bằng từng đoạn, users.segment(id, "profile")`)
	if failure.Failed() {
		return failure
	}
	size := len(h.key.Human())
	for _, segment := range segments {
		size += 1 + len(segment)
	}
	if size > storageKeyLimit {
		return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("segment(…): khoá dài %d byte, quá trần %d byte của một khoá", size, storageKeyLimit))
	}
	next := *h
	next.key = h.key.With(segments...)
	return value.Value{K: value.Proxy, V: &next}
}

// segmentWords reads the arguments of segment(…), of a storage handle and of a file handle alike
// (KITWORK_FILE.md mục 2: segment() as storage's): at least one, each a non-empty string or a finite number,
// which is written as text (segment(123) is "123"). examples and hint are the caller's words: how its
// segment is written, and how to build with it.
func segmentWords(args []value.Value, examples, hint string) ([]string, value.Value) {
	if len(args) == 0 {
		return nil, value.InvalidFailure(value.InvalidArgumentCode, "segment(…) nhận ít nhất một đoạn: "+examples)
	}
	segments := make([]string, len(args))
	for index, argument := range args {
		switch {
		case argument.K == value.String && argument.Text() != "":
			segments[index] = argument.Text()
		case argument.K == value.Number && !math.IsNaN(argument.N) && !math.IsInf(argument.N, 0):
			segments[index] = string(value.AppendNumber(nil, argument.N))
		default:
			kind := value.KindName(argument)
			if argument.K == value.String {
				kind = "chuỗi rỗng"
			}
			return nil, value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("segment(…) nhận chuỗi không rỗng hay số, đoạn thứ %d là %s: %s", index+1, kind, hint))
		}
	}
	return segments, value.Value{K: value.Nil}
}

// withExpire is expire(hạn): the expiry of the next write, as a route's cache writes it (mục 6): a span
// ("30s", "5m", "1d"), a number of milliseconds, or a time of day ("nextday 03:00").
func (h *storageHandle) withExpire(args []value.Value) value.Value {
	usage := `expire(hạn) nhận một khoảng ("30s", "5m", "1d"), số mili giây, hay một mốc giờ ("nextday 03:00")`
	if len(args) != 1 || (args[0].K != value.String && args[0].K != value.Number) {
		return value.InvalidFailure(value.InvalidArgumentCode, usage)
	}
	if args[0].K == value.Number && (args[0].N < 1 || math.IsNaN(args[0].N) || math.IsInf(args[0].N, 0)) {
		return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("expire(%s): số mili giây từ 1 trở lên; %s", args[0].Text(), usage))
	}
	resolver := parseExpiry(args[0])
	if resolver == nil {
		return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("expire(%q): không đọc được hạn; %s", args[0].Text(), usage))
	}
	next := *h
	next.expire, next.expireText = resolver, args[0].Text()
	return value.Value{K: value.Proxy, V: &next}
}

// expireAt is when a value written now expires; zero for never.
func (h *storageHandle) expireAt(now time.Time) time.Time {
	if h.expire == nil {
		return time.Time{}
	}
	span := h.expire(now)
	if span <= 0 {
		return time.Time{}
	}
	return now.Add(span)
}

// place is the store of the handle's place in its app.
func (h *storageHandle) place() (*appStorage, storagePlace, value.Value) {
	app, err := h.tenant.storage()
	if err != nil {
		return nil, nil, value.InvalidFailure(value.UnavailableCode, fmt.Sprintf("%s: %v", h.spelling(), err))
	}
	place, err := app.place(h.key.Place, h.tenant)
	if err != nil {
		return nil, nil, storageFailure(h.spelling(), err)
	}
	return app, place, value.Value{K: value.Nil}
}

func (h *storageHandle) noArguments(word string, args []value.Value) value.Value {
	if len(args) != 0 {
		return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("%s.%s() không nhận đối số", h.spelling(), word))
	}
	return value.Value{K: value.Nil}
}

// get is get() (the value, or null when there is none or it expired) and get(() => …): read, or run the
// function once, keep its result with the handle's expiry, and give it (mục 6).
func (h *storageHandle) get(caller *runtime.VM, args []value.Value) value.Value {
	if len(args) > 1 || (len(args) == 1 && args[0].K != value.Func) {
		return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("%s.get nhận không gì hay một hàm: get() đọc, get(() => …) đọc hoặc tính; hạn đặt trước bằng expire(\"5m\")", h.spelling()))
	}
	app, place, failure := h.place()
	if failure.Failed() {
		return failure
	}
	key := h.key.Encoded()
	if len(args) == 0 {
		return h.read(place, key)
	}
	fn, ok := args[0].V.(*value.Lambda)
	if !ok || fn == nil {
		return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("%s.get(fn): fn là một hàm của chương trình, get(() => …)", h.spelling()))
	}
	if caller == nil {
		return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("%s.get(fn) chạy hàm trong chương trình đã gọi nó; ở đây không có", h.spelling()))
	}
	return h.readOrCompute(caller, app, place, key, fn)
}

// read is the value kept under key, decoded as a value of its own, or null.
func (h *storageHandle) read(place storagePlace, key string) value.Value {
	data, found, err := place.get(key)
	if err != nil {
		return storageFailure(h.spelling(), err)
	}
	if !found {
		return value.Value{K: value.Nil}
	}
	decoded, err := storage.Decode(data)
	if err != nil {
		return storageFailure(h.spelling(), err)
	}
	return decoded
}

// readOrCompute is get(fn) on a miss: one run of fn for every request that misses the key at once (the
// app's flights). fn runs on the caller's VM, with its budget: what it costs is the request's. A waiter
// reads what the run kept; when the run failed, kept nothing, the next waiter runs fn itself, and so each
// caller whose fn fails gets its own failure. A call of the same key from inside fn would wait for itself:
// it fails instead. So does a wait that closes a circle of two keys or more (fn of A waits for B while fn of
// B, in another request, waits for A): the request that would close it fails with CONFLICT, its flight ends,
// and the other goes on.
func (h *storageHandle) readOrCompute(caller *runtime.VM, app *appStorage, place storagePlace, key string, fn *value.Lambda) value.Value {
	for {
		data, found, err := place.get(key)
		if err != nil {
			return storageFailure(h.spelling(), err)
		}
		if found {
			if decoded, err := storage.Decode(data); err == nil {
				return decoded
			}
		}
		flight, leading := app.flights.Begin(key, caller)
		if !leading {
			if failure, again := h.waitFor(caller, app, flight); !again {
				return failure
			}
			continue
		}
		return h.lead(caller, app, place, key, flight, fn)
	}
}

// lead runs fn as the leader of key's flight, and ends the flight however the run ends, a panic included:
// a flight left in the air would hold every later get(fn) of the key.
func (h *storageHandle) lead(caller *runtime.VM, app *appStorage, place storagePlace, key string, flight *storage.Flight, fn *value.Lambda) value.Value {
	defer app.flights.End(key, flight)
	return h.compute(caller, place, key, fn)
}

// waitFor waits for flight, another caller's run of fn: again is true once it ended (read again, or lead);
// otherwise the failure is why the caller does not wait.
func (h *storageHandle) waitFor(caller *runtime.VM, app *appStorage, flight *storage.Flight) (failure value.Value, again bool) {
	if owner, ok := flight.Owner.(*runtime.VM); ok && caller.RunsInside(owner) {
		return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("%s.get(fn): hàm đang tính khoá %q lại đọc chính khoá đó bằng get(fn), sẽ đợi chính nó; đọc bằng get() hay tách khoá", h.spelling(), h.key.Human())), false
	}
	if !app.flights.Wait(caller, flight, flightRunsInside) {
		return value.InvalidFailure(value.ConflictCode, fmt.Sprintf("%s.get(fn): khoá %q đang được một request khác tính, và hàm đó đang đợi chính khoá request này đang tính: hai get(fn) lồng nhau theo hai thứ tự ngược nhau; request này dừng để request kia đi tiếp (thử lại được), hay lồng các khoá theo một thứ tự", h.spelling(), h.key.Human())), false
	}
	defer app.flights.Waited(caller)
	var stop <-chan struct{}
	if caller.Context != nil {
		stop = caller.Context.Done()
	}
	if storageFlightWaiting != nil {
		storageFlightWaiting(h.key.Human())
	}
	select {
	case <-flight.Done():
		return value.Value{K: value.Nil}, true
	case <-stop:
		return value.InvalidFailure(value.CancelledCode, fmt.Sprintf("%s.get(fn): request đã xong trong lúc đợi một request khác tính khoá %q", h.spelling(), h.key.Human())), false
	}
}

// flightRunsInside is runtime.VM.RunsInside for the owners and waiters of storage.Flights.
func flightRunsInside(inner, outer any) bool {
	innerVM, _ := inner.(*runtime.VM)
	outerVM, _ := outer.(*runtime.VM)
	return innerVM != nil && outerVM != nil && innerVM.RunsInside(outerVM)
}

// compute runs fn as the leader of key's flight and keeps what it gives.
func (h *storageHandle) compute(caller *runtime.VM, place storagePlace, key string, fn *value.Lambda) value.Value {
	// another request may have kept the value between the read and the flight
	if data, found, err := place.get(key); err == nil && found {
		if decoded, err := storage.Decode(data); err == nil {
			return decoded
		}
	}
	result := caller.ExecuteLambda(fn, nil)
	if result.K == value.Invalid {
		return result
	}
	if failure := h.write(place, key, result); failure.Failed() {
		return failure
	}
	return result
}

// set(giá_trị) writes the value with the handle's expiry, and gives nothing (mục 5).
func (h *storageHandle) set(args []value.Value) value.Value {
	if len(args) != 1 {
		return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("%s.set nhận một giá trị; hạn đặt trước bằng expire(): %s(%q).expire(\"5m\").set(giá_trị)", h.spelling(), h.key.Place, h.key.Name))
	}
	_, place, failure := h.place()
	if failure.Failed() {
		return failure
	}
	if failure := h.write(place, h.key.Encoded(), args[0]); failure.Failed() {
		return failure
	}
	return value.Value{K: value.Nil}
}

// write encodes item and keeps it under key with the handle's expiry, from now.
func (h *storageHandle) write(place storagePlace, key string, item value.Value) value.Value {
	data, err := storage.Encode(item, storageValueLimit)
	if err != nil {
		return valueFailure(h.spelling()+".set", err)
	}
	if err := place.set(key, data, h.expireAt(storageClock())); err != nil {
		return storageFailure(h.spelling(), err)
	}
	return value.Value{K: value.Nil}
}

// exists() tells "no value" from "null kept" (mục 5).
func (h *storageHandle) exists(args []value.Value) value.Value {
	if failure := h.noArguments("exists", args); failure.Failed() {
		return failure
	}
	_, place, failure := h.place()
	if failure.Failed() {
		return failure
	}
	_, found, err := place.expiry(h.key.Encoded())
	if err != nil {
		return storageFailure(h.spelling(), err)
	}
	return value.New(found)
}

// ttl() is how long the value has left, in milliseconds; null when it has no expiry or there is none.
func (h *storageHandle) ttl(args []value.Value) value.Value {
	if failure := h.noArguments("ttl", args); failure.Failed() {
		return failure
	}
	_, place, failure := h.place()
	if failure.Failed() {
		return failure
	}
	expireAt, found, err := place.expiry(h.key.Encoded())
	if err != nil {
		return storageFailure(h.spelling(), err)
	}
	if !found || expireAt.IsZero() {
		return value.Value{K: value.Nil}
	}
	left := expireAt.Sub(storageClock())
	return value.New(math.Ceil(float64(left) / float64(time.Millisecond)))
}

// delete() drops the value; true when there was one (mục 5).
func (h *storageHandle) remove(args []value.Value) value.Value {
	if failure := h.noArguments("delete", args); failure.Failed() {
		return failure
	}
	_, place, failure := h.place()
	if failure.Failed() {
		return failure
	}
	removed, err := place.remove(h.key.Encoded())
	if err != nil {
		return storageFailure(h.spelling(), err)
	}
	return value.New(removed)
}

// send(phong_bì) carries the envelope to every observer of this key, now; nothing is kept, nothing is
// given back (mục 7). Only this process's observers hear it.
func (h *storageHandle) send(args []value.Value) value.Value {
	message, failure := storageEnvelope("send", args)
	if failure.Failed() {
		return failure
	}
	app, err := h.tenant.storage()
	if err != nil {
		return value.InvalidFailure(value.UnavailableCode, fmt.Sprintf("%s.send: %v", h.spelling(), err))
	}
	app.hub.Send(h.key.Encoded(), message)
	return value.Value{K: value.Nil}
}

// ── the envelope ───────────────────────────────────────────────────────────────────────────────────

// storageEnvelope reads the one argument of send as { event, id, data } (mục 7, KITWORK_SSE.md mục 2): data
// is required and kept as a value is; event and id, when written, are strings with no line break (an SSE
// field ends at one); any other key is refused, so the data of a program never passes for the envelope.
func storageEnvelope(word string, args []value.Value) (*storage.Message, value.Value) {
	refuse := func(why string) (*storage.Message, value.Value) {
		return nil, value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("%s nhận { event, id, data }; dữ liệu đặt trong data: %s", word, why))
	}
	if len(args) != 1 {
		return refuse(fmt.Sprintf("ở đây là %d đối số", len(args)))
	}
	entries := mapOf(args[0])
	if entries == nil {
		return refuse(fmt.Sprintf("ở đây là %s, %s({ data: … })", value.KindName(args[0]), word))
	}
	message := &storage.Message{}
	for key, item := range entries {
		switch key {
		case "event", "id":
			if item.K != value.String {
				return refuse(fmt.Sprintf("%s là chuỗi, ở đây là %s", key, value.KindName(item)))
			}
			if strings.ContainsAny(item.Text(), "\r\n") {
				return refuse(fmt.Sprintf("%s không chứa xuống dòng", key))
			}
			if key == "event" {
				message.Event, message.HasEvent = item.Text(), true
			} else {
				message.ID, message.HasID = item.Text(), true
			}
		case "data":
		default:
			return refuse(fmt.Sprintf("khoá %q không có trong phong bì", key))
		}
	}
	data, present := entries["data"]
	if !present {
		return refuse("thiếu data")
	}
	encoded, err := storage.Encode(data, storageValueLimit)
	if err != nil {
		return nil, valueFailure(word+" (data)", err)
	}
	message.Data = encoded
	return message, value.Value{K: value.Nil}
}

// mapOf is the entries of a plain object, nil for anything else.
func mapOf(item value.Value) map[string]value.Value {
	if item.K != value.Map {
		return nil
	}
	if entries, ok := item.V.(map[string]value.Value); ok {
		if entries == nil {
			return map[string]value.Value{}
		}
		return entries
	}
	return nil
}

// messageValue is a message as observe() hands it to its function: the envelope as it was sent, with data
// decoded as a value of its own, so no two readers share an object.
func messageValue(message *storage.Message) value.Value {
	envelope := map[string]value.Value{}
	if message.HasEvent {
		envelope["event"] = value.New(message.Event)
	}
	if message.HasID {
		envelope["id"] = value.New(message.ID)
	}
	data, err := storage.Decode(message.Data)
	if err != nil {
		data = value.Value{K: value.Nil}
	}
	envelope["data"] = data
	return value.Value{K: value.Map, V: envelope}
}

// ── failures ───────────────────────────────────────────────────────────────────────────────────────

// valueFailure is the failure of a value that cannot be kept or sent: INVALID_ARGUMENT, with what and where.
func valueFailure(where string, err error) value.Value {
	var refused *storage.ValueError
	var tooLarge *storage.SizeError
	switch {
	case errors.As(err, &refused):
		return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("%s: %s; giá trị lưu được là thứ viết được thành JSON (chuỗi, số, boolean, null, mảng, object thường) và bytes (KITWORK_STORAGE.md mục 5)", where, refused.Error()))
	case errors.As(err, &tooLarge):
		return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("%s: %s (KITWORK_STORAGE.md mục 5)", where, tooLarge.Error()))
	}
	return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("%s: %v", where, err))
}

// errStorageFull is a write past the ceiling of disk() or datastore(): the write fails, nothing durable is
// pushed out (mục 9).
var errStorageFull = errors.New("storage full")

// storageFailure is the failure of a place: TOO_LARGE past its ceiling, DENIED for a path that leads outside
// the app, UNAVAILABLE when it cannot be reached, DATABASE for anything else it reports.
func storageFailure(where string, err error) value.Value {
	switch {
	case errors.Is(err, errStorageFull):
		return value.InvalidFailure(value.TooLargeCode, fmt.Sprintf("%s: %v", where, err))
	case errors.Is(err, errStorageOutside):
		return value.InvalidFailure(value.DeniedCode, fmt.Sprintf("%s: %v", where, err))
	case errors.Is(err, errStorageUnavailable):
		return value.InvalidFailure(value.UnavailableCode, fmt.Sprintf("%s: %v", where, err))
	case errors.Is(err, storage.ErrCorrupt):
		return value.InvalidFailure(value.DatabaseCode, fmt.Sprintf("%s: giá trị đã lưu không đọc được (%v)", where, err))
	}
	return value.InvalidFailure(value.DatabaseCode, fmt.Sprintf("%s: %v", where, err))
}

// errStorageUnavailable is a place the app cannot open.
var errStorageUnavailable = errors.New("storage unavailable")
