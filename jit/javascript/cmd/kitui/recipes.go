package main

// recipe is one catalog entry. demo is the exact markup rendered live and shown
// as the copy-paste source, so the two can never drift.
type recipe struct {
	id    string
	title string
	tag   string // "kitjs" (behavioral) or "tailwind" (style-only)
	desc  string
	demo  string
}

type group struct {
	category string
	blurb    string
	items    []recipe
}

// Shared class strings. Kept as constants so repeated controls stay identical;
// the composed demo string still carries the full literal classes for copy-paste
// and for the jitcss scanner.
const (
	btnBase      = "inline-flex items-center justify-center gap-2 rounded-lg px-4 py-2 text-sm font-medium transition"
	btnPrimary   = btnBase + " bg-brand text-white hover:bg-brand/90"
	btnSecondary = btnBase + " border border-slate-300 bg-white text-slate-700 hover:bg-slate-50 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 dark:hover:bg-slate-700"
	btnGhost     = btnBase + " text-slate-600 hover:bg-slate-100 dark:text-slate-300 dark:hover:bg-slate-800"
	inputCls     = "w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 focus:border-brand focus:outline-none dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100"
	labelCls     = "block text-sm font-medium text-slate-700 dark:text-slate-200"
)

var catalog = []group{
	{
		category: "Forms & inputs",
		blurb:    "Native controls styled with jitcss, plus KitJS state where the interaction needs it.",
		items: []recipe{
			{
				id: "input", title: "Input", tag: "tailwind", desc: "Text field with focus ring.",
				demo: `<label class="w-full max-w-xs">
  <span class="` + labelCls + `">Email</span>
  <input type="email" placeholder="you@example.com" class="mt-1.5 ` + inputCls + `">
</label>`,
			},
			{
				id: "textarea", title: "Textarea", tag: "tailwind", desc: "Multi-line input.",
				demo: `<label class="w-full max-w-xs">
  <span class="` + labelCls + `">Message</span>
  <textarea rows="3" placeholder="Write something…" class="mt-1.5 ` + inputCls + `"></textarea>
</label>`,
			},
			{
				id: "select", title: "Select", tag: "tailwind", desc: "Native select.",
				demo: `<label class="w-full max-w-xs">
  <span class="` + labelCls + `">Plan</span>
  <select class="mt-1.5 ` + inputCls + `">
    <option>Free</option>
    <option>Pro</option>
    <option>Enterprise</option>
  </select>
</label>`,
			},
			{
				id: "checkbox", title: "Checkbox & radio", tag: "tailwind", desc: "Accent-colored native controls.",
				demo: `<div class="flex flex-col gap-2 text-sm text-slate-700 dark:text-slate-200">
  <label class="flex items-center gap-2"><input type="checkbox" class="accent-brand" checked> Ship weekly digest</label>
  <label class="flex items-center gap-2"><input type="radio" name="tier" class="accent-brand" checked> Monthly</label>
  <label class="flex items-center gap-2"><input type="radio" name="tier" class="accent-brand"> Yearly</label>
</div>`,
			},
			{
				id: "field", title: "Field", tag: "tailwind", desc: "Label + hint + error wrapper.",
				demo: `<div class="w-full max-w-xs">
  <label class="` + labelCls + `">API token</label>
  <input class="mt-1.5 ` + inputCls + ` border-brand" value="sk_live_…">
  <p class="mt-1.5 text-xs text-brand">This token is invalid or expired.</p>
</div>`,
			},
			{
				id: "switch", title: "Switch", tag: "kitjs", desc: "Guarded binary toggle.",
				demo: `<div data-kit-component="switch@1.0.0" class="flex items-center gap-3">
  <button role="switch" data-kit-click="toggle()" data-kit-bind:aria-checked="checked"
    data-kit-class="checked ? 'bg-brand' : 'bg-slate-300 dark:bg-slate-700'"
    class="relative h-6 w-11 rounded-full transition bg-slate-300 dark:bg-slate-700">
    <span data-kit-class="checked ? 'translate-x-5' : 'translate-x-0'"
      class="absolute left-0.5 top-0.5 h-5 w-5 rounded-full bg-white transition-transform"></span>
  </button>
  <span class="text-sm text-slate-600 dark:text-slate-300" data-kit-text="checked ? 'On' : 'Off'">Off</span>
</div>`,
			},
			{
				id: "slider", title: "Slider", tag: "kitjs", desc: "Bounded value with percent() fill.",
				demo: `<div data-kit-component="slider@1.0.0" data-kit-scope="value: 60, min: 0, max: 100, step: 5" class="w-full max-w-xs">
  <div class="h-2 w-full overflow-hidden rounded-full bg-slate-200 dark:bg-slate-700">
    <div class="h-full rounded-full bg-brand" data-kit-style="width: percent() + '%';"></div>
  </div>
  <div class="mt-3 flex items-center gap-2">
    <button class="` + btnSecondary + `" data-kit-click="decrement()">−</button>
    <output class="w-10 text-center text-sm font-semibold" data-kit-text="value">60</output>
    <button class="` + btnSecondary + `" data-kit-click="increment()">+</button>
  </div>
</div>`,
			},
			{
				id: "stepper", title: "Stepper", tag: "kitjs", desc: "Numeric input with guarded bounds.",
				demo: `<div data-kit-component="stepper@1.0.0" data-kit-scope="value: 1, min: 0, max: 9, step: 1" class="inline-flex items-center rounded-lg border border-slate-300 dark:border-slate-700">
  <button class="px-3 py-2 text-slate-600 hover:text-brand disabled:opacity-40 dark:text-slate-300" data-kit-click="decrement()" data-kit-bind:disabled="!canDecrement()">−</button>
  <output class="w-10 border-x border-slate-200 py-2 text-center text-sm font-semibold dark:border-slate-700" data-kit-text="value">1</output>
  <button class="px-3 py-2 text-slate-600 hover:text-brand disabled:opacity-40 dark:text-slate-300" data-kit-click="increment()" data-kit-bind:disabled="!canIncrement()">+</button>
</div>`,
			},
			{
				id: "otp", title: "OTP input", tag: "kitjs", desc: "Segmented code with auto-advance & paste.",
				demo: `<div data-kit-component="otp@1.0.0" class="flex flex-col items-center gap-3">
  <div class="flex gap-2">
    <input data-otp-slot inputmode="numeric" class="h-12 w-11 rounded-lg border border-slate-300 text-center text-lg font-semibold focus:border-brand focus:outline-none dark:border-slate-700 dark:bg-slate-800">
    <input data-otp-slot inputmode="numeric" class="h-12 w-11 rounded-lg border border-slate-300 text-center text-lg font-semibold focus:border-brand focus:outline-none dark:border-slate-700 dark:bg-slate-800">
    <input data-otp-slot inputmode="numeric" class="h-12 w-11 rounded-lg border border-slate-300 text-center text-lg font-semibold focus:border-brand focus:outline-none dark:border-slate-700 dark:bg-slate-800">
    <input data-otp-slot inputmode="numeric" class="h-12 w-11 rounded-lg border border-slate-300 text-center text-lg font-semibold focus:border-brand focus:outline-none dark:border-slate-700 dark:bg-slate-800">
  </div>
  <output class="text-xs text-slate-500" data-kit-text="isComplete() ? 'complete: ' + value : 'enter 4 digits'">enter 4 digits</output>
</div>`,
			},
			{
				id: "rating", title: "Rating", tag: "kitjs", desc: "Star value with fill projection.",
				demo: `<div data-kit-component="rating@1.0.0" data-kit-scope="value: 3, max: 5" class="flex items-center gap-3">
  <div class="flex">
    <button class="text-2xl text-brand" data-kit-click="rate(1)" data-kit-text="isFilled(1) ? '★' : '☆'">☆</button>
    <button class="text-2xl text-brand" data-kit-click="rate(2)" data-kit-text="isFilled(2) ? '★' : '☆'">☆</button>
    <button class="text-2xl text-brand" data-kit-click="rate(3)" data-kit-text="isFilled(3) ? '★' : '☆'">☆</button>
    <button class="text-2xl text-brand" data-kit-click="rate(4)" data-kit-text="isFilled(4) ? '★' : '☆'">☆</button>
    <button class="text-2xl text-brand" data-kit-click="rate(5)" data-kit-text="isFilled(5) ? '★' : '☆'">☆</button>
  </div>
  <output class="text-sm text-slate-500" data-kit-text="value + ' / 5'">3 / 5</output>
</div>`,
			},
			{
				id: "tags", title: "Tags / Pillbox", tag: "kitjs", desc: "Deduplicated tag input.",
				demo: `<div data-kit-component="tags@1.0.0" data-kit-scope="tags: ['kitwork', 'go'], draft: '', max: 6" class="w-full max-w-xs">
  <div class="mb-2 flex flex-wrap gap-1.5">
    <template data-kit-for="tag of tags">
      <span class="inline-flex items-center gap-1 rounded-full bg-brand/10 px-2.5 py-1 text-xs font-medium text-brand">
        <span data-kit-text="tag"></span>
        <button class="hover:opacity-70" data-kit-click="remove(tag)">×</button>
      </span>
    </template>
  </div>
  <input class="` + inputCls + `" placeholder="Add a tag, press Enter" data-kit-model="draft" data-kit-keydown:enter="add(draft)">
</div>`,
			},
			{
				id: "combobox", title: "Combobox / Autocomplete", tag: "kitjs", desc: "Type-to-filter with selection.",
				demo: `<div data-kit-component="combobox@1.0.0" data-kit-click:outside="hide()"
  data-kit-scope="options: ['Apple', 'Apricot', 'Banana', 'Cherry', 'Grape', 'Kiwi'], query: '', open: false, activeIndex: -1, selected: ''"
  class="relative w-full max-w-xs">
  <input class="` + inputCls + `" placeholder="Search fruit…" data-kit-model="query" data-kit-input="search()" data-kit-focusin="show()" data-kit-keydown:escape="hide()">
  <div class="absolute z-10 mt-1 w-full overflow-hidden rounded-lg border border-slate-200 bg-white shadow-lg dark:border-slate-700 dark:bg-slate-800" data-kit-show="open" hidden>
    <template data-kit-for="option of filtered()">
      <button class="block w-full px-3 py-2 text-left text-sm hover:bg-brand/10" data-kit-click="choose(option)" data-kit-text="option"></button>
    </template>
  </div>
</div>`,
			},
		},
	},
	{
		category: "Actions & overlays",
		blurb:    "Buttons and every overlay surface — modal, drawer, popover, tooltip, toast — driven by KitJS state.",
		items: []recipe{
			{
				id: "button", title: "Button", tag: "tailwind", desc: "Primary, secondary, ghost.",
				demo: `<div class="flex flex-wrap items-center gap-2">
  <button class="` + btnPrimary + `">Primary</button>
  <button class="` + btnSecondary + `">Secondary</button>
  <button class="` + btnGhost + `">Ghost</button>
  <button class="` + btnPrimary + ` opacity-50" disabled>Disabled</button>
</div>`,
			},
			{
				id: "dropdown", title: "Dropdown", tag: "kitjs", desc: "Selection menu with roving state.",
				demo: `<div data-kit-component="dropdown@1.0.0" data-kit-scope="items: ['Profile', 'Billing', 'Sign out']" data-kit-click:outside="hide()" class="relative inline-block">
  <button class="` + btnSecondary + `" data-kit-click="toggle()" data-kit-bind:aria-expanded="open">Menu ▾</button>
  <div class="absolute z-10 mt-1 w-44 overflow-hidden rounded-lg border border-slate-200 bg-white shadow-lg dark:border-slate-700 dark:bg-slate-800" data-kit-show="open" hidden>
    <template data-kit-for="item of items">
      <button class="block w-full px-3 py-2 text-left text-sm hover:bg-brand/10" data-kit-click="choose(item)" data-kit-text="item"></button>
    </template>
  </div>
</div>`,
			},
			{
				id: "dialog", title: "Modal / Dialog", tag: "kitjs", desc: "Backdrop-dismissible overlay.",
				demo: `<div data-kit-component="dialog@1.0.0">
  <button class="` + btnPrimary + `" data-kit-click="show()">Open modal</button>
  <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4" data-kit-show="open" data-kit-click:self="close()" hidden>
    <div class="w-full max-w-md rounded-xl border border-slate-200 bg-white p-6 shadow-xl dark:border-slate-700 dark:bg-slate-900">
      <h3 class="text-base font-semibold text-slate-900 dark:text-white">Delete project</h3>
      <p class="mt-2 text-sm text-slate-500 dark:text-slate-400">This action cannot be undone. This will permanently remove the project.</p>
      <div class="mt-5 flex justify-end gap-2">
        <button class="` + btnSecondary + `" data-kit-click="close()">Cancel</button>
        <button class="` + btnPrimary + `" data-kit-click="close()">Delete</button>
      </div>
    </div>
  </div>
</div>`,
			},
			{
				id: "drawer", title: "Drawer", tag: "kitjs", desc: "Edge panel with backdrop.",
				demo: `<div data-kit-component="drawer@1.0.0">
  <button class="` + btnSecondary + `" data-kit-click="show()">Open drawer</button>
  <div class="fixed inset-0 z-50 bg-black/50" data-kit-show="open" data-kit-click:self="hide()" hidden>
    <aside class="absolute right-0 top-0 h-full w-72 border-l border-slate-200 bg-white p-6 shadow-xl dark:border-slate-700 dark:bg-slate-900">
      <div class="flex items-center justify-between">
        <h3 class="text-base font-semibold">Filters</h3>
        <button class="` + btnGhost + `" data-kit-click="hide()">×</button>
      </div>
      <p class="mt-3 text-sm text-slate-500 dark:text-slate-400">Slide-in surface for filters, carts, or settings.</p>
    </aside>
  </div>
</div>`,
			},
			{
				id: "popover", title: "Popover", tag: "kitjs", desc: "Anchored, click-dismissed panel.",
				demo: `<div data-kit-component="popover@1.0.0" data-kit-click:outside="hide()" class="relative inline-block">
  <button class="` + btnSecondary + `" data-kit-click="toggle()" data-kit-bind:aria-expanded="open">Share ▾</button>
  <div class="absolute z-10 mt-2 w-56 rounded-lg border border-slate-200 bg-white p-3 text-sm shadow-lg dark:border-slate-700 dark:bg-slate-800" data-kit-show="open" hidden>
    <p class="font-medium">Share this page</p>
    <p class="mt-1 text-xs text-slate-500">Anyone with the link can view.</p>
  </div>
</div>`,
			},
			{
				id: "tooltip", title: "Tooltip", tag: "kitjs", desc: "Focus/hover description.",
				demo: `<div data-kit-component="tooltip@1.0.0" data-kit-scope="content: 'Saved automatically'" class="relative inline-block">
  <button class="` + btnSecondary + `" data-kit-click="toggle()" data-kit-focusin="show()" data-kit-focusout="hide()">Focus me</button>
  <span class="absolute bottom-full left-0 mb-2 whitespace-nowrap rounded-md bg-slate-900 px-2 py-1 text-xs text-white" data-kit-show="open" data-kit-text="content" hidden></span>
</div>`,
			},
			{
				id: "toast", title: "Toast", tag: "kitjs", desc: "Transient status message.",
				demo: `<div data-kit-component="toast@1.0.0">
  <button class="` + btnPrimary + `" data-kit-click="show('Changes saved', 'success')">Trigger toast</button>
  <div class="fixed bottom-4 right-4 z-50 flex items-center gap-2 rounded-lg border border-slate-200 bg-white px-4 py-3 text-sm shadow-lg dark:border-slate-700 dark:bg-slate-800" role="status" data-kit-show="visible" hidden>
    <span class="h-2 w-2 rounded-full bg-brand"></span>
    <span data-kit-text="message"></span>
    <button class="ml-2 text-slate-400 hover:text-slate-700" data-kit-click="dismiss()">×</button>
  </div>
</div>`,
			},
			{
				id: "alert", title: "Alert / Callout", tag: "kitjs", desc: "Dismissible inline notice.",
				demo: `<div data-kit-component="alert@1.0.0" data-kit-scope="visible: true, message: 'Your trial ends in 3 days.', tone: 'warning'" class="w-full max-w-md">
  <div class="flex items-start gap-3 rounded-lg border border-brand/30 bg-brand/5 p-4 text-sm" data-kit-show="visible" hidden>
    <span class="mt-0.5 h-2 w-2 shrink-0 rounded-full bg-brand"></span>
    <p class="flex-1 text-slate-700 dark:text-slate-200" data-kit-text="message"></p>
    <button class="text-slate-400 hover:text-slate-700" data-kit-click="dismiss()">×</button>
  </div>
</div>`,
			},
			{
				id: "copy", title: "Copy button", tag: "kitjs", desc: "Clipboard write with reset flag.",
				demo: `<div data-kit-component="copy@1.0.0" data-kit-scope="text: 'npm i kitwork', delay: 1500" class="flex items-center gap-2 rounded-lg border border-slate-200 bg-slate-50 px-3 py-2 dark:border-slate-700 dark:bg-slate-800">
  <code class="text-sm">npm i kitwork</code>
  <button class="` + btnGhost + ` px-2 py-1 text-xs" data-kit-click="copy()" data-kit-text="copied ? '✓ Copied' : 'Copy'">Copy</button>
</div>`,
			},
		},
	},
	{
		category: "Navigation & disclosure",
		blurb:    "Tabs, accordions, breadcrumbs, and pagination.",
		items: []recipe{
			{
				id: "tabs", title: "Tabs", tag: "kitjs", desc: "Ordered selection controller.",
				demo: `<div data-kit-component="tabs@1.0.0" data-kit-scope="tabs: ['overview', 'analytics', 'settings'], active: 'overview'" class="w-full max-w-md">
  <div class="flex gap-1 border-b border-slate-200 dark:border-slate-700">
    <button class="border-b-2 border-transparent px-3 py-2 text-sm text-slate-500 data-[active]:border-brand data-[active]:text-brand" data-kit-click="select('overview')" data-kit-bind:data-active="isActive('overview') ? 'on' : null">Overview</button>
    <button class="border-b-2 border-transparent px-3 py-2 text-sm text-slate-500 data-[active]:border-brand data-[active]:text-brand" data-kit-click="select('analytics')" data-kit-bind:data-active="isActive('analytics') ? 'on' : null">Analytics</button>
    <button class="border-b-2 border-transparent px-3 py-2 text-sm text-slate-500 data-[active]:border-brand data-[active]:text-brand" data-kit-click="select('settings')" data-kit-bind:data-active="isActive('settings') ? 'on' : null">Settings</button>
  </div>
  <div class="p-4 text-sm text-slate-600 dark:text-slate-300">
    <p data-kit-show="isActive('overview')">Overview panel — click a tab.</p>
    <p data-kit-show="isActive('analytics')" hidden>Analytics panel.</p>
    <p data-kit-show="isActive('settings')" hidden>Settings panel.</p>
  </div>
</div>`,
			},
			{
				id: "accordion", title: "Accordion", tag: "kitjs", desc: "Grouped disclosure (single/multiple).",
				demo: `<div data-kit-component="accordion@1.0.0" class="w-full max-w-md divide-y divide-slate-200 rounded-lg border border-slate-200 dark:divide-slate-700 dark:border-slate-700">
  <div>
    <button class="flex w-full items-center justify-between px-4 py-3 text-left text-sm font-medium" data-kit-click="toggle('a')">Is it accessible?<span data-kit-text="isOpen('a') ? '−' : '+'">+</span></button>
    <div class="px-4 pb-3 text-sm text-slate-500" data-kit-show="isOpen('a')" hidden>Yes — the markup owns the ARIA.</div>
  </div>
  <div>
    <button class="flex w-full items-center justify-between px-4 py-3 text-left text-sm font-medium" data-kit-click="toggle('b')">Is it styled?<span data-kit-text="isOpen('b') ? '−' : '+'">+</span></button>
    <div class="px-4 pb-3 text-sm text-slate-500" data-kit-show="isOpen('b')" hidden>With jitcss utilities, yes.</div>
  </div>
</div>`,
			},
			{
				id: "collapse", title: "Collapse", tag: "kitjs", desc: "Single disclosure region.",
				demo: `<div data-kit-component="collapse@1.0.0" class="w-full max-w-md">
  <button class="` + btnSecondary + ` w-full justify-between" data-kit-click="toggle()" data-kit-bind:aria-expanded="open">
    <span>Show details</span><span data-kit-text="open ? '▲' : '▼'">▼</span>
  </button>
  <div class="mt-2 rounded-lg border border-slate-200 bg-slate-50 p-4 text-sm text-slate-500 dark:border-slate-700 dark:bg-slate-800" data-kit-show="open" hidden>
    A single collapsible region — the panel stays in the DOM so CSS owns the transition.
  </div>
</div>`,
			},
			{
				id: "breadcrumbs", title: "Breadcrumbs", tag: "tailwind", desc: "Static path, no JS.",
				demo: `<nav class="flex items-center gap-1.5 text-sm text-slate-500">
  <a href="#" class="hover:text-brand">Home</a><span>/</span>
  <a href="#" class="hover:text-brand">Projects</a><span>/</span>
  <span class="font-medium text-slate-900 dark:text-white">Kitwork</span>
</nav>`,
			},
			{
				id: "pagination", title: "Pagination", tag: "kitjs", desc: "Bounded one-based paging.",
				demo: `<div data-kit-component="pagination@1.0.0" data-kit-scope="page: 1, pages: 5" class="flex items-center gap-1">
  <button class="` + btnSecondary + ` px-3 py-1.5" data-kit-click="previous()" data-kit-bind:disabled="!canPrevious()">Prev</button>
  <span class="px-3 text-sm">Page <span data-kit-text="page">1</span> / 5</span>
  <button class="` + btnSecondary + ` px-3 py-1.5" data-kit-click="next()" data-kit-bind:disabled="!canNext()">Next</button>
</div>`,
			},
		},
	},
	{
		category: "Data display",
		blurb:    "Presentation primitives — most are pure jitcss; carousel adds KitJS state.",
		items: []recipe{
			{
				id: "avatar", title: "Avatar", tag: "tailwind", desc: "Initials & stacked group.",
				demo: `<div class="flex items-center gap-4">
  <span class="flex h-10 w-10 items-center justify-center rounded-full bg-brand text-sm font-semibold text-white">HQ</span>
  <div class="flex">
    <span class="flex h-9 w-9 items-center justify-center rounded-full border-2 border-white bg-slate-700 text-xs font-semibold text-white ml-[-10px] dark:border-slate-900">A</span>
    <span class="flex h-9 w-9 items-center justify-center rounded-full border-2 border-white bg-brand text-xs font-semibold text-white ml-[-10px] dark:border-slate-900">B</span>
    <span class="flex h-9 w-9 items-center justify-center rounded-full border-2 border-white bg-slate-400 text-xs font-semibold text-white ml-[-10px] dark:border-slate-900">+3</span>
  </div>
</div>`,
			},
			{
				id: "badge", title: "Badge", tag: "tailwind", desc: "Status pills.",
				demo: `<div class="flex flex-wrap items-center gap-2">
  <span class="inline-flex items-center gap-1 rounded-full bg-brand/10 px-2.5 py-0.5 text-xs font-medium text-brand"><span class="h-1.5 w-1.5 rounded-full bg-brand"></span>Active</span>
  <span class="rounded-full bg-slate-100 px-2.5 py-0.5 text-xs font-medium text-slate-600 dark:bg-slate-800 dark:text-slate-300">Draft</span>
  <span class="rounded-full bg-brand px-2.5 py-0.5 text-xs font-medium text-white">New</span>
</div>`,
			},
			{
				id: "card", title: "Card", tag: "tailwind", desc: "Header, body, footer.",
				demo: `<div class="w-full max-w-xs overflow-hidden rounded-xl border border-slate-200 bg-white dark:border-slate-700 dark:bg-slate-900">
  <div class="border-b border-slate-100 px-5 py-3 dark:border-slate-800"><h3 class="font-semibold">Starter</h3></div>
  <div class="px-5 py-4"><p class="text-2xl font-bold">$9<span class="text-sm font-normal text-slate-500">/mo</span></p><p class="mt-1 text-sm text-slate-500">For side projects.</p></div>
  <div class="border-t border-slate-100 px-5 py-3 dark:border-slate-800"><button class="` + btnPrimary + ` w-full">Choose</button></div>
</div>`,
			},
			{
				id: "table", title: "Table", tag: "tailwind", desc: "Zebra-free data table.",
				demo: `<div class="w-full max-w-md overflow-x-auto rounded-lg border border-slate-200 dark:border-slate-700">
  <table class="w-full text-left text-sm">
    <thead class="bg-slate-50 text-xs uppercase tracking-wide text-slate-500 dark:bg-slate-800"><tr><th class="px-4 py-2">Name</th><th class="px-4 py-2">Role</th><th class="px-4 py-2">Status</th></tr></thead>
    <tbody class="divide-y divide-slate-100 dark:divide-slate-800">
      <tr><td class="px-4 py-2">Quốc</td><td class="px-4 py-2 text-slate-500">Owner</td><td class="px-4 py-2"><span class="rounded-full bg-brand/10 px-2 py-0.5 text-xs text-brand">Active</span></td></tr>
      <tr><td class="px-4 py-2">Anh</td><td class="px-4 py-2 text-slate-500">Editor</td><td class="px-4 py-2"><span class="rounded-full bg-slate-100 px-2 py-0.5 text-xs text-slate-500 dark:bg-slate-800">Invited</span></td></tr>
    </tbody>
  </table>
</div>`,
			},
			{
				id: "skeleton", title: "Skeleton", tag: "tailwind", desc: "Loading placeholder (animate-pulse).",
				demo: `<div class="w-full max-w-xs animate-pulse">
  <div class="flex items-center gap-3">
    <div class="h-10 w-10 rounded-full bg-slate-200 dark:bg-slate-700"></div>
    <div class="flex-1 space-y-2"><div class="h-3 w-[75%] rounded bg-slate-200 dark:bg-slate-700"></div><div class="h-3 w-[50%] rounded bg-slate-200 dark:bg-slate-700"></div></div>
  </div>
  <div class="mt-4 h-24 rounded-lg bg-slate-200 dark:bg-slate-700"></div>
</div>`,
			},
			{
				id: "stat", title: "Stat", tag: "tailwind", desc: "Metric tile.",
				demo: `<div class="w-full max-w-xs rounded-xl border border-slate-200 bg-white p-5 dark:border-slate-700 dark:bg-slate-900">
  <p class="text-xs font-medium uppercase tracking-wide text-slate-500">Monthly revenue</p>
  <p class="mt-1 text-3xl font-bold">$12,480</p>
  <p class="mt-1 text-xs font-medium text-brand">▲ 12.5% vs last month</p>
</div>`,
			},
			{
				id: "rotator", title: "Rotator", tag: "kitjs", desc: "Carousel that advances itself, and stops when it should.",
				demo: `<div data-kit-component="rotator@1.0.0" data-kit-scope="items: ['Ship it', 'Own it', 'Keep it'], active: 0, interval: 2000" class="w-full max-w-xs">
  <div class="flex h-28 items-center justify-center rounded-lg bg-brand/10 text-2xl font-bold text-brand">
    <span data-kit-show="isActive(0)">Ship it</span>
    <span data-kit-show="isActive(1)" hidden>Own it</span>
    <span data-kit-show="isActive(2)" hidden>Keep it</span>
  </div>
  <div class="mt-3 flex items-center justify-between">
    <button class="` + btnSecondary + ` px-3 py-1.5" data-kit-click="toggle()" data-kit-text="running ? 'Pause' : 'Play'">Pause</button>
    <div class="flex gap-1.5">
      <button class="h-2 w-2 rounded-full bg-slate-300 data-[on]:bg-brand" data-kit-click="select(0)" data-kit-bind:data-on="isActive(0) ? 'on' : null"></button>
      <button class="h-2 w-2 rounded-full bg-slate-300 data-[on]:bg-brand" data-kit-click="select(1)" data-kit-bind:data-on="isActive(1) ? 'on' : null"></button>
      <button class="h-2 w-2 rounded-full bg-slate-300 data-[on]:bg-brand" data-kit-click="select(2)" data-kit-bind:data-on="isActive(2) ? 'on' : null"></button>
    </div>
    <button class="` + btnSecondary + ` px-3 py-1.5" data-kit-click="next()">›</button>
  </div>
  <p class="mt-2 text-xs text-slate-500">Hover, focus, a hidden tab, or reduced motion all hold it.</p>
</div>`,
			},
			{
				id: "carousel", title: "Carousel", tag: "kitjs", desc: "Wrapping slide selection.",
				demo: `<div data-kit-component="carousel@1.0.0" data-kit-scope="slides: ['One', 'Two', 'Three'], active: 0" class="w-full max-w-xs">
  <div class="flex h-28 items-center justify-center rounded-lg bg-brand/10 text-2xl font-bold text-brand">
    <span data-kit-show="isActive(0)">Slide One</span>
    <span data-kit-show="isActive(1)" hidden>Slide Two</span>
    <span data-kit-show="isActive(2)" hidden>Slide Three</span>
  </div>
  <div class="mt-3 flex items-center justify-between">
    <button class="` + btnSecondary + ` px-3 py-1.5" data-kit-click="previous()">‹</button>
    <div class="flex gap-1.5">
      <button class="h-2 w-2 rounded-full bg-slate-300 data-[on]:bg-brand" data-kit-click="select(0)" data-kit-bind:data-on="isActive(0) ? 'on' : null"></button>
      <button class="h-2 w-2 rounded-full bg-slate-300 data-[on]:bg-brand" data-kit-click="select(1)" data-kit-bind:data-on="isActive(1) ? 'on' : null"></button>
      <button class="h-2 w-2 rounded-full bg-slate-300 data-[on]:bg-brand" data-kit-click="select(2)" data-kit-bind:data-on="isActive(2) ? 'on' : null"></button>
    </div>
    <button class="` + btnSecondary + ` px-3 py-1.5" data-kit-click="next()">›</button>
  </div>
</div>`,
			},
		},
	},
}
