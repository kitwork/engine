package site_test

import (
	goruntime "runtime"
	"sync"
	"testing"
	"time"

	"github.com/kitwork/engine/app"
	"github.com/kitwork/engine/site"
)

func TestGenerationPublicationDrainSoak(t *testing.T) {
	appRuntime := app.NewRuntime("soak")
	t.Cleanup(appRuntime.Close)
	siteRuntime, err := appRuntime.Site("apps", "soak.example")
	if err != nil {
		t.Fatal(err)
	}

	prepare := func() (*site.Generation, *generationRouteGraph, *generationRenderPlan) {
		generation, err := siteRuntime.PrepareGeneration()
		if err != nil {
			t.Fatal(err)
		}
		graph := &generationRouteGraph{}
		plan := &generationRenderPlan{}
		if err := generation.SetRouteGraph(graph); err != nil {
			t.Fatal(err)
		}
		if err := generation.SetRenderPlan(plan); err != nil {
			t.Fatal(err)
		}
		return generation, graph, plan
	}

	current, currentGraph, currentPlan := prepare()
	if _, err := siteRuntime.ActivateGeneration(current); err != nil {
		t.Fatal(err)
	}

	const generations = 64
	const leasesPerGeneration = 8
	for revision := 0; revision < generations; revision++ {
		leases := make([]*site.Lease, 0, leasesPerGeneration)
		for index := 0; index < leasesPerGeneration; index++ {
			lease, ok := current.Acquire()
			if !ok {
				t.Fatalf("revision %d refused lease %d", revision, index)
			}
			leases = append(leases, lease)
		}

		next, nextGraph, nextPlan := prepare()
		previous, err := siteRuntime.ActivateGeneration(next)
		if err != nil {
			t.Fatal(err)
		}
		if previous != current {
			t.Fatalf("revision %d replaced %p, want %p", revision, previous, current)
		}

		retired := make(chan struct{})
		go func(generation *site.Generation) {
			generation.Retire()
			close(retired)
		}(current)

		deadline := time.Now().Add(time.Second)
		for !current.Retired() && time.Now().Before(deadline) {
			goruntime.Gosched()
		}
		if !current.Retired() {
			t.Fatalf("revision %d did not enter retirement", revision)
		}
		select {
		case <-retired:
			t.Fatalf("revision %d retired before leases drained", revision)
		default:
		}
		if _, ok := current.Acquire(); ok {
			t.Fatalf("revision %d accepted a lease after retirement", revision)
		}

		var releases sync.WaitGroup
		for _, lease := range leases {
			releases.Add(1)
			go func(lease *site.Lease) {
				defer releases.Done()
				lease.Release()
				lease.Release()
			}(lease)
		}
		releases.Wait()
		select {
		case <-retired:
		case <-time.After(time.Second):
			t.Fatalf("revision %d did not drain", revision)
		}

		if current.Active() != 0 ||
			current.RouteGraph() != nil ||
			current.RenderPlan() != nil {
			t.Fatalf("revision %d retained request or generation resources", revision)
		}
		if currentGraph.closed.Load() != 1 || currentPlan.closed.Load() != 1 {
			t.Fatalf(
				"revision %d close counts: graph=%d plan=%d",
				revision,
				currentGraph.closed.Load(),
				currentPlan.closed.Load(),
			)
		}
		current, currentGraph, currentPlan = next, nextGraph, nextPlan
	}

	siteRuntime.Close()
	if currentGraph.closed.Load() != 1 || currentPlan.closed.Load() != 1 {
		t.Fatalf(
			"current generation close counts: graph=%d plan=%d",
			currentGraph.closed.Load(),
			currentPlan.closed.Load(),
		)
	}
}
