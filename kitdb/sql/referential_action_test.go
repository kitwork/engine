package sql

import "testing"

func TestParseStandaloneReferentialActions(t *testing.T) {
	for _, action := range []string{"CASCADE", "SET NULL", "SET DEFAULT", "NO ACTION", "RESTRICT"} {
		for _, event := range []string{"DELETE", "UPDATE"} {
			for _, declaration := range []string{`pid INTEGER REFERENCES p(id)`, `pid INTEGER, CONSTRAINT link FOREIGN KEY(pid) REFERENCES p(id)`} {
				plan, err := ParseStatement(`CREATE TABLE c(id INTEGER PRIMARY KEY, ` + declaration + ` ON ` + event + ` ` + action + `)`)
				if err != nil || plan.CreateTable == nil || len(plan.CreateTable.ForeignKeys) != 1 {
					t.Fatalf("%s %s: %v %v", event, action, plan, err)
				}
			}
		}
	}
	for _, suffix := range []string{"ON DELETE SET DEFAULT(pid)", "ON UPDATE SET DEFAULT(pid)", "ON DELETE SET NULL(pid)", "DEFERRABLE INITIALLY DEFERRED"} {
		if _, err := ParseStatement(`CREATE TABLE c(id INTEGER PRIMARY KEY, pid INTEGER REFERENCES p(id) ` + suffix + `)`); err == nil {
			t.Fatal("unsupported action accepted", suffix)
		}
	}
}
