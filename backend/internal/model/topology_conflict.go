package model

import (
	"time"

	"cadastral-boundary-topology-resolution/backend/internal/constants"
)

// TopologyConflict is an immutable detection result for a proposal snapshot.
type TopologyConflict struct {
	ID                      uint                   `gorm:"primaryKey" json:"id"`
	ProposalID              uint                   `gorm:"not null;index" json:"proposal_id"`
	ParcelIDs               string                 `gorm:"type:text;not null" json:"parcel_ids"`
	ConflictType            constants.ConflictType `gorm:"size:32;not null;index" json:"conflict_type"`
	GeometryGeoJSON         string                 `gorm:"column:geometry_geojson;type:text" json:"geometry_geojson"`
	MagnitudeSquareM        float64                `gorm:"not null" json:"magnitude_square_m"`
	Severity                string                 `gorm:"size:16;not null" json:"severity"`
	AlgorithmVersion        string                 `gorm:"size:40;not null" json:"algorithm_version"`
	InputHash               string                 `gorm:"size:128;not null;index" json:"input_hash"`
	ConflictState           string                 `gorm:"size:32;not null;index" json:"conflict_state"`
	SuggestedResolutionJSON string                 `gorm:"type:text" json:"suggested_resolution_json"`
	Explanation             string                 `gorm:"size:2000" json:"explanation"`
	DetectedAt              time.Time              `gorm:"not null" json:"detected_at"`
	ResolvedBy              *uint                  `json:"resolved_by"`
}

// TopologyDetectionRun binds an actor and Idempotency-Key to immutable
// detection results so an interrupted client can replay the same operation.
type TopologyDetectionRun struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	ProposalID     uint      `gorm:"not null;index" json:"proposal_id"`
	ActorID        uint      `gorm:"not null;uniqueIndex:idx_detection_actor_key" json:"actor_id"`
	IdempotencyKey string    `gorm:"size:128;not null;uniqueIndex:idx_detection_actor_key" json:"idempotency_key"`
	RequestHash    string    `gorm:"size:128;not null" json:"request_hash"`
	InputHash      string    `gorm:"size:128;not null;index" json:"input_hash"`
	ResultIDs      string    `gorm:"type:text;not null" json:"result_ids"`
	CreatedAt      time.Time `json:"created_at"`
}

// ConflictResolutionBatch groups confirmed conflicts of one parcel so a
// reviewer can dispose them atomically. A previewed batch is unfinished and
// holds an exclusion over its conflicts; completed and failed are terminal.
type ConflictResolutionBatch struct {
	ID             uint       `gorm:"primaryKey" json:"id"`
	ParcelID       uint       `gorm:"not null;index" json:"parcel_id"`
	ActorID        uint       `gorm:"not null;uniqueIndex:idx_conflict_batch_actor_key" json:"actor_id"`
	IdempotencyKey string     `gorm:"size:128;not null;uniqueIndex:idx_conflict_batch_actor_key" json:"idempotency_key"`
	RequestHash    string     `gorm:"size:128;not null" json:"request_hash"`
	BatchState     string     `gorm:"size:24;not null;index" json:"batch_state"`
	FailureReasons string     `gorm:"type:text;not null;default:'[]'" json:"failure_reasons"`
	CreatedAt      time.Time  `json:"created_at"`
	CompletedAt    *time.Time `json:"completed_at"`
}

// ConflictResolutionBatchItem freezes one conflict's disposition inputs at
// preview time: the participating parcel versions and the suggestion hash.
type ConflictResolutionBatchItem struct {
	ID                 uint   `gorm:"primaryKey" json:"id"`
	BatchID            uint   `gorm:"not null;index" json:"batch_id"`
	ConflictID         uint   `gorm:"not null;index" json:"conflict_id"`
	ParcelVersionsJSON string `gorm:"type:text;not null" json:"parcel_versions_json"`
	SuggestionHash     string `gorm:"size:128;not null" json:"suggestion_hash"`
	ProposalID         *uint  `json:"proposal_id"`
}
