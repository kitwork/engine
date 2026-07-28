// Package safepath resolves filesystem paths without allowing lexical or
// symlink-based escapes from an owned root.
package safepath

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Resolve joins paths to base and verifies that the canonical target remains
// under the canonical base. Missing final components are supported for writes.
func Resolve(base string, paths ...string) (string, error) {
	baseAbs, err := filepath.Abs(base)
	if err != nil {
		return "", fmt.Errorf("invalid base path: %w", err)
	}

	target := baseAbs
	for _, part := range paths {
		if filepath.IsAbs(part) {
			target = filepath.Clean(part)
		} else {
			target = filepath.Join(target, part)
		}
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return "", fmt.Errorf("invalid target path: %w", err)
	}

	inside, err := Contains(baseAbs, target)
	if err != nil {
		return "", err
	}
	if !inside {
		return "", fmt.Errorf("permission denied: path %q escapes boundary %q", target, baseAbs)
	}
	return target, nil
}

// Contains reports whether target remains under base after resolving symlinks.
// It resolves the nearest existing ancestor so a not-yet-created write target
// cannot escape through a symlinked parent directory.
func Contains(base, target string) (bool, error) {
	baseReal, err := canonical(base)
	if err != nil {
		return false, fmt.Errorf("resolve base path: %w", err)
	}
	targetReal, err := canonical(target)
	if err != nil {
		return false, fmt.Errorf("resolve target path: %w", err)
	}

	rel, err := filepath.Rel(baseReal, targetReal)
	if err != nil {
		return false, err
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))), nil
}

func canonical(name string) (string, error) {
	current, err := filepath.Abs(name)
	if err != nil {
		return "", err
	}

	var missing []string
	for {
		resolved, resolveErr := filepath.EvalSymlinks(current)
		if resolveErr == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(resolveErr) {
			return "", resolveErr
		}

		parent := filepath.Dir(current)
		if parent == current {
			return "", resolveErr
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}
