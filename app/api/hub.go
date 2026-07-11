package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"gotth/app/lib"
)

type Client struct {
	Conn *websocket.Conn
	Send chan []byte
}

type Room struct {
	mu           sync.Mutex
	clients      map[*Client]bool
	key          string
	drawingID    string
	createdAt    time.Time
	msgCount     int64
	lastElements []json.RawMessage
}

func newRoom(key string) *Room {
	drawingID := ""
	if len(key) > 5 && key[:5] == "draw:" {
		drawingID = key[5:]
	}
	r := &Room{
		clients:   make(map[*Client]bool),
		key:       key,
		drawingID: drawingID,
		createdAt: time.Now(),
	}
	r.loadFromDB()
	return r
}

func (r *Room) loadFromDB() {
	if r.drawingID == "" {
		return
	}
	var content string
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := lib.DB.QueryRowContext(ctx, "SELECT content FROM drawings WHERE id = ?", r.drawingID).Scan(&content)
	if err != nil || content == "" {
		return
	}
	var parsed struct {
		Elements []json.RawMessage `json:"elements"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		log.Printf("[hub] loadFromDB parse error for %s: %v", r.drawingID, err)
		return
	}
	if parsed.Elements != nil {
		r.lastElements = parsed.Elements
	}
}

var collabNotifier = &CollabNotifier{
	watchers: make(map[string][]chan int),
}

type CollabNotifier struct {
	mu       sync.RWMutex
	watchers map[string][]chan int
}

func (n *CollabNotifier) subscribe(drawingID string) chan int {
	ch := make(chan int, 4)
	n.mu.Lock()
	n.watchers[drawingID] = append(n.watchers[drawingID], ch)
	n.mu.Unlock()
	return ch
}

func (n *CollabNotifier) unsubscribe(drawingID string, ch chan int) {
	n.mu.Lock()
	watchers := n.watchers[drawingID]
	for i, w := range watchers {
		if w == ch {
			n.watchers[drawingID] = append(watchers[:i], watchers[i+1:]...)
			break
		}
	}
	if len(n.watchers[drawingID]) == 0 {
		delete(n.watchers, drawingID)
	}
	n.mu.Unlock()
}

func (n *CollabNotifier) notify(drawingID string, count int) {
	n.mu.RLock()
	for _, ch := range n.watchers[drawingID] {
		select {
		case ch <- count:
		default:
		}
	}
	n.mu.RUnlock()
}

func (r *Room) add(client *Client) {
	r.mu.Lock()
	r.clients[client] = true
	count := len(r.clients)
	r.mu.Unlock()
	if r.drawingID != "" {
		collabNotifier.notify(r.drawingID, count)
	}
}

var collabEndedMsg = []byte(`{"type":"COLLAB_ENDED"}`)

func (r *Room) remove(client *Client) {
	r.mu.Lock()
	if _, ok := r.clients[client]; !ok {
		r.mu.Unlock()
		return
	}
	delete(r.clients, client)
	close(client.Send)

	// If only one client remains, tell them collaboration ended
	// so they can downgrade from WS to HTTP solo mode.
	if len(r.clients) == 1 {
		for c := range r.clients {
			select {
			case c.Send <- collabEndedMsg:
			default:
				log.Printf("[hub] COLLAB_ENDED DROP key=%s remote=%s buffer full", r.key, c.Conn.RemoteAddr())
				close(c.Send)
				delete(r.clients, c)
			}
		}
	}
	count := len(r.clients)
	r.mu.Unlock()
	if r.drawingID != "" {
		collabNotifier.notify(r.drawingID, count)
	}
}

func (r *Room) sendSceneInit(client *Client) bool {
	r.mu.Lock()
	elements := r.lastElements
	r.mu.Unlock()
	msg, _ := json.Marshal(map[string]any{
		"type": "SCENE_INIT",
		"payload": map[string]any{
			"elements": elements,
		},
	})
	select {
	case client.Send <- msg:
		return true
	default:
		log.Printf("[hub] sendSceneInit DROP key=%s remote=%s buffer full", r.key, client.Conn.RemoteAddr())
		r.mu.Lock()
		close(client.Send)
		delete(r.clients, client)
		r.mu.Unlock()
		return false
	}
}

func (r *Room) handleMessage(sender *Client, msg []byte) {
	var incoming struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(msg, &incoming); err != nil {
		log.Printf("[hub] bad msg from %s: %v", sender.Conn.RemoteAddr(), err)
		return
	}
	switch incoming.Type {
	case "SCENE_UPDATE":
		var scene struct {
			Payload struct {
				Elements []json.RawMessage `json:"elements"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(msg, &scene); err != nil {
			log.Printf("[hub] SCENE_UPDATE parse error: %v", err)
			return
		}
		r.mu.Lock()
		r.lastElements = scene.Payload.Elements
		r.mu.Unlock()
		r.broadcastAllBut(sender, msg)
	case "SCENE_DELTA":
		// Pure forward. No storage, no parsing.
		r.broadcastAllBut(sender, msg)
	case "MOUSE_LOCATION":
		r.broadcastAllBut(sender, msg)
	default:
		r.broadcastAllBut(sender, msg)
	}
}

func (r *Room) broadcastAllBut(sender *Client, msg []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgCount++
	count := 0
	start := time.Now()
	for client := range r.clients {
		if client == sender {
			continue
		}
		select {
		case client.Send <- msg:
			count++
		default:
			log.Printf("[hub] bcast DROP key=%s remote=%s buffer full", r.key, client.Conn.RemoteAddr())
			close(client.Send)
			delete(r.clients, client)
		}
	}
	_ = start
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
		_ = r
		delete(hub.rooms, key)
	}
}

// HubRoomInfo is a snapshot of a single room for the admin panel.
type HubRoomInfo struct {
	Key   string
	Peers int
	Msgs  int64
	Age   string
}

// HubRooms returns a structured snapshot of all active rooms (admin panel use).
func HubRooms() []HubRoomInfo {
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	out := make([]HubRoomInfo, 0, len(hub.rooms))
	for key, r := range hub.rooms {
		peers, msgs, age := r.snapshot()
		out = append(out, HubRoomInfo{
			Key:   key,
			Peers: peers,
			Msgs:  msgs,
			Age:   age.Round(time.Second).String(),
		})
	}
	return out
}

// ShutdownHub closes all active WebSocket connections so readPump/writePump
// goroutines exit and http.Server.Shutdown() can drain cleanly.
func ShutdownHub() {
	hub.mu.Lock()
	for _, room := range hub.rooms {
		room.mu.Lock()
		for client := range room.clients {
			client.Conn.Close()
		}
		room.mu.Unlock()
	}
	hub.mu.Unlock()
}

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
