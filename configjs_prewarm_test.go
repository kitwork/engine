package engine

import (
	"reflect"
	"strings"
	"testing"
)

func TestAppPrewarmManifestForms(t *testing.T) {
	tests := []struct {
		name    string
		call    string
		mode    PrewarmMode
		domains []string
	}{
		{name: "primary", call: `.prewarm()`, mode: PrewarmPrimary},
		{name: "enabled", call: `.prewarm(true)`, mode: PrewarmPrimary},
		{name: "disabled", call: `.prewarm(false)`, mode: PrewarmDisabled},
		{name: "all", call: `.prewarm("*")`, mode: PrewarmAll},
		{
			name:    "selected",
			call:    `.prewarm("API.Example.COM.", "docs.example.com")`,
			mode:    PrewarmSelected,
			domains: []string{"api.example.com", "docs.example.com"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			file := writeServerJS(t, `import { app } from "kitwork"; app.web({ hostname: "primary.example.com" })`+test.call+`;`)
			raw, err := evalConfigJS(file)
			if err != nil {
				t.Fatal(err)
			}
			config, err := ParseConfig(raw)
			if err != nil {
				t.Fatal(err)
			}
			if config.Prewarm.Mode != test.mode || !reflect.DeepEqual(config.Prewarm.Domains, test.domains) {
				t.Fatalf("prewarm = %+v, want mode=%q domains=%v", config.Prewarm, test.mode, test.domains)
			}
		})
	}
}

func TestAppPrewarmDefaultsToLazyPolicy(t *testing.T) {
	config, err := ParseConfig(map[string]interface{}{"hostname": "primary.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if config.Prewarm.Mode != PrewarmDefault {
		t.Fatalf("default prewarm mode = %q, want zero/default", config.Prewarm.Mode)
	}
}

func TestAppPrewarmRejectsAmbiguousDeclarations(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "mixed all", body: `.prewarm("*", "api.example.com")`, want: `"*" must be the only argument`},
		{name: "glob", body: `.prewarm("*.example.com")`, want: `"*" must be the only argument`},
		{name: "duplicate domain", body: `.prewarm("API.example.com", "api.example.com.")`, want: "declared more than once"},
		{name: "wrong type", body: `.prewarm(42)`, want: "must be a domain string"},
		{name: "declared twice", body: `.prewarm().prewarm("api.example.com")`, want: "only once"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			file := writeServerJS(t, `import { app } from "kitwork"; app.web({ hostname: "primary.example.com" })`+test.body+`;`)
			raw, err := evalConfigJS(file)
			if err == nil {
				_, err = ParseConfig(raw)
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestAppPrewarmRequiresWebSurface(t *testing.T) {
	file := writeServerJS(t, `import { app } from "kitwork"; app.database({ path: "./data.kitdb" }).prewarm();`)
	_, err := commandConfig(file)
	if err == nil || !strings.Contains(err.Error(), "requires an app.website server") {
		t.Fatalf("error = %v, want web surface validation", err)
	}
}
