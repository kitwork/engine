package fonts

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// The weight axis the catalog claims for each variable family (vendor.py's "weights") is the one its
// file draws, measured in a browser: each family's latin file is loaded under a face that takes every
// weight (1–1000, no synthesis), and text is drawn at 1, 50, 100…900, 950 and 1000. Inside the axis
// every step draws more ink than the one before — up to both ends, so a claim of 1–1000 is checked at
// 1 and at 1000; outside it the font holds at its end. A claimed range wider or narrower than the
// file's would ship a weight the file cannot draw, or leave out one it can. A family whose axes list
// opsz (Family.Axes: the axes Kitwork serves) is drawn at both ends of it: the widths differ, so the
// file has the axis.
//
// A family Google lists as Monospace draws every letter at one width in the file Kitwork embeds: a run
// of i, of M, of W, of 0 and of dots is one width. Google Sans Code is monospaced only at its MONO axis'
// default, 1, which vendor.py leaves to Google by not asking for the axis: a file at MONO 0 would set
// code in a proportional face. CONTROL: Google Sans Flex, proportional, draws the i narrower than the M.
func TestBrowserWeightAxesAreTheFilesOwn(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the font axis proof in short mode")
	}
	browser := findHeadlessBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	var slugs []string
	for slug, fam := range catalog {
		if fam.weights[0] > 0 {
			slugs = append(slugs, slug)
		}
	}
	sort.Strings(slugs)
	if len(slugs) == 0 {
		t.Fatal("no variable family in the catalog")
	}
	var faces strings.Builder
	files := map[string][]byte{}
	for _, slug := range slugs {
		path := ""
		for _, f := range catalog[slug].faces {
			if f.subset == "latin" && f.style == "normal" && path == "" {
				path = f.file
			}
		}
		body, err := FS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		files["/"+slug+".woff2"] = body
		faces.WriteString("@font-face{font-family:'probe-" + slug + "';font-weight:1 1000;font-synthesis:none;src:url(/" + slug + ".woff2) format('woff2')}\n")
	}
	probes := []int{1, 50, 100, 200, 300, 400, 500, 600, 700, 800, 900, 950, 1000}
	opsz := map[string][2]float64{}
	var monospace []string
	for _, slug := range slugs {
		fam, ok := Find(catalog[slug].name)
		if !ok {
			t.Fatalf("%s is not in the Google catalog", slug)
		}
		if a, ok := fam.axis("opsz"); ok {
			opsz[slug] = [2]float64{a.Min, a.Max}
		}
		if fam.Category == "Monospace" {
			monospace = append(monospace, slug)
		}
	}
	if !contains(monospace, "google-sans-code") || contains(monospace, "google-sans-flex") {
		t.Fatalf("Google Sans Code is measured as a Monospace family and Google Sans Flex is not: %v", monospace)
	}
	runs := []string{"iiiiiiiiii", "MMMMMMMMMM", "WWWWWWWWWW", "0000000000", ".........."}
	list, _ := json.Marshal(slugs)
	probeList, _ := json.Marshal(probes)
	opszList, _ := json.Marshal(opsz)
	widthList, _ := json.Marshal(append(append([]string(nil), monospace...), "google-sans-flex"))
	runList, _ := json.Marshal(runs)
	html := `<!doctype html><html><head><meta charset="utf-8"><style>` + faces.String() + `</style></head><body><script>
(async () => {
  const slugs = ` + string(list) + `, probes = ` + string(probeList) + `, opsz = ` + string(opszList) + `;
  const widths = ` + string(widthList) + `, runs = ` + string(runList) + `;
  const out = { ink: {}, opsz: {}, widths: {} };
  const canvas = document.createElement("canvas"); canvas.width = 900; canvas.height = 80;
  const ctx = canvas.getContext("2d", { willReadFrequently: true });
  for (const slug of slugs) {
    out.ink[slug] = [];
    for (const w of probes) {
      const font = w + " 48px 'probe-" + slug + "'";
      await document.fonts.load(font, "Hamburgefonstiv 0123");
      ctx.clearRect(0, 0, 900, 80); ctx.font = font; ctx.fillStyle = "#000";
      ctx.fillText("Hamburgefonstiv 0123", 4, 60);
      const data = ctx.getImageData(0, 0, 900, 80).data;
      let ink = 0;
      for (let i = 3; i < data.length; i += 4) ink += data[i];
      out.ink[slug].push(ink);
    }
  }
  for (const slug of Object.keys(opsz)) {
    out.opsz[slug] = [];
    for (const v of opsz[slug]) {
      const span = document.createElement("span");
      span.style.cssText = "position:absolute;white-space:nowrap;font:400 48px 'probe-" + slug + "';font-variation-settings:'opsz' " + v;
      span.textContent = "Hamburgefonstiv 0123";
      document.body.appendChild(span);
      out.opsz[slug].push(span.getBoundingClientRect().width);
      span.remove();
    }
  }
  for (const slug of widths) {
    const font = "400 48px 'probe-" + slug + "'";
    await document.fonts.load(font, runs.join(""));
    ctx.font = font;
    out.widths[slug] = runs.map((run) => ctx.measureText(run).width);
  }
  fetch("/report", { method: "POST", body: JSON.stringify(out) });
})();
</script></body></html>`

	var mu sync.Mutex
	var report []byte
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if body, ok := files[request.URL.Path]; ok {
			response.Header().Set("Content-Type", "font/woff2")
			_, _ = response.Write(body)
			return
		}
		switch request.URL.Path {
		case "/":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(response, html)
		case "/report":
			body, _ := io.ReadAll(request.Body)
			mu.Lock()
			report = body
			mu.Unlock()
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	args := []string{"--headless=new", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage",
		"--disable-background-networking", "--disable-default-apps", "--disable-extensions", "--disable-sync",
		"--metrics-recording-only", "--no-first-run", "--window-size=1000,400",
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
	var measured struct {
		Ink    map[string][]int      `json:"ink"`
		Opsz   map[string][2]float64 `json:"opsz"`
		Widths map[string][]float64  `json:"widths"`
	}
	if err := json.Unmarshal(report, &measured); err != nil {
		t.Fatalf("report %s: %v", report, err)
	}
	index := map[int]int{}
	for i, w := range probes {
		index[w] = i
	}
	for _, slug := range slugs {
		low, high := catalog[slug].weights[0], catalog[slug].weights[1]
		drawn := measured.Ink[slug]
		if len(drawn) != len(probes) {
			t.Fatalf("%s: %v", slug, drawn)
		}
		first, okLow := index[low]
		last, okHigh := index[high]
		if !okLow || !okHigh {
			t.Fatalf("%s claims %d–%d: add both ends to the probes %v", slug, low, high, probes)
		}
		for i := first + 1; i <= last; i++ {
			if drawn[i] <= drawn[i-1] {
				t.Errorf("%s claims %d–%d, but %d draws no more ink than %d: %v", slug, low, high, probes[i], probes[i-1], drawn)
			}
		}
		if first > 0 && drawn[first-1] != drawn[first] {
			t.Errorf("%s claims its axis starts at %d, but %d draws differently: the file goes lower: %v", slug, low, probes[first-1], drawn)
		}
		if last < len(probes)-1 && drawn[last+1] != drawn[last] {
			t.Errorf("%s claims its axis ends at %d, but %d draws differently: the file goes higher: %v", slug, high, probes[last+1], drawn)
		}
	}
	for slug, ends := range opsz {
		widths, ok := measured.Opsz[slug]
		if !ok || widths[0] <= 0 || widths[0] == widths[1] {
			t.Errorf("%s claims opsz %g–%g, but draws %v px wide at both ends: the file has no optical size axis", slug, ends[0], ends[1], widths)
		}
	}
	for _, slug := range monospace {
		widths := measured.Widths[slug]
		if len(widths) != len(runs) || widths[0] <= 0 {
			t.Fatalf("%s: %v", slug, widths)
		}
		for i, width := range widths {
			if math.Abs(width-widths[0]) > 0.01 {
				t.Errorf("%s is a Monospace family, but %q draws %.2f px wide and %q %.2f px: the file is not monospaced", slug, runs[i], width, runs[0], widths[0])
			}
		}
	}
	if flex := measured.Widths["google-sans-flex"]; len(flex) != len(runs) || flex[0] <= 0 || flex[1]-flex[0] < 10 {
		t.Errorf("CONTROL: Google Sans Flex draws a run of i narrower than a run of M, or the measure tells nothing: %v", flex)
	}
}

func findHeadlessBrowser() string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("KITWORK_BROWSER_TESTS"))) {
	case "0", "false", "off", "no":
		return ""
	}
	for _, name := range []string{"google-chrome", "chromium", "chromium-browser", "chrome", "msedge"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	if runtime.GOOS != "windows" {
		return ""
	}
	for _, path := range []string{
		filepath.Join(os.Getenv("ProgramFiles"), "Google", "Chrome", "Application", "chrome.exe"),
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Google", "Chrome", "Application", "chrome.exe"),
	} {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return ""
}
