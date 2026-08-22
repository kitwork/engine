package collection

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/kitwork/engine/capabilities"
	searchengine "github.com/kitwork/engine/search"
	collectionhelper "github.com/kitwork/engine/utilities/collection"
)

const (
	collectionSegmentVersion           = "collection-v2"
	collectionSegmentField             = "text"
	collectionSegmentSignaturePrefix   = "source-"
	collectionSegmentSignatureSuffix   = ".sig"
	collectionSegmentMaximumSignature  = 512
	collectionSegmentMaximumSearchHits = 200
	collectionSegmentCanaryTimeout     = 2 * time.Minute
	collectionSegmentCanaryQueue       = 32
	collectionSegmentCanaryHostTasks   = 256
)

// SegmentSearchCanaryStats are bounded process-local counters. They contain no
// query text, collection path, tenant identity, or other unbounded labels.
type SegmentSearchCanaryStats struct {
	Enabled          bool
	Rebuilds         uint64
	Searches         uint64
	Matches          uint64
	Mismatches       uint64
	Failures         uint64
	Dropped          uint64
	Canceled         uint64
	Stale            uint64
	PendingTasks     int64
	PeakPendingTasks int64
}

// SegmentSearchCanaryTelemetry aggregates fixed-cardinality counters across
// tenant generations. It intentionally has no labels, paths, or query text.
type SegmentSearchCanaryTelemetry struct {
	admissionOnce sync.Once
	tasks         chan struct{}
	pending       atomic.Int64
	peakPending   atomic.Int64

	rebuilds   atomic.Uint64
	searches   atomic.Uint64
	matches    atomic.Uint64
	mismatches atomic.Uint64
	failures   atomic.Uint64
	dropped    atomic.Uint64
	canceled   atomic.Uint64
	stale      atomic.Uint64
}

func NewSegmentSearchCanaryTelemetry() *SegmentSearchCanaryTelemetry {
	telemetry := &SegmentSearchCanaryTelemetry{}
	telemetry.initializeAdmission()
	return telemetry
}

// Snapshot returns detached host-wide counters.
func (telemetry *SegmentSearchCanaryTelemetry) Snapshot() SegmentSearchCanaryStats {
	if telemetry == nil {
		return SegmentSearchCanaryStats{}
	}
	return SegmentSearchCanaryStats{
		Rebuilds: telemetry.rebuilds.Load(), Searches: telemetry.searches.Load(),
		Matches: telemetry.matches.Load(), Mismatches: telemetry.mismatches.Load(),
		Failures: telemetry.failures.Load(), Dropped: telemetry.dropped.Load(),
		Canceled: telemetry.canceled.Load(), Stale: telemetry.stale.Load(),
		PendingTasks: telemetry.pending.Load(), PeakPendingTasks: telemetry.peakPending.Load(),
	}
}

func (telemetry *SegmentSearchCanaryTelemetry) initializeAdmission() {
	telemetry.admissionOnce.Do(func() {
		telemetry.tasks = make(chan struct{}, collectionSegmentCanaryHostTasks)
	})
}

func (telemetry *SegmentSearchCanaryTelemetry) acquireTask() bool {
	if telemetry == nil {
		return false
	}
	telemetry.initializeAdmission()
	select {
	case telemetry.tasks <- struct{}{}:
		pending := telemetry.pending.Add(1)
		for {
			peak := telemetry.peakPending.Load()
			if pending <= peak || telemetry.peakPending.CompareAndSwap(peak, pending) {
				break
			}
		}
		return true
	default:
		return false
	}
}

func (telemetry *SegmentSearchCanaryTelemetry) releaseTask() {
	if telemetry == nil {
		return
	}
	<-telemetry.tasks
	telemetry.pending.Add(-1)
}

type segmentSearchTask struct {
	handle    *Handle
	signature string
	query     string
	limit     int
	legacy    []string
}

type segmentSearchCanary struct {
	enabled atomic.Bool

	lifecycleMu sync.Mutex
	ctx         context.Context
	cancel      context.CancelFunc
	queue       chan segmentSearchTask
	done        chan struct{}
	started     bool
	closed      bool

	// One worker owns signatures, so the map needs no hot-path lock.
	signatures map[string]string
	telemetry  *SegmentSearchCanaryTelemetry
}

func newSegmentSearchCanary(telemetry *SegmentSearchCanaryTelemetry) *segmentSearchCanary {
	if telemetry == nil {
		telemetry = NewSegmentSearchCanaryTelemetry()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &segmentSearchCanary{
		ctx: ctx, cancel: cancel,
		queue: make(chan segmentSearchTask, collectionSegmentCanaryQueue),
		done:  make(chan struct{}), signatures: make(map[string]string), telemetry: telemetry,
	}
}

// EnableSegmentSearchCanary opts this generation-owned collection manager in
// to bounded shadow indexing and ranking. The existing SQLite engine remains
// the serving path, so a segment-engine error cannot change application
// results or block the request on an index rebuild.
func (manager *Manager) EnableSegmentSearchCanary(enabled bool) {
	if manager == nil || manager.segmentCanary == nil {
		return
	}
	manager.segmentCanary.setEnabled(enabled)
}

// SegmentSearchCanaryStats returns a lock-free snapshot of bounded canary
// telemetry suitable for tests and host diagnostics.
func (manager *Manager) SegmentSearchCanaryStats() SegmentSearchCanaryStats {
	if manager == nil || manager.segmentCanary == nil {
		return SegmentSearchCanaryStats{}
	}
	canary := manager.segmentCanary
	stats := canary.telemetry.Snapshot()
	stats.Enabled = canary.enabled.Load()
	return stats
}

func (canary *segmentSearchCanary) setEnabled(enabled bool) {
	canary.lifecycleMu.Lock()
	defer canary.lifecycleMu.Unlock()
	if canary.closed {
		canary.enabled.Store(false)
		return
	}
	canary.enabled.Store(enabled)
	if enabled && !canary.started {
		canary.started = true
		go canary.run()
	}
}

func (canary *segmentSearchCanary) enqueue(task segmentSearchTask) {
	if canary == nil || !canary.enabled.Load() {
		return
	}
	canary.lifecycleMu.Lock()
	defer canary.lifecycleMu.Unlock()
	if canary.closed || !canary.enabled.Load() {
		return
	}
	if !canary.started {
		canary.started = true
		go canary.run()
	}
	if !canary.telemetry.acquireTask() {
		canary.telemetry.dropped.Add(1)
		return
	}
	accepted := false
	defer func() {
		if !accepted {
			canary.telemetry.releaseTask()
		}
	}()

	select {
	case canary.queue <- task:
		canary.telemetry.searches.Add(1)
		accepted = true
	default:
		canary.telemetry.dropped.Add(1)
	}
}

func (canary *segmentSearchCanary) run() {
	defer close(canary.done)
	for {
		select {
		case <-canary.ctx.Done():
			canary.cancelQueued()
			return
		case task := <-canary.queue:
			if canary.ctx.Err() != nil {
				canary.telemetry.canceled.Add(1)
				canary.telemetry.releaseTask()
				canary.cancelQueued()
				return
			}
			canary.runAcceptedTask(task)
		}
	}
}

func (canary *segmentSearchCanary) runAcceptedTask(task segmentSearchTask) {
	defer canary.telemetry.releaseTask()
	canary.runTask(task)
}

func (canary *segmentSearchCanary) cancelQueued() {
	for {
		select {
		case <-canary.queue:
			canary.telemetry.canceled.Add(1)
			canary.telemetry.releaseTask()
		default:
			return
		}
	}
}

func (canary *segmentSearchCanary) runTask(task segmentSearchTask) {
	defer func() {
		if recover() != nil {
			canary.telemetry.failures.Add(1)
		}
	}()
	ctx, cancel := context.WithTimeout(canary.ctx, collectionSegmentCanaryTimeout)
	defer cancel()
	index, err := task.handle.collection.Index()
	if err == nil && collectionDirSignature(index) != task.signature {
		canary.telemetry.stale.Add(1)
		return
	}
	var projection *collectionSegmentProjection
	if err == nil {
		projection, err = task.handle.syncSegmentSearchCanary(ctx, index, task.signature)
	}
	if err == nil {
		var hits []searchengine.Hit
		hits, err = projection.search(ctx, task.query, task.limit)
		if err == nil {
			if sameSegmentCanaryRanking(task.legacy, hits) {
				canary.telemetry.matches.Add(1)
			} else {
				canary.telemetry.mismatches.Add(1)
			}
		}
	}
	if err == nil {
		return
	}
	if canary.ctx.Err() != nil || errors.Is(err, context.Canceled) {
		canary.telemetry.canceled.Add(1)
		return
	}
	canary.telemetry.failures.Add(1)
	if projection != nil {
		delete(canary.signatures, projection.key)
	}
}

func (canary *segmentSearchCanary) close() {
	if canary == nil {
		return
	}
	canary.lifecycleMu.Lock()
	if canary.closed {
		done := canary.done
		canary.lifecycleMu.Unlock()
		<-done
		return
	}
	canary.closed = true
	canary.enabled.Store(false)
	canary.cancel()
	if !canary.started {
		close(canary.done)
	}
	done := canary.done
	canary.lifecycleMu.Unlock()
	<-done
}

func (handle *Handle) runSegmentSearchCanary(
	signature string,
	query string,
	limit int,
	legacy []ftsHit,
) {
	if handle == nil || handle.manager == nil || handle.manager.segmentCanary == nil {
		return
	}
	canary := handle.manager.segmentCanary
	if !canary.enabled.Load() {
		return
	}
	legacyIDs := make([]string, len(legacy))
	for position, hit := range legacy {
		legacyIDs[position] = strings.Clone(hit.slug)
	}
	canary.enqueue(segmentSearchTask{
		handle: handle, signature: strings.Clone(signature),
		query: strings.Clone(query), limit: limit, legacy: legacyIDs,
	})
}

func (handle *Handle) searchSources(index []collectionhelper.IndexEntry) ([]ftsSource, bool) {
	key := handle.collection.Path()
	documents := make([]ftsSource, 0, len(index))
	complete := true
	for _, entry := range index {
		slug := entry.File.Slug
		document, err := handle.collection.Read(slug)
		if err != nil {
			fmt.Printf("[Collection] search index skip %s/%s: %v\n", key, slug, err)
			complete = false
			continue
		}
		title, _ := document.Meta["title"].(string)
		description, _ := document.Meta["description"].(string)
		documents = append(documents, ftsSource{
			slug: slug, title: title, description: description, body: document.Body,
		})
	}
	return documents, complete
}

func (handle *Handle) syncSegmentSearchCanary(
	ctx context.Context,
	index []collectionhelper.IndexEntry,
	signature string,
) (*collectionSegmentProjection, error) {
	projection, err := newCollectionSegmentProjection(handle.scope, handle.collection.Path())
	if err != nil {
		return nil, err
	}
	canary := handle.manager.segmentCanary
	if err := ctx.Err(); err != nil {
		return projection, err
	}
	if canary.signatures[projection.key] == signature {
		return projection, nil
	}
	persisted, err := projection.signature(ctx)
	if err != nil {
		return projection, err
	}
	if persisted == signature {
		canary.signatures[projection.key] = signature
		return projection, nil
	}

	info, complete, err := projection.rebuild(ctx, handle, index, signature)
	if info.Generation != 0 {
		canary.telemetry.rebuilds.Add(1)
	}
	if err != nil {
		return projection, err
	}
	if complete {
		canary.signatures[projection.key] = signature
	} else {
		delete(canary.signatures, projection.key)
	}
	return projection, nil
}

func sameSegmentCanaryRanking(legacy []string, segment []searchengine.Hit) bool {
	if len(legacy) != len(segment) {
		return false
	}
	for position := range legacy {
		if legacy[position] != segment[position].ID {
			return false
		}
	}
	return true
}

type collectionSegmentSearchScope interface {
	CollectionSearchManager() (*searchengine.Manager, error)
	RegisterCollectionSearchIndex(key string)
}

type collectionSegmentCanaryScope interface {
	CollectionSearchCanaryEnabled() bool
}

type collectionSegmentTelemetryScope interface {
	CollectionSearchCanaryTelemetry() *SegmentSearchCanaryTelemetry
}

type collectionSegmentProjection struct {
	manager            *searchengine.Manager
	key                string
	signatureDirectory string
	schema             searchengine.Schema
}

func newCollectionSegmentProjection(scope capabilities.Scope, collectionKey string) (*collectionSegmentProjection, error) {
	if scope == nil {
		return nil, fmt.Errorf("collection: segment search scope is unavailable")
	}
	provider, ok := scope.(collectionSegmentSearchScope)
	if !ok {
		return nil, fmt.Errorf("collection: host search manager is unavailable")
	}
	manager, err := provider.CollectionSearchManager()
	if err != nil {
		return nil, err
	}
	if manager == nil {
		return nil, searchengine.ErrClosed
	}
	schema, err := searchengine.NewSchema(
		searchengine.Text(collectionSegmentField, searchengine.VietnameseAnalyzer()),
	)
	if err != nil {
		return nil, err
	}
	root, err := filepath.Abs(scope.ResolvePath())
	if err != nil {
		return nil, err
	}
	fingerprint := schema.Fingerprint()
	hash := sha256.Sum256([]byte(
		collectionSegmentVersion + "\x00" + filepath.Clean(root) + "\x00" + collectionKey + "\x00" +
			hex.EncodeToString(fingerprint[:]),
	))
	key := "collection-" + hex.EncodeToString(hash[:])
	signatureDirectory, err := capabilities.CleanPath(
		root, ".data", "search-state", collectionSegmentVersion, hex.EncodeToString(hash[:16]),
	)
	if err != nil {
		return nil, err
	}
	provider.RegisterCollectionSearchIndex(key)
	return &collectionSegmentProjection{
		manager: manager, key: key, signatureDirectory: signatureDirectory, schema: schema,
	}, nil
}

func (projection *collectionSegmentProjection) signature(ctx context.Context) (string, error) {
	info, err := projection.manager.Info(ctx, projection.key, projection.schema)
	if err != nil {
		return "", err
	}
	if info.Generation == 0 {
		return "", nil
	}
	path := filepath.Join(
		projection.signatureDirectory,
		collectionSegmentSignatureFilename(info.Generation),
	)
	stat, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !stat.Mode().IsRegular() || stat.Size() < 0 || stat.Size() > collectionSegmentMaximumSignature {
		_ = os.Remove(path)
		return "", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(data) {
		_ = os.Remove(path)
		return "", nil
	}
	return string(data), nil
}

func (projection *collectionSegmentProjection) rebuild(
	ctx context.Context,
	handle *Handle,
	index []collectionhelper.IndexEntry,
	signature string,
) (_ searchengine.IndexInfo, complete bool, returnErr error) {
	if len(signature) > collectionSegmentMaximumSignature || !utf8.ValidString(signature) {
		return searchengine.IndexInfo{}, false, fmt.Errorf("collection: segment signature is invalid")
	}
	replacement, err := projection.manager.BeginReplacement(ctx, projection.key, projection.schema)
	if err != nil {
		return searchengine.IndexInfo{}, false, err
	}
	defer func() { returnErr = errors.Join(returnErr, replacement.Abort()) }()
	complete = true
	for position, entry := range index {
		if position&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return searchengine.IndexInfo{}, false, err
			}
		}
		slug := entry.File.Slug
		document, err := handle.collection.Read(slug)
		if err != nil {
			return searchengine.IndexInfo{}, false, fmt.Errorf(
				"collection: read segment source %q: %w", slug, err,
			)
		}
		title, _ := document.Meta["title"].(string)
		description, _ := document.Meta["description"].(string)
		if err := replacement.Add(ctx, searchengine.Document{
			ID: slug,
			Fields: map[string]string{
				collectionSegmentField: title + "\n" + description + "\n" + document.Body,
			},
		}); err != nil {
			return searchengine.IndexInfo{}, false, err
		}
	}
	info, replaceErr := replacement.Commit(ctx)
	if info.Generation == 0 || (replaceErr != nil && !errors.Is(replaceErr, searchengine.ErrDurabilityUncertain)) {
		return info, false, replaceErr
	}
	signatureErr := projection.writeSignature(info.Generation, signature)
	return info, true, errors.Join(replaceErr, signatureErr)
}

func (projection *collectionSegmentProjection) search(
	ctx context.Context,
	query string,
	limit int,
) ([]searchengine.Hit, error) {
	if limit <= 0 {
		return nil, nil
	}
	if limit > collectionSegmentMaximumSearchHits {
		limit = collectionSegmentMaximumSearchHits
	}
	return projection.manager.Search(
		ctx,
		projection.key,
		projection.schema,
		searchengine.MatchQuery{Field: collectionSegmentField, Text: query},
		searchengine.SearchOptions{Limit: limit},
	)
}

func (projection *collectionSegmentProjection) writeSignature(generation uint64, signature string) (returnErr error) {
	if err := os.MkdirAll(projection.signatureDirectory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(projection.signatureDirectory, ".source-signature-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	closed := false
	defer func() {
		if !closed {
			returnErr = errors.Join(returnErr, temporary.Close())
		}
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temporary.Write([]byte(signature)); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		closed = true
		return err
	}
	closed = true

	finalPath := filepath.Join(projection.signatureDirectory, collectionSegmentSignatureFilename(generation))
	if err := os.Rename(temporaryPath, finalPath); err != nil {
		existing, readErr := os.ReadFile(finalPath)
		if readErr != nil || !bytes.Equal(existing, []byte(signature)) {
			return err
		}
	}
	return projection.pruneSignatures(generation)
}

func (projection *collectionSegmentProjection) pruneSignatures(current uint64) error {
	entries, err := os.ReadDir(projection.signatureDirectory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var removeErrors []error
	for _, entry := range entries {
		generation, valid := parseCollectionSegmentSignatureFilename(entry.Name())
		if !valid || generation == current {
			continue
		}
		if err := os.Remove(filepath.Join(projection.signatureDirectory, entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			removeErrors = append(removeErrors, err)
		}
	}
	return errors.Join(removeErrors...)
}

func collectionSegmentSignatureFilename(generation uint64) string {
	return fmt.Sprintf("%s%020d%s", collectionSegmentSignaturePrefix, generation, collectionSegmentSignatureSuffix)
}

func parseCollectionSegmentSignatureFilename(name string) (uint64, bool) {
	if !strings.HasPrefix(name, collectionSegmentSignaturePrefix) || !strings.HasSuffix(name, collectionSegmentSignatureSuffix) {
		return 0, false
	}
	raw := strings.TrimSuffix(strings.TrimPrefix(name, collectionSegmentSignaturePrefix), collectionSegmentSignatureSuffix)
	if len(raw) != 20 {
		return 0, false
	}
	generation, err := strconv.ParseUint(raw, 10, 64)
	return generation, err == nil && generation != 0
}
