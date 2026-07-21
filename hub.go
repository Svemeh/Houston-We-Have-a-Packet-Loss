package main

import "sync"

type Hub struct {
	mu       sync.Mutex
	buffer   []Sample                  // ring buffer, chronological order
	capacity int                       // max samples retained
	subs     map[chan Sample]struct{}  // active subscribers
}

func NewHub(capacity int) *Hub {
	return &Hub{
		buffer:   make([]Sample, 0, capacity),
		capacity: capacity,
		subs:     make(map[chan Sample]struct{}),
	}
}

func (h *Hub) SetInitial(samples []Sample) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(samples) > h.capacity {
		samples = samples[len(samples)-h.capacity:]
	}
	h.buffer = append(h.buffer[:0], samples...)
}

func (h *Hub) Publish(s Sample) {
	h.mu.Lock()
	h.buffer = append(h.buffer, s)
	if len(h.buffer) > h.capacity {
		h.buffer = h.buffer[len(h.buffer)-h.capacity:]
	}

	subs := make([]chan Sample, 0, len(h.subs))
	for c := range h.subs {
		subs = append(subs, c)
	}
	h.mu.Unlock()

	for _, c := range subs {
		select {
		case c <- s:
		default:
		}
	}
}

func (h *Hub) Subscribe() (ch <-chan Sample, snapshot []Sample, unsub func()) {
	h.mu.Lock()
	defer h.mu.Unlock()

	c := make(chan Sample, 16)
	h.subs[c] = struct{}{}

	snap := make([]Sample, len(h.buffer))
	copy(snap, h.buffer)

	return c, snap, func() {
		h.mu.Lock()
		if _, ok := h.subs[c]; ok {
			delete(h.subs, c)
			close(c)
		}
		h.mu.Unlock()
	}
}
