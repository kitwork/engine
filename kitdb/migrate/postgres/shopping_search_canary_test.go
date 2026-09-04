package postgres

import (
	"errors"
	"net"
	"testing"
	"time"
)

func TestNormalizeShoppingSearchCanaryConfigBoundsRemotePage(t *testing.T) {
	_, err := normalizeShoppingSearchCanaryConfig(ShoppingSearchCanaryConfig{
		SourceURL: "postgres://source", TargetURL: "postgres://target",
		TargetDatabase: "shopping", SearchRoot: t.TempDir(), PageRows: 10_001,
	})
	if err == nil {
		t.Fatal("expected page bound failure")
	}
}

func TestTransientMigrationErrorRecognizesNetworkFailures(t *testing.T) {
	err := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("network unreachable")}
	if !isTransientMigrationTargetError(err) {
		t.Fatal("network error was not classified as transient")
	}
}

func TestNormalizeShoppingSearchCanaryConfigUsesBoundedFullScaleSegments(t *testing.T) {
	config, err := normalizeShoppingSearchCanaryConfig(ShoppingSearchCanaryConfig{
		SourceURL: "postgres://source", TargetURL: "postgres://target",
		TargetDatabase: "shopping", SearchRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.SegmentRows != defaultSearchSegmentRows || config.SegmentBytes != defaultSearchSegmentBytes {
		t.Fatalf("unexpected segment bounds: rows=%d bytes=%d", config.SegmentRows, config.SegmentBytes)
	}
	if config.SourceConnectionLifetime != defaultSourceConnLifetime {
		t.Fatalf("source connection lifetime = %s", config.SourceConnectionLifetime)
	}
	if config.LocalSearchBatch != localSearchTimingBatch {
		t.Fatalf("local search batch = %d", config.LocalSearchBatch)
	}
}

func TestNormalizeShoppingSearchCanaryConfigBoundsSourceConnectionLifetime(t *testing.T) {
	_, err := normalizeShoppingSearchCanaryConfig(ShoppingSearchCanaryConfig{
		SourceURL: "postgres://source", TargetURL: "postgres://target",
		TargetDatabase: "shopping", SearchRoot: t.TempDir(),
		SourceConnectionLifetime: time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected source connection lifetime bound failure")
	}
}

func TestNormalizeShoppingSearchCanaryConfigRequiresFullProjectionForReuse(t *testing.T) {
	_, err := normalizeShoppingSearchCanaryConfig(ShoppingSearchCanaryConfig{
		SourceURL: "postgres://source", TargetURL: "postgres://target",
		TargetDatabase: "shopping", SearchRoot: t.TempDir(),
		ReuseIndex: true, MaxRows: 100,
	})
	if err == nil {
		t.Fatal("expected reused partial projection rejection")
	}
}

func TestNormalizeShoppingSearchCanaryConfigRejectsUnboundedSegment(t *testing.T) {
	_, err := normalizeShoppingSearchCanaryConfig(ShoppingSearchCanaryConfig{
		SourceURL: "postgres://source", TargetURL: "postgres://target",
		TargetDatabase: "shopping", SearchRoot: t.TempDir(),
		SegmentBytes: maximumSearchSegmentBytes + 1,
	})
	if err == nil {
		t.Fatal("expected segment byte bound failure")
	}
}

func TestShoppingSearchSchemaKeepsLegacyAndExpandedFields(t *testing.T) {
	schema, err := ShoppingSearchSchema()
	if err != nil {
		t.Fatal(err)
	}
	fields := schema.Fields()
	if len(fields) != 4 || fields[0].Name != "name" || fields[1].Name != "description" ||
		fields[2].Name != "content" || fields[3].Name != "brand" {
		t.Fatalf("unexpected search fields: %#v", fields)
	}
	if fields[0].Boost != 5 || fields[1].Boost != 2 || fields[2].Boost != 1 || fields[3].Boost != 2 {
		t.Fatalf("unexpected field boosts: %#v", fields)
	}
}

func TestPercentileDurationUsesNearestRank(t *testing.T) {
	durations := []time.Duration{time.Microsecond, 2 * time.Microsecond, 3 * time.Microsecond, 4 * time.Microsecond}
	if got := percentileDuration(durations, 50); got != 2*time.Microsecond {
		t.Fatalf("p50 = %s", got)
	}
	if got := percentileDuration(durations, 95); got != 4*time.Microsecond {
		t.Fatalf("p95 = %s", got)
	}
}
