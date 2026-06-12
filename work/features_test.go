package work

import (
	"encoding/json"
	"net/http"
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
	tenant.databases["default"] = dbConn
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
	tenantFail.databases["default"] = dbConn
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

func TestHTTPSSRFBlocking(t *testing.T) {
	h := &HTTP{}

	// --- 1. Test standard blocking (AllowLocal = false) ---
	AllowLocal = false

	// Test private IP (loopback)
	resLocal := h.Get("http://127.0.0.1:8080/hello")
	respLocal, ok := resLocal.V.(HTTPResponse)
	if !ok {
		t.Fatalf("expected HTTPResponse structure, got %T", resLocal.V)
	}
	if !strings.Contains(respLocal.Error, "SSRF prevention") && !strings.Contains(respLocal.Error, "blocked") {
		t.Errorf("expected SSRF blocked error, got: %s", respLocal.Error)
	}

	// Test hostname localhost (resolves to 127.0.0.1 or ::1)
	resLocalhost := h.Get("http://localhost:8080/hello")
	respLocalhost, _ := resLocalhost.V.(HTTPResponse)
	if !strings.Contains(respLocalhost.Error, "SSRF prevention") && !strings.Contains(respLocalhost.Error, "blocked") {
		t.Errorf("expected SSRF blocked error for localhost, got: %s", respLocalhost.Error)
	}

	// --- 2. Test standard bypass (AllowLocal = true) ---
	AllowLocal = true
	resLocalBypass := h.Get("http://127.0.0.1:8080/hello")
	respLocalBypass, _ := resLocalBypass.V.(HTTPResponse)
	// Should NOT say "SSRF prevention" or "blocked" (might fail with connection refused/etc but not SSRF)
	if strings.Contains(respLocalBypass.Error, "SSRF prevention") {
		t.Errorf("expected private IP request to be allowed when AllowLocal=true, got: %s", respLocalBypass.Error)
	}
	AllowLocal = false // restore

	// --- 3. Test relative path automatic resolution & SSRF bypass ---
	ServerPort = 9999
	resRelative := h.Get("/hello-relative")
	respRelative, _ := resRelative.V.(HTTPResponse)
	// Should resolve to http://127.0.0.1:9999/hello-relative and not block with SSRF prevention
	if strings.Contains(respRelative.Error, "SSRF prevention") {
		t.Errorf("expected relative path to bypass SSRF filter, got: %s", respRelative.Error)
	}
	// Verify it resolved to the right port
	if !strings.Contains(respRelative.Error, "127.0.0.1:9999") && !strings.Contains(respRelative.Error, "dial tcp 127.0.0.1:9999") {
		t.Errorf("expected relative path to target 127.0.0.1:9999, got error: %s", respRelative.Error)
	}
	ServerPort = 0 // restore

	// --- 4. Test public endpoint (should NOT trigger SSRF blocking) ---
	resPublic := h.Get("https://github.com")
	respPublic, _ := resPublic.V.(HTTPResponse)
	if strings.Contains(respPublic.Error, "SSRF prevention") {
		t.Errorf("expected public request not to be blocked by SSRF filter, got: %s", respPublic.Error)
	}
}

func TestDatabaseFallbackAndErrorPropagation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kitwork-fallback-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	tenantDir := filepath.Join(tmpDir, "test", "kitwork.vn")
	err = os.MkdirAll(tenantDir, 0755)
	if err != nil {
		t.Fatalf("failed to create tenant dir: %v", err)
	}

	appJsCode := `
import { router, database } from 'kitwork';
const db = database.connection();

// Configure global catch handler
router.catch((err, response) => {
	return response.text("global caught: " + err);
});

router.get("/test").handle((response) => {
	db.table("user").find("id", 1);
	return response.text("ok");
});

router.get("/error").handle((response) => {
	db.table("non_existent_table").find("id", 1);
	return response.text("unexpected success");
});
`
	err = os.WriteFile(filepath.Join(tenantDir, "app.kitwork.js"), []byte(appJsCode), 0644)
	if err != nil {
		t.Fatalf("failed to write app.kitwork.js: %v", err)
	}

	tenant := NewTenant(tmpDir, "kitwork.vn")
	err = tenant.Run()
	if err != nil {
		t.Fatalf("failed to run tenant: %v", err)
	}

	route, _ := tenant.routes.Match("GET", "/test")
	if route == nil {
		t.Fatal("route /test not found")
	}

	rec := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	tenant.Serve(rec, req)

	dbPath := filepath.Join(tenantDir, "kitwork.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Errorf("expected fallback SQLite database to be created at: %s", dbPath)
	}

	recErr := httptest.NewRecorder()
	reqErr, _ := http.NewRequest("GET", "/error", nil)
	tenant.Serve(recErr, reqErr)

	body := recErr.Body.String()
	if !strings.Contains(body, "global caught:") {
		t.Errorf("expected error to be caught by global JS catch block, got body: %q", body)
	}
}

func TestHandleErrorAndCatch(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kitwork-handle-err-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	tenantDir := filepath.Join(tmpDir, "test", "localhost")
	err = os.MkdirAll(tenantDir, 0755)
	if err != nil {
		t.Fatal(err)
	}

	appJsCode := `
import { router } from 'kitwork';

router.get("/error-flow").handle((ctx) => {
	ctx.res().status(418).text("response_from_handle");
	ctx.error("my_logged_error");
}).catch((err, response) => {
	response.text("modified_by_catch_" + err);
});

router.get("/error-flow-return").handle((ctx) => {
	ctx.res().status(500);
	ctx.error("database_failure");
}).catch((err) => {
	return { error: err };
});

router.get("/error-flow-default-500").handle((ctx) => {
	ctx.error("unhandled_exception");
}).catch((err) => {
	return { error: err };
});
`
	err = os.WriteFile(filepath.Join(tenantDir, "app.kitwork.js"), []byte(appJsCode), 0644)
	if err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(tmpDir, "localhost")
	err = tenant.Run()
	if err != nil {
		t.Fatal(err)
	}

	// Case 1: Existing catch text modification
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/error-flow", nil)
	tenant.Serve(rec, req)

	t.Logf("Response code 1: %d, body: %q", rec.Code, rec.Body.String())
	if rec.Code != 418 {
		t.Errorf("expected status 418, got %d", rec.Code)
	}
	if rec.Body.String() != "modified_by_catch_my_logged_error" {
		t.Errorf("expected body to be modified by catch handler, got %q", rec.Body.String())
	}

	// Case 2: Catch return JSON with custom status 500
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/error-flow-return", nil)
	tenant.Serve(rec2, req2)

	t.Logf("Response code 2: %d, body: %q", rec2.Code, rec2.Body.String())
	if rec2.Code != 500 {
		t.Errorf("expected status 500, got %d", rec2.Code)
	}
	expectedJSON := `{"error":"database_failure"}`
	if rec2.Body.String() != expectedJSON {
		t.Errorf("expected catch JSON return body %q, got %q", expectedJSON, rec2.Body.String())
	}

	// Case 3: Catch return JSON defaulting to status 500
	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest("GET", "/error-flow-default-500", nil)
	tenant.Serve(rec3, req3)

	t.Logf("Response code 3: %d, body: %q", rec3.Code, rec3.Body.String())
	if rec3.Code != 500 {
		t.Errorf("expected status 500 (defaulted), got %d", rec3.Code)
	}
	expectedJSON3 := `{"error":"unhandled_exception"}`
	if rec3.Body.String() != expectedJSON3 {
		t.Errorf("expected catch JSON return body %q, got %q", expectedJSON3, rec3.Body.String())
	}
}

func TestSafeDatabaseMethods(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kitwork-safe-db-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	tenantDir := filepath.Join(tmpDir, "test", "localhost")
	err = os.MkdirAll(tenantDir, 0755)
	if err != nil {
		t.Fatal(err)
	}

	appJsCode := `
import { router, database } from 'kitwork';
const db = database.connection();

router.get("/safe-test").handle((response) => {
	// 1. SafeList on non-existent table (should fail gracefully, not VM halt)
	const users = db.table("non_existent_table").SafeList();
	
	// 2. SafeFirst on non-existent table
	const firstVal = db.table("non_existent_table").SafeFirst();

	return response.json({
		users_is_error: users.isError,
		users_error_code: users.error.code,
		users_error_msg: users.error.message,
		first_is_error: firstVal.isError,
		first_error_code: firstVal.error.code
	});
});
`
	err = os.WriteFile(filepath.Join(tenantDir, "app.kitwork.js"), []byte(appJsCode), 0644)
	if err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(tmpDir, "localhost")
	err = tenant.Run()
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/safe-test", nil)
	tenant.Serve(rec, req)

	if rec.Code != 200 {
		t.Errorf("expected status 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	t.Logf("Response body: %s", body)
	if !strings.Contains(body, `"users_is_error":true`) {
		t.Errorf("expected users_is_error to be true, got body: %s", body)
	}
	if !strings.Contains(body, `"users_error_code":"DATABASE_ERROR"`) {
		t.Errorf("expected users_error_code DATABASE_ERROR, got body: %s", body)
	}
	if !strings.Contains(body, `"first_is_error":true`) {
		t.Errorf("expected first_is_error to be true, got body: %s", body)
	}
}

func TestFileFeature(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kitwork-test-file-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	tenantDir := filepath.Join(tmpDir, "test", "localhost")
	err = os.MkdirAll(tenantDir, 0755)
	if err != nil {
		t.Fatal(err)
	}

	// Write a file to read
	err = os.WriteFile(filepath.Join(tenantDir, "hello.txt"), []byte("hello from file"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	appJsCode := `
import { router, file } from 'kitwork';

router.get("/file-test").handle((response) => {
	const content = file.read("hello.txt");
	const base64 = file.base64("hello.txt");
	file.write("written.txt", "hello write");
	file.save("saved.txt", "data:text/plain;base64,aGVsbG8gc2F2ZQ==");
	return response.json({
		content: content,
		base64: base64,
		written: file.read("written.txt"),
		saved: file.read("saved.txt")
	});
});
`
	err = os.WriteFile(filepath.Join(tenantDir, "app.kitwork.js"), []byte(appJsCode), 0644)
	if err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(tmpDir, "localhost")
	err = tenant.Run()
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/file-test", nil)
	tenant.Serve(rec, req)

	if rec.Code != 200 {
		t.Errorf("expected status 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"content":"hello from file"`) {
		t.Errorf("expected content to be read, got body: %s", body)
	}
	if !strings.Contains(body, `"base64":"data:application/octet-stream;base64,aGVsbG8gZnJvbSBmaWxl"`) {
		t.Errorf("expected base64 to be encoded, got body: %s", body)
	}
	if !strings.Contains(body, `"written":"hello write"`) {
		t.Errorf("expected written to be hello write, got body: %s", body)
	}
	if !strings.Contains(body, `"saved":"hello save"`) {
		t.Errorf("expected saved to be hello save, got body: %s", body)
	}
}

func TestBrowserFeature(t *testing.T) {
	// Spin up a mock HTTP server
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`
			<!DOCTYPE html>
			<html>
			<head><title>Test Page</title></head>
			<body>
				<h1>Welcome to Kitwork</h1>
				<input type="text" id="username" value="" />
				<button id="btn" onclick="document.getElementById('username').value = 'button_clicked'; document.getElementById('msg').innerText = 'Action Completed';">Click Me</button>
				<div id="msg">Waiting</div>
			</body>
			</html>
		`))
	}))
	defer mockServer.Close()

	tmpDir, err := os.MkdirTemp("", "kitwork-browser-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	tenantDir := filepath.Join(tmpDir, "test", "localhost")
	err = os.MkdirAll(tenantDir, 0755)
	if err != nil {
		t.Fatal(err)
	}

	appJsCode := `
import { router, browser } from 'kitwork';

router.get("/browser-test").handle((response) => {
	const b = browser.launch({ width: 1024, height: 768 });
	
	// Test changing viewport dynamically
	b.viewport({ width: 800, height: 600 });
	
	// Test newPage with options, and goto with wait selector option
	b.newPage({ url: "` + mockServer.URL + `" });
	b.goto("` + mockServer.URL + `", { wait: "#username" });
	
	// Initial state checks
	const initTitle = b.evaluate("document.title");
	const initText = b.textContent("#msg");
	
	// Action: Fill input & Click button
	b.fill("#username", "hello_world");
	const filledVal = b.value("#username");
	
	b.click("#btn");
	
	// Wait for DOM update
	b.wait("#msg");
	const updatedText = b.textContent("#msg");
	const updatedVal = b.value("#username");
	const html = b.innerHTML("body");
	
	// Screenshot check (should return bytes / non-empty)
	const screenshot = b.screenshot();
	const screenshotLen = screenshot.length;
	
	b.close();

	return response.json({
		init_title: initTitle,
		init_text: initText,
		filled_val: filledVal,
		updated_text: updatedText,
		updated_val: updatedVal,
		html_contains: html.includes("Welcome to Kitwork"),
		screenshot_len: screenshotLen,
		error: b.err
	});
});
`

	err = os.WriteFile(filepath.Join(tenantDir, "app.kitwork.js"), []byte(appJsCode), 0644)
	if err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(tmpDir, "localhost")
	err = tenant.Run()
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/browser-test", nil)
	tenant.Serve(rec, req)

	if rec.Code != 200 {
		t.Errorf("expected status 200, got %d. Body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	t.Logf("Browser Response body: %s", body)

	// Validate results
	if !strings.Contains(body, `"init_title":"Test Page"`) {
		t.Errorf("expected init_title Test Page, got body: %s", body)
	}
	if !strings.Contains(body, `"init_text":"Waiting"`) {
		t.Errorf("expected init_text Waiting, got body: %s", body)
	}
	if !strings.Contains(body, `"filled_val":"hello_world"`) {
		t.Errorf("expected filled_val hello_world, got body: %s", body)
	}
	if !strings.Contains(body, `"updated_text":"Action Completed"`) {
		t.Errorf("expected updated_text Action Completed, got body: %s", body)
	}
	if !strings.Contains(body, `"updated_val":"button_clicked"`) {
		t.Errorf("expected updated_val button_clicked, got body: %s", body)
	}
	if !strings.Contains(body, `"html_contains":true`) {
		t.Errorf("expected html_contains true, got body: %s", body)
	}
	if strings.Contains(body, `"screenshot_len":0`) {
		t.Errorf("expected screenshot length > 0, got body: %s", body)
	}
}

func TestJWTFeature(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kitwork-jwt-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	tenantDir := filepath.Join(tmpDir, "test", "localhost")
	err = os.MkdirAll(tenantDir, 0755)
	if err != nil {
		t.Fatal(err)
	}

	appJsCode := `
import { router, jwt } from 'kitwork';

router.get("/jwt-test").handle((response) => {
	const secret = "my_super_secret_key";
	const payload = { userId: 42, role: "admin" };
	
	// 1. Sign
	const token = jwt.sign(payload, secret, { expiresIn: "1s" });
	
	// 2. Decode
	const decoded = jwt.decode(token);
	
	// 3. Verify success
	const verifySuccess = jwt.verify(token, secret);
	
	// 4. Verify invalid secret
	const verifyBadSecret = jwt.verify(token, "wrong_secret");
	
	return response.json({
		token_exists: token.length > 0,
		decoded_userId: decoded.userId,
		decoded_role: decoded.role,
		verify_valid: verifySuccess.valid,
		verify_userId: verifySuccess.payload.userId,
		verify_role: verifySuccess.payload.role,
		bad_secret_valid: verifyBadSecret.valid,
		bad_secret_err: verifyBadSecret.error,
		token: token
	});
});
`

	err = os.WriteFile(filepath.Join(tenantDir, "app.kitwork.js"), []byte(appJsCode), 0644)
	if err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(tmpDir, "localhost")
	err = tenant.Run()
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/jwt-test", nil)
	tenant.Serve(rec, req)

	if rec.Code != 200 {
		t.Errorf("expected status 200, got %d. Body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	t.Logf("JWT Response body: %s", body)

	// Validate results
	if !strings.Contains(body, `"token_exists":true`) {
		t.Errorf("expected token_exists true, got body: %s", body)
	}
	if !strings.Contains(body, `"decoded_userId":42`) {
		t.Errorf("expected decoded_userId 42, got body: %s", body)
	}
	if !strings.Contains(body, `"decoded_role":"admin"`) {
		t.Errorf("expected decoded_role admin, got body: %s", body)
	}
	if !strings.Contains(body, `"verify_valid":true`) {
		t.Errorf("expected verify_valid true, got body: %s", body)
	}
	if !strings.Contains(body, `"verify_userId":42`) {
		t.Errorf("expected verify_userId 42, got body: %s", body)
	}
	if !strings.Contains(body, `"verify_role":"admin"`) {
		t.Errorf("expected verify_role admin, got body: %s", body)
	}
	if !strings.Contains(body, `"bad_secret_valid":false`) {
		t.Errorf("expected bad_secret_valid false, got body: %s", body)
	}
	if !strings.Contains(body, `"bad_secret_err":"invalid signature"`) {
		t.Errorf("expected bad_secret_err invalid signature, got body: %s", body)
	}

	// Extract token to verify expiration on Go side
	var respData struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &respData); err != nil {
		t.Fatal(err)
	}

	// Test expiration on Go side: verify should fail after 1.5 seconds
	jwtObj := &JWT{tenant: tenant}
	
	// Verify immediately (should succeed)
	res1 := jwtObj.Verify(value.New(respData.Token), value.New("my_super_secret_key"))
	if resMap, ok := res1.V.(JWTVerifyResult); !ok || !resMap.Valid {
		t.Errorf("expected token to be valid immediately, got: %+v", res1.V)
	}

	// Wait for expiration
	time.Sleep(2200 * time.Millisecond)

	// Verify after expiration (should fail)
	res2 := jwtObj.Verify(value.New(respData.Token), value.New("my_super_secret_key"))
	if resMap, ok := res2.V.(JWTVerifyResult); !ok || resMap.Valid || resMap.Error != "token expired" {
		t.Errorf("expected token to be expired, got: %+v", res2.V)
	}
}

func TestFunctionKeywordFeature(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kitwork-func-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	tenantDir := filepath.Join(tmpDir, "test", "localhost")
	err = os.MkdirAll(tenantDir, 0755)
	if err != nil {
		t.Fatal(err)
	}

	appJsCode := `
import { router } from 'kitwork';

function add(a, b) {
	return a + b;
}

router.get("/func-test").handle((response) => {
	const mult = function(a, b) {
		return a * b;
	};
	const sum = add(5, 10);
	const prod = mult(3, 4);
	return response.json({
		sum: sum,
		prod: prod
	});
});
`

	err = os.WriteFile(filepath.Join(tenantDir, "app.kitwork.js"), []byte(appJsCode), 0644)
	if err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(tmpDir, "localhost")
	err = tenant.Run()
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/func-test", nil)
	tenant.Serve(rec, req)

	if rec.Code != 200 {
		t.Errorf("expected status 200, got %d. Body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	t.Logf("Function Response body: %s", body)

	// Validate results
	if !strings.Contains(body, `"sum":15`) {
		t.Errorf("expected sum 15, got body: %s", body)
	}
	if !strings.Contains(body, `"prod":12`) {
		t.Errorf("expected prod 12, got body: %s", body)
	}
}




