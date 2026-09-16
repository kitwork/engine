package database

import (
	"database/sql"
	"testing"
)

func TestOwnedDatabaseRegistrationIsScopedToOwner(t *testing.T) {
	connection := &sql.DB{}
	unregister, err := RegisterOwned("shop", connection)
	if err != nil {
		t.Fatal(err)
	}
	if got := LookupOwned("shop"); got != connection {
		t.Fatalf("lookup = %p, want %p", got, connection)
	}
	if _, err := RegisterOwned("shop", &sql.DB{}); err == nil {
		t.Fatal("duplicate owned alias was accepted")
	}
	unregister()
	unregister()
	if got := LookupOwned("shop"); got != nil {
		t.Fatalf("lookup after unregister = %p", got)
	}
}

func TestOwnedDatabaseResolverIsLazyScopedAndAmbiguousSafe(t *testing.T) {
	shop := &sql.DB{}
	calls := 0
	unregister, err := RegisterOwnedResolver(func(alias string) (*sql.DB, bool, error) {
		calls++
		return shop, alias == "shop", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(unregister)
	if calls != 0 {
		t.Fatal("resolver ran during registration")
	}
	if got, err := ResolveOwned("shop"); err != nil || got != shop {
		t.Fatalf("resolved shop = %p, %v", got, err)
	}
	if calls != 1 {
		t.Fatalf("resolver calls = %d", calls)
	}

	second := &sql.DB{}
	unregisterSecond, err := RegisterOwnedResolver(func(alias string) (*sql.DB, bool, error) {
		return second, alias == "shop", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveOwned("shop"); err == nil {
		t.Fatal("ambiguous managed roots were accepted")
	}
	unregisterSecond()
	unregister()
	if got, err := ResolveOwned("shop"); err != nil || got != nil {
		t.Fatalf("resolver remained after unregister: %p, %v", got, err)
	}
}
