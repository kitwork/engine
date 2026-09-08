package manifest

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func validManifest() map[string]interface{} {
	return map[string]interface{}{
		"title": "Pocket Notes",
		"icon":  "assets/app-icon.svg",
		"mobile": map[string]interface{}{
			"version": float64(1),
			"id":      "org.kitwork.notes",
			"start": map[string]interface{}{
				"domain": "notes.kitwork.localhost",
			},
		},
	}
}

func TestParseMobileV1Defaults(t *testing.T) {
	got, err := Parse(validManifest())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !got.Declared || got.Legacy || got.Version != 1 {
		t.Fatalf("surface flags = declared %v legacy %v version %d", got.Declared, got.Legacy, got.Version)
	}
	if got.ID != "org.kitwork.notes" || got.Title != "Pocket Notes" || got.Icon != "assets/app-icon.svg" {
		t.Fatalf("identity = %#v", got)
	}
	if got.VersionName != DefaultVersionName || got.VersionCode != DefaultVersionCode {
		t.Fatalf("version defaults = %q/%d", got.VersionName, got.VersionCode)
	}
	if got.Start != (Start{Domain: "notes.kitwork.localhost", Path: "/"}) {
		t.Fatalf("start = %#v", got.Start)
	}
	if got.Orientation != DefaultOrientation || got.Theme != (Theme{Mode: DefaultThemeMode, Color: DefaultThemeColor}) {
		t.Fatalf("presentation defaults = %q %#v", got.Orientation, got.Theme)
	}
	if got.Permissions == nil || len(got.Permissions) != 0 {
		t.Fatalf("permissions default = %#v, want a non-nil empty list", got.Permissions)
	}
}

func TestParseMobileV1AllFields(t *testing.T) {
	raw := validManifest()
	mobile := raw["mobile"].(map[string]interface{})
	mobile["versionName"] = "12.34.56"
	mobile["versionCode"] = 987
	mobile["orientation"] = "landscape"
	mobile["theme"] = map[string]interface{}{"mode": "dark", "color": "#A0B1C2"}
	mobile["permissions"] = []string{
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
	}
	mobile["start"].(map[string]interface{})["path"] = "/notes/new"

	got, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.VersionName != "12.34.56" || got.VersionCode != 987 || got.Orientation != "landscape" {
		t.Fatalf("resolved version/presentation = %#v", got)
	}
	if got.Theme != (Theme{Mode: "dark", Color: "#a0b1c2"}) {
		t.Fatalf("theme = %#v", got.Theme)
	}
	if got.Start.Path != "/notes/new" || len(got.Permissions) != len(permissionSet) {
		t.Fatalf("start/permissions = %#v / %#v", got.Start, got.Permissions)
	}
}

func TestParseMobileV2AddsExportWithoutWideningV1(t *testing.T) {
	v1 := validManifest()
	v1["mobile"].(map[string]interface{})["permissions"] = []string{"files.export"}
	assertParseError(t, v1, `unsupported permission "files.export"`)

	v2 := validManifest()
	mobile := v2["mobile"].(map[string]interface{})
	mobile["version"] = float64(2)
	mobile["permissions"] = []string{"files.import", "files.read", "files.export"}

	got, err := Parse(v2)
	if err != nil {
		t.Fatalf("Parse v2: %v", err)
	}
	if got.Version != 2 || !reflect.DeepEqual(got.Permissions, []string{"files.import", "files.read", "files.export"}) {
		t.Fatalf("v2 manifest = %#v", got)
	}
	encoded, err := EncodeJSON(got)
	if err != nil {
		t.Fatalf("EncodeJSON v2: %v", err)
	}
	if !strings.Contains(string(encoded), `"version":2`) || !strings.Contains(string(encoded), `"files.export"`) {
		t.Fatalf("encoded v2 = %s", encoded)
	}
	roundTrip, err := DecodeJSON(encoded)
	if err != nil || !reflect.DeepEqual(roundTrip, got) {
		t.Fatalf("DecodeJSON v2 = %#v, %v", roundTrip, err)
	}
}

func TestParseMobileV3AddsCameraWithoutWideningV1OrV2(t *testing.T) {
	for _, version := range []float64{1, 2} {
		raw := validManifest()
		mobile := raw["mobile"].(map[string]interface{})
		mobile["version"] = version
		mobile["permissions"] = []string{"camera.capture"}
		assertParseError(t, raw, `unsupported permission "camera.capture"`)
	}

	v3 := validManifest()
	mobile := v3["mobile"].(map[string]interface{})
	mobile["version"] = float64(3)
	mobile["permissions"] = []string{"files.export", "camera.capture"}

	got, err := Parse(v3)
	if err != nil {
		t.Fatalf("Parse v3: %v", err)
	}
	if got.Version != 3 || !reflect.DeepEqual(got.Permissions, []string{"files.export", "camera.capture"}) {
		t.Fatalf("v3 manifest = %#v", got)
	}
	encoded, err := EncodeJSON(got)
	if err != nil {
		t.Fatalf("EncodeJSON v3: %v", err)
	}
	if !strings.Contains(string(encoded), `"version":3`) || !strings.Contains(string(encoded), `"camera.capture"`) {
		t.Fatalf("encoded v3 = %s", encoded)
	}
	roundTrip, err := DecodeJSON(encoded)
	if err != nil || !reflect.DeepEqual(roundTrip, got) {
		t.Fatalf("DecodeJSON v3 = %#v, %v", roundTrip, err)
	}
}

func TestParseMobileV4AddsQRScanWithoutWideningV1ThroughV3(t *testing.T) {
	for _, version := range []float64{1, 2, 3} {
		raw := validManifest()
		mobile := raw["mobile"].(map[string]interface{})
		mobile["version"] = version
		mobile["permissions"] = []string{"qr.scan"}
		assertParseError(t, raw, `unsupported permission "qr.scan"`)
	}

	v4 := validManifest()
	mobile := v4["mobile"].(map[string]interface{})
	mobile["version"] = float64(4)
	mobile["permissions"] = []string{"files.export", "camera.capture", "qr.scan"}

	got, err := Parse(v4)
	if err != nil {
		t.Fatalf("Parse v4: %v", err)
	}
	want := []string{"files.export", "camera.capture", "qr.scan"}
	if got.Version != 4 || !reflect.DeepEqual(got.Permissions, want) {
		t.Fatalf("v4 manifest = %#v", got)
	}
	encoded, err := EncodeJSON(got)
	if err != nil {
		t.Fatalf("EncodeJSON v4: %v", err)
	}
	if !strings.Contains(string(encoded), `"version":4`) || !strings.Contains(string(encoded), `"qr.scan"`) {
		t.Fatalf("encoded v4 = %s", encoded)
	}
	roundTrip, err := DecodeJSON(encoded)
	if err != nil || !reflect.DeepEqual(roundTrip, got) {
		t.Fatalf("DecodeJSON v4 = %#v, %v", roundTrip, err)
	}
}

func TestParseMobileV5AddsScreenKeepAwakeWithoutWideningV1ThroughV4(t *testing.T) {
	for _, version := range []float64{1, 2, 3, 4} {
		raw := validManifest()
		mobile := raw["mobile"].(map[string]interface{})
		mobile["version"] = version
		mobile["permissions"] = []string{"screen.keepAwake"}
		assertParseError(t, raw, `unsupported permission "screen.keepAwake"`)
	}

	v5 := validManifest()
	mobile := v5["mobile"].(map[string]interface{})
	mobile["version"] = float64(5)
	mobile["permissions"] = []string{"files.export", "camera.capture", "qr.scan", "screen.keepAwake"}

	got, err := Parse(v5)
	if err != nil {
		t.Fatalf("Parse v5: %v", err)
	}
	want := []string{"files.export", "camera.capture", "qr.scan", "screen.keepAwake"}
	if got.Version != 5 || !reflect.DeepEqual(got.Permissions, want) {
		t.Fatalf("v5 manifest = %#v", got)
	}
	encoded, err := EncodeJSON(got)
	if err != nil {
		t.Fatalf("EncodeJSON v5: %v", err)
	}
	if !strings.Contains(string(encoded), `"version":5`) || !strings.Contains(string(encoded), `"screen.keepAwake"`) {
		t.Fatalf("encoded v5 = %s", encoded)
	}
	roundTrip, err := DecodeJSON(encoded)
	if err != nil || !reflect.DeepEqual(roundTrip, got) {
		t.Fatalf("DecodeJSON v5 = %#v, %v", roundTrip, err)
	}
}

func TestParseMobileV6AddsNotificationsWithoutWideningV1ThroughV5(t *testing.T) {
	for _, version := range []float64{1, 2, 3, 4, 5} {
		raw := validManifest()
		mobile := raw["mobile"].(map[string]interface{})
		mobile["version"] = version
		mobile["permissions"] = []string{"notifications.show"}
		assertParseError(t, raw, `unsupported permission "notifications.show"`)
	}
	for _, permission := range []string{"notifications.permission", "notifications.requestPermission"} {
		t.Run("reject private "+permission, func(t *testing.T) {
			raw := validManifest()
			mobile := raw["mobile"].(map[string]interface{})
			mobile["version"] = float64(6)
			mobile["permissions"] = []string{permission}
			assertParseError(t, raw, `unsupported permission "`+permission+`"`)
		})
	}

	v6 := validManifest()
	mobile := v6["mobile"].(map[string]interface{})
	mobile["version"] = float64(6)
	mobile["permissions"] = []string{"files.export", "camera.capture", "qr.scan", "screen.keepAwake", "notifications.show"}

	got, err := Parse(v6)
	if err != nil {
		t.Fatalf("Parse v6: %v", err)
	}
	want := []string{"files.export", "camera.capture", "qr.scan", "screen.keepAwake", "notifications.show"}
	if got.Version != 6 || !reflect.DeepEqual(got.Permissions, want) {
		t.Fatalf("v6 manifest = %#v", got)
	}
	encoded, err := EncodeJSON(got)
	if err != nil {
		t.Fatalf("EncodeJSON v6: %v", err)
	}
	if !strings.Contains(string(encoded), `"version":6`) || !strings.Contains(string(encoded), `"notifications.show"`) {
		t.Fatalf("encoded v6 = %s", encoded)
	}
	for _, permission := range []string{"notifications.permission", "notifications.requestPermission"} {
		t.Run("reject encoded private "+permission, func(t *testing.T) {
			tampered := strings.Replace(string(encoded), "notifications.show", permission, 1)
			if decoded, err := DecodeJSON([]byte(tampered)); err == nil {
				t.Fatalf("DecodeJSON accepted private permission %q: %#v", permission, decoded)
			}
		})
	}
	roundTrip, err := DecodeJSON(encoded)
	if err != nil || !reflect.DeepEqual(roundTrip, got) {
		t.Fatalf("DecodeJSON v6 = %#v, %v", roundTrip, err)
	}
}

func TestParseMobileV7AddsDeepLinksWithoutWideningV1ThroughV6(t *testing.T) {
	for _, version := range []float64{1, 2, 3, 4, 5, 6} {
		raw := validManifest()
		mobile := raw["mobile"].(map[string]interface{})
		mobile["version"] = version
		mobile["permissions"] = []string{"deepLinks.receive"}
		assertParseError(t, raw, `unsupported permission "deepLinks.receive"`)

		raw = validManifest()
		mobile = raw["mobile"].(map[string]interface{})
		mobile["version"] = version
		mobile["links"] = map[string]interface{}{"scheme": "kitworknotes"}
		assertParseError(t, raw, "mobile.links: requires mobile.version 7")
	}

	for _, private := range []string{"deepLinks.snapshot", "deepLinks.open"} {
		raw := validManifest()
		mobile := raw["mobile"].(map[string]interface{})
		mobile["version"] = float64(7)
		mobile["permissions"] = []string{private}
		assertParseError(t, raw, `unsupported permission "`+private+`"`)
	}

	v7 := validManifest()
	mobile := v7["mobile"].(map[string]interface{})
	mobile["version"] = float64(7)
	mobile["permissions"] = []string{"notifications.show", "deepLinks.receive"}
	mobile["links"] = map[string]interface{}{"scheme": "kitwork-notes"}

	got, err := Parse(v7)
	if err != nil {
		t.Fatalf("Parse v7: %v", err)
	}
	if got.Version != 7 || got.Links == nil || got.Links.Scheme != "kitwork-notes" {
		t.Fatalf("v7 manifest = %#v", got)
	}
	encoded, err := EncodeJSON(got)
	if err != nil {
		t.Fatalf("EncodeJSON v7: %v", err)
	}
	if !strings.Contains(string(encoded), `"version":7`) || !strings.Contains(string(encoded), `"links":{"scheme":"kitwork-notes"}`) {
		t.Fatalf("encoded v7 = %s", encoded)
	}
	roundTrip, err := DecodeJSON(encoded)
	if err != nil || !reflect.DeepEqual(roundTrip, got) {
		t.Fatalf("DecodeJSON v7 = %#v, %v", roundTrip, err)
	}
}

func TestParseMobileV7DeepLinkConfigIsStrictAndCoupledToGrant(t *testing.T) {
	tests := []struct {
		name        string
		permissions []string
		links       interface{}
		omitLinks   bool
		want        string
	}{
		{name: "grant needs links", permissions: []string{"deepLinks.receive"}, omitLinks: true, want: "is required with permission deepLinks.receive"},
		{name: "links need grant", links: map[string]interface{}{"scheme": "kitworknotes"}, want: "requires permission deepLinks.receive"},
		{name: "object", permissions: []string{"deepLinks.receive"}, links: "kitworknotes", want: "must be an object"},
		{name: "missing scheme", permissions: []string{"deepLinks.receive"}, links: map[string]interface{}{}, want: "mobile.links.scheme: is required"},
		{name: "unknown field", permissions: []string{"deepLinks.receive"}, links: map[string]interface{}{"scheme": "kitworknotes", "host": "app"}, want: `unknown field "host"`},
		{name: "uppercase", permissions: []string{"deepLinks.receive"}, links: map[string]interface{}{"scheme": "KitworkNotes"}, want: "lowercase"},
		{name: "too short", permissions: []string{"deepLinks.receive"}, links: map[string]interface{}{"scheme": "kw"}, want: "3 to"},
		{name: "too long", permissions: []string{"deepLinks.receive"}, links: map[string]interface{}{"scheme": "k" + strings.Repeat("a", maxLinkSchemeBytes)}, want: "3 to"},
		{name: "bad first", permissions: []string{"deepLinks.receive"}, links: map[string]interface{}{"scheme": "1kitwork"}, want: "lowercase letter"},
		{name: "bad character", permissions: []string{"deepLinks.receive"}, links: map[string]interface{}{"scheme": "kit_work"}, want: "contain only"},
		{name: "reserved http", permissions: []string{"deepLinks.receive"}, links: map[string]interface{}{"scheme": "http"}, want: "reserved scheme"},
		{name: "reserved content", permissions: []string{"deepLinks.receive"}, links: map[string]interface{}{"scheme": "content"}, want: "reserved scheme"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := validManifest()
			mobile := raw["mobile"].(map[string]interface{})
			mobile["version"] = float64(7)
			mobile["permissions"] = test.permissions
			if !test.omitLinks {
				mobile["links"] = test.links
			}
			assertParseError(t, raw, test.want)
		})
	}
}

func TestParseMobileV8AddsVerifiedHostsAndNotificationTapWithoutWideningV1ThroughV7(t *testing.T) {
	for version := 1; version <= 7; version++ {
		raw := validManifest()
		mobile := raw["mobile"].(map[string]interface{})
		mobile["version"] = float64(version)
		mobile["permissions"] = []string{"notifications.tap"}
		assertParseError(t, raw, `unsupported permission "notifications.tap"`)
	}

	v7 := validManifest()
	v7Mobile := v7["mobile"].(map[string]interface{})
	v7Mobile["version"] = float64(7)
	v7Mobile["permissions"] = []string{"deepLinks.receive"}
	v7Mobile["links"] = map[string]interface{}{
		"scheme": "kitworknotes",
		"hosts":  []string{"mobile.kitwork.org"},
	}
	assertParseError(t, v7, `unknown field "hosts"`)

	v8 := validManifest()
	mobile := v8["mobile"].(map[string]interface{})
	mobile["version"] = float64(8)
	mobile["permissions"] = []string{
		"notifications.show", "deepLinks.receive", "notifications.tap",
	}
	mobile["links"] = map[string]interface{}{
		"scheme": "kitwork-notes",
		"hosts":  []string{"www.kitwork.org", "mobile.kitwork.org"},
	}

	got, err := Parse(v8)
	if err != nil {
		t.Fatalf("Parse v8: %v", err)
	}
	wantHosts := []string{"mobile.kitwork.org", "www.kitwork.org"}
	if got.Version != 8 || got.Links == nil || got.Links.Scheme != "kitwork-notes" ||
		!reflect.DeepEqual(got.Links.Hosts, wantHosts) {
		t.Fatalf("v8 manifest = %#v", got)
	}
	encoded, err := EncodeJSON(got)
	if err != nil {
		t.Fatalf("EncodeJSON v8: %v", err)
	}
	for _, marker := range []string{
		`"version":8`, `"notifications.tap"`, `"scheme":"kitwork-notes"`,
		`"hosts":["mobile.kitwork.org","www.kitwork.org"]`,
	} {
		if !strings.Contains(string(encoded), marker) {
			t.Fatalf("encoded v8 lost %q: %s", marker, encoded)
		}
	}
	roundTrip, err := DecodeJSON(encoded)
	if err != nil || !reflect.DeepEqual(roundTrip, got) {
		t.Fatalf("DecodeJSON v8 = %#v, %v", roundTrip, err)
	}

	for name, links := range map[string]map[string]interface{}{
		"scheme only": {"scheme": "kitwork-notes"},
		"hosts only":  {"hosts": []string{"mobile.kitwork.org"}},
	} {
		t.Run(name, func(t *testing.T) {
			raw := validManifest()
			mobile := raw["mobile"].(map[string]interface{})
			mobile["version"] = float64(8)
			mobile["permissions"] = []string{"deepLinks.receive"}
			mobile["links"] = links
			if _, err := Parse(raw); err != nil {
				t.Fatalf("Parse v8 %s: %v", name, err)
			}
		})
	}
}

func TestParseMobileV8VerifiedHostsAreStrictAndBounded(t *testing.T) {
	tests := []struct {
		name  string
		hosts interface{}
		want  string
	}{
		{name: "not array", hosts: "mobile.kitwork.org", want: "must be an array"},
		{name: "empty", hosts: []string{}, want: "must contain 1 to"},
		{name: "too many", hosts: []string{"a.kitwork.org", "b.kitwork.org", "c.kitwork.org", "d.kitwork.org", "e.kitwork.org", "f.kitwork.org", "g.kitwork.org", "h.kitwork.org", "i.kitwork.org"}, want: "must contain 1 to"},
		{name: "non string", hosts: []interface{}{"mobile.kitwork.org", true}, want: "must be a string"},
		{name: "duplicate", hosts: []string{"mobile.kitwork.org", "mobile.kitwork.org"}, want: "duplicates"},
		{name: "uppercase", hosts: []string{"Mobile.kitwork.org"}, want: "lowercase"},
		{name: "wildcard", hosts: []string{"*.kitwork.org"}, want: "start and end"},
		{name: "port", hosts: []string{"mobile.kitwork.org:443"}, want: "only lowercase"},
		{name: "ip", hosts: []string{"192.0.2.1"}, want: "not an IP"},
		{name: "single label", hosts: []string{"kitwork"}, want: "at least two"},
		{name: "punycode", hosts: []string{"xn--caf-dma.example"}, want: "not IDN"},
		{name: "local suffix", hosts: []string{"mobile.local"}, want: "reserved or local"},
		{name: "invalid suffix", hosts: []string{"mobile.invalid"}, want: "reserved or local"},
		{name: "example suffix", hosts: []string{"mobile.example"}, want: "reserved or local"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := validManifest()
			mobile := raw["mobile"].(map[string]interface{})
			mobile["version"] = float64(8)
			mobile["permissions"] = []string{"deepLinks.receive"}
			mobile["links"] = map[string]interface{}{"hosts": test.hosts}
			assertParseError(t, raw, test.want)
		})
	}

	raw := validManifest()
	mobile := raw["mobile"].(map[string]interface{})
	mobile["version"] = float64(8)
	mobile["permissions"] = []string{"deepLinks.receive"}
	mobile["links"] = map[string]interface{}{}
	assertParseError(t, raw, "requires scheme, hosts, or both")
}

func TestParseMobileV8NotificationTapRequiresBothAuthorities(t *testing.T) {
	for name, permissions := range map[string][]string{
		"missing show":      {"deepLinks.receive", "notifications.tap"},
		"missing deep link": {"notifications.show", "notifications.tap"},
		"missing both":      {"notifications.tap"},
	} {
		t.Run(name, func(t *testing.T) {
			raw := validManifest()
			mobile := raw["mobile"].(map[string]interface{})
			mobile["version"] = float64(8)
			mobile["permissions"] = permissions
			if hasString(permissions, "deepLinks.receive") {
				mobile["links"] = map[string]interface{}{"hosts": []string{"mobile.kitwork.org"}}
			}
			assertParseError(t, raw, "notifications.tap requires notifications.show and deepLinks.receive")
		})
	}
}

func TestMobilePermissionVersionDeltasAreExact(t *testing.T) {
	sets := []map[string]struct{}{permissionSet, permissionSetV2, permissionSetV3, permissionSetV4, permissionSetV5, permissionSetV6, permissionSetV7, permissionSetV8}
	wantAdded := []string{"files.export", "camera.capture", "qr.scan", "screen.keepAwake", "notifications.show", "deepLinks.receive", "notifications.tap"}
	for index, permission := range wantAdded {
		older := sets[index]
		newer := sets[index+1]
		if len(newer) != len(older)+1 {
			t.Fatalf("mobile v%d widened by %d permissions, want exactly one", index+2, len(newer)-len(older))
		}
		for inherited := range older {
			if _, ok := newer[inherited]; !ok {
				t.Fatalf("mobile v%d dropped inherited permission %q", index+2, inherited)
			}
		}
		if _, ok := older[permission]; ok {
			t.Fatalf("mobile v%d already contained %q", index+1, permission)
		}
		if _, ok := newer[permission]; !ok {
			t.Fatalf("mobile v%d did not add %q", index+2, permission)
		}
	}
}

func TestMobilePermissionSetsKeepV1ThroughV4Immutable(t *testing.T) {
	v1 := keySet(
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
	v2 := keySet(
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
		"files.export",
	)
	v3 := keySet(
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
		"files.export",
		"camera.capture",
	)
	v4 := keySet(
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
		"files.export",
		"camera.capture",
		"qr.scan",
	)
	for index, test := range []struct {
		got  map[string]struct{}
		want map[string]struct{}
	}{
		{permissionSet, v1},
		{permissionSetV2, v2},
		{permissionSetV3, v3},
		{permissionSetV4, v4},
	} {
		if !reflect.DeepEqual(test.got, test.want) {
			t.Fatalf("mobile v%d permission set changed: got %#v, want %#v", index+1, test.got, test.want)
		}
	}
}

func TestSupportedMobileVersionIncludesExactlyV1ThroughV10(t *testing.T) {
	if MobileVersion != 10 {
		t.Fatalf("current mobile manifest version=%d, want 10", MobileVersion)
	}
	for version := -1; version <= 11; version++ {
		want := version >= 1 && version <= 10
		if got := SupportedMobileVersion(version); got != want {
			t.Fatalf("SupportedMobileVersion(%d)=%v, want %v", version, got, want)
		}
	}
}

func TestParseMobileLegacyCompatibility(t *testing.T) {
	tests := []struct {
		name     string
		raw      map[string]interface{}
		declared bool
		legacy   bool
	}{
		{name: "absent", raw: map[string]interface{}{"title": "Legacy"}},
		{name: "boolean", raw: map[string]interface{}{"title": "Legacy", "mobile": true}, declared: true, legacy: true},
		{name: "unversioned object", raw: map[string]interface{}{"mobile": map[string]interface{}{"future": "kept raw"}}, declared: true, legacy: true},
		{name: "null", raw: map[string]interface{}{"mobile": nil}, declared: true, legacy: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Parse(test.raw)
			if err != nil {
				t.Fatalf("Parse legacy: %v", err)
			}
			if got.Declared != test.declared || got.Legacy != test.legacy || got.Version != 0 {
				t.Fatalf("flags = %#v", got)
			}
			if test.name == "absent" && got.Title != "Legacy" {
				t.Fatalf("legacy title = %q", got.Title)
			}
		})
	}
}

func TestParseMobileV1RejectsUnknownFields(t *testing.T) {
	tests := []struct {
		name string
		edit func(map[string]interface{})
		want string
	}{
		{
			name: "mobile",
			edit: func(raw map[string]interface{}) {
				raw["mobile"].(map[string]interface{})["origin"] = "https://example.com"
			},
			want: `mobile: unknown field "origin"`,
		},
		{
			name: "start",
			edit: func(raw map[string]interface{}) {
				raw["mobile"].(map[string]interface{})["start"].(map[string]interface{})["origin"] = "x"
			},
			want: `mobile.start: unknown field "origin"`,
		},
		{
			name: "theme",
			edit: func(raw map[string]interface{}) {
				raw["mobile"].(map[string]interface{})["theme"] = map[string]interface{}{"accent": "#ffffff"}
			},
			want: `mobile.theme: unknown field "accent"`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := validManifest()
			test.edit(raw)
			assertParseError(t, raw, test.want)
		})
	}
}

func TestParseMobileV1Validation(t *testing.T) {
	tests := []struct {
		name string
		edit func(map[string]interface{})
		want string
	}{
		{name: "version type", edit: func(raw map[string]interface{}) { raw["mobile"].(map[string]interface{})["version"] = "1" }, want: "mobile.version"},
		{name: "version unsupported", edit: func(raw map[string]interface{}) { raw["mobile"].(map[string]interface{})["version"] = 11 }, want: "mobile.version"},
		{name: "missing id", edit: func(raw map[string]interface{}) { delete(raw["mobile"].(map[string]interface{}), "id") }, want: "mobile.id: is required"},
		{name: "id uppercase", edit: func(raw map[string]interface{}) { raw["mobile"].(map[string]interface{})["id"] = "org.Kitwork.notes" }, want: "mobile.id: must be lowercase"},
		{name: "id no reverse DNS", edit: func(raw map[string]interface{}) { raw["mobile"].(map[string]interface{})["id"] = "notes" }, want: "reverse-DNS"},
		{name: "id punctuation", edit: func(raw map[string]interface{}) { raw["mobile"].(map[string]interface{})["id"] = "org.kit-work.notes" }, want: "lowercase letters and digits"},
		{name: "title type", edit: func(raw map[string]interface{}) { raw["title"] = true }, want: "title: must be a string"},
		{name: "title empty", edit: func(raw map[string]interface{}) { raw["title"] = "  " }, want: "title: must not be empty"},
		{name: "title control", edit: func(raw map[string]interface{}) { raw["title"] = "bad\nname" }, want: "control characters"},
		{name: "title bound", edit: func(raw map[string]interface{}) { raw["title"] = strings.Repeat("a", maxTitleRunes+1) }, want: "at most"},
		{name: "icon URL", edit: func(raw map[string]interface{}) { raw["icon"] = "https://example.com/icon.svg" }, want: "icon: must not be a URL"},
		{name: "icon absolute", edit: func(raw map[string]interface{}) { raw["icon"] = "/assets/icon.svg" }, want: "relative"},
		{name: "icon drive", edit: func(raw map[string]interface{}) { raw["icon"] = "C:/icon.svg" }, want: "relative"},
		{name: "icon hidden", edit: func(raw map[string]interface{}) { raw["icon"] = "assets/.private/icon.svg" }, want: "hidden"},
		{name: "icon dot", edit: func(raw map[string]interface{}) { raw["icon"] = "assets/../icon.svg" }, want: "dot"},
		{name: "icon encoded dot", edit: func(raw map[string]interface{}) { raw["icon"] = "assets/%2e%2e/icon.svg" }, want: "URL-escaped"},
		{name: "icon bound", edit: func(raw map[string]interface{}) { raw["icon"] = strings.Repeat("a", maxIconBytes+1) }, want: "1 to"},
		{name: "version name", edit: func(raw map[string]interface{}) { raw["mobile"].(map[string]interface{})["versionName"] = "latest" }, want: "mobile.versionName"},
		{name: "version code fraction", edit: func(raw map[string]interface{}) { raw["mobile"].(map[string]interface{})["versionCode"] = 1.5 }, want: "mobile.versionCode"},
		{name: "version code zero", edit: func(raw map[string]interface{}) { raw["mobile"].(map[string]interface{})["versionCode"] = 0 }, want: "mobile.versionCode"},
		{name: "version code bound", edit: func(raw map[string]interface{}) {
			raw["mobile"].(map[string]interface{})["versionCode"] = maxVersionCode + 1
		}, want: "mobile.versionCode"},
		{name: "missing start", edit: func(raw map[string]interface{}) { delete(raw["mobile"].(map[string]interface{}), "start") }, want: "mobile.start: is required"},
		{name: "missing domain", edit: func(raw map[string]interface{}) {
			delete(raw["mobile"].(map[string]interface{})["start"].(map[string]interface{}), "domain")
		}, want: "mobile.start.domain: is required"},
		{name: "domain uppercase", edit: func(raw map[string]interface{}) {
			raw["mobile"].(map[string]interface{})["start"].(map[string]interface{})["domain"] = "Notes.example"
		}, want: "must be lowercase"},
		{name: "domain IP", edit: func(raw map[string]interface{}) {
			raw["mobile"].(map[string]interface{})["start"].(map[string]interface{})["domain"] = "127.0.0.1"
		}, want: "not an IP"},
		{name: "domain hyphen edge", edit: func(raw map[string]interface{}) {
			raw["mobile"].(map[string]interface{})["start"].(map[string]interface{})["domain"] = "-notes.example"
		}, want: "start and end"},
		{name: "domain label bound", edit: func(raw map[string]interface{}) {
			raw["mobile"].(map[string]interface{})["start"].(map[string]interface{})["domain"] = strings.Repeat("a", 64) + ".example"
		}, want: "1 to 63"},
		{name: "path relative", edit: setStartPath("notes"), want: "absolute same-app"},
		{name: "path authority", edit: setStartPath("//evil.example/x"), want: "absolute same-app"},
		{name: "path encoded authority", edit: setStartPath("/%2f%2fevil.example/x"), want: "absolute same-app"},
		{name: "path twice encoded authority", edit: setStartPath("/%252f%252fevil.example/x"), want: "absolute same-app"},
		{name: "path traversal", edit: setStartPath("/notes/../private"), want: "traversal"},
		{name: "path encoded traversal", edit: setStartPath("/notes/%2e%2e/private"), want: "traversal"},
		{name: "path encoded twice traversal", edit: setStartPath("/notes/%252e%252e/private"), want: "traversal"},
		{name: "path backslash", edit: setStartPath("/notes\\private"), want: "backslashes"},
		{name: "path query", edit: setStartPath("/notes?admin=1"), want: "query"},
		{name: "path fragment", edit: setStartPath("/notes#private"), want: "fragment"},
		{name: "path bound", edit: setStartPath("/" + strings.Repeat("a", maxStartPathBytes)), want: "1 to"},
		{name: "orientation", edit: func(raw map[string]interface{}) {
			raw["mobile"].(map[string]interface{})["orientation"] = "upside-down"
		}, want: "mobile.orientation"},
		{name: "theme type", edit: func(raw map[string]interface{}) { raw["mobile"].(map[string]interface{})["theme"] = "dark" }, want: "mobile.theme: must be an object"},
		{name: "theme mode", edit: func(raw map[string]interface{}) {
			raw["mobile"].(map[string]interface{})["theme"] = map[string]interface{}{"mode": "auto"}
		}, want: "mobile.theme.mode"},
		{name: "theme color short", edit: func(raw map[string]interface{}) {
			raw["mobile"].(map[string]interface{})["theme"] = map[string]interface{}{"color": "#fff"}
		}, want: "#RRGGBB"},
		{name: "permissions type", edit: func(raw map[string]interface{}) {
			raw["mobile"].(map[string]interface{})["permissions"] = "clipboard.writeText"
		}, want: "must be an array"},
		{name: "permission unknown", edit: func(raw map[string]interface{}) {
			raw["mobile"].(map[string]interface{})["permissions"] = []interface{}{"http.request"}
		}, want: "unsupported permission"},
		{name: "permission implicit supports", edit: func(raw map[string]interface{}) {
			raw["mobile"].(map[string]interface{})["permissions"] = []interface{}{"capabilities.supports"}
		}, want: "unsupported permission"},
		{name: "permission duplicate", edit: func(raw map[string]interface{}) {
			raw["mobile"].(map[string]interface{})["permissions"] = []interface{}{"device.info", "device.info"}
		}, want: "duplicates"},
		{name: "permission non-string", edit: func(raw map[string]interface{}) {
			raw["mobile"].(map[string]interface{})["permissions"] = []interface{}{true}
		}, want: "must be a string"},
		{name: "permission count bound", edit: func(raw map[string]interface{}) {
			raw["mobile"].(map[string]interface{})["permissions"] = make([]interface{}, len(permissionSet)+1)
		}, want: "too many"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := validManifest()
			test.edit(raw)
			assertParseError(t, raw, test.want)
		})
	}
}

func TestEncodeJSONRoundTripAndNoParserFields(t *testing.T) {
	original, err := Parse(validManifest())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	encoded, err := EncodeJSON(original)
	if err != nil {
		t.Fatalf("EncodeJSON: %v", err)
	}
	text := string(encoded)
	for _, forbidden := range []string{"Declared", "declared", "Legacy", "legacy", "Domain", "Path", "Mode", "Color"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("encoded JSON leaked %q: %s", forbidden, text)
		}
	}
	for _, required := range []string{`"version":1`, `"title":"Pocket Notes"`, `"start":{"domain":`, `"theme":{"mode":`, `"permissions":[]`} {
		if !strings.Contains(text, required) {
			t.Fatalf("encoded JSON missing %q: %s", required, text)
		}
	}
	roundTrip, err := DecodeJSON(encoded)
	if err != nil {
		t.Fatalf("DecodeJSON round trip: %v", err)
	}
	if !reflect.DeepEqual(roundTrip, original) {
		t.Fatalf("round trip = %#v, want %#v", roundTrip, original)
	}
	generic, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if string(generic) != text {
		t.Fatalf("MarshalJSON = %s, EncodeJSON = %s", generic, encoded)
	}

	legacy, err := Parse(map[string]interface{}{"mobile": true})
	if err != nil {
		t.Fatalf("Parse legacy: %v", err)
	}
	if _, err := EncodeJSON(legacy); err == nil || !strings.Contains(err.Error(), "legacy") {
		t.Fatalf("legacy EncodeJSON error = %v", err)
	}
	if _, err := json.Marshal(legacy); err == nil || !strings.Contains(err.Error(), "legacy") {
		t.Fatalf("legacy MarshalJSON error = %v", err)
	}
	original.Permissions = nil
	if _, err := EncodeJSON(original); err == nil || !strings.Contains(err.Error(), "non-nil") {
		t.Fatalf("nil permissions EncodeJSON error = %v", err)
	}
}

func TestDecodeJSONIsStrictAndRevalidates(t *testing.T) {
	valid := []byte(`{
  "version":1,
  "id":"org.kitwork.notes",
  "title":"Notes",
  "icon":"assets/icon.svg",
  "versionName":"1.0.0",
  "versionCode":2,
  "start":{"domain":"notes.example","path":"/open"},
  "orientation":"auto",
  "theme":{"mode":"system","color":"#09090b"},
  "permissions":[]
}`)
	got, err := DecodeJSON(valid)
	if err != nil {
		t.Fatalf("DecodeJSON valid manifest: %v", err)
	}
	if got.ID != "org.kitwork.notes" || got.Start.Path != "/open" {
		t.Fatalf("decoded = %#v", got)
	}

	tests := []struct {
		name string
		json string
		want string
	}{
		{name: "top level", json: `[]`, want: "top level must be an object"},
		{name: "trailing", json: string(valid) + ` {}`, want: "trailing value"},
		{name: "duplicate", json: `{"version":1,"id":"org.kitwork.notes","id":"org.kitwork.other","title":"Notes","versionName":"1.0.0","versionCode":1,"start":{"domain":"notes.example","path":"/"},"orientation":"auto","theme":{"mode":"system","color":"#09090b"},"permissions":[]}`, want: `duplicate field "id"`},
		{name: "invalid JSON", json: `{`, want: "manifest JSON"},
		{name: "unknown", json: `{"version":1,"id":"org.kitwork.notes","title":"Notes","versionName":"1.0.0","versionCode":1,"start":{"domain":"notes.example","path":"/"},"orientation":"auto","theme":{"mode":"system","color":"#09090b"},"permissions":[],"web":{}}`, want: "unknown field"},
		{name: "decimal integer", json: `{"version":1,"id":"org.kitwork.notes","title":"Notes","versionName":"1.0.0","versionCode":1.0000000000000000001,"start":{"domain":"notes.example","path":"/"},"orientation":"auto","theme":{"mode":"system","color":"#09090b"},"permissions":[]}`, want: "versionCode"},
		{name: "exponent integer", json: `{"version":1e0,"id":"org.kitwork.notes","title":"Notes","versionName":"1.0.0","versionCode":1,"start":{"domain":"notes.example","path":"/"},"orientation":"auto","theme":{"mode":"system","color":"#09090b"},"permissions":[]}`, want: "version"},
		{name: "missing canonical field", json: `{"version":1}`, want: "id is required"},
		{name: "revalidate", json: `{"version":1,"id":"org.kitwork.notes","title":"Notes","icon":"../private.svg","versionName":"1.0.0","versionCode":1,"start":{"domain":"notes.example","path":"/"},"orientation":"auto","theme":{"mode":"system","color":"#09090b"},"permissions":[]}`, want: "icon:"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := DecodeJSON([]byte(test.json))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("DecodeJSON error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func setStartPath(path string) func(map[string]interface{}) {
	return func(raw map[string]interface{}) {
		raw["mobile"].(map[string]interface{})["start"].(map[string]interface{})["path"] = path
	}
}

func assertParseError(t *testing.T, raw map[string]interface{}, want string) {
	t.Helper()
	_, err := Parse(raw)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("Parse error = %v, want substring %q", err, want)
	}
}
