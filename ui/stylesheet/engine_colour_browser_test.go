package stylesheet

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The engine colours take their value per mode in a real browser (decision of 13/10/2026): on Kit
// Design, in headless Edge or Chrome, the computed colour of a warning badge's text, a keyword of a code
// block and the words of <error>, and the brand start of the inverse band's background-image, each
// painted on a canvas and read back as 8-bit sRGB, agree with Paint within 1/255 per channel, light and
// then dark (data-theme=dark): #8c5c00, #4500de, #ce0029, #4000d1 in light as before, the accent itself
// #daa965, #7d83fd, #ef7976, #7d83fd in dark. CONTROL: on the same page the author's color="warning+55"
// is one step in both modes, #ffffff in dark, so the badge's property is not the author's.
func TestBrowserEngineColoursPerMode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the engine colours browser proof in short mode")
	}
	browser := findHeadlessBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	palette, err := NewPalette(true)
	if err != nil {
		t.Fatal(err)
	}
	table := NewTable(palette)
	var rules Rules
	class := func(in Intent) string {
		c, err := table.Class(&rules, in)
		if err != nil {
			t.Fatalf("%+v: %v", in, err)
		}
		return c
	}
	keyword, err := table.Code(&rules, "keyword")
	if err != nil {
		t.Fatal(err)
	}
	body := `<span id="badge" class="badge ` + class(Intent{Tag: "badge", Name: "color", Value: "warning"}) + `">x</span>` +
		`<span id="keyword" class="` + keyword + `">x</span>` +
		`<p id="error" class="error">x</p>` +
		`<div id="inverse" class="` + class(Intent{Name: "background", Value: "inverse"}) + `">x</div>` +
		`<p id="author" class="` + class(Intent{Tag: "text", Name: "color", Value: "warning+55"}) + `">x</p>`
	// what is read, and the colour Paint says each is
	reads := []struct{ id, value string }{
		{"badge", BadgeColour("warning")}, {"keyword", CodeColour("brand")}, {"error", ErrorColour},
		{"inverse", InverseColour}, {"author", "warning+55"},
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
	  function colour(id) {
	    var style = getComputedStyle(document.getElementById(id));
	    if (id !== "inverse") return pixel(style.color);
	    var first = /rgba?\([^)]*\)/.exec(style.backgroundImage); // the band's first stop: its brand start
	    return first ? pixel(first[0]) : "0,0,0,0 " + style.backgroundImage;
	  }
	  function read() {
	    var out = {};
	    ["badge", "keyword", "error", "inverse", "author"].forEach(function (id) { out[id] = colour(id); });
	    return out;
	  }
	  var report = { light: read() };
	  document.documentElement.setAttribute("data-theme", "dark");
	  report.dark = read();
	  fetch("/report", { method: "POST", body: JSON.stringify(report) });
	});
	</script>`
	html := `<!doctype html><html><head><meta charset="utf-8"><title>engine colours</title><link rel="stylesheet" href="/reboot.css">` +
		`<link rel="stylesheet" href="/colors.css"><link rel="stylesheet" href="/page.css"></head><body>` + body + script + `</body></html>`

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
	var got map[string]map[string]string
	if err := json.Unmarshal(report, &got); err != nil {
		t.Fatalf("report %s: %v", report, err)
	}

	pinned := map[Mode]map[string]string{
		LightMode: {"badge": "#8c5c00", "keyword": "#4500de", "error": "#ce0029", "inverse": "#4000d1", "author": "#8c5c00"},
		DarkMode:  {"badge": "#daa965", "keyword": "#7d83fd", "error": "#ef7976", "inverse": "#7d83fd", "author": "#ffffff"},
	}
	pixelOf := regexp.MustCompile(`^(\d+),(\d+),(\d+),(\d+) `)
	for mode, word := range map[Mode]string{LightMode: "light", DarkMode: "dark"} {
		for _, r := range reads {
			want, ok := palette.Paint(r.value, mode)
			if !ok {
				t.Fatalf("%s: not a colour", r.value)
			}
			if want.Hex() != pinned[mode][r.id] {
				t.Errorf("%s %s: Paint says %s, the plan measured %s", word, r.id, want.Hex(), pinned[mode][r.id])
			}
			m := pixelOf.FindStringSubmatch(got[word][r.id])
			if m == nil {
				t.Errorf("%s %s: the browser gave %q", word, r.id, got[word][r.id])
				continue
			}
			channels := [3]float64{want.R, want.G, want.B}
			for c := 0; c < 3; c++ {
				px, _ := strconv.Atoi(m[c+1])
				if d := math.Abs(channels[c]*255 - float64(px)); d > 1 {
					t.Errorf("%s %s (%s): browser %s, Paint %s: channel %d is %.2f/255 away", word, r.id, r.value, strings.TrimSpace(got[word][r.id]), want.Hex(), c, d)
				}
			}
			if m[4] != "255" {
				t.Errorf("%s %s: the browser's colour is not opaque: %s", word, r.id, got[word][r.id])
			}
		}
	}
}
