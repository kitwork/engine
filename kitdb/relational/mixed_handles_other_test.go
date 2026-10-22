//go:build !windows

package relational

func mixedProcessHandles() (uint32, bool) { return 0, false }
