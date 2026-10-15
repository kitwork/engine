package fonts

// Any Google font by its name: Ensure downloads a family ONCE from Google into a cache folder on the
// server, and from then on it is served from RoutePrefix like an embedded family — visitors never
// contact Google, and the server needs the network only the first time a family is used.
//
//	<cache>/itim/manifest.json         the faces: style, weights, subset, unicode-range, size, SHA-256
//	<cache>/itim/400-latin.woff2       <weight or range>-<subset>[-italic].woff2
//	<cache>/inter/100-900-latin-italic.woff2
//
// The cache folder is the host's to set (SetCacheDir), os.UserCacheDir()/kitwork/fonts by default —
// never the temp folder, which every user of the machine can write to (CacheDir).
// What the network says never names a file: a folder is a catalog slug, a file is built from numbers
// and a subset word read against strict patterns, and the manifest is read back against the same
// rules — the address of a woff2 only ever says where to fetch it from.

import (
	"container/list"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	cssHost  = "fonts.googleapis.com"
	fileHost = "fonts.gstatic.com"

	// userAgent is a browser that reads woff2: Google answers a client it does not know with ttf.
	userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"

	// fileLimit is the most one face may weigh. Google's subsets are tens of kilobytes; a CJK slice a
	// few hundred.
	fileLimit = 5 << 20
	// cssLimit is the most Google's CSS for one family may weigh (a static CJK family with every
	// weight is about a megabyte).
	cssLimit = 4 << 20
	// faceLimit is the most faces one family may have (a static CJK family: weights × ~120 slices).
	faceLimit = 4000
	// manifestLimit is the most a manifest may weigh when it is read back.
	manifestLimit = 8 << 20

	manifestName    = "manifest.json"
	manifestVersion = 1
	// downloadPrefix and removePrefix name the folders a download works in and an old copy leaves
	// through, next to the families; neither is ever read as a family.
	downloadPrefix = ".download-"
	removePrefix   = ".remove-"
)

var (
	// familyLimit is the most one family may weigh on disk, all faces together. A variable, so a test
	// can show the limit holds without downloading this much.
	familyLimit int64 = 128 << 20
	// downloadWorkers is how many faces of a family are fetched at once.
	downloadWorkers = 6
	// requestTimeout bounds one request; familyTimeout one family's whole download.
	requestTimeout = 60 * time.Second
	familyTimeout  = 10 * time.Minute

	// transportHook replaces the network in the tests (download_test.go): an unexported variable, so
	// nothing a site, a config or a request can set reaches it.
	transportHook http.RoundTripper
	// readHook is called each time a family is read from disk (download_test.go counts the reads):
	// unexported, like transportHook.
	readHook func(dir string)

	defaultTransportOnce sync.Once
	defaultTransport     http.RoundTripper
)

// cachedFile is one downloaded face: the file on disk, its digest, its served URL.
type cachedFile struct {
	name   string // "itim-400-latin": the family slug and the face, as the served name has them
	disk   string // the woff2 in the cache
	digest string // SHA-256 of the bytes, hex
	path   string // /_kitwork/fonts/itim-400-latin.<hash>.woff2
	face   manifestFace
	family *cachedFamily // the read it belongs to: what is forgotten when the file is gone
}

// cachedFamily is a family read from the cache.
type cachedFamily struct {
	slug  string
	root  string // the cache folder it was read from
	files []*cachedFile
}

var (
	cacheMu    sync.RWMutex
	cacheDir   string                       // SetCacheDir; "" is the default
	cachedFams = map[string]*cachedFamily{} // slug → the family, read from the cache
	cachedFace = map[string]*cachedFile{}   // "itim-400-latin" → its file
	absent     = map[string]bool{}          // slugs FileAt looked for in the cache and did not find
	flightMu   sync.Mutex                   // guards flights
	flights    = map[string]*flight{}       // slug → the download (or the read) under way
)

// SetCacheDir sets the folder downloaded families live in — the host's choice (a data folder next to
// the apps, a volume in a container). "" goes back to the default, os.UserCacheDir()/kitwork/fonts.
// The families read from the old folder are forgotten and read again from the new one.
func SetCacheDir(dir string) {
	if dir != "" {
		if abs, err := filepath.Abs(dir); err == nil {
			dir = abs
		}
	}
	cacheMu.Lock()
	defer cacheMu.Unlock()
	cacheDir = dir
	cachedFams = map[string]*cachedFamily{}
	cachedFace = map[string]*cachedFile{}
	absent = map[string]bool{}
}

// CacheDir is the folder downloaded families live in; "" when the host set none and the process has
// no cache folder of its own (a service with no HOME) — Ensure then says what to do. There is no
// fallback to the temp folder: on Linux /tmp is every user's, and a family put there first would be
// served as Google's.
func CacheDir() string {
	cacheMu.RLock()
	defer cacheMu.RUnlock()
	return cacheRootLocked()
}

func cacheRootLocked() string {
	if cacheDir != "" {
		return cacheDir
	}
	if base, err := os.UserCacheDir(); err == nil && filepath.IsAbs(base) {
		return filepath.Join(base, "kitwork", "fonts")
	}
	return ""
}

// noCacheFolder is the error of a family that has nowhere to be downloaded to (CacheDir is "").
func noCacheFolder(fam Family) error {
	return fmt.Errorf("không có thư mục để lưu họ chữ %q khi tải về: tiến trình máy chủ không có thư mục bộ nhớ đệm của người dùng "+
		"(biến môi trường XDG_CACHE_HOME hoặc HOME; trên Windows là LocalAppData) — hãy đặt một biến đó cho máy chủ, hoặc để máy chủ "+
		"đặt thư mục bộ nhớ đệm phông chữ (SetCacheDir). Kitwork không dùng thư mục tạm dùng chung, vì người dùng khác trên máy có thể đặt sẵn phông chữ vào đó",
		fam.Name)
}

// Ensure makes a family ready to be served: an embedded family is ready as it is; any other is read
// from the cache, or downloaded from Google into it the first time — once, however many callers ask
// at the same time. Only an open-source family is downloaded. The error says what to do, in
// Vietnamese, for the compiler to show.
func Ensure(name string) error {
	fam, ok := Find(name)
	if !ok {
		return unknownFamily(name)
	}
	if fam.Embedded || current(fam.Slug) != nil {
		return nil
	}
	return once(fam.Slug, func() error {
		// the host moving the cache folder meanwhile (SetCacheDir) leaves the family unregistered: it is
		// then read, or downloaded, again into the new folder — nil always means FaceCSS works
		for range 3 {
			if current(fam.Slug) != nil {
				return nil
			}
			root := CacheDir()
			if root == "" {
				return noCacheFolder(fam)
			}
			read, err := obtain(root, fam)
			if err != nil {
				return err
			}
			if register(root, read) {
				return nil
			}
		}
		return fmt.Errorf("thư mục bộ nhớ đệm phông chữ đổi liên tục trong lúc tải họ chữ %q — hãy đặt nó một lần khi máy chủ khởi động rồi chạy lại", fam.Name)
	})
}

// obtain is a family in the cache folder: read from disk when it is there, else downloaded into it.
func obtain(root string, fam Family) (*cachedFamily, error) {
	if read, err := readFamily(root, fam); err == nil {
		return read, nil
	}
	if !fam.OpenSource {
		return nil, fmt.Errorf("họ chữ %q không phải mã nguồn mở nên Kitwork không tải về để tự phục vụ — hãy chọn một họ chữ khác", fam.Name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), familyTimeout)
	defer cancel()
	return download(ctx, root, fam)
}

// current is the family registered for a slug, if its manifest is still where it was read from. A
// family whose folder went away — a cache folder the system or someone cleared — is forgotten, so the
// next Ensure downloads it again instead of writing faces no file answers for.
func current(slug string) *cachedFamily {
	cacheMu.RLock()
	read := cachedFams[slug]
	cacheMu.RUnlock()
	if read == nil {
		return nil
	}
	if _, err := os.Stat(filepath.Join(read.root, read.slug, manifestName)); err != nil {
		forget(read)
		return nil
	}
	return read
}

// forget drops a family that no longer reads from disk as it was read — that read only: a newer one
// stays.
func forget(read *cachedFamily) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if cachedFams[read.slug] != read {
		return
	}
	delete(cachedFams, read.slug)
	for _, f := range read.files {
		if cachedFace[f.name] == f {
			delete(cachedFace, f.name)
		}
	}
}

// register makes a family read from root servable, and reports whether it did: not when the host
// moved the cache meanwhile.
func register(root string, read *cachedFamily) bool {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if cacheRootLocked() != root {
		return false
	}
	if old := cachedFams[read.slug]; old != nil {
		for _, f := range old.files {
			if cachedFace[f.name] == f {
				delete(cachedFace, f.name)
			}
		}
	}
	cachedFams[read.slug] = read
	for _, f := range read.files {
		cachedFace[f.name] = f
	}
	delete(absent, read.slug)
	return true
}

// flight is one family's download (or read) under way; the callers that come while it runs wait for
// it and get its error.
type flight struct {
	done chan struct{}
	err  error
}

func once(slug string, work func() error) error {
	flightMu.Lock()
	if f, ok := flights[slug]; ok {
		flightMu.Unlock()
		<-f.done
		return f.err
	}
	f := &flight{done: make(chan struct{})}
	flights[slug] = f
	flightMu.Unlock()
	defer func() {
		flightMu.Lock()
		delete(flights, slug)
		flightMu.Unlock()
		close(f.done)
	}()
	f.err = work()
	return f.err
}

// cssRequest is the css2 address that asks Google for every face of a family: a variable family's
// whole weight axis (and its optical size axis when it has one), a static family's every weight;
// italics too when the family has them.
//
//	Inter:ital,opsz,wght@0,14..32,100..900;1,14..32,100..900
//	Lato:ital,wght@0,100;0,300;…;1,900
//	Itim:wght@400
func cssRequest(fam Family) string {
	spec := ""
	italic := len(fam.Italics) > 0
	number := func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
	if wght, ok := fam.axis("wght"); ok {
		tags := []string{"wght"}
		ranges := []string{number(wght.Min) + ".." + number(wght.Max)}
		if opsz, ok := fam.axis("opsz"); ok {
			tags = append([]string{"opsz"}, tags...)
			ranges = append([]string{number(opsz.Min) + ".." + number(opsz.Max)}, ranges...)
		}
		if italic {
			spec = "ital," + strings.Join(tags, ",") + "@0," + strings.Join(ranges, ",") + ";1," + strings.Join(ranges, ",")
		} else {
			spec = strings.Join(tags, ",") + "@" + strings.Join(ranges, ",")
		}
	} else {
		var tuples []string
		if italic {
			for _, w := range fam.Weights {
				tuples = append(tuples, "0,"+strconv.Itoa(w))
			}
			for _, w := range fam.Italics {
				tuples = append(tuples, "1,"+strconv.Itoa(w))
			}
			spec = "ital,wght@" + strings.Join(tuples, ";")
		} else {
			for _, w := range fam.Weights {
				tuples = append(tuples, strconv.Itoa(w))
			}
			spec = "wght@" + strings.Join(tuples, ";")
		}
	}
	return "https://" + cssHost + "/css2?family=" + strings.ReplaceAll(fam.Name, " ", "+") + ":" + spec + "&display=swap"
}

// refusal is an answer Kitwork will not take: a host other than Google's, a redirect elsewhere, a
// body that is not a woff2, too big. Its message is the error as it is shown.
type refusal struct{ message string }

func (r *refusal) Error() string { return r.message }

func refuse(format string, args ...any) error {
	return &refusal{fmt.Sprintf(format, args...)}
}

// allowed reports whether Kitwork may fetch an address: https, exactly the host given, no port, no
// user.
func allowed(address *url.URL, host string) error {
	if address.Scheme != "https" || address.Host != host || address.User != nil {
		return refuse("Kitwork chỉ tải phông chữ qua https từ %s và %s, không tải %q — hãy thử lại sau; nếu vẫn vậy, Google Fonts đã đổi cách trả lời và Kitwork cần được cập nhật",
			cssHost, fileHost, address.Redacted())
	}
	return nil
}

func client() *http.Client {
	transport := transportHook
	if transport == nil {
		defaultTransportOnce.Do(func() {
			t := http.DefaultTransport.(*http.Transport).Clone()
			t.TLSHandshakeTimeout = 15 * time.Second
			t.ResponseHeaderTimeout = 30 * time.Second
			defaultTransport = t
		})
		transport = defaultTransport
	}
	return &http.Client{
		Transport: transport,
		Timeout:   requestTimeout,
		// A redirect stays on the host asked: Google moving a request to another host is refused.
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return refuse("Google Fonts chuyển hướng quá nhiều lần — hãy thử lại sau")
			}
			if err := allowed(request.URL, via[0].URL.Host); err != nil {
				return refuse("Google Fonts chuyển hướng sang %q — Kitwork chỉ tải từ %s và %s qua https nên không theo; hãy thử lại sau",
					request.URL.Redacted(), cssHost, fileHost)
			}
			return nil
		},
	}
}

// fetch GETs an address on one host, at most limit bytes.
func fetch(ctx context.Context, address, host string, limit int64, fam Family, root string) ([]byte, error) {
	u, err := url.Parse(address)
	if err != nil {
		return nil, refuse("Google Fonts đưa một địa chỉ không đọc được cho họ chữ %q — hãy thử lại sau", fam.Name)
	}
	if err := allowed(u, host); err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", userAgent)
	response, err := client().Do(request)
	if err != nil {
		var r *refusal
		if errors.As(err, &r) {
			return nil, r
		}
		return nil, offline(fam, root, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Google Fonts trả lời %d khi tải họ chữ %q — hãy thử lại sau", response.StatusCode, fam.Name)
	}
	if response.ContentLength > limit {
		return nil, tooBig(fam, limit)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, offline(fam, root, err)
	}
	if int64(len(body)) > limit {
		return nil, tooBig(fam, limit)
	}
	return body, nil
}

func tooBig(fam Family, limit int64) error {
	return refuse("một tệp Google Fonts của họ chữ %q lớn hơn %d MiB — Kitwork không lưu nó; hãy thử lại sau hoặc chọn họ chữ khác", fam.Name, limit>>20)
}

// offline is the error of a download the network did not let through.
func offline(fam Family, root string, err error) error {
	return fmt.Errorf("không tải được họ chữ %q từ Google Fonts (%v). Lần đầu một họ chữ được dùng, máy chủ cần mạng để tải nó về một lần; "+
		"sau đó họ chữ nằm ở %s và chạy không cần mạng — hãy kiểm tra mạng của máy chủ rồi chạy lại",
		fam.Name, err, filepath.Join(root, fam.Slug))
}

// download fetches Google's CSS for every face of the family and every face's woff2, into a folder of
// its own next to the families, renamed to the family's folder only once all is there: a failed
// download leaves nothing behind.
func download(ctx context.Context, root string, fam Family) (*cachedFamily, error) {
	css, err := fetch(ctx, cssRequest(fam), cssHost, cssLimit, fam, root)
	if err != nil {
		return nil, err
	}
	faces, err := parseFaces(string(css), fam)
	if err != nil {
		return nil, refuse("Google Fonts trả về CSS Kitwork không đọc được cho họ chữ %q (%v) — hãy thử lại sau", fam.Name, err)
	}
	for _, f := range faces {
		u, err := url.Parse(f.src)
		if err != nil {
			return nil, refuse("Google Fonts đưa một địa chỉ không đọc được cho họ chữ %q — hãy thử lại sau", fam.Name)
		}
		if err := allowed(u, fileHost); err != nil {
			return nil, err
		}
	}
	// the server's own: nobody else on the machine reads or writes the faces it serves
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, writeError(fam, root, err)
	}
	sweep(root, fam.Slug)
	work, err := os.MkdirTemp(root, downloadPrefix+fam.Slug+"-")
	if err != nil {
		return nil, writeError(fam, root, err)
	}
	defer os.RemoveAll(work) // gone once renamed; everything, if anything failed

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		total     atomic.Int64
		firstErr  error
		errOnce   sync.Once
		wg        sync.WaitGroup
		next      = make(chan int)
		manifests = make([]manifestFace, len(faces))
	)
	fail := func(err error) {
		errOnce.Do(func() { firstErr = err })
		cancel()
	}
	for range min(downloadWorkers, len(faces)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				f := faces[i]
				body, err := fetch(ctx, f.src, fileHost, fileLimit, fam, root)
				if err != nil {
					fail(err)
					continue
				}
				if len(body) < 4 || string(body[:4]) != "wOF2" {
					fail(refuse("Google Fonts trả về một tệp không phải woff2 cho họ chữ %q — Kitwork không lưu nó; hãy thử lại sau", fam.Name))
					continue
				}
				if total.Add(int64(len(body))) > familyLimit {
					fail(refuse("họ chữ %q nặng hơn %d MiB — Kitwork không lưu cả họ; hãy chọn một họ chữ khác", fam.Name, familyLimit>>20))
					continue
				}
				if err := os.WriteFile(filepath.Join(work, f.file), body, 0o600); err != nil {
					fail(writeError(fam, root, err))
					continue
				}
				sum := sha256.Sum256(body)
				manifests[i] = manifestFace{
					File: f.file, Style: f.style, Weight: f.weight, Stretch: f.stretch, Subset: f.subset,
					Unicode: f.unicode, Size: int64(len(body)), SHA256: hex.EncodeToString(sum[:]),
				}
			}
		}()
	}
feed:
	for i := range faces {
		select {
		case next <- i:
		case <-ctx.Done():
			break feed
		}
	}
	close(next)
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, offline(fam, root, err)
	}

	m := manifest{Version: manifestVersion, Family: fam.Name, Slug: fam.Slug, Request: cssRequest(fam),
		Fetched: time.Now().UTC().Format(time.RFC3339), Faces: manifests}
	data, err := json.MarshalIndent(m, "", "\t")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(work, manifestName), append(data, '\n'), 0o600); err != nil {
		return nil, writeError(fam, root, err)
	}
	// the download is read back by the rules the cache is read by before it becomes the family's
	// folder: a folder that would not read never appears
	if _, err := readFamilyIn(work, fam); err != nil {
		return nil, refuse("họ chữ %q tải về không đọc lại được (%v) — Kitwork không lưu nó; hãy thử lại sau", fam.Name, err)
	}
	if err := install(root, work, fam); err != nil {
		return nil, writeError(fam, root, err)
	}
	read, err := readFamily(root, fam)
	if err != nil {
		return nil, fmt.Errorf("họ chữ %q đã tải về %s nhưng không đọc lại được (%v) — hãy xoá thư mục đó rồi chạy lại",
			fam.Name, filepath.Join(root, fam.Slug), err)
	}
	return read, nil
}

// install renames a finished download into the family's folder. A folder already there is either
// another process's good copy (kept) or a copy that does not read (moved aside, then removed).
func install(root, work string, fam Family) error {
	final := filepath.Join(root, fam.Slug)
	err := os.Rename(work, final)
	if err == nil {
		return nil
	}
	if _, statErr := os.Stat(final); statErr != nil {
		return err
	}
	if _, readErr := readFamily(root, fam); readErr == nil {
		return nil
	}
	aside := filepath.Join(root, removePrefix+fam.Slug+"-"+randomWord())
	if err := os.Rename(final, aside); err != nil {
		return err
	}
	if err := os.Rename(work, final); err != nil {
		os.Rename(aside, final)
		return err
	}
	os.RemoveAll(aside)
	return nil
}

// sweep removes what a download of the family stopped mid-way left (a crash): its work folders and
// the old copies it moved aside, once they are an hour old — younger ones may be another process's.
func sweep(root, slug string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || !(strings.HasPrefix(name, downloadPrefix+slug+"-") || strings.HasPrefix(name, removePrefix+slug+"-")) {
			continue
		}
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > time.Hour {
			os.RemoveAll(filepath.Join(root, name))
		}
	}
}

func randomWord() string {
	var b [8]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func writeError(fam Family, root string, err error) error {
	return fmt.Errorf("không ghi được họ chữ %q vào %s (%v) — hãy kiểm tra quyền ghi của thư mục đó, hoặc đặt thư mục bộ nhớ đệm phông chữ khác",
		fam.Name, root, err)
}

// manifest is <cache>/<slug>/manifest.json: the faces of a downloaded family.
type manifest struct {
	Version int            `json:"version"`
	Family  string         `json:"family"`
	Slug    string         `json:"slug"`
	Request string         `json:"request"`
	Fetched string         `json:"fetched"`
	Faces   []manifestFace `json:"faces"`
}

// manifestFace is one face of a downloaded family.
type manifestFace struct {
	File    string `json:"file"`   // 400-latin.woff2 — always faceName(weight, subset, style) + ".woff2"
	Style   string `json:"style"`  // normal or italic
	Weight  [2]int `json:"weight"` // lightest, heaviest (equal for one weight)
	Stretch string `json:"stretch,omitempty"`
	Subset  string `json:"subset"` // latin, vietnamese…, japanese-slice-12 / emoji-slice-3 for a numbered slice, fallback
	Unicode string `json:"unicodeRange,omitempty"`
	Size    int64  `json:"size"`
	SHA256  string `json:"sha256"`
}

var (
	digestRe = regexp.MustCompile(`^[0-9a-f]{64}$`)
	// unicodeRangeRe is a unicode-range as Google writes it; anything else would be text written into
	// a stylesheet, so it is refused.
	unicodeRangeRe = regexp.MustCompile(`^[Uu]\+[0-9A-Fa-f?]{1,6}(-[0-9A-Fa-f]{1,6})?(,\s*[Uu]\+[0-9A-Fa-f?]{1,6}(-[0-9A-Fa-f]{1,6})?)*$`)
	stretchRe      = regexp.MustCompile(`^(\d{1,3}(\.\d{1,3})?%( \d{1,3}(\.\d{1,3})?%)?|normal|(ultra-|extra-|semi-)?(condensed|expanded))$`)
	// faceFileRe is every name a face file can have: <weight>[-<weight>]-<subset>[-italic].woff2.
	faceFileRe = regexp.MustCompile(`^[0-9]{1,4}(-[0-9]{1,4})?-[a-z0-9]+(-[a-z0-9]+)*\.woff2$`)
)

// faceName is a face's name: the file's without .woff2, and the served name's after the family slug.
func faceName(weight [2]int, subset, style string) string {
	name := strconv.Itoa(weight[0])
	if weight[1] != weight[0] {
		name += "-" + strconv.Itoa(weight[1])
	}
	name += "-" + subset
	if style == "italic" {
		name += "-italic"
	}
	return name
}

// validFace reports whether a face read from the network or the cache is what Kitwork writes: the
// style one of two words, weights within 1–1000, the subset a word, the ranges Google's syntax.
func validFace(style string, weight [2]int, subset, stretch, unicodeRange string) bool {
	return (style == "normal" || style == "italic") &&
		weight[0] >= 1 && weight[0] <= weight[1] && weight[1] <= 1000 &&
		subsetRe.MatchString(subset) &&
		(stretch == "" || stretchRe.MatchString(stretch)) &&
		(unicodeRange == "" || unicodeRangeRe.MatchString(unicodeRange))
}

// readFamily reads a family from the cache: its manifest, every face checked against the rules a
// download follows, every file against its size and digest. An error when anything does not read —
// the caller then downloads it again.
func readFamily(root string, fam Family) (*cachedFamily, error) {
	if root == "" {
		return nil, errors.New("no cache folder") // never a folder relative to where the server runs
	}
	read, err := readFamilyIn(filepath.Join(root, fam.Slug), fam)
	if err != nil {
		return nil, err
	}
	read.root = root
	return read, nil
}

// readFamilyIn is readFamily on the folder given: the family's own, or a finished download's before it
// is renamed into place.
func readFamilyIn(dir string, fam Family) (*cachedFamily, error) {
	if readHook != nil {
		readHook(dir)
	}
	data, err := readLimited(filepath.Join(dir, manifestName), manifestLimit)
	if err != nil {
		return nil, err
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	if m.Version != manifestVersion || m.Family != fam.Name || m.Slug != fam.Slug || len(m.Faces) == 0 || len(m.Faces) > faceLimit {
		return nil, errors.New("manifest does not match the family")
	}
	read := &cachedFamily{slug: fam.Slug}
	seen := map[string]bool{}
	for _, f := range m.Faces {
		name := faceName(f.Weight, f.Subset, f.Style)
		if !validFace(f.Style, f.Weight, f.Subset, f.Stretch, f.Unicode) || f.File != name+".woff2" ||
			!faceFileRe.MatchString(f.File) || seen[f.File] || !digestRe.MatchString(f.SHA256) || f.Size < 4 || f.Size > fileLimit {
			return nil, errors.New("manifest face " + strconv.Quote(f.File))
		}
		seen[f.File] = true
		disk := filepath.Join(dir, f.File)
		body, err := readLimited(disk, fileLimit)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(body)
		if int64(len(body)) != f.Size || hex.EncodeToString(sum[:]) != f.SHA256 || string(body[:4]) != "wOF2" {
			return nil, errors.New("face " + strconv.Quote(f.File) + " changed on disk")
		}
		read.files = append(read.files, &cachedFile{
			name:   fam.Slug + "-" + name,
			disk:   disk,
			digest: f.SHA256,
			path:   RoutePrefix + fam.Slug + "-" + name + "." + f.SHA256[:hashLength] + ".woff2",
			face:   f,
			family: read,
		})
	}
	return read, nil
}

// readLimited reads a file of at most limit bytes.
func readLimited(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, errors.New(path + " is too large")
	}
	return body, nil
}

// cachedFamilyOf is a family's faces from the cache — read now when it was not yet (no network).
func cachedFamilyOf(fam Family) (*cachedFamily, error) {
	if read := readCached(fam); read != nil {
		return read, nil
	}
	root := CacheDir()
	if root == "" {
		return nil, noCacheFolder(fam)
	}
	return nil, fmt.Errorf("họ chữ %q chưa được tải về %s — cần tải nó về trước (Ensure) khi biên dịch trang", fam.Name, filepath.Join(root, fam.Slug))
}

// readCached is a family from the cache, read from disk when this process has not read it yet — once,
// however many ask at the same time (a page's faces asked for together after a restart); nil when it
// is not in the cache, which is then remembered (absent). Never downloaded: that is Ensure's. Reads
// wait for reads only (their own flight), never for a download under way.
func readCached(fam Family) *cachedFamily {
	if read := current(fam.Slug); read != nil {
		return read
	}
	once("read "+fam.Slug, func() error {
		if current(fam.Slug) != nil {
			return nil
		}
		root := CacheDir()
		read, err := readFamily(root, fam)
		if err == nil && register(root, read) {
			return nil
		}
		cacheMu.Lock()
		if cacheRootLocked() == root && cachedFams[fam.Slug] == nil {
			absent[fam.Slug] = true
		}
		cacheMu.Unlock()
		return err
	})
	cacheMu.RLock()
	defer cacheMu.RUnlock()
	return cachedFams[fam.Slug]
}

// cachedFileAt is the downloaded face a served name names (FileAt, after the embedded faces): face is
// "<slug>-<face>" from the request, only ever a key. A family on disk that this process has not read
// yet (a page cached in a browser across a restart) is read the first time one of its faces is asked
// for; a slug found absent is not looked for again until the cache moves or the family is downloaded.
// The body is read from disk and checked against its digest once, then served from memory (held). A
// face whose file is gone or changed is not served, and its family is forgotten: the next Ensure
// downloads it again.
func cachedFileAt(face, path string) (*File, bool) {
	cacheMu.RLock()
	f := cachedFace[face]
	cacheMu.RUnlock()
	if f == nil {
		f = readFaceFamily(face)
	}
	if f == nil {
		return nil, false
	}
	body := bodyOf(f)
	if body == nil {
		return nil, false
	}
	return &File{Path: f.path, Body: body, Digest: f.digest}, f.path == path
}

// bodyOf is a downloaded face's bytes: from memory when they were served lately, else read from disk
// and checked against the digest — once, however many ask at the same time. nil when the file is gone
// or changed; its family is then forgotten.
func bodyOf(f *cachedFile) []byte {
	if body := held.get(f.digest); body != nil {
		return body
	}
	var body []byte // set when this caller is the one that reads
	err := once("body "+f.digest, func() error {
		read, err := verifiedBody(f)
		if err != nil {
			forget(f.family)
			return err
		}
		held.put(f.digest, read)
		body = read
		return nil
	})
	if err != nil {
		return nil
	}
	if body == nil {
		body = held.get(f.digest)
	}
	if body == nil { // put out of memory as soon as it was read: read for this request alone
		body, _ = verifiedBody(f)
	}
	return body
}

// verifiedBody reads a face from disk, its bytes checked against its digest.
func verifiedBody(f *cachedFile) ([]byte, error) {
	body, err := readLimited(f.disk, fileLimit)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != f.digest {
		return nil, errors.New("face " + strconv.Quote(f.disk) + " changed on disk")
	}
	return body, nil
}

// held keeps the bytes of the downloaded faces served lately, at most its limit, the face served least
// lately put out first: a face is read from disk and checked once, then served from memory like an
// embedded one — a request (a HEAD, a 304, an old hash) costs no read. Keyed by digest, so whatever
// family or folder a face was read from, a body is the bytes its key names.
var held = newBodyCache(64 << 20)

func newBodyCache(limit int64) *bodyCache {
	return &bodyCache{limit: limit, order: list.New(), at: map[string]*list.Element{}}
}

type bodyCache struct {
	mu    sync.Mutex
	limit int64
	used  int64
	order *list.List               // the bodies, served most lately first: *heldBody
	at    map[string]*list.Element // digest → its body in order
}

type heldBody struct {
	digest string
	body   []byte
}

func (c *bodyCache) get(digest string) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.at[digest]
	if e == nil {
		return nil
	}
	c.order.MoveToFront(e)
	return e.Value.(*heldBody).body
}

func (c *bodyCache) put(digest string, body []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.at[digest] != nil || int64(len(body)) > c.limit {
		return
	}
	c.at[digest] = c.order.PushFront(&heldBody{digest, body})
	c.used += int64(len(body))
	for c.used > c.limit {
		h := c.order.Remove(c.order.Back()).(*heldBody)
		delete(c.at, h.digest)
		c.used -= int64(len(h.body))
	}
}

// readFaceFamily reads from the cache the family a face name starts with, and returns the face.
func readFaceFamily(face string) *cachedFile {
	google()
	for i := 0; i < len(face); i++ {
		if face[i] != '-' {
			continue
		}
		fam := googleBySlug[face[:i]]
		if fam == nil || fam.Embedded {
			continue
		}
		cacheMu.RLock()
		known, missing := cachedFams[fam.Slug] != nil, absent[fam.Slug]
		cacheMu.RUnlock()
		if known || missing {
			continue
		}
		readCached(fam.clone())
	}
	cacheMu.RLock()
	defer cacheMu.RUnlock()
	return cachedFace[face]
}

// parsedFace is one @font-face of Google's CSS.
type parsedFace struct {
	style   string
	weight  [2]int
	stretch string
	subset  string
	unicode string
	src     string // where to fetch it — never part of a name on disk
	file    string // faceName + ".woff2"
}

var (
	// fontFaceRe is one @font-face with the comment Google writes before it (the subset), if any.
	fontFaceRe  = regexp.MustCompile(`(?s)(?:/\*([^*]*)\*/\s*)?@font-face\s*\{([^}]*)\}`)
	declRe      = regexp.MustCompile(`([a-z-]+)\s*:\s*([^;]*)`)
	srcURLRe    = regexp.MustCompile(`url\(\s*['"]?([^'")\s]+)['"]?\s*\)`)
	sliceWordRe = regexp.MustCompile(`^\[(\d{1,4})\]$`)
)

// parseFaces reads Google's css2 answer: every @font-face block, its subset from the comment before
// it — a script ("latin", "vietnamese"), a numbered slice ("[12]" → japanese-slice-12; Google also
// leaves the slices without a comment, numbered then in order), or "fallback" — its style, weight (one
// or a range), stretch, unicode-range and the address of its woff2. A slice is named by the subset it
// is cut from (sliceGroup): japanese for Noto Sans JP, emoji for Noto Color Emoji.
func parseFaces(css string, fam Family) ([]parsedFace, error) {
	var faces []parsedFace
	seen := map[string]string{}
	unnamed := 0
	blocks := fontFaceRe.FindAllStringSubmatch(css, -1)
	named := map[string]bool{}
	for _, block := range blocks {
		named[strings.TrimSpace(block[1])] = true
	}
	group := sliceGroup(fam, named)
	for _, block := range blocks {
		comment := strings.TrimSpace(block[1])
		var subset string
		switch m := sliceWordRe.FindStringSubmatch(comment); {
		case comment == "":
			subset = sliceName(group, unnamed)
			unnamed++
		case m != nil:
			n, _ := strconv.Atoi(m[1])
			subset = sliceName(group, n)
		case subsetRe.MatchString(comment):
			subset = comment
		default:
			return nil, fmt.Errorf("subset %q", comment)
		}
		decl := map[string]string{}
		for _, d := range declRe.FindAllStringSubmatch(block[2], -1) {
			decl[d[1]] = strings.TrimSpace(d[2])
		}
		if name := strings.Trim(decl["font-family"], `'"`); name != fam.Name {
			return nil, fmt.Errorf("font-family %q", decl["font-family"])
		}
		f := parsedFace{style: decl["font-style"], stretch: decl["font-stretch"], subset: subset, unicode: decl["unicode-range"]}
		if f.style == "" {
			f.style = "normal"
		}
		weights := strings.Fields(decl["font-weight"])
		if len(weights) == 0 || len(weights) > 2 {
			return nil, fmt.Errorf("font-weight %q", decl["font-weight"])
		}
		for i := range f.weight {
			n, err := strconv.Atoi(weights[min(i, len(weights)-1)])
			if err != nil {
				return nil, fmt.Errorf("font-weight %q", decl["font-weight"])
			}
			f.weight[i] = n
		}
		if !validFace(f.style, f.weight, f.subset, f.stretch, f.unicode) {
			return nil, fmt.Errorf("@font-face %s %s %s", f.style, decl["font-weight"], f.subset)
		}
		src := srcURLRe.FindStringSubmatch(decl["src"])
		if src == nil {
			return nil, fmt.Errorf("src %q", decl["src"])
		}
		f.src = src[1]
		f.file = faceName(f.weight, f.subset, f.style) + ".woff2"
		if !faceFileRe.MatchString(f.file) {
			return nil, fmt.Errorf("face %q", f.file)
		}
		if previous, twice := seen[f.file]; twice {
			if previous == f.src {
				continue
			}
			return nil, fmt.Errorf("two faces named %s", f.file)
		}
		seen[f.file] = f.src
		faces = append(faces, f)
		if len(faces) > faceLimit {
			return nil, fmt.Errorf("more than %d faces", faceLimit)
		}
	}
	if len(faces) == 0 {
		return nil, errors.New("no @font-face")
	}
	return faces, nil
}

// sliceGroup is the subset a family's numbered slices are cut from: the one subset Google cuts into
// slices (a CJK script, emoji) that no comment names in its CSS — "" when that is not exactly one.
func sliceGroup(fam Family, named map[string]bool) string {
	group := ""
	for _, s := range fam.Subsets {
		if !slicedSubsets[s] || named[s] {
			continue
		}
		if group != "" {
			return ""
		}
		group = s
	}
	return group
}

// sliceName is the subset word of slice n: "japanese-slice-12", or "slice-12" when its subset is not
// known (sliceOf reads it back).
func sliceName(group string, n int) string {
	if group == "" {
		return "slice-" + strconv.Itoa(n)
	}
	return group + "-slice-" + strconv.Itoa(n)
}
