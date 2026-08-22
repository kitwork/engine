package engine

import (
	"runtime"
	"testing"
)

func TestVersionInfoUsesCurrentRuntimePlatform(t *testing.T) {
	info := GetVersionInfo()
	if info.OS != runtime.GOOS || info.Arch != runtime.GOARCH {
		t.Fatalf("platform = %s/%s, want %s/%s", info.OS, info.Arch, runtime.GOOS, runtime.GOARCH)
	}
	if info.GoVersion == "" || info.BytecodeVersion == 0 || info.ProgramEncodingVersion == 0 {
		t.Fatalf("version info = %+v", info)
	}
}
