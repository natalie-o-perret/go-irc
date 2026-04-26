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
		cl.Sendf(irc.JOIN, "#test")
	})
	c2.On("001", func(cl *client.Client, _ *irc.Message) {
		cl.Sendf(irc.JOIN, "#test")
	})
	c2.On(irc.PRIVMSG, func(_ *client.Client, msg *irc.Message) {
		if msg.Prefix != nil && msg.Prefix.Nick == "nick1" {
			recv <- msg.Trailing()
		}
	})

	go c1.Connect()
	go c2.Connect()

	// Wait for both to join
	time.Sleep(300 * time.Millisecond)

	c1.Sendf(irc.PRIVMSG, "#test", "hello from nick1")

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

