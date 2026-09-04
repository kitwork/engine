//go:build !windows

package node

import (
	"errors"
	"fmt"
	"os"
)

func syncProductionPublisherDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("kitdb node: open publisher directory for sync: %w", err)
	}
	return errors.Join(directory.Sync(), directory.Close())
}
