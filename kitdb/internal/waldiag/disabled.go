//go:build !windows || !kitdb_waldiag

package waldiag

import (
	"fmt"
	"os"
)

func Prepare(root, report string) error {
	return fmt.Errorf("WAL diagnostic capture requires windows and kitdb_waldiag")
}

// Ordinary builds never inspect configuration, metadata or resource users.
func Observe(phase, staging, target string, published bool, cause error, file *os.File) error {
	return nil
}
