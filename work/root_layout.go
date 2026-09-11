package work

// RootLayout defines how a configured source root maps a request domain to a
// site directory. Auto preserves the historical mixed-layout resolver for
// embedders that construct core.Engine or Tenant directly.
type RootLayout uint8

const (
	RootLayoutAuto RootLayout = iota
	RootLayoutSingle
	RootLayoutMultiDomain
	RootLayoutMultiTenant
)

// RootAppIdentity is the stable logical identity of the one app rooted at
// app/. It is not a filesystem segment; app layouts resolve their shared
// resources directly from the configured root.
const RootAppIdentity = "app"

func (layout RootLayout) String() string {
	switch layout {
	case RootLayoutSingle:
		return "single"
	case RootLayoutMultiDomain:
		return "multi-domain"
	case RootLayoutMultiTenant:
		return "multi-tenant"
	default:
		return "auto"
	}
}

// IsSingleApp reports whether every site belongs to the one app rooted at
// app/. The two layouts differ only in where their site source lives.
func (layout RootLayout) IsSingleApp() bool {
	return layout == RootLayoutSingle || layout == RootLayoutMultiDomain
}

func (layout RootLayout) Valid() bool {
	return layout >= RootLayoutAuto && layout <= RootLayoutMultiTenant
}
