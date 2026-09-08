package manifest

// Version 10 adds explicit foreground hardware and local scheduling authority.
// Older manifest versions retain their exact permission sets.
var permissionSetV10 = func() map[string]struct{} {
	result := make(map[string]struct{}, len(permissionSetV9)+4)
	for permission := range permissionSetV9 {
		result[permission] = struct{}{}
	}
	for _, permission := range []string{"biometric.authenticate", "geolocation.read", "nfc.scan", "notifications.schedule"} {
		result[permission] = struct{}{}
	}
	return result
}()
