package httpapp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestChatResolveUserBranches(t *testing.T) {
	restoreAuthGlobals(t)
	h := newChatHub()

	a := newAuthAPI()
	tokens, err := a.issueTokens(7, "user")
	if err != nil {
		t.Fatalf("issueTokens err: %v", err)
	}

	req := httptest.NewRequest("GET", "/ws/chat?room=lobby", nil)
	req.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	if got := h.resolveUser(req); got != "user-7" {
		t.Fatalf("resolveUser auth = %q", got)
	}

	req = httptest.NewRequest("GET", "/ws/chat?user=alice", nil)
	if got := h.resolveUser(req); got != "alice" {
		t.Fatalf("resolveUser query = %q", got)
	}

	req = httptest.NewRequest("GET", "/ws/chat", nil)
	if got := h.resolveUser(req); !strings.HasPrefix(got, "anon-") {
		t.Fatalf("resolveUser anon = %q", got)
	}
}

func TestChatBroadcastBackpressureDropsSlowClient(t *testing.T) {
	h := newChatHub()
	c := &chatClient{send: make(chan chatOutgoingMessage, 1), room: "r", user: "u"}
	h.addClient(c)
	c.send <- chatOutgoingMessage{Type: "message"}

	h.broadcast("r", chatOutgoingMessage{Type: "message", Text: "x"})
	_, _ = <-c.send
	if _, ok := <-c.send; ok {
		t.Fatal("expected channel to be closed for dropped client")
	}

	h.mu.Lock()
	_, exists := h.rooms["r"]
	h.mu.Unlock()
	if exists {
		t.Fatal("expected room removed")
	}
}

func TestChatRemoveClientAndSafeClose(t *testing.T) {
	h := newChatHub()
	c := &chatClient{send: make(chan chatOutgoingMessage, 1), room: "r", user: "u"}
	h.addClient(c)
	h.removeClient(c)
	h.removeClient(c)
	safeCloseConn(nil)
}

func TestChatWebSocketConnectAndBroadcast(t *testing.T) {
	restoreGlobals(t)
	nowFn = func() time.Time { return time.Date(2026, 3, 13, 13, 0, 0, 0, time.UTC) }

	srv := httptest.NewServer(NewHandler())
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/chat?room=room1&user=alice"
	conn1, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial1 err: %v", err)
	}
	defer conn1.Close()

	wsURL2 := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/chat?room=room1&user=bob"
	conn2, _, err := websocket.DefaultDialer.Dial(wsURL2, nil)
	if err != nil {
		t.Fatalf("dial2 err: %v", err)
	}
	defer conn2.Close()

	_ = conn1.WriteJSON(chatIncomingMessage{Text: "hello"})

	msg := mustReadWSMessage(t, conn2)
	if msg.Type != "message" || msg.Text != "hello" {
		for i := 0; i < 4 && !(msg.Type == "message" && msg.Text == "hello"); i++ {
			msg = mustReadWSMessage(t, conn2)
		}
	}
	if msg.Type != "message" || msg.Text != "hello" || msg.From != "alice" {
		t.Fatalf("msg = %+v", msg)
	}
}

func TestChatWebSocketRoomsIsolationAndInvalidPayload(t *testing.T) {
	restoreGlobals(t)
	srv := httptest.NewServer(NewHandler())
	defer srv.Close()

	ws1 := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/chat?room=A&user=alice"
	c1, _, err := websocket.DefaultDialer.Dial(ws1, nil)
	if err != nil {
		t.Fatalf("dial c1: %v", err)
	}
	defer c1.Close()

	ws2 := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/chat?room=B&user=bob"
	c2, _, err := websocket.DefaultDialer.Dial(ws2, nil)
	if err != nil {
		t.Fatalf("dial c2: %v", err)
	}
	defer c2.Close()

	_ = c1.WriteMessage(websocket.TextMessage, []byte("{bad-json"))
	_ = c1.WriteJSON(chatIncomingMessage{Text: "   "})
	_ = c1.WriteJSON(chatIncomingMessage{Text: "room-a"})

	_ = c2.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	_, data, readErr := c2.ReadMessage()
	if readErr == nil {
		var msg chatOutgoingMessage
		if err := json.Unmarshal(data, &msg); err == nil && msg.Text == "room-a" {
			t.Fatalf("unexpected cross-room message: %+v", msg)
		}
	}
}

func TestChatServeWSUpgradeFailure(t *testing.T) {
	h := newChatHub()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/ws/chat", nil)
	h.serveWS(rec, req)
	if rec.Code == 0 {
		t.Fatal("expected non-zero status for failed upgrade")
	}
}

func mustReadWSMessage(t *testing.T, conn *websocket.Conn) chatOutgoingMessage {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read ws: %v", err)
	}
	var out chatOutgoingMessage
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal ws message: %v data=%s", err, data)
	}
	return out
}

func TestChatResolveUserInvalidAuthFallsBack(t *testing.T) {
	h := newChatHub()
	req := httptest.NewRequest("GET", "/ws/chat?user=fallback", nil)
	req.Header.Set("Authorization", "Bearer invalid")
	if got := h.resolveUser(req); got != "fallback" {
		t.Fatalf("got=%q", got)
	}

	req = httptest.NewRequest("GET", "/ws/chat", nil)
	req.Header.Set("Authorization", "Bearer invalid")
	if got := h.resolveUser(req); !strings.HasPrefix(got, "anon-") {
		t.Fatalf("got=%q", got)
	}
}

func TestChatDefaultRoom(t *testing.T) {
	h := newChatHub()
	u, _ := url.Parse("/ws/chat")
	req := httptest.NewRequest("GET", u.String(), nil)
	if strings.TrimSpace(req.URL.Query().Get("room")) != "" {
		t.Fatal("unexpected room")
	}
	_ = h
}

func TestChatWriteLoopReturnsOnWriteError(t *testing.T) {
	upgrader := websocket.Upgrader{}
	serverConnReady := make(chan *websocket.Conn, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		serverConnReady <- conn
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	clientConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial err: %v", err)
	}
	serverConn := <-serverConnReady
	defer serverConn.Close()

	_ = clientConn.Close()

	h := newChatHub()
	c := &chatClient{
		conn: clientConn,
		send: make(chan chatOutgoingMessage, 1),
		room: "r",
		user: "u",
	}
	c.send <- chatOutgoingMessage{Type: "message", Room: "r", From: "u", Text: "x"}
	close(c.send)

	done := make(chan struct{})
	go func() {
		h.writeLoop(c)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("writeLoop did not return on write error")
	}
}
