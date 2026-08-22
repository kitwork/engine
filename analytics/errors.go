package analytics

import (
	"errors"
	"fmt"
)

var (
	ErrCorruptSegment     = errors.New("analytics: corrupt segment")
	ErrCorruptManifest    = errors.New("analytics: corrupt manifest")
	ErrUnsupportedVersion = errors.New("analytics: unsupported version")
)

func corruptf(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrCorruptSegment, fmt.Sprintf(format, arguments...))
}
