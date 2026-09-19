package repository

import (
	"errors"
	"fmt"
	"time"

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

// ConflictResolutionBatchRepository persists reviewer batch dispositions and
// their frozen conflict lines.
type ConflictResolutionBatchRepository struct{ db *gorm.DB }

// Create persists the batch and its frozen lines in one database
// transaction. The items slice is repopulated through the returned model so
// every frozen row carries the generated batch ID.
func (r *ConflictResolutionBatchRepository) Create(batch *model.ConflictResolutionBatch, items []model.ConflictResolutionBatchItem) error {
	persisted := make([]model.ConflictResolutionBatchItem, len(items))
	copy(persisted, items)
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(batch).Error; err != nil {
			return fmt.Errorf("create resolution batch: %w", err)
		}
		for index := range persisted {
			persisted[index].ID = 0
			persisted[index].BatchID = batch.ID
			if err := tx.Create(&persisted[index]).Error; err != nil {
				return fmt.Errorf("create resolution batch item: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	copy(items, persisted)
	return nil
}
func (r *ConflictResolutionBatchRepository) Get(id uint) (model.ConflictResolutionBatch, error) {
	var batch model.ConflictResolutionBatch
	if err := r.db.First(&batch, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return batch, ErrNotFound
		}
		return batch, fmt.Errorf("get resolution batch: %w", err)
	}
	return batch, nil
}

func (r *ConflictResolutionBatchRepository) GetByCode(code string) (model.ConflictResolutionBatch, error) {
	var batch model.ConflictResolutionBatch
	if err := r.db.Where("batch_code = ?", code).First(&batch).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return batch, ErrNotFound
		}
		return batch, fmt.Errorf("get resolution batch by code: %w", err)
	}
	return batch, nil
}

func (r *ConflictResolutionBatchRepository) ListItems(batchID uint) ([]model.ConflictResolutionBatchItem, error) {
	var items []model.ConflictResolutionBatchItem
	if err := r.db.Where("batch_id = ?", batchID).Order("id ASC").Find(&items).Error; err != nil {
		return nil, fmt.Errorf("list resolution batch items: %w", err)
	}
	return items, nil
}

// List returns batches newest first, optionally scoped to one parcel.
func (r *ConflictResolutionBatchRepository) List(parcelID *uint) ([]model.ConflictResolutionBatch, error) {
	var batches []model.ConflictResolutionBatch
	query := r.db.Model(&model.ConflictResolutionBatch{})
	if parcelID != nil {
		query = query.Where("parcel_id = ?", *parcelID)
	}
	if err := query.Order("created_at DESC, id DESC").Find(&batches).Error; err != nil {
		return nil, fmt.Errorf("list resolution batches: %w", err)
	}
	return batches, nil
}

// HasOpenItem reports whether an earlier open batch already claims the
// conflict. The submitting batch itself is excluded, and only batches created
// earlier block it, so the oldest open preview deterministically wins and two
// competing previews cannot deadlock.
func (r *ConflictResolutionBatchRepository) HasOpenItem(conflictID uint, openStates []string, excludeBatchID uint) (bool, error) {
	var count int64
	query := r.db.Model(&model.ConflictResolutionBatchItem{}).
		Joins("JOIN conflict_resolution_batches ON conflict_resolution_batches.id = conflict_resolution_batch_items.batch_id").
		Where("conflict_resolution_batch_items.conflict_id = ?", conflictID).
		Where("conflict_resolution_batches.batch_state IN ?", openStates).
		Where("conflict_resolution_batches.id <> ?", excludeBatchID).
		Where("conflict_resolution_batches.id < ?", excludeBatchID)
	if err := query.Count(&count).Error; err != nil {
		return false, fmt.Errorf("check open batch coverage: %w", err)
	}
	return count > 0, nil
}

// Cancel discards a preview without touching conflicts or proposals.
func (r *ConflictResolutionBatchRepository) Cancel(batchID uint) error {
	result := r.db.Model(&model.ConflictResolutionBatch{}).
		Where("id = ? AND batch_state = ?", batchID, "preview").
		Update("batch_state", "cancelled")
	if result.Error != nil {
		return fmt.Errorf("cancel resolution batch: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("resolution batch is no longer open: %w", gorm.ErrInvalidTransaction)
	}
	return nil
}

// MarkSubmitted records the terminal outcome of a successful submission. The
// conditional update makes a double-submit (or a concurrently finished batch)
// affect zero rows instead of silently creating a second proposal set.
func (r *ConflictResolutionBatchRepository) MarkSubmitted(batchID uint, submittedBy uint, proposalIDs string, submittedAt time.Time) error {
	result := r.db.Model(&model.ConflictResolutionBatch{}).
		Where("id = ? AND batch_state = ?", batchID, "preview").
		Updates(map[string]any{"batch_state": "submitted", "submitted_by": submittedBy, "submitted_at": submittedAt, "proposal_ids": proposalIDs})
	if result.Error != nil {
		return fmt.Errorf("mark resolution batch submitted: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("resolution batch is no longer open: %w", gorm.ErrInvalidTransaction)
	}
	return nil
}

// ConflictResolutionBatchRunRepository owns submission idempotency records.
type ConflictResolutionBatchRunRepository struct{ db *gorm.DB }

func (r *ConflictResolutionBatchRunRepository) GetByActorKey(actorID uint, key string) (model.ConflictResolutionBatchRun, error) {
	var run model.ConflictResolutionBatchRun
	if err := r.db.Where("actor_id = ? AND idempotency_key = ?", actorID, key).First(&run).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return run, ErrNotFound
		}
		return run, fmt.Errorf("find batch idempotency key: %w", err)
	}
	return run, nil
}

func (r *ConflictResolutionBatchRunRepository) Create(run *model.ConflictResolutionBatchRun) error {
	if err := r.db.Create(run).Error; err != nil {
		return fmt.Errorf("create resolution batch run: %w", err)
	}
	return nil
}
