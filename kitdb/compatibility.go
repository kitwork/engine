package kitdb

// ReleaseTarget is the semantic-version line being qualified. Stability stays
// release-candidate until the cross-platform release gate and canary evidence
// for the same commit have both been reviewed.
const (
	ReleaseTarget = "1.0.0"
	Stability     = "release-candidate"

	CompatibilityContract = "kitdb/1"
)

// VersionRange describes an inclusive readable format range.
type VersionRange struct {
	Minimum uint16 `json:"minimum"`
	Maximum uint16 `json:"maximum"`
}

// DurableCompatibility records the durable encodings owned by the kernel.
// It is release evidence, not a negotiation mechanism.
type DurableCompatibility struct {
	MainFileRead     VersionRange `json:"main_file_read"`
	MainFileWrite    uint16       `json:"main_file_write"`
	WAL              uint16       `json:"wal"`
	TransactionFrame uint16       `json:"transaction_frame"`
	History          uint16       `json:"history"`
	HistoryPins      uint16       `json:"history_pins"`
	ReplicaProtocol  uint16       `json:"replica_protocol"`
	ReplicaWire      uint16       `json:"replica_wire"`
}

// KernelLimits records the hard transaction boundaries that applications may
// rely on for the 1.x compatibility line.
type KernelLimits struct {
	KeyBytes              int `json:"key_bytes"`
	ValueBytes            int `json:"value_bytes"`
	TransactionBytes      int `json:"transaction_bytes"`
	TransactionOperations int `json:"transaction_operations"`
}

// CompatibilityProfile is the machine-readable KitDB 1.x kernel contract.
// Additive fields may be introduced, but changing an existing value requires
// an explicit compatibility decision and updated release evidence.
type CompatibilityProfile struct {
	Contract      string               `json:"contract"`
	ReleaseTarget string               `json:"release_target"`
	Stability     string               `json:"stability"`
	Durable       DurableCompatibility `json:"durable"`
	Limits        KernelLimits         `json:"limits"`
}

// CurrentCompatibility returns the compatibility profile compiled into this
// engine. The returned value owns no mutable state.
func CurrentCompatibility() CompatibilityProfile {
	return CompatibilityProfile{
		Contract:      CompatibilityContract,
		ReleaseTarget: ReleaseTarget,
		Stability:     Stability,
		Durable: DurableCompatibility{
			MainFileRead: VersionRange{
				Minimum: mainLegacyFormatVersion,
				Maximum: mainFormatVersion,
			},
			MainFileWrite:    mainFormatVersion,
			WAL:              walFormatVersion,
			TransactionFrame: frameFormatVersion,
			History:          historyFormatVersion,
			HistoryPins:      historyPinsFormatVersion,
			ReplicaProtocol:  ReplicaProtocolVersion,
			ReplicaWire:      ReplicaWireVersion,
		},
		Limits: KernelLimits{
			KeyBytes:              maxKeySize,
			ValueBytes:            maxValueSize,
			TransactionBytes:      maxPayloadSize,
			TransactionOperations: maxOperations,
		},
	}
}
