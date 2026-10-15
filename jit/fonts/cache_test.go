package fonts

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A family whose files go away under a running process — a cache folder the system or someone cleared
// — is not written into pages any more, and the next Ensure downloads it again. The same when one face
// file is gone and the manifest is still there: FileAt finds it gone, and the family is forgotten.
func TestACacheClearedUnderARunningProcessIsDownloadedAgain(t *testing.T) {
	g := newFakeGoogle(t)
	if err := Ensure("Itim"); err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(filepath.Join(g.dir, "itim"))
	if css, err := FaceCSS("Itim", Need{}); err == nil {
		t.Errorf("a family whose folder is gone is written into the page:\n%s", css)
	}
	if err := Ensure("Itim"); err != nil || g.cssRequests.Load() != 2 {
		t.Fatalf("the next Ensure downloads it again: %v, %d css requests", err, g.cssRequests.Load())
	}
	css, err := FaceCSS("Itim", Need{})
	if err != nil {
		t.Fatal(err)
	}
	srcs := served(css)
	latin := ""
	for _, src := range srcs {
		if strings.Contains(src, "/itim-400-latin.") {
			latin = src
		}
	}
	os.Remove(filepath.Join(g.dir, "itim", "400-latin.woff2"))
	if file, _ := FileAt(latin); file != nil {
		t.Errorf("a face whose file is gone is served")
	}
	if err := Ensure("Itim"); err != nil || g.cssRequests.Load() != 3 {
		t.Fatalf("a family with a face gone is downloaded again: %v, %d css requests", err, g.cssRequests.Load())
	}
	for _, src := range srcs {
		if file, exact := FileAt(src); file == nil || !exact {
			t.Errorf("%s is not served again", src)
		}
	}
}

// With no cache folder of its own (a service with no HOME) and none set by the host, nothing is
// downloaded, and the error says what to set. The shared temp folder is never the cache: on Linux every
// user of the machine can write there, and a family planted in it would be served as Google's.
func TestNoCacheFolderIsNeverTheTempFolder(t *testing.T) {
	g := newFakeGoogle(t)
	temp := t.TempDir()
	for _, key := range []string{"TMP", "TEMP", "TMPDIR"} {
		t.Setenv(key, temp)
	}
	for _, key := range []string{"XDG_CACHE_HOME", "HOME", "LocalAppData"} {
		t.Setenv(key, "")
	}
	body := []byte("wOF2 planted glyphs")
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	planted := filepath.Join(temp, "kitwork", "fonts", "itim")
	os.MkdirAll(planted, 0o755)
	os.WriteFile(filepath.Join(planted, "400-latin.woff2"), body, 0o644)
	os.WriteFile(filepath.Join(planted, manifestName), []byte(`{"version":1,"family":"Itim","slug":"itim","faces":[{"file":"400-latin.woff2","style":"normal","weight":[400,400],"subset":"latin","size":19,"sha256":"`+digest+`"}]}`), 0o644)

	SetCacheDir("")
	if dir := CacheDir(); dir != "" {
		t.Fatalf("with no cache folder of its own the cache is %s", dir)
	}
	err := Ensure("Itim")
	if err == nil || !strings.Contains(err.Error(), `không có thư mục để lưu họ chữ "Itim"`) || !strings.Contains(err.Error(), "SetCacheDir") {
		t.Errorf("the error says what to set: %v", err)
	}
	if file, _ := FileAt("/_kitwork/fonts/itim-400-latin." + digest[:16] + ".woff2"); file != nil {
		t.Errorf("a family planted in the temp folder is served: %q", file.Body)
	}
	if n := g.dials.Load(); n != 0 {
		t.Errorf("with nowhere to put it, the family was fetched: %d dials", n)
	}
}

// After a restart, many visitors asking at once for the faces of a family on disk that the process has
// not read yet cost one read of it, not one each; so do many pages compiled at once.
func TestColdReadsOfAFamilyAreOne(t *testing.T) {
	g := newFakeGoogle(t)
	if err := Ensure("Inter"); err != nil {
		t.Fatal(err)
	}
	css, err := FaceCSS("Inter", Need{})
	if err != nil {
		t.Fatal(err)
	}
	srcs := served(css)
	family := filepath.Join(g.dir, "inter")
	var reads atomic.Int32
	readHook = func(dir string) {
		if dir == family {
			reads.Add(1)
			time.Sleep(50 * time.Millisecond)
		}
	}
	t.Cleanup(func() { readHook = nil })

	SetCacheDir(g.dir) // a new process: nothing read yet
	var wg sync.WaitGroup
	for i := range 64 {
		wg.Go(func() {
			src := srcs[i%len(srcs)]
			if file, exact := FileAt(src); file == nil || !exact {
				t.Errorf("%s is not served", src)
			}
		})
	}
	wg.Wait()
	if n := reads.Load(); n != 1 {
		t.Errorf("64 visitors read the family %d times, want 1", n)
	}

	SetCacheDir(g.dir)
	reads.Store(0)
	for range 16 {
		wg.Go(func() {
			if _, err := FaceCSS("Inter", Need{}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if n := reads.Load(); n != 1 {
		t.Errorf("16 pages read the family %d times, want 1", n)
	}
}

// A downloaded face is read from disk and checked once, then served from memory like an embedded one:
// another request for it — a HEAD, a 304, an old hash — costs no read. The memory held is bounded, the
// face served least lately put out first.
func TestDownloadedFacesAreHeldInMemory(t *testing.T) {
	newFakeGoogle(t)
	if err := Ensure("Itim"); err != nil {
		t.Fatal(err)
	}
	css, _ := FaceCSS("Itim", Need{})
	src := served(css)[0]
	first, _ := FileAt(src)
	second, _ := FileAt(src)
	old, exact := FileAt(regexp.MustCompile(`\.[0-9a-f]{16}\.woff2$`).ReplaceAllString(src, "."+strings.Repeat("0", 16)+".woff2"))
	if first == nil || second == nil || old == nil || exact {
		t.Fatalf("%s is not served", src)
	}
	if &first.Body[0] != &second.Body[0] || &old.Body[0] != &first.Body[0] {
		t.Errorf("each request read the face from disk again")
	}

	c := newBodyCache(10)
	c.put("a", []byte("aaaa"))
	c.put("b", []byte("bbbb"))
	c.get("a")
	c.put("c", []byte("cccc"))
	if c.get("b") != nil || c.get("a") == nil || c.get("c") == nil || c.used > c.limit {
		t.Errorf("the body served least lately goes first, within the limit: used %d of %d", c.used, c.limit)
	}
	c.put("d", make([]byte, 11))
	if c.get("d") != nil || c.used > c.limit {
		t.Errorf("a body over the limit is not held")
	}
}

// The host moving the cache folder while a family downloads (SetCacheDir) does not make Ensure's nil a
// lie: the family is brought into the new folder, and FaceCSS works right after.
func TestEnsureWhileTheCacheFolderMoves(t *testing.T) {
	g := newFakeGoogle(t)
	arrived, release := make(chan struct{}), make(chan struct{})
	var first atomic.Bool
	g.set(func(g *fakeGoogle) {
		g.cssHook = func(http.ResponseWriter, *http.Request) bool {
			if first.CompareAndSwap(false, true) {
				close(arrived)
				<-release
			}
			return false
		}
	})
	done := make(chan error, 1)
	go func() { done <- Ensure("Itim") }()
	<-arrived
	moved := t.TempDir()
	SetCacheDir(moved)
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if css, err := FaceCSS("Itim", Need{}); err != nil || len(served(css)) != 4 {
		t.Errorf("Ensure returned nil, so FaceCSS works: %v", err)
	}
	if _, err := os.Stat(filepath.Join(moved, "itim", manifestName)); err != nil {
		t.Errorf("the family is in the new folder: %v", err)
	}
	if n := g.cssRequests.Load(); n != 2 {
		t.Errorf("%d css requests, want 2: one per folder", n)
	}
}
