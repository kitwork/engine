package search

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// GarbageCollectOptions define the generation drain boundary. Zero retains all
// manifests and only removes artifacts that no committed generation references.
type GarbageCollectOptions struct {
	OldestRetainedGeneration uint64
}

// GarbageCollectInfo reports files reclaimed from an index directory.
type GarbageCollectInfo struct {
	OldestRetainedGeneration uint64
	RemovedManifests         int
	RemovedSegments          int
	RemovedSidecars          int
	RemovedTemporary         int
	ReclaimedBytes           int64
}

// GarbageCollect removes obsolete manifests first, then artifacts not reachable
// from any retained generation. Callers must only advance the drain boundary
// after all readers older than OldestRetainedGeneration have closed.
func (writer *IndexWriter) GarbageCollect(
	ctx context.Context,
	options GarbageCollectOptions,
) (GarbageCollectInfo, error) {
	if err := writer.ensureOpen(); err != nil {
		return GarbageCollectInfo{}, err
	}
	if writer.replacement != nil {
		return GarbageCollectInfo{}, ErrReplacementActive
	}
	if ctx == nil {
		return GarbageCollectInfo{}, fmt.Errorf("search: garbage collection context is nil")
	}
	if err := ctx.Err(); err != nil {
		return GarbageCollectInfo{}, err
	}
	if writer.builder.DocumentCount() != 0 || len(writer.pending) != 0 || len(writer.pendingDeletes) != 0 || writer.committed.generation == 0 {
		if _, err := writer.Commit(ctx); err != nil {
			return GarbageCollectInfo{}, err
		}
	}
	if options.OldestRetainedGeneration > writer.committed.generation {
		return GarbageCollectInfo{}, fmt.Errorf(
			"search: retained generation %d exceeds current generation %d",
			options.OldestRetainedGeneration, writer.committed.generation,
		)
	}

	manifests, err := readAllManifests(writer.directory)
	if err != nil {
		return GarbageCollectInfo{}, err
	}
	if len(manifests) == 0 || manifests[len(manifests)-1].generation != writer.committed.generation {
		return GarbageCollectInfo{}, corruptIndexf("writer generation does not match the latest manifest")
	}
	retained := make(map[string]struct{})
	obsoleteManifests := make([]string, 0)
	for _, manifest := range manifests {
		keep := options.OldestRetainedGeneration == 0 ||
			manifest.generation >= options.OldestRetainedGeneration ||
			manifest.generation == writer.committed.generation
		if keep {
			for _, segment := range manifest.segments {
				retained[segment.name] = struct{}{}
				if segment.identifierName != "" {
					retained[segment.identifierName] = struct{}{}
				}
				if segment.deletionName != "" {
					retained[segment.deletionName] = struct{}{}
				}
			}
			continue
		}
		obsoleteManifests = append(obsoleteManifests, manifest.path)
	}

	info := GarbageCollectInfo{OldestRetainedGeneration: options.OldestRetainedGeneration}
	for _, path := range obsoleteManifests {
		if err := ctx.Err(); err != nil {
			return info, err
		}
		bytes, err := regularFileSize(path)
		if err != nil {
			return info, err
		}
		if err := os.Remove(path); err != nil {
			return info, err
		}
		info.RemovedManifests++
		info.ReclaimedBytes = addReclaimedBytes(info.ReclaimedBytes, bytes)
	}
	if len(obsoleteManifests) != 0 {
		if err := syncDirectory(writer.directory); err != nil {
			return info, err
		}
	}

	candidates, err := collectGarbageCandidates(writer.directory, retained)
	if err != nil {
		return info, err
	}
	if err := removeGarbageCandidates(ctx, writer.directory, candidates, &info); err != nil {
		return info, err
	}
	return info, nil
}

// reclaimUnpublishedArtifacts removes immutable files that no manifest can
// reach. The caller holds the directory writer lock, so this is safe even when
// the first replacement stopped before publishing generation one.
func (writer *IndexWriter) reclaimUnpublishedArtifacts(ctx context.Context) (GarbageCollectInfo, error) {
	if err := writer.ensureOpen(); err != nil {
		return GarbageCollectInfo{}, err
	}
	if ctx == nil {
		return GarbageCollectInfo{}, fmt.Errorf("search: garbage collection context is nil")
	}
	if err := ctx.Err(); err != nil {
		return GarbageCollectInfo{}, err
	}
	if writer.replacement != nil || writer.hasPendingChanges() {
		return GarbageCollectInfo{}, ErrPendingChanges
	}
	retained := make(map[string]struct{}, len(writer.committed.segments)*3)
	for _, segment := range writer.committed.segments {
		for _, name := range []string{segment.name, segment.identifierName, segment.deletionName} {
			if name != "" {
				retained[name] = struct{}{}
			}
		}
	}
	candidates, err := collectGarbageCandidates(writer.directory, retained)
	if err != nil {
		return GarbageCollectInfo{}, err
	}
	info := GarbageCollectInfo{}
	if err := removeGarbageCandidates(ctx, writer.directory, candidates, &info); err != nil {
		return info, err
	}
	return info, nil
}

func removeGarbageCandidates(
	ctx context.Context,
	directory string,
	candidates []garbageCandidate,
	info *GarbageCollectInfo,
) error {
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return err
		}
		path := filepath.Join(directory, candidate.name)
		if err := os.Remove(path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
		info.ReclaimedBytes = addReclaimedBytes(info.ReclaimedBytes, candidate.bytes)
		switch candidate.kind {
		case garbageSegment:
			info.RemovedSegments++
		case garbageSidecar:
			info.RemovedSidecars++
		case garbageTemporary:
			info.RemovedTemporary++
		}
	}
	if len(candidates) != 0 {
		if err := syncDirectory(directory); err != nil {
			return err
		}
	}
	return nil
}

func readAllManifests(directory string) ([]indexManifest, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	manifests := make([]indexManifest, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		generation, valid := parseManifestFilename(entry.Name())
		if !valid {
			continue
		}
		manifest, err := readManifest(filepath.Join(directory, entry.Name()))
		if err != nil {
			return nil, err
		}
		if manifest.generation != generation {
			return nil, corruptIndexf(
				"manifest filename generation is %d; body generation is %d", generation, manifest.generation,
			)
		}
		manifests = append(manifests, manifest)
	}
	sort.Slice(manifests, func(left, right int) bool {
		return manifests[left].generation < manifests[right].generation
	})
	for position := 1; position < len(manifests); position++ {
		if manifests[position-1].generation == manifests[position].generation {
			return nil, corruptIndexf("duplicate manifest generation %d", manifests[position].generation)
		}
	}
	return manifests, nil
}

type garbageKind uint8

const (
	garbageSegment garbageKind = iota
	garbageSidecar
	garbageTemporary
)

type garbageCandidate struct {
	name  string
	bytes int64
	kind  garbageKind
}

func collectGarbageCandidates(directory string, retained map[string]struct{}) ([]garbageCandidate, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	candidates := make([]garbageCandidate, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if _, keep := retained[name]; keep {
			continue
		}
		kind, recognized := garbageArtifactKind(name)
		if !recognized {
			continue
		}
		entryInfo, err := entry.Info()
		if err != nil {
			return nil, err
		}
		if !entryInfo.Mode().IsRegular() {
			continue
		}
		candidates = append(candidates, garbageCandidate{name: name, bytes: entryInfo.Size(), kind: kind})
	}
	sort.Slice(candidates, func(left, right int) bool {
		if candidates[left].kind != candidates[right].kind {
			return candidates[left].kind < candidates[right].kind
		}
		return candidates[left].name < candidates[right].name
	})
	return candidates, nil
}

func garbageArtifactKind(name string) (garbageKind, bool) {
	if temporaryBase, temporary := splitTemporaryArtifactName(name); temporary {
		if validArtifactFilename(temporaryBase, ".ks") || validArtifactFilename(temporaryBase, ".ki") ||
			validArtifactFilename(temporaryBase, ".kd") || parseManifestFilenameValid(temporaryBase) ||
			temporaryBase == managedCheckpointFilename {
			return garbageTemporary, true
		}
		return 0, false
	}
	if validArtifactFilename(name, ".ks") {
		return garbageSegment, true
	}
	if validArtifactFilename(name, ".ki") || validArtifactFilename(name, ".kd") {
		return garbageSidecar, true
	}
	return 0, false
}

func splitTemporaryArtifactName(name string) (string, bool) {
	const marker = ".tmp-"
	position := strings.LastIndex(name, marker)
	if position < 1 || len(name)-(position+len(marker)) != 16 {
		return "", false
	}
	for _, character := range name[position+len(marker):] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return "", false
		}
	}
	return name[:position], true
}

func parseManifestFilenameValid(name string) bool {
	_, valid := parseManifestFilename(name)
	return valid
}

func regularFileSize(path string) (int64, error) {
	stat, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	if !stat.Mode().IsRegular() {
		return 0, fmt.Errorf("search: garbage collection target is not a regular file: %s", path)
	}
	return stat.Size(), nil
}

func addReclaimedBytes(current, addition int64) int64 {
	if addition <= 0 {
		return current
	}
	if current > math.MaxInt64-addition {
		return math.MaxInt64
	}
	return current + addition
}
