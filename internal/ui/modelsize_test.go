package ui

import (
	"testing"
	"unsafe"
)

// §21 pitfall 15: the model is copied on every Update, and Bubble Tea boxes it
// into a tea.Model interface on the way out — so every keystroke and every
// ingest batch allocates a copy of whatever this struct weighs. Big embedded
// value types (a textarea, a textinput, a help model) turn a keypress into
// tens of kilobytes of garbage.
//
// The ceiling is deliberately loose. The point is not the exact number, it is
// that adding a fat component by value to the root model fails a test instead
// of quietly costing every frame.
func TestModelStaysSmallEnoughToCopyPerUpdate(t *testing.T) {
	const ceiling = 2048
	if got := unsafe.Sizeof(Model{}); got > ceiling {
		t.Errorf("Model is %d bytes; it is copied on every Update, so keep it under %d "+
			"by holding heavy components behind pointers", got, ceiling)
	}
}
