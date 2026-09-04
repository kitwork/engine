package node

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/kitwork/engine/kitdb"
)

const (
	defaultDirectoryPublisherMaxEntries = 4_096
	defaultDirectoryPublisherKeep       = 8
	directoryPublisherPrefix            = "published-v1-"
	directoryPublisherSuffix            = ".kitdb"
)

// VerifiedDirectoryPublisherOptions bound one explicitly provisioned anchor
// directory. MaxEntries counts unrelated entries too; KeepAnchors applies only
// to fully verified KitDB objects and must leave room for one new publication.
type VerifiedDirectoryPublisherOptions struct {
	MaxEntries  int
	KeepAnchors int
}

// VerifiedDirectoryPublisher publishes exact immutable copies into a dedicated
// pre-created directory, suitable for a separately mounted volume or a host
// transfer spool. The type cannot prove that the directory is a distinct
// physical failure domain; deployment owns that assertion.
type VerifiedDirectoryPublisher struct {
	directory   string
	maxEntries  int
	keepAnchors int
	mu          sync.Mutex
}

type directoryPublishedAnchor struct {
	name        string
	path        string
	databaseID  string
	transaction uint64
	sha256      string
}

// NewVerifiedDirectoryPublisher validates an existing dedicated directory
// without creating or changing it.
func NewVerifiedDirectoryPublisher(
	directory string,
	options VerifiedDirectoryPublisherOptions,
) (*VerifiedDirectoryPublisher, error) {
	if options.MaxEntries == 0 {
		options.MaxEntries = defaultDirectoryPublisherMaxEntries
	}
	if options.MaxEntries < 3 || options.MaxEntries > maximumProductionStoreEntries {
		return nil, fmt.Errorf(
			"kitdb node: directory publisher MaxEntries must be between 3 and %d",
			maximumProductionStoreEntries,
		)
	}
	if options.KeepAnchors == 0 {
		options.KeepAnchors = min(
			defaultDirectoryPublisherKeep,
			options.MaxEntries-1,
		)
	}
	if options.KeepAnchors < 2 || options.KeepAnchors >= options.MaxEntries {
		return nil, fmt.Errorf(
			"kitdb node: directory publisher KeepAnchors must be at least 2 and less than MaxEntries",
		)
	}
	absolute, _, err := canonicalProductionDirectory(directory)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return nil, fmt.Errorf("kitdb node: inspect directory publisher root: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, fmt.Errorf("kitdb node: directory publisher root is not a regular directory")
	}
	return &VerifiedDirectoryPublisher{
		directory: absolute, maxEntries: options.MaxEntries,
		keepAnchors: options.KeepAnchors,
	}, nil
}

// Directory returns the canonical host-owned destination. Never expose it to
// tenant code or include it in health snapshots.
func (publisher *VerifiedDirectoryPublisher) Directory() string {
	if publisher == nil {
		return ""
	}
	return publisher.directory
}

func (publisher *VerifiedDirectoryPublisher) productionPublisherDirectory() string {
	return publisher.Directory()
}

// Publish copies, syncs, retrieves, and fully verifies one exact anchor. The
// deterministic content name makes retry and process restart idempotent.
func (publisher *VerifiedDirectoryPublisher) Publish(
	ctx context.Context,
	anchor kitdb.BackupAnchor,
) (ProductionAnchorReceipt, error) {
	if publisher == nil {
		return ProductionAnchorReceipt{}, fmt.Errorf("kitdb node: nil directory publisher")
	}
	if ctx == nil {
		return ProductionAnchorReceipt{}, fmt.Errorf("kitdb node: nil directory publisher context")
	}
	if err := ctx.Err(); err != nil {
		return ProductionAnchorReceipt{}, err
	}
	verifiedSource, err := kitdb.VerifyBackupAnchor(ctx, anchor.Path)
	if err != nil {
		return ProductionAnchorReceipt{}, errors.Join(ErrProductionUnsafe, err)
	}
	if !productionAnchorsMatch(verifiedSource, anchor) {
		return ProductionAnchorReceipt{}, errors.Join(
			ErrProductionUnsafe,
			fmt.Errorf("kitdb node: production anchor changed before publication"),
		)
	}

	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	if err := publisher.validateRoot(); err != nil {
		return ProductionAnchorReceipt{}, err
	}
	records, total, err := publisher.scan(ctx)
	if err != nil {
		return ProductionAnchorReceipt{}, err
	}
	for _, record := range records {
		if record.databaseID != anchor.DatabaseID {
			return ProductionAnchorReceipt{}, errors.Join(
				ErrProductionUnsafe,
				fmt.Errorf("kitdb node: directory publisher contains mixed database identities"),
			)
		}
	}
	if len(records) != 0 {
		latest := records[len(records)-1]
		if anchor.Transaction < latest.transaction ||
			(anchor.Transaction == latest.transaction && anchor.SHA256 != latest.sha256) {
			return ProductionAnchorReceipt{}, errors.Join(
				ErrProductionUnsafe,
				fmt.Errorf("kitdb node: directory publisher cannot move backward or fork"),
			)
		}
	}

	name := directoryPublisherAnchorName(anchor)
	destination := filepath.Join(publisher.directory, name)
	for _, record := range records {
		if record.name != name {
			continue
		}
		verified, err := publisher.verifyRecord(ctx, record)
		if err != nil {
			return ProductionAnchorReceipt{}, err
		}
		if !productionAnchorsMatch(verified, anchor) {
			return ProductionAnchorReceipt{}, errors.Join(
				ErrProductionUnsafe,
				fmt.Errorf("kitdb node: published anchor content conflicts with its deterministic name"),
			)
		}
		pruned, pruneErr := publisher.prune(ctx, records)
		return productionAnchorReceipt(verified, true, pruned), pruneErr
	}

	roomPruned, records, total, err := publisher.makeRoom(ctx, records, total)
	if err != nil {
		return ProductionAnchorReceipt{}, err
	}
	if total >= publisher.maxEntries {
		return ProductionAnchorReceipt{}, ErrProductionPublisherLimit
	}
	restored, err := kitdb.RestoreToTransaction(
		ctx, anchor.Path, "", destination, anchor.Transaction,
	)
	if err != nil && !errors.Is(err, kitdb.ErrRestoreExists) {
		return ProductionAnchorReceipt{}, errors.Join(ErrProductionPublication, err)
	}
	alreadyPublished := errors.Is(err, kitdb.ErrRestoreExists)
	verified, verifyErr := kitdb.VerifyBackupAnchor(ctx, destination)
	if verifyErr != nil {
		return ProductionAnchorReceipt{}, errors.Join(ErrProductionUnsafe, verifyErr)
	}
	if !alreadyPublished && !productionAnchorsMatch(restored.BackupAnchor, verified) {
		return ProductionAnchorReceipt{}, errors.Join(
			ErrProductionUnsafe,
			fmt.Errorf("kitdb node: directory publication changed after restore"),
		)
	}
	if !productionAnchorsMatch(verified, anchor) {
		return ProductionAnchorReceipt{}, errors.Join(
			ErrProductionUnsafe,
			fmt.Errorf("kitdb node: directory publication differs from its source anchor"),
		)
	}
	records = append(records, directoryPublishedAnchor{
		name: name, path: destination, databaseID: anchor.DatabaseID,
		transaction: anchor.Transaction, sha256: anchor.SHA256,
	})
	pruned, pruneErr := publisher.prune(ctx, records)
	receipt := productionAnchorReceipt(
		verified, alreadyPublished, roomPruned+pruned,
	)
	return receipt, pruneErr
}

func (publisher *VerifiedDirectoryPublisher) validateRoot() error {
	info, err := os.Lstat(publisher.directory)
	if err != nil {
		return fmt.Errorf("kitdb node: inspect directory publisher root: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.Join(
			ErrProductionUnsafe,
			fmt.Errorf("kitdb node: directory publisher root changed type"),
		)
	}
	return nil
}

func (publisher *VerifiedDirectoryPublisher) scan(
	ctx context.Context,
) ([]directoryPublishedAnchor, int, error) {
	opened, err := os.Open(publisher.directory)
	if err != nil {
		return nil, 0, fmt.Errorf("kitdb node: open directory publisher: %w", err)
	}
	entries := make([]os.DirEntry, 0, min(publisher.maxEntries+1, 256))
	var readErr error
	for len(entries) <= publisher.maxEntries {
		batchSize := min(256, publisher.maxEntries+1-len(entries))
		var batch []os.DirEntry
		batch, readErr = opened.ReadDir(batchSize)
		entries = append(entries, batch...)
		if errors.Is(readErr, io.EOF) {
			readErr = nil
			break
		}
		if readErr != nil || len(batch) == 0 {
			break
		}
	}
	closeErr := opened.Close()
	if readErr != nil {
		return nil, len(entries), errors.Join(
			fmt.Errorf("kitdb node: list directory publisher: %w", readErr),
			closeErr,
		)
	}
	if closeErr != nil {
		return nil, len(entries), closeErr
	}
	if len(entries) > publisher.maxEntries {
		return nil, len(entries), ErrProductionPublisherLimit
	}
	records := make([]directoryPublishedAnchor, 0, len(entries))
	stagingPaths := make([]string, 0)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, len(entries), err
		}
		if isDirectoryPublisherRestoreStaging(entry.Name()) {
			if !directoryPublisherEntryIsRegular(entry) {
				return nil, len(entries), errors.Join(
					ErrProductionUnsafe,
					fmt.Errorf("kitdb node: directory publisher staging is not a regular file"),
				)
			}
			stagingPaths = append(
				stagingPaths,
				filepath.Join(publisher.directory, entry.Name()),
			)
			continue
		}
		record, matches, err := parseDirectoryPublisherAnchorName(entry.Name())
		if err != nil {
			return nil, len(entries), errors.Join(ErrProductionUnsafe, err)
		}
		if !matches {
			continue
		}
		if !directoryPublisherEntryIsRegular(entry) {
			return nil, len(entries), errors.Join(
				ErrProductionUnsafe,
				fmt.Errorf("kitdb node: directory publisher anchor is not a regular file"),
			)
		}
		record.path = filepath.Join(publisher.directory, entry.Name())
		records = append(records, record)
	}
	for _, stagingPath := range stagingPaths {
		if err := ctx.Err(); err != nil {
			return nil, len(entries), err
		}
		if err := os.Remove(stagingPath); err != nil {
			return nil, len(entries), fmt.Errorf(
				"kitdb node: remove abandoned directory publisher staging: %w", err,
			)
		}
	}
	if len(stagingPaths) != 0 {
		if err := syncProductionPublisherDirectory(publisher.directory); err != nil {
			return nil, len(entries) - len(stagingPaths), errors.Join(
				kitdb.ErrDurabilityUncertain,
				err,
			)
		}
	}
	sortDirectoryPublishedAnchors(records)
	return records, len(entries) - len(stagingPaths), nil
}

func isDirectoryPublisherRestoreStaging(name string) bool {
	const prefix = ".kitdb-restore-"
	const suffix = ".tmp"
	return strings.HasPrefix(name, prefix) && strings.HasSuffix(name, suffix) &&
		len(name) > len(prefix)+len(suffix)
}

func directoryPublisherEntryIsRegular(entry os.DirEntry) bool {
	return !entry.IsDir() && entry.Type()&os.ModeSymlink == 0 &&
		(entry.Type() == 0 || entry.Type().IsRegular())
}

func (publisher *VerifiedDirectoryPublisher) makeRoom(
	ctx context.Context,
	records []directoryPublishedAnchor,
	total int,
) (int, []directoryPublishedAnchor, int, error) {
	removed := 0
	for total >= publisher.maxEntries && len(records) > publisher.keepAnchors {
		if _, err := publisher.verifyRecord(ctx, records[0]); err != nil {
			return removed, records, total, err
		}
		if err := os.Remove(records[0].path); err != nil {
			return removed, records, total, fmt.Errorf(
				"kitdb node: prune directory publisher anchor: %w", err,
			)
		}
		records = records[1:]
		total--
		removed++
	}
	if removed != 0 {
		if err := syncProductionPublisherDirectory(publisher.directory); err != nil {
			return removed, records, total, errors.Join(kitdb.ErrDurabilityUncertain, err)
		}
	}
	return removed, records, total, nil
}

func (publisher *VerifiedDirectoryPublisher) prune(
	ctx context.Context,
	records []directoryPublishedAnchor,
) (int, error) {
	sortDirectoryPublishedAnchors(records)
	if len(records) <= publisher.keepAnchors {
		return 0, nil
	}
	removed := 0
	for _, record := range records[:len(records)-publisher.keepAnchors] {
		if _, err := publisher.verifyRecord(ctx, record); err != nil {
			return removed, err
		}
		if err := os.Remove(record.path); err != nil {
			return removed, fmt.Errorf(
				"kitdb node: prune directory publisher anchor: %w", err,
			)
		}
		removed++
	}
	if removed != 0 {
		if err := syncProductionPublisherDirectory(publisher.directory); err != nil {
			return removed, errors.Join(kitdb.ErrDurabilityUncertain, err)
		}
	}
	return removed, nil
}

func (publisher *VerifiedDirectoryPublisher) verifyRecord(
	ctx context.Context,
	record directoryPublishedAnchor,
) (kitdb.BackupAnchor, error) {
	verified, err := kitdb.VerifyBackupAnchor(ctx, record.path)
	if err != nil {
		return kitdb.BackupAnchor{}, errors.Join(ErrProductionUnsafe, err)
	}
	if verified.DatabaseID != record.databaseID ||
		verified.Transaction != record.transaction ||
		verified.SHA256 != record.sha256 {
		return kitdb.BackupAnchor{}, errors.Join(
			ErrProductionUnsafe,
			fmt.Errorf("kitdb node: directory publisher filename does not match its anchor"),
		)
	}
	return verified, nil
}

func directoryPublisherAnchorName(anchor kitdb.BackupAnchor) string {
	return fmt.Sprintf(
		"%s%s-%020d-%s%s",
		directoryPublisherPrefix,
		anchor.DatabaseID,
		anchor.Transaction,
		anchor.SHA256,
		directoryPublisherSuffix,
	)
}

func parseDirectoryPublisherAnchorName(
	name string,
) (directoryPublishedAnchor, bool, error) {
	if !strings.HasPrefix(name, directoryPublisherPrefix) ||
		!strings.HasSuffix(name, directoryPublisherSuffix) {
		return directoryPublishedAnchor{}, false, nil
	}
	body := strings.TrimSuffix(
		strings.TrimPrefix(name, directoryPublisherPrefix),
		directoryPublisherSuffix,
	)
	parts := strings.Split(body, "-")
	if len(parts) != 3 || len(parts[0]) != 32 || len(parts[1]) != 20 ||
		len(parts[2]) != 64 || parts[0] != strings.ToLower(parts[0]) ||
		parts[2] != strings.ToLower(parts[2]) {
		return directoryPublishedAnchor{}, true, fmt.Errorf(
			"kitdb node: malformed directory publisher anchor name",
		)
	}
	identity, identityErr := hex.DecodeString(parts[0])
	digest, digestErr := hex.DecodeString(parts[2])
	transaction, transactionErr := strconv.ParseUint(parts[1], 10, 64)
	if identityErr != nil || len(identity) != 16 ||
		digestErr != nil || len(digest) != 32 || transactionErr != nil ||
		parts[1] != fmt.Sprintf("%020d", transaction) {
		return directoryPublishedAnchor{}, true, fmt.Errorf(
			"kitdb node: malformed directory publisher anchor name",
		)
	}
	return directoryPublishedAnchor{
		name: name, databaseID: parts[0], transaction: transaction,
		sha256: parts[2],
	}, true, nil
}

func sortDirectoryPublishedAnchors(records []directoryPublishedAnchor) {
	sort.Slice(records, func(left, right int) bool {
		if records[left].transaction == records[right].transaction {
			return records[left].sha256 < records[right].sha256
		}
		return records[left].transaction < records[right].transaction
	})
}
