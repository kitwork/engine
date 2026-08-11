package site_test

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/kitwork/engine/app"
	"github.com/kitwork/engine/capabilities"
	"github.com/kitwork/engine/compiler"
	jitcss "github.com/kitwork/engine/jit/css"
	"github.com/kitwork/engine/site"
	"github.com/kitwork/engine/utilities/cache"
	"github.com/kitwork/engine/value"
)

type generationCapabilityCloser struct {
	closed atomic.Int32
}

func (c *generationCapabilityCloser) Close() error {
	c.closed.Add(1)
	return nil
}

type generationRouteGraph struct {
	closed atomic.Int32
}

func (g *generationRouteGraph) Close() {
	g.closed.Add(1)
}

type generationRenderPlan struct {
	closed atomic.Int32
}

func (p *generationRenderPlan) Close() {
	p.closed.Add(1)
}

func TestGenerationActivationAndDrain(t *testing.T) {
	appRuntime := app.NewRuntime("identity-a")
	siteRuntime, err := appRuntime.Site("apps", "example.com")
	if err != nil {
		t.Fatal(err)
	}

	first, err := siteRuntime.PrepareGeneration()
	if err != nil {
		t.Fatal(err)
	}
	if previous, err := siteRuntime.ActivateGeneration(first); err != nil || previous != nil {
		t.Fatalf("first activation: previous=%v err=%v", previous, err)
	}
	lease, ok := first.Acquire()
	if !ok {
		t.Fatal("active generation refused a request lease")
	}

	second, err := siteRuntime.PrepareGeneration()
	if err != nil {
		t.Fatal(err)
	}
	previous, err := siteRuntime.ActivateGeneration(second)
	if err != nil || previous != first {
		t.Fatalf("swap: previous=%v err=%v", previous, err)
	}
	if _, err := siteRuntime.ActivateGeneration(first); err == nil {
		t.Fatal("site runtime reactivated a stale generation")
	}

	retired := make(chan struct{})
	go func() {
		first.Retire()
		close(retired)
	}()
	select {
	case <-retired:
		t.Fatal("generation retired before its request drained")
	case <-time.After(10 * time.Millisecond):
	}
	if _, ok := first.Acquire(); ok {
		t.Fatal("retiring generation accepted a new request")
	}
	lease.Release()
	select {
	case <-retired:
	case <-time.After(time.Second):
		t.Fatal("generation did not retire after its request drained")
	}

	siteRuntime.Close()
	if !second.Retired() {
		t.Fatal("site close did not retire the current generation")
	}
}

func TestGenerationOwnsOptionalBytecodeCache(t *testing.T) {
	appRuntime := app.NewRuntime("identity-a")
	siteRuntime, err := appRuntime.Site("apps", "example.com")
	if err != nil {
		t.Fatal(err)
	}
	generation, err := siteRuntime.PrepareGeneration()
	if err != nil {
		t.Fatal(err)
	}
	bytecodeCache := compiler.NewFileCache(t.TempDir())
	if err := generation.SetBytecodeCache(bytecodeCache); err != nil {
		t.Fatal(err)
	}
	if generation.BytecodeCache() != bytecodeCache {
		t.Fatal("generation did not retain its bytecode cache")
	}
	if err := generation.SetBytecodeCache(compiler.NewFileCache(t.TempDir())); err == nil {
		t.Fatal("generation accepted a second bytecode cache")
	}
	if _, err := siteRuntime.ActivateGeneration(generation); err != nil {
		t.Fatal(err)
	}
	if err := generation.SetBytecodeCache(compiler.NewFileCache(t.TempDir())); err == nil {
		t.Fatal("published generation accepted a bytecode cache mutation")
	}
	siteRuntime.Close()
}

func TestGenerationCannotCrossSiteRuntime(t *testing.T) {
	appRuntime := app.NewRuntime("identity-a")
	firstSite, _ := appRuntime.Site("apps", "first.example")
	secondSite, _ := appRuntime.Site("apps", "second.example")
	generation, err := firstSite.PrepareGeneration()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := secondSite.ActivateGeneration(generation); err == nil {
		t.Fatal("site accepted another site's generation")
	}
	generation.Retire()
}

func TestGenerationOwnsAndClosesSiteCapabilities(t *testing.T) {
	appRuntime := app.NewRuntime("identity-a")
	siteRuntime, _ := appRuntime.Site("apps", "example.com")
	generation, err := siteRuntime.PrepareGeneration()
	if err != nil {
		t.Fatal(err)
	}

	registry := capabilities.NewRegistry()
	closer := &generationCapabilityCloser{}
	registry.Register("resource", func(capabilities.Scope) value.Value {
		return value.New(closer)
	})
	if _, ok := generation.CapabilitiesCache().GetOrCompute("resource", registry, nil); !ok {
		t.Fatal("site capability was not created")
	}

	generation.Retire()
	generation.Retire()
	if got := closer.closed.Load(); got != 1 {
		t.Fatalf("site capability closed %d times, want 1", got)
	}
	if _, ok := generation.CapabilitiesCache().GetOrCompute("resource", registry, nil); ok {
		t.Fatal("retired generation recreated a site capability")
	}
}

func TestGenerationOwnsRouteGraphUntilRequestDrain(t *testing.T) {
	appRuntime := app.NewRuntime("identity-a")
	siteRuntime, _ := appRuntime.Site("apps", "example.com")
	generation, err := siteRuntime.PrepareGeneration()
	if err != nil {
		t.Fatal(err)
	}
	graph := &generationRouteGraph{}
	if err := generation.SetRouteGraph(graph); err != nil {
		t.Fatal(err)
	}
	if generation.RouteGraph() != graph {
		t.Fatal("generation did not retain its prepared route graph")
	}
	if err := generation.SetRouteGraph(&generationRouteGraph{}); err == nil {
		t.Fatal("generation accepted a second route graph")
	}
	if _, err := siteRuntime.ActivateGeneration(generation); err != nil {
		t.Fatal(err)
	}
	if err := generation.SetRouteGraph(&generationRouteGraph{}); err == nil {
		t.Fatal("published generation accepted a route graph mutation")
	}

	lease, ok := generation.Acquire()
	if !ok {
		t.Fatal("active generation refused a request lease")
	}
	retired := make(chan struct{})
	go func() {
		generation.Retire()
		close(retired)
	}()
	select {
	case <-retired:
		t.Fatal("route graph closed before its request drained")
	case <-time.After(10 * time.Millisecond):
	}
	if graph.closed.Load() != 0 {
		t.Fatal("route graph closed while a request still held the generation")
	}
	lease.Release()
	select {
	case <-retired:
	case <-time.After(time.Second):
		t.Fatal("generation did not retire after route graph request drain")
	}
	if graph.closed.Load() != 1 {
		t.Fatalf("route graph closed %d times, want 1", graph.closed.Load())
	}
	if generation.RouteGraph() != nil {
		t.Fatal("retired generation retained its route graph")
	}
}

func TestGenerationOwnsRenderPlanUntilRequestDrain(t *testing.T) {
	appRuntime := app.NewRuntime("identity-a")
	siteRuntime, _ := appRuntime.Site("apps", "example.com")
	generation, err := siteRuntime.PrepareGeneration()
	if err != nil {
		t.Fatal(err)
	}
	plan := &generationRenderPlan{}
	if err := generation.SetRenderPlan(plan); err != nil {
		t.Fatal(err)
	}
	if generation.RenderPlan() != plan {
		t.Fatal("generation did not retain its prepared render plan")
	}
	if err := generation.SetRenderPlan(&generationRenderPlan{}); err == nil {
		t.Fatal("generation accepted a second render plan")
	}
	if _, err := siteRuntime.ActivateGeneration(generation); err != nil {
		t.Fatal(err)
	}

	lease, ok := generation.Acquire()
	if !ok {
		t.Fatal("active generation refused a request lease")
	}
	retired := make(chan struct{})
	go func() {
		generation.Retire()
		close(retired)
	}()
	select {
	case <-retired:
		t.Fatal("render plan closed before its request drained")
	case <-time.After(10 * time.Millisecond):
	}
	if plan.closed.Load() != 0 {
		t.Fatal("render plan closed while a request still held the generation")
	}
	lease.Release()
	select {
	case <-retired:
	case <-time.After(time.Second):
		t.Fatal("generation did not retire after render request drain")
	}
	if plan.closed.Load() != 1 {
		t.Fatalf("render plan closed %d times, want 1", plan.closed.Load())
	}
	if generation.RenderPlan() != nil {
		t.Fatal("retired generation retained its render plan")
	}
}

func TestGenerationFreezesPresentationAtActivation(t *testing.T) {
	appRuntime := app.NewRuntime("identity-a")
	siteRuntime, _ := appRuntime.Site("apps", "example.com")
	generation, err := siteRuntime.PrepareGeneration()
	if err != nil {
		t.Fatal(err)
	}
	presentation := generation.Presentation()
	config := &jitcss.Config{
		Colors: map[string]jitcss.Color{"brand": jitcss.Hex("#123456")},
		Order:  []string{"brand"},
	}
	if !presentation.SetJITConfig(config) ||
		!presentation.SetFaviconFile("favicon.ico") ||
		!presentation.AddAssetMount(site.AssetMount{URL: "assets", Disk: "_assets"}) ||
		!presentation.SetThemeMode("force") ||
		!presentation.SetKitJS(true) {
		t.Fatal("presentation rejected preparation-time declarations")
	}
	if err := generation.SetEnvironment(value.New("env-v1")); err != nil {
		t.Fatal(err)
	}

	// The builder owns a detached copy even before activation.
	config.Colors["brand"] = jitcss.Hex("#ffffff")
	if _, err := siteRuntime.ActivateGeneration(generation); err != nil {
		t.Fatal(err)
	}
	snapshot := presentation.Snapshot()
	if !snapshot.Frozen {
		t.Fatal("activated generation presentation is mutable")
	}
	if !snapshot.KitJS {
		t.Fatal("activated presentation lost the KitJS opt-in")
	}
	if !generation.Sources().Frozen() {
		t.Fatal("activated generation source manifest is mutable")
	}
	if got := snapshot.JITConfig.Colors["brand"]; got != jitcss.Hex("#123456") {
		t.Fatalf("JIT config alias leaked into generation: got %v", got)
	}
	if presentation.SetThemeMode("off") {
		t.Fatal("frozen presentation accepted a mutation")
	}
	if presentation.SetKitJS(false) {
		t.Fatal("frozen presentation accepted a KitJS mutation")
	}
	if err := generation.SetEnvironment(value.New("env-v2")); err == nil {
		t.Fatal("published generation accepted an environment mutation")
	}
	if got := generation.Environment().Text(); got != "env-v1" {
		t.Fatalf("environment = %q, want env-v1", got)
	}

	// A request cannot mutate the stored snapshot through a returned map.
	snapshot.JITConfig.Colors["brand"] = jitcss.Hex("#000000")
	if got := presentation.Snapshot().JITConfig.Colors["brand"]; got != jitcss.Hex("#123456") {
		t.Fatalf("snapshot mutation leaked into generation: got %v", got)
	}
	generation.ResponseCache().Set("page", cache.Entry{Body: []byte("cached")}, time.Hour)
	siteRuntime.Close()
	if _, ok := generation.ResponseCache().Get("page"); ok {
		t.Fatal("retired generation retained its RAM response cache")
	}
}
