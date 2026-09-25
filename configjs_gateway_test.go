package engine

import (
	"strings"
	"testing"
)

func TestAppGatewayCapturesVarargsAndRepeatedCalls(t *testing.T) {
	file := writeServerJS(t, `
import { app } from "kitwork";
app.gateway(
  { domain: "api.example.com", target: "http://node-a:3000", weight: 2, health: "/health" },
  { domain: "api.example.com", target: "http://node-b:3000" }
);
app.gateway({ target: "http://fallback:4000" });
`)

	builder, err := evalServerBuilder(file)
	if err != nil {
		t.Fatal(err)
	}
	if !builder.hasGateway || builder.hasWeb {
		t.Fatalf("surfaces: gateway=%t web=%t", builder.hasGateway, builder.hasWeb)
	}
	raw, err := builderToMap(builder, file)
	if err != nil {
		t.Fatal(err)
	}
	config, err := ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Gateway) != 3 {
		t.Fatalf("gateway targets = %d, want 3", len(config.Gateway))
	}
	if config.Gateway[0].Weight != 2 || config.Gateway[0].Health != "/health" || config.Gateway[1].Weight != 1 || config.Gateway[2].Domain != "" {
		t.Fatalf("unexpected normalized gateway targets: %#v", config.Gateway)
	}
	if _, err := evalConfigJS(file); err != nil {
		t.Fatalf("gateway-only manifest should be accepted: %v", err)
	}
}

func TestAppGatewayRejectsInvalidDeclarations(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "no targets", body: `app.gateway();`, want: "at least one target"},
		{name: "not object", body: `app.gateway("http://node:3000");`, want: "must be an object"},
		{name: "missing target", body: `app.gateway({ domain: "api.example.com" });`, want: "target must be"},
		{name: "zero weight", body: `app.gateway({ target: "http://node:3000", weight: 0 });`, want: "weight must be"},
		{name: "relative health", body: `app.gateway({ target: "http://node:3000", health: "health" });`, want: "absolute path"},
		{name: "unknown field", body: `app.gateway({ target: "http://node:3000", url: "http://old:3000" });`, want: "unknown field"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			file := writeServerJS(t, `import { app } from "kitwork"; `+test.body)
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

func TestGatewayOnlyCommandNeedsNoAppRoot(t *testing.T) {
	file := writeServerJS(t, `
import { app } from "kitwork";
app.gateway({ target: "http://node:3000" });
`)
	config, err := commandConfig(file)
	if err != nil {
		t.Fatal(err)
	}
	if config.Web || config.Root != "." || len(config.Gateway) != 1 {
		t.Fatalf("gateway-only config = %#v", config)
	}
}

func TestDomainlessGatewayCannotShadowWebSurface(t *testing.T) {
	file := writeServerJS(t, `
import { app } from "kitwork";
app.web({ port: 8080 });
app.gateway({ target: "http://node:3000" });
`)
	_, err := commandConfig(file)
	if err == nil || !strings.Contains(err.Error(), "gateway-only manifest") {
		t.Fatalf("error = %v, want gateway-only manifest validation", err)
	}
}
