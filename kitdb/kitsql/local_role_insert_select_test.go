package kitsql_test

import (
	"database/sql"
	"testing"
)

func TestLocalRoleInsertSelectAcrossAdapters(t *testing.T) {
	ctx, admin, open := localRoleWriteFixture(t)
	run := func(q string) {
		t.Helper()
		if _, err := admin.ExecContext(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	run("CREATE TABLE drafts(id BIGINT PRIMARY KEY,title TEXT)")
	run("INSERT INTO drafts VALUES(3,'draft-a'),(4,'draft-b')")
	run("CREATE ROLE publisher")
	run("GRANT ROLE publisher ON DATABASE shop TO writer")
	for _, adapter := range []string{"native", "pg", "http"} {
		t.Run(adapter, func(t *testing.T) {
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
					t.Fatal("unexpected permission", q)
				}
			}
			exec := func(q string, args ...any) {
				t.Helper()
				if _, err := guest.ExecContext(ctx, q, args...); err != nil {
					t.Fatal(q, err)
				}
			}
			count := func(q string, want int) {
				t.Helper()
				var n int
				if err := admin.QueryRowContext(ctx, q).Scan(&n); err != nil || n != want {
					t.Fatal(q, n, want, err)
				}
			}
			denied("INSERT INTO articles SELECT id,title FROM drafts WHERE id=3")
			run("GRANT SELECT ON TABLE drafts TO ROLE publisher")
			denied("INSERT INTO articles SELECT id,title FROM drafts WHERE id=3")
			run("GRANT INSERT ON TABLE articles TO ROLE publisher")
			exec("INSERT INTO articles(id,title) SELECT id,title FROM drafts WHERE id=$1", 3)
			count("SELECT COUNT(*) FROM articles WHERE id=3", 1)
			// INSERT-only target permission cannot read either existing or returned rows.
			denied("SELECT * FROM articles")
			denied("INSERT INTO articles SELECT id+10,title FROM drafts WHERE id=4 RETURNING title")
			count("SELECT COUNT(*) FROM articles WHERE id=14", 0)
			run("GRANT SELECT ON TABLE articles TO ROLE publisher")
			var title string
			if err := guest.QueryRowContext(ctx, "INSERT INTO articles SELECT id+10,title FROM drafts WHERE id=$1 RETURNING title", 4).Scan(&title); err != nil || title != "draft-b" {
				t.Fatal("returning", title, err)
			}
			for _, q := range []string{
				"INSERT INTO articles SELECT id,'private' FROM secret WHERE id=999",
				"INSERT INTO articles SELECT d.id,d.title FROM drafts d JOIN secret s ON d.id=s.id",
				"INSERT INTO articles SELECT id,title FROM drafts UNION ALL SELECT id,'private' FROM secret",
				"INSERT INTO articles SELECT id,upper(title) FROM drafts",
				"INSERT INTO articles SELECT id,title FROM (SELECT id,title FROM drafts) x",
				"INSERT INTO articles SELECT id,title FROM drafts ON CONFLICT(id) DO UPDATE SET title=upper(excluded.title)",
				"INSERT INTO articles SELECT 10,'no-source'",
			} {
				denied(q)
			}
			// Constraint failure after an earlier source row must publish nothing.
			denied("INSERT INTO articles SELECT id,title FROM drafts ORDER BY id DESC")
			count("SELECT COUNT(*) FROM articles WHERE id=4", 0)
			run("REVOKE SELECT ON TABLE drafts FROM ROLE publisher")
			denied("INSERT INTO articles SELECT id,title FROM drafts WHERE id=999")
			run("GRANT SELECT ON TABLE drafts TO ROLE publisher")
			run("REVOKE INSERT ON TABLE articles FROM ROLE publisher")
			denied("INSERT INTO articles SELECT id+20,title FROM drafts WHERE id=4")
			run("GRANT INSERT ON TABLE articles TO ROLE publisher")
			if adapter == "pg" {
				stmt, err := guest.PrepareContext(ctx, "INSERT INTO articles SELECT id+20,title FROM drafts WHERE id=$1")
				if err != nil {
					t.Fatal(err)
				}
				run("REVOKE SELECT ON TABLE drafts FROM ROLE publisher")
				if _, err = stmt.ExecContext(ctx, 4); err == nil {
					t.Fatal("prepared source revoke bypass")
				}
				run("GRANT SELECT ON TABLE drafts TO ROLE publisher")
				if _, err = stmt.ExecContext(ctx, 4); err != nil {
					t.Fatal(err)
				}
				run("REVOKE INSERT ON TABLE articles FROM ROLE publisher")
				if _, err = stmt.ExecContext(ctx, 3); err == nil {
					t.Fatal("prepared target revoke bypass")
				}
				run("GRANT INSERT ON TABLE articles TO ROLE publisher")
				if _, err = stmt.ExecContext(ctx, 3); err != nil {
					t.Fatal("prepared target regrant", err)
				}
				stmt.Close()
			}
			if adapter != "http" {
				tx, err := guest.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				run("REVOKE SELECT ON TABLE drafts FROM ROLE publisher")
				if _, err = tx.ExecContext(ctx, "INSERT INTO articles SELECT id+30,title FROM drafts WHERE id=4"); err != nil {
					tx.Rollback()
					t.Fatal("pinned source ACL", err)
				}
				if err = tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				count("SELECT COUNT(*) FROM articles WHERE id=34", 0)
				denied("INSERT INTO articles SELECT id+30,title FROM drafts WHERE id=4")
				run("GRANT SELECT ON TABLE drafts TO ROLE publisher")
				tx, err = guest.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				if _, err = tx.ExecContext(ctx, "INSERT INTO articles SELECT id+30,title FROM drafts"); err == nil {
					tx.Rollback()
					t.Fatal("readonly insert-select")
				}
				tx.Rollback()
			}
			// Same-table reads use the original bounded statement source, not newly added rows.
			exec("INSERT INTO articles SELECT id+100,title FROM articles WHERE id=3")
			count("SELECT COUNT(*) FROM articles WHERE id=103", 1)
			run("DELETE FROM articles WHERE id>=3")
			run("REVOKE SELECT ON TABLE drafts FROM ROLE publisher")
			run("REVOKE SELECT ON TABLE articles FROM ROLE publisher")
			run("REVOKE INSERT ON TABLE articles FROM ROLE publisher")
		})
	}
}
