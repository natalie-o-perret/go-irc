package sasl_test

import (
	"testing"

	"github.com/natalie-o-perret/go-irc/client/sasl"
)

func TestPlain(t *testing.T) {
	m := &sasl.Plain{Username: "alice", Password: "s3cr3t"}
	if m.Name() != "PLAIN" {
		t.Errorf("Name: got %q", m.Name())
	}
	resp, done, err := m.Next(nil)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if !done {
		t.Error("PLAIN should be done after first Next")
	}
	want := "\x00alice\x00s3cr3t"
	if string(resp) != want {
		t.Errorf("response: got %q want %q", resp, want)
	}
}

func TestExternal(t *testing.T) {
	m := &sasl.External{}
	if m.Name() != "EXTERNAL" {
		t.Errorf("Name: got %q", m.Name())
	}
	resp, done, err := m.Next(nil)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if !done {
		t.Error("EXTERNAL should be done after first Next")
	}
	if string(resp) != "" {
		t.Errorf("EXTERNAL response should be empty, got %q", resp)
	}
}
