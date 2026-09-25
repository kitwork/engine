package engine

import (
	"fmt"
	"math"
	"strings"

	"github.com/kitwork/engine/proxies"
)

const maxProxyListeners = 32

func parseProxyConfigs(raw interface{}) ([]proxies.Config, error) {
	items, ok := raw.([]interface{})
	if !ok {
		return nil, fmt.Errorf("app.proxy declarations must be an array")
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("app.proxy requires at least one listener")
	}
	if len(items) > maxProxyListeners {
		return nil, fmt.Errorf("app.proxy has %d listeners; limit is %d", len(items), maxProxyListeners)
	}

	configs := make([]proxies.Config, 0, len(items))
	for index, item := range items {
		object, ok := item.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("app.proxy listener %d must be an object", index+1)
		}
		for key := range object {
			switch key {
			case "protocol", "host", "port", "username", "password", "allowPrivate":
			default:
				return nil, fmt.Errorf("app.proxy listener %d has unknown field %q", index+1, key)
			}
		}

		configured := proxies.Config{Protocol: "http", Host: "127.0.0.1"}
		if value, exists := object["protocol"]; exists {
			protocol, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("app.proxy listener %d protocol must be a string", index+1)
			}
			configured.Protocol = strings.ToLower(strings.TrimSpace(protocol))
		}
		if value, exists := object["host"]; exists {
			host, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("app.proxy listener %d host must be a string", index+1)
			}
			configured.Host = strings.TrimSpace(host)
		}
		port, ok := proxyPort(object["port"])
		if !ok || port < 1 || port > 65535 {
			return nil, fmt.Errorf("app.proxy listener %d port must be an integer from 1 to 65535", index+1)
		}
		configured.Port = port
		if value, exists := object["username"]; exists {
			if value == nil {
				value = ""
			}
			username, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("app.proxy listener %d username must be a string", index+1)
			}
			configured.Username = username
		}
		if value, exists := object["password"]; exists {
			if value == nil {
				value = ""
			}
			password, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("app.proxy listener %d password must be a string", index+1)
			}
			configured.Password = password
		}
		if value, exists := object["allowPrivate"]; exists {
			allow, ok := value.(bool)
			if !ok {
				return nil, fmt.Errorf("app.proxy listener %d allowPrivate must be a boolean", index+1)
			}
			configured.AllowPrivate = allow
		}
		configs = append(configs, configured)
	}

	if err := proxies.Validate(configs); err != nil {
		return nil, fmt.Errorf("app.proxy: %w", err)
	}
	return configs, nil
}

func proxyPort(value interface{}) (int, bool) {
	switch port := value.(type) {
	case int:
		return port, true
	case int64:
		return int(port), int64(int(port)) == port
	case float64:
		if math.Trunc(port) != port || port > float64(math.MaxInt) || port < float64(math.MinInt) {
			return 0, false
		}
		return int(port), true
	default:
		return 0, false
	}
}
