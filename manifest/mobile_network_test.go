package manifest

import (
	"reflect"
	"testing"
)

func networkManifest() map[string]interface{} {
	raw := validManifest()
	mobile := raw["mobile"].(map[string]interface{})
	mobile["version"] = 9
	mobile["permissions"] = []string{"network.request", "network.upload"}
	mobile["network"] = map[string]interface{}{"origins": []string{"https://example.org", "https://example.com"}}
	return raw
}

func TestMobileV9NetworkRoundTrip(t *testing.T) {
	parsed, err := Parse(networkManifest())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(parsed.Network.Origins, []string{"https://example.com", "https://example.org"}) {
		t.Fatalf("origins: %#v", parsed.Network)
	}
	encoded, err := EncodeJSON(parsed)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeJSON(encoded)
	if err != nil || !reflect.DeepEqual(parsed, decoded) {
		t.Fatalf("round trip: %#v %v", decoded, err)
	}
}

func TestMobileNetworkStrictOriginsAndGrants(t *testing.T) {
	for _, origin := range []string{"http://example.com", "https://EXAMPLE.com", "https://example.com/", "https://example.com:443", "https://127.0.0.1", "https://localhost", "https://foo.local", "https://*.example.com", "https://user@example.com", "https://example.com?x", "https://example.com#", "https://xn--test.com", "https://example.com\\x"} {
		t.Run(origin, func(t *testing.T) {
			raw := networkManifest()
			raw["mobile"].(map[string]interface{})["network"] = map[string]interface{}{"origins": []string{origin}}
			if _, err := Parse(raw); err == nil {
				t.Fatal("accepted noncanonical origin")
			}
		})
	}
	for _, edit := range []func(map[string]interface{}){
		func(m map[string]interface{}) { m["version"] = 8 },
		func(m map[string]interface{}) { delete(m, "network") },
		func(m map[string]interface{}) { m["permissions"] = []string{} },
		func(m map[string]interface{}) { m["network"] = map[string]interface{}{"origins": []string{}} },
		func(m map[string]interface{}) {
			m["network"] = map[string]interface{}{"origins": []string{"https://example.com", "https://example.com"}}
		},
		func(m map[string]interface{}) {
			m["network"] = map[string]interface{}{"origins": []string{"https://example.com"}, "redirects": true}
		},
	} {
		raw := networkManifest()
		edit(raw["mobile"].(map[string]interface{}))
		if _, err := Parse(raw); err == nil {
			t.Fatal("accepted invalid policy")
		}
	}
}

func TestNetworkOriginsRejectNonPublicSuffixesWithoutChangingLinks(t *testing.T) {
	for _, host := range []string{"service.onion", "service.arpa", "service.home", "service.lan", "service.123", "0177.0.0.1"} {
		t.Run(host, func(t *testing.T) {
			if err := validateLinkHost(host); err != nil {
				t.Fatalf("existing link policy unexpectedly changed: %v", err)
			}
			if err := ValidateNetworkOrigin("https://" + host); err == nil {
				t.Fatal("network origin accepted a non-public host suffix")
			}
			raw := networkManifest()
			raw["mobile"].(map[string]interface{})["network"] = map[string]interface{}{"origins": []string{"https://" + host}}
			if _, err := Parse(raw); err == nil {
				t.Fatal("manifest accepted a non-public network origin")
			}
		})
	}
	for _, origin := range []string{"https://example.com", "https://api.example.org", "https://api.ex4mple.c0m"} {
		if err := ValidateNetworkOrigin(origin); err != nil {
			t.Fatalf("public origin %s rejected: %v", origin, err)
		}
	}
}
