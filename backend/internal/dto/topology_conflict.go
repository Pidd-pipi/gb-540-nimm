package dto

import "time"

type ConflictQuery struct {
	ProposalID *uint
	State      string
	Type       string
	Page       int
	PageSize   int
}

type DetectConflictRequest struct {
	ProposalID     uint    `json:"proposal_id" validate:"required,gt=0"`
	SnapToleranceM float64 `json:"snap_tolerance_m" validate:"omitempty,gt=0,lte=1000"`
}

type ConflictTransitionRequest struct {
	To string `json:"to" validate:"required"`
}

type ApplySuggestionRequest struct {
	Rationale string `json:"rationale" validate:"max=2000"`
}

type ConflictBatchPreviewRequest struct {
	ConflictIDs []uint `json:"conflict_ids" validate:"required,min=2,max=50,dive,gt=0"`
}

type ConflictBatchQuery struct {
	ParcelID *uint
	State    string
	Page     int
	PageSize int
}

// ConflictBatchItemView is the read model for one frozen conflict entry.
type ConflictBatchItemView struct {
	ConflictID     uint          `json:"conflict_id"`
	ProposalID     *uint         `json:"proposal_id"`
	ParcelVersions map[uint]uint `json:"parcel_versions"`
	SuggestionHash string        `json:"suggestion_hash"`
	Submittable    bool          `json:"submittable"`
	Reasons        []string      `json:"reasons"`
}

// ConflictBatchView is the read model returned by preview, submit, and GET so
// the page renders the same evaluation after a refresh.
type ConflictBatchView struct {
	ID             uint                    `json:"id"`
	ParcelID       uint                    `json:"parcel_id"`
	BatchState     string                  `json:"batch_state"`
	IdempotencyKey string                  `json:"idempotency_key"`
	Submittable    bool                    `json:"submittable"`
	Reasons        []string                `json:"reasons"`
	Items          []ConflictBatchItemView `json:"items"`
	CreatedAt      time.Time               `json:"created_at"`
	CompletedAt    *time.Time              `json:"completed_at"`
}
