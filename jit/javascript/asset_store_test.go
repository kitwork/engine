package javascript

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestAssetStoreComposeFreezeLookupAndClose(t *testing.T) {
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewAssetStore(composer, AssetLimits{MaxAssets: 4, MaxBytes: 4 << 20})
	if err != nil {
		t.Fatal(err)
	}

	bundle, err := store.ComposeHTML([]byte(`<main data-kit-component="theme" data-kit-version="1.0.0"></main>`))
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Empty() || store.Len() != 1 {
		t.Fatalf("bundle=%+v assets=%d", bundle, store.Len())
	}
	asset, ok := store.Lookup(bundle.ContentHash)
	if !ok || !bytes.Equal(asset.JavaScript, bundle.JavaScript) || asset.ETag != `"`+bundle.ContentHash+`"` {
		t.Fatalf("lookup=%+v ok=%v", asset, ok)
	}
	asset.JavaScript[0] ^= 0xff
	again, ok := store.Lookup(bundle.ContentHash)
	if !ok || !bytes.Equal(again.JavaScript, bundle.JavaScript) {
		t.Fatal("lookup exposed mutable store bytes")
	}
	if _, err := store.Snapshot(); !errors.Is(err, ErrAssetStoreMutable) {
		t.Fatalf("snapshot before freeze error = %v", err)
	}

	if err := store.Freeze(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Snapshot()
	if err != nil || len(snapshot) != 1 || snapshot[0].ContentHash != bundle.ContentHash ||
		!bytes.Equal(snapshot[0].JavaScript, bundle.JavaScript) {
		t.Fatalf("snapshot=%+v error=%v", snapshot, err)
	}
	snapshot[0].JavaScript[0] ^= 0xff
	if current, found := store.Lookup(bundle.ContentHash); !found || !bytes.Equal(current.JavaScript, bundle.JavaScript) {
		t.Fatal("snapshot exposed mutable store bytes")
	}
	if _, err := store.ComposeHTML([]byte(`<main data-kit-text="count"></main>`)); !errors.Is(err, ErrAssetStoreFrozen) {
		t.Fatalf("compose after freeze error = %v", err)
	}
	store.Close()
	store.Close()
	if _, ok := store.Lookup(bundle.ContentHash); ok || store.Len() != 0 {
		t.Fatal("closed store retained a public asset")
	}
	if _, err := store.ComposeHTML([]byte(`<main data-kit-text="count"></main>`)); !errors.Is(err, ErrAssetStoreClosed) {
		t.Fatalf("compose after close error = %v", err)
	}
}

func TestAssetStorePreparedAppBundlesReuseUnionAndSeparateIdentities(t *testing.T) {
	store, err := NewDefaultAssetStore()
	if err != nil {
		t.Fatal(err)
	}
	docsCounter, err := ScanHTML([]byte(`<html data-kit-app="docs"><main data-kit-component="counter"></main></html>`))
	if err != nil {
		t.Fatal(err)
	}
	docsTheme, err := ScanHTML([]byte(`<html data-kit-app="docs"><main data-kit-component="theme"></main></html>`))
	if err != nil {
		t.Fatal(err)
	}
	adminDropdown, err := ScanHTML([]byte(`<html data-kit-app="admin"><main data-kit-component="dropdown"></main></html>`))
	if err != nil {
		t.Fatal(err)
	}

	docs, err := store.PrepareAppBundle([]ScanResult{docsCounter, docsTheme})
	if err != nil {
		t.Fatal(err)
	}
	docsReversed, err := store.PrepareAppBundle([]ScanResult{docsTheme, docsCounter})
	if err != nil {
		t.Fatal(err)
	}
	if docs.ContentHash != docsReversed.ContentHash || store.Len() != 1 {
		t.Fatalf("same app union was not deterministic: %s %s assets=%d", docs.ContentHash, docsReversed.ContentHash, store.Len())
	}
	for _, source := range [][]byte{
		[]byte(`<html data-kit-app="docs"><main data-kit-component="counter"></main></html>`),
		[]byte(`<html data-kit-app="docs"><main data-kit-component="theme"></main></html>`),
	} {
		bundle, err := store.ComposeHTML(source)
		if err != nil {
			t.Fatal(err)
		}
		if bundle.ContentHash != docs.ContentHash || !reflect.DeepEqual(bundle.Modules, docs.Modules) {
			t.Fatalf("prepared document graph=%s %#v, want app union=%s %#v", bundle.ContentHash, bundle.Modules, docs.ContentHash, docs.Modules)
		}
	}
	admin, err := store.PrepareAppBundle([]ScanResult{adminDropdown})
	if err != nil {
		t.Fatal(err)
	}
	if admin.ContentHash == docs.ContentHash || store.Len() != 2 {
		t.Fatalf("different app identities shared a graph: docs=%s admin=%s assets=%d", docs.ContentHash, admin.ContentHash, store.Len())
	}

	if err := store.Freeze(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PrepareAppBundle([]ScanResult{docsCounter}); !errors.Is(err, ErrAssetStoreFrozen) {
		t.Fatalf("prepare after freeze error=%v, want ErrAssetStoreFrozen", err)
	}
	store.Close()
	if _, err := store.PrepareAppBundle([]ScanResult{docsCounter}); !errors.Is(err, ErrAssetStoreClosed) {
		t.Fatalf("prepare after close error=%v, want ErrAssetStoreClosed", err)
	}
}

func TestAssetStoreConcurrentPreparedAppBundleDeduplicates(t *testing.T) {
	store, err := NewDefaultAssetStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	counter, err := ScanHTML([]byte(`<html data-kit-app="docs"><main data-kit-component="counter"></main></html>`))
	if err != nil {
		t.Fatal(err)
	}
	theme, err := ScanHTML([]byte(`<html data-kit-app="docs"><main data-kit-component="theme"></main></html>`))
	if err != nil {
		t.Fatal(err)
	}

	const workers = 16
	hashes := make(chan string, workers)
	errorsSeen := make(chan error, workers)
	var wait sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			bundle, prepareErr := store.PrepareAppBundle([]ScanResult{counter, theme})
			if prepareErr != nil {
				errorsSeen <- prepareErr
				return
			}
			hashes <- bundle.ContentHash
		}()
	}
	wait.Wait()
	close(hashes)
	close(errorsSeen)
	for prepareErr := range errorsSeen {
		t.Fatal(prepareErr)
	}
	first := ""
	for contentHash := range hashes {
		if first == "" {
			first = contentHash
		} else if contentHash != first {
			t.Fatalf("prepared app hashes %s and %s differ", first, contentHash)
		}
	}
	if store.Len() != 1 {
		t.Fatalf("prepared app assets=%d, want one", store.Len())
	}
}

func TestAssetStoreConcurrentCompositionDeduplicates(t *testing.T) {
	store, err := NewDefaultAssetStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	const workers = 32
	hashes := make(chan string, workers)
	errorsSeen := make(chan error, workers)
	var wait sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			bundle, composeErr := store.ComposeHTML([]byte(`<main data-kit-component="toast"></main>`))
			if composeErr != nil {
				errorsSeen <- composeErr
				return
			}
			hashes <- bundle.ContentHash
		}()
	}
	wait.Wait()
	close(hashes)
	close(errorsSeen)
	for composeErr := range errorsSeen {
		t.Fatal(composeErr)
	}
	first := ""
	for contentHash := range hashes {
		if first == "" {
			first = contentHash
		} else if contentHash != first {
			t.Fatalf("non-deterministic hashes %q and %q", first, contentHash)
		}
	}
	if store.Len() != 1 {
		t.Fatalf("assets=%d, want one deduplicated graph", store.Len())
	}
}

func TestAssetStoreBoundsAndHashValidation(t *testing.T) {
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewAssetStore(composer, AssetLimits{MaxAssets: 1, MaxBytes: 4 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.ComposeHTML([]byte(`<main data-kit-component="dialog"></main>`)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ComposeHTML([]byte(`<main data-kit-component="drawer"></main>`)); !errors.Is(err, ErrAssetCapacity) {
		t.Fatalf("capacity error = %v", err)
	}
	byteBounded, err := NewAssetStore(composer, AssetLimits{MaxAssets: 4, MaxBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer byteBounded.Close()
	if _, err := byteBounded.ComposeHTML([]byte(`<main data-kit-text="count"></main>`)); !errors.Is(err, ErrAssetCapacity) {
		t.Fatalf("byte capacity error = %v", err)
	}
	for _, invalid := range []string{"", strings.Repeat("a", 63), strings.Repeat("A", 64), "../" + strings.Repeat("a", 61)} {
		if ValidContentHash(invalid) {
			t.Fatalf("accepted invalid content hash %q", invalid)
		}
		if _, ok := store.Lookup(invalid); ok {
			t.Fatalf("looked up invalid content hash %q", invalid)
		}
	}
}

func TestInjectRuntimeExactlyOnce(t *testing.T) {
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := composer.ComposeHTML([]byte(`<html data-kit-app="docs"><body></body></html>`))
	if err != nil {
		t.Fatal(err)
	}
	output, err := InjectRuntime([]byte(`<html><head><title>Docs</title></head><body></body></html>`), bundle)
	if err != nil {
		t.Fatal(err)
	}
	tag := `<script data-kitwork-runtime data-kitwork-plan="` + bundle.ContentHash + `" src="/kit.js/` + bundle.ContentHash + `.js" defer></script>`
	if strings.Count(string(output), tag) != 1 || strings.Index(string(output), tag) > strings.Index(string(output), "</head>") {
		t.Fatalf("runtime tag not injected exactly once before head close:\n%s", output)
	}
	if _, err := InjectRuntime(output, bundle); !errors.Is(err, ErrRuntimeMarker) {
		t.Fatalf("duplicate marker error = %v", err)
	}
	commented, err := InjectRuntime([]byte(`<html><head {{ if section == "docs" }}data-ready="yes" {{ end }}><!-- data-kitwork-runtime --><script>"data-kitwork-runtime"</script></head><body><code>&lt;script data-kitwork-runtime&gt;</code><script>"</head>"</script></body></html>`), bundle)
	if err != nil || strings.Count(string(commented), "<script data-kitwork-runtime") != 1 {
		t.Fatalf("comment/raw-text marker caused a false positive: error=%v output=%s", err, commented)
	}
	if strings.Index(string(commented), "<script data-kitwork-runtime") > strings.Index(string(commented), "</head>") {
		t.Fatalf("runtime tag was injected at a raw-text </head> string: %s", commented)
	}
	empty, err := InjectRuntime([]byte(`<main>Static</main>`), Bundle{})
	if err != nil || string(empty) != `<main>Static</main>` {
		t.Fatalf("empty injection output=%q error=%v", empty, err)
	}
}

func TestComposeStandaloneUsesSameGraph(t *testing.T) {
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	components := []ComponentRef{{Name: "theme", Version: "1.0.0"}}
	standalone, err := composer.ComposeStandalone(components, true)
	if err != nil {
		t.Fatal(err)
	}
	native, err := composer.ComposeHTML([]byte(`<html data-kit-app="docs"><main data-kit-component="theme" data-kit-version="1.0.0"></main></html>`))
	if err != nil {
		t.Fatal(err)
	}
	if standalone.ContentHash != native.ContentHash || !bytes.Equal(standalone.JavaScript, native.JavaScript) {
		t.Fatal("standalone and Kitwork-native graph composition drifted")
	}
}
