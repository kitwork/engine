package node

import (
	"context"
	"fmt"

	"github.com/kitwork/engine/kitdb"
)

// DropDatabase closes one idle managed handle and permanently removes its
// engine-owned files. Topology owners must stop exposing the path before this
// call; the manager blocks new leases only for the duration of removal.
func (manager *Manager) DropDatabase(ctx context.Context, path string) error {
	if manager == nil {
		return ErrClosed
	}
	if ctx == nil {
		return fmt.Errorf("kitdb node: drop context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	absolute, key, err := canonicalDatabasePath(path)
	if err != nil {
		return err
	}

	// Keep topology and maintenance admission frozen until the physical file
	// set has disappeared. Existing active leases fail closed as busy.
	manager.replicaFileLinkMu.Lock()
	defer manager.replicaFileLinkMu.Unlock()
	if manager.replicaFileLinkDatabases[key] != 0 {
		return fmt.Errorf("%w: %q participates in a replica link", ErrDatabaseBusy, absolute)
	}
	manager.maintenanceMu.Lock()
	defer manager.maintenanceMu.Unlock()
	if manager.maintenancePerDatabase[key] != 0 {
		return fmt.Errorf("%w: %q has queued or running maintenance", ErrDatabaseBusy, absolute)
	}

	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		return ErrClosed
	}
	if _, retiring := manager.retiring[key]; retiring {
		manager.mu.Unlock()
		return fmt.Errorf("%w: %q is already being removed", ErrDatabaseBusy, absolute)
	}
	entry := manager.databases[key]
	if entry != nil && (entry.state != entryOpen || entry.leases != 0) {
		manager.mu.Unlock()
		return fmt.Errorf("%w: %q has active leases", ErrDatabaseBusy, absolute)
	}
	manager.retiring[key] = struct{}{}
	if entry != nil {
		manager.startClosingLocked(entry)
	}
	manager.mu.Unlock()
	defer func() {
		manager.mu.Lock()
		delete(manager.retiring, key)
		manager.signalLocked()
		manager.mu.Unlock()
	}()

	if entry != nil {
		if err := manager.closeEntry(entry); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return kitdb.RemoveDatabase(absolute)
}
