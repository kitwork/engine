package storage

import (
	"container/list"
	"sync"
	"time"
)

// MemoryStore is the RAM of memory() for one app (mục 2, 9): encoded values by key, each with the time it
// expires (zero for never), bounded by a number of keys and a number of bytes. Full, it pushes out the key
// used least recently; an expired value is gone for every read, and is dropped when it is read or reached
// at the cold end. It is safe for concurrent use.
type MemoryStore struct {
	mu       sync.Mutex
	clock    func() time.Time
	maxKeys  int
	maxBytes int
	bytes    int
	order    *list.List // front: used most recently
	items    map[string]*list.Element
}

type memoryEntry struct {
	key      string
	data     []byte
	expireAt time.Time
}

// NewMemoryStore is an empty store of at most maxKeys keys and maxBytes bytes of keys and values; clock is
// time.Now when nil (a test passes its own).
func NewMemoryStore(maxKeys, maxBytes int, clock func() time.Time) *MemoryStore {
	if clock == nil {
		clock = time.Now
	}
	return &MemoryStore{clock: clock, maxKeys: maxKeys, maxBytes: maxBytes, order: list.New(), items: map[string]*list.Element{}}
}

// Now is the store's clock.
func (m *MemoryStore) Now() time.Time { return m.clock() }

func entrySize(entry *memoryEntry) int { return len(entry.key) + len(entry.data) }

// live is the entry of key if it has not expired, dropping it if it has. Called with mu held.
func (m *MemoryStore) live(key string, now time.Time) *list.Element {
	element := m.items[key]
	if element == nil {
		return nil
	}
	entry := element.Value.(*memoryEntry)
	if !entry.expireAt.IsZero() && !now.Before(entry.expireAt) {
		m.remove(element)
		return nil
	}
	return element
}

func (m *MemoryStore) remove(element *list.Element) {
	entry := element.Value.(*memoryEntry)
	m.order.Remove(element)
	delete(m.items, entry.key)
	m.bytes -= entrySize(entry)
}

// Get is the value of key, and whether there is one that has not expired. The bytes are the store's:
// the caller decodes them, never writes them.
func (m *MemoryStore) Get(key string) ([]byte, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	element := m.live(key, m.clock())
	if element == nil {
		return nil, false
	}
	m.order.MoveToFront(element)
	return element.Value.(*memoryEntry).data, true
}

// Expiry is when the value of key expires (zero for never), and whether there is one.
func (m *MemoryStore) Expiry(key string) (time.Time, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	element := m.live(key, m.clock())
	if element == nil {
		return time.Time{}, false
	}
	return element.Value.(*memoryEntry).expireAt, true
}

// Set writes the value of key, expiring at expireAt (zero for never), then pushes out what the bounds no
// longer hold: what has expired at the cold end first, then the keys used least recently. The value
// written stays, even alone past the bounds of bytes: the size of one value has its own ceiling.
func (m *MemoryStore) Set(key string, data []byte, expireAt time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if element := m.items[key]; element != nil {
		m.remove(element)
	}
	entry := &memoryEntry{key: key, data: data, expireAt: expireAt}
	m.items[key] = m.order.PushFront(entry)
	m.bytes += entrySize(entry)
	now := m.clock()
	for element := m.order.Back(); element != nil && m.order.Len() > 1; {
		previous := element.Prev()
		cold := element.Value.(*memoryEntry)
		expired := !cold.expireAt.IsZero() && !now.Before(cold.expireAt)
		over := (m.maxKeys > 0 && m.order.Len() > m.maxKeys) || (m.maxBytes > 0 && m.bytes > m.maxBytes)
		if !expired && !over {
			break
		}
		m.remove(element)
		element = previous
	}
}

// Delete drops the value of key; true when there was one that had not expired.
func (m *MemoryStore) Delete(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	element := m.live(key, m.clock())
	if element == nil {
		return false
	}
	m.remove(element)
	return true
}

// Len is how many keys the store holds, expired ones not dropped yet included.
func (m *MemoryStore) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.order.Len()
}
