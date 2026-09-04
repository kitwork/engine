package work

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

func TestKitDBCreateManyCommitsBoundedBatchesThroughRelationalPath(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	source := `import { router, database } from "kitwork";
const { kitdb, struct, text, int, choice } = database;
const products = struct({
  id: text().key(),
  sku: text().notNull().unique(),
  category: text().notNull().index(),
  stock: int().notNull().check(">=", 0),
  status: choice("active", "disabled").default("active")
});
const db = kitdb("bulk.kitdb", { products });
router.get((ctx) => {
  const rows = [];
  for (let i = 0; i < 600; i++) {
    rows.push({ id: "product-" + i, sku: "SKU-" + i, category: "category-" + (i % 10), stock: i });
  }
  const progress = db.products.createMany(rows, { batch: 128 });
  return ctx.json({
    progress: progress,
    count: db.products.count(),
    category: db.products.where("category", "=", "category-7").count(),
    found: db.products.where("sku", "=", "SKU-511").first().id
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
	recorder := httptest.NewRecorder()
	tenant.Serve(recorder, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("bulk route status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	for _, expected := range []string{
		`"inserted":600`, `"batches":5`, `"next":600`, `"complete":true`,
		`"count":600`, `"category":60`, `"found":"product-511"`,
	} {
		if !strings.Contains(recorder.Body.String(), expected) {
			t.Errorf("bulk response does not contain %s: %s", expected, recorder.Body.String())
		}
	}
}

func TestKitDBCreateManyReportsDurableResumeOffsetAfterBatchFailure(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	source := `import { router, database } from "kitwork";
const { kitdb, struct, text } = database;
const products = struct({ id: text().key(), sku: text().notNull().unique() });
const db = kitdb("bulk-resume.kitdb", { products });
router.get((ctx) => {
  const action = ctx.query("action");
  const rows = [];
  const start = action === "retry" ? 256 : 0;
  for (let i = start; i < 300; i++) {
    const sku = action !== "retry" && i === 270 ? "SKU-0" : "SKU-" + i;
    rows.push({ id: "product-" + i, sku: sku });
  }
  if (action === "retry") {
    const progress = db.products.createMany(rows, { batch: 64 });
    return ctx.json({ progress: progress, count: db.products.count() });
  }
  const attempt = db.products.createMany(rows, { batch: 128 }).safe();
  return ctx.json({
    ok: attempt.ok,
    code: attempt.code,
    progress: attempt.value,
    count: db.products.count(),
    rolledBack: db.products.find("product-269") === null
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

	failed := httptest.NewRecorder()
	tenant.Serve(failed, httptest.NewRequest(http.MethodGet, "http://localhost/?action=fail", nil))
	if failed.Code != http.StatusOK {
		t.Fatalf("failed bulk status = %d, body = %s", failed.Code, failed.Body.String())
	}
	for _, expected := range []string{
		`"ok":false`, `"code":"KITDB_CREATE_MANY_PARTIAL"`, `"inserted":256`,
		`"batches":2`, `"next":256`, `"failedRow":270`, `"complete":false`,
		`"count":256`, `"rolledBack":true`,
	} {
		if !strings.Contains(failed.Body.String(), expected) {
			t.Errorf("failed bulk response does not contain %s: %s", expected, failed.Body.String())
		}
	}

	retried := httptest.NewRecorder()
	tenant.Serve(retried, httptest.NewRequest(http.MethodGet, "http://localhost/?action=retry", nil))
	if retried.Code != http.StatusOK {
		t.Fatalf("retry bulk status = %d, body = %s", retried.Code, retried.Body.String())
	}
	for _, expected := range []string{
		`"inserted":44`, `"batches":1`, `"next":44`, `"complete":true`, `"count":300`,
	} {
		if !strings.Contains(retried.Body.String(), expected) {
			t.Errorf("retry bulk response does not contain %s: %s", expected, retried.Body.String())
		}
	}
}

func TestKitDBCreateManyCancellationStopsBeforeFirstBatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := (&SchemaTable{}).createManyKitDB(
		ctx,
		[]value.Value{value.New(map[string]value.Value{"id": value.New("one")})},
		1,
	)
	if !result.IsError {
		t.Fatalf("canceled createMany result = %#v, want attached error", result)
	}
	progress := result.Map()
	if progress["inserted"].Int() != 0 || progress["next"].Int() != 0 || progress["complete"].N != 0 {
		t.Fatalf("canceled createMany progress = %#v", progress)
	}
	failure, ok := value.FailureFrom(result)
	if !ok || failure.Code != kitDBCreateManyFailedCode {
		t.Fatalf("canceled createMany error = %#v", result.ErrorVal)
	}
}

func TestKitDBCreateManyArgumentsAreBoundedBeforeWriting(t *testing.T) {
	rows := make([]value.Value, kitDBCreateManyRowLimit+1)
	for index := range rows {
		rows[index] = value.New(map[string]value.Value{"id": value.New(index)})
	}
	if _, _, err := parseKitDBCreateManyArguments([]value.Value{value.New(rows)}); err == nil ||
		!strings.Contains(err.Error(), "at most 10000 rows") {
		t.Fatalf("row bound error = %v", err)
	}
	if _, _, err := parseKitDBCreateManyArguments([]value.Value{
		value.New([]value.Value{}),
		value.New(map[string]value.Value{"batch": value.New(257)}),
	}); err == nil || !strings.Contains(err.Error(), "between 1 and 256") {
		t.Fatalf("batch bound error = %v", err)
	}
}
