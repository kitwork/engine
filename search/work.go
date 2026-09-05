package search

// Work hooks are compiled away unless the diagnostic searchwork tag is set.
type workEvent uint8

const (
	workPostingLists workEvent = iota
	workAdvanceCalls
	workAdvanceCurrent
	workAdvanceInBlock
	workNextCalls
	workHeaders
	workDecodedBlocks
	workDecodedPostings
	workDecodedPayloadBytes
	workSkippedTargetBlocks
	workSkippedScoreBlocks
	workReadRequests
	workRequestedBytes
	workReadAheadHits
	workReaderCalls
	workReaderBytes
	workUnionAdvances
	workMultiCandidates
	workMultiTermScores
	workMultiFieldScores
	workMultiMismatches
	workMultiMismatchDistance
	workMultiMatches
	workMultiRankRejected
	workMultiPrefixRejected
	workMultiCollected
	workPositionVarints
	workVarintWidth1
	workEventCount = workVarintWidth1 + 10
)
