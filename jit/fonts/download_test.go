package fonts

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeGoogle answers as fonts.googleapis.com, fonts.gstatic.com and evil.example, over TLS with a
// certificate for those names. The downloader reaches it through transportHook, which only moves the
// connection: every address keeps its real host, so the host checks, the redirect check and the TLS
// name check are the ones the server runs. The CSS answers are real ones, recorded in testdata
// (css2-itim.css static, css2-inter.css variable); a woff2 is a few bytes that start as one does.
type fakeGoogle struct {
	t      *testing.T
	server *httptest.Server
	dir    string // the cache folder

	mu       sync.Mutex
	css      map[string]string // family name → the CSS Google answers with
	queries  []string          // every css2 query asked, in order
	cssHook  func(http.ResponseWriter, *http.Request) bool
	fontHook func(http.ResponseWriter, *http.Request) bool
	delay    time.Duration

	cssRequests, fontRequests, evilRequests, dials atomic.Int32
	offline                                        atomic.Bool
}

func fakeWoff2(path string) []byte { return []byte("wOF2 fake face " + path) }

func newFakeGoogle(t *testing.T) *fakeGoogle {
	t.Helper()
	g := &fakeGoogle{t: t, css: map[string]string{}}
	for name, file := range map[string]string{"Itim": "css2-itim.css", "Inter": "css2-inter.css"} {
		data, err := os.ReadFile(filepath.Join("testdata", file))
		if err != nil {
			t.Fatal(err)
		}
		g.css[name] = string(data)
	}
	g.server = httptest.NewUnstartedServer(http.HandlerFunc(g.serve))
	g.server.TLS = &tls.Config{Certificates: []tls.Certificate{testCertificate(t, cssHost, fileHost, "evil.example", "fonts.gstatic.com.evil.example")}}
	g.server.StartTLS()
	pool := x509.NewCertPool()
	pool.AddCert(g.server.Certificate())
	address := g.server.Listener.Addr().String()
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			g.dials.Add(1)
			if g.offline.Load() {
				return nil, &net.OpError{Op: "dial", Net: network, Err: errors.New("network is unreachable")}
			}
			var dialer net.Dialer
			return dialer.DialContext(ctx, "tcp", address)
		},
		TLSClientConfig: &tls.Config{RootCAs: pool},
	}
	previousHook, previousDir, previousHeld := transportHook, cacheSetting(), held
	transportHook = transport
	held = newBodyCache(previousHeld.limit) // a new process: no face held in memory yet
	g.dir = t.TempDir()
	SetCacheDir(g.dir)
	t.Cleanup(func() {
		transport.CloseIdleConnections()
		g.server.Close()
		transportHook = previousHook
		held = previousHeld
		SetCacheDir(previousDir)
	})
	return g
}

func cacheSetting() string {
	cacheMu.RLock()
	defer cacheMu.RUnlock()
	return cacheDir
}

// testCertificate is a self-signed certificate for the names given.
func testCertificate(t *testing.T, names ...string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: names[0]},
		DNSNames:              names,
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func (g *fakeGoogle) serve(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	cssHook, fontHook, delay := g.cssHook, g.fontHook, g.delay
	g.mu.Unlock()
	switch r.Host {
	case cssHost:
		g.cssRequests.Add(1)
		time.Sleep(delay)
		if cssHook != nil && cssHook(w, r) {
			return
		}
		g.mu.Lock()
		g.queries = append(g.queries, r.URL.RawQuery)
		name, _, _ := strings.Cut(strings.TrimPrefix(r.URL.RawQuery, "family="), ":")
		css, ok := g.css[strings.ReplaceAll(name, "+", " ")]
		g.mu.Unlock()
		if !ok {
			http.Error(w, "<!doctype html><title>Error 400 (Bad Request)</title>", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		io.WriteString(w, css)
	case fileHost:
		g.fontRequests.Add(1)
		if fontHook != nil && fontHook(w, r) {
			return
		}
		w.Header().Set("Content-Type", "font/woff2")
		w.Write(fakeWoff2(r.URL.Path))
	default:
		g.evilRequests.Add(1)
		w.Header().Set("Content-Type", "font/woff2")
		w.Write(fakeWoff2("/evil"))
	}
}

func (g *fakeGoogle) set(change func(g *fakeGoogle)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	change(g)
}

func (g *fakeGoogle) query(i int) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if i >= len(g.queries) {
		return ""
	}
	return g.queries[i]
}

// entries lists what is in the cache folder, recursively, as slash paths.
func (g *fakeGoogle) entries() []string {
	var out []string
	filepath.WalkDir(g.dir, func(path string, d os.DirEntry, err error) error {
		if err == nil && path != g.dir {
			rel, _ := filepath.Rel(g.dir, path)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// served are the src URLs of an @font-face block, in order.
func served(css string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`src:url\(([^)]+)\)`).FindAllStringSubmatch(css, -1) {
		out = append(out, m[1])
	}
	return out
}

// A family is downloaded once into the cache — one folder, a manifest and one file per face named by
// numbers and a subset word — then served exactly like an embedded one: the same name form, its bytes
// in the hash, found by FileAt; FaceCSS ships the faces a page needs. A second Ensure asks nothing;
// a new process (the cache forgotten, the network gone) reads it back from disk.
func TestEnsureDownloadsOnceCachesAndServes(t *testing.T) {
	g := newFakeGoogle(t)
	if err := Ensure("itim"); err != nil {
		t.Fatal(err)
	}
	if q := g.query(0); q != "family=Itim:wght@400&display=swap" {
		t.Errorf("css2 asked for %q", q)
	}
	want := []string{"itim", "itim/400-latin-ext.woff2", "itim/400-latin.woff2", "itim/400-thai.woff2", "itim/400-vietnamese.woff2", "itim/manifest.json"}
	if got := g.entries(); !equalStrings(got, want) {
		t.Fatalf("the cache holds %v, want %v", got, want)
	}
	if n := g.fontRequests.Load(); n != 4 {
		t.Errorf("%d font requests, want 4", n)
	}

	css, err := FaceCSS("Itim", Need{Weights: []int{400}})
	if err != nil {
		t.Fatal(err)
	}
	srcs := served(css)
	if len(srcs) != 4 || strings.Count(css, "@font-face{font-family:'Itim';font-style:normal;font-weight:400;font-display:swap;src:url(") != 4 {
		t.Fatalf("four faces, one per subset:\n%s", css)
	}
	cachedName := regexp.MustCompile(`^/_kitwork/fonts/itim-400-(thai|vietnamese|latin-ext|latin)\.[0-9a-f]{16}\.woff2$`)
	for _, src := range srcs {
		if !cachedName.MatchString(src) {
			t.Errorf("%s is not served as /_kitwork/fonts/itim-400-<subset>.<hash>.woff2", src)
		}
		file, exact := FileAt(src)
		if file == nil || !exact || file.Path != src {
			t.Fatalf("%s does not resolve to its file", src)
		}
		sum := sha256.Sum256(file.Body)
		if !bytes.HasPrefix(file.Body, []byte("wOF2 fake face /s/itim/")) || file.Digest != hex.EncodeToString(sum[:]) || !strings.Contains(src, file.Digest[:16]) {
			t.Errorf("%s: body %q digest %s", src, file.Body, file.Digest)
		}
	}
	if !strings.Contains(css, ";unicode-range:U+0102-0103, U+0110-0111") {
		t.Errorf("Google's unicode-range rides along:\n%s", css)
	}
	if only, _ := FaceCSS("Itim", Need{Weights: []int{400}, Subsets: []string{"latin", "vietnamese"}}); len(served(only)) != 2 ||
		!strings.Contains(only, "itim-400-latin.") || !strings.Contains(only, "itim-400-vietnamese.") || strings.Contains(only, "thai") {
		t.Errorf("the subsets asked for, and no other:\n%s", only)
	}
	if bold, _ := FaceCSS("Itim", Need{Weights: []int{700}}); len(served(bold)) != 4 || !strings.Contains(bold, "font-weight:400;") {
		t.Errorf("a weight Itim lacks ships the face it is drawn from:\n%s", bold)
	}
	if preload := PreloadFace("Itim", Need{Weights: []int{400}}); !strings.Contains(preload, `href="/_kitwork/fonts/itim-400-latin.`) {
		t.Errorf("preload the latin face: %s", preload)
	}

	css2, fonts2, dials2 := g.cssRequests.Load(), g.fontRequests.Load(), g.dials.Load()
	if err := Ensure("Itim"); err != nil || g.cssRequests.Load() != css2 || g.fontRequests.Load() != fonts2 || g.dials.Load() != dials2 {
		t.Errorf("a second Ensure asked the network: %v", err)
	}

	SetCacheDir(g.dir) // a new process: nothing read yet
	g.offline.Store(true)
	if file, exact := FileAt(srcs[0]); file == nil || !exact {
		t.Errorf("a face in the cache is served before anything asked for its family")
	}
	SetCacheDir(g.dir)
	if err := Ensure("Itim"); err != nil {
		t.Errorf("the cache serves without the network: %v", err)
	}
	if again, err := FaceCSS("Itim", Need{Weights: []int{400}}); err != nil || again != css {
		t.Errorf("the faces read back are the faces downloaded: %v", err)
	}
	if n := g.dials.Load(); n != dials2 {
		t.Errorf("reading the cache dialed %d times", n-dials2)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// A variable family asks for its whole axes and comes back one file per subset and style; a page gets
// one face per subset with the weight range, whatever weights it sets.
func TestEnsureVariableFamily(t *testing.T) {
	g := newFakeGoogle(t)
	if err := Ensure("Inter"); err != nil {
		t.Fatal(err)
	}
	if q := g.query(0); q != "family=Inter:ital,opsz,wght@0,14..32,100..900;1,14..32,100..900&display=swap" {
		t.Errorf("css2 asked for %q", q)
	}
	var files []string
	for _, e := range g.entries() {
		if strings.HasSuffix(e, ".woff2") {
			files = append(files, strings.TrimPrefix(e, "inter/"))
		}
	}
	if len(files) != 14 || !contains(files, "100-900-latin.woff2") || !contains(files, "100-900-vietnamese-italic.woff2") {
		t.Fatalf("one file per subset and style: %v", files)
	}
	css, err := FaceCSS("Inter", Need{Weights: []int{400, 700}, Subsets: []string{"latin", "vietnamese"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(css, "@font-face{font-family:'Inter';font-style:normal;font-weight:100 900;font-display:swap;") != 2 || len(served(css)) != 2 {
		t.Errorf("one face per subset, the whole range:\n%s", css)
	}
	if italic, _ := FaceCSS("Inter", Need{Weights: []int{400}, Subsets: []string{"latin", "vietnamese"}, Italic: true}); len(served(italic)) != 4 ||
		!strings.Contains(italic, "font-style:italic;font-weight:100 900;") {
		t.Errorf("italic text ships the italic faces:\n%s", italic)
	}
	if all, _ := FaceCSS("Inter", Need{}); len(served(all)) != 7 {
		t.Errorf("no subsets named ships every upright one: %d", len(served(all)))
	}
}

// The embedded families need neither the network nor the cache: with the cache folder unusable and
// the network gone they are ready, their faces are written and served.
func TestEmbeddedFamiliesNeedNoCache(t *testing.T) {
	g := newFakeGoogle(t)
	g.offline.Store(true)
	blocked := filepath.Join(g.dir, "a-file")
	os.WriteFile(blocked, []byte("not a folder"), 0o644)
	SetCacheDir(blocked)
	for _, name := range []string{DefaultBody, DefaultCode, "JetBrains Mono", "Outfit", "Fira Code"} {
		if err := Ensure(name); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	css, err := FaceCSS(DefaultBody, Need{Weights: []int{400, 700}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(css, "@font-face{font-family:'Google Sans Flex';font-style:normal;font-weight:1 1000;font-display:swap;") != 3 {
		t.Errorf("Google Sans Flex: one face per embedded subset, 1–1000:\n%s", css)
	}
	for _, src := range served(css) {
		file, exact := FileAt(src)
		if file == nil || !exact || !bytes.HasPrefix(file.Body, []byte("wOF2")) {
			t.Errorf("%s is not served from the embed", src)
		}
	}
	if outfit, _ := FaceCSS("Outfit", Need{Weights: []int{400}}); strings.Count(outfit, "font-weight:100 900;") != 2 || !strings.Contains(outfit, "/outfit-400-latin.") {
		t.Errorf("Outfit, one file per subset for its whole axis:\n%s", outfit)
	}
	if code, _ := FaceCSS(DefaultCode, Need{Weights: []int{400}, Italic: true}); strings.Count(code, "@font-face{font-family:'Google Sans Code';font-style:normal;font-weight:300 800;") != 3 || strings.Contains(code, "italic") {
		t.Errorf("Google Sans Code, 300–800, no italic embedded:\n%s", code)
	}
	if mono, _ := FaceCSS("JetBrains Mono", Need{Weights: []int{400}, Italic: true}); strings.Count(mono, "font-weight:100 800;") != 3 || strings.Contains(mono, "italic") {
		t.Errorf("JetBrains Mono, 100–800, no italic embedded:\n%s", mono)
	}
	if preload := PreloadFace(DefaultBody, Need{}); !strings.Contains(preload, "/_kitwork/fonts/google-sans-flex-1-1000-latin.") {
		t.Errorf("preload: %s", preload)
	}
	if preload := PreloadFace(DefaultCode, Need{}); !strings.Contains(preload, "/_kitwork/fonts/google-sans-code-300-800-latin.") {
		t.Errorf("preload of the code face: %s", preload)
	}
	if n := g.dials.Load(); n != 0 {
		t.Errorf("an embedded family dialed %d times", n)
	}
	if _, err := FaceCSS("Itim", Need{}); err == nil || !strings.Contains(err.Error(), "chưa được tải về") {
		t.Errorf("a family not downloaded says so: %v", err)
	}
}

// FileAt finds an embedded face first: a folder in the cache named like an embedded family is never
// read, and never answers in its place.
func TestEmbeddedFacesComeBeforeTheCache(t *testing.T) {
	g := newFakeGoogle(t)
	embedded := routePath("families/outfit/400-latin.woff2")
	fake := []byte("wOF2 not outfit")
	sum := sha256.Sum256(fake)
	digest := hex.EncodeToString(sum[:])
	os.MkdirAll(filepath.Join(g.dir, "outfit"), 0o755)
	os.WriteFile(filepath.Join(g.dir, "outfit", "400-latin.woff2"), fake, 0o644)
	os.WriteFile(filepath.Join(g.dir, "outfit", "manifest.json"), []byte(`{"version":1,"family":"Outfit","slug":"outfit","faces":[{"file":"400-latin.woff2","style":"normal","weight":[400,400],"subset":"latin","size":15,"sha256":"`+digest+`"}]}`), 0o644)
	file, exact := FileAt(embedded)
	want, _ := FS.ReadFile("families/outfit/400-latin.woff2")
	if file == nil || !exact || !bytes.Equal(file.Body, want) {
		t.Errorf("the embedded face answers")
	}
	if file, _ := FileAt("/_kitwork/fonts/outfit-400-latin." + digest[:16] + ".woff2"); file == nil || !bytes.Equal(file.Body, want) {
		t.Errorf("the cache copy's name gets the embedded face (an old hash), never the cache's bytes")
	}
}

// Many callers at once — one download. Run with -race: FaceCSS and FileAt read the cache while it is
// written.
func TestEnsureConcurrentDownloadsOnce(t *testing.T) {
	g := newFakeGoogle(t)
	g.set(func(g *fakeGoogle) { g.delay = 150 * time.Millisecond })
	var wg sync.WaitGroup
	errs := make([]error, 32)
	done := make(chan struct{})
	for i := range errs {
		wg.Go(func() { errs[i] = Ensure("Itim") })
	}
	var readers sync.WaitGroup
	for range 4 {
		readers.Go(func() {
			for {
				select {
				case <-done:
					return
				default:
				}
				FaceCSS("Itim", Need{Weights: []int{400}})
				FileAt("/_kitwork/fonts/itim-400-latin." + strings.Repeat("0", 16) + ".woff2")
			}
		})
	}
	wg.Wait()
	close(done)
	readers.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("caller %d: %v", i, err)
		}
	}
	if c, f := g.cssRequests.Load(), g.fontRequests.Load(); c != 1 || f != 4 {
		t.Errorf("%d css and %d font requests for 32 callers, want 1 and 4", c, f)
	}
}

// Offline the first time: the error says the network is needed once and where the family will live;
// nothing is left behind, and the next try, online, works.
func TestEnsureOfflineSaysWhatToDo(t *testing.T) {
	g := newFakeGoogle(t)
	g.offline.Store(true)
	err := Ensure("Itim")
	if err == nil {
		t.Fatal("offline, yet the download succeeded")
	}
	for _, want := range []string{
		`không tải được họ chữ "Itim" từ Google Fonts`,
		"Lần đầu một họ chữ được dùng, máy chủ cần mạng để tải nó về một lần",
		"sau đó họ chữ nằm ở " + filepath.Join(g.dir, "itim") + " và chạy không cần mạng",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error lacks %q:\n%v", want, err)
		}
	}
	if left := g.entries(); len(left) != 0 {
		t.Errorf("a failed download left %v", left)
	}
	g.offline.Store(false)
	if err := Ensure("Itim"); err != nil {
		t.Errorf("online again: %v", err)
	}
}

// What Kitwork will not take, each refused with nothing written: a face on another host, over http,
// on a look-alike host or another port; a redirect away from Google or to http; a body that is not a
// woff2; a face over 5 MiB (with and without a length); a family over its limit; a family that is not
// open source; names shaped like paths, in the request or in Google's answer; a server that does not
// answer.
func TestEnsureRefuses(t *testing.T) {
	itim, err := os.ReadFile(filepath.Join("testdata", "css2-itim.css"))
	if err != nil {
		t.Fatal(err)
	}
	firstSrc := regexp.MustCompile(`url\(https://fonts\.gstatic\.com/[^)]+\)`).FindString(string(itim))
	withSrc := func(src string) string { return strings.Replace(string(itim), firstSrc, "url("+src+")", 1) }
	answer := func(css string) func(*fakeGoogle) {
		return func(g *fakeGoogle) { g.css["Itim"] = css }
	}
	oversize := append([]byte("wOF2"), bytes.Repeat([]byte{0}, fileLimit-3)...)
	for _, c := range []struct {
		name   string
		family string
		setup  func(*fakeGoogle)
		want   string
	}{
		{"a face on another host", "Itim", answer(withSrc("https://evil.example/s/itim/v16/x.woff2")), `không tải "https://evil.example/`},
		{"a face over http", "Itim", answer(withSrc("http://fonts.gstatic.com/s/itim/v16/x.woff2")), `không tải "http://fonts.gstatic.com/`},
		{"a face on a look-alike host", "Itim", answer(withSrc("https://fonts.gstatic.com.evil.example/x.woff2")), "không tải"},
		{"a face on another port", "Itim", answer(withSrc("https://fonts.gstatic.com:8443/x.woff2")), "không tải"},
		{"a redirect away from Google", "Itim", func(g *fakeGoogle) {
			g.cssHook = func(w http.ResponseWriter, r *http.Request) bool {
				http.Redirect(w, r, "https://evil.example/css2?"+r.URL.RawQuery, http.StatusFound)
				return true
			}
		}, `chuyển hướng sang "https://evil.example/css2`},
		{"a redirect to http", "Itim", func(g *fakeGoogle) {
			g.fontHook = func(w http.ResponseWriter, r *http.Request) bool {
				http.Redirect(w, r, "http://fonts.gstatic.com"+r.URL.Path, http.StatusMovedPermanently)
				return true
			}
		}, `chuyển hướng sang "http://fonts.gstatic.com/`},
		{"a body that is not a woff2", "Itim", func(g *fakeGoogle) {
			g.fontHook = func(w http.ResponseWriter, r *http.Request) bool {
				if strings.HasSuffix(r.URL.Path, "ANAkA.woff2") {
					io.WriteString(w, "<!doctype html><title>Not a font</title>")
					return true
				}
				return false
			}
		}, "không phải woff2"},
		{"a face over 5 MiB, with its length", "Itim", func(g *fakeGoogle) {
			g.fontHook = func(w http.ResponseWriter, r *http.Request) bool {
				http.ServeContent(w, r, "x.woff2", time.Time{}, bytes.NewReader(oversize))
				return true
			}
		}, "lớn hơn 5 MiB"},
		{"a face over 5 MiB, streamed", "Itim", func(g *fakeGoogle) {
			g.fontHook = func(w http.ResponseWriter, r *http.Request) bool {
				for i := 0; i < len(oversize); i += 1 << 20 {
					w.Write(oversize[i:min(i+1<<20, len(oversize))])
					w.(http.Flusher).Flush()
				}
				return true
			}
		}, "lớn hơn 5 MiB"},
		{"a family over its limit", "Itim", func(*fakeGoogle) { setFamilyLimit(64) }, "nặng hơn"},
		{"a family that is not open source", "Closed Sans", nil, "không phải mã nguồn mở"},
		{"a name shaped like a path", "../../evil", nil, "không có trong Google Fonts"},
		{"a subset shaped like a path", "Itim", answer(strings.Replace(string(itim), "/* latin */", "/* ../../evil */", 1)), "không đọc được"},
		{"a weight shaped like a path", "Itim", answer(strings.Replace(string(itim), "font-weight: 400;", "font-weight: 400/../../evil;", 1)), "không đọc được"},
		{"a style shaped like a path", "Itim", answer(strings.Replace(string(itim), "font-style: normal;", "font-style: ../evil;", 1)), "không đọc được"},
		{"a unicode-range that is not one", "Itim", answer(strings.Replace(string(itim), "unicode-range: U+02D7,", "unicode-range: expression(alert(1)), U+02D7,", 1)), "không đọc được"},
		{"another family's faces", "Itim", answer(strings.ReplaceAll(string(itim), "'Itim'", "'Evil'")), "không đọc được"},
		{"a server that does not answer", "Itim", func(g *fakeGoogle) {
			setRequestTimeout(300 * time.Millisecond)
			g.delay = 3 * time.Second
		}, "Lần đầu một họ chữ được dùng"},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := newFakeGoogle(t)
			addClosedFamily(t)
			if c.setup != nil {
				g.set(c.setup)
			}
			err := Ensure(c.family)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want %q", err, c.want)
			}
			if left := g.entries(); len(left) != 0 {
				t.Errorf("a refused download left %v", left)
			}
			if n := g.evilRequests.Load(); n != 0 {
				t.Errorf("%d requests reached another host", n)
			}
			if strayed, _ := filepath.Glob(filepath.Join(filepath.Dir(g.dir), "*evil*")); len(strayed) != 0 {
				t.Errorf("written outside the cache: %v", strayed)
			}
			if c.family != "Itim" && g.dials.Load() != 0 {
				t.Errorf("a family that is never downloaded dialed %d times", g.dials.Load())
			}
		})
	}
}

// setFamilyLimit and setRequestTimeout change a limit for one test.
func setFamilyLimit(n int64) {
	previous := familyLimit
	familyLimit = n
	limitRestore = append(limitRestore, func() { familyLimit = previous })
}

func setRequestTimeout(d time.Duration) {
	previous := requestTimeout
	requestTimeout = d
	limitRestore = append(limitRestore, func() { requestTimeout = previous })
}

var limitRestore []func()

// addClosedFamily adds "Closed Sans", a family that is not open source, for one test; it also undoes
// the limits a case changed.
func addClosedFamily(t *testing.T) {
	google()
	previous := googleList
	list := append(append([]*Family(nil), previous...), &Family{Name: "Closed Sans", Slug: "closed-sans", Category: "Sans Serif",
		Subsets: []string{"latin"}, Weights: []int{400}, OpenSource: false})
	setGoogle(list)
	t.Cleanup(func() {
		setGoogle(previous)
		for _, restore := range limitRestore {
			restore()
		}
		limitRestore = nil
	})
}

// A face exactly at the limit is taken: the limit is 5 MiB, not less.
func TestEnsureTakesAFaceAtTheLimit(t *testing.T) {
	g := newFakeGoogle(t)
	limit := append([]byte("wOF2"), bytes.Repeat([]byte{0}, fileLimit-4)...)
	g.set(func(g *fakeGoogle) {
		g.fontHook = func(w http.ResponseWriter, r *http.Request) bool {
			w.Write(limit)
			return true
		}
	})
	if err := Ensure("Itim"); err != nil {
		t.Fatal(err)
	}
}

// A cache changed on disk is not served: a manifest that names a path, or a file whose bytes are not
// the manifest's. The next Ensure downloads the family again in its place.
func TestTamperedCacheIsNotServed(t *testing.T) {
	g := newFakeGoogle(t)
	if err := Ensure("Itim"); err != nil {
		t.Fatal(err)
	}
	css, _ := FaceCSS("Itim", Need{Weights: []int{400}})
	srcs := served(css)

	face := filepath.Join(g.dir, "itim", "400-latin.woff2")
	original, _ := os.ReadFile(face)
	os.WriteFile(face, []byte("wOF2 changed"), 0o644)
	for _, src := range srcs {
		file, _ := FileAt(src)
		if strings.Contains(src, "itim-400-latin.") && file != nil {
			t.Errorf("a face whose bytes changed on disk is served")
		}
		if !strings.Contains(src, "itim-400-latin.") && file == nil {
			t.Errorf("an untouched face is not served")
		}
	}
	os.WriteFile(face, original, 0o644)

	manifestPath := filepath.Join(g.dir, "itim", manifestName)
	data, _ := os.ReadFile(manifestPath)
	os.WriteFile(manifestPath, bytes.Replace(data, []byte(`"file": "400-latin.woff2"`), []byte(`"file": "../../evil.woff2"`), 1), 0o644)
	// the path it names leads out of the cache, to a file with the very bytes the manifest expects
	outside := filepath.Join(filepath.Dir(g.dir), "evil.woff2")
	os.WriteFile(outside, original, 0o644)
	defer os.Remove(outside)
	SetCacheDir(g.dir) // a new process
	if _, err := FaceCSS("Itim", Need{}); err == nil {
		t.Errorf("a manifest that names a path is read")
	}
	if file, _ := FileAt(srcs[0]); file != nil {
		t.Errorf("a face of a manifest that names a path is served")
	}
	if err := Ensure("Itim"); err != nil {
		t.Fatal(err)
	}
	if n := g.cssRequests.Load(); n != 2 {
		t.Errorf("the broken copy is downloaded again: %d css requests", n)
	}
	if again, err := FaceCSS("Itim", Need{Weights: []int{400}}); err != nil || again != css {
		t.Errorf("the family is whole again: %v", err)
	}
	if left := g.entries(); len(left) != 6 {
		t.Errorf("the old copy is gone: %v", left)
	}
}

// A path is only ever a key: nothing outside the faces is reached through FileAt.
func TestFileAtReachesOnlyFaces(t *testing.T) {
	newFakeGoogle(t)
	if err := Ensure("Itim"); err != nil {
		t.Fatal(err)
	}
	zeros := strings.Repeat("0", 16)
	for _, path := range []string{
		"/_kitwork/fonts/../../etc/passwd." + zeros + ".woff2",
		"/_kitwork/fonts/itim-400-latin/../manifest." + zeros + ".woff2",
		"/_kitwork/fonts/itim-manifest." + zeros + ".woff2",
		"/_kitwork/fonts/itim-..-..-manifest." + zeros + ".woff2",
		"/_kitwork/fonts/itim-400-latin.json." + zeros + ".woff2",
		"/jitfonts/itim/400-latin.woff2",
		"/_kitwork/fonts/itim/400-latin.woff2",
	} {
		if file, _ := FileAt(path); file != nil {
			t.Errorf("%s reached %s", path, file.Path)
		}
	}
	if file, exact := FileAt("/_kitwork/fonts/itim-400-latin." + zeros + ".woff2"); file == nil || exact {
		t.Errorf("an old hash of a cached face gets today's file, not as its name")
	}
}

// Google's CSS in every shape it comes in: a CJK family's numbered slices without a comment (today)
// or with one ("/* [12] */", before), a "fallback" face without a unicode-range.
func TestParseFacesSlicesAndFallback(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "css2-noto-sans-jp-excerpt.css"))
	if err != nil {
		t.Fatal(err)
	}
	jp, _ := Find("Noto Sans JP")
	faces, err := parseFaces(string(data), jp)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range faces {
		names = append(names, f.file)
	}
	if want := []string{"100-900-japanese-slice-0.woff2", "100-900-japanese-slice-1.woff2", "100-900-latin-ext.woff2", "100-900-latin.woff2"}; !equalStrings(names, want) {
		t.Errorf("got %v, want %v", names, want)
	}
	old := "/* [12] */\n@font-face {\n  font-family: 'Noto Sans JP';\n  font-style: normal;\n  font-weight: 400;\n  src: url(https://fonts.gstatic.com/a.12.woff2) format('woff2');\n  unicode-range: U+4e00-4e0f;\n}\n" +
		"/* [13] */\n@font-face {\n  font-family: 'Noto Sans JP';\n  font-style: normal;\n  font-weight: 400;\n  src: url(https://fonts.gstatic.com/a.13.woff2) format('woff2');\n  unicode-range: U+4e10-4e1f;\n}\n"
	if faces, err := parseFaces(old, jp); err != nil || len(faces) != 2 || faces[0].subset != "japanese-slice-12" || faces[1].file != "400-japanese-slice-13.woff2" {
		t.Errorf("numbered comments: %+v %v", faces, err)
	}
	fallback := "/* fallback */\n@font-face {\n  font-family: 'Playwrite CA Guides';\n  font-style: normal;\n  font-weight: 400;\n  font-display: swap;\n  src: url(https://fonts.gstatic.com/s/p/v1/x.woff2) format('woff2');\n}\n"
	playwrite, _ := Find("Playwrite CA Guides")
	faces, err = parseFaces(fallback, playwrite)
	if err != nil || len(faces) != 1 || faces[0].subset != "fallback" || faces[0].unicode != "" {
		t.Fatalf("fallback: %+v %v", faces, err)
	}
	vietnamese := Need{Subsets: []string{"latin", "vietnamese"}}
	if !vietnamese.ships(rule{style: "normal", subset: "fallback"}) {
		t.Errorf("a face without a unicode-range draws every letter: it ships")
	}
	slice := rule{style: "normal", subset: "slice-3", unicode: "U+4e00"}
	if vietnamese.ships(slice) || !(Need{Subsets: []string{"japanese"}}).ships(slice) {
		t.Errorf("a CJK slice ships with the CJK subset only")
	}
}

// The css2 request asks for every face: the axes of a variable family (opsz with wght, italics
// alongside), every static weight and italic of a static one.
func TestCSSRequestAsksForEveryFace(t *testing.T) {
	for name, want := range map[string]string{
		"Inter":        "Inter:ital,opsz,wght@0,14..32,100..900;1,14..32,100..900",
		"Itim":         "Itim:wght@400",
		"Lato":         "Lato:ital,wght@0,100;0,300;0,400;0,700;0,900;1,100;1,300;1,400;1,700;1,900",
		"Noto Sans JP": "Noto+Sans+JP:wght@100..900",
		DefaultBody:    "Google+Sans+Flex:opsz,wght@6..144,1..1000",
	} {
		f, _ := Find(name)
		if got := cssRequest(f); got != "https://fonts.googleapis.com/css2?family="+want+"&display=swap" {
			t.Errorf("%s: %s", name, got)
		}
	}
}
