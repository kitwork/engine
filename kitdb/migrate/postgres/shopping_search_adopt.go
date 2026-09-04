package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/search"
)

type ShoppingSearchAdoptConfig struct {
	SourceRoot        string
	SourceIndexKey    string
	TenantRoot        string
	StorageName       string
	Table             string
	ExpectedDocuments uint64
	SkipVerify        bool
}

type ShoppingSearchAdoptReport struct {
	Identity        ShoppingSearchProjectionIdentity `json:"identity"`
	SourceDirectory string                           `json:"source_directory"`
	DatabasePath    string                           `json:"database_path"`
	Index           search.IndexInfo                 `json:"index"`
	Transaction     uint64                           `json:"transaction"`
	CatalogRevision string                           `json:"catalog_revision"`
	Signature       string                           `json:"signature"`
	Verified        bool                             `json:"verified"`
	Adopted         bool                             `json:"adopted"`
}

type shoppingSearchCatalogDefinition struct {
	Fields []struct {
		Name         string `json:"name"`
		Searchable   bool   `json:"searchable"`
		SearchWeight int    `json:"searchWeight"`
	} `json:"fields"`
}

const shoppingSearchAdoptionSignaturePrefix = "id:text-integer-v1|"

// AdoptShoppingSearchProjection verifies and atomically attaches one offline
// Shopping index to its tenant-scoped runtime identity. The target KitDB must
// be offline so its transaction/catalog boundary cannot change during adopt.
func AdoptShoppingSearchProjection(
	ctx context.Context,
	config ShoppingSearchAdoptConfig,
) (_ ShoppingSearchAdoptReport, returnErr error) {
	if ctx == nil {
		return ShoppingSearchAdoptReport{}, fmt.Errorf("shopping search adopt: context is nil")
	}
	config.SourceRoot = strings.TrimSpace(config.SourceRoot)
	config.SourceIndexKey = strings.TrimSpace(config.SourceIndexKey)
	config.StorageName = strings.TrimSpace(config.StorageName)
	config.Table = strings.TrimSpace(config.Table)
	if config.Table == "" {
		config.Table = "shopping"
	}
	if config.SourceRoot == "" || config.SourceIndexKey == "" ||
		config.TenantRoot == "" || config.StorageName == "" || config.ExpectedDocuments == 0 {
		return ShoppingSearchAdoptReport{}, fmt.Errorf(
			"shopping search adopt: source root, source index key, tenant root, storage name and expected documents are required",
		)
	}
	if filepath.Base(config.StorageName) != config.StorageName {
		return ShoppingSearchAdoptReport{}, fmt.Errorf("shopping search adopt: storage name must be a filename")
	}
	identity, err := ResolveShoppingSearchProjectionIdentity(
		config.TenantRoot, config.StorageName, config.Table,
	)
	if err != nil {
		return ShoppingSearchAdoptReport{}, err
	}
	sourceDirectory, err := managedSearchIndexDirectory(config.SourceRoot, config.SourceIndexKey)
	if err != nil {
		return ShoppingSearchAdoptReport{}, err
	}
	if filepath.Clean(sourceDirectory) == filepath.Clean(identity.IndexDirectory) {
		return ShoppingSearchAdoptReport{}, fmt.Errorf("shopping search adopt: source already has the live identity")
	}
	schema, err := ShoppingSearchSchema()
	if err != nil {
		return ShoppingSearchAdoptReport{}, err
	}
	index, err := search.OpenIndex(sourceDirectory, schema)
	if err != nil {
		return ShoppingSearchAdoptReport{}, fmt.Errorf("shopping search adopt: open source index: %w", err)
	}
	info := index.Info()
	if info.Documents != config.ExpectedDocuments || info.Deleted != 0 ||
		info.PhysicalDocuments != info.Documents {
		_ = index.Close()
		return ShoppingSearchAdoptReport{}, fmt.Errorf(
			"shopping search adopt: source index boundary is documents=%d physical=%d deleted=%d, expected %d live documents",
			info.Documents, info.PhysicalDocuments, info.Deleted, config.ExpectedDocuments,
		)
	}
	verified := false
	if !config.SkipVerify {
		if err := index.Verify(ctx); err != nil {
			_ = index.Close()
			return ShoppingSearchAdoptReport{}, fmt.Errorf("shopping search adopt: verify source index: %w", err)
		}
		verified = true
	}
	if err := index.Close(); err != nil {
		return ShoppingSearchAdoptReport{}, fmt.Errorf("shopping search adopt: close source index: %w", err)
	}

	databasePath := filepath.Join(identity.TenantRoot, ".data", config.StorageName)
	database, err := kitdb.OpenWithOptions(databasePath, kitdb.OpenOptions{PageCacheBytes: -1})
	if err != nil {
		return ShoppingSearchAdoptReport{}, fmt.Errorf(
			"shopping search adopt: open offline target (stop its server first): %w", err,
		)
	}
	defer func() { returnErr = errors.Join(returnErr, database.Close()) }()
	catalog, found, err := database.CatalogStructByName(config.Table)
	if err != nil {
		return ShoppingSearchAdoptReport{}, err
	}
	if !found {
		return ShoppingSearchAdoptReport{}, fmt.Errorf("shopping search adopt: target catalog has no struct %q", config.Table)
	}
	if err := validateShoppingSearchCatalog(catalog.Definition); err != nil {
		return ShoppingSearchAdoptReport{}, err
	}
	lastTransaction, err := database.LastTransaction()
	if err != nil {
		return ShoppingSearchAdoptReport{}, err
	}
	catalogVersion, err := database.CatalogVersion()
	if err != nil {
		return ShoppingSearchAdoptReport{}, err
	}
	if catalogVersion.Transaction != lastTransaction || catalogVersion.Revision == "" {
		return ShoppingSearchAdoptReport{}, fmt.Errorf("shopping search adopt: target catalog boundary is inconsistent")
	}
	signature := fmt.Sprintf("%d:%s", lastTransaction, catalogVersion.Revision)
	if sourceSignature, found, err := readShoppingSearchSourceSignature(
		config.SourceRoot, config.SourceIndexKey,
	); err != nil {
		return ShoppingSearchAdoptReport{}, err
	} else if found {
		// Preserve the content boundary that originally published this exact
		// index. The live runtime can then prove row freshness independently of
		// later catalog/migration transactions before upgrading to a watermark.
		signature = sourceSignature
	}
	signature = shoppingSearchAdoptionSignaturePrefix + signature

	if _, err := os.Stat(identity.IndexDirectory); err == nil {
		return ShoppingSearchAdoptReport{}, fmt.Errorf("shopping search adopt: destination index already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return ShoppingSearchAdoptReport{}, err
	}
	if _, err := os.Stat(identity.SignaturePath); err == nil {
		return ShoppingSearchAdoptReport{}, fmt.Errorf("shopping search adopt: destination signature already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return ShoppingSearchAdoptReport{}, err
	}
	if err := os.MkdirAll(filepath.Dir(identity.IndexDirectory), 0o700); err != nil {
		return ShoppingSearchAdoptReport{}, err
	}
	if err := os.Rename(sourceDirectory, identity.IndexDirectory); err != nil {
		return ShoppingSearchAdoptReport{}, fmt.Errorf("shopping search adopt: publish index directory: %w", err)
	}
	if err := writeShoppingSearchSignature(identity.SignaturePath, signature); err != nil {
		rollbackErr := os.Rename(identity.IndexDirectory, sourceDirectory)
		return ShoppingSearchAdoptReport{}, errors.Join(
			fmt.Errorf("shopping search adopt: publish signature: %w", err), rollbackErr,
		)
	}
	return ShoppingSearchAdoptReport{
		Identity: identity, SourceDirectory: sourceDirectory, DatabasePath: databasePath,
		Index: info, Transaction: lastTransaction, CatalogRevision: catalogVersion.Revision,
		Signature: signature, Verified: verified, Adopted: true,
	}, nil
}

func readShoppingSearchSourceSignature(root, indexKey string) (string, bool, error) {
	path := filepath.Join(filepath.Dir(filepath.Clean(root)), "search-state", indexKey+".signature")
	encoded, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if len(encoded) == 0 || len(encoded) > 512 || strings.TrimSpace(string(encoded)) != string(encoded) {
		return "", false, fmt.Errorf("shopping search adopt: source signature is invalid")
	}
	return string(encoded), true, nil
}

func validateShoppingSearchCatalog(encoded []byte) error {
	var definition shoppingSearchCatalogDefinition
	if err := json.Unmarshal(encoded, &definition); err != nil {
		return fmt.Errorf("shopping search adopt: decode target catalog: %w", err)
	}
	schema, err := ShoppingSearchSchema()
	if err != nil {
		return err
	}
	fields := schema.Fields()
	position := 0
	for _, field := range definition.Fields {
		if !field.Searchable {
			continue
		}
		if position >= len(fields) {
			return fmt.Errorf("shopping search adopt: target catalog has extra searchable field %q", field.Name)
		}
		weight := field.SearchWeight
		if weight <= 0 {
			weight = 1
		}
		expected := fields[position]
		if field.Name != expected.Name || float64(weight) != expected.Boost {
			return fmt.Errorf(
				"shopping search adopt: searchable field %d is %s/%d, expected %s/%g",
				position, field.Name, weight, expected.Name, expected.Boost,
			)
		}
		position++
	}
	if position != len(fields) {
		return fmt.Errorf("shopping search adopt: target catalog has %d searchable fields, expected %d", position, len(fields))
	}
	return nil
}

func writeShoppingSearchSignature(path string, signature string) (returnErr error) {
	if signature == "" {
		return fmt.Errorf("shopping search adopt: signature is empty")
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".search-signature-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		if returnErr != nil {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temporary.WriteString(signature); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return nil
}
