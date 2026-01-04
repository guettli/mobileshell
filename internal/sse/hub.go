package sse

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Event represents an SSE event to send to clients
type Event struct {
	Type string      // Event type (e.g., "process_started", "process_updated", "process_finished")
	Data interface{} // Event data (will be JSON encoded)
}

// Client represents a single SSE client connection
type Client struct {
	ID         string
	WorkspaceID string
	ProcessID  string // Optional: for single-process subscriptions
	EventChan  chan Event
	Done       chan struct{}
}

// Hub manages SSE connections and broadcasts events
type Hub struct {
	mu            sync.RWMutex
	clients       map[string]*Client
	rateLimiters  map[string]*RateLimiter // processID -> rate limiter
}

// RateLimiter tracks rate limiting for a single process
type RateLimiter struct {
	lastUpdate time.Time
	minInterval time.Duration
}

// NewHub creates a new SSE hub
func NewHub() *Hub {
	return &Hub{
		clients:      make(map[string]*Client),
		rateLimiters: make(map[string]*RateLimiter),
	}
}

// RegisterClient registers a new SSE client
func (h *Hub) RegisterClient(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	
	h.clients[client.ID] = client
	slog.Info("SSE client registered", "clientID", client.ID, "workspaceID", client.WorkspaceID, "processID", client.ProcessID)
}

// UnregisterClient removes a client from the hub
// The client's Done channel should be closed by the handler that created the client,
// not by this method. This ensures proper cleanup order and prevents race conditions.
func (h *Hub) UnregisterClient(clientID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	
	if client, ok := h.clients[clientID]; ok {
		// Remove from the map to stop new broadcasts
		delete(h.clients, clientID)
		// Check if client is already done (optional, for logging purposes)
		select {
		case <-client.Done:
			// Client already signaled done
		default:
			// Client not yet done, handler will close Done channel
		}
		slog.Info("SSE client unregistered", "clientID", clientID)
	}
}

// BroadcastToWorkspace broadcasts an event to all clients subscribed to a workspace
func (h *Hub) BroadcastToWorkspace(workspaceID string, event Event) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	
	for _, client := range h.clients {
		if client.WorkspaceID == workspaceID && client.ProcessID == "" {
			select {
			case client.EventChan <- event:
			case <-client.Done:
				// Client disconnected
			default:
				// Channel full, skip
				slog.Warn("SSE client channel full, dropping event", "clientID", client.ID)
			}
		}
	}
}

// BroadcastToProcess broadcasts an event to all clients subscribed to a specific process
func (h *Hub) BroadcastToProcess(workspaceID, processID string, event Event) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	
	for _, client := range h.clients {
		if client.WorkspaceID == workspaceID && client.ProcessID == processID {
			select {
			case client.EventChan <- event:
			case <-client.Done:
				// Client disconnected
			default:
				// Channel full, skip
				slog.Warn("SSE client channel full, dropping event", "clientID", client.ID)
			}
		}
	}
}

// ShouldSendUpdate checks if an update should be sent for a process (rate limiting)
func (h *Hub) ShouldSendUpdate(processID string, minInterval time.Duration) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	
	limiter, exists := h.rateLimiters[processID]
	if !exists {
		limiter = &RateLimiter{
			lastUpdate: time.Time{}, // Zero time, so first update is always allowed
			minInterval: minInterval,
		}
		h.rateLimiters[processID] = limiter
	}
	
	now := time.Now()
	if now.Sub(limiter.lastUpdate) >= limiter.minInterval {
		limiter.lastUpdate = now
		return true
	}
	
	return false
}

// CleanupRateLimiters removes rate limiters for completed processes
func (h *Hub) CleanupRateLimiters(processIDs []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	
	// Create a set of active process IDs
	activeIDs := make(map[string]bool)
	for _, id := range processIDs {
		activeIDs[id] = true
	}
	
	// Remove rate limiters for processes not in the active list
	for id := range h.rateLimiters {
		if !activeIDs[id] {
			delete(h.rateLimiters, id)
		}
	}
}

// FormatSSE formats an event for Server-Sent Events protocol
func FormatSSE(event Event) ([]byte, error) {
	// Encode data as JSON
	dataJSON, err := json.Marshal(event.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal event data: %w", err)
	}
	
	// Format as SSE
	// event: <type>\ndata: <json>\n\n
	output := fmt.Sprintf("event: %s\ndata: %s\n\n", event.Type, string(dataJSON))
	return []byte(output), nil
}
