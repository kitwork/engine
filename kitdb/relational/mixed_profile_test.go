package relational

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"runtime/trace"
	"testing"
)

// Profiles surround only the workload and its admitted-operation drain. Keep
// these runs separate from latency evidence; process-wide profilers add work.
func startMixedProfile(mode, prefix string) (func() error, error) {
	if mode == "" && prefix == "" {
		return func() error { return nil }, nil
	}
	if prefix == "" || (mode != "cpu" && mode != "alloc" && mode != "trace") {
		return nil, fmt.Errorf("mixed profiling requires a prefix and mode cpu, alloc or trace")
	}
	create := func(suffix string) (*os.File, error) {
		return os.OpenFile(prefix+suffix, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	}
	if mode == "alloc" {
		before, err := create(".before.heap")
		if err != nil {
			return nil, err
		}
		after, err := create(".after.heap")
		if err != nil {
			return nil, errors.Join(err, before.Close())
		}
		if err := errors.Join(pprof.WriteHeapProfile(before), before.Close()); err != nil {
			return nil, errors.Join(err, after.Close())
		}
		return func() error {
			// Flush sampled allocations before verification creates more objects.
			runtime.GC()
			return errors.Join(pprof.WriteHeapProfile(after), after.Close())
		}, nil
	}
	file, err := create("." + mode)
	if err != nil {
		return nil, err
	}
	if mode == "cpu" {
		if err := pprof.StartCPUProfile(file); err != nil {
			return nil, errors.Join(err, file.Close())
		}
		return func() error { pprof.StopCPUProfile(); return file.Close() }, nil
	}
	if err := trace.Start(file); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return func() error { trace.Stop(); return file.Close() }, nil
}

func TestMixedProfileRejectsInvalidConfig(t *testing.T) {
	for _, config := range [][2]string{{"cpu", ""}, {"", "prefix"}, {"other", "prefix"}} {
		if _, err := startMixedProfile(config[0], config[1]); err == nil {
			t.Fatalf("accepted invalid profile configuration %v", config)
		}
	}
}

func TestMixedProfileExclusiveFiles(t *testing.T) {
	for _, mode := range []string{"cpu", "alloc", "trace"} {
		t.Run(mode, func(t *testing.T) {
			prefix := filepath.Join(t.TempDir(), "profile")
			finish, err := startMixedProfile(mode, prefix)
			if err != nil {
				t.Fatal(err)
			}
			if err := finish(); err != nil {
				t.Fatal(err)
			}
			if _, err := startMixedProfile(mode, prefix); !errors.Is(err, os.ErrExist) {
				t.Fatalf("profile overwrite accepted: %v", err)
			}
		})
	}
}
