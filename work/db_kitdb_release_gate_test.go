package work

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	kitdbengine "github.com/kitwork/engine/kitdb"
)

const (
	kitDBReleaseEvidenceSchemaVersion = 1
	kitDBReleaseReportEnvironment     = "KITDB_RELEASE_REPORT"
)

type kitDBReleaseStepEvidence struct {
	Name       string `json:"name"`
	DurationMS int64  `json:"duration_ms"`
	Success    bool   `json:"success"`
	Error      string `json:"error,omitempty"`
}

type kitDBReleaseEvidence struct {
	SchemaVersion      uint16                     `json:"schema_version"`
	StartedAt          time.Time                  `json:"started_at"`
	FinishedAt         time.Time                  `json:"finished_at"`
	DurationMS         int64                      `json:"duration_ms"`
	GoVersion          string                     `json:"go_version"`
	OS                 string                     `json:"os"`
	Arch               string                     `json:"arch"`
	Success            bool                       `json:"success"`
	Failure            string                     `json:"failure,omitempty"`
	DatabaseID         string                     `json:"database_id,omitempty"`
	SourceTransaction  uint64                     `json:"source_transaction"`
	BackupTransaction  uint64                     `json:"backup_transaction"`
	RestoreTransaction uint64                     `json:"restore_transaction"`
	BackupSHA256       string                     `json:"backup_sha256,omitempty"`
	Rows               int                        `json:"rows"`
	Steps              []kitDBReleaseStepEvidence `json:"steps"`
}

type kitDBReleaseJourney struct {
	root         string
	directory    string
	routerPath   string
	databasePath string
	anchorPath   string
	restoredPath string
	anchor       kitdbengine.BackupAnchor
}

// TestKitDBDatabaseReleaseGate composes the complete supported database
// journey over one durable catalog and row representation. Focused unit tests
// still prove each failure boundary; this gate prevents the frontends and
// operational primitives from succeeding only in isolation.
func TestKitDBDatabaseReleaseGate(t *testing.T) {
	startedAt := time.Now().UTC()
	evidence := kitDBReleaseEvidence{
		SchemaVersion: kitDBReleaseEvidenceSchemaVersion,
		StartedAt:     startedAt,
		GoVersion:     runtime.Version(),
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
	}
	journey, err := newKitDBReleaseJourney(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	steps := []struct {
		name string
		run  func(*kitDBReleaseJourney, *kitDBReleaseEvidence) error
	}{
		{name: "schema-crud-transaction-index", run: exerciseKitDBReleaseFrontends},
		{name: "restart-and-query", run: restartKitDBReleaseSource},
		{name: "verify-source", run: verifyKitDBReleaseSource},
		{name: "backup-and-verify", run: backupKitDBReleaseSource},
		{name: "restore-and-verify", run: restoreKitDBReleaseAnchor},
		{name: "reopen-restored-and-query", run: reopenKitDBReleaseRestore},
	}

	for _, step := range steps {
		stepStartedAt := time.Now()
		stepErr := step.run(journey, &evidence)
		result := kitDBReleaseStepEvidence{
			Name:       step.name,
			DurationMS: time.Since(stepStartedAt).Milliseconds(),
			Success:    stepErr == nil,
		}
		if stepErr != nil {
			result.Error = sanitizeKitDBReleaseFailure(stepErr.Error(), journey.root)
			evidence.Failure = result.Error
		}
		evidence.Steps = append(evidence.Steps, result)
		if stepErr != nil {
			break
		}
	}

	evidence.FinishedAt = time.Now().UTC()
	evidence.DurationMS = time.Since(startedAt).Milliseconds()
	evidence.Success = evidence.Failure == "" && len(evidence.Steps) == len(steps)
	if reportPath := strings.TrimSpace(os.Getenv(kitDBReleaseReportEnvironment)); reportPath != "" {
		resolved := kitDBReleaseReportPath(reportPath)
		if err := writeKitDBReleaseEvidence(resolved, evidence); err != nil {
			t.Fatalf("write KitDB release evidence: %v", err)
		}
		t.Logf("KitDB database evidence: %s", reportPath)
	}
	if !evidence.Success {
		t.Fatal(evidence.Failure)
	}
	t.Logf(
		"KitDB database journey passed: transaction=%d rows=%d backup=%s",
		evidence.SourceTransaction,
		evidence.Rows,
		evidence.BackupSHA256,
	)
}

func newKitDBReleaseJourney(root string) (*kitDBReleaseJourney, error) {
	directory := filepath.Join(root, "test", "localhost")
	dataDirectory := filepath.Join(directory, ".data")
	backupDirectory := filepath.Join(root, "backups")
	for _, path := range []string{directory, dataDirectory, backupDirectory} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			return nil, fmt.Errorf("create release-gate directory: %w", err)
		}
	}
	return &kitDBReleaseJourney{
		root:         root,
		directory:    directory,
		routerPath:   filepath.Join(directory, RouterFileName),
		databasePath: filepath.Join(dataDirectory, "journey.kitdb"),
		anchorPath:   filepath.Join(backupDirectory, "journey-anchor.kitdb"),
		restoredPath: filepath.Join(dataDirectory, "restored.kitdb"),
	}, nil
}

func exerciseKitDBReleaseFrontends(journey *kitDBReleaseJourney, evidence *kitDBReleaseEvidence) error {
	if err := journey.writeRouter("journey.kitdb", true); err != nil {
		return err
	}
	tenant, server, err := journey.startTenant()
	if err != nil {
		return err
	}
	defer server.Close()
	defer tenant.Close()

	if body, err := kitDBReleaseGET(server.URL + "/?action=seed"); err != nil {
		return err
	} else if err := requireKitDBReleaseContains(body, `"count":2`, `"stock":8`); err != nil {
		return fmt.Errorf("local ORM seed transaction: %w", err)
	}

	batch := []string{
		"BEGIN",
		"UPDATE products SET price = price + 5 WHERE sku = 'KIT-1'",
		"INSERT INTO products (id, sku, title, price, stock, status) VALUES ('product_3', 'KIT-3', 'Gamma', 300, 4, 'disabled')",
		"DELETE FROM products WHERE sku = 'KIT-2'",
		"COMMIT",
	}
	if body, err := kitDBReleaseHranaBatch(server.URL, "journey.kitdb", batch); err != nil {
		return err
	} else if bytes.Contains(body, []byte(`"type":"error"`)) {
		return fmt.Errorf("remote transaction failed: %s", boundedKitDBReleaseBody(body))
	}

	selected, err := kitDBReleaseHranaExecute(
		server.URL,
		"journey.kitdb",
		"SELECT sku, price, stock, status FROM products ORDER BY sku ASC",
		true,
	)
	if err != nil {
		return err
	}
	if err := requireKitDBReleaseContains(selected, `"KIT-1"`, `"KIT-3"`, `"105"`, `"disabled"`); err != nil {
		return fmt.Errorf("remote select after transaction: %w", err)
	}
	if bytes.Contains(selected, []byte(`"KIT-2"`)) {
		return fmt.Errorf("remote transaction retained deleted KIT-2: %s", boundedKitDBReleaseBody(selected))
	}

	if _, err := kitDBReleaseHranaExecute(
		server.URL,
		"journey.kitdb",
		"CREATE INDEX products_status_price ON products (status, price)",
		false,
	); err != nil {
		return err
	}
	if _, err := kitDBReleaseHranaExecute(
		server.URL,
		"journey.kitdb",
		"PRAGMA index_status(products)",
		true,
	); err != nil {
		return err
	}
	if err := waitForKitDBReleaseIndex(server.URL, "journey.kitdb"); err != nil {
		return err
	}

	status, err := kitDBReleaseGET(server.URL + "/")
	if err != nil {
		return err
	}
	if err := requireKitDBReleaseContains(
		status,
		`"count":2`,
		`"total":405`,
		`"stock":8`,
		`"disabled":1`,
	); err != nil {
		return fmt.Errorf("local ORM post-transaction state: %w", err)
	}
	evidence.Rows = 2
	return nil
}

func restartKitDBReleaseSource(journey *kitDBReleaseJourney, _ *kitDBReleaseEvidence) error {
	if err := journey.writeRouter("journey.kitdb", false); err != nil {
		return err
	}
	tenant, server, err := journey.startTenant()
	if err != nil {
		return err
	}
	defer server.Close()
	defer tenant.Close()

	status, err := kitDBReleaseGET(server.URL + "/")
	if err != nil {
		return err
	}
	if err := requireKitDBReleaseContains(status, `"count":2`, `"total":405`, `"stock":8`); err != nil {
		return fmt.Errorf("catalog-hydrated ORM restart: %w", err)
	}
	selected, err := kitDBReleaseHranaExecute(
		server.URL,
		"journey.kitdb",
		"SELECT sku FROM products WHERE status = 'disabled' AND price >= 250 ORDER BY price ASC LIMIT 5",
		true,
	)
	if err != nil {
		return err
	}
	return requireKitDBReleaseContains(selected, `"KIT-3"`)
}

func verifyKitDBReleaseSource(journey *kitDBReleaseJourney, evidence *kitDBReleaseEvidence) error {
	database, err := kitdbengine.OpenWithOptions(
		journey.databasePath,
		kitdbengine.OpenOptions{VerifyOnOpen: true},
	)
	if err != nil {
		return fmt.Errorf("verified source open: %w", err)
	}
	if err := database.Verify(); err != nil {
		_ = database.Close()
		return fmt.Errorf("verify source: %w", err)
	}
	transaction, err := database.LastTransaction()
	if err != nil {
		_ = database.Close()
		return fmt.Errorf("read source transaction: %w", err)
	}
	databaseID := database.ID()
	if transaction == 0 || databaseID == "" {
		_ = database.Close()
		return fmt.Errorf("verified source has empty identity or transaction")
	}
	if err := database.Close(); err != nil {
		return fmt.Errorf("close verified source: %w", err)
	}
	evidence.DatabaseID = databaseID
	evidence.SourceTransaction = transaction
	return nil
}

func backupKitDBReleaseSource(journey *kitDBReleaseJourney, evidence *kitDBReleaseEvidence) error {
	database, err := kitdbengine.OpenWithOptions(
		journey.databasePath,
		kitdbengine.OpenOptions{VerifyOnOpen: true},
	)
	if err != nil {
		return fmt.Errorf("open source for backup: %w", err)
	}
	anchor, backupErr := database.CreateBackupAnchor(context.Background(), journey.anchorPath)
	closeErr := database.Close()
	if backupErr != nil {
		return fmt.Errorf("create backup anchor: %w", backupErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close source after backup: %w", closeErr)
	}
	verified, err := kitdbengine.VerifyBackupAnchor(context.Background(), journey.anchorPath)
	if err != nil {
		return fmt.Errorf("verify backup anchor: %w", err)
	}
	if verified.DatabaseID != evidence.DatabaseID ||
		verified.Transaction != evidence.SourceTransaction ||
		verified.SHA256 == "" || verified != anchor {
		return fmt.Errorf(
			"backup boundary mismatch: id=%t transaction=%d/%d digest=%t",
			verified.DatabaseID == evidence.DatabaseID,
			verified.Transaction,
			evidence.SourceTransaction,
			verified.SHA256 != "",
		)
	}
	journey.anchor = verified
	evidence.BackupTransaction = verified.Transaction
	evidence.BackupSHA256 = verified.SHA256
	return nil
}

func restoreKitDBReleaseAnchor(journey *kitDBReleaseJourney, evidence *kitDBReleaseEvidence) error {
	result, err := kitdbengine.RestoreToTransaction(
		context.Background(),
		journey.anchorPath,
		"",
		journey.restoredPath,
		journey.anchor.Transaction,
	)
	if err != nil {
		return fmt.Errorf("restore backup anchor: %w", err)
	}
	verified, err := kitdbengine.VerifyBackupAnchor(context.Background(), journey.restoredPath)
	if err != nil {
		return fmt.Errorf("verify restored image: %w", err)
	}
	if result.DatabaseID != evidence.DatabaseID ||
		verified.DatabaseID != evidence.DatabaseID ||
		result.Transaction != evidence.SourceTransaction ||
		verified.Transaction != evidence.SourceTransaction ||
		result.SHA256 != verified.SHA256 {
		return fmt.Errorf("restored image does not preserve source identity and boundary")
	}
	evidence.RestoreTransaction = result.Transaction
	return nil
}

func reopenKitDBReleaseRestore(journey *kitDBReleaseJourney, evidence *kitDBReleaseEvidence) error {
	if err := journey.writeRouter("restored.kitdb", false); err != nil {
		return err
	}
	tenant, server, err := journey.startTenant()
	if err != nil {
		return err
	}
	defer server.Close()
	defer tenant.Close()

	status, err := kitDBReleaseGET(server.URL + "/")
	if err != nil {
		return err
	}
	if err := requireKitDBReleaseContains(
		status,
		`"count":2`,
		`"total":405`,
		`"stock":8`,
		`"disabled":1`,
	); err != nil {
		return fmt.Errorf("restored local ORM query: %w", err)
	}
	selected, err := kitDBReleaseHranaExecute(
		server.URL,
		"restored.kitdb",
		"SELECT sku, price FROM products WHERE status = 'disabled' ORDER BY price ASC",
		true,
	)
	if err != nil {
		return err
	}
	if err := requireKitDBReleaseContains(selected, `"KIT-3"`, `"300"`); err != nil {
		return fmt.Errorf("restored Hrana query: %w", err)
	}
	explained, err := kitDBReleaseHranaExecute(
		server.URL,
		"restored.kitdb",
		"EXPLAIN QUERY PLAN SELECT sku FROM products WHERE status = 'disabled' AND price >= 250 ORDER BY price ASC LIMIT 5",
		true,
	)
	if err != nil {
		return err
	}
	if err := requireKitDBReleaseContains(explained, "products_status_price"); err != nil {
		return fmt.Errorf("restored index plan: %w", err)
	}
	if evidence.SourceTransaction != evidence.BackupTransaction ||
		evidence.SourceTransaction != evidence.RestoreTransaction {
		return fmt.Errorf("release journey crossed different transaction boundaries")
	}
	return nil
}

func (journey *kitDBReleaseJourney) writeRouter(databaseName string, declared bool) error {
	declaration := `const db = database.kitdb("` + databaseName + `", {}, { token: "release-secret", access: "readwrite" });`
	if declared {
		declaration = `const { kitdb, struct, id, text, int, choice } = database;
const products = struct({
  id: id(),
  sku: text().notNull().unique(),
  title: text().notNull(),
  price: int().default(0),
  stock: int().default(0),
  status: choice("active", "disabled").default("active")
});
const db = kitdb("` + databaseName + `", { products }, { token: "release-secret", access: "readwrite" });`
	}
	source := `import { router, database } from "kitwork";
` + declaration + `
router.get((ctx) => {
  const action = ctx.query("action");
  if (action === "seed") {
    const seeded = db.transaction((tx) => {
      tx.products.create({ id: "product_1", sku: "KIT-1", title: "Alpha", price: 100, stock: 10 });
      tx.products.create({ id: "product_2", sku: "KIT-2", title: "Beta", price: 200, stock: 5, status: "disabled" });
      tx.products.where("sku", "=", "KIT-1").update({ stock: 8 });
      return { count: tx.products.count(), stock: tx.products.where("sku", "=", "KIT-1").first().stock };
    });
    return ctx.json(seeded);
  }
  const first = db.products.where("sku", "=", "KIT-1").first();
  return ctx.json({
    count: db.products.count(),
    total: db.products.sum("price"),
    stock: first ? first.stock : -1,
    disabled: db.products.where("status", "=", "disabled").count()
  });
});`
	if err := os.WriteFile(journey.routerPath, []byte(source), 0o644); err != nil {
		return fmt.Errorf("write release-gate router: %w", err)
	}
	return nil
}

func (journey *kitDBReleaseJourney) startTenant() (*Tenant, *httptest.Server, error) {
	tenant := NewTenant(journey.root, "localhost")
	if err := tenant.Run(); err != nil {
		tenant.Close()
		return nil, nil, fmt.Errorf("start release-gate tenant: %w", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		request.Host = "localhost"
		tenant.Serve(writer, request)
	}))
	return tenant, server, nil
}

func kitDBReleaseGET(url string) ([]byte, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("release-gate GET: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("read release-gate response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release-gate GET status %d: %s", response.StatusCode, boundedKitDBReleaseBody(body))
	}
	return body, nil
}

func kitDBReleaseHranaExecute(serverURL, databaseName, sql string, wantRows bool) ([]byte, error) {
	return kitDBReleaseHranaRequest(serverURL, databaseName, map[string]any{
		"baton": nil,
		"requests": []any{
			map[string]any{
				"type": "execute",
				"stmt": map[string]any{"sql": sql, "want_rows": wantRows},
			},
			map[string]any{"type": "close"},
		},
	})
}

func kitDBReleaseHranaBatch(serverURL, databaseName string, statements []string) ([]byte, error) {
	steps := make([]any, 0, len(statements))
	for _, sql := range statements {
		steps = append(steps, map[string]any{
			"stmt": map[string]any{"sql": sql, "want_rows": false},
		})
	}
	return kitDBReleaseHranaRequest(serverURL, databaseName, map[string]any{
		"baton": nil,
		"requests": []any{
			map[string]any{"type": "batch", "batch": map[string]any{"steps": steps}},
			map[string]any{"type": "close"},
		},
	})
}

func kitDBReleaseHranaRequest(serverURL, databaseName string, payload any) ([]byte, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode release-gate Hrana request: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		serverURL+"/"+databaseName+"/v3/pipeline",
		bytes.NewReader(encoded),
	)
	if err != nil {
		return nil, fmt.Errorf("create release-gate Hrana request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer release-secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("execute release-gate Hrana request: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("read release-gate Hrana response: %w", err)
	}
	if response.StatusCode != http.StatusOK || bytes.Contains(body, []byte(`"type":"error"`)) {
		return nil, fmt.Errorf("release-gate Hrana status %d: %s", response.StatusCode, boundedKitDBReleaseBody(body))
	}
	return body, nil
}

func waitForKitDBReleaseIndex(serverURL, databaseName string) error {
	deadline := time.Now().Add(15 * time.Second)
	var last []byte
	for {
		explained, err := kitDBReleaseHranaExecute(
			serverURL,
			databaseName,
			"EXPLAIN QUERY PLAN SELECT sku FROM products WHERE status = 'disabled' AND price >= 250 ORDER BY price ASC LIMIT 5",
			true,
		)
		if err != nil {
			return err
		}
		last = explained
		if bytes.Contains(explained, []byte("products_status_price")) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("online index did not become planner-visible: %s", boundedKitDBReleaseBody(last))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func requireKitDBReleaseContains(body []byte, expected ...string) error {
	for _, fragment := range expected {
		if !bytes.Contains(body, []byte(fragment)) {
			return fmt.Errorf("response %s does not contain %q", boundedKitDBReleaseBody(body), fragment)
		}
	}
	return nil
}

func boundedKitDBReleaseBody(body []byte) string {
	const maximum = 4 << 10
	if len(body) > maximum {
		body = body[len(body)-maximum:]
	}
	return strings.TrimSpace(string(body))
}

func sanitizeKitDBReleaseFailure(message, workspace string) string {
	for _, path := range []string{workspace, filepath.ToSlash(workspace)} {
		if path != "" {
			message = strings.ReplaceAll(message, path, "<gate-workspace>")
		}
	}
	return message
}

func writeKitDBReleaseEvidence(path string, evidence kitDBReleaseEvidence) error {
	if directory := filepath.Dir(path); directory != "." && directory != "" {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return err
		}
	}
	encoded, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	return os.WriteFile(path, encoded, 0o644)
}

func kitDBReleaseReportPath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		return path
	}
	return filepath.Join(filepath.Dir(filepath.Dir(sourceFile)), path)
}
