package javascript

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestAssetStorePreservesStagedCASLifecycleAndDetachedBytes(t *testing.T) {
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewAssetStore(composer, AssetLimits{MaxAssets: 8, MaxBytes: 4 << 20})
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := store.ComposeHTML([]byte(`<main data-kit-component="progress-bar" data-kit-version="2.0.0"></main>`))
	if err != nil {
		t.Fatal(err)
	}
	roles := deliveryRoles(delivery)
	if strings.Join(roles, ",") != "runtime,graph,service,component" {
		t.Fatalf("delivery roles = %v", roles)
	}
	for _, reference := range delivery.Artifacts() {
		asset, ok := store.Lookup(reference.ContentHash)
		if !ok || asset.ETag != `"`+reference.ContentHash+`"` || asset.Role != reference.Role ||
			asset.Suffix != reference.Suffix || asset.Name != reference.Name || asset.Integrity != reference.Integrity ||
			asset.Integrity != contentIntegrity(reference.ContentHash) || ContentHash(asset.JavaScript) != reference.ContentHash {
			t.Fatalf("lookup=%+v reference=%+v ok=%v", asset, reference, ok)
		}
		asset.JavaScript[0] ^= 0xff
		again, ok := store.Lookup(reference.ContentHash)
		if !ok || ContentHash(again.JavaScript) != reference.ContentHash {
			t.Fatal("lookup exposed mutable retained bytes")
		}
	}
	if _, err := store.Snapshot(); !errors.Is(err, ErrAssetStoreMutable) {
		t.Fatalf("snapshot before freeze error=%v", err)
	}
	if err := store.Freeze(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Snapshot()
	if err != nil || len(snapshot) != len(delivery.Artifacts()) {
		t.Fatalf("snapshot=%+v error=%v", snapshot, err)
	}
	if _, err := store.ComposeHTML([]byte(`<main data-kit-scope="count: 0"></main>`)); !errors.Is(err, ErrAssetStoreFrozen) {
		t.Fatalf("compose after freeze error=%v", err)
	}
	store.Close()
	store.Close()
	if _, ok := store.Lookup(delivery.GraphHash()); ok || store.Len() != 0 {
		t.Fatal("closed store retained public state")
	}
}

func TestAssetStorePreparedGenerationUsesRouteGraphsAndSharedBaseChunks(t *testing.T) {
	store, err := NewDefaultAssetStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	scopeSource := []byte(`<html><main data-kit-scope="count: 0"></main></html>`)
	progressSource := []byte(`<html><main data-kit-component="progress-bar" data-kit-version="2.0.0"></main></html>`)
	scope, err := ScanHTML(scopeSource)
	if err != nil {
		t.Fatal(err)
	}
	progress, err := ScanHTML(progressSource)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PrepareGeneration([]ScanResult{scope, progress}); err != nil {
		t.Fatal(err)
	}
	scopeDelivery, err := store.ComposeHTML(scopeSource)
	if err != nil {
		t.Fatal(err)
	}
	progressDelivery, err := store.ComposeHTML(progressSource)
	if err != nil {
		t.Fatal(err)
	}
	if scopeDelivery.GraphHash() == progressDelivery.GraphHash() || scopeDelivery.GraphKey() == progressDelivery.GraphKey() {
		t.Fatal("different route package graphs reused one graph artifact")
	}
	if scopeDelivery.GraphHash() == scopeDelivery.GraphKey() || progressDelivery.GraphHash() == progressDelivery.GraphKey() {
		t.Fatal("graph manifest identity was confused with the exact graph-script content hash")
	}
	for _, role := range []JITRole{JITRoleRuntime, JITRoleHydrate} {
		left, _ := deliveryArtifact(scopeDelivery, role, "")
		right, _ := deliveryArtifact(progressDelivery, role, "")
		if left.ContentHash == "" || left.ContentHash != right.ContentHash {
			t.Fatalf("%s chunk was not shared: left=%+v right=%+v", role, left, right)
		}
	}
	if _, ok := deliveryArtifact(scopeDelivery, JITRoleComponent, "progress-bar"); ok {
		t.Fatal("scope-only route received the progress-bar component")
	}
	if _, ok := deliveryArtifact(progressDelivery, JITRoleService, "progress"); !ok {
		t.Fatal("progress route omitted its progress service")
	}
	if _, ok := deliveryArtifact(progressDelivery, JITRoleComponent, "progress-bar"); !ok {
		t.Fatal("progress route omitted its component chunk")
	}
	if store.Len() != 6 {
		t.Fatalf("prepared assets=%d, want runtime + hydrate + 2 graphs + service + component", store.Len())
	}
}

func TestAssetStoreBundlesOnlyComponentsSharedByEveryPreparedDocument(t *testing.T) {
	store, err := NewDefaultAssetStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	homeSource := []byte(`<html><body data-kit-component="app" data-kit-version="1.1.0" data-kit-as="$app"><div data-kit-component="theme" data-kit-version="3.0.0"></div><main data-kit-component="dialog" data-kit-version="2.0.0"></main></body></html>`)
	docsSource := []byte(`<html><body data-kit-component="app" data-kit-version="1.1.0" data-kit-as="$app"><div data-kit-component="theme" data-kit-version="3.0.0"></div><main data-kit-component="dropdown" data-kit-version="2.0.0"></main></body></html>`)
	home, err := ScanHTML(homeSource)
	if err != nil {
		t.Fatal(err)
	}
	docs, err := ScanHTML(docsSource)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PrepareGeneration([]ScanResult{home, docs}); err != nil {
		t.Fatal(err)
	}
	homeDelivery, err := store.ComposeHTML(homeSource)
	if err != nil {
		t.Fatal(err)
	}
	docsDelivery, err := store.ComposeHTML(docsSource)
	if err != nil {
		t.Fatal(err)
	}
	homeBundle, ok := deliveryArtifact(homeDelivery, JITRoleComponents, "components")
	if !ok {
		t.Fatal("home route omitted the shared components bundle")
	}
	docsBundle, ok := deliveryArtifact(docsDelivery, JITRoleComponents, "components")
	if !ok || docsBundle.ContentHash != homeBundle.ContentHash {
		t.Fatalf("shared bundle mismatch: home=%+v docs=%+v", homeBundle, docsBundle)
	}
	for _, delivery := range []Delivery{homeDelivery, docsDelivery} {
		if _, ok := deliveryArtifact(delivery, JITRoleComponent, "app"); ok {
			t.Fatal("app appeared both bundled and singular")
		}
		if _, ok := deliveryArtifact(delivery, JITRoleComponent, "theme"); ok {
			t.Fatal("theme appeared both bundled and singular")
		}
	}
	if _, ok := deliveryArtifact(homeDelivery, JITRoleComponent, "dialog"); !ok {
		t.Fatal("home-specific dialog was not kept singular")
	}
	if _, ok := deliveryArtifact(docsDelivery, JITRoleComponent, "dropdown"); !ok {
		t.Fatal("docs-specific dropdown was not kept singular")
	}
	if homeDelivery.GraphHash() == docsDelivery.GraphHash() {
		t.Fatal("different singular components unexpectedly shared a graph")
	}
}

func TestAssetStoreConcurrentGenerationPreparationDeduplicates(t *testing.T) {
	store, err := NewDefaultAssetStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	scope, err := ScanHTML([]byte(`<main data-kit-scope="count: 0"></main>`))
	if err != nil {
		t.Fatal(err)
	}
	const workers = 16
	errorsSeen := make(chan error, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if err := store.PrepareGeneration([]ScanResult{scope}); err != nil {
				errorsSeen <- err
			}
		}()
	}
	wait.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		t.Fatal(err)
	}
	if store.Len() != 3 {
		t.Fatalf("generation assets=%d, want runtime + hydrate + graph", store.Len())
	}
}

func TestAssetStoreRejectsGenerationReplacementWithoutRetainingCandidate(t *testing.T) {
	store, err := NewDefaultAssetStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	scope, err := ScanHTML([]byte(`<main data-kit-scope="count: 0"></main>`))
	if err != nil {
		t.Fatal(err)
	}
	progress, err := ScanHTML([]byte(`<main data-kit-component="progress-bar" data-kit-version="2.0.0"></main>`))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PrepareGeneration([]ScanResult{scope}); err != nil {
		t.Fatal(err)
	}
	before := store.Len()
	if err := store.PrepareGeneration([]ScanResult{progress}); !errors.Is(err, ErrInvalidBundle) {
		t.Fatalf("replacement error=%v", err)
	}
	if store.Len() != before {
		t.Fatalf("rejected replacement retained assets: before=%d after=%d", before, store.Len())
	}
}

func TestAssetStoreConcurrentCompositionDeduplicatesChunks(t *testing.T) {
	store, err := NewDefaultAssetStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	const workers = 24
	results := make(chan string, workers)
	errorsSeen := make(chan error, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			delivery, err := store.ComposeHTML([]byte(`<main data-kit-component="progress-bar" data-kit-version="2.0.0"></main>`))
			if err != nil {
				errorsSeen <- err
				return
			}
			results <- delivery.GraphHash()
		}()
	}
	wait.Wait()
	close(results)
	close(errorsSeen)
	for err := range errorsSeen {
		t.Fatal(err)
	}
	first := ""
	for result := range results {
		if first == "" {
			first = result
		} else if result != first {
			t.Fatalf("graph hashes %s and %s differ", first, result)
		}
	}
	if store.Len() != 4 {
		t.Fatalf("assets=%d, want runtime + graph + service + component", store.Len())
	}
}

func TestAssetStoreBoundsAreAtomicAndHashValidationIsClosed(t *testing.T) {
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewAssetStore(composer, AssetLimits{MaxAssets: 2, MaxBytes: 4 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.ComposeHTML([]byte(`<main data-kit-scope="count: 0"></main>`)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ComposeHTML([]byte(`<main data-kit-component="progress-bar" data-kit-version="2.0.0"></main>`)); !errors.Is(err, ErrAssetCapacity) {
		t.Fatalf("capacity error=%v", err)
	}
	if store.Len() != 2 {
		t.Fatalf("failed batch partially mutated the store: assets=%d", store.Len())
	}
	for _, invalid := range []string{"", strings.Repeat("a", 63), strings.Repeat("A", 64), "../" + strings.Repeat("a", 61)} {
		if ValidContentHash(invalid) {
			t.Fatalf("accepted invalid hash %q", invalid)
		}
	}
}

func TestInjectDeliveryPreservesOrderedRoleAndHashTags(t *testing.T) {
	store, err := NewDefaultAssetStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	delivery, err := store.ComposeHTML([]byte(`<main data-kit-component="progress-bar" data-kit-version="2.0.0"></main>`))
	if err != nil {
		t.Fatal(err)
	}
	source := []byte(`<html><head data-shell="docs"><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="script-src 'self'"><base href="https://assets.invalid/"><title>Docs</title></head><body></body></html>`)
	output, err := InjectDelivery(source, delivery)
	if err != nil {
		t.Fatal(err)
	}
	html := string(output)
	prior := -1
	for _, artifact := range delivery.Artifacts() {
		tag := `<script data-kitwork-jit="` + string(artifact.Role) + `" data-kitwork-hash="` + artifact.ContentHash +
			`" src="/jit/` + artifact.Name + `" integrity="` + artifact.Integrity + `" crossorigin="anonymous" defer></script>`
		index := strings.Index(html, tag)
		if index <= prior || index > strings.Index(html, `<base href="https://assets.invalid/">`) {
			t.Fatalf("ordered staged tag missing before head: %s\n%s", tag, html)
		}
		prior = index
	}
	if strings.Contains(html, "data-kitwork-plan") || strings.Contains(html, "data-kitwork-runtime") {
		t.Fatalf("legacy delivery marker leaked into staged HTML:\n%s", html)
	}
	charset := strings.Index(html, `<meta charset="utf-8">`)
	csp := strings.Index(html, `<meta http-equiv="Content-Security-Policy"`)
	first := strings.Index(html, `<script data-kitwork-jit="runtime"`)
	base := strings.Index(html, `<base href="https://assets.invalid/">`)
	if charset < 0 || csp < 0 || first < 0 || base < 0 || !(charset < csp && csp < first && first < base) {
		t.Fatalf("want charset < CSP < staged scripts < base:\n%s", html)
	}
	if charset >= 1024 {
		t.Fatalf("charset offset=%d, want declaration in the first 1024 bytes", charset)
	}
	if _, err := InjectDelivery(output, delivery); !errors.Is(err, ErrRuntimeMarker) {
		t.Fatalf("duplicate marker error=%v", err)
	}
	empty, err := InjectDelivery([]byte(`<main>Static</main>`), Delivery{})
	if err != nil || !bytes.Equal(empty, []byte(`<main>Static</main>`)) {
		t.Fatalf("empty delivery output=%q error=%v", empty, err)
	}
}

func TestInjectDeliveryRejectsSecurityMetadataAfterBase(t *testing.T) {
	store, err := NewDefaultAssetStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	delivery, err := store.ComposeHTML([]byte(`<main data-kit-scope="count: 0"></main>`))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		`<html><head><base href="https://assets.invalid/"><meta charset="utf-8"></head><body></body></html>`,
		`<html><head><base href="https://assets.invalid/"><meta http-equiv="Content-Security-Policy" content="script-src 'self'"></head><body></body></html>`,
		`<html><head><base href="https://assets.invalid/"><meta http-equiv="content-type" content="text/html; charset=utf-8"></head><body></body></html>`,
		`<html><head><base href="https://assets.invalid/"><meta http-equiv="{{ @policy }}" content="script-src 'self'"></head><body></body></html>`,
		`<html><head><base href="https://assets.invalid/"><meta {{ @security }}></head><body></body></html>`,
	} {
		if _, err := InjectDelivery([]byte(source), delivery); !errors.Is(err, ErrUnsafeHeadOrder) {
			t.Fatalf("unsafe head error=%v for %s", err, source)
		}
	}
}

func TestInjectDeliveryPreservesImplicitHeadSecurityMetadata(t *testing.T) {
	store, err := NewDefaultAssetStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	delivery, err := store.ComposeHTML([]byte(`<main data-kit-scope="count: 0"></main>`))
	if err != nil {
		t.Fatal(err)
	}
	source := `<!doctype html><!--preamble--><html><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="script-src 'self'"><title>Docs</title><body><main>Body</main></body></html>`
	output, err := InjectDelivery([]byte(source), delivery)
	if err != nil {
		t.Fatal(err)
	}
	html := string(output)
	doctype := strings.Index(html, `<!doctype html>`)
	comment := strings.Index(html, `<!--preamble-->`)
	charset := strings.Index(html, `<meta charset="utf-8">`)
	csp := strings.Index(html, `<meta http-equiv="Content-Security-Policy"`)
	first := strings.Index(html, `<script data-kitwork-jit="runtime"`)
	body := strings.Index(html, `<body>`)
	if doctype != 0 || comment < 0 || charset < 0 || csp < 0 || first < 0 || body < 0 ||
		!(doctype < comment && comment < charset && charset < csp && csp < first && first < body) {
		t.Fatalf("want preamble < implicit-head security metadata < staged scripts < body:\n%s", html)
	}
}

func TestInjectDeliveryPrecedesPotentialDynamicBase(t *testing.T) {
	store, err := NewDefaultAssetStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	delivery, err := store.ComposeHTML([]byte(`<main data-kit-scope="count: 0"></main>`))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		`<html><head><meta charset="utf-8"><base {{ if useAssets }}href="https://assets.invalid/"{{ end }}><title>Docs</title></head><body></body></html>`,
		`<html><head><meta charset="utf-8"><base href="{{ assets }}"><title>Docs</title></head><body></body></html>`,
	} {
		output, err := InjectDelivery([]byte(source), delivery)
		if err != nil {
			t.Fatal(err)
		}
		html := string(output)
		first := strings.Index(html, `<script data-kitwork-jit="runtime"`)
		base := strings.Index(html, `<base`)
		if first < 0 || base < 0 || first > base {
			t.Fatalf("staged scripts did not precede potential dynamic base:\n%s", html)
		}
	}
}

func TestInjectDeliveryRejectsConditionalHeadSecurity(t *testing.T) {
	store, err := NewDefaultAssetStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	delivery, err := store.ComposeHTML([]byte(`<main data-kit-scope="count: 0"></main>`))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		`<html><head>{{ if count < 2 }}<meta http-equiv="Content-Security-Policy" content="script-src 'self'">{{ end }}</head><body></body></html>`,
		`<!doctype html><html>{{ if count < 2 }}<meta http-equiv="Content-Security-Policy" content="script-src 'self'">{{ end }}<body></body></html>`,
		`<html><head>{{ if useBase }}<base href="https://assets.invalid/">{{ end }}</head><body></body></html>`,
		`<!doctype html><html>{{ if useBase }}<base href="https://assets.invalid/">{{ end }}<body></body></html>`,
		`<html><head>{{ maybeEmpty }}<meta http-equiv="Content-Security-Policy" content="script-src 'self'"></head><body></body></html>`,
		`<html><head>{{ maybeEmpty }}<base href="https://assets.invalid/"></head><body></body></html>`,
		`{{ if count < 2 }}<head><title>Conditional shell</title></head>{{ end }}<body><main data-kit-scope="count: 0"></main></body>`,
		`<html><head><title>{{ if enabled }}Title</title><meta http-equiv="Content-Security-Policy" content="script-src 'self'">{{ end }}</head><body></body></html>`,
		`<html><head><meta name="description" content="{{ if enabled }}value"><meta http-equiv="Content-Security-Policy" content="script-src 'self'">{{ end }}</head><body></body></html>`,
		`<html><head><template>{{ if enabled }}<p>Template</p></template><meta http-equiv="Content-Security-Policy" content="script-src 'self'">{{ end }}</head><body></body></html>`,
		`<html><head><title>{{ raw("</title><meta http-equiv='Content-Security-Policy'>") }}</title></head><body></body></html>`,
		`{{ if count < 2 }}Conditional text{{ end }}<head><title>Late head</title></head><body></body>`,
		`{{ if enabled }}<!-- no close {{ end }}<body><main data-kit-scope="count: 0"></main></body>`,
		`{{ if enabled }}<!broken declaration {{ end }}`,
		`{{ if enabled }}<{{ end }}<body><main data-kit-scope="count: 0"></main></body>`,
	} {
		if _, err := InjectDelivery([]byte(source), delivery); !errors.Is(err, ErrUnsafeHeadOrder) {
			t.Fatalf("conditional/dynamic head security error=%v for %s", err, source)
		}
	}
}

func TestInjectDeliveryAllowsLocallyBalancedOpaqueHeadControls(t *testing.T) {
	store, err := NewDefaultAssetStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	delivery, err := store.ComposeHTML([]byte(`<main data-kit-scope="count: 0"></main>`))
	if err != nil {
		t.Fatal(err)
	}
	source := `<html><head><title>{{ label == "</title>" }}{{ if enabled }}On{{ else }}Off{{ end }}</title><meta name="description" content="{{ if enabled }}ready{{ end }}"><template>{{ if enabled }}<base href="https://assets.invalid/">{{ end }}</template></head><body></body></html>`
	output, err := InjectDelivery([]byte(source), delivery)
	if err != nil {
		t.Fatal(err)
	}
	html := string(output)
	templateEnd := strings.Index(html, `</template>`)
	first := strings.Index(html, `<script data-kitwork-jit="runtime"`)
	headEnd := strings.Index(html, `</head>`)
	if templateEnd < 0 || first < 0 || headEnd < 0 || !(templateEnd < first && first < headEnd) {
		t.Fatalf("locally balanced opaque controls changed staged placement:\n%s", html)
	}
}

func TestInjectDeliveryWithoutBaseUsesEndOfHead(t *testing.T) {
	store, err := NewDefaultAssetStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	delivery, err := store.ComposeHTML([]byte(`<main data-kit-scope="count: 0"></main>`))
	if err != nil {
		t.Fatal(err)
	}
	output, err := InjectDelivery([]byte(`<html><head><meta charset="utf-8"><title>Docs</title></head><body></body></html>`), delivery)
	if err != nil {
		t.Fatal(err)
	}
	html := string(output)
	title := strings.Index(html, `</title>`)
	first := strings.Index(html, `<script data-kitwork-jit="runtime"`)
	headEnd := strings.Index(html, `</head>`)
	if title < 0 || first < 0 || headEnd < 0 || !(title < first && first < headEnd) {
		t.Fatalf("want no-base scripts at the end of head:\n%s", html)
	}
}

func TestInjectDeliveryIgnoresBaseInsideTemplate(t *testing.T) {
	store, err := NewDefaultAssetStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	delivery, err := store.ComposeHTML([]byte(`<main data-kit-scope="count: 0"></main>`))
	if err != nil {
		t.Fatal(err)
	}
	source := `<html><head><meta charset="utf-8"><template><base href="https://assets.invalid/"><meta http-equiv="Content-Security-Policy" content="script-src 'none'"></template><title>Docs</title></head><body></body></html>`
	output, err := InjectDelivery([]byte(source), delivery)
	if err != nil {
		t.Fatal(err)
	}
	html := string(output)
	templateEnd := strings.Index(html, `</template>`)
	titleEnd := strings.Index(html, `</title>`)
	first := strings.Index(html, `<script data-kitwork-jit="runtime"`)
	headEnd := strings.Index(html, `</head>`)
	if templateEnd < 0 || titleEnd < 0 || first < 0 || headEnd < 0 || !(templateEnd < titleEnd && titleEnd < first && first < headEnd) {
		t.Fatalf("inert template base changed active head injection:\n%s", html)
	}
}

func TestInjectDeliveryIgnoresBaseInsideHeadNoscript(t *testing.T) {
	store, err := NewDefaultAssetStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	delivery, err := store.ComposeHTML([]byte(`<main data-kit-scope="count: 0"></main>`))
	if err != nil {
		t.Fatal(err)
	}
	source := `<html><head><meta charset="utf-8"><noscript><base href="https://assets.invalid/"><meta http-equiv="Content-Security-Policy" content="script-src 'none'"></noscript><title>Docs</title></head><body></body></html>`
	output, err := InjectDelivery([]byte(source), delivery)
	if err != nil {
		t.Fatal(err)
	}
	html := string(output)
	noscriptEnd := strings.Index(html, `</noscript>`)
	titleEnd := strings.Index(html, `</title>`)
	first := strings.Index(html, `<script data-kitwork-jit="runtime"`)
	headEnd := strings.Index(html, `</head>`)
	if noscriptEnd < 0 || titleEnd < 0 || first < 0 || headEnd < 0 || !(noscriptEnd < titleEnd && titleEnd < first && first < headEnd) {
		t.Fatalf("inert noscript base changed active head injection:\n%s", html)
	}
}

func TestInjectDeliveryStopsAtBrowserEffectiveHeadEnd(t *testing.T) {
	store, err := NewDefaultAssetStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	delivery, err := store.ComposeHTML([]byte(`<main data-kit-scope="count: 0"></main>`))
	if err != nil {
		t.Fatal(err)
	}
	source := `<html><head><meta charset="utf-8"><main>Body starts here<base href="https://assets.invalid/"></main></head><body></body></html>`
	output, err := InjectDelivery([]byte(source), delivery)
	if err != nil {
		t.Fatal(err)
	}
	html := string(output)
	first := strings.Index(html, `<script data-kitwork-jit="runtime"`)
	main := strings.Index(html, `<main>`)
	base := strings.Index(html, `<base`)
	if first < 0 || main < 0 || base < 0 || !(first < main && main < base) {
		t.Fatalf("scripts escaped the browser-effective head:\n%s", html)
	}
}

func TestInjectDeliveryMarkerGuardIsNarrowAndReserved(t *testing.T) {
	store, err := NewDefaultAssetStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	delivery, err := store.ComposeHTML([]byte(`<main data-kit-scope="count: 0"></main>`))
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []JITRole{
		JITRoleRuntime, JITRoleHydrate, JITRoleGraph, JITRoleService, JITRoleComponent, JITRoleComponents,
	} {
		source := []byte(`<script data-kitwork-jit="` + string(role) + `"></script>`)
		if _, err := InjectDelivery(source, delivery); !errors.Is(err, ErrRuntimeMarker) {
			t.Fatalf("role %q marker error=%v", role, err)
		}
	}
	for _, source := range [][]byte{
		[]byte(`<script data-kitwork-hash="` + strings.Repeat("0", 64) + `"></script>`),
		[]byte(`<div data-kitwork-hash></div>`),
		[]byte(`<script data-kitwork-runtime></script>`),
	} {
		if _, err := InjectDelivery(source, delivery); !errors.Is(err, ErrRuntimeMarker) {
			t.Fatalf("reserved marker %q error=%v", source, err)
		}
	}
	for _, source := range [][]byte{
		[]byte(`<script data-kitwork-jit="theme"></script>`),
		[]byte(`<script data-kitwork-jit="js"></script>`),
		[]byte(`<style data-kitwork-jit="style"></style>`),
	} {
		if _, err := InjectDelivery(source, delivery); err != nil {
			t.Fatalf("unrelated JIT marker %q was rejected: %v", source, err)
		}
	}
}

func deliveryRoles(delivery Delivery) []string {
	artifacts := delivery.Artifacts()
	roles := make([]string, len(artifacts))
	for index, artifact := range artifacts {
		roles[index] = string(artifact.Role)
	}
	return roles
}

func deliveryArtifact(delivery Delivery, role JITRole, suffix string) (AssetReference, bool) {
	for _, artifact := range delivery.Artifacts() {
		if artifact.Role == role && (suffix == "" || artifact.Suffix == suffix) {
			return artifact, true
		}
	}
	return AssetReference{}, false
}
