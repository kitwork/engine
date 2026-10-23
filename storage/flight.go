package storage

import "sync"

// Flights is the single flight of get(() => …) (mục 6): requests that miss one key at once run the
// function once; the others wait for it, then read what it wrote. One per app, keyed by the encoded key.
// It also knows who waits for which flight, so a wait that would close a circle (A computing waits for B,
// whose computing waits for A) is refused instead of waiting for ever.
type Flights struct {
	mu      sync.Mutex
	calls   map[string]*Flight
	waiting map[any]*Flight // who waits → the flight it waits for
}

// Flight is one run of a function for a key. Owner is whoever runs it (the VM), so a call from inside the
// function for the same key is told apart from a call of another request.
type Flight struct {
	Owner any
	done  chan struct{}
}

// Done is closed when the run ends, whatever came of it.
func (f *Flight) Done() <-chan struct{} { return f.done }

func (f *Flight) ended() bool {
	select {
	case <-f.done:
		return true
	default:
		return false
	}
}

// Begin is the flight of key: a new one owned by owner, leading is true and the caller runs the function
// then calls End; or the one already running, which the caller waits for.
func (f *Flights) Begin(key string, owner any) (flight *Flight, leading bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if current := f.calls[key]; current != nil {
		return current, false
	}
	if f.calls == nil {
		f.calls = map[string]*Flight{}
	}
	flight = &Flight{Owner: owner, done: make(chan struct{})}
	f.calls[key] = flight
	return flight, true
}

// End ends the flight Begin gave its leader: the key is free for the next miss, and its waiters go on.
func (f *Flights) End(key string, flight *Flight) {
	f.mu.Lock()
	if f.calls[key] == flight {
		delete(f.calls, key)
	}
	f.mu.Unlock()
	close(flight.done)
}

// Wait records that waiter waits for flight, and reports true; or, when that wait would close a circle,
// records nothing and reports false. A circle is the flight's owner waiting, through any number of flights,
// for a flight whose owner waiter runs inside. inside(inner, outer) reports whether inner is outer or runs
// nested in it (what outer leads, inner cannot wait for). A waiter calls Waited when it stops waiting.
func (f *Flights) Wait(waiter any, flight *Flight, inside func(inner, outer any) bool) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	owner := flight.Owner
	// each step follows one wait; more steps than waits is a walk that went round
	for range len(f.waiting) + 1 {
		if inside(waiter, owner) {
			return false
		}
		var next *Flight
		for who, awaited := range f.waiting {
			if !awaited.ended() && inside(who, owner) {
				next = awaited
				break
			}
		}
		if next == nil {
			break
		}
		owner = next.Owner
	}
	if f.waiting == nil {
		f.waiting = map[any]*Flight{}
	}
	f.waiting[waiter] = flight
	return true
}

// Waited ends what Wait recorded for waiter.
func (f *Flights) Waited(waiter any) {
	f.mu.Lock()
	delete(f.waiting, waiter)
	f.mu.Unlock()
}

// Running is how many flights are in the air.
func (f *Flights) Running() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}
