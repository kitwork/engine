//go:build kitdb_profile

package relational

import (
	"encoding/json"
	"fmt"
	"testing"
)

func startFixedLockProfile(tenants []*mixedTenant, enabled bool) error {
	if !enabled {
		return nil
	}
	for _, tenant := range tenants {
		if err := tenant.lease.DB().EnableLockProfile(); err != nil {
			return err
		}
	}
	return nil
}

func finishFixedLockProfile(tenants []*mixedTenant, enabled bool) ([]json.RawMessage, error) {
	if !enabled {
		return nil, nil
	}
	result := make([]json.RawMessage, 0, len(tenants))
	for id, tenant := range tenants {
		profile := tenant.lease.DB().LockProfile()
		if profile.Dropped != 0 {
			return nil, fmt.Errorf("database %d lock profile dropped %d intervals", id, profile.Dropped)
		}
		data, err := json.Marshal(struct {
			Database int `json:"database"`
			Profile  any `json:"profile"`
		}{id, profile})
		if err != nil {
			return nil, err
		}
		result = append(result, data)
	}
	return result, nil
}

func TestCheckpointLockProfileSmoke(t *testing.T) {
	c := attributionConfig("mixed", "", "")
	c.Databases, c.Writes, c.Reads, c.CheckpointEvery, c.LockProfile = 2, 4, 4, 2, true
	r, err := runFixedWork(t.TempDir(), c)
	if err != nil || !r.Success || len(r.LockProfiles) != 2 {
		t.Fatalf("success=%v profiles=%d error=%v", r.Success, len(r.LockProfiles), err)
	}
}
