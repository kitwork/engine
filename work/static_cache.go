package work

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

// A static file of a site (assets/…, /favicon.ico, any safe file a site with no router.assets serves)
// is answered by http.ServeFile, which writes Last-Modified and answers If-Modified-Since with 304 —
// and, until 14/10/2026, nothing else: no Cache-Control, so a browser chose by itself how long to keep
// /assets/logo.svg (Lighthouse on the kitwork example named it). staticCacheHeaders writes the rest
// before ServeFile answers:
//
//   - ETag: the file's modification time and size, as most servers spell it ("<mtime>-<size>" in hex),
//     weak (W/): utilities/compress gzips a text file of a kilobyte or more after this is written, and
//     a strong ETag names one representation only (RFC 9110 §8.8.3), not the gzipped bytes and the
//     plain ones at once. http.ServeFile compares If-None-Match weakly, so it answers it with 304 when
//     it matches. With Last-Modified, a browser that asks again is told "not modified" without the
//     bytes.
//   - Cache-Control, by what the name promises:
//     a name that carries the hash of its bytes (logo.3f2a9c1be04d77a1.svg, the engine's own way of
//     naming, checked against the bytes: staticNameCarriesItsHash) can never mean other bytes, so it
//     is kept for a year and never asked again (immutable);
//     any other name may mean new bytes after an edit or a deploy: kept for an hour in production
//     (staticLifetime), then asked again (304 when unchanged); while developing (AllowLocal) asked on
//     every use (no-cache), so an edited file shows on the next load.
func staticCacheHeaders(w http.ResponseWriter, urlPath, file string, info os.FileInfo) {
	header := w.Header()
	header.Set("ETag", `W/"`+strconv.FormatInt(info.ModTime().UnixNano(), 16)+"-"+strconv.FormatInt(info.Size(), 16)+`"`)
	switch {
	case staticNameCarriesItsHash(urlPath, file, info):
		header.Set("Cache-Control", "public, max-age=31536000, immutable")
	case AllowLocal:
		header.Set("Cache-Control", "no-cache")
	default:
		header.Set("Cache-Control", "public, max-age="+strconv.Itoa(int(staticLifetime/time.Second)))
	}
}

// staticLifetime is how long a browser keeps a static file whose name carries no hash before asking
// again: an hour. Long enough that the pages of one visit never fetch the logo twice, short enough that
// a file edited in place reaches every visitor within the hour; the ETag and Last-Modified make the
// question after that cheap (a 304 without the bytes).
const staticLifetime = time.Hour

// staticNameCarriesItsHash reports whether a static file's name carries the hash of its bytes: one of
// the parts of its name between dots or dashes (logo.3f2a9c1be04d77a1.svg, app-3f2a9c1b.js) is at
// least 8 lowercase hex digits, a letter among them, that start the SHA-256 of the file. Checked
// against the bytes, never guessed from the spelling: keeping a name a year that does not carry its
// hash would keep the old file after an edit. A part of digits only is a date, a time or a counter
// (logo.20261014.svg, 1719557903791117408_photo.jpg), never read for it: until the fix of 14/10/2026 the first
// request for every such upload read the whole file before its first byte went out. (A hash of
// digits only, about one name in two thousand of the engine's 16 digits, is then kept an hour.) The
// answer is kept per file, size and time of change (staticHashes).
func staticNameCarriesItsHash(urlPath, file string, info os.FileInfo) bool {
	name := path.Base(urlPath)
	if dot := strings.LastIndexByte(name, '.'); dot > 0 {
		name = name[:dot] // the extension is never the hash
	}
	var candidates []string
	for _, part := range strings.FieldsFunc(name, func(r rune) bool { return r == '.' || r == '-' || r == '_' }) {
		if len(part) >= 8 && len(part) <= 64 && isLowerHex(part) && strings.ContainsAny(part, "abcdef") {
			candidates = append(candidates, part)
		}
	}
	if len(candidates) == 0 {
		return false
	}
	digest, ok := staticHashes.digest(file, info)
	if !ok {
		return false
	}
	for _, candidate := range candidates {
		if strings.HasPrefix(digest, candidate) {
			return true
		}
	}
	return false
}

func isLowerHex(s string) bool {
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// staticDigests keeps the SHA-256 of the static files whose names look hashed, by path, size and time
// of change, so a file is read for it once and again only after it changes; requests that ask for one
// while it is being read wait for that read (reading) instead of reading it again. At most
// staticDigestBound files: past that one is let go for each new one (the names that look hashed are
// few on a site; the store is the process's, every site's).
type staticDigests struct {
	mu      sync.Mutex
	files   map[string]staticDigest
	reading map[string]*staticReading // by path, size and time of change
	// read hashes a file (fileDigest when nil): a test counts the reads
	read func(file string) (string, bool)
}

// staticReading is one read of a file under way: its answer once done is closed.
type staticReading struct {
	done   chan struct{}
	digest string
	ok     bool
}

type staticDigest struct {
	size    int64
	changed time.Time
	digest  string
}

const staticDigestBound = 4096

var staticHashes staticDigests

func (s *staticDigests) digest(file string, info os.FileInfo) (string, bool) {
	key := file + "\x00" + strconv.FormatInt(info.Size(), 10) + "\x00" + strconv.FormatInt(info.ModTime().UnixNano(), 10)
	s.mu.Lock()
	if known, ok := s.files[file]; ok && known.size == info.Size() && known.changed.Equal(info.ModTime()) {
		s.mu.Unlock()
		return known.digest, true
	}
	if under, ok := s.reading[key]; ok {
		s.mu.Unlock()
		<-under.done
		return under.digest, under.ok
	}
	reading := &staticReading{done: make(chan struct{})}
	if s.reading == nil {
		s.reading = map[string]*staticReading{}
	}
	s.reading[key] = reading
	s.mu.Unlock()

	read := s.read
	if read == nil {
		read = fileDigest
	}
	reading.digest, reading.ok = read(file)

	s.mu.Lock()
	delete(s.reading, key)
	if reading.ok {
		if s.files == nil {
			s.files = map[string]staticDigest{}
		}
		if _, held := s.files[file]; !held && len(s.files) >= staticDigestBound {
			for other := range s.files { // one let go, whichever: the others stay
				delete(s.files, other)
				break
			}
		}
		s.files[file] = staticDigest{size: info.Size(), changed: info.ModTime(), digest: reading.digest}
	}
	s.mu.Unlock()
	close(reading.done)
	return reading.digest, reading.ok
}

// fileDigest is the SHA-256 of a file's bytes, hex.
func fileDigest(file string) (string, bool) {
	f, err := os.Open(file)
	if err != nil {
		return "", false
	}
	defer f.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return "", false
	}
	return hex.EncodeToString(sum.Sum(nil)), true
}
