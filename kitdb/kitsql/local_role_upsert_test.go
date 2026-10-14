package kitsql_test

import (
	"database/sql"
	"testing"
)

func TestLocalRoleUpsertAcrossAdapters(t *testing.T) {
	ctx, admin, open := localRoleWriteFixture(t)
	run := func(q string) {
		t.Helper()
		if _, err := admin.ExecContext(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	run("CREATE TABLE items(id BIGINT PRIMARY KEY,label TEXT UNIQUE,n BIGINT CHECK(n>=0))")
	run("CREATE TABLE incoming(id BIGINT PRIMARY KEY,label TEXT,n BIGINT)")
	run("INSERT INTO incoming VALUES(5,'E',50)")
	run("CREATE ROLE publisher")
	run("GRANT ROLE publisher ON DATABASE shop TO writer")
	run("CREATE TABLE composite(tenant BIGINT,id BIGINT,label TEXT,PRIMARY KEY(tenant,id),UNIQUE(tenant,label))")
	run("GRANT INSERT ON TABLE composite TO ROLE publisher")
	run("GRANT UPDATE ON TABLE composite TO ROLE publisher")
	for _, adapter := range []string{"native", "pg", "http"} {
		t.Run(adapter, func(t *testing.T) {
			run("INSERT INTO items VALUES(1,'A',10)")
			guest := open(adapter, "writer")
			defer guest.Close()
			denied := func(q string, args ...any) {
				t.Helper()
				rows, err := guest.QueryContext(ctx, q, args...)
				if rows != nil {
					for rows.Next() {
					}
					if rows.Err() != nil {
						err = rows.Err()
					}
					rows.Close()
				}
				if err == nil {
					t.Fatal("allowed", q)
				}
			}
			exec := func(q string, args ...any) {
				t.Helper()
				if _, err := guest.ExecContext(ctx, q, args...); err != nil {
					t.Fatal(q, err)
				}
			}
			check := func(q string, want int) {
				t.Helper()
				var n int
				if err := admin.QueryRowContext(ctx, q).Scan(&n); err != nil || n != want {
					t.Fatal(q, n, want, err)
				}
			}
			denied("INSERT INTO items VALUES(1,'A',0) ON CONFLICT(id) DO NOTHING")
			run("GRANT INSERT ON TABLE items TO ROLE publisher")
			exec("INSERT INTO items VALUES(1,'A',0) ON CONFLICT(id) DO NOTHING")
			exec("INSERT INTO items VALUES(2,'B',20) ON CONFLICT DO NOTHING")
			check("SELECT n FROM items WHERE id=1", 10)
			denied("SELECT * FROM items")
			denied("INSERT INTO items VALUES(3,'C',30) ON CONFLICT(id) DO UPDATE SET n=excluded.n")
			check("SELECT COUNT(*) FROM items WHERE id=3", 0)
			run("GRANT UPDATE ON TABLE items TO ROLE publisher")
			exec("INSERT INTO items VALUES(1,'A',12) ON CONFLICT(id) DO UPDATE SET n=EXCLUDED.n")
			exec("INSERT INTO items VALUES(9,'B',25) ON CONFLICT(label) DO UPDATE SET n=excluded.n")
			check("SELECT n FROM items WHERE id=1", 12)
			check("SELECT n FROM items WHERE id=2", 25)
			exec("INSERT INTO items VALUES(3,'C',30) ON CONFLICT(id) DO UPDATE SET n=excluded.n")
			denied("INSERT INTO items VALUES(1,'A',1) ON CONFLICT(id) DO UPDATE SET n=items.n+excluded.n")
			denied("INSERT INTO items VALUES(4,'D',40) ON CONFLICT(id) DO UPDATE SET n=items.n+excluded.n")
			denied("INSERT INTO items VALUES(4,'D',40) ON CONFLICT(id) DO UPDATE SET n=excluded.n WHERE excluded.n>0")
			denied("INSERT INTO items VALUES(4,'D',40) ON CONFLICT(id) DO NOTHING RETURNING n")
			check("SELECT COUNT(*) FROM items WHERE id=4", 0)
			run("GRANT SELECT ON TABLE items TO ROLE publisher")
			var n int
			if err := guest.QueryRowContext(ctx, "INSERT INTO items VALUES(1,'A',2) ON CONFLICT(id) DO UPDATE SET n=items.n+excluded.n RETURNING n").Scan(&n); err != nil || n != 14 {
				t.Fatal("returning", n, err)
			}
			exec("INSERT INTO items VALUES(1,'A',3) ON CONFLICT(id) DO UPDATE SET n=excluded.n WHERE items.n<0")
			check("SELECT n FROM items WHERE id=1", 14)
			// No branch may read/execute a function under the restricted profile.
			for _, q := range []string{
				"INSERT INTO items VALUES(4,'D',40) ON CONFLICT(id) DO UPDATE SET n=abs(excluded.n)",
				"INSERT INTO items VALUES(1,'A',1) ON CONFLICT(id) DO UPDATE SET n=other.n",
				"INSERT INTO items VALUES(4,'D',40) ON CONFLICT(n) DO NOTHING",
				"INSERT INTO articles VALUES(9,'private') ON CONFLICT(id) DO NOTHING",
			} {
				denied(q)
			}
			// A later unique/check failure must undo preceding upsert updates/inserts.
			denied("INSERT INTO items VALUES(1,'A',99),(4,'B',40) ON CONFLICT(id) DO UPDATE SET n=excluded.n")
			check("SELECT n FROM items WHERE id=1", 14)
			check("SELECT COUNT(*) FROM items WHERE id=4", 0)
			denied("INSERT INTO items VALUES(1,'A',99),(4,'D',-1) ON CONFLICT(id) DO UPDATE SET n=excluded.n")
			check("SELECT n FROM items WHERE id=1", 14)
			denied("INSERT INTO items SELECT id,label,n FROM incoming ON CONFLICT(id) DO UPDATE SET n=excluded.n")
			run("GRANT SELECT ON TABLE incoming TO ROLE publisher")
			exec("INSERT INTO items SELECT id,label,n FROM incoming ON CONFLICT(id) DO UPDATE SET n=excluded.n")
			check("SELECT n FROM items WHERE id=5", 50)
			run("INSERT INTO composite VALUES(1,1,'A')")
			exec("INSERT INTO composite VALUES(1,9,'A') ON CONFLICT(label,tenant) DO UPDATE SET label=excluded.label||'B'")
			exec("INSERT INTO composite VALUES(2,1,'A') ON CONFLICT(tenant,id) DO UPDATE SET label=excluded.label")
			check("SELECT COUNT(*) FROM composite WHERE tenant=1 AND id=1 AND label='AB'", 1)
			denied("SELECT * FROM composite")
			run("DELETE FROM composite WHERE tenant>=1")
			if adapter == "pg" {
				stmt, err := guest.PrepareContext(ctx, "INSERT INTO items VALUES(1,'A',$1) ON CONFLICT(id) DO UPDATE SET n=excluded.n")
				if err != nil {
					t.Fatal(err)
				}
				run("REVOKE UPDATE ON TABLE items FROM ROLE publisher")
				if _, err = stmt.ExecContext(ctx, 77); err == nil {
					t.Fatal("prepared UPDATE revoke bypass")
				}
				run("GRANT UPDATE ON TABLE items TO ROLE publisher")
				if _, err = stmt.ExecContext(ctx, 15); err != nil {
					t.Fatal(err)
				}
				run("REVOKE INSERT ON TABLE items FROM ROLE publisher")
				if _, err = stmt.ExecContext(ctx, 77); err == nil {
					t.Fatal("prepared INSERT revoke bypass")
				}
				run("GRANT INSERT ON TABLE items TO ROLE publisher")
				stmt.Close()
			}
			if adapter != "http" {
				tx, err := guest.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				run("REVOKE UPDATE ON TABLE items FROM ROLE publisher")
				if _, err = tx.ExecContext(ctx, "INSERT INTO items VALUES(1,'A',88) ON CONFLICT(id) DO UPDATE SET n=excluded.n"); err != nil {
					tx.Rollback()
					t.Fatal("pinned UPDATE ACL", err)
				}
				tx.Rollback()
				denied("INSERT INTO items VALUES(1,'A',88) ON CONFLICT(id) DO UPDATE SET n=excluded.n")
				run("GRANT UPDATE ON TABLE items TO ROLE publisher")
				tx, err = guest.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				if _, err = tx.ExecContext(ctx, "INSERT INTO items VALUES(1,'A',88) ON CONFLICT(id) DO NOTHING"); err == nil {
					tx.Rollback()
					t.Fatal("readonly upsert admitted")
				}
				tx.Rollback()
			}
			run("DELETE FROM items WHERE id>=1")
			for _, p := range []string{"INSERT", "UPDATE", "SELECT"} {
				run("REVOKE " + p + " ON TABLE items FROM ROLE publisher")
			}
			run("REVOKE SELECT ON TABLE incoming FROM ROLE publisher")
		})
	}
}
