package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/sekia-ai/sekia/pkg/protocol"
)

// EventBus fans NATS events out to SSE clients and keeps a ring buffer of recent events.
type EventBus struct {
	mu       sync.RWMutex
	clients  map[chan []byte]struct{}
	ring     [][]byte
	ringSize int
	ringPos  int
	ringLen  int
}

// NewEventBus creates an event bus with the given ring buffer size.
func NewEventBus(size int) *EventBus {
	return &EventBus{
		clients:  make(map[chan []byte]struct{}),
		ring:     make([][]byte, size),
		ringSize: size,
	}
}

// Publish adds an event to the ring buffer and fans it out to all SSE clients.
func (eb *EventBus) Publish(data []byte) {
	eb.mu.Lock()
	eb.ring[eb.ringPos] = append([]byte(nil), data...)
	eb.ringPos = (eb.ringPos + 1) % eb.ringSize
	if eb.ringLen < eb.ringSize {
		eb.ringLen++
	}
	clients := make([]chan []byte, 0, len(eb.clients))
	for ch := range eb.clients {
		clients = append(clients, ch)
	}
	eb.mu.Unlock()

	for _, ch := range clients {
		select {
		case ch <- data:
		default:
			// Drop event for slow clients.
		}
	}
}

// Subscribe returns a channel that receives events and an unsubscribe function.
func (eb *EventBus) Subscribe() (chan []byte, func()) {
	ch := make(chan []byte, 64)
	eb.mu.Lock()
	eb.clients[ch] = struct{}{}
	eb.mu.Unlock()
	return ch, func() {
		eb.mu.Lock()
		delete(eb.clients, ch)
		eb.mu.Unlock()
	}
}

// Recent returns the ring buffer contents in chronological order.
func (eb *EventBus) Recent() [][]byte {
	eb.mu.RLock()
	defer eb.mu.RUnlock()

	result := make([][]byte, 0, eb.ringLen)
	start := (eb.ringPos - eb.ringLen + eb.ringSize) % eb.ringSize
	for i := 0; i < eb.ringLen; i++ {
		idx := (start + i) % eb.ringSize
		if eb.ring[idx] != nil {
			result = append(result, eb.ring[idx])
		}
	}
	return result
}

func (s *Server) handleEventStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch, unsub := s.eventBus.Subscribe()
	defer unsub()

	for {
		select {
		case data := <-ch:
			html := s.renderEventRow(data)
			fmt.Fprintf(w, "event: event\ndata: %s\n\n", html)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) renderEventRow(data []byte) string {
	var evt protocol.Event
	if err := json.Unmarshal(data, &evt); err != nil {
		return ""
	}
	payload, _ := json.Marshal(evt.Payload)
	ed := EventData{
		Time:    time.Unix(evt.Timestamp, 0).Format("2006-01-02 15:04:05"),
		Type:    evt.Type,
		Payload: string(payload),
	}
	var buf bytes.Buffer
	s.templates.ExecuteTemplate(&buf, "event_row", ed)
	return buf.String()
}
