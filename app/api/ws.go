package api

import (
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
	// Allow same origin only in production; the Caddy reverse proxy sits in front.
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
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
	log.Printf("[ws]  OWNER  upgrade  room=%s remote=%s", roomKey, r.RemoteAddr)
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
	log.Printf("[ws]  GUEST  upgrade  room=%s slug=%s remote=%s", roomKey, slug, r.RemoteAddr)
	serveWS(w, r, roomKey)
}

// serveWS upgrades the connection, registers it in the hub, and starts the read loop.
func serveWS(w http.ResponseWriter, r *http.Request, roomKey string) {
	upgradeStart := time.Now()
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[ws]  upgrade FAIL  room=%s remote=%s err=%v", roomKey, r.RemoteAddr, err)
		return
	}
	log.Printf("[ws]  upgrade OK    room=%s remote=%s elapsed=%s",
		roomKey, conn.RemoteAddr(), time.Since(upgradeStart))

	// OOM guard: reject frames larger than 512KB.
	conn.SetReadLimit(maxReadBytes)

	// Laptop-lid / silent TCP drop guard.
	conn.SetReadDeadline(time.Now().Add(pongDeadline))
	conn.SetPongHandler(func(appData string) error {
		conn.SetReadDeadline(time.Now().Add(pongDeadline))
		log.Printf("[ws]  pong   room=%s remote=%s", roomKey, conn.RemoteAddr())
		return nil
	})

	room := getOrCreateRoom(roomKey)
	room.add(conn)

	// done is closed when the read loop exits, signalling the ping goroutine
	// to terminate immediately rather than waiting up to pingInterval.
	done := make(chan struct{})

	// Ping goroutine: keep the connection alive and detect silent drops.
	go func() {
		ticker := time.NewTicker(pingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				log.Printf("[ws]  ping   room=%s remote=%s", roomKey, conn.RemoteAddr())
				conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					log.Printf("[ws]  ping  FAIL room=%s remote=%s err=%v", roomKey, conn.RemoteAddr(), err)
					return
				}
			}
		}
	}()

	// connectedAt used to log total session duration on disconnect.
	connectedAt := time.Now()

	// Read loop: broadcast every incoming message to all other room members.
	defer func() {
		close(done) // signal ping goroutine to exit immediately
		conn.Close()
		room.remove(conn)
		deleteRoomIfEmpty(roomKey)
		log.Printf("[ws]  DISCONN room=%s remote=%s session=%s",
			roomKey, conn.RemoteAddr(), time.Since(connectedAt).Round(time.Millisecond))
	}()

	for {
		readStart := time.Now()
		msgType, msg, err := conn.ReadMessage()
		if err != nil {
			// Normal close or deadline exceeded — exit cleanly.
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("[ws]  read  ERR  room=%s remote=%s err=%v", roomKey, conn.RemoteAddr(), err)
			} else {
				log.Printf("[ws]  read  CLOSE room=%s remote=%s reason=%v", roomKey, conn.RemoteAddr(), err)
			}
			return
		}
		readElapsed := time.Since(readStart)
		log.Printf("[ws]  read  MSG  room=%s remote=%s bytes=%d type=%d readWait=%s",
			roomKey, conn.RemoteAddr(), len(msg), msgType, readElapsed)

		room.broadcast(conn, msgType, msg)
	}
}
