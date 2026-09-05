package sql

import (
	"strings"
	"testing"
)

func TestJoinConjunctiveEqualities(t *testing.T) {
	for _, on := range []string{
		`p.merchant = e.merchant AND e.product_id = p.id`,
		`(p.merchant = e.merchant AND (e.product_id = p.id))`,
	} {
		parsed, err := ParseStatement(`SELECT COUNT(*) FROM events e JOIN products p ON ` + on + ` WHERE e.id > $1`)
		if err != nil {
			t.Fatal(err)
		}
		join := parsed.Select.Joins[0]
		if join.Left != "p.merchant" || join.Right != "e.merchant" || len(join.And) != 1 ||
			join.And[0].Left != "e.product_id" || join.And[0].Right != "p.id" {
			t.Fatalf("equalities = %+v", join)
		}
	}
	base := `SELECT e.id FROM events e JOIN products p ON `
	for _, on := range []string{
		`e.id > p.id`, `e.id = 1`, `e.id = p.id OR e.merchant = p.merchant`,
		`e.id = p.id AND`, `()`, `(e.id = p.id`,
		strings.Repeat("(", MaximumQueryNesting+1) + `e.id = p.id` + strings.Repeat(")", MaximumQueryNesting+1),
		strings.Repeat(`e.id = p.id AND `, MaximumJoinEqualities) + `e.id = p.id`,
	} {
		if _, err := ParseStatement(base + on); err == nil {
			t.Fatalf("accepted unsupported JOIN ON %s", on)
		}
	}
	if _, err := ParseStatement(base + strings.Repeat(`e.id = p.id AND `, MaximumJoinEqualities-1) + `e.id = p.id`); err != nil {
		t.Fatalf("exact equality bound: %v", err)
	}
}
