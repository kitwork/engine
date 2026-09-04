package kitdb

import (
	"encoding/json"
	"testing"
)

func TestKitDBV1CompatibilityProfile(t *testing.T) {
	profile := CurrentCompatibility()
	if profile.Contract != "kitdb/1" ||
		profile.ReleaseTarget != "1.0.0" ||
		profile.Stability != "release-candidate" {
		t.Fatalf("release identity = %#v", profile)
	}
	if profile.Durable.MainFileRead != (VersionRange{Minimum: 1, Maximum: 3}) ||
		profile.Durable.MainFileWrite != 3 ||
		profile.Durable.WAL != 1 ||
		profile.Durable.TransactionFrame != 1 ||
		profile.Durable.History != 1 ||
		profile.Durable.HistoryPins != 1 ||
		profile.Durable.ReplicaProtocol != 1 ||
		profile.Durable.ReplicaWire != 1 {
		t.Fatalf("durable compatibility = %#v", profile.Durable)
	}
	if profile.Limits != (KernelLimits{
		KeyBytes: 64 << 10, ValueBytes: 32 << 20,
		TransactionBytes: 64 << 20, TransactionOperations: 1 << 18,
	}) {
		t.Fatalf("kernel limits = %#v", profile.Limits)
	}
	encoded, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	var decoded CompatibilityProfile
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded != profile {
		t.Fatalf("JSON round trip = %#v, want %#v", decoded, profile)
	}
}
