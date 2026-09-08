// Package manifest parses native application surfaces from Kitwork's executable
// app manifest. It deliberately has no dependency on the engine runtime so native
// build tools can share one strict, deterministic schema.
package manifest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MobileVersion1      = 1
	MobileVersion       = 10
	DefaultTitle        = "Kitwork"
	DefaultVersionName  = "0.1.0"
	DefaultVersionCode  = 1
	DefaultOrientation  = "auto"
	DefaultThemeMode    = "system"
	DefaultThemeColor   = "#09090b"
	maxTitleRunes       = 80
	maxIdentifierBytes  = 255
	maxVersionNameBytes = 32
	maxIconBytes        = 512
	maxDomainBytes      = 253
	maxStartPathBytes   = 2048
	maxLinkSchemeBytes  = 64
	// MaxLinkHosts bounds the verified HTTPS origins compiled into one shell.
	MaxLinkHosts   = 8
	maxVersionCode = 2_100_000_000
)

var versionNamePattern = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.(0|[1-9][0-9]*)){0,2}$`)
var linkSchemePattern = regexp.MustCompile(`^[a-z][a-z0-9+.-]{2,63}$`)

var mobileKeys = keySet(
	"version", "id", "versionName", "versionCode", "start", "orientation", "theme", "permissions", "links", "network",
)

var encodedMobileKeys = keySet(
	"version", "id", "title", "icon", "versionName", "versionCode", "start", "orientation", "theme", "permissions", "links", "network",
)

var startKeys = keySet("domain", "path")
var themeKeys = keySet("mode", "color")
var linksV7Keys = keySet("scheme")
var linksV8Keys = keySet("scheme", "hosts")

var permissionSet = keySet(
	"clipboard.writeText",
	"clipboard.readText",
	"share.open",
	"shell.open",
	"secureStorage.get",
	"secureStorage.set",
	"secureStorage.remove",
	"device.info",
	"device.vibrate",
	"network.status",
	"files.import",
	"files.read",
	"files.share",
)

var permissionSetV2 = func() map[string]struct{} {
	result := make(map[string]struct{}, len(permissionSet)+1)
	for permission := range permissionSet {
		result[permission] = struct{}{}
	}
	result["files.export"] = struct{}{}
	return result
}()

var permissionSetV3 = func() map[string]struct{} {
	result := make(map[string]struct{}, len(permissionSetV2)+1)
	for permission := range permissionSetV2 {
		result[permission] = struct{}{}
	}
	result["camera.capture"] = struct{}{}
	return result
}()

var permissionSetV4 = func() map[string]struct{} {
	result := make(map[string]struct{}, len(permissionSetV3)+1)
	for permission := range permissionSetV3 {
		result[permission] = struct{}{}
	}
	result["qr.scan"] = struct{}{}
	return result
}()

var permissionSetV5 = func() map[string]struct{} {
	result := make(map[string]struct{}, len(permissionSetV4)+1)
	for permission := range permissionSetV4 {
		result[permission] = struct{}{}
	}
	result["screen.keepAwake"] = struct{}{}
	return result
}()

var permissionSetV6 = func() map[string]struct{} {
	result := make(map[string]struct{}, len(permissionSetV5)+1)
	for permission := range permissionSetV5 {
		result[permission] = struct{}{}
	}
	result["notifications.show"] = struct{}{}
	return result
}()

var permissionSetV7 = func() map[string]struct{} {
	result := make(map[string]struct{}, len(permissionSetV6)+1)
	for permission := range permissionSetV6 {
		result[permission] = struct{}{}
	}
	result["deepLinks.receive"] = struct{}{}
	return result
}()

var permissionSetV8 = func() map[string]struct{} {
	result := make(map[string]struct{}, len(permissionSetV7)+1)
	for permission := range permissionSetV7 {
		result[permission] = struct{}{}
	}
	result["notifications.tap"] = struct{}{}
	return result
}()

// SupportedMobileVersion reports whether version is a strict manifest schema
// understood by this build. Version 1 remains readable and keeps its original
// thirteen-permission allowlist; version 2 adds files.export, version 3 adds
// camera.capture, version 4 adds qr.scan, version 5 adds screen.keepAwake, and
// version 6 adds immediate local notifications, version 7 adds bounded
// same-app custom-scheme receipt, and version 8 adds verified HTTPS origins
// plus explicit notification-tap routing without granting newer authority to
// older manifests.
func SupportedMobileVersion(version int) bool {
	return version >= MobileVersion1 && version <= MobileVersion
}

func permissionsForMobileVersion(version int) map[string]struct{} {
	if version >= 10 {
		return permissionSetV10
	}
	if version >= 9 {
		return permissionSetV9
	}
	if version >= 8 {
		return permissionSetV8
	}
	if version >= 7 {
		return permissionSetV7
	}
	if version >= 6 {
		return permissionSetV6
	}
	if version >= 5 {
		return permissionSetV5
	}
	if version >= 4 {
		return permissionSetV4
	}
	if version == 3 {
		return permissionSetV3
	}
	if version == 2 {
		return permissionSetV2
	}
	return permissionSet
}

// Mobile is the resolved mobile surface consumed by native build and shell
// tooling. Declared distinguishes an absent surface from one using defaults.
// Legacy marks the intentionally unvalidated pre-v1 forms such as mobile(true)
// and unversioned objects; native tooling must not treat Legacy as a grant.
type Mobile struct {
	Declared    bool
	Legacy      bool
	Version     int
	ID          string
	Title       string
	Icon        string
	VersionName string
	VersionCode int
	Start       Start
	Orientation string
	Theme       Theme
	Permissions []string
	Links       *Links
	Network     *Network
}

// Start selects one tenant and an internal route. It never contains an origin:
// the native host owns the private origin/transport for the selected platform.
type Start struct {
	Domain string `json:"domain"`
	Path   string `json:"path"`
}

// Theme contains only native presentation defaults. Page styling remains owned
// by the tenant's Kitwork theme.
type Theme struct {
	Mode  string `json:"mode"`
	Color string `json:"color"`
}

// Links declares ingress origins owned by a mobile shell. V7 requires one
// custom scheme. V8 may use a custom scheme, up to eight verified HTTPS hosts,
// or both. Native hosts expose only a normalized origin-free internal path.
type Links struct {
	Scheme string   `json:"scheme,omitempty"`
	Hosts  []string `json:"hosts,omitempty"`
}

type encodedMobile struct {
	Version     int      `json:"version"`
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Icon        string   `json:"icon,omitempty"`
	VersionName string   `json:"versionName"`
	VersionCode int      `json:"versionCode"`
	Start       Start    `json:"start"`
	Orientation string   `json:"orientation"`
	Theme       Theme    `json:"theme"`
	Permissions []string `json:"permissions"`
	Links       *Links   `json:"links,omitempty"`
	Network     *Network `json:"network,omitempty"`
}

// MarshalJSON is intentionally identical to EncodeJSON so generic JSON callers
// cannot accidentally expose the parser-only Declared and Legacy fields.
func (mobile Mobile) MarshalJSON() ([]byte, error) {
	return EncodeJSON(mobile)
}

// Parse resolves the mobile surface from the raw map returned by
// engine.ReadManifest. Root keys unrelated to mobile are intentionally ignored.
// A versioned block is strict; absent and unversioned mobile declarations stay
// readable for compatibility and are marked Legacy instead of being interpreted.
func Parse(raw map[string]interface{}) (Mobile, error) {
	result := defaultMobile()
	if raw == nil {
		return result, nil
	}

	mobileValue, declared := raw["mobile"]
	if !declared {
		applyLegacyIdentity(&result, raw)
		return result, nil
	}
	result.Declared = true

	mobile, ok := asObject(mobileValue)
	if !ok {
		result.Legacy = true
		applyLegacyIdentity(&result, raw)
		return result, nil
	}

	versionValue, versioned := mobile["version"]
	if !versioned {
		result.Legacy = true
		applyLegacyIdentity(&result, raw)
		return result, nil
	}
	version, err := boundedInteger(versionValue, MobileVersion1, MobileVersion)
	if err != nil {
		return Mobile{}, fieldError("mobile.version", err)
	}
	result.Version = version
	if result.Version < 7 {
		if _, exists := mobile["links"]; exists {
			return Mobile{}, fieldError("mobile.links", fmt.Errorf("requires mobile.version 7 or newer"))
		}
	}

	if err := rejectUnknownKeys("mobile", mobile, mobileKeys); err != nil {
		return Mobile{}, err
	}
	if err := parseIdentity(&result, raw); err != nil {
		return Mobile{}, err
	}

	id, err := requiredString(mobile, "id", "mobile.id")
	if err != nil {
		return Mobile{}, err
	}
	if err := validateIdentifier(id); err != nil {
		return Mobile{}, fieldError("mobile.id", err)
	}
	result.ID = id

	if value, exists := mobile["versionName"]; exists {
		versionName, ok := value.(string)
		if !ok {
			return Mobile{}, fieldError("mobile.versionName", fmt.Errorf("must be a string"))
		}
		if err := validateVersionName(versionName); err != nil {
			return Mobile{}, fieldError("mobile.versionName", err)
		}
		result.VersionName = versionName
	}
	if value, exists := mobile["versionCode"]; exists {
		versionCode, err := boundedInteger(value, 1, maxVersionCode)
		if err != nil {
			return Mobile{}, fieldError("mobile.versionCode", err)
		}
		result.VersionCode = versionCode
	}

	startValue, exists := mobile["start"]
	if !exists {
		return Mobile{}, fieldError("mobile.start", fmt.Errorf("is required"))
	}
	start, ok := asObject(startValue)
	if !ok {
		return Mobile{}, fieldError("mobile.start", fmt.Errorf("must be an object"))
	}
	if err := rejectUnknownKeys("mobile.start", start, startKeys); err != nil {
		return Mobile{}, err
	}
	domain, err := requiredString(start, "domain", "mobile.start.domain")
	if err != nil {
		return Mobile{}, err
	}
	if err := validateDomain(domain); err != nil {
		return Mobile{}, fieldError("mobile.start.domain", err)
	}
	result.Start.Domain = domain
	if value, exists := start["path"]; exists {
		startPath, ok := value.(string)
		if !ok {
			return Mobile{}, fieldError("mobile.start.path", fmt.Errorf("must be a string"))
		}
		if err := validateStartPath(startPath); err != nil {
			return Mobile{}, fieldError("mobile.start.path", err)
		}
		result.Start.Path = startPath
	}

	if value, exists := mobile["orientation"]; exists {
		orientation, ok := value.(string)
		if !ok {
			return Mobile{}, fieldError("mobile.orientation", fmt.Errorf("must be a string"))
		}
		switch orientation {
		case "auto", "portrait", "landscape":
			result.Orientation = orientation
		default:
			return Mobile{}, fieldError("mobile.orientation", fmt.Errorf("must be auto, portrait, or landscape"))
		}
	}

	if value, exists := mobile["theme"]; exists {
		theme, ok := asObject(value)
		if !ok {
			return Mobile{}, fieldError("mobile.theme", fmt.Errorf("must be an object"))
		}
		if err := rejectUnknownKeys("mobile.theme", theme, themeKeys); err != nil {
			return Mobile{}, err
		}
		if modeValue, exists := theme["mode"]; exists {
			mode, ok := modeValue.(string)
			if !ok {
				return Mobile{}, fieldError("mobile.theme.mode", fmt.Errorf("must be a string"))
			}
			switch mode {
			case "system", "light", "dark":
				result.Theme.Mode = mode
			default:
				return Mobile{}, fieldError("mobile.theme.mode", fmt.Errorf("must be system, light, or dark"))
			}
		}
		if colorValue, exists := theme["color"]; exists {
			color, ok := colorValue.(string)
			if !ok {
				return Mobile{}, fieldError("mobile.theme.color", fmt.Errorf("must be a string"))
			}
			if !validHexColor(color) {
				return Mobile{}, fieldError("mobile.theme.color", fmt.Errorf("must use #RRGGBB"))
			}
			result.Theme.Color = strings.ToLower(color)
		}
	}

	if value, exists := mobile["permissions"]; exists {
		allowedPermissions := permissionsForMobileVersion(result.Version)
		permissions, ok := asSlice(value)
		if !ok {
			return Mobile{}, fieldError("mobile.permissions", fmt.Errorf("must be an array"))
		}
		if len(permissions) > len(allowedPermissions) {
			return Mobile{}, fieldError("mobile.permissions", fmt.Errorf("contains too many entries"))
		}
		seen := make(map[string]struct{}, len(permissions))
		result.Permissions = make([]string, 0, len(permissions))
		for index, permissionValue := range permissions {
			permission, ok := permissionValue.(string)
			if !ok {
				return Mobile{}, fieldError(fmt.Sprintf("mobile.permissions[%d]", index), fmt.Errorf("must be a string"))
			}
			if _, allowed := allowedPermissions[permission]; !allowed {
				return Mobile{}, fieldError(fmt.Sprintf("mobile.permissions[%d]", index), fmt.Errorf("unsupported permission %q", permission))
			}
			if _, duplicate := seen[permission]; duplicate {
				return Mobile{}, fieldError(fmt.Sprintf("mobile.permissions[%d]", index), fmt.Errorf("duplicates %q", permission))
			}
			seen[permission] = struct{}{}
			result.Permissions = append(result.Permissions, permission)
		}
	}

	linksValue, hasLinks := mobile["links"]
	if err := parseNetwork(&result, mobile); err != nil {
		return Mobile{}, err
	}
	wantsDeepLinks := hasString(result.Permissions, "deepLinks.receive")
	if hasLinks != wantsDeepLinks {
		if hasLinks {
			return Mobile{}, fieldError("mobile.links", fmt.Errorf("requires permission deepLinks.receive"))
		}
		return Mobile{}, fieldError("mobile.links", fmt.Errorf("is required with permission deepLinks.receive"))
	}
	if hasLinks {
		links, ok := asObject(linksValue)
		if !ok {
			return Mobile{}, fieldError("mobile.links", fmt.Errorf("must be an object"))
		}
		allowedLinkKeys := linksV7Keys
		if result.Version >= 8 {
			allowedLinkKeys = linksV8Keys
		}
		if err := rejectUnknownKeys("mobile.links", links, allowedLinkKeys); err != nil {
			return Mobile{}, err
		}
		resolved := &Links{}
		if schemeValue, exists := links["scheme"]; exists {
			scheme, ok := schemeValue.(string)
			if !ok {
				return Mobile{}, fieldError("mobile.links.scheme", fmt.Errorf("must be a string"))
			}
			if err := validateLinkScheme(scheme); err != nil {
				return Mobile{}, fieldError("mobile.links.scheme", err)
			}
			resolved.Scheme = scheme
		} else if result.Version == 7 {
			return Mobile{}, fieldError("mobile.links.scheme", fmt.Errorf("is required"))
		}
		if hostsValue, exists := links["hosts"]; exists {
			hostValues, ok := asSlice(hostsValue)
			if !ok {
				return Mobile{}, fieldError("mobile.links.hosts", fmt.Errorf("must be an array"))
			}
			if len(hostValues) == 0 || len(hostValues) > MaxLinkHosts {
				return Mobile{}, fieldError("mobile.links.hosts", fmt.Errorf("must contain 1 to %d hosts", MaxLinkHosts))
			}
			seen := make(map[string]struct{}, len(hostValues))
			resolved.Hosts = make([]string, 0, len(hostValues))
			for index, hostValue := range hostValues {
				host, ok := hostValue.(string)
				if !ok {
					return Mobile{}, fieldError(fmt.Sprintf("mobile.links.hosts[%d]", index), fmt.Errorf("must be a string"))
				}
				if err := validateLinkHost(host); err != nil {
					return Mobile{}, fieldError(fmt.Sprintf("mobile.links.hosts[%d]", index), err)
				}
				if _, duplicate := seen[host]; duplicate {
					return Mobile{}, fieldError(fmt.Sprintf("mobile.links.hosts[%d]", index), fmt.Errorf("duplicates %q", host))
				}
				seen[host] = struct{}{}
				resolved.Hosts = append(resolved.Hosts, host)
			}
			sort.Strings(resolved.Hosts)
		}
		if resolved.Scheme == "" && len(resolved.Hosts) == 0 {
			return Mobile{}, fieldError("mobile.links", fmt.Errorf("requires scheme, hosts, or both"))
		}
		result.Links = resolved
	}

	if hasString(result.Permissions, "notifications.schedule") && !hasString(result.Permissions, "notifications.show") {
		return Mobile{}, fieldError("mobile.permissions", fmt.Errorf("notifications.schedule requires notifications.show"))
	}
	if hasString(result.Permissions, "notifications.tap") {
		if !hasString(result.Permissions, "notifications.show") || !wantsDeepLinks {
			return Mobile{}, fieldError(
				"mobile.permissions",
				fmt.Errorf("notifications.tap requires notifications.show and deepLinks.receive"),
			)
		}
	}

	return result, nil
}

// EncodeJSON writes the canonical, resolved mobile build artifact. It deliberately
// excludes Declared and Legacy, and refuses to turn an absent, legacy, or invalid
// surface into a capability grant.
func EncodeJSON(mobile Mobile) ([]byte, error) {
	if !mobile.Declared {
		return nil, fmt.Errorf("mobile manifest: surface is not declared")
	}
	if mobile.Legacy {
		return nil, fmt.Errorf("mobile manifest: legacy surface cannot be encoded")
	}
	if !SupportedMobileVersion(mobile.Version) {
		return nil, fmt.Errorf("mobile manifest: version must be between %d and %d", MobileVersion1, MobileVersion)
	}
	if mobile.Permissions == nil {
		return nil, fmt.Errorf("mobile manifest: permissions must be a non-nil list")
	}

	raw := map[string]interface{}{
		"title": mobile.Title,
		"mobile": map[string]interface{}{
			"version":     mobile.Version,
			"id":          mobile.ID,
			"versionName": mobile.VersionName,
			"versionCode": mobile.VersionCode,
			"start": map[string]interface{}{
				"domain": mobile.Start.Domain,
				"path":   mobile.Start.Path,
			},
			"orientation": mobile.Orientation,
			"theme": map[string]interface{}{
				"mode":  mobile.Theme.Mode,
				"color": mobile.Theme.Color,
			},
			"permissions": mobile.Permissions,
		},
	}
	if mobile.Network != nil {
		raw["mobile"].(map[string]interface{})["network"] = map[string]interface{}{"origins": mobile.Network.Origins}
	}
	if mobile.Icon != "" {
		raw["icon"] = mobile.Icon
	}
	if mobile.Links != nil {
		links := map[string]interface{}{}
		if mobile.Links.Scheme != "" {
			links["scheme"] = mobile.Links.Scheme
		}
		if len(mobile.Links.Hosts) != 0 {
			links["hosts"] = mobile.Links.Hosts
		}
		raw["mobile"].(map[string]interface{})["links"] = links
	}
	normalized, err := Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("mobile manifest: %w", err)
	}
	if !normalized.Declared || normalized.Legacy || normalized.Version != mobile.Version {
		return nil, fmt.Errorf("mobile manifest: validation did not preserve version %d", mobile.Version)
	}

	wire := encodedMobile{
		Version:     normalized.Version,
		ID:          normalized.ID,
		Title:       normalized.Title,
		Icon:        normalized.Icon,
		VersionName: normalized.VersionName,
		VersionCode: normalized.VersionCode,
		Start:       normalized.Start,
		Orientation: normalized.Orientation,
		Theme:       normalized.Theme,
		Permissions: normalized.Permissions,
		Links:       normalized.Links,
		Network:     normalized.Network,
	}
	encoded, err := json.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("mobile manifest: encode JSON: %w", err)
	}
	return encoded, nil
}

// DecodeJSON decodes one complete canonical mobile build artifact, rejects
// duplicate/unknown fields and trailing values, then re-runs the same versioned
// validation as Parse. JSON numbers remain json.Number until validation so
// integer bounds are not lost to float64.
func DecodeJSON(data []byte) (Mobile, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	decoded, err := decodeValue(decoder, "manifest")
	if err != nil {
		return Mobile{}, fmt.Errorf("manifest JSON: %w", err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return Mobile{}, fmt.Errorf("manifest JSON: trailing value")
		}
		return Mobile{}, fmt.Errorf("manifest JSON: trailing value: %w", err)
	}
	raw, ok := decoded.(map[string]interface{})
	if !ok {
		return Mobile{}, fmt.Errorf("manifest JSON: top level must be an object")
	}
	if err := rejectUnknownKeys("mobile artifact", raw, encodedMobileKeys); err != nil {
		return Mobile{}, fmt.Errorf("manifest JSON: %w", err)
	}
	for _, required := range []string{"version", "id", "title", "versionName", "versionCode", "start", "orientation", "theme", "permissions"} {
		if _, exists := raw[required]; !exists {
			return Mobile{}, fmt.Errorf("manifest JSON: %s is required", required)
		}
	}

	mobileRaw := make(map[string]interface{}, len(mobileKeys))
	for key, value := range raw {
		switch key {
		case "title", "icon":
			continue
		default:
			mobileRaw[key] = value
		}
	}
	manifestRaw := map[string]interface{}{
		"title":  raw["title"],
		"mobile": mobileRaw,
	}
	if icon, exists := raw["icon"]; exists {
		manifestRaw["icon"] = icon
	}
	resolved, err := Parse(manifestRaw)
	if err != nil {
		return Mobile{}, fmt.Errorf("manifest JSON: %w", err)
	}
	if !resolved.Declared || resolved.Legacy || !SupportedMobileVersion(resolved.Version) {
		return Mobile{}, fmt.Errorf("manifest JSON: mobile artifact must use a supported version")
	}
	return resolved, nil
}

func defaultMobile() Mobile {
	return Mobile{
		Title:       DefaultTitle,
		VersionName: DefaultVersionName,
		VersionCode: DefaultVersionCode,
		Start:       Start{Path: "/"},
		Orientation: DefaultOrientation,
		Theme:       Theme{Mode: DefaultThemeMode, Color: DefaultThemeColor},
		Permissions: []string{},
	}
}

func parseIdentity(result *Mobile, raw map[string]interface{}) error {
	if value, exists := raw["title"]; exists {
		title, ok := value.(string)
		if !ok {
			return fieldError("title", fmt.Errorf("must be a string"))
		}
		title = strings.TrimSpace(title)
		if err := validateTitle(title); err != nil {
			return fieldError("title", err)
		}
		result.Title = title
	}
	if value, exists := raw["icon"]; exists {
		icon, ok := value.(string)
		if !ok {
			return fieldError("icon", fmt.Errorf("must be a string"))
		}
		if err := validateIcon(icon); err != nil {
			return fieldError("icon", err)
		}
		result.Icon = icon
	}
	return nil
}

// Legacy manifests remain readable but do not acquire v1 validation semantics.
// Only obviously usable identity strings are copied into the diagnostic result.
func applyLegacyIdentity(result *Mobile, raw map[string]interface{}) {
	if title, ok := raw["title"].(string); ok {
		title = strings.TrimSpace(title)
		if validateTitle(title) == nil {
			result.Title = title
		}
	}
	if icon, ok := raw["icon"].(string); ok && validateIcon(icon) == nil {
		result.Icon = icon
	}
}

func validateTitle(title string) error {
	if title == "" {
		return fmt.Errorf("must not be empty")
	}
	if !utf8.ValidString(title) {
		return fmt.Errorf("must be valid UTF-8")
	}
	if utf8.RuneCountInString(title) > maxTitleRunes {
		return fmt.Errorf("must be at most %d characters", maxTitleRunes)
	}
	for _, r := range title {
		if unicode.IsControl(r) {
			return fmt.Errorf("must not contain control characters")
		}
	}
	return nil
}

func validateIdentifier(id string) error {
	if len(id) == 0 || len(id) > maxIdentifierBytes {
		return fmt.Errorf("must contain 1 to %d bytes", maxIdentifierBytes)
	}
	if id != strings.ToLower(id) {
		return fmt.Errorf("must be lowercase")
	}
	parts := strings.Split(id, ".")
	if len(parts) < 2 {
		return fmt.Errorf("must be a reverse-DNS identifier with at least two labels")
	}
	for _, part := range parts {
		if len(part) == 0 || len(part) > 63 || part[0] < 'a' || part[0] > 'z' {
			return fmt.Errorf("each label must start with a lowercase letter and contain at most 63 bytes")
		}
		for index := 1; index < len(part); index++ {
			c := part[index]
			if c < 'a' || c > 'z' {
				if c < '0' || c > '9' {
					return fmt.Errorf("labels may contain only lowercase letters and digits")
				}
			}
		}
	}
	return nil
}

func validateVersionName(name string) error {
	if len(name) == 0 || len(name) > maxVersionNameBytes {
		return fmt.Errorf("must contain 1 to %d bytes", maxVersionNameBytes)
	}
	if !versionNamePattern.MatchString(name) {
		return fmt.Errorf("must contain one to three dot-separated non-negative integers")
	}
	return nil
}

func validateLinkScheme(scheme string) error {
	if len(scheme) < 3 || len(scheme) > maxLinkSchemeBytes {
		return fmt.Errorf("must contain 3 to %d bytes", maxLinkSchemeBytes)
	}
	if !linkSchemePattern.MatchString(scheme) {
		return fmt.Errorf("must start with a lowercase letter and contain only lowercase letters, digits, plus, dot, or hyphen")
	}
	switch scheme {
	case "http", "https", "file", "data", "javascript", "about", "content":
		return fmt.Errorf("must not use reserved scheme %q", scheme)
	}
	return nil
}

func validateLinkHost(host string) error {
	if err := validateDomain(host); err != nil {
		return err
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return fmt.Errorf("must contain at least two DNS labels")
	}
	for _, label := range labels {
		if strings.HasPrefix(label, "xn--") {
			return fmt.Errorf("must use direct ASCII labels, not IDN punycode")
		}
	}
	switch labels[len(labels)-1] {
	case "localhost", "local", "internal", "invalid", "test", "example":
		return fmt.Errorf("must not use a reserved or local DNS suffix")
	}
	return nil
}

func validateDomain(domain string) error {
	if len(domain) == 0 || len(domain) > maxDomainBytes {
		return fmt.Errorf("must contain 1 to %d bytes", maxDomainBytes)
	}
	if domain != strings.ToLower(domain) {
		return fmt.Errorf("must be lowercase")
	}
	if net.ParseIP(domain) != nil {
		return fmt.Errorf("must be a DNS name, not an IP address")
	}
	for _, label := range strings.Split(domain, ".") {
		if len(label) == 0 || len(label) > 63 {
			return fmt.Errorf("each DNS label must contain 1 to 63 bytes")
		}
		if !asciiLetterOrDigit(label[0]) || !asciiLetterOrDigit(label[len(label)-1]) {
			return fmt.Errorf("DNS labels must start and end with a letter or digit")
		}
		for index := 0; index < len(label); index++ {
			if !asciiLetterOrDigit(label[index]) && label[index] != '-' {
				return fmt.Errorf("DNS labels may contain only lowercase letters, digits, and hyphens")
			}
		}
	}
	return nil
}

func validateStartPath(startPath string) error {
	if len(startPath) == 0 || len(startPath) > maxStartPathBytes {
		return fmt.Errorf("must contain 1 to %d bytes", maxStartPathBytes)
	}
	if !strings.HasPrefix(startPath, "/") || strings.HasPrefix(startPath, "//") {
		return fmt.Errorf("must be an absolute same-app path beginning with one slash")
	}
	if strings.Contains(startPath, "\\") {
		return fmt.Errorf("must not contain backslashes")
	}
	parsed, err := url.Parse(startPath)
	if err != nil {
		return fmt.Errorf("is not a valid URL path: %w", err)
	}
	if parsed.Scheme != "" || parsed.Host != "" || parsed.User != nil || parsed.Opaque != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("must not contain a scheme, authority, query, or fragment")
	}

	decoded := startPath
	for {
		next, err := url.PathUnescape(decoded)
		if err != nil {
			return fmt.Errorf("contains invalid escaping")
		}
		if err := validateDecodedPath(next); err != nil {
			return err
		}
		if next == decoded {
			break
		}
		decoded = next
	}
	return nil
}

func validateDecodedPath(decoded string) error {
	if strings.HasPrefix(decoded, "//") {
		return fmt.Errorf("must be an absolute same-app path beginning with one slash")
	}
	if strings.Contains(decoded, "\\") {
		return fmt.Errorf("must not contain encoded backslashes")
	}
	for _, r := range decoded {
		if r == 0 || unicode.IsControl(r) {
			return fmt.Errorf("must not contain control characters")
		}
	}
	for _, segment := range strings.Split(decoded, "/") {
		if segment == "." || segment == ".." {
			return fmt.Errorf("must not contain dot traversal segments")
		}
	}
	return nil
}

func validateIcon(icon string) error {
	if len(icon) == 0 || len(icon) > maxIconBytes {
		return fmt.Errorf("must contain 1 to %d bytes", maxIconBytes)
	}
	if !utf8.ValidString(icon) {
		return fmt.Errorf("must be valid UTF-8")
	}
	if strings.Contains(icon, "\\") || strings.HasPrefix(icon, "/") || filepath.IsAbs(icon) || windowsDrivePath(icon) {
		return fmt.Errorf("must be a relative forward-slash path")
	}
	if strings.Contains(icon, "%") {
		return fmt.Errorf("must not contain URL-escaped path segments")
	}
	parsed, err := url.Parse(icon)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" || parsed.User != nil || parsed.Opaque != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("must not be a URL")
	}
	for _, segment := range strings.Split(icon, "/") {
		if segment == "" || strings.HasPrefix(segment, ".") {
			return fmt.Errorf("must not contain empty, dot, or hidden path segments")
		}
		if strings.Contains(segment, ":") {
			return fmt.Errorf("must not contain colon path segments")
		}
		for _, r := range segment {
			if r == 0 || unicode.IsControl(r) {
				return fmt.Errorf("must not contain control characters")
			}
		}
	}
	return nil
}

func validHexColor(color string) bool {
	if len(color) != 7 || color[0] != '#' {
		return false
	}
	for index := 1; index < len(color); index++ {
		c := color[index]
		if c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' {
			continue
		}
		return false
	}
	return true
}

func boundedInteger(value interface{}, minimum, maximum int) (int, error) {
	var number float64
	switch typed := value.(type) {
	case json.Number:
		raw := string(typed)
		if !validJSONIntegerToken(raw) {
			return 0, fmt.Errorf("must be an integer")
		}
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < int64(minimum) || parsed > int64(maximum) {
			return 0, fmt.Errorf("must be an integer between %d and %d", minimum, maximum)
		}
		return int(parsed), nil
	case float64:
		number = typed
	case float32:
		number = float64(typed)
	case int:
		number = float64(typed)
	case int8:
		number = float64(typed)
	case int16:
		number = float64(typed)
	case int32:
		number = float64(typed)
	case int64:
		number = float64(typed)
	case uint:
		number = float64(typed)
	case uint8:
		number = float64(typed)
	case uint16:
		number = float64(typed)
	case uint32:
		number = float64(typed)
	case uint64:
		if typed > uint64(maximum) {
			return 0, fmt.Errorf("must be between %d and %d", minimum, maximum)
		}
		number = float64(typed)
	default:
		return 0, fmt.Errorf("must be an integer")
	}
	if math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number || number < float64(minimum) || number > float64(maximum) {
		return 0, fmt.Errorf("must be an integer between %d and %d", minimum, maximum)
	}
	return int(number), nil
}

// validJSONIntegerToken deliberately rejects decimals and exponents even when
// they round to an integer in binary floating point. The generated manifest is
// a cross-platform build contract, so 1, 1.0, and 1e0 must not acquire
// different meanings in Go, JSONObject, and JSONSerialization.
func validJSONIntegerToken(raw string) bool {
	if raw == "" {
		return false
	}
	start := 0
	if raw[0] == '-' {
		start = 1
		if start == len(raw) {
			return false
		}
	}
	if raw[start] == '0' {
		return start+1 == len(raw)
	}
	if raw[start] < '1' || raw[start] > '9' {
		return false
	}
	for index := start + 1; index < len(raw); index++ {
		if raw[index] < '0' || raw[index] > '9' {
			return false
		}
	}
	return true
}

func requiredString(object map[string]interface{}, key, field string) (string, error) {
	value, exists := object[key]
	if !exists {
		return "", fieldError(field, fmt.Errorf("is required"))
	}
	text, ok := value.(string)
	if !ok {
		return "", fieldError(field, fmt.Errorf("must be a string"))
	}
	return text, nil
}

func rejectUnknownKeys(field string, object map[string]interface{}, allowed map[string]struct{}) error {
	unknown := make([]string, 0)
	for key := range object {
		if _, ok := allowed[key]; !ok {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return fieldError(field, fmt.Errorf("unknown field %q", unknown[0]))
}

func keySet(keys ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		result[key] = struct{}{}
	}
	return result
}

func hasString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func asObject(value interface{}) (map[string]interface{}, bool) {
	object, ok := value.(map[string]interface{})
	return object, ok
}

func asSlice(value interface{}) ([]interface{}, bool) {
	switch typed := value.(type) {
	case []interface{}:
		return typed, true
	case []string:
		result := make([]interface{}, len(typed))
		for index := range typed {
			result[index] = typed[index]
		}
		return result, true
	default:
		return nil, false
	}
}

func asciiLetterOrDigit(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= '0' && value <= '9'
}

func windowsDrivePath(value string) bool {
	return len(value) >= 2 && (value[0] >= 'a' && value[0] <= 'z' || value[0] >= 'A' && value[0] <= 'Z') && value[1] == ':'
}

func fieldError(field string, err error) error {
	return fmt.Errorf("%s: %w", field, err)
}

func decodeValue(decoder *json.Decoder, path string) (interface{}, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return token, nil
	}

	switch delimiter {
	case '{':
		object := make(map[string]interface{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, fmt.Errorf("%s: object key must be a string", path)
			}
			if _, duplicate := object[key]; duplicate {
				return nil, fmt.Errorf("%s: duplicate field %q", path, key)
			}
			value, err := decodeValue(decoder, path+"."+key)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		if end, err := decoder.Token(); err != nil || end != json.Delim('}') {
			if err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("%s: malformed object", path)
		}
		return object, nil
	case '[':
		array := make([]interface{}, 0)
		for index := 0; decoder.More(); index++ {
			value, err := decodeValue(decoder, fmt.Sprintf("%s[%d]", path, index))
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		if end, err := decoder.Token(); err != nil || end != json.Delim(']') {
			if err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("%s: malformed array", path)
		}
		return array, nil
	default:
		return nil, fmt.Errorf("%s: unexpected delimiter %q", path, delimiter)
	}
}
