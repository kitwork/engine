package sql

import "fmt"

const (
	PartitionVersion1    = 1
	PartitionHashBuckets = 64
)

// Partition is a storage-neutral analytical routing policy. Field is a stable
// schema tag, so renames do not change partition identity. It never changes the
// canonical KROW key or transaction boundary.
type Partition struct {
	Version  int    `json:"version"`
	Field    uint32 `json:"field"`
	Strategy string `json:"strategy"`
	Buckets  uint32 `json:"buckets,omitempty"`
}

// IntegerPartitionBucket is part of the persisted HASH partition contract.
// SplitMix64 gives deterministic avalanche without a process seed.
func IntegerPartitionBucket(value int64, buckets uint32) (uint32, error) {
	if buckets == 0 {
		return 0, fmt.Errorf("partition bucket count is zero")
	}
	x := uint64(value) + 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	x ^= x >> 31
	return uint32(x % uint64(buckets)), nil
}
