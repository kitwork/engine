package kitdb

import (
	"testing"
	"time"
)

func TestCheckpointPressureTimesAreProcessLocal(t *testing.T) {
	db := memoryDB(t, OpenOptions{})
	stats, err := db.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if !stats.OverlaySince.IsZero() || stats.LastCheckpointAt.IsZero() {
		t.Fatal("clean open timestamps")
	}
	memoryPut(t, db, "key", []byte("first"))
	first, _ := db.Stats()
	if first.OverlaySince.IsZero() {
		t.Fatal("write did not start age")
	}
	memoryPut(t, db, "key", []byte("second"))
	second, _ := db.Stats()
	if !first.OverlaySince.Equal(second.OverlaySince) {
		t.Fatal("overwrite reset pending age")
	}
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	clean, _ := db.Stats()
	if !clean.OverlaySince.IsZero() || clean.LastCheckpointAt.Before(first.OverlaySince) {
		t.Fatal("checkpoint timestamps")
	}
	memoryPut(t, db, "pending", []byte("third"))
	path := db.Path()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	stats, err = reopened.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if !stats.LastCheckpointAt.IsZero() || stats.OverlaySince.Before(start) {
		t.Fatal("recovery fabricated historical checkpoint time")
	}
}
