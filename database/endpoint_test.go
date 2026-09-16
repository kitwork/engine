package database

import "testing"

// A URL connection must describe itself by host and path, never by password — and never as the
// ":0 (DB: )" the field-based line produced when Host/Port/Name were empty.
func TestEndpoint(t *testing.T) {
	for _, c := range []struct {
		cfg  Config
		want string
	}{
		{Config{Type: "postgres", Host: "db.internal", Port: 5432, Name: "kitwork"}, "db.internal:5432 (DB: kitwork)"},
		{Config{Type: "postgres", URL: "postgres://kit:secret@replica.internal:5432/kitwork?sslmode=disable"}, "replica.internal:5432/kitwork (url)"},
		{Config{Type: "postgres", URL: "postgresql://a:b@c/d"}, "c/d (url)"},
	} {
		if got := c.cfg.Endpoint(); got != c.want {
			t.Errorf("Endpoint(%+v) = %q, want %q", c.cfg, got, c.want)
		}
		if got := c.cfg.Endpoint(); c.cfg.URL != "" && (contains(got, "secret") || contains(got, ":b@")) {
			t.Errorf("Endpoint leaked a password: %q", got)
		}
	}
}

func contains(s, sub string) bool { return len(sub) > 0 && len(s) >= len(sub) && indexOf(s, sub) >= 0 }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
