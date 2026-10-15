package site

import (
	"fmt"
	"sync"

	jitcss "github.com/kitwork/engine/jit/css"
)

// AssetMount maps one public URL prefix to a site-relative disk prefix.
type AssetMount struct {
	URL  string
	Disk string
}

// PresentationSnapshot is the immutable presentation state consumed by one
// request. Slices, maps, and the JIT config are detached from the builder.
type PresentationSnapshot struct {
	JITConfig        *jitcss.Config
	FaviconFile      string
	AssetMounts      []AssetMount
	ThemeMode        string
	Themes           []string // appearance modes router.themes() asked the engine to derive
	HighlightTheme   string
	HighlightPalette map[string]string
	ManifestPath     string
	// Drive is router.reload(false): a link fetches the next page and swaps it in place
	Drive  bool
	Frozen bool
}

// Presentation collects site-wide declarations while a generation is being
// prepared. Activation freezes it so published requests see one coherent
// configuration for the generation's entire lifetime.
type Presentation struct {
	mu sync.RWMutex

	jitConfig        *jitcss.Config
	faviconFile      string
	assetMounts      []AssetMount
	themeMode        string
	themes           []string
	highlightTheme   string
	highlightPalette map[string]string
	manifestPath     string
	drive            bool
	frozen           bool

	frozenSnapshot PresentationSnapshot
}

func newPresentation() *Presentation {
	return &Presentation{}
}

func (p *Presentation) SetJITConfig(config *jitcss.Config) bool {
	if p == nil || config == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.frozen {
		return false
	}
	p.jitConfig = cloneJITConfig(config)
	return true
}

func (p *Presentation) SetFaviconFile(filename string) bool {
	if p == nil || filename == "" {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.frozen {
		return false
	}
	p.faviconFile = filename
	return true
}

// AddAssetMount registers one public-URL→disk mount. It is idempotent (an identical mount is a
// no-op) and FAILS CLOSED on a conflict: the same public URL prefix pointing at a different disk
// dir — e.g. declaring both "_assets" (→ /assets) and "assets" (→ /assets) — which would make
// static serving non-deterministic. Returns nil when added, deduped, empty or frozen; an error on
// conflict so generation can reject the tenant instead of silently shadowing one folder.
func (p *Presentation) AddAssetMount(mount AssetMount) error {
	if p == nil || mount.URL == "" || mount.Disk == "" {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.frozen {
		return nil
	}
	for _, current := range p.assetMounts {
		if current == mount {
			return nil // idempotent under hot-reload
		}
		if current.URL == mount.URL {
			return fmt.Errorf("asset mount conflict: url %q maps to both disk %q and %q", mount.URL, current.Disk, mount.Disk)
		}
	}
	p.assetMounts = append(p.assetMounts, mount)
	return nil
}

func (p *Presentation) SetThemeMode(mode string) bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.frozen {
		return false
	}
	p.themeMode = mode
	return true
}

// SetThemes records the appearance modes router.themes() asked for (and the dark a { light, dark }
// pair in the site root's router.colors() declares). Like highlight, the modes ride the JIT config at
// snapshot time, so router.css() and router.themes() may be written in either order. The pre-paint
// follows from what the site declared (see resolvedThemeMode).
func (p *Presentation) SetThemes(modes []string) bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.frozen {
		return false
	}
	p.themes = append([]string(nil), modes...)
	return true
}

// resolvedThemeMode is the anti-flash decision, made from what the site declared rather than a
// switch it sets: an explicit router.themes({ prepaint: false }) (or the deprecated jittheme)
// wins; otherwise a site with a derived mode (router.themes({ dark: true })) or a class-switched
// darkMode (router.css({ darkMode: ["class"] })) pre-paints EVERY page — a dark skin that flashes
// light on load is not a dark skin, and a page without the toggle must not flash either. A site
// that declared neither falls back to the per-page scan for a toggle ("").
func (p *Presentation) resolvedThemeMode() string {
	if p.themeMode != "" {
		return p.themeMode
	}
	if len(p.themes) > 0 || (p.jitConfig != nil && p.jitConfig.DarkByClass) {
		return "force"
	}
	return ""
}

// SetHighlightPalette records the per-role overrides from
// router.highlight({ keyword: … }). They sit between the named theme and the
// site's own router.css() declarations.
func (p *Presentation) SetHighlightPalette(palette map[string]string) bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.frozen {
		return false
	}
	if len(palette) == 0 {
		p.highlightPalette = nil
		return true
	}
	copied := make(map[string]string, len(palette))
	for role, hex := range palette {
		copied[role] = hex
	}
	p.highlightPalette = copied
	return true
}

// SetManifestPath records where router.manifest() publishes, so the render pass
// can link it from <head>. Empty means the site declared no manifest and no
// link is injected — a link to a path that 404s is worse than no link.
func (p *Presentation) SetManifestPath(path string) bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.frozen {
		return false
	}
	p.manifestPath = path
	return true
}

// SetHighlightTheme pins the default palette for a bare {{ src.highlight() }}
// across the site generation (router.highlight). A page may still override per
// call with {{ src.highlight("mono") }}.
func (p *Presentation) SetHighlightTheme(theme string) bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.frozen {
		return false
	}
	p.highlightTheme = theme
	return true
}

// SetDrive opts the generation into Kitwork Drive: same-origin links and GET forms are fetched and
// morphed in place instead of reloading the page. router.reload(false) sets it, router.reload(true)
// clears it.
//
// The builder starts OFF, which is a template site's default: Drive changes what a visitor SEES
// happen when they click, so such a site says yes to it deliberately. A Kitwork UI site starts ON
// (the router sets it before router.work runs). It used to switch itself on wherever a root marker
// appeared, which meant the decision rode on an attribute authors wrote for a different reason
// entirely.
func (p *Presentation) SetDrive(enabled bool) bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.frozen {
		return false
	}
	p.drive = enabled
	return true
}

// Freeze prevents further mutations. It is idempotent.
func (p *Presentation) Freeze() {
	if p == nil {
		return
	}
	p.mu.Lock()
	if !p.frozen {
		p.frozen = true
		p.frozenSnapshot = p.snapshotLocked()
		p.frozenSnapshot.Frozen = true
	}
	p.mu.Unlock()
}

// View returns the generation's read-only frozen view without allocating.
// Callers must never mutate its JIT maps, slices, or asset mounts.
func (p *Presentation) View() PresentationSnapshot {
	if p == nil {
		return PresentationSnapshot{}
	}
	p.mu.RLock()
	if p.frozen {
		snapshot := p.frozenSnapshot
		p.mu.RUnlock()
		return snapshot
	}
	snapshot := p.snapshotLocked()
	p.mu.RUnlock()
	return snapshot
}

// AssetMounts returns a detached preparation-time mount list.
func (p *Presentation) AssetMounts() []AssetMount {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	mounts := append([]AssetMount(nil), p.assetMounts...)
	p.mu.RUnlock()
	return mounts
}

func (p *Presentation) Snapshot() PresentationSnapshot {
	if p == nil {
		return PresentationSnapshot{}
	}
	p.mu.RLock()
	source := p.snapshotLocked()
	if p.frozen {
		source = p.frozenSnapshot
	}
	snapshot := clonePresentationSnapshot(source)
	p.mu.RUnlock()
	return snapshot
}

func (p *Presentation) snapshotLocked() PresentationSnapshot {
	// router.highlight() rides the JIT config so colour resolution can see it.
	// Stamping it here rather than at call time means router.css() and
	// router.highlight() can be written in either order without one erasing the
	// other — buildJitcssConfig rebuilds from defaults every time it runs.
	jitConfig := cloneJITConfig(p.jitConfig)
	if jitConfig == nil && (p.highlightTheme != "" || len(p.highlightPalette) > 0) {
		jitConfig = cloneJITConfig(&jitcss.DefaultConfig)
	}
	if jitConfig == nil && len(p.themes) > 0 {
		jitConfig = cloneJITConfig(&jitcss.DefaultConfig)
	}
	if jitConfig != nil {
		jitConfig.HighlightTheme = p.highlightTheme
		jitConfig.HighlightPalette = p.highlightPalette
		// router.themes(): derive each mode's block from the tokens router.css() declared. The
		// warnings for a mode the engine cannot derive were already printed by the router.
		jitcss.DeriveThemes(jitConfig, p.themes...)
	}

	return PresentationSnapshot{
		JITConfig:        jitConfig,
		FaviconFile:      p.faviconFile,
		AssetMounts:      append([]AssetMount(nil), p.assetMounts...),
		ThemeMode:        p.resolvedThemeMode(),
		Themes:           append([]string(nil), p.themes...),
		HighlightTheme:   p.highlightTheme,
		HighlightPalette: p.highlightPalette,
		ManifestPath:     p.manifestPath,
		Drive:            p.drive,
		Frozen:           p.frozen,
	}
}

func clonePresentationSnapshot(source PresentationSnapshot) PresentationSnapshot {
	source.JITConfig = cloneJITConfig(source.JITConfig)
	source.AssetMounts = append([]AssetMount(nil), source.AssetMounts...)
	return source
}

// CloneJITConfig is a copy of a CSS configuration that shares no map or slice with it: the render
// plan merges a branch's router.colors() and router.fonts() over the site's without touching the
// site's.
func CloneJITConfig(config *jitcss.Config) *jitcss.Config { return cloneJITConfig(config) }

func cloneJITConfig(config *jitcss.Config) *jitcss.Config {
	if config == nil {
		return nil
	}
	clone := *config
	clone.Colors = cloneMap(config.Colors)
	clone.Order = append([]string(nil), config.Order...)
	clone.MediaQueries = cloneMap(config.MediaQueries)
	clone.States = cloneMap(config.States)
	clone.ShadowLevels = cloneMap(config.ShadowLevels)
	clone.FontFamily = cloneMap(config.FontFamily)
	clone.HighlightPalette = cloneMap(config.HighlightPalette)
	clone.Scale = append([]int(nil), config.Scale...)
	clone.AlphaScales = append([]int(nil), config.AlphaScales...)
	clone.Opacities = append([]int(nil), config.Opacities...)
	clone.ZIndices = append([]int(nil), config.ZIndices...)
	clone.Animations = cloneMap(config.Animations)
	clone.Keyframes = cloneMap(config.Keyframes)
	if config.Themes != nil {
		clone.Themes = make(map[string]map[string]jitcss.Color, len(config.Themes))
		for mode, colors := range config.Themes {
			clone.Themes[mode] = cloneMap(colors)
		}
	}
	return &clone
}

func cloneMap[K comparable, V any](source map[K]V) map[K]V {
	if source == nil {
		return nil
	}
	clone := make(map[K]V, len(source))
	for key, item := range source {
		clone[key] = item
	}
	return clone
}
