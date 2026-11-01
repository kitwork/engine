package gateway

import (
	"context"
	"strings"
	"testing"

	"github.com/kitwork/engine/kitdb/pgwire"
)

// A decimal column (orders.amount at scale 2: 4.50, -0.05 and 1234.56, stored 450, -5 and 123456) compares and computes
// as its decimal: a literal or a parameter it is compared with becomes the stored integer, and an index on the column
// still serves; a sum is exact (0.10 + 0.20 is 0.3); a subquery, a CTE or a SELECT * inside one hands its outer SELECT the decimal.
//
// CONTROL: with decimalStatement returning the statement as it came, "amount > 100 = 1|3, want 3"; with every
// comparison wrapped instead of its literal scaled, "amount = '4.50' = , want 1"; with sum taking the REALs,
// "sum(total), avg(total) = 0.30000000000000004,0.15000000000000002, want 0.3,0.15"; with a subquery's item kept as
// stored, "(SELECT amount FROM orders) WHERE amount > 100 = 2, want 1"; with the first SELECT of a UNION keeping its
// column as stored, "… UNION ALL SELECT amount * 2 … = 4.50|0.09, want 4.5|9".
func TestDecimalComparesAsItsValue(t *testing.T) {
	fixture := newFixture(t)
	if _, err := fixture.writer.Exec("CREATE INDEX orders_amount ON orders(amount)"); err != nil {
		t.Fatal(err)
	}
	// 0.10 and 0.20, whose sum as REALs is 0.30000000000000004.
	if _, err := fixture.billingDB.Exec("INSERT INTO invoices VALUES (12, 3, 10), (13, 3, 20)"); err != nil {
		t.Fatal(err)
	}
	session := openSession(t, fixture, defaultAccess(), Options{})
	for _, check := range []struct{ query, want string }{
		{"SELECT id FROM orders WHERE amount > 100 ORDER BY id", "3"},
		{"SELECT id FROM orders WHERE amount = 4.5", "1"},
		{"SELECT id FROM orders WHERE amount = '4.50'", "1"},
		{"SELECT id FROM orders WHERE amount < 0", "2"},
		{"SELECT id FROM orders WHERE 100 < amount", "3"},
		{"SELECT id FROM orders WHERE amount = -0.05", "2"},
		{"SELECT id FROM orders WHERE amount BETWEEN 0 AND 5", "1"},
		{"SELECT id FROM orders WHERE amount NOT BETWEEN 0 AND 5 ORDER BY id", "2|3"},
		{"SELECT id FROM orders WHERE amount IN (4.5, -0.05) ORDER BY id", "1|2"},
		{"SELECT id FROM orders WHERE orders.amount >= 1234.56", "3"},
		{"SELECT id FROM public.orders WHERE public.orders.amount <= -0.05", "2"},
		{"SELECT id FROM orders o WHERE o.amount > 4.505", "3"},
		{"SELECT id FROM orders WHERE amount = 4.505", ""},
		{"SELECT id FROM orders WHERE amount * 2 > 9 ORDER BY id", "3"},
		{"SELECT id FROM orders WHERE amount IS NOT NULL ORDER BY id", "1|2|3"},
		{"SELECT id FROM orders ORDER BY amount DESC", "3|1|2"},
		{"SELECT amount FROM orders WHERE amount > 100", "1234.56"},
		{"SELECT sum(amount) FROM orders", "1239.01"},
		{"SELECT max(amount), min(amount), count(amount), avg(amount) > 413 FROM orders", "1234.56,-0.05,3,1"},
		{"SELECT amount * 2 AS twice FROM orders WHERE id = 1", "9"},
		{"SELECT u.name, sum(o.amount) FROM users u JOIN orders o ON o.user_id = u.id GROUP BY u.name HAVING sum(o.amount) > 4 ORDER BY u.name",
			"An,4.45|Binh,1234.56"},
		{"SELECT count(*) FROM orders o JOIN users u ON u.id = o.user_id WHERE o.amount < u.score * 10", "2"},
		{"SELECT count(*) FROM (SELECT amount FROM orders) WHERE amount > 100", "1"},
		{"SELECT count(*) FROM (SELECT * FROM orders) d WHERE d.amount > 100", "1"},
		{"SELECT t FROM (SELECT amount t FROM orders) WHERE t > 100", "1234.56"},
		{"WITH big AS (SELECT id, amount FROM orders) SELECT id FROM big WHERE amount > 100", "3"},
		{"SELECT name FROM users u WHERE EXISTS (SELECT 1 FROM orders WHERE user_id = u.id AND amount > 100)", "Binh"},
		{"SELECT id FROM orders WHERE amount > (SELECT total FROM billing.invoices WHERE id = 11) ORDER BY id", "3"},
		{"SELECT sum(total), avg(total) FROM billing.invoices WHERE user_id = 3", "0.3,0.15"},
		{"SELECT amount FROM orders WHERE id = 1 UNION ALL SELECT amount * 2 FROM orders WHERE id = 1", "4.5|9"},
		{"SELECT * FROM orders WHERE id = 3 UNION ALL SELECT * FROM orders WHERE id = 2", "3,2,1234.56|2,1,-0.05"},
	} {
		if got := cells(mustRun(t, session, check.query)); got != check.want {
			t.Fatalf("%s = %s, want %s", check.query, got, check.want)
		}
	}

	parameters := []struct {
		query  string
		values []string
		want   string
	}{
		{"SELECT id FROM orders WHERE amount > $1 ORDER BY id", []string{"4"}, "1|3"},
		{"SELECT id FROM orders WHERE amount BETWEEN $1 AND $2 ORDER BY id", []string{"-1", "5"}, "1|2"},
		{"SELECT id FROM orders WHERE amount = $1", []string{"1234.56"}, "3"},
	}
	for _, check := range parameters {
		result, err := session.Execute(context.Background(), check.query, textParameters(check.values...))
		if err != nil || cells(result) != check.want {
			t.Fatalf("%s %v = %s (%v), want %s", check.query, check.values, cells(result), err, check.want)
		}
	}
	if _, err := session.Execute(context.Background(), "SELECT id FROM orders WHERE amount > $1", textParameters("abc")); codeOf(err) != "22P02" {
		t.Fatalf("amount > 'abc' = %v, want 22P02", err)
	}

	// A comparison with a literal reads the index on the column, as it did before.
	plan := cells(mustRun(t, session, "EXPLAIN QUERY PLAN SELECT id FROM orders WHERE amount > 100"))
	if !strings.Contains(plan, "orders_amount") {
		t.Fatalf("plan of amount > 100 = %q, want the index orders_amount", plan)
	}

	// The column alone is still the numeric of its scale.
	if bare := mustRun(t, session, "SELECT amount FROM orders WHERE amount > 100"); bare.Columns[0].DataTypeOID != pgwire.OIDNumeric {
		t.Fatalf("amount alone has OID %d, want numeric", bare.Columns[0].DataTypeOID)
	}

	// The names SQLite gave the columns stay.
	named := mustRun(t, session, "SELECT amount * 2, sum(amount) OVER (), max(amount) AS most FROM orders WHERE id = 1")
	for index, want := range []string{"amount * 2", "sum(amount) OVER ()", "most"} {
		if named.Columns[index].Name != want {
			t.Fatalf("column %d is named %q, want %q", index, named.Columns[index].Name, want)
		}
	}

	expectCode(t, session, "SELECT count(*) FROM (SELECT * FROM orders NATURAL JOIN users)", "0A000", "list the columns")
}

// A literal becomes the stored integer exactly, whatever its form; what is not a decimal number is not one.
func TestScaledDecimal(t *testing.T) {
	for _, check := range []struct {
		text  string
		scale int
		want  string
	}{
		{"1000", 2, "100000"}, {"4.5", 2, "450"}, {"4.50", 2, "450"}, {"-0.05", 2, "-5"}, {"1.005", 2, "100.5"},
		{".5", 2, "50"}, {"0.001", 2, "0.1"}, {"5e-3", 2, "0.5"}, {"1e3", 2, "100000"}, {"1E+2", 3, "100000"},
		{"10.", 2, "1000"}, {"-0", 2, "0"}, {"+7", 0, "7"}, {"007.10", 1, "71"},
	} {
		if got, ok := scaledDecimal(check.text, check.scale); !ok || got != check.want {
			t.Fatalf("scaledDecimal(%q, %d) = %q %v, want %q", check.text, check.scale, got, ok, check.want)
		}
	}
	for _, text := range []string{"", ".", "abc", "0x10", "1_000", "1e", "1e999", "4.5.6", "--1"} {
		if got, ok := scaledDecimal(text, 2); ok {
			t.Fatalf("scaledDecimal(%q) = %q, want no number", text, got)
		}
	}
}
