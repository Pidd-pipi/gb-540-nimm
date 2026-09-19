export type ResolutionBatchState = 'preview' | 'submitted' | 'cancelled'

// Mirrors backend/internal/service/conflict_resolution_batch_service.go reason
// codes returned in 409 error details and per-item read models.
export type ResolutionBatchReasonCode =
  | 'conflict_processed'
  | 'parcel_version_changed'
  | 'suggestion_changed'
  | 'covered_by_open_batch'

export interface ResolutionBatchItem {
  conflict_id: number
  proposal_id: number
  parcel_id: number
  frozen_parcel_version: number
  current_parcel_version: number
  frozen_state: string
  current_state: string
  suggestion_hash: string
  submittable: boolean
  applied?: boolean
  reason_code?: string
  reason?: string
}

export interface ResolutionBatch {
  id: number
  batch_code: string
  parcel_id: number
  batch_state: ResolutionBatchState
  conflict_ids: number[]
  proposal_ids: number[]
  rationale: string
  created_by: number
  submitted_by?: number | null
  created_at: string
  updated_at: string
  submitted_at?: string | null
  submittable: boolean
  invalid_reason?: string
  invalid_conflict_ids?: number[]
  items: ResolutionBatchItem[]
}

export interface ResolutionBatchCreateInput {
  parcel_id: number
  conflict_ids: number[]
  rationale?: string
}

export interface ResolutionBatchSubmitInput {
  rationale?: string
}

export const batchReasonLabels: Record<ResolutionBatchReasonCode, string> = {
  conflict_processed: '冲突已被其他流程处理',
  parcel_version_changed: '地块边界版本已变化',
  suggestion_changed: '建议哈希与冻结时不一致',
  covered_by_open_batch: '该冲突已被其他未结束批次覆盖',
}
