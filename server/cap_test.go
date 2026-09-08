package server

import (
	"bytes"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/natalie-o-perret/go-irc/irc"
)

type recordingConn struct{ bytes.Buffer }

func (*recordingConn) Close() error                     { return nil }
func (*recordingConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (*recordingConn) RemoteAddr() net.Addr             { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1} }
func (*recordingConn) SetDeadline(time.Time) error      { return nil }
func (*recordingConn) SetReadDeadline(time.Time) error  { return nil }
func (*recordingConn) SetWriteDeadline(time.Time) error { return nil }

func TestCAPLS302AdvertisesImplementedCapabilities(t *testing.T) {
	srv := New(Config{Name: "irc.test"})
	conn := &recordingConn{}
	s := newSession(conn, srv)
	srv.handleCAP(s, irc.MustParse("CAP LS 302"))

	line := conn.String()
	if strings.Contains(line, "sasl") || strings.Contains(line, "account-tag") {
		t.Fatalf("advertised unsupported capability: %q", line)
	}
	if !strings.Contains(line, " batch ") || !strings.Contains(line, " draft/chathistory ") {
		t.Fatalf("missing history capabilities: %q", line)
	}
	if !s.capEnabled(irc.CapCapNotify) || s.state != StateCapNeg {
		t.Fatalf("CAP 302 state: caps=%v state=%v", s.caps, s.state)
	}
}

func TestCAPREQIsAtomic(t *testing.T) {
	srv := New(Config{Name: "irc.test"})
	conn := &recordingConn{}
	s := newSession(conn, srv)
	s.caps[irc.CapServerTime] = true
	srv.handleCAP(s, irc.MustParse("CAP REQ :message-tags unsupported"))

	if s.capEnabled(irc.CapMessageTags) || !s.capEnabled(irc.CapServerTime) {
		t.Fatalf("CAP NAK changed capabilities: %v", s.caps)
	}
	if !strings.Contains(conn.String(), " CAP * NAK :message-tags unsupported") {
		t.Fatalf("unexpected CAP response: %q", conn.String())
	}
}
