package javascript

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
)

func TestGenerationDeduplicatesRepeatedScansBeforePackageMaterialization(t *testing.T) {
	tenantComponent := ComponentPackage{
		Name:    "tenant-heavy",
		Version: "1.0.0",
		Source: []byte("; kit.component(\"tenant-heavy\", { pad: \"" +
			strings.Repeat("x", 256<<10) + "\" });\n"),
	}
	newStore := func() *AssetStore {
		store, err := NewDefaultAssetStoreWithOptions(AssetStoreOptions{}, tenantComponent)
		if err != nil {
			t.Fatal(err)
		}
		return store
	}
	scan, err := ScanHTML([]byte(`<main data-kit-component="tenant-heavy@1.0.0"></main>`))
	if err != nil {
		t.Fatal(err)
	}

	const repeatedDocuments = 512
	scans := make([]ScanResult, repeatedDocuments)
	for index := range scans {
		scans[index] = scan
	}
	probeStore := newStore()
	inputs, _, err := uniqueStagedScanInputs(probeStore.composer, scans, DefaultMaxAssets)
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 1 || inputs[0].documentIndex != 0 {
		t.Fatalf("unique scans = %+v, want one first-document build", inputs)
	}
	probeStore.Close()

	limitedStore := newStore()
	other, err := ScanHTML([]byte(`<main data-kit-scope="count: 0"></main>`))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := uniqueStagedScanInputs(limitedStore.composer, []ScanResult{scan, other}, 3); !errors.Is(err, ErrAssetCapacity) {
		t.Fatalf("unique graph capacity error = %v", err)
	}
	if _, _, err := uniqueStagedScanInputs(limitedStore.composer, []ScanResult{scan}, 1); !errors.Is(err, ErrAssetCapacity) ||
		!strings.Contains(err.Error(), "limit 1") {
		t.Fatalf("sub-core asset limit error = %v", err)
	}
	limitedStore.Close()

	singleStore := newStore()
	singleAllocated := prepareGenerationAllocatedBytes(t, singleStore, []ScanResult{scan})
	singleStore.Close()

	repeatedStore := newStore()
	repeatedAllocated := prepareGenerationAllocatedBytes(t, repeatedStore, scans)
	t.Logf("PrepareGeneration allocation: one scan=%d bytes, %d duplicate scans=%d bytes",
		singleAllocated, repeatedDocuments, repeatedAllocated)
	if repeatedStore.Len() != 4 {
		t.Fatalf("repeated generation assets = %d, want runtime + Hydrate + graph + component", repeatedStore.Len())
	}
	requireGenerationAllocationWithin(t, "repeated generation", repeatedAllocated, singleAllocated+(8<<20))
	repeatedStore.Close()
}

func TestGenerationCanonicalizesProgrammaticDefaultComponentVersionsBeforeDedupe(t *testing.T) {
	store, err := NewDefaultAssetStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	app, err := store.composer.catalog.component("app", "")
	if err != nil {
		t.Fatal(err)
	}
	theme, err := store.composer.catalog.component("theme", "")
	if err != nil {
		t.Fatal(err)
	}
	implicit := ScanResult{Components: []ComponentRef{{Name: "app"}, {Name: "theme"}}}
	explicit := ScanResult{Components: []ComponentRef{
		{Name: app.identity.Name, Version: app.identity.Version},
		{Name: theme.identity.Name, Version: theme.identity.Version},
	}}
	inputs, shared, err := uniqueStagedScanInputs(store.composer, []ScanResult{implicit, explicit}, DefaultMaxAssets)
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 1 {
		t.Fatalf("canonical inputs = %d, want one graph", len(inputs))
	}
	if strings.Join(shared, ",") != "app,theme" {
		t.Fatalf("canonical shared components = %v", shared)
	}
	if err := store.PrepareGeneration([]ScanResult{implicit, explicit}); err != nil {
		t.Fatal(err)
	}
	reordered := explicit
	reordered.Components = []ComponentRef{explicit.Components[1], explicit.Components[0]}
	if err := store.PrepareGeneration([]ScanResult{reordered, implicit}); err != nil {
		t.Fatalf("equivalent canonical generation retry error = %v", err)
	}
}

func TestGenerationMaterializesLargeComponentOnceAcrossDistinctGraphs(t *testing.T) {
	const graphCount = 48
	heavy := ComponentPackage{
		Name:    "tenant-heavy",
		Version: "1.0.0",
		Source: []byte("; kit.component(\"tenant-heavy\", { pad: \"" +
			strings.Repeat("x", 512<<10) + "\" });\n"),
	}
	packages := make([]ComponentPackage, 0, graphCount+1)
	packages = append(packages, heavy)
	scans := make([]ScanResult, graphCount)
	pages := make([][]byte, graphCount)
	for index := range graphCount {
		name := fmt.Sprintf("tenant-leaf-%02d", index)
		packages = append(packages, ComponentPackage{
			Name:    name,
			Version: "1.0.0",
			Source:  []byte("; kit.component(\"" + name + "\", {});\n"),
		})
		pages[index] = []byte(`<main data-kit-component="tenant-heavy@1.0.0"><span data-kit-component="` +
			name + `@1.0.0"></span></main>`)
		scan, err := ScanHTML(pages[index])
		if err != nil {
			t.Fatal(err)
		}
		scans[index] = scan
	}

	store, err := NewDefaultAssetStore(packages...)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	inputs, shared, err := uniqueStagedScanInputs(store.composer, scans, DefaultMaxAssets)
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != graphCount || len(shared) != 0 {
		t.Fatalf("unique inputs/shared = %d/%v, want %d/no bundle", len(inputs), shared, graphCount)
	}
	allocated := prepareGenerationAllocatedBytes(t, store, scans)
	t.Logf("PrepareGeneration allocation: %d distinct graphs sharing %d-byte source = %d bytes",
		graphCount, len(heavy.Source), allocated)
	requireGenerationAllocationWithin(t, "distinct graph generation", allocated, 32<<20)
	wantAssets := 2*graphCount + 3 // runtime + Hydrate + graphs + heavy + leaves
	if store.Len() != wantAssets {
		t.Fatalf("prepared assets = %d, want %d", store.Len(), wantAssets)
	}
	if store.generation == nil || len(store.generation.deliveries) != graphCount {
		t.Fatalf("prepared deliveries = %+v", store.generation)
	}
	if store.generation.candidateArtifactValidations != store.Len() {
		t.Fatalf("candidate artifact validations = %d, want one per %d unique artifacts",
			store.generation.candidateArtifactValidations, store.Len())
	}
	materialized := store.generation.packageMaterializations
	if materialized.components != graphCount+1 || materialized.services != 0 || materialized.bundles != 0 {
		t.Fatalf("package materializations = %+v, want %d components only", materialized, graphCount+1)
	}

	runtime.GC()
	var composeBefore runtime.MemStats
	runtime.ReadMemStats(&composeBefore)
	heavyHash := ""
	var firstDelivery Delivery
	for index, page := range pages {
		delivery, err := store.ComposeHTML(page)
		if err != nil {
			t.Fatalf("compose prepared page %d: %v", index, err)
		}
		found := 0
		for _, artifact := range delivery.Artifacts() {
			if artifact.Role != JITRoleComponent || artifact.Suffix != heavy.Name {
				continue
			}
			found++
			if heavyHash == "" {
				heavyHash = artifact.ContentHash
			} else if artifact.ContentHash != heavyHash {
				t.Fatalf("page %d heavy hash = %s, want %s", index, artifact.ContentHash, heavyHash)
			}
		}
		if found != 1 {
			t.Fatalf("page %d heavy artifact count = %d", index, found)
		}
		if index == 0 {
			firstDelivery = delivery
		}
	}
	var composeAfter runtime.MemStats
	runtime.ReadMemStats(&composeAfter)
	composeAllocated := composeAfter.TotalAlloc - composeBefore.TotalAlloc
	t.Logf("ComposeHTML allocation: %d prepared graphs sharing %d-byte source = %d bytes",
		graphCount, len(heavy.Source), composeAllocated)
	requireGenerationAllocationWithin(t, "prepared graph lookup", composeAllocated, 8<<20)

	lookupVariant := []byte(`<span data-kit-component="tenant-leaf-00@1.0.0"></span>` +
		`<main data-kit-component="tenant-heavy@1.0.0" data-kit-alias="$heavy" data-kit-retain="heavy"></main>`)
	variantDelivery, err := store.ComposeHTML(lookupVariant)
	if err != nil {
		t.Fatalf("canonical prepared lookup variant: %v", err)
	}
	if !sameDelivery(firstDelivery, variantDelivery) {
		t.Fatal("reordered alias/retain metadata changed the prepared delivery")
	}
	heavyCatalog := store.composer.catalog.components[heavy.Name][heavy.Version]
	heavyCatalog.source = nil
	store.composer.catalog.components[heavy.Name][heavy.Version] = heavyCatalog
	afterPoison, err := store.ComposeHTML(lookupVariant)
	if err != nil {
		t.Fatalf("prepared lookup reopened poisoned catalog source: %v", err)
	}
	if !sameDelivery(firstDelivery, afterPoison) {
		t.Fatal("prepared lookup changed after catalog source poison")
	}

	limited, err := NewDefaultAssetStoreWithOptions(AssetStoreOptions{Limits: AssetLimits{
		MaxAssets: wantAssets - 1,
		MaxBytes:  DefaultMaxAssetBytes,
	}}, packages...)
	if err != nil {
		t.Fatal(err)
	}
	defer limited.Close()
	if err := limited.PrepareGeneration(scans); !errors.Is(err, ErrAssetCapacity) {
		t.Fatalf("bounded candidate error = %v", err)
	}
	if limited.Len() != 0 || limited.generation != nil {
		t.Fatalf("failed candidate published assets=%d generation=%+v", limited.Len(), limited.generation)
	}
	sourceLimited, err := NewDefaultAssetStoreWithOptions(AssetStoreOptions{Limits: AssetLimits{
		MaxAssets: DefaultMaxAssets,
		MaxBytes:  len(heavy.Source) - 1,
	}}, packages...)
	if err != nil {
		t.Fatal(err)
	}
	defer sourceLimited.Close()
	if err := sourceLimited.PrepareGeneration(scans); !errors.Is(err, ErrAssetCapacity) ||
		!strings.Contains(err.Error(), "candidate package source") {
		t.Fatalf("package source bound error = %v", err)
	}
	if sourceLimited.Len() != 0 || sourceLimited.generation != nil {
		t.Fatalf("source-bounded candidate published assets=%d generation=%+v",
			sourceLimited.Len(), sourceLimited.generation)
	}

	exactCandidateBytes := store.bytes
	wrappedLimited, err := NewDefaultAssetStoreWithOptions(AssetStoreOptions{Limits: AssetLimits{
		MaxAssets: DefaultMaxAssets,
		MaxBytes:  exactCandidateBytes - 1,
	}}, packages...)
	if err != nil {
		t.Fatal(err)
	}
	defer wrappedLimited.Close()
	if err := wrappedLimited.PrepareGeneration(scans); !errors.Is(err, ErrAssetCapacity) ||
		!strings.Contains(err.Error(), "candidate assets=") {
		t.Fatalf("wrapped artifact byte bound error = %v", err)
	}
	if wrappedLimited.Len() != 0 || wrappedLimited.bytes != 0 || wrappedLimited.generation != nil {
		t.Fatalf("wrapped-byte candidate published assets=%d bytes=%d generation=%+v",
			wrappedLimited.Len(), wrappedLimited.bytes, wrappedLimited.generation)
	}
	exactLimited, err := NewDefaultAssetStoreWithOptions(AssetStoreOptions{Limits: AssetLimits{
		MaxAssets: wantAssets,
		MaxBytes:  exactCandidateBytes,
	}}, packages...)
	if err != nil {
		t.Fatal(err)
	}
	defer exactLimited.Close()
	if err := exactLimited.PrepareGeneration(scans); err != nil {
		t.Fatalf("exact candidate capacity: %v", err)
	}
	if exactLimited.Len() != wantAssets || exactLimited.bytes != exactCandidateBytes {
		t.Fatalf("exact candidate assets/bytes = %d/%d, want %d/%d",
			exactLimited.Len(), exactLimited.bytes, wantAssets, exactCandidateBytes)
	}
}

func TestGenerationPackageCacheMatchesCanonicalStagedBuild(t *testing.T) {
	tests := []struct {
		name       string
		sources    [][]byte
		wantShared string
		wantBundle int
	}{
		{
			name: "common bundle with shared services",
			sources: [][]byte{
				[]byte(`<main data-kit-component="app@1.1.0"><div data-kit-component="theme@3.0.0"></div><div data-kit-component="dialog@2.0.0"></div></main>`),
				[]byte(`<main data-kit-component="app@1.1.0"><div data-kit-component="theme@3.0.0"></div><div data-kit-component="dropdown@2.0.0"></div></main>`),
			},
			wantShared: "app,theme",
			wantBundle: 1,
		},
		{
			name:       "individual component",
			sources:    [][]byte{[]byte(`<main data-kit-component="progress-bar@2.0.0"></main>`)},
			wantBundle: 0,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, err := NewDefaultAssetStore()
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			scans := make([]ScanResult, len(test.sources))
			for index, source := range test.sources {
				scan, scanErr := ScanHTML(source)
				if scanErr != nil {
					t.Fatal(scanErr)
				}
				scans[index] = scan
			}
			inputs, shared, err := uniqueStagedScanInputs(store.composer, scans, DefaultMaxAssets)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(shared, ",") != test.wantShared {
				t.Fatalf("shared components = %v, want %q", shared, test.wantShared)
			}
			cache, err := newStagedGenerationPackageCache(store.composer.catalog, shared, nil, -1)
			if err != nil {
				t.Fatal(err)
			}
			core, err := prepareStagedCoreArtifacts(ProfileHydrate, true)
			if err != nil {
				t.Fatal(err)
			}
			var bundleSource *byte
			for _, input := range inputs {
				cached, err := cache.build(ProfileHydrate, &core, input.components)
				if err != nil {
					t.Fatalf("cached document %d: %v", input.documentIndex, err)
				}
				if cached.ComponentsBundle != nil {
					if bundleSource == nil {
						bundleSource = &cached.ComponentsBundle.source[0]
					} else if bundleSource != &cached.ComponentsBundle.source[0] {
						t.Fatal("common bundle source was rebuilt across cached graphs")
					}
				}
				options, err := store.composer.stagedBuildOptions(scans[input.documentIndex], ProfileHydrate, shared)
				if err != nil {
					t.Fatal(err)
				}
				options.MinifyCore = true
				canonical, err := buildStaged(options, &core)
				if err != nil {
					t.Fatalf("canonical document %d: %v", input.documentIndex, err)
				}
				if cached.GraphKey() != canonical.GraphKey() {
					t.Fatalf("document %d graph key mismatch: cached=%s canonical=%s",
						input.documentIndex, cached.GraphKey(), canonical.GraphKey())
				}
				cachedArtifacts := cached.Artifacts()
				canonicalArtifacts := canonical.Artifacts()
				if len(cachedArtifacts) != len(canonicalArtifacts) {
					t.Fatalf("document %d artifacts = %d, want %d",
						input.documentIndex, len(cachedArtifacts), len(canonicalArtifacts))
				}
				for index := range cachedArtifacts {
					if !sameJITArtifact(cachedArtifacts[index], canonicalArtifacts[index]) {
						t.Fatalf("document %d artifact %d mismatch: cached=%+v canonical=%+v",
							input.documentIndex, index, cachedArtifacts[index], canonicalArtifacts[index])
					}
				}
			}
			if cache.materializations().bundles != test.wantBundle {
				t.Fatalf("bundle builds = %d, want %d", cache.materializations().bundles, test.wantBundle)
			}
			if (bundleSource != nil) != (test.wantBundle == 1) {
				t.Fatalf("bundle source presence = %v, want bundle count %d", bundleSource != nil, test.wantBundle)
			}
		})
	}
}

func TestGenerationSharesPreparedCoreBuffersAndDeduplicatesArtifacts(t *testing.T) {
	option := stagedTestOptions(ProfileHydrate, []string{"app", "tabs"})
	option.MinifyCore = true

	core, err := prepareStagedCoreArtifacts(ProfileHydrate, true)
	if err != nil {
		t.Fatal(err)
	}
	first, err := buildStaged(option, &core)
	if err != nil {
		t.Fatal(err)
	}
	second, err := buildStaged(option, &core)
	if err != nil {
		t.Fatal(err)
	}
	if &first.Runtime.source[0] != &second.Runtime.source[0] {
		t.Fatal("repeated graph assembly cloned the prepared runtime buffer")
	}
	if first.Hydrate == nil || second.Hydrate == nil || &first.Hydrate.source[0] != &second.Hydrate.source[0] {
		t.Fatal("repeated graph assembly cloned the prepared Hydrate buffer")
	}

	accumulator := newJITArtifactAccumulator(len(first.Artifacts()))
	const repeatedArtifacts = 512
	for range repeatedArtifacts {
		if err := accumulator.Add(first.Artifacts()); err != nil {
			t.Fatal(err)
		}
	}
	uniqueArtifacts := accumulator.Artifacts()
	if len(uniqueArtifacts) != len(first.Artifacts()) {
		t.Fatalf("unique artifacts = %d, want %d", len(uniqueArtifacts), len(first.Artifacts()))
	}
	for _, artifact := range uniqueArtifacts {
		if artifact.Role() == JITRoleRuntime && &artifact.source[0] != &first.Runtime.source[0] {
			t.Fatal("artifact deduplication copied the shared runtime buffer")
		}
		if artifact.Role() == JITRoleHydrate && &artifact.source[0] != &first.Hydrate.source[0] {
			t.Fatal("artifact deduplication copied the shared Hydrate buffer")
		}
	}
	assetBound := newBoundedJITArtifactAccumulator(len(first.Artifacts()), AssetLimits{
		MaxAssets: len(first.Artifacts()) - 1,
		MaxBytes:  1 << 30,
	})
	if err := assetBound.Add(first.Artifacts()); !errors.Is(err, ErrAssetCapacity) {
		t.Fatalf("candidate asset-count bound error = %v", err)
	}
	byteBound := newBoundedJITArtifactAccumulator(len(first.Artifacts()), AssetLimits{
		MaxAssets: 1 << 20,
		MaxBytes:  first.Runtime.Size() - 1,
	})
	if err := byteBound.Add([]JITArtifact{first.Runtime}); !errors.Is(err, ErrAssetCapacity) {
		t.Fatalf("candidate byte bound error = %v", err)
	}
}

func TestCandidateAccumulatorReusesValidatedArtifactsWithoutTrustingClaimedHashes(t *testing.T) {
	first, err := newJITArtifact(JITRoleComponent, "first", "1.0.0", "first",
		[]byte("; kit.component(\"first\", {});\n"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := newJITArtifact(JITRoleComponent, "second", "1.0.0", "second",
		[]byte("; kit.component(\"second\", {});\n"))
	if err != nil {
		t.Fatal(err)
	}

	accumulator := newBoundedJITArtifactAccumulator(2, AssetLimits{MaxAssets: 2, MaxBytes: 1 << 20})
	references, err := accumulator.AddAndReferences([]JITArtifact{first, second, first})
	if err != nil {
		t.Fatal(err)
	}
	if len(references) != 3 || references[0].Suffix != "first" || references[1].Suffix != "second" ||
		references[2] != references[0] {
		t.Fatalf("ordered references = %+v", references)
	}
	if accumulator.validations != 2 || len(accumulator.byHash) != 2 {
		t.Fatalf("candidate validations/artifacts = %d/%d, want 2/2",
			accumulator.validations, len(accumulator.byHash))
	}

	changedBytes := first
	changedBytes.source = append([]byte(nil), first.source...)
	changedBytes.source[len(changedBytes.source)-2] = ' '
	if err := accumulator.Add([]JITArtifact{changedBytes}); !errors.Is(err, ErrInvalidBundle) ||
		!strings.Contains(err.Error(), "content hash collision") {
		t.Fatalf("changed bytes with prior claimed hash error = %v", err)
	}
	changedMetadata := first
	changedMetadata.role = JITRoleService
	if err := accumulator.Add([]JITArtifact{changedMetadata}); !errors.Is(err, ErrInvalidBundle) ||
		!strings.Contains(err.Error(), "content hash collision") {
		t.Fatalf("changed metadata with prior claimed hash error = %v", err)
	}

	partial := newBoundedJITArtifactAccumulator(1, AssetLimits{MaxAssets: 1, MaxBytes: 1 << 20})
	if _, err := partial.AddAndReferences([]JITArtifact{first, second}); !errors.Is(err, ErrAssetCapacity) {
		t.Fatalf("partial candidate capacity error = %v", err)
	}
	if len(partial.byHash) != 1 || partial.byHash[first.sha256].sha256 != first.sha256 {
		t.Fatalf("candidate-local partial accumulator = %+v", partial.byHash)
	}
}

func TestGenerationPackageSourceZeroLimitIsBounded(t *testing.T) {
	store, err := NewDefaultAssetStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	cache, err := newStagedGenerationPackageCache(store.composer.catalog, nil, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cache.prepareComponent(ComponentVersion{Name: "app", Version: "1.1.0"}); !errors.Is(err, ErrAssetCapacity) || !strings.Contains(err.Error(), "limit=0") {
		t.Fatalf("zero remaining package-source limit error = %v", err)
	}
	if len(cache.components) != 0 || cache.sourceBytes != 0 {
		t.Fatalf("zero-limit cache materialized components/bytes = %d/%d",
			len(cache.components), cache.sourceBytes)
	}
}

func prepareGenerationAllocatedBytes(t *testing.T, store *AssetStore, scans []ScanResult) uint64 {
	t.Helper()
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	if err := store.PrepareGeneration(scans); err != nil {
		t.Fatal(err)
	}
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

func BenchmarkPrepareGenerationDistinctGraphsSharedLargeComponent(b *testing.B) {
	for _, graphCount := range []int{1, 16, 64} {
		b.Run(fmt.Sprintf("graphs-%d", graphCount), func(b *testing.B) {
			heavy := ComponentPackage{
				Name:    "tenant-heavy",
				Version: "1.0.0",
				Source: []byte("; kit.component(\"tenant-heavy\", { pad: \"" +
					strings.Repeat("x", 256<<10) + "\" });\n"),
			}
			packages := make([]ComponentPackage, 0, graphCount+1)
			packages = append(packages, heavy)
			scans := make([]ScanResult, graphCount)
			for index := range graphCount {
				name := fmt.Sprintf("tenant-leaf-%02d", index)
				packages = append(packages, ComponentPackage{
					Name:    name,
					Version: "1.0.0",
					Source:  []byte("; kit.component(\"" + name + "\", {});\n"),
				})
				scan, err := ScanHTML([]byte(`<main data-kit-component="tenant-heavy@1.0.0"><span data-kit-component="` +
					name + `@1.0.0"></span></main>`))
				if err != nil {
					b.Fatal(err)
				}
				scans[index] = scan
			}
			composer, err := NewDefaultComposer(packages...)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				store, err := NewAssetStore(composer, AssetLimits{})
				if err != nil {
					b.Fatal(err)
				}
				if err := store.PrepareGeneration(scans); err != nil {
					b.Fatal(err)
				}
				store.Close()
			}
		})
	}
}
