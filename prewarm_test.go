package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/core"
	"github.com/kitwork/engine/work"
)

func TestConfiguredPrewarmPolicies(t *testing.T) {
	root := t.TempDir()
	for _, domain := range []string{"primary.example.com", "second.example.com"} {
		directory := filepath.Join(root, "identity", domain)
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, work.RouterFileName), []byte(`const ready = true;`), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name   string
		config PrewarmConfig
		host   string
		want   int
	}{
		{name: "default lazy", host: "primary.example.com", want: 0},
		{name: "explicit primary", config: PrewarmConfig{Mode: PrewarmPrimary}, host: "primary.example.com", want: 1},
		{name: "selected", config: PrewarmConfig{Mode: PrewarmSelected, Domains: []string{"second.example.com"}}, host: "primary.example.com", want: 1},
		{name: "all", config: PrewarmConfig{Mode: PrewarmAll}, host: "primary.example.com", want: 2},
		{name: "disabled", config: PrewarmConfig{Mode: PrewarmDisabled}, host: "primary.example.com", want: 0},
		{name: "no implicit hostname", want: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := core.New(root, 0, false, test.host)
			t.Cleanup(handler.Close)
			if err := handler.SetRootLayout(work.RootLayoutMultiTenant); err != nil {
				t.Fatal(err)
			}
			config := &Config{
				Web:        true,
				Root:       root,
				RootLayout: work.RootLayoutMultiTenant,
				Hostname:   test.host,
				Prewarm:    test.config,
			}
			if err := prewarmConfiguredSites(handler, config); err != nil {
				t.Fatal(err)
			}
			if got := handler.Health().LoadedSites; got != test.want {
				t.Fatalf("loaded sites = %d, want %d", got, test.want)
			}
		})
	}
}

func TestExplicitPrimaryPrewarmRequiresHostname(t *testing.T) {
	handler := core.New(t.TempDir(), 0, false, "")
	t.Cleanup(handler.Close)
	config := &Config{Web: true, Prewarm: PrewarmConfig{Mode: PrewarmPrimary}}
	if err := prewarmConfiguredSites(handler, config); err == nil {
		t.Fatal("expected explicit primary prewarm without hostname to fail")
	}
}

func TestSelectedPrewarmFailsReadinessForMissingSite(t *testing.T) {
	handler := core.New(t.TempDir(), 0, false, "")
	t.Cleanup(handler.Close)
	if err := handler.SetRootLayout(work.RootLayoutMultiTenant); err != nil {
		t.Fatal(err)
	}

	config := &Config{
		Web:        true,
		RootLayout: work.RootLayoutMultiTenant,
		Prewarm: PrewarmConfig{
			Mode:    PrewarmSelected,
			Domains: []string{"missing.example.com"},
		},
	}
	err := prewarmConfiguredSites(handler, config)
	if err == nil || !strings.Contains(err.Error(), "missing.example.com") {
		t.Fatalf("error = %v, want missing selected site readiness failure", err)
	}
	if got := handler.Health().LoadedSites; got != 0 {
		t.Fatalf("loaded sites = %d, want 0 after failed prewarm", got)
	}
}
