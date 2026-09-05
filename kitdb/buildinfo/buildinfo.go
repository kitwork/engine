// Package buildinfo identifies operator binaries without depending on Kitwork.
package buildinfo

import (
	"runtime"
	"runtime/debug"
)

// These strings are set only by the distribution linker. An ordinary Go build
// remains a development build, even when it includes a VCS revision.
var (
	version = "development"
	commit  = ""
)

type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit,omitempty"`
	Dirty     bool   `json:"dirty"`
	GoVersion string `json:"go_version"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
}

func Current() Info {
	result := Info{Version: version, Commit: commit, GoVersion: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				if result.Commit == "" {
					result.Commit = setting.Value
				}
			case "vcs.modified":
				result.Dirty = setting.Value == "true"
			}
		}
	}
	return result
}
