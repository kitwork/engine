package site

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"
)

func testContentAsset(body string) ContentAsset {
	digest := sha256.Sum256([]byte(body))
	return ContentAsset{
		ContentHash: hex.EncodeToString(digest[:]),
		Body:        []byte(body),
	}
}

func TestContentAssetStorePinsAndRetainsAcrossGenerationHandoff(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	store := newContentAssetStoreWith(contentAssetLimits{
		maxEntries: 2,
		maxBytes:   1024,
		retention:  time.Minute,
	}, func() time.Time { return now })
	defer store.close()

	first := testContentAsset("first generation")
	second := testContentAsset("second generation")
	third := testContentAsset("third generation")
	firstHashes, err := store.retain([]ContentAsset{first})
	if err != nil {
		t.Fatal(err)
	}
	secondHashes, err := store.retain([]ContentAsset{second})
	if err != nil {
		t.Fatal(err)
	}
	store.release(firstHashes)
	if retained, ok := store.lookup(first.ContentHash); !ok || string(retained.Body) != string(first.Body) {
		t.Fatal("retired generation asset disappeared during the hand-off window")
	}

	if _, err := store.retain([]ContentAsset{third}); !errors.Is(err, errContentAssetCapacity) {
		t.Fatalf("unexpired hand-off asset was evicted: %v", err)
	}
	now = now.Add(time.Minute + time.Second)
	thirdHashes, err := store.retain([]ContentAsset{third})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := store.lookup(first.ContentHash); ok {
		t.Fatal("expired unpinned asset was not evicted at the capacity boundary")
	}
	if _, ok := store.lookup(second.ContentHash); !ok {
		t.Fatal("pinned current-generation asset was evicted")
	}
	if _, ok := store.lookup(third.ContentHash); !ok {
		t.Fatal("new generation asset was not retained")
	}

	store.release(secondHashes)
	store.release(thirdHashes)
}

func TestContentAssetStoreRejectsInvalidContent(t *testing.T) {
	store := newContentAssetStore()
	defer store.close()
	asset := testContentAsset("valid")
	asset.ContentHash = "A" + asset.ContentHash[1:]
	if _, err := store.retain([]ContentAsset{asset}); !errors.Is(err, errInvalidContentAsset) {
		t.Fatalf("invalid hash error = %v", err)
	}
}

func TestContentAssetStorePreservesAndSealsDeliveryMetadata(t *testing.T) {
	store := newContentAssetStore()
	defer store.close()

	asset := testContentAsset("staged runtime")
	asset.Role = "runtime"
	asset.Suffix = "runtime"
	if _, err := store.retain([]ContentAsset{asset}); err != nil {
		t.Fatal(err)
	}
	retained, ok := store.lookup(asset.ContentHash)
	if !ok || retained.Role != asset.Role || retained.Suffix != asset.Suffix {
		t.Fatalf("retained metadata = role %q suffix %q", retained.Role, retained.Suffix)
	}

	mismatch := asset
	mismatch.Role = "component"
	mismatch.Suffix = "dialog"
	if _, err := store.retain([]ContentAsset{mismatch}); !errors.Is(err, errInvalidContentAsset) {
		t.Fatalf("metadata mismatch error = %v", err)
	}

	incomplete := testContentAsset("incomplete metadata")
	incomplete.Role = "graph"
	if _, err := store.retain([]ContentAsset{incomplete}); !errors.Is(err, errInvalidContentAsset) {
		t.Fatalf("incomplete metadata error = %v", err)
	}

	wrongSuffix := testContentAsset("wrong fixed suffix")
	wrongSuffix.Role = "runtime"
	wrongSuffix.Suffix = "dialog"
	if _, err := store.retain([]ContentAsset{wrongSuffix}); !errors.Is(err, errInvalidContentAsset) {
		t.Fatalf("fixed role suffix error = %v", err)
	}
}
