package websocket

import (
	"context"
	"encoding/json"
	"testing"
)

type stubTaskChecker struct {
	check func(ctx context.Context, userID int64, taskID int64) error
}

func (s stubTaskChecker) CheckTaskSubscription(ctx context.Context, userID int64, taskID int64) error {
	if s.check == nil {
		return nil
	}
	return s.check(ctx, userID, taskID)
}

func newTestClient(h *WSHub) *WSClient {
	return &WSClient{
		userID:        "1",
		deviceType:    "ios",
		send:          make(chan []byte, 8),
		hub:           h,
		subscriptions: make(map[Channel]bool),
	}
}

func readAck(t *testing.T, c *WSClient) SubscriptionAck {
	t.Helper()

	select {
	case raw := <-c.send:
		var ack SubscriptionAck
		if err := json.Unmarshal(raw, &ack); err != nil {
			t.Fatalf("unmarshal ack: %v", err)
		}
		return ack
	default:
		t.Fatal("expected ack message")
		return SubscriptionAck{}
	}
}

func TestHandleMessageSubscribeSendsAckAndRegistersSubscription(t *testing.T) {
	hub := NewWSHub(nil)
	client := newTestClient(hub)

	hub.AddClient(client)
	defer hub.RemoveClient(client)

	client.handleMessage([]byte(`{"action":"subscribe","type":"task","id":"123"}`))

	ack := readAck(t, client)
	if ack.Type != "subscription_ack" {
		t.Fatalf("unexpected ack type: %s", ack.Type)
	}
	if ack.Action != "subscribe" {
		t.Fatalf("unexpected ack action: %s", ack.Action)
	}
	if ack.Topic != "task" || ack.ID != "123" || !ack.Ok {
		t.Fatalf("unexpected ack payload: %+v", ack)
	}

	channel := Channel("task:123")
	if !client.subscriptions[channel] {
		t.Fatalf("subscription not recorded for channel %s", channel)
	}
	if got := len(hub.channels[channel]); got != 1 {
		t.Fatalf("expected one subscriber, got %d", got)
	}
}

func TestHandleMessageUnsubscribeSendsAckAndRemovesSubscription(t *testing.T) {
	hub := NewWSHub(nil)
	client := newTestClient(hub)

	hub.AddClient(client)
	defer hub.RemoveClient(client)

	channel := Channel("task:123")
	hub.Subscribe(client, channel)

	client.handleMessage([]byte(`{"action":"unsubscribe","type":"task","id":"123"}`))

	ack := readAck(t, client)
	if ack.Action != "unsubscribe" {
		t.Fatalf("unexpected ack action: %s", ack.Action)
	}
	if ack.Topic != "task" || ack.ID != "123" || !ack.Ok {
		t.Fatalf("unexpected ack payload: %+v", ack)
	}

	if client.subscriptions[channel] {
		t.Fatalf("subscription still recorded for channel %s", channel)
	}
	if subs := hub.channels[channel]; len(subs) != 0 {
		t.Fatalf("expected no subscribers for channel %s, got %d", channel, len(subs))
	}
}

func TestHandleMessageRepeatedSubscribeDoesNotDuplicateSubscribers(t *testing.T) {
	hub := NewWSHub(nil)
	client := newTestClient(hub)

	hub.AddClient(client)
	defer hub.RemoveClient(client)

	payload := []byte(`{"action":"subscribe","type":"task","id":"123"}`)
	client.handleMessage(payload)
	client.handleMessage(payload)

	_ = readAck(t, client)
	_ = readAck(t, client)

	channel := Channel("task:123")
	if got := len(hub.channels[channel]); got != 1 {
		t.Fatalf("expected one subscriber after repeated subscribe, got %d", got)
	}
}

func TestHandleMessageInvalidJSONReturnsInvalidRequestAck(t *testing.T) {
	hub := NewWSHub(nil)
	client := newTestClient(hub)

	hub.AddClient(client)
	defer hub.RemoveClient(client)

	client.handleMessage([]byte(`{`))

	ack := readAck(t, client)
	if ack.Ok {
		t.Fatalf("expected invalid json ack to fail: %+v", ack)
	}
	if ack.ErrorCode != ErrCodeInvalidRequest {
		t.Fatalf("unexpected error code: %s", ack.ErrorCode)
	}
}

func TestHandleMessageTaskNotFoundReturnsAckError(t *testing.T) {
	hub := NewWSHub(stubTaskChecker{
		check: func(ctx context.Context, userID int64, taskID int64) error {
			return newAckError(ErrCodeTaskNotFound, "task 123 not found")
		},
	})
	client := newTestClient(hub)

	hub.AddClient(client)
	defer hub.RemoveClient(client)

	client.handleMessage([]byte(`{"action":"subscribe","type":"task","id":"123"}`))

	ack := readAck(t, client)
	if ack.Ok {
		t.Fatalf("expected not found ack to fail: %+v", ack)
	}
	if ack.ErrorCode != ErrCodeTaskNotFound {
		t.Fatalf("unexpected error code: %s", ack.ErrorCode)
	}
	if client.subscriptions[Channel("task:123")] {
		t.Fatalf("subscription should not be recorded on not found")
	}
}

func TestHandleMessageTaskForbiddenReturnsAckError(t *testing.T) {
	hub := NewWSHub(stubTaskChecker{
		check: func(ctx context.Context, userID int64, taskID int64) error {
			return newAckError(ErrCodeTaskForbidden, "task 123 forbidden")
		},
	})
	client := newTestClient(hub)

	hub.AddClient(client)
	defer hub.RemoveClient(client)

	client.handleMessage([]byte(`{"action":"subscribe","type":"task","id":"123"}`))

	ack := readAck(t, client)
	if ack.Ok {
		t.Fatalf("expected forbidden ack to fail: %+v", ack)
	}
	if ack.ErrorCode != ErrCodeTaskForbidden {
		t.Fatalf("unexpected error code: %s", ack.ErrorCode)
	}
}

func TestHandleMessageTaskAlreadyFinishedReturnsAckError(t *testing.T) {
	hub := NewWSHub(stubTaskChecker{
		check: func(ctx context.Context, userID int64, taskID int64) error {
			return newAckError(ErrCodeTaskAlreadyFinished, "task 123 already finished")
		},
	})
	client := newTestClient(hub)

	hub.AddClient(client)
	defer hub.RemoveClient(client)

	client.handleMessage([]byte(`{"action":"subscribe","type":"task","id":"123"}`))

	ack := readAck(t, client)
	if ack.Ok {
		t.Fatalf("expected already finished ack to fail: %+v", ack)
	}
	if ack.ErrorCode != ErrCodeTaskAlreadyFinished {
		t.Fatalf("unexpected error code: %s", ack.ErrorCode)
	}
	if client.subscriptions[Channel("task:123")] {
		t.Fatalf("subscription should not be recorded when task already finished")
	}
}

func TestHandleMessageInvalidTaskIDReturnsInvalidRequestAck(t *testing.T) {
	hub := NewWSHub(nil)
	client := newTestClient(hub)

	hub.AddClient(client)
	defer hub.RemoveClient(client)

	client.handleMessage([]byte(`{"action":"subscribe","type":"task","id":"abc"}`))

	ack := readAck(t, client)
	if ack.Ok {
		t.Fatalf("expected invalid task id ack to fail: %+v", ack)
	}
	if ack.ErrorCode != ErrCodeInvalidRequest {
		t.Fatalf("unexpected error code: %s", ack.ErrorCode)
	}
}
