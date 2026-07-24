package work

import (
	"fmt"

	ssehelper "github.com/kitwork/engine/utilities/sse"
	"github.com/kitwork/engine/value"
)

// SSEClient represents an active client connection
type SSEClient = ssehelper.SSEClient

// SSEBroker manages all client connections per tenant and performs Pub/Sub routing
type SSEBroker = ssehelper.SSEBroker

// NewSSEBroker creates and starts a tenant-isolated event broker
func NewSSEBroker() *SSEBroker {
	return ssehelper.NewSSEBroker()
}

func sseBrokerFor(key string) *SSEBroker {
	return ssehelper.SSEBrokerFor(key)
}

// ConnectOptions specifies initial client connection settings.
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

	client := &SSEClient{
		ID:             ssehelper.GenerateSessionID(),
		SendChan:       make(chan []byte, 20),
		Channels:       channels,
		MaxConnections: opt.MaxConnections,
	}

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
	payload, err := ssehelper.FormatSSEPayload(opt.ID, opt.Event, opt.Data)
	if err != nil {
		s.context.Error(value.New(fmt.Sprintf("%s error: %v", where, err)))
		return nil, false
	}
	return payload, true
}

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

	payload, err := ssehelper.FormatSSEPayload(opt.ID, opt.Event, opt.Data)
	if err != nil {
		s.context.Error(value.New(fmt.Sprintf("sse.publish error: %v", err)))
		return value.New(false)
	}

	s.tenant.SSEBroker().Publish(channel, opt.ID, payload)
	return value.New(true)
}
