package work

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDataHelpersCapability(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kitwork-data-helpers-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	tenantDir := filepath.Join(tmpDir, "test", "localhost")
	if err := os.MkdirAll(tenantDir, 0755); err != nil {
		t.Fatal(err)
	}

	script := `
import { log } from "kitwork";

// 1. Test Chunk
const numbers = [1, 2, 3, 4, 5];
const chunks = numbers.chunk(2);
log.Print("CHUNKS_LEN: " + chunks.length);
log.Print("CHUNK_0_LEN: " + chunks[0].length);
if (chunks.length != 3) fail("chunk length failed");
if (chunks[0][0] != 1 || chunks[0][1] != 2) fail("chunk content failed");
if (chunks[2][0] != 5) fail("last chunk content failed");

// 2. Test Unique with callback
const dupUsers = [
    { id: 1, name: "Alice" },
    { id: 2, name: "Bob" },
    { id: 1, name: "Alice Dup" }
];
const uniqueUsers = dupUsers.unique(u => u.id);
log.Print("UNIQUE_LEN: " + uniqueUsers.length);
if (uniqueUsers.length != 2) fail("unique key callback failed");
if (uniqueUsers[1].name != "Bob") fail("unique content mismatch");

// 3. Test GroupBy
const people = [
    { name: "Alice", country: "US" },
    { name: "Bob", country: "VN" },
    { name: "Charlie", country: "US" }
];
const grouped = people.groupBy(p => p.country);
log.Print("GROUPED_US_LEN: " + grouped.US.length);
log.Print("GROUPED_VN_LEN: " + grouped.VN.length);
if (grouped.US.length != 2) fail("group US length failed");
if (grouped.VN[0].name != "Bob") fail("group VN content failed");

// 4. Test SortBy
const unsorted = [
    { name: "Alice", age: 30 },
    { name: "Bob", age: 20 },
    { name: "Charlie", age: 25 }
];
const sorted = unsorted.sortBy(p => p.age);
log.Print("SORTED_0_NAME: " + sorted[0].name);
log.Print("SORTED_1_NAME: " + sorted[1].name);
if (sorted[0].name != "Bob" || sorted[2].name != "Alice") fail("sortBy age failed");

// 5. Test Pick & Omit
const obj = { id: 101, name: "Product A", price: 50, secret: "xxx" };
const picked = obj.pick("id", "price");
log.Print("PICKED_ID: " + picked.id + " PICKED_PRICE: " + picked.price + " PICKED_SECRET: " + picked.secret);
if (picked.id != 101 || picked.price != 50 || picked.secret != null) fail("pick failed");

const omitted = obj.omit(["secret", "price"]);
log.Print("OMITTED_NAME: " + omitted.name + " OMITTED_SECRET: " + omitted.secret);
if (omitted.name != "Product A" || omitted.secret != null || omitted.price != null) fail("omit failed");
`
	if err := os.WriteFile(filepath.Join(tenantDir, "app.kitwork.js"), []byte(script), 0644); err != nil {
		t.Fatal(err)
	}

	if err := NewTenant(tmpDir, "localhost").Run(); err != nil {
		t.Fatalf("data helpers E2E test failed at runtime: %v", err)
	}
}
