// render.go adds the JIT Material layer to package material — a sibling of jit/css, jit/icons
// and jit/js. Render scans the page for Kitwork material classes (.button/.btn, .card, …) and
// injects a <style data-kitwork-jit="material"> with CSS for ONLY the families the page uses.
package material

import (
	"regexp"
	"strings"
)

const materialMarker = `data-kitwork-jit="material"`

// family is a material family: it is emitted only when the page uses a class whose token equals or
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
	{[]string{"avatar"}, avatarCSS},
	{[]string{"dialog", "modal"}, dialogCSS},
	{[]string{"skeleton"}, skeletonCSS},
	{[]string{"tooltip"}, tooltipCSS},
	{[]string{"stat"}, statCSS},
	{[]string{"navbar"}, navbarCSS},
	{[]string{"timeline"}, timelineCSS},
}

var materialClassRe = regexp.MustCompile(`class="([^"]*)"`)

// usedTokens returns the set of distinct class tokens used in class="…" attributes.
func usedTokens(html string) map[string]bool {
	out := make(map[string]bool)
	for _, m := range materialClassRe.FindAllStringSubmatch(html, -1) {
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

// Render scans html for material classes and injects ONE inline
// <style data-kitwork-jit="material"> before </head> holding rules for ONLY the used families.
// It is a fast no-op if no material classes are used or if a style with materialMarker is already present.
func Render(html string) string {
	if html == "" || strings.Contains(html, materialMarker) {
		return html
	}

	used := usedTokens(html)
	if len(used) == 0 {
		return html
	}

	var sb strings.Builder
	for _, fam := range families {
		if triggered(used, fam.bases) {
			sb.WriteString(fam.css)
		}
	}

	css := sb.String()
	if css == "" {
		return html
	}

	style := "<style " + materialMarker + ">" + css + "</style>"
	if idx := strings.Index(html, "</head>"); idx >= 0 {
		return html[:idx] + style + html[idx:]
	}
	if idx := strings.Index(html, "<body"); idx >= 0 {
		return html[:idx] + style + html[idx:]
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
	`.button-brand,.btn-brand{background:var(--kitwork-brand,#f82244);color:#fff}` +
	`.button-brand:hover,.btn-brand:hover{background:#e01d3c}` +
	`.button-outline,.btn-outline{background:transparent;border-color:currentColor}` +
	`.button-ghost,.btn-ghost{background:transparent}` +
	`.button-ghost:hover,.btn-ghost:hover{background:rgba(127,127,127,.1)}`

// cardCSS — surface cards.
const cardCSS = `.card{display:flex;flex-direction:column;background:var(--kitwork-surface,#fff);` +
	`border-radius:.75rem;overflow:hidden}` +
	`.card-header{padding:1.25rem 1.5rem 0}` +
	`.card-body{padding:1.25rem 1.4rem 1.4rem;flex:1}` +
	`.card-footer{padding:0 1.5rem 1.25rem}` +
	`.card-media{display:block;aspect-ratio:16/9;overflow:hidden;background:rgba(127,127,127,.06)}` +
	`.card-media img{width:100%;height:100%;object-fit:cover}` +
	`.card-title{display:block;font-weight:700;font-size:1.05rem;line-height:1.35;color:var(--kitwork-text-hi,#0f172a)}` +
	`.card-text{margin-top:.4rem;font-size:.85rem;line-height:1.55;color:var(--kitwork-text-lo,#64748b);` +
	`overflow:hidden;display:-webkit-box;-webkit-line-clamp:2;-webkit-box-orient:vertical}` +
	`.card-meta{display:block;margin-top:.6rem;font-size:.72rem;color:var(--kitwork-text-muted,#94a3b8)}` +
	`.card-hover{transition:background .15s ease}` +
	`.card-hover:hover{background:var(--kitwork-surface-hi,rgba(127,127,127,.05))}`

// proseCSS — readable long-form typography for raw/CMS HTML (articles, docs, blog).
const proseCSS = `.prose{font-size:1rem;line-height:1.85;color:var(--kitwork-text,#52525b)}` +
	`.dark .prose{color:var(--kitwork-text,#a1a1aa)}` +
	`.prose h1{font-weight:900;font-size:2rem;line-height:1.15;margin:0 0 1.4rem;color:var(--kitwork-text-hi,#18181b)}` +
	`.prose h2{font-weight:800;font-size:1.55rem;line-height:1.25;margin:2.75rem 0 1rem;color:var(--kitwork-text-hi,#18181b)}` +
	`.prose h3{font-weight:700;font-size:1.25rem;margin:2.25rem 0 .75rem;color:var(--kitwork-text-hi,#18181b)}` +
	`.prose h4{font-weight:700;font-size:1.05rem;margin:1.8rem 0 .65rem;color:var(--kitwork-text-hi,#18181b)}` +
	`.dark .prose h1,.dark .prose h2,.dark .prose h3,.dark .prose h4{color:var(--kitwork-text-hi,#f4f4f5)}` +
	`.prose p{margin:1.2rem 0}` +
	`.prose a{color:var(--kitwork-brand,#f82244);text-decoration:underline;text-underline-offset:2px}` +
	`.prose strong{font-weight:700;color:var(--kitwork-text-hi,#18181b)}` +
	`.dark .prose strong{color:var(--kitwork-text-hi,#f4f4f5)}` +
	`.prose blockquote{margin:1.9rem 0;padding:.5rem 0 .5rem 1.5rem;border-left:3px solid var(--kitwork-brand,#f82244);font-style:italic;color:var(--kitwork-text-lo,#64748b)}` +
	`.dark .prose blockquote{color:var(--kitwork-text-lo,#a1a1aa)}` +
	`.prose ul,.prose ol{margin:1.2rem 0;padding-left:1.5rem;list-style:revert}` +
	`.prose li{margin:.45rem 0}` +
	`.prose img{max-width:100%;height:auto;border-radius:.75rem;margin:1.9rem 0}` +
	`.prose figure{margin:1.9rem 0}` +
	`.prose figcaption{font-size:.8rem;text-align:center;color:var(--kitwork-text-muted,#94a3b8);margin-top:.5rem}` +
	`.prose hr{border:0;border-top:1px solid var(--kitwork-border,rgba(0,0,0,.1));margin:2rem 0}` +
	`.dark .prose hr{border-top-color:var(--kitwork-border,rgba(255,255,255,.1))}` +
	`.prose pre{overflow-x:auto;padding:1.1rem 1.3rem;border-radius:.7rem;background:#0d1117;color:#e6edf3;` +
	`font-size:.86rem;line-height:1.65;margin:1.9rem 0}` +
	`.prose pre code{background:none;padding:0;color:inherit;font-size:inherit}` +
	`.prose code{font-family:'Fira Code',ui-monospace,SFMono-Regular,Menlo,monospace;font-size:.88em;` +
	`background:var(--kitwork-surface-hi,rgba(127,127,127,.08));padding:.15em .4em;border-radius:.35rem}`

// badgeCSS — status pills and tags.
const badgeCSS = `.badge{display:inline-flex;align-items:center;padding:.15rem .55rem;border-radius:9999px;` +
	`font-size:.72rem;font-weight:600;line-height:1;white-space:nowrap}` +
	`.badge-brand{background:rgba(var(--kitwork-brand-rgb,248,34,68),.12);color:var(--kitwork-brand,#f82244)}` +
	`.badge-success{background:rgba(16,185,129,.12);color:#059669}` +
	`.badge-warning{background:rgba(245,158,11,.14);color:#b45309}` +
	`.badge-danger{background:rgba(239,68,68,.12);color:#dc2626}` +
	`.badge-neutral{background:rgba(127,127,127,.14);color:var(--kitwork-text-lo,#64748b)}`

// alertCSS — callout boxes.
const alertCSS = `.alert{display:flex;gap:.75rem;padding:.9rem 1.1rem;border-radius:.6rem;` +
	`font-size:.9rem;line-height:1.55}` +
	`.alert-info{background:rgba(59,130,246,.08);color:#1d4ed8}` +
	`.alert-success{background:rgba(16,185,129,.08);color:#047857}` +
	`.alert-warning{background:rgba(245,158,11,.1);color:#b45309}` +
	`.alert-danger{background:rgba(239,68,68,.08);color:#b91c1c}`

// inputCSS — form fields.
const inputCSS = `.input,.textarea,.select{width:100%;padding:.6rem .85rem;border-radius:.5rem;` +
	`border:1px solid transparent;background:var(--kitwork-input,rgba(127,127,127,.06));` +
	`color:var(--kitwork-text-hi,#0f172a);font-family:inherit;font-size:.9rem;line-height:1.4;` +
	`transition:border-color .15s ease,background .15s ease}` +
	`.input:focus,.textarea:focus,.select:focus{outline:none;border-color:var(--kitwork-brand,#f82244);` +
	`background:var(--kitwork-surface,#fff)}` +
	`.input::placeholder,.textarea::placeholder{color:var(--kitwork-text-muted,#94a3b8)}` +
	`.input-small{padding:.4rem .65rem;font-size:.8rem}.input-large{padding:.8rem 1.1rem;font-size:1rem}`

// tableCSS — data tables.
const tableCSS = `.table{width:100%;border-collapse:collapse;font-size:.9rem;text-align:left}` +
	`.table th,.table td{padding:.7rem 1rem;border-bottom:1px solid var(--kitwork-border,rgba(0,0,0,.05))}` +
	`.table th{font-weight:600;font-size:.72rem;text-transform:uppercase;letter-spacing:.05em;` +
	`color:var(--kitwork-text-muted,#94a3b8)}` +
	`.table tbody tr:hover{background:rgba(127,127,127,.04)}` +
	`.table-zebra tbody tr:nth-child(even){background:rgba(127,127,127,.03)}`

// avatarCSS — user profile pictures. `.avatar` + `.avatar-small` / `.avatar-large` / `.avatar-group`.
const avatarCSS = `.avatar{display:inline-flex;align-items:center;justify-content:center;width:2.5rem;height:2.5rem;` +
	`border-radius:9999px;background:rgba(127,127,127,.1);overflow:hidden;object-fit:cover;font-weight:600;font-size:.875rem}` +
	`.avatar-small,.avatar-sm{width:1.75rem;height:1.75rem;font-size:.7rem}` +
	`.avatar-large,.avatar-lg{width:3.5rem;height:3.5rem;font-size:1.1rem}` +
	`.avatar-group{display:inline-flex}` +
	`.avatar-group .avatar{border:2px solid var(--kitwork-surface,#fff);margin-left:-.6rem}` +
	`.avatar-group .avatar:first-child{margin-left:0}`

// dialogCSS — modals and popups. `.dialog` + `.dialog-backdrop`.
const dialogCSS = `.dialog-backdrop{position:fixed;inset:0;background:rgba(0,0,0,.6);backdrop-filter:blur(4px);` +
	`z-index:999;display:flex;align-items:center;justify-content:center}` +
	`.dialog{background:var(--kitwork-surface,#fff);border-radius:.75rem;width:100%;max-width:32rem;padding:1.5rem;` +
	`box-shadow:0 20px 25px -5px rgba(0,0,0,.25)}` +
	`.dialog-header{font-size:1.125rem;font-weight:700;margin-bottom:.75rem}` +
	`.dialog-body{font-size:.875rem;line-height:1.5;margin-bottom:1.25rem}` +
	`.dialog-footer{display:flex;justify-content:flex-end;gap:.75rem}`

// skeletonCSS — loading state placeholder animations.
const skeletonCSS = `.skeleton{background:linear-gradient(90deg,rgba(127,127,127,.05) 25%,rgba(127,127,127,.12) 50%,rgba(127,127,127,.05) 75%);` +
	`background-size:200% 100%;animation:skeleton-wave 1.5s infinite ease-in-out;border-radius:.375rem}` +
	`.skeleton-text{height:1rem;width:100%;margin-bottom:.5rem}` +
	`.skeleton-avatar{width:2.5rem;height:2.5rem;border-radius:9999px}` +
	`@keyframes skeleton-wave{0%{background-position:200% 0}100%{background-position:-200% 0}}`

// tooltipCSS — hover tooltips. `.tooltip[data-tooltip="..."]`.
const tooltipCSS = `.tooltip{position:relative;display:inline-block}` +
	`.tooltip::after{content:attr(data-tooltip);position:absolute;bottom:100%;left:50%;transform:translateX(-50%);` +
	`padding:.25rem .5rem;background:#000;color:#fff;font-size:.7rem;border-radius:.25rem;white-space:nowrap;` +
	`opacity:0;pointer-events:none;transition:opacity .2s ease;margin-bottom:.35rem;z-index:1000}` +
	`.tooltip:hover::after{opacity:1}`

// statCSS — metrics and dashboard stats. `.stat` + `.stat-title` / `.stat-value` / `.stat-desc`.
const statCSS = `.stat{display:flex;flex-direction:column;padding:1.25rem;background:var(--kitwork-surface,#fff);` +
	`border-radius:.75rem;border:1px solid var(--kitwork-border,rgba(0,0,0,.05))}` +
	`.stat-title{font-size:.8rem;font-weight:500;color:var(--kitwork-text-lo,#64748b);text-transform:uppercase;letter-spacing:.05em}` +
	`.stat-value{font-size:1.75rem;font-weight:700;color:var(--kitwork-text-hi,#0f172a);margin:.25rem 0}` +
	`.stat-desc{font-size:.75rem;color:var(--kitwork-text-muted,#94a3b8)}`

// navbarCSS — top site navigation header. `.navbar` + `.navbar-brand` / `.navbar-nav` / `.navbar-item`.
const navbarCSS = `.navbar{display:flex;align-items:center;justify-content:space-between;padding:.75rem 1.5rem;` +
	`background:var(--kitwork-surface,#fff);border-bottom:1px solid var(--kitwork-border,rgba(0,0,0,.05));width:100%}` +
	`.navbar-brand{font-size:1.125rem;font-weight:700;color:var(--kitwork-text-hi,#0f172a);text-decoration:none}` +
	`.navbar-nav{display:flex;align-items:center;gap:1rem;list-style:none;margin:0;padding:0}` +
	`.navbar-item{color:var(--kitwork-text-lo,#64748b);text-decoration:none;font-size:.875rem;font-weight:500;transition:color .15s ease}` +
	`.navbar-item:hover{color:var(--kitwork-text-hi,#0f172a)}`

// timelineCSS — history / activity timelines. `.timeline` + `.timeline-item`.
const timelineCSS = `.timeline{position:relative;padding-left:1.5rem;border-left:2px solid var(--kitwork-border,rgba(0,0,0,.08));list-style:none;margin:0}` +
	`.timeline-item{position:relative;margin-bottom:1.25rem}` +
	`.timeline-item::before{content:'';position:absolute;left:-1.95rem;top:.25rem;width:.75rem;height:.75rem;border-radius:9999px;background:var(--kitwork-brand,#f82244);border:2px solid var(--kitwork-surface,#fff)}` +
	`.timeline-time{font-size:.75rem;color:var(--kitwork-text-muted,#94a3b8);margin-bottom:.25rem}` +
	`.timeline-content{font-size:.875rem;color:var(--kitwork-text,#52525b)}`
