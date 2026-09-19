export type ConflictBatchState = 'previewed' | 'completed' | 'failed'

export interface ConflictBatchItem {
  conflict_id: number
  proposal_id?: number | null
  parcel_versions: Record<string, number>
  suggestion_hash: string
  submittable: boolean
  reasons: string[]
}

export interface ConflictBatch {
  id: number
  parcel_id: number
  batch_state: ConflictBatchState
  idempotency_key: string
  submittable: boolean
  reasons: string[]
  items: ConflictBatchItem[]
  created_at: string
  completed_at?: string | null
}

// The backend may omit empty arrays; normalize so pages always iterate arrays.
export type ConflictBatchWire = Omit<ConflictBatch, 'reasons' | 'items'> & {
  reasons?: string[] | null
  items?: Array<Omit<ConflictBatchItem, 'reasons' | 'parcel_versions'> & {
    parcel_versions?: Record<string, number> | null
    reasons?: string[] | null
  }> | null
}

export function normalizeConflictBatch(item: ConflictBatchWire): ConflictBatch {
  return {
    ...item,
    reasons: item.reasons ?? [],
    items: (item.items ?? []).map((entry) => ({
      ...entry,
      parcel_versions: entry.parcel_versions ?? {},
      reasons: entry.reasons ?? [],
    })),
  }
}
