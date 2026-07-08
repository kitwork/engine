package http

import (
	"fmt"
	"net"
	stdhttp "net/http"
	"syscall"
	"time"
)

var IsLocalAllowed func() bool
var GetServerPort func() int

var sharedTransport = &stdhttp.Transport{
	DialContext: (&net.Dialer{
		Timeout: 30 * time.Second,
		Control: func(network, address string, c syscall.RawConn) error {
			// Basic SSRF control
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip != nil {
				if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() {
					return fmt.Errorf("SSRF prevention: connection to private/local space is blocked (%s)", host)
				}
			}
			return nil
		},
	}).DialContext,
	MaxIdleConns:        100,
	IdleConnTimeout:     90 * time.Second,
	MaxIdleConnsPerHost: 100,
}

var localTransport = &stdhttp.Transport{
	DialContext: (&net.Dialer{
		Timeout: 30 * time.Second,
	}).DialContext,
	MaxIdleConns:        100,
	IdleConnTimeout:     90 * time.Second,
	MaxIdleConnsPerHost: 100,
}

var sharedClient = &stdhttp.Client{
	Transport: sharedTransport,
	Timeout:   10 * time.Second,
}

var localClient = &stdhttp.Client{
	Transport: localTransport,
	Timeout:   10 * time.Second,
}
