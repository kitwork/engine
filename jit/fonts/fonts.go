// Package fonts is Kitwork's web-font engine, a sibling of jit/icons. Self-hosted Google Fonts: the
// woff2 (already subset by Google, e.g. latin / latin-ext) of the engine's own families are vendored
// and embedded, and served from RoutePrefix straight off the embed, hard-cached. No Google at runtime,
// no third-party CDN. (Until 27/10/2026 Render also scanned a template page for the families it used
// and wrote their @font-face; that went with the template pages.)
//
// Any Google font by its name (google.go, url.go, download.go, faces.go): the package carries Google's
// family list (google_catalog.json — Find, Suggest), reads a pasted Google Fonts address (ParseURL),
// downloads a family ONCE from Google into a server-side cache and serves it from RoutePrefix like an
// embedded one (Ensure, FileAt), and writes its @font-face for the weights a page uses (FaceCSS).
// Visitors never contact Google. The default typefaces (DefaultBody, DefaultCode) are embedded, so
// the default works offline.
//
// Fonts are OFL/Apache (see FONTS_LICENSE). Add a family / refresh the files with vendor.py, which
// regenerates catalog_gen.go (the `catalog` map) from Google's CSS; refresh Google's family list with
// google_catalog.py.
package fonts

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"sort"
	"strings"
	"sync"
)

//go:embed families
var FS embed.FS

// RoutePrefix is where the vendored woff2 are served from (the engine maps it onto FS). It sits
// under /_kitwork/, the one prefix everything the engine serves lives under; the @font-face `src`
// and the preload links both point here.
//
// Each file is named by its face and its bytes, flat in the folder, like every file the engine
// serves (<role>.<hash>.<ext>):
//
//	/_kitwork/fonts/outfit-400-latin.9c1e0b7a44d2f816.woff2
//	/_kitwork/fonts/<family>-<weight>-<subset>[-<style>].<hash>.woff2
//
// The face part is the vendored file's own name (families/<family>/<weight>-<subset>[-<style>].woff2,
// vendor.py), so an italic — or any axis vendor.py ever writes into a file name — gets its word in
// the served name the same way. The hash is what makes keeping the file forever safe: re-vendor a
// face and its name changes.
const RoutePrefix = "/_kitwork/fonts/"

// LegacyRoutePrefix is where they were served until 11/10/2026, as /jitfonts/<family>/<weight>-
// <subset>.woff2. The engine still answers it — and the same unhashed form under RoutePrefix,
// which a stopped run wrote for a while — with today's file, never as its exact name (FileAt).
const LegacyRoutePrefix = "/jitfonts/"

// hashLength is how much of the SHA-256 a served name carries, as for the engine's JavaScript: 64
// bits tell two versions of one face apart.
const hashLength = 16

// File is one served woff2.
type File struct {
	Path   string // the URL: /_kitwork/fonts/<face>.<hash>.woff2
	Body   []byte
	Digest string // the whole SHA-256, hex — the ETag
}

var (
	filesOnce  sync.Once
	fileByFS   map[string]*File // "families/outfit/400-latin.woff2" → its file
	fileByFace map[string]*File // "outfit-400-latin" → its file
)

// files names every face of the catalog by its bytes, once.
func files() {
	filesOnce.Do(func() {
		fileByFS = map[string]*File{}
		fileByFace = map[string]*File{}
		for slug, fam := range catalog {
			for _, f := range fam.faces {
				body, err := FS.ReadFile(f.file)
				if err != nil {
					continue
				}
				sum := sha256.Sum256(body)
				digest := hex.EncodeToString(sum[:])
				face := slug + "-" + strings.TrimSuffix(f.file[strings.LastIndexByte(f.file, '/')+1:], ".woff2")
				file := &File{Path: RoutePrefix + face + "." + digest[:hashLength] + ".woff2", Body: body, Digest: digest}
				fileByFS[f.file] = file
				fileByFace[face] = file
			}
		}
	})
}

// FileAt is the woff2 a path names. exact says whether the path is that file's current name; when
// it is not — an old hash, /jitfonts/<family>/<weight>-<subset>.woff2, or that unhashed form under
// RoutePrefix — the file is still today's, and the caller must not let it be cached under the old
// name. nil when the path names no face: only a catalog face can ever be answered, so a path cannot
// reach anything else in FS.
//
// An embedded face is found first; then a face of a family downloaded into the cache (Ensure), under
// the same name form — /_kitwork/fonts/itim-400-latin.<hash>.woff2 — and only by its hashed name.
// The path is only ever a key: the file read is the one the family's manifest names (cachedFileAt).
func FileAt(path string) (file *File, exact bool) {
	files()
	rest, current := strings.CutPrefix(path, RoutePrefix)
	if !current {
		var legacy bool
		if rest, legacy = strings.CutPrefix(path, LegacyRoutePrefix); !legacy {
			return nil, false
		}
	}
	if len(rest) > 160 || !strings.HasSuffix(rest, ".woff2") {
		return nil, false
	}
	if strings.Contains(rest, "/") {
		// <family>/<weight>-<subset>.woff2: the vendored file's own path, as the names before were.
		if file = fileByFS["families/"+rest]; file == nil {
			return nil, false
		}
		return file, false
	}
	if !current {
		return nil, false
	}
	name := strings.TrimSuffix(rest, ".woff2")
	dot := strings.LastIndexByte(name, '.')
	if dot < 0 || len(name)-dot-1 != hashLength {
		return nil, false
	}
	if file = fileByFace[name[:dot]]; file == nil {
		return cachedFileAt(name[:dot], path)
	}
	return file, file.Path == path
}

// face is one weight×style×subset of a family — a single vendored woff2. weight is the CSS numeric
// weight; file is the path inside FS; unicode is the subset's unicode-range (may be ""). through is
// the heavy end of a face that is a whole weight range — Google's answer to a range request
// (wght@1..1000) is one file per subset, "font-weight: 1 1000", vendored as weight 1, through 1000
// and the file <family>/1-1000-<subset>.woff2; 0 for a face of one weight.
type face struct {
	weight  int
	through int
	style   string
	subset  string
	file    string
	unicode string
}

// family is a vendored font: its CSS family name, a fallback stack, and the faces we ship. weights
// is the weight axis of a variable font, low and high: Google serves one file per subset for every
// weight of such a family, so any weight in the range is that file (vendor.py writes the range).
// Zero for a family with one file per weight. A Kitwork UI page reaches every embedded family by its
// name through Find, FaceCSS, PreloadFace and FileAt.
type family struct {
	name     string
	fallback string
	weights  [2]int
	faces    []face
}

// catalog (slug → family) is generated in catalog_gen.go by vendor.py.

// Has reports whether slug is a vendored family.
func Has(slug string) bool { _, ok := catalog[slug]; return ok }

// Names lists every vendored family slug, sorted (for a gallery / docs).
func Names() []string {
	out := make([]string, 0, len(catalog))
	for s := range catalog {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// routePath turns an FS path ("families/outfit/400-latin.woff2") into its served URL
// ("/_kitwork/fonts/outfit-400-latin.<hash>.woff2"), or "" for a face whose file is not embedded.
func routePath(fsPath string) string {
	files()
	if file := fileByFS[fsPath]; file != nil {
		return file.Path
	}
	return ""
}
