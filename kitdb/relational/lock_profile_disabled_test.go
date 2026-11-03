//go:build !kitdb_profile

package relational

import (
	"encoding/json"
	"fmt"
)

func startFixedLockProfile(_ []*mixedTenant, enabled bool) error {
	if enabled {
		return fmt.Errorf("lock profiling requires kitdb_profile build")
	}
	return nil
}

func finishFixedLockProfile(_ []*mixedTenant, _ bool) ([]json.RawMessage, error) { return nil, nil }
