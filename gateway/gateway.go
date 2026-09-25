// Package gateway implements the host-owned reverse-proxy surface declared by
// app.gateway(...). Each Handler is an immutable configuration snapshot;
// Manager publishes replacements atomically and drains superseded snapshots.
package gateway

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	maxTargetsPerGateway    = 256
	maxPoolWeight           = 4096
	defaultFailureThreshold = int32(3)
	defaultFailureCooldown  = 10 * time.Second
	defaultHealthInterval   = 10 * time.Second
	defaultHealthTimeout    = 2 * time.Second
)

// Target maps an optional incoming domain to one operator-controlled backend.
// An empty Domain belongs to the gateway's default pool. Health is an optional
// absolute path on Target, for example "/health".
type Target struct {
	Domain string `json:"domain,omitempty" yaml:"domain,omitempty"`
	Target string `json:"target" yaml:"target"`
	Weight int    `json:"weight,omitempty" yaml:"weight,omitempty"`
	Health string `json:"health,omitempty" yaml:"health,omitempty"`
}

// TargetStats is a bounded point-in-time view suitable for diagnostics and a
// future control-plane dashboard. It contains no request headers or secrets.
type TargetStats struct {
	Domain              string    `json:"domain,omitempty"`
	Target              string    `json:"target"`
	Weight              int       `json:"weight"`
	Health              string    `json:"health,omitempty"`
	Available           bool      `json:"available"`
	ConsecutiveFailures int32     `json:"consecutive_failures"`
	UnavailableUntil    time.Time `json:"unavailable_until,omitempty"`
	Requests            uint64    `json:"requests"`
	Responses           uint64    `json:"responses"`
	Failures            uint64    `json:"failures"`
	HealthChecks        uint64    `json:"health_checks"`
	HealthFailures      uint64    `json:"health_failures"`
}

type target struct {
	config           Target
	proxy            *httputil.ReverseProxy
	healthURL        *url.URL
	failureThreshold int32
	cooldown         time.Duration
	consecutive      atomic.Int32
	unavailableUntil atomic.Int64
	requests         atomic.Uint64
	responses        atomic.Uint64
	failures         atomic.Uint64
	healthChecks     atomic.Uint64
	healthFailures   atomic.Uint64
}

func (t *target) available(now time.Time) bool {
	until := t.unavailableUntil.Load()
	return until == 0 || now.UnixNano() >= until
}

func (t *target) success() {
	t.consecutive.Store(0)
	t.unavailableUntil.Store(0)
}

func (t *target) failure(now time.Time) {
	t.failures.Add(1)
	if t.consecutive.Add(1) >= t.failureThreshold {
		t.unavailableUntil.Store(now.Add(t.cooldown).UnixNano())
	}
}

func (t *target) stats(now time.Time) TargetStats {
	until := t.unavailableUntil.Load()
	var unavailableUntil time.Time
	if until != 0 {
		unavailableUntil = time.Unix(0, until)
	}
	return TargetStats{
		Domain:              t.config.Domain,
		Target:              t.config.Target,
		Weight:              t.config.Weight,
		Health:              t.config.Health,
		Available:           t.available(now),
		ConsecutiveFailures: t.consecutive.Load(),
		UnavailableUntil:    unavailableUntil,
		Requests:            t.requests.Load(),
		Responses:           t.responses.Load(),
		Failures:            t.failures.Load(),
		HealthChecks:        t.healthChecks.Load(),
		HealthFailures:      t.healthFailures.Load(),
	}
}

type preparedTarget struct {
	config Target
	target *target
}

type pool struct {
	schedule []*target
	next     atomic.Uint64
}

func (p *pool) serve(writer http.ResponseWriter, request *http.Request) {
	start := p.next.Add(1) - 1
	now := time.Now()
	for offset := range uint64(len(p.schedule)) {
		selected := p.schedule[(start+offset)%uint64(len(p.schedule))]
		if selected.available(now) {
			selected.requests.Add(1)
			selected.proxy.ServeHTTP(writer, request)
			return
		}
	}
	http.Error(writer, "Service Unavailable", http.StatusServiceUnavailable)
}

type handlerOptions struct {
	failureThreshold int32
	failureCooldown  time.Duration
	healthInterval   time.Duration
	healthTimeout    time.Duration
}

func defaultHandlerOptions() handlerOptions {
	return handlerOptions{
		failureThreshold: defaultFailureThreshold,
		failureCooldown:  defaultFailureCooldown,
		healthInterval:   defaultHealthInterval,
		healthTimeout:    defaultHealthTimeout,
	}
}

// Handler dispatches immutable exact-domain pools and one optional default
// pool. It exposes match methods rather than ServeHTTP so core.Engine keeps
// host rate limiting and hostname normalization ahead of gateway dispatch.
type Handler struct {
	exact        map[string]*pool
	fallback     *pool
	targets      []*target
	transport    *http.Transport
	healthClient *http.Client
	healthEvery  time.Duration
	cancel       context.CancelFunc
	wait         sync.WaitGroup
	closeOnce    sync.Once
}

// Validate checks the public target contract without starting transports or
// health workers.
func Validate(targets []Target) error {
	_, err := prepare(targets)
	return err
}

// New validates targets, builds smooth weighted schedules, and starts bounded
// health workers for targets that declare a health path.
func New(targets []Target) (*Handler, error) {
	return newHandler(targets, defaultHandlerOptions())
}

func newHandler(targets []Target, options handlerOptions) (*Handler, error) {
	prepared, err := prepare(targets)
	if err != nil {
		return nil, err
	}
	if options.failureThreshold < 1 {
		options.failureThreshold = defaultFailureThreshold
	}
	if options.failureCooldown <= 0 {
		options.failureCooldown = defaultFailureCooldown
	}
	if options.healthInterval <= 0 {
		options.healthInterval = defaultHealthInterval
	}
	if options.healthTimeout <= 0 {
		options.healthTimeout = defaultHealthTimeout
	}

	groups := make(map[string][]preparedTarget)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	handler := &Handler{
		exact:        make(map[string]*pool),
		transport:    transport,
		healthClient: &http.Client{Transport: transport, Timeout: options.healthTimeout},
		healthEvery:  options.healthInterval,
	}
	for _, configured := range prepared {
		destination, _ := url.Parse(configured.Target)
		healthURL, _ := healthTarget(destination, configured.Health)
		current := &target{
			config:           configured,
			healthURL:        healthURL,
			failureThreshold: options.failureThreshold,
			cooldown:         options.failureCooldown,
		}
		current.proxy = newReverseProxy(current, destination, transport)
		handler.targets = append(handler.targets, current)
		groups[configured.Domain] = append(groups[configured.Domain], preparedTarget{
			config: configured,
			target: current,
		})
	}

	for domain, group := range groups {
		built, err := buildPool(group)
		if err != nil {
			transport.CloseIdleConnections()
			return nil, fmt.Errorf("gateway domain %q: %w", domain, err)
		}
		if domain == "" {
			handler.fallback = built
		} else {
			handler.exact[domain] = built
		}
	}
	healthContext, cancel := context.WithCancel(context.Background())
	handler.cancel = cancel
	for _, current := range handler.targets {
		if current.healthURL == nil {
			continue
		}
		handler.wait.Add(1)
		go handler.runHealthChecks(healthContext, current)
	}
	return handler, nil
}

func prepare(targets []Target) ([]Target, error) {
	if len(targets) == 0 {
		return nil, fmt.Errorf("gateway requires at least one target")
	}
	if len(targets) > maxTargetsPerGateway {
		return nil, fmt.Errorf("gateway has %d targets; limit is %d", len(targets), maxTargetsPerGateway)
	}

	prepared := make([]Target, 0, len(targets))
	seen := make(map[string]struct{}, len(targets))
	poolWeights := make(map[string]int)
	for index, configured := range targets {
		domain, err := normalizeDomain(configured.Domain)
		if err != nil {
			return nil, fmt.Errorf("gateway target %d: %w", index+1, err)
		}
		destination, err := parseTarget(configured.Target)
		if err != nil {
			return nil, fmt.Errorf("gateway target %d: %w", index+1, err)
		}
		if _, err := healthTarget(destination, configured.Health); err != nil {
			return nil, fmt.Errorf("gateway target %d: %w", index+1, err)
		}
		weight := configured.Weight
		if weight == 0 {
			weight = 1
		}
		if weight < 1 {
			return nil, fmt.Errorf("gateway target %d: weight must be at least 1", index+1)
		}
		poolWeights[domain] += weight
		if poolWeights[domain] > maxPoolWeight {
			return nil, fmt.Errorf("gateway domain %q: total weight exceeds %d", domain, maxPoolWeight)
		}

		key := domain + "\x00" + destination.String()
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("gateway target %d duplicates backend %q for domain %q", index+1, destination.String(), domain)
		}
		seen[key] = struct{}{}
		configured.Domain = domain
		configured.Target = destination.String()
		configured.Weight = weight
		configured.Health = strings.TrimSpace(configured.Health)
		prepared = append(prepared, configured)
	}
	return prepared, nil
}

func buildPool(targets []preparedTarget) (*pool, error) {
	total := 0
	for _, item := range targets {
		total += item.config.Weight
		if total > maxPoolWeight {
			return nil, fmt.Errorf("total weight exceeds %d", maxPoolWeight)
		}
	}

	// Smooth weighted round-robin is prepared once. Request-time selection is
	// one atomic increment followed by an immutable availability scan.
	current := make([]int, len(targets))
	schedule := make([]*target, 0, total)
	for range total {
		selected := 0
		for index := range targets {
			current[index] += targets[index].config.Weight
			if current[index] > current[selected] {
				selected = index
			}
		}
		current[selected] -= total
		schedule = append(schedule, targets[selected].target)
	}
	return &pool{schedule: schedule}, nil
}

func newReverseProxy(current *target, destination *url.URL, transport http.RoundTripper) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(request *httputil.ProxyRequest) {
			request.Out.Header.Del("Forwarded")
			request.Out.Header.Del("X-Forwarded-For")
			request.Out.Header.Del("X-Forwarded-Host")
			request.Out.Header.Del("X-Forwarded-Proto")
			request.SetURL(destination)
			request.Out.Host = request.In.Host
			request.SetXForwarded()
		},
		ModifyResponse: func(response *http.Response) error {
			current.responses.Add(1)
			if response.StatusCode == http.StatusBadGateway ||
				response.StatusCode == http.StatusServiceUnavailable ||
				response.StatusCode == http.StatusGatewayTimeout {
				current.failure(time.Now())
			} else {
				current.success()
			}
			return nil
		},
		ErrorHandler: func(writer http.ResponseWriter, request *http.Request, err error) {
			current.failure(time.Now())
			slog.Warn("Gateway target failed", "target", destination.Redacted(), "error", err)
			http.Error(writer, "Bad Gateway", http.StatusBadGateway)
		},
	}
}

func (h *Handler) runHealthChecks(ctx context.Context, current *target) {
	defer h.wait.Done()
	h.checkHealth(ctx, current)
	ticker := time.NewTicker(h.healthEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.checkHealth(ctx, current)
		}
	}
}

func (h *Handler) checkHealth(ctx context.Context, current *target) {
	current.healthChecks.Add(1)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, current.healthURL.String(), nil)
	if err != nil {
		current.healthFailures.Add(1)
		current.failure(time.Now())
		return
	}
	response, err := h.healthClient.Do(request)
	if err != nil {
		if ctx.Err() == nil {
			current.healthFailures.Add(1)
			current.failure(time.Now())
		}
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	_ = response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 400 {
		current.healthFailures.Add(1)
		current.failure(time.Now())
		return
	}
	current.success()
}

// ServeExact proxies a request when domain has an explicitly declared pool.
func (h *Handler) ServeExact(writer http.ResponseWriter, request *http.Request, domain string) bool {
	if h == nil {
		return false
	}
	selected := h.exact[strings.ToLower(strings.TrimSuffix(domain, "."))]
	if selected == nil {
		return false
	}
	selected.serve(writer, request)
	return true
}

// ServeDefault proxies through the domainless pool when one was declared.
func (h *Handler) ServeDefault(writer http.ResponseWriter, request *http.Request) bool {
	if h == nil || h.fallback == nil {
		return false
	}
	h.fallback.serve(writer, request)
	return true
}

// HasDefault reports whether domainless targets were declared.
func (h *Handler) HasDefault() bool { return h != nil && h.fallback != nil }

// Snapshot returns a bounded, stable-order view of every configured target.
func (h *Handler) Snapshot() []TargetStats {
	if h == nil {
		return nil
	}
	now := time.Now()
	result := make([]TargetStats, 0, len(h.targets))
	for _, current := range h.targets {
		result = append(result, current.stats(now))
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Domain != result[j].Domain {
			return result[i].Domain < result[j].Domain
		}
		return result[i].Target < result[j].Target
	})
	return result
}

// Close stops health workers and releases idle upstream connections.
func (h *Handler) Close() {
	if h == nil {
		return
	}
	h.closeOnce.Do(func() {
		if h.cancel != nil {
			h.cancel()
		}
		h.wait.Wait()
		if h.transport != nil {
			h.transport.CloseIdleConnections()
		}
	})
}

func parseTarget(source string) (*url.URL, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return nil, fmt.Errorf("target is required")
	}
	parsed, err := url.Parse(source)
	if err != nil {
		return nil, fmt.Errorf("invalid target %q: %w", source, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("target %q must use http or https", source)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("target %q must include a host", source)
	}
	if parsed.User != nil {
		return nil, fmt.Errorf("target %q must not contain credentials", source)
	}
	if parsed.Fragment != "" {
		return nil, fmt.Errorf("target %q must not contain a fragment", source)
	}
	return parsed, nil
}

func healthTarget(destination *url.URL, source string) (*url.URL, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return nil, nil
	}
	if !strings.HasPrefix(source, "/") {
		return nil, fmt.Errorf("health must be an absolute path")
	}
	reference, err := url.Parse(source)
	if err != nil || reference.IsAbs() || reference.Host != "" || reference.Fragment != "" {
		return nil, fmt.Errorf("health %q is invalid", source)
	}
	resolved := *destination
	resolved.Path = reference.Path
	resolved.RawPath = reference.RawPath
	resolved.RawQuery = reference.RawQuery
	resolved.Fragment = ""
	return &resolved, nil
}

func normalizeDomain(source string) (string, error) {
	domain := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(source, ".")))
	if domain == "" {
		return "", nil
	}
	if len(domain) > 253 || strings.Contains(domain, ":") {
		return "", fmt.Errorf("domain %q is invalid", source)
	}
	for _, label := range strings.Split(domain, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("domain %q is invalid", source)
		}
		for _, char := range label {
			if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
				return "", fmt.Errorf("domain %q is invalid", source)
			}
		}
	}
	return domain, nil
}
