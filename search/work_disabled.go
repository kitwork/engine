//go:build !searchwork

package search

import "context"

type postingWork struct{}

func postingWorkFromContext(context.Context) postingWork       { return postingWork{} }
func frequencyWorkContext(ctx context.Context) context.Context { return ctx }
func (postingWork) add(workEvent, uint64)                      {}
func (postingWork) varint(int, bool)                           {}
