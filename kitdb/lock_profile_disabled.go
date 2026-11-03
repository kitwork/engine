//go:build !kitdb_profile

package kitdb

type lockProfile struct{}
type lockSpan struct{}
type lockStamp struct{}

func (*lockProfile) start(lockSite) lockSpan { return lockSpan{} }
func (*lockSpan) acquired()                  {}
func (*lockSpan) releasing()                 {}
func (*lockSpan) end()                       {}
func (*lockSpan) transaction(uint64)         {}
func (*lockSpan) mark() lockStamp            { return lockStamp{} }
func (*lockSpan) phase(int, lockStamp)       {}
