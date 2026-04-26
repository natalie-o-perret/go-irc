package dcc_test

import (
	"net"
	"testing"

	"github.com/natalie-o-perret/go-irc/client/dcc"
)

func TestCTCPEncodeDecodeRoundtrip(t *testing.T) {
	cases := []struct {
		cmd, args string
	}{
		{"DCC", "SEND file.txt 2130706433 1234 1000"},
		{"ACTION", "waves"},
		{"VERSION", ""},
	}
	for _, tc := range cases {
		encoded := dcc.EncodeCTCP(tc.cmd, tc.args)
		gotCmd, gotArgs, ok := dcc.DecodeCTCP(encoded)
		if !ok {
			t.Errorf("DecodeCTCP(%q): not ok", encoded)
			continue
		}
		if gotCmd != tc.cmd {
			t.Errorf("cmd: got %q want %q", gotCmd, tc.cmd)
		}
		if gotArgs != tc.args {
			t.Errorf("args: got %q want %q", gotArgs, tc.args)
		}
	}
}

func TestDecodeCTCPInvalid(t *testing.T) {
	_, _, ok := dcc.DecodeCTCP("no delimiters")
	if ok {
		t.Error("expected ok=false for non-CTCP text")
	}
}

func TestParseSendRequest(t *testing.T) {
	// IP as uint32: 192.168.1.1 = 3232235777
	req, err := dcc.ParseSendRequest("file.txt 3232235777 1234 102400")
	if err != nil {
		t.Fatalf("ParseSendRequest: %v", err)
	}
	if req.Filename != "file.txt" {
		t.Errorf("filename: got %q", req.Filename)
	}
	want := net.IPv4(192, 168, 1, 1)
	if !req.IP.Equal(want) {
		t.Errorf("IP: got %v want %v", req.IP, want)
	}
	if req.Port != 1234 {
		t.Errorf("port: got %d want 1234", req.Port)
	}
	if req.Size != 102400 {
		t.Errorf("size: got %d want 102400", req.Size)
	}
}

func TestParseSendRequestPassive(t *testing.T) {
	req, err := dcc.ParseSendRequest("file.mkv 2130706433 0 500000 42")
	if err != nil {
		t.Fatalf("ParseSendRequest passive: %v", err)
	}
	if !req.Passive {
		t.Error("expected Passive=true for port=0 with token")
	}
	if req.Token != 42 {
		t.Errorf("token: got %d want 42", req.Token)
	}
}
