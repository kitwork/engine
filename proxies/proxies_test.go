package proxies

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"testing"
	"time"
)

func TestHTTPProxyForwardsRequestsAndAuthenticates(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Proxy-Authorization") != "" {
			t.Error("proxy credentials leaked to upstream")
		}
		writer.Header().Set("X-Upstream", "yes")
		_, _ = writer.Write([]byte("forwarded"))
	}))
	defer upstream.Close()

	port := freePort(t)
	manager, err := New([]Config{{
		Protocol: "http", Host: "127.0.0.1", Port: port,
		Username: "kitwork", Password: "secret", AllowPrivate: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeManager(t, manager) })

	proxyURL, _ := url.Parse("http://kitwork:secret@127.0.0.1:" + strconv.Itoa(port))
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	response, err := client.Get(upstream.URL + "/hello")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK || string(body) != "forwarded" || response.Header.Get("X-Upstream") != "yes" {
		t.Fatalf("response = %d %q %q", response.StatusCode, body, response.Header.Get("X-Upstream"))
	}
}

func TestHTTPProxyConnectTunnel(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("tunneled"))
	}))
	defer upstream.Close()

	port := freePort(t)
	manager, err := New([]Config{{Protocol: "http", Host: "127.0.0.1", Port: port, AllowPrivate: true}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeManager(t, manager) })
	proxyURL, _ := url.Parse("http://127.0.0.1:" + strconv.Itoa(port))
	client := &http.Client{Transport: &http.Transport{
		Proxy:           http.ProxyURL(proxyURL),
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // local test server only
	}}
	response, err := client.Get(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if string(body) != "tunneled" {
		t.Fatalf("body = %q", body)
	}
}

func TestSOCKS5ProxyConnectsWithUsernamePassword(t *testing.T) {
	echo, target := startEchoServer(t)
	defer echo.Close()
	port := freePort(t)
	manager, err := New([]Config{{
		Protocol: "socks5", Host: "127.0.0.1", Port: port,
		Username: "user", Password: "pass", AllowPrivate: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeManager(t, manager) })

	connection, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	reader := bufio.NewReader(connection)
	_, _ = connection.Write([]byte{5, 1, 2})
	expectBytes(t, reader, []byte{5, 2})
	_, _ = connection.Write([]byte{1, 4, 'u', 's', 'e', 'r', 4, 'p', 'a', 's', 's'})
	expectBytes(t, reader, []byte{1, 0})

	host, targetPortText, _ := net.SplitHostPort(target)
	targetPort, _ := strconv.Atoi(targetPortText)
	ip := net.ParseIP(host).To4()
	request := []byte{5, 1, 0, 1, ip[0], ip[1], ip[2], ip[3], 0, 0}
	binary.BigEndian.PutUint16(request[8:], uint16(targetPort))
	_, _ = connection.Write(request)
	reply := make([]byte, 10)
	if _, err := io.ReadFull(reader, reply); err != nil || reply[1] != 0 {
		t.Fatalf("SOCKS5 reply = %v, error = %v", reply, err)
	}
	_, _ = connection.Write([]byte("hello"))
	expectBytes(t, reader, []byte("hello"))
}

func TestProxyValidationDefaultsAndRejectsOpenPublicListener(t *testing.T) {
	prepared, err := prepare([]Config{{Port: 8888}})
	if err != nil {
		t.Fatal(err)
	}
	if prepared[0].Protocol != "http" || prepared[0].Host != "127.0.0.1" {
		t.Fatalf("defaults = %#v", prepared[0])
	}
	if err := Validate([]Config{{Host: "0.0.0.0", Port: 8888}}); err == nil {
		t.Fatal("expected public open proxy to be rejected")
	}
}

func TestHTTPProxyRejectsMissingCredentials(t *testing.T) {
	port := freePort(t)
	manager, err := New([]Config{{
		Protocol: "http", Host: "127.0.0.1", Port: port,
		Username: "kitwork", Password: "secret", AllowPrivate: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeManager(t, manager) })
	proxyURL, _ := url.Parse("http://127.0.0.1:" + strconv.Itoa(port))
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	response, err := client.Get("http://127.0.0.1:1/")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusProxyAuthRequired {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusProxyAuthRequired)
	}
}

func TestDestinationPolicyBlocksPrivateNetworksByDefault(t *testing.T) {
	dialer := newDestinationDialer(false)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := dialer.DialContext(ctx, "tcp", "127.0.0.1:80"); err == nil {
		t.Fatal("expected loopback destination to be blocked")
	}
	if blockedDestination(netip.MustParseAddr("127.0.0.1"), true) {
		t.Fatal("allowPrivate should permit an intentional loopback destination")
	}
	if !blockedDestination(netip.MustParseAddr("169.254.169.254"), true) {
		t.Fatal("allowPrivate must not permit link-local metadata destinations")
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port
}

func closeManager(t *testing.T, manager *Manager) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := manager.Close(ctx); err != nil {
		t.Errorf("close proxy manager: %v", err)
	}
}

func startEchoServer(t *testing.T) (net.Listener, string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer connection.Close()
				_, _ = io.Copy(connection, connection)
			}()
		}
	}()
	return listener, listener.Addr().String()
}

func expectBytes(t *testing.T, reader io.Reader, expected []byte) {
	t.Helper()
	actual := make([]byte, len(expected))
	if _, err := io.ReadFull(reader, actual); err != nil {
		t.Fatal(err)
	}
	if string(actual) != string(expected) {
		t.Fatalf("bytes = %v, want %v", actual, expected)
	}
}
