package work

import (
	"github.com/kitwork/engine/value"
)

// Javascript is gone (11/10/2026), with everything it took: drive (moving between pages is
// router.reload(false)), prerender (the server no longer fills a template page's page-scope answers
// on request) and the component version pins (a component is found by name — component="…" in a
// .ui file, data-kit-component on a template page — and the engine ships the latest; a version written
// in a template page's own markup still names one).
//
// It never decided WHETHER a page gets JavaScript — a page that uses the grammar gets the kernel and
// one file per component it names, a page that uses none gets nothing — so nothing replaces it. A
// router that still calls it keeps booting and keeps its chain, but the call applies nothing,
// whatever it is given, and the boot names it once for the file (removedMethodWarnings).
func (f *FolderRouter) Javascript(args ...value.Value) *FolderRouter {
	f.removed(removedJavascript)
	return f
}

// Jitjs is the older name of router.javascript(), gone with it: the same boot line, once per file
// however many of the two names the file calls.
func (f *FolderRouter) Jitjs(args ...value.Value) *FolderRouter { return f.Javascript(args...) }
