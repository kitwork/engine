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
	maximumCatalogTriggers             = 1024
	maximumTriggersPerStruct           = 32
	maximumTriggerDefinitionBytes      = 64 << 10
	catalogTriggerTag             byte = 'T'
)

// CatalogTrigger's dependency header is understood by the kernel. Its bounded
// action expressions are validated/executed by the standalone SQL frontend.
type CatalogTrigger struct {
	Version                    int
	ID, Name, Hash             string
	SourceStruct, TargetStruct string
	SourceFields, TargetFields []uint32
	Event                      string
	Definition                 []byte
}

func cloneCatalogTrigger(entry CatalogTrigger) CatalogTrigger {
	entry.Definition = bytes.Clone(entry.Definition)
	entry.SourceFields = append([]uint32(nil), entry.SourceFields...)
	entry.TargetFields = append([]uint32(nil), entry.TargetFields...)
	return entry
}

func isCatalogTriggerKey(key []byte) bool {
	return len(key) == 18 && key[0] == catalogNamespace && key[1] == catalogTriggerTag
}

func catalogTriggerKey(id string) ([]byte, error) {
	normalized, err := normalizeCatalogID(id)
	if err != nil {
		return nil, err
	}
	decoded, _ := hex.DecodeString(normalized)
	return append([]byte{catalogNamespace, catalogTriggerTag}, decoded...), nil
}

func decodeCatalogTrigger(definition []byte) (CatalogTrigger, error) {
	var entry CatalogTrigger
	if len(definition) == 0 || len(definition) > maximumTriggerDefinitionBytes || !utf8.Valid(definition) {
		return entry, fmt.Errorf("%w: trigger definition must be UTF-8, at most %d bytes", ErrInvalidCatalog, maximumTriggerDefinitionBytes)
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
		{"sourceStruct", &entry.SourceStruct}, {"targetStruct", &entry.TargetStruct},
		{"sourceFields", &entry.SourceFields}, {"targetFields", &entry.TargetFields}, {"event", &entry.Event},
	} {
		if err := decodeRequiredCatalogField(fields, field.name, field.target); err != nil {
			return entry, err
		}
	}
	for _, id := range []string{entry.ID, entry.SourceStruct, entry.TargetStruct} {
		if normalized, err := normalizeCatalogID(id); err != nil || normalized != id {
			return entry, fmt.Errorf("%w: invalid trigger identity", ErrInvalidCatalog)
		}
	}
	if entry.Version != 1 || strings.TrimSpace(entry.Name) == "" || len(entry.Name) > 128 || strings.ContainsRune(entry.Name, '\x00') ||
		entry.Hash == "" || len(entry.Hash) > maximumCatalogNameBytes || len(entry.SourceFields) > 64 || len(entry.TargetFields) < 1 || len(entry.TargetFields) > 32 {
		return entry, fmt.Errorf("%w: invalid trigger header", ErrInvalidCatalog)
	}
	switch entry.Event {
	case "insert", "update", "delete":
	default:
		return entry, fmt.Errorf("%w: invalid trigger event", ErrInvalidCatalog)
	}
	for _, tags := range [][]uint32{entry.SourceFields, entry.TargetFields} {
		seen := make(map[uint32]bool, len(tags))
		for _, tag := range tags {
			if tag == 0 || seen[tag] {
				return entry, fmt.Errorf("%w: invalid trigger field tags", ErrInvalidCatalog)
			}
			seen[tag] = true
		}
	}
	entry.Definition = bytes.Clone(definition)
	return entry, nil
}

func (state *catalogState) putTrigger(key, definition []byte) error {
	entry, err := decodeCatalogTrigger(definition)
	if err != nil {
		return err
	}
	if entry.ID != hex.EncodeToString(key[2:]) {
		return fmt.Errorf("%w: trigger ID does not match key", ErrInvalidCatalog)
	}
	nameKey := entry.SourceStruct + ":" + entry.Name
	if id, found := state.triggerNames[nameKey]; found && id != entry.ID {
		return fmt.Errorf("%w: duplicate trigger name %q", ErrInvalidCatalog, entry.Name)
	}
	old, exists := state.triggers[entry.ID]
	if exists && !bytes.Equal(old.Definition, definition) {
		return fmt.Errorf("%w: trigger definitions are immutable; drop before recreating", ErrInvalidCatalog)
	}
	if !exists {
		if len(state.triggers) >= maximumCatalogTriggers {
			return fmt.Errorf("%w: catalog exceeds %d triggers", ErrInvalidCatalog, maximumCatalogTriggers)
		}
		count := 0
		for _, trigger := range state.triggers {
			if trigger.SourceStruct == entry.SourceStruct {
				count++
			}
		}
		if count >= maximumTriggersPerStruct {
			return fmt.Errorf("%w: table exceeds %d triggers", ErrInvalidCatalog, maximumTriggersPerStruct)
		}
	}
	size := state.bytes + len(entry.Definition) - len(old.Definition)
	if size > maximumCatalogBytes {
		return fmt.Errorf("%w: catalog exceeds byte budget", ErrInvalidCatalog)
	}
	state.triggers[entry.ID], state.triggerNames[nameKey], state.bytes = entry, entry.ID, size
	return nil
}

func (tx *Tx) DefineTrigger(definition []byte) error {
	entry, err := decodeCatalogTrigger(definition)
	if err != nil {
		return err
	}
	key, err := catalogTriggerKey(entry.ID)
	if err != nil {
		return err
	}
	return tx.add(operation{kind: operationPut, key: key, value: entry.Definition})
}

func (tx *Tx) DeleteTrigger(id string) error {
	key, err := catalogTriggerKey(id)
	if err != nil {
		return err
	}
	return tx.Delete(key)
}

func (state catalogState) validateTriggerReferences() error {
	if len(state.triggers) == 0 {
		return nil
	}
	tagsByStruct := make(map[string]map[uint32]bool)
	checkFields := func(id string, tags []uint32) error {
		fields := tagsByStruct[id]
		if fields == nil {
			entry, exists := state.byID[id]
			if !exists {
				return fmt.Errorf("%w: trigger references missing table %s", ErrInvalidCatalog, id)
			}
			var schema struct{ Fields []struct{ Tag uint32 } }
			if err := json.Unmarshal(entry.Definition, &schema); err != nil {
				return err
			}
			fields = make(map[uint32]bool, len(schema.Fields))
			for _, field := range schema.Fields {
				fields[field.Tag] = true
			}
			tagsByStruct[id] = fields
		}
		for _, tag := range tags {
			if !fields[tag] {
				return fmt.Errorf("%w: trigger references missing field tag %d on %s", ErrInvalidCatalog, tag, id)
			}
		}
		return nil
	}
	edges := make(map[string][]string)
	for _, trigger := range state.triggers {
		if err := checkFields(trigger.SourceStruct, trigger.SourceFields); err != nil {
			return err
		}
		if err := checkFields(trigger.TargetStruct, trigger.TargetFields); err != nil {
			return err
		}
		edges[trigger.SourceStruct] = append(edges[trigger.SourceStruct], trigger.TargetStruct)
	}
	// Deliberately conservative: all table-action cycles are rejected, even
	// where an event or WHEN guard might prevent that cycle at runtime.
	marks := make(map[string]uint8)
	var visit func(string) error
	visit = func(id string) error {
		if marks[id] == 1 {
			return fmt.Errorf("%w: cyclic trigger table dependency", ErrInvalidCatalog)
		}
		if marks[id] == 2 {
			return nil
		}
		marks[id] = 1
		for _, target := range edges[id] {
			if err := visit(target); err != nil {
				return err
			}
		}
		marks[id] = 2
		return nil
	}
	for id := range edges {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}
