package work

// router.bundle (14/10/2026): how a Kitwork UI page delivers its CSS and its JavaScript.
//
//	router.bundle("auto");                                  // the default: nobody needs to write it
//	router.bundle("inline");                                // every part in the HTML
//	router.bundle("split");                                 // the site's part one file, the page's part inline
//	router.bundle(false);                                   // one file per part, the delivery before 14/10
//	router.bundle({ css: "inline", javascript: "split" });  // each part its own way
//
// A page's CSS has a part the whole site shares, the site part (reboot, the route's colours, the fonts'
// @font-face), and a part of its own, the page part (its rules, its icons, a logo). Until 14/10 each was
// a file of its own, five render-blocking requests before the first paint (Lighthouse on the kitwork
// example: 290–520 ms lost). Its JavaScript is the kernel, Drive, scrolled and one file per component,
// directive and service, every one deferred: none blocks the first paint (TBT 0).
//
//	          CSS                                           JavaScript
//	"auto"    page part inline; site part inline when the   one deferred file per part (as before)
//	          page with all its CSS fits the first window
//	          (initialWindowBytes), else site.<hash>.css
//	"inline"  everything in <style>                         every part but the kernel inline
//	"split"   site.<hash>.css + page part inline            one deferred bundle.<hash>.js per set of parts
//	false     one file per role (reboot, colors, fonts,     one deferred file per part
//	          icons, logo, page)
//
// Why CSS moves and JavaScript stays: a stylesheet blocks the first paint until it arrives, a deferred
// script never does. A file per JavaScript part is shared by every page of the site in the browser's
// cache and fits Drive, which loads only the components a page adds; a bundle is one file per set, so
// each new set downloads the parts already held again. The kernel is the same file on every page of
// every site: it is always a file of its own.
//
// The nearest folder that names a part decides it, as router.transition is inherited: a string names
// both parts, an object only the ones it writes. A legacy .kitwork.html page reads none of this: its
// JIT CSS is already inline (one <style> per pass) and its JavaScript one deferred file per part.

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	jitjs "github.com/kitwork/engine/jit/js"
	"github.com/kitwork/engine/render"
	"github.com/kitwork/engine/ui/stylesheet"
	"github.com/kitwork/engine/value"
)

// The ways router.bundle delivers a part.
const (
	bundleAuto   = "auto"
	bundleInline = "inline"
	bundleSplit  = "split"
	bundleFiles  = "false" // router.bundle(false): one file per part
)

// bundleChoice is how a route's .ui pages deliver each part: one of the bundle words; "" in a folder's
// own router.bundle for a part it does not name.
type bundleChoice struct {
	css, javascript string
}

// initialWindowBytes is what "auto" lets a page weigh, gzipped, with all its CSS inline: what a
// server sends in the first round trip of a new connection. A new TCP connection starts with ten
// segments in flight (RFC 6928, every current OS) of at most 1460 bytes each on Ethernet (the 1500-byte
// MTU less 40 bytes of IP and TCP headers), 14 600 bytes; the HTTP status line and headers (a few
// hundred bytes) ride in the same window, so 14 KB is the page. A page that fits arrives whole after one
// round trip and paints at once; a larger one waits for a second round trip anyway, so its site part
// stays one file the browser keeps across pages (site.<hash>.css) and only the page part is inline.
// Compared gzipped (gzip.DefaultCompression, the level utilities/compress sends) because that is what
// travels.
const initialWindowBytes = 14 * 1024

// Bundle declares how the .ui pages of this folder and the folders below deliver their CSS and their
// JavaScript (the table above). A wrong value stops the boot with the values and the nearest one.
func (f *FolderRouter) Bundle(args ...value.Value) (*FolderRouter, error) {
	if len(args) != 1 {
		return f, fmt.Errorf(`router.bundle nhận một giá trị: "auto" (mặc định), "inline", "split", false, hoặc { css: …, javascript: … }`)
	}
	arg := args[0]
	var choice bundleChoice
	if arg.IsMap() {
		fields := arg.Map()
		if len(fields) == 0 {
			return f, fmt.Errorf(`router.bundle({}): nêu ít nhất một phần, css hay javascript: router.bundle({ css: "inline" })`)
		}
		names := make([]string, 0, len(fields))
		for name := range fields {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if name != "css" && name != "javascript" {
				return f, fmt.Errorf(`router.bundle({ %s: … }): không có phần "%s"; router.bundle nhận css và javascript. Có phải ý là %s?`, name, name, nearestBundlePart(name))
			}
			word, err := bundleWord(fields[name], "{ "+name+": %s }")
			if err != nil {
				return f, err
			}
			if name == "css" {
				choice.css = word
			} else {
				choice.javascript = word
			}
		}
	} else {
		word, err := bundleWord(arg, "%s")
		if err != nil {
			return f, err
		}
		choice = bundleChoice{css: word, javascript: word}
	}
	if f != nil {
		if choice.css != "" {
			f.bundle.css = choice.css
		}
		if choice.javascript != "" {
			f.bundle.javascript = choice.javascript
		}
	}
	return f, nil
}

// bundleWord reads one way of delivering: "auto", "inline", "split" or false. shape is how the message
// writes the call around the value ("%s" for router.bundle(x), "{ css: %s }" inside an object).
func bundleWord(v value.Value, shape string) (string, error) {
	call := func(written string) string { return "router.bundle(" + fmt.Sprintf(shape, written) + ")" }
	suggest := func(word string) string { return call(`"` + word + `"`) }
	const ways = `các cách là "auto" (mặc định: CSS theo cỡ trang, JavaScript mỗi phần một file), "inline" (mọi phần trong HTML), "split" (phần chung của site một file, phần riêng của trang trong HTML) và false (mỗi phần một file như trước 14/10/2026)`
	switch {
	case v.IsString():
		switch word := v.String(); word {
		case bundleAuto, bundleInline, bundleSplit:
			return word, nil
		default:
			quoted, _ := json.Marshal(word)
			return "", fmt.Errorf(`%s: không có cách gói "%s"; %s. Có phải ý là %s?`, call(string(quoted)), word, ways, suggest(nearestBundleWord(word)))
		}
	case v.K == value.Bool && v.N == 0:
		return bundleFiles, nil
	case v.K == value.Bool:
		return "", fmt.Errorf(`%s: true không phải một cách gói; gói theo cỡ trang là "auto", cũng là mặc định: viết %s hay bỏ dòng này (false là mỗi phần một file như trước)`, call("true"), suggest(bundleAuto))
	}
	return "", fmt.Errorf(`%s: cách gói là một chữ hay false; %s. Có phải ý là %s?`, call(v.Text()), ways, suggest(bundleAuto))
}

// nearestBundleWord is the bundle word a mistyped one was most likely meant to be: the one at most two
// edits away ("inlin", "Split", "splt"), else "auto", the default ("one", "all", "bundle").
func nearestBundleWord(word string) string {
	best, distance := bundleAuto, 3
	for _, candidate := range []string{bundleAuto, bundleInline, bundleSplit} {
		if d := bundleEditDistance(strings.ToLower(word), candidate); d < distance {
			best, distance = candidate, d
		}
	}
	return best
}

// nearestBundlePart is the part a key that is not css or javascript was most likely meant to be: the
// shorthands of JavaScript and of a stylesheet, else the nearer of the two.
func nearestBundlePart(name string) string {
	switch strings.ToLower(name) {
	case "js", "script", "scripts", "java", "javascripts":
		return "javascript"
	case "style", "styles", "stylesheet", "stylesheets", "sheet":
		return "css"
	}
	if bundleEditDistance(strings.ToLower(name), "css") <= bundleEditDistance(strings.ToLower(name), "javascript") {
		return "css"
	}
	return "javascript"
}

// bundleEditDistance is the Levenshtein distance of two short words.
func bundleEditDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	previous := make([]int, len(rb)+1)
	current := make([]int, len(rb)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		current[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
		}
		previous, current = current, previous
	}
	return previous[len(rb)]
}

// routeBundle is how a route's .ui pages deliver each part: per part, the router.bundle of the nearest
// folder from the route up to the site root that names it; "auto" when none does.
func routeBundle(node *RouteNode) bundleChoice {
	var choice bundleChoice
	for n := node; n != nil && (choice.css == "" || choice.javascript == ""); n = n.parent {
		if n.folder == nil {
			continue
		}
		if choice.css == "" {
			choice.css = n.folder.bundle.css
		}
		if choice.javascript == "" {
			choice.javascript = n.folder.bundle.javascript
		}
	}
	if choice.css == "" {
		choice.css = bundleAuto
	}
	if choice.javascript == "" {
		choice.javascript = bundleAuto
	}
	return choice
}

// cssPart is one stylesheet of a page: its role (the first part of its file's name), the
// data-kitwork-generated value of its link or block, and its CSS.
type cssPart struct {
	role, generated string
	css             []byte
}

// inlineStyle is one part of a page's CSS written in the page: a <style> block marked with the role's
// data-kitwork-generated value, so Drive trades it on a move as it trades a link. "</style" inside the
// CSS (a string can spell it) is written "<\/style", which CSS reads as the same characters and the
// HTML parser does not read as the end of the block.
func inlineStyle(generated string, css []byte) string {
	if len(css) == 0 {
		return ""
	}
	return `<style data-kitwork-generated="` + generated + `">` + escapeEndTag(string(css), "</style") + `</style>`
}

// escapeEndTag writes every "</tag" of text (any case) as "<\/tag": the same characters to CSS and to
// JavaScript, never the end of the element to the HTML parser.
func escapeEndTag(text, tag string) string {
	lower := strings.ToLower(text)
	if !strings.Contains(lower, tag) {
		return text
	}
	var b strings.Builder
	from := 0
	for {
		i := strings.Index(lower[from:], tag)
		if i < 0 {
			b.WriteString(text[from:])
			return b.String()
		}
		b.WriteString(text[from : from+i])
		b.WriteString(`<\/`)
		b.WriteString(text[from+i+2 : from+i+len(tag)])
		from += i + len(tag)
	}
}

// escapeScriptText writes JavaScript so that it stays inside its <script>: "</script" as "<\/script"
// (escapeEndTag) and "<!--" as "<\!--", which keeps the HTML parser out of the state where even
// "</script>" no longer ends the element. Both read the same to JavaScript in a string or a comment,
// where an engine file or a site's component could spell them.
func escapeScriptText(text string) string {
	return strings.ReplaceAll(escapeEndTag(text, "</script"), "<!--", `<\!--`)
}

// joinCSS is the CSS of several parts as one stylesheet, in their order: the site part of a page
// (reboot, colours, fonts), one file (site.<hash>.css) or one block.
func joinCSS(parts []cssPart) []byte {
	var b bytes.Buffer
	for _, part := range parts {
		if len(part.css) == 0 {
			continue
		}
		if b.Len() > 0 && b.Bytes()[b.Len()-1] != '\n' {
			b.WriteByte('\n')
		}
		b.Write(part.css)
	}
	return b.Bytes()
}

// gzipSize is how many bytes text weighs gzipped at the level the engine sends (utilities/compress).
func gzipSize(text string) int {
	var out bytes.Buffer
	writer, _ := gzip.NewWriterLevel(&out, gzip.DefaultCompression)
	_, _ = writer.Write([]byte(text))
	_ = writer.Close()
	return out.Len()
}

// fitsInitialWindow reports whether a page, with head as its head (every part of its CSS inline),
// weighs no more than initialWindowBytes gzipped: the document respondUI writes for it without data
// (weighedDocument). A request's data and its own title add to it, which a compile cannot know.
func (u *uiSite) fitsInitialWindow(compiled *uiCompiled, head string) bool {
	return gzipSize(u.weighedDocument(compiled, head)) <= initialWindowBytes
}

// weighedDocument is the document respondUI writes for a page with head as its head and no data: the
// route's path, what every response of the route writes (routeStyle.document: its title, description,
// language, transition, appearance, favicon) and the site's origin (absolute URLs of structured data).
// Until the fix of 14/10/2026 "auto" weighed the path, head and language only, a few hundred bytes gzipped short
// of what travels.
func (u *uiSite) weighedDocument(compiled *uiCompiled, head string) string {
	request := compiled.style.document
	request.Path, request.Origin, request.Head = compiled.route, u.origin, head
	return compiled.page.Document(request)
}

// pageCSS writes the CSS links and blocks of a page's head by its router.bundle (css): site are the
// site part's files (reboot, colors, fonts), own its icons file (nil when it shows none), logos and
// its own (page) the rest of the page part. The icons go in iconsSlot, so a response whose data named
// icons of its own can carry another set there; iconsInline says whether that set is a block. fits
// answers "auto": whether the page, its head carrying these links and blocks (icons in place), fits
// the first window (fitsInitialWindow).
func (u *uiSite) pageCSS(mode string, site []cssPart, own *cssPart, logos []cssPart, page cssPart, fits func(links string) bool) (links, icons string, iconsInline bool) {
	link := func(part cssPart) string {
		if href := u.store(part.role, part.css); href != "" {
			return render.Link(href, part.generated)
		}
		return ""
	}
	pagePart := func(write func(cssPart) string) string {
		var b strings.Builder
		b.WriteString(iconsSlot)
		for _, logo := range logos {
			b.WriteString(write(logo))
		}
		b.WriteString(write(page))
		return b.String()
	}
	inline := func(part cssPart) string { return inlineStyle(part.generated, part.css) }
	if mode == bundleFiles {
		var b strings.Builder
		for _, part := range site {
			b.WriteString(link(part))
		}
		if own != nil {
			icons = link(*own)
		}
		return b.String() + pagePart(link), icons, false
	}
	if own != nil {
		icons = inline(*own)
	}
	siteCSS := joinCSS(site)
	inlineSite := inlineStyle("site", siteCSS)
	switch mode {
	case bundleInline:
		return inlineSite + pagePart(inline), icons, true
	case bundleAuto:
		if whole := inlineSite + pagePart(inline); fits(strings.Replace(whole, iconsSlot, icons, 1)) {
			return whole, icons, true
		}
	}
	return link(cssPart{role: "site", generated: "site", css: siteCSS}) + pagePart(inline), icons, true
}

// scriptPart is one JavaScript file of a page that router.bundle may deliver another way: its URL (the
// key a bundle and an inline part run it once by), its role and its bytes.
type scriptPart struct {
	path, role string
	body       []byte
}

// partsKey is where a page keeps the parts it has run: window.kitworkParts, by each part's URL. A part
// in a bundle or inline runs once per page whatever brings it again (the bundle of the next page's set,
// the next page's inline copy that Drive runs), as a file Drive has loaded is never loaded again.
const partsKey = "kitworkParts"

// runOnce wraps a part's JavaScript so it runs once per page, keyed by its URL. Every part the engine
// delivers is already a function scope of its own, so wrapping it changes nothing else; the line break
// before the end keeps a part whose last line is a // comment from swallowing it.
func runOnce(part scriptPart) string {
	key, _ := json.Marshal(part.path)
	return ";(function(parts){if(parts[" + string(key) + "])return;parts[" + string(key) + "]=1;\n" +
		string(part.body) + "\n})(window." + partsKey + "||(window." + partsKey + "={}));\n"
}

// bundleRole is the role of a page's JavaScript bundle (router.bundle javascript "split"):
// /_kitwork/bundle.<hash>.js, stored with the generation like the warnings files.
const bundleRole = "bundle"

// pageScripts rewrites a built head's JavaScript by the route's router.bundle (javascript): the
// parts after the kernel (Drive, directives, services, components), and scrolled when the page links
// it (scrolledTag, which stands among the stylesheets). "auto" and false leave the files as they are.
// "split" makes the parts one deferred bundle.<hash>.js where the first of them stood (two or more
// parts: one alone stays its own file); "inline" writes each part in the page, where the first stood,
// as a deferred module script (data-kitwork-part names the file it stands for, Drive runs the ones a
// move brings). The kernel is always its own file. It rewrites built (its Stylesheet place follows
// what moved before it) and returns what replaces scrolledTag.
func (u *uiSite) pageScripts(mode string, built *render.PageHead, scrolledTag string) string {
	if mode != bundleSplit && mode != bundleInline {
		return scrolledTag
	}
	head, assets := built.Head, built.Scripts
	var parts []scriptPart
	if scrolledTag != "" {
		script := scrolledScript()
		parts = append(parts, scriptPart{path: script.path, role: "scrolled", body: script.body})
	}
	for _, asset := range assets {
		if asset.Role != "runtime" {
			parts = append(parts, scriptPart{path: asset.Path, role: asset.Role, body: asset.Body})
		}
	}
	if len(parts) == 0 || mode == bundleSplit && len(parts) < 2 {
		return scrolledTag
	}
	// take the parts' tags out of the head, remembering where the first stood
	at := -1
	for _, asset := range assets {
		if asset.Role == "runtime" {
			continue
		}
		before := len(head)
		var place int
		if head, place = removeScriptTag(head, asset.Path); place >= 0 {
			if place < built.Stylesheet {
				built.Stylesheet -= before - len(head)
			}
			if at < 0 || place < at {
				at = place
			}
		}
	}
	if at < 0 {
		at = len(head) // only scrolled: after the head the passes wrote
	}
	var tags strings.Builder
	if mode == bundleSplit {
		var body strings.Builder
		paths := make([]string, 0, len(parts))
		for _, part := range parts {
			body.WriteString(runOnce(part))
			paths = append(paths, part.path)
		}
		// data-kitwork-parts names the files the bundle stands for: what a name a later generation did not
		// make loads instead (staleBundle)
		tags.WriteString(`<script data-kitwork-generated="` + bundleRole + `" src="` + u.storeScript(bundleRole, []byte(body.String())) +
			`" data-kitwork-parts="` + strings.Join(paths, " ") + `" defer></script>`)
	} else {
		for _, part := range parts {
			// a module script: deferred like the files it stands for, so it runs after the kernel file
			tags.WriteString(`<script type="module" data-kitwork-generated="` + part.role + `" data-kitwork-part="` + part.path + `">` +
				escapeScriptText(runOnce(part)) + `</script>`)
		}
	}
	if at < built.Stylesheet {
		built.Stylesheet += tags.Len()
	}
	built.Head = head[:at] + tags.String() + head[at:]
	return ""
}

// removeScriptTag takes the <script …src=path…></script> of one file out of a head (quoted or not: the
// minifier drops the quotes it can) and returns where it stood, -1 when the head does not carry it.
func removeScriptTag(head, path string) (string, int) {
	i := strings.Index(head, path)
	if i < 0 {
		return head, -1
	}
	open := strings.LastIndex(head[:i], "<script")
	end := strings.Index(head[i:], "</script>")
	if open < 0 || end < 0 {
		return head, -1
	}
	end += i + len("</script>")
	return head[:open] + head[end:], open
}

// storeScript keeps one script of the generation under <role>.<hash> (the warnings files, the
// bundles) and returns its URL.
func (u *uiSite) storeScript(role string, body []byte) string {
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	name := role + "." + digest[:pageStylesheetHash]
	script := engineScript{path: jitjs.AssetPrefix + name + ".js", body: body, digest: digest}
	u.stylesheetsMu.Lock()
	u.scripts[name] = script
	u.stylesheetsMu.Unlock()
	return script.path
}

// staleBundle is the answer to a bundle name the generation did not make (bundle.<16 hex>.js): the name
// a page compiled before a deploy wrote, whose HTML a browser still holds (kept, served by the
// generation before, or fetched ahead by Drive's hover). The bundle's tag names the files of the parts
// it stood for (data-kitwork-parts, their URLs then), and this script, run as that tag, loads each as a
// file of its own, in order, unless the page has run it (window.kitworkParts) or already links it. The
// engine answers every such URL by its name with today's file, marked no-cache (jitjs.AssetAt,
// scrolledScriptAt), as it answers any older name of a part: the page keeps its components and Drive,
// a moment later than a whole load would run them (a component registered after boot paints on its
// own). It is served no-cache too: the same name may be a bundle again after the next deploy.
var staleBundle = sync.OnceValue(func() engineScript {
	body := []byte(`(function(s){var p=s&&s.getAttribute("data-kitwork-parts"),r=window.` + partsKey + `||{};if(p)p.split(" ").forEach(function(f){` +
		`if(r[f]||document.querySelector('script[src="'+f+'"]'))return;var e=document.createElement("script");e.src=f;e.async=false;document.head.appendChild(e)})})(document.currentScript);` + "\n")
	sum := sha256.Sum256(body)
	return engineScript{body: body, digest: hex.EncodeToString(sum[:])}
})

// generationScriptName is the <role>.<hash> a request path names when it is the spelling of a script
// the generation stores (warnings while developing, a page's JavaScript bundle):
// /_kitwork/<role>.<16 hex>.js; false for anything else. Whether that name is a file is the
// generation's store's to say.
func generationScriptName(p string) (string, bool) {
	for _, role := range []string{warningsRole, bundleRole} {
		rest, ok := strings.CutPrefix(p, jitjs.AssetPrefix+role+".")
		hash, isScript := strings.CutSuffix(rest, ".js")
		if !ok || !isScript || len(hash) != pageStylesheetHash {
			continue
		}
		valid := true
		for _, c := range hash {
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
				valid = false
			}
		}
		if valid {
			return role + "." + hash, true
		}
	}
	return "", false
}

// siteCSSParts is the site part of a page's CSS: reboot, the route's colours, its fonts.
func siteCSSParts(style routeStyle, fonts []byte) []cssPart {
	return []cssPart{
		{role: "reboot", generated: "reboot", css: stylesheet.Reboot().CSS},
		{role: "colors", generated: "colors", css: stylesheet.Colors(style.palette).CSS},
		{role: "fonts", generated: "fonts", css: fonts},
	}
}
