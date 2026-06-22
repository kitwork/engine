package work

import (
	"bytes"
	"testing"
	"time"
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
			got, err := formatSSEPayload(tt.id, tt.event, tt.data)
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
		sendChan: make(chan []byte, 5),
		channels: []string{"topicA", "broadcast"},
	}
	client2 := &SSEClient{
		sendChan: make(chan []byte, 5),
		channels: []string{"topicB", "broadcast"},
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
	case m := <-client1.sendChan:
		if !bytes.Equal(m, msgA) {
			t.Errorf("client 1 expected msgA, got %s", m)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timeout waiting for msgA on client 1")
	}

	select {
	case m := <-client1.sendChan:
		if !bytes.Equal(m, msgB) {
			t.Errorf("client 1 expected msgB, got %s", m)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timeout waiting for msgB on client 1")
	}

	// Receive on client2 (should only receive broadcast, not topicA)
	select {
	case m := <-client2.sendChan:
		if !bytes.Equal(m, msgB) {
			t.Errorf("client 2 expected msgB, got %s", m)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timeout waiting for msgB on client 2")
	}

	// Check client2 didn't receive msgA
	select {
	case m := <-client2.sendChan:
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
		sendChan: make(chan []byte, 10),
		channels: []string{"chat"},
	}
	broker.Register(client)

	// Replay from 'msg1' (should replay msg2 and msg3)
	broker.Replay(client, "msg1")

	// Read msg2
	select {
	case m := <-client.sendChan:
		if !bytes.Contains(m, []byte("hello 2")) {
			t.Errorf("expected hello 2, got %s", m)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timeout waiting for msg2 replay")
	}

	// Read msg3
	select {
	case m := <-client.sendChan:
		if !bytes.Contains(m, []byte("hello 3")) {
			t.Errorf("expected hello 3, got %s", m)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timeout waiting for msg3 replay")
	}

	// Verify no more messages in channel
	select {
	case m := <-client.sendChan:
		t.Errorf("unexpected replayed message: %s", m)
	default:
		// success
	}
}

func TestSSEClientSessionIDGeneration(t *testing.T) {
	id1 := generateSessionID()
	id2 := generateSessionID()

	if len(id1) != 32 {
		t.Errorf("expected length 32, got %d", len(id1))
	}
	if id1 == id2 {
		t.Errorf("session IDs should be unique, got duplicate: %s", id1)
	}

	// Check if hex
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
		id:       clientID,
		sendChan: make(chan []byte, 10),
		channels: []string{"general"},
	}

	broker.Register(client)
	time.Sleep(10 * time.Millisecond) // wait for registration

	// Publish to General (subscribed initially)
	msgGen := []byte("data: general message\n\n")
	broker.Publish("general", "", msgGen)

	select {
	case m := <-client.sendChan:
		if !bytes.Equal(m, msgGen) {
			t.Errorf("expected general message, got %s", m)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timeout waiting for general message")
	}

	// Dynamic Subscribe to room101
	broker.Subscribe(clientID, []string{"room101"})
	time.Sleep(10 * time.Millisecond) // wait for subscription update

	msgRoom := []byte("data: room101 message\n\n")
	broker.Publish("room101", "", msgRoom)

	select {
	case m := <-client.sendChan:
		if !bytes.Equal(m, msgRoom) {
			t.Errorf("expected room101 message, got %s", m)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timeout waiting for room101 message")
	}

	// Dynamic Unsubscribe from general
	broker.Unsubscribe(clientID, []string{"general"})
	time.Sleep(10 * time.Millisecond) // wait for unsubscribe update

	broker.Publish("general", "", msgGen) // should not receive

	select {
	case m := <-client.sendChan:
		t.Errorf("unexpected message received after unsubscribe: %s", m)
	case <-time.After(100 * time.Millisecond):
		// success: no message received
	}
}

// Gap B: 1-to-1 delivery to a specific session id via SendTo (vs publish fan-out).
func TestSSEBrokerSendTo(t *testing.T) {
	broker := NewSSEBroker()
	defer broker.Stop()

	client := &SSEClient{
		id:       "sess-1",
		sendChan: make(chan []byte, 5),
		channels: []string{"general"},
	}
	broker.Register(client)
	time.Sleep(10 * time.Millisecond)

	// Deliver only to this session id.
	msg := []byte("data: direct\n\n")
	broker.SendTo("sess-1", msg)
	select {
	case m := <-client.sendChan:
		if !bytes.Equal(m, msg) {
			t.Errorf("expected direct message, got %s", m)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timeout waiting for SendTo message")
	}

	// Unknown session id → no delivery, no panic.
	broker.SendTo("does-not-exist", []byte("data: ghost\n\n"))
	select {
	case m := <-client.sendChan:
		t.Errorf("unexpected message for unknown session: %s", m)
	case <-time.After(50 * time.Millisecond):
		// success
	}

	broker.Unregister(client)
}

// TestSSEBrokerRegistry proves the shared-by-identity guarantee: the same key always returns the
// SAME broker (so a hot-reload recompile keeps publishing to the broker the client connected to),
// while different keys get isolated brokers (tenant isolation). This is the root-cause fix for SSE
// messages landing on a fresh, empty broker after a recompile.
func TestSSEBrokerRegistry(t *testing.T) {
	const keyA = "id-a/site-a.vn"
	const keyB = "id-b/site-b.io"

	b1 := sseBrokerFor(keyA)
	b2 := sseBrokerFor(keyA) // simulates a recompiled *Tenant asking again
	if b1 != b2 {
		t.Fatal("same identity key returned different brokers — recompile would lose live clients")
	}

	other := sseBrokerFor(keyB)
	if other == b1 {
		t.Fatal("different identity keys share a broker — tenant isolation broken")
	}

	// A message published to keyA must reach a client registered on the broker obtained via the
	// SAME key from a *different* lookup (the recompile scenario), and must NOT cross to keyB.
	client := &SSEClient{id: "sess-x", sendChan: make(chan []byte, 5), channels: []string{"general"}}
	b1.Register(client)
	time.Sleep(10 * time.Millisecond)

	sseBrokerFor(keyA).Publish("general", "1", []byte("hello"))
	select {
	case <-client.sendChan:
		// success: recompiled instance's publish reached the original client
	case <-time.After(100 * time.Millisecond):
		t.Error("publish via re-looked-up broker did not reach the registered client")
	}

	sseBrokerFor(keyB).Publish("general", "2", []byte("leak"))
	select {
	case m := <-client.sendChan:
		t.Errorf("message leaked across identities: %s", m)
	case <-time.After(50 * time.Millisecond):
		// success: isolated
	}

	b1.Unregister(client)
	// Brokers live in a package-level registry; stop them so the goroutines don't linger.
	b1.Stop()
	other.Stop()
	sseBrokerRegistry.Delete(keyA)
	sseBrokerRegistry.Delete(keyB)
}
