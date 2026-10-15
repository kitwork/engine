package work

import (
	"net/http"

	fonts "github.com/kitwork/engine/jit/fonts"
)

// serveFont serves a vendored woff2 off the embedded jit/fonts FS: the path is under
// fonts.RoutePrefix (/_kitwork/fonts/<family>-<weight>-<subset>.<hash>.woff2) or a name written
// before (fonts.FileAt). Always-on — the @font-face `src` and the preload links point here — with no
// VM and no tenant lookup. The exact name is hard-cached and immutable, its hash being the bytes';
// an old name gets today's file, not kept. Access-Control-Allow-Origin lets the anonymous fetch every
// font load makes (preload crossorigin, `format('woff2')`) accept it.
func serveFont(w http.ResponseWriter, r *http.Request) {
	file, exact := fonts.FileAt(r.URL.Path)
	if file == nil {
		engineNotFound(w, r)
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	serveEngineFile(w, r, "font/woff2", file.Body, file.Digest, exact)
}
