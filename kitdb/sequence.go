package kitdb

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"
)

const (
	maximumSequences = 1024
	// MaximumSequenceCache bounds refill CPU while each lease remains O(1) memory.
	MaximumSequenceCache = 4096
)

var (
	ErrSequenceNotFound = errors.New("kitdb: sequence does not exist")
	ErrSequenceExists   = errors.New("kitdb: sequence already exists")
	ErrSequenceLimit    = errors.New("kitdb: sequence limit reached")
)

// Sequence is an immutable catalog definition. Counter reservations live in a
// separate keyspace, so nextval does not invalidate prepared schema bindings.
type Sequence struct {
	Version     int    `json:"version"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	DataType    string `json:"dataType,omitempty"`
	Start       int64  `json:"start"`
	Increment   int64  `json:"increment"`
	Minimum     int64  `json:"minimum"`
	Maximum     int64  `json:"maximum"`
	Cycle       bool   `json:"cycle"`
	Cache       int64  `json:"cache,omitempty"`
	OwnerStruct string `json:"ownerStruct,omitempty"`
	OwnerTag    uint32 `json:"ownerTag,omitempty"`
}

func (sequence Sequence) validate() error {
	typeMinimum, typeMaximum, validType := sequenceTypeBounds(sequence.DataTypeName())
	if (sequence.OwnerStruct == "") != (sequence.OwnerTag == 0) {
		return fmt.Errorf("%w: incomplete sequence owner", ErrInvalidCatalog)
	}
	if sequence.OwnerStruct != "" {
		if _, err := normalizeCatalogID(sequence.OwnerStruct); err != nil {
			return err
		}
	}
	if sequence.Version != 1 || sequence.Name == "" || len(sequence.Name) > 128 || !validType ||
		!utf8.ValidString(sequence.Name) || strings.ContainsAny(sequence.Name, "\x00.") ||
		sequence.Increment == 0 || sequence.Minimum >= sequence.Maximum ||
		sequence.Cache < 0 || sequence.Cache > MaximumSequenceCache ||
		sequence.Minimum < typeMinimum || sequence.Maximum > typeMaximum ||
		sequence.Start < sequence.Minimum || sequence.Start > sequence.Maximum {
		return fmt.Errorf("%w: invalid sequence definition", ErrInvalidCatalog)
	}
	_, err := normalizeCatalogID(sequence.ID)
	return err
}

// DataTypeName returns the PostgreSQL-compatible sequence type. The empty
// durable value is the original BIGINT encoding, so old canonical bytes and
// logical digests remain unchanged.
func (sequence Sequence) DataTypeName() string {
	if sequence.DataType == "" {
		return "bigint"
	}
	return sequence.DataType
}

func sequenceTypeBounds(dataType string) (int64, int64, bool) {
	switch dataType {
	case "smallint":
		return math.MinInt16, math.MaxInt16, true
	case "integer":
		return math.MinInt32, math.MaxInt32, true
	case "bigint":
		return math.MinInt64, math.MaxInt64, true
	default:
		return 0, 0, false
	}
}

// CacheSize returns the bounded lease size. Zero is the legacy encoding of
// CACHE 1, retained so existing canonical catalog bytes and hashes do not move.
func (sequence Sequence) CacheSize() int64 {
	if sequence.Cache == 0 {
		return 1
	}
	return sequence.Cache
}

func normalizeSequenceOptions(sequence Sequence) Sequence {
	if sequence.Cache == 0 {
		sequence.Cache = 1
	}
	return sequence
}

type sequenceLease struct {
	next      int64
	remaining int64
}

func isCatalogSequenceKey(key []byte) bool {
	return len(key) == 18 && key[0] == catalogNamespace && key[1] == 'S'
}

func sequenceKeys(id string) (definition, counter []byte) {
	decoded, _ := hex.DecodeString(id)
	return append([]byte{catalogNamespace, 'S'}, decoded...), append([]byte{0x02}, decoded...)
}

func (state *catalogState) putSequence(key, definition []byte) error {
	if len(definition) > 4096 || !utf8.Valid(definition) {
		return fmt.Errorf("%w: invalid sequence definition size/encoding", ErrInvalidCatalog)
	}
	var sequence Sequence
	decoder := json.NewDecoder(bytes.NewReader(definition))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&sequence); err != nil {
		return errors.Join(ErrInvalidCatalog, err)
	}
	canonical, _ := json.Marshal(sequence)
	if !bytes.Equal(canonical, definition) {
		return fmt.Errorf("%w: sequence definition must use canonical encoding", ErrInvalidCatalog)
	}
	if err := sequence.validate(); err != nil {
		return err
	}
	if sequence.ID != hex.EncodeToString(key[2:]) {
		return fmt.Errorf("%w: sequence ID does not match its key", ErrInvalidCatalog)
	}
	if id, exists := state.sequenceNames[sequence.Name]; exists && id != sequence.ID {
		return ErrSequenceExists
	}
	if _, exists := state.byName[sequence.Name]; exists {
		return fmt.Errorf("%w: sequence name conflicts with a table", ErrInvalidCatalog)
	}
	old, exists := state.sequences[sequence.ID]
	if exists && old != sequence {
		return fmt.Errorf("%w: sequence definitions are immutable", ErrInvalidCatalog)
	}
	if exists {
		return nil
	}
	if len(state.sequences) >= maximumSequences || state.bytes+len(definition) > maximumCatalogBytes {
		return fmt.Errorf("%w: sequence catalog budget exceeded", ErrInvalidCatalog)
	}
	state.sequences[sequence.ID] = sequence
	state.sequenceNames[sequence.Name] = sequence.ID
	state.bytes += len(definition)
	return nil
}

// SequenceByName reads a definition, not the session-local currval.
func (db *DB) SequenceByName(name string) (Sequence, error) {
	if err := db.ensureCatalogLoaded(); err != nil {
		return Sequence{}, err
	}
	db.mu.RLock()
	defer db.mu.RUnlock()
	if err := db.stateErrorLocked(); err != nil {
		return Sequence{}, err
	}
	id, found := db.catalog.sequenceNames[name]
	if !found {
		return Sequence{}, fmt.Errorf("%w: %q", ErrSequenceNotFound, name)
	}
	return db.catalog.sequences[id], nil
}

func (db *DB) SequenceByID(id string) (Sequence, error) {
	if err := db.ensureCatalogLoaded(); err != nil {
		return Sequence{}, err
	}
	db.mu.RLock()
	defer db.mu.RUnlock()
	if err := db.stateErrorLocked(); err != nil {
		return Sequence{}, err
	}
	sequence, found := db.catalog.sequences[id]
	if !found {
		return Sequence{}, fmt.Errorf("%w: ID %q", ErrSequenceNotFound, id)
	}
	return sequence, nil
}

// CreateSequence stages both records, allowing table and owned sequences to
// publish in one frame. CommitSequenceChanges serializes publication with the
// allocator. Callers must roll back after any staging error.
func (tx *Tx) CreateSequence(options Sequence) (Sequence, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return Sequence{}, err
	}
	options.Version, options.ID = 1, hex.EncodeToString(id[:])
	options = normalizeSequenceOptions(options)
	if err := options.validate(); err != nil {
		return Sequence{}, err
	}
	definition, _ := json.Marshal(options)
	key, counter := sequenceKeys(options.ID)
	if err := tx.Put(key, definition); err != nil {
		return Sequence{}, err
	}
	if err := tx.Put(counter, encodeSequenceCounter(options.Start, false)); err != nil {
		return Sequence{}, err
	}
	return options, nil
}

func (tx *Tx) DropSequence(id string) error {
	if _, err := normalizeCatalogID(id); err != nil {
		return err
	}
	key, counter := sequenceKeys(id)
	if err := tx.Delete(key); err != nil {
		return err
	}
	return tx.Delete(counter)
}

func (tx *Tx) CommitSequenceChanges() (uint64, error) {
	tx.mu.Lock()
	ids := make([]string, 0)
	for _, operation := range tx.operations {
		if len(operation.key) == 17 && operation.key[0] == 0x02 {
			ids = append(ids, hex.EncodeToString(operation.key[1:]))
		}
	}
	tx.mu.Unlock()
	tx.db.sequenceMu.Lock()
	defer tx.db.sequenceMu.Unlock()
	transaction, err := tx.Commit()
	if err == nil {
		for _, id := range ids {
			delete(tx.db.sequenceLeases, id)
		}
	}
	return transaction, err
}

func (db *DB) NextSequenceByID(ctx context.Context, id string) (Sequence, int64, error) {
	sequence, err := db.SequenceByID(id)
	if err != nil {
		return Sequence{}, 0, err
	}
	return db.reserveSequence(ctx, sequence.Name, nil, true, false, id)
}

// CreateSequence atomically publishes its definition and initial counter.
// Options must explicitly contain valid Start, Increment, Minimum and Maximum.
func (db *DB) CreateSequence(ctx context.Context, options Sequence) (Sequence, error) {
	if ctx == nil {
		return Sequence{}, fmt.Errorf("kitdb: sequence context is nil")
	}
	db.sequenceMu.Lock()
	defer db.sequenceMu.Unlock()
	if err := ctx.Err(); err != nil {
		return Sequence{}, err
	}
	if _, err := db.SequenceByName(options.Name); err == nil {
		return Sequence{}, ErrSequenceExists
	} else if !errors.Is(err, ErrSequenceNotFound) {
		return Sequence{}, err
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return Sequence{}, err
	}
	options.Version, options.ID = 1, hex.EncodeToString(id[:])
	options = normalizeSequenceOptions(options)
	if err := options.validate(); err != nil {
		return Sequence{}, err
	}
	definition, _ := json.Marshal(options)
	key, counter := sequenceKeys(options.ID)
	tx, err := db.Begin()
	if err != nil {
		return Sequence{}, err
	}
	defer tx.Rollback()
	if err := tx.Put(key, definition); err != nil {
		return Sequence{}, err
	}
	if err := tx.Put(counter, encodeSequenceCounter(options.Start, false)); err != nil {
		return Sequence{}, err
	}
	_, err = tx.Commit()
	return options, err
}

func (db *DB) DropSequence(ctx context.Context, name string) error {
	if ctx == nil {
		return fmt.Errorf("kitdb: sequence context is nil")
	}
	db.sequenceMu.Lock()
	defer db.sequenceMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	sequence, err := db.SequenceByName(name)
	if err != nil {
		return err
	}
	key, counter := sequenceKeys(sequence.ID)
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := tx.Delete(key); err != nil {
		return err
	}
	if err := tx.Delete(counter); err != nil {
		return err
	}
	_, err = tx.Commit()
	if err == nil {
		delete(db.sequenceLeases, sequence.ID)
	}
	return err
}

func encodeSequenceCounter(value int64, called bool) []byte {
	encoded := make([]byte, 10)
	encoded[0] = 1
	if called {
		encoded[1] = 1
	}
	binary.BigEndian.PutUint64(encoded[2:], uint64(value))
	return encoded
}

// NextSequence and SetSequence durably reserve outside any caller transaction.
// CACHE N persists a high-watermark before serving an O(1) in-memory lease;
// unused values are deliberately lost when the handle closes or restarts.
func (db *DB) NextSequence(ctx context.Context, name string) (Sequence, int64, error) {
	return db.reserveSequence(ctx, name, nil, true, false)
}

func (db *DB) SetSequence(ctx context.Context, name string, value int64, called bool) (Sequence, int64, error) {
	return db.reserveSequence(ctx, name, &value, called, false)
}

// RestartSequence is an autocommit DDL boundary, unlike setval.
func (db *DB) RestartSequence(ctx context.Context, name string, value *int64) error {
	_, _, err := db.reserveSequence(ctx, name, value, false, true)
	return err
}

func (db *DB) reserveSequence(ctx context.Context, name string, set *int64, called bool, restart bool, expectedID ...string) (Sequence, int64, error) {
	if ctx == nil {
		return Sequence{}, 0, fmt.Errorf("kitdb: sequence context is nil")
	}
	db.sequenceMu.Lock()
	defer db.sequenceMu.Unlock()
	if err := ctx.Err(); err != nil {
		return Sequence{}, 0, err
	}
	sequence, err := db.SequenceByName(name)
	if err != nil {
		return Sequence{}, 0, err
	}
	if len(expectedID) != 0 && sequence.ID != expectedID[0] {
		return Sequence{}, 0, ErrSequenceNotFound
	}
	if restart && set == nil {
		set = &sequence.Start
	}
	if set == nil && !restart {
		if value, found, err := db.consumeSequenceLease(sequence); found || err != nil {
			return sequence, value, err
		}
	}
	_, key := sequenceKeys(sequence.ID)
	encoded, found, err := db.Get(key)
	if err != nil {
		return Sequence{}, 0, err
	}
	if !found || len(encoded) != 10 || encoded[0] != 1 || encoded[1] > 1 {
		return Sequence{}, 0, fmt.Errorf("%w: invalid sequence counter", ErrCorrupt)
	}
	value := int64(binary.BigEndian.Uint64(encoded[2:]))
	if value < sequence.Minimum || value > sequence.Maximum {
		return Sequence{}, 0, fmt.Errorf("%w: sequence counter outside bounds", ErrCorrupt)
	}
	if set != nil {
		value = *set
		if value < sequence.Minimum || value > sequence.Maximum {
			return Sequence{}, 0, fmt.Errorf("%w: setval outside sequence bounds", ErrSequenceLimit)
		}
	} else if encoded[1] == 1 {
		value, err = advanceSequenceValue(sequence, value)
		if err != nil {
			return Sequence{}, 0, err
		}
	}
	first, high, reserved := value, value, int64(1)
	if set == nil && !restart {
		for reserved < sequence.CacheSize() {
			next, advanceErr := advanceSequenceValue(sequence, high)
			if errors.Is(advanceErr, ErrSequenceLimit) {
				break
			}
			if advanceErr != nil {
				return Sequence{}, 0, advanceErr
			}
			high, reserved = next, reserved+1
		}
		value, called = high, true
	}
	tx, err := db.Begin()
	if err != nil {
		return Sequence{}, 0, err
	}
	defer tx.Rollback()
	if err := tx.Put(key, encodeSequenceCounter(value, called)); err != nil {
		return Sequence{}, 0, err
	}
	_, err = tx.commit(nil, !restart)
	if err != nil {
		return Sequence{}, 0, err
	}
	delete(db.sequenceLeases, sequence.ID)
	if set == nil && !restart && reserved > 1 {
		next, advanceErr := advanceSequenceValue(sequence, first)
		if advanceErr != nil {
			return Sequence{}, 0, errors.Join(ErrCorrupt, advanceErr)
		}
		if db.sequenceLeases == nil {
			db.sequenceLeases = make(map[string]sequenceLease)
		}
		db.sequenceLeases[sequence.ID] = sequenceLease{next: next, remaining: reserved - 1}
	}
	return sequence, first, nil
}

func (db *DB) consumeSequenceLease(sequence Sequence) (int64, bool, error) {
	lease, found := db.sequenceLeases[sequence.ID]
	if !found || lease.remaining < 1 {
		return 0, false, nil
	}
	value := lease.next
	if lease.remaining == 1 {
		delete(db.sequenceLeases, sequence.ID)
		return value, true, nil
	}
	next, err := advanceSequenceValue(sequence, value)
	if err != nil {
		delete(db.sequenceLeases, sequence.ID)
		return 0, true, errors.Join(ErrCorrupt, fmt.Errorf("kitdb: invalid sequence lease: %w", err))
	}
	lease.next, lease.remaining = next, lease.remaining-1
	db.sequenceLeases[sequence.ID] = lease
	return value, true, nil
}

func advanceSequenceValue(sequence Sequence, value int64) (int64, error) {
	step := sequence.Increment
	overflow := step > 0 && value > math.MaxInt64-step || step < 0 && value < math.MinInt64-step
	if !overflow {
		value += step
	}
	if !overflow && value >= sequence.Minimum && value <= sequence.Maximum {
		return value, nil
	}
	if !sequence.Cycle {
		return 0, ErrSequenceLimit
	}
	if step > 0 {
		return sequence.Minimum, nil
	}
	return sequence.Maximum, nil
}
