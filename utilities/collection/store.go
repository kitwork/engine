package collection

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// SnapshotStore is the optional disk tier injected by the host.
type SnapshotStore interface {
	Load(key string) ([]byte, bool)
	Save(key string, body []byte, ttl time.Duration) error
}

type documentCache struct {
	signature  string
	document   *Document
	lastAccess time.Time
}

type indexCache struct {
	signature  string
	index      []IndexEntry
	lastAccess time.Time
}

type flight struct {
	done  chan struct{}
	value any
	err   error
}

// Store owns collection caches for one tenant.
type Store struct {
	root string
	disk SnapshotStore
	max  int

	mu        sync.Mutex
	documents map[string]documentCache
	indexes   map[string]indexCache
	flights   map[string]*flight
}

func NewStore(root string, disk SnapshotStore) (*Store, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	return &Store{
		root:      absolute,
		disk:      disk,
		max:       512,
		documents: make(map[string]documentCache),
		indexes:   make(map[string]indexCache),
		flights:   make(map[string]*flight),
	}, nil
}

func (s *Store) Open(path string) (*Collection, error) {
	clean := filepath.Clean(filepath.FromSlash(strings.TrimSpace(path)))
	if clean == "." || clean == "" || filepath.IsAbs(clean) {
		return nil, fmt.Errorf("collection: invalid folder")
	}
	full := filepath.Join(s.root, clean)
	relative, err := filepath.Rel(s.root, full)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("collection: access outside tenant root denied")
	}
	info, err := os.Stat(full)
	if err != nil {
		return nil, fmt.Errorf("collection: open %s: %w", path, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("collection: %s is not a folder", path)
	}
	return &Collection{
		store:        s,
		path:         filepath.ToSlash(relative),
		dir:          full,
		cacheEnabled: true,
	}, nil
}

func (s *Store) run(key string, fn func() (any, error)) (any, error) {
	s.mu.Lock()
	if active, ok := s.flights[key]; ok {
		s.mu.Unlock()
		<-active.done
		return active.value, active.err
	}
	active := &flight{done: make(chan struct{})}
	s.flights[key] = active
	s.mu.Unlock()

	active.value, active.err = fn()
	s.mu.Lock()
	delete(s.flights, key)
	close(active.done)
	s.mu.Unlock()
	return active.value, active.err
}

func (s *Store) getDocument(key, signature string, ttl time.Duration) (*Document, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.documents[key]
	if !ok || entry.signature != signature {
		return nil, false
	}
	if ttl > 0 && time.Since(entry.lastAccess) > ttl {
		delete(s.documents, key)
		return nil, false
	}
	entry.lastAccess = time.Now()
	s.documents[key] = entry
	return entry.document, true
}

func (s *Store) setDocument(key, signature string, document *Document) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.documents[key]; !exists && len(s.documents) >= s.max {
		for old := range s.documents {
			delete(s.documents, old)
			break
		}
	}
	s.documents[key] = documentCache{signature: signature, document: document, lastAccess: time.Now()}
}

func (s *Store) getIndex(key, signature string, ttl time.Duration) ([]IndexEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.indexes[key]
	if !ok || entry.signature != signature {
		return nil, false
	}
	if ttl > 0 && time.Since(entry.lastAccess) > ttl {
		delete(s.indexes, key)
		return nil, false
	}
	entry.lastAccess = time.Now()
	s.indexes[key] = entry
	return entry.index, true
}

func (s *Store) setIndex(key, signature string, index []IndexEntry) {
	s.mu.Lock()
	s.indexes[key] = indexCache{signature: signature, index: index, lastAccess: time.Now()}
	s.mu.Unlock()
}

func snapshotKey(kind, path string) string {
	sum := sha256.Sum256([]byte(filepath.ToSlash(path)))
	return kind + "/" + hex.EncodeToString(sum[:16])
}

func fileSignature(info os.FileInfo) string {
	return fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())
}

func directorySignature(files []File) string {
	hash := sha256.New()
	for _, file := range files {
		hash.Write([]byte(file.Name))
		hash.Write([]byte{0})
		hash.Write([]byte(file.signature))
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)[:16])
}

func scanMarkdownFiles(dir string) ([]File, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	files := make([]File, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		extension := strings.ToLower(filepath.Ext(entry.Name()))
		if extension != ".md" && extension != ".markdown" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		files = append(files, File{
			Name:      entry.Name(),
			Slug:      strings.TrimSuffix(entry.Name(), extension),
			Extension: extension,
			Size:      info.Size(),
			Modified:  info.ModTime().UTC().Format(time.RFC3339),
			signature: fileSignature(info),
			path:      filepath.Join(dir, entry.Name()),
		})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	return files, nil
}

type documentSnapshot struct {
	Signature string    `json:"signature"`
	Document  *Document `json:"document"`
}

type indexSnapshot struct {
	Signature string       `json:"signature"`
	Index     []IndexEntry `json:"index"`
	// File.signature is unexported (it must not leak into JS payloads), so a JSON round-trip through
	// the persist tier would silently DROP it — and every derived index (FTS search sync) that compares
	// per-file signatures would then see "" == "" and treat all documents as unchanged, indexing
	// nothing. Carry the signatures alongside and re-attach on decode.
	FileSignatures []string `json:"fileSignatures"`
}

func decodeDocumentSnapshot(body []byte, signature string) (*Document, bool) {
	var snapshot documentSnapshot
	if json.Unmarshal(body, &snapshot) != nil || snapshot.Signature != signature || snapshot.Document == nil {
		return nil, false
	}
	return snapshot.Document, true
}

func decodeIndexSnapshot(body []byte, signature string) ([]IndexEntry, bool) {
	var snapshot indexSnapshot
	if json.Unmarshal(body, &snapshot) != nil || snapshot.Signature != signature {
		return nil, false
	}
	// Re-attach the unexported per-file signatures (older snapshots without them stay signature-less
	// and derived indexes treat those entries as always-changed — correct, just less cached).
	if len(snapshot.FileSignatures) == len(snapshot.Index) {
		for i := range snapshot.Index {
			snapshot.Index[i].File.signature = snapshot.FileSignatures[i]
		}
	}
	return snapshot.Index, true
}

func encodeIndexSnapshot(signature string, index []IndexEntry) ([]byte, error) {
	sigs := make([]string, len(index))
	for i := range index {
		sigs[i] = index[i].File.signature
	}
	return json.Marshal(indexSnapshot{Signature: signature, Index: index, FileSignatures: sigs})
}
