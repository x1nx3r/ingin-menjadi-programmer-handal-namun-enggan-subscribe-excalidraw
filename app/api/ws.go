package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"

	"gotth/app/lib"
)

const (
	// Maximum size of an incoming WebSocket frame. 512KB is generous for
	// Excalidraw element patches; a 50MB malicious payload is dead on arrival.
	maxReadBytes = 512 * 1024

	// How often the server pings idle clients.
	pingInterval = 30 * time.Second

	// If no pong is received within this window, the connection is considered dead.
	// Must be > pingInterval to avoid false positives.
	pongDeadline = 60 * time.Second
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		switch origin {
		case "https://canvas.x1nx3r.dev",
			"http://localhost:3000",
			"http://localhost:5173":
			return true
		}
		return false
	},
}

// CollabEventsHandler streams SSE events for collab count changes.
// The owner connects this instead of polling collab-status.
// Route: GET /api/draw/{id}/collab-events  (auth required)
func CollabEventsHandler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !checkOwnership(r, id) {
		http.NotFound(w, r)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := collabNotifier.subscribe(id)
	defer collabNotifier.unsubscribe(id, ch)

	// Send current count immediately
	hub.mu.RLock()
	room, ok := hub.rooms["draw:"+id]
	hub.mu.RUnlock()
	count := 0
	if ok {
		room.mu.Lock()
		count = len(room.clients)
		room.mu.Unlock()
	}

	fmt.Fprintf(w, "event: count\ndata: %d\n\n", count)
	flusher.Flush()

	for {
		select {
		case count := <-ch:
			fmt.Fprintf(w, "event: count\ndata: %d\n\n", count)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// CollabStatusHandler returns the number of online collaborators for a drawing.
// Designed for the owner's page to poll and decide whether to upgrade to WebSocket.
// Route: GET /api/draw/{id}/collab-status  (auth required)
func CollabStatusHandler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	hub.mu.RLock()
	room, ok := hub.rooms["draw:"+id]
	hub.mu.RUnlock()

	online := 0
	if ok {
		room.mu.Lock()
		online = len(room.clients)
		room.mu.Unlock()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]int{"online": online})
}

// WsStatsHandler exposes a plain-text snapshot of all active hub rooms.
// Route: GET /api/ws/stats  (no auth — internal diagnostic only)
func WsStatsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(HubStats()))
}

// OwnerWSHandler handles authenticated WebSocket connections from the drawing owner.
// Route: GET /api/draw/{id}/ws
func OwnerWSHandler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !checkOwnership(r, id) {
		http.NotFound(w, r)
		return
	}
	// Room key is always keyed by drawing ID so the owner and guests
	// (who connect via slug) land in the same logical room.
	roomKey := "draw:" + id
	serveWS(w, r, roomKey)
}

// GuestWSHandler handles WebSocket connections from users with a share link.
// Any valid shared drawing can connect to receive live updates (live presentation).
// Active edit broadcasting is enforced client-side via the allow_public_edits flag.
// Route: GET /api/shared/{slug}/ws
func GuestWSHandler(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")

	// Resolve the slug to the canonical drawing ID so guests and the owner
	// share the same hub room (keyed by "draw:"+id).
	var drawingID string
	err := lib.DB.QueryRowContext(r.Context(),
		`SELECT id FROM drawings WHERE share_slug = ?`, slug,
	).Scan(&drawingID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	roomKey := "draw:" + drawingID
	serveWS(w, r, roomKey)
}

func serveWS(w http.ResponseWriter, r *http.Request, roomKey string) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[ws]  upgrade FAIL  room=%s remote=%s err=%v", roomKey, r.RemoteAddr, err)
		return
	}

	client := &Client{
		Conn: conn,
		Send: make(chan []byte, 32),
	}

	room := getOrCreateRoom(roomKey)
	room.add(client)

	// Send the current scene to the new client immediately on connect.
	// If the send buffer is full, bail before starting pumps.
	if !room.sendSceneInit(client) {
		room.remove(client)
		client.Conn.Close()
		return
	}

	// Start the write pump in a background goroutine.
	go writePump(client, roomKey, room)

	// Run the read pump in the current goroutine.
	readPump(client, roomKey, room)
}

func readPump(client *Client, roomKey string, room *Room) {
	// OOM guard: reject frames larger than 512KB.
	client.Conn.SetReadLimit(maxReadBytes)

	// Laptop-lid / silent TCP drop guard.
	client.Conn.SetReadDeadline(time.Now().Add(pongDeadline))
	client.Conn.SetPongHandler(func(string) error {
		client.Conn.SetReadDeadline(time.Now().Add(pongDeadline))
		return nil
	})

	defer func() {
		room.remove(client)
		client.Conn.Close()
		deleteRoomIfEmpty(roomKey)
	}()

	for {
		_, msg, err := client.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				log.Printf("[ws]  read  ERR  room=%s remote=%s err=%v", roomKey, client.Conn.RemoteAddr(), err)
			}
			return
		}

		room.handleMessage(client, msg)
	}
}

func writePump(client *Client, roomKey string, room *Room) {
	ticker := time.NewTicker(pingInterval)
	defer func() {
		ticker.Stop()
		client.Conn.Close()
	}()

	for {
		select {
		case msg, ok := <-client.Send:
			client.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				// The room closed the channel (e.g. straggler disconnect).
				client.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			if err := client.Conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				log.Printf("[ws]  write FAIL room=%s remote=%s err=%v", roomKey, client.Conn.RemoteAddr(), err)
				return
			}
		case <-ticker.C:
			client.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := client.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				log.Printf("[ws]  ping  FAIL room=%s remote=%s err=%v", roomKey, client.Conn.RemoteAddr(), err)
				return
			}
		}
	}
}
