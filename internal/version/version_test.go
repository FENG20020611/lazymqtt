package version

import (
	"strings"
	"testing"
)

func TestInfoIncludesEveryField(t *testing.T) {
	restoreAfter(t)
	Version, Commit, Date = "v1.2.3", "abc1234", "2026-03-14T09:26:53Z"

	got := Info()
	for _, want := range []string{"lazymqtt", "v1.2.3", "abc1234", "2026-03-14T09:26:53Z"} {
		if !strings.Contains(got, want) {
			t.Errorf("Info() = %q, missing %q", got, want)
		}
	}
	if strings.Contains(got, "\n") {
		t.Errorf("Info() must be a single line, got %q", got)
	}
}

// Resolve is the `go install ...@latest` path: no ldflags, so the values come
// from the embedded build info instead. It must never overwrite a version that
// ldflags did supply.
func TestResolveDoesNotOverwriteInjectedValues(t *testing.T) {
	restoreAfter(t)
	Version, Commit, Date = "v1.2.3", "injected", "injected-date"

	Resolve()

	if Version != "v1.2.3" || Commit != "injected" || Date != "injected-date" {
		t.Errorf("Resolve overwrote linker-injected values: %s / %s / %s", Version, Commit, Date)
	}
}

// Under `go test` there is build info but no module version, so Resolve has
// nothing better to offer than the defaults. What matters is that it neither
// panics nor leaves a field empty — an empty version string in a --version
// output is worse than "dev".
func TestResolveLeavesNoFieldEmpty(t *testing.T) {
	restoreAfter(t)
	Version, Commit, Date = "dev", "none", "unknown"

	Resolve()

	if Version == "" || Commit == "" || Date == "" {
		t.Errorf("Resolve left a field empty: %q / %q / %q", Version, Commit, Date)
	}
	if Info() == "" {
		t.Error("Info() is empty after Resolve")
	}
}

// restoreAfter puts the package variables back, since these tests mutate
// package state to stand in for what the linker would inject.
func restoreAfter(t *testing.T) {
	t.Helper()
	v, c, d := Version, Commit, Date
	t.Cleanup(func() { Version, Commit, Date = v, c, d })
}
