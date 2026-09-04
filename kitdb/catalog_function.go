package kitdb

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	maximumCatalogFunctions             = 1024
	maximumFunctionDefinitionBytes      = 64 << 10
	catalogFunctionTag             byte = 'F'
)

// CatalogFunction is a versioned SQL function definition, not a table. Its
// bytes share the catalog revision, WAL and backup boundary with schemas.
type CatalogFunction struct {
	Version    int
	ID         string
	Name       string
	Hash       string
	Definition []byte
}

func isCatalogFunctionKey(key []byte) bool {
	return len(key) == 18 && key[0] == catalogNamespace && key[1] == catalogFunctionTag
}

func catalogFunctionKey(id string) ([]byte, error) {
	normalized, err := normalizeCatalogID(id)
	if err != nil {
		return nil, err
	}
	decoded, _ := hex.DecodeString(normalized)
	return append([]byte{catalogNamespace, catalogFunctionTag}, decoded...), nil
}

func decodeCatalogFunction(definition []byte) (CatalogFunction, error) {
	var entry CatalogFunction
	if len(definition) == 0 || len(definition) > maximumFunctionDefinitionBytes || !utf8.Valid(definition) {
		return entry, fmt.Errorf("%w: function definition must be valid UTF-8, at most %d bytes", ErrInvalidCatalog, maximumFunctionDefinitionBytes)
	}
	fields, err := decodeCatalogObject(definition)
	if err != nil {
		return entry, err
	}
	for _, field := range []struct {
		name   string
		target any
	}{
		{"version", &entry.Version}, {"id", &entry.ID}, {"name", &entry.Name}, {"hash", &entry.Hash},
	} {
		if err := decodeRequiredCatalogField(fields, field.name, field.target); err != nil {
			return entry, err
		}
	}
	entry.ID, err = normalizeCatalogID(entry.ID)
	if err != nil {
		return entry, err
	}
	if entry.Version != 1 || strings.TrimSpace(entry.Name) == "" || len(entry.Name) > maximumCatalogNameBytes ||
		strings.ContainsRune(entry.Name, '\x00') || entry.Hash == "" || len(entry.Hash) > maximumCatalogNameBytes {
		return entry, fmt.Errorf("%w: invalid function header", ErrInvalidCatalog)
	}
	entry.Definition = bytes.Clone(definition)
	return entry, nil
}

func (state *catalogState) putFunction(key, definition []byte) error {
	entry, err := decodeCatalogFunction(definition)
	if err != nil {
		return err
	}
	if entry.ID != hex.EncodeToString(key[2:]) {
		return fmt.Errorf("%w: function ID does not match catalog key", ErrInvalidCatalog)
	}
	if id, found := state.functionNames[entry.Name]; found && id != entry.ID {
		return fmt.Errorf("%w: duplicate function name %q", ErrInvalidCatalog, entry.Name)
	}
	old, exists := state.functions[entry.ID]
	if !exists && len(state.functions) >= maximumCatalogFunctions {
		return fmt.Errorf("%w: catalog exceeds %d functions", ErrInvalidCatalog, maximumCatalogFunctions)
	}
	size := state.bytes + len(entry.Definition) - len(old.Definition)
	if size > maximumCatalogBytes {
		return fmt.Errorf("%w: catalog exceeds byte budget", ErrInvalidCatalog)
	}
	if exists {
		delete(state.functionNames, old.Name)
	}
	state.functions[entry.ID] = entry
	state.functionNames[entry.Name] = entry.ID
	state.bytes = size
	return nil
}

func (tx *Tx) DefineFunction(definition []byte) error {
	entry, err := decodeCatalogFunction(definition)
	if err != nil {
		return err
	}
	key, err := catalogFunctionKey(entry.ID)
	if err != nil {
		return err
	}
	return tx.add(operation{kind: operationPut, key: key, value: entry.Definition})
}

func (tx *Tx) DeleteFunction(id string) error {
	key, err := catalogFunctionKey(id)
	if err != nil {
		return err
	}
	return tx.Delete(key)
}
