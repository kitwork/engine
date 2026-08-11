package work

import (
	"fmt"
	"path/filepath"
	"sort"
	"sync"
	"time"

	kitjavascript "github.com/kitwork/engine/jit/javascript"
	"github.com/kitwork/engine/render"
	"github.com/kitwork/engine/site"
	"github.com/kitwork/engine/value"
)

// RenderPlan is the immutable route-to-template plan owned by one site
// generation.
type RenderPlan struct {
	mu sync.RWMutex

	base        string
	snapshot    *render.Snapshot
	renderers   map[string]*plannedRenderer
	kitJSAssets *kitjavascript.AssetStore
	closed      bool
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
	complete := false
	defer func() {
		if !complete {
			plan.Close()
		}
	}()
	if presentation.KitJS {
		plan.kitJSAssets, err = kitjavascript.NewDefaultAssetStore()
		if err != nil {
			return nil, fmt.Errorf("prepare KitJS asset store: %w", err)
		}
	}
	configFor := func(relative string) render.Config {
		return render.Config{
			Base:          base,
			JitConfig:     presentation.JITConfig,
			Directory:     ".",
			Path:          relative,
			Notfound:      "notfound",
			JitCSS:        true,
			DefaultMinify: !AllowLocal,
			ThemeMode:     presentation.ThemeMode,
			KitJSAssets:   plan.kitJSAssets,
			Source:        snapshot,
		}
	}
	if plan.kitJSAssets != nil {
		appScans := make(map[string][]kitjavascript.ScanResult)
		for _, node := range tree.routeNodes() {
			relative := node.relPath()
			config := configFor(relative)
			pageUse, scanErr := render.New(config).ScanKitJS()
			if scanErr != nil {
				return nil, fmt.Errorf("scan KitJS template for route %q: %w", relative, scanErr)
			}
			if pageUse.HasApp {
				appScans[pageUse.App] = append(appScans[pageUse.App], pageUse)
			}

			notfoundConfig := config
			notfoundConfig.NotfoundMode = true
			notfoundUse, scanErr := render.New(notfoundConfig).ScanKitJS()
			if scanErr != nil {
				return nil, fmt.Errorf("scan KitJS notfound template for route %q: %w", relative, scanErr)
			}
			if notfoundUse.HasApp {
				appScans[notfoundUse.App] = append(appScans[notfoundUse.App], notfoundUse)
			}
		}
		identities := make([]string, 0, len(appScans))
		for identity := range appScans {
			identities = append(identities, identity)
		}
		sort.Strings(identities)
		for _, identity := range identities {
			if _, err := plan.kitJSAssets.PrepareAppBundle(appScans[identity]); err != nil {
				return nil, fmt.Errorf("prepare KitJS application graph %q: %w", identity, err)
			}
		}
	}
	for _, node := range tree.routeNodes() {
		relative := node.relPath()
		config := configFor(relative)
		baseRender := render.New(config)
		notfoundConfig := config
		notfoundConfig.NotfoundMode = true
		pageRender := baseRender.Prepare()
		if presentation.KitJS || snapshot.Exists(filepath.Join(base, filepath.FromSlash(relative), "page.kitwork.html")) {
			if err := pageRender.PreparationError(); err != nil {
				return nil, fmt.Errorf("prepare template for route %q: %w", relative, err)
			}
		}
		notfoundRender := render.New(notfoundConfig).Prepare()
		if presentation.KitJS {
			if err := notfoundRender.PreparationError(); err != nil {
				return nil, fmt.Errorf("prepare notfound template for route %q: %w", relative, err)
			}
		}
		plan.renderers[relative] = &plannedRenderer{
			base:     baseRender,
			page:     pageRender,
			notfound: notfoundRender,
			health:   t.runtimeHealth,
		}
	}
	if plan.kitJSAssets != nil {
		if err := plan.kitJSAssets.Freeze(); err != nil {
			return nil, fmt.Errorf("freeze KitJS asset store: %w", err)
		}
	}
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
	kitJSAssets := p.kitJSAssets
	p.snapshot = nil
	p.kitJSAssets = nil
	p.renderers = nil
	p.mu.Unlock()
	if snapshot != nil {
		snapshot.Close()
	}
	if kitJSAssets != nil {
		kitJSAssets.Close()
	}
}

func (p *RenderPlan) kitJSAsset(contentHash string) (kitjavascript.Asset, bool) {
	if p == nil {
		return kitjavascript.Asset{}, false
	}
	p.mu.RLock()
	store := p.kitJSAssets
	closed := p.closed
	p.mu.RUnlock()
	if closed || store == nil {
		return kitjavascript.Asset{}, false
	}
	return store.Lookup(contentHash)
}

func (p *RenderPlan) kitJSEnabled() bool {
	if p == nil {
		return false
	}
	p.mu.RLock()
	enabled := !p.closed && p.kitJSAssets != nil
	p.mu.RUnlock()
	return enabled
}

// ContentAssets implements site.ContentAssetProvider. Activation copies this
// frozen generation snapshot into the bounded site-lifetime CAS before the
// generation becomes current.
func (p *RenderPlan) ContentAssets() ([]site.ContentAsset, error) {
	if p == nil {
		return nil, nil
	}
	p.mu.RLock()
	store := p.kitJSAssets
	closed := p.closed
	p.mu.RUnlock()
	if closed {
		return nil, fmt.Errorf("render plan is closed")
	}
	if store == nil {
		return nil, nil
	}
	assets, err := store.Snapshot()
	if err != nil {
		return nil, err
	}
	contentAssets := make([]site.ContentAsset, 0, len(assets))
	for _, asset := range assets {
		contentAssets = append(contentAssets, site.ContentAsset{
			ContentHash: asset.ContentHash,
			Body:        asset.JavaScript,
		})
	}
	return contentAssets, nil
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
