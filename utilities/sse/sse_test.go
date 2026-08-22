package sse_test

import (
	"testing"
	"time"

	"github.com/kitwork/engine/utilities/sse"
)

func TestSSEBrokerPubSub(t *testing.T) {
	broker := sse.NewSSEBroker()
	defer broker.Stop()

	client := &sse.SSEClient{
		ID:       "client_1",
		SendChan: make(chan []byte, 10),
		Channels: []string{"chat"},
	}

	broker.Register(client)
	time.Sleep(20 * time.Millisecond)

	if count := broker.ClientCount(); count != 1 {
		t.Fatalf("Expected 1 client, got %d", count)
	}

	payload, err := sse.FormatSSEPayload("msg_1", "message", "hello world")
	if err != nil {
		t.Fatalf("FormatSSEPayload failed: %v", err)
	}

	broker.Publish("chat", "msg_1", payload)

	select {
	case msg := <-client.SendChan:
		if len(msg) == 0 {
			t.Fatal("Received empty payload")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Timed out waiting for message")
	}

	broker.Unregister(client)
	time.Sleep(20 * time.Millisecond)

	if count := broker.ClientCount(); count != 0 {
		t.Fatalf("Expected 0 clients after unregister, got %d", count)
	}
}

func TestSSEBrokerStopDrainsAndRejectsLateRegistration(t *testing.T) {
	broker := sse.NewSSEBroker()
	client := &sse.SSEClient{
		ID:       "accepted",
		SendChan: make(chan []byte, 1),
		Channels: []string{"updates"},
	}
	if !broker.Register(client) {
		t.Fatal("open broker rejected a client")
	}

	deadline := time.Now().Add(time.Second)
	for broker.ClientCount() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if broker.ClientCount() != 1 {
		t.Fatal("broker did not take ownership of the accepted client")
	}

	broker.Stop()
	select {
	case _, open := <-client.SendChan:
		if open {
			t.Fatal("broker stop left the accepted client channel open")
		}
	default:
		t.Fatal("broker stop returned before closing the accepted client")
	}

	late := &sse.SSEClient{
		ID:       "late",
		SendChan: make(chan []byte, 1),
	}
	if broker.Register(late) {
		t.Fatal("stopped broker accepted a late client")
	}
	close(late.SendChan)
}
