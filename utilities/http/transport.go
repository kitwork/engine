package http

import (
	"fmt"
	"net"
	stdhttp "net/http"
	"syscall"
	"time"

	"github.com/kitwork/engine/internal/loopback"
)

// GetServerPort is the port this host serves on: a self-call ("/path") goes to 127.0.0.1 there.
var GetServerPort func() int

// deniedAddress is the SSRF guard refusing an address in private or local space: the request ends in
// DENIED (failureCode).
type deniedAddress struct{ host string }

func (e *deniedAddress) Error() string {
	return fmt.Sprintf("SSRF prevention: connection to private/local space is blocked (%s)", e.host)
}

var (
	// sharedAddressSpace is 100.64.0.0/10 (RFC 6598, carrier-grade NAT), where some clouds keep their
	// metadata service (100.100.100.200).
	sharedAddressSpace = mustCIDR("100.64.0.0/10")
	// thisNetwork is 0.0.0.0/8, which some systems route to the host itself.
	thisNetwork = mustCIDR("0.0.0.0/8")
	// nat64 is 64:ff9b::/96 (RFC 6052): the IPv4 address in its last four bytes is the one reached.
	nat64 = mustCIDR("64:ff9b::/96")
)

func mustCIDR(text string) *net.IPNet {
	_, network, err := net.ParseCIDR(text)
	if err != nil {
		panic(err)
	}
	return network
}

// deniedIP reports an address the guard refuses: loopback (unless the engine's tests or its synthetic
// canary opened it with internal/loopback), private, unspecified, link-local, multicast, broadcast,
// 0.0.0.0/8, 100.64.0.0/10, and a NAT64 address of any of them.
func deniedIP(ip net.IP) bool {
	if nat64.Contains(ip) && len(ip) == net.IPv6len {
		ip = net.IPv4(ip[12], ip[13], ip[14], ip[15])
	}
	if ip.IsLoopback() {
		return !loopback.Opened()
	}
	return ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.Equal(net.IPv4bcast) ||
		sharedAddressSpace.Contains(ip) || thisNetwork.Contains(ip)
}

// guardDial runs for every connection the guarded transport opens, after DNS and after every redirect:
// address is the IP being dialled.
func guardDial(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || deniedIP(ip) {
		return &deniedAddress{host: host}
	}
	return nil
}

// guardedTransport carries every request to an absolute URL.
var guardedTransport = &stdhttp.Transport{
	DialContext: (&net.Dialer{
		Timeout: 30 * time.Second,
		Control: guardDial,
	}).DialContext,
	MaxIdleConns:        100,
	IdleConnTimeout:     90 * time.Second,
	MaxIdleConnsPerHost: 100,
}

// selfTransport carries a self-call ("/path") to this host's own port, past the guard: the address is
// the engine's, never the program's. Its redirects stay on that port (stayOnSelf).
var selfTransport = &stdhttp.Transport{
	DialContext: (&net.Dialer{
		Timeout: 30 * time.Second,
	}).DialContext,
	MaxIdleConns:        100,
	IdleConnTimeout:     90 * time.Second,
	MaxIdleConnsPerHost: 100,
}
