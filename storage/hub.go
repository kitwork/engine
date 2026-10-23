package storage

import (
	"errors"
	"sync"
)

// Message is one send() (mục 7): the envelope { event, id, data } as it was sent, data encoded once by
// Encode. It is shared by every observer it reaches and never changed: each reader decodes data into a
// value of its own, and JSON is written once for all who forward it as it is.
type Message struct {
	Event    string
	ID       string
	HasEvent bool
	HasID    bool
	Data     []byte

	jsonOnce sync.Once
	json     []byte
	jsonErr  error
}

// JSON is data written as JSON, once for every reader of the message.
func (m *Message) JSON() ([]byte, error) {
	m.jsonOnce.Do(func() {
		decoded, err := Decode(m.Data)
		if err != nil {
			m.jsonErr = err
			return
		}
		m.json, m.jsonErr = decoded.MarshalJSON()
	})
	return m.json, m.jsonErr
}

// ErrObserverLimit is an observe() past the app's ceiling of observers.
var ErrObserverLimit = errors.New("storage: app has reached its ceiling of observers")

// ErrHubClosed is an observe() once the app is closing.
var ErrHubClosed = errors.New("storage: app is closing")

// Hub carries send() to observe() for one app (mục 7): observers by encoded key, at most limit of them;
// each with a queue of at most queueLimit messages. A send never waits: an observer whose queue is full is
// closed instead, so a slow one neither holds messages without end nor slows the sender. Only one process:
// a send reaches the observers of this hub.
type Hub struct {
	mu         sync.Mutex
	keys       map[string]map[*Observer]struct{}
	count      int
	limit      int
	queueLimit int
	closed     bool
}

// NewHub is an empty hub of at most limit observers, each queueing at most queueLimit messages.
func NewHub(limit, queueLimit int) *Hub {
	if queueLimit < 1 {
		queueLimit = 1
	}
	return &Hub{keys: map[string]map[*Observer]struct{}{}, limit: limit, queueLimit: queueLimit}
}

// Observer is one observe(): the messages sent to its key since it began, in the order each sender sent
// them, until it is closed. wake is called (never blocking) when a message is queued or the observer is
// closed by the hub, so its owner comes to Take them.
type Observer struct {
	hub  *Hub
	key  string
	wake func()

	mu         sync.Mutex
	queue      []*Message
	closed     bool
	overflowed bool
}

// Observe begins an observer of key.
func (h *Hub) Observe(key string, wake func()) (*Observer, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, ErrHubClosed
	}
	if h.limit > 0 && h.count >= h.limit {
		return nil, ErrObserverLimit
	}
	observer := &Observer{hub: h, key: key, wake: wake}
	observers := h.keys[key]
	if observers == nil {
		observers = map[*Observer]struct{}{}
		h.keys[key] = observers
	}
	observers[observer] = struct{}{}
	h.count++
	return observer, nil
}

// Send queues m for every observer of key, and gives how many it reached.
func (h *Hub) Send(key string, m *Message) int {
	h.mu.Lock()
	observers := make([]*Observer, 0, len(h.keys[key]))
	for observer := range h.keys[key] {
		observers = append(observers, observer)
	}
	h.mu.Unlock()
	reached := 0
	for _, observer := range observers {
		if observer.deliver(m, h.queueLimit) {
			reached++
		}
	}
	return reached
}

// Observers is how many observers key has (a test reads it).
func (h *Hub) Observers(key string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.keys[key])
}

// Count is how many observers the hub has.
func (h *Hub) Count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.count
}

// Close closes every observer, and refuses any new one.
func (h *Hub) Close() {
	h.mu.Lock()
	h.closed = true
	var observers []*Observer
	for _, set := range h.keys {
		for observer := range set {
			observers = append(observers, observer)
		}
	}
	h.mu.Unlock()
	for _, observer := range observers {
		observer.Close()
		if observer.wake != nil {
			observer.wake()
		}
	}
}

func (h *Hub) forget(observer *Observer) {
	h.mu.Lock()
	defer h.mu.Unlock()
	observers := h.keys[observer.key]
	if _, present := observers[observer]; !present {
		return
	}
	delete(observers, observer)
	if len(observers) == 0 {
		delete(h.keys, observer.key)
	}
	h.count--
}

// deliver queues m, or closes the observer when its queue is full; true when m was queued.
func (o *Observer) deliver(m *Message, limit int) bool {
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return false
	}
	queued := len(o.queue) < limit
	if queued {
		o.queue = append(o.queue, m)
	} else {
		o.overflowed, o.closed = true, true
		o.queue = nil
	}
	o.mu.Unlock()
	if !queued {
		o.hub.forget(o)
	}
	if o.wake != nil {
		o.wake()
	}
	return queued
}

// Take hands over the messages queued so far, oldest first. overflowed is true once the hub closed the
// observer because its queue was full.
func (o *Observer) Take() (messages []*Message, overflowed bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	messages, o.queue = o.queue, nil
	return messages, o.overflowed
}

// Close ends the observer: nothing more reaches it, and what was queued is dropped. Closing twice is
// closing once.
func (o *Observer) Close() {
	o.mu.Lock()
	wasOpen := !o.closed
	o.closed = true
	o.queue = nil
	o.mu.Unlock()
	if wasOpen {
		o.hub.forget(o)
	}
}

// Closed reports whether the observer is closed (by Close, or by the hub when its queue was full).
func (o *Observer) Closed() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.closed
}
