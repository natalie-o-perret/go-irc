package mode_test

import (
	"testing"

	"github.com/natalie-o-perret/go-irc/irc"
	"github.com/natalie-o-perret/go-irc/server/mode"
)

func TestSetApplyFlags(t *testing.T) {
	s := mode.New()
	v := mode.ChannelValidator{}
	changes := irc.ParseModeString("+imn", nil)
	applied := s.Apply(changes, v)
	if len(applied) != 3 {
		t.Fatalf("expected 3 applied changes, got %d", len(applied))
	}
	if !s.Has('i') || !s.Has('m') || !s.Has('n') {
		t.Error("flags not set")
	}

	// Idempotent
	applied2 := s.Apply(changes, v)
	if len(applied2) != 0 {
		t.Errorf("expected 0 on re-apply of same flags, got %d", len(applied2))
	}

	// Unset
	un := irc.ParseModeString("-m", nil)
	s.Apply(un, v)
	if s.Has('m') {
		t.Error("+m should have been removed")
	}
}

func TestSetApplyList(t *testing.T) {
	s := mode.New()
	v := mode.ChannelValidator{}
	changes := irc.ParseModeString("+b", []string{"*!*@evil.host"})
	s.Apply(changes, v)

	list := s.List('b')
	if len(list) != 1 || list[0] != "*!*@evil.host" {
		t.Errorf("ban list: got %v", list)
	}

	// Remove
	rem := irc.ParseModeString("-b", []string{"*!*@evil.host"})
	s.Apply(rem, v)
	if len(s.List('b')) != 0 {
		t.Error("ban should have been removed")
	}
}

func TestFormatModeChanges(t *testing.T) {
	changes := []irc.ModeChange{
		{Add: true, Mode: 'o', Arg: "nick"},
		{Add: true, Mode: 'v', Arg: "other"},
		{Add: false, Mode: 'm'},
	}
	modeStr, args := irc.FormatModeChanges(changes)
	if modeStr == "" {
		t.Error("expected non-empty mode string")
	}
	if len(args) != 2 {
		t.Errorf("expected 2 args, got %d: %v", len(args), args)
	}
}

