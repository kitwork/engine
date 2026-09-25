package engine

import (
	"strings"
	"testing"
)

func TestAppProxyCapturesProtocolsAndRepeatedCalls(t *testing.T) {
	file := writeServerJS(t, `
import { app } from "kitwork";
app.proxy({ port: 8888 });
app.proxy({ protocol: "socks5", host: "127.0.0.1", port: 1080 });
`)
	builder, err := evalServerBuilder(file)
	if err != nil {
		t.Fatal(err)
	}
	if !builder.hasProxy || builder.hasWeb || builder.hasGateway {
		t.Fatalf("surfaces: proxy=%t web=%t gateway=%t", builder.hasProxy, builder.hasWeb, builder.hasGateway)
	}
	raw, err := builderToMap(builder, file)
	if err != nil {
		t.Fatal(err)
	}
	config, err := ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Proxies) != 2 {
		t.Fatalf("proxy listeners = %d, want 2", len(config.Proxies))
	}
	if config.Proxies[0].Protocol != "http" || config.Proxies[0].Host != "127.0.0.1" || config.Proxies[1].Protocol != "socks5" {
		t.Fatalf("unexpected normalized proxies: %#v", config.Proxies)
	}
	if _, err := evalConfigJS(file); err != nil {
		t.Fatalf("proxy-only manifest should be accepted: %v", err)
	}
}

func TestAppProxyRejectsUnsafeDeclarations(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "none", body: `app.proxy();`, want: "at least one"},
		{name: "not object", body: `app.proxy(8888);`, want: "must be an object"},
		{name: "missing port", body: `app.proxy({});`, want: "port must be"},
		{name: "bad protocol", body: `app.proxy({ protocol: "tcp", port: 9000 });`, want: "http or socks5"},
		{name: "partial credentials", body: `app.proxy({ port: 9000, username: "user" });`, want: "configured together"},
		{name: "public open proxy", body: `app.proxy({ host: "0.0.0.0", port: 9000 });`, want: "without authentication"},
		{name: "unknown field", body: `app.proxy({ port: 9000, bind: "127.0.0.1" });`, want: "unknown field"},
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

func TestProxyOnlyCommandNeedsNoAppRoot(t *testing.T) {
	file := writeServerJS(t, `
import { app } from "kitwork";
app.proxy({ port: 8888 });
`)
	config, err := commandConfig(file)
	if err != nil {
		t.Fatal(err)
	}
	if config.Web || config.Root != "." || len(config.Proxies) != 1 {
		t.Fatalf("proxy-only config = %#v", config)
	}
}

func TestAppProxyTreatsMissingOptionalCredentialEnvAsUnset(t *testing.T) {
	file := writeServerJS(t, `
import { app, env } from "kitwork";
app.proxy({
  port: 8888,
  username: env.PROXY_USERNAME_THAT_DOES_NOT_EXIST,
  password: env.PROXY_PASSWORD_THAT_DOES_NOT_EXIST
});
`)
	raw, err := evalConfigJS(file)
	if err != nil {
		t.Fatal(err)
	}
	config, err := ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	if config.Proxies[0].Username != "" || config.Proxies[0].Password != "" {
		t.Fatalf("missing optional credentials = %#v", config.Proxies[0])
	}
}

func TestRemovedProxyAliasesDoNotDeclareASurface(t *testing.T) {
	for _, call := range []string{
		`app.proxies({ port: 8888 });`,
		`app.socks5({ port: 1080 });`,
	} {
		file := writeServerJS(t, `import { app } from "kitwork"; `+call)
		builder, err := evalServerBuilder(file)
		if err != nil {
			t.Fatal(err)
		}
		if builder.hasProxy {
			t.Fatalf("removed alias declared proxy surface: %s", call)
		}
		if warning := builder.unknownMethodWarning(); !strings.Contains(warning, "is not a method") {
			t.Fatalf("removed alias warning = %q", warning)
		}
	}
}
