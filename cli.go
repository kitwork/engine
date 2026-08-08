package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
)

// Version Constants
const (
	Version = "0.9.0"
)

// VersionInfo returns structured metadata about the Kitwork engine build.
type VersionInfo struct {
	EngineVersion          string `json:"engine_version"`
	BytecodeVersion        uint16 `json:"bytecode_version"`
	ProgramEncodingVersion uint16 `json:"program_encoding_version"`
	CompilerSchemaVersion  uint16 `json:"compiler_schema_version"`
	GoVersion              string `json:"go_version"`
	OS                     string `json:"os"`
	Arch                   string `json:"arch"`
}

// GetVersionInfo returns current runtime and compiler version specs.
func GetVersionInfo() VersionInfo {
	goVer := "unknown"
	if info, ok := debug.ReadBuildInfo(); ok {
		goVer = info.GoVersion
	}

	return VersionInfo{
		EngineVersion:          Version,
		BytecodeVersion:        runtime.BytecodeVersion,
		ProgramEncodingVersion: runtime.ProgramEncodingVersion,
		CompilerSchemaVersion:  compiler.CompilerSchemaVersion,
		GoVersion:              goVer,
		OS:                     "windows",
		Arch:                   "amd64",
	}
}

// PrintVersion prints formatted version specs to stdout.
func PrintVersion() {
	info := GetVersionInfo()
	fmt.Printf("Kitwork Engine v%s\n", info.EngineVersion)
	fmt.Printf("  Bytecode Format:  v%d\n", info.BytecodeVersion)
	fmt.Printf("  Program Encoding: v%d\n", info.ProgramEncodingVersion)
	fmt.Printf("  Compiler Schema:  v%d\n", info.CompilerSchemaVersion)
}

// CreateTenantScaffold scaffolds a new tenant application folder structure.
func CreateTenantScaffold(appsRoot, domain string) (string, error) {
	cleanDomain := strings.TrimSpace(strings.ToLower(domain))
	cleanDomain = strings.TrimPrefix(cleanDomain, "http://")
	cleanDomain = strings.TrimPrefix(cleanDomain, "https://")
	cleanDomain = strings.TrimSuffix(cleanDomain, "/")

	if cleanDomain == "" {
		return "", fmt.Errorf("domain name cannot be empty")
	}

	if appsRoot == "" {
		appsRoot = "apps"
	}

	targetDir := filepath.Join(appsRoot, cleanDomain)

	if _, err := os.Stat(targetDir); err == nil {
		return "", fmt.Errorf("tenant directory already exists: %s", targetDir)
	}

	// Subdirectories to create
	dirs := []string{
		targetDir,
		filepath.Join(targetDir, "_assets"),
		filepath.Join(targetDir, "_core"),
		filepath.Join(targetDir, "about"),
		filepath.Join(targetDir, "api", "health"),
	}

	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			return "", fmt.Errorf("failed to create directory %s: %w", d, err)
		}
	}

	// Templates map
	files := map[string]string{
		filepath.Join(targetDir, ".env"): fmt.Sprintf(`# Per-tenant environment variables for %s
SITE_HOSTNAME=%s
PORT=8080
ALLOW_LOCAL=true
`, cleanDomain, cleanDomain),

		filepath.Join(targetDir, "router.kitwork.js"): fmt.Sprintf(`import { router } from "kitwork";
import { getStatus } from "./_core/utils.kitwork.js";

router
    .assets("/assets/*", "_assets")
    .language("en")
    .title("Welcome to %s")
    .description("A new tenant site running on Kitwork sovereign Go VM engine.")
    .jittheme(true);

router.get((ctx) => {
    const status = getStatus();
    return ctx.bind({
        siteName: "%s",
        status: status
    });
});
`, cleanDomain, cleanDomain),

		filepath.Join(targetDir, "index.kitwork.html"): `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>{{ $.meta.title }}</title>
  <meta name="description" content="{{ $.meta.description }}">
</head>
<body class="bg-slate-950 text-slate-100 font-sans antialiased min-h-screen flex flex-col">
  <header class="border-b border-slate-800 bg-slate-900/80 backdrop-blur px-6 py-4 flex items-center justify-between">
    <div class="flex items-center space-x-3">
      <i class="icon-rocket text-indigo-400 text-2xl"></i>
      <span class="font-bold text-xl tracking-tight text-white">{{ $.siteName }}</span>
    </div>
    <nav class="flex items-center space-x-6 text-sm font-medium text-slate-400">
      <a href="/" class="hover:text-white transition">Home</a>
      <a href="/about" class="hover:text-white transition">About</a>
      <a href="/api/health" class="hover:text-white transition">API Health</a>
    </nav>
  </header>

  <main class="flex-1">
    {{ yield }}
  </main>

  <footer class="border-t border-slate-800 py-6 text-center text-xs text-slate-500">
    <p>Powered by <a href="https://kitwork.io" class="text-indigo-400 hover:underline">Kitwork Engine</a> — Sovereign Cloud Infrastructure</p>
  </footer>
</body>
</html>
`,

		filepath.Join(targetDir, "page.kitwork.html"): `<div class="max-w-4xl mx-auto px-6 py-16">
  <div class="text-center space-y-4">
    <div class="inline-flex items-center space-x-2 px-3 py-1 rounded-full bg-indigo-500/10 text-indigo-400 text-xs font-semibold border border-indigo-500/20">
      <i class="icon-sparkles"></i>
      <span>Kitwork Tenant Initialized</span>
    </div>
    <h1 class="text-4xl font-extrabold tracking-tight text-white sm:text-5xl">
      Welcome to {{ $.siteName }}
    </h1>
    <p class="text-lg text-slate-400 max-w-2xl mx-auto">
      This site is running on Kitwork's custom Bytecode VM engine with Zero-VM static delivery.
    </p>
  </div>

  <div class="mt-12 grid grid-cols-1 md:grid-cols-3 gap-6">
    <div class="p-6 rounded-xl bg-slate-900 border border-slate-800 hover:border-slate-700 transition">
      <i class="icon-cpu text-indigo-400 text-2xl mb-3"></i>
      <h3 class="font-semibold text-white">Go Bytecode VM</h3>
      <p class="text-sm text-slate-400 mt-1">Constrained JS subset compiled to bytecode and executed in a stack-based VM.</p>
    </div>

    <div class="p-6 rounded-xl bg-slate-900 border border-slate-800 hover:border-slate-700 transition">
      <i class="icon-bolt text-indigo-400 text-2xl mb-3"></i>
      <h3 class="font-semibold text-white">Zero-VM Assets</h3>
      <p class="text-sm text-slate-400 mt-1">Files inside <code class="text-indigo-300">_assets/</code> are streamed zero-copy directly from disk.</p>
    </div>

    <div class="p-6 rounded-xl bg-slate-900 border border-slate-800 hover:border-slate-700 transition">
      <i class="icon-palette text-indigo-400 text-2xl mb-3"></i>
      <h3 class="font-semibold text-white">JIT Tailwind & Icons</h3>
      <p class="text-sm text-slate-400 mt-1">Zero-config JIT utility extraction and Tabler/Simple Icons mask engine.</p>
    </div>
  </div>
</div>
`,

		filepath.Join(targetDir, "about", "page.kitwork.html"): fmt.Sprintf(`<div class="max-w-3xl mx-auto px-6 py-12">
  <h1 class="text-3xl font-bold text-white mb-4">About %s</h1>
  <p class="text-slate-300 leading-relaxed">
    This is the about page route node for %s, rendered directly from <code class="text-indigo-300">about/page.kitwork.html</code>.
  </p>
</div>
`, cleanDomain, cleanDomain),

		filepath.Join(targetDir, "api", "health", "router.kitwork.js"): `import { router } from "kitwork";

router.get((ctx) => {
    return ctx.json({
        status: "ok",
        uptime: "healthy",
        engine: "kitwork-go-vm"
    });
});
`,

		filepath.Join(targetDir, "_core", "utils.kitwork.js"): `export const getStatus = () => "active";
`,
	}

	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return "", fmt.Errorf("failed to create file %s: %w", path, err)
		}
	}

	return targetDir, nil
}
