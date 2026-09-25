package engine

import (
	"fmt"
	"math"
	"strings"

	"github.com/kitwork/engine/gateway"
)

const maxGatewayTargets = 256

func parseGatewayTargets(raw interface{}) ([]gateway.Target, error) {
	items, ok := raw.([]interface{})
	if !ok {
		return nil, fmt.Errorf("app.gateway declarations must be an array")
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("app.gateway requires at least one target")
	}
	if len(items) > maxGatewayTargets {
		return nil, fmt.Errorf("app.gateway has %d targets; limit is %d", len(items), maxGatewayTargets)
	}

	targets := make([]gateway.Target, 0, len(items))
	for index, item := range items {
		object, ok := item.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("app.gateway target %d must be an object", index+1)
		}
		for key := range object {
			switch key {
			case "domain", "target", "weight", "health":
			default:
				return nil, fmt.Errorf("app.gateway target %d has unknown field %q", index+1, key)
			}
		}

		target, ok := object["target"].(string)
		if !ok || strings.TrimSpace(target) == "" {
			return nil, fmt.Errorf("app.gateway target %d target must be a non-empty string", index+1)
		}
		declaration := gateway.Target{Target: target, Weight: 1}
		if configured, exists := object["domain"]; exists {
			domain, ok := configured.(string)
			if !ok {
				return nil, fmt.Errorf("app.gateway target %d domain must be a string", index+1)
			}
			declaration.Domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
		}
		if configured, exists := object["weight"]; exists {
			weight, ok := parseGatewayWeight(configured)
			if !ok || weight < 1 || weight > 4096 {
				return nil, fmt.Errorf("app.gateway target %d weight must be an integer from 1 to 4096", index+1)
			}
			declaration.Weight = weight
		}
		if configured, exists := object["health"]; exists {
			health, ok := configured.(string)
			if !ok {
				return nil, fmt.Errorf("app.gateway target %d health must be a string", index+1)
			}
			declaration.Health = strings.TrimSpace(health)
		}
		targets = append(targets, declaration)
	}

	if err := gateway.Validate(targets); err != nil {
		return nil, fmt.Errorf("app.gateway: %w", err)
	}
	return targets, nil
}

func parseGatewayWeight(value interface{}) (int, bool) {
	switch weight := value.(type) {
	case int:
		return weight, true
	case int64:
		return int(weight), int64(int(weight)) == weight
	case float64:
		if math.Trunc(weight) != weight || weight > float64(math.MaxInt) || weight < float64(math.MinInt) {
			return 0, false
		}
		return int(weight), true
	default:
		return 0, false
	}
}

func validateGatewaySurface(hasWeb bool, targets []gateway.Target) error {
	if !hasWeb {
		return nil
	}
	for _, target := range targets {
		if target.Domain == "" {
			return fmt.Errorf("app.gateway: a domainless target is only valid in a gateway-only manifest")
		}
	}
	return nil
}
