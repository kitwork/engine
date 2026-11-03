//go:build !kitdb_profile

package kitdb

import (
	"testing"
	"unsafe"
)

func TestLockProfileDisabled(t *testing.T) {
	if unsafe.Sizeof(lockProfile{}) != 0 || unsafe.Sizeof(lockSpan{}) != 0 {
		t.Fatal("normal build retains diagnostic state")
	}
	db := &DB{}
	if n := testing.AllocsPerRun(100, func() {
		span := db.profileRLock(lockSnapshot)
		db.profileRUnlock(&span)
		span = db.profileLock(lockCheckpointInstall)
		db.profileUnlock(&span)
	}); n != 0 {
		t.Fatalf("normal lock allocation=%g", n)
	}
}
