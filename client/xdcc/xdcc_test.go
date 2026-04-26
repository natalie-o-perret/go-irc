package xdcc_test

import (
	"testing"

	"github.com/natalie-o-perret/go-irc/client/xdcc"
)

func TestParseListNoticeIroffer(t *testing.T) {
	line := " #3  12x  [45.6M]  some.movie.2024.mkv"
	p := xdcc.ParseListNotice(line)
	if p == nil {
		t.Fatal("expected non-nil pack")
	}
	if p.Number != 3 {
		t.Errorf("Number: got %d want 3", p.Number)
	}
	if p.Gets != 12 {
		t.Errorf("Gets: got %d want 12", p.Gets)
	}
	if p.Filename != "some.movie.2024.mkv" {
		t.Errorf("Filename: got %q", p.Filename)
	}
	// 45.6M = 47841689 = ~47.8MB
	if p.Size <= 0 {
		t.Errorf("Size should be positive, got %d", p.Size)
	}
}

func TestParseListNoticeNoMatch(t *testing.T) {
	line := "Total offered: 100 packs, 2.3 GB"
	p := xdcc.ParseListNotice(line)
	if p != nil {
		t.Errorf("expected nil for non-pack line, got %+v", p)
	}
}

func TestPackListByNumber(t *testing.T) {
	pl := xdcc.PackList{
		{Number: 1, Filename: "a.txt"},
		{Number: 2, Filename: "b.txt"},
		{Number: 5, Filename: "e.txt"},
	}
	p, ok := pl.ByNumber(2)
	if !ok || p.Filename != "b.txt" {
		t.Errorf("ByNumber(2): got %+v ok=%v", p, ok)
	}
	_, ok = pl.ByNumber(99)
	if ok {
		t.Error("ByNumber(99) should return ok=false")
	}
}

func TestFormatPackSize(t *testing.T) {
	cases := []struct {
		size int64
		want string
	}{
		{1024, "1.0K"},
		{1048576, "1.0M"},
		{1073741824, "1.0G"},
		{512, "512B"},
	}
	for _, tc := range cases {
		got := xdcc.FormatPackSize(tc.size)
		if got != tc.want {
			t.Errorf("FormatPackSize(%d): got %q want %q", tc.size, got, tc.want)
		}
	}
}

