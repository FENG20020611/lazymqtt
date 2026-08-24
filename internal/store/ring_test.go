package store

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestRingWraparound(t *testing.T) {
	r := NewRing[int](3)
	for i := 1; i <= 3; i++ {
		if _, ev := r.Push(i); ev {
			t.Fatalf("push %d evicted early", i)
		}
	}
	if got, want := r.Slice(0, r.Len()), []int{1, 2, 3}; !cmp.Equal(got, want) {
		t.Fatalf("full ring = %v, want %v", got, want)
	}
	old, ev := r.Push(4)
	if !ev || old != 1 {
		t.Fatalf("Push(4) = (%d, %v), want (1, true)", old, ev)
	}
	if got, want := r.Slice(0, r.Len()), []int{2, 3, 4}; !cmp.Equal(got, want) {
		t.Fatalf("after wrap = %v, want %v", got, want)
	}
	if r.At(0) != 2 || r.At(2) != 4 {
		t.Fatalf("At across the wrap boundary is wrong: %d %d", r.At(0), r.At(2))
	}
	if last, ok := r.Last(); !ok || last != 4 {
		t.Fatalf("Last = (%d, %v)", last, ok)
	}
}

func TestRingSliceClampsAndSpansWrap(t *testing.T) {
	r := NewRing[int](4)
	for i := 1; i <= 6; i++ { // 3,4,5,6 live, head is mid-buffer
		r.Push(i)
	}
	if got, want := r.Slice(1, 3), []int{4, 5}; !cmp.Equal(got, want) {
		t.Fatalf("Slice(1,3) = %v, want %v", got, want)
	}
	if got := r.Slice(-5, 99); !cmp.Equal(got, []int{3, 4, 5, 6}) {
		t.Fatalf("clamped Slice = %v", got)
	}
	if got := r.Slice(3, 1); got != nil {
		t.Fatalf("inverted Slice = %v, want nil", got)
	}
}

func TestRingDegenerateCapacities(t *testing.T) {
	one := NewRing[int](1)
	one.Push(1)
	if old, ev := one.Push(2); !ev || old != 1 {
		t.Fatalf("cap-1 ring: Push(2) = (%d, %v)", old, ev)
	}
	if one.Len() != 1 || one.At(0) != 2 {
		t.Fatalf("cap-1 ring holds the wrong element")
	}

	zero := NewRing[int](0)
	if _, ev := zero.Push(1); !ev {
		t.Fatal("cap-0 ring should report the push as evicted")
	}
	if zero.Len() != 0 || zero.Slice(0, 1) != nil {
		t.Fatal("cap-0 ring retained something")
	}
}

func TestRingReset(t *testing.T) {
	r := NewRing[*int](2)
	v := 7
	r.Push(&v)
	r.Reset()
	if r.Len() != 0 {
		t.Fatalf("Len after Reset = %d", r.Len())
	}
	if r.buf[0] != nil {
		t.Fatal("Reset left a live pointer in the backing array")
	}
}
