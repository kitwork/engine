package work

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kitwork/engine/database"
	"github.com/kitwork/engine/value"
)


func TestConsoleAndJSON(t *testing.T) {
	// Setup a temporary directory for the tenant
	tmpDir, err := os.MkdirTemp("", "kitwork-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	tenantDir := filepath.Join(tmpDir, "test", "localhost")
	err = os.MkdirAll(tenantDir, 0755)
	if err != nil {
		t.Fatal(err)
	}

	// Write app.kitwork.js with JSON and console usage
	scriptCode := `
let obj = { name: "Kitwork", version: 1 };
let serialized = JSON.stringify(obj);
let parsed = JSON.parse(serialized);
console.log("Parsed name:", parsed.name);
`
	err = os.WriteFile(filepath.Join(tenantDir, "app.kitwork.js"), []byte(scriptCode), 0644)
	if err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(tmpDir, "localhost")
	err = tenant.Run()
	if err != nil {
		t.Fatalf("failed to run tenant: %v", err)
	}

	// Verify globals are set
	val, ok := tenant.vm.Globals["JSON"]
	if !ok {
		t.Error("JSON global not found")
	}
	if val.K != value.Map {
		t.Errorf("expected JSON to be a map, got %v", val.K)
	}
}

func TestVMEnergyLimitAndLineMapping(t *testing.T) {
	// Setup a temporary directory
	tmpDir, err := os.MkdirTemp("", "kitwork-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	tenantDir := filepath.Join(tmpDir, "test", "localhost")
	err = os.MkdirAll(tenantDir, 0755)
	if err != nil {
		t.Fatal(err)
	}

	// Write script containing a loop that will exceed energy limits
	// We put it at line 5 to test line mapping accuracy!
	scriptCode := `// line 1
// line 2
// line 3
let recursive = (x) => {
    return recursive(x + 1);
};
recursive(0);
`
	err = os.WriteFile(filepath.Join(tenantDir, "app.kitwork.js"), []byte(scriptCode), 0644)
	if err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(tmpDir, "localhost")
	tenant.MaxEnergy = 300 // Set limit so it executes the initial call but halts inside body
	err = tenant.Run()
	if err == nil {
		t.Fatal("expected run to fail due to energy limit, but it succeeded")
	}

	if !strings.Contains(err.Error(), "Energy Limit Exceeded") {
		t.Errorf("expected energy limit error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "at line 5") {
		t.Errorf("expected line mapping to point to line 5, got: %v", err)
	}
}

func TestDatabaseAtomic(t *testing.T) {
	// 1. Create a database config
	cfg := &database.Config{
		Type:     "postgres",
		User:     "postgres",
		Password: "db.kitwork.io@03122025",
		Name:     "postgres",
		Host:     "152.42.253.164",
		Port:     5432,
		SSLMode:  "require",
		Timezone: "Asia/Ho_Chi_Minh",
	}

	// 2. Connect
	dbConn, err := cfg.Connect()
	if err != nil {
		t.Skipf("Skipping test because database connection failed: %v", err)
		return
	}
	defer dbConn.Close()
	database.Default = dbConn

	// 3. Setup test table
	_, err = dbConn.Exec(`CREATE TABLE IF NOT EXISTS test_atomic_tx (
		id SERIAL PRIMARY KEY,
		val VARCHAR(255),
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		deleted_at TIMESTAMP
	)`)
	if err != nil {
		t.Fatalf("Failed to create test table: %v", err)
	}
	defer func() {
		dbConn.Exec(`DROP TABLE IF EXISTS test_atomic_tx`)
	}()

	// 4. Create Tenant and KitWork environment
	tmpDir, err := os.MkdirTemp("", "kitwork-db-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	tenantDir := filepath.Join(tmpDir, "test", "localhost")
	err = os.MkdirAll(tenantDir, 0755)
	if err != nil {
		t.Fatal(err)
	}

	// CASE 1: Successful Transaction (Commit)
	scriptSuccess := `
	const { database } = kitwork();
	const db = database.connection();
	db.atomic((tx) => {
		tx.table("test_atomic_tx").create({ val: "success_1" });
		tx.table("test_atomic_tx").create({ val: "success_2" });
	});
	`
	err = os.WriteFile(filepath.Join(tenantDir, "app.kitwork.js"), []byte(scriptSuccess), 0644)
	if err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(tmpDir, "localhost")
	err = tenant.Run()
	if err != nil {
		t.Fatalf("run success script failed: %v", err)
	}

	// Check if both records are committed
	var count int
	err = dbConn.QueryRow("SELECT COUNT(*) FROM test_atomic_tx").Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("Expected 2 records to be committed, got %d", count)
	}

	// CASE 2: Transaction with Rollback (logical JS error/panic inside VM)
	scriptFail := `
	const { database } = kitwork();
	const db = database.connection();
	db.atomic((tx) => {
		tx.table("test_atomic_tx").create({ val: "should_rollback" });
		return JSON.parse("{");
	});
	`
	err = os.WriteFile(filepath.Join(tenantDir, "app.kitwork.js"), []byte(scriptFail), 0644)
	if err != nil {
		t.Fatal(err)
	}

	tenantFail := NewTenant(tmpDir, "localhost")
	_ = tenantFail.Run()

	// Check count again, should still be 2 (success_1 and success_2)
	err = dbConn.QueryRow("SELECT COUNT(*) FROM test_atomic_tx WHERE val = 'should_rollback'").Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("Expected 0 rollback records, got %d", count)
	}
}

func TestRouterStaticCache(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kitwork-static-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	tenantDir := filepath.Join(tmpDir, "test", "localhost")
	err = os.MkdirAll(tenantDir, 0755)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Create a script that configures a route with static cache
	scriptCode := `
	const { router } = kitwork();
	
	// Increment counter on each dynamic execution
	let count = 0;
	router.get("/dynamic-to-static").static("1s").handle((response) => {
		count = count + 1;
		return response.json({ execution_count: count });
	});
	`
	err = os.WriteFile(filepath.Join(tenantDir, "app.kitwork.js"), []byte(scriptCode), 0644)
	if err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(tmpDir, "localhost")
	err = tenant.Run()
	if err != nil {
		t.Fatalf("failed to run tenant: %v", err)
	}

	// 2. Perform HTTP-like requests by directly calling Serve
	// First request: Should trigger VM and cache the output
	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest("GET", "/dynamic-to-static", nil)
	tenant.Serve(rec1, req1)

	if rec1.Code != 200 {
		t.Errorf("expected status 200, got %d", rec1.Code)
	}
	if !strings.Contains(rec1.Body.String(), `"execution_count":1`) {
		t.Errorf("expected execution_count 1, got: %s", rec1.Body.String())
	}

	// Second request: Should hit static cache, not VM (execution_count remains 1)
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/dynamic-to-static", nil)
	tenant.Serve(rec2, req2)

	if rec2.Code != 200 {
		t.Errorf("expected status 200, got %d", rec2.Code)
	}
	if !strings.Contains(rec2.Body.String(), `"execution_count":1`) {
		t.Errorf("expected execution_count 1 from cache, got: %s", rec2.Body.String())
	}

	// 3. Verify the static cache file was created under tenants/test/localhost/.static/
	staticDir := filepath.Join(tenantDir, ".static")
	if _, err := os.Stat(staticDir); os.IsNotExist(err) {
		t.Error("expected static cache directory to be created, but it was not found")
	}

	// 4. Wait for cache expiration (1.1 seconds)
	time.Sleep(1100 * time.Millisecond)

	// Third request: Cache expired, should trigger VM again (execution_count incremented to 2)
	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest("GET", "/dynamic-to-static", nil)
	tenant.Serve(rec3, req3)

	if rec3.Code != 200 {
		t.Errorf("expected status 200, got %d", rec3.Code)
	}
	if !strings.Contains(rec3.Body.String(), `"execution_count":2`) {
		t.Errorf("expected execution_count 2 after cache expiration, got: %s", rec3.Body.String())
	}
}


