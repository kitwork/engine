package postgres

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
)

// ShoppingSearchProjectionIdentity describes the exact on-disk identity used
// by Kitwork for one Shopping search projection. Keeping this derivation in
// migration tooling lets an offline rebuild be verified before it is adopted
// by the live tenant.
type ShoppingSearchProjectionIdentity struct {
	TenantRoot     string `json:"tenant_root"`
	StorageName    string `json:"storage_name"`
	Table          string `json:"table"`
	SchemaHash     string `json:"schema_fingerprint"`
	IndexKey       string `json:"index_key"`
	ManagerRoot    string `json:"manager_root"`
	IndexDirectory string `json:"index_directory"`
	SignaturePath  string `json:"signature_path"`
}

// ResolveShoppingSearchProjectionIdentity mirrors Kitwork's tenant-scoped
// search identity without opening either the database or search index.
func ResolveShoppingSearchProjectionIdentity(
	tenantRoot string,
	storageName string,
	table string,
) (ShoppingSearchProjectionIdentity, error) {
	tenantRoot = strings.TrimSpace(tenantRoot)
	storageName = strings.TrimSpace(storageName)
	table = strings.TrimSpace(table)
	if tenantRoot == "" || storageName == "" || table == "" {
		return ShoppingSearchProjectionIdentity{}, fmt.Errorf(
			"shopping search identity: tenant root, storage name and table are required",
		)
	}
	absolute, err := filepath.Abs(tenantRoot)
	if err != nil {
		return ShoppingSearchProjectionIdentity{}, fmt.Errorf("shopping search identity: resolve tenant root: %w", err)
	}
	absolute = filepath.Clean(absolute)
	schema, err := ShoppingSearchSchema()
	if err != nil {
		return ShoppingSearchProjectionIdentity{}, err
	}
	fingerprint := schema.Fingerprint()
	schemaHash := hex.EncodeToString(fingerprint[:])
	scope := absolute + "|kitdb|" + storageName + "|" + table
	// Shopping's live schema uses (merchant, id) as a composite primary key.
	// Runtime search includes the stable field identities in its index scope so
	// a primary-key layout change cannot accidentally hydrate an older index.
	structID := shoppingSearchStableSchemaID("struct", table)
	merchantID := shoppingSearchStableSchemaID("field", structID+":merchant")
	productID := shoppingSearchStableSchemaID("field", structID+":id")
	scope += "|" + schemaHash + "|composite-primary:" + merchantID + "," + productID
	identityHash := sha256.Sum256([]byte(scope))
	indexKey := "db-" + hex.EncodeToString(identityHash[:])
	directoryHash := sha256.Sum256([]byte(indexKey))
	directoryName := hex.EncodeToString(directoryHash[:])
	managerRoot := filepath.Join(absolute, ".data", "search")
	return ShoppingSearchProjectionIdentity{
		TenantRoot: absolute, StorageName: storageName, Table: table,
		SchemaHash: schemaHash, IndexKey: indexKey, ManagerRoot: managerRoot,
		IndexDirectory: filepath.Join(managerRoot, directoryName[:2], directoryName),
		SignaturePath:  filepath.Join(absolute, ".data", "search-state", indexKey+".signature"),
	}, nil
}

func shoppingSearchStableSchemaID(kind, name string) string {
	digest := sha256.Sum256([]byte("kitwork:schema:v1:" + kind + ":" + name))
	return hex.EncodeToString(digest[:16])
}

func managedSearchIndexDirectory(root string, key string) (string, error) {
	root = strings.TrimSpace(root)
	key = strings.TrimSpace(key)
	if root == "" || key == "" {
		return "", fmt.Errorf("shopping search identity: search root and index key are required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("shopping search identity: resolve search root: %w", err)
	}
	digest := sha256.Sum256([]byte(key))
	name := hex.EncodeToString(digest[:])
	return filepath.Join(filepath.Clean(absolute), name[:2], name), nil
}
