package work

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	jitjs "github.com/kitwork/engine/jit/js"
	"github.com/kitwork/engine/render"
	"github.com/kitwork/engine/value"
)

// RenderPlan is the immutable route-to-template plan owned by one site
// generation.
type RenderPlan struct {
	mu sync.RWMutex

	base           string
	snapshot       *render.Snapshot
	renderers      map[string]*plannedRenderer
	siteComponents *siteComponents
	ui             *uiSite // Kitwork UI pages (*.ui) with their heads and stylesheets, compiled here
	closed         bool
}

// siteComponentsOrNil hands render a typed nil-free value: a tenant owning no component passes no
// site at all, so its pages render exactly as they did before this existed.
func (p *RenderPlan) siteComponentsOrNil() jitjs.Site {
	if p == nil || p.siteComponents == nil {
		return nil
	}
	return p.siteComponents
}

// SiteComponents is the tenant's frozen component set (nil when it owns none) — the /kit.js route
// resolves a component:<name> the embedded catalogue does not have against it.
func (p *RenderPlan) SiteComponents() *siteComponents {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.siteComponents
}

type plannedRenderer struct {
	base     *render.Render
	page     *render.Render
	notfound *render.Render
	health   *RuntimeHealth
}

func newRenderPlan(t *Tenant, tree *RouteTree) (*RenderPlan, error) {
	if t == nil || tree == nil {
		return nil, fmt.Errorf("render plan requires a tenant and route graph")
	}
	productionMinify := !AllowLocal || Minify // MINIFY=true: production's bytes while developing
	base := t.resolve()
	snapshot, err := render.NewSnapshot(base)
	if err != nil {
		return nil, err
	}
	if err := t.generation.Sources().WatchTemplateTree(base); err != nil {
		snapshot.Close()
		return nil, err
	}
	// The identity's components (.components/<name>.work and .ui) are shared by its sites and lie
	// outside the site folder: watched too, so an edit to one reloads the pages that use it.
	if err := t.generation.Sources().WatchTemplateTree(filepath.Join(filepath.Dir(base), ".components")); err != nil {
		snapshot.Close()
		return nil, err
	}

	presentation := t.presentation().View()
	// The site's own browser components (_components/<name>.js, this domain then the identity it
	// belongs to) are read once here and frozen for the generation; watching the folders makes an
	// edit reload the site like a template edit does.
	siteComponents := loadSiteComponents(base)
	for _, directory := range componentDirectories(base) {
		if err := t.generation.Sources().WatchDirectory(directory); err != nil {
			snapshot.Close()
			return nil, err
		}
	}
	plan := &RenderPlan{
		base:           base,
		snapshot:       snapshot,
		siteComponents: siteComponents,
		ui:             newUISite(base, !presentation.Drive),
		renderers:      make(map[string]*plannedRenderer),
	}
	plan.ui.origin = "https://" + t.Domain()
	complete := false
	defer func() {
		if !complete {
			plan.Close()
		}
	}()
	// A {{ expression }} of a template page that fails writes its message into the page while
	// developing; in production the page writes nothing there and the message goes to the log
	// (render.Config.ExpressionFailed), as a 500 shows a visitor only its status and request id.
	var expressionFailed func(expression, message string)
	if !AllowLocal {
		expressionFailed = func(expression, message string) {
			slog.Error("Kitwork template expression failed", "app", t.AppID(), "site", t.Domain(), "expression", expression, "message", message)
		}
	}
	configFor := func(relative string) render.Config {
		return render.Config{
			Base:             base,
			JitConfig:        presentation.JITConfig,
			Directory:        ".",
			Path:             relative,
			Notfound:         "notfound",
			JitCSS:           true,
			DefaultMinify:    productionMinify,
			ThemeMode:        presentation.ThemeMode,
			Drive:            presentation.Drive,
			HighlightTheme:   presentation.HighlightTheme,
			ManifestPath:     presentation.ManifestPath,
			HighlightPalette: presentation.HighlightPalette,
			SiteComponents:   plan.siteComponentsOrNil(),
			Source:           snapshot,
			ExpressionFailed: expressionFailed,
		}
	}
	var routes []string
	for _, node := range tree.routeNodes() {
		relative := node.relPath()
		routes = append(routes, relative)
		config := configFor(relative)
		// router.colors()/fonts() of the folders down to this route, over the site's; router.appearance()
		// of the nearest folder that says. A dark look the site did not have (a branch's dark pair, a
		// route shown dark first) pre-paints too, unless the site opted out.
		css, dark := routeCSS(presentation.JITConfig, node, routeAppearance(node))
		config.JitConfig = css
		if dark && config.ThemeMode == "" {
			config.ThemeMode = "force"
		}
		baseRender := render.New(config)
		plan.ui.setRoute(relative, node, baseRender)
		notfoundConfig := config
		notfoundConfig.NotfoundMode = true
		pageRender := baseRender.Prepare()
		if snapshot.Exists(filepath.Join(base, filepath.FromSlash(relative), "page.kitwork.html")) {
			if err := pageRender.PreparationError(); err != nil {
				return nil, fmt.Errorf("prepare template for route %q: %w", relative, err)
			}
		}
		notfoundRender := render.New(notfoundConfig).Prepare()
		plan.renderers[relative] = &plannedRenderer{
			base:     baseRender,
			page:     pageRender,
			notfound: notfoundRender,
			health:   t.runtimeHealth,
		}
	}
	// Kitwork UI pages compile now, with the generation, so their stylesheets are in its store before
	// the first request asks for one.
	plan.ui.compileAll(routes)
	complete = true
	return plan, nil
}

func (p *RenderPlan) renderer(relative string) *plannedRenderer {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	renderer := p.renderers[relative]
	closed := p.closed
	p.mu.RUnlock()
	if closed {
		return nil
	}
	return renderer
}

func (p *RenderPlan) hasPage(relative string) bool {
	if p == nil {
		return false
	}
	p.mu.RLock()
	snapshot := p.snapshot
	base := p.base
	closed := p.closed
	p.mu.RUnlock()
	if closed || snapshot == nil {
		return false
	}
	return snapshot.Exists(filepath.Join(base, filepath.FromSlash(relative), "page.kitwork.html"))
}

func (p *RenderPlan) Close() {
	if p == nil {
		return
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	snapshot := p.snapshot
	p.snapshot = nil
	p.renderers = nil
	p.mu.Unlock()
	if snapshot != nil {
		snapshot.Close()
	}
}

func (r *plannedRenderer) BindPage(page string, notfound bool, data value.Value) value.Value {
	if r == nil {
		return value.New("")
	}
	if r.health == nil {
		return r.bindPage(page, notfound, data)
	}
	started := time.Now()
	output := r.bindPage(page, notfound, data)
	prepared := page == "" && ((!notfound && r.page.PresentationPrepared()) ||
		(notfound && r.notfound.PresentationPrepared()))
	r.health.RecordRender(time.Since(started), prepared)
	return output
}

func (r *plannedRenderer) bindPage(page string, notfound bool, data value.Value) value.Value {
	if page == "" {
		if notfound {
			return r.notfound.Bind(data)
		}
		return r.page.Bind(data)
	}
	return r.base.BindPage(page, notfound, data)
}
