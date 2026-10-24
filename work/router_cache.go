package work

// Response-cache glue for tree routes. Cache packages store opaque HTTP records; this layer
// extracts and replays body, content type, status, and validator headers without entering the VM.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/kitwork/engine/utilities/cache"
	"github.com/kitwork/engine/utilities/persist"
	"github.com/kitwork/engine/value"
)

const (
	maxResponseCacheQueryFields = 16
	maxResponseCacheQueryName   = 64
	maxResponseCacheQueryValue  = 256
	maxResponseCacheQueryBytes  = 2048
)

type responseCacheQueryField struct {
	name    string
	allowed []string
}

// responseCacheKeyPolicy is shared by .cache() and .persist(). A nil policy preserves the
// historical full-query key; a non-nil policy projects only the declared query fields.
type responseCacheKeyPolicy struct {
	fields []responseCacheQueryField
}

func responseCacheQueryName(v value.Value) (string, error) {
	if !v.IsString() {
		return "", fmt.Errorf("cache query field names must be strings")
	}
	name := v.Text()
	if name == "" || name != strings.TrimSpace(name) || len(name) > maxResponseCacheQueryName {
		return "", fmt.Errorf("cache query field %q must be 1-%d characters without surrounding whitespace", name, maxResponseCacheQueryName)
	}
	return name, nil
}

func responseCacheAllowedValues(v value.Value) ([]string, error) {
	if v.K != value.Array {
		return nil, fmt.Errorf("cache query allowlists must be arrays of strings")
	}
	seen := map[string]struct{}{}
	allowed := make([]string, 0, len(v.Array()))
	for _, item := range v.Array() {
		if !item.IsString() {
			return nil, fmt.Errorf("cache query allowlists must contain only strings")
		}
		candidate := item.Text()
		if len(candidate) > maxResponseCacheQueryValue {
			return nil, fmt.Errorf("cache query allowlist values must be at most %d characters", maxResponseCacheQueryValue)
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		allowed = append(allowed, candidate)
	}
	if len(allowed) == 0 {
		return nil, fmt.Errorf("cache query allowlists cannot be empty")
	}
	sort.Strings(allowed)
	return allowed, nil
}

func parseResponseCacheKeyPolicy(selector value.Value) (*responseCacheKeyPolicy, error) {
	fields := map[string]responseCacheQueryField{}
	add := func(name string, allowed []string) error {
		if _, exists := fields[name]; exists {
			return nil
		}
		if len(fields) >= maxResponseCacheQueryFields {
			return fmt.Errorf("cache query projection supports at most %d fields", maxResponseCacheQueryFields)
		}
		fields[name] = responseCacheQueryField{name: name, allowed: allowed}
		return nil
	}

	switch selector.K {
	case value.String:
		name, err := responseCacheQueryName(selector)
		if err != nil {
			return nil, err
		}
		if err := add(name, nil); err != nil {
			return nil, err
		}
	case value.Array:
		for _, item := range selector.Array() {
			name, err := responseCacheQueryName(item)
			if err != nil {
				return nil, err
			}
			if err := add(name, nil); err != nil {
				return nil, err
			}
		}
	case value.Map:
		for name, rule := range selector.Map() {
			if _, err := responseCacheQueryName(value.New(name)); err != nil {
				return nil, err
			}
			switch rule.K {
			case value.Bool:
				if rule.N > 0 {
					if err := add(name, nil); err != nil {
						return nil, err
					}
				}
			case value.Array:
				allowed, err := responseCacheAllowedValues(rule)
				if err != nil {
					return nil, fmt.Errorf("cache query field %q: %w", name, err)
				}
				if err := add(name, allowed); err != nil {
					return nil, err
				}
			default:
				return nil, fmt.Errorf("cache query field %q must be true, false, or a string allowlist", name)
			}
		}
	default:
		return nil, fmt.Errorf("cache query projection must be a string, array, or object")
	}

	policy := &responseCacheKeyPolicy{fields: make([]responseCacheQueryField, 0, len(fields))}
	for _, field := range fields {
		policy.fields = append(policy.fields, field)
	}
	sort.Slice(policy.fields, func(i, j int) bool { return policy.fields[i].name < policy.fields[j].name })
	return policy, nil
}

func equalResponseCacheKeyPolicies(a, b *responseCacheKeyPolicy) bool {
	if a == nil || b == nil {
		return a == b
	}
	if len(a.fields) != len(b.fields) {
		return false
	}
	for i := range a.fields {
		if a.fields[i].name != b.fields[i].name || len(a.fields[i].allowed) != len(b.fields[i].allowed) {
			return false
		}
		for j := range a.fields[i].allowed {
			if a.fields[i].allowed[j] != b.fields[i].allowed[j] {
				return false
			}
		}
	}
	return true
}

func (m *FolderMethod) configureResponseCacheKey(args ...value.Value) {
	if m.cacheKeyErr != nil || len(args) < 2 {
		return
	}
	if len(args) > 2 {
		m.cacheKeyErr = fmt.Errorf("cache and persist accept only an expiry and an optional query projection")
		return
	}
	policy, err := parseResponseCacheKeyPolicy(args[1])
	if err != nil {
		m.cacheKeyErr = err
		return
	}
	if m.cacheKeyPolicy != nil && !equalResponseCacheKeyPolicies(m.cacheKeyPolicy, policy) {
		m.cacheKeyErr = fmt.Errorf("cache and persist on the same route must use the same query projection")
		return
	}
	m.cacheKeyPolicy = policy
}

func (f *FolderRouter) responseCacheConfigurationError() error {
	for methodName, method := range f.methods {
		if method.cacheKeyErr != nil {
			return fmt.Errorf("%s response cache: %w", methodName, method.cacheKeyErr)
		}
	}
	for outputPath, method := range f.outputs {
		if method.cacheKeyErr != nil {
			return fmt.Errorf("%s response cache: %w", outputPath, method.cacheKeyErr)
		}
	}
	return nil
}

func responseCacheValueAllowed(allowed []string, candidate string) bool {
	if len(allowed) == 0 {
		return true
	}
	index := sort.SearchStrings(allowed, candidate)
	return index < len(allowed) && allowed[index] == candidate
}

func cacheKey(r *http.Request, methodPolicy *FolderMethod) (string, bool) {
	method := r.Method
	if method == http.MethodHead {
		method = http.MethodGet
	}
	// a move by Drive (X-Kitwork-Hydrate, the old KitJS X-KitJS-Drive) is its own key, apart from the
	// load of the page
	drive := "0"
	if r.Header.Get("X-KitJS-Drive") == "1" || r.Header.Get("X-Kitwork-Hydrate") == "1" {
		drive = "1"
	}
	key := method + "\x00" + normalizedPath(r.URL.EscapedPath()) + "\x00drive=" + drive
	if r.URL.RawQuery == "" {
		return key, true
	}

	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return "", false
	}
	if methodPolicy.cacheKeyPolicy == nil {
		encoded := query.Encode()
		if len(encoded) > maxResponseCacheQueryBytes {
			return "", false
		}
		return key + "\x00query=" + encoded, true
	}

	projected := url.Values{}
	for _, field := range methodPolicy.cacheKeyPolicy.fields {
		values, exists := query[field.name]
		if !exists {
			continue
		}
		if len(values) != 1 || len(values[0]) > maxResponseCacheQueryValue || !responseCacheValueAllowed(field.allowed, values[0]) {
			return "", false
		}
		projected.Set(field.name, values[0])
	}
	encoded := projected.Encode()
	if len(encoded) > maxResponseCacheQueryBytes {
		return "", false
	}
	if encoded != "" {
		key += "\x00query=" + encoded
	}
	return key, true
}

// normalizedPath is the path a route answers, as the cache and the rate limits key it (KITWORK_ROUTER
// §2, 20/10/2026): a trailing slash and a doubled one change nothing, /a/ and /a//b are /a and /a/b.
func normalizedPath(p string) string {
	return path.Clean("/" + p)
}

func hashKey(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func responseBytes(resp *Response) (body []byte, contentType string, status int, headers map[string]string, ok bool) {
	if resp == nil || resp.Data().K == value.Invalid || resp.IsError() {
		return nil, "", 0, nil, false
	}
	status = resp.Code()
	if status == 0 {
		status = http.StatusOK
	}
	headers = resp.headerMap()
	switch resp.Kind() {
	case "html", "":
		return []byte(resp.Data().String()), resp.mediaType("text/html; charset=utf-8"), status, headers, true
	case "text":
		return []byte(resp.Data().String()), resp.mediaType("text/plain; charset=utf-8"), status, headers, true
	case "css":
		return []byte(resp.Data().String()), resp.mediaType("text/css; charset=utf-8"), status, headers, true
	case "svg":
		return []byte(resp.Data().String()), resp.mediaType("image/svg+xml; charset=utf-8"), status, headers, true
	case "typed":
		return []byte(resp.Data().String()), resp.ContentType(), status, headers, true
	case "json":
		data, err := json.Marshal(resp.Data())
		if err != nil {
			return nil, "", 0, nil, false
		}
		return data, resp.mediaType("application/json; charset=utf-8"), status, headers, true
	case "image":
		return resp.Data().Bytes(), resp.mediaType("image/png"), status, headers, true
	case "bytes":
		return resp.Data().Bytes(), resp.mediaType("application/octet-stream"), status, headers, true
	default:
		return nil, "", 0, nil, false
	}
}

func serveCached(
	w http.ResponseWriter,
	request *http.Request,
	dynamic *Response,
	body []byte,
	contentType string,
	status int,
	headers map[string]string,
) {
	if status == 0 {
		status = http.StatusOK
	}
	w.Header().Set("Content-Type", contentType)
	for name, data := range headers {
		w.Header().Set(name, data)
	}
	// Folder guards still run on a cache hit. Their per-request headers and cookies must win over
	// the stored representation rather than being silently dropped.
	if dynamic != nil {
		dynamic.writeHeaders(w)
		dynamic.writeCookies(w)
	}
	allowThemePrepaint(w.Header(), body)
	w.Header().Set("X-Kitwork-Cache", "hit")
	if requestNotModified(request, w.Header()) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.WriteHeader(status)
	if request.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

// cachedResponse reads the route's cache, RAM then disk (KITWORK_ROUTER §9, 20/10/2026). A disk entry
// still fresh when RAM has none fills RAM again, for no longer than what is left of the disk entry: a
// read never stretches the first expiry.
func (t *Tenant) cachedResponse(
	method *FolderMethod,
	key string,
) (body []byte, contentType string, status int, headers map[string]string, ok bool) {
	if method.cacheExpiry != nil {
		if entry, hit := t.respCache.Get(key); hit {
			return entry.Body, entry.ContentType, entry.Status, entry.Headers, true
		}
	}
	if method.persistExpiry != nil {
		if record, hit := t.persistStore.Get(hashKey(key)); hit {
			if method.cacheExpiry != nil {
				now := t.routeNow()
				t.respCache.Set(key, cache.Entry{
					Body: record.Body, ContentType: record.ContentType, Status: record.Status, Headers: record.Headers,
				}, refillExpiry(method.cacheExpiry(now), record.ExpireAt, now))
			}
			return record.Body, record.ContentType, record.Status, record.Headers, true
		}
	}
	return nil, "", 0, nil, false
}

// refillExpiry is how long RAM keeps a disk entry it is filled from: its own span, cut to what is left
// of the disk entry (0, forever, takes what is left; a disk entry with no expiry leaves RAM its own).
func refillExpiry(ram time.Duration, diskExpireAt, now time.Time) time.Duration {
	if diskExpireAt.IsZero() {
		return ram
	}
	left := diskExpireAt.Sub(now)
	if left <= 0 {
		left = time.Millisecond
	}
	if ram <= 0 || ram > left {
		return left
	}
	return ram
}

// cacheableStatus are the statuses HTTP caches by default (RFC 9111): only these are kept; never 5xx.
var cacheableStatus = map[int]bool{200: true, 203: true, 204: true, 300: true, 301: true, 308: true, 404: true, 405: true, 410: true, 414: true}

// sharedCacheable reports whether an answer may be kept for everyone the guards let through
// (KITWORK_ROUTER §9): a status HTTP caches, no Set-Cookie of the answer, no Cache-Control private or
// no-store. cookiesBefore is how many cookies the guards set: they run on every request, a hit included
// (serveCached), so theirs are not the answer's.
func sharedCacheable(response *Response, cookiesBefore int, status int, headers map[string]string) bool {
	if !cacheableStatus[status] || len(response.cookies) > cookiesBefore {
		return false
	}
	for name, data := range headers {
		switch strings.ToLower(name) {
		case "set-cookie":
			return false
		case "cache-control":
			directives := strings.ToLower(data)
			if strings.Contains(directives, "private") || strings.Contains(directives, "no-store") {
				return false
			}
		}
	}
	return true
}

func (t *Tenant) saveResponse(method *FolderMethod, key string, response *Response, cookiesBefore int) {
	body, contentType, status, headers, ok := responseBytes(response)
	if !ok || !sharedCacheable(response, cookiesBefore, status, headers) {
		return
	}
	// a boundary ("hourly", "nextday 03:00") falls on the app's clock in a Kitwork UI site (routeNow)
	now := t.routeNow()
	if method.cacheExpiry != nil {
		t.respCache.Set(key, cache.Entry{
			Body: body, ContentType: contentType, Status: status, Headers: headers,
		}, method.cacheExpiry(now))
	}
	if method.persistExpiry != nil {
		_ = t.persistStore.Set(hashKey(key), persist.Record{
			Body: body, ContentType: contentType, Status: status, Headers: headers,
		}, method.persistExpiry(now))
	}
}
