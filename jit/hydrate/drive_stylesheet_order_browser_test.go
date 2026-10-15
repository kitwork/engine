package hydrate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// The engine's CSS for a page is one list in its head, files and blocks mixed, in the order of the
// cascade: a Kitwork UI page's site part (a block, or site.<hash>.css under router.bundle "auto" when
// the page is past the first window) before its own part; a template page's JIT blocks css, then
// material, then icons — not layered, so for them the order IS the cascade. After a Drive move every
// part must stand where a whole load of the arriving page puts it, whichever parts were kept, or the
// cascade is not a whole load's: until the fix of 14/10/2026 a new block went to the end of <head> and a new file
// to the end when the page on screen linked none, so a part kept byte for byte (the page block of two
// pages of one look, a template's material block) ended up BEFORE a new part it must follow.
//
// The parts (one rule each pair fights over, the same specificity, so the later one wins):
//
//	site   <style> (S) or site.5555….css (F)  .focus rgb(9,0,0)                .mark rgb(1,1,1) / rgb(2,2,2)
//	css    <style> (C, or D: other bytes)     .focus rgb(0,9,0)  .button-x rgb(0,0,7)
//	material <style> (M, the same everywhere)                   .button-y rgb(7,0,0)
//
// A whole load of every page shows focus=rgb(0,9,0) (css after site) and button=rgb(7,0,0) (material
// after css); mark says which site part applies. The walk is / [S C M] → /linked [F C M] (the site part
// becomes a file, the page block is kept: the case of a .ui page past the window) → /changed [F D M]
// (the first block changes, the second is kept: the template case) → / [S C M]. At each arrival and at
// the first change of <main> (no frame shows the new content in another cascade) one controller
// outside <main> records the look and the order of the parts ("file:" a link, "?" one still off the
// screen), and whether the material block, and the page block on /linked, are still the first page's
// elements. CONTROL: the first load is a whole load; the site file is fetched once.
func TestBrowserDriveKeepsTheCascadeOrderOfFilesAndBlocks(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the stylesheet order browser proof in short mode")
	}
	browser := findHeadlessBrowserForDrive()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	const file = "/_kitwork/site.5555555555555555.css"
	parts := map[string]string{
		"S": `<style data-kitwork-generated="site">.focus{color:rgb(9,0,0)}.mark{color:rgb(1,1,1)}</style>`,
		"F": `<link rel="stylesheet" href="` + file + `" data-kitwork-generated="site">`,
		"C": `<style data-kitwork-generated="css">.focus{color:rgb(0,9,0)}.button-x{color:rgb(0,0,7)}</style>`,
		"D": `<style data-kitwork-generated="css">.focus{color:rgb(0,9,0)}.button-x{color:rgb(0,0,7)}.other{color:red}</style>`,
		"M": `<style data-kitwork-generated="material">.button-y{color:rgb(7,0,0)}</style>`,
	}
	links := `<a id="linked" href="/linked">linked</a><a id="changed" href="/changed">changed</a><a id="home" href="/">home</a>`
	page := func(title, head, extra string) string {
		css := ""
		for _, part := range head {
			css += parts[string(part)]
		}
		return `<!doctype html><html><head><meta charset="utf-8"><title>` + title + `</title>` + css + pageRuntimeTags + `</head>` +
			`<body><main><h1 id="title">` + title + `</h1>` + links + `</main>` +
			`<p id="focus" class="focus">f</p><b id="button" class="button-x button-y">b</b><i id="mark" class="mark">m</i>` + extra + `</body></html>`
	}
	controller := `<script>
	  var log = [], token = {}, watching = "";
	  function generated() { return Array.prototype.slice.call(document.head.querySelectorAll('style[data-kitwork-generated],link[rel="stylesheet"][data-kitwork-generated]')); }
	  function order() {
	    return generated().map(function (n) {
	      return (n.nodeName === "LINK" ? "file:" : "") + n.getAttribute("data-kitwork-generated") + (n.getAttribute("media") === "print" ? "?" : "");
	    }).join(",");
	  }
	  function role(name) { return generated().filter(function (n) { return n.getAttribute("data-kitwork-generated") === name && n.getAttribute("media") !== "print"; }); }
	  function first(name) { var on = role(name); return on.length === 1 && on[0].__first === token; }
	  function color(id) { return getComputedStyle(document.getElementById(id)).color.replace(/ /g, ""); }
	  function looks() { return "focus=" + color("focus") + ",button=" + color("button") + ",mark=" + color("mark") + ",order=" + order(); }
	  new MutationObserver(function () {
	    if (!watching) return;
	    log.push("swap-" + watching + ":" + document.getElementById("title").textContent + "," + looks());
	    watching = "";
	  }).observe(document.querySelector("main"), { childList: true, subtree: true, characterData: true });
	  function go(id) { return function () { watching = id; document.getElementById(id).click(); }; }
	  var steps = [
	    function () {
	      generated().forEach(function (n) { n.__first = token; });
	      log.push("start:" + looks());
	      go("linked")();
	    },
	    function () {
	      log.push("linked:" + looks() + ",css-kept=" + first("css") + ",material-kept=" + first("material"));
	      go("changed")();
	    },
	    function () {
	      log.push("changed:" + looks() + ",css-kept=" + first("css") + ",material-kept=" + first("material"));
	      go("home")();
	    },
	    function () {
	      log.push("home:" + looks() + ",material-kept=" + first("material"));
	      fetch("/report?log=" + encodeURIComponent(log.join(" ")));
	    }
	  ];
	  fetch("/hold"); // a request waiting on its answer keeps the browser's clock running in real time
	  var step = 0;
	  function next() { var run = steps[step++]; if (run) setTimeout(run, 60); }
	  document.addEventListener("kitwork:load", next);
	  window.addEventListener("load", function () { setTimeout(next, 60); });
	</script>`

	var mu sync.Mutex
	reported := make(chan struct{})
	var once sync.Once
	var report string
	hits := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		mu.Lock()
		hits[request.URL.Path+" "+request.Header.Get("X-Kitwork-Hydrate")]++
		mu.Unlock()
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		switch request.URL.Path {
		case "/kit/kernel.js", "/kit/drive.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write([]byte(pageRuntimeFile(request.URL.Path)))
		case file:
			time.Sleep(300 * time.Millisecond) // the swap waits for it
			response.Header().Set("Content-Type", "text/css; charset=utf-8")
			response.Header().Set("Cache-Control", "no-store")
			_, _ = response.Write([]byte(`.focus{color:rgb(9,0,0)}.mark{color:rgb(2,2,2)}`))
		case "/":
			extra := ""
			if request.Header.Get("X-Kitwork-Hydrate") == "" {
				extra = controller
			}
			_, _ = response.Write([]byte(page("home", "SCM", extra)))
		case "/linked":
			_, _ = response.Write([]byte(page("linked", "FCM", "")))
		case "/changed":
			_, _ = response.Write([]byte(page("changed", "FDM", "")))
		case "/hold":
			select {
			case <-reported:
			case <-time.After(15 * time.Second):
			}
		case "/report":
			mu.Lock()
			report = request.URL.Query().Get("log")
			mu.Unlock()
			once.Do(func() { close(reported) })
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	args := []string{"--headless=new", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage",
		"--disable-background-networking", "--disable-default-apps", "--disable-extensions", "--disable-sync",
		"--metrics-recording-only", "--no-first-run", "--run-all-compositor-stages-before-draw",
		"--user-data-dir=" + filepath.Join(t.TempDir(), "p"), "--virtual-time-budget=12000", "--dump-dom", server.URL + "/"}
	if runtime.GOOS == "windows" {
		args = append([]string{"--disable-background-mode", "--disable-breakpad", "--disable-crashpad-for-testing"}, args...)
	}
	output, _ := exec.CommandContext(ctx, browser, args...).CombinedOutput()

	mu.Lock()
	defer mu.Unlock()
	if report == "" {
		t.Fatalf("the walk never reported; requests=%v\n%.700s", hits, output)
	}
	whole := "focus=rgb(0,9,0),button=rgb(7,0,0)" // a whole load's cascade: css after site, material after css
	inline, linked := whole+",mark=rgb(1,1,1),order=site,css,material", whole+",mark=rgb(2,2,2),order=file:site,css,material"
	got := strings.Split(report, " ")
	want := []string{
		"start:" + inline, // CONTROL: the whole load
		"swap-linked:linked," + linked,
		"linked:" + linked + ",css-kept=true,material-kept=true", // the site part became a file, before the kept page block
		"swap-changed:changed," + linked,
		"changed:" + linked + ",css-kept=false,material-kept=true", // the new first block before the kept second one
		"swap-home:home," + inline,
		"home:" + inline + ",material-kept=true",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the cascade across the moves:\n got  %s\n want %s", strings.Join(got, "\n      "), strings.Join(want, "\n      "))
	}
	if hits[file+" "] != 1 {
		t.Errorf("the site file is fetched once in the whole walk, was %d times: %v", hits[file+" "], hits)
	}
	for _, path := range []string{"/linked ", "/changed "} {
		if hits[path] != 0 {
			t.Errorf("a page was loaded whole instead of moved to (%s); requests=%v", path, hits)
		}
	}
}
