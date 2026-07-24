package work

import (
	"bytes"
	"testing"
	"time"

	ssehelper "github.com/kitwork/engine/helpers/sse"
)

func TestSSEPayloadFormatting(t *testing.T) {
	tests := []struct {
		name     string
		id       string
		event    string
		data     interface{}
		expected string
	}{
		{
			name:     "simple string payload",
			id:       "1",
			event:    "chat",
			data:     "hello world",
			expected: "id: 1\nevent: chat\ndata: hello world\n\n",
		},
		{
			name:     "object payload",
			id:       "2",
			event:    "status",
			data:     map[string]string{"foo": "bar"},
			expected: "id: 2\nevent: status\ndata: {\"foo\":\"bar\"}\n\n",
		},
		{
			name:     "multiline string payload",
			id:       "",
			event:    "log",
			data:     "line 1\nline 2",
			expected: "event: log\ndata: line 1\ndata: line 2\n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ssehelper.FormatSSEPayload(tt.id, tt.event, tt.data)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(got) != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, string(got))
			}
		})
	}
}

func TestSSEBrokerPubSub(t *testing.T) {
	broker := NewSSEBroker()
	defer broker.Stop()

	client1 := &SSEClient{
		SendChan: make(chan []byte, 5),
		Channels: []string{"topicA", "broadcast"},
	}
	client2 := &SSEClient{
		SendChan: make(chan []byte, 5),
		Channels: []string{"topicB", "broadcast"},
	}

	broker.Register(client1)
	broker.Register(client2)

	// Wait for register
	time.Sleep(10 * time.Millisecond)

	// Publish to topicA
	msgA := []byte("data: message for A\n\n")
	broker.Publish("topicA", "", msgA)

	// Publish to broadcast
	msgB := []byte("data: message for all\n\n")
	broker.Publish("broadcast", "", msgB)

	// Receive on client1
	select {
	case m := <-client1.SendChan:
		if !bytes.Equal(m, msgA) {
			t.Errorf("client 1 expected msgA, got %s", m)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timeout waiting for msgA on client 1")
	}

	select {
	case m := <-client1.SendChan:
		if !bytes.Equal(m, msgB) {
			t.Errorf("client 1 expected msgB, got %s", m)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timeout waiting for msgB on client 1")
	}

	// Receive on client2 (should only receive broadcast, not topicA)
	select {
	case m := <-client2.SendChan:
		if !bytes.Equal(m, msgB) {
			t.Errorf("client 2 expected msgB, got %s", m)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timeout waiting for msgB on client 2")
	}

	// Check client2 didn't receive msgA
	select {
	case m := <-client2.SendChan:
		t.Errorf("client 2 received unexpected message: %s", m)
	default:
		// success: no message waiting
	}

	// Unregister
	broker.Unregister(client1)
	broker.Unregister(client2)
}

func TestSSEHistoryReplay(t *testing.T) {
	broker := NewSSEBroker()
	defer broker.Stop()

	// Publish messages to 'chat' topic with IDs
	broker.Publish("chat", "msg1", []byte("id: msg1\ndata: hello 1\n\n"))
	broker.Publish("chat", "msg2", []byte("id: msg2\ndata: hello 2\n\n"))
	broker.Publish("chat", "msg3", []byte("id: msg3\ndata: hello 3\n\n"))

	// Let the select loop process publish requests
	time.Sleep(50 * time.Millisecond)

	// Create client registered to 'chat' topic
	client := &SSEClient{
		SendChan: make(chan []byte, 10),
		Channels: []string{"chat"},
	}
	broker.Register(client)

	// Replay from 'msg1' (should replay msg2 and msg3)
	broker.Replay(client, "msg1")

	// Read msg2
	select {
	case m := <-client.SendChan:
		if !bytes.Contains(m, []byte("hello 2")) {
			t.Errorf("expected hello 2, got %s", m)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timeout waiting for msg2 replay")
	}

	// Read msg3
	select {
	case m := <-client.SendChan:
		if !bytes.Contains(m, []byte("hello 3")) {
			t.Errorf("expected hello 3, got %s", m)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timeout waiting for msg3 replay")
	}

	// Verify no more messages in channel
	select {
	case m := <-client.SendChan:
		t.Errorf("unexpected replayed message: %s", m)
	default:
		// success
	}
}

func TestSSEClientSessionIDGeneration(t *testing.T) {
	id1 := ssehelper.GenerateSessionID()
	id2 := ssehelper.GenerateSessionID()

	if len(id1) != 32 {
		t.Errorf("expected length 32, got %d", len(id1))
	}
	if id1 == id2 {
		t.Errorf("session IDs should be unique, got duplicate: %s", id1)
	}

	for _, char := range id1 {
		isHex := (char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')
		if !isHex {
			t.Errorf("expected hex char, got %c", char)
		}
	}
}

func TestSSEBrokerDynamicSubscription(t *testing.T) {
	broker := NewSSEBroker()
	defer broker.Stop()

	clientID := "test_client_id"
	client := &SSEClient{
		ID:       clientID,
		SendChan: make(chan []byte, 10),
		Channels: []string{"general"},
	}

	broker.Register(client)
	time.Sleep(10 * time.Millisecond)

	msgGen := []byte("data: general message\n\n")
	broker.Publish("general", "", msgGen)

	select {
	case m := <-client.SendChan:
		if !bytes.Equal(m, msgGen) {
			t.Errorf("expected general message, got %s", m)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timeout waiting for general message")
	}

	broker.Subscribe(clientID, []string{"room101"})
	time.Sleep(10 * time.Millisecond)

	msgRoom := []byte("data: room101 message\n\n")
	broker.Publish("room101", "", msgRoom)

	select {
	case m := <-client.SendChan:
		if !bytes.Equal(m, msgRoom) {
			t.Errorf("expected room101 message, got %s", m)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timeout waiting for room101 message")
	}

	broker.Unsubscribe(clientID, []string{"general"})
	time.Sleep(10 * time.Millisecond)

	broker.Publish("general", "", msgGen)

	select {
	case m := <-client.SendChan:
		t.Errorf("unexpected message received after unsubscribe: %s", m)
	case <-time.After(100 * time.Millisecond):
		// success: no message received
	}
}

func TestSSEBrokerSendTo(t *testing.T) {
	broker := NewSSEBroker()
	defer broker.Stop()

	client := &SSEClient{
		ID:       "sess-1",
		SendChan: make(chan []byte, 5),
		Channels: []string{"general"},
	}
	broker.Register(client)
	time.Sleep(10 * time.Millisecond)

	msg := []byte("data: direct\n\n")
	broker.SendTo("sess-1", msg)
	select {
	case m := <-client.SendChan:
		if !bytes.Equal(m, msg) {
			t.Errorf("expected direct message, got %s", m)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timeout waiting for SendTo message")
	}

	broker.SendTo("does-not-exist", []byte("data: ghost\n\n"))
	select {
	case m := <-client.SendChan:
		t.Errorf("unexpected message for unknown session: %s", m)
	case <-time.After(50 * time.Millisecond):
		// success
	}

	broker.Unregister(client)
}

func TestSSEBrokerRegistry(t *testing.T) {
	const keyA = "id-a/site-a.vn"
	const keyB = "id-b/site-b.io"

	b1 := sseBrokerFor(keyA)
	b2 := sseBrokerFor(keyA)
	if b1 != b2 {
		t.Fatal("same identity key returned different brokers — recompile would lose live clients")
	}

	other := sseBrokerFor(keyB)
	if other == b1 {
		t.Fatal("different identity keys share a broker — tenant isolation broken")
	}

	client := &SSEClient{ID: "sess-x", SendChan: make(chan []byte, 5), Channels: []string{"general"}}
	b1.Register(client)
	time.Sleep(10 * time.Millisecond)

	sseBrokerFor(keyA).Publish("general", "1", []byte("hello"))
	select {
	case <-client.SendChan:
		// success
	case <-time.After(100 * time.Millisecond):
		t.Error("publish via re-looked-up broker did not reach the registered client")
	}

	sseBrokerFor(keyB).Publish("general", "2", []byte("leak"))
	select {
	case m := <-client.SendChan:
		t.Errorf("message leaked across identities: %s", m)
	case <-time.After(50 * time.Millisecond):
		// success: isolated
	}

	b1.Unregister(client)
	b1.Stop()
	other.Stop()
}
