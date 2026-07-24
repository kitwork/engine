// Package sse provides a multi-channel Server-Sent Events broker and client pub/sub engine.
package sse

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// SSEClient represents an active client connection.
type SSEClient struct {
	ID             string
	SendChan       chan []byte
	Channels       []string
	MaxConnections int
}

// SendChannel returns the read-only channel for client event streaming.
func (c *SSEClient) GetSendChan() <-chan []byte {
	return c.SendChan
}

// GetID returns the unique session ID of the client connection.
func (c *SSEClient) GetID() string {
	return c.ID
}

type sseHistoryItem struct {
	id      string
	payload []byte
}

type publishRequest struct {
	channel string
	id      string
	message []byte
}

type subscribeRequest struct {
	clientID string
	channels []string
}

type unsubscribeRequest struct {
	clientID string
	channels []string
}

type sendToRequest struct {
	clientID string
	message  []byte
}

// SSEBroker manages all client connections per tenant and performs Pub/Sub routing.
type SSEBroker struct {
	clients     map[*SSEClient]bool
	clientMap   map[string]*SSEClient // clientSessionId -> *SSEClient
	channels    map[string]map[*SSEClient]bool
	history     map[string][]sseHistoryItem // channel -> ring buffer of recent events
	register    chan *SSEClient
	unregister  chan *SSEClient
	publish     chan publishRequest
	subscribe   chan subscribeRequest
	unsubscribe chan unsubscribeRequest
	sendTo      chan sendToRequest
	stopChan    chan struct{}
	mu          sync.RWMutex
}

// NewSSEBroker creates and starts a tenant-isolated event broker.
func NewSSEBroker() *SSEBroker {
	b := &SSEBroker{
		clients:     make(map[*SSEClient]bool),
		clientMap:   make(map[string]*SSEClient),
		channels:    make(map[string]map[*SSEClient]bool),
		history:     make(map[string][]sseHistoryItem),
		register:    make(chan *SSEClient),
		unregister:  make(chan *SSEClient),
		publish:     make(chan publishRequest),
		subscribe:   make(chan subscribeRequest),
		unsubscribe: make(chan unsubscribeRequest),
		sendTo:      make(chan sendToRequest),
		stopChan:    make(chan struct{}),
	}
	go b.run()
	return b
}

var sseBrokerRegistry sync.Map // identity/domain key -> *SSEBroker

// SSEBrokerFor returns or creates the shared SSEBroker for a given tenant identity key.
func SSEBrokerFor(key string) *SSEBroker {
	if b, ok := sseBrokerRegistry.Load(key); ok {
		return b.(*SSEBroker)
	}
	b := NewSSEBroker()
	if actual, loaded := sseBrokerRegistry.LoadOrStore(key, b); loaded {
		b.Stop() // lost the create race — discard ours, use the winner's
		return actual.(*SSEBroker)
	}
	return b
}

func (b *SSEBroker) run() {
	for {
		select {
		case <-b.stopChan:
			b.mu.Lock()
			for client := range b.clients {
				close(client.SendChan)
			}
			b.clients = nil
			b.clientMap = nil
			b.channels = nil
			b.history = nil
			b.mu.Unlock()
			return

		case client := <-b.register:
			b.mu.Lock()
			b.clients[client] = true
			if client.ID != "" {
				b.clientMap[client.ID] = client
			}
			for _, channel := range client.Channels {
				if b.channels[channel] == nil {
					b.channels[channel] = make(map[*SSEClient]bool)
				}
				b.channels[channel][client] = true
			}
			b.mu.Unlock()

		case client := <-b.unregister:
			b.mu.Lock()
			if _, ok := b.clients[client]; ok {
				delete(b.clients, client)
				if client.ID != "" {
					delete(b.clientMap, client.ID)
				}
				close(client.SendChan)
				for _, channel := range client.Channels {
					delete(b.channels[channel], client)
				}
			}
			b.mu.Unlock()

		case req := <-b.subscribe:
			b.mu.Lock()
			client, ok := b.clientMap[req.clientID]
			if ok {
				for _, channel := range req.channels {
					found := false
					for _, c := range client.Channels {
						if c == channel {
							found = true
							break
						}
					}
					if !found {
						client.Channels = append(client.Channels, channel)
					}
					if b.channels[channel] == nil {
						b.channels[channel] = make(map[*SSEClient]bool)
					}
					b.channels[channel][client] = true
				}
			}
			b.mu.Unlock()

		case req := <-b.unsubscribe:
			b.mu.Lock()
			client, ok := b.clientMap[req.clientID]
			if ok {
				for _, channel := range req.channels {
					if b.channels[channel] != nil {
						delete(b.channels[channel], client)
					}
					for i, c := range client.Channels {
						if c == channel {
							client.Channels = append(client.Channels[:i], client.Channels[i+1:]...)
							break
						}
					}
				}
			}
			b.mu.Unlock()

		case req := <-b.sendTo:
			b.mu.Lock()
			if client, ok := b.clientMap[req.clientID]; ok {
				select {
				case client.SendChan <- req.message:
				default:
				}
			}
			b.mu.Unlock()

		case req := <-b.publish:
			b.mu.Lock()
			if req.id != "" {
				item := sseHistoryItem{
					id:      req.id,
					payload: req.message,
				}
				b.history[req.channel] = append(b.history[req.channel], item)
				if len(b.history[req.channel]) > 100 {
					b.history[req.channel] = b.history[req.channel][1:]
				}
			}

			if clients, exists := b.channels[req.channel]; exists {
				for client := range clients {
					select {
					case client.SendChan <- req.message:
					default:
					}
				}
			}
			b.mu.Unlock()
		}
	}
}

// Register registers a client connection to the broker.
func (b *SSEBroker) Register(c *SSEClient) {
	b.mu.RLock()
	active := b.clients != nil
	b.mu.RUnlock()
	if active {
		b.register <- c
	}
}

// Unregister removes a client connection from the broker.
func (b *SSEBroker) Unregister(c *SSEClient) {
	b.mu.RLock()
	active := b.clients != nil
	b.mu.RUnlock()
	if active {
		b.unregister <- c
	}
}

// Subscribe adds channels dynamically to an active connection.
func (b *SSEBroker) Subscribe(clientID string, channels []string) {
	b.mu.RLock()
	active := b.clients != nil
	b.mu.RUnlock()
	if active {
		b.subscribe <- subscribeRequest{clientID: clientID, channels: channels}
	}
}

// Unsubscribe removes channels dynamically from an active connection.
func (b *SSEBroker) Unsubscribe(clientID string, channels []string) {
	b.mu.RLock()
	active := b.clients != nil
	b.mu.RUnlock()
	if active {
		b.unsubscribe <- unsubscribeRequest{clientID: clientID, channels: channels}
	}
}

// SendTo delivers raw event bytes to a single connection by session ID.
func (b *SSEBroker) SendTo(clientID string, message []byte) {
	b.mu.RLock()
	active := b.clients != nil
	b.mu.RUnlock()
	if active {
		b.sendTo <- sendToRequest{clientID: clientID, message: message}
	}
}

// Publish broadcasts raw event bytes to all subscribers of a channel.
func (b *SSEBroker) Publish(channel string, id string, message []byte) {
	b.mu.RLock()
	active := b.clients != nil
	b.mu.RUnlock()
	if active {
		b.publish <- publishRequest{channel: channel, id: id, message: message}
	}
}

// Stop stops the broker and cleans up all clients.
func (b *SSEBroker) Stop() {
	close(b.stopChan)
}

// ClientCount returns the number of active clients on the broker.
func (b *SSEBroker) ClientCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.clients)
}

// Replay sends all buffered events since lastEventID to the client.
func (b *SSEBroker) Replay(client *SSEClient, lastEventID string) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for _, channel := range client.Channels {
		history, exists := b.history[channel]
		if !exists {
			continue
		}

		foundIdx := -1
		for i, item := range history {
			if item.id == lastEventID {
				foundIdx = i
				break
			}
		}

		if foundIdx != -1 {
			for i := foundIdx + 1; i < len(history); i++ {
				select {
				case client.SendChan <- history[i].payload:
				default:
				}
			}
		}
	}
}

// GenerateSessionID produces a random hexadecimal session ID string.
func GenerateSessionID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("conn_%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// FormatSSEPayload formats an ID, event name, and data interface into raw SSE text bytes.
func FormatSSEPayload(id, event string, data interface{}) ([]byte, error) {
	var sb strings.Builder
	if id != "" {
		sb.WriteString(fmt.Sprintf("id: %s\n", id))
	}
	if event != "" {
		sb.WriteString(fmt.Sprintf("event: %s\n", event))
	}

	var dataStr string
	if str, ok := data.(string); ok {
		dataStr = str
	} else {
		b, err := json.Marshal(data)
		if err != nil {
			return nil, err
		}
		dataStr = string(b)
	}

	lines := strings.Split(dataStr, "\n")
	for _, line := range lines {
		sb.WriteString(fmt.Sprintf("data: %s\n", line))
	}
	sb.WriteString("\n")

	return []byte(sb.String()), nil
}
