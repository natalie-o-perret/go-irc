// Package history provides IRC message history storage for the bouncer.
package history

import (
	"sync"
	"time"

	"github.com/natalie-o-perret/go-irc/internal/ringbuf"
	"github.com/natalie-o-perret/go-irc/irc"
)

// Entry is a stored IRC message with a server-side timestamp.
type Entry struct {
	Time time.Time
	Msg  *irc.Message
}

// Query describes a history query.
type Query struct {
	Before     *time.Time
	After      *time.Time
	Around     *time.Time
	AfterMsgID string
	Limit      int
}

// Store is the interface for history backends.
type Store interface {
	// Append stores a message for a given network + target.
	Append(network, target string, t time.Time, msg *irc.Message) error
	// Query retrieves messages matching the query.
	Query(network, target string, q Query) ([]*Entry, error)
	// Close releases resources.
	Close() error
}

// ---------------------------------------------------------------------------
// Memory store
// ---------------------------------------------------------------------------

const defaultLimit = 500

// MemoryStore keeps history in per-target ring buffers.
type MemoryStore struct {
	mu      sync.Mutex
	buffers map[string]*ringbuf.RingBuf[*Entry]
	limit   int
}

// NewMemoryStore creates an in-memory history store.
func NewMemoryStore(limit int) *MemoryStore {
	if limit <= 0 {
		limit = defaultLimit
	}
	return &MemoryStore{
		buffers: make(map[string]*ringbuf.RingBuf[*Entry]),
		limit:   limit,
	}
}

func (m *MemoryStore) key(network, target string) string {
	return network + "\x00" + target
}

func (m *MemoryStore) buf(k string) *ringbuf.RingBuf[*Entry] {
	if b, ok := m.buffers[k]; ok {
		return b
	}
	b := ringbuf.New[*Entry](m.limit)
	m.buffers[k] = b
	return b
}

// Append stores a message.
func (m *MemoryStore) Append(network, target string, t time.Time, msg *irc.Message) error {
	k := m.key(network, target)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.buf(k).Push(&Entry{Time: t, Msg: msg.Clone()})
	return nil
}

// Query retrieves messages for a target, filtered by time and limit.
func (m *MemoryStore) Query(network, target string, q Query) ([]*Entry, error) {
	k := m.key(network, target)
	m.mu.Lock()
	defer m.mu.Unlock()
	all := m.buf(k).Slice()

	limit := q.Limit
	if limit <= 0 {
		limit = defaultLimit
	}

	start := 0
	if q.AfterMsgID != "" {
		start = len(all)
		for i, entry := range all {
			if entry.Msg.Tags["msgid"] == q.AfterMsgID {
				start = i + 1
				break
			}
		}
	}

	var result []*Entry
	for _, e := range all[start:] {
		if q.Before != nil && !e.Time.Before(*q.Before) {
			continue
		}
		if q.After != nil && !e.Time.After(*q.After) {
			continue
		}
		result = append(result, e)
	}

	// trim to limit (most recent)
	if len(result) > limit {
		result = result[len(result)-limit:]
	}

	return result, nil
}

// Close is a no-op for the memory store.
func (m *MemoryStore) Close() error { return nil }
