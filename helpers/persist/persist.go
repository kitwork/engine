// Package persist is a DISK response cache: it writes rendered responses (any content type —
// html, json, image, …) to a directory (e.g. <tenant>/.persist) and reads them back with no VM.
// Unlike the in-memory cache it SURVIVES restarts. It is pure: it stores opaque records by key
// and knows nothing about HTTP or the router.
package persist

import (
	"encoding/gob"
	"os"
	"path/filepath"
	"time"
)

// Record is a persisted response. Body is raw bytes, so any content type works.
type Record struct {
	Body        []byte
	ContentType string
	Status      int
	ExpireAt    time.Time
}

// Store persists Records as gob files under Dir.
type Store struct{ Dir string }

// New returns a store rooted at dir (created lazily on first write).
func New(dir string) *Store { return &Store{Dir: dir} }

func (s *Store) path(key string) string { return filepath.Join(s.Dir, key+".gob") }

// Get returns the record for key if present and unexpired (expired files are removed).
func (s *Store) Get(key string) (Record, bool) {
	f, err := os.Open(s.path(key))
	if err != nil {
		return Record{}, false
	}
	defer f.Close()
	var r Record
	if gob.NewDecoder(f).Decode(&r) != nil {
		return Record{}, false
	}
	if !r.ExpireAt.IsZero() && time.Now().After(r.ExpireAt) {
		os.Remove(s.path(key))
		return Record{}, false
	}
	return r, true
}

// Set writes r under key with the given TTL (0 = no expiry). The write is atomic (temp + rename).
func (s *Store) Set(key string, r Record, ttl time.Duration) error {
	if ttl > 0 {
		r.ExpireAt = time.Now().Add(ttl)
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	tmp := s.path(key) + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := gob.NewEncoder(f).Encode(r); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	f.Close()
	return os.Rename(tmp, s.path(key))
}
