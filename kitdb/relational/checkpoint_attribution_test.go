package relational

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
)

func attributionConfig(mode, profile, prefix string) fixedWorkConfig {
	c := fixedWorkConfig{Databases: 16, Writes: 64, Reads: 192, CheckpointEvery: 4, Workload: mode, ProfileMode: profile, ProfilePrefix: prefix}
	if mode == "read" {
		c.Writes, c.CheckpointEvery = 0, 0
	}
	if mode == "write" {
		c.Reads = 0
	}
	return c
}

func TestCheckpointAttributionModes(t *testing.T) {
	for _, mode := range []string{"read", "write", "mixed"} {
		t.Run(mode, func(t *testing.T) {
			c := attributionConfig(mode, "", "")
			c.Databases = 2
			if c.Writes > 0 {
				c.Writes, c.CheckpointEvery = 4, 2
			}
			if c.Reads > 0 {
				c.Reads = 4
			}
			r, err := runFixedWork(t.TempDir(), c)
			if err != nil || !r.Success || r.ACKs != uint64(c.Databases*c.Writes) || r.Points != c.Databases*c.Reads || r.Indexes != r.Points || r.Verified != c.Databases {
				t.Fatalf("report=%+v error=%v", r, err)
			}
			if mode == "read" && len(r.Samples) != 0 {
				t.Fatal("read workload checkpointed")
			}
		})
	}
	for _, mode := range []string{"read", "write", "mixed", "invalid"} {
		c := attributionConfig(mode, "", "")
		if mode == "read" {
			c.Writes = 1
		} else if mode == "write" {
			c.Reads = 1
		} else {
			c.Reads = 0
		}
		if validateFixedWork(c) == nil {
			t.Fatalf("invalid %s configuration accepted", mode)
		}
	}
}

func TestCheckpointAttributionCampaign(t *testing.T) {
	if os.Getenv("KITDB_TAIL_WORK") != "1" {
		t.Skip("set KITDB_TAIL_WORK=1 for isolated workload attribution")
	}
	file, err := os.OpenFile(os.Getenv("KITDB_TAIL_REPORT"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := errors.Join(file.Sync(), file.Close()); err != nil {
			t.Error(err)
		}
	})
	c := attributionConfig(os.Getenv("KITDB_TAIL_MODE"), os.Getenv("KITDB_TAIL_PROFILE"), os.Getenv("KITDB_TAIL_PREFIX"))
	c.LockProfile = os.Getenv("KITDB_LOCK_PROFILE") == "1"
	r, err := runFixedWork(t.TempDir(), c)
	if writeErr := json.NewEncoder(file).Encode(r); writeErr != nil {
		t.Fatal(writeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
}
