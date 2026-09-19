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

// CreateResolutionBatchRequest freezes a set of confirmed conflicts that all
// belong to the same parcel. No Idempotency-Key is required for previews:
// previews never mutate conflicts or proposals, so repeating a preview simply
// returns another frozen snapshot.
type CreateResolutionBatchRequest struct {
	ParcelID    uint   `json:"parcel_id" validate:"required,gt=0"`
	ConflictIDs []uint `json:"conflict_ids" validate:"required,min=1,max=100,dive,gt=0"`
	Rationale   string `json:"rationale" validate:"max=2000"`
}

// SubmitResolutionBatchRequest finalizes a preview batch. The Idempotency-Key
// header replays the original outcome and rejects key reuse across requests.
type SubmitResolutionBatchRequest struct {
	// Rationale optionally overrides the rationale captured at preview time.
	Rationale string `json:"rationale" validate:"max=2000"`
}

// ResolutionBatchItemView is one frozen conflict line with its live status.
type ResolutionBatchItemView struct {
	ConflictID           uint   `json:"conflict_id"`
	ProposalID           uint   `json:"proposal_id"`
	ParcelID             uint   `json:"parcel_id"`
	FrozenParcelVersion  uint   `json:"frozen_parcel_version"`
	CurrentParcelVersion uint   `json:"current_parcel_version"`
	FrozenState          string `json:"frozen_state"`
	CurrentState         string `json:"current_state"`
	SuggestionHash       string `json:"suggestion_hash"`
	// Submittable is true for a frozen preview line that still matches live
	// state. Lines of submitted batches report Applied instead.
	Submittable bool `json:"submittable"`
	// Applied is true once the batch finished and the line produced a draft.
	Applied    bool   `json:"applied"`
	ReasonCode string `json:"reason_code,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// ResolutionBatchView is the read model returned for preview, submit, detail
// and list. Submittable plus per-item reasons drive the conflict page.
type ResolutionBatchView struct {
	ID                 uint                      `json:"id"`
	BatchCode          string                    `json:"batch_code"`
	ParcelID           uint                      `json:"parcel_id"`
	BatchState         string                    `json:"batch_state"`
	ConflictIDs        []uint                    `json:"conflict_ids"`
	ProposalIDs        []uint                    `json:"proposal_ids"`
	Rationale          string                    `json:"rationale"`
	CreatedBy          uint                      `json:"created_by"`
	SubmittedBy        *uint                     `json:"submitted_by"`
	CreatedAt          time.Time                 `json:"created_at"`
	UpdatedAt          time.Time                 `json:"updated_at"`
	SubmittedAt        *time.Time                `json:"submitted_at"`
	Submittable        bool                      `json:"submittable"`
	InvalidReason      string                    `json:"invalid_reason,omitempty"`
	InvalidConflictIDs []uint                    `json:"invalid_conflict_ids,omitempty"`
	Items              []ResolutionBatchItemView `json:"items"`
}
