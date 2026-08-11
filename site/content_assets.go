package site

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

const (
	defaultContentAssetMaxEntries = 1024
	defaultContentAssetMaxBytes   = 64 << 20
	defaultContentAssetRetention  = time.Minute
)

var (
	errContentAssetCapacity = errors.New("site: immutable content asset capacity exceeded")
	errInvalidContentAsset  = errors.New("site: invalid immutable content asset")
)

// ContentAsset is one detached content-addressed artifact supplied by a
// generation render plan or returned by a site Runtime. ContentHash is the
// canonical lowercase SHA-256 of Body.
type ContentAsset struct {
	ContentHash string
	Body        []byte
}

// ContentAssetProvider is an optional RenderPlan extension. Activation copies
// its frozen artifacts into the site-owned CAS before publishing the
// generation, so a subsequent HTTP request can still fetch a hash after the
// originating generation has drained.
type ContentAssetProvider interface {
	ContentAssets() ([]ContentAsset, error)
}

type contentAssetLimits struct {
	maxEntries int
	maxBytes   int
	retention  time.Duration
}

type contentAssetEntry struct {
	body       []byte
	references int
	releasedAt time.Time
}

// contentAssetStore is a bounded site-lifetime CAS. Published generations pin
// their hashes until retirement. Unpinned artifacts remain available for a
// short hand-off window because the browser fetches an external script in a
// separate request after receiving HTML.
type contentAssetStore struct {
	mu     sync.RWMutex
	assets map[string]*contentAssetEntry
	bytes  int
	limits contentAssetLimits
	now    func() time.Time
	closed bool
}

func newContentAssetStore() *contentAssetStore {
	return newContentAssetStoreWith(
		contentAssetLimits{
			maxEntries: defaultContentAssetMaxEntries,
			maxBytes:   defaultContentAssetMaxBytes,
			retention:  defaultContentAssetRetention,
		},
		time.Now,
	)
}

func newContentAssetStoreWith(limits contentAssetLimits, now func() time.Time) *contentAssetStore {
	if limits.maxEntries <= 0 {
		limits.maxEntries = defaultContentAssetMaxEntries
	}
	if limits.maxBytes <= 0 {
		limits.maxBytes = defaultContentAssetMaxBytes
	}
	if limits.retention < 0 {
		limits.retention = 0
	}
	if now == nil {
		now = time.Now
	}
	return &contentAssetStore{
		assets: make(map[string]*contentAssetEntry),
		limits: limits,
		now:    now,
	}
}

func (store *contentAssetStore) retain(assets []ContentAsset) ([]string, error) {
	if store == nil {
		return nil, errInvalidContentAsset
	}
	validated := make(map[string][]byte, len(assets))
	for _, asset := range assets {
		if err := validateContentAsset(asset); err != nil {
			return nil, err
		}
		body := append([]byte(nil), asset.Body...)
		if prior, exists := validated[asset.ContentHash]; exists {
			if !bytes.Equal(prior, body) {
				return nil, fmt.Errorf("%w: duplicate hash %s has different bytes", errInvalidContentAsset, asset.ContentHash)
			}
			continue
		}
		validated[asset.ContentHash] = body
	}
	hashes := make([]string, 0, len(validated))
	for contentHash := range validated {
		hashes = append(hashes, contentHash)
	}
	sort.Strings(hashes)

	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed {
		return nil, errInvalidContentAsset
	}

	additionalEntries := 0
	additionalBytes := 0
	for _, contentHash := range hashes {
		if current, exists := store.assets[contentHash]; exists {
			if !bytes.Equal(current.body, validated[contentHash]) {
				return nil, fmt.Errorf("%w: SHA-256 collision for %s", errInvalidContentAsset, contentHash)
			}
			continue
		}
		additionalEntries++
		additionalBytes += len(validated[contentHash])
	}
	if additionalEntries > store.limits.maxEntries || additionalBytes > store.limits.maxBytes {
		return nil, fmt.Errorf("%w: incoming entries=%d/%d bytes=%d/%d",
			errContentAssetCapacity, additionalEntries, store.limits.maxEntries,
			additionalBytes, store.limits.maxBytes)
	}

	entriesAfter := len(store.assets) + additionalEntries
	bytesAfter := store.bytes + additionalBytes
	if entriesAfter > store.limits.maxEntries || bytesAfter > store.limits.maxBytes {
		now := store.now()
		type candidate struct {
			hash       string
			releasedAt time.Time
			bytes      int
		}
		candidates := make([]candidate, 0)
		for contentHash, current := range store.assets {
			if current.references != 0 || current.releasedAt.IsZero() || now.Sub(current.releasedAt) < store.limits.retention {
				continue
			}
			candidates = append(candidates, candidate{
				hash:       contentHash,
				releasedAt: current.releasedAt,
				bytes:      len(current.body),
			})
		}
		sort.Slice(candidates, func(left, right int) bool {
			if !candidates[left].releasedAt.Equal(candidates[right].releasedAt) {
				return candidates[left].releasedAt.Before(candidates[right].releasedAt)
			}
			return candidates[left].hash < candidates[right].hash
		})

		removeCount := 0
		removeBytes := 0
		for _, current := range candidates {
			if entriesAfter-removeCount <= store.limits.maxEntries && bytesAfter-removeBytes <= store.limits.maxBytes {
				break
			}
			removeCount++
			removeBytes += current.bytes
		}
		if entriesAfter-removeCount > store.limits.maxEntries || bytesAfter-removeBytes > store.limits.maxBytes {
			return nil, fmt.Errorf("%w: entries=%d/%d bytes=%d/%d",
				errContentAssetCapacity, entriesAfter, store.limits.maxEntries,
				bytesAfter, store.limits.maxBytes)
		}
		for index := 0; index < removeCount; index++ {
			current := candidates[index]
			delete(store.assets, current.hash)
			store.bytes -= current.bytes
		}
	}

	for _, contentHash := range hashes {
		current := store.assets[contentHash]
		if current == nil {
			body := validated[contentHash]
			current = &contentAssetEntry{body: body}
			store.assets[contentHash] = current
			store.bytes += len(body)
		}
		current.references++
		current.releasedAt = time.Time{}
	}
	return hashes, nil
}

func (store *contentAssetStore) release(hashes []string) {
	if store == nil || len(hashes) == 0 {
		return
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed {
		return
	}
	now := store.now()
	for _, contentHash := range hashes {
		current := store.assets[contentHash]
		if current == nil || current.references == 0 {
			continue
		}
		current.references--
		if current.references == 0 {
			current.releasedAt = now
		}
	}
}

func (store *contentAssetStore) lookup(contentHash string) (ContentAsset, bool) {
	if store == nil || !validContentHash(contentHash) {
		return ContentAsset{}, false
	}
	store.mu.RLock()
	current := store.assets[contentHash]
	closed := store.closed
	var body []byte
	if current != nil && !closed {
		body = append([]byte(nil), current.body...)
	}
	store.mu.RUnlock()
	if current == nil || closed {
		return ContentAsset{}, false
	}
	return ContentAsset{ContentHash: contentHash, Body: body}, true
}

func (store *contentAssetStore) close() {
	if store == nil {
		return
	}
	store.mu.Lock()
	store.closed = true
	store.assets = nil
	store.bytes = 0
	store.mu.Unlock()
}

func validateContentAsset(asset ContentAsset) error {
	if !validContentHash(asset.ContentHash) || len(asset.Body) == 0 {
		return errInvalidContentAsset
	}
	digest := sha256.Sum256(asset.Body)
	if hex.EncodeToString(digest[:]) != asset.ContentHash {
		return fmt.Errorf("%w: content hash does not match body", errInvalidContentAsset)
	}
	return nil
}

func validContentHash(contentHash string) bool {
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
