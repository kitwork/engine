//go:build kitdb_profile

package storagetrace

import (
	"context"
	"runtime/trace"
)

type Region = trace.Region

func Start(name string) *Region { return trace.StartRegion(context.Background(), name) }
