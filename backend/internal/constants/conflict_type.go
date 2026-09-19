package constants

// ConflictType is the stable vocabulary shared by geometry services and clients.
type ConflictType string

const (
	ConflictOverlap          ConflictType = "overlap"
	ConflictGap              ConflictType = "gap"
	ConflictSelfIntersection ConflictType = "self_intersection"
	ConflictDanglingEdge     ConflictType = "dangling_edge"
)

const (
	ConflictDetected           = "detected"
	ConflictConfirmed          = "confirmed"
	ConflictFalsePositive      = "false_positive"
	ConflictResolutionProposed = "resolution_proposed"
	ConflictResolved           = "resolved"
	ConflictClosed             = "closed"
)

var conflictTransitions = map[string]map[string]bool{
	ConflictDetected:           {ConflictConfirmed: true, ConflictFalsePositive: true},
	ConflictConfirmed:          {ConflictResolutionProposed: true},
	ConflictFalsePositive:      {ConflictClosed: true},
	ConflictResolutionProposed: {ConflictResolved: true},
	ConflictResolved:           {ConflictClosed: true},
	ConflictClosed:             {},
}

func CanConflictTransition(from, to string) bool { return conflictTransitions[from][to] }

// ResolutionBatchState is the lifecycle vocabulary for reviewer batch
// disposition of confirmed topology conflicts.
type ResolutionBatchState string

const (
	// BatchPreview holds frozen parcel versions and suggestion hashes until the
	// reviewer submits it. A preview batch is still "open".
	BatchPreview ResolutionBatchState = "preview"
	// BatchSubmitted means every conflict advanced and every draft proposal was
	// created in one transaction. A submitted batch is finished.
	BatchSubmitted ResolutionBatchState = "submitted"
	// BatchCancelled is a discarded preview. It never created proposals and no
	// longer claims its conflicts.
	BatchCancelled ResolutionBatchState = "cancelled"
)

// BatchOpenStates lists states that still claim their conflicts. Preview is
// the only open state today; the table keeps a vocabulary so terminal
// failure states cannot silently overlap with a new preview.
var BatchOpenStates = []string{string(BatchPreview)}

func (s ResolutionBatchState) Valid() bool {
	return s == BatchPreview || s == BatchSubmitted || s == BatchCancelled
}
