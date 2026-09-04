package relational

import (
	"context"
	"errors"
	"testing"
)

func TestProjectionPreflightAndOpenPolicies(t *testing.T) {
	engine := projectionTestDatabase(t, 257)
	ctx := context.Background()
	path := engine.Path()

	missing, err := engine.PreflightProjections(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !missing.Valid || missing.Ready || missing.DatabaseID == "" || missing.Transaction == 0 ||
		len(missing.Analytics) != 1 || missing.Analytics[0].Status != "missing" ||
		len(missing.Search) != 1 || missing.Search[0].Status != "missing" {
		t.Fatalf("missing preflight = %+v", missing)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	assertProjectionOpenRejected(t, path, ProjectionOpenRequireReady, "analytics", "missing")

	engine, err = OpenWithOptions(path, Options{ExperimentalProjections: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.RefreshProjections(ctx); err != nil {
		t.Fatal(err)
	}
	ready, err := engine.PreflightProjections(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !ready.Valid || !ready.Ready || len(ready.Analytics) != 1 || len(ready.Search) != 1 ||
		ready.Analytics[0].Status != "ready" || ready.Search[0].Status != "ready" ||
		ready.Search[0].Documents != 257 || ready.Search[0].Segments == 0 {
		t.Fatalf("ready preflight = %+v", ready)
	}
	projectionExecute(t, engine, `UPDATE products SET price = 999 WHERE id = 1`)
	stale, err := engine.PreflightProjections(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !stale.Valid || stale.Ready || stale.Analytics[0].Status != "stale" || stale.Search[0].Status != "stale" {
		t.Fatalf("stale preflight = %+v", stale)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}

	validated, err := OpenWithOptions(path, Options{
		ExperimentalProjections: true,
		ProjectionOpenPolicy:    ProjectionOpenValidate,
	})
	if err != nil {
		t.Fatalf("validate rejected safe stale fallback: %v", err)
	}
	if err := validated.Close(); err != nil {
		t.Fatal(err)
	}
	assertProjectionOpenRejected(t, path, ProjectionOpenRequireReady, "analytics", "stale")

	engine, err = OpenWithOptions(path, Options{ExperimentalProjections: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.RefreshProjections(ctx); err != nil {
		t.Fatal(err)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	appendProjectionManifest(t, path+".analytics", func(manifest *projectionManifest) {
		for id, table := range manifest.Tables {
			table.Chunks[0].Length++
			manifest.Tables[id] = table
			return
		}
	})
	assertProjectionOpenRejected(t, path, ProjectionOpenValidate, "analytics", "invalid")

	lazy, err := OpenWithOptions(path, Options{ExperimentalProjections: true})
	if err != nil {
		t.Fatalf("lazy policy rejected rebuildable projection: %v", err)
	}
	defer lazy.Close()
	invalid, err := lazy.PreflightProjections(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if invalid.Valid || invalid.Ready || invalid.Analytics[0].Status != "invalid" {
		t.Fatalf("invalid preflight = %+v", invalid)
	}
}

func TestProjectionOpenPolicyValidationAndCancellation(t *testing.T) {
	for source, want := range map[string]ProjectionOpenPolicy{
		"":              ProjectionOpenDefault,
		" LAZY ":        ProjectionOpenLazy,
		"Validate":      ProjectionOpenValidate,
		"REQUIRE-READY": ProjectionOpenRequireReady,
	} {
		got, err := ParseProjectionOpenPolicy(source)
		if err != nil || got != want {
			t.Fatalf("ParseProjectionOpenPolicy(%q) = %q, %v; want %q", source, got, err, want)
		}
	}
	if _, err := ParseProjectionOpenPolicy("strict"); err == nil {
		t.Fatal("invalid projection policy accepted")
	}
	if _, err := OpenWithOptions("unused.kitdb", Options{ProjectionOpenPolicy: ProjectionOpenValidate}); err == nil {
		t.Fatal("projection policy accepted while projections are disabled")
	}

	engine := projectionTestDatabase(t, 1)
	path := engine.Path()
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := OpenWithContext(ctx, path, Options{
		ExperimentalProjections: true,
		ProjectionOpenPolicy:    ProjectionOpenValidate,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled projection open = %v", err)
	}
}

func assertProjectionOpenRejected(
	t *testing.T,
	path string,
	policy ProjectionOpenPolicy,
	kind string,
	status string,
) {
	t.Helper()
	opened, err := OpenWithOptions(path, Options{
		ExperimentalProjections: true,
		ProjectionOpenPolicy:    policy,
	})
	if opened != nil {
		_ = opened.Close()
		t.Fatalf("policy %q accepted %s/%s", policy, kind, status)
	}
	var problem *ProjectionOpenError
	if !errors.As(err, &problem) || problem.Policy != policy || problem.Kind != kind || problem.Status != status {
		t.Fatalf("policy %q error = %#v, %v", policy, problem, err)
	}
}
