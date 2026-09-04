package postgres

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNormalizeConfigBounds(t *testing.T) {
	normalized, err := normalizeConfig(Config{URL: "postgres://example"})
	if err != nil {
		t.Fatalf("normalize config: %v", err)
	}
	if normalized.Schema != defaultSchema || normalized.Timeout != defaultTimeout || normalized.MaxTables != defaultMaxTables {
		t.Fatalf("unexpected defaults: %+v", normalized)
	}
	if _, err := normalizeConfig(Config{URL: "postgres://example", Timeout: time.Millisecond}); err == nil {
		t.Fatal("expected timeout bound failure")
	}
	if _, err := normalizeConfig(Config{URL: "postgres://example", MaxTables: maxListedTables + 1}); err == nil {
		t.Fatal("expected table-list bound failure")
	}
}

func TestEvaluateReportFailsClosed(t *testing.T) {
	report := Report{
		Role: RoleReport{DefaultReadOnly: true, TransactionReadOnly: true},
		Scope: ScopeReport{
			Exists: true, Usage: true, Tables: 2, ReadableTables: 2,
			Sequences: 1, ReadableSequences: 1,
		},
	}
	evaluateReport(&report)
	if !report.Ready {
		t.Fatalf("expected ready report: %+v", report)
	}

	report.Scope.RowSecurityTables = 1
	evaluateReport(&report)
	if report.Ready {
		t.Fatal("RLS scope must require review")
	}
}

func TestEvaluateReportRejectsPersistentCreatePrivilegeAndEmptyScope(t *testing.T) {
	report := Report{
		Role:  RoleReport{DefaultReadOnly: true, TransactionReadOnly: true},
		Scope: ScopeReport{Exists: true, Usage: true, Create: true},
	}
	evaluateReport(&report)
	if report.Ready || report.LeastPrivilegeRole {
		t.Fatalf("create-capable empty source must fail closed: %+v", report)
	}
}

func TestSourceErrorRedactsPasswordAndDSN(t *testing.T) {
	dsn := "postgresql://reader:very-secret@example.test/app"
	err := sourceError(dsn, "connect", errors.New("failed "+dsn+" very-secret"))
	if strings.Contains(err.Error(), dsn) || strings.Contains(err.Error(), "very-secret") {
		t.Fatalf("secret leaked through error: %v", err)
	}
}
