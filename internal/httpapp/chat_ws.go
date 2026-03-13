package httpapp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const chatDefaultRoom = "lobby"
const chatSendBuffer = 16

type chatHub struct {
	mu    sync.Mutex
	rooms map[string]map[*chatClient]struct{}
}

type chatClient struct {
	conn *websocket.Conn
	send chan chatOutgoingMessage
	room string
	user string
}

type chatIncomingMessage struct {
	Text string `json:"text"`
}

type chatOutgoingMessage struct {
	Type      string `json:"type"`
	Room      string `json:"room"`
	From      string `json:"from"`
	Text      string `json:"text,omitempty"`
	Timestamp string `json:"timestamp"`
}

var chatAnonSeq uint64

var chatWSUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(*http.Request) bool {
		return true
	},
}

func newChatHub() *chatHub {
	return &chatHub{rooms: map[string]map[*chatClient]struct{}{}}
}

func (h *chatHub) serveWS(w http.ResponseWriter, r *http.Request) {
	room := strings.TrimSpace(r.URL.Query().Get("room"))
	if room == "" {
		room = chatDefaultRoom
	}

	conn, err := chatWSUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	user := h.resolveUser(r)
	client := &chatClient{conn: conn, send: make(chan chatOutgoingMessage, chatSendBuffer), room: room, user: user}
	h.addClient(client)

	h.broadcast(room, chatOutgoingMessage{
		Type:      "system",
		Room:      room,
		From:      "system",
		Text:      fmt.Sprintf("%s joined", user),
		Timestamp: nowFn().UTC().Format(time.RFC3339),
	})

	go h.writeLoop(client)
	h.readLoop(client)
}

func (h *chatHub) resolveUser(r *http.Request) string {
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		tok, err := parseBearerToken(authHeader)
		if err == nil {
			uid, _, err := validateAccessToken(tok)
			if err == nil {
				return fmt.Sprintf("user-%d", uid)
			}
		}
	}
	if q := strings.TrimSpace(r.URL.Query().Get("user")); q != "" {
		return q
	}
	id := atomic.AddUint64(&chatAnonSeq, 1)
	return fmt.Sprintf("anon-%d", id)
}

func (h *chatHub) addClient(c *chatClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.rooms[c.room]; !ok {
		h.rooms[c.room] = map[*chatClient]struct{}{}
	}
	h.rooms[c.room][c] = struct{}{}
}

func (h *chatHub) removeClient(c *chatClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	clients, ok := h.rooms[c.room]
	if !ok {
		return
	}
	if _, exists := clients[c]; exists {
		delete(clients, c)
		close(c.send)
	}
	if len(clients) == 0 {
		delete(h.rooms, c.room)
	}
}

func (h *chatHub) readLoop(c *chatClient) {
	defer func() {
		h.removeClient(c)
		safeCloseConn(c.conn)
		h.broadcast(c.room, chatOutgoingMessage{
			Type:      "system",
			Room:      c.room,
			From:      "system",
			Text:      fmt.Sprintf("%s left", c.user),
			Timestamp: nowFn().UTC().Format(time.RFC3339),
		})
	}()

	for {
		_, payload, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		var in chatIncomingMessage
		if err := json.Unmarshal(payload, &in); err != nil {
			continue
		}
		in.Text = strings.TrimSpace(in.Text)
		if in.Text == "" {
			continue
		}
		h.broadcast(c.room, chatOutgoingMessage{
			Type:      "message",
			Room:      c.room,
			From:      c.user,
			Text:      in.Text,
			Timestamp: nowFn().UTC().Format(time.RFC3339),
		})
	}
}

func (h *chatHub) writeLoop(c *chatClient) {
	for msg := range c.send {
		if err := c.conn.WriteJSON(msg); err != nil {
			return
		}
	}
}

func (h *chatHub) broadcast(room string, msg chatOutgoingMessage) {
	h.mu.Lock()
	clients := h.rooms[room]
	for c := range clients {
		select {
		case c.send <- msg:
		default:
			delete(clients, c)
			close(c.send)
			safeCloseConn(c.conn)
		}
	}
	if len(clients) == 0 {
		delete(h.rooms, room)
	}
	h.mu.Unlock()
}

func safeCloseConn(conn *websocket.Conn) {
	if conn != nil {
		_ = conn.Close()
	}
}
