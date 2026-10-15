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

// A Kitwork UI page links its CSS as files named by their role and their bytes, the page's own first,
// <link rel="stylesheet" href="/_kitwork/page.<hash>.css" data-kitwork-generated="css">; this test
// walks pages that link that one only (the roles together: the next test). A move to a
// page that links another file must not show the new content before its CSS has arrived, nor the old
// content in the new look while it loads: Drive loads the new file first, kept off the screen, waits
// for it, then swaps the content and removes the old file. A move to a page linking the same file
// leaves it alone: the same element stays, and nothing is fetched again.
//
// The walk is / (a.css) → /same (a.css) → /other (b.css, answered slowly by the server). One
// controller outside <main> lives through every move and records:
//   - on the move to /same: whether the stylesheet element is the one the first page loaded, how many
//     generated links there are, and how many times the browser fetched a.css;
//   - on the move to /other, the moment b.css has loaded — its load event, which runs before the
//     swap Drive waits for it to start: the title still on screen and its colour (still a's: the new
//     file is loaded off the screen), and the media b.css has then;
//   - at the first change of <main>'s content on that move: the colour the new title is painted with
//     (b's, so b.css had arrived), and whether a.css is still linked.
//
// a.css and b.css are answered with no-store, so a link Drive re-created would show as a second fetch.
func TestBrowserDriveSwapsThePageStylesheetOnceItHasLoaded(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the page stylesheet browser proof in short mode")
	}
	browser := findHeadlessBrowserForDrive()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	const a, b = "/_kitwork/page.aaaaaaaaaaaaaaaa.css", "/_kitwork/page.bbbbbbbbbbbbbbbb.css"
	links := `<a id="same" href="/same">same</a><a id="other" href="/other">other</a>`
	page := func(title, sheet, extra string) string {
		return `<!doctype html><html><head><meta charset="utf-8"><title>` + title + `</title>` +
			`<link rel="stylesheet" href="` + sheet + `" data-kitwork-generated="css">` + pageRuntimeTags + `</head>` +
			`<body><main><h1 id="title" class="look">` + title + `</h1>` + links + `</main>` + extra + `</body></html>`
	}
	controller := `<script>
	  var log = [], token = {};
	  function generated() { return document.querySelectorAll('link[rel="stylesheet"][data-kitwork-generated]'); }
	  function linked(href) { return Array.prototype.filter.call(generated(), function (n) { return n.getAttribute("href") === href; }); }
	  function color() { var h = document.getElementById("title"); return h ? getComputedStyle(h).color.replace(/ /g, "") : "none"; }
	  function fetched(href) { return performance.getEntriesByType("resource").filter(function (e) { return e.name.indexOf(href) >= 0; }).length; }
	  var steps = [
	    function () {
	      generated()[0].__first = token;
	      document.getElementById("same").click();
	    },
	    function () {
	      var sheets = generated();
	      log.push("same:kept=" + (sheets.length === 1 && sheets[0].__first === token) + ",links=" + sheets.length + ",fetched=" + fetched("` + a + `"));
	      var main = document.querySelector("main"), seen = false;
	      new MutationObserver(function () {
	        if (seen) return;
	        seen = true;
	        var b = linked("` + b + `");
	        log.push("swap:title=" + document.getElementById("title").textContent + ",color=" + color() + ",a=" + linked("` + a + `").length +
	          ",b=" + b.length + ",media=" + (b.length ? b[0].getAttribute("media") : "-"));
	      }).observe(main, { childList: true, subtree: true, characterData: true });
	      // capturing, so it runs before the link's own load handler, the one that lets the swap start
	      document.addEventListener("load", function (e) {
	        var n = e.target;
	        if (!n || n.nodeName !== "LINK" || n.getAttribute("href") !== "` + b + `") return;
	        log.push("loaded:title=" + document.getElementById("title").textContent + ",color=" + color() + ",media=" + n.getAttribute("media"));
	      }, true);
	      document.getElementById("other").click();
	    },
	    function () {
	      log.push("after:color=" + color() + ",links=" + generated().length + ",a=" + linked("` + a + `").length);
	      fetch("/report?log=" + encodeURIComponent(log.join(" ")));
	    }
	  ];
	  fetch("/hold"); // a request waiting on its answer keeps the browser's clock running in real time
	  var i = 0;
	  function next() { var step = steps[i++]; if (step) setTimeout(step, 60); }
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
		case a:
			response.Header().Set("Content-Type", "text/css; charset=utf-8")
			response.Header().Set("Cache-Control", "no-store")
			_, _ = response.Write([]byte(`.look{color:rgb(1,2,3)}`))
		case b:
			time.Sleep(700 * time.Millisecond) // slow: the swap must wait for it
			response.Header().Set("Content-Type", "text/css; charset=utf-8")
			response.Header().Set("Cache-Control", "no-store")
			_, _ = response.Write([]byte(`.look{color:rgb(4,5,6)}`))
		case "/":
			if request.Header.Get("X-Kitwork-Hydrate") == "" {
				_, _ = response.Write([]byte(page("start", a, controller)))
			} else {
				_, _ = response.Write([]byte(page("start", a, "")))
			}
		case "/same":
			_, _ = response.Write([]byte(page("same", a, "")))
		case "/other":
			_, _ = response.Write([]byte(page("other", b, "")))
		case "/hold":
			select {
			case <-reported:
			case <-time.After(12 * time.Second):
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

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	args := []string{"--headless=new", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage",
		"--disable-background-networking", "--disable-default-apps", "--disable-extensions", "--disable-sync",
		"--metrics-recording-only", "--no-first-run", "--run-all-compositor-stages-before-draw",
		"--user-data-dir=" + filepath.Join(t.TempDir(), "p"), "--virtual-time-budget=10000", "--dump-dom", server.URL + "/"}
	if runtime.GOOS == "windows" {
		args = append([]string{"--disable-background-mode", "--disable-breakpad", "--disable-crashpad-for-testing"}, args...)
	}
	output, _ := exec.CommandContext(ctx, browser, args...).CombinedOutput()

	mu.Lock()
	defer mu.Unlock()
	if report == "" {
		t.Fatalf("the walk never reported; requests=%v\n%.700s", hits, output)
	}
	got := strings.Split(report, " ")
	want := []string{
		"same:kept=true,links=1,fetched=1",                     // the same file: the same element, fetched once
		"loaded:title=same,color=rgb(1,2,3),media=print",       // b.css arrived, kept off the screen until the swap
		"swap:title=other,color=rgb(4,5,6),a=0,b=1,media=null", // the content changes with its CSS on, a.css gone
		"after:color=rgb(4,5,6),links=1,a=0",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the page stylesheet across the moves:\n got  %s\n want %s", strings.Join(got, "\n      "), strings.Join(want, "\n      "))
	}
	if hits[a+" "] != 1 || hits[b+" "] != 1 {
		t.Errorf("each stylesheet is fetched once (a.css on the first page only, b.css on the move): %v", hits)
	}
	for _, path := range []string{"/same ", "/other "} {
		if hits[path] != 0 {
			t.Errorf("a page was loaded whole instead of moved to (%s); requests=%v", path, hits)
		}
	}
}

// A Kitwork UI page links one stylesheet per generator, in the order of the cascade: its own
// (page.<hash>.css), the icons, a logo when it has one, the fonts. A move trades them one role at a
// time: a file both pages link stays the same element and is never fetched again nor taken off, even
// for a moment; a role whose file changed has its new file loaded off the screen and waited for, the
// old one gone at the swap; a role only the arriving page has is added and waited for; a role it lacks
// goes at the swap. After the swap the files stand in the arriving page's order, so the cascade is the
// one a whole load of it would have.
//
// The walk is / → /same → /other → /plain:
//
//	/, /same, /plain   a (page)  i (icons)           f (fonts)
//	/other             b (page)  i (icons)  l (logo)  f (fonts)    b and l answered slowly, l the slower
//
// Each file styles one element: a and b the title's colour (inside <main>), i a mark's background, f a
// face's outline, l a brand's border (inside /other's <main>); elements outside <main> live through
// every move. The cascade is read on #both, which a, b and i all colour with the same specificity:
// i's colour shows only while i stands after the page's own file. One controller records the looks
// and the order of the links (a "?" marks a file still off the screen):
//   - on /same: the same three elements, none fetched again;
//   - when b and when l have loaded (load listeners that run before Drive's own): the old content in
//     the old look, the new files off the screen and placed in the cascade's order — the swap waits
//     for l, the slower;
//   - at the first change of <main> on each move: the new content in its whole look, the links in its
//     order;
//   - at the end: how many times a link of i or f was taken off the page (a MutationObserver on
//     <head>), and whether they are still the first page's elements.
//
// Every file is answered with no-store, so a link Drive re-created would show as a second fetch.
func TestBrowserDriveTradesThePageStylesheetsByRole(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the page stylesheets browser proof in short mode")
	}
	browser := findHeadlessBrowserForDrive()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	const (
		a = "/_kitwork/page.aaaaaaaaaaaaaaaa.css"
		b = "/_kitwork/page.bbbbbbbbbbbbbbbb.css"
		i = "/_kitwork/icons.1111111111111111.css"
		l = "/_kitwork/logo.2222222222222222.css"
		f = "/_kitwork/fonts.3333333333333333.css"
	)
	css := map[string]string{
		a: `.look{color:rgb(1,2,3)}.both{color:rgb(1,1,1)}`,
		b: `.look{color:rgb(4,5,6)}.both{color:rgb(3,3,3)}`,
		i: `.mark{background-color:rgb(7,7,7)}.both{color:rgb(2,2,2)}`,
		l: `.brand{border-top-color:rgb(8,8,8)}`,
		f: `.face{outline-color:rgb(9,9,9)}`,
	}
	slow := map[string]time.Duration{b: 500 * time.Millisecond, l: 1000 * time.Millisecond}
	links := `<a id="same" href="/same">same</a><a id="other" href="/other">other</a><a id="plain" href="/plain">plain</a>`
	page := func(title string, sheets []string, content, extra string) string {
		head := ""
		for _, sheet := range sheets {
			generated := strings.TrimPrefix(sheet, "/_kitwork/")
			generated = generated[:strings.Index(generated, ".")]
			if generated == "page" {
				generated = "css"
			}
			head += `<link rel="stylesheet" href="` + sheet + `" data-kitwork-generated="` + generated + `">`
		}
		return `<!doctype html><html><head><meta charset="utf-8"><title>` + title + `</title>` + head + pageRuntimeTags + `</head>` +
			`<body><main><h1 id="title" class="look">` + title + `</h1>` + content + links + `</main>` +
			`<p id="both" class="both">both</p><span id="mark" class="mark">m</span><span id="face" class="face">f</span>` + extra + `</body></html>`
	}
	plain := []string{a, i, f}
	other := []string{b, i, l, f}
	controller := `<script>
	  var log = [], token = {}, takenOff = 0, watching = "";
	  var names = {"` + a + `": "a", "` + b + `": "b", "` + i + `": "i", "` + l + `": "l", "` + f + `": "f"};
	  function generated() { return document.head.querySelectorAll('link[rel="stylesheet"][data-kitwork-generated]'); }
	  function order() {
	    return Array.prototype.map.call(generated(), function (n) {
	      return (names[n.getAttribute("href")] || "?") + (n.getAttribute("media") === "print" ? "?" : "");
	    }).join("");
	  }
	  function linked(href) { return Array.prototype.filter.call(generated(), function (n) { return n.getAttribute("href") === href; }); }
	  function kept() {
	    var mine = linked("` + i + `").concat(linked("` + f + `"));
	    return mine.length === 2 && mine[0].__first === token && mine[1].__first === token;
	  }
	  function look(id, property) { var n = document.getElementById(id); return n ? getComputedStyle(n)[property].replace(/ /g, "") : "none"; }
	  function looks() {
	    var title = document.getElementById("title");
	    return "title=" + (title ? title.textContent : "none") + ",look=" + look("title", "color") + ",both=" + look("both", "color") +
	      ",mark=" + look("mark", "backgroundColor") + ",face=" + look("face", "outlineColor") + ",brand=" + look("brand", "borderTopColor");
	  }
	  new MutationObserver(function (records) {
	    records.forEach(function (r) {
	      Array.prototype.forEach.call(r.removedNodes, function (n) {
	        var href = n.getAttribute && n.getAttribute("href");
	        if (href === "` + i + `" || href === "` + f + `") takenOff++;
	      });
	    });
	  }).observe(document.head, { childList: true });
	  new MutationObserver(function () {
	    if (!watching) return;
	    log.push(watching + ":" + looks() + ",order=" + order());
	    watching = "";
	  }).observe(document.querySelector("main"), { childList: true, subtree: true, characterData: true });
	  // capturing, so it runs before the link's own load handler, the one that lets the swap start
	  document.addEventListener("load", function (e) {
	    var n = e.target, name = n && n.nodeName === "LINK" ? names[n.getAttribute("href")] : "";
	    if (name === "b" || name === "l") log.push("loaded-" + name + ":" + looks() + ",order=" + order());
	  }, true);
	  var steps = [
	    function () {
	      generated().forEach(function (n) { n.__first = token; });
	      log.push("start:" + looks() + ",order=" + order());
	      document.getElementById("same").click();
	    },
	    function () {
	      log.push("same:kept=" + kept() + ",order=" + order());
	      watching = "swap-other";
	      document.getElementById("other").click();
	    },
	    function () {
	      watching = "swap-plain";
	      document.getElementById("plain").click();
	    },
	    function () {
	      log.push("end:takenOff=" + takenOff + ",kept=" + kept() + ",order=" + order());
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
		if body, ok := css[request.URL.Path]; ok {
			time.Sleep(slow[request.URL.Path])
			response.Header().Set("Content-Type", "text/css; charset=utf-8")
			response.Header().Set("Cache-Control", "no-store")
			_, _ = response.Write([]byte(body))
			return
		}
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		switch request.URL.Path {
		case "/kit/kernel.js", "/kit/drive.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write([]byte(pageRuntimeFile(request.URL.Path)))
		case "/":
			extra := ""
			if request.Header.Get("X-Kitwork-Hydrate") == "" {
				extra = controller
			}
			_, _ = response.Write([]byte(page("start", plain, "", extra)))
		case "/same":
			_, _ = response.Write([]byte(page("same", plain, "", "")))
		case "/other":
			_, _ = response.Write([]byte(page("other", other, `<b id="brand" class="brand">brand</b>`, "")))
		case "/plain":
			_, _ = response.Write([]byte(page("plain", plain, "", "")))
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
	old := "look=rgb(1,2,3),both=rgb(2,2,2),mark=rgb(7,7,7),face=rgb(9,9,9),brand=none"
	got := strings.Split(report, " ")
	want := []string{
		"start:title=start," + old + ",order=aif",
		"same:kept=true,order=aif", // every file the same element, none fetched again
		// b arrived: the old content, all its files on; b and l off the screen, already in the cascade's place
		"loaded-b:title=same," + old + ",order=ab?il?f",
		// l arrived too: still the old content — the swap waits for every file that changes
		"loaded-l:title=same," + old + ",order=ab?il?f",
		// the swap: the new content in its whole look, a gone, i still after the page's own file (#both)
		"swap-other:title=other,look=rgb(4,5,6),both=rgb(2,2,2),mark=rgb(7,7,7),face=rgb(9,9,9),brand=rgb(8,8,8),order=bilf",
		// back to a page with no logo: l gone with b, a in its place before i
		"swap-plain:title=plain," + old + ",order=aif",
		"end:takenOff=0,kept=true,order=aif", // i and f never left the page, not even for a moment
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the page stylesheets across the moves:\n got  %s\n want %s", strings.Join(got, "\n      "), strings.Join(want, "\n      "))
	}
	for _, sheet := range []string{i, f, b, l} {
		if hits[sheet+" "] != 1 {
			t.Errorf("%s must be fetched once in the whole walk, was %d times: %v", sheet, hits[sheet+" "], hits)
		}
	}
	for _, path := range []string{"/same ", "/other ", "/plain "} {
		if hits[path] != 0 {
			t.Errorf("a page was loaded whole instead of moved to (%s); requests=%v", path, hits)
		}
	}
}
