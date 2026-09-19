package service

import (
	"crypto/rand"
	"encoding/hex"
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
	return &AppError{Code: CodeForbidden, Status: http.StatusForbidden, Message: "role is not permitted for this operation"}
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
	return &AppError{Code: CodeInvalidInput, Status: http.StatusUnprocessableEntity, Message: "geometry or coordinate system is invalid: " + err.Error(), Err: err}
}

func wrapCadastral(err error, message string) error {
	var appErr *AppError
	if errors.As(err, &appErr) {
		return err
	}
	return internal(message, err)
}

// Per-conflict reason codes returned when a frozen batch can no longer be
// submitted. They are part of the API contract and mirrored by the frontend.
const (
	batchReasonConflictProcessed = "conflict_processed"
	batchReasonParcelVersion     = "parcel_version_changed"
	batchReasonSuggestionChanged = "suggestion_changed"
	batchReasonOpenBatchCoverage = "covered_by_open_batch"
)

// CreateResolutionBatch freezes confirmed conflicts of one parcel into a
// preview batch. The preview never mutates conflicts or proposals; it only
// records the parcel boundary version and suggestion hash of every line.
func (s *CadastralService) CreateResolutionBatch(req dto.CreateResolutionBatchRequest, actor Actor) (dto.ResolutionBatchView, error) {
	if err := requireAnyRole(actor, constants.RoleReviewer, constants.RoleAdmin); err != nil {
		return dto.ResolutionBatchView{}, err
	}
	conflictIDs := uniqueSortedIDs(append([]uint{}, req.ConflictIDs...))
	if len(conflictIDs) == 0 {
		return dto.ResolutionBatchView{}, invalid("at least one conflict must be selected", nil)
	}
	parcel, err := s.store.Parcels.Get(req.ParcelID)
	if errors.Is(err, repository.ErrNotFound) {
		return dto.ResolutionBatchView{}, notFound("parcel")
	}
	if err != nil {
		return dto.ResolutionBatchView{}, internal("load parcel failed", err)
	}
	invalidReasons := map[string]any{}
	items := make([]model.ConflictResolutionBatchItem, 0, len(conflictIDs))
	for _, conflictID := range conflictIDs {
		item, reason, appErr := s.buildBatchItem(conflictID, parcel, req.ParcelID)
		if appErr != nil {
			return dto.ResolutionBatchView{}, appErr
		}
		if reason != "" {
			invalidReasons[strconv.FormatUint(uint64(conflictID), 10)] = reason
			continue
		}
		items = append(items, item)
	}
	if len(invalidReasons) > 0 {
		return dto.ResolutionBatchView{}, conflictDetails("only confirmed conflicts of the selected parcel can be batched", invalidReasons, nil)
	}
	code, codeErr := newBatchCode()
	if codeErr != nil {
		return dto.ResolutionBatchView{}, internal("generate batch code failed", codeErr)
	}
	now := time.Now().UTC()
	batch := model.ConflictResolutionBatch{
		BatchCode: code, ParcelID: req.ParcelID, BatchState: string(constants.BatchPreview),
		ConflictIDs: encodeIDs(conflictIDs), Rationale: strings.TrimSpace(req.Rationale), CreatedBy: actor.ID,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.store.Transaction(func(tx *repository.Store) error {
		if createErr := tx.ResolutionBatches.Create(&batch, items); createErr != nil {
			return createErr
		}
		return tx.Audits.Create(audit(actor, "conflict_batch.created", "ConflictResolutionBatch", batch.ID, &batch.ParcelID, "{}", snapshot(batch)))
	}); err != nil {
		return dto.ResolutionBatchView{}, wrapCadastral(err, "create resolution batch failed")
	}
	return s.assembleBatchView(batch, actor)
}

func (s *CadastralService) buildBatchItem(conflictID uint, parcel model.LandParcel, parcelID uint) (model.ConflictResolutionBatchItem, string, error) {
	item, err := s.store.Conflicts.Get(conflictID)
	if errors.Is(err, repository.ErrNotFound) {
		return model.ConflictResolutionBatchItem{}, "", notFound("conflict")
	}
	if err != nil {
		return model.ConflictResolutionBatchItem{}, "", internal("load conflict failed", err)
	}
	if item.ConflictState != constants.ConflictConfirmed {
		return model.ConflictResolutionBatchItem{}, batchReasonConflictProcessed, nil
	}
	// A conflict belongs to the parcel whose proposal produced it; the
	// participant list also contains neighbouring parcels that must not be
	// treated as owners.
	sourceProposal, proposalErr := s.store.Proposals.Get(item.ProposalID)
	if errors.Is(proposalErr, repository.ErrNotFound) {
		return model.ConflictResolutionBatchItem{}, "", notFound("proposal")
	}
	if proposalErr != nil {
		return model.ConflictResolutionBatchItem{}, "", internal("load conflict proposal failed", proposalErr)
	}
	if sourceProposal.ParcelID != parcelID {
		return model.ConflictResolutionBatchItem{}, "", invalid(fmt.Sprintf("conflict %d does not belong to parcel %d", conflictID, parcelID), nil)
	}
	suggestedGeoJSON, hash, suggestErr := conflictSuggestion(item)
	if suggestErr != nil {
		return model.ConflictResolutionBatchItem{}, "", suggestErr
	}
	if _, parseErr := geometry.ParsePolygon(suggestedGeoJSON); parseErr != nil {
		return model.ConflictResolutionBatchItem{}, "", internal("stored suggested geometry is invalid", parseErr)
	}
	return model.ConflictResolutionBatchItem{
		ConflictID: item.ID, ProposalID: item.ProposalID, ParcelID: parcel.ID,
		FrozenParcelVersion: parcel.BoundaryVersion, FrozenState: item.ConflictState,
		SuggestionHash: hash, SuggestedGeoJSON: suggestedGeoJSON,
	}, "", nil
}

// SubmitResolutionBatch validates every frozen line and, only when all lines
// are still valid, creates one draft proposal per conflict and advances each
// conflict in the same transaction. Any stale line fails the whole batch and
// leaves conflicts, proposals and counters untouched.
func (s *CadastralService) SubmitResolutionBatch(id uint, req dto.SubmitResolutionBatchRequest, idempotencyKey string, actor Actor) (dto.ResolutionBatchView, error) {
	if err := requireAnyRole(actor, constants.RoleReviewer, constants.RoleAdmin); err != nil {
		return dto.ResolutionBatchView{}, err
	}
	key := strings.TrimSpace(idempotencyKey)
	if key == "" || len(key) > 128 {
		return dto.ResolutionBatchView{}, invalid("Idempotency-Key must contain between 1 and 128 characters", nil)
	}
	batch, err := s.store.ResolutionBatches.Get(id)
	if errors.Is(err, repository.ErrNotFound) {
		return dto.ResolutionBatchView{}, notFound("resolution batch")
	}
	if err != nil {
		return dto.ResolutionBatchView{}, internal("load resolution batch failed", err)
	}
	requestHash := geometry.Hash(strconv.FormatUint(uint64(batch.ID), 10), strings.TrimSpace(req.Rationale))
	if existing, findErr := s.store.ResolutionBatchRuns.GetByActorKey(actor.ID, key); findErr == nil {
		return s.replayBatchRun(existing, batch, requestHash, actor)
	} else if !errors.Is(findErr, repository.ErrNotFound) {
		return dto.ResolutionBatchView{}, internal("check batch idempotency failed", findErr)
	}
	if batch.BatchState != string(constants.BatchPreview) {
		return dto.ResolutionBatchView{}, conflict("the resolution batch has already been submitted", nil)
	}

	items, err := s.store.ResolutionBatches.ListItems(batch.ID)
	if err != nil {
		return dto.ResolutionBatchView{}, internal("load batch items failed", err)
	}
	parcel, err := s.store.Parcels.Get(batch.ParcelID)
	if errors.Is(err, repository.ErrNotFound) {
		return dto.ResolutionBatchView{}, notFound("parcel")
	}
	if err != nil {
		return dto.ResolutionBatchView{}, internal("load parcel failed", err)
	}
	rationale := strings.TrimSpace(req.Rationale)
	if rationale == "" {
		rationale = strings.TrimSpace(batch.Rationale)
	}
	// Evaluate every frozen line before opening the write transaction so a
	// single failure never creates partial proposals or state changes.
	frozen, reasons, err := s.validateBatchItems(items, parcel)
	if err != nil {
		return dto.ResolutionBatchView{}, err
	}
	if len(reasons) > 0 {
		return dto.ResolutionBatchView{}, conflictDetails("the resolution batch is no longer valid and was not submitted", reasons, nil)
	}

	now := time.Now().UTC()
	createdProposals := make([]model.BoundaryProposal, 0, len(frozen))
	err = s.store.Transaction(func(tx *repository.Store) error {
		for _, line := range frozen {
			sourceProposal, sourceErr := tx.Proposals.Get(line.item.ProposalID)
			if errors.Is(sourceErr, repository.ErrNotFound) {
				return conflictDetails("source proposal vanished during submission", map[string]any{strconv.FormatUint(uint64(line.item.ConflictID), 10): batchReasonConflictProcessed}, sourceErr)
			}
			if sourceErr != nil {
				return sourceErr
			}
			polygon, parseErr := geometry.ParsePolygon(line.item.SuggestedGeoJSON)
			if parseErr != nil {
				return internal("stored suggested geometry is invalid", parseErr)
			}
			lineRationale := rationale
			if lineRationale == "" {
				lineRationale = fmt.Sprintf("Batch %s: applied deterministic suggestion from conflict %d.", batch.BatchCode, line.item.ConflictID)
			}
			proposal := model.BoundaryProposal{
				ParcelID: batch.ParcelID, BaseVersion: parcel.BoundaryVersion, ProposedGeoJSON: line.item.SuggestedGeoJSON,
				ObservationIDs: sourceProposal.ObservationIDs, SnapToleranceM: sourceProposal.SnapToleranceM,
				AreaDeltaSquareM: polygon.Area - parcel.AreaSquareM, ProposalState: constants.ProposalDraft,
				Rationale: lineRationale, Version: sourceProposal.Version + 1, CreatedBy: actor.ID,
			}
			if createErr := tx.Proposals.Create(&proposal); createErr != nil {
				return createErr
			}
			// Advance through the declared conflict state machine with
			// conditional updates so concurrent handling fails the batch.
			if transitionErr := tx.Conflicts.Transition(line.item.ConflictID, constants.ConflictConfirmed, constants.ConflictResolutionProposed, nil); transitionErr != nil {
				return batchLineConflict(line.item.ConflictID, transitionErr)
			}
			resolvedBy := actor.ID
			if transitionErr := tx.Conflicts.Transition(line.item.ConflictID, constants.ConflictResolutionProposed, constants.ConflictResolved, &resolvedBy); transitionErr != nil {
				return batchLineConflict(line.item.ConflictID, transitionErr)
			}
			createdProposals = append(createdProposals, proposal)
			if auditErr := tx.Audits.Create(audit(actor, "proposal.created_from_batch", "BoundaryProposal", proposal.ID, &batch.ParcelID, "{}", snapshot(proposal))); auditErr != nil {
				return auditErr
			}
			if auditErr := tx.Audits.Create(audit(actor, "conflict.batch_resolved", "TopologyConflict", line.item.ConflictID, &proposal.ID, snapshot(line.conflict), snapshot(map[string]any{"conflict_state": constants.ConflictResolved, "batch_id": batch.ID, "proposal_id": proposal.ID}))); auditErr != nil {
				return auditErr
			}
		}
		proposalIDs := encodeUintIDs(proposalIDList(createdProposals))
		if markErr := tx.ResolutionBatches.MarkSubmitted(batch.ID, actor.ID, proposalIDs, now); markErr != nil {
			return conflict("the resolution batch was finished by another request", markErr)
		}
		run := model.ConflictResolutionBatchRun{
			BatchID: batch.ID, ActorID: actor.ID, IdempotencyKey: key, RequestHash: requestHash, ProposalIDs: proposalIDs, CreatedAt: now,
		}
		if createErr := tx.ResolutionBatchRuns.Create(&run); createErr != nil {
			return createErr
		}
		return tx.Audits.Create(audit(actor, "conflict_batch.submitted", "ConflictResolutionBatch", batch.ID, &batch.ParcelID, snapshot(batch), snapshot(map[string]any{"batch_state": constants.BatchSubmitted, "proposal_ids": proposalIDList(createdProposals)})))
	})
	if err != nil {
		if existing, findErr := s.store.ResolutionBatchRuns.GetByActorKey(actor.ID, key); findErr == nil {
			reloaded, reloadErr := s.store.ResolutionBatches.Get(batch.ID)
			if reloadErr == nil {
				return s.replayBatchRun(existing, reloaded, requestHash, actor)
			}
		}
		return dto.ResolutionBatchView{}, wrapCadastral(err, "submit resolution batch failed")
	}
	batch.BatchState = string(constants.BatchSubmitted)
	batch.SubmittedBy = &actor.ID
	batch.SubmittedAt = &now
	batch.ProposalIDs = encodeUintIDs(proposalIDList(createdProposals))
	return s.assembleBatchView(batch, actor)
}

type validatedBatchLine struct {
	item     model.ConflictResolutionBatchItem
	conflict model.TopologyConflict
}

// validateBatchItems returns the frozen lines in order and a map of
// conflict-id → reason for every line that can no longer be submitted.
func (s *CadastralService) validateBatchItems(items []model.ConflictResolutionBatchItem, parcel model.LandParcel) ([]validatedBatchLine, map[string]any, error) {
	reasons := map[string]any{}
	lines := make([]validatedBatchLine, 0, len(items))
	for _, item := range items {
		key := strconv.FormatUint(uint64(item.ConflictID), 10)
		conflict, err := s.store.Conflicts.Get(item.ConflictID)
		if errors.Is(err, repository.ErrNotFound) {
			reasons[key] = batchReasonConflictProcessed
			continue
		}
		if err != nil {
			return nil, nil, internal("reload conflict failed", err)
		}
		if conflict.ConflictState != item.FrozenState {
			reasons[key] = batchReasonConflictProcessed
			continue
		}
		if parcel.BoundaryVersion != item.FrozenParcelVersion {
			reasons[key] = batchReasonParcelVersion
			continue
		}
		_, currentHash, hashErr := conflictSuggestion(conflict)
		if hashErr != nil || currentHash != item.SuggestionHash {
			reasons[key] = batchReasonSuggestionChanged
			continue
		}
		covered, coveredErr := s.store.ResolutionBatches.HasOpenItem(item.ConflictID, constants.BatchOpenStates, item.BatchID)
		if coveredErr != nil {
			return nil, nil, internal("check open batch coverage failed", coveredErr)
		}
		if covered {
			reasons[key] = batchReasonOpenBatchCoverage
			continue
		}
		lines = append(lines, validatedBatchLine{item: item, conflict: conflict})
	}
	return lines, reasons, nil
}

// CancelResolutionBatch discards a stale preview without modifying any
// conflict or proposal. Submitted or already cancelled batches cannot be
// cancelled again (409).
func (s *CadastralService) CancelResolutionBatch(id uint, actor Actor) (dto.ResolutionBatchView, error) {
	if err := requireAnyRole(actor, constants.RoleReviewer, constants.RoleAdmin); err != nil {
		return dto.ResolutionBatchView{}, err
	}
	batch, err := s.store.ResolutionBatches.Get(id)
	if errors.Is(err, repository.ErrNotFound) {
		return dto.ResolutionBatchView{}, notFound("resolution batch")
	}
	if err != nil {
		return dto.ResolutionBatchView{}, internal("load resolution batch failed", err)
	}
	if batch.BatchState != string(constants.BatchPreview) {
		return dto.ResolutionBatchView{}, conflict("only an open preview batch can be cancelled", nil)
	}
	now := time.Now().UTC()
	if err := s.store.Transaction(func(tx *repository.Store) error {
		if cancelErr := tx.ResolutionBatches.Cancel(batch.ID); cancelErr != nil {
			return conflict("the resolution batch changed while cancelling", cancelErr)
		}
		return tx.Audits.Create(audit(actor, "conflict_batch.cancelled", "ConflictResolutionBatch", batch.ID, &batch.ParcelID, snapshot(batch), snapshot(map[string]any{"batch_state": constants.BatchCancelled})))
	}); err != nil {
		return dto.ResolutionBatchView{}, wrapCadastral(err, "cancel resolution batch failed")
	}
	batch.BatchState = string(constants.BatchCancelled)
	batch.UpdatedAt = now
	return s.assembleBatchView(batch, actor)
}

func (s *CadastralService) replayBatchRun(run model.ConflictResolutionBatchRun, batch model.ConflictResolutionBatch, requestHash string, actor Actor) (dto.ResolutionBatchView, error) {
	if run.RequestHash != requestHash {
		return dto.ResolutionBatchView{}, conflict("Idempotency-Key has already been used with a different request", nil)
	}
	return s.assembleBatchView(batch, actor)
}

// GetResolutionBatch reads one preview or submitted batch back so a page
// refresh shows the same submittable state and invalidation reasons.
func (s *CadastralService) GetResolutionBatch(id uint) (dto.ResolutionBatchView, error) {
	batch, err := s.store.ResolutionBatches.Get(id)
	if errors.Is(err, repository.ErrNotFound) {
		return dto.ResolutionBatchView{}, notFound("resolution batch")
	}
	if err != nil {
		return dto.ResolutionBatchView{}, internal("load resolution batch failed", err)
	}
	return s.assembleBatchView(batch, Actor{})
}

// ListResolutionBatches returns batches for a parcel (or all batches) newest
// first so the conflict page can rehydrate after a refresh.
func (s *CadastralService) ListResolutionBatches(parcelID *uint) ([]dto.ResolutionBatchView, error) {
	batches, err := s.store.ResolutionBatches.List(parcelID)
	if err != nil {
		return nil, internal("list resolution batches failed", err)
	}
	views := make([]dto.ResolutionBatchView, 0, len(batches))
	for _, batch := range batches {
		view, viewErr := s.assembleBatchView(batch, Actor{})
		if viewErr != nil {
			return nil, viewErr
		}
		views = append(views, view)
	}
	return views, nil
}

func (s *CadastralService) assembleBatchView(batch model.ConflictResolutionBatch, _ Actor) (dto.ResolutionBatchView, error) {
	items, err := s.store.ResolutionBatches.ListItems(batch.ID)
	if err != nil {
		return dto.ResolutionBatchView{}, internal("load batch items failed", err)
	}
	parcel, err := s.store.Parcels.Get(batch.ParcelID)
	if err != nil {
		return dto.ResolutionBatchView{}, internal("load batch parcel failed", err)
	}
	view := dto.ResolutionBatchView{
		ID: batch.ID, BatchCode: batch.BatchCode, ParcelID: batch.ParcelID, BatchState: batch.BatchState,
		ConflictIDs: parseIDListOrEmpty(batch.ConflictIDs), ProposalIDs: parseIDListOrEmpty(batch.ProposalIDs),
		Rationale: batch.Rationale, CreatedBy: batch.CreatedBy, SubmittedBy: batch.SubmittedBy,
		CreatedAt: batch.CreatedAt, UpdatedAt: batch.UpdatedAt, SubmittedAt: batch.SubmittedAt,
		// Submittable means "the page is allowed to offer submit", which only
		// applies to a still-open preview. Submitted batches read back as a
		// finished, non-submittable outcome.
		Submittable: false,
	}
	var reasonMap map[string]any
	if batch.BatchState == string(constants.BatchPreview) {
		view.Submittable = true
		_, reasonMap, err = s.validateBatchItems(items, parcel)
		if err != nil {
			return dto.ResolutionBatchView{}, err
		}
		if len(reasonMap) > 0 {
			view.Submittable = false
			view.InvalidReason = summarizeBatchReasons(reasonMap)
			view.InvalidConflictIDs = sortedReasonKeys(reasonMap)
		}
	}
	view.Items = make([]dto.ResolutionBatchItemView, 0, len(items))
	for _, item := range items {
		line := dto.ResolutionBatchItemView{
			ConflictID: item.ConflictID, ProposalID: item.ProposalID, ParcelID: item.ParcelID,
			FrozenParcelVersion: item.FrozenParcelVersion, FrozenState: item.FrozenState,
			CurrentParcelVersion: parcel.BoundaryVersion, SuggestionHash: item.SuggestionHash,
			Applied: batch.BatchState == string(constants.BatchSubmitted),
		}
		current, conflictErr := s.store.Conflicts.Get(item.ConflictID)
		switch {
		case conflictErr == nil:
			line.CurrentState = current.ConflictState
		case errors.Is(conflictErr, repository.ErrNotFound):
			line.CurrentState = "missing"
		default:
			return dto.ResolutionBatchView{}, internal("reload batch conflict failed", conflictErr)
		}
		if batch.BatchState == string(constants.BatchPreview) {
			line.Submittable = true
			if reason, ok := reasonMap[strconv.FormatUint(uint64(item.ConflictID), 10)]; ok {
				reasonString, _ := reason.(string)
				line.Submittable = false
				line.ReasonCode = reasonString
				line.Reason = batchReasonMessage(reasonString)
			}
		}
		view.Items = append(view.Items, line)
	}
	return view, nil
}

// batchLineConflict turns a failed conditional conflict update inside the
// submission transaction into the same structured 409 as pre-transaction
// validation, so the HTTP contract reports a stale line rather than a 500.
func batchLineConflict(conflictID uint, err error) error {
	return conflictDetails("a conflict changed while submitting the batch", map[string]any{
		strconv.FormatUint(uint64(conflictID), 10): batchReasonConflictProcessed,
	}, err)
}

func conflictSuggestion(item model.TopologyConflict) (string, string, error) {
	var suggestion struct {
		SnappedGeoJSON json.RawMessage `json:"snapped_geojson"`
	}
	if err := json.Unmarshal([]byte(item.SuggestedResolutionJSON), &suggestion); err != nil || len(suggestion.SnappedGeoJSON) == 0 {
		return "", "", conflict("the conflict has no usable snapped-boundary suggestion", err)
	}
	geo := strings.TrimSpace(string(suggestion.SnappedGeoJSON))
	return geo, geometry.Hash(geo), nil
}

func newBatchCode() (string, error) {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "RB-" + strings.ToUpper(hex.EncodeToString(raw)), nil
}

func parseIDList(raw string) ([]uint, error) {
	var ids []uint
	if strings.TrimSpace(raw) == "" {
		return []uint{}, nil
	}
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return nil, err
	}
	return ids, nil
}

func parseIDListOrEmpty(raw string) []uint {
	ids, err := parseIDList(raw)
	if err != nil {
		return []uint{}
	}
	return ids
}

func encodeIDs(ids []uint) string { return string(mustJSON(uniqueSortedIDs(append([]uint{}, ids...)))) }

func encodeUintIDs(ids []uint) string { return string(mustJSON(ids)) }

func mustJSON(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage("[]")
	}
	return encoded
}

func proposalIDList(proposals []model.BoundaryProposal) []uint {
	ids := make([]uint, 0, len(proposals))
	for _, proposal := range proposals {
		ids = append(ids, proposal.ID)
	}
	return ids
}

func sortedReasonKeys(reasons map[string]any) []uint {
	ids := make([]uint, 0, len(reasons))
	for key := range reasons {
		if id, err := strconv.ParseUint(key, 10, 64); err == nil {
			ids = append(ids, uint(id))
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func summarizeBatchReasons(reasons map[string]any) string {
	codes := map[string]bool{}
	for _, value := range reasons {
		if code, ok := value.(string); ok {
			codes[code] = true
		}
	}
	order := []string{batchReasonConflictProcessed, batchReasonParcelVersion, batchReasonSuggestionChanged, batchReasonOpenBatchCoverage}
	messages := make([]string, 0, len(codes))
	for _, code := range order {
		if codes[code] {
			messages = append(messages, batchReasonMessage(code))
		}
	}
	return strings.Join(messages, "；")
}

func batchReasonMessage(code string) string {
	switch code {
	case batchReasonConflictProcessed:
		return "冲突已被其他流程处理"
	case batchReasonParcelVersion:
		return "地块边界版本已变化"
	case batchReasonSuggestionChanged:
		return "建议哈希与冻结时不一致"
	case batchReasonOpenBatchCoverage:
		return "该冲突已被其他未结束批次覆盖"
	default:
		return "批次行已失效"
	}
}
