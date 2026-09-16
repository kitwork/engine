package engine

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kitwork/engine/work"
)

func TestVersionInfoUsesCurrentRuntimePlatform(t *testing.T) {
	info := GetVersionInfo()
	if info.OS != runtime.GOOS || info.Arch != runtime.GOARCH {
		t.Fatalf("platform = %s/%s, want %s/%s", info.OS, info.Arch, runtime.GOOS, runtime.GOARCH)
	}
	if info.GoVersion == "" || info.BytecodeVersion == 0 || info.ProgramEncodingVersion == 0 {
		t.Fatalf("version info = %+v", info)
	}
}

func TestCreateTenantScaffoldUsesIdentityDomainLayout(t *testing.T) {
	root := filepath.Join(t.TempDir(), "apps")
	identity := "0123456789abcdefghijklmnopqrstuvwxyz"
	path, err := createTenantScaffold(root, identity, "https://Example.COM/")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, identity, "example.com")
	if path != want {
		t.Fatalf("scaffold path = %q, want %q", path, want)
	}
	if info, err := os.Stat(filepath.Join(path, work.RouterFileName)); err != nil || info.IsDir() {
		t.Fatalf("root router was not created: %v", err)
	}

	sites, err := work.DiscoverTenantSites(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 1 || sites[0].Identity != identity || sites[0].Domain != "example.com" {
		t.Fatalf("discovered scaffold = %+v", sites)
	}

	_, err = createTenantScaffold(
		root,
		"abcdefghijklmnopqrstuvwxyz0123456789",
		"example.com",
	)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate domain error = %v", err)
	}
}

func TestNormalizeScaffoldDomainRejectsUnsafePathsAndAuthorities(t *testing.T) {
	for _, input := range []string{
		"",
		"localhost",
		"../../outside.example",
		"example.com/path",
		"example.com:8080",
		"user@example.com",
		"https://example.com/path",
		"-bad.example",
		"bad_.example",
	} {
		t.Run(input, func(t *testing.T) {
			if got, err := normalizeScaffoldDomain(input); err == nil {
				t.Fatalf("normalizeScaffoldDomain(%q) = %q, want error", input, got)
			}
		})
	}
}

func TestCreateTenantScaffoldGeneratesSafeIdentity(t *testing.T) {
	root := filepath.Join(t.TempDir(), "apps")
	path, err := CreateTenantScaffold(root, "product.example")
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(filepath.ToSlash(relative), "/")
	if len(parts) != 2 || !validScaffoldIdentity(parts[0]) || parts[1] != "product.example" {
		t.Fatalf("generated scaffold path = %q", relative)
	}
}
