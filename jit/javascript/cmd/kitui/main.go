// Command kitui serves the KitJS + jitcss component recipe catalog: a
// shadcn-style docs page where every entry is copy-paste HTML (Tailwind
// utilities + data-kit-* directives) rendered live above its own source.
//
// It uses the real engine surfaces end to end:
//
//   - jitcss.GenerateFramework + GenerateJIT scan the page and emit only the
//     Tailwind utilities actually used.
//
//   - jit/javascript ComposeStandalone seals only the KitJS components used.
//
//     go run -C engine ./jit/javascript/cmd/kitui -port 8091
package main

import (
	"flag"
	"html"
	"log"
	"net/http"
	"strings"

	jitcss "github.com/kitwork/engine/jit/css"
	"github.com/kitwork/engine/jit/javascript"
)

// behavioralFamilies is every KitJS component the recipes mount. The composer
// seals exactly these (copy also pulls the clipboard service).
var behavioralFamilies = []string{
	"accordion", "alert", "carousel", "collapse", "combobox", "command", "context-menu", "copy", "dialog", "dropzone",
	"drawer", "dropdown", "otp", "pagination", "popover", "rating", "shortcut",
	"rotator", "slider", "stepper", "switch", "tabs", "tags", "terminal", "toast", "tooltip",
}

func main() {
	port := flag.String("port", "8091", "TCP port to serve the catalog on")
	flag.Parse()

	composer, err := javascript.NewDefaultComposer()
	if err != nil {
		log.Fatalf("kitui: composer: %v", err)
	}
	refs := make([]javascript.ComponentRef, 0, len(behavioralFamilies))
	for _, name := range behavioralFamilies {
		refs = append(refs, javascript.ComponentRef{Name: name, Version: "1.0.0"})
	}
	bundle, err := composer.ComposeStandalone(refs, false)
	if err != nil {
		log.Fatalf("kitui: compose: %v", err)
	}

	page := buildPage()

	mux := http.NewServeMux()
	mux.HandleFunc("/kit.js", func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		response.Header().Set("Cache-Control", "no-store")
		_, _ = response.Write(bundle.JavaScript)
	})
	mux.HandleFunc("/", func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/" {
			http.NotFound(response, request)
			return
		}
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		response.Header().Set("Cache-Control", "no-store")
		_, _ = response.Write([]byte(page))
	})

	addr := "127.0.0.1:" + *port
	log.Printf("KitJS + jitcss catalog: http://localhost:%s  (%d components sealed, bundle %d bytes)",
		*port, len(behavioralFamilies), len(bundle.JavaScript))
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("kitui: serve: %v", err)
	}
}

// buildPage assembles the body from the recipe data, generates exactly the
// Tailwind utilities that body uses, and returns the whole document.
func buildPage() string {
	body := renderBody()

	// Scan the live body for utilities. GenerateJIT reads class="..." literals,
	// so only the rendered demos (not the escaped code blocks) contribute.
	utilities := jitcss.GenerateJIT(body, &jitcss.DefaultConfig)
	framework := jitcss.GenerateFramework()

	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="en"><head>`)
	b.WriteString(`<meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">`)
	b.WriteString(`<title>KitJS UI — Component Recipes</title>`)
	b.WriteString(`<style>`)
	b.WriteString(baseCSS)
	b.WriteString("\n/* --- jitcss framework --- */\n")
	b.WriteString(framework)
	b.WriteString("\n/* --- jitcss utilities (only what the page uses) --- */\n")
	b.WriteString(utilities)
	b.WriteString(`</style></head><body>`)
	b.WriteString(body)
	b.WriteString(copyScript)
	b.WriteString(`<script src="/kit.js"></script>`)
	b.WriteString(`</body></html>`)
	return b.String()
}

// renderBody builds the sidebar + every category section from the recipe data.
func renderBody() string {
	var nav strings.Builder
	var sections strings.Builder

	for _, group := range catalog {
		nav.WriteString(`<div class="mb-4"><p class="px-3 mb-1 text-[11px] font-semibold uppercase tracking-wider text-slate-400 dark:text-slate-500">` + html.EscapeString(group.category) + `</p>`)
		sections.WriteString(`<section class="mb-14"><h2 class="mb-1 text-lg font-bold text-slate-900 dark:text-white">` + html.EscapeString(group.category) + `</h2>`)
		sections.WriteString(`<p class="mb-5 text-sm text-slate-500 dark:text-slate-400">` + html.EscapeString(group.blurb) + `</p>`)
		sections.WriteString(`<div class="grid grid-cols-1 gap-5 lg:grid-cols-2">`)
		for _, item := range group.items {
			nav.WriteString(`<a href="#` + item.id + `" class="block rounded-md px-3 py-1.5 text-sm text-slate-600 hover:bg-slate-100 hover:text-slate-900 dark:text-slate-300 dark:hover:bg-slate-800 dark:hover:text-white">` + html.EscapeString(item.title) + `</a>`)
			sections.WriteString(renderCard(item))
		}
		sections.WriteString(`</div></section>`)
		nav.WriteString(`</div>`)
	}

	var b strings.Builder
	b.WriteString(`<div class="min-h-screen bg-slate-50 text-slate-900 dark:bg-slate-950 dark:text-slate-100">`)
	// header
	b.WriteString(`<header class="sticky top-0 z-30 border-b border-slate-200 bg-white/80 backdrop-blur dark:border-slate-800 dark:bg-slate-950/80">`)
	b.WriteString(`<div class="mx-auto flex max-w-6xl items-center justify-between px-6 py-3">`)
	b.WriteString(`<div class="flex items-center gap-3"><span class="text-base font-bold tracking-tight">Kit<span class="text-brand">JS</span> UI</span>`)
	b.WriteString(`<span class="rounded-full bg-brand/10 px-2 py-0.5 text-[11px] font-semibold text-brand">recipes</span></div>`)
	b.WriteString(`<p class="hidden text-xs text-slate-500 sm:block dark:text-slate-400">Tailwind (jitcss) + KitJS · copy-paste, no build step</p>`)
	b.WriteString(`</div></header>`)
	// layout: sidebar + main
	b.WriteString(`<div class="mx-auto flex max-w-6xl gap-8 px-6 py-8">`)
	b.WriteString(`<aside class="sticky top-16 hidden h-[calc(100vh-5rem)] w-56 shrink-0 overflow-y-auto lg:block">` + nav.String() + `</aside>`)
	b.WriteString(`<main class="min-w-0 flex-1">`)
	b.WriteString(`<div class="mb-10"><h1 class="text-2xl font-bold tracking-tight">Component recipes</h1>`)
	b.WriteString(`<p class="mt-2 max-w-2xl text-sm text-slate-500 dark:text-slate-400">Each recipe is plain semantic HTML styled with jitcss utilities and made interactive with <code class="rounded bg-slate-200 px-1 text-[12px] dark:bg-slate-800">data-kit-*</code>. Copy the markup — the engine ships only the utilities and KitJS components you actually use.</p></div>`)
	b.WriteString(sections.String())
	b.WriteString(`</main></div></div>`)
	return b.String()
}

// renderCard shows one recipe: live demo above its own escaped source, with a
// copy control. The demo markup is written once and reused as the code sample.
func renderCard(item recipe) string {
	var b strings.Builder
	b.WriteString(`<article id="` + item.id + `" class="scroll-mt-20 overflow-hidden rounded-xl border border-slate-200 bg-white dark:border-slate-800 dark:bg-slate-900">`)
	// head
	b.WriteString(`<div class="flex items-start justify-between gap-3 border-b border-slate-100 px-5 py-3 dark:border-slate-800">`)
	b.WriteString(`<div><h3 class="text-sm font-semibold text-slate-900 dark:text-white">` + html.EscapeString(item.title) + `<span class="ml-2 rounded bg-slate-100 px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide text-slate-500 dark:bg-slate-800 dark:text-slate-400">` + html.EscapeString(item.tag) + `</span></h3>`)
	b.WriteString(`<p class="mt-0.5 text-xs text-slate-500 dark:text-slate-400">` + html.EscapeString(item.desc) + `</p></div></div>`)
	// live demo
	b.WriteString(`<div class="flex min-h-[96px] items-center justify-center gap-3 p-6">` + item.demo + `</div>`)
	// code
	b.WriteString(`<details class="border-t border-slate-100 dark:border-slate-800">`)
	b.WriteString(`<summary class="flex cursor-pointer items-center justify-between px-5 py-2 text-xs font-medium text-slate-500 hover:text-slate-900 dark:hover:text-white">`)
	b.WriteString(`<span>Show markup</span>`)
	b.WriteString(`<button type="button" data-copy class="rounded-md border border-slate-200 px-2 py-1 text-[11px] hover:border-brand dark:border-slate-700"><span data-copy-label>Copy</span></button>`)
	b.WriteString(`</summary>`)
	b.WriteString(`<pre class="overflow-x-auto bg-slate-950 px-5 py-4 text-[12px] leading-relaxed text-slate-100"><code>` + html.EscapeString(strings.TrimSpace(item.demo)) + `</code></pre>`)
	b.WriteString(`</details></article>`)
	return b.String()
}

// baseCSS is the small non-utility base: font, smooth scroll, and code wrapping
// that Tailwind utilities do not cover cleanly for this docs shell.
const baseCSS = `
*{box-sizing:border-box}
html{scroll-behavior:smooth}
body{margin:0;font-family:ui-sans-serif,system-ui,-apple-system,"Segoe UI",Roboto,sans-serif}
pre{margin:0;font-family:ui-monospace,"SF Mono","JetBrains Mono",Menlo,Consolas,monospace}
summary::-webkit-details-marker{display:none}
`

// copyScript wires every [data-copy] button to copy its card's <pre> text. This
// is docs tooling, deliberately separate from the KitJS copy component (which is
// itself one of the recipes below).
const copyScript = `<script>
document.addEventListener("click", function (event) {
  var button = event.target.closest("[data-copy]");
  if (!button) return;
  var article = button.closest("article");
  var pre = article && article.querySelector("pre");
  if (!pre || !navigator.clipboard) return;
  navigator.clipboard.writeText(pre.innerText).then(function () {
    var label = button.querySelector("[data-copy-label]") || button;
    var previous = label.textContent;
    label.textContent = "Copied";
    setTimeout(function () { label.textContent = previous; }, 1200);
  }, function () {});
});
</script>`
