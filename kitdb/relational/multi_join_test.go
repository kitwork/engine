package relational

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestIndexedMultiJoinUsesOneSnapshot(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "multi-join.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	for _, statement := range []string{
		`CREATE TABLE teams (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`,
		`CREATE TABLE users (id INTEGER PRIMARY KEY, team_id INTEGER, name TEXT NOT NULL)`,
		`CREATE TABLE events (id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL, kind TEXT NOT NULL)`,
		`CREATE TABLE labels (id INTEGER PRIMARY KEY, event_id INTEGER NOT NULL, label TEXT NOT NULL)`,
		`INSERT INTO teams (id, name) VALUES (10, 'Core')`,
		`INSERT INTO users (id, team_id, name) VALUES (1, 10, 'An'), (2, NULL, 'Binh')`,
		`INSERT INTO events (id, user_id, kind) VALUES (100, 1, 'open'), (101, 2, 'click')`,
		`INSERT INTO labels (id, event_id, label) VALUES (1, 100, 'important')`,
	} {
		if _, err := engine.Execute(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}

	result, err := engine.Execute(ctx, `
		SELECT e.id AS event_id, u.name AS user_name, t.name AS team_name
		FROM events e
		JOIN users u ON u.id = e.user_id
		LEFT JOIN teams t ON t.id = u.team_id
		ORDER BY event_id
	`)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rows) != 2 || result.Rows[0][1] != "An" || result.Rows[0][2] != "Core" ||
		result.Rows[1][1] != "Binh" || result.Rows[1][2] != nil {
		t.Fatalf("multi JOIN rows = %#v", result.Rows)
	}

	if _, err := engine.Execute(ctx, `
		SELECT e.id, l.label
		FROM events e JOIN labels l ON l.event_id = e.id
	`); err == nil || !strings.Contains(err.Error(), "requires a primary, unique, or leading index") {
		t.Fatalf("unindexed JOIN error = %v", err)
	}
}
