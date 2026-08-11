package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type sseEntry struct {
	id    int
	event string
	data  string // JSON-encoded payload
}

func formatSSEMessage(e sseEntry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "id: %d\n", e.id)
	fmt.Fprintf(&b, "event: %s\n", e.event)
	for _, line := range strings.Split(e.data, "\n") {
		fmt.Fprintf(&b, "data: %s\n", line)
	}
	b.WriteString("\n")
	return b.String()
}

// sseClient wraps one connected SSE response, serializing writes since
// both the broadcast path and this client's own heartbeat ticker write
// to it concurrently.
type sseClient struct {
	w       http.ResponseWriter
	flusher http.Flusher
	mu      sync.Mutex
}

func (c *sseClient) write(s string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := io.WriteString(c.w, s); err != nil {
		return err
	}
	c.flusher.Flush()
	return nil
}

// SSEHub owns the event ring buffer and the set of connected clients.
type SSEHub struct {
	mu      sync.Mutex
	nextID  int
	buffer  []sseEntry
	clients map[*sseClient]struct{}
}

func newSSEHub() *SSEHub {
	return &SSEHub{clients: make(map[*sseClient]struct{})}
}

// PushEvent mirrors pushSseEvent(): assigns a monotonic id, optionally
// injects sessionId into the payload (overwriting any existing key),
// appends to the ring buffer, and broadcasts to all connected clients.
func (h *SSEHub) PushEvent(event string, data jmap, sessionID *string) {
	if data == nil {
		data = jmap{}
	}
	if sessionID != nil {
		data["sessionId"] = *sessionID
	}
	raw, err := json.Marshal(data)
	if err != nil {
		raw = []byte("{}")
	}

	h.mu.Lock()
	h.nextID++
	entry := sseEntry{id: h.nextID, event: event, data: string(raw)}
	if len(h.buffer) >= sseBufferSize {
		h.buffer = h.buffer[1:]
	}
	h.buffer = append(h.buffer, entry)

	snapshot := make([]*sseClient, 0, len(h.clients))
	for c := range h.clients {
		snapshot = append(snapshot, c)
	}
	h.mu.Unlock()

	msg := formatSSEMessage(entry)
	for _, c := range snapshot {
		if err := c.write(msg); err != nil {
			h.removeClient(c)
		}
	}
}

func (h *SSEHub) addClient(c *sseClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[c] = struct{}{}
}

func (h *SSEHub) removeClient(c *sseClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, c)
}

func (h *SSEHub) clientCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

func (h *SSEHub) bufferSize() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.buffer)
}

// replayFrom returns buffered entries with id > lastID, for Last-Event-ID
// reconnect support.
func (h *SSEHub) replayFrom(lastID int) []sseEntry {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]sseEntry, 0)
	for _, e := range h.buffer {
		if e.id > lastID {
			out = append(out, e)
		}
	}
	return out
}

func (br *Bridge) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, jmap{"error": "Method not allowed"})
		return
	}
	if !br.auth.RequireAuth(r) {
		jsonResponse(w, http.StatusUnauthorized, jmap{"error": "Unauthorized"})
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonResponse(w, http.StatusInternalServerError, jmap{"error": "Streaming unsupported"})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	client := &sseClient{w: w, flusher: flusher}

	if lastIDHeader := r.Header.Get("Last-Event-ID"); lastIDHeader != "" {
		if lastID, err := strconv.Atoi(lastIDHeader); err == nil {
			for _, e := range br.sse.replayFrom(lastID) {
				_ = client.write(formatSSEMessage(e))
			}
		}
	}

	br.sse.addClient(client)
	logMsg("info", fmt.Sprintf("SSE client connected (total: %d)", br.sse.clientCount()))

	// Sync late-connecting clients with currently running sessions.
	for _, snap := range br.sessions.RunningSnapshot() {
		br.sse.mu.Lock()
		br.sse.nextID++
		id := br.sse.nextID
		br.sse.mu.Unlock()
		data, _ := json.Marshal(jmap{
			"state":      "running",
			"agent":      snap.Agent,
			"cwd":        snap.Cwd,
			"folderName": snap.FolderName,
			"sessionId":  snap.ID,
		})
		_ = client.write(formatSSEMessage(sseEntry{id: id, event: "session", data: string(data)}))
	}

	ticker := time.NewTicker(sseHeartbeatInterval)
	defer ticker.Stop()

	notify := r.Context().Done()
	for {
		select {
		case <-notify:
			br.sse.removeClient(client)
			logMsg("info", fmt.Sprintf("SSE client disconnected (total: %d)", br.sse.clientCount()))
			return
		case <-br.shutdownCh:
			br.sse.removeClient(client)
			return
		case <-ticker.C:
			if err := client.write(":heartbeat\n\n"); err != nil {
				br.sse.removeClient(client)
				return
			}
		}
	}
}
