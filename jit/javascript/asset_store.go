package javascript

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

const (
	// DefaultMaxAssets bounds the number of distinct staged chunks one
	// generation may publish. Identical chunks share one content hash.
	DefaultMaxAssets = 512
	// DefaultMaxAssetBytes bounds the readable JavaScript retained by one
	// generation's preparation store. That private copy disappears when the
	// generation drains; activation has already copied it into the site CAS.
	DefaultMaxAssetBytes = 32 << 20
)

var (
	ErrAssetStoreClosed  = errors.New("kitjs: asset store is closed")
	ErrAssetStoreFrozen  = errors.New("kitjs: asset store is frozen")
	ErrAssetStoreMutable = errors.New("kitjs: asset store is not frozen")
	ErrAssetCapacity     = errors.New("kitjs: asset store capacity exceeded")
	ErrInvalidBundle     = errors.New("kitjs: invalid staged delivery")
	ErrRuntimeMarker     = errors.New("kitjs: authored data-kitwork delivery marker is reserved")
	ErrUnsafeHeadOrder   = errors.New("kitjs: unsafe staged script order in head")
)

// AssetLimits caps generation-owned memory and route-graph cardinality. Zero
// values select the production defaults.
type AssetLimits struct {
	MaxAssets int
	MaxBytes  int
}

// Asset is an immutable staged script snapshot returned by AssetStore.Lookup.
type Asset struct {
	JavaScript  []byte
	ContentHash string
	ETag        string
	Role        JITRole
	Package     string
	Version     string
	Suffix      string
	Name        string
	Integrity   string
}

// AssetReference is the immutable metadata needed to emit one external script.
// The corresponding bytes remain in the generation/site content-addressed
// stores and are never copied into prepared HTML.
type AssetReference struct {
	Role        JITRole
	ContentHash string
	Suffix      string
	Name        string
	Integrity   string
}

// Delivery is the exact ordered staged script set for one prepared document.
// Its fields are private so authored or request-time code cannot forge a graph.
type Delivery struct {
	artifacts []AssetReference
	graphKey  string
}

func (delivery Delivery) Empty() bool { return len(delivery.artifacts) == 0 }

// GraphKey identifies the exact profile, package graph, bytes, and chunk
// layout. The graph script itself carries this key and verifies registrations.
func (delivery Delivery) GraphKey() string { return delivery.graphKey }

// GraphHash is the SHA-256 of the exact graph script referenced by this
// document. Drive compares this engine-emitted marker between documents.
func (delivery Delivery) GraphHash() string {
	for _, artifact := range delivery.artifacts {
		if artifact.Role == JITRoleGraph {
			return artifact.ContentHash
		}
	}
	return ""
}

// Artifacts returns a detached copy in classic-defer execution order.
func (delivery Delivery) Artifacts() []AssetReference {
	return append([]AssetReference(nil), delivery.artifacts...)
}

type storedAsset struct {
	source      []byte
	role        JITRole
	packageName string
	version     string
	suffix      string
	name        string
	integrity   string
}

type preparedGeneration struct {
	deliveries           map[string]Delivery
	sharedComponentNames []string
	fingerprint          string
}

// AssetStore owns one generation's staged composer output. Composition is
// allowed only during generation preparation; Freeze turns the store into a
// read-only request asset table, and Close releases all retained source.
type AssetStore struct {
	mu         sync.RWMutex
	prepareMu  sync.Mutex
	composer   *Composer
	assets     map[string]storedAsset
	generation *preparedGeneration
	bytes      int
	limits     AssetLimits
	frozen     bool
	closed     bool
}

func NewAssetStore(composer *Composer, limits AssetLimits) (*AssetStore, error) {
	if !composer.valid() {
		return nil, fmt.Errorf("%w: nil composer", ErrInvalidModule)
	}
	if limits.MaxAssets < 0 || limits.MaxBytes < 0 {
		return nil, fmt.Errorf("%w: negative asset limit", ErrInvalidModule)
	}
	if limits.MaxAssets == 0 {
		limits.MaxAssets = DefaultMaxAssets
	}
	if limits.MaxBytes == 0 {
		limits.MaxBytes = DefaultMaxAssetBytes
	}
	return &AssetStore{
		composer: composer,
		assets:   make(map[string]storedAsset),
		limits:   limits,
	}, nil
}

func NewDefaultAssetStore(tenantComponents ...ComponentPackage) (*AssetStore, error) {
	composer, err := NewDefaultComposer(tenantComponents...)
	if err != nil {
		return nil, err
	}
	return NewAssetStore(composer, AssetLimits{})
}

// ComposeHTML selects the exact preprepared route graph from source. Outside a
// Kitwork generation it prepares one ProfileKit staged delivery on demand;
// once a generation graph is prepared it never creates a new route artifact.
func (store *AssetStore) ComposeHTML(source []byte) (Delivery, error) {
	if store == nil {
		return Delivery{}, ErrAssetStoreClosed
	}
	store.prepareMu.Lock()
	defer store.prepareMu.Unlock()

	store.mu.RLock()
	closed := store.closed
	frozen := store.frozen
	composer := store.composer
	prepared := store.generation
	store.mu.RUnlock()
	if closed {
		return Delivery{}, ErrAssetStoreClosed
	}
	if frozen {
		return Delivery{}, ErrAssetStoreFrozen
	}

	use, err := ScanHTML(source)
	if err != nil {
		return Delivery{}, err
	}
	profile := ProfileKit
	shared := []string(nil)
	if prepared != nil {
		profile = ProfileHydrate
		shared = prepared.sharedComponentNames
	} else if !use.NeedsRuntime {
		return Delivery{}, nil
	}
	options, err := composer.stagedBuildOptions(use, profile, shared)
	if err != nil {
		return Delivery{}, err
	}
	key := stagedUseKey(options.Profile, options.Components)
	if prepared != nil {
		delivery, exists := prepared.deliveries[key]
		if !exists {
			return Delivery{}, fmt.Errorf("%w: document graph was not prepared with its generation", ErrInvalidBundle)
		}
		return cloneDelivery(delivery), nil
	}

	assembly, err := BuildStaged(options)
	if err != nil {
		return Delivery{}, fmt.Errorf("compose staged KitJS %s delivery: %w", profile, err)
	}
	delivery, err := deliveryFromAssembly(assembly)
	if err != nil {
		return Delivery{}, err
	}
	if err := store.retainArtifacts(assembly.Artifacts()); err != nil {
		return Delivery{}, err
	}
	return delivery, nil
}

// PrepareGeneration prebuilds one Hydrate delivery per exact document graph.
// Runtime, Hydrate, services, components, and identical graphs deduplicate by
// content hash. At least two exact components common to every prepared
// document are grouped into one stable components chunk; all others stay
// individually cacheable.
func (store *AssetStore) PrepareGeneration(scans []ScanResult) error {
	if store == nil {
		return ErrAssetStoreClosed
	}
	store.prepareMu.Lock()
	defer store.prepareMu.Unlock()

	store.mu.RLock()
	closed := store.closed
	frozen := store.frozen
	composer := store.composer
	store.mu.RUnlock()
	if closed {
		return ErrAssetStoreClosed
	}
	if frozen {
		return ErrAssetStoreFrozen
	}
	if len(scans) == 0 {
		return fmt.Errorf("%w: generation requires at least one prepared document", ErrInvalidBundle)
	}

	options := make([]StagedBuildOptions, len(scans))
	for index, scan := range scans {
		prepared, err := composer.stagedBuildOptions(scan, ProfileHydrate, nil)
		if err != nil {
			return err
		}
		options[index] = prepared
	}
	shared := commonComponentNames(options)
	deliveries := make(map[string]Delivery, len(options))
	allArtifacts := make([]JITArtifact, 0, len(options)*4)
	for index := range options {
		options[index].SharedComponentNames = append([]string(nil), shared...)
		assembly, err := BuildStaged(options[index])
		if err != nil {
			return fmt.Errorf("prepare staged KitJS document %d: %w", index, err)
		}
		delivery, err := deliveryFromAssembly(assembly)
		if err != nil {
			return err
		}
		key := stagedUseKey(options[index].Profile, options[index].Components)
		if prior, exists := deliveries[key]; exists && !sameDelivery(prior, delivery) {
			return fmt.Errorf("%w: one document key produced different staged graphs", ErrInvalidBundle)
		}
		deliveries[key] = delivery
		allArtifacts = append(allArtifacts, assembly.Artifacts()...)
	}
	fingerprint := preparedGenerationFingerprint(deliveries, shared)

	store.mu.RLock()
	prior := store.generation
	store.mu.RUnlock()
	if prior != nil {
		if prior.fingerprint != fingerprint {
			return fmt.Errorf("%w: generation already prepared as %s", ErrInvalidBundle, prior.fingerprint)
		}
		return nil
	}
	if err := store.retainArtifacts(allArtifacts); err != nil {
		return err
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed {
		return ErrAssetStoreClosed
	}
	if store.frozen {
		return ErrAssetStoreFrozen
	}
	if store.generation != nil {
		if store.generation.fingerprint != fingerprint {
			return fmt.Errorf("%w: generation already prepared as %s", ErrInvalidBundle, store.generation.fingerprint)
		}
		return nil
	}
	store.generation = &preparedGeneration{
		deliveries:           cloneDeliveries(deliveries),
		sharedComponentNames: append([]string(nil), shared...),
		fingerprint:          fingerprint,
	}
	return nil
}

func commonComponentNames(options []StagedBuildOptions) []string {
	if len(options) < 2 {
		return nil
	}
	common := make(map[string]string, len(options[0].Components))
	for _, component := range options[0].Components {
		common[component.Name] = component.Version
	}
	for _, option := range options[1:] {
		current := make(map[string]string, len(option.Components))
		for _, component := range option.Components {
			current[component.Name] = component.Version
		}
		for name, version := range common {
			if current[name] != version {
				delete(common, name)
			}
		}
	}
	if len(common) < 2 {
		return nil
	}
	names := make([]string, 0, len(common))
	for name := range common {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func stagedUseKey(profile Profile, components []ComponentPackage) string {
	hash := sha256.New()
	writeHashFrame(hash, []byte("kitjs-prepared-use-v1"))
	writeHashFrame(hash, []byte(profile))
	for _, component := range components {
		writeHashFrame(hash, []byte(component.Name))
		writeHashFrame(hash, []byte(component.Version))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func preparedGenerationFingerprint(deliveries map[string]Delivery, shared []string) string {
	hash := sha256.New()
	writeHashFrame(hash, []byte("kitjs-prepared-generation-v1"))
	for _, name := range shared {
		writeHashFrame(hash, []byte("shared-component"))
		writeHashFrame(hash, []byte(name))
	}
	keys := make([]string, 0, len(deliveries))
	for key := range deliveries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		delivery := deliveries[key]
		writeHashFrame(hash, []byte(key))
		writeHashFrame(hash, []byte(delivery.graphKey))
		for _, artifact := range delivery.artifacts {
			writeHashFrame(hash, []byte(artifact.Role))
			writeHashFrame(hash, []byte(artifact.ContentHash))
			writeHashFrame(hash, []byte(artifact.Suffix))
			writeHashFrame(hash, []byte(artifact.Integrity))
		}
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func deliveryFromAssembly(assembly StagedAssembly) (Delivery, error) {
	if !ValidContentHash(assembly.GraphKey()) {
		return Delivery{}, fmt.Errorf("%w: invalid graph key", ErrInvalidBundle)
	}
	artifacts := assembly.Artifacts()
	if len(artifacts) == 0 {
		return Delivery{}, ErrInvalidBundle
	}
	references := make([]AssetReference, len(artifacts))
	for index, artifact := range artifacts {
		if err := validateJITArtifact(artifact); err != nil {
			return Delivery{}, err
		}
		references[index] = AssetReference{
			Role:        artifact.Role(),
			ContentHash: artifact.SHA256(),
			Suffix:      artifact.Suffix(),
			Name:        artifact.Name(),
			Integrity:   artifact.Integrity(),
		}
	}
	delivery := Delivery{artifacts: references, graphKey: assembly.GraphKey()}
	if err := validateDelivery(delivery); err != nil {
		return Delivery{}, err
	}
	return delivery, nil
}

func validateJITArtifact(artifact JITArtifact) error {
	if artifact.Empty() || !validJITRole(artifact.Role()) || !ValidContentHash(artifact.SHA256()) ||
		!validStagedSuffixSyntax(artifact.Suffix()) {
		return ErrInvalidBundle
	}
	if artifact.Name() != artifact.SHA256()+"."+artifact.Suffix()+".js" {
		return fmt.Errorf("%w: staged filename does not match hash and suffix", ErrInvalidBundle)
	}
	if ContentHash(artifact.Bytes()) != artifact.SHA256() {
		return fmt.Errorf("%w: staged content hash does not match JavaScript", ErrInvalidBundle)
	}
	if artifact.Integrity() != contentIntegrity(artifact.SHA256()) {
		return fmt.Errorf("%w: staged integrity does not match content hash", ErrInvalidBundle)
	}
	switch artifact.Role() {
	case JITRoleRuntime, JITRoleHydrate, JITRoleGraph, JITRoleComponents:
		if artifact.Package() != "" || artifact.Version() != "" || artifact.Suffix() != string(artifact.Role()) {
			return fmt.Errorf("%w: invalid %s artifact identity", ErrInvalidBundle, artifact.Role())
		}
	case JITRoleService, JITRoleComponent:
		if artifact.Package() == "" || artifact.Version() == "" || artifact.Suffix() != artifact.Package() ||
			!validStagedPackageSuffix(artifact.Suffix()) {
			return fmt.Errorf("%w: invalid %s package identity", ErrInvalidBundle, artifact.Role())
		}
	}
	return nil
}

func validJITRole(role JITRole) bool {
	switch role {
	case JITRoleRuntime, JITRoleHydrate, JITRoleGraph, JITRoleService, JITRoleComponent, JITRoleComponents:
		return true
	default:
		return false
	}
}

func validateDelivery(delivery Delivery) error {
	if delivery.Empty() || !ValidContentHash(delivery.graphKey) {
		return ErrInvalidBundle
	}
	stage := 0
	graphCount := 0
	for index, artifact := range delivery.artifacts {
		if !validJITRole(artifact.Role) || !ValidContentHash(artifact.ContentHash) ||
			!validStagedSuffixSyntax(artifact.Suffix) ||
			artifact.Name != artifact.ContentHash+"."+artifact.Suffix+".js" ||
			artifact.Integrity != contentIntegrity(artifact.ContentHash) {
			return ErrInvalidBundle
		}
		switch artifact.Role {
		case JITRoleRuntime, JITRoleHydrate, JITRoleGraph, JITRoleComponents:
			if artifact.Suffix != string(artifact.Role) {
				return fmt.Errorf("%w: %s suffix mismatch", ErrInvalidBundle, artifact.Role)
			}
		case JITRoleService, JITRoleComponent:
			if !validStagedPackageSuffix(artifact.Suffix) {
				return fmt.Errorf("%w: invalid %s package suffix", ErrInvalidBundle, artifact.Role)
			}
		}
		switch artifact.Role {
		case JITRoleRuntime:
			if index != 0 || stage != 0 {
				return fmt.Errorf("%w: runtime must be first", ErrInvalidBundle)
			}
			stage = 1
		case JITRoleHydrate:
			if stage != 1 {
				return fmt.Errorf("%w: hydrate loaded out of order", ErrInvalidBundle)
			}
			stage = 2
		case JITRoleGraph:
			if stage != 1 && stage != 2 {
				return fmt.Errorf("%w: graph loaded out of order", ErrInvalidBundle)
			}
			graphCount++
			stage = 3
		case JITRoleService:
			if stage != 3 && stage != 4 {
				return fmt.Errorf("%w: service loaded out of order", ErrInvalidBundle)
			}
			stage = 4
		case JITRoleComponents:
			if stage != 3 && stage != 4 {
				return fmt.Errorf("%w: components bundle loaded out of order", ErrInvalidBundle)
			}
			stage = 5
		case JITRoleComponent:
			if stage < 3 || stage > 6 {
				return fmt.Errorf("%w: component loaded out of order", ErrInvalidBundle)
			}
			stage = 6
		}
	}
	if graphCount != 1 {
		return fmt.Errorf("%w: delivery requires exactly one graph", ErrInvalidBundle)
	}
	return nil
}

func cloneDelivery(delivery Delivery) Delivery {
	return Delivery{
		artifacts: append([]AssetReference(nil), delivery.artifacts...),
		graphKey:  delivery.graphKey,
	}
}

func cloneDeliveries(deliveries map[string]Delivery) map[string]Delivery {
	cloned := make(map[string]Delivery, len(deliveries))
	for key, delivery := range deliveries {
		cloned[key] = cloneDelivery(delivery)
	}
	return cloned
}

func sameDelivery(left, right Delivery) bool {
	if left.graphKey != right.graphKey || len(left.artifacts) != len(right.artifacts) {
		return false
	}
	for index := range left.artifacts {
		if left.artifacts[index] != right.artifacts[index] {
			return false
		}
	}
	return true
}

func (store *AssetStore) retainArtifacts(artifacts []JITArtifact) error {
	validated := make(map[string]storedAsset, len(artifacts))
	for _, artifact := range artifacts {
		if err := validateJITArtifact(artifact); err != nil {
			return err
		}
		current := storedAsset{
			source:      artifact.Bytes(),
			role:        artifact.Role(),
			packageName: artifact.Package(),
			version:     artifact.Version(),
			suffix:      artifact.Suffix(),
			name:        artifact.Name(),
			integrity:   artifact.Integrity(),
		}
		if prior, exists := validated[artifact.SHA256()]; exists {
			if !sameStoredAsset(prior, current) {
				return fmt.Errorf("%w: content hash collision for %s", ErrInvalidBundle, artifact.SHA256())
			}
			continue
		}
		validated[artifact.SHA256()] = current
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed {
		return ErrAssetStoreClosed
	}
	if store.frozen {
		return ErrAssetStoreFrozen
	}
	additionalAssets := 0
	additionalBytes := 0
	for contentHash, incoming := range validated {
		if prior, exists := store.assets[contentHash]; exists {
			if !sameStoredAsset(prior, incoming) {
				return fmt.Errorf("%w: content hash collision for %s", ErrInvalidBundle, contentHash)
			}
			continue
		}
		additionalAssets++
		additionalBytes += len(incoming.source)
	}
	if len(store.assets)+additionalAssets > store.limits.MaxAssets || store.bytes+additionalBytes > store.limits.MaxBytes {
		return fmt.Errorf("%w: assets=%d/%d bytes=%d/%d", ErrAssetCapacity,
			len(store.assets)+additionalAssets, store.limits.MaxAssets,
			store.bytes+additionalBytes, store.limits.MaxBytes)
	}
	for contentHash, incoming := range validated {
		if _, exists := store.assets[contentHash]; exists {
			continue
		}
		store.assets[contentHash] = incoming
		store.bytes += len(incoming.source)
	}
	return nil
}

func sameStoredAsset(left, right storedAsset) bool {
	return left.role == right.role && left.packageName == right.packageName && left.version == right.version &&
		left.suffix == right.suffix && left.name == right.name && left.integrity == right.integrity &&
		bytes.Equal(left.source, right.source)
}

// Freeze ends generation preparation. It is idempotent.
func (store *AssetStore) Freeze() error {
	if store == nil {
		return ErrAssetStoreClosed
	}
	store.prepareMu.Lock()
	defer store.prepareMu.Unlock()
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed {
		return ErrAssetStoreClosed
	}
	store.frozen = true
	return nil
}

// Lookup returns a detached immutable asset snapshot.
func (store *AssetStore) Lookup(contentHash string) (Asset, bool) {
	if store == nil || !ValidContentHash(contentHash) {
		return Asset{}, false
	}
	store.mu.RLock()
	retained, exists := store.assets[contentHash]
	closed := store.closed
	if exists && !closed {
		retained.source = append([]byte(nil), retained.source...)
	}
	store.mu.RUnlock()
	if !exists || closed {
		return Asset{}, false
	}
	return Asset{
		JavaScript:  retained.source,
		ContentHash: contentHash,
		ETag:        `"` + contentHash + `"`,
		Role:        retained.role,
		Package:     retained.packageName,
		Version:     retained.version,
		Suffix:      retained.suffix,
		Name:        retained.name,
		Integrity:   retained.integrity,
	}, true
}

// Snapshot returns every retained artifact in deterministic hash order. It is
// available only after Freeze, allowing activation to copy a complete staged
// set into the site-lifetime CAS without exposing mutable preparation state.
func (store *AssetStore) Snapshot() ([]Asset, error) {
	if store == nil {
		return nil, ErrAssetStoreClosed
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	if store.closed {
		return nil, ErrAssetStoreClosed
	}
	if !store.frozen {
		return nil, ErrAssetStoreMutable
	}
	hashes := make([]string, 0, len(store.assets))
	for contentHash := range store.assets {
		hashes = append(hashes, contentHash)
	}
	sort.Strings(hashes)
	assets := make([]Asset, 0, len(hashes))
	for _, contentHash := range hashes {
		retained := store.assets[contentHash]
		assets = append(assets, Asset{
			JavaScript:  append([]byte(nil), retained.source...),
			ContentHash: contentHash,
			ETag:        `"` + contentHash + `"`,
			Role:        retained.role,
			Package:     retained.packageName,
			Version:     retained.version,
			Suffix:      retained.suffix,
			Name:        retained.name,
			Integrity:   retained.integrity,
		})
	}
	return assets, nil
}

func (store *AssetStore) Len() int {
	if store == nil {
		return 0
	}
	store.mu.RLock()
	length := len(store.assets)
	store.mu.RUnlock()
	return length
}

// Close releases generation-owned artifacts. It is idempotent.
func (store *AssetStore) Close() {
	if store == nil {
		return
	}
	store.prepareMu.Lock()
	defer store.prepareMu.Unlock()
	store.mu.Lock()
	store.closed = true
	store.frozen = true
	store.composer = nil
	store.assets = nil
	store.generation = nil
	store.bytes = 0
	store.mu.Unlock()
}

// ValidContentHash accepts only canonical lowercase SHA-256 hex. Keeping the
// URL alphabet closed prevents aliases and traversal-shaped cache keys.
func ValidContentHash(contentHash string) bool {
	if len(contentHash) != sha256.Size*2 {
		return false
	}
	for index := range len(contentHash) {
		char := contentHash[index]
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func contentIntegrity(contentHash string) string {
	if !ValidContentHash(contentHash) {
		return ""
	}
	digest, err := hex.DecodeString(contentHash)
	if err != nil || len(digest) != sha256.Size {
		return ""
	}
	return "sha256-" + base64.StdEncoding.EncodeToString(digest)
}

// InjectDelivery adds the engine-owned external staged scripts. Authored
// delivery markers are rejected instead of trusted or silently duplicated.
func InjectDelivery(source []byte, delivery Delivery) ([]byte, error) {
	if hasRuntimeMarkerAttribute(source) {
		return nil, ErrRuntimeMarker
	}
	if delivery.Empty() {
		return append([]byte(nil), source...), nil
	}
	if err := validateDelivery(delivery); err != nil {
		return nil, err
	}

	var tags strings.Builder
	tags.Grow(len(delivery.artifacts) * 220)
	for _, artifact := range delivery.artifacts {
		tags.WriteString(`<script data-kitwork-jit="`)
		tags.WriteString(string(artifact.Role))
		tags.WriteString(`" data-kitwork-hash="`)
		tags.WriteString(artifact.ContentHash)
		tags.WriteString(`" src="/jit/`)
		tags.WriteString(artifact.Name)
		tags.WriteString(`" integrity="`)
		tags.WriteString(artifact.Integrity)
		tags.WriteString(`" crossorigin="anonymous" defer></script>`)
	}
	tagBytes := []byte(tags.String())
	index, err := deliveryHeadOffset(source)
	if err != nil {
		return nil, err
	}
	if index >= 0 {
		output := make([]byte, 0, len(source)+len(tagBytes))
		output = append(output, source[:index]...)
		output = append(output, tagBytes...)
		output = append(output, source[index:]...)
		return output, nil
	}
	output := make([]byte, 0, len(source)+len(tagBytes))
	output = append(output, tagBytes...)
	output = append(output, source...)
	return output, nil
}
