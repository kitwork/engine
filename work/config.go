package work

import (
	"time"
)

// System Defaults
// These values act as the baseline configuration for the Engine logic.
// They can be overridden by specific runtime configurations if needed.
const (
	// Execution Defaults
	DefaultAPITimeout  = 30 * time.Second
	DefaultWorkerRetry = 0
	DefaultStaticCache = 24 * time.Hour
)
