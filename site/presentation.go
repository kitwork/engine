package site

import (
	"errors"
	"fmt"
	"sync"

	jitcss "github.com/kitwork/engine/jit/css"
)

const (
	// MaxJITComponentSources bounds the generation-owned tenant component
	// catalog. The staged browser cache has the same component ceiling.
	MaxJITComponentSources = 256
	// MaxJITComponentSourceBytes bounds one snapshotted tenant component before
	// it can allocate generation-owned memory.
	MaxJITComponentSourceBytes = 1 << 20
	// MaxJITComponentTotalBytes keeps declarations from consuming the complete
	// staged asset-store budget before any engine package is assembled.
	MaxJITComponentTotalBytes = 16 << 20
)

var (
	ErrInvalidJITComponent   = errors.New("site: invalid JIT component declaration")
	ErrDuplicateJITComponent = errors.New("site: duplicate JIT component declaration")
	ErrJITComponentCapacity  = errors.New("site: JIT component catalog capacity exceeded")
)

// AssetMount maps one public URL prefix to a site-relative disk prefix.
type AssetMount struct {
	URL  string
	Disk string
}

// JITComponentSource is a detached, generation-scoped tenant component. Site
// deliberately stores only neutral identity and bytes so it does not import
// the JavaScript delivery package.
type JITComponentSource struct {
	Name       string
	Version    string
	Filename   string
	JavaScript []byte
}

// PresentationSnapshot is the immutable presentation state consumed by one
// request. Slices, maps, and the JIT config are detached from the builder.
type PresentationSnapshot struct {
	JITConfig     *jitcss.Config
	FaviconFile   string
	AssetMounts   []AssetMount
	ThemeMode     string
	KitJS         bool
	JITComponents []JITComponentSource
	Frozen        bool
}

// Presentation collects site-wide declarations while a generation is being
// prepared. Activation freezes it so published requests see one coherent
// configuration for the generation's entire lifetime.
type Presentation struct {
	mu sync.RWMutex

	jitConfig         *jitcss.Config
	faviconFile       string
	assetMounts       []AssetMount
	themeMode         string
	kitJS             bool
	jitComponents     []JITComponentSource
	jitComponentBytes int
	frozen            bool

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

// SetKitJS opts this complete site generation into the component-first KitJS
// composer. The default is false so existing tenants retain the legacy
// jit/hydrate pipeline until they migrate deliberately.
func (p *Presentation) SetKitJS(enabled bool) bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.frozen {
		return false
	}
	p.kitJS = enabled
	return true
}

// AddJITComponent adds one exact tenant-managed component to the pending
// generation. A name can have only one declaration and one version, keeping
// the unversioned HTML form deterministic.
func (p *Presentation) AddJITComponent(component JITComponentSource) error {
	if p == nil || component.Name == "" || component.Version == "" || component.Filename == "" || len(component.JavaScript) == 0 {
		return ErrInvalidJITComponent
	}
	if len(component.JavaScript) > MaxJITComponentSourceBytes {
		return fmt.Errorf("%w: component %q has %d bytes (limit %d)", ErrJITComponentCapacity,
			component.Name, len(component.JavaScript), MaxJITComponentSourceBytes)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.frozen {
		return fmt.Errorf("%w: presentation is frozen", ErrInvalidJITComponent)
	}
	for _, current := range p.jitComponents {
		if current.Name == component.Name {
			return fmt.Errorf("%w: component %q is already declared as %s from %q",
				ErrDuplicateJITComponent, component.Name, current.Version, current.Filename)
		}
	}
	if len(p.jitComponents) >= MaxJITComponentSources ||
		p.jitComponentBytes+len(component.JavaScript) > MaxJITComponentTotalBytes {
		return fmt.Errorf("%w: components=%d/%d bytes=%d/%d", ErrJITComponentCapacity,
			len(p.jitComponents)+1, MaxJITComponentSources,
			p.jitComponentBytes+len(component.JavaScript), MaxJITComponentTotalBytes)
	}
	component.JavaScript = append([]byte(nil), component.JavaScript...)
	p.jitComponents = append(p.jitComponents, component)
	p.jitComponentBytes += len(component.JavaScript)
	return nil
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
	return PresentationSnapshot{
		JITConfig:     cloneJITConfig(p.jitConfig),
		FaviconFile:   p.faviconFile,
		AssetMounts:   append([]AssetMount(nil), p.assetMounts...),
		ThemeMode:     p.themeMode,
		KitJS:         p.kitJS,
		JITComponents: cloneJITComponentSources(p.jitComponents),
		Frozen:        p.frozen,
	}
}

func clonePresentationSnapshot(source PresentationSnapshot) PresentationSnapshot {
	source.JITConfig = cloneJITConfig(source.JITConfig)
	source.AssetMounts = append([]AssetMount(nil), source.AssetMounts...)
	source.JITComponents = cloneJITComponentSources(source.JITComponents)
	return source
}

func cloneJITComponentSources(source []JITComponentSource) []JITComponentSource {
	if source == nil {
		return nil
	}
	cloned := make([]JITComponentSource, len(source))
	for index, component := range source {
		component.JavaScript = append([]byte(nil), component.JavaScript...)
		cloned[index] = component
	}
	return cloned
}

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
	clone.Scale = append([]int(nil), config.Scale...)
	clone.AlphaScales = append([]int(nil), config.AlphaScales...)
	clone.Opacities = append([]int(nil), config.Opacities...)
	clone.ZIndices = append([]int(nil), config.ZIndices...)
	clone.Animations = cloneMap(config.Animations)
	clone.Keyframes = cloneMap(config.Keyframes)
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
