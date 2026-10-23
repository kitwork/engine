package storage

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kitwork/engine/value"
)

// segment("a:b") and segment("a", "b") are two keys (KITWORK_STORAGE.md mục 4): each part is written with
// its length, so no part can pass for two. key() reads the same for both; it is for a person, not a store.
//
// CONTROL: with Encoded writing each part without its length, three pairs met: "{memory users [a:b]} and
// {memory users [a b]} encode alike: :memory:users:a:b".
func TestKeyEncodingNeverMeets(t *testing.T) {
	users := Key{Place: Memory, Name: "users"}
	pairs := [][2]Key{
		{users.With("a:b"), users.With("a", "b")},
		{users.With("1:a"), users.With("1", "a")},
		{users.With(""), users},
		{Key{Place: Memory, Name: "users:1"}, users.With("1")},
		{Key{Place: Disk, Name: "users"}, users},
	}
	for _, pair := range pairs {
		if pair[0].Encoded() == pair[1].Encoded() {
			t.Errorf("%v and %v encode alike: %s", pair[0], pair[1], pair[0].Encoded())
		}
	}
	if got := users.With("123", "profile").Human(); got != "users:123:profile" {
		t.Errorf("Human = %q, want users:123:profile", got)
	}
	if users.With("a:b").Human() != users.With("a", "b").Human() {
		t.Errorf("the key a person reads is the same for both spellings")
	}
	base := users.With("1")
	_ = base.With("x")
	if len(base.Segments) != 1 {
		t.Errorf("With changed the key it was called on: %v", base.Segments)
	}
}

// A value comes back as it was written, and as a value of its own: two reads never share an object.
//
// CONTROL: with Encode writing a date as a number, it read back as one: "read back {\"at\":1700000000000000000, …".
func TestCodecRoundTrip(t *testing.T) {
	written := value.New(map[string]value.Value{
		"name":  value.New("Kitwork"),
		"count": value.New(3.5),
		"ok":    value.New(true),
		"none":  value.NewNil(),
		"tags":  value.New([]value.Value{value.New("a"), value.New(1.0)}),
		"bytes": value.New([]byte{0, 1, 2, 255}),
		"at":    value.New(time.Unix(1700000000, 5)),
		"inner": value.New(map[string]value.Value{"deep": value.New([]value.Value{})}),
	})
	data, err := Encode(written, 0)
	if err != nil {
		t.Fatal(err)
	}
	first, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := Decode(data)
	firstJSON, _ := first.MarshalJSON()
	writtenJSON, _ := written.MarshalJSON()
	if string(firstJSON) != string(writtenJSON) {
		t.Fatalf("read back %s, wrote %s", firstJSON, writtenJSON)
	}
	if first.Get("bytes").K != value.Bytes || first.Get("at").K != value.Time {
		t.Errorf("bytes and a date lost their kind: %v %v", first.Get("bytes").K, first.Get("at").K)
	}
	first.Map()["name"] = value.New("changed")
	if second.Get("name").Text() != "Kitwork" {
		t.Errorf("two reads share one object")
	}
}

// A function, a handle of the engine, an object that holds itself, are refused by name and place; past the
// limit is ErrTooLarge.
//
// CONTROL: with Encode writing a function as null, "Encode = <nil>, want \"không lưu được hàm ở .items[1]\"".
func TestCodecRefusesWhatJSONCannotWrite(t *testing.T) {
	function := value.NewFunc(func(...value.Value) value.Value { return value.NewNil() })
	cases := map[string]value.Value{
		"không lưu được hàm ở .items[1]":                                                       value.New(map[string]value.Value{"items": value.New([]value.Value{value.New(1.0), function})}),
		"không lưu được handle hay object của engine (database, job, routine, luồng, context)": {K: value.Proxy, V: struct{}{}},
	}
	for want, item := range cases {
		_, err := Encode(item, 0)
		var refused *ValueError
		if !errors.As(err, &refused) || err.Error() != want {
			t.Errorf("Encode = %v, want %q", err, want)
		}
	}
	self := map[string]value.Value{}
	self["me"] = value.Value{K: value.Map, V: self}
	if _, err := Encode(value.Value{K: value.Map, V: self}, 0); err == nil || !strings.Contains(err.Error(), "tự chứa mình") {
		t.Errorf("an object holding itself: %v", err)
	}
	if _, err := Encode(value.New(strings.Repeat("x", 100)), 50); !errors.Is(err, ErrTooLarge) {
		t.Errorf("a value past the limit: %v, want ErrTooLarge", err)
	}
	if _, err := Encode(value.New(strings.Repeat("x", 40)), 50); err != nil {
		t.Errorf("a value under the limit: %v", err)
	}
	if _, err := Decode([]byte{codecVersion, tagArray, 200}); !errors.Is(err, ErrCorrupt) {
		t.Errorf("damaged bytes: %v, want ErrCorrupt", err)
	}
}

// memory() forgets an expired value for every read, and when full pushes out the key used least recently.
//
// CONTROL: with Get not moving its entry to the front, the recently read "a" was pushed out instead of "b".
func TestMemoryStoreExpiresAndPushesOutTheColdest(t *testing.T) {
	now := time.Unix(1000, 0)
	store := NewMemoryStore(2, 0, func() time.Time { return now })
	store.Set("a", []byte("1"), now.Add(time.Minute))
	if at, ok := store.Expiry("a"); !ok || !at.Equal(now.Add(time.Minute)) {
		t.Fatalf("Expiry = %v %v", at, ok)
	}
	now = now.Add(time.Minute)
	if _, ok := store.Get("a"); ok {
		t.Errorf("a value read at its expiry is still there")
	}
	if store.Delete("a") {
		t.Errorf("Delete of an expired value reports one")
	}

	store.Set("a", []byte("1"), time.Time{})
	store.Set("b", []byte("2"), time.Time{})
	store.Get("a")
	store.Set("c", []byte("3"), time.Time{})
	if _, ok := store.Get("b"); ok {
		t.Errorf("b, used least recently, was kept")
	}
	if _, ok := store.Get("a"); !ok {
		t.Errorf("a, read just before, was pushed out")
	}
	if store.Len() != 2 {
		t.Errorf("Len = %d, want 2", store.Len())
	}

	bounded := NewMemoryStore(0, 10, func() time.Time { return now })
	bounded.Set("k1", []byte("1234"), time.Time{}) // 6 bytes with its key
	bounded.Set("k2", []byte("1234"), time.Time{})
	if _, ok := bounded.Get("k1"); ok {
		t.Errorf("past the bound of bytes, the coldest key stayed")
	}
}

// A send reaches the observers of its key only; a full queue closes its observer and never blocks the
// sender; a closed observer is forgotten by the hub.
//
// CONTROL: with deliver appending past the limit, "the slow observer was not closed" and its queue held 5.
func TestHubDeliversClosesTheSlowAndForgets(t *testing.T) {
	hub := NewHub(3, 2)
	woken := 0
	var mu sync.Mutex
	wake := func() { mu.Lock(); woken++; mu.Unlock() }
	room, _ := hub.Observe("room", wake)
	other, _ := hub.Observe("other", nil)
	if _, err := hub.Observe("third", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := hub.Observe("fourth", nil); !errors.Is(err, ErrObserverLimit) {
		t.Fatalf("an observer past the ceiling: %v", err)
	}
	data, _ := Encode(value.New("hi"), 0)
	if reached := hub.Send("room", &Message{Data: data}); reached != 1 {
		t.Errorf("reached %d, want the one observer of room", reached)
	}
	if messages, _ := other.Take(); len(messages) != 0 {
		t.Errorf("an observer of another key got %d messages", len(messages))
	}
	for range 4 {
		hub.Send("room", &Message{Data: data})
	}
	messages, overflowed := room.Take()
	if !overflowed || !room.Closed() || len(messages) != 0 {
		t.Fatalf("the slow observer was not closed: overflowed=%v closed=%v queued=%d", overflowed, room.Closed(), len(messages))
	}
	if hub.Observers("room") != 0 || hub.Count() != 2 {
		t.Errorf("the hub still counts the closed observer: room=%d count=%d", hub.Observers("room"), hub.Count())
	}
	if woken == 0 {
		t.Errorf("the owner was never woken")
	}
	other.Close()
	other.Close()
	if hub.Count() != 1 {
		t.Errorf("Count after closing twice = %d, want 1", hub.Count())
	}
	message := &Message{Data: data}
	if text, err := message.JSON(); err != nil || string(text) != `"hi"` {
		t.Errorf("JSON = %s %v", text, err)
	}
}

// Misses of one key at once run the function once: the first leads, the others wait for it.
//
// CONTROL: with Begin never finding the flight in the air, "a second miss leads a flight of its own".
func TestFlightsLeadOnce(t *testing.T) {
	var flights Flights
	first, leading := flights.Begin("k", "vm-1")
	if !leading || first.Owner != "vm-1" {
		t.Fatal("the first miss does not lead")
	}
	second, leading := flights.Begin("k", "vm-2")
	if leading || second != first {
		t.Fatal("a second miss leads a flight of its own")
	}
	if _, leading := flights.Begin("other", "vm-2"); !leading {
		t.Fatal("another key waits for this one")
	}
	flights.End("k", first)
	select {
	case <-second.Done():
	default:
		t.Fatal("the waiter was not released")
	}
	if _, leading := flights.Begin("k", "vm-3"); !leading {
		t.Fatal("the key stays taken after its flight ended")
	}
}

// A wait that would close a circle is refused, never recorded: vm-1 leads A and waits for B, vm-2 leads B and
// would wait for A. A wait that closes nothing is recorded, a waiter running inside the owner (here a name
// with the owner's prefix) counts as the owner, and a flight that has ended is followed no further.
//
// CONTROL: with Wait recording every wait and reporting true, "vm-2 waiting for A, which waits for B of
// vm-2, was let wait".
func TestFlightsRefuseAWaitThatClosesACircle(t *testing.T) {
	var flights Flights
	inside := func(inner, outer any) bool { return strings.HasPrefix(inner.(string), outer.(string)) }
	a, _ := flights.Begin("A", "vm-1")
	b, _ := flights.Begin("B", "vm-2")
	if !flights.Wait("vm-1", b, inside) {
		t.Fatal("vm-1 waiting for B closes no circle, and was refused")
	}
	if flights.Wait("vm-2", a, inside) {
		t.Fatal("vm-2 waiting for A, which waits for B of vm-2, was let wait")
	}
	if flights.Wait("vm-2/nested", a, inside) {
		t.Fatal("a VM nested in vm-2 waiting for A was let wait: what vm-2 leads, it leads too")
	}
	c, _ := flights.Begin("C", "vm-3")
	if !flights.Wait("vm-3", a, inside) || flights.Wait("vm-2", c, inside) {
		t.Fatal("a circle of three: vm-3 may wait for A; vm-2 then waiting for C of vm-3 closes it")
	}
	flights.Waited("vm-1")
	if !flights.Wait("vm-2", a, inside) {
		t.Fatal("once vm-1 stopped waiting, vm-2 waiting for A closes nothing, and was refused")
	}
	flights.Waited("vm-2")
	flights.Waited("vm-3")
	if !flights.Wait("vm-1", b, inside) {
		t.Fatal("vm-1 waits for B again")
	}
	flights.End("B", b)
	if !flights.Wait("vm-2", a, inside) {
		t.Fatal("B has ended: vm-1's wait for it leads nowhere, and vm-2 waiting for A was refused")
	}
}
