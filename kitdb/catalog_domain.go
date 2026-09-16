package kitdb

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	maximumCatalogDomains             = 1024
	maximumDomainDefinitionBytes      = 64 << 10
	catalogDomainTag             byte = 'D'
)

// CatalogDomain shares the database catalog, WAL and recovery boundary.
// The SQL frontend owns the versioned constraint definition.
type CatalogDomain struct {
	Version        int
	ID, Name, Hash string
	Definition     []byte
}

func isCatalogDomainKey(key []byte) bool {
	return len(key) == 18 && key[0] == catalogNamespace && key[1] == catalogDomainTag
}

func catalogDomainKey(id string) ([]byte, error) {
	normalized, err := normalizeCatalogID(id)
	if err != nil {
		return nil, err
	}
	decoded, _ := hex.DecodeString(normalized)
	return append([]byte{catalogNamespace, catalogDomainTag}, decoded...), nil
}

func decodeCatalogDomain(definition []byte) (CatalogDomain, error) {
	var entry CatalogDomain
	if len(definition) == 0 || len(definition) > maximumDomainDefinitionBytes || !utf8.Valid(definition) {
		return entry, fmt.Errorf("%w: domain definition must be valid UTF-8, at most %d bytes", ErrInvalidCatalog, maximumDomainDefinitionBytes)
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
		return entry, fmt.Errorf("%w: invalid domain header", ErrInvalidCatalog)
	}
	entry.Definition = bytes.Clone(definition)
	return entry, nil
}

func (state *catalogState) putDomain(key, definition []byte) error {
	entry, err := decodeCatalogDomain(definition)
	if err != nil {
		return err
	}
	if entry.ID != hex.EncodeToString(key[2:]) {
		return fmt.Errorf("%w: domain ID does not match key", ErrInvalidCatalog)
	}
	if id, found := state.domainNames[entry.Name]; found && id != entry.ID {
		return fmt.Errorf("%w: duplicate domain name %q", ErrInvalidCatalog, entry.Name)
	}
	old, exists := state.domains[entry.ID]
	if exists && !bytes.Equal(old.Definition, definition) {
		return fmt.Errorf("%w: domain definitions are immutable; drop unused domain before recreating", ErrInvalidCatalog)
	}
	if !exists && len(state.domains) >= maximumCatalogDomains {
		return fmt.Errorf("%w: catalog exceeds %d domains", ErrInvalidCatalog, maximumCatalogDomains)
	}
	size := state.bytes + len(entry.Definition) - len(old.Definition)
	if size > maximumCatalogBytes {
		return fmt.Errorf("%w: catalog exceeds byte budget", ErrInvalidCatalog)
	}
	state.domains[entry.ID], state.domainNames[entry.Name], state.bytes = entry, entry.ID, size
	return nil
}

func (tx *Tx) DefineDomain(definition []byte) error {
	entry, err := decodeCatalogDomain(definition)
	if err != nil {
		return err
	}
	key, err := catalogDomainKey(entry.ID)
	if err != nil {
		return err
	}
	return tx.add(operation{kind: operationPut, key: key, value: entry.Definition})
}

func (tx *Tx) DeleteDomain(id string) error {
	key, err := catalogDomainKey(id)
	if err != nil {
		return err
	}
	return tx.Delete(key)
}

// Validate the final graph, not operation order. A second frontend cannot
// delete/replace a domain still referenced by a table's frozen constraints.
func (state catalogState) validateDomainReferences() error {
	for _, entry := range state.byID {
		var schema struct {
			Fields []struct {
				Name   string
				Domain *struct{ ID, Name, Hash string }
			}
		}
		if err := json.Unmarshal(entry.Definition, &schema); err != nil {
			return err
		}
		for _, field := range schema.Fields {
			if field.Domain == nil {
				continue
			}
			ref := field.Domain
			domain, found := state.domains[ref.ID]
			if entry.Version < 9 || !found || domain.Name != ref.Name || domain.Hash != ref.Hash {
				return fmt.Errorf("%w: field %q on %q references a missing or changed domain %q", ErrInvalidCatalog, field.Name, entry.Name, ref.Name)
			}
		}
	}
	return nil
}
