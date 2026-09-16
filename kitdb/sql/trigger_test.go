package sql

import (
	"strings"
	"testing"
)

func TestTriggerParser(t *testing.T) {
	plan, err := ParseStatement(`CREATE TRIGGER audit_price AFTER UPDATE ON public.products FOR EACH ROW WHEN (OLD.price <> NEW.price) INSERT INTO public.audit (id, before_price, after_price, label) VALUES (DEFAULT, OLD.price, NEW.price, lower(NEW.name))`)
	if err != nil {
		t.Fatal(err)
	}
	trigger := plan.CreateTrigger
	if trigger == nil || trigger.Name != "audit_price" || trigger.Event != "update" || trigger.Table != "products" || trigger.Target != "audit" || trigger.When == nil || len(trigger.Values) != 4 || trigger.Values[0].Literal.Kind != LiteralDefault || trigger.Values[1].Field != "OLD.price" && trigger.Values[1].Field != "old.price" {
		t.Fatalf("trigger: %#v", trigger)
	}
	for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
		if _, err := ParseStatement(`CREATE TRIGGER t AFTER ` + event + ` ON a FOR EACH ROW INSERT INTO b (id) VALUES (1)`); err != nil {
			t.Fatal(err)
		}
	}
	drop, err := ParseStatement(`DROP TRIGGER IF EXISTS audit_price ON public.products RESTRICT`)
	if err != nil || drop.DropTrigger == nil || !drop.DropTrigger.IfExists || drop.DropTrigger.Table != "products" {
		t.Fatalf("drop: %#v %v", drop, err)
	}
	for _, query := range []string{
		`CREATE TRIGGER t BEFORE INSERT ON a FOR EACH ROW INSERT INTO b (id) VALUES (1)`,
		`CREATE TRIGGER t AFTER INSERT OR UPDATE ON a FOR EACH ROW INSERT INTO b (id) VALUES (1)`,
		`CREATE TRIGGER t AFTER UPDATE OF name ON a FOR EACH ROW INSERT INTO b (id) VALUES (1)`,
		`CREATE TRIGGER t AFTER INSERT ON a FOR EACH STATEMENT INSERT INTO b (id) VALUES (1)`,
		`CREATE TRIGGER t AFTER INSERT ON a FOR EACH ROW EXECUTE FUNCTION f()`,
		`CREATE TRIGGER t AFTER INSERT ON a FOR EACH ROW BEGIN INSERT INTO b (id) VALUES (1); END`,
		`CREATE TRIGGER t AFTER INSERT ON a FOR EACH ROW INSERT INTO b (id) VALUES (1), (2)`,
		`CREATE TRIGGER t AFTER INSERT ON a FOR EACH ROW INSERT INTO b (id) VALUES (1) RETURNING id`,
		`CREATE TRIGGER t AFTER INSERT ON a FOR EACH ROW INSERT INTO b (id) VALUES (1, 2)`,
		`CREATE TRIGGER t AFTER INSERT ON a FOR EACH ROW INSERT INTO b (id) SELECT 1`,
		`CREATE OR REPLACE TRIGGER t AFTER INSERT ON a FOR EACH ROW INSERT INTO b (id) VALUES (1)`,
		`DROP TRIGGER t ON a CASCADE`,
		`CREATE TRIGGER t AFTER INSERT ON a FOR EACH ROW INSERT INTO b (` + strings.Repeat("id,", 32) + `id) VALUES (1)`,
	} {
		if _, err := ParseStatement(query); err == nil {
			t.Errorf("accepted: %s", query)
		}
	}
}
