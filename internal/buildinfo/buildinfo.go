// Package buildinfo exposes build metadata in a neutral location so services
// can reference it without importing the handler layer. Linker flags inject
// the variables at release builds; local builds fall back to
// debug.ReadBuildInfo VCS settings. Unknown values stay "unknown" (the
// unavailable state) instead of being fabricated.
package buildinfo

import (
	"runtime"
	"runtime/debug"
)

// Linker-injected build metadata. Keep the variable names stable: Makefile
// and scripts/get_version.sh pass -X flags against this package.
var (
	Version   = "unknown"
	Edition   = "standard"
	CommitID  = "unknown"
	BuildTime = "unknown"
	GoVersion = "unknown"
)

// Info is one immutable snapshot of build metadata.
type Info struct {
	Version   string `json:"version"`
	Edition   string `json:"edition"`
	CommitID  string `json:"commit_id"`
	Dirty     *bool  `json:"dirty"`
	BuildTime string `json:"build_time"`
	GoVersion string `json:"go_version"`
}

// Unknown reports whether a build metadata value carries real information.
func Unknown(value string) bool {
	return value == "" || value == "unknown"
}

// Get returns the current build metadata with debug.ReadBuildInfo fallbacks:
// vcs.revision fills CommitID and vcs.modified fills Dirty when the linker did
// not inject them. GoVersion falls back to the runtime compiler version.
func Get() Info {
	info := Info{
		Version:   Version,
		Edition:   Edition,
		CommitID:  CommitID,
		BuildTime: BuildTime,
		GoVersion: GoVersion,
	}
	if Unknown(info.GoVersion) {
		info.GoVersion = runtime.Version()
	}
	if build, ok := debug.ReadBuildInfo(); ok {
		var revision, modified string
		for _, setting := range build.Settings {
			switch setting.Key {
			case "vcs.revision":
				revision = setting.Value
			case "vcs.modified":
				modified = setting.Value
			}
		}
		if Unknown(info.CommitID) && revision != "" {
			info.CommitID = revision
		}
		if modified != "" {
			dirty := modified == "true"
			info.Dirty = &dirty
		}
	}
	return info
}
