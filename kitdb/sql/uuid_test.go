package sql

import "testing"

func TestCanonicalUUIDMatchesPostgreSQLInputProfile(t *testing.T) {
	want := "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"
	for _, source := range []string{
		want,
		"A0EEBC99-9C0B-4EF8-BB6D-6BB9BD380A11",
		"{a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11}",
		"a0eebc999c0b4ef8bb6d6bb9bd380a11",
		"a0ee-bc99-9c0b-4ef8-bb6d-6bb9-bd38-0a11",
		"{a0eebc99-9c0b4ef8-bb6d6bb9-bd380a11}",
	} {
		if got, err := CanonicalUUID(source); err != nil || got != want {
			t.Fatalf("CanonicalUUID(%q) = %q, %v; want %q", source, got, err, want)
		}
	}
}

func TestCanonicalUUIDRejectsMalformedValues(t *testing.T) {
	for _, source := range []string{
		"", "not-a-uuid", " a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11",
		"{a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", "a0eeb-c999c0b4ef8bb6d6bb9bd380a11",
		"a0eebc99--9c0b-4ef8-bb6d-6bb9bd380a11", "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a1100",
	} {
		if got, err := CanonicalUUID(source); err == nil {
			t.Fatalf("CanonicalUUID(%q) = %q, want error", source, got)
		}
	}
}

func TestParseAndFormatUUIDRoundTripAllBits(t *testing.T) {
	source := "00000000-0000-0000-0000-000000000000"
	value, err := ParseUUID(source)
	if err != nil || FormatUUID(value) != source {
		t.Fatalf("round trip = %q, %v", FormatUUID(value), err)
	}
}
