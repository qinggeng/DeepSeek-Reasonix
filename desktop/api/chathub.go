// Package api provides the HTTP handler implementations for the desktop API.
package api

import (
	"sync"

	"reasonix/internal/event"
)

// StreamHub manages per-stream event channels for SSE connections.
// Each submitted prompt gets a unique streamId; the SSE handler reads from
// the associated channel until TurnDone is received.
type StreamHub struct {
	mu       sync.RWMutex
	streams  map[string]chan event.Event // streamId → buffered event channel
	bufSize  int                         // channel buffer size (default 256)
}

// NewStreamHub creates a StreamHub with the given buffer size per channel.
func NewStreamHub(bufSize int) *StreamHub {
	if bufSize <= 0 {
		bufSize = 256
	}
	return &StreamHub{
		streams: make(map[string]chan event.Event),
		bufSize: bufSize,
	}
}

// Register creates and registers a new event channel for the given streamId.
// Returns the channel; the caller should close it after TurnDone.
func (h *StreamHub) Register(streamID string) chan event.Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch := make(chan event.Event, h.bufSize)
	h.streams[streamID] = ch
	return ch
}

// Lookup returns the event channel for a streamId, or nil if not found.
func (h *StreamHub) Lookup(streamID string) <-chan event.Event {
	h.mu.RLock()
	defer h.mu.RUnlock()
	ch, ok := h.streams[streamID]
	if !ok {
		return nil
	}
	return ch
}

// Unregister removes and closes the channel for a streamId.
func (h *StreamHub) Unregister(streamID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if ch, ok := h.streams[streamID]; ok {
		delete(h.streams, streamID)
		close(ch)
	}
}

// Len returns the number of active streams.
func (h *StreamHub) Len() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.streams)
}
