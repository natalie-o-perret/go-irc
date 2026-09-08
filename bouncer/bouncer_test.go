package bouncer

import (
	"bytes"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/natalie-o-perret/go-irc/bouncer/history"
	"github.com/natalie-o-perret/go-irc/irc"
)

type recordingConn struct{ bytes.Buffer }

func (*recordingConn) Close() error                     { return nil }
func (*recordingConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (*recordingConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (*recordingConn) SetDeadline(time.Time) error      { return nil }
func (*recordingConn) SetReadDeadline(time.Time) error  { return nil }
func (*recordingConn) SetWriteDeadline(time.Time) error { return nil }

func TestChatHistoryLatest(t *testing.T) {
	store := history.NewMemoryStore(10)
	network := newNetwork(NetworkConfig{Name: "net"}, store)
	network.state.Channels["#chan"] = &ChannelState{Members: make(map[string]string)}
	for _, id := range []string{"one", "two", "three"} {
		msg := &irc.Message{
			Tags:    irc.Tags{"msgid": id},
			Prefix:  &irc.Prefix{Nick: "nick", User: "user", Host: "host"},
			Command: irc.PRIVMSG,
			Params:  []string{"#chan", id},
		}
		if err := store.Append("net", "#chan", time.Now(), msg); err != nil {
			t.Fatal(err)
		}
	}

	conn := &recordingConn{}
	ds := newDownstreamSession(conn, &Bouncer{})
	ds.nick = "reader"
	ds.network = network
	ds.caps = map[string]bool{
		irc.CapBatch:       true,
		irc.CapChatHistory: true,
		irc.CapMessageTags: true,
		irc.CapServerTime:  true,
	}
	ds.handleChatHistory(irc.MustParse("CHATHISTORY LATEST #chan * 2"))

	lines := strings.Split(strings.TrimSpace(conn.String()), "\r\n")
	if len(lines) != 4 {
		t.Fatalf("history lines: %q", lines)
	}
	start := irc.MustParse(lines[0])
	batchID := strings.TrimPrefix(start.Param(0), "+")
	for i, line := range lines[1:3] {
		msg := irc.MustParse(line)
		if msg.Tags["msgid"] != []string{"two", "three"}[i] || msg.Tags["batch"] != batchID {
			t.Errorf("history[%d]: %v", i, msg.Tags)
		}
	}
	if end := irc.MustParse(lines[3]); end.Param(0) != "-"+batchID {
		t.Fatalf("batch end: %q", lines[3])
	}
}
