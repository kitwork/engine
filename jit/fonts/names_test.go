package fonts

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// servedName is /_kitwork/fonts/<family>-<weight>-<subset>[-<style>].<hash>.woff2: flat in the
// folder, the face in words, the bytes in the hash. The weight of a face that is a whole range is
// both ends: google-sans-flex-1-1000-latin.
var servedName = regexp.MustCompile(`^/_kitwork/fonts/([a-z0-9-]+?)-([0-9]+(?:-[0-9]+)?)-([a-z-]+)\.([0-9a-f]{16})\.woff2$`)

// Every face is served under a name that says which face and which bytes: the family, weight and
// subset in words, then the first 64 bits of the SHA-256 of the file. The name resolves back to that
// very file, as its exact name.
func TestEachFaceIsNamedByItsFaceAndItsBytes(t *testing.T) {
	seen := map[string]string{}
	faces := 0
	for slug, fam := range catalog {
		for _, f := range fam.faces {
			faces++
			path := routePath(f.file)
			m := servedName.FindStringSubmatch(path)
			if m == nil {
				t.Errorf("%s is served as %q, want /_kitwork/fonts/<family>-<weight>-<subset>.<hash>.woff2", f.file, path)
				continue
			}
			subset := f.subset
			if f.style != "normal" {
				subset += "-" + f.style
			}
			weight := strconv.Itoa(f.weight)
			if f.through > 0 {
				weight += "-" + strconv.Itoa(f.through)
			}
			if m[1] != slug || m[2] != weight || m[3] != subset {
				t.Errorf("%s is served as %s: want family %s, weight %s, subset %s", f.file, path, slug, weight, subset)
			}
			body, err := FS.ReadFile(f.file)
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(body)
			if m[4] != hex.EncodeToString(sum[:])[:16] {
				t.Errorf("%s: the hash in the name is not the hash of the file's bytes", path)
			}
			file, exact := FileAt(path)
			if file == nil || !exact || !bytes.Equal(file.Body, body) || file.Digest != hex.EncodeToString(sum[:]) {
				t.Errorf("%s does not resolve back to its own file, exactly", path)
			}
			if other, taken := seen[path]; taken {
				t.Errorf("%s names two faces: %s and %s", path, other, f.file)
			}
			seen[path] = f.file
		}
	}
	if faces < 10 {
		t.Fatalf("read %d faces: the catalog was not where this test looks", faces)
	}
}

// What a page writes points at those names: each @font-face src and the preload link are the
// served names, and nothing on the page names the vendored folder layout any more.
func TestThePageNamesTheHashedFiles(t *testing.T) {
	out := Render(`<html><head></head><body><p class="font-outfit">x</p></body></html>`)
	var named []string
	for _, m := range regexp.MustCompile(`src:url\(([^)]+)\)`).FindAllStringSubmatch(out, -1) {
		named = append(named, m[1])
	}
	if len(named) != len(catalog["outfit"].faces) {
		t.Fatalf("want one src per Outfit face (%d), got %d: %s", len(catalog["outfit"].faces), len(named), out)
	}
	preload := regexp.MustCompile(`<link rel="preload" as="font" type="font/woff2" href="([^"]+)" crossorigin>`).FindStringSubmatch(out)
	if preload == nil {
		t.Fatalf("no preload link: %s", out)
	}
	named = append(named, preload[1])
	for _, path := range named {
		if file, exact := FileAt(path); file == nil || !exact || !servedName.MatchString(path) {
			t.Errorf("%s is not the exact served name of a face", path)
		}
	}
	if want := routePath("families/outfit/400-latin.woff2"); preload[1] != want {
		t.Errorf("the preload is the 400 latin face %s, got %s", want, preload[1])
	}
	if strings.Contains(out, "/jitfonts/") || strings.Contains(out, "/outfit/") {
		t.Errorf("the page still names the old layout: %s", out)
	}
}

// The names written before still find their file — /jitfonts/<family>/<weight>-<subset>.woff2, the
// same unhashed form under /_kitwork/fonts/ that a stopped run wrote, and an old hash — but never as
// their exact name, so the server marks them not to be kept. CONTROL: names that mean no face — a
// family, a weight or a subset the catalog lacks, a path that climbs, the new form without its hash
// or under the old prefix — name nothing.
func TestOldFontNamesFindTodaysFile(t *testing.T) {
	current := routePath("families/outfit/400-latin.woff2")
	file, exact := FileAt(current)
	if file == nil || !exact {
		t.Fatalf("%s: the current name must be exact", current)
	}
	stale := current[:len(current)-len(".woff2")-16] + strings.Repeat("0", 16) + ".woff2"
	for _, old := range []string{
		"/jitfonts/outfit/400-latin.woff2",
		"/_kitwork/fonts/outfit/400-latin.woff2",
		stale,
	} {
		if got, exact := FileAt(old); got != file || exact {
			t.Errorf("%s: want today's %s, not exact; got %v exact=%t", old, current, pathOf(got), exact)
		}
	}
	for _, nothing := range []string{
		"/jitfonts/outfit/999-latin.woff2",
		"/jitfonts/nonesuch/400-latin.woff2",
		"/jitfonts/outfit/400-cyrillic.woff2",
		"/jitfonts/outfit/../outfit/400-latin.woff2",
		"/jitfonts/../families/outfit/400-latin.woff2",
		"/jitfonts/outfit-400-latin." + current[len(current)-len(".woff2")-16:],
		"/_kitwork/fonts/outfit-400-latin.woff2",
		"/_kitwork/fonts/outfit-999-latin." + strings.Repeat("0", 16) + ".woff2",
		"/_kitwork/fonts/outfit-400-latin." + strings.Repeat("0", 15) + ".woff2",
		"/_kitwork/fonts/families/outfit/400-latin.woff2",
		"/_kitwork/fonts/",
		"/_kitwork/kernel." + strings.Repeat("0", 16) + ".woff2",
		"/other/outfit/400-latin.woff2",
	} {
		if got, _ := FileAt(nothing); got != nil {
			t.Errorf("%s must name nothing, got %s", nothing, got.Path)
		}
	}
}

func pathOf(file *File) string {
	if file == nil {
		return "<nil>"
	}
	return file.Path
}
