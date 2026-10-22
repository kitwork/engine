package engine

import "testing"

// The host's ceiling on a request body is the app's to set: max_body (app.web({ maxBody })), a size
// read as request.json({ limit }) reads one; an unreadable one is a configuration error.
func TestConfigMaxBody(t *testing.T) {
	cfg, err := ParseConfig(map[string]interface{}{"max_body": "4mb"})
	if err != nil || cfg.MaxBody != 4<<20 {
		t.Fatalf("max_body 4mb: %v, %v", cfg, err)
	}
	if _, err := ParseConfig(map[string]interface{}{"max_body": "lots"}); err == nil {
		t.Fatal("an unreadable max_body passed")
	}
	if readHeaderTimeout <= 0 {
		t.Fatal("the server has no header timeout")
	}
}
