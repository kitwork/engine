// nativeproof is M0 of the Native Bridge RFC: serve a REAL tenant with ZERO sockets — the
// in-memory route mapper a native shell (WebView2/WKWebView scheme handler) will call. It loads
// the tenant exactly like the engine does, then answers kitwork://app/... requests through a
// recorder. No port, no firewall prompt, no localhost.
//
//	go run ./cmd/nativeproof <tenants-root> <domain> [paths...]
//	go run ./cmd/nativeproof ../tenants huynhnhanquoc.com / /kit.js /about
package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"time"

	"github.com/kitwork/engine/work"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Println("usage: nativeproof <tenants-root> <domain> [paths...]")
		os.Exit(2)
	}
	root, domain := os.Args[1], os.Args[2]
	paths := os.Args[3:]
	if len(paths) == 0 {
		paths = []string{"/", "/kit.js"}
	}

	tenant := work.NewTenant(root, domain)
	if err := tenant.Run(); err != nil {
		fmt.Println("tenant failed to run:", err)
		os.Exit(1)
	}

	fmt.Printf("tenant %s loaded from %s — serving IN MEMORY (no socket, no localhost)\n\n", domain, root)
	for _, p := range paths {
		start := time.Now()
		req := httptest.NewRequest(http.MethodGet, "kitwork://app"+p, nil)
		req.Host = domain
		rec := httptest.NewRecorder()
		tenant.Serve(rec, req)

		preview := strings.ReplaceAll(rec.Body.String(), "\n", " ")
		if len(preview) > 80 {
			preview = preview[:80] + "…"
		}
		fmt.Printf("kitwork://app%-24s → %d  %-24s %6db  %8s  %s\n",
			p, rec.Code, rec.Header().Get("Content-Type"), rec.Body.Len(),
			time.Since(start).Round(time.Millisecond), preview)
	}
}
