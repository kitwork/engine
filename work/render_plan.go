package work

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// RenderPlan is one site generation's Kitwork UI pages (*.ui), compiled with their heads and
// stylesheets before the generation answers a request. Until 27/10/2026 it also held the template
// pages' renderers (.kitwork.html); those went with the template pages.
type RenderPlan struct {
	mu sync.RWMutex

	base   string
	ui     *uiSite
	closed bool
}

func newRenderPlan(t *Tenant, tree *RouteTree) (*RenderPlan, error) {
	if t == nil || tree == nil {
		return nil, fmt.Errorf("render plan requires a tenant and route graph")
	}
	productionMinify := !AllowLocal || Minify // MINIFY=true: production's bytes while developing
	base := t.resolve()
	if err := refuseTemplatePages(base); err != nil {
		return nil, err
	}
	if err := t.generation.Sources().WatchTemplateTree(base); err != nil {
		return nil, err
	}
	// The identity's components (.components/<name>.work and .ui) are shared by its sites and lie
	// outside the site folder: watched too, so an edit to one reloads the pages that use it.
	if err := t.generation.Sources().WatchTemplateTree(filepath.Join(filepath.Dir(base), ".components")); err != nil {
		return nil, err
	}

	presentation := t.presentation().View()
	plan := &RenderPlan{
		base: base,
		ui:   newUISite(base, !presentation.Drive),
	}
	plan.ui.origin = "https://" + t.Domain()
	// a Kitwork UI site's pages read only what their route gives them: a name read and not given is the
	// page's compile error ("router.get không trả về x", ui/given.go)
	plan.ui.site.RouterWork = tree.ui
	// what a route's .ui pages take from the site to build their head; setRoute adds the route's dark look
	uiBase := uiPresentation{drive: presentation.Drive, manifestPath: presentation.ManifestPath, minify: productionMinify}
	var routes []string
	for _, node := range tree.routeNodes() {
		relative := node.relPath()
		routes = append(routes, relative)
		plan.ui.setRoute(relative, node, uiBase)
	}
	// Kitwork UI pages compile now, with the generation, so their stylesheets are in its store before
	// the first request asks for one.
	plan.ui.compileAll(routes)
	return plan, nil
}

// refuseTemplatePages stops a site that still holds a template page (*.kitwork.html): the template
// pages went from the engine on 27/10/2026, and a page that is silently no longer drawn is worse than
// a site that says why it does not start. Every such file is named, from the site's folder. A folder
// that is not there or cannot be read is the walk's error, as it was the template snapshot's: no site
// there is fs.ErrNotExist, which the engine answers as a host with no site.
func refuseTemplatePages(base string) error {
	var found []string
	err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != base && strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir // .database/, .kitwork/: the machine's
			}
			return nil
		}
		if strings.HasSuffix(strings.ToLower(entry.Name()), ".kitwork.html") {
			if relative, err := filepath.Rel(base, path); err == nil {
				found = append(found, filepath.ToSlash(relative))
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(found) == 0 {
		return nil
	}
	sort.Strings(found)
	return fmt.Errorf("%s: trang template (.kitwork.html) không còn được đọc (27/10/2026): trang của một thư mục là content.ui, khung là view.ui, trang không tìm thấy là notfound.ui", strings.Join(found, ", "))
}

func (p *RenderPlan) Close() {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
}
