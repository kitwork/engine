package engine

import (
	"fmt"
	"strings"
)

type PrewarmMode string

const (
	PrewarmDefault  PrewarmMode = ""
	PrewarmDisabled PrewarmMode = "disabled"
	PrewarmPrimary  PrewarmMode = "primary"
	PrewarmSelected PrewarmMode = "selected"
	PrewarmAll      PrewarmMode = "all"
)

// PrewarmConfig selects which web generations must exist before the host is
// ready. The zero value preserves request-driven lazy loading.
type PrewarmConfig struct {
	Mode    PrewarmMode `json:"mode,omitempty" yaml:"mode,omitempty"`
	Domains []string    `json:"domains,omitempty" yaml:"domains,omitempty"`
}

func parsePrewarmConfig(raw interface{}) (PrewarmConfig, error) {
	switch configured := raw.(type) {
	case bool:
		if configured {
			return PrewarmConfig{Mode: PrewarmPrimary}, nil
		}
		return PrewarmConfig{Mode: PrewarmDisabled}, nil
	case string:
		return prewarmDomains([]string{configured})
	case []interface{}:
		domains := make([]string, 0, len(configured))
		for index, item := range configured {
			domain, ok := item.(string)
			if !ok {
				return PrewarmConfig{}, fmt.Errorf("app.prewarm argument %d must be a domain string", index+1)
			}
			domains = append(domains, domain)
		}
		return prewarmDomains(domains)
	default:
		return PrewarmConfig{}, fmt.Errorf("app.prewarm must be false, true, a domain, or an array of domains")
	}
}

func prewarmDomains(domains []string) (PrewarmConfig, error) {
	if len(domains) == 0 {
		return PrewarmConfig{Mode: PrewarmPrimary}, nil
	}
	if len(domains) == 1 && strings.TrimSpace(domains[0]) == "*" {
		return PrewarmConfig{Mode: PrewarmAll}, nil
	}

	normalized := make([]string, 0, len(domains))
	seen := make(map[string]struct{}, len(domains))
	for index, source := range domains {
		domain := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(source, ".")))
		if domain == "" {
			return PrewarmConfig{}, fmt.Errorf("app.prewarm argument %d cannot be empty", index+1)
		}
		if strings.Contains(domain, "*") {
			return PrewarmConfig{}, fmt.Errorf("app.prewarm %q is invalid; \"*\" must be the only argument", source)
		}
		if strings.ContainsAny(domain, "/\\:@ ") {
			return PrewarmConfig{}, fmt.Errorf("app.prewarm domain %q is invalid", source)
		}
		if _, exists := seen[domain]; exists {
			return PrewarmConfig{}, fmt.Errorf("app.prewarm domain %q is declared more than once", domain)
		}
		seen[domain] = struct{}{}
		normalized = append(normalized, domain)
	}
	return PrewarmConfig{Mode: PrewarmSelected, Domains: normalized}, nil
}

func validatePrewarmSurface(hasWeb bool, configured PrewarmConfig) error {
	if !hasWeb && configured.Mode != PrewarmDefault {
		return fmt.Errorf("app.prewarm requires an app.website server")
	}
	return nil
}
