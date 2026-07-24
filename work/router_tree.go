package work

// Filesystem-as-runtime routing tree.
//
// A tree tenant is routed by its FOLDERS, not by a flat table of router.get("/path") calls:
// every folder is a runtime node, the folder location IS the URL, and each folder's
// router.kitwork.js declares only runtime BEHAVIOUR (guards, middleware, methods). See
// ROUTER_API_SPEC.md / REQUEST_EXECUTION_FLOW.MD. This file is the resolver: it walks the
// folder tree one segment at a time, lazily discovering + caching children on first hit, so
// the cache IS the tree and its size is bounded by the number of folders, not by traffic.

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// ── dynamic-segment matcher ────────────────────────────────────────────────
// A folder name in {braces} is a dynamic segment: {user} {id[number]} {slug(regex)} {...rest}.

type segKind int

const (
	segTyped segKind = iota // {id[number]}  — most specific
	segRegex                // {slug(^[a-z-]+$)}
	segPlain                // {user}
	segSplat                // {...rest}      — least specific (catch-all)
)

type segMatcher struct {
	name string
	kind segKind
	typ  string // for segTyped: the declared type name ("number", "string", …)
	re   *regexp.Regexp
}

// parseSegment turns a folder name into a matcher, or nil if it is a static (exact) folder.
func parseSegment(seg string) *segMatcher {
	if len(seg) < 2 || seg[0] != '{' || seg[len(seg)-1] != '}' {
		return nil
	}
	in := seg[1 : len(seg)-1]
	if strings.HasPrefix(in, "...") {
		return &segMatcher{name: in[3:], kind: segSplat}
	}
	// The name ends at the first hint bracket. A regex may itself contain '[', so pick whichever
	// of '[' (type) or '(' (regex) appears first — that is the real boundary.
	br := strings.IndexByte(in, '[')
	par := strings.IndexByte(in, '(')
	switch {
	case br >= 0 && (par < 0 || br < par): // {id[number]} / {slug[string]}
		typ := ""
		if in[len(in)-1] == ']' {
			typ = in[br+1 : len(in)-1]
		}
		return &segMatcher{name: in[:br], kind: segTyped, typ: typ}
	case par >= 0: // {slug(regex)}
		re, err := regexp.Compile(in[par+1 : len(in)-1])
		if err != nil {
			return &segMatcher{name: in[:par], kind: segPlain} // malformed → treat as plain
		}
		return &segMatcher{name: in[:par], kind: segRegex, re: re}
	default:
		return &segMatcher{name: in, kind: segPlain}
	}
}

func (m *segMatcher) test(v string) bool {
	switch m.kind {
	case segTyped:
		if v == "" {
			return false
		}
		switch m.typ {
		case "number", "int", "integer", "digit", "digits":
			return strings.IndexFunc(v, func(r rune) bool { return r < '0' || r > '9' }) < 0
		default: // "string" and any other/unknown type → match any non-empty segment
			return true
		}
	case segRegex:
		return m.re != nil && m.re.MatchString(v)
	default: // segPlain, segSplat
		return true
	}
}

// ── node ────────────────────────────────────────────────────────────────────
// One folder. It stores only what it IS (segment + parent); the disk path is derived.

type RouteNode struct {
	seg     string      // own folder name: "" (root), "users", "{user}"
	matcher *segMatcher // nil = static; else dynamic
	parent  *RouteNode
	base    string // ONLY the root sets this — the tenant's disk anchor

	childMu  sync.Mutex                 // guards the one-time children build
	children atomic.Pointer[[]*RouteNode] // copy-on-write → readers never lock
	built    atomic.Bool

	folder      *FolderRouter // compiled router.kitwork.js (behaviour) — see tree_folder.go
	folderMu    sync.Mutex    // guards the one-time folder compile
	folderReady atomic.Bool

	// Per-folder hot reload (active when tenant.HotReload): the recorded source snapshot the
	// throttled hotCheck compares against. srcFiles/srcMod/dirMod are guarded by folderMu.
	hotCheckAt atomic.Int64 // unix nanos of the last check — 1s throttle via CAS
	srcFiles   []string     // router.kitwork.js + every natively-bundled import
	srcMod     int64        // newest modtime across srcFiles at compile (0 = no router)
	dirMod     int64        // the folder's own modtime — changes on child create/remove
}

// diskPath derives the on-disk location by walking up to the root anchor.
func (n *RouteNode) diskPath() string {
	if n.parent == nil {
		return n.base
	}
	return filepath.Join(n.parent.diskPath(), n.seg)
}

// relPath is the tenant-root-relative path (forward slash), used to point the render engine
// at this folder. Root = "".
func (n *RouteNode) relPath() string {
	if n.parent == nil {
		return ""
	}
	return path.Join(n.parent.relPath(), n.seg)
}

// validRouteFolder skips hidden/private folders so they never become routes.
func validRouteFolder(name string) bool {
	if name == "" || name[0] == '.' || name[0] == '_' {
		return false
	}
	return true
}

// nodeRank orders children so statics come first, then dynamics by precedence. A single scan
// that returns the first match then honours "exact beats {id[number]} beats {slug} beats {...}".
func nodeRank(n *RouteNode) int {
	if n.matcher == nil {
		return -1
	}
	return int(n.matcher.kind)
}

// ensureChildren discovers this node's child folders from disk exactly once (double-checked),
// then publishes them atomically so every later request reads them without a lock.
func (n *RouteNode) ensureChildren() {
	if n.built.Load() {
		return
	}
	n.childMu.Lock()
	defer n.childMu.Unlock()
	if n.built.Load() {
		return
	}
	var kids []*RouteNode
	if entries, err := os.ReadDir(n.diskPath()); err == nil {
		for _, e := range entries {
			if !e.IsDir() || !validRouteFolder(e.Name()) {
				continue
			}
			kids = append(kids, &RouteNode{seg: e.Name(), matcher: parseSegment(e.Name()), parent: n})
		}
		sort.SliceStable(kids, func(i, j int) bool { return nodeRank(kids[i]) < nodeRank(kids[j]) })
	}
	n.children.Store(&kids)
	n.built.Store(true)
}

// child resolves ONE path segment against this node — exact-then-dynamic, in one scan.
func (n *RouteNode) child(seg string, params map[string]string) *RouteNode {
	n.ensureChildren()
	kids := n.children.Load()
	if kids == nil {
		return nil
	}
	for _, c := range *kids {
		if c.matcher == nil {
			if c.seg == seg {
				return c
			}
		} else if c.matcher.test(seg) {
			params[c.matcher.name] = seg
			return c
		}
	}
	return nil
}

// ── tree ─────────────────────────────────────────────────────────────────────

type RouteTree struct {
	tenant *Tenant
	root   *RouteNode
}

func NewRouteTree(t *Tenant) *RouteTree {
	return &RouteTree{
		tenant: t,
		root:   &RouteNode{base: t.resolve()},
	}
}

// RouteMatch is the outcome of resolving a URL path against the tree.
type RouteMatch struct {
	Node   *RouteNode        // the deepest node reached (leaf if Found, else last matched)
	Chain  []*RouteNode      // root → Node: guards run DOWN this, views resolve UP it
	Params map[string]string // {user}=quoc, {id}=123
	Found  bool              // false = a segment had no matching folder (→ notfound)
}

func (rt *RouteTree) Resolve(urlPath string) *RouteMatch {
	m := &RouteMatch{Node: rt.root, Chain: []*RouteNode{rt.root}, Params: map[string]string{}, Found: true}
	for _, seg := range splitPath(urlPath) {
		c := m.Node.child(seg, m.Params)
		if c == nil {
			m.Found = false
			return m
		}
		m.Node = c
		m.Chain = append(m.Chain, c)
	}
	return m
}

func splitPath(p string) []string {
	raw := strings.Split(p, "/")
	out := make([]string, 0, len(raw))
	for _, s := range raw {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
