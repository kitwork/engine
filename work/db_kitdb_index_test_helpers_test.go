package work

import (
	"context"
	"testing"

	kitdbengine "github.com/kitwork/engine/kitdb"
)

func advanceKitDBSecondaryIndexForTest(t testing.TB, database *kitdbengine.DB) bool {
	t.Helper()
	progress, err := kitDBSharedSecondaryIndexDriver.AdvanceSecondaryIndex(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	if progress.Pending && !progress.Advanced {
		t.Fatal("secondary-index worker reported pending work without durable progress")
	}
	return progress.Pending
}

func completeKitDBSecondaryIndexForTest(t testing.TB, database *kitdbengine.DB) {
	t.Helper()
	for attempt := 0; attempt < 128; attempt++ {
		if !advanceKitDBSecondaryIndexForTest(t, database) {
			return
		}
	}
	t.Fatal("secondary-index worker did not finish within 128 bounded chunks")
}
