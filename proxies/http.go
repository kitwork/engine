package proxies

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

func (m *Manager) serveHTTP(runtime *listenerRuntime) {
	m.report(runtime.http.Serve(runtime.listener))
}

func prepareHTTP(runtime *listenerRuntime) {
	dialer := newDestinationDialer(runtime.config.AllowPrivate)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = dialer.DialContext
	runtime.transport = transport
	handler := &httpProxy{config: runtime.config, dialer: dialer, transport: transport}
	runtime.http = &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
}

type httpProxy struct {
	config    Config
	dialer    *destinationDialer
	transport *http.Transport
}

func (p *httpProxy) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if !p.authorized(request.Header.Get("Proxy-Authorization")) {
		writer.Header().Set("Proxy-Authenticate", `Basic realm="Kitwork Proxy"`)
		http.Error(writer, "Proxy Authentication Required", http.StatusProxyAuthRequired)
		return
	}
	if request.Method == http.MethodConnect {
		p.connect(writer, request)
		return
	}
	p.forward(writer, request)
}

func (p *httpProxy) authorized(header string) bool {
	if !p.config.hasAuthentication() {
		return true
	}
	const prefix = "Basic "
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(header[len(prefix):]))
	if err != nil {
		return false
	}
	username, password, ok := strings.Cut(string(decoded), ":")
	if !ok {
		return false
	}
	usernameHash := sha256.Sum256([]byte(username))
	configuredUsernameHash := sha256.Sum256([]byte(p.config.Username))
	passwordHash := sha256.Sum256([]byte(password))
	configuredPasswordHash := sha256.Sum256([]byte(p.config.Password))
	userMatch := subtle.ConstantTimeCompare(usernameHash[:], configuredUsernameHash[:])
	passwordMatch := subtle.ConstantTimeCompare(passwordHash[:], configuredPasswordHash[:])
	return userMatch&passwordMatch == 1
}

func (p *httpProxy) connect(writer http.ResponseWriter, request *http.Request) {
	target := request.Host
	if !strings.Contains(target, ":") {
		target += ":443"
	}
	upstream, err := p.dialer.DialContext(request.Context(), "tcp", target)
	if err != nil {
		http.Error(writer, "Bad Gateway", http.StatusBadGateway)
		return
	}

	hijacker, ok := writer.(http.Hijacker)
	if !ok {
		_ = upstream.Close()
		http.Error(writer, "Proxy tunneling is unavailable", http.StatusInternalServerError)
		return
	}
	client, buffered, err := hijacker.Hijack()
	if err != nil {
		_ = upstream.Close()
		return
	}
	if _, err := buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		_ = client.Close()
		_ = upstream.Close()
		return
	}
	if err := buffered.Flush(); err != nil {
		_ = client.Close()
		_ = upstream.Close()
		return
	}
	if buffered.Reader.Buffered() > 0 {
		if _, err := io.CopyN(upstream, buffered, int64(buffered.Reader.Buffered())); err != nil {
			_ = client.Close()
			_ = upstream.Close()
			return
		}
	}
	go tunnel(client, upstream)
}

func (p *httpProxy) forward(writer http.ResponseWriter, request *http.Request) {
	if request.URL == nil || !request.URL.IsAbs() || request.URL.Host == "" ||
		(request.URL.Scheme != "http" && request.URL.Scheme != "https") {
		http.Error(writer, "Forward proxy requests require an absolute http or https URL", http.StatusBadRequest)
		return
	}
	outbound := request.Clone(request.Context())
	outbound.RequestURI = ""
	outbound.Header = request.Header.Clone()
	removeHopHeaders(outbound.Header)
	outbound.Header.Del("Proxy-Authorization")
	outbound.Header.Del("Proxy-Connection")
	outbound.Header.Del("Forwarded")
	outbound.Header.Del("X-Forwarded-For")
	outbound.Header.Del("X-Forwarded-Host")
	outbound.Header.Del("X-Forwarded-Proto")
	response, err := p.transport.RoundTrip(outbound)
	if err != nil {
		http.Error(writer, "Bad Gateway", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	removeHopHeaders(response.Header)
	for key, values := range response.Header {
		for _, value := range values {
			writer.Header().Add(key, value)
		}
	}
	writer.WriteHeader(response.StatusCode)
	_, _ = io.Copy(writer, response.Body)
}

func removeHopHeaders(header http.Header) {
	for _, value := range header.Values("Connection") {
		for token := range strings.SplitSeq(value, ",") {
			header.Del(strings.TrimSpace(token))
		}
	}
	for _, key := range []string{
		"Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
		"Proxy-Connection", "TE", "Trailer", "Transfer-Encoding", "Upgrade",
	} {
		header.Del(key)
	}
}

func tunnel(left, right io.ReadWriteCloser) {
	done := make(chan struct{}, 2)
	copySide := func(destination io.Writer, source io.Reader) {
		_, _ = io.Copy(destination, source)
		done <- struct{}{}
	}
	go copySide(left, right)
	go copySide(right, left)
	<-done
	_ = left.Close()
	_ = right.Close()
	<-done
}

func (p *httpProxy) String() string {
	return fmt.Sprintf("http proxy %s", p.config.address())
}
