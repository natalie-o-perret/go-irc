// Package ringbuf provides a generic fixed-size circular buffer.
package ringbuf

import "sync"

// RingBuf is a thread-safe, fixed-capacity circular buffer.
type RingBuf[T any] struct {
	mu   sync.Mutex
	buf  []T
	head int // index of oldest element
	size int // number of elements currently stored
	cap_ int
}

// New creates a RingBuf with the given capacity.
func New[T any](capacity int) *RingBuf[T] {
	if capacity <= 0 {
		capacity = 1
	}
	return &RingBuf[T]{
		buf:  make([]T, capacity),
		cap_: capacity,
	}
}

// Push appends an element, overwriting the oldest if full.
func (r *RingBuf[T]) Push(v T) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.size == r.cap_ {
		// overwrite oldest
		r.buf[r.head] = v
		r.head = (r.head + 1) % r.cap_
	} else {
		idx := (r.head + r.size) % r.cap_
		r.buf[idx] = v
		r.size++
	}
}

// Len returns the number of elements in the buffer.
func (r *RingBuf[T]) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.size
}

// Slice returns all elements in order from oldest to newest.
func (r *RingBuf[T]) Slice() []T {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]T, r.size)
	for i := range r.size {
		out[i] = r.buf[(r.head+i)%r.cap_]
	}
	return out
}

// Last returns the last n elements (or all if n > Len).
func (r *RingBuf[T]) Last(n int) []T {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n > r.size {
		n = r.size
	}
	out := make([]T, n)
	start := r.size - n
	for i := range n {
		out[i] = r.buf[(r.head+start+i)%r.cap_]
	}
	return out
}

// ForEach calls fn for each element from oldest to newest; stops if fn returns false.
func (r *RingBuf[T]) ForEach(fn func(T) bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.size {
		if !fn(r.buf[(r.head+i)%r.cap_]) {
			return
		}
	}
}

