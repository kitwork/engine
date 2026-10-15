package fonts

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"
)

// The real thing, once, on request: KITWORK_LIVE_FONTS=1 downloads Itim from Google into a temporary
// cache through the real transport, and serves it.
//
//	KITWORK_LIVE_FONTS=1 go test ./jit/fonts -run Live -v
func TestLiveDownloadsItim(t *testing.T) {
	if os.Getenv("KITWORK_LIVE_FONTS") != "1" {
		t.Skip("set KITWORK_LIVE_FONTS=1 to download Itim from Google for real")
	}
	previous := cacheSetting()
	dir := t.TempDir()
	SetCacheDir(dir)
	t.Cleanup(func() { SetCacheDir(previous) })

	start := time.Now()
	if err := Ensure("Itim"); err != nil {
		t.Fatal(err)
	}
	t.Logf("downloaded in %v into %s", time.Since(start).Round(time.Millisecond), dir)
	css, err := FaceCSS("Itim", Need{Weights: []int{400}, Subsets: []string{"latin", "latin-ext", "vietnamese"}})
	if err != nil {
		t.Fatal(err)
	}
	srcs := served(css)
	if len(srcs) != 3 {
		t.Fatalf("latin, latin-ext, vietnamese:\n%s", css)
	}
	for _, src := range srcs {
		file, exact := FileAt(src)
		if file == nil || !exact || !bytes.HasPrefix(file.Body, []byte("wOF2")) || len(file.Body) < 1000 {
			t.Fatalf("%s is not a real woff2 served", src)
		}
		t.Logf("%s  %d bytes", src, len(file.Body))
	}
	if !strings.Contains(css, "font-display:swap") {
		t.Errorf("%s", css)
	}
	again := time.Now()
	if err := Ensure("Itim"); err != nil || time.Since(again) > 50*time.Millisecond {
		t.Errorf("the second Ensure went back to Google: %v %v", err, time.Since(again))
	}
}
