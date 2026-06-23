package websocket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type WSClient struct {
	userID     string
	deviceType string

	conn *websocket.Conn
	send chan []byte

	hub *WSHub

	subscriptions map[Channel]bool
	mu            sync.Mutex
}

type WSHub struct {
	mu sync.RWMutex

	clients map[*WSClient]bool

	// user -> device -> client
	userClients map[string]map[string]*WSClient

	channels map[Channel]map[*WSClient]bool

	taskChecker         TaskAccessChecker
	conversationChecker ConversationAccessChecker
}

type TaskAccessChecker interface {
	CheckTaskSubscription(ctx context.Context, userID int64, taskID int64) error
}

// ConversationAccessChecker validates that a user may subscribe to a
// conversation's live feed (ownership). Optional; nil = no check.
type ConversationAccessChecker interface {
	CheckConversationSubscription(ctx context.Context, userID int64, conversationID int64) error
}

// SetConversationChecker wires the conversation ownership check (Agent layer).
func (h *WSHub) SetConversationChecker(checker ConversationAccessChecker) {
	h.conversationChecker = checker
}

func NewWSHub(taskChecker TaskAccessChecker) *WSHub {

	return &WSHub{
		clients:     make(map[*WSClient]bool),
		userClients: make(map[string]map[string]*WSClient),
		channels:    make(map[Channel]map[*WSClient]bool),
		taskChecker: taskChecker,
	}
}

func (h *WSHub) AddClient(c *WSClient) {

	h.mu.Lock()
	defer h.mu.Unlock()

	devices, ok := h.userClients[c.userID]
	if !ok {
		devices = make(map[string]*WSClient)
		h.userClients[c.userID] = devices
	}

	if old, ok := devices[c.deviceType]; ok {

		log.Printf("replace ws user=%s device=%s", c.userID, c.deviceType)

		h.removeClientLocked(old)

		old.conn.Close()
	}

	h.clients[c] = true
	devices[c.deviceType] = c
}

func (h *WSHub) RemoveClient(c *WSClient) {

	h.mu.Lock()
	defer h.mu.Unlock()

	h.removeClientLocked(c)
}

func (h *WSHub) removeClientLocked(c *WSClient) {

	if _, ok := h.clients[c]; !ok {
		return
	}

	delete(h.clients, c)

	if devices, ok := h.userClients[c.userID]; ok {

		if cur, ok := devices[c.deviceType]; ok && cur == c {
			delete(devices, c.deviceType)
		}

		if len(devices) == 0 {
			delete(h.userClients, c.userID)
		}
	}

	for ch := range c.subscriptions {

		if subs, ok := h.channels[ch]; ok {

			delete(subs, c)

			if len(subs) == 0 {
				delete(h.channels, ch)
			}
		}
	}

	close(c.send)
}

func (h *WSHub) Subscribe(c *WSClient, ch Channel) {

	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.channels[ch]; !ok {

		h.channels[ch] = make(map[*WSClient]bool)
	}

	h.channels[ch][c] = true

	c.mu.Lock()
	c.subscriptions[ch] = true
	c.mu.Unlock()
}

func (h *WSHub) Unsubscribe(c *WSClient, ch Channel) {

	h.mu.Lock()
	defer h.mu.Unlock()

	if subs, ok := h.channels[ch]; ok {

		delete(subs, c)

		if len(subs) == 0 {
			delete(h.channels, ch)
		}
	}

	c.mu.Lock()
	delete(c.subscriptions, ch)
	c.mu.Unlock()
}

func (h *WSHub) Publish(ch Channel, event any) {

	h.mu.RLock()
	subs := h.channels[ch]
	h.mu.RUnlock()

	if len(subs) == 0 {
		return
	}

	msg, err := json.Marshal(event)
	if err != nil {
		return
	}

	for c := range subs {
		select {
		case c.send <- msg:
		default:
			go h.RemoveClient(c)

		}
	}
}

type WSMessage struct {
	Action string `json:"action" required:"true"`
	Type   string `json:"type" required:"true"`
	ID     string `json:"id" required:"true"`
}

type SubscriptionAck struct {
	Type      string `json:"type"`
	Topic     string `json:"topic"`
	ID        string `json:"id"`
	Action    string `json:"action,omitempty"`
	Ok        bool   `json:"ok"`
	ErrorCode string `json:"error_code,omitempty"`
	Message   string `json:"message,omitempty"`
}

const (
	ErrCodeInvalidRequest          = "INVALID_REQUEST"
	ErrCodeInvalidSubscriptionType = "INVALID_SUBSCRIPTION_TYPE"
	ErrCodeTaskNotFound            = "TASK_NOT_FOUND"
	ErrCodeTaskForbidden           = "TASK_FORBIDDEN"
	ErrCodeTaskAlreadyFinished     = "TASK_ALREADY_FINISHED"
	ErrCodeConversationNotFound    = "CONVERSATION_NOT_FOUND"
	ErrCodeConversationForbidden   = "CONVERSATION_FORBIDDEN"
)

type AckError struct {
	Code    string
	Message string
}

func (e *AckError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func newAckError(code, message string) *AckError {
	return &AckError{
		Code:    code,
		Message: message,
	}
}

// NewAckError builds a typed ack error so external checkers (e.g. the Agent
// conversation ownership check) can return precise error codes.
func NewAckError(code, message string) *AckError { return newAckError(code, message) }

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func (h *WSHub) ServeWS(w http.ResponseWriter, r *http.Request, userID, deviceType string) {

	if userID == "" || deviceType == "" {

		http.Error(w, "missing user or device", http.StatusUnauthorized)

		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}

	client := &WSClient{
		userID:        userID,
		deviceType:    deviceType,
		conn:          conn,
		send:          make(chan []byte, 256),
		hub:           h,
		subscriptions: make(map[Channel]bool),
	}

	h.AddClient(client)
	go client.writePump()
	go client.readPump()
}

func (c *WSClient) enqueueJSON(v any) error {
	msg, err := json.Marshal(v)
	if err != nil {
		return err
	}

	select {
	case c.send <- msg:
		return nil
	default:
		return errors.New("ws send buffer full")
	}
}

func (c *WSClient) sendSubscriptionAck(action, topic, id string, ok bool) {
	if err := c.enqueueJSON(SubscriptionAck{
		Type:   "subscription_ack",
		Topic:  topic,
		ID:     id,
		Action: action,
		Ok:     ok,
	}); err != nil {
		log.Printf("send subscription ack failed: action=%s topic=%s id=%s err=%v", action, topic, id, err)
	}
}

func (c *WSClient) sendSubscriptionAckError(action, topic, id, errorCode, message string) {
	if err := c.enqueueJSON(SubscriptionAck{
		Type:      "subscription_ack",
		Topic:     topic,
		ID:        id,
		Action:    action,
		Ok:        false,
		ErrorCode: errorCode,
		Message:   message,
	}); err != nil {
		log.Printf("send subscription ack error failed: action=%s topic=%s id=%s code=%s err=%v", action, topic, id, errorCode, err)
	}
}

func (c *WSClient) validateTaskSubscription(taskID string) error {
	if c.hub.taskChecker == nil {
		return nil
	}

	userID, err := strconv.ParseInt(c.userID, 10, 64)
	if err != nil {
		return newAckError(ErrCodeInvalidRequest, "invalid user id")
	}

	parsedTaskID, err := strconv.ParseInt(taskID, 10, 64)
	if err != nil {
		return newAckError(ErrCodeInvalidRequest, "invalid task id")
	}

	if err := c.hub.taskChecker.CheckTaskSubscription(context.Background(), userID, parsedTaskID); err != nil {
		return err
	}

	return nil
}

func (c *WSClient) validateConversationSubscription(id string) error {
	if c.hub.conversationChecker == nil {
		return nil
	}
	userID, err := strconv.ParseInt(c.userID, 10, 64)
	if err != nil {
		return newAckError(ErrCodeInvalidRequest, "invalid user id")
	}
	convID, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return newAckError(ErrCodeInvalidRequest, "invalid conversation id")
	}
	return c.hub.conversationChecker.CheckConversationSubscription(context.Background(), userID, convID)
}

func ackErrorFrom(err error) *AckError {
	if err == nil {
		return nil
	}
	var ackErr *AckError
	if errors.As(err, &ackErr) {
		return ackErr
	}
	return newAckError(ErrCodeInvalidRequest, err.Error())
}

func (c *WSClient) handleMessage(msg []byte) {
	var m WSMessage

	if err := json.Unmarshal(msg, &m); err != nil {
		c.sendSubscriptionAckError("", "", "", ErrCodeInvalidRequest, "invalid json")
		return
	}

	if m.Action == "" || m.Type == "" || m.ID == "" {
		c.sendSubscriptionAckError(m.Action, m.Type, m.ID, ErrCodeInvalidRequest, "missing required fields")
		return
	}

	if m.Action != "subscribe" && m.Action != "unsubscribe" {
		c.sendSubscriptionAckError(m.Action, m.Type, m.ID, ErrCodeInvalidRequest, "unsupported action")
		return
	}

	if m.Type != "task" && m.Type != "conversation" {
		c.sendSubscriptionAckError(m.Action, m.Type, m.ID, ErrCodeInvalidSubscriptionType, "unsupported subscription type")
		return
	}

	if _, err := strconv.ParseInt(m.ID, 10, 64); err != nil {
		c.sendSubscriptionAckError(m.Action, m.Type, m.ID, ErrCodeInvalidRequest, "invalid id")
		return
	}

	if m.Action == "subscribe" {
		var verr error
		switch m.Type {
		case "task":
			verr = c.validateTaskSubscription(m.ID)
		case "conversation":
			verr = c.validateConversationSubscription(m.ID)
		}
		if verr != nil {
			ackErr := ackErrorFrom(verr)
			c.sendSubscriptionAckError(m.Action, m.Type, m.ID, ackErr.Code, ackErr.Message)
			return
		}
	}

	channel := Channel(fmt.Sprintf("%s:%s", m.Type, m.ID))

	switch m.Action {
	case "subscribe":
		c.hub.Subscribe(c, channel)
		c.sendSubscriptionAck("subscribe", m.Type, m.ID, true)
	case "unsubscribe":
		c.hub.Unsubscribe(c, channel)
		c.sendSubscriptionAck("unsubscribe", m.Type, m.ID, true)
	}
}

func (c *WSClient) readPump() {

	defer func() {
		c.hub.RemoveClient(c)
		c.conn.Close()
	}()

	for {

		_, msg, err := c.conn.ReadMessage()

		if err != nil {
			log.Printf("WebSocket ReadMessage failed: %v", err)
			break
		}

		c.handleMessage(msg)
	}
}
func (c *WSClient) writePump() {

	ticker := time.NewTicker(50 * time.Second)

	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {

		select {

		case msg, ok := <-c.send:

			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))

			if !ok {

				c.conn.WriteMessage(websocket.CloseMessage, []byte{})

				return
			}

			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {

				return
			}

		case <-ticker.C:

			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))

			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {

				return
			}
		}
	}
}
