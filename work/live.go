package work

import (
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/kitwork/engine/value"
)

// Message represents an HTML update pushed to a specific topic.
type Message struct {
	Topic string
	HTML  string
}

// Client represents an active SSE connection.
type Client chan Message

// Broker manages SSE client connections and event routing.
type Broker struct {
	sync.RWMutex
	// clients maps client channel -> set of subscribed topics
	clients map[Client]map[string]bool
}

var (
	// GlobalBroker is the default broker for SSE.
	GlobalBroker *Broker
	once         sync.Once
)

// GetBroker returns the global broker instance.
func GetBroker() *Broker {
	once.Do(func() {
		GlobalBroker = &Broker{
			clients: make(map[Client]map[string]bool),
		}
	})
	return GlobalBroker
}

// ServeHTTP handles SSE client connections.
func (b *Broker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Get topics from query params, e.g., ?topics=topic1,topic2
	topicsStr := r.URL.Query().Get("topics")
	if topicsStr == "" {
		http.Error(w, "Missing topics query parameter", http.StatusBadRequest)
		return
	}

	topicsList := strings.Split(topicsStr, ",")
	topicsMap := make(map[string]bool)
	for _, t := range topicsList {
		t = strings.TrimSpace(t)
		if t != "" {
			topicsMap[t] = true
		}
	}

	if len(topicsMap) == 0 {
		http.Error(w, "No valid topics provided", http.StatusBadRequest)
		return
	}

	// Register client
	clientChan := make(Client, 10)
	b.Lock()
	b.clients[clientChan] = topicsMap
	b.Unlock()

	defer func() {
		b.Lock()
		delete(b.clients, clientChan)
		b.Unlock()
		close(clientChan)
	}()

	// Send initial connection establish event
	fmt.Fprintf(w, "event: connected\ndata: ok\n\n")
	flusher.Flush()

	for {
		select {
		case msg, ok := <-clientChan:
			if !ok {
				return
			}
			// Write SSE format
			// event: <topic>
			// data: <single-line or multiline html>
			htmlData := strings.ReplaceAll(msg.HTML, "\n", "")
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", msg.Topic, htmlData)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// Publish broadcasts the HTML fragment to all clients subscribed to the topic.
func (b *Broker) Publish(topic string, html string) {
	b.RLock()
	defer b.RUnlock()

	msg := Message{Topic: topic, HTML: html}
	for clientChan, topics := range b.clients {
		if topics[topic] {
			select {
			case clientChan <- msg:
			default:
				// Skip client if channel buffer is full to avoid blocking
			}
		}
	}
}

// LiveStream represents the capability returned by kitwork().liveStream inside the JS VM.
type LiveStream struct {
	tenant *Tenant
}

func (l *LiveStream) Publish(topicVal value.Value, htmlVal value.Value) {
	topic := topicVal.String()
	html := htmlVal.String()
	GetBroker().Publish(topic, html)
}
