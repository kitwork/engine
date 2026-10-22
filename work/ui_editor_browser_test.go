package work

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kitwork/engine/utilities/richtext"
)

// editorBrowserCanonical is the editor of the decision of 20/10/2026 as the owner wrote it, word for
// word: a post form, the field's label, a toolbar of seven formats and the link, undo and redo, the
// link's popover with its address and its error, the writing area, the count under it and the words
// that say the post is not saved.
const editorBrowserCanonical = `<form method="post" gap="4">
  <field>
    <label>Nội dung</label>
    <editor as="$body" gap="2">
      <box alt="Định dạng" flex="row" wrap="true" gap="1">
        <button click="toggle('bold')" active:background="brand/15"><icon value="bold" alt="Đậm" /></button>
        <button click="toggle('italic')" active:background="brand/15"><icon value="italic" alt="Nghiêng" /></button>
        <button click="toggle('heading', 2)" active:background="brand/15"><icon value="h-2" alt="Tiêu đề mục" /></button>
        <button click="toggle('heading', 3)" active:background="brand/15"><icon value="h-3" alt="Tiêu đề nhỏ" /></button>
        <button click="toggle('quote')" active:background="brand/15"><icon value="blockquote" alt="Trích dẫn" /></button>
        <button click="toggle('list')" active:background="brand/15"><icon value="list" alt="Danh sách" /></button>
        <button click="toggle('list', 'ordered')" active:background="brand/15"><icon value="list-numbers" alt="Danh sách số" /></button>
        <button click="link()"><icon value="link" alt="Liên kết" /></button>
        <button click="undo()"><icon value="arrow-back-up" alt="Hoàn tác" /></button>
        <button click="redo()"><icon value="arrow-forward-up" alt="Làm lại" /></button>
      </box>
      <popover place="below-left" flex="row" wrap="true" gap="2" padding="2" radius="medium" background="paper">
        <input value="{{ address }}" alt="Địa chỉ liên kết" placeholder="https://… hay /trang" />
        <button click="link(address)">Gắn</button>
        <button click="unlink()">Bỏ liên kết</button>
        <button click="close()">Huỷ</button>
        <error>Chỉ nhận https://, http://, mailto:, tel: hay đường dẫn bắt đầu bằng /.</error>
      </popover>
      <content name="body" value="{{ post.body }}" required="true" placeholder="Viết bài ở đây…" rows="12"
               padding="4" radius="medium" border="line" />
    </editor>
    <hint>{{ $body.words }} từ</hint>
  </field>
  <button type="submit">Lưu bài</button>
  <text show="$body.unsaved">Chưa lưu</text>
</form>`

// editorBrowserSaved is the post the page starts with, as a store might hold it: a b the cleaning makes
// strong and a script it takes out (it would say so in window.__ran).
const editorBrowserSaved = `<p>Xin chào <b>thế giới</b></p><script>window.__ran = 'saved'</script>`

// editorBrowserFiles is a site that moves with Drive (router.reload(false)) whose first page is the
// canonical editor, starting with editorBrowserSaved. Its router.post reads the field from the body
// (request.form) and answers with the page again for what was sent: the editor holding it, the post
// shown through <richtext> (cleaned by richtext() of "kitwork") and what was sent, to the letter, in a
// block of code. Nothing is stored. A link goes to /tiep, which links back.
var editorBrowserFiles = map[string]string{
	"localhost/router.work": `import { router, richtext } from "kitwork";

router.language("vi");
router.reload(false);

router.view((ctx) => {
  const post = { body: "` + editorBrowserSaved + `" };
  const sent = "";
  const kept = richtext(post.body);
});

router.post((ctx) => {
  const sent = ctx.request.form("body");
  return ctx.view({ post: { body: sent }, sent, kept: richtext(sent) });
});
`,
	"localhost/page.ui": `<page><content /></page>`,
	"localhost/content.ui": `<content padding="4" gap="3">
` + editorBrowserCanonical + `
  <box id="shown"><richtext value="{{ kept.value }}" /></box>
  <code language="html">{{ sent }}</code>
  <link id="next" value="/tiep">Tiếp</link>
</content>`,
	"localhost/tiep/content.ui": `<content padding="4" gap="2">
  <text id="here">Tiếp</text>
  <link id="back" value="/">Về</link>
</content>`,
}

// editorBrowserPaste is what Google Docs puts on the clipboard for a paragraph and a list item (its
// wrapper b of font-weight normal with its docs-internal-guid id, bold and italic said in each span's
// style, the list item's paragraph), with what a page must never run beside it: an image whose error
// runs, a script, a javascript: link. editorBrowserPastePlain is the plain text beside it.
const (
	editorBrowserPaste = `<meta charset="utf-8"><b style="font-weight:normal;" id="docs-internal-guid-6e1c2b7f-7fff-1b2a-8a3b-3c1d2e4f5a6b">` +
		`<p dir="ltr" style="line-height:1.38;margin-top:0pt;margin-bottom:0pt;"><span style="font-size:11pt;font-family:Arial,sans-serif;font-weight:700;font-style:normal;white-space:pre-wrap;">Ghi chú</span>` +
		`<span style="font-size:11pt;font-family:Arial,sans-serif;font-weight:400;font-style:normal;white-space:pre-wrap;"> từ Docs, </span>` +
		`<span style="font-size:11pt;font-family:Arial,sans-serif;font-weight:400;font-style:italic;white-space:pre-wrap;">nghiêng</span>` +
		`<img src="/nothing.png" onerror="window.__ran='paste img'"></p>` +
		`<script>window.__ran='paste script'</script>` +
		`<ul style="margin-top:0;margin-bottom:0;padding-inline-start:48px;"><li dir="ltr" style="list-style-type:disc;font-size:11pt;" aria-level="1">` +
		`<p dir="ltr" style="line-height:1.38;" role="presentation"><span style="font-size:11pt;font-weight:400;"><a href="javascript:window.__ran='paste href'">Một mục</a></span></p></li></ul></b>`
	editorBrowserPastePlain = "Ghi chú từ Docs, nghiêng\nMột mục"
)

// editorBrowserProbe is the page's hands and eyes for the proof, installed once in each document (it
// says "ready" when it already is): select(words), the person's selection of the first words that read
// so (a drag of the mouse would make it; every edit after it is a key or a click); paste, the browser's
// paste event with a clipboard of HTML and plain text (DevTools types text only: Input.insertText has
// no clipboard, so the event the browser fires on Control+V is fired with a DataTransfer the page
// fills); and read, what the page shows of the editor, once drawn: the area's HTML as the browser holds
// it, its field, the editor's state, the words under it (the hint, the unsaved words), the formats
// pressed, the commands disabled, the popover, the focus, and what ran.
const editorBrowserProbe = `(function () {
  if (window.__editor) return "ready";
  // the editor is looked for at each call: a Drive move puts another in its place
  var area = function () { return document.querySelector(".editor-area"); };
  var E = window.__editor = {
    // two frames drawn, once each part the editor asked for has loaded (the file a paste or a drop is
    // read by, at the first one; the file that places its tooltips and its popover, at the first focus
    // in it or hover on its toolbar: its tag in the head, then its function under its symbol)
    frames: function () {
      var start = Date.now(), loaded = function () {
        return Array.prototype.every.call(document.querySelectorAll('script[src*="/_kitwork/parts/"]'), function (tag) {
          return window[Symbol.for("kitwork:" + /parts\/([a-z-]+)\./.exec(tag.getAttribute("src"))[1])];
        }) || Date.now() - start > 3000;
      };
      return new Promise(function (done) {
        (function wait() { if (!loaded()) return setTimeout(wait, 10); requestAnimationFrame(function () { requestAnimationFrame(done); }); })();
      });
    },
    find: function (words) {
      var walker = document.createTreeWalker(area(), NodeFilter.SHOW_TEXT);
      while (walker.nextNode()) { var at = walker.currentNode.data.indexOf(words); if (at >= 0) return [walker.currentNode, at]; }
      throw new Error("no " + JSON.stringify(words) + " in " + area().innerHTML);
    },
    select: function (words) {
      var at = E.find(words);
      getSelection().setBaseAndExtent(at[0], at[1], at[0], at[1] + words.length);
      return E.frames().then(function () { return "selected"; });
    },
    paste: function (html, text) {
      var transfer = new DataTransfer();
      transfer.setData("text/html", html);
      transfer.setData("text/plain", text);
      var event = new ClipboardEvent("paste", { clipboardData: transfer, bubbles: true, cancelable: true });
      area().dispatchEvent(event);
      return E.frames().then(function () { return event.defaultPrevented ? "taken" : "left to the browser"; });
    },
    read: function () {
      return E.frames().then(function () {
        var b = kit.scope.$body, popover = document.querySelector(".editor-popover"), input = document.querySelector(".editor-address");
        var error = document.querySelector(".editor-address-error"), active = document.activeElement, writing = area();
        var unsaved = document.querySelector('[data-kitwork-bind="show: $body.unsaved"]');
        var names = function (selector, attribute) {
          return Array.prototype.map.call(document.querySelectorAll(selector), function (button) { return button.getAttribute(attribute); }).join(" ") || "none";
        };
        return JSON.stringify({
          html: writing.innerHTML, field: document.querySelector(".editor-field").value,
          state: ["empty " + b.empty, "words " + b.words, "characters " + b.characters, "unsaved " + b.unsaved, "open " + b.open, "address " + JSON.stringify(b.address)].join(", "),
          shown: document.querySelector(".hint").textContent + " | " + (unsaved.hidden ? "nothing" : unsaved.textContent),
          pressed: names('[data-kitwork-format][aria-pressed="true"]', "data-kitwork-format"),
          disabled: names('.editor-toolbar [aria-disabled="true"]', "data-kitwork-command"),
          popover: popover.hidden ? "closed" : "open, input " + JSON.stringify(input.value) + " invalid " + input.getAttribute("aria-invalid") +
            ", error " + (error.hidden ? "hidden" : "shown") + ", described by " + (input.getAttribute("aria-describedby") === error.id ? "the error" : input.getAttribute("aria-describedby")),
          focus: active === writing ? "area" : active === input ? "address" : active ? active.tagName.toLowerCase() + (active.id ? "#" + active.id : "") : "none",
          ran: window.__ran === undefined ? "nothing" : String(window.__ran)
        });
      });
    }
  };
  return "installed";
})()`

// editorRead is one look at the editor (editorBrowserProbe's read).
type editorRead struct {
	HTML, Field, State, Shown, Pressed, Disabled, Popover, Focus, Ran string
}

// editorLook installs the probe on the page if it is not there and reads the editor.
func editorLook(d *devtools) editorRead {
	d.t.Helper()
	d.evaluate(editorBrowserProbe)
	var got editorRead
	if raw := d.evaluate(`window.__editor.read()`); json.Unmarshal([]byte(raw), &got) != nil {
		d.t.Fatalf("the probe read %s", raw)
	}
	return got
}

// editorKeyCodes are the keys the proof presses that are no letter: the code, the Windows key code and
// the text the key types.
var editorKeyCodes = map[string]struct {
	code string
	key  int
	text string
}{
	"Enter": {"Enter", 13, "\r"}, "Backspace": {"Backspace", 8, ""}, "Escape": {"Escape", 27, ""},
	"End": {"End", 35, ""}, "Home": {"Home", 36, ""}, "ArrowLeft": {"ArrowLeft", 37, ""}, "ArrowRight": {"ArrowRight", 39, ""},
}

// editorPress presses one key on the keyboard and lets it go, as the browser hears a person's: chord
// is the key after its modifiers, "Control+Shift+Z", "Enter", "Shift+Enter", "a".
func editorPress(d *devtools, chord string) {
	d.t.Helper()
	parts := strings.Split(chord, "+")
	key, modifiers := parts[len(parts)-1], 0
	for _, m := range parts[:len(parts)-1] {
		modifiers |= map[string]int{"Alt": 1, "Control": 2, "Meta": 4, "Shift": 8}[m]
	}
	down := map[string]any{"type": "rawKeyDown", "modifiers": modifiers}
	if k, ok := editorKeyCodes[key]; ok {
		down["key"], down["code"], down["windowsVirtualKeyCode"] = key, k.code, k.key
		if k.text != "" {
			down["type"], down["text"] = "keyDown", k.text
		}
	} else {
		letter := strings.ToUpper(key)
		down["key"], down["code"], down["windowsVirtualKeyCode"] = strings.ToLower(key), "Key"+letter, int(letter[0])
		if modifiers&8 != 0 {
			down["key"] = letter
		}
		if modifiers&^8 == 0 {
			down["type"], down["text"] = "keyDown", down["key"]
		}
	}
	d.call("Input.dispatchKeyEvent", down)
	d.call("Input.dispatchKeyEvent", map[string]any{"type": "keyUp", "modifiers": modifiers, "key": down["key"], "code": down["code"], "windowsVirtualKeyCode": down["windowsVirtualKeyCode"]})
}

// editorType types words as the keyboard's text input does (an input method's commit, a phone's
// keyboard): beforeinput insertText, the browser's own insertion, input.
func editorType(d *devtools, words string) {
	d.t.Helper()
	d.call("Input.insertText", map[string]any{"text": words})
}

// editorCenter is the middle of the first element selector finds, on the screen.
func editorCenter(d *devtools, selector string) (float64, float64) {
	d.t.Helper()
	var at struct{ X, Y float64 }
	raw := d.evaluate(`(function () { var e = document.querySelector(` + jsString(selector) + `); if (!e) return "none"; e.scrollIntoView({ block: "center" }); var r = e.getBoundingClientRect(); return JSON.stringify({ x: r.left + r.width / 2, y: r.top + r.height / 2 }); })()`)
	if err := json.Unmarshal([]byte(raw), &at); err != nil {
		d.t.Fatalf("nothing on the page is %s (%s)", selector, raw)
	}
	return at.X, at.Y
}

// editorClick clicks the middle of the first element selector finds with the mouse, as a person does.
func editorClick(d *devtools, selector string) {
	d.t.Helper()
	x, y := editorCenter(d, selector)
	for _, kind := range []string{"mouseMoved", "mousePressed", "mouseReleased"} {
		d.call("Input.dispatchMouseEvent", map[string]any{"type": kind, "x": x, "y": y, "button": "left", "clickCount": 1})
	}
}

// editorClickAway is editorClick on a link whose page may ask before it goes (beforeunload): the
// browser answers the release only once the page has gone or stayed, so it is sent without waiting for
// its answer.
func editorClickAway(d *devtools, selector string) {
	d.t.Helper()
	x, y := editorCenter(d, selector)
	for _, kind := range []string{"mouseMoved", "mousePressed"} {
		d.call("Input.dispatchMouseEvent", map[string]any{"type": kind, "x": x, "y": y, "button": "left", "clickCount": 1})
	}
	d.next++
	message, _ := json.Marshal(map[string]any{"id": d.next, "method": "Input.dispatchMouseEvent",
		"params": map[string]any{"type": "mouseReleased", "x": x, "y": y, "button": "left", "clickCount": 1}})
	d.write(message)
}

// editorDialog waits up to two seconds for the page to ask before it goes and returns the kind of the
// question the browser shows ("beforeunload"), or "no question". It asks the browser itself for the
// page's history (no script of the page runs for it, so it is answered while a question holds the
// page) and reads the page's events that come before each answer.
func editorDialog(d *devtools) string {
	d.t.Helper()
	for tries := 0; tries < 40; tries++ {
		d.next++
		message, _ := json.Marshal(map[string]any{"id": d.next, "method": "Page.getNavigationHistory", "params": map[string]any{}})
		d.write(message)
		_ = d.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		for {
			var answer struct {
				ID     int    `json:"id"`
				Method string `json:"method"`
				Params struct {
					Type string `json:"type"`
				} `json:"params"`
			}
			if err := json.Unmarshal(d.read(), &answer); err != nil {
				d.t.Fatal(err)
			}
			if answer.Method == "Page.javascriptDialogOpening" {
				return answer.Params.Type
			}
			if answer.ID == d.next {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return "no question"
}

// editorSite serves a tenant to a browser as host, one request at a time, and keeps what the
// browser asked for: each request's method, address, X-Kitwork-Hydrate, Sec-Fetch-Site, Content-Type
// and status.
type editorSite struct {
	*httptest.Server
	mu   sync.Mutex
	hits []richtextHit
}

func newEditorSite(tenant *Tenant, host string) *editorSite {
	site := &editorSite{}
	site.Server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		forward := request.Clone(request.Context())
		forward.Host = host
		written := &richtextStatus{ResponseWriter: response}
		site.mu.Lock()
		defer site.mu.Unlock()
		tenant.Serve(written, forward)
		site.hits = append(site.hits, richtextHit{method: request.Method, path: request.URL.RequestURI(), hydrate: request.Header.Get("X-Kitwork-Hydrate"),
			site: request.Header.Get("Sec-Fetch-Site"), kind: request.Header.Get("Content-Type"), status: written.status})
	}))
	return site
}

// heard is how many requests the site has been sent.
func (site *editorSite) heard() int {
	site.mu.Lock()
	defer site.mu.Unlock()
	return len(site.hits)
}

// pages is what the browser asked the site for itself from the n-th request on, the engine's own files
// (/_kitwork/) and the site's (an address with an extension: a logo, the favicon) left out: the pages
// it loaded whole, the forms it sent.
func (site *editorSite) pages(from int) string {
	site.mu.Lock()
	defer site.mu.Unlock()
	var out []string
	for _, h := range site.hits[min(from, len(site.hits)):] {
		if strings.HasPrefix(h.path, "/_kitwork/") || path.Ext(strings.SplitN(h.path, "?", 2)[0]) != "" || h.hydrate != "" {
			continue
		}
		line := fmt.Sprintf("%s %s %d", h.method, h.path, h.status)
		if h.method == http.MethodPost {
			line += " site=" + h.site + " " + h.kind
		}
		out = append(out, line)
	}
	return strings.Join(out, ", ")
}

// drive is what Drive fetched from the n-th request on (with X-Kitwork-Hydrate): its moves, and the
// pages it fetches ahead while the mouse rests on a link.
func (site *editorSite) drive(from int) string {
	site.mu.Lock()
	defer site.mu.Unlock()
	var out []string
	for _, h := range site.hits[min(from, len(site.hits)):] {
		if h.hydrate != "" {
			out = append(out, fmt.Sprintf("%s %s %d", h.method, h.path, h.status))
		}
	}
	return strings.Join(out, ", ")
}

// fetched reports whether the browser ever asked for address.
func (site *editorSite) fetched(address string) bool {
	site.mu.Lock()
	defer site.mu.Unlock()
	for _, h := range site.hits {
		if h.path == address {
			return true
		}
	}
	return false
}

// waitFor waits up to ten seconds for test to hold.
func (site *editorSite) waitFor(t *testing.T, what string, test func() bool) {
	t.Helper()
	for tries := 0; !test(); tries++ {
		if tries == 200 {
			t.Fatalf("waited 10s for %s: pages %s, Drive %s", what, site.pages(0), site.drive(0))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// editorStep reads the editor once what was done is drawn, and holds it to the identity: the area's
// HTML cleaned by Go is the field the form posts, which is its own Clean.
func editorStep(t *testing.T, d *devtools, name string) editorRead {
	t.Helper()
	got := editorLook(d)
	if cleaned := richtext.Clean(got.HTML).HTML; cleaned != got.Field {
		t.Errorf("%s: what the area holds, cleaned, is not what the form posts:\n area  %s\n clean %q\n field %q", name, got.HTML, cleaned, got.Field)
	}
	if again := richtext.Clean(got.Field).HTML; again != got.Field {
		t.Errorf("%s: the field is not its own Clean:\n field %q\n clean %q", name, got.Field, again)
	}
	return got
}

// editorSays is a look in one line: the field, the state, the words under the editor, the formats
// pressed, the commands disabled, the popover, the focus, what ran.
func editorSays(r editorRead) string {
	return strings.Join([]string{r.Field, r.State, r.Shown, "pressed " + r.Pressed, "disabled " + r.Disabled, r.Popover, "focus " + r.Focus, "ran " + r.Ran}, " | ")
}

// editorBrowserHeard is put in every document before its own scripts run: every complaint of the
// page's scripts (a console warning or error, an error thrown) in window.__complaints.
const editorBrowserHeard = `(function () {
  window.__complaints = [];
  ["warn", "error"].forEach(function (level) {
    var write = console[level];
    console[level] = function () { window.__complaints.push(Array.prototype.join.call(arguments, " ")); return write.apply(console, arguments); };
  });
  window.addEventListener("error", function (event) { window.__complaints.push(String(event.message)); });
})()`

// editorBrowserWritten is the post the proof writes, as the field holds it and the form posts it: the
// saved post with words typed after its bold, three words bolded and put in italic, a heading, a list
// of two items, a paragraph whose first word an input method composed and whose last words link to a
// bare host made https://, and the paste of Google Docs, its bold and italic kept, its list item a
// list, its javascript: link only words.
const editorBrowserWritten = "<p>Xin chào <strong>thế giới</strong>! Đây là <strong>bài</strong> <strong>viết</strong> <em>đầu tiên</em>.</p>\n" +
	"<h2>Thực đơn</h2>\n<ul>\n<li>Cà phê sữa</li>\n<li>Trà đào</li>\n</ul>\n" +
	"<p>Tiếng Việt ở <a href=\"https://kitwork.io/docs\">trang tài liệu</a></p>\n" +
	"<p><strong>Ghi chú</strong> từ Docs, <em>nghiêng</em></p>\n<ul>\n<li>Một mục</li>\n</ul>"

// editorBrowserPasted is what the paste added to the post, at its end.
const editorBrowserPasted = "\n<p><strong>Ghi chú</strong> từ Docs, <em>nghiêng</em></p>\n<ul>\n<li>Một mục</li>\n</ul>"

// TestBrowserEditorWritesAPost proves <editor> end to end on pages the engine serves, in headless
// Chrome or Edge with the kernel and Drive on the page, a person's hands played through DevTools: the
// mouse (Input.dispatchMouseEvent), the keys (Input.dispatchKeyEvent), the keyboard's text
// (Input.insertText) and an input method composing a Vietnamese word (Input.imeSetComposition), the
// selections set by script as a drag sets them. Each step reads the editor whole, and in every state
// it reaches what the browser holds in the area, cleaned by Go's richtext.Clean, is exactly the field
// the form posts, which is its own Clean (the identity of the decision); the page's scripts complain
// of nothing.
//   - with JavaScript: the page, reached by Drive (to /tiep and back, each fetched with
//     X-Kitwork-Hydrate: nothing holds Drive yet), shows the saved post cleaned (b as strong, its script
//     gone) and the component turns the area on, the field out of the way but still drawn; a click in
//     it and Control+End put the caret at the end, Control+B with nothing selected leaves the bold for
//     what is typed next, the count under the editor follows the words and the unsaved words show; a
//     word bolded with the toolbar's button (pressed while the words are bold, its active: background
//     drawn by aria-pressed), another with Control+B, two put in italic with Control+I; Enter, a
//     heading made with its button, Enter after it a paragraph, a list made with its button, Enter a
//     new item, Enter on the empty item out of the list, a word an input method composed; words linked
//     through the popover: its button opens it (open, aria-expanded, the address focused), a
//     javascript: address is refused with Enter (the popover stays, the address aria-invalid, the
//     error shown and describing it, nothing linked), a bare host linked with the popover's button as
//     https://, the popover closed and the focus back in the area; a paste of Google Docs on a line of
//     its own, taken by the editor (its bold and italic from the styles, its wrapper no bold, its list
//     item a list, its image, script and javascript: link gone, the image never fetched); Control+Z
//     takes the paste back in one step, Control+Y brings it again, the toolbar's undo and redo the
//     same, redo disabled at the end; a link clicked while unsaved is the browser's own move, which
//     asks first (beforeunload), and staying keeps every word, no page loaded; Save asks nothing: the
//     browser posts the form itself, urlencoded, same-origin, without X-Kitwork-Hydrate, and the answer,
//     a new document, shows what was sent, exactly editorBrowserWritten, the field as it was: through
//     <richtext> exactly as richtext.Display writes it, the editor holding it again; nothing that was
//     pasted or saved ran anywhere; the answer to a post is unsaved (the decision: a page that answers
//     a post may be a refused save shown again), so a link clicked there asks too, and leaving loads
//     the next page whole (no X-Kitwork-Hydrate);
//   - without JavaScript (Emulation.setScriptExecutionDisabled, the page loaded again): scripting: none
//     draws the field, holding the saved post's canonical HTML, and neither the toolbar, the area nor
//     the popover; what is typed into the field is posted as it is, and the answer shows it cleaned,
//     its field holding the cleaned post.
//
// CONTROL, each against a copy served through -overlay (the tree untouched), the proof failing where
// named (run 20/10/2026): the editor (jit/js/components/editor/v2.0.0.js), while Control+B with nothing
// selected still left a marker in the area (U+200B, until the review of 20/10/2026: the mark now waits
// in no element), reading that marker kept broke the identity at the first words typed after it
// ("typed": the area cleaned is not the field, the field not its own Clean); its field written
// only at the start (sync() and the formdata listener leaving it) held the saved post from the first
// words on ("typed after the bold, Control+B first"); the hold asking nothing on unload, and Drive's
// click listener taking a link while held (jit/hydrate/drive.js), each let the link go unasked ("a
// link clicked while unsaved": no question); the hold kept on the form's submit asked on Save ("Save
// clicked": beforeunload), and no post was sent; a paste parsed into a live element of the page instead
// of an inert document ran its image's error ("pasted": ran paste img) and fetched the image; the page
// rendered without the request's method (work/router_ui.go, no Method) painted the answer saved ("the
// answer's editor": unsaved false) and let its link go unasked; without the scripting: none rule
// (ui/stylesheet/editor.go) the page without JavaScript drew the area and the toolbar and hid the field
// ("what is drawn"). Not a control: with the toolbar's pointerdown and mousedown left to the browser
// the proof still passes in Chrome (the button takes the focus, the selection stays in the area and
// the command gives the area its focus back), so this proof says nothing of those two listeners.
func TestBrowserEditorWritesAPost(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the editor browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant := buildUISite(t, editorBrowserFiles)
	first := serveUI(t, tenant, "http://localhost/")
	served := first.Body.String()
	if first.Code != http.StatusOK || warningsScriptRe.MatchString(served) {
		t.Fatalf("/ = %d, or it links warnings:\n%.3000s", first.Code, served)
	}
	saved := richtext.Clean(editorBrowserSaved).HTML
	if saved != "<p>Xin chào <strong>thế giới</strong></p>" {
		t.Fatalf("the saved post cleans to %q", saved)
	}
	for _, want := range []string{
		`data-kitwork-component="editor: $body" data-kitwork-scope="empty: false; words: 4; characters: 17; unsaved: false; open: false; address: &#39;&#39;; format: &#39;rich&#39;; dropped: []"`,
		`aria-readonly="true" translate="no" spellcheck="true" data-kitwork-name="body">` + saved + `</div>`,
		`<textarea class="editor-field padding-4 radius-medium border-line" id="kitwork-field-1" name="body" required rows="12"`,
		`/_kitwork/components/editor.`, `/_kitwork/drive.`,
	} {
		if !strings.Contains(served, want) {
			t.Errorf("the page as the server writes it has no %s", want)
		}
	}
	if strings.Contains(served, "__ran") || strings.Contains(served, "contenteditable") {
		t.Errorf("the first paint carries the saved script or an editable area:\n%.3000s", served)
	}

	site := newEditorSite(tenant, "localhost")
	defer site.Close()
	server, heard, pages, drive, fetched := site.Server, site.heard, site.pages, site.drive, site.fetched
	check := func(t *testing.T, what, got, want string) {
		t.Helper()
		if got != want {
			t.Errorf("%s:\n got  %s\n want %s", what, got, want)
		}
	}
	waitFor := site.waitFor
	step, says := editorStep, editorSays
	const state = `empty false, words %d, characters %d, unsaved %t, open %t, address %q`

	t.Run("with JavaScript", func(t *testing.T) {
		d := openDevtools(t, browser, server.URL+"/", 1000, 1400, "")
		d.loaded(exampleSettle)
		d.call("Page.enable", map[string]any{})
		d.call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": editorBrowserHeard})
		networkReload(d)

		// to /tiep and back by Drive, its links clicked by script (no mouse resting on them): nothing
		// holds it yet
		from := heard()
		d.evaluate(`document.getElementById("next").click()`)
		waitFor(t, "Drive's move to /tiep", func() bool { return d.evaluate(`String(!!document.getElementById("here"))`) == "true" })
		d.evaluate(`document.getElementById("back").click()`)
		waitFor(t, "Drive's move back", func() bool { return d.evaluate(`String(!!document.querySelector(".editor-area"))`) == "true" })
		check(t, "the moves to /tiep and back", "loaded "+pages(from)+"; Drive "+drive(from), "loaded ; Drive GET /tiep 200, GET / 200")
		d.evaluate(`window.__before = true`)

		start := step(t, d, "as it starts")
		check(t, "as it starts", says(start), saved+" | "+fmt.Sprintf(state, 4, 17, false, false, "")+" | 4 từ | nothing | pressed none | disabled link undo redo | closed | focus main | ran nothing")
		check(t, "the area turned on, the field out of the way", d.evaluate(`(function () { var a = document.querySelector(".editor-area"), f = document.querySelector(".editor-field");
  var s = getComputedStyle(f);
  return [a.contentEditable, a.hasAttribute("aria-readonly"), f.tabIndex, f.getAttribute("aria-hidden"), s.display, s.position, s.clipPath].join(" "); })()`),
			"true false -1 true block absolute inset(50%)")

		// the caret at the end, the bold left for what is typed next
		editorClick(d, ".editor-area p")
		editorPress(d, "Control+End")
		editorPress(d, "Control+B")
		editorType(d, "! Đây là bài viết đầu tiên.")
		typed := "<p>Xin chào <strong>thế giới</strong>! Đây là bài viết đầu tiên.</p>"
		check(t, "typed after the bold, Control+B first", says(step(t, d, "typed")),
			typed+" | "+fmt.Sprintf(state, 10, 44, true, false, "")+" | 10 từ | Chưa lưu | pressed none | disabled link redo | closed | focus area | ran nothing")

		// bài with the toolbar's button, viết with Control+B, đầu tiên in italic with Control+I
		d.evaluate(`window.__editor.select("bài")`)
		editorClick(d, `[data-kitwork-format="bold"]`)
		bolded := strings.Replace(typed, "bài", "<strong>bài</strong>", 1)
		check(t, "bài bolded with the button", says(step(t, d, "bài")),
			bolded+" | "+fmt.Sprintf(state, 10, 44, true, false, "")+" | 10 từ | Chưa lưu | pressed bold | disabled redo | closed | focus area | ran nothing")
		check(t, "the pressed button's active: background", d.evaluate(`(function () { var b = getComputedStyle(document.querySelector('[data-kitwork-format="bold"]')).backgroundColor,
  i = getComputedStyle(document.querySelector('[data-kitwork-format="italic"]')).backgroundColor; return b !== i && b !== "rgba(0, 0, 0, 0)" ? "drawn" : "bold " + b + ", italic " + i; })()`), "drawn")
		d.evaluate(`window.__editor.select("viết")`)
		editorPress(d, "Control+B")
		d.evaluate(`window.__editor.select("đầu tiên")`)
		editorPress(d, "Control+I")
		marked := strings.Replace(strings.Replace(bolded, "viết", "<strong>viết</strong>", 1), "đầu tiên", "<em>đầu tiên</em>", 1)
		check(t, "viết with Control+B, đầu tiên with Control+I", says(step(t, d, "marks")),
			marked+" | "+fmt.Sprintf(state, 10, 44, true, false, "")+" | 10 từ | Chưa lưu | pressed italic | disabled redo | closed | focus area | ran nothing")

		// a heading, a list of two items, out of it, a word an input method composed
		editorPress(d, "Control+End")
		editorPress(d, "Enter")
		editorType(d, "Thực đơn")
		editorClick(d, `[data-kitwork-format="heading-2"]`)
		editorPress(d, "Enter")
		editorType(d, "Cà phê sữa")
		editorClick(d, `[data-kitwork-format="list"]`)
		editorPress(d, "Enter")
		editorType(d, "Trà đào")
		editorPress(d, "Enter")
		editorPress(d, "Enter")
		d.call("Input.imeSetComposition", map[string]any{"text": "tiê", "selectionStart": 3, "selectionEnd": 3})
		d.call("Input.imeSetComposition", map[string]any{"text": "tiế", "selectionStart": 3, "selectionEnd": 3})
		editorType(d, "Tiếng")
		editorType(d, " Việt ở trang tài liệu")
		blocks := marked + "\n<h2>Thực đơn</h2>\n<ul>\n<li>Cà phê sữa</li>\n<li>Trà đào</li>\n</ul>\n<p>Tiếng Việt ở trang tài liệu</p>"
		check(t, "a heading, a list, a paragraph after it", says(step(t, d, "blocks")),
			blocks+" | "+fmt.Sprintf(state, 23, 96, true, false, "")+" | 23 từ | Chưa lưu | pressed none | disabled link redo | closed | focus area | ran nothing")

		// the link: the popover, a javascript: address refused, a bare host linked as https://
		expanded := func() string {
			return d.evaluate(`document.querySelector('.editor-toolbar [data-kitwork-command="link"]').getAttribute("aria-expanded")`)
		}
		d.evaluate(`window.__editor.select("trang tài liệu")`)
		editorClick(d, `.editor-toolbar [data-kitwork-command="link"]`)
		check(t, "the popover opened", says(step(t, d, "open"))+" | expanded "+expanded(),
			blocks+" | "+fmt.Sprintf(state, 23, 96, true, true, "")+` | 23 từ | Chưa lưu | pressed none | disabled redo | open, input "" invalid null, error hidden, described by null | focus address | ran nothing | expanded true`)
		editorType(d, "javascript:alert(1)")
		editorPress(d, "Enter")
		check(t, "a javascript: address, Enter", says(step(t, d, "refused")),
			blocks+" | "+fmt.Sprintf(state, 23, 96, true, true, "javascript:alert(1)")+` | 23 từ | Chưa lưu | pressed none | disabled redo | open, input "javascript:alert(1)" invalid true, error shown, described by the error | focus address | ran nothing`)
		editorPress(d, "Control+A")
		editorType(d, "kitwork.io/docs")
		editorClick(d, `.editor-popover [data-kitwork-command="link"]`)
		linked := strings.Replace(blocks, "trang tài liệu", `<a href="https://kitwork.io/docs">trang tài liệu</a>`, 1)
		check(t, "a bare host, the popover's button", says(step(t, d, "linked"))+" | expanded "+expanded(),
			linked+" | "+fmt.Sprintf(state, 23, 96, true, false, "kitwork.io/docs")+" | 23 từ | Chưa lưu | pressed none | disabled redo | closed | focus area | ran nothing | expanded false")

		// a paste of Google Docs on a line of its own, then back and again
		editorPress(d, "Control+End")
		editorPress(d, "Enter")
		check(t, "the paste", d.evaluate(`window.__editor.paste(`+jsString(editorBrowserPaste)+`, `+jsString(editorBrowserPastePlain)+`)`), "taken")
		pasted := linked + editorBrowserPasted
		after := " | 30 từ | Chưa lưu | pressed list | disabled link redo | closed | focus area | ran nothing"
		check(t, "pasted", says(step(t, d, "pasted")), pasted+" | "+fmt.Sprintf(state, 30, 127, true, false, "kitwork.io/docs")+after)
		before := " | " + fmt.Sprintf(state, 23, 96, true, false, "kitwork.io/docs") + " | 23 từ | Chưa lưu | pressed none | disabled link | closed | focus area | ran nothing"
		editorPress(d, "Control+Z")
		check(t, "Control+Z", says(step(t, d, "undo")), linked+before)
		editorPress(d, "Control+Y")
		check(t, "Control+Y", says(step(t, d, "redo")), pasted+" | "+fmt.Sprintf(state, 30, 127, true, false, "kitwork.io/docs")+after)
		editorClick(d, `[data-kitwork-command="undo"]`)
		check(t, "the undo button", says(step(t, d, "undo button")), linked+before)
		editorClick(d, `[data-kitwork-command="redo"]`)
		written := step(t, d, "redo button")
		check(t, "the redo button", says(written), editorBrowserWritten+" | "+fmt.Sprintf(state, 30, 127, true, false, "kitwork.io/docs")+after)
		if fetched("/nothing.png") {
			t.Error("the pasted image was fetched")
		}

		// a link clicked while unsaved: the browser's own move, which asks; staying keeps every word
		from = heard()
		editorClickAway(d, "#next")
		check(t, "a link clicked while unsaved", editorDialog(d), "beforeunload")
		d.call("Page.handleJavaScriptDialog", map[string]any{"accept": false})
		check(t, "staying", says(step(t, d, "stayed"))+" | "+d.evaluate(`location.pathname + " " + window.__before`)+" | loaded "+pages(from),
			strings.Replace(says(written), "focus area", "focus a#next", 1)+" | / true | loaded ")

		// Save: nothing asked, the browser posts the form, the answer a new document
		from = heard()
		editorClickAway(d, `button[type="submit"]`)
		check(t, "Save clicked", editorDialog(d), "no question")
		waitFor(t, "the post", func() bool { return strings.Contains(pages(from), "POST") })
		d.loaded(`(window.__before ? "wait of 0" : ` + exampleSettle + `)`)
		check(t, "what the browser sent", pages(from), "POST / 200 site=same-origin application/x-www-form-urlencoded")
		sent := d.evaluate(`document.querySelector("pre code").textContent`)
		check(t, "what was sent", sent, editorBrowserWritten)
		check(t, "the post shown", d.evaluate(`document.querySelector("#shown .richtext").innerHTML`), richtext.Display(sent, "http://localhost"))
		answer := step(t, d, "the answer")
		check(t, "the answer's editor", says(answer), editorBrowserWritten+" | "+fmt.Sprintf(state, 30, 127, true, false, "")+" | 30 từ | Chưa lưu | pressed none | disabled link undo redo | closed | focus body | ran nothing")
		check(t, "the answer's area", answer.HTML, strings.ReplaceAll(editorBrowserWritten, "\n", ""))
		check(t, "complaints of the answer", d.evaluate(`JSON.stringify(window.__complaints)`), "[]")
		if fetched("/nothing.png") {
			t.Error("the pasted image was fetched")
		}

		// the answer to a post holds too: a link asks, and leaving loads the next page whole
		from = heard()
		editorClickAway(d, "#next")
		check(t, "a link clicked on the answer", editorDialog(d), "beforeunload")
		d.call("Page.handleJavaScriptDialog", map[string]any{"accept": true})
		waitFor(t, "/tiep", func() bool { return strings.Contains(pages(from), "/tiep") })
		d.loaded(exampleSettle)
		check(t, "leaving the answer", pages(from)+" | "+d.evaluate(`location.pathname + " " + !!document.getElementById("here")`), "GET /tiep 200 | /tiep true")
		check(t, "complaints of /tiep", d.evaluate(`JSON.stringify(window.__complaints)`), "[]")
	})

	t.Run("without JavaScript", func(t *testing.T) {
		d := openDevtools(t, browser, server.URL+"/", 1000, 1400, "")
		d.loaded(exampleSettle)
		d.call("Emulation.setScriptExecutionDisabled", map[string]any{"value": true})
		d.evaluate(`window.__stale = true`)
		d.call("Page.reload", map[string]any{"ignoreCache": true})
		// no script of the page runs, nor a callback of the proof's: the page is read once it has loaded
		loaded := func() {
			waitFor(t, "the page loaded", func() bool { return d.evaluate(`document.readyState + " " + !!window.__stale`) == "complete false" })
		}
		loaded()
		const drawn = `(function () {
  var show = function (selector) { var e = document.querySelector(selector), s = getComputedStyle(e); return s.display === "none" ? "none" : s.position + " " + (e.offsetWidth > 300 && e.offsetHeight > 100 ? "large" : e.offsetWidth + "x" + e.offsetHeight); };
  return [matchMedia("(scripting: none)").matches ? "scripting none" : "scripting", "field " + show(".editor-field"), "area " + show(".editor-area"),
    "toolbar " + show(".editor-toolbar"), "popover " + show(".editor-popover"), "contenteditable " + document.querySelector(".editor-area").getAttribute("contenteditable")].join(", ");
})()`
		check(t, "what is drawn", d.evaluate(drawn), "scripting none, field static large, area none, toolbar none, popover none, contenteditable null")
		check(t, "the field holds", d.evaluate(`document.querySelector(".editor-field").value`), saved)

		const more = "\n<p>Viết khi <i>không</i> có JavaScript</p><script>window.__ran = 'typed'</script>"
		editorClick(d, ".editor-field")
		editorPress(d, "Control+End")
		editorType(d, more)
		from := heard()
		d.evaluate(`window.__stale = true`)
		editorClick(d, `button[type="submit"]`)
		waitFor(t, "the post", func() bool { return strings.Contains(pages(from), "POST") })
		loaded()
		check(t, "what the browser sent", pages(from)+" | Drive "+drive(from), "POST / 200 site=same-origin application/x-www-form-urlencoded | Drive ")
		sent := d.evaluate(`document.querySelector("pre code").textContent`)
		check(t, "what was sent", sent, saved+more)
		check(t, "the post shown", d.evaluate(`document.querySelector("#shown .richtext").innerHTML`), richtext.Display(sent, "http://localhost"))
		check(t, "the answer's field", d.evaluate(`document.querySelector(".editor-field").value`), richtext.Clean(sent).HTML)
		check(t, "the answer is drawn", d.evaluate(drawn), "scripting none, field static large, area none, toolbar none, popover none, contenteditable null")
	})
}

// editorBrowserDropped is what a drop from another page may carry: words in the grammar, and what must
// never reach the area, its form or the network: form controls (a submit with a formaction elsewhere
// among them), a frame, a video, images by src, srcset and SVG, a style.
const editorBrowserDropped = `<p>thả <b>đậm</b></p>` +
	`<input name="injected" value="from-drop"><button formaction="/probe-steal" formmethod="post" name="via" value="b">Lưu bài</button>` +
	`<textarea name="other">ta</textarea><select name="sel"><option value="s1" selected>s1</option></select>` +
	`<iframe src="/probe-iframe"></iframe><video src="/probe-video.mp4" autoplay></video><img src="/probe-image.png">` +
	`<img srcset="/probe-srcset.png 1x"><svg><image href="/probe-svg.png"/></svg><div style="background-image:url(/probe-background.png)">nền</div>`

// editorDrop drags markup from outside the page and drops it at the middle of the first element
// selector finds, as the mouse does (Input.dispatchDragEvent: enter, over, drop).
func editorDrop(d *devtools, selector, markup string) {
	d.t.Helper()
	x, y := editorCenter(d, selector)
	data := map[string]any{"items": []map[string]any{{"mimeType": "text/html", "data": markup}, {"mimeType": "text/plain", "data": "thả đậm nền"}}, "dragOperationsMask": 1}
	for _, kind := range []string{"dragEnter", "dragOver", "drop"} {
		d.call("Input.dispatchDragEvent", map[string]any{"type": kind, "x": x, "y": y, "data": data, "modifiers": 0})
	}
}

// editorControls is what the page's post form holds as controls, its elements, one word each.
const editorControls = `Array.prototype.map.call(document.querySelector(".editor-area").closest("form").elements, function (e) { return e.tagName.toLowerCase() + (e.name ? "[" + e.name + "]" : ""); }).join(" ")`

// A drop from elsewhere is the editor's from its very start: the browser never builds what it carries
// in the page, so nothing it names is fetched (an image, a srcset, an SVG image, a video, a frame, a
// background), and it is read onto the grammar like a paste, at the place it lands, as one step. While
// an input method composes in the area (Telex, a phone's keyboard) the drop waits for the composition's
// end: nothing of it reaches the area meanwhile, so the post form never takes its controls (a submit
// whose formaction sends the post elsewhere among them).
// CONTROL, against copies of v2.0.0.js through -overlay (run 20/10/2026): the file as it was before
// the review of 20/10/2026 failed both runs on "nothing fetched" (the browser built the drop in the
// page and fetched its image, srcset, SVG image and video, composing its frame and background too) and
// the composing run on "the form's controls while composing" and "the drop reached the area" (the
// dropped input, button, textarea and select were controls of the post form); with the drop listener
// alone left out, both runs failed on "nothing fetched" alone (beforeinput insertFromDrop still kept
// the composing run's controls out); with the drop listener left out and beforeinput and paste back to
// returning during a composition, the composing run failed on the form's controls again.
func TestBrowserEditorDropIsTheEditors(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the editor browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant := buildUISite(t, editorBrowserFiles)
	site := newEditorSite(tenant, "localhost")
	defer site.Close()
	probes := func(from int) string {
		site.mu.Lock()
		defer site.mu.Unlock()
		var out []string
		for _, h := range site.hits[min(from, len(site.hits)):] {
			if strings.Contains(h.path, "probe") {
				out = append(out, h.method+" "+h.path)
			}
		}
		return strings.Join(out, ", ")
	}
	for _, composing := range []bool{false, true} {
		t.Run(map[bool]string{false: "a drop", true: "a drop while composing"}[composing], func(t *testing.T) {
			d := openDevtools(t, browser, site.URL+"/", 1000, 1400, "")
			d.loaded(exampleSettle)
			d.call("Page.enable", map[string]any{})
			d.call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": editorBrowserHeard})
			networkReload(d)
			controls := d.evaluate(editorControls)
			from := site.heard()
			editorClick(d, ".editor-area p")
			editorPress(d, "Control+End")
			editorPress(d, "Enter")
			if composing {
				d.call("Input.imeSetComposition", map[string]any{"text": "vie", "selectionStart": 3, "selectionEnd": 3})
			}
			editorDrop(d, ".editor-area p:last-child", editorBrowserDropped)
			if composing {
				during := d.evaluate(`new Promise(function (done) { setTimeout(function () { done(document.querySelector(".editor-area").innerHTML); }, 300); })`)
				if got := d.evaluate(editorControls); got != controls {
					t.Errorf("the form's controls while composing:\n got  %s\n want %s\n area %s", got, controls, during)
				}
				if strings.Contains(during, "thả") {
					t.Errorf("the drop reached the area during the composition: %s", during)
				}
				d.call("Input.insertText", map[string]any{"text": "việt"})
			}
			got := editorStep(t, d, "dropped")
			if strings.Count(got.Field, "thả") != 1 || !strings.Contains(got.Field, "thả <strong>đậm</strong>") || strings.Contains(got.Field, "Lưu") {
				t.Errorf("the drop read onto the grammar, once: %s", got.Field)
			}
			if !composing && !strings.HasSuffix(got.Field, "\n<p>thả <strong>đậm</strong></p>\n<p>nền</p>") {
				t.Errorf("the drop at the line it landed on: %s", got.Field)
			}
			if composing && !strings.Contains(got.Field, "việt") {
				t.Errorf("the composed word kept: %s", got.Field)
			}
			if after := d.evaluate(editorControls); after != controls {
				t.Errorf("the form's controls after the drop:\n got  %s\n want %s", after, controls)
			}
			time.Sleep(300 * time.Millisecond)
			if fetched := probes(from); fetched != "" {
				t.Errorf("nothing fetched of what the drop names; got %s", fetched)
			}
			if complaints := d.evaluate(`JSON.stringify(window.__complaints)`); complaints != "[]" {
				t.Errorf("the page complained: %s", complaints)
			}
			// one step: undone whole
			editorPress(d, "Control+Z")
			if undone := editorLook(d); strings.Contains(undone.Field, "thả") {
				t.Errorf("Control+Z takes the drop back in one step: %s", undone.Field)
			}
		})
	}
}

// editorReviewFiles is a site that moves with Drive whose page holds the canonical editor with a post
// that links to /tiep, and a page /small whose writing area is written size="small".
func editorReviewFiles() map[string]string {
	post := func(body string) string {
		return `import { router } from "kitwork";

router.language("vi");
router.reload(false);

router.view((ctx) => {
  const post = { body: "` + body + `" };
});
`
	}
	page := func(form string) string { return "<content padding=\"4\" gap=\"3\">\n" + form + "\n</content>" }
	return map[string]string{
		"localhost/router.work":       post(`<p>Đọc thêm ở <a href=\"/tiep\">trang tài liệu</a> nhé.</p><p>Một hai ba</p>`),
		"localhost/page.ui":           `<page><content /></page>`,
		"localhost/content.ui":        page(editorBrowserCanonical),
		"localhost/small/router.work": post(`<p>Một hai ba</p>`),
		"localhost/small/content.ui":  page(strings.Replace(editorBrowserCanonical, `rows="12"`, `rows="12" size="small"`, 1)),
		"localhost/tiep/content.ui":   `<content padding="4"><text id="here">Tiếp</text></content>`,
	}
}

// editorReviewKey presses a key that types nothing (Tab, Escape) and lets it go.
func editorReviewKey(d *devtools, key, code string, windows int, modifiers int) {
	d.t.Helper()
	d.call("Input.dispatchKeyEvent", map[string]any{"type": "rawKeyDown", "key": key, "code": code, "windowsVirtualKeyCode": windows, "modifiers": modifiers})
	d.call("Input.dispatchKeyEvent", map[string]any{"type": "keyUp", "key": key, "code": code, "windowsVirtualKeyCode": windows, "modifiers": modifiers})
}

// editorCaretIn puts the caret in the middle of the first words of the area that read so.
func editorCaretIn(d *devtools, words string) {
	d.t.Helper()
	d.evaluate(`(function () { var walker = document.createTreeWalker(document.querySelector(".editor-area"), NodeFilter.SHOW_TEXT);
  while (walker.nextNode()) { var at = walker.currentNode.data.indexOf(` + jsString(words) + `); if (at >= 0) { getSelection().collapse(walker.currentNode, at + 2); return "ok"; } }
  throw new Error("no words"); })()`)
	d.evaluate(`new Promise(function (done) { requestAnimationFrame(function () { requestAnimationFrame(done); }); })`)
}

// editorPopoverState is the link popover in one line: shown or not, the link button's aria-expanded,
// where the focus is.
const editorPopoverState = `new Promise(function (done) { requestAnimationFrame(function () { requestAnimationFrame(function () {
  var pop = document.querySelector(".editor-popover"), a = document.activeElement, link = document.querySelector('.editor-toolbar [data-kitwork-command="link"]');
  var focus = a === document.querySelector(".editor-area") ? "area" : a === document.querySelector(".editor-address") ? "address" : a && a.closest(".editor-popover") ? "popover " + a.textContent : a ? a.tagName.toLowerCase() : "none";
  done((pop.hidden ? "closed" : "open") + ", expanded " + link.getAttribute("aria-expanded") + ", open " + kit.scope.$body.open + ", focus " + focus);
}); }); })`

// The link popover closes however the writer leaves it: Escape wherever its focus is (a button of its
// own, not only the address), the focus moved out by Tab into the area, a click in the area. Closed,
// the link button says so (aria-expanded false) and the editor's open is false.
// CONTROL: against a copy of v2.0.0.js whose popover heard Escape only in the address and nothing
// else (the file as it was before the review of 20/10/2026), the three rows "Escape on its button",
// "Tab out into the area" and "a click in the area" each failed: the popover stayed open, expanded.
func TestBrowserEditorPopoverCloses(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the editor browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	site := newEditorSite(buildUISite(t, editorReviewFiles()), "localhost")
	defer site.Close()
	d := openDevtools(t, browser, site.URL+"/", 1000, 1400, "")
	d.loaded(exampleSettle)
	check := func(what, want string) {
		t.Helper()
		if got := d.evaluate(editorPopoverState); got != want {
			t.Errorf("%s:\n got  %s\n want %s", what, got, want)
		}
	}
	tab := func(times int) {
		for i := 0; i < times; i++ {
			editorReviewKey(d, "Tab", "Tab", 9, 0)
		}
	}
	open := func(what string) {
		t.Helper()
		editorClick(d, ".editor-area p:last-child")
		editorCaretIn(d, "trang tài liệu")
		editorPress(d, "Control+K")
		check(what+": Control+K in the link", "open, expanded true, open true, focus address")
	}
	open("Escape")
	tab(1)
	check("Tab from the address", "open, expanded true, open true, focus popover Gắn")
	editorReviewKey(d, "Escape", "Escape", 27, 0)
	check("Escape on its button", "closed, expanded false, open false, focus area")

	open("Tab")
	tab(4)
	check("Tab out into the area", "closed, expanded false, open false, focus area")

	open("click")
	editorClick(d, ".editor-area p:last-child")
	check("a click in the area", "closed, expanded false, open false, focus area")
}

// A link in the writing area is words being written: a click on it puts the caret there, and Drive
// does not move to its page, unsaved or not (a freshly opened post is not).
// CONTROL: against a copy of drive.js whose drivable() did not look at isContentEditable (the file as
// it was before the review of 20/10/2026), the click left the page: Drive fetched /tiep and swapped
// the editor out.
func TestBrowserEditorLinkInTheAreaIsNoMove(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the editor browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	site := newEditorSite(buildUISite(t, editorReviewFiles()), "localhost")
	defer site.Close()
	d := openDevtools(t, browser, site.URL+"/", 1000, 1400, "")
	d.loaded(exampleSettle)
	site.waitFor(t, "the editor to start", func() bool {
		return d.evaluate(`String(document.querySelector(".editor-area").isContentEditable)`) == "true"
	})
	d.evaluate(`window.__before = true`)
	from := site.heard()
	editorClick(d, ".editor-area a")
	time.Sleep(1500 * time.Millisecond)
	got := d.evaluate(`location.pathname + " " + !!window.__before + " " + !!document.querySelector(".editor-area") + " unsaved " + kit.scope.$body.unsaved + " caret in the link " + !!(getSelection().anchorNode && getSelection().anchorNode.parentNode.closest(".editor-area a"))`)
	if want := "/ true true unsaved false caret in the link true"; got != want || site.pages(from) != "" || site.drive(from) != "" {
		t.Errorf("a click on a link in the area:\n got  %s; loaded %q; Drive %q\n want %s, nothing loaded", got, site.pages(from), site.drive(from), want)
	}
}

// On a touch screen the words of the writing area are at least 16px, so a phone does not zoom in on
// focus, even where the author wrote size="small" on <content> (a class of the page layer, which
// outranks the elements layer the editor's look is in).
// CONTROL: against a copy of ui/stylesheet/editor.go whose touch rule had no !important (the file as it
// was before the review of 20/10/2026), /small drew the area and its paragraph at 14px.
func TestBrowserEditorTouchWordsAre16px(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the editor browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	site := newEditorSite(buildUISite(t, editorReviewFiles()), "localhost")
	defer site.Close()
	d := openDevtools(t, browser, site.URL+"/small/", 400, 900, "")
	d.loaded(exampleSettle)
	d.call("Emulation.setTouchEmulationEnabled", map[string]any{"enabled": true, "maxTouchPoints": 5})
	networkReload(d)
	const sizes = `(function () { var area = document.querySelector(".editor-area");
  return [matchMedia("(pointer: coarse)").matches ? "coarse" : "fine", "area " + getComputedStyle(area).fontSize, "p " + getComputedStyle(area.querySelector("p")).fontSize,
    "small " + area.classList.contains("size-small")].join(", "); })()`
	if got, want := d.evaluate(sizes), "coarse, area 16px, p 16px, small true"; got != want {
		t.Errorf("the writing area written small, on a touch screen:\n got  %s\n want %s", got, want)
	}
}

// editorBrowserCanonicalV2 is the editor of toolbar v2 as the owner decided it (20/10/2026, evening),
// word for word the editorCanonical of ui/editor_test.go: a post form, the field's label, a toolbar of
// seven groups (the marks; the headings, H4 to H6 in a dropdown that calls the editor through its
// alias; the blocks; the lists; the alignments; the link and the rule; undo and redo), the link's
// popover, the writing area, the count under it and the words that say the post is not saved.
const editorBrowserCanonicalV2 = `<form method="post" gap="4">
  <field>
    <label>Nội dung</label>
    <editor as="$body" gap="2">
      <box alt="Định dạng" flex="row" wrap="true" align="left" gap="3">
        <group alt="Kiểu chữ" flex="row" gap="1">
          <button click="toggle('bold')" active:background="brand/15"><icon value="bold" alt="Đậm" /></button>
          <button click="toggle('italic')" active:background="brand/15"><icon value="italic" alt="Nghiêng" /></button>
          <button click="toggle('underline')" active:background="brand/15"><icon value="underline" alt="Gạch chân" /></button>
          <button click="toggle('strikethrough')" active:background="brand/15"><icon value="strikethrough" alt="Gạch ngang" /></button>
          <button click="toggle('code')" active:background="brand/15"><icon value="code" alt="Mã" /></button>
        </group>
        <group alt="Tiêu đề" flex="row" gap="1">
          <button click="toggle('heading', 2)" active:background="brand/15"><icon value="h-2" alt="Tiêu đề mục" /></button>
          <button click="toggle('heading', 3)" active:background="brand/15"><icon value="h-3" alt="Tiêu đề nhỏ" /></button>
          <dropdown>
            <button click="toggle()"><icon value="chevron-down" alt="Tiêu đề nhỏ hơn" /></button>
            <popover flex="row" gap="1" padding="1">
              <button click="$body.toggle('heading', 4)" active:background="brand/15"><icon value="h-4" alt="Tiêu đề mức 4" /></button>
              <button click="$body.toggle('heading', 5)" active:background="brand/15"><icon value="h-5" alt="Tiêu đề mức 5" /></button>
              <button click="$body.toggle('heading', 6)" active:background="brand/15"><icon value="h-6" alt="Tiêu đề mức 6" /></button>
            </popover>
          </dropdown>
        </group>
        <group alt="Khối" flex="row" gap="1">
          <button click="toggle('quote')" active:background="brand/15"><icon value="blockquote" alt="Trích dẫn" /></button>
          <button click="toggle('code', 'block')" active:background="brand/15"><icon value="source-code" alt="Khối mã" /></button>
        </group>
        <group alt="Danh sách" flex="row" gap="1">
          <button click="toggle('list')" active:background="brand/15"><icon value="list" alt="Danh sách" /></button>
          <button click="toggle('list', 'ordered')" active:background="brand/15"><icon value="list-numbers" alt="Danh sách số" /></button>
        </group>
        <group alt="Căn lề" flex="row" gap="1">
          <button click="align('left')" active:background="brand/15"><icon value="align-left" alt="Căn trái" /></button>
          <button click="align('center')" active:background="brand/15"><icon value="align-center" alt="Căn giữa" /></button>
          <button click="align('right')" active:background="brand/15"><icon value="align-right" alt="Căn phải" /></button>
        </group>
        <group alt="Chèn" flex="row" gap="1">
          <button click="link()"><icon value="link" alt="Liên kết" /></button>
          <button click="rule()"><icon value="separator-horizontal" alt="Đường kẻ ngang" /></button>
        </group>
        <group alt="Lịch sử" flex="row" gap="1">
          <button click="undo()"><icon value="arrow-back-up" alt="Hoàn tác" /></button>
          <button click="redo()"><icon value="arrow-forward-up" alt="Làm lại" /></button>
        </group>
      </box>
      <popover place="below-left" flex="row" wrap="true" gap="2" padding="2" radius="medium" background="paper">
        <input value="{{ address }}" alt="Địa chỉ liên kết" placeholder="https://… hay /trang" />
        <button click="link(address)">Gắn</button>
        <button click="unlink()">Bỏ liên kết</button>
        <button click="close()">Huỷ</button>
        <error>Chỉ nhận https://, http://, mailto:, tel: hay đường dẫn bắt đầu bằng /.</error>
      </popover>
      <content name="body" value="{{ post.body }}" required="true" placeholder="Viết bài ở đây…" rows="12"
               padding="4" radius="medium" border="line" />
    </editor>
    <hint>{{ $body.words }} từ</hint>
  </field>
  <button type="submit">Lưu bài</button>
  <text show="$body.unsaved">Chưa lưu</text>
</form>`

// editorV2Saved is the post the v2 page starts with: one line of a meeting's notes.
const editorV2Saved = `<p>Họp nhóm lúc chín giờ mười giờ sáng nay</p>`

// editorV2Files is editorBrowserFiles with the toolbar of v2: the page holds the canonical editor of v2
// starting with editorV2Saved, the post shown through <richtext> (cleaned by richtext() of "kitwork")
// and what was sent, to the letter, in a block of HTML code. Its router.post answers with the page for
// what was sent. Nothing is stored.
var editorV2Files = map[string]string{
	"localhost/router.work": `import { router, richtext } from "kitwork";

router.language("vi");
router.reload(false);

router.view((ctx) => {
  const post = { body: "` + editorV2Saved + `" };
  const sent = "";
  const kept = richtext(post.body);
});

router.post((ctx) => {
  const sent = ctx.request.form("body");
  return ctx.view({ post: { body: sent }, sent, kept: richtext(sent) });
});
`,
	"localhost/page.ui": `<page><content /></page>`,
	"localhost/content.ui": `<content padding="4" gap="3">
` + editorBrowserCanonicalV2 + `
  <box id="shown"><richtext value="{{ kept.value }}" /></box>
  <code language="html">{{ sent }}</code>
</content>`,
}

// editorV2Paste is what Google Docs puts on the clipboard for two paragraphs: the first centred
// (text-align in its style), words of it underlined, struck through and in a monospace face, each said
// in a span's style (text-decoration, font-family), inside Docs's wrapper b of font-weight normal;
// editorV2PastePlain is the plain text beside it.
const (
	editorV2Paste = `<meta charset="utf-8"><b style="font-weight:normal;" id="docs-internal-guid-4b9e0c31-7fff-52d1-9a0e-1f2e3d4c5b6a">` +
		`<p dir="ltr" style="line-height:1.38;text-align: center;margin-top:0pt;margin-bottom:0pt;">` +
		`<span style="font-size:11pt;font-family:Arial,sans-serif;font-weight:400;text-decoration:underline;-webkit-text-decoration-skip:none;text-decoration-skip-ink:none;white-space:pre-wrap;">Ghi chú</span>` +
		`<span style="font-size:11pt;font-family:Arial,sans-serif;font-weight:400;white-space:pre-wrap;"> từ Docs, </span>` +
		`<span style="font-size:11pt;font-family:Arial,sans-serif;font-weight:400;text-decoration:line-through;-webkit-text-decoration-skip:none;white-space:pre-wrap;">bỏ ý này</span>` +
		`<span style="font-size:11pt;font-family:Arial,sans-serif;font-weight:400;white-space:pre-wrap;"> và chạy </span>` +
		`<span style="font-size:10pt;font-family:'Roboto Mono',monospace;font-weight:400;white-space:pre-wrap;">go build</span></p>` +
		`<p dir="ltr" style="line-height:1.38;margin-top:0pt;margin-bottom:0pt;"><span style="font-size:11pt;font-family:Arial,sans-serif;font-weight:400;white-space:pre-wrap;">Dòng cuối của Docs</span></p></b>`
	editorV2PastePlain = "Ghi chú từ Docs, bỏ ý này và chạy go build\nDòng cuối của Docs"
)

// editorV2Probe adds to editorBrowserProbe what toolbar v2 shows (installed once in each document):
// read, the formats and the commands pressed (an alignment is a command with aria-pressed), those
// disabled, the dropdown of smaller headings (its popover shown, its trigger's aria-expanded, its open)
// and the focus; look, what a richtext (or the writing area, a richtext too) shows, one run of words a
// line, the words drawn alike joined, however its elements nest: the words, the line they stand in (its
// tag and class) and its text-align, their weight, style, the lines drawn through or under them
// (text-decoration-line of every element up to the richtext), their face (monospace or not), size,
// white-space and the fill of the code they stand in; and each rule, its top edge.
const editorV2Probe = `(function () {
  if (window.__editorV2) return "ready";
  // (a switch of format by the format it writes: format=rich)
  var name = function (b) { return b.getAttribute("data-kitwork-format") || (b.hasAttribute("data-kitwork-format-value") ? "format=" + b.getAttribute("data-kitwork-format-value") : b.getAttribute("data-kitwork-command")); };
  var names = function (selector) { return Array.prototype.map.call(document.querySelectorAll(selector), name).join(" ") || "none"; };
  window.__editorV2 = {
    read: function () {
      return window.__editor.frames().then(function () {
        var menu = document.querySelector('.editor-toolbar [data-kitwork-component="dropdown"]'), trigger = menu.querySelector("button");
        var pop = menu.querySelector(".popover"), active = document.activeElement;
        return JSON.stringify({
          pressed: names('.editor [aria-pressed="true"]'),
          disabled: names('.editor-toolbar [aria-disabled="true"]'),
          menu: (pop.hidden ? "closed" : "open") + ", expanded " + trigger.getAttribute("aria-expanded") + ", open " + kit.scopeFor(menu).open,
          focus: active === document.querySelector(".editor-area") ? "area" : active === trigger ? "trigger" : active ? active.tagName.toLowerCase() + (name(active) ? "[" + name(active) + "]" : "") : "none"
        });
      });
    },
    look: function (selector) {
      var root = document.querySelector(selector), runs = [], walker = document.createTreeWalker(root, NodeFilter.SHOW_ELEMENT | NodeFilter.SHOW_TEXT);
      while (walker.nextNode()) {
        var node = walker.currentNode;
        if (node.nodeType === 1) {
          if (node.tagName === "HR") { var r = getComputedStyle(node); runs.push({ text: "", key: "hr " + r.borderTopWidth + " " + r.borderTopStyle }); }
          continue;
        }
        var el = node.parentNode, s = getComputedStyle(el), line = el.closest("p,h2,h3,h4,h5,h6,li,pre"), lines = [];
        if (!line) continue; // the white space between blocks
        var filled = line.tagName === "PRE" ? line : el.closest("code"); // a code block's fill is its pre's
        for (var up = el; up !== root; up = up.parentNode) {
          var drawn = getComputedStyle(up).textDecorationLine;
          if (drawn !== "none" && lines.indexOf(drawn) < 0) lines.push(drawn);
        }
        var key = [line.tagName.toLowerCase() + (line.className ? "." + line.className : ""), getComputedStyle(line).textAlign, "weight " + s.fontWeight, s.fontStyle,
          lines.sort().join("+") || "no line", /mono/i.test(s.fontFamily) ? "mono" : "text", s.fontSize, s.whiteSpace, filled && getComputedStyle(filled).backgroundColor !== "rgba(0, 0, 0, 0)" ? "fill" : "no fill"].join(" ");
        var text = s.whiteSpace === "pre" ? node.data : node.data.replace(/[\s ]+/g, " "), last = runs[runs.length - 1];
        if (last && last.key === key && last.line === line) last.text += text;
        else runs.push({ text: text, key: key, line: line });
      }
      // a line's words as the browser lays them out: no white space at either end
      runs.forEach(function (run, i) {
        if (!run.line || run.line.tagName === "PRE") return;
        if (!runs[i - 1] || runs[i - 1].line !== run.line) run.text = run.text.replace(/^ /, "");
        if (!runs[i + 1] || runs[i + 1].line !== run.line) run.text = run.text.replace(/ $/, "");
      });
      return runs.filter(function (run) { return run.text || !run.line; }).map(function (run) { return (run.line ? JSON.stringify(run.text) + " " : "") + run.key; }).join("\n");
    }
  };
  return "installed";
})()`

// editorV2Read is one look at toolbar v2 (editorV2Probe's read).
type editorV2Read struct{ Pressed, Disabled, Menu, Focus string }

// editorV2Look reads the editor (editorStep: held to the identity) and its toolbar of v2, in one line:
// the field, the state, the formats and commands pressed, those disabled, the dropdown, the focus, what
// ran.
func editorV2Look(t *testing.T, d *devtools, name string) string {
	t.Helper()
	got := editorStep(t, d, name)
	d.evaluate(editorV2Probe)
	var bar editorV2Read
	if raw := d.evaluate(`window.__editorV2.read()`); json.Unmarshal([]byte(raw), &bar) != nil {
		t.Fatalf("the v2 probe read %s", raw)
	}
	return strings.Join([]string{got.Field, got.State, "pressed " + bar.Pressed, "disabled " + bar.Disabled, "menu " + bar.Menu, "focus " + bar.Focus, "ran " + got.Ran}, " | ")
}

// editorV2Wants is what editorV2Look should read for a field: its state (its counts richtext.Clean's,
// the popover closed, the address as given) and the rest as given.
func editorV2Wants(field string, unsaved bool, address, pressed, disabled, menu, focus string) string {
	doc := richtext.Clean(field)
	state := fmt.Sprintf(`empty %t, words %d, characters %d, unsaved %t, open false, address %q`, field == "", doc.Words, doc.Characters, unsaved, address)
	return strings.Join([]string{field, state, "pressed " + pressed, "disabled " + disabled, "menu " + menu, "focus " + focus, "ran nothing"}, " | ")
}

// editorV2Written is the post TestBrowserEditorWritesGrammarTwo writes, as the field holds it and the
// form posts it.
const editorV2Written = "<p><u>Họp nhóm</u> lúc <s>chín giờ</s> mười giờ <u>sáng nay</u>, chạy <code>go test</code> trước.</p>\n" +
	"<h4 class=\"align-center\">Việc cần làm</h4>\n<p>Chạy hai lệnh:</p>\n<pre><code>go vet ./...\ngo test ./...</code></pre>\n" +
	"<p>Xong thì báo.</p>\n<hr>\n<p class=\"align-right\">Hẹn gặp lại.</p>\n" +
	"<p class=\"align-center\"><u>Ghi chú</u> từ Docs, <s>bỏ ý này</s> và chạy <code>go build</code></p>\n<p>Dòng cuối của Docs</p>"

// editorV2Drawn is what a richtext shows of editorV2Written (editorV2Probe's look), one run of words a
// line: the words, their line and its text-align, weight, style, the lines through or under them, their
// face, size, white-space and fill; a rule its top edge. Underlined and struck words drawn so, code in
// the code face on its fill, the heading (600) and the pasted paragraph centred, the line after the rule
// at the end (align-right: text-align end), the code block in the code face on its fill keeping its
// newline, the rule a 1px line.
const editorV2Drawn = `"Họp nhóm" p start weight 400 normal underline text 16px normal no fill
" lúc " p start weight 400 normal no line text 16px normal no fill
"chín giờ" p start weight 400 normal line-through text 16px normal no fill
" mười giờ " p start weight 400 normal no line text 16px normal no fill
"sáng nay" p start weight 400 normal underline text 16px normal no fill
", chạy " p start weight 400 normal no line text 16px normal no fill
"go test" p start weight 400 normal no line mono 16px normal fill
" trước." p start weight 400 normal no line text 16px normal no fill
"Việc cần làm" h4.align-center center weight 600 normal no line text 16px normal no fill
"Chạy hai lệnh:" p start weight 400 normal no line text 16px normal no fill
"go vet ./...\ngo test ./..." pre start weight 400 normal no line mono 16px pre fill
"Xong thì báo." p start weight 400 normal no line text 16px normal no fill
hr 1px solid
"Hẹn gặp lại." p.align-right end weight 400 normal no line text 16px normal no fill
"Ghi chú" p.align-center center weight 400 normal underline text 16px normal no fill
" từ Docs, " p.align-center center weight 400 normal no line text 16px normal no fill
"bỏ ý này" p.align-center center weight 400 normal line-through text 16px normal no fill
" và chạy " p.align-center center weight 400 normal no line text 16px normal no fill
"go build" p.align-center center weight 400 normal no line mono 16px normal fill
"Dòng cuối của Docs" p start weight 400 normal no line text 16px normal no fill`

// TestBrowserEditorWritesGrammarTwo proves the editor of toolbar v2 end to end on a page the engine
// serves, in headless Chrome or Edge, with the hands and eyes of TestBrowserEditorWritesAPost (the mouse,
// the keys, the keyboard's text; every look held to the identity: the area cleaned by Go is the field,
// its own Clean, its counts Clean's; the page's scripts complain of nothing):
//   - the page as the server writes it: the groups (role="group", named), the dropdown of smaller
//     headings (its trigger a stop of the toolbar controlling its popover, the popover's commands called
//     through the alias), the alignments and the rule, Control+U on underline;
//   - words underlined with the toolbar's U, underlined with Control+U, struck through with S, Control+U
//     with nothing selected leaving the underline off for what is typed next, words made code with the
//     code button; the buttons pressed by the selection, the alignment of the caret's block (left: no
//     class) pressed among the alignments;
//   - a line made H4 from the dropdown: its trigger opens it (aria-expanded, open), H4 in its popover
//     makes the line a heading through $body and the dropdown closes, the selection and the focus back
//     in the area, H4 pressed; the heading centred with the align button, the paragraph after it (Enter)
//     centred too and put back left;
//   - a line made a code block: in it the marks, the link and the alignments are disabled, Enter writes
//     a newline, Enter on its empty last line leaves it for a paragraph;
//   - a rule after the last line (rule()): in the value at once (21/10/2026: a rule that ends the post
//     is kept), the empty line under it not; the line after it typed and aligned right;
//   - a paste of Google Docs: a centred paragraph whose spans say underline, line-through and a
//     monospace face, and a plain one, taken by the editor as u, s, code and the align-center class;
//   - Save, the browser's own post: what was sent is exactly editorV2Written, the field as the editor
//     showed it; richtext() keeps it as it is; <richtext> shows it exactly as richtext.Display writes it,
//     and shows what the editor's area showed (editorV2Drawn, the area's and <richtext>'s alike: the
//     alignments, the lines, the code face and fills, the code block's white space, the rule's line);
//     the answer's editor holds it again and shows it so.
//
// CONTROL, each against a copy served through -overlay (the tree untouched; controls.py and
// run_controls.sh in the scratchpad's editor-more/integrate, run 21/10/2026), the proof failing where
// named: the editor (jit/js/components/editor/v2.0.0.js) with Control+U refused again (KEYS u: []) at
// "Họp nhóm with Control+U"; with its buttons found by context.owned (the dropdown's, in a component of
// its own, left out) at "H5 by the keyboard" (H5 not pressed); without its click listener on window that
// closes the dropdown after a command at "H5 by the keyboard" (the dropdown open); with enter() taking a
// code block for a paragraph at "Enter in the code block"; reading no text-decoration in a paste at
// "pasted"; the compiler (ui/editor.go) with editorMenuElement writing nothing at "the page as the server
// writes it" (no trigger wire, no H4 command); the display (ui/stylesheet/richtext.go) without the rule
// of align-right, or of the fill of code in a line, or of the rule's line, at "the post as the editor
// drew it", "the post drawn by <richtext>" and "the answer's editor, drawn"; the cleaner
// (utilities/richtext/tree.go) folding h4–h6 into h3 again at "H5" (the area cleaned is not the field).
// On 21/10/2026 (controls.py in the scratchpad's final-rule): the editor with read() dropping a rule at
// the end again (its kept.pop() put back) at "a rule after the last line, nothing under it yet" (the
// field without the rule, and not the area cleaned).
// Not a control: without the rule of u the proof still passes (a browser underlines u by itself).
func TestBrowserEditorWritesGrammarTwo(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the editor browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant := buildUISite(t, editorV2Files)
	first := serveUI(t, tenant, "http://localhost/")
	served := first.Body.String()
	if first.Code != http.StatusOK || warningsScriptRe.MatchString(served) {
		t.Fatalf("/ = %d, or it links warnings:\n%.3000s", first.Code, served)
	}
	for _, want := range []string{
		`<div class="flex-row gap-1 editor-group" aria-label="Kiểu chữ" role="group">`,
		`data-kitwork-format="underline" aria-pressed="false" aria-keyshortcuts="Control+U"`,
		`aria-controls="kitwork-editor-1-menu-1" data-kitwork-bind="aria-expanded: open" data-kitwork-event="click: toggle()"`,
		`data-kitwork-format="heading-4" aria-pressed="false" data-kitwork-event="click: $body.toggle(&#39;heading&#39;, 4)"`,
		`data-kitwork-command="align-center" aria-pressed="false"`, `data-kitwork-command="rule"`,
		`/_kitwork/components/editor.`, `/_kitwork/components/dropdown.`,
	} {
		if !strings.Contains(served, want) {
			t.Errorf("the page as the server writes it has no %s", want)
		}
	}

	site := newEditorSite(tenant, "localhost")
	defer site.Close()
	check := func(what, got, want string) {
		t.Helper()
		if got != want {
			t.Errorf("%s:\n got  %s\n want %s", what, got, want)
		}
	}
	look, wants := editorV2Look, editorV2Wants
	const closed = "closed, expanded false, open false"
	const marks = "bold italic underline strikethrough code"
	const aligns = "align-left align-center align-right"

	d := openDevtools(t, browser, site.URL+"/", 1000, 1400, "")
	d.loaded(exampleSettle)
	d.call("Page.enable", map[string]any{})
	d.call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": editorBrowserHeard})
	networkReload(d)
	d.evaluate(`window.__before = true`)
	saved := richtext.Clean(editorV2Saved).HTML
	check("as it starts", look(t, d, "as it starts"), wants(saved, false, "", "none", aligns+" link undo redo", closed, "body"))

	// the marks: U, Control+U, S, Control+U at a caret, code
	editorClick(d, ".editor-area p")
	d.evaluate(`window.__editor.select("sáng nay")`)
	editorClick(d, `[data-kitwork-format="underline"]`)
	post := "<p>Họp nhóm lúc chín giờ mười giờ <u>sáng nay</u></p>"
	check("sáng nay with U", look(t, d, "U"), wants(post, true, "", "underline align-left", "redo", closed, "area"))
	d.evaluate(`window.__editor.select("Họp nhóm")`)
	editorPress(d, "Control+U")
	post = "<p><u>Họp nhóm</u> lúc chín giờ mười giờ <u>sáng nay</u></p>"
	check("Họp nhóm with Control+U", look(t, d, "Control+U"), wants(post, true, "", "underline align-left", "redo", closed, "area"))
	d.evaluate(`window.__editor.select("chín giờ")`)
	editorClick(d, `[data-kitwork-format="strikethrough"]`)
	post = "<p><u>Họp nhóm</u> lúc <s>chín giờ</s> mười giờ <u>sáng nay</u></p>"
	check("chín giờ with S", look(t, d, "S"), wants(post, true, "", "strikethrough align-left", "redo", closed, "area"))
	editorPress(d, "Control+End")
	editorPress(d, "Control+U")
	editorType(d, ", chạy go test trước.")
	post = "<p><u>Họp nhóm</u> lúc <s>chín giờ</s> mười giờ <u>sáng nay</u>, chạy go test trước.</p>"
	check("typed after the underline, Control+U first", look(t, d, "typed"), wants(post, true, "", "align-left", "link redo", closed, "area"))
	d.evaluate(`window.__editor.select("go test")`)
	editorClick(d, `[data-kitwork-format="code"]`)
	post = strings.Replace(post, "go test", "<code>go test</code>", 1)
	check("go test with the code button", look(t, d, "code"), wants(post, true, "", "code align-left", "redo", closed, "area"))

	// H4 from the dropdown, centred; the paragraph after it centred too, then left
	editorPress(d, "Control+End")
	editorPress(d, "Enter")
	editorType(d, "Việc cần làm")
	post += "\n<p>Việc cần làm</p>"
	// by the keyboard first: Alt+F10 to the toolbar, the arrows along it to the dropdown's trigger (a
	// stop of its own), Enter opens it, Tab into its popover, Enter on H5
	editorReviewKey(d, "F10", "F10", 121, 1)
	for i := 0; i < 7; i++ {
		editorReviewKey(d, "ArrowRight", "ArrowRight", 39, 0)
	}
	check("the trigger reached by the arrows", look(t, d, "arrows"), wants(post, true, "", "align-left", "link redo", closed, "trigger"))
	editorPress(d, "Enter")
	check("Enter on the trigger", look(t, d, "dropdown by the keyboard"), wants(post, true, "", "align-left", "link redo", "open, expanded true, open true", "trigger"))
	editorReviewKey(d, "Tab", "Tab", 9, 0)
	editorReviewKey(d, "Tab", "Tab", 9, 0)
	check("Tab into its popover", look(t, d, "Tab"), wants(post, true, "", "align-left", "link redo", "open, expanded true, open true", "button[heading-5]"))
	editorPress(d, "Enter")
	check("H5 by the keyboard", look(t, d, "H5"), wants(strings.Replace(post, "<p>Việc cần làm</p>", "<h5>Việc cần làm</h5>", 1), true, "", "heading-5 align-left", "link redo", closed, "area"))
	// then by the mouse: the trigger, H4
	editorClick(d, `.editor-toolbar [data-kitwork-component="dropdown"] > button`)
	check("the dropdown opened", look(t, d, "dropdown"), wants(strings.Replace(post, "<p>Việc cần làm</p>", "<h5>Việc cần làm</h5>", 1), true, "", "heading-5 align-left", "link redo", "open, expanded true, open true", "trigger"))
	editorClick(d, `[data-kitwork-format="heading-4"]`)
	post = strings.Replace(post, "<p>Việc cần làm</p>", "<h4>Việc cần làm</h4>", 1)
	check("H4 from the dropdown", look(t, d, "H4"), wants(post, true, "", "heading-4 align-left", "link redo", closed, "area"))
	editorClick(d, `[data-kitwork-command="align-center"]`)
	post = strings.Replace(post, "<h4>", `<h4 class="align-center">`, 1)
	check("the heading centred", look(t, d, "centred"), wants(post, true, "", "heading-4 align-center", "link redo", closed, "area"))
	editorPress(d, "Enter")
	editorType(d, "Chạy hai lệnh:")
	check("the line after it", look(t, d, "after the heading"), wants(post+"\n<p class=\"align-center\">Chạy hai lệnh:</p>", true, "", "align-center", "link redo", closed, "area"))
	editorClick(d, `[data-kitwork-command="align-left"]`)
	post += "\n<p>Chạy hai lệnh:</p>"
	check("put back left", look(t, d, "left"), wants(post, true, "", "align-left", "link redo", closed, "area"))

	// a code block: Enter in it a newline, Enter on its empty last line out of it
	editorPress(d, "Enter")
	editorType(d, "go vet ./...")
	editorClick(d, `[data-kitwork-format="code-block"]`)
	inCode := marks + " " + aligns + " link redo"
	check("a code block", look(t, d, "code block"), wants(post+"\n<pre><code>go vet ./...</code></pre>", true, "", "code-block", inCode, closed, "area"))
	editorPress(d, "Enter")
	editorType(d, "go test ./...")
	check("Enter in the code block", look(t, d, "a newline"), wants(post+"\n<pre><code>go vet ./...\ngo test ./...</code></pre>", true, "", "code-block", inCode, closed, "area"))
	editorPress(d, "Enter")
	editorPress(d, "Enter")
	post += "\n<pre><code>go vet ./...\ngo test ./...</code></pre>"
	check("Enter on its empty last line", look(t, d, "out of it"), wants(post, true, "", "align-left", "link redo", closed, "area"))

	// a rule after a line, the line after it aligned right
	editorType(d, "Xong thì báo.")
	editorClick(d, `[data-kitwork-command="rule"]`)
	post += "\n<p>Xong thì báo.</p>"
	check("a rule after the last line, nothing under it yet: the rule is in the value", look(t, d, "rule"), wants(post+"\n<hr>", true, "", "align-left", "link redo", closed, "area"))
	editorType(d, "Hẹn gặp lại.")
	editorClick(d, `[data-kitwork-command="align-right"]`)
	post += "\n<hr>\n<p class=\"align-right\">Hẹn gặp lại.</p>"
	check("the line after the rule, right", look(t, d, "right"), wants(post, true, "", "align-right", "link redo", closed, "area"))

	// a paste of Google Docs on a line of its own
	editorPress(d, "Control+End")
	editorPress(d, "Enter")
	check("the paste", d.evaluate(`window.__editor.paste(`+jsString(editorV2Paste)+`, `+jsString(editorV2PastePlain)+`)`), "taken")
	post += "\n<p class=\"align-center\"><u>Ghi chú</u> từ Docs, <s>bỏ ý này</s> và chạy <code>go build</code></p>\n<p>Dòng cuối của Docs</p>"
	check("pasted", post, editorV2Written)
	check("pasted", look(t, d, "pasted"), wants(editorV2Written, true, "", "align-left", "link redo", closed, "area"))
	d.evaluate(editorV2Probe)
	shownBefore := d.evaluate(`window.__editorV2.look(".editor-area")`)

	// Save: the browser posts the form, the answer a new document
	from := site.heard()
	editorClickAway(d, `button[type="submit"]`)
	check("Save clicked", editorDialog(d), "no question")
	site.waitFor(t, "the post", func() bool { return strings.Contains(site.pages(from), "POST") })
	d.loaded(`(window.__before ? "wait of 0" : ` + exampleSettle + `)`)
	check("what the browser sent", site.pages(from), "POST / 200 site=same-origin application/x-www-form-urlencoded")
	sent := d.evaluate(`document.querySelector("pre code.language-html").textContent`)
	check("what was sent", sent, editorV2Written)
	check("what richtext() keeps of it", richtext.Clean(sent).HTML, sent)
	check("the post shown", d.evaluate(`document.querySelector("#shown .richtext").innerHTML`), richtext.Display(sent, "http://localhost"))
	d.evaluate(editorV2Probe)
	shown := d.evaluate(`window.__editorV2.look("#shown .richtext")`)
	check("the post as the editor drew it", shownBefore, editorV2Drawn)
	check("the post drawn by <richtext>", shown, editorV2Drawn)
	check("the answer's editor, drawn", d.evaluate(`window.__editorV2.look(".editor-area")`), editorV2Drawn)
	check("the answer's editor", look(t, d, "the answer"), wants(editorV2Written, true, "", "none", aligns+" link undo redo", closed, "body"))
	check("complaints", d.evaluate(`JSON.stringify(window.__complaints)`), "[]")
}

// editorV2Page opens the page of editorV2Files holding saved in place of editorV2Saved, the probe of
// the editor installed (editorBrowserProbe).
func editorV2Page(t *testing.T, saved string) *devtools {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping the editor browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	files := map[string]string{}
	for name, source := range editorV2Files {
		files[name] = strings.Replace(source, editorV2Saved, saved, 1)
	}
	site := newEditorSite(buildUISite(t, files), "localhost")
	t.Cleanup(site.Close)
	d := openDevtools(t, browser, site.URL+"/", 1000, 1400, "")
	d.loaded(exampleSettle)
	d.call("Page.enable", map[string]any{})
	d.call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": editorBrowserHeard})
	networkReload(d)
	d.evaluate(editorBrowserProbe)
	return d
}

// editorKeysState is what a proof by the keys reads, once drawn: the field, where the caret is (a text
// and its offset, or the writing area itself: "area@3"), the words selected, the focus (a button of the
// toolbar by its format or command, the dropdown's own button "trigger", "(hidden)" after a button in a
// closed popover) and the toolbar's dropdown (shown or not, its button's aria-expanded).
const editorKeysState = `new Promise(function (done) { requestAnimationFrame(function () { requestAnimationFrame(function () {
  var area = document.querySelector(".editor-area"), s = getSelection(), a = document.activeElement;
  var menu = document.querySelector('.editor-toolbar [data-kitwork-component="dropdown"]'), pop = menu.querySelector(".popover"), trigger = menu.querySelector("button");
  var name = function (b) { return b === trigger ? "trigger" : b.getAttribute("data-kitwork-format") || b.getAttribute("data-kitwork-command") || b.tagName.toLowerCase(); };
  var place = function (n, o) { return !n ? "none" : n === area ? "area@" + o : n.nodeType === 3 ? n.data + "@" + o : n.nodeName.toLowerCase() + "@" + o; };
  var popover = a && a.closest(".popover");
  done(JSON.stringify({ field: document.querySelector(".editor-field").value, caret: place(s.anchorNode, s.anchorOffset), selected: s.toString(),
    focus: a === area ? "area" : a && a.closest(".editor-toolbar") ? name(a) + (popover && popover.hidden ? " (hidden)" : "") : a ? a.tagName.toLowerCase() : "none",
    menu: (pop.hidden ? "closed" : "open") + ", expanded " + trigger.getAttribute("aria-expanded") }));
}); }); })`

// editorKeysRead is editorKeysState read.
type editorKeysRead struct {
	Field, Caret, Selected, Focus, Menu string
}

func editorKeysLook(t *testing.T, d *devtools) editorKeysRead {
	t.Helper()
	var r editorKeysRead
	if err := json.Unmarshal([]byte(d.evaluate(editorKeysState)), &r); err != nil {
		t.Fatalf("the page reads no state: %v", err)
	}
	return r
}

// editorCaretAt puts the caret in the first words of the area that read so, offset characters in (-1:
// at their end).
func editorCaretAt(d *devtools, words string, offset int) {
	d.t.Helper()
	at := fmt.Sprint(offset)
	if offset < 0 {
		at = fmt.Sprint(len([]rune(words)))
	}
	d.evaluate(`(function () { var walker = document.createTreeWalker(document.querySelector(".editor-area"), NodeFilter.SHOW_TEXT);
  while (walker.nextNode()) { var at = walker.currentNode.data.indexOf(` + jsString(words) + `); if (at >= 0) { getSelection().collapse(walker.currentNode, at + ` + at + `); return "ok"; } }
  throw new Error("no words"); })()`)
	d.evaluate(`new Promise(function (done) { requestAnimationFrame(function () { requestAnimationFrame(done); }); })`)
}

// The caret beside a rule, where Chrome's own arrows leave it (ArrowRight from the end of the line before
// the rule puts it in the writing area itself, before the rule): a heading, Enter, a paste, a mark at the
// caret then words typed, and a selection made from there by Shift and the arrows, each by the keyboard
// and the mouse as a person does it, act on the line before the rule (at its end), or for the selection
// the line after it (the review of 21/10/2026 found them all at the start of the post's first line, off
// screen in a long post).
// CONTROL, against a copy of v2.0.0.js through -overlay (run 21/10/2026, fix in the scratchpad): with
// within() handing back every range as it is, "H2 beside the rule" failed (<h2>Mở đầu bài</h2>), and every
// row after it; with inward() taking the line's last text, empty or not, "Control+U beside the rule, then
// y" failed (<p>xdány</p>: the paste's split left an empty text last, where the browser does not type).
func TestBrowserEditorRuleCaretByTheKeys(t *testing.T) {
	d := editorV2Page(t, `<p>Mở đầu bài</p><p>đoạn hai</p><p>đoạn ba</p><hr><p>đoạn bốn</p>`)
	check := func(what string, got, want string) {
		t.Helper()
		if got != want {
			t.Errorf("%s:\n got  %q\n want %q", what, got, want)
		}
	}
	beside := func(words, place string) {
		t.Helper()
		editorCaretAt(d, words, -1)
		editorReviewKey(d, "ArrowRight", "ArrowRight", 39, 0)
		check("ArrowRight from the end of "+words+" (Chrome's caret, beside the rule)", editorKeysLook(t, d).Caret, place)
	}
	rest := "\n<hr>\n<p>đoạn bốn</p>"
	editorClick(d, ".editor-area p:nth-child(3)")
	beside("đoạn ba", "area@3")
	editorClick(d, `[data-kitwork-format="heading-2"]`)
	check("H2 beside the rule", editorKeysLook(t, d).Field, "<p>Mở đầu bài</p>\n<p>đoạn hai</p>\n<h2>đoạn ba</h2>"+rest)
	beside("đoạn ba", "area@3")
	editorPress(d, "Enter")
	editorType(d, "x")
	check("Enter beside the rule, then x", editorKeysLook(t, d).Field, "<p>Mở đầu bài</p>\n<p>đoạn hai</p>\n<h2>đoạn ba</h2>\n<p>x</p>"+rest)
	beside("x", "area@4")
	check("a paste beside the rule", d.evaluate(`window.__editor.paste("<p>dán</p>", "dán")`), "taken")
	check("pasted", editorKeysLook(t, d).Field, "<p>Mở đầu bài</p>\n<p>đoạn hai</p>\n<h2>đoạn ba</h2>\n<p>xdán</p>"+rest)
	beside("dán", "area@4")
	editorPress(d, "Control+U")
	editorType(d, "y")
	check("Control+U beside the rule, then y", editorKeysLook(t, d).Field, "<p>Mở đầu bài</p>\n<p>đoạn hai</p>\n<h2>đoạn ba</h2>\n<p>xdán<u>y</u></p>"+rest)
	beside("y", "area@4")
	editorReviewKey(d, "ArrowRight", "ArrowRight", 39, 8)
	editorReviewKey(d, "End", "End", 35, 8)
	editorClick(d, `[data-kitwork-format="bold"]`)
	check("a selection from beside the rule by Shift+ArrowRight and Shift+End, then B", editorKeysLook(t, d).Field,
		"<p>Mở đầu bài</p>\n<p>đoạn hai</p>\n<h2>đoạn ba</h2>\n<p>xdán<u>y</u></p>\n<hr>\n<p><strong>đoạn bốn</strong></p>")
	check("complaints", d.evaluate(`JSON.stringify(window.__complaints)`), "[]")
}

// The toolbar's dropdown of smaller headings by the keyboard: Escape on its button closes it, the focus
// back in the writing area with the words still selected; Escape on a button of its popover closes it,
// the focus on its button (never left on a button hidden); a Tab past its last button closes it, so
// Shift+Tab from the area comes back to the toolbar's stop, not into the closed popover; a press in the
// area closes it too. Its button says so each time (aria-expanded false). (The review of 21/10/2026: it
// stayed open, expanded, through all of these.)
// CONTROL, against a copy of v2.0.0.js through -overlay (run 21/10/2026, fix in the scratchpad): with the
// toolbar's keydown and focusout as they were (Escape heard on a stop only, back to the area, nothing
// closed), "Escape on its button" failed (open, expanded true), and so did "a press in the area".
func TestBrowserEditorToolbarDropdownByTheKeys(t *testing.T) {
	d := editorV2Page(t, `<p>một hai ba</p><p>bốn năm</p>`)
	check := func(what string, got editorKeysRead, focus, menu string) {
		t.Helper()
		if got.Focus != focus || got.Menu != menu {
			t.Errorf("%s:\n got  focus %s, %s\n want focus %s, %s", what, got.Focus, got.Menu, focus, menu)
		}
	}
	const open, closed = "open, expanded true", "closed, expanded false"
	key := func(name, code string, windows, modifiers int) { editorReviewKey(d, name, code, windows, modifiers) }
	editorClick(d, ".editor-area p")
	d.evaluate(`window.__editor.select("hai")`)
	key("F10", "F10", 121, 1)
	for i := 0; i < 7; i++ {
		key("ArrowRight", "ArrowRight", 39, 0)
	}
	check("its button by the arrows", editorKeysLook(t, d), "trigger", closed)
	editorPress(d, "Enter")
	check("Enter opens it", editorKeysLook(t, d), "trigger", open)
	key("Escape", "Escape", 27, 0)
	got := editorKeysLook(t, d)
	check("Escape on its button", got, "area", closed)
	if got.Selected != "hai" {
		t.Errorf("the words selected stay selected: %q", got.Selected)
	}
	key("F10", "F10", 121, 1)
	editorPress(d, "Enter")
	key("Tab", "Tab", 9, 0)
	check("Tab into its popover", editorKeysLook(t, d), "heading-4", open)
	key("Escape", "Escape", 27, 0)
	check("Escape on a button of its popover", editorKeysLook(t, d), "trigger", closed)
	editorPress(d, "Enter")
	for i := 0; i < 4; i++ {
		key("Tab", "Tab", 9, 0)
	}
	check("a Tab past its last button", editorKeysLook(t, d), "area", closed)
	key("Tab", "Tab", 9, 8)
	check("Shift+Tab from the area", editorKeysLook(t, d), "trigger", closed)
	editorClick(d, `.editor-toolbar [data-kitwork-component="dropdown"] > button`)
	check("its button pressed", editorKeysLook(t, d), "trigger", open)
	editorClick(d, ".editor-area p:last-child")
	check("a press in the area", editorKeysLook(t, d), "area", closed)
	if complaints := d.evaluate(`JSON.stringify(window.__complaints)`); complaints != "[]" {
		t.Errorf("the page complained: %s", complaints)
	}
}

// Backspace at the start of a paragraph after a code block joins its words, marks gone, to the code's
// last line as it shows, by the keys as a person does it: the path the review of 21/10/2026 found (a
// code block made, Enter, y, Enter twice out of it, Backspace back in, Backspace taking the y: the
// browser's empty-line placeholder is the code's last line), where the browser's own join put the words
// before the code's last newline. A code block whose last line has words takes them after those words.
// CONTROL, against a copy of v2.0.0.js through -overlay (run 21/10/2026, fix in the scratchpad): with
// Backspace there left to the browser again (no joining), "Backspace at the start of đậm" failed:
// <pre><code>x := 1đậm sau\n</code></pre>, the words before the code's last newline.
func TestBrowserEditorBackspaceAfterACodeBlock(t *testing.T) {
	d := editorV2Page(t, `<p>một</p><p>x := 1</p><p><strong>đậm</strong> sau</p><pre><code>a := 1</code></pre><p>cuối</p>`)
	check := func(what, want string) {
		t.Helper()
		if got := editorKeysLook(t, d).Field; got != want {
			t.Errorf("%s:\n got  %q\n want %q", what, got, want)
		}
	}
	backspace := func() { editorReviewKey(d, "Backspace", "Backspace", 8, 0) }
	const after = "\n<pre><code>a := 1</code></pre>\n<p>cuối</p>"
	editorClick(d, ".editor-area p")
	editorCaretAt(d, "x := 1", -1)
	editorClick(d, `[data-kitwork-format="code-block"]`)
	editorPress(d, "Enter")
	editorType(d, "y")
	editorPress(d, "Enter")
	editorPress(d, "Enter")
	backspace()
	backspace()
	check("the y taken, the code's last line empty", "<p>một</p>\n<pre><code>x := 1\n\n</code></pre>\n<p><strong>đậm</strong> sau</p>"+after)
	editorCaretAt(d, "đậm", 0)
	backspace()
	check("Backspace at the start of đậm", "<p>một</p>\n<pre><code>x := 1\nđậm sau</code></pre>"+after)
	editorType(d, "!")
	check("the caret where the words joined", "<p>một</p>\n<pre><code>x := 1\n!đậm sau</code></pre>"+after)
	editorCaretAt(d, "cuối", 0)
	backspace()
	check("Backspace at the start of cuối", "<p>một</p>\n<pre><code>x := 1\n!đậm sau</code></pre>\n<pre><code>a := 1cuối</code></pre>")
	if complaints := d.evaluate(`JSON.stringify(window.__complaints)`); complaints != "[]" {
		t.Errorf("the page complained: %s", complaints)
	}
}
