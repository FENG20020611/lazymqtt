package ui

import (
	"testing"

	"go.uber.org/goleak"
)

// The root model must not start anything of its own. Every goroutine in this
// app belongs either to a tea.Cmd, which Bubble Tea owns, or to the bridge,
// which internal/app owns. A goroutine started from Update or View would run
// unowned for the life of the process — and there is no natural place to stop
// it, which is exactly why it must not exist.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
