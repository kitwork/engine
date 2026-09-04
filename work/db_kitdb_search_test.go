package work

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestKitDBSearchBuildOutlivesCanceledWaiter(t *testing.T) {
	state := &searchIndexState{}
	started := make(chan struct{})
	release := make(chan struct{})
	var runs atomic.Int32
	build := state.ensureKitDBSearchBuild("r:7", func() error {
		runs.Add(1)
		close(started)
		<-release
		return nil
	})
	<-started

	waitContext, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitKitDBSearchBuild(waitContext, build, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled waiter error = %v, want context.Canceled", err)
	}
	select {
	case <-build.done:
		t.Fatal("request cancellation stopped the node-owned build")
	default:
	}

	shared := state.ensureKitDBSearchBuild("r:7", func() error {
		runs.Add(1)
		return nil
	})
	if shared != build {
		t.Fatal("concurrent request did not share the active build")
	}
	if err := waitKitDBSearchBuild(context.Background(), build, time.Millisecond); !errors.Is(
		err, errKitDBSearchProjectionBuilding,
	) {
		t.Fatalf("bounded foreground wait error = %v, want projection-building status", err)
	}
	close(release)
	if err := waitKitDBSearchBuild(context.Background(), build, time.Second); err != nil {
		t.Fatal(err)
	}
	completed := state.ensureKitDBSearchBuild("r:7", func() error {
		runs.Add(1)
		return nil
	})
	if completed != build || runs.Load() != 1 {
		t.Fatalf("successful build was restarted: build=%p completed=%p runs=%d", build, completed, runs.Load())
	}
}

func TestKitDBSearchBuildsFromKernelSnapshotAndSurvivesRestart(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	source := `import { router, database } from "kitwork";
const { kitdb, struct, text } = database;
const products = struct({
  id: text().key(),
  title: text().notNull().searchable({ weight: 3 }),
  body: text().notNull().searchable()
});
const db = kitdb("search.kitdb", { products });
router.get((ctx) => {
  if (db.products.count() === 0) {
    db.products.create({ id: "p1", title: "Áo thun cotton nam", body: "hàng ngày bền đẹp" });
    db.products.create({ id: "p2", title: "Quần jean xanh", body: "chất cotton co giãn" });
    db.products.create({ id: "p3", title: "Giày thể thao", body: "nhẹ êm chân" });
  }
	const action = ctx.query("action");
	if (action === "update") {
		db.products.where("id", "=", "p3").update({ body: "học vuejs ngay" });
	}
	if (action === "analyze") {
		db.products.analyze();
	}
  return ctx.json({
    weighted: db.products.search("cotton").limit(10).list(),
    folded: db.products.search("ao").limit(10).list(),
		conjunctive: db.products.search("cotton jean").limit(10).list(),
		fresh: db.products.search(action === "update" ? "vuejs" : "the thao").limit(10).list()
  });
});`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	first := NewTenant(root, "localhost")
	if err := first.Run(); err != nil {
		t.Fatal(err)
	}
	settings, _ := registeredKitDBOpenSettings(first, "search.kitdb")
	if !settings.RetainHistory {
		first.Close()
		t.Fatal("searchable KitDB declaration did not retain commit history")
	}
	body := serveKitDBSearchTest(t, first)
	assertKitDBSearchResponse(t, body)
	if stats := first.searchManager.Stats(); stats.Commits != 1 {
		t.Fatalf("initial projection commits = %d, want 1", stats.Commits)
	}
	analyzed := serveKitDBSearchTestURL(t, first, "http://localhost/?action=analyze")
	assertKitDBSearchResponse(t, analyzed)
	if stats := first.searchManager.Stats(); stats.Commits != 1 {
		t.Fatalf("ANALYZE rebuilt unchanged projection: %#v", stats)
	}
	updated := serveKitDBSearchTestURL(t, first, "http://localhost/?action=update")
	if fresh := section(updated, `"fresh":[`); !strings.Contains(fresh, `"id":"p3"`) {
		t.Fatalf("updated KitDB projection = %s", fresh)
	}
	if stats := first.searchManager.Stats(); stats.Commits != 2 {
		t.Fatalf("stale projection commits = %d, want 2", stats.Commits)
	}
	first.Close()

	second := NewTenant(root, "localhost")
	if err := second.Run(); err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	body = serveKitDBSearchTest(t, second)
	assertKitDBSearchResponse(t, body)
	if stats := second.searchManager.Stats(); stats.Commits != 0 {
		t.Fatalf("unchanged restart rebuilt projection: %#v", stats)
	}
}

func TestKitDBSearchHydratesNativeCompositePrimaryRows(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	source := `import { router, database } from "kitwork";
const { kitdb, struct, text, int } = database;
const shopping = struct({
  merchant: text().key(1),
  id: int().key(2),
  name: text().notNull().searchable({ weight: 3 })
});
const db = kitdb("composite-search.kitdb", { shopping });
router.get((ctx) => {
  if (db.shopping.count() === 0) {
    db.shopping.create({ merchant: "tiki", id: 7, name: "Bàn phím Logitech" });
    db.shopping.create({ merchant: "shopee", id: 7, name: "Chuột không dây" });
  }
	if (ctx.query("action") === "mutate") {
		db.shopping.where("merchant", "=", "tiki").where("id", "=", 7).update({ name: "Bàn phím cơ Keychron" });
		db.shopping.where("merchant", "=", "shopee").where("id", "=", 7).delete();
		db.shopping.create({ merchant: "lazada", id: 9, name: "Web camera Logitech" });
	}
  return ctx.json({
    found: db.shopping.find({ merchant: "tiki", id: 7 }),
		hits: db.shopping.search("ban phim logitech").limit(10).list(),
		keychron: db.shopping.search("keychron").limit(10).list(),
		mouse: db.shopping.search("chuot").limit(10).list(),
		camera: db.shopping.search("camera").limit(10).list()
  });
});`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	defer tenant.Close()
	body := serveKitDBSearchTest(t, tenant)
	if !strings.Contains(body, `"found":{"id":7,"merchant":"tiki"`) ||
		!strings.Contains(section(body, `"hits":[`), `"merchant":"tiki"`) ||
		!strings.Contains(body, `"_score":`) || !strings.Contains(body, `"_snippet":`) {
		t.Fatalf("composite-key search response = %s", body)
	}
	mutated := serveKitDBSearchTestURL(t, tenant, "http://localhost/?action=mutate")
	if result := section(mutated, `"keychron":[`); !strings.Contains(result, `"merchant":"tiki"`) {
		t.Fatalf("composite update search = %s", result)
	}
	if result := section(mutated, `"mouse":[`); strings.Contains(result, `"merchant":`) {
		t.Fatalf("composite delete search = %s", result)
	}
	if result := section(mutated, `"camera":[`); !strings.Contains(result, `"merchant":"lazada"`) {
		t.Fatalf("composite insert search = %s", result)
	}
	if stats := tenant.searchManager.Stats(); stats.Commits != 2 {
		t.Fatalf("composite bootstrap + catch-up commits = %d, want 2", stats.Commits)
	}
}

func TestKitDBSearchAdoptsLegacyCompositeProjectionWithoutRebuild(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	source := `import { router, database } from "kitwork";
const { kitdb, struct, text, int } = database;
const shopping = struct({
  merchant: text().key(1),
  id: int().key(2),
  name: text().notNull().searchable()
});
const db = kitdb("legacy-search.kitdb", { shopping });
router.get(() => {
  if (db.shopping.count() === 0) {
    db.shopping.create({ merchant: "shopee", id: 7, name: "Bàn phím Logitech" });
  }
  return db.shopping.search("logitech").limit(10).list();
});`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	first := NewTenant(root, "localhost")
	if err := first.Run(); err != nil {
		t.Fatal(err)
	}
	if body := serveKitDBSearchTest(t, first); !strings.Contains(body, `"merchant":"shopee"`) {
		first.Close()
		t.Fatalf("initial composite search = %s", body)
	}
	key := tenantScopeKey(first) + "|kitdb|legacy-search.kitdb"
	schemaRegMu.Lock()
	proxy := schemaReg[key]
	schemaRegMu.Unlock()
	if proxy == nil {
		first.Close()
		t.Fatal("legacy search schema was not registered")
	}
	tables, definitions := proxy.schemaSnapshot()
	table := &SchemaTable{
		tenant: first, engine: "kitdb", dbName: "legacy-search.kitdb", table: "shopping",
		columns: tables["shopping"], definition: definitions["shopping"],
	}
	columns := table.searchableColumns()
	searchSchema, _, err := newSearchSchema(columns)
	if err != nil {
		first.Close()
		t.Fatal(err)
	}
	indexKey := table.searchIndexKey(searchSchema)
	managed, err := kitDBForRequest(first, "legacy-search.kitdb", nil).database()
	if err != nil {
		first.Close()
		t.Fatal(err)
	}
	legacySignature, err := table.currentKitDBSearchSignature(managed.database)
	managed.Release()
	if err != nil {
		first.Close()
		t.Fatal(err)
	}
	if err := table.writeStoredSearchSignature(indexKey, legacySignature); err != nil {
		first.Close()
		t.Fatal(err)
	}
	first.Close()

	var checkpointPath string
	err = filepath.Walk(filepath.Join(directory, ".data", "search"), func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.Mode().IsRegular() && info.Name() == "projection.checkpoint" {
			checkpointPath = path
		}
		return nil
	})
	if err != nil || checkpointPath == "" {
		t.Fatalf("find legacy projection checkpoint = %q, %v", checkpointPath, err)
	}
	if err := os.Remove(checkpointPath); err != nil {
		t.Fatal(err)
	}

	second := NewTenant(root, "localhost")
	if err := second.Run(); err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if body := serveKitDBSearchTest(t, second); !strings.Contains(body, `"merchant":"shopee"`) {
		t.Fatalf("adopted composite search = %s", body)
	}
	if stats := second.searchManager.Stats(); stats.Commits != 0 {
		t.Fatalf("legacy composite projection rebuilt: %#v", stats)
	}
}

func TestKitDBSearchCatchesUpMutationsAndRebuildsCorruptWatermark(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	source := `import { router, database } from "kitwork";
const { kitdb, struct, text } = database;
const products = struct({
  id: text().key(),
  title: text().notNull().searchable()
});
const db = kitdb("catchup.kitdb", { products });
router.get((ctx) => {
  if (db.products.count() === 0) {
    db.products.create({ id: "p1", title: "old keyboard" });
    db.products.create({ id: "p2", title: "obsolete mouse" });
  }
  if (ctx.query("action") === "mutate") {
    db.products.where("id", "=", "p1").update({ title: "mechanical keyboard" });
    db.products.where("id", "=", "p2").delete();
    db.products.create({ id: "p3", title: "web camera" });
  }
  if (ctx.query("action") === "gap") {
    db.products.where("id", "=", "p1").update({ title: "history gap keyboard" });
    return ctx.json({ written: true });
  }
  return ctx.json({
    mechanical: db.products.search("mechanical").limit(10).list(),
    gap: db.products.search("history gap").limit(10).list(),
    old: db.products.search("old").limit(10).list(),
    obsolete: db.products.search("obsolete").limit(10).list(),
    camera: db.products.search("camera").limit(10).list()
  });
});`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	first := NewTenant(root, "localhost")
	if err := first.Run(); err != nil {
		t.Fatal(err)
	}
	initial := serveKitDBSearchTest(t, first)
	if old := section(initial, `"old":[`); !strings.Contains(old, `"id":"p1"`) {
		first.Close()
		t.Fatalf("initial old result = %s", old)
	}
	mutated := serveKitDBSearchTestURL(t, first, "http://localhost/?action=mutate")
	for marker, identifier := range map[string]string{
		`"mechanical":[`: "p1",
		`"camera":[`:     "p3",
	} {
		if result := section(mutated, marker); !strings.Contains(result, `"id":"`+identifier+`"`) {
			first.Close()
			t.Fatalf("catch-up result %s = %s", marker, result)
		}
	}
	for _, marker := range []string{`"old":[`, `"obsolete":[`} {
		if result := section(mutated, marker); strings.Contains(result, `"id":`) {
			first.Close()
			t.Fatalf("stale catch-up result %s = %s", marker, result)
		}
	}
	if stats := first.searchManager.Stats(); stats.Commits != 2 {
		first.Close()
		t.Fatalf("bootstrap + mutation commits = %d, want 2", stats.Commits)
	}
	managed, err := kitDBForRequest(first, "catchup.kitdb", nil).database()
	if err != nil {
		first.Close()
		t.Fatal(err)
	}
	pins, err := managed.database.HistoryPins()
	managed.Release()
	if err != nil {
		first.Close()
		t.Fatal(err)
	}
	wantPin := "search/" + stableSchemaID("struct", "products")
	foundPin := false
	for _, pin := range pins {
		foundPin = foundPin || pin.Name == wantPin
	}
	if !foundPin {
		first.Close()
		t.Fatalf("history pins = %#v, missing %q", pins, wantPin)
	}
	gapWrite := serveKitDBSearchTestURL(t, first, "http://localhost/?action=gap")
	if !strings.Contains(gapWrite, `"written":true`) {
		first.Close()
		t.Fatalf("history-gap source write = %s", gapWrite)
	}
	managed, err = kitDBForRequest(first, "catchup.kitdb", nil).database()
	if err != nil {
		first.Close()
		t.Fatal(err)
	}
	if err := managed.database.ReleaseHistoryPin(context.Background(), wantPin); err != nil {
		managed.Release()
		first.Close()
		t.Fatal(err)
	}
	if _, err := managed.database.Checkpoint(); err != nil {
		managed.Release()
		first.Close()
		t.Fatal(err)
	}
	gapBoundary, err := managed.database.CurrentCursor()
	if err == nil {
		_, err = managed.database.PruneHistory(context.Background(), gapBoundary.Transaction)
	}
	managed.Release()
	if err != nil {
		first.Close()
		t.Fatal(err)
	}
	gapRecovered := serveKitDBSearchTest(t, first)
	if result := section(gapRecovered, `"mechanical":[`); strings.Contains(result, `"id":`) {
		first.Close()
		t.Fatalf("history-gap stale result = %s", result)
	}
	if result := section(gapRecovered, `"old":[`); strings.Contains(result, `"id":`) {
		first.Close()
		t.Fatalf("history-gap old result = %s", result)
	}
	if result := section(gapRecovered, `"gap":[`); !strings.Contains(result, `"id":"p1"`) {
		first.Close()
		t.Fatalf("history-gap rebuilt result = %s", result)
	}
	if stats := first.searchManager.Stats(); stats.Commits != 3 {
		first.Close()
		t.Fatalf("history-gap fallback commits = %d, want 3", stats.Commits)
	}
	first.Close()

	var checkpointPath string
	err = filepath.Walk(filepath.Join(directory, ".data", "search"), func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.Mode().IsRegular() && info.Name() == "projection.checkpoint" {
			checkpointPath = path
		}
		return nil
	})
	if err != nil || checkpointPath == "" {
		t.Fatalf("find projection checkpoint = %q, %v", checkpointPath, err)
	}
	if err := os.WriteFile(checkpointPath, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}

	second := NewTenant(root, "localhost")
	if err := second.Run(); err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	rebuilt := serveKitDBSearchTest(t, second)
	if mechanical := section(rebuilt, `"mechanical":[`); strings.Contains(mechanical, `"id":`) {
		t.Fatalf("rebuilt stale mechanical result = %s", mechanical)
	}
	if gap := section(rebuilt, `"gap":[`); !strings.Contains(gap, `"id":"p1"`) {
		t.Fatalf("rebuilt history-gap result = %s", gap)
	}
	if camera := section(rebuilt, `"camera":[`); !strings.Contains(camera, `"id":"p3"`) {
		t.Fatalf("rebuilt camera result = %s", camera)
	}
	if stats := second.searchManager.Stats(); stats.Commits != 1 {
		t.Fatalf("corrupt watermark rebuild commits = %d, want 1", stats.Commits)
	}
}

func serveKitDBSearchTest(t *testing.T, tenant *Tenant) string {
	return serveKitDBSearchTestURL(t, tenant, "http://localhost/")
}

func serveKitDBSearchTestURL(t *testing.T, tenant *Tenant, target string) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	tenant.Serve(recorder, httptest.NewRequest(http.MethodGet, target, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("search route status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	return recorder.Body.String()
}

func assertKitDBSearchResponse(t *testing.T, body string) {
	t.Helper()
	weighted := section(body, `"weighted":[`)
	if first, second := strings.Index(weighted, `"id":"p1"`), strings.Index(weighted, `"id":"p2"`); first < 0 || second < 0 || first > second {
		t.Fatalf("weighted KitDB result = %s", weighted)
	}
	if folded := section(body, `"folded":[`); !strings.Contains(folded, `"id":"p1"`) {
		t.Fatalf("folded KitDB result = %s", folded)
	}
	conjunctive := section(body, `"conjunctive":[`)
	if !strings.Contains(conjunctive, `"id":"p2"`) || strings.Contains(conjunctive, `"id":"p1"`) {
		t.Fatalf("conjunctive KitDB result = %s", conjunctive)
	}
	if !strings.Contains(body, `"_score":`) || !strings.Contains(body, `"_snippet":`) {
		t.Fatalf("KitDB search metadata missing: %s", body)
	}
}

func TestParseKitDBSearchIdentifierPreservesScalarPrimaryKinds(t *testing.T) {
	integer, err := parseKitDBSearchIdentifier("integer", "42")
	if err != nil || integer.Int() != 42 {
		t.Fatalf("integer identifier = %#v, err=%v", integer, err)
	}
	text, err := parseKitDBSearchIdentifier("text", "product-42")
	if err != nil || text.String() != "product-42" {
		t.Fatalf("text identifier = %#v, err=%v", text, err)
	}
}

func TestKitDBSearchSignatureAcceptsLegacyCatalogSuffixOnly(t *testing.T) {
	if !kitDBSearchSignaturesMatch("42:catalog-revision", "t:42") {
		t.Fatal("legacy transaction signature did not survive a catalog-only revision")
	}
	if kitDBSearchSignaturesMatch("41:catalog-revision", "t:42") {
		t.Fatal("legacy signature accepted a different source transaction")
	}
	if kitDBSearchSignaturesMatch("42:catalog-revision", "r:42") {
		t.Fatal("legacy transaction signature was confused with a content revision")
	}
}

func TestKitDBSearchDoesNotRebuildForSecondaryIndexCatalogChange(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, RouterFileName)
	writeSource := func(indexed bool) {
		index := ""
		if indexed {
			index = `.index("products_category_idx")`
		}
		source := `import { router, database } from "kitwork";
const { kitdb, struct, text } = database;
const products = struct({
  id: text().key(),
  title: text().notNull().searchable(),
  category: text().notNull()` + index + `
});
const db = kitdb("catalog-search.kitdb", { products }, { migrate: true });
router.get(() => {
  if (db.products.count() === 0) {
    db.products.create({ id: "p1", title: "Bàn phím Logitech", category: "keyboard" });
  }
  return db.products.search("logitech").limit(1).list();
});`
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	writeSource(false)
	first := NewTenant(root, "localhost")
	if err := first.Run(); err != nil {
		t.Fatal(err)
	}
	serveKitDBSearchTest(t, first)
	if stats := first.searchManager.Stats(); stats.Commits != 1 {
		t.Fatalf("initial projection commits = %d, want 1", stats.Commits)
	}
	first.Close()

	writeSource(true)
	second := NewTenant(root, "localhost")
	if err := second.Run(); err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		recorder := httptest.NewRecorder()
		second.Serve(recorder, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
		if recorder.Code == http.StatusOK {
			break
		}
		if time.Now().After(deadline) || !strings.Contains(recorder.Body.String(), "index layout advanced") {
			t.Fatalf("search route status = %d, body = %s", recorder.Code, recorder.Body.String())
		}
		time.Sleep(time.Millisecond)
	}
	if stats := second.searchManager.Stats(); stats.Commits != 0 {
		t.Fatalf("secondary-index catalog change rebuilt search projection: %#v", stats)
	}
}
