package main

import (
	"strings"
	"testing"
)

func TestSummarize(t *testing.T) {
	input := `M=1 P=0 G=7 RegionBegin Time=10 Task=0 Type="kitdb.checkpoint.execute"
M=1 P=0 G=7 RegionBegin Time=20 Task=0 Type="kitdb.storage.sync"
M=2 P=1 G=8 RegionBegin Time=21 Task=0 Type="kitdb.storage.sync"
M=1 P=0 G=7 RegionEnd Time=30 Task=0 Type="kitdb.storage.sync"
M=2 P=1 G=8 RegionEnd Time=31 Task=0 Type="kitdb.storage.sync"
M=1 P=0 G=7 RegionEnd Time=40 Task=0 Type="kitdb.checkpoint.execute"`
	result, err := summarize(strings.NewReader(input))
	if err != nil || len(result) != 3 {
		t.Fatalf("%+v: %v", result, err)
	}
	if result[1].Count != 1 || result[1].TotalMS != .00001 || result[2].Count != 2 {
		t.Fatalf("bad nesting: %+v", result)
	}
}

func TestRejectUnmatched(t *testing.T) {
	for _, input := range []string{"", `G=1 RegionEnd Time=1 Task=0 Type="kitdb.storage.sync"`, `G=1 RegionBegin Time=1 Task=0 Type="kitdb.storage.sync"`, `G=1 RegionBegin Time=bad Task=0 Type="kitdb.storage.sync"`} {
		if _, err := summarize(strings.NewReader(input)); err == nil {
			t.Fatalf("accepted %q", input)
		}
	}
}
