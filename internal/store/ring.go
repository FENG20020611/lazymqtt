package store

// Ring is a fixed-capacity FIFO buffer that overwrites its oldest element
// when full. Index 0 is always the oldest live element.
type Ring[T any] struct {
	buf  []T
	head int // index of the oldest element
	size int
}

// NewRing returns a ring holding at most capacity elements. A capacity of
// zero or less yields a ring that accepts pushes and keeps nothing, which is
// the behaviour a "history: 0" config should produce.
func NewRing[T any](capacity int) *Ring[T] {
	if capacity < 0 {
		capacity = 0
	}
	return &Ring[T]{buf: make([]T, capacity)}
}

// Cap returns the ring's fixed capacity.
func (r *Ring[T]) Cap() int { return len(r.buf) }

// Len returns the number of live elements.
func (r *Ring[T]) Len() int { return r.size }

// Push appends v. If the ring was full, the evicted element is returned with
// didEvict true so the caller can adjust byte accounting.
func (r *Ring[T]) Push(v T) (evicted T, didEvict bool) {
	if len(r.buf) == 0 {
		return evicted, true
	}
	if r.size == len(r.buf) {
		evicted, didEvict = r.buf[r.head], true
		r.buf[r.head] = v
		r.head = (r.head + 1) % len(r.buf)
		return evicted, didEvict
	}
	r.buf[(r.head+r.size)%len(r.buf)] = v
	r.size++
	return evicted, false
}

// At returns the i'th element counting from the oldest. It panics on an
// out-of-range index, matching slice semantics.
func (r *Ring[T]) At(i int) T {
	if i < 0 || i >= r.size {
		panic("store: Ring.At index out of range")
	}
	return r.buf[(r.head+i)%len(r.buf)]
}

// Last returns the newest element, or the zero value when empty.
func (r *Ring[T]) Last() (v T, ok bool) {
	if r.size == 0 {
		return v, false
	}
	return r.At(r.size - 1), true
}

// Slice copies the half-open range [from, to) into a fresh slice, in
// oldest-first order. Out-of-range bounds are clamped rather than panicking,
// because the caller is usually a viewport whose window may lag the data.
//
// This exists so the message panel renders only the visible window: the
// difference between O(visible) and O(stream_history) per frame.
func (r *Ring[T]) Slice(from, to int) []T {
	if from < 0 {
		from = 0
	}
	if to > r.size {
		to = r.size
	}
	if from >= to {
		return nil
	}
	out := make([]T, 0, to-from)
	for i := from; i < to; i++ {
		out = append(out, r.buf[(r.head+i)%len(r.buf)])
	}
	return out
}

// Reset drops every element, zeroing the backing array so that pointers do
// not keep payloads alive.
func (r *Ring[T]) Reset() {
	var zero T
	for i := range r.buf {
		r.buf[i] = zero
	}
	r.head, r.size = 0, 0
}
