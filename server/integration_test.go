package server_test

import (
	"strings"
	"testing"
	"time"

	"github.com/natalie-o-perret/go-irc/client"
	"github.com/natalie-o-perret/go-irc/irc"
	"github.com/natalie-o-perret/go-irc/server"
)

// startTestServer starts an ircd on a random port and returns the address.
func startTestServer(t *testing.T) string {
	t.Helper()
	srv := server.New(server.Config{
		Name:    "irc.test",
		Network: "TestNet",
		Listen:  "127.0.0.1:0",
		MOTD:    "Test server",
	})
	addr, err := srv.ListenRandom()
	if err != nil {
		t.Fatalf("startTestServer: %v", err)
	}
	go srv.ServeListener()
	t.Cleanup(srv.Close)
	return addr
}

func TestClientServerHandshake(t *testing.T) {
	addr := startTestServer(t)

	welcome := make(chan string, 1)
	c := client.New(client.Config{
		Addr:           addr,
		Nick:           "testnick",
		User:           "testuser",
		RealName:       "Test User",
		ConnectTimeout: 5 * time.Second,
	})
	c.On("001", func(cl *client.Client, msg *irc.Message) {
		welcome <- msg.Trailing()
	})

	done := make(chan error, 1)
	go func() { done <- c.Connect() }()

	select {
	case text := <-welcome:
		if !strings.Contains(text, "testnick") && !strings.Contains(text, "Welcome") {
			t.Errorf("unexpected welcome text: %q", text)
		}
	case err := <-done:
		t.Fatalf("client exited before welcome: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for 001 welcome")
	}

	c.Disconnect("bye")
}

func TestClientJoinAndPrivmsg(t *testing.T) {
	addr := startTestServer(t)

	var received []string
	recv := make(chan string, 10)

	c1 := client.New(client.Config{
		Addr: addr, Nick: "nick1", User: "u1", RealName: "N1",
		ConnectTimeout: 5 * time.Second,
	})
	c2 := client.New(client.Config{
		Addr: addr, Nick: "nick2", User: "u2", RealName: "N2",
		ConnectTimeout: 5 * time.Second,
	})

	c1.On("001", func(cl *client.Client, _ *irc.Message) {
		_ = cl.Sendf(irc.JOIN, "#test")
	})
	c2.On("001", func(cl *client.Client, _ *irc.Message) {
		_ = cl.Sendf(irc.JOIN, "#test")
	})
	c2.On(irc.PRIVMSG, func(_ *client.Client, msg *irc.Message) {
		if msg.Prefix != nil && msg.Prefix.Nick == "nick1" {
			recv <- msg.Trailing()
		}
	})

	go func() { _ = c1.Connect() }()
	go func() { _ = c2.Connect() }()

	// Wait for both to join
	time.Sleep(300 * time.Millisecond)

	_ = c1.Sendf(irc.PRIVMSG, "#test", "hello from nick1")

	select {
	case text := <-recv:
		received = append(received, text)
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for PRIVMSG")
	}

	if len(received) == 0 || received[0] != "hello from nick1" {
		t.Errorf("unexpected received: %v", received)
	}

	c1.Disconnect("")
	c2.Disconnect("")
}

func TestClientReplyTag(t *testing.T) {
	addr := startTestServer(t)
	ready := make(chan struct{}, 2)
	parentIDs := make(chan string, 1)
	reply := make(chan *irc.Message, 1)

	c1 := client.New(client.Config{
		Addr: addr, Nick: "nick1", User: "u1", RealName: "N1",
		ConnectTimeout: 5 * time.Second,
	})
	c2 := client.New(client.Config{
		Addr: addr, Nick: "nick2", User: "u2", RealName: "N2",
		ConnectTimeout: 5 * time.Second,
	})
	t.Cleanup(func() {
		c1.Disconnect("")
		c2.Disconnect("")
	})

	c1.On("001", func(*client.Client, *irc.Message) { ready <- struct{}{} })
	c2.On("001", func(*client.Client, *irc.Message) { ready <- struct{}{} })
	c2.On(irc.PRIVMSG, func(cl *client.Client, msg *irc.Message) {
		if msg.Trailing() != "parent" {
			return
		}
		parentID, ok := msg.Tags.Get("msgid")
		if !ok || parentID == "" {
			t.Error("parent message has no msgid")
			return
		}
		parentIDs <- parentID
		_ = cl.Send(&irc.Message{
			Tags:    irc.Tags{"+reply": parentID},
			Command: irc.PRIVMSG,
			Params:  []string{"nick1", "child"},
		})
	})
	c1.On(irc.PRIVMSG, func(_ *client.Client, msg *irc.Message) {
		if msg.Trailing() == "child" {
			reply <- msg
		}
	})

	go func() { _ = c1.Connect() }()
	go func() { _ = c2.Connect() }()
	for range 2 {
		select {
		case <-ready:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for clients")
		}
	}

	_ = c1.Sendf(irc.PRIVMSG, "nick2", "parent")
	select {
	case msg := <-reply:
		parentID := <-parentIDs
		if value, ok := msg.Tags.Get("+reply"); !ok || value != parentID {
			t.Errorf("reply tag not relayed: %v", msg.Tags)
		}
		if value, ok := msg.Tags.Get("msgid"); !ok || value == "" || value == parentID {
			t.Errorf("reply message has no msgid: %v", msg.Tags)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for reply")
	}
}

func TestChatHistoryLatest(t *testing.T) {
	addr := startTestServer(t)
	live := make(chan *irc.Message, 3)
	c1 := client.New(client.Config{Addr: addr, Nick: "writer", User: "u1", RealName: "Writer"})
	c2 := client.New(client.Config{Addr: addr, Nick: "reader", User: "u2", RealName: "Reader"})
	t.Cleanup(func() {
		c1.Disconnect("")
		c2.Disconnect("")
	})
	c1.On("001", func(cl *client.Client, _ *irc.Message) { _ = cl.Sendf(irc.JOIN, "#history") })
	c2.On("001", func(cl *client.Client, _ *irc.Message) { _ = cl.Sendf(irc.JOIN, "#history") })
	c2.On(irc.PRIVMSG, func(_ *client.Client, msg *irc.Message) {
		if msg.Prefix != nil && msg.Prefix.Nick == "writer" {
			if _, replayed := msg.Tags.Get("batch"); !replayed {
				live <- msg
			}
		}
	})
	go func() { _ = c1.Connect() }()
	go func() { _ = c2.Connect() }()
	time.Sleep(300 * time.Millisecond)

	for _, text := range []string{"one", "two", "three"} {
		_ = c1.Sendf(irc.PRIVMSG, "#history", text)
	}
	var sent []*irc.Message
	for range 3 {
		select {
		case msg := <-live:
			sent = append(sent, msg)
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for live messages")
		}
	}

	events := make(chan *irc.Message, 4)
	c2.On("*", func(_ *client.Client, msg *irc.Message) {
		if msg.Command == irc.BATCH {
			events <- msg
			return
		}
		if _, ok := msg.Tags.Get("batch"); ok {
			events <- msg
		}
	})
	_ = c2.Sendf(irc.CHATHISTORY, "LATEST", "#history", "*", "2")

	var got []*irc.Message
	for range 4 {
		select {
		case msg := <-events:
			got = append(got, msg)
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for history batch")
		}
	}
	if got[0].Command != irc.BATCH || !strings.HasPrefix(got[0].Param(0), "+") || got[0].Param(1) != "chathistory" {
		t.Fatalf("invalid batch start: %s", got[0])
	}
	batchID := strings.TrimPrefix(got[0].Param(0), "+")
	for i, msg := range got[1:3] {
		if msg.Trailing() != []string{"two", "three"}[i] {
			t.Errorf("history[%d]: %q", i, msg.Trailing())
		}
		if msg.Tags["msgid"] != sent[i+1].Tags["msgid"] || msg.Tags["batch"] != batchID {
			t.Errorf("history[%d] tags: %v", i, msg.Tags)
		}
	}
	if got[3].Command != irc.BATCH || got[3].Param(0) != "-"+batchID {
		t.Fatalf("invalid batch end: %s", got[3])
	}
}

func TestClientTagmsg(t *testing.T) {
	addr := startTestServer(t)
	ready := make(chan struct{}, 2)
	received := make(chan *irc.Message, 1)
	c1 := client.New(client.Config{Addr: addr, Nick: "nick1", User: "u1", RealName: "N1"})
	c2 := client.New(client.Config{Addr: addr, Nick: "nick2", User: "u2", RealName: "N2"})
	t.Cleanup(func() {
		c1.Disconnect("")
		c2.Disconnect("")
	})
	c1.On("001", func(*client.Client, *irc.Message) { ready <- struct{}{} })
	c2.On("001", func(*client.Client, *irc.Message) { ready <- struct{}{} })
	c2.On(irc.TAGMSG, func(_ *client.Client, msg *irc.Message) { received <- msg })
	go func() { _ = c1.Connect() }()
	go func() { _ = c2.Connect() }()
	for range 2 {
		select {
		case <-ready:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for clients")
		}
	}

	_ = c1.Send(&irc.Message{
		Tags:    irc.Tags{"+typing": "active", "admin": "true"},
		Command: irc.TAGMSG,
		Params:  []string{"nick2"},
	})
	select {
	case msg := <-received:
		if value, ok := msg.Tags.Get("+typing"); !ok || value != "active" {
			t.Errorf("client-only tag not relayed: %v", msg.Tags)
		}
		if _, ok := msg.Tags.Get("admin"); ok {
			t.Errorf("untrusted server tag relayed: %v", msg.Tags)
		}
		if value, ok := msg.Tags.Get("msgid"); !ok || value == "" {
			t.Errorf("TAGMSG has no msgid: %v", msg.Tags)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for TAGMSG")
	}
}
