package manifest

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// Network grants exact public HTTPS origins to the native HTTP client only.
// It does not allow WebView navigation, cookies, redirects, or private networks.
type Network struct {
	Origins []string `json:"origins"`
}

const MaxNetworkOrigins = 32

var permissionSetV9 = func() map[string]struct{} {
	result := make(map[string]struct{}, len(permissionSetV8)+2)
	for permission := range permissionSetV8 {
		result[permission] = struct{}{}
	}
	result["network.request"] = struct{}{}
	result["network.upload"] = struct{}{}
	return result
}()

// ValidateNetworkOrigin validates the canonical manifest spelling. Runtime DNS
// checks are additionally required; a public name can resolve to a private IP.
func ValidateNetworkOrigin(origin string) error {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Opaque != "" ||
		u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
		origin != "https://"+u.Hostname() || validateLinkHost(u.Hostname()) != nil {
		return fmt.Errorf("must be a canonical public HTTPS origin without port, path, credentials, query, or fragment")
	}
	suffix := u.Hostname()[strings.LastIndexByte(u.Hostname(), '.')+1:]
	switch suffix {
	case "onion", "arpa", "home", "lan":
		return fmt.Errorf("network origin uses a reserved host suffix")
	}
	if !strings.ContainsAny(suffix, "abcdefghijklmnopqrstuvwxyz") {
		return fmt.Errorf("network origin must not use a numeric final host label")
	}
	return nil
}

func parseNetwork(result *Mobile, mobile map[string]interface{}) error {
	value, exists := mobile["network"]
	wanted := hasString(result.Permissions, "network.request") || hasString(result.Permissions, "network.upload")
	if exists && result.Version < 9 {
		return fieldError("mobile.network", fmt.Errorf("requires mobile.version 9"))
	}
	if exists != wanted {
		return fieldError("mobile.network", fmt.Errorf("network policy and network.request/upload permission must be declared together"))
	}
	if !exists {
		return nil
	}
	network, ok := asObject(value)
	if !ok {
		return fieldError("mobile.network", fmt.Errorf("must be an object"))
	}
	if err := rejectUnknownKeys("mobile.network", network, keySet("origins")); err != nil {
		return err
	}
	values, ok := asSlice(network["origins"])
	if !ok || len(values) == 0 || len(values) > MaxNetworkOrigins {
		return fieldError("mobile.network.origins", fmt.Errorf("requires 1 to %d origins", MaxNetworkOrigins))
	}
	origins := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		origin, ok := value.(string)
		if !ok {
			return fieldError("mobile.network.origins", fmt.Errorf("origins must be strings"))
		}
		if err := ValidateNetworkOrigin(origin); err != nil {
			return fieldError("mobile.network.origins", err)
		}
		if seen[origin] {
			return fieldError("mobile.network.origins", fmt.Errorf("duplicate origin"))
		}
		seen[origin] = true
		origins = append(origins, origin)
	}
	sort.Strings(origins)
	result.Network = &Network{Origins: origins}
	return nil
}
