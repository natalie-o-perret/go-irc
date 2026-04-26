package history_test

import (
	"testing"
	"time"

	"github.com/natalie-o-perret/go-irc/bouncer/history"
	"github.com/natalie-o-perret/go-irc/irc"
)

func makeMsg(text string) *irc.Message {
	return &irc.Message{
		Prefix:  &irc.Prefix{Nick: "test", User: "u", Host: "h"},
		Command: irc.PRIVMSG,
		Params:  []string{"#chan", text},
	}
}

func TestMemoryStoreAppendQuery(t *testing.T) {
	s := history.NewMemoryStore(100)
	now := time.Now()

	for i := range 10 {
		_ = s.Append("libera", "#go", now.Add(time.Duration(i)*time.Second), makeMsg("msg"))
	}

	entries, err := s.Query("libera", "#go", history.Query{Limit: 5})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(entries) != 5 {
		t.Errorf("expected 5 entries, got %d", len(entries))
	}
}

func TestMemoryStoreTimeBounds(t *testing.T) {
	s := history.NewMemoryStore(100)
	base := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	for i := range 10 {
		_ = s.Append("net", "#chan", base.Add(time.Duration(i)*time.Minute), makeMsg("m"))
	}

	after := base.Add(4 * time.Minute)
	entries, _ := s.Query("net", "#chan", history.Query{After: &after, Limit: 100})
	// Should only return messages with time > 4 min mark (i=5..9 = 5 entries)
	if len(entries) != 5 {
		t.Errorf("expected 5 entries after t+4m, got %d", len(entries))
	}
}

func TestMemoryStoreEmpty(t *testing.T) {
	s := history.NewMemoryStore(10)
	entries, err := s.Query("net", "#nonexistent", history.Query{Limit: 10})
	if err != nil {
		t.Fatalf("Query on empty store: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestMemoryStoreRingOverwrite(t *testing.T) {
	s := history.NewMemoryStore(3)
	for i := range 6 {
		_ = s.Append("n", "#c", time.Now(), makeMsg(string(rune('a'+i))))
	}
	entries, _ := s.Query("n", "#c", history.Query{Limit: 10})
	if len(entries) != 3 {
		t.Errorf("expected 3 entries (ring size), got %d", len(entries))
	}
}
