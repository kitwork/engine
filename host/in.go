package host

import (
	"net"
	"os"
)

type Host struct {
	Name string // hostname hoặc ip
}

func New(names ...string) *Host {
	if len(names) > 0 {
		return &Host{Name: names[0]}
	}

	name, _ := os.Hostname()
	return &Host{Name: name}
}

func IsLan(names ...string) bool {
	return New(names...).IsLAN()
}

func IsLocalhost(names ...string) bool {
	return New(names...).IsLocalhost()
}

func IsIP(names ...string) bool {
	return New(names...).IsIP()
}

func (h *Host) IsIP() bool {
	return net.ParseIP(h.Name) != nil
}

func (h *Host) IsLAN() bool {
	ip := net.ParseIP(h.Name)
	return ip != nil && ip.IsPrivate()
}

func (h *Host) IsLocalhost() bool {
	return h.Name == "localhost" || h.Name == "127.0.0.1" || h.Name == "::1"
}
