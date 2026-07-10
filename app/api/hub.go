package api

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Room holds all active WebSocket connections for a single drawing session.
// The key is the connection pointer; the value is unused (map as a set).
type Room struct {
	mu        sync.Mutex
	clients   map[*websocket.Conn]bool
	key       string
	createdAt time.Time
	msgCount  int64 // total messages broadcast through this room
}

func newRoom(key string) *Room {
	r := &Room{
		clients:   make(map[*websocket.Conn]bool),
		key:       key,
		createdAt: time.Now(),
	}
	log.Printf("[hub] room OPEN  key=%s", key)
	return r
}

func (r *Room) add(conn *websocket.Conn) {
	r.mu.Lock()
	r.clients[conn] = true
	n := len(r.clients)
	r.mu.Unlock()
	log.Printf("[hub] conn  JOIN  key=%s peers=%d remote=%s", r.key, n, conn.RemoteAddr())
}

func (r *Room) remove(conn *websocket.Conn) {
	r.mu.Lock()
	delete(r.clients, conn)
	n := len(r.clients)
	r.mu.Unlock()
	log.Printf("[hub] conn  LEFT  key=%s peers=%d remote=%s", r.key, n, conn.RemoteAddr())
}

// broadcast writes msg to every client in the room except the sender.
// Each write happens under the lock; slow clients will delay others.
// Latency of each individual write is logged so we can catch the culprit.
func (r *Room) broadcast(sender *websocket.Conn, msgType int, msg []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.msgCount++
	count := 0
	skipped := 0
	start := time.Now()

	for conn := range r.clients {
		if conn == sender {
			skipped++
			continue
		}
		ws := time.Now()
		conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		err := conn.WriteMessage(msgType, msg)
		elapsed := time.Since(ws)
		if err != nil {
			log.Printf("[hub] bcast FAIL  key=%s remote=%s bytes=%d elapsed=%s err=%v",
				r.key, conn.RemoteAddr(), len(msg), elapsed, err)
		} else {
			count++
			if elapsed > 50*time.Millisecond {
				// Slow write — this is the latency culprit.
				log.Printf("[hub] bcast SLOW  key=%s remote=%s bytes=%d elapsed=%s",
					r.key, conn.RemoteAddr(), len(msg), elapsed)
			}
		}
	}

	total := time.Since(start)
	log.Printf("[hub] bcast DONE  key=%s msg#=%d bytes=%d sent=%d skip=%d total=%s",
		r.key, r.msgCount, len(msg), count, skipped, total)
}

func (r *Room) snapshot() (int, int64, time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.clients), r.msgCount, time.Since(r.createdAt)
}

func (r *Room) empty() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.clients) == 0
}

// hub is the global registry of active rooms, keyed by "draw:"+drawingID.
var hub = struct {
	mu    sync.RWMutex
	rooms map[string]*Room
}{
	rooms: make(map[string]*Room),
}

func getOrCreateRoom(key string) *Room {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if r, ok := hub.rooms[key]; ok {
		return r
	}
	r := newRoom(key)
	hub.rooms[key] = r
	return r
}

func deleteRoomIfEmpty(key string) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if r, ok := hub.rooms[key]; ok && r.empty() {
		log.Printf("[hub] room CLOSE key=%s msgs=%d age=%s", key, r.msgCount, time.Since(r.createdAt))
		delete(hub.rooms, key)
	}
}

// HubStats returns a human-readable snapshot of all active rooms.
func HubStats() string {
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	if len(hub.rooms) == 0 {
		return "no active rooms\n"
	}
	out := fmt.Sprintf("active rooms: %d\n", len(hub.rooms))
	for key, r := range hub.rooms {
		peers, msgs, age := r.snapshot()
		out += fmt.Sprintf("  %-36s  peers=%-3d msgs=%-6d age=%s\n", key, peers, msgs, age.Round(time.Second))
	}
	return out
}
