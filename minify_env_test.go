package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// MINIFY=true in the host env (the root .env or the real environment) turns production minification on
// while ALLOW_LOCAL stays true, so a local run serves production's bytes (14/10/2026): before it,
// minification followed !AllowLocal alone, and ALLOW_LOCAL must stay true on a local machine (no ACME,
// no TLS). Production always minifies. The real environment wins over .env, as for every key; a value
// other than true or false stops the boot naming both words.
//
// CONTROL: the same manifest without MINIFY does not minify while developing.
func TestMinifyEnvTurnsMinificationOnWhileDeveloping(t *testing.T) {
	t.Setenv("MINIFY", "") // restored when the test ends
	os.Unsetenv("MINIFY")  // the machine's own MINIFY, if it has one, would win over every .env below
	manifest := `import { app } from "kitwork";
app.web({ port: 8080, allowLocal: true });`
	boot := func(env string) (*Config, error) {
		t.Helper()
		dir := t.TempDir()
		file := filepath.Join(dir, "app.kitwork.js")
		if err := os.WriteFile(file, []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
		if env != "" {
			if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(env), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		builder, err := evalServerBuilder(file)
		if err != nil {
			return nil, err
		}
		raw, err := builderToMap(builder, file)
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := ParseConfig(raw)
		if err != nil {
			t.Fatal(err)
		}
		cfg.Minify = builder.minify // as Run does
		return cfg, nil
	}

	cfg, err := boot("MINIFY=true\n")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AllowLocal || !cfg.Minify || !minifies(cfg) {
		t.Errorf("MINIFY=true with allowLocal: AllowLocal %v, Minify %v, minifies %v; want all true", cfg.AllowLocal, cfg.Minify, minifies(cfg))
	}
	cfg, err = boot("")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AllowLocal || cfg.Minify || minifies(cfg) {
		t.Errorf("CONTROL: no MINIFY while developing does not minify: Minify %v, minifies %v", cfg.Minify, minifies(cfg))
	}
	cfg, err = boot("MINIFY=false\n")
	if err != nil || minifies(cfg) {
		t.Errorf("MINIFY=false while developing does not minify: %v, %v", err, cfg)
	}
	// production minifies whatever MINIFY says
	if !minifies(&Config{AllowLocal: false}) || !minifies(&Config{AllowLocal: false, Minify: false}) {
		t.Error("production always minifies")
	}
	// the real environment wins over .env
	t.Setenv("MINIFY", "false")
	if cfg, err = boot("MINIFY=true\n"); err != nil || minifies(cfg) {
		t.Errorf("MINIFY=false in the environment wins over MINIFY=true in .env: %v, minifies %v", err, cfg != nil && minifies(cfg))
	}
	t.Setenv("MINIFY", "yes")
	_, err = boot("")
	if err == nil || !strings.Contains(err.Error(), "MINIFY=yes: chỉ nhận true hoặc false") {
		t.Errorf("MINIFY=yes stops the boot naming both words, got %v", err)
	}
}
