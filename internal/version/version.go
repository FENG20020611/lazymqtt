// Package version exposes build metadata injected at link time.
package version

import "runtime/debug"

// Injected via -ldflags "-X github.com/Onizuka893/lazymqtt/internal/version.Version=..."
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// Info is a single-line human readable build description.
func Info() string {
	return "lazymqtt " + Version + " (commit " + Commit + ", built " + Date + ")"
}

// Resolve fills in blanks from the embedded build info when ldflags were not
// supplied — the `go install ...@latest` path.
func Resolve() {
	if Version != "dev" {
		return
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	if bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		Version = bi.Main.Version
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			if Commit == "none" && s.Value != "" {
				Commit = s.Value
			}
		case "vcs.time":
			if Date == "unknown" && s.Value != "" {
				Date = s.Value
			}
		}
	}
}
