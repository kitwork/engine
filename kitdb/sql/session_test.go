package sql

import "testing"

func TestParseKillSession(t *testing.T) {
	for _, source := range []string{"KILL SESSION 12", "kill session 9223372036854775807;"} {
		id, handled, err := ParseKillSession(source)
		if !handled || err != nil || id <= 0 {
			t.Fatalf("%s: %d %v %v", source, id, handled, err)
		}
	}
	for _, source := range []string{"KILL SESSION 0", "KILL SESSION -1", "KILL SESSION 1.5", "KILL SESSION $1", "KILL SESSION '1'", "KILL SESSION 9223372036854775808", "KILL SESSION 1; SELECT 1", "KILL 1"} {
		_, handled, err := ParseKillSession(source)
		if !handled || err == nil {
			t.Fatalf("accepted %s", source)
		}
	}
	for _, source := range []string{"SELECT 'kill session 12'", "SELECT * FROM kill", "CREATE TABLE kills(id INT)"} {
		if _, handled, _ := ParseKillSession(source); handled {
			t.Fatalf("claimed %s", source)
		}
	}
}
