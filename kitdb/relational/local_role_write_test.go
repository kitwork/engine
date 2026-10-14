package relational

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/kitwork/engine/kitdb/pgwire"
)

type localRoleBytes []byte

func (data localRoleBytes) Get([]byte) ([]byte, bool, error) { return data, true, nil }

func TestLocalRoleWriteCatalogVersion(t *testing.T) {
	const id = "0123456789abcdef0123456789abcdef"
	for _, version := range []int{1, 2} {
		c := localRoleCatalog{Version: version, Roles: []localRole{{ID: id, Name: "editor", Select: []string{id}}}}
		b, _ := json.Marshal(c)
		if _, err := readLocalRoles(localRoleBytes(b)); err != nil {
			t.Fatal(err)
		}
		c.Roles[0].Insert = []string{id}
		b, _ = json.Marshal(c)
		_, err := readLocalRoles(localRoleBytes(b))
		if (err == nil) != (version == 2) {
			t.Fatalf("version %d write acceptance: %v", version, err)
		}
	}
	for _, role := range []localRole{
		{ID: id, Name: "editor", Insert: []string{id, id}},
		{ID: id, Name: "editor", Update: []string{"bad"}},
		{ID: id, Name: "editor", Delete: make([]string, 4097)},
	} {
		b, _ := json.Marshal(localRoleCatalog{Version: 2, Roles: []localRole{role}})
		if _, err := readLocalRoles(localRoleBytes(b)); err == nil {
			t.Fatal("invalid write catalog accepted")
		}
	}
}

func TestLocalRoleWriteConcurrentRevoke(t *testing.T) {
	_, admin, reader := roleCatalogFixture(t)
	if _, err := admin.Exec("GRANT INSERT ON TABLE articles TO ROLE article_reader"); err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	errorsSeen := make(chan error, 40)
	for worker := 0; worker < 2; worker++ {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			for i := 0; i < 20; i++ {
				_, err := reader.Exec("INSERT INTO articles VALUES($1,'concurrent')", 100+worker*20+i)
				var wire *pgwire.Error
				if err != nil && !errors.Is(err, ErrTransactionConflict) && !(errors.As(err, &wire) && wire.Code == "42501") {
					errorsSeen <- err
				}
			}
		}(worker)
	}
	for i := 0; i < 20; i++ {
		for _, q := range []string{"REVOKE INSERT ON TABLE articles FROM ROLE article_reader", "GRANT INSERT ON TABLE articles TO ROLE article_reader"} {
			if _, err := admin.Exec(q); err != nil {
				t.Error(err)
			}
		}
	}
	workers.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		t.Error(err)
	}
	if _, err := admin.Exec("REVOKE INSERT ON TABLE articles FROM ROLE article_reader"); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Exec("INSERT INTO articles VALUES(999,'after')"); err == nil {
		t.Fatal("write allowed after final revoke")
	}
}

func TestLocalRoleWritePinnedSnapshot(t *testing.T) {
	_, admin, reader := roleCatalogFixture(t)
	if _, err := admin.Exec("GRANT INSERT ON TABLE articles TO ROLE article_reader"); err != nil {
		t.Fatal(err)
	}
	tx, err := reader.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = admin.Exec("REVOKE INSERT ON TABLE articles FROM ROLE article_reader"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("INSERT INTO articles VALUES(2,'pinned')"); err != nil {
		t.Fatal("pinned grant not honored", err)
	}
	var allowed bool
	if err = tx.QueryRow("SELECT can_insert FROM kitdb_catalog.effective_privileges WHERE policy_scope='session' AND table_name='articles'").Scan(&allowed); err != nil || !allowed {
		t.Fatal("pinned view", allowed, err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err = reader.Exec("INSERT INTO articles VALUES(2,'fresh')"); err == nil {
		t.Fatal("fresh snapshot ignored revoke")
	}
	var n int
	if err = admin.QueryRow("SELECT COUNT(*) FROM articles WHERE id=2").Scan(&n); err != nil || n != 0 {
		t.Fatal("rollback or denied mutation changed data", n, err)
	}
}

func TestLocalRoleWriteNodeReadOnly(t *testing.T) {
	node, admin, reader := roleCatalogFixture(t)
	if _, err := admin.Exec("GRANT INSERT ON TABLE articles TO ROLE article_reader"); err != nil {
		t.Fatal(err)
	}
	reader.Close()
	admin.Close()
	if err := node.Close(); err != nil {
		t.Fatal(err)
	}
	fresh, err := OpenPostgresNode(PostgresNodeOptions{Root: node.root, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	db, err := fresh.OpenAuthenticatedDatabase(context.Background(), "reader", "session-test-password", "shop")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec("INSERT INTO articles VALUES(2,'readonly')"); err == nil {
		t.Fatal("node readonly bypass")
	}
	var allowed bool
	if err = db.QueryRow("SELECT can_insert FROM kitdb_catalog.effective_privileges WHERE policy_scope='session' AND table_name='articles'").Scan(&allowed); err != nil || allowed {
		t.Fatal("readonly view", allowed, err)
	}
}
