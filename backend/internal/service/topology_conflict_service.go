package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"cadastral-boundary-topology-resolution/backend/internal/constants"
	"cadastral-boundary-topology-resolution/backend/internal/dto"
	"cadastral-boundary-topology-resolution/backend/internal/geometry"
	"cadastral-boundary-topology-resolution/backend/internal/model"
	"cadastral-boundary-topology-resolution/backend/internal/repository"
)

func (s *CadastralService) DetectConflicts(req dto.DetectConflictRequest, idempotencyKey string, actor Actor) ([]model.TopologyConflict, error) {
	key := strings.TrimSpace(idempotencyKey)
	if key == "" || len(key) > 128 {
		return nil, invalid("Idempotency-Key must contain between 1 and 128 characters", nil)
	}
	proposal, err := s.store.Proposals.Get(req.ProposalID)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, notFound("proposal")
	}
	if err != nil {
		return nil, internal("load proposal failed", err)
	}
	tolerance := req.SnapToleranceM
	if tolerance == 0 {
		tolerance = proposal.SnapToleranceM
	}
	requestHash := geometry.Hash(strconv.FormatUint(uint64(req.ProposalID), 10), fmt.Sprintf("%.6f", tolerance))
	if existing, findErr := s.store.DetectionRuns.GetByActorKey(actor.ID, key); findErr == nil {
		return s.replayDetectionRun(existing, requestHash)
	} else if !errors.Is(findErr, repository.ErrNotFound) {
		return nil, internal("check conflict idempotency failed", findErr)
	}

	parcel, err := s.store.Parcels.Get(proposal.ParcelID)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, notFound("parcel")
	}
	if err != nil {
		return nil, internal("load parcel failed", err)
	}
	base, baseErr := geometry.ParsePolygon(parcel.BoundaryGeoJSON)
	if baseErr != nil {
		return nil, geoInvalid(baseErr)
	}
	proposed, proposedErr := geometry.ParsePolygon(proposal.ProposedGeoJSON)
	if proposedErr != nil {
		return nil, geoInvalid(proposedErr)
	}
	neighbourModels, neighbourErr := s.store.Parcels.ListActiveByCoordinateSystem(parcel.CoordinateSystem, parcel.ID)
	if neighbourErr != nil {
		return nil, internal("load neighbouring parcels failed", neighbourErr)
	}
	neighbours := make([]geometry.ParcelReference, 0, len(neighbourModels))
	references := []geometry.Polygon{base}
	hashParts := []string{parcel.BoundaryGeoJSON, proposal.ProposedGeoJSON, parcel.CoordinateSystem, fmt.Sprintf("%.6f", tolerance), geometry.AlgorithmVersion}
	for _, neighbour := range neighbourModels {
		polygon, parseErr := geometry.ParsePolygon(neighbour.BoundaryGeoJSON)
		if parseErr != nil {
			return nil, internal(fmt.Sprintf("stored geometry for neighbouring parcel %d is invalid", neighbour.ID), parseErr)
		}
		neighbours = append(neighbours, geometry.ParcelReference{ID: neighbour.ID, Polygon: polygon})
		references = append(references, polygon)
		hashParts = append(hashParts, strconv.FormatUint(uint64(neighbour.ID), 10), neighbour.BoundaryGeoJSON)
	}
	sort.Slice(neighbours, func(i, j int) bool { return neighbours[i].ID < neighbours[j].ID })
	snapped, snapErr := geometry.SnapToReferences(proposed, references, tolerance)
	if snapErr != nil {
		return nil, geoInvalid(snapErr)
	}
	findings, detectErr := geometry.DetectTopology(base, snapped.Polygon, neighbours, tolerance)
	if detectErr != nil {
		return nil, internal("detect topology conflicts failed", detectErr)
	}
	inputHash := geometry.Hash(hashParts...)
	suggestedJSON, marshalErr := json.Marshal(map[string]any{
		"action": "review_snapped_boundary", "snapped_geojson": json.RawMessage(snapped.SuggestedGeoJSON), "snap_changes": snapped.Changes,
		"tolerance_m": tolerance, "coordinate_system": parcel.CoordinateSystem, "algorithm_version": geometry.AlgorithmVersion, "topology_input_hash": inputHash,
	})
	if marshalErr != nil {
		return nil, internal("encode topology suggestion failed", marshalErr)
	}
	items := make([]model.TopologyConflict, 0, len(findings))
	now := time.Now().UTC()
	for _, finding := range findings {
		participantIDs := uniqueSortedIDs(append([]uint{parcel.ID}, finding.ParcelIDs...))
		parcelIDs, encodeErr := json.Marshal(participantIDs)
		if encodeErr != nil {
			return nil, internal("encode topology participants failed", encodeErr)
		}
		items = append(items, model.TopologyConflict{
			ProposalID: proposal.ID, ParcelIDs: string(parcelIDs), ConflictType: constants.ConflictType(finding.ConflictType), GeometryGeoJSON: finding.Geometry,
			MagnitudeSquareM: finding.Magnitude, Severity: conflictSeverity(finding.ConflictType, finding.Magnitude, tolerance), AlgorithmVersion: geometry.AlgorithmVersion,
			InputHash: inputHash, ConflictState: constants.ConflictDetected, SuggestedResolutionJSON: string(suggestedJSON), Explanation: finding.Explanation, DetectedAt: now,
		})
	}
	err = s.store.Transaction(func(tx *repository.Store) error {
		for index := range items {
			if createErr := tx.Conflicts.Create(&items[index]); createErr != nil {
				return createErr
			}
			if auditErr := tx.Audits.Create(audit(actor, "conflict.detected", "TopologyConflict", items[index].ID, &parcel.ID, "{}", snapshot(items[index]))); auditErr != nil {
				return auditErr
			}
		}
		resultIDs := make([]uint, 0, len(items))
		for _, item := range items {
			resultIDs = append(resultIDs, item.ID)
		}
		encodedIDs, encodeErr := json.Marshal(resultIDs)
		if encodeErr != nil {
			return encodeErr
		}
		run := model.TopologyDetectionRun{ProposalID: proposal.ID, ActorID: actor.ID, IdempotencyKey: key, RequestHash: requestHash, InputHash: inputHash, ResultIDs: string(encodedIDs)}
		if createErr := tx.DetectionRuns.Create(&run); createErr != nil {
			return createErr
		}
		return tx.Audits.Create(audit(actor, "conflict.detection_completed", "TopologyDetectionRun", run.ID, &proposal.ID, "{}", snapshot(run)))
	})
	if err != nil {
		if existing, findErr := s.store.DetectionRuns.GetByActorKey(actor.ID, key); findErr == nil {
			return s.replayDetectionRun(existing, requestHash)
		}
		return nil, wrapCadastral(err, "detect conflicts failed")
	}
	return items, nil
}

func (s *CadastralService) ListConflicts(q dto.ConflictQuery) ([]model.TopologyConflict, dto.Pagination, error) {
	normalizePage(&q.Page, &q.PageSize)
	items, total, err := s.store.Conflicts.List(q)
	if err != nil {
		return nil, dto.Pagination{}, internal("list conflicts failed", err)
	}
	return items, dto.Pagination{Page: q.Page, PageSize: q.PageSize, Total: total}, nil
}

func (s *CadastralService) GetConflict(id uint) (model.TopologyConflict, error) {
	item, err := s.store.Conflicts.Get(id)
	if errors.Is(err, repository.ErrNotFound) {
		return item, notFound("conflict")
	}
	if err != nil {
		return item, internal("get conflict failed", err)
	}
	return item, nil
}

func (s *CadastralService) TransitionConflict(id uint, req dto.ConflictTransitionRequest, actor Actor) (model.TopologyConflict, error) {
	item, err := s.store.Conflicts.Get(id)
	if errors.Is(err, repository.ErrNotFound) {
		return item, notFound("conflict")
	}
	if err != nil {
		return item, internal("get conflict failed", err)
	}
	if !constants.CanConflictTransition(item.ConflictState, req.To) {
		return item, conflict("conflict state transition is not allowed", nil)
	}
	var resolved *uint
	if req.To == constants.ConflictResolved || req.To == constants.ConflictClosed {
		resolved = &actor.ID
	}
	err = s.store.Transaction(func(tx *repository.Store) error {
		if transitionErr := tx.Conflicts.Transition(id, item.ConflictState, req.To, resolved); transitionErr != nil {
			return transitionErr
		}
		return tx.Audits.Create(audit(actor, "conflict.state_changed", "TopologyConflict", id, nil, snapshot(item), snapshot(map[string]any{"state": req.To})))
	})
	if err != nil {
		return item, conflict("conflict changed while transitioning", err)
	}
	item.ConflictState = req.To
	item.ResolvedBy = resolved
	return item, nil
}

func (s *CadastralService) ApplyConflictSuggestion(id uint, req dto.ApplySuggestionRequest, actor Actor) (model.BoundaryProposal, error) {
	if err := requireAnyRole(actor, constants.RoleReviewer, constants.RoleAdmin); err != nil {
		return model.BoundaryProposal{}, err
	}
	item, err := s.store.Conflicts.Get(id)
	if errors.Is(err, repository.ErrNotFound) {
		return model.BoundaryProposal{}, notFound("conflict")
	}
	if err != nil {
		return model.BoundaryProposal{}, internal("get conflict failed", err)
	}
	if item.ConflictState != constants.ConflictResolutionProposed {
		return model.BoundaryProposal{}, conflict("a suggestion can only be applied from resolution_proposed", nil)
	}
	proposal, err := s.store.Proposals.Get(item.ProposalID)
	if errors.Is(err, repository.ErrNotFound) {
		return model.BoundaryProposal{}, notFound("proposal")
	}
	if err != nil {
		return model.BoundaryProposal{}, internal("load proposal failed", err)
	}
	var suggestion struct {
		SnappedGeoJSON json.RawMessage `json:"snapped_geojson"`
	}
	if err := json.Unmarshal([]byte(item.SuggestedResolutionJSON), &suggestion); err != nil || len(suggestion.SnappedGeoJSON) == 0 {
		return model.BoundaryProposal{}, conflict("the conflict has no usable snapped-boundary suggestion", err)
	}
	polygon, parseErr := geometry.ParsePolygon(string(suggestion.SnappedGeoJSON))
	if parseErr != nil {
		return model.BoundaryProposal{}, internal("stored suggested geometry is invalid", parseErr)
	}
	parcel, parcelErr := s.store.Parcels.Get(proposal.ParcelID)
	if errors.Is(parcelErr, repository.ErrNotFound) {
		return model.BoundaryProposal{}, notFound("parcel")
	}
	if parcelErr != nil {
		return model.BoundaryProposal{}, internal("load parcel failed", parcelErr)
	}
	rationale := strings.TrimSpace(req.Rationale)
	if rationale == "" {
		rationale = fmt.Sprintf("Applied deterministic suggestion from topology conflict %d.", item.ID)
	}
	derived := model.BoundaryProposal{
		ParcelID: proposal.ParcelID, BaseVersion: parcel.BoundaryVersion, ProposedGeoJSON: string(suggestion.SnappedGeoJSON), ObservationIDs: proposal.ObservationIDs,
		SnapToleranceM: proposal.SnapToleranceM, AreaDeltaSquareM: polygon.Area - parcel.AreaSquareM, ProposalState: constants.ProposalDraft,
		Rationale: rationale, Version: proposal.Version + 1, CreatedBy: actor.ID,
	}
	resolvedBy := actor.ID
	err = s.store.Transaction(func(tx *repository.Store) error {
		if createErr := tx.Proposals.Create(&derived); createErr != nil {
			return createErr
		}
		if transitionErr := tx.Conflicts.Transition(item.ID, item.ConflictState, constants.ConflictResolved, &resolvedBy); transitionErr != nil {
			return transitionErr
		}
		if auditErr := tx.Audits.Create(audit(actor, "proposal.created_from_conflict", "BoundaryProposal", derived.ID, &derived.ParcelID, "{}", snapshot(derived))); auditErr != nil {
			return auditErr
		}
		return tx.Audits.Create(audit(actor, "conflict.suggestion_applied", "TopologyConflict", item.ID, &derived.ID, snapshot(item), snapshot(map[string]any{"conflict_state": constants.ConflictResolved, "proposal_id": derived.ID})))
	})
	if err != nil {
		return model.BoundaryProposal{}, wrapCadastral(err, "apply conflict suggestion failed")
	}
	return derived, nil
}

func (s *CadastralService) replayDetectionRun(run model.TopologyDetectionRun, requestHash string) ([]model.TopologyConflict, error) {
	if run.RequestHash != requestHash {
		return nil, conflict("Idempotency-Key has already been used with a different request", nil)
	}
	var resultIDs []uint
	if err := json.Unmarshal([]byte(run.ResultIDs), &resultIDs); err != nil {
		return nil, internal("stored idempotency result is invalid", err)
	}
	items, err := s.store.Conflicts.ListByIDs(resultIDs)
	if err != nil {
		return nil, internal("load idempotent detection result failed", err)
	}
	return items, nil
}

func canTransitionObservation(from, to string) bool {
	return (from == "accepted" && (to == "rejected" || to == "superseded")) || (from == "rejected" && to == "accepted")
}

func requireAnyRole(actor Actor, roles ...string) error {
	for _, role := range roles {
		if actor.Role == role {
			return nil
		}
	}
	return &AppError{CodeForbidden, http.StatusForbidden, "role is not permitted for this operation", nil}
}

func conflictSeverity(kind string, magnitude, tolerance float64) string {
	if kind == string(constants.ConflictOverlap) && magnitude > 100 {
		return "high"
	}
	if kind == string(constants.ConflictGap) && magnitude > tolerance*10 {
		return "high"
	}
	return "medium"
}

func uniqueSortedIDs(ids []uint) []uint {
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	output := ids[:0]
	for _, id := range ids {
		if len(output) == 0 || output[len(output)-1] != id {
			output = append(output, id)
		}
	}
	return output
}

func geoInvalid(err error) error {
	return &AppError{CodeInvalidInput, http.StatusUnprocessableEntity, "geometry or coordinate system is invalid: " + err.Error(), err}
}

func wrapCadastral(err error, message string) error {
	var appErr *AppError
	if errors.As(err, &appErr) {
		return err
	}
	return internal(message, err)
}

// PreviewConflictBatch freezes the parcel versions and suggestion hashes of
// the selected confirmed conflicts into an unfinished batch. Replaying the
// same actor/key/request returns the original batch; reusing the key with a
// different conflict set is rejected.
func (s *CadastralService) PreviewConflictBatch(req dto.ConflictBatchPreviewRequest, idempotencyKey string, actor Actor) (dto.ConflictBatchView, error) {
	if err := requireAnyRole(actor, constants.RoleReviewer, constants.RoleAdmin); err != nil {
		return dto.ConflictBatchView{}, err
	}
	key := strings.TrimSpace(idempotencyKey)
	if key == "" || len(key) > 128 {
		return dto.ConflictBatchView{}, invalid("Idempotency-Key must contain between 1 and 128 characters", nil)
	}
	conflictIDs := uniqueSortedIDs(req.ConflictIDs)
	if len(conflictIDs) < 2 {
		return dto.ConflictBatchView{}, invalid("a conflict batch requires at least two distinct conflicts", nil)
	}
	requestHash := conflictBatchRequestHash(conflictIDs)
	if existing, findErr := s.store.ConflictBatches.GetByActorKey(actor.ID, key); findErr == nil {
		return s.replayConflictBatch(existing, requestHash)
	} else if !errors.Is(findErr, repository.ErrNotFound) {
		return dto.ConflictBatchView{}, internal("check conflict batch idempotency failed", findErr)
	}

	conflicts, err := s.store.Conflicts.ListByIDs(conflictIDs)
	if errors.Is(err, repository.ErrNotFound) {
		return dto.ConflictBatchView{}, notFound("conflict")
	}
	if err != nil {
		return dto.ConflictBatchView{}, internal("load batch conflicts failed", err)
	}
	parcelID := uint(0)
	for _, item := range conflicts {
		if item.ConflictState != constants.ConflictConfirmed {
			return dto.ConflictBatchView{}, conflict(fmt.Sprintf("conflict #%d is %s; only confirmed conflicts can join a batch", item.ID, item.ConflictState), nil)
		}
		proposal, proposalErr := s.store.Proposals.Get(item.ProposalID)
		if errors.Is(proposalErr, repository.ErrNotFound) {
			return dto.ConflictBatchView{}, notFound("proposal")
		}
		if proposalErr != nil {
			return dto.ConflictBatchView{}, internal("load batch proposal failed", proposalErr)
		}
		if parcelID == 0 {
			parcelID = proposal.ParcelID
		} else if proposal.ParcelID != parcelID {
			return dto.ConflictBatchView{}, invalid("all conflicts in a batch must belong to the same parcel", nil)
		}
	}

	items := make([]model.ConflictResolutionBatchItem, 0, len(conflicts))
	for _, item := range conflicts {
		versions, freezeErr := s.freezeParcelVersions(item, parcelID)
		if freezeErr != nil {
			return dto.ConflictBatchView{}, freezeErr
		}
		versionsJSON, encodeErr := json.Marshal(versions)
		if encodeErr != nil {
			return dto.ConflictBatchView{}, internal("encode frozen parcel versions failed", encodeErr)
		}
		if _, suggestionErr := conflictSnappedGeoJSON(item); suggestionErr != nil {
			return dto.ConflictBatchView{}, suggestionErr
		}
		items = append(items, model.ConflictResolutionBatchItem{
			ConflictID: item.ID, ParcelVersionsJSON: string(versionsJSON), SuggestionHash: geometry.Hash(item.SuggestedResolutionJSON),
		})
	}
	batch := model.ConflictResolutionBatch{
		ParcelID: parcelID, ActorID: actor.ID, IdempotencyKey: key, RequestHash: requestHash,
		BatchState: constants.ConflictBatchPreviewed, FailureReasons: "[]",
	}
	err = s.store.Transaction(func(tx *repository.Store) error {
		if createErr := tx.ConflictBatches.Create(&batch, &items); createErr != nil {
			return createErr
		}
		return tx.Audits.Create(audit(actor, "conflict_batch.previewed", "ConflictResolutionBatch", batch.ID, &parcelID, "{}", snapshot(map[string]any{"batch": batch, "items": items})))
	})
	if err != nil {
		if existing, findErr := s.store.ConflictBatches.GetByActorKey(actor.ID, key); findErr == nil {
			return s.replayConflictBatch(existing, requestHash)
		}
		return dto.ConflictBatchView{}, wrapCadastral(err, "preview conflict batch failed")
	}
	return s.conflictBatchView(batch.ID)
}

// SubmitConflictBatch validates the frozen inputs and, only when every item is
// still valid, creates one draft proposal per conflict and advances the
// conflicts inside a single transaction. Any staleness fails the whole batch
// without touching conflicts, proposals, or their counts.
func (s *CadastralService) SubmitConflictBatch(id uint, actor Actor) (dto.ConflictBatchView, error) {
	if err := requireAnyRole(actor, constants.RoleReviewer, constants.RoleAdmin); err != nil {
		return dto.ConflictBatchView{}, err
	}
	batch, err := s.store.ConflictBatches.Get(id)
	if errors.Is(err, repository.ErrNotFound) {
		return dto.ConflictBatchView{}, notFound("conflict batch")
	}
	if err != nil {
		return dto.ConflictBatchView{}, internal("load conflict batch failed", err)
	}
	if batch.BatchState == constants.ConflictBatchCompleted {
		return s.conflictBatchView(id)
	}
	if batch.BatchState == constants.ConflictBatchFailed {
		return dto.ConflictBatchView{}, conflict("conflict batch has already failed; generate a new preview", nil)
	}
	items, err := s.store.ConflictBatches.ListItems(id)
	if err != nil {
		return dto.ConflictBatchView{}, internal("load conflict batch items failed", err)
	}
	evaluation := s.evaluateConflictBatch(batch, items)
	now := time.Now().UTC()
	if !evaluation.submittable {
		reasonsJSON, encodeErr := json.Marshal(evaluation.reasons)
		if encodeErr != nil {
			return dto.ConflictBatchView{}, internal("encode conflict batch failure reasons failed", encodeErr)
		}
		err = s.store.Transaction(func(tx *repository.Store) error {
			if failErr := tx.ConflictBatches.Fail(id, string(reasonsJSON), now); failErr != nil {
				return failErr
			}
			return tx.Audits.Create(audit(actor, "conflict_batch.failed", "ConflictResolutionBatch", id, &batch.ParcelID, "{}", snapshot(map[string]any{"reasons": evaluation.reasons})))
		})
		if err != nil {
			return dto.ConflictBatchView{}, wrapCadastral(err, "fail conflict batch failed")
		}
		return dto.ConflictBatchView{}, conflict("conflict batch is no longer submittable: "+strings.Join(evaluation.reasons, "; "), nil)
	}

	err = s.store.Transaction(func(tx *repository.Store) error {
		for _, item := range items {
			if applyErr := s.applyBatchItem(tx, batch, item, actor); applyErr != nil {
				return applyErr
			}
		}
		if completeErr := tx.ConflictBatches.Complete(id, now); completeErr != nil {
			return completeErr
		}
		return tx.Audits.Create(audit(actor, "conflict_batch.completed", "ConflictResolutionBatch", id, &batch.ParcelID, "{}", snapshot(map[string]any{"batch_state": constants.ConflictBatchCompleted, "conflict_count": len(items)})))
	})
	if err != nil {
		return dto.ConflictBatchView{}, wrapCadastral(err, "submit conflict batch failed")
	}
	return s.conflictBatchView(id)
}

func (s *CadastralService) GetConflictBatch(id uint) (dto.ConflictBatchView, error) {
	if _, err := s.store.ConflictBatches.Get(id); errors.Is(err, repository.ErrNotFound) {
		return dto.ConflictBatchView{}, notFound("conflict batch")
	} else if err != nil {
		return dto.ConflictBatchView{}, internal("load conflict batch failed", err)
	}
	return s.conflictBatchView(id)
}

func (s *CadastralService) ListConflictBatches(q dto.ConflictBatchQuery) ([]dto.ConflictBatchView, dto.Pagination, error) {
	normalizePage(&q.Page, &q.PageSize)
	batches, total, err := s.store.ConflictBatches.List(q)
	if err != nil {
		return nil, dto.Pagination{}, internal("list conflict batches failed", err)
	}
	views := make([]dto.ConflictBatchView, 0, len(batches))
	for _, batch := range batches {
		view, viewErr := s.conflictBatchView(batch.ID)
		if viewErr != nil {
			return nil, dto.Pagination{}, viewErr
		}
		views = append(views, view)
	}
	return views, dto.Pagination{Page: q.Page, PageSize: q.PageSize, Total: total}, nil
}

// applyBatchItem runs inside the submit transaction. Frozen versions, the
// suggestion hash, and the conflict state are re-verified so any concurrent
// change rolls the whole batch back.
func (s *CadastralService) applyBatchItem(tx *repository.Store, batch model.ConflictResolutionBatch, item model.ConflictResolutionBatchItem, actor Actor) error {
	finding, err := tx.Conflicts.Get(item.ConflictID)
	if err != nil {
		return err
	}
	if geometry.Hash(finding.SuggestedResolutionJSON) != item.SuggestionHash {
		return conflict(fmt.Sprintf("conflict #%d suggestion changed since preview", item.ConflictID), nil)
	}
	var frozen map[uint]uint
	if err := json.Unmarshal([]byte(item.ParcelVersionsJSON), &frozen); err != nil {
		return internal("stored frozen parcel versions are invalid", err)
	}
	for parcelID, version := range frozen {
		parcel, parcelErr := tx.Parcels.Get(parcelID)
		if parcelErr != nil {
			return parcelErr
		}
		if parcel.BoundaryVersion != version {
			return conflict(fmt.Sprintf("parcel #%d boundary version changed since preview", parcelID), nil)
		}
	}
	snapped, err := conflictSnappedGeoJSON(finding)
	if err != nil {
		return err
	}
	polygon, parseErr := geometry.ParsePolygon(snapped)
	if parseErr != nil {
		return internal("stored suggested geometry is invalid", parseErr)
	}
	proposal, err := tx.Proposals.Get(finding.ProposalID)
	if err != nil {
		return err
	}
	parcel, err := tx.Parcels.Get(proposal.ParcelID)
	if err != nil {
		return err
	}
	derived := model.BoundaryProposal{
		ParcelID: parcel.ID, BaseVersion: parcel.BoundaryVersion, ProposedGeoJSON: snapped, ObservationIDs: proposal.ObservationIDs,
		SnapToleranceM: proposal.SnapToleranceM, AreaDeltaSquareM: polygon.Area - parcel.AreaSquareM, ProposalState: constants.ProposalDraft,
		Rationale: fmt.Sprintf("Batch disposition of topology conflict #%d (batch #%d).", finding.ID, batch.ID), Version: proposal.Version + 1, CreatedBy: actor.ID,
	}
	if err := tx.Proposals.Create(&derived); err != nil {
		return err
	}
	if err := tx.Conflicts.Transition(finding.ID, constants.ConflictConfirmed, constants.ConflictResolutionProposed, nil); err != nil {
		return err
	}
	if err := tx.Conflicts.Transition(finding.ID, constants.ConflictResolutionProposed, constants.ConflictResolved, &actor.ID); err != nil {
		return err
	}
	if err := tx.ConflictBatches.SetItemProposal(item.ID, derived.ID); err != nil {
		return err
	}
	if err := tx.Audits.Create(audit(actor, "proposal.created_from_conflict", "BoundaryProposal", derived.ID, &derived.ParcelID, "{}", snapshot(derived))); err != nil {
		return err
	}
	return tx.Audits.Create(audit(actor, "conflict.batch_resolved", "TopologyConflict", finding.ID, &derived.ID, snapshot(finding), snapshot(map[string]any{"conflict_state": constants.ConflictResolved, "proposal_id": derived.ID, "batch_id": batch.ID})))
}

type conflictBatchEvaluation struct {
	submittable bool
	reasons     []string
	itemReasons map[uint][]string
}

// evaluateConflictBatch recomputes whether a previewed batch can still be
// submitted: no conflict may have been processed, no frozen parcel version may
// have moved, no suggestion may have changed, and no other unfinished batch
// may cover the same conflicts.
func (s *CadastralService) evaluateConflictBatch(batch model.ConflictResolutionBatch, items []model.ConflictResolutionBatchItem) conflictBatchEvaluation {
	evaluation := conflictBatchEvaluation{submittable: true, itemReasons: map[uint][]string{}}
	add := func(conflictID uint, reason string) {
		evaluation.submittable = false
		evaluation.itemReasons[conflictID] = append(evaluation.itemReasons[conflictID], reason)
	}
	conflictIDs := make([]uint, 0, len(items))
	for _, item := range items {
		conflictIDs = append(conflictIDs, item.ConflictID)
		finding, err := s.store.Conflicts.Get(item.ConflictID)
		if errors.Is(err, repository.ErrNotFound) {
			add(item.ConflictID, fmt.Sprintf("conflict #%d no longer exists", item.ConflictID))
			continue
		}
		if err != nil {
			add(item.ConflictID, fmt.Sprintf("conflict #%d could not be re-read", item.ConflictID))
			continue
		}
		if finding.ConflictState != constants.ConflictConfirmed {
			add(item.ConflictID, fmt.Sprintf("conflict #%d has been processed (current state: %s)", item.ConflictID, finding.ConflictState))
		}
		if geometry.Hash(finding.SuggestedResolutionJSON) != item.SuggestionHash {
			add(item.ConflictID, fmt.Sprintf("conflict #%d suggestion hash changed since preview", item.ConflictID))
		}
		var frozen map[uint]uint
		if err := json.Unmarshal([]byte(item.ParcelVersionsJSON), &frozen); err != nil {
			add(item.ConflictID, fmt.Sprintf("conflict #%d frozen parcel versions are unreadable", item.ConflictID))
			continue
		}
		for _, parcelID := range sortedKeys(frozen) {
			parcel, parcelErr := s.store.Parcels.Get(parcelID)
			if errors.Is(parcelErr, repository.ErrNotFound) {
				add(item.ConflictID, fmt.Sprintf("parcel #%d no longer exists", parcelID))
				continue
			}
			if parcelErr != nil {
				add(item.ConflictID, fmt.Sprintf("parcel #%d could not be re-read", parcelID))
				continue
			}
			if parcel.BoundaryVersion != frozen[parcelID] {
				add(item.ConflictID, fmt.Sprintf("parcel #%d boundary version changed (frozen %d, current %d)", parcelID, frozen[parcelID], parcel.BoundaryVersion))
			}
		}
	}
	coverage, coverageErr := s.store.ConflictBatches.UnfinishedCoverage(conflictIDs, batch.ID)
	if coverageErr != nil {
		for _, item := range items {
			add(item.ConflictID, "unfinished-batch coverage could not be verified")
		}
	} else {
		for _, covered := range coverage {
			add(covered.ConflictID, fmt.Sprintf("conflict #%d is covered by unfinished batch #%d", covered.ConflictID, covered.BatchID))
		}
	}
	seen := map[string]bool{}
	for _, item := range items {
		for _, reason := range evaluation.itemReasons[item.ConflictID] {
			if !seen[reason] {
				seen[reason] = true
				evaluation.reasons = append(evaluation.reasons, reason)
			}
		}
	}
	sort.Strings(evaluation.reasons)
	return evaluation
}

// conflictBatchView builds the read model. Previewed batches are evaluated
// live; terminal batches replay their stored outcome so a page refresh reads
// back the same state and reasons.
func (s *CadastralService) conflictBatchView(id uint) (dto.ConflictBatchView, error) {
	batch, err := s.store.ConflictBatches.Get(id)
	if errors.Is(err, repository.ErrNotFound) {
		return dto.ConflictBatchView{}, notFound("conflict batch")
	}
	if err != nil {
		return dto.ConflictBatchView{}, internal("load conflict batch failed", err)
	}
	items, err := s.store.ConflictBatches.ListItems(id)
	if err != nil {
		return dto.ConflictBatchView{}, internal("load conflict batch items failed", err)
	}
	view := dto.ConflictBatchView{
		ID: batch.ID, ParcelID: batch.ParcelID, BatchState: batch.BatchState, IdempotencyKey: batch.IdempotencyKey,
		Reasons: []string{}, Items: make([]dto.ConflictBatchItemView, 0, len(items)), CreatedAt: batch.CreatedAt, CompletedAt: batch.CompletedAt,
	}
	itemReasons := map[uint][]string{}
	if batch.BatchState == constants.ConflictBatchPreviewed {
		evaluation := s.evaluateConflictBatch(batch, items)
		view.Submittable = evaluation.submittable
		view.Reasons = evaluation.reasons
		itemReasons = evaluation.itemReasons
	} else if batch.BatchState == constants.ConflictBatchFailed {
		var reasons []string
		if err := json.Unmarshal([]byte(batch.FailureReasons), &reasons); err == nil {
			view.Reasons = reasons
		}
	}
	for _, item := range items {
		var versions map[uint]uint
		if err := json.Unmarshal([]byte(item.ParcelVersionsJSON), &versions); err != nil {
			return dto.ConflictBatchView{}, internal("stored frozen parcel versions are invalid", err)
		}
		reasons := itemReasons[item.ConflictID]
		if reasons == nil {
			reasons = []string{}
		}
		view.Items = append(view.Items, dto.ConflictBatchItemView{
			ConflictID: item.ConflictID, ProposalID: item.ProposalID, ParcelVersions: versions,
			SuggestionHash: item.SuggestionHash, Submittable: batch.BatchState == constants.ConflictBatchPreviewed && len(reasons) == 0, Reasons: reasons,
		})
	}
	return view, nil
}

func (s *CadastralService) replayConflictBatch(batch model.ConflictResolutionBatch, requestHash string) (dto.ConflictBatchView, error) {
	if batch.RequestHash != requestHash {
		return dto.ConflictBatchView{}, conflict("Idempotency-Key has already been used with a different request", nil)
	}
	return s.conflictBatchView(batch.ID)
}

// freezeParcelVersions snapshots the boundary version of every parcel
// participating in the conflict plus the anchor parcel of the batch.
func (s *CadastralService) freezeParcelVersions(item model.TopologyConflict, anchorParcelID uint) (map[uint]uint, error) {
	var participantIDs []uint
	if err := json.Unmarshal([]byte(item.ParcelIDs), &participantIDs); err != nil {
		return nil, internal("stored conflict participants are invalid", err)
	}
	versions := map[uint]uint{}
	for _, parcelID := range uniqueSortedIDs(append(participantIDs, anchorParcelID)) {
		parcel, err := s.store.Parcels.Get(parcelID)
		if errors.Is(err, repository.ErrNotFound) {
			return nil, notFound("parcel")
		}
		if err != nil {
			return nil, internal("load participating parcel failed", err)
		}
		versions[parcelID] = parcel.BoundaryVersion
	}
	return versions, nil
}

func conflictSnappedGeoJSON(item model.TopologyConflict) (string, error) {
	var suggestion struct {
		SnappedGeoJSON json.RawMessage `json:"snapped_geojson"`
	}
	if err := json.Unmarshal([]byte(item.SuggestedResolutionJSON), &suggestion); err != nil || len(suggestion.SnappedGeoJSON) == 0 {
		return "", conflict(fmt.Sprintf("conflict #%d has no usable snapped-boundary suggestion", item.ID), err)
	}
	return string(suggestion.SnappedGeoJSON), nil
}

func conflictBatchRequestHash(conflictIDs []uint) string {
	parts := make([]string, 0, len(conflictIDs)+1)
	parts = append(parts, "conflict-batch")
	for _, id := range conflictIDs {
		parts = append(parts, strconv.FormatUint(uint64(id), 10))
	}
	return geometry.Hash(parts...)
}

func sortedKeys(versions map[uint]uint) []uint {
	keys := make([]uint, 0, len(versions))
	for key := range versions {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}
