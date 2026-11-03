package relational

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

type secondaryTailReport struct {
	Version         int               `json:"version"`
	Epochs          []fixedWorkReport `json:"epochs"`
	Success         bool              `json:"success"`
	FixtureRoot     string            `json:"fixture_root"`
	FixtureRetained bool              `json:"fixture_retained"`
}

func secondaryTailFixtureRoot(t *testing.T, requested string) (string, error) {
	if requested != "" && !filepath.IsAbs(requested) {
		return "", fmt.Errorf("secondary tail fixture root must be absolute and fresh")
	}
	return lifecycleFixtureRoot(t, requested)
}

func TestSecondaryTailFixtureRoot(t *testing.T) {
	if _, err := secondaryTailFixtureRoot(t, "relative"); err == nil {
		t.Fatal("accepted relative fixture root")
	}
	path := filepath.Join(t.TempDir(), "retained")
	root, err := secondaryTailFixtureRoot(t, path)
	if err != nil || root != path {
		t.Fatalf("root=%q err=%v", root, err)
	}
	if _, err := secondaryTailFixtureRoot(t, path); err == nil {
		t.Fatal("reused existing fixture")
	}
}

func secondaryTailPlan(profile, prefix string, locks bool) (fixedWorkConfig, int, error) {
	config := fixedWorkConfig{
		Databases: 16, Writes: 128, Reads: 512, CheckpointEvery: 4,
		Workload: "mixed", ProfileMode: profile, ProfilePrefix: prefix,
		LockProfile: locks,
	}
	if err := validateFixedWork(config); err != nil {
		return config, 0, err
	}
	if profile != "" && profile != "cpu" && profile != "alloc" && profile != "trace" {
		return config, 0, fmt.Errorf("unsupported secondary tail profile %q", profile)
	}
	epochs := 4
	if profile != "" || locks {
		epochs = 1 // Attribution is deliberately separate from normal latency evidence.
	}
	return config, epochs, nil
}

func TestSecondaryTailPlan(t *testing.T) {
	for _, profile := range []string{"", "cpu", "alloc", "trace"} {
		for _, locks := range []bool{false, true} {
			prefix := ""
			if profile != "" {
				prefix = "diagnostic"
			}
			config, epochs, err := secondaryTailPlan(profile, prefix, locks)
			want := 4
			if profile != "" || locks {
				want = 1
			}
			if err != nil || epochs != want || config.Databases != 16 || config.Writes != 128 || config.Reads != 512 || config.CheckpointEvery != 4 || config.Workload != "mixed" || config.LockProfile != locks {
				t.Fatalf("config=%+v epochs=%d err=%v", config, epochs, err)
			}
		}
	}
	for _, input := range [][2]string{{"cpu", ""}, {"", "prefix"}, {"invalid", "prefix"}} {
		if _, _, err := secondaryTailPlan(input[0], input[1], false); err == nil {
			t.Fatalf("accepted invalid profile %v", input)
		}
	}
}

// Each epoch has an independent fixture and verifies ACKs, reopens and drains.
// Pool checkpoint samples, not the individual epoch percentiles. This is not a
// sustained single-database soak and must never be silently selected by a gate.
func TestSecondaryTailCampaign(t *testing.T) {
	if os.Getenv("KITDB_SECONDARY_TAIL") != "1" {
		t.Skip("set KITDB_SECONDARY_TAIL=1 for bounded secondary-read A/A and A/B attribution")
	}
	config, epochs, err := secondaryTailPlan(os.Getenv("KITDB_TAIL_PROFILE"), os.Getenv("KITDB_TAIL_PREFIX"), os.Getenv("KITDB_LOCK_PROFILE") == "1")
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(os.Getenv("KITDB_TAIL_REPORT"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := errors.Join(file.Sync(), file.Close()); err != nil {
			t.Error(err)
		}
	})
	requestedRoot := os.Getenv("KITDB_TAIL_ROOT")
	root, err := secondaryTailFixtureRoot(t, requestedRoot)
	if err != nil {
		t.Fatal(err)
	}
	report := secondaryTailReport{Version: 1, Epochs: make([]fixedWorkReport, 0, epochs), FixtureRoot: root, FixtureRetained: requestedRoot != ""}
	for epoch := range epochs {
		path := filepath.Join(root, fmt.Sprintf("epoch-%02d", epoch))
		if err = os.Mkdir(path, 0700); err != nil {
			break
		}
		var result fixedWorkReport
		result, err = runFixedWork(path, config)
		report.Epochs = append(report.Epochs, result)
		if err != nil {
			break
		}
	}
	report.Success = err == nil && len(report.Epochs) == epochs
	if writeErr := json.NewEncoder(file).Encode(report); writeErr != nil {
		t.Fatal(writeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
}
