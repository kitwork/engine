// Command gallery serves a live KitJS component gallery. It seals the eight
// common components into one standalone Kit-profile bundle through the same
// composer the engine uses, then serves a single page that exercises each one.
//
//	go run -C engine ./jit/javascript/cmd/gallery -port 8099
package main

import (
	"flag"
	"log"
	"net/http"
	"strings"

	"github.com/kitwork/engine/jit/javascript"
)

func main() {
	port := flag.String("port", "8099", "TCP port to serve the gallery on")
	flag.Parse()

	composer, err := javascript.NewDefaultComposer()
	if err != nil {
		log.Fatalf("gallery: composer: %v", err)
	}
	refs := []javascript.ComponentRef{
		{Name: "stepper", Version: "1.0.0"},
		{Name: "slider", Version: "1.0.0"},
		{Name: "rating", Version: "1.0.0"},
		{Name: "tags", Version: "1.0.0"},
		{Name: "terminal", Version: "1.0.0"},
		{Name: "dropzone", Version: "1.0.0"},
		{Name: "command", Version: "1.0.0"},
		{Name: "context-menu", Version: "1.0.0"},
		{Name: "collapse", Version: "1.0.0"},
		{Name: "scrolled", Version: "1.0.0"},
		{Name: "combobox", Version: "1.0.0"},
		{Name: "copy", Version: "1.0.0"},
		{Name: "otp", Version: "1.0.0"},
	}
	bundle, err := composer.ComposeStandalone(refs, false)
	if err != nil {
		log.Fatalf("gallery: compose: %v", err)
	}
	page := strings.ReplaceAll(galleryHTML, "__BUNDLE_HASH__", bundle.ContentHash)

	mux := http.NewServeMux()
	mux.HandleFunc("/kit.js", func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		response.Header().Set("Cache-Control", "no-store")
		_, _ = response.Write(bundle.JavaScript)
	})
	mux.HandleFunc("/", func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/" {
			http.NotFound(response, request)
			return
		}
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		response.Header().Set("Cache-Control", "no-store")
		_, _ = response.Write([]byte(page))
	})

	addr := "127.0.0.1:" + *port
	log.Printf("KitJS gallery: http://localhost:%s  (bundle %d bytes, sha256:%s)", *port, len(bundle.JavaScript), bundle.ContentHash[:12])
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("gallery: serve: %v", err)
	}
}
