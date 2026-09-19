package service

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"cadastral-boundary-topology-resolution/backend/internal/constants"
	"cadastral-boundary-topology-resolution/backend/internal/dto"
	"cadastral-boundary-topology-resolution/backend/internal/model"
	"cadastral-boundary-topology-resolution/backend/internal/repository"
)

func createTestConflict(t *testing.T, store *repository.Store, proposal model.BoundaryProposal, parcelID uint, state, snappedPolygon string) model.TopologyConflict {
	t.Helper()
	suggestion := `{"action":"review_snapped_boundary","snapped_geojson":` + snappedPolygon + `,"tolerance_m":0.1,"algorithm_version":"test-v1"}`
	item := model.TopologyConflict{
		ProposalID: proposal.ID, ParcelIDs: fmt.Sprintf("[%d]", parcelID), ConflictType: constants.ConflictOverlap,
		MagnitudeSquareM: 10, Severity: "high", AlgorithmVersion: "test-v1", InputHash: fmt.Sprintf("input-%d", time.Now().UnixNano()),
		ConflictState: state, SuggestedResolutionJSON: suggestion, Explanation: "batch test fixture", DetectedAt: time.Now().UTC(),
	}
	if err := store.Conflicts.Create(&item); err != nil {
		t.Fatalf("create conflict fixture: %v", err)
	}
	return item
}

func countProposals(t *testing.T, store *repository.Store) int64 {
	t.Helper()
	var total int64
	if err := store.DB.Model(&model.BoundaryProposal{}).Count(&total).Error; err != nil {
		t.Fatalf("count proposals: %v", err)
	}
	return total
}

func conflictStateOf(t *testing.T, store *repository.Store, id uint) string {
	t.Helper()
	item, err := store.Conflicts.Get(id)
	if err != nil {
		t.Fatalf("reload conflict %d: %v", id, err)
	}
	return item.ConflictState
}

func newBatchFixture(t *testing.T, svc *CadastralService, store *repository.Store, snapped string) (model.LandParcel, model.BoundaryProposal, model.TopologyConflict, model.TopologyConflict) {
	t.Helper()
	surveyor := testActor(101, constants.RoleSurveyor, "batch-fixture-parcel")
	parcel := createTestParcel(t, svc, "P-BATCH-"+t.Name(), serviceTestPolygon(`[0,0],[10,0],[10,10],[0,10],[0,0]`), surveyor)
	proposal := createTestProposal(t, svc, parcel, serviceTestPolygon(`[0,0],[11,0],[11,10],[0,10],[0,0]`), surveyor)
	first := createTestConflict(t, store, proposal, parcel.ID, constants.ConflictConfirmed, snapped)
	second := createTestConflict(t, store, proposal, parcel.ID, constants.ConflictConfirmed, snapped)
	return parcel, proposal, first, second
}

func TestConflictBatchSubmitCreatesDraftsAndResolvesConflictsAtomically(t *testing.T) {
	svc, store := newCadastralTestService(t)
	snapped := serviceTestPolygon(`[0,0],[10.5,0],[10.5,10],[0,10],[0,0]`)
	parcel, source, first, second := newBatchFixture(t, svc, store, snapped)
	reviewer := testActor(301, constants.RoleReviewer, "batch-preview")

	preview, err := svc.PreviewConflictBatch(dto.ConflictBatchPreviewRequest{ConflictIDs: []uint{second.ID, first.ID}}, "batch-key-happy", reviewer)
	if err != nil {
		t.Fatalf("PreviewConflictBatch() error = %v", err)
	}
	if preview.BatchState != constants.ConflictBatchPreviewed || !preview.Submittable || len(preview.Reasons) != 0 {
		t.Fatalf("preview = %#v, want submittable previewed batch", preview)
	}
	if preview.ParcelID != parcel.ID || len(preview.Items) != 2 || preview.Items[0].ConflictID != first.ID {
		t.Fatalf("preview items = %#v, want items ordered by conflict id on parcel %d", preview.Items, parcel.ID)
	}
	if preview.Items[0].ParcelVersions[parcel.ID] != parcel.BoundaryVersion {
		t.Fatalf("frozen versions = %#v, want parcel version %d", preview.Items[0].ParcelVersions, parcel.BoundaryVersion)
	}
	proposalsBefore := countProposals(t, store)

	submitted, err := svc.SubmitConflictBatch(preview.ID, reviewer)
	if err != nil {
		t.Fatalf("SubmitConflictBatch() error = %v", err)
	}
	if submitted.BatchState != constants.ConflictBatchCompleted || submitted.CompletedAt == nil {
		t.Fatalf("submitted batch = %#v, want completed", submitted)
	}
	created := map[uint]bool{}
	for _, item := range submitted.Items {
		if item.ProposalID == nil {
			t.Fatalf("submitted item = %#v, want a created draft proposal", item)
		}
		created[*item.ProposalID] = true
		proposal, getErr := store.Proposals.Get(*item.ProposalID)
		if getErr != nil {
			t.Fatalf("load derived proposal: %v", getErr)
		}
		if proposal.ProposalState != constants.ProposalDraft || proposal.ParcelID != parcel.ID || proposal.BaseVersion != parcel.BoundaryVersion || proposal.Version != source.Version+1 {
			t.Fatalf("derived proposal = %#v, want draft on parcel %d frozen version %d", proposal, parcel.ID, parcel.BoundaryVersion)
		}
	}
	if len(created) != 2 || countProposals(t, store) != proposalsBefore+2 {
		t.Fatalf("created proposals = %v, want exactly 2 new drafts", created)
	}
	for _, id := range []uint{first.ID, second.ID} {
		if state := conflictStateOf(t, store, id); state != constants.ConflictResolved {
			t.Fatalf("conflict %d state = %s, want resolved", id, state)
		}
	}

	replayed, err := svc.SubmitConflictBatch(preview.ID, reviewer)
	if err != nil || replayed.BatchState != constants.ConflictBatchCompleted {
		t.Fatalf("replayed submit = %#v, err = %v, want completed batch", replayed, err)
	}
	if countProposals(t, store) != proposalsBefore+2 {
		t.Fatalf("replayed submit created extra proposals, count = %d", countProposals(t, store))
	}
	reloaded, err := svc.GetConflictBatch(preview.ID)
	if err != nil {
		t.Fatalf("GetConflictBatch() error = %v", err)
	}
	if reloaded.BatchState != submitted.BatchState || len(reloaded.Items) != len(submitted.Items) || *reloaded.Items[0].ProposalID != *submitted.Items[0].ProposalID {
		t.Fatalf("reloaded view = %#v, want consistent read-back of %#v", reloaded, submitted)
	}
}

func TestConflictBatchPreviewReplayAndKeyReuse(t *testing.T) {
	svc, store := newCadastralTestService(t)
	snapped := serviceTestPolygon(`[0,0],[10.5,0],[10.5,10],[0,10],[0,0]`)
	_, _, first, second := newBatchFixture(t, svc, store, snapped)
	reviewer := testActor(302, constants.RoleReviewer, "batch-replay")

	preview, err := svc.PreviewConflictBatch(dto.ConflictBatchPreviewRequest{ConflictIDs: []uint{first.ID, second.ID}}, "batch-key-replay", reviewer)
	if err != nil {
		t.Fatalf("PreviewConflictBatch() error = %v", err)
	}
	replayed, err := svc.PreviewConflictBatch(dto.ConflictBatchPreviewRequest{ConflictIDs: []uint{second.ID, first.ID}}, "batch-key-replay", reviewer)
	if err != nil {
		t.Fatalf("replayed PreviewConflictBatch() error = %v", err)
	}
	if replayed.ID != preview.ID {
		t.Fatalf("replayed preview id = %d, want original batch %d", replayed.ID, preview.ID)
	}
	var batchCount int64
	if err := store.DB.Model(&model.ConflictResolutionBatch{}).Count(&batchCount).Error; err != nil {
		t.Fatalf("count batches: %v", err)
	}
	if batchCount != 1 {
		t.Fatalf("batch count = %d, want 1 after replay", batchCount)
	}
	_, err = svc.PreviewConflictBatch(dto.ConflictBatchPreviewRequest{ConflictIDs: []uint{first.ID, 999998}}, "batch-key-replay", reviewer)
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Code != CodeConflict || appErr.Status != 409 {
		t.Fatalf("key reuse with different request error = %v, want 409 %s", err, CodeConflict)
	}
	_, err = svc.PreviewConflictBatch(dto.ConflictBatchPreviewRequest{ConflictIDs: []uint{first.ID, second.ID}}, "", reviewer)
	if !errors.As(err, &appErr) || appErr.Code != CodeInvalidInput || appErr.Status != 400 {
		t.Fatalf("missing Idempotency-Key error = %v, want 400 %s", err, CodeInvalidInput)
	}
	_, err = svc.PreviewConflictBatch(dto.ConflictBatchPreviewRequest{ConflictIDs: []uint{first.ID, second.ID}}, "batch-key-role", testActor(303, constants.RoleGISAnalyst, "batch-role"))
	if !errors.As(err, &appErr) || appErr.Code != CodeForbidden || appErr.Status != 403 {
		t.Fatalf("non-reviewer preview error = %v, want 403 %s", err, CodeForbidden)
	}
}

func TestConflictBatchSubmitFailsWhenConflictProcessed(t *testing.T) {
	svc, store := newCadastralTestService(t)
	snapped := serviceTestPolygon(`[0,0],[10.5,0],[10.5,10],[0,10],[0,0]`)
	_, _, first, second := newBatchFixture(t, svc, store, snapped)
	reviewer := testActor(304, constants.RoleReviewer, "batch-stale")

	preview, err := svc.PreviewConflictBatch(dto.ConflictBatchPreviewRequest{ConflictIDs: []uint{first.ID, second.ID}}, "batch-key-stale", reviewer)
	if err != nil {
		t.Fatalf("PreviewConflictBatch() error = %v", err)
	}
	if _, err := svc.TransitionConflict(first.ID, dto.ConflictTransitionRequest{To: constants.ConflictResolutionProposed}, reviewer); err != nil {
		t.Fatalf("transition fixture conflict: %v", err)
	}
	proposalsBefore := countProposals(t, store)

	_, err = svc.SubmitConflictBatch(preview.ID, reviewer)
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Code != CodeConflict || appErr.Status != 409 {
		t.Fatalf("stale submit error = %v, want 409 %s", err, CodeConflict)
	}
	if countProposals(t, store) != proposalsBefore {
		t.Fatalf("failed submit created proposals, count = %d", countProposals(t, store))
	}
	if state := conflictStateOf(t, store, second.ID); state != constants.ConflictConfirmed {
		t.Fatalf("untouched conflict state = %s, want confirmed", state)
	}
	failed, getErr := svc.GetConflictBatch(preview.ID)
	if getErr != nil {
		t.Fatalf("GetConflictBatch() error = %v", getErr)
	}
	if failed.BatchState != constants.ConflictBatchFailed || failed.Submittable || len(failed.Reasons) == 0 {
		t.Fatalf("failed batch view = %#v, want failed state with reasons", failed)
	}
	if _, resubmitErr := svc.SubmitConflictBatch(preview.ID, reviewer); !errors.As(resubmitErr, &appErr) || appErr.Code != CodeConflict {
		t.Fatalf("resubmit of failed batch error = %v, want 409", resubmitErr)
	}
}

func TestConflictBatchSubmitFailsWhenParcelVersionChanges(t *testing.T) {
	svc, store := newCadastralTestService(t)
	snapped := serviceTestPolygon(`[0,0],[10.5,0],[10.5,10],[0,10],[0,0]`)
	parcel, _, first, second := newBatchFixture(t, svc, store, snapped)
	reviewer := testActor(305, constants.RoleReviewer, "batch-version")

	preview, err := svc.PreviewConflictBatch(dto.ConflictBatchPreviewRequest{ConflictIDs: []uint{first.ID, second.ID}}, "batch-key-version", reviewer)
	if err != nil {
		t.Fatalf("PreviewConflictBatch() error = %v", err)
	}
	renamed := "renamed parcel"
	if _, err := svc.UpdateParcel(parcel.ID, dto.UpdateParcelRequest{Name: &renamed}, testActor(101, constants.RoleSurveyor, "parcel-rename")); err != nil {
		t.Fatalf("bump parcel version: %v", err)
	}
	proposalsBefore := countProposals(t, store)

	_, err = svc.SubmitConflictBatch(preview.ID, reviewer)
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Code != CodeConflict || appErr.Status != 409 {
		t.Fatalf("version-stale submit error = %v, want 409 %s", err, CodeConflict)
	}
	if countProposals(t, store) != proposalsBefore {
		t.Fatalf("failed submit changed proposal count = %d", countProposals(t, store))
	}
	for _, id := range []uint{first.ID, second.ID} {
		if state := conflictStateOf(t, store, id); state != constants.ConflictConfirmed {
			t.Fatalf("conflict %d state = %s, want confirmed after failed batch", id, state)
		}
	}
	failed, _ := svc.GetConflictBatch(preview.ID)
	if failed.BatchState != constants.ConflictBatchFailed || len(failed.Reasons) == 0 {
		t.Fatalf("failed batch view = %#v, want stored invalidation reasons", failed)
	}
}

func TestConflictBatchSubmitFailsWhenUnfinishedBatchOverlaps(t *testing.T) {
	svc, store := newCadastralTestService(t)
	snapped := serviceTestPolygon(`[0,0],[10.5,0],[10.5,10],[0,10],[0,0]`)
	_, proposal, first, second := newBatchFixture(t, svc, store, snapped)
	third := createTestConflict(t, store, proposal, proposal.ParcelID, constants.ConflictConfirmed, snapped)
	reviewer := testActor(306, constants.RoleReviewer, "batch-overlap")

	firstBatch, err := svc.PreviewConflictBatch(dto.ConflictBatchPreviewRequest{ConflictIDs: []uint{first.ID, second.ID}}, "batch-key-overlap-a", reviewer)
	if err != nil {
		t.Fatalf("preview first batch: %v", err)
	}
	secondBatch, err := svc.PreviewConflictBatch(dto.ConflictBatchPreviewRequest{ConflictIDs: []uint{second.ID, third.ID}}, "batch-key-overlap-b", reviewer)
	if err != nil {
		t.Fatalf("preview second batch: %v", err)
	}
	if secondBatch.Submittable || len(secondBatch.Reasons) == 0 {
		t.Fatalf("overlapped preview = %#v, want unsubmittable with reasons", secondBatch)
	}
	proposalsBefore := countProposals(t, store)

	_, err = svc.SubmitConflictBatch(secondBatch.ID, reviewer)
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Code != CodeConflict || appErr.Status != 409 {
		t.Fatalf("overlapped submit error = %v, want 409 %s", err, CodeConflict)
	}
	if countProposals(t, store) != proposalsBefore {
		t.Fatalf("overlapped failure changed proposal count = %d", countProposals(t, store))
	}
	if state := conflictStateOf(t, store, third.ID); state != constants.ConflictConfirmed {
		t.Fatalf("conflict %d state = %s, want confirmed after failed batch", third.ID, state)
	}
	completed, err := svc.SubmitConflictBatch(firstBatch.ID, reviewer)
	if err != nil || completed.BatchState != constants.ConflictBatchCompleted {
		t.Fatalf("first batch submit = %#v, err = %v, want completed", completed, err)
	}
}

func TestConflictBatchPreviewValidatesSelection(t *testing.T) {
	svc, store := newCadastralTestService(t)
	snapped := serviceTestPolygon(`[0,0],[10.5,0],[10.5,10],[0,10],[0,0]`)
	_, _, first, second := newBatchFixture(t, svc, store, snapped)
	reviewer := testActor(307, constants.RoleReviewer, "batch-validate")

	_, err := svc.PreviewConflictBatch(dto.ConflictBatchPreviewRequest{ConflictIDs: []uint{first.ID}}, "batch-key-single", reviewer)
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Code != CodeInvalidInput || appErr.Status != 400 {
		t.Fatalf("single-conflict batch error = %v, want 400 %s", err, CodeInvalidInput)
	}
	_, err = svc.PreviewConflictBatch(dto.ConflictBatchPreviewRequest{ConflictIDs: []uint{first.ID, 999999}}, "batch-key-missing", reviewer)
	if !errors.As(err, &appErr) || appErr.Code != CodeNotFound || appErr.Status != 404 {
		t.Fatalf("missing conflict error = %v, want 404 %s", err, CodeNotFound)
	}
	if _, err := svc.TransitionConflict(second.ID, dto.ConflictTransitionRequest{To: constants.ConflictResolutionProposed}, reviewer); err != nil {
		t.Fatalf("move fixture conflict out of confirmed: %v", err)
	}
	_, err = svc.PreviewConflictBatch(dto.ConflictBatchPreviewRequest{ConflictIDs: []uint{first.ID, second.ID}}, "batch-key-state", reviewer)
	if !errors.As(err, &appErr) || appErr.Code != CodeConflict || appErr.Status != 409 {
		t.Fatalf("non-confirmed selection error = %v, want 409 %s", err, CodeConflict)
	}

	surveyor := testActor(101, constants.RoleSurveyor, "other-parcel")
	otherParcel := createTestParcel(t, svc, "P-BATCH-OTHER", serviceTestPolygon(`[20,20],[30,20],[30,30],[20,30],[20,20]`), surveyor)
	otherProposal := createTestProposal(t, svc, otherParcel, serviceTestPolygon(`[20,20],[31,20],[31,30],[20,30],[20,20]`), surveyor)
	other := createTestConflict(t, store, otherProposal, otherParcel.ID, constants.ConflictConfirmed, snapped)
	_, err = svc.PreviewConflictBatch(dto.ConflictBatchPreviewRequest{ConflictIDs: []uint{first.ID, other.ID}}, "batch-key-parcels", reviewer)
	if !errors.As(err, &appErr) || appErr.Code != CodeInvalidInput || appErr.Status != 400 {
		t.Fatalf("cross-parcel batch error = %v, want 400 %s", err, CodeInvalidInput)
	}
}
