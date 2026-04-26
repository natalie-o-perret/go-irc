package ringbuf_test

import (
	"testing"

	"github.com/natalie-o-perret/go-irc/internal/ringbuf"
)

func TestPushAndSlice(t *testing.T) {
	r := ringbuf.New[int](3)
	r.Push(1)
	r.Push(2)
	r.Push(3)

	got := r.Slice()
	want := []int{1, 2, 3}
	if len(got) != len(want) {
		t.Fatalf("len: got %d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d]=%d want %d", i, got[i], want[i])
		}
	}
}

func TestOverwrite(t *testing.T) {
	r := ringbuf.New[int](3)
	for i := 1; i <= 6; i++ {
		r.Push(i)
	}
	got := r.Slice()
	want := []int{4, 5, 6}
	if len(got) != len(want) {
		t.Fatalf("len: got %d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d]=%d want %d", i, got[i], want[i])
		}
	}
}

func TestLast(t *testing.T) {
	r := ringbuf.New[int](10)
	for i := 1; i <= 7; i++ {
		r.Push(i)
	}
	got := r.Last(3)
	want := []int{5, 6, 7}
	if len(got) != len(want) {
		t.Fatalf("len: got %d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d]=%d want %d", i, got[i], want[i])
		}
	}
}

func TestLen(t *testing.T) {
	r := ringbuf.New[string](5)
	if r.Len() != 0 {
		t.Errorf("empty: got %d want 0", r.Len())
	}
	r.Push("a")
	r.Push("b")
	if r.Len() != 2 {
		t.Errorf("after 2 pushes: got %d want 2", r.Len())
	}
}
