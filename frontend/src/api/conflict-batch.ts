import { api } from './client'
import type { ApiEnvelope } from '@/types/api'
import { normalizeConflictBatch, type ConflictBatchWire } from '@/types/conflict-batch'

export interface ConflictBatchPreviewInput {
  conflict_ids: number[]
}

export interface ConflictBatchQuery {
  parcel_id?: number
  state?: string
  page?: number
  page_size?: number
}

export const conflictBatchApi = {
  async preview(body: ConflictBatchPreviewInput, idempotencyKey: string) {
    const response = await api.post<ApiEnvelope<ConflictBatchWire>>('/conflicts/batches/preview', body, {
      headers: { 'Idempotency-Key': idempotencyKey },
    })
    return { ...response, data: { ...response.data, data: normalizeConflictBatch(response.data.data) } }
  },
  async submit(id: number) {
    const response = await api.post<ApiEnvelope<ConflictBatchWire>>(`/conflicts/batches/${id}/submit`)
    return { ...response, data: { ...response.data, data: normalizeConflictBatch(response.data.data) } }
  },
  async detail(id: number) {
    const response = await api.get<ApiEnvelope<ConflictBatchWire>>(`/conflicts/batches/${id}`)
    return { ...response, data: { ...response.data, data: normalizeConflictBatch(response.data.data) } }
  },
  async list(params?: ConflictBatchQuery) {
    const response = await api.get<ApiEnvelope<ConflictBatchWire[]>>('/conflicts/batches', { params })
    return { ...response, data: { ...response.data, data: response.data.data.map(normalizeConflictBatch) } }
  },
}
