package realtime

import (
	"sync"

	"github.com/kitwork/engine/capabilities"
)

type Broker struct {
	mu      sync.Mutex
	scope   capabilities.Scope
	clients map[chan string]bool
}

func NewBroker(scope capabilities.Scope) *Broker {
	return &Broker{
		scope:   scope,
		clients: make(map[chan string]bool),
	}
}

func (b *Broker) Subscribe() chan string {
	b.mu.Lock()
	defer b.mu.Unlock()

	ch := make(chan string, 10)
	b.clients[ch] = true
	return ch
}

func (b *Broker) Unsubscribe(ch chan string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if _, ok := b.clients[ch]; ok {
		delete(b.clients, ch)
		close(ch)
	}
}

func (b *Broker) Publish(msg string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for ch := range b.clients {
		select {
		case ch <- msg:
		default:
		}
	}
}
