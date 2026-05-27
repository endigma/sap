package sap

import (
	"sync"

	sapv1 "github.com/endigma/sap/gen/sap/v1"
)

// Hub fans out emitted records to in-process subscribers.
type Hub struct {
	mu     sync.RWMutex
	subs   map[chan *sapv1.Record]struct{}
	closed bool
}

// NewHub creates an in-process record hub.
func NewHub() *Hub {
	return &Hub{subs: make(map[chan *sapv1.Record]struct{})}
}

// Subscribe registers a buffered subscriber and returns a cancel function.
func (h *Hub) Subscribe(buffer int) (<-chan *sapv1.Record, func()) {
	if buffer <= 0 {
		buffer = 256
	}
	ch := make(chan *sapv1.Record, buffer)
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		close(ch)
		return ch, func() {}
	}
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		if _, ok := h.subs[ch]; ok {
			delete(h.subs, ch)
			close(ch)
		}
		h.mu.Unlock()
	}
}

// Publish sends record to all active subscribers without blocking slow subscribers.
func (h *Hub) Publish(record *sapv1.Record) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.closed {
		return
	}
	for ch := range h.subs {
		select {
		case ch <- record:
		default:
		}
	}
}

// Close closes the hub and all active subscriber channels.
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	for ch := range h.subs {
		close(ch)
		delete(h.subs, ch)
	}
}
