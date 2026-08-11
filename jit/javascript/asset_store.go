package javascript

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
)

const (
	// DefaultMaxAssets bounds the number of distinct component graphs one
	// generation may publish. Identical graphs share one content hash.
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
	ErrInvalidBundle     = errors.New("kitjs: invalid composed bundle")
	ErrRuntimeMarker     = errors.New("kitjs: authored data-kitwork-runtime marker is reserved")
)

// AssetLimits caps generation-owned memory and user-controlled graph
// cardinality. Zero values select the production defaults.
type AssetLimits struct {
	MaxAssets int
	MaxBytes  int
}

// Asset is an immutable snapshot returned by AssetStore.Lookup.
type Asset struct {
	JavaScript  []byte
	ContentHash string
	ETag        string
}

// AssetStore owns one generation's composer output. Composition is allowed
// only during generation preparation; Freeze turns the store into a read-only
// request asset table, and Close releases all retained source.
type AssetStore struct {
	mu       sync.RWMutex
	composer *Composer
	assets   map[string][]byte
	apps     map[string]preparedAppBundle
	bytes    int
	limits   AssetLimits
	frozen   bool
	closed   bool
}

type preparedAppBundle struct {
	contentHash string
	modules     []ModuleID
}

func NewAssetStore(composer *Composer, limits AssetLimits) (*AssetStore, error) {
	if composer == nil || composer.registry == nil {
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
		assets:   make(map[string][]byte),
		apps:     make(map[string]preparedAppBundle),
		limits:   limits,
	}, nil
}

func NewDefaultAssetStore() (*AssetStore, error) {
	composer, err := NewDefaultComposer()
	if err != nil {
		return nil, err
	}
	return NewAssetStore(composer, AssetLimits{})
}

// ComposeHTML selects the exact graph from source and retains its artifact.
// The source scan is the only authority for module selection.
func (store *AssetStore) ComposeHTML(source []byte) (Bundle, error) {
	if store == nil {
		return Bundle{}, ErrAssetStoreClosed
	}
	store.mu.RLock()
	if store.closed {
		store.mu.RUnlock()
		return Bundle{}, ErrAssetStoreClosed
	}
	if store.frozen {
		store.mu.RUnlock()
		return Bundle{}, ErrAssetStoreFrozen
	}
	composer := store.composer
	store.mu.RUnlock()

	use, err := ScanHTML(source)
	if err != nil {
		return Bundle{}, err
	}
	if use.HasApp {
		if bundle, ok := store.preparedApp(use.App); ok {
			return bundle, nil
		}
	}
	plan, err := composer.registry.resolveUse(use)
	if err != nil {
		return Bundle{}, err
	}
	bundle, err := composer.composePlan(plan)
	if err != nil || bundle.Empty() {
		return bundle, err
	}
	if err := store.retain(bundle); err != nil {
		return Bundle{}, err
	}
	return bundle, nil
}

// PrepareAppBundle composes and retains the one generation-owned graph shared
// by every prepared document in an application. Call it before route renders
// are prepared; subsequent ComposeHTML calls for that app reuse the exact
// content hash and never create per-route variants.
func (store *AssetStore) PrepareAppBundle(scans []ScanResult) (Bundle, error) {
	if store == nil {
		return Bundle{}, ErrAssetStoreClosed
	}
	store.mu.RLock()
	if store.closed {
		store.mu.RUnlock()
		return Bundle{}, ErrAssetStoreClosed
	}
	if store.frozen {
		store.mu.RUnlock()
		return Bundle{}, ErrAssetStoreFrozen
	}
	composer := store.composer
	store.mu.RUnlock()

	merged, err := mergeAppScans(scans)
	if err != nil {
		return Bundle{}, err
	}
	bundle, err := composer.ComposeAppScans(scans)
	if err != nil {
		return Bundle{}, err
	}
	if bundle.Empty() {
		return Bundle{}, ErrInvalidBundle
	}
	if err := store.retain(bundle); err != nil {
		return Bundle{}, err
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed {
		return Bundle{}, ErrAssetStoreClosed
	}
	if store.frozen {
		return Bundle{}, ErrAssetStoreFrozen
	}
	if prior, exists := store.apps[merged.App]; exists && prior.contentHash != bundle.ContentHash {
		return Bundle{}, fmt.Errorf("%w: application %q already prepared as %s", ErrInvalidBundle, merged.App, prior.contentHash)
	}
	store.apps[merged.App] = preparedAppBundle{
		contentHash: bundle.ContentHash,
		modules:     append([]ModuleID(nil), bundle.Modules...),
	}
	return bundle, nil
}

func (store *AssetStore) preparedApp(identity string) (Bundle, bool) {
	store.mu.RLock()
	prepared, exists := store.apps[identity]
	source := store.assets[prepared.contentHash]
	closed := store.closed
	frozen := store.frozen
	if exists && !closed && !frozen {
		source = append([]byte(nil), source...)
	}
	modules := append([]ModuleID(nil), prepared.modules...)
	store.mu.RUnlock()
	if !exists || closed || frozen || len(source) == 0 {
		return Bundle{}, false
	}
	return Bundle{
		JavaScript:  source,
		ContentHash: prepared.contentHash,
		Modules:     modules,
	}, true
}

func (store *AssetStore) retain(bundle Bundle) error {
	if err := validateBundle(bundle); err != nil {
		return err
	}
	source := append([]byte(nil), bundle.JavaScript...)

	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed {
		return ErrAssetStoreClosed
	}
	if store.frozen {
		return ErrAssetStoreFrozen
	}
	if prior, exists := store.assets[bundle.ContentHash]; exists {
		if !bytes.Equal(prior, source) {
			return fmt.Errorf("%w: content hash collision for %s", ErrInvalidBundle, bundle.ContentHash)
		}
		return nil
	}
	if len(store.assets) >= store.limits.MaxAssets || store.bytes+len(source) > store.limits.MaxBytes {
		return fmt.Errorf("%w: assets=%d/%d bytes=%d/%d", ErrAssetCapacity,
			len(store.assets)+1, store.limits.MaxAssets, store.bytes+len(source), store.limits.MaxBytes)
	}
	store.assets[bundle.ContentHash] = source
	store.bytes += len(source)
	return nil
}

// Freeze ends generation preparation. It is idempotent.
func (store *AssetStore) Freeze() error {
	if store == nil {
		return ErrAssetStoreClosed
	}
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
	source, exists := store.assets[contentHash]
	closed := store.closed
	if exists && !closed {
		source = append([]byte(nil), source...)
	}
	store.mu.RUnlock()
	if !exists || closed {
		return Asset{}, false
	}
	return Asset{
		JavaScript:  source,
		ContentHash: contentHash,
		ETag:        `"` + contentHash + `"`,
	}, true
}

// Snapshot returns every retained artifact in deterministic hash order. It is
// available only after Freeze, allowing activation to copy a complete graph
// set into the site-lifetime content-addressed store without exposing mutable
// preparation state.
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
		assets = append(assets, Asset{
			JavaScript:  append([]byte(nil), store.assets[contentHash]...),
			ContentHash: contentHash,
			ETag:        `"` + contentHash + `"`,
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
	store.mu.Lock()
	store.closed = true
	store.frozen = true
	store.composer = nil
	store.assets = nil
	store.apps = nil
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

func validateBundle(bundle Bundle) error {
	if bundle.Empty() || !ValidContentHash(bundle.ContentHash) {
		return ErrInvalidBundle
	}
	digest := sha256.Sum256(bundle.JavaScript)
	if hex.EncodeToString(digest[:]) != bundle.ContentHash {
		return fmt.Errorf("%w: content hash does not match JavaScript", ErrInvalidBundle)
	}
	return nil
}

// InjectRuntime adds the one engine-owned external runtime script. Authored
// runtime markers are rejected instead of trusted or silently duplicated.
func InjectRuntime(source []byte, bundle Bundle) ([]byte, error) {
	if hasRuntimeMarkerAttribute(source) {
		return nil, ErrRuntimeMarker
	}
	if bundle.Empty() {
		return append([]byte(nil), source...), nil
	}
	if err := validateBundle(bundle); err != nil {
		return nil, err
	}
	tag := []byte(`<script data-kitwork-runtime data-kitwork-plan="` + bundle.ContentHash +
		`" src="/kit.js/` + bundle.ContentHash + `.js" defer></script>`)
	if index := headCloseOffset(source); index >= 0 {
		output := make([]byte, 0, len(source)+len(tag))
		output = append(output, source[:index]...)
		output = append(output, tag...)
		output = append(output, source[index:]...)
		return output, nil
	}
	output := make([]byte, 0, len(source)+len(tag))
	output = append(output, tag...)
	output = append(output, source...)
	return output, nil
}
