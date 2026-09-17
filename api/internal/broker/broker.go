// Package broker fans a session's live events out to whoever is watching it.
// Postgres is the record; the broker only carries what happens while someone watches.
package broker

import (
	"sync"

	"github.com/badcodetv/bob/internal/store"
)

type Broker struct {
	mu   sync.Mutex
	subs map[string]map[chan store.Event]struct{}
}

func New() *Broker { return &Broker{subs: map[string]map[chan store.Event]struct{}{}} }

// Subscribe returns a channel of the session's events and a function to stop.
// A watcher too slow to keep up misses events rather than stalling the turn; it can
// re-read stored events from Postgres.
func (b *Broker) Subscribe(sessionID string) (<-chan store.Event, func()) {
	ch := make(chan store.Event, 256)
	b.mu.Lock()
	if b.subs[sessionID] == nil {
		b.subs[sessionID] = map[chan store.Event]struct{}{}
	}
	b.subs[sessionID][ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		delete(b.subs[sessionID], ch)
		if len(b.subs[sessionID]) == 0 {
			delete(b.subs, sessionID)
		}
		b.mu.Unlock()
	}
}

func (b *Broker) Publish(e store.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs[e.SessionID] {
		select {
		case ch <- e:
		default:
		}
	}
}
