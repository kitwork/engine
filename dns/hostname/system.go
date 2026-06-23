package hostname

import (
	"net"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// Host represents a parsed hostname.
//
// Example:
//
//	api.dev.example.co.uk
//	│   │   └─ root
//	│   └───── subdomain
//	└───────── subdomain
//
//	host = api.dev.example.co.uk
//	root = example.co.uk
//	tld  = co.uk
//	sub  = [api dev]
type Host struct {
	host string
	root string
	tld  string
	sub  []string
}

// Parse parses a hostname into a Host.
//
// Supported:
//
//	example.com
//	api.example.com
//	api.dev.example.co.uk
//	api.example.com:443
//
// Parse automatically:
//
// - Converts to lowercase
// - Trims spaces
// - Removes ports
// - Removes trailing dots
func Parse(host string) (*Host, error) {
	host = strings.ToLower(strings.TrimSpace(host))

	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}

	host = strings.TrimSuffix(host, ".")

	root, err := publicsuffix.EffectiveTLDPlusOne(host)
	if err != nil {
		return nil, err
	}

	tld, _ := publicsuffix.PublicSuffix(host)

	h := &Host{
		host: host,
		root: root,
		tld:  tld,
	}

	sub := strings.TrimSuffix(host, "."+root)

	if sub != "" {
		h.sub = strings.Split(sub, ".")
	}

	return h, nil
}

// String implements fmt.Stringer.
func (h *Host) String() string {
	return h.host
}

// Host returns the normalized hostname.
func (h *Host) Host() string {
	return h.host
}

// Root returns the registrable domain.
//
//	api.example.co.uk -> example.co.uk
func (h *Host) Root() string {
	return h.root
}

// TLD returns the public suffix.
//
//	api.example.co.uk -> co.uk
func (h *Host) TLD() string {
	return h.tld
}

// Subdomains returns all subdomains.
//
//	api.dev.example.com -> [api dev]
func (h *Host) Subdomains() []string {
	return append([]string(nil), h.sub...)
}

// Left returns the left-most subdomain.
//
//	api.dev.example.com -> api
func (h *Host) Left() string {
	if len(h.sub) == 0 {
		return ""
	}

	return h.sub[0]
}

// Right returns the subdomain nearest to the root.
//
//	api.dev.example.com -> dev
func (h *Host) Right() string {
	if len(h.sub) == 0 {
		return ""
	}

	return h.sub[len(h.sub)-1]
}

// Depth returns the number of subdomains.
func (h *Host) Depth() int {
	return len(h.sub)
}

// Labels returns all hostname labels.
//
//	api.dev.example.com
//
//	[api dev example com]
func (h *Host) Labels() []string {
	return strings.Split(h.host, ".")
}

// Reverse returns labels in reverse order.
//
//	api.dev.example.com
//
//	[com example dev api]
func (h *Host) Reverse() []string {
	labels := h.Labels()

	for i, j := 0, len(labels)-1; i < j; i, j = i+1, j-1 {
		labels[i], labels[j] = labels[j], labels[i]
	}

	return labels
}

// Level returns a label by index.
//
//	api.dev.example.com
//
//	0 -> api
//	1 -> dev
//	2 -> example
//	3 -> com
func (h *Host) Level(index int) string {
	labels := h.Labels()

	if index < 0 || index >= len(labels) {
		return ""
	}

	return labels[index]
}

// IsRoot reports whether the hostname has no subdomains.
func (h *Host) IsRoot() bool {
	return len(h.sub) == 0
}

// HasSubdomain reports whether the hostname has subdomains.
func (h *Host) HasSubdomain() bool {
	return len(h.sub) > 0
}

// Contains reports whether a subdomain exists.
func (h *Host) Contains(sub string) bool {
	for _, s := range h.sub {
		if s == sub {
			return true
		}
	}

	return false
}

// Parent returns the parent hostname.
//
//	api.dev.example.com -> dev.example.com
//	api.example.com -> example.com
func (h *Host) Parent() string {
	if len(h.sub) <= 1 {
		return h.root
	}

	return strings.Join(h.sub[1:], ".") + "." + h.root
}

// Parents returns all parent hostnames.
//
//	api.dev.example.com
//
//	[dev.example.com example.com]
func (h *Host) Parents() []string {
	if len(h.sub) == 0 {
		return []string{h.root}
	}

	var parents []string

	for i := 1; i <= len(h.sub); i++ {
		if i == len(h.sub) {
			parents = append(parents, h.root)
			continue
		}

		parents = append(
			parents,
			strings.Join(h.sub[i:], ".")+"."+h.root,
		)
	}

	return parents
}

// Is reports whether two hostnames are equal.
func (h *Host) Is(host string) bool {
	return strings.EqualFold(h.host, host)
}

// IsRootOf reports whether another hostname belongs
// to the same registrable domain.
func (h *Host) IsRootOf(host string) bool {
	other, err := Parse(host)
	if err != nil {
		return false
	}

	return h.root == other.root
}

// IsSubdomainOf reports whether this hostname is
// a subdomain of another hostname.
func (h *Host) IsSubdomainOf(host string) bool {
	other, err := Parse(host)
	if err != nil {
		return false
	}

	if h.host == other.host {
		return false
	}

	return strings.HasSuffix(h.host, "."+other.host)
}

//
// Builders
//

// Wildcard returns a wildcard hostname.
//
//	example.com -> *.example.com
func (h *Host) Wildcard() string {
	return "*." + h.root
}

// HTTP returns the hostname with http://.
func (h *Host) HTTP() string {
	return "http://" + h.host
}

// HTTPS returns the hostname with https://.
func (h *Host) HTTPS() string {
	return "https://" + h.host
}

// URL returns the hostname with a scheme.
//
//	URL("https")
//
//	https://api.example.com
func (h *Host) URL(scheme string) string {
	scheme = strings.TrimSpace(scheme)

	if scheme == "" {
		scheme = "https"
	}

	return scheme + "://" + h.host
}

// Join replaces all subdomains.
//
//	api.dev.example.com
//
//	Join("cdn", "v2")
//
//	cdn.v2.example.com
func (h *Host) Join(subdomains ...string) string {
	if len(subdomains) == 0 {
		return h.root
	}

	return strings.Join(subdomains, ".") + "." + h.root
}

// Prepend adds subdomains to the beginning.
//
//	api.example.com
//
//	Prepend("cdn")
//
//	cdn.api.example.com
func (h *Host) Prepend(subdomains ...string) string {
	if len(subdomains) == 0 {
		return h.host
	}

	parts := append(subdomains, h.Labels()...)

	return strings.Join(parts, ".")
}

// Append adds a subdomain nearest to the root.
//
//	api.dev.example.com
//
//	Append("stage")
//
//	api.dev.stage.example.com
func (h *Host) Append(subdomain string) string {
	subs := append([]string(nil), h.sub...)

	subs = append(subs, subdomain)

	return strings.Join(subs, ".") + "." + h.root
}

// Replace replaces the left-most subdomain.
//
//	api.dev.example.com
//
//	admin.dev.example.com
func (h *Host) Replace(subdomain string) string {
	if len(h.sub) == 0 {
		return subdomain + "." + h.root
	}

	subs := append([]string(nil), h.sub...)

	subs[0] = subdomain

	return strings.Join(subs, ".") + "." + h.root
}

// Remove removes a subdomain.
func (h *Host) Remove(subdomain string) string {
	var subs []string

	for _, s := range h.sub {
		if s != subdomain {
			subs = append(subs, s)
		}
	}

	if len(subs) == 0 {
		return h.root
	}

	return strings.Join(subs, ".") + "." + h.root
}

// Shift removes the left-most subdomain.
//
//	api.dev.example.com
//
//	dev.example.com
func (h *Host) Shift() string {
	if len(h.sub) <= 1 {
		return h.root
	}

	return strings.Join(h.sub[1:], ".") + "." + h.root
}

// Pop removes the subdomain nearest to the root.
//
//	api.dev.example.com
//
//	api.example.com
func (h *Host) Pop() string {
	if len(h.sub) <= 1 {
		return h.root
	}

	return strings.Join(h.sub[:len(h.sub)-1], ".") + "." + h.root
}
