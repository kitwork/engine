package stylesheet

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// contrastShades are the colours the browser proof reads: ink and every kind of token made lighter
// (the old shades, each X.N now X-(100-N)) to the ends of the scale, and made stronger, the label rung
// among them.
var contrastShades = []string{
	"ink-30", "ink-60", "ink-95", "ink-5", "brand-50", "danger-15", "success-80",
	"warning-40", "info-67", "paper-90", "panel-23", "line-50", "brand.on-30",
	"brand+15", "brand+40", "danger+25", "warning+55", "ink+20", "brand.on+10", "success+0",
}

// A lighter colour is computed as the browser computes color-mix(in oklab, X (100-N)%, var(--canvas))
// in each mode, and a stronger one is the custom property the page's stylesheet sets for that mode:
// the page's own classes (color-ink-30, color-brand+15…) with its colours file, in headless Edge or
// Chrome, each computed colour painted on a canvas and read back as 8-bit sRGB, light and then dark
// (data-theme=dark). Every colour agrees with Paint within 1/255 per channel. CONTROL: the same mix
// done in sRGB instead of oklab is more than 1/255 away for some lighter colour: the proof tells the
// two apart.
func TestBrowserOperatorsAreTheBrowsers(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the operators browser proof in short mode")
	}
	browser := findHeadlessBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	palette, err := NewPalette(false, map[string]Color{
		"brand":  {Light: "#c8742c"},
		"canvas": {Light: "#f6f3ee", Dark: "#141110"},
		"panel":  {Light: "#efe9e1", Dark: "#1a1613"},
		"paper":  {Light: "#ffffff", Dark: "#1f1a17"},
		"ink":    {Light: "#2b211b", Dark: "#f3ece6"},
		"line":   {Light: "#e6ded5", Dark: "#3a302a"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !palette.Dark() {
		t.Fatal("the palette has no dark look")
	}
	table := NewTable(palette)
	var rules Rules
	var body strings.Builder
	for i, shade := range contrastShades {
		class, err := table.Class(&rules, Intent{Tag: "text", Name: "color", Value: shade})
		if err != nil {
			t.Fatalf("%s: %v", shade, err)
		}
		fmt.Fprintf(&body, `<p id="s%d" class="%s">x</p>`, i, class)
	}
	files := map[string][]byte{"/reboot.css": Reboot().CSS, "/colors.css": Colors(palette).CSS, "/page.css": rules.CSS()}
	script := `<script>
	fetch("/hold");
	window.addEventListener("load", function () {
	  var canvas = document.createElement("canvas");
	  canvas.width = canvas.height = 1;
	  var context = canvas.getContext("2d", { willReadFrequently: true });
	  function pixel(css) {
	    context.clearRect(0, 0, 1, 1);
	    context.fillStyle = "#123456";
	    context.fillStyle = css;
	    context.fillRect(0, 0, 1, 1);
	    var d = context.getImageData(0, 0, 1, 1).data;
	    return [d[0], d[1], d[2], d[3]].join(",") + " " + css;
	  }
	  function read() {
	    var out = [];
	    for (var i = 0; i < ` + strconv.Itoa(len(contrastShades)) + `; i++) out.push(pixel(getComputedStyle(document.getElementById("s" + i)).color));
	    return out;
	  }
	  var report = { light: read() };
	  document.documentElement.setAttribute("data-theme", "dark");
	  report.dark = read();
	  fetch("/report", { method: "POST", body: JSON.stringify(report) });
	});
	</script>`
	html := `<!doctype html><html><head><meta charset="utf-8"><title>operators</title><link rel="stylesheet" href="/reboot.css">` +
		`<link rel="stylesheet" href="/colors.css"><link rel="stylesheet" href="/page.css"></head><body>` + body.String() + script + `</body></html>`

	var mu sync.Mutex
	reported := make(chan struct{})
	var once sync.Once
	var report []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if css, ok := files[r.URL.Path]; ok {
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
			_, _ = w.Write(css)
			return
		}
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, html)
		case "/hold":
			select {
			case <-reported:
			case <-time.After(15 * time.Second):
			}
		case "/report":
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			report = b
			mu.Unlock()
			once.Do(func() { close(reported) })
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args := []string{"--headless=new", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage",
		"--disable-background-networking", "--disable-default-apps", "--disable-extensions", "--disable-sync",
		"--metrics-recording-only", "--no-first-run", "--window-size=800,600",
		"--user-data-dir=" + filepath.Join(t.TempDir(), "profile"), "--virtual-time-budget=10000", "--dump-dom", server.URL + "/"}
	if runtime.GOOS == "windows" {
		args = append([]string{"--disable-background-mode", "--disable-breakpad", "--disable-crashpad-for-testing"}, args...)
	}
	output, _ := exec.CommandContext(ctx, browser, args...).CombinedOutput()
	mu.Lock()
	defer mu.Unlock()
	if report == nil {
		t.Fatalf("the page never reported\n%.700s", output)
	}
	var got map[string][]string
	if err := json.Unmarshal(report, &got); err != nil {
		t.Fatalf("report %s: %v", report, err)
	}

	srgbApart := false
	for mode, word := range map[Mode]string{LightMode: "light", DarkMode: "dark"} {
		if len(got[word]) != len(contrastShades) {
			t.Fatalf("%s: read %d shades, want %d", word, len(got[word]), len(contrastShades))
		}
		for i, shade := range contrastShades {
			fields := strings.SplitN(got[word][i], " ", 2)
			var px [4]int
			for j, n := range strings.Split(fields[0], ",") {
				px[j], _ = strconv.Atoi(n)
			}
			want, ok := palette.Paint(shade, mode)
			if !ok {
				t.Fatalf("%s: not a colour", shade)
			}
			channels := [3]float64{want.R, want.G, want.B}
			for c := 0; c < 3; c++ {
				if d := math.Abs(channels[c]*255 - float64(px[c])); d > 1 {
					t.Errorf("%s %s: browser %v (%s), Paint %s: channel %d is %.2f/255 away", word, shade, px[:3], fields[1], want.Hex(), c, d)
				}
			}
			if px[3] != 255 {
				t.Errorf("%s %s: the browser's colour is not opaque: %v", word, shade, px)
			}
			// the control: the same mix in sRGB
			c, _ := SplitColour(shade)
			if c.Op != '-' {
				continue
			}
			base, _ := palette.Paint(c.Base, mode)
			canvas, _ := palette.Paint("canvas", mode)
			p := float64(100-c.Amount) / 100
			naive := [3]float64{base.R*p + canvas.R*(1-p), base.G*p + canvas.G*(1-p), base.B*p + canvas.B*(1-p)}
			for ch := 0; ch < 3; ch++ {
				if math.Abs(naive[ch]*255-float64(px[ch])) > 1 {
					srgbApart = true
				}
			}
		}
	}
	if !srgbApart {
		t.Error("CONTROL: a mix in sRGB agrees with the browser on every lighter colour: the proof cannot tell oklab from sRGB")
	}
}
