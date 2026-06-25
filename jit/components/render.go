// render.go adds the JIT UI-component layer to package components — a sibling of jit/css, jit/icons
// and jit/js. Render scans the page for Kitwork component classes (.button/.btn, .card, …) and
// injects a <style data-kitwork-jit="components"> with CSS for ONLY the families the page uses.
//
// Naming follows the project rule "no abbreviations": the FULL word is canonical (`.button`,
// `.button-brand`, `.button-small`) and a short alias (`.btn`, `.btn-brand`, `.btn-sm`) shares the
// same rule so users coming from Tailwind/Bootstrap aren't lost.
//
// (The GenerateButtons/Cards/… helpers in components.go are an older all-at-once experiment that
// emits every variant; this file is the JIT, only-used path. They share only the package name.)
package components

import (
	"regexp"
	"strings"
)

const componentMarker = `data-kitwork-jit="components"`

// family is a component family: it is emitted only when the page uses a class whose token equals or
// starts with one of its bases (e.g. base "button" matches `button` and `button-brand`).
type family struct {
	bases []string
	css   string
}

var families = []family{
	{[]string{"button", "btn"}, buttonCSS},
	{[]string{"card"}, cardCSS},
	{[]string{"prose"}, proseCSS},
	{[]string{"badge"}, badgeCSS},
	{[]string{"alert"}, alertCSS},
	{[]string{"input", "textarea", "select"}, inputCSS},
	{[]string{"table"}, tableCSS},
}

var componentClassRe = regexp.MustCompile(`class="([^"]*)"`)

// usedBases returns the set of distinct class tokens used in class="…" attributes.
func usedTokens(html string) map[string]bool {
	out := make(map[string]bool)
	for _, m := range componentClassRe.FindAllStringSubmatch(html, -1) {
		for _, tok := range strings.Fields(m[1]) {
			out[tok] = true
		}
	}
	return out
}

// triggered reports whether any used token equals a base or starts with "<base>-".
func triggered(used map[string]bool, bases []string) bool {
	for tok := range used {
		for _, base := range bases {
			if tok == base || strings.HasPrefix(tok, base+"-") {
				return true
			}
		}
	}
	return false
}

// Render injects a <style data-kitwork-jit="components"> with CSS for only the component families
// the page uses, before </head>. A cheap no-op when no component classes are present.
func Render(html string) string {
	used := usedTokens(html)
	var b strings.Builder
	for _, f := range families {
		if triggered(used, f.bases) {
			b.WriteString(f.css)
		}
	}
	if b.Len() == 0 {
		return html
	}
	style := "<style " + componentMarker + ">" + b.String() + "</style>"
	if i := strings.LastIndex(html, "</head>"); i >= 0 {
		return html[:i] + style + html[i:]
	}
	return style + html
}

// buttonCSS — full-word `.button*` canonical, `.btn*` alias on the same rules.
const buttonCSS = `.button,.btn{display:inline-flex;align-items:center;justify-content:center;gap:.5rem;` +
	`border:1px solid transparent;border-radius:.5rem;font-weight:600;font-family:inherit;cursor:pointer;` +
	`transition:all .2s cubic-bezier(.4,0,.2,1);text-decoration:none;user-select:none;padding:.5rem 1rem;` +
	`font-size:.875rem;line-height:1.25rem}` +
	`.button:active,.btn:active{transform:translateY(1px)}` +
	`.button:disabled,.btn:disabled{opacity:.5;cursor:not-allowed;pointer-events:none}` +
	`.button-small,.btn-sm{padding:.25rem .75rem;font-size:.75rem}` +
	`.button-large,.btn-lg{padding:.75rem 1.5rem;font-size:1rem}` +
	`.button-brand,.btn-brand{background:#f82244;color:#fff}` +
	`.button-brand:hover,.btn-brand:hover{box-shadow:0 4px 16px rgba(248,34,68,.35)}` +
	`.button-outline,.btn-outline{background:transparent;border-color:currentColor}` +
	`.button-ghost,.btn-ghost{background:transparent}` +
	`.button-ghost:hover,.btn-ghost:hover{background:rgba(127,127,127,.1)}`

// cardCSS — `.card` container + header/body/footer + media/title/text/meta pieces + `.card-hover`.
// Theme-aware via --kitwork-* custom properties (with light fallbacks for tenants that don't set them).
const cardCSS = `.card{display:flex;flex-direction:column;background:var(--kitwork-surface,#fff);` +
	`border:1px solid var(--kitwork-border,rgba(0,0,0,.08));border-radius:.75rem;overflow:hidden}` +
	`.card-header{padding:1.25rem 1.5rem;border-bottom:1px solid var(--kitwork-border,rgba(0,0,0,.06))}` +
	`.card-body{padding:1.25rem 1.4rem 1.4rem;flex:1}` +
	`.card-footer{padding:1rem 1.5rem;border-top:1px solid var(--kitwork-border,rgba(0,0,0,.06))}` +
	`.card-media{display:block;aspect-ratio:16/9;overflow:hidden;background:rgba(127,127,127,.08)}` +
	`.card-media img{width:100%;height:100%;object-fit:cover}` +
	`.card-title{display:block;font-weight:800;font-size:1.05rem;line-height:1.35;color:var(--kitwork-text-hi,#0f172a)}` +
	`.card-text{margin-top:.4rem;font-size:.85rem;line-height:1.55;color:var(--kitwork-text-lo,#64748b);` +
	`overflow:hidden;display:-webkit-box;-webkit-line-clamp:2;-webkit-box-orient:vertical}` +
	`.card-meta{display:block;margin-top:.6rem;font-size:.72rem;color:var(--kitwork-text-muted,#94a3b8)}` +
	`.card-hover{transition:transform .2s ease,box-shadow .2s ease}` +
	`.card-hover:hover{transform:translateY(-3px);box-shadow:0 16px 40px -16px rgba(0,0,0,.25)}`

// proseCSS — readable long-form typography for raw/CMS HTML (articles, docs, blog). `.prose` for the
// container; the `.prose-frame` modifier crops in-article images to a uniform 16:9.
const proseCSS = `.prose{font-size:1.1rem;line-height:1.78;color:var(--kitwork-text,#334155)}` +
	`.prose h2{font-weight:800;font-size:1.7rem;line-height:1.25;margin:2.75rem 0 1rem;color:var(--kitwork-text-hi,#0f172a)}` +
	`.prose h3{font-weight:700;font-size:1.35rem;margin:2.25rem 0 .75rem;color:var(--kitwork-text-hi,#0f172a)}` +
	`.prose p{margin:1.2rem 0}` +
	`.prose a{color:var(--kitwork-brand,#f82244);text-decoration:underline;text-underline-offset:2px}` +
	`.prose blockquote{margin:1.9rem 0;padding:.5rem 0 .5rem 1.5rem;border-left:3px solid var(--kitwork-brand,#f82244);font-style:italic;color:var(--kitwork-text-lo,#64748b)}` +
	`.prose ul,.prose ol{margin:1.2rem 0;padding-left:1.5rem;list-style:revert}` +
	`.prose li{margin:.45rem 0}` +
	`.prose img{max-width:100%;height:auto;border-radius:.75rem;margin:1.9rem 0}` +
	`.prose figure{margin:1.9rem 0}` +
	`.prose figcaption{font-size:.8rem;text-align:center;color:var(--kitwork-text-muted,#94a3b8);margin-top:.5rem}` +
	`.prose pre{overflow-x:auto;padding:1.1rem 1.3rem;border-radius:.7rem;background:#0d1117;color:#e6edf3;` +
	`border:1px solid rgba(255,255,255,.08);font-size:.86rem;line-height:1.65;margin:1.9rem 0}` +
	`.prose pre code{background:none;padding:0;color:inherit;font-size:inherit}` +
	`.prose code{font-family:'Fira Code',ui-monospace,SFMono-Regular,Menlo,monospace;font-size:.88em;` +
	`background:rgba(127,127,127,.16);padding:.15em .4em;border-radius:.3em}` +
	`.prose-frame img{aspect-ratio:16/9;object-fit:cover}`

// badgeCSS — small pill labels. `.badge` + colour variants (brand/success/warning/danger/neutral)
// + `.badge-dot` (a leading status dot in currentColor).
const badgeCSS = `.badge{display:inline-flex;align-items:center;gap:.35rem;padding:.15rem .55rem;` +
	`border-radius:9999px;font-size:.7rem;font-weight:700;text-transform:uppercase;letter-spacing:.04em;` +
	`line-height:1.4;white-space:nowrap;border:1px solid transparent}` +
	`.badge-dot{width:.4rem;height:.4rem;border-radius:9999px;background:currentColor}` +
	`.badge-brand{background:rgba(248,34,68,.12);color:#f82244;border-color:rgba(248,34,68,.25)}` +
	`.badge-success{background:rgba(16,185,129,.12);color:#059669;border-color:rgba(16,185,129,.25)}` +
	`.badge-warning{background:rgba(245,158,11,.14);color:#b45309;border-color:rgba(245,158,11,.3)}` +
	`.badge-danger{background:rgba(239,68,68,.12);color:#dc2626;border-color:rgba(239,68,68,.25)}` +
	`.badge-neutral{background:rgba(127,127,127,.14);color:var(--kitwork-text-lo,#64748b);border-color:rgba(127,127,127,.2)}`

// alertCSS — callout boxes. `.alert` + info/success/warning/danger.
const alertCSS = `.alert{display:flex;gap:.75rem;padding:.9rem 1.1rem;border-radius:.6rem;` +
	`border:1px solid transparent;font-size:.9rem;line-height:1.55}` +
	`.alert-info{background:rgba(59,130,246,.08);border-color:rgba(59,130,246,.25);color:#1d4ed8}` +
	`.alert-success{background:rgba(16,185,129,.08);border-color:rgba(16,185,129,.25);color:#047857}` +
	`.alert-warning{background:rgba(245,158,11,.1);border-color:rgba(245,158,11,.3);color:#b45309}` +
	`.alert-danger{background:rgba(239,68,68,.08);border-color:rgba(239,68,68,.25);color:#b91c1c}`

// inputCSS — form fields. `.input` / `.textarea` / `.select` + `.input-small` / `.input-large`.
const inputCSS = `.input,.textarea,.select{width:100%;padding:.6rem .85rem;border-radius:.5rem;` +
	`border:1px solid var(--kitwork-border,rgba(0,0,0,.12));background:var(--kitwork-surface,#fff);` +
	`color:var(--kitwork-text-hi,#0f172a);font-family:inherit;font-size:.9rem;line-height:1.4;` +
	`transition:border-color .15s ease,box-shadow .15s ease}` +
	`.input:focus,.textarea:focus,.select:focus{outline:none;border-color:var(--kitwork-brand,#f82244);` +
	`box-shadow:0 0 0 3px rgba(248,34,68,.12)}` +
	`.input::placeholder,.textarea::placeholder{color:var(--kitwork-text-muted,#94a3b8)}` +
	`.input-small{padding:.4rem .65rem;font-size:.8rem}.input-large{padding:.8rem 1.1rem;font-size:1rem}`

// tableCSS — data tables. `.table` + `.table-zebra` modifier; row hover. (A page using Tailwind's
// `table-auto` only triggers an unused rule — harmless.)
const tableCSS = `.table{width:100%;border-collapse:collapse;font-size:.9rem;text-align:left}` +
	`.table th,.table td{padding:.7rem 1rem;border-bottom:1px solid var(--kitwork-border,rgba(0,0,0,.08))}` +
	`.table th{font-weight:700;font-size:.72rem;text-transform:uppercase;letter-spacing:.05em;` +
	`color:var(--kitwork-text-muted,#94a3b8)}` +
	`.table tbody tr:hover{background:rgba(127,127,127,.05)}` +
	`.table-zebra tbody tr:nth-child(even){background:rgba(127,127,127,.04)}`
