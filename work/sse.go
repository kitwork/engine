package work

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/kitwork/engine/value"
)

// SSEClient represents an active client connection
type SSEClient struct {
	id             string
	sendChan       chan []byte
	channels       []string
	maxConnections int
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

// SSEBroker manages all client connections per tenant and performs Pub/Sub routing
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

// NewSSEBroker creates and starts a tenant-isolated event broker
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

func (b *SSEBroker) run() {
	for {
		select {
		case <-b.stopChan:
			b.mu.Lock()
			for client := range b.clients {
				close(client.sendChan)
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
			if client.id != "" {
				b.clientMap[client.id] = client
			}
			for _, channel := range client.channels {
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
				if client.id != "" {
					delete(b.clientMap, client.id)
				}
				close(client.sendChan)
				for _, channel := range client.channels {
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
					for _, c := range client.channels {
						if c == channel {
							found = true
							break
						}
					}
					if !found {
						client.channels = append(client.channels, channel)
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
					for i, c := range client.channels {
						if c == channel {
							client.channels = append(client.channels[:i], client.channels[i+1:]...)
							break
						}
					}
				}
			}
			b.mu.Unlock()

		case req := <-b.sendTo:
			// Deliver to ONE connection by its session id (1-to-1), unlike publish (fan-out).
			b.mu.Lock()
			if client, ok := b.clientMap[req.clientID]; ok {
				select {
				case client.sendChan <- req.message:
				default: // non-blocking if client channel is full
				}
			}
			b.mu.Unlock()

		case req := <-b.publish:
			b.mu.Lock()
			// 1. Save to channel history buffer (max 100 items) if event has an ID
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

			// 2. Broadcast to channel subscribers
			if clients, exists := b.channels[req.channel]; exists {
				for client := range clients {
					select {
					case client.sendChan <- req.message:
					default: // non-blocking if client channel is full
					}
				}
			}
			b.mu.Unlock()
		}
	}
}

// Register registers a client to the broker
func (b *SSEBroker) Register(c *SSEClient) {
	b.mu.RLock()
	active := b.clients != nil
	b.mu.RUnlock()
	if active {
		b.register <- c
	}
}

// Unregister removes a client connection from the broker
func (b *SSEBroker) Unregister(c *SSEClient) {
	b.mu.RLock()
	active := b.clients != nil
	b.mu.RUnlock()
	if active {
		b.unregister <- c
	}
}

// Subscribe adds channels dynamically to an active connection
func (b *SSEBroker) Subscribe(clientID string, channels []string) {
	b.mu.RLock()
	active := b.clients != nil
	b.mu.RUnlock()
	if active {
		b.subscribe <- subscribeRequest{clientID: clientID, channels: channels}
	}
}

// Unsubscribe removes channels dynamically from an active connection
func (b *SSEBroker) Unsubscribe(clientID string, channels []string) {
	b.mu.RLock()
	active := b.clients != nil
	b.mu.RUnlock()
	if active {
		b.unsubscribe <- unsubscribeRequest{clientID: clientID, channels: channels}
	}
}

// SendTo delivers raw event bytes to a single connection by its session id (1-to-1).
func (b *SSEBroker) SendTo(clientID string, message []byte) {
	b.mu.RLock()
	active := b.clients != nil
	b.mu.RUnlock()
	if active {
		b.sendTo <- sendToRequest{clientID: clientID, message: message}
	}
}

// Publish broadcasts raw event bytes to a channel
func (b *SSEBroker) Publish(channel string, id string, message []byte) {
	b.mu.RLock()
	active := b.clients != nil
	b.mu.RUnlock()
	if active {
		b.publish <- publishRequest{channel: channel, id: id, message: message}
	}
}

// Stop stops the broker and cleanups all clients
func (b *SSEBroker) Stop() {
	close(b.stopChan)
}

// ClientCount returns the number of active clients on the broker
func (b *SSEBroker) ClientCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.clients)
}

// Replay sends all buffered events since lastEventID to the client
func (b *SSEBroker) Replay(client *SSEClient, lastEventID string) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for _, channel := range client.channels {
		history, exists := b.history[channel]
		if !exists {
			continue
		}

		// Find the index of the matching lastEventID
		foundIdx := -1
		for i, item := range history {
			if item.id == lastEventID {
				foundIdx = i
				break
			}
		}

		// If matched ID is found, replay all subsequent items
		if foundIdx != -1 {
			for i := foundIdx + 1; i < len(history); i++ {
				select {
				case client.sendChan <- history[i].payload:
				default:
				}
			}
		}
	}
}

// ConnectOptions specifies initial client connection settings. `channel`/`channels` is the
// canonical pub/sub key; `topic`/`topics` are accepted as back-compat aliases.
type ConnectOptions struct {
	Channel        string   `json:"channel"`
	Channels       []string `json:"channels"`
	Topic          string   `json:"topic"`  // alias for channel
	Topics         []string `json:"topics"` // alias for channels
	Retry          int      `json:"retry"`
	MaxConnections int      `json:"maxConnections"`
}

// EventOptions specifies the properties of an event payload
type EventOptions struct {
	Event string      `json:"event"`
	Data  interface{} `json:"data"`
	ID    string      `json:"id"`
}

// SseHelper is the bridge exposed to the VM
type SseHelper struct {
	tenant  *Tenant
	context *Context
}

func generateSessionID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp + pseudo random string
		return fmt.Sprintf("conn_%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// channelArg reads a channel name (or list) from a JS value: a string or an array of strings.
func channelArg(v value.Value) []string {
	var channels []string
	if v.IsArray() {
		for _, el := range v.Array() {
			if el.IsString() && el.String() != "" {
				channels = append(channels, el.String())
			}
		}
	} else if v.IsString() && v.String() != "" {
		channels = append(channels, v.String())
	}
	return channels
}

// Connect prepares sse connection context and returns immediately to release VM
func (s *SseHelper) Connect(options value.Value) value.Value {
	var opt ConnectOptions
	if err := options.To(&opt); err != nil {
		s.context.Error(value.New(fmt.Sprintf("sse.connect: invalid options: %v", err)))
		return value.Value{K: value.Nil}
	}

	// channel/channels canonical; topic/topics accepted as aliases.
	var channels []string
	if opt.Channel != "" {
		channels = append(channels, opt.Channel)
	}
	channels = append(channels, opt.Channels...)
	if opt.Topic != "" {
		channels = append(channels, opt.Topic)
	}
	channels = append(channels, opt.Topics...)

	if len(channels) == 0 {
		s.context.Error(value.New("sse.connect: must specify 'channel' or 'channels'"))
		return value.Value{K: value.Nil}
	}

	// Build the SSE client details to hand off to Go Core
	client := &SSEClient{
		id:             generateSessionID(),
		sendChan:       make(chan []byte, 20),
		channels:       channels,
		maxConnections: opt.MaxConnections,
	}

	// Set Response kind to "sse" to be intercepted outside JS VM lifecycle
	s.context.Response().Return(value.New(client), "sse", 200)

	return value.Value{K: value.Nil}
}

// Subscribe adds channels dynamically to an active connection by session id
func (s *SseHelper) Subscribe(clientIDVal value.Value, channelsVal value.Value) value.Value {
	clientID := clientIDVal.String()
	if clientID == "" {
		s.context.Error(value.New("sse.subscribe: clientID cannot be empty"))
		return value.New(false)
	}

	channels := channelArg(channelsVal)
	if len(channels) == 0 {
		s.context.Error(value.New("sse.subscribe: channels cannot be empty"))
		return value.New(false)
	}

	s.tenant.SSEBroker().Subscribe(clientID, channels)
	return value.New(true)
}

// Unsubscribe removes channels dynamically from an active connection by session id
func (s *SseHelper) Unsubscribe(clientIDVal value.Value, channelsVal value.Value) value.Value {
	clientID := clientIDVal.String()
	if clientID == "" {
		s.context.Error(value.New("sse.unsubscribe: clientID cannot be empty"))
		return value.New(false)
	}

	channels := channelArg(channelsVal)
	if len(channels) == 0 {
		s.context.Error(value.New("sse.unsubscribe: channels cannot be empty"))
		return value.New(false)
	}

	s.tenant.SSEBroker().Unsubscribe(clientID, channels)
	return value.New(true)
}

// eventPayload turns a JS event value ({ event, data, id } or a bare value) into SSE bytes.
func (s *SseHelper) eventPayload(eventVal value.Value, where string) ([]byte, bool) {
	opt := EventOptions{Event: "message"}
	if eventVal.IsMap() {
		if _, hasData := eventVal.Map()["data"]; hasData {
			if err := eventVal.To(&opt); err != nil {
				s.context.Error(value.New(fmt.Sprintf("%s: invalid event options: %v", where, err)))
				return nil, false
			}
		} else {
			opt.Data = eventVal.Interface()
		}
	} else {
		opt.Data = eventVal.Interface()
	}
	payload, err := formatSSEPayload(opt.ID, opt.Event, opt.Data)
	if err != nil {
		s.context.Error(value.New(fmt.Sprintf("%s error: %v", where, err)))
		return nil, false
	}
	return payload, true
}

// Send delivers an event to ONE specific connection by its session id (1-to-1), unlike publish
// which fans out to a channel. Pair with the clientSessionId the client received on connect.
func (s *SseHelper) Send(clientIDVal value.Value, eventVal value.Value) value.Value {
	clientID := clientIDVal.String()
	if clientID == "" {
		s.context.Error(value.New("sse.send: clientID cannot be empty"))
		return value.New(false)
	}
	payload, ok := s.eventPayload(eventVal, "sse.send")
	if !ok {
		return value.New(false)
	}
	s.tenant.SSEBroker().SendTo(clientID, payload)
	return value.New(true)
}

// Publish broadcasts event data to a specific channel (fan-out to all subscribers)
func (s *SseHelper) Publish(channelVal value.Value, eventVal value.Value) value.Value {
	channel := channelVal.String()
	if channel == "" {
		s.context.Error(value.New("sse.publish: channel cannot be empty"))
		return value.New(false)
	}

	opt := EventOptions{Event: "message"}
	if eventVal.IsMap() {
		if _, hasData := eventVal.Map()["data"]; hasData {
			if err := eventVal.To(&opt); err != nil {
				s.context.Error(value.New(fmt.Sprintf("sse.publish: invalid event options: %v", err)))
				return value.New(false)
			}
		} else {
			opt.Data = eventVal.Interface()
		}
	} else {
		opt.Data = eventVal.Interface()
	}

	payload, err := formatSSEPayload(opt.ID, opt.Event, opt.Data)
	if err != nil {
		s.context.Error(value.New(fmt.Sprintf("sse.publish error: %v", err)))
		return value.New(false)
	}

	s.tenant.SSEBroker().Publish(channel, opt.ID, payload)
	return value.New(true)
}

func formatSSEPayload(id, event string, data interface{}) ([]byte, error) {
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
