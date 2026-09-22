package work

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// A site's own browser components live in `_components/<name>.js`:
//
//	apps/<identity>/_components/bid-stepper.js      ← every domain of this identity
//	apps/<identity>/<domain>/_components/search.js  ← this domain only (wins on a name clash)
//
// The underscore and the walk-up are the conventions `_core`, `_cron` and `_queue` already use: a
// folder the machine owns, found by walking up from the site. There is no configuration — dropping
// the file is the declaration, exactly like an icon or a font.
//
// A component file is ordinary browser JavaScript (NOT the Kitwork subset — it never enters the VM)
// and registers itself the way an embedded one does:
//
//	kit.component("bid-stepper", { amount: 0, init(context) { … } });
//
// Markup names it with no version: <div data-kit-component="bid-stepper">. See jit/js/site.go for
// why a site's component has no version while the engine's catalogue does.
//
// Sources are read once per generation and frozen; a request never touches the filesystem. Editing
// a file changes the directory fingerprint the generation watches, so hot reload picks it up.
type siteComponents struct {
	sources map[string]string
	names   []string
	tag     string
}

// componentFileRe is the file name a component may have: the same slug the markup can name.
var componentFileRe = regexp.MustCompile(`^[a-z][a-z0-9-]*\.js$`)

// maxSiteComponentBytes bounds one component file. A browser module this large is a bundle, and a
// bundle belongs in the site's own asset pipeline, not in the shared runtime response.
const maxSiteComponentBytes = 256 * 1024

// Component implements jit/js Site.
func (s *siteComponents) Component(name string) string {
	if s == nil {
		return ""
	}
	return s.sources[name]
}

// Names lists what the site owns, in order — for diagnostics and tests.
func (s *siteComponents) Names() []string {
	if s == nil {
		return nil
	}
	return append([]string(nil), s.names...)
}

// Tag is a content signature over every source, so the cached /kit.js asset of one tenant can never
// be served for another, and an edited component invalidates its own entry.
func (s *siteComponents) Tag() string {
	if s == nil {
		return ""
	}
	return s.tag
}

// componentDirectories returns the _components folders to read, nearest LAST so the nearer one
// overwrites the shared one: identity level first, then the site's own.
func componentDirectories(base string) []string {
	absolute, err := filepath.Abs(base)
	if err != nil {
		return nil
	}
	identity := filepath.Dir(absolute)
	dirs := []string{filepath.Join(identity, "_components")}
	if identity != absolute {
		dirs = append(dirs, filepath.Join(absolute, "_components"))
	}
	return dirs
}

// loadSiteComponents reads every component the site owns. A malformed name or an oversized file is
// a named warning at boot and is left out — never a silent nothing, never a failed generation.
func loadSiteComponents(base string) *siteComponents {
	set := &siteComponents{sources: make(map[string]string)}
	for _, dir := range componentDirectories(base) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // no _components here: the ordinary case
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if !strings.HasSuffix(strings.ToLower(name), ".js") {
				continue
			}
			if !componentFileRe.MatchString(name) {
				fmt.Printf("[components] %s: a component file is named <name>.js in lower case (letters, digits, dashes) — ignored\n", filepath.Join(dir, name))
				continue
			}
			path := filepath.Join(dir, name)
			info, err := entry.Info()
			if err != nil {
				continue
			}
			if info.Size() > maxSiteComponentBytes {
				fmt.Printf("[components] %s: %d bytes is past the %d limit for one component — ignored\n", path, info.Size(), maxSiteComponentBytes)
				continue
			}
			source, err := os.ReadFile(path)
			if err != nil {
				fmt.Printf("[components] %s: %v — ignored\n", path, err)
				continue
			}
			set.sources[strings.TrimSuffix(name, ".js")] = strings.TrimSpace(string(source))
		}
	}
	if len(set.sources) == 0 {
		return nil // nothing owned: the tenant renders exactly as before
	}
	for name := range set.sources {
		set.names = append(set.names, name)
	}
	sort.Strings(set.names)
	hash := sha256.New()
	for _, name := range set.names {
		_, _ = hash.Write([]byte(name))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(set.sources[name]))
		_, _ = hash.Write([]byte{0})
	}
	set.tag = hex.EncodeToString(hash.Sum(nil)[:8])
	return set
}
