// Package proxies implements the host-owned forward-proxy listeners declared
// by app.proxy(...). It runs outside tenant VMs and owns no application data.
package proxies

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

const maxListeners = 32

// Config declares one independent HTTP or SOCKS5 forward-proxy listener.
type Config struct {
	Protocol     string `json:"protocol,omitempty" yaml:"protocol,omitempty"`
	Host         string `json:"host,omitempty" yaml:"host,omitempty"`
	Port         int    `json:"port" yaml:"port"`
	Username     string `json:"username,omitempty" yaml:"username,omitempty"`
	Password     string `json:"password,omitempty" yaml:"password,omitempty"`
	AllowPrivate bool   `json:"allow_private,omitempty" yaml:"allow_private,omitempty"`
}

func (c Config) address() string { return net.JoinHostPort(c.Host, strconv.Itoa(c.Port)) }

func (c Config) hasAuthentication() bool { return c.Username != "" && c.Password != "" }

// Validate checks the complete listener set without opening sockets.
func Validate(configs []Config) error {
	_, err := prepare(configs)
	return err
}

func prepare(configs []Config) ([]Config, error) {
	if len(configs) == 0 {
		return nil, fmt.Errorf("requires at least one listener")
	}
	if len(configs) > maxListeners {
		return nil, fmt.Errorf("has %d listeners; limit is %d", len(configs), maxListeners)
	}

	prepared := make([]Config, 0, len(configs))
	seen := make(map[string]struct{}, len(configs))
	for index, configured := range configs {
		configured.Protocol = strings.ToLower(strings.TrimSpace(configured.Protocol))
		if configured.Protocol == "" {
			configured.Protocol = "http"
		}
		if configured.Protocol != "http" && configured.Protocol != "socks5" {
			return nil, fmt.Errorf("listener %d protocol must be http or socks5", index+1)
		}
		configured.Host = strings.TrimSpace(configured.Host)
		if configured.Host == "" {
			configured.Host = "127.0.0.1"
		}
		if configured.Port < 1 || configured.Port > 65535 {
			return nil, fmt.Errorf("listener %d port must be from 1 to 65535", index+1)
		}
		if (configured.Username == "") != (configured.Password == "") {
			return nil, fmt.Errorf("listener %d username and password must be configured together", index+1)
		}
		if configured.Protocol == "socks5" && (len(configured.Username) > 255 || len(configured.Password) > 255) {
			return nil, fmt.Errorf("listener %d SOCKS5 credentials must not exceed 255 bytes", index+1)
		}
		if !isLocalBind(configured.Host) && !configured.hasAuthentication() {
			return nil, fmt.Errorf("listener %d exposes %s without authentication", index+1, configured.address())
		}
		key := strings.ToLower(configured.address())
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("listener %d duplicates %s", index+1, configured.address())
		}
		seen[key] = struct{}{}
		prepared = append(prepared, configured)
	}
	return prepared, nil
}

func isLocalBind(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}
