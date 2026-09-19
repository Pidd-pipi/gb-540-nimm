package service

import (
	"errors"
	"fmt"
	"testing"

	"cadastral-boundary-topology-resolution/backend/internal/constants"
	"cadastral-boundary-topology-resolution/backend/internal/dto"
	"cadastral-boundary-topology-resolution/backend/internal/model"
	"cadastral-boundary-topology-resolution/backend/internal/repository"
)

// batchFixture creates two neighbouring parcels, proposals and overlap
// conflicts, confirms every conflict and returns the service, store and ids.
type batchFixture struct {
	svc       *CadastralService
	store     *repository.Store
	parcel    model.LandParcel
	proposals []model.BoundaryProposal
	conflicts []model.TopologyConflict
	reviewer  Actor
}

func newBatchFixture(t *testing.T) batchFixture {
	t.Helper()
	svc, store := newCadastralTestService(t)
	surveyor := testActor(101, constants.RoleSurveyor, "batch-parcel")
	base := createTestParcel(t, svc, "P-BATCH-A", serviceTestPolygon(`[0,0],[10,0],[10,10],[0,10],[0,0]`), surveyor)
	createTestParcel(t, svc, "P-BATCH-NEIGHBOR", serviceTestPolygon(`[10,0],[20,0],[20,10],[10,10],[10,0]`), testActor(102, constants.RoleSurveyor, "batch-neighbour"))
	reviewer := testActor(301, constants.RoleReviewer, "batch-review")

	proposals := []model.BoundaryProposal{
		createTestProposal(t, svc, base, serviceTestPolygon(`[0,0],[11,0],[11,10],[0,10],[0,0]`), surveyor),
	}
	conflicts := []model.TopologyConflict{}
	for _, proposal := range proposals {
		found, err := svc.DetectConflicts(dto.DetectConflictRequest{ProposalID: proposal.ID, SnapToleranceM: 0.1}, fmt.Sprintf("detect-%d", proposal.ID), testActor(101, constants.RoleGISAnalyst, "batch-detect"))
		if err != nil {
			t.Fatalf("DetectConflicts: %v", err)
		}
		if len(found) == 0 {
			t.Fatalf("expected overlap findings for proposal %d", proposal.ID)
		}
		for _, conflict := range found {
			confirmed, err := svc.TransitionConflict(conflict.ID, dto.ConflictTransitionRequest{To: constants.ConflictConfirmed}, reviewer)
			if err != nil {
				t.Fatalf("confirm conflict %d: %v", conflict.ID, err)
			}
			conflicts = append(conflicts, confirmed)
		}
	}
	return batchFixture{svc: svc, store: store, parcel: base, proposals: proposals, conflicts: conflicts, reviewer: reviewer}
}

func TestResolutionBatchHappyPathCreatesProposalsAndAdvancesConflicts(t *testing.T) {
	fixture := newBatchFixture(t)
	ids := make([]uint, 0, len(fixture.conflicts))
	for _, conflict := range fixture.conflicts {
		ids = append(ids, conflict.ID)
	}
	preview, err := fixture.svc.CreateResolutionBatch(dto.CreateResolutionBatchRequest{ParcelID: fixture.parcel.ID, ConflictIDs: ids, Rationale: "batch disposition"}, fixture.reviewer)
	if err != nil {
		t.Fatalf("CreateResolutionBatch: %v", err)
	}
	if !preview.Submittable || preview.BatchState != "preview" || len(preview.Items) != len(ids) {
		t.Fatalf("preview = %#v, want submittable preview with %d items", preview, len(ids))
	}

	submitted, err := fixture.svc.SubmitResolutionBatch(preview.ID, dto.SubmitResolutionBatchRequest{}, "batch-submit-key", fixture.reviewer)
	if err != nil {
		t.Fatalf("SubmitResolutionBatch: %v", err)
	}
	if submitted.BatchState != "submitted" || submitted.Submittable || len(submitted.ProposalIDs) != len(ids) {
		t.Fatalf("submitted batch = %#v", submitted)
	}
	for index, line := range submitted.Items {
		if !line.Applied {
			t.Fatalf("submitted item %d = %#v, want applied", index, line)
		}
	}
	for _, conflictID := range ids {
		conflict, err := fixture.svc.GetConflict(conflictID)
		if err != nil {
			t.Fatalf("reload conflict %d: %v", conflictID, err)
		}
		if conflict.ConflictState != constants.ConflictResolved {
			t.Fatalf("conflict %d state = %s, want resolved", conflictID, conflict.ConflictState)
		}
	}
	for _, proposalID := range submitted.ProposalIDs {
		proposal, err := fixture.svc.GetProposal(proposalID)
		if err != nil {
			t.Fatalf("reload proposal %d: %v", proposalID, err)
		}
		if proposal.ProposalState != constants.ProposalDraft || proposal.ParcelID != fixture.parcel.ID {
			t.Fatalf("batch proposal %d = %#v, want draft for parcel %d", proposalID, proposal, fixture.parcel.ID)
		}
	}

	// Replaying the same key returns the same batch; a different request body
	// with the same key is rejected.
	replay, err := fixture.svc.SubmitResolutionBatch(preview.ID, dto.SubmitResolutionBatchRequest{}, "batch-submit-key", testActor(301, constants.RoleReviewer, "batch-replay"))
	if err != nil {
		t.Fatalf("replay SubmitResolutionBatch: %v", err)
	}
	if replay.ID != submitted.ID || len(replay.ProposalIDs) != len(submitted.ProposalIDs) {
		t.Fatalf("replay = %#v, want original batch %d", replay, submitted.ID)
	}
	_, err = fixture.svc.SubmitResolutionBatch(preview.ID, dto.SubmitResolutionBatchRequest{Rationale: "changed request"}, "batch-submit-key", fixture.reviewer)
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Status != 409 {
		t.Fatalf("same key different request error = %v, want 409", err)
	}
}

func TestResolutionBatchFailsWhenConflictProcessed(t *testing.T) {
	fixture := newBatchFixture(t)
	target := fixture.conflicts[0]
	preview, err := fixture.svc.CreateResolutionBatch(dto.CreateResolutionBatchRequest{ParcelID: fixture.parcel.ID, ConflictIDs: []uint{target.ID}}, fixture.reviewer)
	if err != nil {
		t.Fatalf("CreateResolutionBatch: %v", err)
	}
	// Another reviewer flow advances the conflict before submission.
	if _, err := fixture.svc.TransitionConflict(target.ID, dto.ConflictTransitionRequest{To: constants.ConflictResolutionProposed}, testActor(302, constants.RoleReviewer, "race")); err != nil {
		t.Fatalf("race transition: %v", err)
	}
	proposalsBefore := countProposals(t, fixture.store)
	_, err = fixture.svc.SubmitResolutionBatch(preview.ID, dto.SubmitResolutionBatchRequest{}, "stale-conflict-key", fixture.reviewer)
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Status != 409 || appErr.Details[fmt.Sprint(target.ID)] != "conflict_processed" {
		t.Fatalf("submit after processing error = %v, want 409 conflict_processed", err)
	}
	if proposalsAfter := countProposals(t, fixture.store); proposalsAfter != proposalsBefore {
		t.Fatalf("proposal count changed %d -> %d on failed batch", proposalsBefore, proposalsAfter)
	}
	conflict, _ := fixture.svc.GetConflict(target.ID)
	if conflict.ConflictState != constants.ConflictResolutionProposed {
		t.Fatalf("conflict state = %s, want untouched resolution_proposed", conflict.ConflictState)
	}

	// Read-back reports the invalidation reason consistently.
	reread, err := fixture.svc.GetResolutionBatch(preview.ID)
	if err != nil {
		t.Fatalf("GetResolutionBatch: %v", err)
	}
	if reread.Submittable || len(reread.InvalidConflictIDs) != 1 || reread.Items[0].ReasonCode != "conflict_processed" {
		t.Fatalf("reread batch = %#v, want non-submittable with processed reason", reread)
	}
}

func TestResolutionBatchFailsWhenParcelVersionChanges(t *testing.T) {
	fixture := newBatchFixture(t)
	target := fixture.conflicts[0]
	preview, err := fixture.svc.CreateResolutionBatch(dto.CreateResolutionBatchRequest{ParcelID: fixture.parcel.ID, ConflictIDs: []uint{target.ID}}, fixture.reviewer)
	if err != nil {
		t.Fatalf("CreateResolutionBatch: %v", err)
	}
	surveyor := testActor(101, constants.RoleSurveyor, "parcel-bump")
	if _, err := fixture.svc.UpdateParcel(fixture.parcel.ID, dto.UpdateParcelRequest{BoundaryVersion: &fixture.parcel.BoundaryVersion, Name: strPtr("renamed parcel")}, surveyor); err != nil {
		t.Fatalf("bump parcel version: %v", err)
	}
	_, err = fixture.svc.SubmitResolutionBatch(preview.ID, dto.SubmitResolutionBatchRequest{}, "stale-version-key", fixture.reviewer)
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Status != 409 || appErr.Details[fmt.Sprint(target.ID)] != "parcel_version_changed" {
		t.Fatalf("submit after parcel change error = %v, want 409 parcel_version_changed", err)
	}
	conflict, _ := fixture.svc.GetConflict(target.ID)
	if conflict.ConflictState != constants.ConflictConfirmed {
		t.Fatalf("conflict state = %s, want untouched confirmed", conflict.ConflictState)
	}
}

func TestResolutionBatchFailsWhenCoveredByAnotherOpenBatch(t *testing.T) {
	fixture := newBatchFixture(t)
	target := fixture.conflicts[0]
	first, err := fixture.svc.CreateResolutionBatch(dto.CreateResolutionBatchRequest{ParcelID: fixture.parcel.ID, ConflictIDs: []uint{target.ID}}, fixture.reviewer)
	if err != nil {
		t.Fatalf("first CreateResolutionBatch: %v", err)
	}
	otherReviewer := testActor(302, constants.RoleReviewer, "second-preview")
	second, err := fixture.svc.CreateResolutionBatch(dto.CreateResolutionBatchRequest{ParcelID: fixture.parcel.ID, ConflictIDs: []uint{target.ID}}, otherReviewer)
	if err != nil {
		t.Fatalf("second CreateResolutionBatch: %v", err)
	}
	// The later preview is blocked by the earlier open batch even before the
	// first one finishes.
	reread, err := fixture.svc.GetResolutionBatch(second.ID)
	if err != nil {
		t.Fatalf("GetResolutionBatch(second): %v", err)
	}
	if reread.Submittable || reread.Items[0].ReasonCode != "covered_by_open_batch" {
		t.Fatalf("second preview = %#v, want covered_by_open_batch", reread)
	}
	_, err = fixture.svc.SubmitResolutionBatch(second.ID, dto.SubmitResolutionBatchRequest{}, "second-loses", otherReviewer)
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Status != 409 || appErr.Details[fmt.Sprint(target.ID)] != "covered_by_open_batch" {
		t.Fatalf("covered batch submit error = %v, want 409 covered_by_open_batch", err)
	}
	// The earliest preview still submits normally and advances the conflict.
	if _, err := fixture.svc.SubmitResolutionBatch(first.ID, dto.SubmitResolutionBatchRequest{}, "first-wins", fixture.reviewer); err != nil {
		t.Fatalf("first SubmitResolutionBatch: %v", err)
	}
	conflict, _ := fixture.svc.GetConflict(target.ID)
	if conflict.ConflictState != constants.ConflictResolved {
		t.Fatalf("conflict state = %s, want resolved after earliest batch", conflict.ConflictState)
	}
}

func TestResolutionBatchCancelledPreviewReleasesCoverage(t *testing.T) {
	fixture := newBatchFixture(t)
	target := fixture.conflicts[0]
	first, err := fixture.svc.CreateResolutionBatch(dto.CreateResolutionBatchRequest{ParcelID: fixture.parcel.ID, ConflictIDs: []uint{target.ID}}, fixture.reviewer)
	if err != nil {
		t.Fatalf("first CreateResolutionBatch: %v", err)
	}
	otherReviewer := testActor(302, constants.RoleReviewer, "second-preview-cancel")
	second, err := fixture.svc.CreateResolutionBatch(dto.CreateResolutionBatchRequest{ParcelID: fixture.parcel.ID, ConflictIDs: []uint{target.ID}}, otherReviewer)
	if err != nil {
		t.Fatalf("second CreateResolutionBatch: %v", err)
	}
	cancelled, err := fixture.svc.CancelResolutionBatch(first.ID, fixture.reviewer)
	if err != nil {
		t.Fatalf("CancelResolutionBatch: %v", err)
	}
	if cancelled.BatchState != "cancelled" || cancelled.Submittable {
		t.Fatalf("cancelled batch = %#v", cancelled)
	}
	// After cancellation the later preview becomes submittable.
	reread, err := fixture.svc.GetResolutionBatch(second.ID)
	if err != nil {
		t.Fatalf("GetResolutionBatch: %v", err)
	}
	if !reread.Submittable {
		t.Fatalf("second preview = %#v, want submittable after cancel", reread)
	}
	if _, err := fixture.svc.SubmitResolutionBatch(second.ID, dto.SubmitResolutionBatchRequest{}, "second-after-cancel", otherReviewer); err != nil {
		t.Fatalf("submit after cancel: %v", err)
	}
	if _, err := fixture.svc.CancelResolutionBatch(first.ID, fixture.reviewer); err == nil {
		t.Fatalf("cancelling a cancelled batch must fail")
	}
}

func TestResolutionBatchRejectsUnconfirmedConflictPreview(t *testing.T) {
	fixture := newBatchFixture(t)
	// A freshly detected conflict stays in detected state.
	detected, err := fixture.svc.DetectConflicts(dto.DetectConflictRequest{ProposalID: fixture.proposals[0].ID, SnapToleranceM: 0.2}, "detected-preview", testActor(101, constants.RoleGISAnalyst, "batch-detect-detected"))
	if err != nil {
		t.Fatalf("DetectConflicts: %v", err)
	}
	var detectedID uint
	for _, conflict := range detected {
		if conflict.ConflictState == constants.ConflictDetected {
			detectedID = conflict.ID
			break
		}
	}
	if detectedID == 0 {
		t.Fatalf("no detected conflict returned: %#v", detected)
	}
	_, err = fixture.svc.CreateResolutionBatch(dto.CreateResolutionBatchRequest{ParcelID: fixture.parcel.ID, ConflictIDs: []uint{detectedID}}, fixture.reviewer)
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Status != 409 || appErr.Details[fmt.Sprint(detectedID)] != "conflict_processed" {
		t.Fatalf("preview with detected conflict error = %v, want 409 detail", err)
	}
}

func TestResolutionBatchRejectsForeignParcelConflict(t *testing.T) {
	fixture := newBatchFixture(t)
	other := createTestParcel(t, fixture.svc, "P-BATCH-B", serviceTestPolygon(`[30,0],[40,0],[40,10],[30,10],[30,0]`), testActor(102, constants.RoleSurveyor, "other-parcel"))
	_, err := fixture.svc.CreateResolutionBatch(dto.CreateResolutionBatchRequest{ParcelID: other.ID, ConflictIDs: []uint{fixture.conflicts[0].ID}}, fixture.reviewer)
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Status != 400 {
		t.Fatalf("foreign parcel conflict error = %v, want 400", err)
	}
}

func TestResolutionBatchFailsWhenSuggestionHashDrifts(t *testing.T) {
	fixture := newBatchFixture(t)
	target := fixture.conflicts[0]
	preview, err := fixture.svc.CreateResolutionBatch(dto.CreateResolutionBatchRequest{ParcelID: fixture.parcel.ID, ConflictIDs: []uint{target.ID}}, fixture.reviewer)
	if err != nil {
		t.Fatalf("CreateResolutionBatch: %v", err)
	}
	// Simulate a suggestion record changing under the frozen hash while the
	// conflict state and parcel version stay untouched.
	drifted := target
	drifted.SuggestedResolutionJSON = `{"snapped_geojson":{"type":"Polygon","coordinates":[[[0,0],[12,0],[12,10],[0,10],[0,0]]]}}`
	if err := fixture.store.DB.Model(&model.TopologyConflict{}).Where("id = ?", target.ID).
		Update("suggested_resolution_json", drifted.SuggestedResolutionJSON).Error; err != nil {
		t.Fatalf("tamper suggestion: %v", err)
	}
	_, err = fixture.svc.SubmitResolutionBatch(preview.ID, dto.SubmitResolutionBatchRequest{}, "stale-suggestion-key", fixture.reviewer)
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Status != 409 || appErr.Details[fmt.Sprint(target.ID)] != "suggestion_changed" {
		t.Fatalf("submit after suggestion drift error = %v, want 409 suggestion_changed", err)
	}
	conflict, _ := fixture.svc.GetConflict(target.ID)
	if conflict.ConflictState != constants.ConflictConfirmed {
		t.Fatalf("conflict state = %s, want untouched confirmed", conflict.ConflictState)
	}
}

func TestResolutionBatchWritesAuditTrail(t *testing.T) {
	fixture := newBatchFixture(t)
	target := fixture.conflicts[0]
	preview, err := fixture.svc.CreateResolutionBatch(dto.CreateResolutionBatchRequest{ParcelID: fixture.parcel.ID, ConflictIDs: []uint{target.ID}, Rationale: "audit me"}, fixture.reviewer)
	if err != nil {
		t.Fatalf("CreateResolutionBatch: %v", err)
	}
	if _, err := fixture.svc.SubmitResolutionBatch(preview.ID, dto.SubmitResolutionBatchRequest{}, "audit-submit-key", fixture.reviewer); err != nil {
		t.Fatalf("SubmitResolutionBatch: %v", err)
	}
	var actions []string
	if err := fixture.store.DB.Model(&model.AuditLog{}).Where("resource_type = ?", "ConflictResolutionBatch").Order("id ASC").Pluck("action", &actions).Error; err != nil {
		t.Fatalf("query batch audits: %v", err)
	}
	want := []string{"conflict_batch.created", "conflict_batch.submitted"}
	if len(actions) != 2 || actions[0] != want[0] || actions[1] != want[1] {
		t.Fatalf("batch audit actions = %v, want %v", actions, want)
	}
	var conflictAuditCount int64
	if err := fixture.store.DB.Model(&model.AuditLog{}).Where("action = ? AND resource_type = ?", "conflict.batch_resolved", "TopologyConflict").Count(&conflictAuditCount).Error; err != nil {
		t.Fatalf("query conflict audits: %v", err)
	}
	if conflictAuditCount != 1 {
		t.Fatalf("conflict.batch_resolved audit count = %d, want 1", conflictAuditCount)
	}
}

func strPtr(value string) *string { return &value }
func countProposals(t *testing.T, store *repository.Store) int64 {
	t.Helper()
	var count int64
	if err := store.DB.Model(&model.BoundaryProposal{}).Count(&count).Error; err != nil {
		t.Fatalf("count proposals: %v", err)
	}
	return count
}
