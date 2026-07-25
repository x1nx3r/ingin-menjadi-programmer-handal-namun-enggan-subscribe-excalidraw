package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/websocket"
)

func TestHubRoomCreationDoesNotPanic(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	room := getOrCreateRoom("draw:test-room")
	room2 := getOrCreateRoom("draw:test-room")

	if room != room2 {
		t.Error("getOrCreateRoom should return the same room for the same key")
	}

	room.ensureLoaded()

	client := &Client{
		Conn: nil,
		Send: make(chan []byte, 1),
	}
	room.add(client)
	if len(room.clients) != 1 {
		t.Errorf("expected 1 client, got %d", len(room.clients))
	}

	room.remove(client)
	if len(room.clients) != 0 {
		t.Errorf("expected 0 clients, got %d", len(room.clients))
	}

	// Second remove must not panic (channel already closed).
	room.remove(client)

	deleteRoomIfEmpty("draw:test-room")
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	if _, ok := hub.rooms["draw:test-room"]; ok {
		t.Error("room should have been deleted")
	}
}

func TestGuestWSHandlerRejectsUnknownSlug(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/shared/unknown/ws", nil)
	req.SetPathValue("slug", "unknown")

	GuestWSHandler(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
}

func TestOwnerWSHandlerRejectsUnauthorized(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/draw/x/ws", nil)
	req.SetPathValue("id", "x")

	OwnerWSHandler(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
}

func TestWebSocketUpgraderCheckOrigin(t *testing.T) {
	cases := []struct {
		origin string
		want   bool
	}{
		{"https://canvas.x1nx3r.dev", true},
		{"http://localhost:3000", true},
		{"http://localhost:5173", true},
		{"https://evil.com", false},
	}
	for _, tc := range cases {
		t.Run(tc.origin, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/draw/x/ws", nil)
			req.Header.Set("Origin", tc.origin)
			got := upgrader.CheckOrigin(req)
			if got != tc.want {
				t.Errorf("origin %q: got %v, want %v", tc.origin, got, tc.want)
			}
		})
	}
}

func TestClientSendChannelBehavior(t *testing.T) {
	ch := make(chan []byte, 1)
	ch <- []byte("test")
	close(ch)

	msg, ok := <-ch
	if !ok || string(msg) != "test" {
		t.Errorf("unexpected first read: %s, ok=%v", msg, ok)
	}
	_, ok = <-ch
	if ok {
		t.Error("expected closed channel to return ok=false")
	}
}

func TestWebsocketCloseMessage(t *testing.T) {
	// Just a smoke test that the CloseMessage constant is still used as intended.
	msg := websocket.CloseMessage
	if msg != 8 {
		t.Errorf("unexpected CloseMessage value: %d", msg)
	}
}
