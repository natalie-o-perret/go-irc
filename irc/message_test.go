package irc_test

import (
	"strings"
	"testing"

	"github.com/natalie-o-perret/go-irc/irc"
)

var parseTests = []struct {
	raw     string
	command string
	prefix  string
	params  []string
	tags    map[string]string
}{
	{
		raw:     "PING :irc.example.com",
		command: "PING",
		params:  []string{"irc.example.com"},
	},
	{
		raw:     ":nick!user@host PRIVMSG #channel :hello world",
		command: "PRIVMSG",
		prefix:  "nick!user@host",
		params:  []string{"#channel", "hello world"},
	},
	{
		raw:     ":irc.example.com 001 nick :Welcome to the IRC network, nick!",
		command: "001",
		prefix:  "irc.example.com",
		params:  []string{"nick", "Welcome to the IRC network, nick!"},
	},
	{
		raw:     "@time=2024-01-01T00:00:00Z;msgid=abc :nick!u@h PRIVMSG #c :hey",
		command: "PRIVMSG",
		prefix:  "nick!u@h",
		params:  []string{"#c", "hey"},
		tags:    map[string]string{"time": "2024-01-01T00:00:00Z", "msgid": "abc"},
	},
	{
		raw:     "CAP * LS :sasl multi-prefix",
		command: "CAP",
		params:  []string{"*", "LS", "sasl multi-prefix"},
	},
}

func TestParse(t *testing.T) {
	for _, tt := range parseTests {
		t.Run(tt.raw, func(t *testing.T) {
			msg, err := irc.Parse(tt.raw)
			if err != nil {
				t.Fatalf("Parse error: %v", err)
			}
			if msg.Command != tt.command {
				t.Errorf("command: got %q want %q", msg.Command, tt.command)
			}
			if tt.prefix != "" {
				if msg.Prefix == nil {
					t.Fatalf("expected prefix %q, got nil", tt.prefix)
				}
				if msg.Prefix.String() != tt.prefix {
					t.Errorf("prefix: got %q want %q", msg.Prefix.String(), tt.prefix)
				}
			}
			if len(tt.params) > 0 {
				if len(msg.Params) != len(tt.params) {
					t.Fatalf("params count: got %d want %d (%v)", len(msg.Params), len(tt.params), msg.Params)
				}
				for i, p := range tt.params {
					if msg.Params[i] != p {
						t.Errorf("param[%d]: got %q want %q", i, msg.Params[i], p)
					}
				}
			}
			for k, v := range tt.tags {
				got, ok := msg.Tags.Get(k)
				if !ok {
					t.Errorf("tag %q missing", k)
				} else if got != v {
					t.Errorf("tag %q: got %q want %q", k, got, v)
				}
			}
		})
	}
}

func TestFormatRoundtrip(t *testing.T) {
	lines := []string{
		"PING :irc.example.com",
		":nick!user@host PRIVMSG #channel :hello world",
		":irc.example.com 001 nick :Welcome",
		"CAP * LS :sasl multi-prefix",
	}
	for _, raw := range lines {
		msg, err := irc.Parse(raw)
		if err != nil {
			t.Fatalf("Parse(%q): %v", raw, err)
		}
		formatted := irc.Format(msg)
		msg2, err := irc.Parse(formatted)
		if err != nil {
			t.Fatalf("Parse(reformatted %q): %v", formatted, err)
		}
		if msg2.Command != msg.Command {
			t.Errorf("command mismatch after round-trip: %q vs %q", msg.Command, msg2.Command)
		}
	}
}

func TestInputTooLong(t *testing.T) {
	if irc.InputTooLong("@" + strings.Repeat("a", 4094) + " PING x") {
		t.Error("4094 bytes of tag data rejected")
	}
	if !irc.InputTooLong("@" + strings.Repeat("a", 4095) + " PING x") {
		t.Error("4095 bytes of tag data accepted")
	}
	if !irc.InputTooLong(strings.Repeat("a", 511)) {
		t.Error("511-byte message accepted")
	}
}

func TestTagEscaping(t *testing.T) {
	msg := irc.MustParse(`@tag=semi\:space\sback\\slash\r\nunknown\qtrailing\ PRIVMSG nick :hi`)
	if got, want := msg.Tags["tag"], "semi;space back\\slash\r\nunknownqtrailing"; got != want {
		t.Fatalf("tag: got %q want %q", got, want)
	}
}

func TestServerTagsFormatBeforeClientTags(t *testing.T) {
	line := (&irc.Message{
		Tags:    irc.Tags{"+reply": "parent", "msgid": "child"},
		Command: irc.PRIVMSG,
		Params:  []string{"nick", "hi"},
	}).String()
	if !strings.HasPrefix(line, "@msgid=child;+reply=parent ") {
		t.Fatalf("unexpected tag order: %q", line)
	}
}

func TestParseModeString(t *testing.T) {
	changes := irc.ParseModeString("+o-v+b", []string{"nick", "other", "*!*@*"})
	if len(changes) != 3 {
		t.Fatalf("expected 3 changes, got %d", len(changes))
	}
	if !changes[0].Add || changes[0].Mode != 'o' || changes[0].Arg != "nick" {
		t.Errorf("change[0] wrong: %+v", changes[0])
	}
	if changes[1].Add || changes[1].Mode != 'v' || changes[1].Arg != "other" {
		t.Errorf("change[1] wrong: %+v", changes[1])
	}
	if !changes[2].Add || changes[2].Mode != 'b' || changes[2].Arg != "*!*@*" {
		t.Errorf("change[2] wrong: %+v", changes[2])
	}
}
