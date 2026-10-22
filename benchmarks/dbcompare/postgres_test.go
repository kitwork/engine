package main

import "testing"

func TestPostgresBenchmarkRefusesUnsafeTargets(t *testing.T) {
	for _, source := range []string{
		"", "host=127.0.0.1 dbname=kitdb_benchmark_test",
		"postgres://user@192.0.2.1/kitdb_benchmark_test",
		"postgres://user@127.0.0.1/postgres", "postgres://user@localhost/kitdb_benchmark_",
		"postgres://user@localhost/kitdb_benchmark_test?host=remote",
		"postgres://user@localhost/kitdb_benchmark_test?options=-csearch_path=public",
		"postgres://user@localhost/kitdb_benchmark_test?sslmode=disable&sslmode=require",
		"postgres://user@localhost/kitdb_benchmark_test?bad=%zz",
		"postgres://user@localhost/kitdb_benchmark_test#fragment",
		"postgres://user@localhost/kitdb_benchmark_test/other",
	} {
		if err := validatePostgresDSN(source); err == nil {
			t.Fatalf("accepted unsafe target %q", source)
		}
	}
	for _, source := range []string{
		"postgres://user:password@127.0.0.1:55437/kitdb_benchmark_test?sslmode=disable&connect_timeout=5",
		"postgresql://user@[::1]:55437/kitdb_benchmark_test",
	} {
		if err := validatePostgresDSN(source); err != nil {
			t.Fatal(err)
		}
	}
}
