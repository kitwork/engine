package work

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kitwork/engine/utilities/compress"
)

// A static file's ETag is weak (fixed 14/10/2026): utilities/compress gzips a text file of a kilobyte or
// more after the ETag is written, so the plain bytes and the gzipped ones go out under one ETag, and a
// strong ETag may name one representation only (RFC 9110 §8.8.3). Both encodings still answer their
// ETag with 304. CONTROL: the gzipped answer is another representation (Content-Encoding gzip, fewer
// bytes); another ETag gets the file.
func TestStaticFileETagIsWeakAcrossEncodings(t *testing.T) {
	logo := `<svg xmlns="http://www.w3.org/2000/svg">` + strings.Repeat(`<circle cx="1" cy="1" r="1"/>`, 60) + `</svg>`
	tenant := buildUISiteMode(t, withFiles(map[string]string{"localhost/assets/logo.svg": logo}), false)
	handler := compress.Middleware(http.HandlerFunc(tenant.Serve))
	ask := func(gzip bool, etag string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "http://localhost/assets/logo.svg", nil)
		if gzip {
			request.Header.Set("Accept-Encoding", "gzip")
		}
		if etag != "" {
			request.Header.Set("If-None-Match", etag)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}
	plain, packed := ask(false, ""), ask(true, "")
	if plain.Code != http.StatusOK || packed.Code != http.StatusOK || plain.Body.String() != logo ||
		packed.Header().Get("Content-Encoding") != "gzip" || packed.Body.Len() >= len(logo) {
		t.Fatalf("CONTROL: the logo goes out plain (%d, %d bytes) and gzipped (%d, %q, %d bytes)",
			plain.Code, plain.Body.Len(), packed.Code, packed.Header().Get("Content-Encoding"), packed.Body.Len())
	}
	etag := plain.Header().Get("ETag")
	if etag != packed.Header().Get("ETag") {
		t.Fatalf("one ETag for the file's bytes however they travel: %q and %q", etag, packed.Header().Get("ETag"))
	}
	if !strings.HasPrefix(etag, `W/"`) {
		t.Errorf("the plain and the gzipped bytes share the ETag %q: it must be weak (W/), a strong one names one representation", etag)
	}
	for _, gzip := range []bool{false, true} {
		if again := ask(gzip, etag); again.Code != http.StatusNotModified {
			t.Errorf("gzip %v: the ETag answers 304, got %d", gzip, again.Code)
		}
		if other := ask(gzip, `W/"0-0"`); other.Code != http.StatusOK {
			t.Errorf("gzip %v: CONTROL: another ETag gets the file, got %d", gzip, other.Code)
		}
	}
}

// A part of a static file's name made of digits only (a date, a time, a counter: the uploads of
// huynhnhanquoc.com are named 1719557903791117408_….jpg) is never taken for a hash, so the file is
// never read for it (fixed 14/10/2026: until then the first request for each such upload read the whole file
// before its first byte went out). CONTROL: a name part with a letter that is not the file's hash is
// read and compared (not kept a year), and the file's own hash is (kept a year).
func TestStaticNamesOfDigitsAreNeverRead(t *testing.T) {
	folder := t.TempDir()
	bytes := []byte("a photo\n")
	sum := sha256.Sum256(bytes)
	own := hex.EncodeToString(sum[:])[:16]
	for _, c := range []struct {
		name         string
		read, hashed bool
	}{
		{"1719557903791117408_photo.jpg", false, false},
		{"1662286048-x.jpg", false, false},
		{"logo.20261014.svg", false, false},
		{"photo.abcdef0123456789.jpg", true, false}, // CONTROL: read, not its hash
		{"photo." + own + ".jpg", true, true},       // CONTROL: its own hash
	} {
		file := filepath.Join(folder, c.name)
		if err := os.WriteFile(file, bytes, 0o644); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(file)
		if err != nil {
			t.Fatal(err)
		}
		hashed := staticNameCarriesItsHash("/assets/"+c.name, file, info)
		staticHashes.mu.Lock()
		_, read := staticHashes.files[file]
		staticHashes.mu.Unlock()
		if read != c.read || hashed != c.hashed {
			t.Errorf("%s: read for its hash %v (want %v), kept as hashed %v (want %v)", c.name, read, c.read, hashed, c.hashed)
		}
	}
}

// Requests that ask for one file's hash while it is being read wait for that read (fixed 14/10/2026: until
// then each of N first requests read the whole file, N times). CONTROL: every request gets the hash;
// a later one reads nothing.
func TestStaticDigestsReadAFileOnceAtATime(t *testing.T) {
	file := filepath.Join(t.TempDir(), "app.abcdef01.js")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	var reads atomic.Int32
	entered, release := make(chan struct{}, 8), make(chan struct{})
	store := &staticDigests{read: func(string) (string, bool) {
		reads.Add(1)
		entered <- struct{}{}
		<-release
		return "abcdef01", true
	}}
	var group sync.WaitGroup
	answers := make([]string, 8)
	for i := range answers {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			answers[i], _ = store.digest(file, info)
		}(i)
	}
	<-entered                          // the first read has started
	time.Sleep(100 * time.Millisecond) // the other seven ask meanwhile
	close(release)
	group.Wait()
	if n := reads.Load(); n != 1 {
		t.Errorf("eight requests at once read the file %d times, want once", n)
	}
	for i, answer := range answers {
		if answer != "abcdef01" {
			t.Errorf("CONTROL: request %d got %q", i, answer)
		}
	}
	store.digest(file, info)
	if n := reads.Load(); n != 1 {
		t.Errorf("CONTROL: a later request reads nothing, reads = %d", n)
	}
}

// A full store lets one file go for a new one (fixed 14/10/2026: until then it started over, every file of
// the process read again). CONTROL: the new file is kept.
func TestStaticDigestsLetOneGoWhenFull(t *testing.T) {
	store := &staticDigests{files: map[string]staticDigest{}, read: func(string) (string, bool) { return "abcdef01", true }}
	for i := 0; i < staticDigestBound; i++ {
		store.files["file-"+strconv.Itoa(i)] = staticDigest{digest: "0"}
	}
	file := filepath.Join(t.TempDir(), "new.abcdef01.js")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	store.digest(file, info)
	if _, kept := store.files[file]; !kept || len(store.files) != staticDigestBound {
		t.Errorf("a full store keeps %d files and the new one (%v), want %d: one let go", len(store.files), kept, staticDigestBound)
	}
}
