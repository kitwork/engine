package site

import (
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
	JITConfig   *jitcss.Config
	FaviconFile string
	AssetMounts []AssetMount
	ThemeMode   string
	Frozen      bool
}

// Presentation collects site-wide declarations while a generation is being
// prepared. Activation freezes it so published requests see one coherent
// configuration for the generation's entire lifetime.
type Presentation struct {
	mu sync.RWMutex

	jitConfig   *jitcss.Config
	faviconFile string
	assetMounts []AssetMount
	themeMode   string
	frozen      bool

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

func (p *Presentation) AddAssetMount(mount AssetMount) bool {
	if p == nil || mount.URL == "" || mount.Disk == "" {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.frozen {
		return false
	}
	for _, current := range p.assetMounts {
		if current == mount {
			return true
		}
	}
	p.assetMounts = append(p.assetMounts, mount)
	return true
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
		JITConfig:   cloneJITConfig(p.jitConfig),
		FaviconFile: p.faviconFile,
		AssetMounts: append([]AssetMount(nil), p.assetMounts...),
		ThemeMode:   p.themeMode,
		Frozen:      p.frozen,
	}
}

func clonePresentationSnapshot(source PresentationSnapshot) PresentationSnapshot {
	source.JITConfig = cloneJITConfig(source.JITConfig)
	source.AssetMounts = append([]AssetMount(nil), source.AssetMounts...)
	return source
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
