package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kitwork/engine/kitdb"
)

const doctorFormat = "kitdb-doctor/v1"

type doctorCheck struct {
	Name               string `json:"name"`
	Passed             bool   `json:"passed"`
	ControlledRequired bool   `json:"controlled_required"`
	StableRequired     bool   `json:"stable_required"`
	Detail             string `json:"detail"`
}

type doctorBackup struct {
	Transaction uint64 `json:"transaction"`
	Records     uint64 `json:"records"`
	Bytes       int64  `json:"bytes"`
	SHA256      string `json:"sha256"`
}

type doctorResult struct {
	Format          string                     `json:"format"`
	Compatibility   kitdb.CompatibilityProfile `json:"compatibility"`
	DatabaseID      string                     `json:"database_id"`
	Transaction     uint64                     `json:"transaction"`
	Records         uint64                     `json:"records"`
	LogicalBytes    uint64                     `json:"logical_bytes"`
	LogicalSHA256   string                     `json:"logical_sha256"`
	MainFormat      uint16                     `json:"main_format"`
	WALBytes        int64                      `json:"wal_bytes"`
	Backup          doctorBackup               `json:"backup"`
	Checks          []doctorCheck              `json:"checks"`
	ControlledReady bool                       `json:"controlled_ready"`
	StableReady     bool                       `json:"stable_ready"`
	DurationMS      int64                      `json:"duration_ms"`
}

func runDoctor(ctx context.Context, args []string) (doctorResult, error) {
	startedAt := time.Now()
	if ctx == nil {
		return doctorResult{}, fmt.Errorf("nil doctor context")
	}
	flags := newFlagSet("doctor")
	expectedID := flags.String("expected-id", "", "expected stable database identity")
	maxWALBytes := flags.Int64("max-wal-bytes", 256<<20, "maximum healthy WAL size")
	if err := flags.Parse(args); err != nil {
		return doctorResult{}, err
	}
	if flags.NArg() != 1 {
		return doctorResult{}, fmt.Errorf("doctor requires DATABASE")
	}
	if *maxWALBytes < 1 || *maxWALBytes > 1<<40 {
		return doctorResult{}, fmt.Errorf("doctor max-wal-bytes must be between 1 and 1 TiB")
	}
	path, err := existingDatabasePath(flags.Arg(0))
	if err != nil {
		return doctorResult{}, err
	}
	workspace, err := os.MkdirTemp(filepath.Dir(path), ".kitdb-doctor-*")
	if err != nil {
		return doctorResult{}, fmt.Errorf("create same-filesystem doctor workspace: %w", err)
	}
	defer os.RemoveAll(workspace)

	result := doctorResult{
		Format:        doctorFormat,
		Compatibility: kitdb.CurrentCompatibility(),
		Checks:        make([]doctorCheck, 0, 8),
	}
	database, err := kitdb.OpenWithOptions(path, kitdb.OpenOptions{
		PageCacheBytes: -1,
		VerifyOnOpen:   true,
	})
	if err != nil {
		return doctorResult{}, fmt.Errorf("open and verify source: %w", err)
	}
	closed := false
	closeSource := func() error {
		if closed {
			return nil
		}
		closed = true
		return database.Close()
	}
	defer func() {
		_ = closeSource()
	}()

	stats, err := database.Stats()
	if err != nil {
		return doctorResult{}, errors.Join(err, closeSource())
	}
	result.DatabaseID = database.ID()
	result.Transaction = stats.LastTransaction
	result.MainFormat = stats.MainFormatVersion
	result.WALBytes = stats.WALBytes
	if expected := strings.TrimSpace(*expectedID); expected != "" && expected != result.DatabaseID {
		return doctorResult{}, errors.Join(
			fmt.Errorf("database identity %q does not match expected %q", result.DatabaseID, expected),
			closeSource(),
		)
	}
	result.addCheck(
		"compatibility", stats.MainFormatVersion >= result.Compatibility.Durable.MainFileRead.Minimum &&
			stats.MainFormatVersion <= result.Compatibility.Durable.MainFileRead.Maximum,
		true, true,
		fmt.Sprintf("main format v%d is inside readable range v%d..v%d",
			stats.MainFormatVersion,
			result.Compatibility.Durable.MainFileRead.Minimum,
			result.Compatibility.Durable.MainFileRead.Maximum,
		),
	)
	result.addCheck("full_verify", true, true, true, "every source page and checksum verified")
	quiescent := stats.ActiveSnapshots == 0 && stats.ActiveTransactions == 0 && stats.PendingCommits == 0
	result.addCheck(
		"quiescent", quiescent, true, true,
		fmt.Sprintf("snapshots=%d transactions=%d pending_commits=%d",
			stats.ActiveSnapshots, stats.ActiveTransactions, stats.PendingCommits),
	)
	result.addCheck(
		"wal_pressure", stats.WALBytes <= *maxWALBytes, true, true,
		fmt.Sprintf("wal_bytes=%d limit=%d", stats.WALBytes, *maxWALBytes),
	)

	sourceDigest, err := database.LogicalDigest(ctx)
	if err != nil {
		return doctorResult{}, errors.Join(err, closeSource())
	}
	result.Records = sourceDigest.Records
	result.LogicalBytes = sourceDigest.Bytes
	result.LogicalSHA256 = sourceDigest.SHA256

	backupPath := filepath.Join(workspace, "anchor.kitdb")
	anchor, err := database.CreateBackupAnchor(ctx, backupPath)
	if err != nil {
		return doctorResult{}, errors.Join(err, closeSource())
	}
	verifiedAnchor, err := kitdb.VerifyBackupAnchor(ctx, backupPath)
	backupPassed := err == nil && verifiedAnchor == anchor &&
		anchor.DatabaseID == result.DatabaseID && anchor.Transaction == result.Transaction
	result.addCheck(
		"verified_backup", backupPassed, true, true,
		fmt.Sprintf("transaction=%d records=%d bytes=%d", anchor.Transaction, anchor.Records, anchor.Bytes),
	)
	if err != nil || !backupPassed {
		return doctorResult{}, errors.Join(
			err, fmt.Errorf("verified backup does not match source boundary"), closeSource(),
		)
	}
	result.Backup = doctorBackup{
		Transaction: anchor.Transaction,
		Records:     anchor.Records,
		Bytes:       anchor.Bytes,
		SHA256:      anchor.SHA256,
	}

	if err := closeSource(); err != nil {
		return doctorResult{}, err
	}
	restorePath := filepath.Join(workspace, "restored.kitdb")
	restored, err := kitdb.RestoreToTransaction(ctx, backupPath, "", restorePath, anchor.Transaction)
	if err != nil {
		return doctorResult{}, err
	}
	restoreBoundaryPassed := restored.DatabaseID == result.DatabaseID &&
		restored.Transaction == result.Transaction
	result.addCheck(
		"restore_boundary", restoreBoundaryPassed, true, true,
		fmt.Sprintf("database_id=%s transaction=%d", restored.DatabaseID, restored.Transaction),
	)
	if !restoreBoundaryPassed {
		return doctorResult{}, fmt.Errorf("restored boundary does not match source")
	}

	reopened, err := kitdb.OpenWithOptions(restorePath, kitdb.OpenOptions{
		PageCacheBytes: -1,
		VerifyOnOpen:   true,
	})
	if err != nil {
		return doctorResult{}, err
	}
	restoredDigest, digestErr := reopened.LogicalDigest(ctx)
	restoreCloseErr := reopened.Close()
	logicalPassed := digestErr == nil && restoredDigest == sourceDigest
	result.addCheck(
		"logical_restore", logicalPassed, true, true,
		fmt.Sprintf("records=%d logical_sha256=%s", restoredDigest.Records, restoredDigest.SHA256),
	)
	if digestErr != nil || restoreCloseErr != nil || !logicalPassed {
		return doctorResult{}, errors.Join(
			digestErr, restoreCloseErr, fmt.Errorf("restored logical digest does not match source"),
		)
	}
	stable := result.Compatibility.Stability == "stable"
	result.addCheck(
		"stable_release", stable, false, true,
		"compiled stability="+result.Compatibility.Stability,
	)
	result.ControlledReady = result.checksPassed(false)
	result.StableReady = result.checksPassed(true)
	result.DurationMS = time.Since(startedAt).Milliseconds()
	return result, nil
}

func (result *doctorResult) addCheck(
	name string,
	passed, controlledRequired, stableRequired bool,
	detail string,
) {
	result.Checks = append(result.Checks, doctorCheck{
		Name: name, Passed: passed,
		ControlledRequired: controlledRequired,
		StableRequired:     stableRequired,
		Detail:             detail,
	})
}

func (result doctorResult) checksPassed(stable bool) bool {
	for _, check := range result.Checks {
		required := check.ControlledRequired
		if stable {
			required = check.StableRequired
		}
		if required && !check.Passed {
			return false
		}
	}
	return true
}
