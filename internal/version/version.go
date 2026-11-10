// Package version reports which build of Anvil is running.
package version

import "runtime/debug"

// Version and Commit are stamped at build time:
//
//	-ldflags "-X github.com/anvil-lab/anvil/internal/version.Version=v0.368.14 \
//	          -X github.com/anvil-lab/anvil/internal/version.Commit=c03ff39"
var (
	Version = "dev"
	Commit  = ""
)

var resolvedCommit = resolveCommit()

// Info returns the build version and short commit. Unstamped builds made inside
// a git checkout fall back to the revision Go embeds; anything else is "unknown".
func Info() (string, string) {
	return Version, resolvedCommit
}

func resolveCommit() string {
	if Commit != "" {
		return Commit
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	var revision string
	var modified bool
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value == "true"
		}
	}
	if len(revision) < 7 {
		return "unknown"
	}
	if modified {
		return revision[:7] + "-dirty"
	}
	return revision[:7]
}
