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

// ConflictResolutionBatch groups multiple confirmed TopologyConflict records
// that share the same parcel so a reviewer can apply their deterministic
// suggestions in one all-or-nothing submission. A preview freezes the parcel
// boundary version of every participating parcel and a hash of every
// suggestion; submission refuses to run when any frozen fact drifted.
type ConflictResolutionBatch struct {
	ID          uint       `gorm:"primaryKey" json:"id"`
	BatchCode   string     `gorm:"size:64;not null;uniqueIndex" json:"batch_code"`
	ParcelID    uint       `gorm:"not null;index" json:"parcel_id"`
	BatchState  string     `gorm:"size:24;not null;index" json:"batch_state"`
	ConflictIDs string     `gorm:"column:conflict_ids;type:text;not null" json:"-"`
	CreatedBy   uint       `gorm:"not null" json:"created_by"`
	SubmittedBy *uint      `json:"submitted_by"`
	SubmittedAt *time.Time `json:"submitted_at"`
	ProposalIDs string     `gorm:"column:proposal_ids;type:text" json:"-"`
	Rationale   string     `gorm:"size:2000" json:"rationale"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// ConflictResolutionBatchItem is one frozen conflict line inside a batch. The
// snapshot columns let the page show why a preview can no longer be submitted.
type ConflictResolutionBatchItem struct {
	ID                  uint      `gorm:"primaryKey" json:"id"`
	BatchID             uint      `gorm:"not null;uniqueIndex:idx_batch_item,priority:1;index" json:"batch_id"`
	ConflictID          uint      `gorm:"not null;uniqueIndex:idx_batch_item,priority:2;index:idx_batch_conflict" json:"conflict_id"`
	ProposalID          uint      `gorm:"not null" json:"proposal_id"`
	ParcelID            uint      `gorm:"not null;index" json:"parcel_id"`
	FrozenParcelVersion uint      `gorm:"not null" json:"frozen_parcel_version"`
	FrozenState         string    `gorm:"size:32;not null" json:"frozen_state"`
	SuggestionHash      string    `gorm:"size:128;not null" json:"suggestion_hash"`
	SuggestedGeoJSON    string    `gorm:"column:suggested_geojson;type:text;not null" json:"suggested_geojson"`
	CreatedAt           time.Time `json:"created_at"`
}

// ConflictResolutionBatchRun binds an actor and Idempotency-Key to one batch
// submission, mirroring the detection-run idempotency record. A failed
// validation never creates a run, so the same key can still be replayed after
// the reviewer fixes the underlying conflict coverage.
type ConflictResolutionBatchRun struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	BatchID        uint      `gorm:"not null;index" json:"batch_id"`
	ActorID        uint      `gorm:"not null;uniqueIndex:idx_batch_run_actor_key" json:"actor_id"`
	IdempotencyKey string    `gorm:"size:128;not null;uniqueIndex:idx_batch_run_actor_key" json:"idempotency_key"`
	RequestHash    string    `gorm:"size:128;not null" json:"request_hash"`
	ProposalIDs    string    `gorm:"type:text;not null" json:"proposal_ids"`
	CreatedAt      time.Time `json:"created_at"`
}
