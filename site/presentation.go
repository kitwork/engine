package site

import (
	"fmt"
	"sync"
)

// AssetMount maps one public URL prefix to a site-relative disk prefix.
type AssetMount struct {
	URL  string
	Disk string
}

// PresentationSnapshot is the immutable presentation state consumed by one
// request. Its slices are detached from the builder.
type PresentationSnapshot struct {
	FaviconFile  string
	AssetMounts  []AssetMount
	ManifestPath string
	// Drive is router.reload(false): a link fetches the next page and swaps it in place
	Drive  bool
	Frozen bool
}

// Presentation collects site-wide declarations while a generation is being
// prepared. Activation freezes it so published requests see one coherent
// configuration for the generation's entire lifetime. Until 27/10/2026 it also
// carried the template pages' CSS configuration (router.css, router.themes, the
// highlight theme); those went with the template pages.
type Presentation struct {
	mu sync.RWMutex

	faviconFile  string
	assetMounts  []AssetMount
	manifestPath string
	drive        bool
	frozen       bool

	frozenSnapshot PresentationSnapshot
}

func newPresentation() *Presentation {
	return &Presentation{}
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

// SetManifestPath records where router.manifest() publishes, so a .ui page's head
// links it. Empty means the site declared no manifest and no link is injected — a
// link to a path that 404s is worse than no link.
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

// SetDrive opts the generation into Kitwork Drive: same-origin links and GET forms are fetched and
// morphed in place instead of reloading the page. router.reload(false) sets it, router.reload(true)
// clears it.
//
// The builder starts OFF. A Kitwork UI site starts ON (the router sets it before router.work runs).
// It used to switch itself on wherever a root marker appeared, which meant the decision rode on an
// attribute authors wrote for a different reason entirely.
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
// Callers must never mutate its asset mounts.
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
		FaviconFile:  p.faviconFile,
		AssetMounts:  append([]AssetMount(nil), p.assetMounts...),
		ManifestPath: p.manifestPath,
		Drive:        p.drive,
		Frozen:       p.frozen,
	}
}

func clonePresentationSnapshot(source PresentationSnapshot) PresentationSnapshot {
	source.AssetMounts = append([]AssetMount(nil), source.AssetMounts...)
	return source
}
