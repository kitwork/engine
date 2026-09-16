package kitdb

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	catalogNamespace              byte = 0x01
	maximumCatalogStructs              = 16_384
	maximumCatalogBytes                = 128 << 20
	maximumCatalogDefinitionBytes      = 16 << 20
	maximumCatalogNameBytes            = 1 << 10
	maximumCatalogFields               = 65_535
)

// CatalogStruct is one caller-owned schema definition loaded from KitDB's
// reserved catalog keyspace. Definition is the complete versioned definition
// document so SQL, Kitwork and future frontends can share one durable source.
type CatalogStruct struct {
	Version    int
	ID         string
	Name       string
	Hash       string
	Definition []byte
}

// CatalogSnapshot is an immutable point-in-time view of the schema catalog.
// Revision changes only when catalog bytes change. Transaction identifies the
// database boundary at which this snapshot was observed.
type CatalogSnapshot struct {
	Transaction uint64
	Revision    string
	Structs     []CatalogStruct
	Functions   []CatalogFunction
	Domains     []CatalogDomain
	Triggers    []CatalogTrigger
	Sequences   []Sequence
}

// CatalogVersion is the allocation-light identity of the active catalog.
// Transaction is the database boundary at observation time; Revision changes
// only when catalog bytes change, so record-only commits retain the revision.
type CatalogVersion struct {
	Transaction uint64
	Revision    string
}

type catalogState struct {
	byID          map[string]CatalogStruct
	byName        map[string]string
	bytes         int
	revision      string
	functions     map[string]CatalogFunction
	functionNames map[string]string
	domains       map[string]CatalogDomain
	domainNames   map[string]string
	triggers      map[string]CatalogTrigger
	triggerNames  map[string]string
	sequences     map[string]Sequence
	sequenceNames map[string]string
}

func newCatalogState() catalogState {
	state := catalogState{
		byID:          make(map[string]CatalogStruct),
		byName:        make(map[string]string),
		functions:     make(map[string]CatalogFunction),
		functionNames: make(map[string]string),
		domains:       make(map[string]CatalogDomain),
		domainNames:   make(map[string]string),
		triggers:      make(map[string]CatalogTrigger),
		triggerNames:  make(map[string]string),
		sequences:     make(map[string]Sequence),
		sequenceNames: make(map[string]string),
	}
	state.refreshRevision()
	return state
}

func loadCatalogState(main *mainImage, overlay map[string]rowMutation) (catalogState, error) {
	state := newCatalogState()
	iterator := newLogicalRowIterator(main, overlay, []byte{catalogNamespace})
	for {
		key, definition, found, err := iterator.next()
		if err != nil {
			return catalogState{}, errors.Join(ErrCorrupt, fmt.Errorf("kitdb: load catalog: %w", err))
		}
		if !found || len(key) == 0 || key[0] != catalogNamespace {
			break
		}
		if err := state.put(key, definition); err != nil {
			return catalogState{}, errors.Join(ErrCorrupt, fmt.Errorf("kitdb: load catalog: %w", err))
		}
	}
	if err := state.validateSequenceReferences(); err != nil {
		return catalogState{}, errors.Join(ErrCorrupt, err)
	}
	if err := state.validateDomainReferences(); err != nil {
		return catalogState{}, errors.Join(ErrCorrupt, err)
	}
	if err := state.validateTriggerReferences(); err != nil {
		return catalogState{}, errors.Join(ErrCorrupt, err)
	}
	state.refreshRevision()
	return state, nil
}

func (db *DB) ensureCatalogLoaded() error {
	return db.ensureCatalogLoadedForState(false)
}

func (db *DB) ensureCatalogLoadedForCommit() error {
	return db.ensureCatalogLoadedForState(true)
}

func (db *DB) ensureCatalogLoadedForState(admittedCommit bool) error {
	db.catalogLoadMu.Lock()
	defer db.catalogLoadMu.Unlock()

	db.mu.RLock()
	if db.catalogLoaded {
		err := db.catalogErr
		db.mu.RUnlock()
		return err
	}
	if err := db.catalogLoadStateErrorLocked(admittedCommit); err != nil {
		db.mu.RUnlock()
		return err
	}
	db.mu.RUnlock()

	// Checkpoints and commits both own commitMu while publishing a new visible
	// storage boundary. Holding it gives the catalog loader one coherent view.
	db.commitMu.Lock()
	defer db.commitMu.Unlock()
	db.mu.RLock()
	if db.catalogLoaded {
		err := db.catalogErr
		db.mu.RUnlock()
		return err
	}
	if err := db.catalogLoadStateErrorLocked(admittedCommit); err != nil {
		db.mu.RUnlock()
		return err
	}
	state, err := loadCatalogState(db.main, db.overlay)
	db.mu.RUnlock()

	db.mu.Lock()
	if err != nil {
		db.catalogErr = err
	} else {
		db.catalog = state
	}
	db.catalogLoaded = true
	db.mu.Unlock()
	return err
}

func (db *DB) catalogLoadStateErrorLocked(admittedCommit bool) error {
	if admittedCommit {
		return db.commitStateErrorLocked()
	}
	return db.stateErrorLocked()
}

// Catalog returns definitions sorted by name and detached from engine memory.
func (db *DB) Catalog() (CatalogSnapshot, error) {
	if err := db.ensureCatalogLoaded(); err != nil {
		return CatalogSnapshot{}, err
	}
	db.mu.RLock()
	defer db.mu.RUnlock()
	if err := db.stateErrorLocked(); err != nil {
		return CatalogSnapshot{}, err
	}
	return db.catalog.snapshot(db.lastTx), nil
}

// CatalogVersion returns the current catalog identity without cloning schema
// definitions. After lazy catalog hydration it performs no filesystem I/O.
func (db *DB) CatalogVersion() (CatalogVersion, error) {
	if err := db.ensureCatalogLoaded(); err != nil {
		return CatalogVersion{}, err
	}
	db.mu.RLock()
	defer db.mu.RUnlock()
	if err := db.stateErrorLocked(); err != nil {
		return CatalogVersion{}, err
	}
	return CatalogVersion{Transaction: db.lastTx, Revision: db.catalog.revision}, nil
}

// CatalogStructByID returns one catalog definition by its stable 16-byte ID.
func (db *DB) CatalogStructByID(id string) (CatalogStruct, bool, error) {
	normalized, err := normalizeCatalogID(id)
	if err != nil {
		return CatalogStruct{}, false, err
	}
	if err := db.ensureCatalogLoaded(); err != nil {
		return CatalogStruct{}, false, err
	}
	db.mu.RLock()
	defer db.mu.RUnlock()
	if err := db.stateErrorLocked(); err != nil {
		return CatalogStruct{}, false, err
	}
	entry, found := db.catalog.byID[normalized]
	if !found {
		return CatalogStruct{}, false, nil
	}
	return cloneCatalogStruct(entry), true, nil
}

// CatalogStructHashByID returns only the active definition hash. Record-layer
// write admission uses this allocation-light lookup to reject a stale schema
// without cloning a potentially large definition document on every commit.
func (db *DB) CatalogStructHashByID(id string) (string, bool, error) {
	normalized, err := normalizeCatalogID(id)
	if err != nil {
		return "", false, err
	}
	if err := db.ensureCatalogLoaded(); err != nil {
		return "", false, err
	}
	db.mu.RLock()
	defer db.mu.RUnlock()
	if err := db.stateErrorLocked(); err != nil {
		return "", false, err
	}
	entry, found := db.catalog.byID[normalized]
	if !found {
		return "", false, nil
	}
	return entry.Hash, true, nil
}

// CatalogStructByName performs an exact catalog-name lookup.
func (db *DB) CatalogStructByName(name string) (CatalogStruct, bool, error) {
	if err := db.ensureCatalogLoaded(); err != nil {
		return CatalogStruct{}, false, err
	}
	db.mu.RLock()
	defer db.mu.RUnlock()
	if err := db.stateErrorLocked(); err != nil {
		return CatalogStruct{}, false, err
	}
	id, found := db.catalog.byName[name]
	if !found {
		return CatalogStruct{}, false, nil
	}
	return cloneCatalogStruct(db.catalog.byID[id]), true, nil
}

// DefineStruct adds or replaces one versioned definition in this transaction.
// The catalog mutation shares the same WAL frame and durability boundary as
// every row or index mutation added to tx.
func (tx *Tx) DefineStruct(definition []byte) error {
	entry, err := decodeCatalogStruct(definition)
	if err != nil {
		return err
	}
	key, err := catalogKey(entry.ID)
	if err != nil {
		return err
	}
	return tx.add(operation{kind: operationPut, key: key, value: entry.Definition})
}

// DeleteStruct removes one durable catalog definition in this transaction.
// Callers that own row or index keyspaces must delete those keys in the same
// transaction when they need a complete logical structure drop.
func (tx *Tx) DeleteStruct(id string) error {
	normalized, err := normalizeCatalogID(id)
	if err != nil {
		return err
	}
	key, err := catalogKey(normalized)
	if err != nil {
		return err
	}
	return tx.Delete(key)
}

func (state catalogState) snapshot(transaction uint64) CatalogSnapshot {
	entries := make([]CatalogStruct, 0, len(state.byID))
	for _, entry := range state.byID {
		entries = append(entries, cloneCatalogStruct(entry))
	}
	sort.Slice(entries, func(left, right int) bool {
		if entries[left].Name != entries[right].Name {
			return entries[left].Name < entries[right].Name
		}
		return entries[left].ID < entries[right].ID
	})
	functions := make([]CatalogFunction, 0, len(state.functions))
	for _, entry := range state.functions {
		entry.Definition = bytes.Clone(entry.Definition)
		functions = append(functions, entry)
	}
	sort.Slice(functions, func(i, j int) bool { return functions[i].Name < functions[j].Name })
	sequences := make([]Sequence, 0, len(state.sequences))
	for _, entry := range state.sequences {
		sequences = append(sequences, entry)
	}
	sort.Slice(sequences, func(i, j int) bool { return sequences[i].Name < sequences[j].Name })
	domains := make([]CatalogDomain, 0, len(state.domains))
	for _, entry := range state.domains {
		entry.Definition = bytes.Clone(entry.Definition)
		domains = append(domains, entry)
	}
	sort.Slice(domains, func(i, j int) bool { return domains[i].Name < domains[j].Name })
	triggers := make([]CatalogTrigger, 0, len(state.triggers))
	for _, entry := range state.triggers {
		triggers = append(triggers, cloneCatalogTrigger(entry))
	}
	sort.Slice(triggers, func(i, j int) bool {
		if triggers[i].SourceStruct != triggers[j].SourceStruct {
			return triggers[i].SourceStruct < triggers[j].SourceStruct
		}
		return triggers[i].Name < triggers[j].Name
	})
	return CatalogSnapshot{Transaction: transaction, Revision: state.revision, Structs: entries, Functions: functions, Domains: domains, Triggers: triggers, Sequences: sequences}
}

func (state catalogState) clone() catalogState {
	cloned := catalogState{
		byID:   make(map[string]CatalogStruct, len(state.byID)),
		byName: make(map[string]string, len(state.byName)),
		bytes:  state.bytes, revision: state.revision,
		functions:     make(map[string]CatalogFunction, len(state.functions)),
		functionNames: make(map[string]string, len(state.functionNames)),
		domains:       make(map[string]CatalogDomain, len(state.domains)),
		domainNames:   make(map[string]string, len(state.domainNames)),
		triggers:      make(map[string]CatalogTrigger, len(state.triggers)),
		triggerNames:  make(map[string]string, len(state.triggerNames)),
		sequences:     make(map[string]Sequence, len(state.sequences)),
		sequenceNames: make(map[string]string, len(state.sequenceNames)),
	}
	for id, entry := range state.functions {
		cloned.functions[id] = entry
	}
	for id, entry := range state.domains {
		cloned.domains[id] = entry
	}
	for id, entry := range state.triggers {
		cloned.triggers[id] = entry
	}
	for name, id := range state.triggerNames {
		cloned.triggerNames[name] = id
	}
	for name, id := range state.domainNames {
		cloned.domainNames[name] = id
	}
	for id, entry := range state.sequences {
		cloned.sequences[id] = entry
	}
	for name, id := range state.sequenceNames {
		cloned.sequenceNames[name] = id
	}
	for name, id := range state.functionNames {
		cloned.functionNames[name] = id
	}
	for id, entry := range state.byID {
		cloned.byID[id] = entry
	}
	for name, id := range state.byName {
		cloned.byName[name] = id
	}
	return cloned
}

func (state *catalogState) put(key, definition []byte) error {
	if isCatalogTriggerKey(key) {
		return state.putTrigger(key, definition)
	}
	if isCatalogDomainKey(key) {
		return state.putDomain(key, definition)
	}
	if isCatalogSequenceKey(key) {
		return state.putSequence(key, definition)
	}
	if isCatalogFunctionKey(key) {
		return state.putFunction(key, definition)
	}
	if len(key) != 17 || key[0] != catalogNamespace {
		return fmt.Errorf("%w: catalog key must be namespace plus a 16-byte struct ID", ErrInvalidCatalog)
	}
	entry, err := decodeCatalogStruct(definition)
	if err != nil {
		return err
	}
	id := hex.EncodeToString(key[1:])
	if entry.ID != id {
		return fmt.Errorf("%w: definition ID %q does not match catalog key %q", ErrInvalidCatalog, entry.ID, id)
	}
	if owner, exists := state.byName[entry.Name]; exists && owner != id {
		return fmt.Errorf("%w: struct name %q belongs to both %s and %s", ErrInvalidCatalog, entry.Name, owner, id)
	}
	if _, exists := state.sequenceNames[entry.Name]; exists {
		return fmt.Errorf("%w: table name conflicts with a sequence", ErrInvalidCatalog)
	}
	previous, replacing := state.byID[id]
	if !replacing && len(state.byID) >= maximumCatalogStructs {
		return fmt.Errorf("%w: catalog exceeds %d structs", ErrInvalidCatalog, maximumCatalogStructs)
	}
	nextBytes := state.bytes + len(entry.Definition)
	if replacing {
		nextBytes -= len(previous.Definition)
	}
	if nextBytes > maximumCatalogBytes {
		return fmt.Errorf("%w: catalog exceeds %d bytes", ErrInvalidCatalog, maximumCatalogBytes)
	}
	if replacing && previous.Name != entry.Name {
		delete(state.byName, previous.Name)
	}
	state.byID[id] = entry
	state.byName[entry.Name] = id
	state.bytes = nextBytes
	return nil
}

func (state *catalogState) delete(key []byte) error {
	if isCatalogTriggerKey(key) {
		id := hex.EncodeToString(key[2:])
		if entry, found := state.triggers[id]; found {
			delete(state.triggers, id)
			delete(state.triggerNames, entry.SourceStruct+":"+entry.Name)
			state.bytes -= len(entry.Definition)
		}
		return nil
	}
	if isCatalogDomainKey(key) {
		id := hex.EncodeToString(key[2:])
		if entry, found := state.domains[id]; found {
			delete(state.domains, id)
			delete(state.domainNames, entry.Name)
			state.bytes -= len(entry.Definition)
		}
		return nil
	}
	if isCatalogSequenceKey(key) {
		id := hex.EncodeToString(key[2:])
		if entry, found := state.sequences[id]; found {
			definition, _ := json.Marshal(entry)
			state.bytes -= len(definition)
			delete(state.sequences, id)
			delete(state.sequenceNames, entry.Name)
		}
		return nil
	}
	if isCatalogFunctionKey(key) {
		id := hex.EncodeToString(key[2:])
		if entry, found := state.functions[id]; found {
			delete(state.functions, id)
			delete(state.functionNames, entry.Name)
			state.bytes -= len(entry.Definition)
		}
		return nil
	}
	if len(key) != 17 || key[0] != catalogNamespace {
		return fmt.Errorf("%w: catalog key must be namespace plus a 16-byte struct ID", ErrInvalidCatalog)
	}
	id := hex.EncodeToString(key[1:])
	entry, found := state.byID[id]
	if !found {
		return nil
	}
	delete(state.byID, id)
	delete(state.byName, entry.Name)
	state.bytes -= len(entry.Definition)
	return nil
}

func (state catalogState) applyOperations(operations []operation) (catalogState, error) {
	touched := false
	for _, operation := range operations {
		if len(operation.key) != 0 && operation.key[0] == catalogNamespace {
			touched = true
			break
		}
	}
	if !touched {
		return state, nil
	}
	next := state.clone()
	for _, operation := range operations {
		if len(operation.key) == 0 || operation.key[0] != catalogNamespace {
			continue
		}
		var err error
		switch operation.kind {
		case operationPut:
			err = next.put(operation.key, operation.value)
		case operationDelete:
			err = next.delete(operation.key)
		default:
			err = fmt.Errorf("%w: unsupported catalog operation %d", ErrInvalidCatalog, operation.kind)
		}
		if err != nil {
			return catalogState{}, err
		}
	}
	if err := next.validateSequenceReferences(); err != nil {
		return catalogState{}, err
	}
	if err := next.validateDomainReferences(); err != nil {
		return catalogState{}, err
	}
	if err := next.validateTriggerReferences(); err != nil {
		return catalogState{}, err
	}
	next.refreshRevision()
	return next, nil
}

func requestTouchesCatalog(request *commitRequest) bool {
	for _, operation := range request.operations {
		if len(operation.key) != 0 && operation.key[0] == catalogNamespace {
			return true
		}
	}
	return false
}

func (state *catalogState) refreshRevision() {
	ids := make([]string, 0, len(state.byID))
	for id := range state.byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	digest := sha256.New()
	var size [8]byte
	for _, id := range ids {
		entry := state.byID[id]
		binary.BigEndian.PutUint64(size[:], uint64(len(id)))
		_, _ = digest.Write(size[:])
		_, _ = digest.Write([]byte(id))
		binary.BigEndian.PutUint64(size[:], uint64(len(entry.Definition)))
		_, _ = digest.Write(size[:])
		_, _ = digest.Write(entry.Definition)
	}
	state.revision = hex.EncodeToString(digest.Sum(nil))
	if len(state.functions) != 0 {
		ids = ids[:0]
		for id := range state.functions {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		_, _ = digest.Write([]byte("\x00functions\x00"))
		for _, id := range ids {
			entry := state.functions[id]
			_, _ = digest.Write([]byte(id))
			binary.BigEndian.PutUint64(size[:], uint64(len(entry.Definition)))
			_, _ = digest.Write(size[:])
			_, _ = digest.Write(entry.Definition)
		}
		state.revision = hex.EncodeToString(digest.Sum(nil))
	}
	if len(state.sequences) != 0 {
		ids = ids[:0]
		for id := range state.sequences {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		_, _ = digest.Write([]byte("\x00sequences\x00"))
		for _, id := range ids {
			definition, _ := json.Marshal(state.sequences[id])
			binary.BigEndian.PutUint64(size[:], uint64(len(definition)))
			_, _ = digest.Write(size[:])
			_, _ = digest.Write(definition)
		}
		state.revision = hex.EncodeToString(digest.Sum(nil))
	}
	if len(state.domains) != 0 {
		ids = ids[:0]
		for id := range state.domains {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		_, _ = digest.Write([]byte("\x00domains\x00"))
		for _, id := range ids {
			entry := state.domains[id]
			_, _ = digest.Write([]byte(id))
			binary.BigEndian.PutUint64(size[:], uint64(len(entry.Definition)))
			_, _ = digest.Write(size[:])
			_, _ = digest.Write(entry.Definition)
		}
		state.revision = hex.EncodeToString(digest.Sum(nil))
	}
	if len(state.triggers) != 0 {
		ids = ids[:0]
		for id := range state.triggers {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		_, _ = digest.Write([]byte("\x00triggers\x00"))
		for _, id := range ids {
			entry := state.triggers[id]
			_, _ = digest.Write([]byte(id))
			binary.BigEndian.PutUint64(size[:], uint64(len(entry.Definition)))
			_, _ = digest.Write(size[:])
			_, _ = digest.Write(entry.Definition)
		}
		state.revision = hex.EncodeToString(digest.Sum(nil))
	}
}

func decodeCatalogStruct(definition []byte) (CatalogStruct, error) {
	if len(definition) == 0 || len(definition) > maximumCatalogDefinitionBytes {
		return CatalogStruct{}, fmt.Errorf("%w: definition must contain between 1 and %d bytes", ErrInvalidCatalog, maximumCatalogDefinitionBytes)
	}
	if !utf8.Valid(definition) {
		return CatalogStruct{}, fmt.Errorf("%w: definition is not valid UTF-8", ErrInvalidCatalog)
	}
	fields, err := decodeCatalogObject(definition)
	if err != nil {
		return CatalogStruct{}, err
	}
	var entry CatalogStruct
	if err := decodeRequiredCatalogField(fields, "version", &entry.Version); err != nil {
		return CatalogStruct{}, err
	}
	if err := decodeRequiredCatalogField(fields, "id", &entry.ID); err != nil {
		return CatalogStruct{}, err
	}
	if err := decodeRequiredCatalogField(fields, "name", &entry.Name); err != nil {
		return CatalogStruct{}, err
	}
	if err := decodeRequiredCatalogField(fields, "hash", &entry.Hash); err != nil {
		return CatalogStruct{}, err
	}
	var schemaFields []json.RawMessage
	if err := decodeRequiredCatalogField(fields, "fields", &schemaFields); err != nil {
		return CatalogStruct{}, err
	}
	if entry.Version < 1 {
		return CatalogStruct{}, fmt.Errorf("%w: schema version must be positive", ErrInvalidCatalog)
	}
	normalizedID, err := normalizeCatalogID(entry.ID)
	if err != nil {
		return CatalogStruct{}, err
	}
	entry.ID = normalizedID
	if strings.TrimSpace(entry.Name) == "" || len(entry.Name) > maximumCatalogNameBytes || strings.ContainsRune(entry.Name, '\x00') {
		return CatalogStruct{}, fmt.Errorf("%w: struct name is empty, too large, or contains NUL", ErrInvalidCatalog)
	}
	if entry.Hash == "" || len(entry.Hash) > maximumCatalogNameBytes {
		return CatalogStruct{}, fmt.Errorf("%w: struct hash is empty or too large", ErrInvalidCatalog)
	}
	if len(schemaFields) == 0 || len(schemaFields) > maximumCatalogFields {
		return CatalogStruct{}, fmt.Errorf("%w: struct must contain between 1 and %d fields", ErrInvalidCatalog, maximumCatalogFields)
	}
	entry.Definition = bytes.Clone(definition)
	return entry, nil
}

func decodeCatalogObject(definition []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(definition))
	token, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("%w: decode definition: %v", ErrInvalidCatalog, err)
	}
	delimiter, ok := token.(json.Delim)
	if !ok || delimiter != '{' {
		return nil, fmt.Errorf("%w: definition must be a JSON object", ErrInvalidCatalog)
	}
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("%w: decode definition field: %v", ErrInvalidCatalog, err)
		}
		name, ok := token.(string)
		if !ok {
			return nil, fmt.Errorf("%w: definition field name is not a string", ErrInvalidCatalog)
		}
		if _, duplicate := fields[name]; duplicate {
			return nil, fmt.Errorf("%w: definition repeats field %q", ErrInvalidCatalog, name)
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, fmt.Errorf("%w: decode definition field %q: %v", ErrInvalidCatalog, name, err)
		}
		fields[name] = bytes.Clone(raw)
	}
	if _, err := decoder.Token(); err != nil {
		return nil, fmt.Errorf("%w: close definition object: %v", ErrInvalidCatalog, err)
	}
	if token, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err != nil {
			return nil, fmt.Errorf("%w: trailing definition data: %v", ErrInvalidCatalog, err)
		}
		return nil, fmt.Errorf("%w: trailing definition token %v", ErrInvalidCatalog, token)
	}
	return fields, nil
}

func decodeRequiredCatalogField(fields map[string]json.RawMessage, name string, destination any) error {
	raw, found := fields[name]
	if !found {
		return fmt.Errorf("%w: definition has no %q field", ErrInvalidCatalog, name)
	}
	if err := json.Unmarshal(raw, destination); err != nil {
		return fmt.Errorf("%w: definition field %q: %v", ErrInvalidCatalog, name, err)
	}
	return nil
}

func catalogKey(id string) ([]byte, error) {
	normalized, err := normalizeCatalogID(id)
	if err != nil {
		return nil, err
	}
	decoded, _ := hex.DecodeString(normalized)
	key := make([]byte, 1, 17)
	key[0] = catalogNamespace
	return append(key, decoded...), nil
}

func normalizeCatalogID(id string) (string, error) {
	decoded, err := hex.DecodeString(id)
	if err != nil || len(decoded) != 16 {
		return "", fmt.Errorf("%w: struct ID must encode exactly 16 bytes", ErrInvalidCatalog)
	}
	return hex.EncodeToString(decoded), nil
}

func cloneCatalogStruct(entry CatalogStruct) CatalogStruct {
	entry.Definition = bytes.Clone(entry.Definition)
	return entry
}
