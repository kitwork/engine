package work

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// driveRolesFiles is a site whose three pages differ in one more role at each step: /hai writes other
// classes than / (its page file) and shows the same icon; /nhanh is a branch with its own router.colors
// (its colours file), another icon (its icons file) and classes of its own (a page file neither of the
// others links). The reboot (the engine's) and the fonts (one file per site) are the same on all three.
// router.bundle(false): every role a file, the delivery these moves trade (the default writes the CSS
// in the page: TestBrowserDriveSwapsInlineCSS).
var driveRolesFiles = map[string]string{
	"localhost/router.work": `import { router } from "kitwork";

router.colors({ brand: "#c8742c", canvas: "#f6f3ee", paper: "#ffffff", ink: "#2b211b", line: "#e6ded5" });
router.fonts({ body: "Outfit" });
router.reload(false);
router.bundle(false);
`,
	"localhost/page.ui": `<page><content /></page>`,
	"localhost/content.ui": `<content gap="4" padding="4">
  <text id="title" heading="1" color="brand">Một</text>
  <icon value="coffee" alt="cà phê"></icon>
  <link id="next" value="/hai">Hai</link>
</content>`,
	"localhost/hai/content.ui": `<content gap="4" padding="6">
  <text id="title" heading="1" color="ink-30">Hai</text>
  <icon value="coffee" alt="cà phê"></icon>
  <link id="next" value="/nhanh">Nhánh</link>
</content>`,
	"localhost/nhanh/router.work": `import { router } from "kitwork";

router.colors({ brand: "#2f6fde" });
`,
	"localhost/nhanh/content.ui": `<content gap="2" padding="4">
  <text id="title" heading="1" color="brand">Nhánh</text>
  <icon value="mug" alt="cốc"></icon>
</content>`,
}

// driveRolesController walks / → /hai → /nhanh by clicking the links (Drive moves, no reload) and, at
// each arrival (kitwork:load, which Drive sends once the swap is done), reads the page's stylesheet
// links against the ones before the move: per role (data-kitwork-generated), "kept" when it is the
// same element, "traded" when another; the roles whose link was taken off the page during the move
// (a MutationObserver on <head>); the roles in the order they stand; how many links are still off the
// screen (media="print"); and the colour the title is painted with, which the arriving page's files
// decide: "ink-30" when it is the site's ink 30% of the way toward its canvas, as the browser itself
// mixes the two declared colours (a lighter colour's computed value is the browser's to write).
const driveRolesController = `<script>
(function () {
  var log = {}, removed = [], before = {}, arrivals = ["hai", "nhanh"];
  function links() { return Array.prototype.slice.call(document.head.querySelectorAll('link[rel="stylesheet"][data-kitwork-generated]')); }
  function role(node) { return node.getAttribute("data-kitwork-generated"); }
  function remember() { before = {}; links().forEach(function (node) { before[role(node)] = node; }); removed = []; }
  function taken(records) {
    records.forEach(function (record) {
      Array.prototype.forEach.call(record.removedNodes, function (node) {
        if (node.nodeName === "LINK" && node.getAttribute("data-kitwork-generated")) removed.push(role(node));
      });
    });
  }
  var observer = new MutationObserver(taken);
  observer.observe(document.head, { childList: true });
  var shade = (function () {
    var sample = document.createElement("span");
    sample.style.color = "color-mix(in oklab,#2b211b 70%,#f6f3ee)";
    if (!sample.style.color) return "no color-mix";
    document.documentElement.appendChild(sample);
    var color = getComputedStyle(sample).color;
    sample.remove();
    return color;
  })();
  function title() {
    var color = getComputedStyle(document.getElementById("title")).color;
    return "title=" + (color === shade ? "ink-30" : color.replace(/ /g, ""));
  }
  function read() {
    taken(observer.takeRecords()); // kitwork:load runs inside the swap, before the observer is told
    var words = links().map(function (node) { return role(node) + "=" + (before[role(node)] === node ? "kept" : "traded"); });
    words.push("removed=" + (removed.join(",") || "none"));
    words.push("order=" + links().map(role).join(","));
    words.push("staged=" + links().filter(function (node) { return node.getAttribute("media") === "print"; }).length);
    words.push(title());
    return words.join(" ");
  }
  function send() {
    var close = function () { window.close(); };
    fetch("/report", { method: "POST", body: JSON.stringify(log) }).then(close, close);
  }
  function move() { remember(); setTimeout(function () { document.getElementById("next").click(); }, 50); }
  document.addEventListener("kitwork:load", function () {
    var at = arrivals.shift();
    if (!at) return;
    log[at] = read();
    if (arrivals.length) move(); else send();
  });
  window.addEventListener("load", function () { log.start = title(); move(); });
})();
</script>`

// TestBrowserDriveTradesOnlyTheRolesThatChanged walks a Kitwork UI site whose pages link the five
// roles (reboot, colors, fonts, icons, page) and checks Drive moves trade exactly the roles whose file
// changed: on / → /hai the page file only; on /hai → /nhanh (a branch with its own colours and icon)
// colors, icons and the page file. The reboot and the fonts stay the same elements through the walk:
// never taken off, never fetched again (every stylesheet is answered no-store, so a link Drive
// re-created would be a second request). After each move the links stand in the cascade's order,
// none is left off the screen, and the title takes the arriving page's colour. The trades are the
// control: the same reading that says "kept" says "traded" when the file changed.
func TestBrowserDriveTradesOnlyTheRolesThatChanged(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the Drive roles browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant := buildUISite(t, driveRolesFiles)

	// The site is the one the walk needs: per page, the href of each role.
	hrefs := map[string]map[string]string{}
	for _, page := range []string{"/", "/hai", "/nhanh"} {
		answer := serveUI(t, tenant, "http://localhost"+page)
		if answer.Code != http.StatusOK {
			t.Fatalf("%s = %d:\n%.800s", page, answer.Code, answer.Body.String())
		}
		hrefs[page] = map[string]string{}
		for _, sheet := range stylesheetsOf(t, tenant, answer.Body.String()) {
			hrefs[page][sheet.generated] = sheet.href
		}
	}
	for _, step := range []struct {
		role, from, to string
		same           bool
	}{
		{"reboot", "/", "/hai", true}, {"reboot", "/hai", "/nhanh", true},
		{"fonts", "/", "/hai", true}, {"fonts", "/hai", "/nhanh", true},
		{"colors", "/", "/hai", true}, {"colors", "/hai", "/nhanh", false},
		{"icons", "/", "/hai", true}, {"icons", "/hai", "/nhanh", false},
		{"css", "/", "/hai", false}, {"css", "/hai", "/nhanh", false},
	} {
		if same := hrefs[step.from][step.role] == hrefs[step.to][step.role]; same != step.same {
			t.Fatalf("the site is not the walk's: the %s files of %s and %s are the same: %v, want %v\n%v", step.role, step.from, step.to, same, step.same, hrefs)
		}
	}

	var serving sync.Mutex
	hits := map[string]int{}
	moved := map[string]string{}
	reports := make(chan map[string]string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		path := request.URL.Path
		if path == "/report" {
			var report map[string]string
			body, _ := io.ReadAll(request.Body)
			if err := json.Unmarshal(body, &report); err != nil {
				t.Errorf("report %s: %v", body, err)
			}
			select {
			case reports <- report:
			default:
			}
			return
		}
		forward := request.Clone(request.Context())
		forward.Host = "localhost"
		forward.Header.Del("Accept-Encoding") // the controller goes into the document as text
		answer := httptest.NewRecorder()
		serving.Lock()
		hits[path]++
		if path == "/hai" || path == "/nhanh" {
			moved[path] = request.Header.Get("X-Kitwork-Hydrate")
		}
		tenant.Serve(answer, forward)
		serving.Unlock()
		for name, values := range answer.Header() {
			response.Header()[name] = values
		}
		body := answer.Body.String()
		if strings.HasSuffix(path, ".css") {
			response.Header().Set("Cache-Control", "no-store")
		}
		if path == "/" && request.Header.Get("X-Kitwork-Hydrate") == "" {
			at := strings.LastIndex(body, "</body>")
			if at < 0 {
				t.Errorf("/ has no </body>:\n%.500s", body)
			} else {
				body = body[:at] + driveRolesController + body[at:]
			}
			response.Header().Del("Content-Length")
		}
		response.WriteHeader(answer.Code)
		_, _ = io.WriteString(response, body)
	}))
	defer server.Close()

	got := runBrowser(t, browser, server.URL+"/", "drive roles", 1000, "", reports)

	order := "order=reboot,colors,fonts,icons,css"
	want := map[string]string{
		"start": "title=rgb(200,116,44)",
		"hai":   "reboot=kept colors=kept fonts=kept icons=kept css=traded removed=css " + order + " staged=0 title=ink-30",
		"nhanh": "reboot=kept colors=traded fonts=kept icons=traded css=traded removed=colors,icons,css " + order + " staged=0 title=rgb(47,111,222)",
	}
	for _, step := range []string{"start", "hai", "nhanh"} {
		if got[step] != want[step] {
			t.Errorf("%s:\n got  %s\n want %s", step, got[step], want[step])
		}
	}
	serving.Lock()
	defer serving.Unlock()
	for _, page := range []string{"/hai", "/nhanh"} {
		if moved[page] == "" {
			t.Errorf("%s was loaded whole, not moved to by Drive (requests %v)", page, hits)
		}
	}
	for page, roles := range hrefs {
		for role, href := range roles {
			if hits[href] != 1 {
				t.Errorf("the %s file of %s (%s) was fetched %d times, want once over the walk", role, page, href, hits[href])
			}
		}
	}
}
