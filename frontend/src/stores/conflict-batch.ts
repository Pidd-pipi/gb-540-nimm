import { defineStore } from 'pinia'
import { ref } from 'vue'
import { conflictBatchApi, type ConflictBatchQuery } from '@/api/conflict-batch'
import type { ConflictBatch } from '@/types/conflict-batch'

export const useConflictBatchStore = defineStore('conflict-batches', () => {
  const current = ref<ConflictBatch | null>(null)
  const recent = ref<ConflictBatch[]>([])
  const loading = ref(false)
  // One key per batch attempt: preview retries replay the original batch
  // instead of stacking unfinished batches that would block each other.
  let previewKey = ''

  async function preview(conflictIds: number[]) {
    loading.value = true
    try {
      if (!previewKey) previewKey = crypto.randomUUID()
      const { data } = await conflictBatchApi.preview({ conflict_ids: conflictIds }, previewKey)
      current.value = data.data
      return data.data
    } finally {
      loading.value = false
    }
  }

  async function submit() {
    if (!current.value) return null
    loading.value = true
    try {
      const { data } = await conflictBatchApi.submit(current.value.id)
      current.value = data.data
      return data.data
    } finally {
      loading.value = false
    }
  }

  async function refreshCurrent() {
    if (!current.value) return null
    const { data } = await conflictBatchApi.detail(current.value.id)
    current.value = data.data
    return data.data
  }

  async function loadBatch(id: number) {
    const { data } = await conflictBatchApi.detail(id)
    current.value = data.data
    previewKey = data.data.idempotency_key
    return data.data
  }

  async function fetchRecent(params?: ConflictBatchQuery) {
    const { data } = await conflictBatchApi.list({ page_size: 10, ...params })
    recent.value = data.data
    return data.data
  }

  function reset() {
    current.value = null
    previewKey = ''
  }

  return { current, recent, loading, preview, submit, refreshCurrent, loadBatch, fetchRecent, reset }
})
