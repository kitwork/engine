package main

import (
	"context"
	"database/sql"
	"debug/buildinfo"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
)

type commerceEvidence struct {
	Format   string     `json:"format"`
	Platform string     `json:"platform"`
	Go       string     `json:"go_version"`
	Scope    string     `json:"scope"`
	Binaries []artifact `json:"binaries"`
	Phases   []string   `json:"passed_phases"`
	Success  bool       `json:"success"`
}

// Unlike the distribution journey, this gate never skips or accepts an old
// binary directory. Build the current checkout into a disposable native pair.
func TestKitDBCommerceNativeJourney(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	evidence := &commerceEvidence{Format: "kitdb-commerce-journey/v1", Platform: runtime.GOOS + "/" + runtime.GOARCH, Go: runtime.Version(), Scope: "current-checkout native process drill; not distribution or power-loss qualification"}
	t.Cleanup(func() {
		evidence.Success = !t.Failed()
		data, err := json.MarshalIndent(evidence, "", "  ")
		if err != nil {
			t.Error(err)
			return
		}
		t.Log(string(data))
		if path := os.Getenv("KITDB_COMMERCE_REPORT"); path != "" {
			if !filepath.IsAbs(path) {
				path = filepath.Join(root, path)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Error(err)
				return
			}
			if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
				t.Error(err)
			}
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	binary := nativeApplicationBinary(t.TempDir())
	environment := map[string]string{"CGO_ENABLED": "0", "GOOS": runtime.GOOS, "GOARCH": runtime.GOARCH, "GOFLAGS": "", "GOWORK": "off", "GOEXPERIMENT": ""}
	dependencies, err := run(ctx, root, environment, "go", "list", "-mod=readonly", "-buildvcs=false", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}", "./cmd/kitdb", "./cmd/kitdbpg")
	if err != nil {
		t.Fatal(err)
	}
	for _, dependency := range strings.Fields(string(dependencies)) {
		if !allowedPackage(dependency) {
			t.Fatalf("commerce binary imports non-standalone package %s", dependency)
		}
	}
	for _, name := range []string{"kitdb", "kitdbpg"} {
		_, err := run(ctx, root, environment, "go", "build", "-mod=readonly", "-buildvcs=false", "-o", binary(name), "./cmd/"+name)
		if err != nil {
			t.Fatal(err)
		}
		info, err := buildinfo.ReadFile(binary(name))
		if err != nil {
			t.Fatal(err)
		}
		pure := false
		for _, setting := range info.Settings {
			if setting.Key == "CGO_ENABLED" && setting.Value == "0" {
				pure = true
			}
		}
		if !pure || info.Path != modulePath+"/cmd/"+name || len(info.Deps) != 0 {
			t.Fatalf("unexpected native binary profile: %+v", info)
		}
		digest, err := digestFile(binary(name))
		if err != nil {
			t.Fatal(err)
		}
		evidence.Binaries = append(evidence.Binaries, digest)
	}
	evidence.Phases = append(evidence.Phases, "fresh-pure-go-binaries")
	exerciseCommerceBinaries(t, binary, evidence)
}

// Expected money is computed in integer cents, never read back from KitDB.
// Audit IDs are allowed gaps, but their order and every business field must match.
type commerceModel struct {
	next                 int64
	stock                [3]int64
	orders, items, audit [][]string
}

var commercePrices = [3]int64{1995, 505, 9007199254740993}

func commerceMoney(cents int64) string    { return fmt.Sprintf("%d.%02d", cents/100, cents%100) }
func commerceNumber(n int64) string       { return strconv.FormatInt(n, 10) }
func commerceRational(cents int64) string { return big.NewRat(cents, 100).RatString() }

func (model *commerceModel) clone() *commerceModel {
	copy := *model
	copy.orders = append([][]string(nil), model.orders...)
	copy.items = append([][]string(nil), model.items...)
	copy.audit = append([][]string(nil), model.audit...)
	return &copy
}

func commerceExec(t *testing.T, ctx context.Context, client *sql.DB, query string) {
	t.Helper()
	if _, err := client.ExecContext(ctx, query); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func commerceSchema(t *testing.T, ctx context.Context, client *sql.DB) {
	t.Helper()
	for _, q := range []string{
		`CREATE DOMAIN money AS NUMERIC(22,2) NOT NULL CHECK (VALUE >= 0)`,
		`CREATE SEQUENCE order_ids AS BIGINT START 1000 CACHE 1`,
		`CREATE FUNCTION normalize_reference(s TEXT) RETURNS TEXT LANGUAGE SQL RETURN upper(trim(s))`,
		`CREATE FUNCTION line_amount(price NUMERIC, quantity INTEGER) RETURNS NUMERIC LANGUAGE SQL RETURN price * quantity`,
		`CREATE TABLE products (id BIGINT PRIMARY KEY, sku TEXT UNIQUE NOT NULL, price money, stock INTEGER NOT NULL CHECK (stock >= 0))`,
		`CREATE TABLE orders (id BIGINT PRIMARY KEY DEFAULT nextval('order_ids'), external_ref TEXT UNIQUE NOT NULL, total money DEFAULT 0, status TEXT NOT NULL DEFAULT 'pending' CHECK (status = 'pending' OR status = 'paid'))`,
		`CREATE TABLE order_items (order_id BIGINT REFERENCES orders(id) ON DELETE RESTRICT, product_id BIGINT REFERENCES products(id) ON DELETE RESTRICT, quantity INTEGER NOT NULL CHECK (quantity > 0), unit_price money, line_total money, PRIMARY KEY (order_id,product_id))`,
		`CREATE TABLE audit (id BIGSERIAL PRIMARY KEY, order_id BIGINT NOT NULL REFERENCES orders(id) ON DELETE RESTRICT, product_id BIGINT NOT NULL, event TEXT NOT NULL, amount money, ref TEXT NOT NULL CHECK (ref <> 'REJECT-AUDIT'))`,
		`CREATE INDEX orders_status ON orders(status)`,
		`CREATE INDEX items_product ON order_items(product_id)`,
		`CREATE TRIGGER orders_created AFTER INSERT ON orders FOR EACH ROW INSERT INTO audit(order_id,product_id,event,amount,ref) VALUES (NEW.id,0,'created',NEW.total,NEW.external_ref)`,
		`CREATE TRIGGER items_created AFTER INSERT ON order_items FOR EACH ROW INSERT INTO audit(order_id,product_id,event,amount,ref) VALUES (NEW.order_id,NEW.product_id,'item',NEW.line_total,'ITEM')`,
		`CREATE TRIGGER orders_changed AFTER UPDATE ON orders FOR EACH ROW INSERT INTO audit(order_id,product_id,event,amount,ref) VALUES (NEW.id,0,'paid',NEW.total,NEW.external_ref)`,
		`INSERT INTO products VALUES (1,'KEYBOARD',19.95,200),(2,'CABLE',5.05,180),(3,'EXACT',90071992547409.93,2)`,
	} {
		commerceExec(t, ctx, client, q)
	}
}

func commerceRead(ctx context.Context, client *sql.DB, query string, money []int, serial bool) ([][]string, error) {
	rows, err := client.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var result [][]string
	var previous int64
	for rows.Next() {
		values := make([]sql.NullString, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, err
		}
		row := make([]string, len(values))
		for i, v := range values {
			if !v.Valid {
				return nil, fmt.Errorf("unexpected NULL in column %d", i)
			}
			row[i] = v.String
		}
		if serial {
			id, err := strconv.ParseInt(row[0], 10, 64)
			if err != nil || id <= previous {
				return nil, fmt.Errorf("invalid audit identity %q", row[0])
			}
			previous = id
			row = row[1:]
		}
		for _, i := range money {
			value, ok := new(big.Rat).SetString(row[i])
			if !ok {
				return nil, fmt.Errorf("invalid decimal %q", row[i])
			}
			row[i] = value.RatString()
		}
		result = append(result, row)
		if len(result) > 256 {
			return nil, fmt.Errorf("commerce oracle row budget exceeded")
		}
	}
	return result, rows.Err()
}

func (model *commerceModel) verify(ctx context.Context, client *sql.DB) error {
	products := make([][]string, 3)
	for i, sku := range []string{"KEYBOARD", "CABLE", "EXACT"} {
		products[i] = []string{commerceNumber(int64(i + 1)), sku, commerceRational(commercePrices[i]), commerceNumber(model.stock[i])}
	}
	for _, check := range []struct {
		query  string
		money  []int
		serial bool
		want   [][]string
	}{
		{`SELECT id,sku,price,stock FROM products ORDER BY id`, []int{2}, false, products},
		{`SELECT id,external_ref,total,status FROM orders ORDER BY id`, []int{2}, false, model.orders},
		{`SELECT order_id,product_id,quantity,unit_price,line_total FROM order_items ORDER BY order_id,product_id`, []int{3, 4}, false, model.items},
		{`SELECT id,order_id,product_id,event,amount,ref FROM audit ORDER BY id`, []int{3}, true, model.audit},
	} {
		got, err := commerceRead(ctx, client, check.query, check.money, check.serial)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(got, check.want) {
			return fmt.Errorf("independent commerce model mismatch for %s: got %v, want %v", check.query, got, check.want)
		}
	}
	// Independently check maintained counts, a secondary-index predicate, and
	// a JOIN aggregate rather than trusting the table-scan path alone.
	for i, table := range []string{"products", "orders", "order_items", "audit"} {
		var count int
		if err := client.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
			return err
		}
		if want := []int{3, len(model.orders), len(model.items), len(model.audit)}[i]; count != want {
			return fmt.Errorf("%s count=%d want=%d", table, count, want)
		}
	}
	paid, err := commerceRead(ctx, client, `SELECT id,external_ref,total,status FROM orders WHERE status='paid' ORDER BY id`, []int{2}, false)
	if err != nil || !reflect.DeepEqual(paid, model.orders) {
		return fmt.Errorf("paid index mismatch: %v %v", paid, err)
	}
	joined, err := commerceRead(ctx, client, `SELECT o.id,o.total,SUM(i.line_total) FROM orders o JOIN order_items i ON o.id=i.order_id GROUP BY o.id,o.total ORDER BY o.id`, []int{1, 2}, false)
	if err != nil {
		return err
	}
	var expected [][]string
	for _, order := range model.orders {
		expected = append(expected, []string{order[0], order[2], order[2]})
	}
	if !reflect.DeepEqual(joined, expected) {
		return fmt.Errorf("order/line totals disagree: %v want %v", joined, expected)
	}
	for _, check := range []struct {
		query string
		count int
	}{
		{"SELECT proname FROM pg_proc", 2}, {"SELECT tgname FROM pg_trigger", 3},
	} {
		rows, err := commerceRead(ctx, client, check.query, nil, false)
		if err != nil || len(rows) != check.count {
			return fmt.Errorf("%s catalog count: %d %v", check.query, len(rows), err)
		}
	}
	return nil
}

// Leave pending transactions open only for the hard-kill phase. Expected rows
// are appended only after an acknowledged COMMIT; errors never trigger retry.
func commerceOrder(t *testing.T, ctx context.Context, client *sql.DB, model *commerceModel, reference, failure string) *sql.Tx {
	t.Helper()
	tx, err := client.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	keep := false
	defer func() {
		if !keep {
			_ = tx.Rollback()
		}
	}()
	id := model.next
	var actual int64
	var ref string
	if err := tx.QueryRowContext(ctx, `INSERT INTO orders(external_ref) VALUES (normalize_reference($1)) RETURNING id,normalize_reference(external_ref)`, " "+strings.ToLower(reference)+" ").Scan(&actual, &ref); err != nil || actual != id || ref != reference {
		t.Fatalf("order identity: %d/%s want %d/%s: %v", actual, ref, id, reference, err)
	}
	model.next++ // CACHE 1 sequence reservation survives rollback/process death.
	first := 0
	if reference == "EXACT" {
		first = 2
	}
	var items, audit [][]string
	audit = append(audit, []string{commerceNumber(id), "0", "created", "0", reference})
	stock := model.stock
	var total int64
	for _, p := range []int{first, 1} {
		quantity := int64(1)
		if p == 0 {
			quantity = 2
		}
		amount := commercePrices[p] * quantity
		total += amount
		var stored, calculated string
		q := `INSERT INTO order_items(order_id,product_id,quantity,unit_price,line_total) VALUES ($1,$2,$3,CAST($4 AS NUMERIC),line_amount(CAST($4 AS NUMERIC),$3)) RETURNING line_total,line_amount(unit_price,quantity)`
		if err := tx.QueryRowContext(ctx, q, id, p+1, quantity, commerceMoney(commercePrices[p])).Scan(&stored, &calculated); err != nil {
			t.Fatal(err)
		}
		for _, value := range []string{stored, calculated} {
			got, ok := new(big.Rat).SetString(value)
			if !ok || got.Cmp(big.NewRat(amount, 100)) != 0 {
				t.Fatalf("line amount %s, want %d cents", value, amount)
			}
		}
		var remaining int64
		if err := tx.QueryRowContext(ctx, `UPDATE products SET stock=stock-$1 WHERE id=$2 RETURNING stock`, quantity, p+1).Scan(&remaining); err != nil || remaining != stock[p]-quantity {
			t.Fatalf("inventory %d: %d %v", p+1, remaining, err)
		}
		stock[p] -= quantity
		items = append(items, []string{commerceNumber(id), commerceNumber(int64(p + 1)), commerceNumber(quantity), commerceRational(commercePrices[p]), commerceRational(amount)})
		audit = append(audit, []string{commerceNumber(id), commerceNumber(int64(p + 1)), "item", commerceRational(amount), "ITEM"})
	}
	// SQL orders lines by composite key, not by the order they were inserted.
	if first == 2 {
		items[0], items[1] = items[1], items[0]
	}
	query := `UPDATE orders SET total=CAST($1 AS NUMERIC),status='paid' WHERE id=$2 RETURNING total`
	args := []any{commerceMoney(total), id}
	message := ""
	switch failure {
	case "domain":
		args[0] = "-1"
		message = "domain"
	case "trigger":
		query = `UPDATE orders SET total=CAST($1 AS NUMERIC),status='paid',external_ref='REJECT-AUDIT' WHERE id=$2 RETURNING total`
		message = "trigger"
	case "returning":
		query = `UPDATE orders SET total=CAST($1 AS NUMERIC),status='paid' WHERE id=$2 RETURNING total/(total-total)`
		message = "RETURNING row 1"
	case "foreign-key":
		query = `INSERT INTO order_items VALUES ($1,9999,1,1,1) RETURNING line_total`
		args = []any{id}
		message = "foreign"
	case "unique":
		query = `INSERT INTO order_items VALUES ($1,2,1,1,1) RETURNING line_total`
		args = []any{id}
		message = "primary key"
	}
	var returned string
	err = tx.QueryRowContext(ctx, query, args...).Scan(&returned)
	if message != "" {
		if err == nil || !strings.Contains(err.Error(), message) {
			t.Fatalf("expected final %s failure, got %v", failure, err)
		}
		var ignored int
		err = tx.QueryRowContext(ctx, `SELECT 1`).Scan(&ignored)
		var pgErr *pq.Error
		if !errors.As(err, &pgErr) || pgErr.Code != "25P02" {
			t.Fatalf("failed transaction is not aborted: %v", err)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	got, ok := new(big.Rat).SetString(returned)
	if !ok || got.Cmp(big.NewRat(total, 100)) != 0 {
		t.Fatalf("order total %s, want %d cents", returned, total)
	}
	// Another connection must see only the previously committed model.
	if err := model.verify(ctx, client); err != nil {
		t.Fatalf("uncommitted visibility: %v", err)
	}
	if failure == "pending" {
		keep = true
		return tx
	}
	if failure == "rollback" {
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		return nil
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	model.stock = stock
	model.orders = append(model.orders, []string{commerceNumber(id), reference, commerceRational(total), "paid"})
	model.items = append(model.items, items...)
	audit = append(audit, []string{commerceNumber(id), "0", "paid", commerceRational(total), reference})
	model.audit = append(model.audit, audit...)
	return nil
}

func exerciseCommerceBinaries(t *testing.T, binary func(string) string, evidence *commerceEvidence) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	root := t.TempDir()
	source := filepath.Join(root, "commerce.kitdb")
	server := startApplicationServer(t, ctx, binary("kitdbpg"), source)
	model := &commerceModel{next: 1000, stock: [3]int64{200, 180, 2}}
	commerceSchema(t, ctx, server.client)
	check := func(client *sql.DB, expected *commerceModel) {
		t.Helper()
		if err := expected.verify(ctx, client); err != nil {
			t.Fatal(err)
		}
	}
	for _, ref := range []string{"ORDER-1", "ORDER-2", "EXACT"} {
		commerceOrder(t, ctx, server.client, model, ref, "")
		check(server.client, model)
	}
	evidence.Phases = append(evidence.Phases, "committed-multi-table-orders-exact-money")
	for _, failure := range []string{"domain", "trigger", "returning", "foreign-key", "unique", "rollback"} {
		commerceOrder(t, ctx, server.client, model, "REUSED-REFERENCE", failure)
		check(server.client, model)
		phase := "rollback-" + failure
		if failure == "rollback" {
			phase = "explicit-transaction-rollback"
		}
		evidence.Phases = append(evidence.Phases, phase)
	}
	commerceOrder(t, ctx, server.client, model, "REUSED-REFERENCE", "")
	check(server.client, model)
	evidence.Phases = append(evidence.Phases, "failed-unique-entry-reusable")
	pending := commerceOrder(t, ctx, server.client, model, "CRASH-PENDING", "pending")
	if err := server.command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	var killed *exec.ExitError
	if err := server.command.Wait(); !errors.As(err, &killed) {
		t.Fatalf("server was not forcibly terminated: %v", err)
	}
	server.stopped = true
	_ = server.client.Close()
	_ = pending.Rollback()
	server = startApplicationServer(t, ctx, binary("kitdbpg"), source)
	check(server.client, model)
	evidence.Phases = append(evidence.Phases, "hard-kill-acknowledged-commit-and-pending-audit-recovery")
	server.stop()
	cli := func(args ...string) []byte {
		t.Helper()
		data, err := run(ctx, root, nil, binary("kitdb"), args...)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	cli("verify", source)
	// Exercise the embedded relational SQL entry via the standalone operator,
	// separately from pgwire's decoding/catalog compatibility path.
	data := cli("query", source, `SELECT COUNT(*) FROM orders`)
	var count struct{ Result struct{ Rows [][]int64 } }
	if err := json.Unmarshal(data, &count); err != nil || len(count.Result.Rows) != 1 || len(count.Result.Rows[0]) != 1 || count.Result.Rows[0][0] != int64(len(model.orders)) {
		t.Fatalf("embedded operator count: %s %v", data, err)
	}
	anchor := filepath.Join(root, "anchor.kitdb")
	restored := filepath.Join(root, "restored.kitdb")
	cli("backup", source, anchor)
	cli("restore", anchor, restored)
	backup := startApplicationServer(t, ctx, binary("kitdbpg"), restored)
	check(backup.client, model)
	copyModel := model.clone()
	commerceOrder(t, ctx, backup.client, copyModel, "RESTORED-ORDER", "")
	check(backup.client, copyModel)
	backup.stop()
	cli("verify", restored)
	evidence.Phases = append(evidence.Phases, "verified-backup-independent-writable-restore")
	server = startApplicationServer(t, ctx, binary("kitdbpg"), source)
	check(server.client, model)
	server.stop()
	// Capture before the later destructive session, not from a database that
	// was already changed. The anchor and retained history are independent.
	recoveryTime := time.Now().UTC().Format(time.RFC3339Nano)
	server = startApplicationServer(t, ctx, binary("kitdbpg"), source)
	commerceExec(t, ctx, server.client, `UPDATE products SET stock=0`)
	commerceExec(t, ctx, server.client, `DELETE FROM order_items`)
	commerceExec(t, ctx, server.client, `DROP TRIGGER orders_changed ON orders`)
	commerceExec(t, ctx, server.client, `CREATE OR REPLACE FUNCTION line_amount(price NUMERIC,quantity INTEGER) RETURNS NUMERIC LANGUAGE SQL RETURN 0`)
	if err := model.verify(ctx, server.client); err == nil || !strings.Contains(err.Error(), "independent commerce model mismatch") {
		t.Fatalf("oracle accepted deliberately damaged data: %v", err)
	}
	server.stop()
	evidence.Phases = append(evidence.Phases, "negative-oracle-detects-data-damage")
	recovered := filepath.Join(root, "recovered.kitdb")
	cli("restore-time", "--at", recoveryTime, source, recovered)
	recovery := startApplicationServer(t, ctx, binary("kitdbpg"), recovered)
	check(recovery.client, model)
	recoveryModel := model.clone()
	commerceOrder(t, ctx, recovery.client, recoveryModel, "RECOVERED-ORDER", "")
	check(recovery.client, recoveryModel)
	commerceOrder(t, ctx, recovery.client, recoveryModel, "RESTORED-DOMAIN", "domain")
	check(recovery.client, recoveryModel)
	recovery.stop()
	cli("verify", recovered)
	server = startApplicationServer(t, ctx, binary("kitdbpg"), source)
	if err := model.verify(ctx, server.client); err == nil || !strings.Contains(err.Error(), "independent commerce model mismatch") {
		t.Fatalf("damaged source was rewritten or could not be checked: %v", err)
	}
	server.stop()
	evidence.Phases = append(evidence.Phases, "pitr-restores-data-function-trigger-domain-sequence")
	t.Logf("commerce drill: %d committed orders, %d lines, %d audit events; rollback, hard-kill, native verify, backup and PITR passed", len(model.orders), len(model.items), len(model.audit))
}
