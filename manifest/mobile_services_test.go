package manifest

import "testing"

func TestMobileV10CommonServicesExplicitAndVersioned(t *testing.T) {
	for _, grant := range []string{"biometric.authenticate", "geolocation.read", "nfc.scan", "notifications.schedule"} {
		for version := 1; version <= 10; version++ {
			raw := validManifest()
			mobile := raw["mobile"].(map[string]interface{})
			mobile["version"] = version
			permissions := []interface{}{grant}
			if grant == "notifications.schedule" {
				permissions = append(permissions, "notifications.show")
			}
			mobile["permissions"] = permissions
			parsed, err := Parse(raw)
			if version < 10 {
				if err == nil {
					t.Fatalf("v%d unexpectedly grants %s", version, grant)
				}
			} else {
				if err != nil {
					t.Fatalf("v10 %s: %v", grant, err)
				}
				encoded, err := EncodeJSON(parsed)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = DecodeJSON(encoded); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	raw := validManifest()
	m := raw["mobile"].(map[string]interface{})
	m["version"] = 10
	m["permissions"] = []interface{}{"notifications.schedule"}
	if _, err := Parse(raw); err == nil {
		t.Fatal("schedule gained show authority")
	}
	for _, private := range []string{"biometric.begin", "geolocation.permission", "nfc.poll", "notifications.cancel"} {
		m["permissions"] = []interface{}{private}
		if _, err := Parse(raw); err == nil {
			t.Fatalf("private action granted: %s", private)
		}
	}
}
