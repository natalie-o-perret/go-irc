package client

import (
	"bufio"
	"bytes"
	"strings"
	"testing"

	"github.com/natalie-o-perret/go-irc/irc"
)

type framingMechanism struct {
	response   []byte
	challenges [][]byte
}

func (m *framingMechanism) Name() string { return "TEST" }

func (m *framingMechanism) Next(challenge []byte) ([]byte, bool, error) {
	m.challenges = append(m.challenges, append([]byte(nil), challenge...))
	return m.response, true, nil
}

func clientWithWriter() (*Client, *bytes.Buffer) {
	c := New(Config{})
	buf := &bytes.Buffer{}
	c.writer = bufio.NewWriter(buf)
	return c, buf
}

func TestCAPWireFormatAndDisable(t *testing.T) {
	c, output := clientWithWriter()
	c.handleCAP(c, irc.MustParse(":server CAP nick LS :message-tags"))
	if got, want := output.String(), "CAP REQ message-tags\r\n"; got != want {
		t.Fatalf("CAP REQ: got %q want %q", got, want)
	}

	output.Reset()
	c.capState.enabled[irc.CapMessageTags] = true
	c.handleCAP(c, irc.MustParse(":server CAP nick ACK :-message-tags"))
	if c.CAPEnabled(irc.CapMessageTags) {
		t.Error("negative ACK left message-tags enabled")
	}
	if got, want := output.String(), "CAP END\r\n"; got != want {
		t.Fatalf("CAP END: got %q want %q", got, want)
	}
}

func TestSASLIncomingChunks(t *testing.T) {
	c, _ := clientWithWriter()
	mechanism := &framingMechanism{}
	c.saslMech = mechanism

	c.handleAuthenticate(c, &irc.Message{Params: []string{strings.Repeat("A", 400)}})
	if len(mechanism.challenges) != 0 {
		t.Fatal("400-byte chunk completed the challenge")
	}
	c.handleAuthenticate(c, &irc.Message{Params: []string{"YQ=="}})
	if len(mechanism.challenges) != 1 || len(mechanism.challenges[0]) != 301 {
		t.Fatalf("challenge lengths: %v", len(mechanism.challenges))
	}
}

func TestSASLOutgoingExactChunk(t *testing.T) {
	c, output := clientWithWriter()
	c.saslMech = &framingMechanism{response: make([]byte, 300)}
	c.handleAuthenticate(c, &irc.Message{Params: []string{"+"}})

	lines := strings.Split(strings.TrimSuffix(output.String(), "\r\n"), "\r\n")
	if len(lines) != 2 || len(strings.TrimPrefix(lines[0], "AUTHENTICATE ")) != 400 || lines[1] != "AUTHENTICATE +" {
		t.Fatalf("unexpected SASL chunks: %q", lines)
	}
}
