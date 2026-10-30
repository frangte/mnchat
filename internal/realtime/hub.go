// Package realtime is an in-memory, single-process event hub for WebSocket clients.
// Scale-up path: replace with Redis pub/sub so multiple API replicas share events.
package realtime

import (
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/frangte/mnchat/internal/domain"
)

const clientBuffer = 64

type Client struct {
	workspaceID int64
	userID      int64
	send        chan []byte
}

// Messages yields encoded events; it is closed when the client is dropped.
func (c *Client) Messages() <-chan []byte { return c.send }

type Hub struct {
	mu      sync.Mutex
	clients map[int64]map[*Client]struct{} // by workspace id
}

func NewHub() *Hub {
	return &Hub{clients: make(map[int64]map[*Client]struct{})}
}

func (h *Hub) Subscribe(workspaceID, userID int64) *Client {
	c := &Client{workspaceID: workspaceID, userID: userID, send: make(chan []byte, clientBuffer)}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.clients[workspaceID] == nil {
		h.clients[workspaceID] = make(map[*Client]struct{})
	}
	h.clients[workspaceID][c] = struct{}{}
	return c
}

func (h *Hub) Unsubscribe(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.removeLocked(c)
}

// Publish sends ev to every client in the workspace. Clients whose buffer is
// full are dropped rather than blocking the publisher; they refetch on reconnect.
func (h *Hub) Publish(workspaceID int64, ev domain.Event) {
	payload, err := json.Marshal(ev)
	if err != nil {
		slog.Error("encode event", "type", ev.Type, "err", err)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients[workspaceID] {
		select {
		case c.send <- payload:
		default:
			slog.Warn("dropping slow websocket client", "user_id", c.userID)
			h.removeLocked(c)
		}
	}
}

// Close drops every client; used on server shutdown.
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, set := range h.clients {
		for c := range set {
			h.removeLocked(c)
		}
	}
}

func (h *Hub) removeLocked(c *Client) {
	set := h.clients[c.workspaceID]
	if _, ok := set[c]; !ok {
		return
	}
	delete(set, c)
	if len(set) == 0 {
		delete(h.clients, c.workspaceID)
	}
	close(c.send)
}
