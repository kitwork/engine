//go:build !windows

package relational

func lifecycleProcessThreads() (uint32, error) { return 0, nil }
