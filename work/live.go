package work

import (
	livehelper "github.com/kitwork/engine/utilities/live"
	"github.com/kitwork/engine/value"
)

// Message represents an HTML update pushed to a specific topic.
type Message = livehelper.Message

// Client represents an active SSE connection.
type Client = livehelper.Client

// Broker manages SSE client connections and event routing.
type Broker = livehelper.Broker

// GetBroker returns the global broker instance.
func GetBroker() *Broker {
	return livehelper.GetBroker()
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
