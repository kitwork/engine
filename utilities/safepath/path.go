// Package safepath resolves filesystem paths without allowing lexical or
// link-based escapes (a symlink, or on Windows a junction) from an owned root.
package safepath

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Boundary is an immutable canonical filesystem root. Preparing it once lets
// request paths verify changing targets without resolving the owned root on
// every access.
type Boundary struct {
	base string
}

// NewBoundary resolves and freezes one owned root.
func NewBoundary(base string) (*Boundary, error) {
	baseReal, err := canonical(base)
	if err != nil {
		return nil, fmt.Errorf("resolve base path: %w", err)
	}
	return &Boundary{base: baseReal}, nil
}

// Contains reports whether target remains inside this canonical boundary.
// The target is deliberately resolved on every call: a regular path may be
// replaced by a symlink after the boundary was prepared.
func (b *Boundary) Contains(target string) (bool, error) {
	if b == nil || b.base == "" {
		return false, fmt.Errorf("filesystem boundary is not initialized")
	}
	targetReal, err := canonical(target)
	if err != nil {
		return false, fmt.Errorf("resolve target path: %w", err)
	}
	return containsCanonical(b.base, targetReal)
}

// Relative is target's canonical path relative to this boundary, links
// resolved and, on Windows, short names and case read as the disk holds them:
// "" for the root itself. inside is false for a target that leads outside. A
// check of names (a folder no program may read) reads this, not the target as
// written, so a link or another spelling of a name cannot get past it.
func (b *Boundary) Relative(target string) (relative string, inside bool, err error) {
	if b == nil || b.base == "" {
		return "", false, fmt.Errorf("filesystem boundary is not initialized")
	}
	targetReal, err := canonical(target)
	if err != nil {
		return "", false, fmt.Errorf("resolve target path: %w", err)
	}
	inside, err = containsCanonical(b.base, targetReal)
	if err != nil || !inside {
		return "", false, err
	}
	relative, err = filepath.Rel(b.base, targetReal)
	if err != nil {
		return "", false, err
	}
	if relative == "." {
		relative = ""
	}
	return relative, true, nil
}

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

	boundary, err := NewBoundary(baseAbs)
	if err != nil {
		return "", err
	}
	inside, err := boundary.Contains(target)
	if err != nil {
		return "", err
	}
	if !inside {
		return "", fmt.Errorf("permission denied: path %q escapes boundary %q", target, baseAbs)
	}
	return target, nil
}

// Contains reports whether target remains under base after resolving links.
// It resolves the nearest existing ancestor so a not-yet-created write target
// cannot escape through a linked parent directory.
func Contains(base, target string) (bool, error) {
	boundary, err := NewBoundary(base)
	if err != nil {
		return false, err
	}
	return boundary.Contains(target)
}

func containsCanonical(baseReal, targetReal string) (bool, error) {
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
		resolved, resolveErr := resolveLinks(current)
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
