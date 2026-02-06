package work

import "time"

// System Defaults
// These values act as the baseline configuration for the Engine logic.
// They can be overridden by specific runtime configurations if needed.
const (
	// Database Defaults
	DefaultDBLimit    = 60  // Soft limit if .take() is not specified
	DefaultDBMaxLimit = 120 // Hard safety cap if .limited() is not specified

	// Execution Defaults
	DefaultAPITimeout  = 30 * time.Second
	DefaultWorkerRetry = 0
	DefaultStaticCache = 24 * time.Hour
)
