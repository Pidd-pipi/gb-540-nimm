package repository

import (
	"errors"
	"fmt"
	"time"

	"cadastral-boundary-topology-resolution/backend/internal/constants"
	"cadastral-boundary-topology-resolution/backend/internal/dto"
	"cadastral-boundary-topology-resolution/backend/internal/model"
	"gorm.io/gorm"
)

// TopologyConflictRepository stores immutable findings and their lifecycle.
type TopologyConflictRepository struct{ db *gorm.DB }

func (r *TopologyConflictRepository) Create(item *model.TopologyConflict) error {
	if err := r.db.Create(item).Error; err != nil {
		return fmt.Errorf("create topology conflict: %w", err)
	}
	return nil
}

func (r *TopologyConflictRepository) Get(id uint) (model.TopologyConflict, error) {
	var item model.TopologyConflict
	if err := r.db.First(&item, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return item, ErrNotFound
		}
		return item, fmt.Errorf("get conflict: %w", err)
	}
	return item, nil
}

func (r *TopologyConflictRepository) ListByIDs(ids []uint) ([]model.TopologyConflict, error) {
	if len(ids) == 0 {
		return []model.TopologyConflict{}, nil
	}
	var items []model.TopologyConflict
	if err := r.db.Where("id IN ?", ids).Find(&items).Error; err != nil {
		return nil, fmt.Errorf("find topology conflicts by ids: %w", err)
	}
	byID := make(map[uint]model.TopologyConflict, len(items))
	for _, item := range items {
		byID[item.ID] = item
	}
	ordered := make([]model.TopologyConflict, 0, len(ids))
	for _, id := range ids {
		item, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("topology conflict %d missing from detection run: %w", id, ErrNotFound)
		}
		ordered = append(ordered, item)
	}
	return ordered, nil
}

func (r *TopologyConflictRepository) List(q dto.ConflictQuery) ([]model.TopologyConflict, int64, error) {
	db := r.db.Model(&model.TopologyConflict{})
	if q.ProposalID != nil {
		db = db.Where("proposal_id = ?", *q.ProposalID)
	}
	if q.State != "" {
		db = db.Where("conflict_state = ?", q.State)
	}
	if q.Type != "" {
		db = db.Where("conflict_type = ?", q.Type)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count conflicts: %w", err)
	}
	var items []model.TopologyConflict
	if err := db.Order("detected_at DESC").Offset((q.Page - 1) * q.PageSize).Limit(q.PageSize).Find(&items).Error; err != nil {
		return nil, 0, fmt.Errorf("list conflicts: %w", err)
	}
	return items, total, nil
}

func (r *TopologyConflictRepository) Transition(id uint, from, to string, resolvedBy *uint) error {
	updates := map[string]any{"conflict_state": to}
	if resolvedBy != nil {
		updates["resolved_by"] = resolvedBy
	}
	result := r.db.Model(&model.TopologyConflict{}).Where("id = ? AND conflict_state = ?", id, from).Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("transition conflict: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("conflict state changed: %w", gorm.ErrInvalidTransaction)
	}
	return nil
}

// TopologyDetectionRunRepository owns idempotency records for conflict detection.
type TopologyDetectionRunRepository struct{ db *gorm.DB }

func (r *TopologyDetectionRunRepository) GetByActorKey(actorID uint, key string) (model.TopologyDetectionRun, error) {
	var item model.TopologyDetectionRun
	if err := r.db.Where("actor_id = ? AND idempotency_key = ?", actorID, key).First(&item).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return item, ErrNotFound
		}
		return item, fmt.Errorf("find detection idempotency key: %w", err)
	}
	return item, nil
}

func (r *TopologyDetectionRunRepository) Create(item *model.TopologyDetectionRun) error {
	if err := r.db.Create(item).Error; err != nil {
		return fmt.Errorf("create topology detection run: %w", err)
	}
	return nil
}

// ConflictResolutionBatchRepository stores disposition batches and their
// frozen items. Batches bind an actor and Idempotency-Key to one preview.
type ConflictResolutionBatchRepository struct{ db *gorm.DB }

func (r *ConflictResolutionBatchRepository) Create(batch *model.ConflictResolutionBatch, items *[]model.ConflictResolutionBatchItem) error {
	if err := r.db.Create(batch).Error; err != nil {
		return fmt.Errorf("create conflict resolution batch: %w", err)
	}
	for index := range *items {
		(*items)[index].BatchID = batch.ID
	}
	if err := r.db.Create(items).Error; err != nil {
		return fmt.Errorf("create conflict resolution batch items: %w", err)
	}
	return nil
}

func (r *ConflictResolutionBatchRepository) Get(id uint) (model.ConflictResolutionBatch, error) {
	var item model.ConflictResolutionBatch
	if err := r.db.First(&item, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return item, ErrNotFound
		}
		return item, fmt.Errorf("get conflict resolution batch: %w", err)
	}
	return item, nil
}

func (r *ConflictResolutionBatchRepository) GetByActorKey(actorID uint, key string) (model.ConflictResolutionBatch, error) {
	var item model.ConflictResolutionBatch
	if err := r.db.Where("actor_id = ? AND idempotency_key = ?", actorID, key).First(&item).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return item, ErrNotFound
		}
		return item, fmt.Errorf("find conflict batch idempotency key: %w", err)
	}
	return item, nil
}

func (r *ConflictResolutionBatchRepository) List(q dto.ConflictBatchQuery) ([]model.ConflictResolutionBatch, int64, error) {
	db := r.db.Model(&model.ConflictResolutionBatch{})
	if q.ParcelID != nil {
		db = db.Where("parcel_id = ?", *q.ParcelID)
	}
	if q.State != "" {
		db = db.Where("batch_state = ?", q.State)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count conflict resolution batches: %w", err)
	}
	var items []model.ConflictResolutionBatch
	if err := db.Order("created_at DESC, id DESC").Offset((q.Page - 1) * q.PageSize).Limit(q.PageSize).Find(&items).Error; err != nil {
		return nil, 0, fmt.Errorf("list conflict resolution batches: %w", err)
	}
	return items, total, nil
}

func (r *ConflictResolutionBatchRepository) ListItems(batchID uint) ([]model.ConflictResolutionBatchItem, error) {
	var items []model.ConflictResolutionBatchItem
	if err := r.db.Where("batch_id = ?", batchID).Order("conflict_id ASC").Find(&items).Error; err != nil {
		return nil, fmt.Errorf("list conflict resolution batch items: %w", err)
	}
	return items, nil
}

// UnfinishedCoverage returns items of other previewed (unfinished) batches
// that cover any of the given conflicts.
func (r *ConflictResolutionBatchRepository) UnfinishedCoverage(conflictIDs []uint, excludeBatchID uint) ([]model.ConflictResolutionBatchItem, error) {
	if len(conflictIDs) == 0 {
		return []model.ConflictResolutionBatchItem{}, nil
	}
	var items []model.ConflictResolutionBatchItem
	err := r.db.Table("conflict_resolution_batch_items AS items").
		Select("items.*").
		Joins("JOIN conflict_resolution_batches batches ON batches.id = items.batch_id").
		Where("batches.batch_state = ? AND batches.id <> ? AND items.conflict_id IN ?", constants.ConflictBatchPreviewed, excludeBatchID, conflictIDs).
		Order("items.batch_id ASC, items.conflict_id ASC").
		Find(&items).Error
	if err != nil {
		return nil, fmt.Errorf("find unfinished conflict batch coverage: %w", err)
	}
	return items, nil
}

func (r *ConflictResolutionBatchRepository) SetItemProposal(itemID, proposalID uint) error {
	result := r.db.Model(&model.ConflictResolutionBatchItem{}).Where("id = ?", itemID).Update("proposal_id", proposalID)
	if result.Error != nil {
		return fmt.Errorf("set conflict batch item proposal: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("conflict batch item missing: %w", gorm.ErrInvalidTransaction)
	}
	return nil
}

// Complete moves a previewed batch to completed; the conditional update keeps
// concurrent submitters from both finishing the same batch.
func (r *ConflictResolutionBatchRepository) Complete(id uint, completedAt time.Time) error {
	return r.finish(id, constants.ConflictBatchCompleted, "[]", completedAt)
}

// Fail moves a previewed batch to failed and stores the invalidation reasons.
func (r *ConflictResolutionBatchRepository) Fail(id uint, reasonsJSON string, completedAt time.Time) error {
	return r.finish(id, constants.ConflictBatchFailed, reasonsJSON, completedAt)
}

func (r *ConflictResolutionBatchRepository) finish(id uint, to, reasonsJSON string, completedAt time.Time) error {
	result := r.db.Model(&model.ConflictResolutionBatch{}).
		Where("id = ? AND batch_state = ?", id, constants.ConflictBatchPreviewed).
		Updates(map[string]any{"batch_state": to, "failure_reasons": reasonsJSON, "completed_at": completedAt})
	if result.Error != nil {
		return fmt.Errorf("finish conflict resolution batch: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("conflict batch state changed: %w", gorm.ErrInvalidTransaction)
	}
	return nil
}
