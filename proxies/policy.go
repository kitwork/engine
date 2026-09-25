package proxies

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

var alwaysBlockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("2001:db8::/32"),
}

var privatePrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
}

type destinationDialer struct {
	allowPrivate bool
	dialer       net.Dialer
}

func newDestinationDialer(allowPrivate bool) *destinationDialer {
	return &destinationDialer{
		allowPrivate: allowPrivate,
		dialer: net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		},
	}
}

func (d *destinationDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("invalid destination %q", address)
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return nil, fmt.Errorf("invalid destination port %q", port)
	}

	addresses, err := resolveDestination(ctx, strings.Trim(host, "[]"))
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, addressIP := range addresses {
		if blockedDestination(addressIP, d.allowPrivate) {
			lastErr = fmt.Errorf("destination %s is not allowed", addressIP)
			continue
		}
		connection, err := d.dialer.DialContext(ctx, network, net.JoinHostPort(addressIP.String(), port))
		if err == nil {
			return connection, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("destination %q resolved to no usable address", host)
	}
	return nil, lastErr
}

func resolveDestination(ctx context.Context, host string) ([]netip.Addr, error) {
	if parsed, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{parsed.Unmap()}, nil
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("resolve destination %q: %w", host, err)
	}
	result := make([]netip.Addr, 0, len(addresses))
	for _, address := range addresses {
		result = append(result, address.Unmap())
	}
	return result, nil
}

func blockedDestination(address netip.Addr, allowPrivate bool) bool {
	if !address.IsValid() || address.IsUnspecified() || address.IsLinkLocalUnicast() ||
		address.IsLinkLocalMulticast() || address.IsMulticast() {
		return true
	}
	for _, prefix := range alwaysBlockedPrefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	if allowPrivate {
		return false
	}
	if address.IsLoopback() || address.IsPrivate() {
		return true
	}
	for _, prefix := range privatePrefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}
