import { defineStore } from 'pinia'
import { ref } from 'vue'
import { resolutionBatchApi } from '@/api/resolution-batch'
import type { ResolutionBatch, ResolutionBatchCreateInput, ResolutionBatchSubmitInput } from '@/types/resolution-batch'

export const useResolutionBatchStore = defineStore('resolution-batches', () => {
  const items = ref<ResolutionBatch[]>([])
  const loading = ref(false)

  async function fetch(parcelId?: number) {
    loading.value = true
    try {
      const { data } = await resolutionBatchApi.list(parcelId)
      items.value = data.data
    } finally {
      loading.value = false
    }
  }

  async function fetchOne(id: number): Promise<ResolutionBatch> {
    const { data } = await resolutionBatchApi.detail(id)
    upsert(data.data)
    return data.data
  }

  async function create(body: ResolutionBatchCreateInput): Promise<ResolutionBatch> {
    const { data } = await resolutionBatchApi.create(body)
    upsert(data.data)
    return data.data
  }

  async function submit(id: number, body: ResolutionBatchSubmitInput, idempotencyKey?: string): Promise<ResolutionBatch> {
    const { data } = await resolutionBatchApi.submit(id, body, idempotencyKey)
    upsert(data.data)
    return data.data
  }

  async function cancel(id: number): Promise<ResolutionBatch> {
    const { data } = await resolutionBatchApi.cancel(id)
    upsert(data.data)
    return data.data
  }

  function upsert(batch: ResolutionBatch) {
    const index = items.value.findIndex((current) => current.id === batch.id)
    if (index >= 0) items.value[index] = batch
    else items.value.unshift(batch)
  }

  return { items, loading, fetch, fetchOne, create, submit, cancel, upsert }
})
