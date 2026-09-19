import { api } from './client'
import type { ApiEnvelope } from '@/types/api'
import type { ResolutionBatch, ResolutionBatchCreateInput, ResolutionBatchSubmitInput } from '@/types/resolution-batch'

function newIdempotencyKey() {
  return crypto.randomUUID()
}

export const resolutionBatchApi = {
  async list(parcelId?: number) {
    const response = await api.get<ApiEnvelope<ResolutionBatch[]>>('/resolution-batches', {
      params: parcelId ? { parcel_id: parcelId } : undefined,
    })
    return response
  },
  async detail(id: number) {
    return api.get<ApiEnvelope<ResolutionBatch>>(`/resolution-batches/${id}`)
  },
  async create(body: ResolutionBatchCreateInput) {
    return api.post<ApiEnvelope<ResolutionBatch>>('/resolution-batches', body)
  },
  async submit(id: number, body: ResolutionBatchSubmitInput, idempotencyKey: string = newIdempotencyKey()) {
    return api.post<ApiEnvelope<ResolutionBatch>>(`/resolution-batches/${id}/submit`, body, {
      headers: { 'Idempotency-Key': idempotencyKey },
    })
  },
  async cancel(id: number) {
    return api.post<ApiEnvelope<ResolutionBatch>>(`/resolution-batches/${id}/cancel`, {})
  },
}
