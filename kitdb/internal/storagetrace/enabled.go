//go:build kitdb_profile

package storagetrace

import (
	"context"
	"runtime/trace"
	"strconv"
)

type Region = trace.Region

func Start(name string) *Region { return trace.StartRegion(context.Background(), name) }

func CheckpointResult(path string, transaction uint64) {
	trace.Log(context.Background(), "kitdb.checkpoint.identity", path+"\x00"+strconv.FormatUint(transaction, 10))
}
