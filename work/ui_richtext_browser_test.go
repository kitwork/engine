package work

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kitwork/engine/utilities/richtext"
)

// richtextBrowserFiles is a site that moves with Drive (router.reload(false)) whose first page is a
// form a person writes a post in: a plain <textarea name="body"> and a submit button, posted to the
// page itself. Its router.post reads the field from the body (request.form), cleans it (richtext of
// "kitwork") and answers with the page again, which shows the post through <richtext> and its cleaned
// HTML in a block of code, and gives the textarea back what was written. A link goes to /tiep, which
// links back: the form is reached by Drive's moves too.
var richtextBrowserFiles = map[string]string{
	"localhost/router.work": `import { router, richtext } from "kitwork";

router.language("en");
router.reload(false);

router.view((ctx) => {
  const source = "";
  const post = richtext(source);
});

router.post((ctx) => {
  const source = ctx.request.form("body");
  return ctx.view({ source, post: richtext(source) });
});
`,
	"localhost/view.ui": `<view><content /></view>`,
	"localhost/content.ui": `<content padding="4" gap="3">
  <form id="write" method="post" gap="2">
    <field><label>Post</label><textarea name="body" rows="6" value="{{ source }}"></textarea></field>
    <button id="send" type="submit">Send</button>
  </form>
  <richtext value="{{ post.value }}" />
  <code language="html">{{ post.value }}</code>
  <link id="next" value="/tiep">Next</link>
</content>`,
	"localhost/tiep/content.ui": `<content padding="4" gap="2">
  <text id="here">Next</text>
  <link id="back" value="/">Back</link>
</content>`,
}

// richtextAttack is what the person types: every way a post could run something on the page that
// shows it (a script, an event attribute on an element kept and on one dropped, javascript: links
// written plainly and with spaces and mixed case, a script inside svg, an iframe's srcdoc, a details
// that opens itself, a stylesheet, an image that fails, a way out of the textarea the post is given
// back in), beside what a post keeps: a heading, bold and italic, a link to another host, a link on
// the site, and a list with a list inside it. Each attack that runs leaves its name in window.__ran.
const richtextAttack = `<h1 onclick="window.__ran='h1'">Notes from the café</h1>` +
	`<p style="color:red" onmouseover="window.__ran='hover'">Coffee <b>with milk</b>, <i>no sugar</i>. ` +
	`<a href="javascript:window.__ran='href'">a javascript link</a>, <a href=" JaVaScRiPt:window.__ran='spaced'">a spaced one</a>, ` +
	`<a href="https://maps.example/cafe?a=1&b=2">the map</a>, <a href="/menu">the menu</a>, <a href="//evil.example/x">a link with no scheme</a>.</p>` +
	`<script>window.__ran='script'</script>` +
	`<img src="/nothing.png" onerror="window.__ran='img'">` +
	`<svg onload="window.__ran='svg'"><script>window.__ran='svg script'</script></svg>` +
	`<iframe srcdoc="&lt;script&gt;parent.__ran='iframe'&lt;/script&gt;"></iframe>` +
	`<details open ontoggle="window.__ran='details'"><summary>More</summary>hidden</details>` +
	`<ul><li>Bạc xỉu<ul><li>with ice</li></ul></li><li>Trà đào</li></ul>` +
	`<style>body{display:none}</style>` +
	`</textarea><script>window.__ran='textarea'</script>`

// richtextRead is what the page holds once the post is answered, read in the browser after the page
// has had a moment for anything the post could start (an image's error, a details' toggle): whether
// any attack ran, whether the document is the one the form was sent from (window.__before), where it
// is, what the richtext holds (its HTML as the browser reads it back, its parts), the cleaned HTML the
// code block shows, what the textarea was given back, every event attribute and every address that
// would run script anywhere in the document, and each link of the richtext with its rel.
const richtextRead = `new Promise(function (done) { setTimeout(done, 300); }).then(function () {
  var post = document.querySelector(".richtext"), code = document.querySelector("pre code.language-html");
  var events = [], scripts = [];
  document.querySelectorAll("*").forEach(function (element) {
    Array.prototype.forEach.call(element.attributes, function (attribute) {
      var name = attribute.name.toLowerCase(), value = attribute.value.replace(/[\u0000-\u0020]/g, "").toLowerCase();
      if (name.indexOf("on") === 0) events.push(element.tagName + " " + name);
      if (/^(href|src|action|formaction|srcdoc|xlink:href)$/.test(name) && /^(javascript|vbscript|data):/.test(value)) scripts.push(element.tagName + " " + name);
      if (name === "srcdoc") scripts.push(element.tagName + " srcdoc");
    });
  });
  var links = Array.prototype.map.call(post ? post.querySelectorAll("a") : [], function (a) {
    return a.textContent + " -> " + a.getAttribute("href") + (new URL(a.href).host === location.host ? " here" : " away") + " rel=" + a.getAttribute("rel");
  });
  var count = function (selector) { return post ? post.querySelectorAll(selector).length : -1; };
  return JSON.stringify({
    ran: window.__ran === undefined ? "nothing" : String(window.__ran),
    fresh: window.__before === undefined,
    path: location.pathname + location.search,
    html: post ? post.innerHTML : "no richtext",
    parts: ["h2", "strong", "em", "ul", "li", "ul ul", "script", "img", "svg", "iframe", "style", "details"].map(function (s) { return s + " " + count(s); }).join(", "),
    code: code ? code.textContent : "no code",
    written: document.querySelector("textarea").value,
    events: events.join(", ") || "none",
    scripts: scripts.join(", ") || "none",
    links: links.join(" | "),
    shown: getComputedStyle(document.body).display
  });
})`

// richtextHit is one request the browser sent the site, with what the test reads of it and the status
// it was answered with.
type richtextHit struct {
	method, path, hydrate, site, kind string
	status                            int
}

// richtextStatus keeps the status a response was written with.
type richtextStatus struct {
	http.ResponseWriter
	status int
}

func (w *richtextStatus) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *richtextStatus) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (w *richtextStatus) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// TestBrowserRichtextPost proves a post written in a browser and shown back, end to end on pages the
// engine serves, in headless Chrome or Edge with the kernel and Drive on the page:
//   - the page is reached by Drive (to /tiep and back, each fetched with X-Kitwork-Hydrate); the
//     person types the attack into the textarea (Input.insertText) and presses Send with the mouse;
//   - Drive steps aside for the POST: the browser sends it itself, urlencoded, to the page's own
//     address, with no X-Kitwork-Hydrate, as same-origin, and loads the answer as a new document (the
//     marker set before sending is gone);
//   - router.post read the body's field and cleaned it: the richtext holds exactly what
//     richtext.Display writes for the cleaned post (h1 as h2, b as strong, i as em, the inner list
//     flattened into the outer, three items), the code block shows exactly richtext.Clean's HTML, and
//     the textarea is given back what was typed, to the letter (its way out stayed words);
//   - nothing the attack wrote ran (window.__ran unset, the body still shown), no element anywhere in
//     the document has an event attribute or an address that runs script, no srcdoc, and the image
//     the attack wrote was never fetched;
//   - the link to another host carries rel="external noopener", the link on the site none, and the
//     javascript: links and the link with no scheme are words;
//   - a page of another site that posts to the same route is refused with 403 before the handler, both
//     from another site (localhost against 127.0.0.1: cross-site) and from a sibling on the same host
//     (another port of 127.0.0.1: same-site), and the browser shows the refusal.
//
// CONTROL, each against a copy served through -overlay (the tree untouched), the proof failing where
// named (run 20/10/2026): richtext() of "kitwork" answering the source as it came (work/richtext.go: no
// Clean) left the richtext right, the page cleaning it again, but the code block showed the attack
// ("the code block shows"); richtextOp writing its value as it came (ui/richtext.go: no
// richtext.Display) lost the map link's rel ("links"); both together ran the attack (window.__ran
// "svg"), hid the body under its stylesheet, left five event attributes, two javascript: hrefs and a
// srcdoc in the document, and fetched the attack's image (GET /nothing.png); Drive's submit listener
// taking a post form as its own (drive.js: the method check gone) sent no POST, Drive fetching
// GET /?body=… ("the browser sent no POST of its own"); crossOriginRefusal letting every request
// through answered both other sites' posts 200 ("a post from cross-site", "from same-site").
func TestBrowserRichtextPost(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the richtext browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant := buildUISite(t, richtextBrowserFiles)
	first := serveUI(t, tenant, "http://localhost/")
	if first.Code != http.StatusOK || warningsScriptRe.MatchString(first.Body.String()) {
		t.Fatalf("/ = %d, or it links warnings:\n%.3000s", first.Code, first.Body.String())
	}
	for _, want := range []string{`method="post"`, `<textarea`, `name="body"`, `<div class="richtext"></div>`, `/_kitwork/drive.`} {
		if !strings.Contains(first.Body.String(), want) {
			t.Errorf("the page as the server writes it has no %s", want)
		}
	}

	var mu, serving sync.Mutex
	var hits []richtextHit
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		forward := request.Clone(request.Context())
		forward.Host = "localhost"
		written := &richtextStatus{ResponseWriter: response}
		serving.Lock()
		tenant.Serve(written, forward)
		serving.Unlock()
		mu.Lock()
		hits = append(hits, richtextHit{method: request.Method, path: request.URL.RequestURI(), hydrate: request.Header.Get("X-Kitwork-Hydrate"),
			site: request.Header.Get("Sec-Fetch-Site"), kind: request.Header.Get("Content-Type"), status: written.status})
		mu.Unlock()
	}))
	defer server.Close()
	// heard is the requests the site was sent, from the n-th on
	heard := func(from int) []richtextHit {
		mu.Lock()
		defer mu.Unlock()
		return append([]richtextHit(nil), hits[min(from, len(hits)):]...)
	}
	waitFor := func(what string, test func() bool) {
		t.Helper()
		for tries := 0; !test(); tries++ {
			if tries == 200 {
				t.Fatalf("waited 10s for %s: requests %+v", what, heard(0))
			}
			time.Sleep(50 * time.Millisecond)
		}
	}

	d := openDevtools(t, browser, server.URL+"/", 1000, 900, "")
	d.loaded(exampleSettle)
	// to /tiep and back by Drive: the form is reached the way a person on the site reaches it
	moves := len(heard(0))
	d.evaluate(`document.getElementById("next").click()`)
	waitFor("Drive's move to /tiep", func() bool { return d.evaluate(`String(!!document.getElementById("here"))`) == "true" })
	d.evaluate(`document.getElementById("back").click()`)
	waitFor("Drive's move back", func() bool { return d.evaluate(`String(!!document.getElementById("send"))`) == "true" })
	var driven []string
	for _, h := range heard(moves) {
		driven = append(driven, fmt.Sprintf("%s %s %d hydrate=%s", h.method, h.path, h.status, h.hydrate))
	}
	if got := strings.Join(driven, ", "); got != "GET /tiep 200 hydrate=1, GET / 200 hydrate=1" {
		t.Fatalf("the moves to /tiep and back are Drive's: %s", got)
	}

	// the person types the post and presses Send
	sent := len(heard(0))
	d.evaluate(`window.__before = true; window.__stale = true; document.querySelector("textarea").focus()`)
	d.call("Input.insertText", map[string]any{"text": richtextAttack})
	if got := d.evaluate(`document.querySelector("textarea").value`); got != richtextAttack {
		t.Fatalf("the textarea holds %q after typing", got)
	}
	var at struct{ X, Y float64 }
	if err := json.Unmarshal([]byte(d.evaluate(`(function () { var r = document.getElementById("send").getBoundingClientRect(); return JSON.stringify({ x: r.left + r.width / 2, y: r.top + r.height / 2 }); })()`)), &at); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"mouseMoved", "mousePressed", "mouseReleased"} {
		d.call("Input.dispatchMouseEvent", map[string]any{"type": kind, "x": at.X, "y": at.Y, "button": "left", "clickCount": 1})
	}
	// besides the engine's own files (the fonts the typed words and the answer need), one request: the
	// browser's POST, read once the answer has had its moment (richtextRead)
	const browserPost = `POST / 200 hydrate="" site=same-origin application/x-www-form-urlencoded`
	sentByThePage := func() string {
		var posts []string
		for _, h := range heard(sent) {
			if h.method != http.MethodGet || !strings.HasPrefix(h.path, "/_kitwork/") {
				posts = append(posts, fmt.Sprintf("%s %s %d hydrate=%q site=%s %s", h.method, h.path, h.status, h.hydrate, h.site, h.kind))
			}
		}
		return strings.Join(posts, ", ")
	}
	waitFor("the request the form sends", func() bool { return sentByThePage() != "" })
	if got := sentByThePage(); !strings.HasPrefix(got, browserPost) {
		t.Fatalf("the browser sent no POST of its own:\n got  %s\n want %s", got, browserPost)
	}
	d.loaded(`(window.__stale ? "wait of 0" : ` + exampleSettle + `)`)

	var got struct{ Ran, Path, HTML, Parts, Code, Written, Events, Scripts, Links, Shown string }
	var fresh struct{ Fresh bool }
	read := d.evaluate(richtextRead)
	if err := json.Unmarshal([]byte(read), &got); err != nil || json.Unmarshal([]byte(read), &fresh) != nil {
		t.Fatalf("%v: %s", err, read)
	}
	t.Logf("the answer to the POST, as the browser holds it: %s", read)
	cleaned := richtext.Clean(richtextAttack).HTML
	check := func(what, got, want string) {
		t.Helper()
		if got != want {
			t.Errorf("%s:\n got  %s\n want %s", what, got, want)
		}
	}
	check("what the page sent (the attack's image never asked for)", sentByThePage(), browserPost)
	check("what ran", got.Ran, "nothing")
	check("the body is shown", got.Shown, "flex")
	check("event attributes in the document", got.Events, "none")
	check("addresses that run script in the document", got.Scripts, "none")
	if !fresh.Fresh {
		t.Error("the answer to the POST is not a new document: Drive took the post form")
	}
	check("where the answer is", got.Path, "/")
	check("the richtext as the browser reads it back", got.HTML, richtext.Display(cleaned, "http://localhost"))
	check("the code block shows", got.Code, cleaned)
	check("the textarea is given back", got.Written, richtextAttack)
	check("the richtext's parts", got.Parts, "h2 1, strong 1, em 1, ul 1, li 3, ul ul 0, script 0, img 0, svg 0, iframe 0, style 0, details 0")
	check("links", got.Links, "the map -> https://maps.example/cafe?a=1&b=2 away rel=external noopener | the menu -> /menu here rel=null")
	for _, want := range []string{"Notes from the café", "a javascript link", "a spaced one", "a link with no scheme", "Bạc xỉu", "with ice", "Trà đào"} {
		if !strings.Contains(got.HTML, want) {
			t.Errorf("the richtext lost the words %q: %s", want, got.HTML)
		}
	}

	// a page of another site posts to the same route: refused before the handler, and the browser
	// shows the refusal
	attacker := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(response, `<!doctype html><title>Another site</title><form method="post" action="%s/"><input type="hidden" name="body" value="&lt;b&gt;from another site&lt;/b&gt;"></form><script>document.forms[0].submit()</script>`, server.URL)
	}))
	defer attacker.Close()
	for _, from := range []struct{ site, url string }{
		{"cross-site", strings.Replace(attacker.URL, "127.0.0.1", "localhost", 1) + "/"},
		{"same-site", attacker.URL + "/"},
	} {
		before := len(heard(0))
		d.call("Page.navigate", map[string]any{"url": from.url})
		waitFor("the post of "+from.site, func() bool {
			for _, h := range heard(before) {
				if h.method == http.MethodPost {
					return true
				}
			}
			return false
		})
		var refused []string
		for _, h := range heard(before) {
			if h.method != http.MethodGet {
				refused = append(refused, fmt.Sprintf("%s %s %d site=%s", h.method, h.path, h.status, h.site))
			}
		}
		check("a post from "+from.site, strings.Join(refused, ", "), "POST / 403 site="+from.site)
		waitFor("the refusal on screen", func() bool {
			return d.evaluate(`location.href + " " + document.readyState`) == server.URL+"/ complete"
		})
		if shown := d.evaluate(`document.body.innerText`); !strings.Contains(shown, "POST / bị từ chối: request đến từ trang của một site khác (Sec-Fetch-Site: "+from.site+")") ||
			strings.Contains(shown, "from another site") {
			t.Errorf("the browser shows, after the post from %s:\n%s", from.site, shown)
		}
	}
}
