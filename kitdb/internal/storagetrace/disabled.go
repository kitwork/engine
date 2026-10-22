//go:build !kitdb_profile

// Package storagetrace exposes bounded phase regions only in diagnostic builds.
package storagetrace

type Region struct{}

func Start(string) Region { return Region{} }
func (Region) End()       {}
