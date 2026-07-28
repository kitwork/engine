package work

import (
	"fmt"
	"path/filepath"
	"sync"

	"github.com/kitwork/engine/render"
	"github.com/kitwork/engine/value"
)

// RenderPlan is the immutable route-to-template plan owned by one site
// generation.
type RenderPlan struct {
	mu sync.RWMutex

	base      string
	snapshot  *render.Snapshot
	renderers map[string]*plannedRenderer
	closed    bool
}

type plannedRenderer struct {
	base     *render.Render
	page     *render.Render
	notfound *render.Render
}

func newRenderPlan(t *Tenant, tree *RouteTree) (*RenderPlan, error) {
	if t == nil || tree == nil {
		return nil, fmt.Errorf("render plan requires a tenant and route graph")
	}
	base := t.resolve()
	snapshot, err := render.NewSnapshot(base)
	if err != nil {
		return nil, err
	}
	if err := t.generation.Sources().WatchTemplateTree(base); err != nil {
		snapshot.Close()
		return nil, err
	}

	presentation := t.presentation().View()
	plan := &RenderPlan{
		base:      base,
		snapshot:  snapshot,
		renderers: make(map[string]*plannedRenderer),
	}
	for _, node := range tree.routeNodes() {
		relative := node.relPath()
		config := render.Config{
			Base:          base,
			JitConfig:     presentation.JITConfig,
			Directory:     ".",
			Path:          relative,
			Notfound:      "notfound",
			JitCSS:        true,
			DefaultMinify: !AllowLocal,
			ThemeMode:     presentation.ThemeMode,
			Source:        snapshot,
		}
		baseRender := render.New(config)
		notfoundConfig := config
		notfoundConfig.NotfoundMode = true
		pageRender := baseRender.Prepare()
		if snapshot.Exists(filepath.Join(base, filepath.FromSlash(relative), "page.kitwork.html")) {
			if err := pageRender.PreparationError(); err != nil {
				snapshot.Close()
				return nil, fmt.Errorf("prepare template for route %q: %w", relative, err)
			}
		}
		plan.renderers[relative] = &plannedRenderer{
			base:     baseRender,
			page:     pageRender,
			notfound: render.New(notfoundConfig).Prepare(),
		}
	}
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
	if page == "" {
		if notfound {
			return r.notfound.Bind(data)
		}
		return r.page.Bind(data)
	}
	return r.base.BindPage(page, notfound, data)
}
