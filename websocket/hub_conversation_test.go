package websocket

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeConvChecker struct{ err error }

func (f fakeConvChecker) CheckConversationSubscription(_ context.Context, _, _ int64) error {
	return f.err
}

func TestConversationSubscribeAndPublish(t *testing.T) {
	hub := NewWSHub(nil)
	hub.SetConversationChecker(fakeConvChecker{})
	c := newTestClient(hub)

	c.handleMessage([]byte(`{"action":"subscribe","type":"conversation","id":"100200"}`))
	ack := readAck(t, c)
	require.Equal(t, "subscription_ack", ack.Type)
	require.Equal(t, "conversation", ack.Topic)
	require.True(t, ack.Ok)

	// publish into the room → client receives the frame
	hub.Publish(ConversationChannel(100200), map[string]any{
		"type": "message", "topic": "conversation", "id": "100200",
		"data": map[string]any{"kind": "plan_card"},
	})
	var frame map[string]any
	require.NoError(t, json.Unmarshal(<-c.send, &frame))
	require.Equal(t, "message", frame["type"])
	require.Equal(t, "conversation", frame["topic"])

	// unsubscribe → no longer receives
	c.handleMessage([]byte(`{"action":"unsubscribe","type":"conversation","id":"100200"}`))
	_ = readAck(t, c)
	hub.Publish(ConversationChannel(100200), map[string]any{"type": "message"})
	select {
	case <-c.send:
		t.Fatal("should not receive after unsubscribe")
	default:
	}
}

func TestConversationSubscribeForbidden(t *testing.T) {
	hub := NewWSHub(nil)
	hub.SetConversationChecker(fakeConvChecker{err: NewAckError(ErrCodeConversationForbidden, "forbidden")})
	c := newTestClient(hub)

	c.handleMessage([]byte(`{"action":"subscribe","type":"conversation","id":"100200"}`))
	ack := readAck(t, c)
	require.False(t, ack.Ok)
	require.Equal(t, ErrCodeConversationForbidden, ack.ErrorCode)
}

func TestUnsupportedSubscriptionType(t *testing.T) {
	hub := NewWSHub(nil)
	c := newTestClient(hub)
	c.handleMessage([]byte(`{"action":"subscribe","type":"banana","id":"1"}`))
	ack := readAck(t, c)
	require.False(t, ack.Ok)
	require.Equal(t, ErrCodeInvalidSubscriptionType, ack.ErrorCode)
}
