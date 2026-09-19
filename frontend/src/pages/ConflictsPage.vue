<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Check, FilePlus, Layers, RefreshCw, ScanSearch, Send, X } from 'lucide-vue-next'
import PageHeader from '@/components/common/PageHeader.vue'
import TopologyLegend from '@/components/common/TopologyLegend.vue'
import GeometryEvidenceDrawer from '@/components/common/GeometryEvidenceDrawer.vue'
import ProposalStateBadge from '@/components/common/ProposalStateBadge.vue'
import { useTopologyConflictStore } from '@/stores/topology-conflict'
import { useBoundaryProposalStore } from '@/stores/boundary-proposal'
import { useLandParcelStore } from '@/stores/land-parcel'
import { useResolutionBatchStore } from '@/stores/resolution-batch'
import { useAuth } from '@/hooks/useAuth'
import { conflictTypeLabel } from '@/types/enums/conflict-type'
import type { ProposalState } from '@/types/enums/proposal-state'
import type { TopologyConflict } from '@/types/topology-conflict'
import type { ResolutionBatch, ResolutionBatchReasonCode } from '@/types/resolution-batch'
import { batchReasonLabels } from '@/types/resolution-batch'
import type { AxiosError } from 'axios'

const conflicts = useTopologyConflictStore()
const proposals = useBoundaryProposalStore()
const parcels = useLandParcelStore()
const batches = useResolutionBatchStore()
const auth = useAuth()
const detectOpen = ref(false)
const batchOpen = ref(false)
const evidenceOpen = ref(false)
const selected = ref<TopologyConflict | null>(null)
const form = reactive({ proposal_id: 0 })
const batchForm = reactive({ parcel_id: 0, rationale: '' })
const batchSelection = ref<TopologyConflict[]>([])
const batchSaving = ref(false)
const activeBatch = ref<ResolutionBatch | null>(null)

function typeLabel(value: unknown) {
  return conflictTypeLabel[value as keyof typeof conflictTypeLabel] ?? String(value)
}

function proposalStateFor(conflict: TopologyConflict): ProposalState | null {
  return proposals.items.find((proposal) => proposal.id === conflict.proposal_id)?.proposal_state ?? null
}

function parcelLabelFor(conflict: TopologyConflict) {
  const labels = conflict.parcel_ids.map((id) => {
    const parcel = parcels.items.find((item) => item.id === id)
    return parcel ? parcel.parcel_code : `地块 #${id}`
  })
  return labels.length ? labels.join(' · ') : '未记录参与地块'
}

function ownerParcelId(conflict: TopologyConflict): number | null {
  return proposals.items.find((proposal) => proposal.id === conflict.proposal_id)?.parcel_id ?? null
}

function parcelCode(id: number | null | undefined) {
  if (!id) return '未知地块'
  return parcels.items.find((item) => item.id === id)?.parcel_code ?? `地块 #${id}`
}

const eligibleConflicts = computed(() =>
  conflicts.items.filter(
    (item) => item.conflict_state === 'confirmed' && ownerParcelId(item) === batchForm.parcel_id,
  ),
)

const parcelBatches = computed(() => batches.items.filter((batch) => batch.parcel_id === batchForm.parcel_id))

async function load() {
  await Promise.all([
    parcels.fetch({ page_size: 100 }),
    proposals.fetch({ page_size: 100 }),
    conflicts.fetch({ page_size: 100 }),
  ])
  if (auth.hasRole('reviewer', 'admin')) {
    await batches.fetch()
  }
}

async function detect() {
  await conflicts.detect({ proposal_id: form.proposal_id })
  detectOpen.value = false
  await load()
}

async function transition(item: TopologyConflict, to: string) {
  await conflicts.transition(item.id, { to })
  await load()
}

async function applySuggestion(item: TopologyConflict) {
  try {
    await ElMessageBox.confirm(
      `将基于冲突 #${item.id} 的吸附证据创建新的草稿提案；原始提案和地块边界不会被改写。`,
      '应用建议',
      { confirmButtonText: '创建草稿', cancelButtonText: '取消', type: 'warning' },
    )
  } catch (reason) {
    if (reason === 'cancel' || reason === 'close') return
    throw reason
  }
  await conflicts.applySuggestion(item.id)
  await load()
}

function showEvidence(item: TopologyConflict) {
  selected.value = item
  evidenceOpen.value = true
}

function openBatchDialog() {
  const ownerIds = new Set(conflicts.items.filter((item) => item.conflict_state === 'confirmed').map(ownerParcelId))
  const firstParcel = parcels.items.find((parcel) => ownerIds.has(parcel.id))
  batchForm.parcel_id = firstParcel?.id ?? parcels.items[0]?.id ?? 0
  batchForm.rationale = ''
  batchSelection.value = []
  activeBatch.value = null
  batchOpen.value = true
}

function onBatchSelectionChange(rows: TopologyConflict[]) {
  batchSelection.value = rows
}

function reasonFromError(error: unknown): Record<string, string> | undefined {
  const axiosError = error as AxiosError<{ error?: { details?: Record<string, unknown> } }>
  const details = axiosError.response?.data?.error?.details
  if (!details) return undefined
  const reasons: Record<string, string> = {}
  for (const [key, value] of Object.entries(details)) {
    reasons[key] = batchReasonLabels[value as ResolutionBatchReasonCode] ?? String(value)
  }
  return Object.keys(reasons).length ? reasons : undefined
}

async function createBatch() {
  if (!batchForm.parcel_id || batchSelection.value.length === 0) {
    ElMessage.warning('请选择同一地块下至少一条已确认冲突')
    return
  }
  batchSaving.value = true
  try {
    activeBatch.value = await batches.create({
      parcel_id: batchForm.parcel_id,
      conflict_ids: batchSelection.value.map((item) => item.id),
      rationale: batchForm.rationale,
    })
    await conflicts.fetch({ page_size: 100 })
  } catch {
    // 统一拦截器已弹出 409 消息；失败时不生成预览，选择保留以便调整。
  } finally {
    batchSaving.value = false
  }
}

async function submitBatch(batch: ResolutionBatch) {
  try {
    await ElMessageBox.confirm(
      `批次 ${batch.batch_code} 将为 ${batch.conflict_ids.length} 条冲突创建草稿提案并推进冲突；任一行失效都会整批失败且不改动数据。`,
      '提交批次处置',
      { confirmButtonText: '整批提交', cancelButtonText: '取消', type: 'warning' },
    )
  } catch (reason) {
    if (reason === 'cancel' || reason === 'close') return
    throw reason
  }
  try {
    activeBatch.value = await batches.submit(batch.id, { rationale: batch.rationale })
    ElMessage.success(`批次 ${batch.batch_code} 已全部提交`)
    await load()
  } catch (error) {
    const reasons = reasonFromError(error)
    const refreshed = await batches.fetchOne(batch.id)
    activeBatch.value = refreshed
    if (reasons) {
      // 明细同时渲染在预览面板中，这里给出汇总提示。
      ElMessage.warning('整批失败：' + Array.from(new Set(Object.values(reasons))).join('；'))
    }
  }
}

async function rereadBatch(batch: ResolutionBatch) {
  activeBatch.value = await batches.fetchOne(batch.id)
}

async function cancelBatch(batch: ResolutionBatch) {
  try {
    await ElMessageBox.confirm(
      `取消批次 ${batch.batch_code} 不会改动任何冲突或提案，仅释放其对冲突的覆盖。`,
      '取消批次',
      { confirmButtonText: '取消批次', cancelButtonText: '返回', type: 'warning' },
    )
  } catch (reason) {
    if (reason === 'cancel' || reason === 'close') return
    throw reason
  }
  const updated = await batches.cancel(batch.id)
  if (activeBatch.value?.id === updated.id) activeBatch.value = updated
  await load()
}

function viewBatch(batch: ResolutionBatch) {
  activeBatch.value = batch
}

function batchStateLabel(state: ResolutionBatch['batch_state']) {
  if (state === 'submitted') return '已提交'
  if (state === 'cancelled') return '已取消'
  return '预览'
}

const previewBatchRows = computed(() =>
  parcelBatches.value.filter((batch) => batch.batch_state === 'preview'),
)

const finishedBatchRows = computed(() =>
  parcelBatches.value.filter((batch) => batch.batch_state !== 'preview'),
)

onMounted(load)
</script>

<template>
  <PageHeader title="冲突消解" eyebrow="TOPOLOGY CONFLICTS" description="查看重叠、缝隙和无效拓扑证据，所有建议都需要人工确认。">
    <el-button v-if="auth.hasRole('reviewer', 'admin')" type="success" plain @click="openBatchDialog"><Layers :size="15" />批次处置</el-button>
    <el-button v-if="auth.hasRole('gis_analyst', 'admin')" type="primary" @click="detectOpen = true"><ScanSearch :size="15" />运行检测</el-button>
  </PageHeader>

  <section class="content-band">
    <div class="toolbar"><el-button @click="load"><RefreshCw :size="15" />刷新</el-button><TopologyLegend /><span class="toolbar-spacer subtle-count">{{ conflicts.items.length }} 条冲突 · {{ batches.items.length }} 个批次</span></div>
    <div class="data-surface">
      <el-table v-loading="conflicts.loading" :data="conflicts.items" row-key="id">
        <el-table-column label="冲突" width="90"><template #default="scope"><strong>#{{ scope.row.id }}</strong></template></el-table-column>
        <el-table-column label="参与地块" min-width="180"><template #default="scope"><strong>{{ parcelLabelFor(scope.row) }}</strong><small class="muted">提案 #{{ scope.row.proposal_id }}</small><ProposalStateBadge :state="proposalStateFor(scope.row)" /></template></el-table-column>
        <el-table-column label="类型" width="125"><template #default="scope"><span :class="['conflict-tag', `tone-${scope.row.conflict_type}`]">{{ typeLabel(scope.row.conflict_type) }}</span></template></el-table-column>
        <el-table-column prop="severity" label="严重度" width="95" />
        <el-table-column label="量级" width="125"><template #default="scope">{{ scope.row.magnitude_square_m.toFixed(2) }} m²</template></el-table-column>
        <el-table-column prop="explanation" label="说明" min-width="220" show-overflow-tooltip />
        <el-table-column label="状态" width="145"><template #default="scope"><span class="status-pill" :class="scope.row.conflict_state">{{ scope.row.conflict_state }}</span></template></el-table-column>
        <el-table-column label="动作" width="270"><template #default="scope"><div class="conflict-actions"><el-button text @click="showEvidence(scope.row)">证据</el-button><template v-if="auth.hasRole('reviewer', 'admin')"><el-button v-if="scope.row.conflict_state === 'detected'" text type="primary" @click="transition(scope.row, 'confirmed')"><Check :size="14" />确认</el-button><el-button v-if="scope.row.conflict_state === 'detected'" text type="warning" @click="transition(scope.row, 'false_positive')"><X :size="14" />误报</el-button><el-button v-if="scope.row.conflict_state === 'confirmed'" text type="primary" @click="transition(scope.row, 'resolution_proposed')"><FilePlus :size="14" />准备建议</el-button><el-button v-if="scope.row.conflict_state === 'resolution_proposed'" text type="primary" @click="applySuggestion(scope.row)"><FilePlus :size="14" />应用建议</el-button><el-button v-if="scope.row.conflict_state === 'false_positive' || scope.row.conflict_state === 'resolved'" text @click="transition(scope.row, 'closed')"><X :size="14" />关闭</el-button></template></div></template></el-table-column>
      </el-table>
      <div v-if="!conflicts.loading && !conflicts.items.length" class="empty-state"><div><strong>暂无冲突</strong><span>选择一个提案运行检测，系统会记录算法版本和输入哈希。</span></div></div>
    </div>
  </section>

  <el-dialog v-model="detectOpen" title="检测提案拓扑" width="min(480px, calc(100vw - 28px))">
    <el-form label-position="top"><el-form-item label="边界提案"><el-select v-model="form.proposal_id" placeholder="选择提案" style="width: 100%"><el-option v-for="item in proposals.items" :key="item.id" :label="`提案 #${item.id} · ${item.proposal_state}`" :value="item.id" /></el-select></el-form-item><el-alert type="warning" :closable="false" title="检测是离线决策支持，不会修改原始地块边界或法定登记。" /></el-form>
    <template #footer><el-button @click="detectOpen = false">取消</el-button><el-button type="primary" :disabled="!form.proposal_id" @click="detect">开始检测</el-button></template>
  </el-dialog>

  <el-dialog v-model="batchOpen" title="批次处置" width="min(920px, calc(100vw - 28px))" @open="batches.fetch()">
    <el-form label-position="top" class="batch-form">
      <el-form-item label="处置地块（仅可选择同一地块下的已确认冲突）">
        <el-select v-model="batchForm.parcel_id" placeholder="选择地块" style="width: 320px" @change="activeBatch = null">
          <el-option v-for="parcel in parcels.items" :key="parcel.id" :label="`${parcel.parcel_code} · v${parcel.boundary_version}`" :value="parcel.id" />
        </el-select>
      </el-form-item>
    </el-form>

    <el-alert v-if="eligibleConflicts.length === 0" type="info" :closable="false" title="该地块下没有已确认（confirmed）冲突；先在列表中确认冲突后再生成批次预览。" class="batch-alert" />

    <template v-else>
      <div class="batch-section-title">选择冲突并冻结快照</div>
      <el-table :data="eligibleConflicts" row-key="id" max-height="240" size="small" @selection-change="onBatchSelectionChange">
        <el-table-column type="selection" width="42" :selectable="() => true" />
        <el-table-column label="冲突" width="80"><template #default="scope">#{{ scope.row.id }}</template></el-table-column>
        <el-table-column label="类型" width="140"><template #default="scope">{{ typeLabel(scope.row.conflict_type) }}</template></el-table-column>
        <el-table-column label="来源提案" width="100"><template #default="scope">#{{ scope.row.proposal_id }}</template></el-table-column>
        <el-table-column prop="explanation" label="说明" min-width="220" show-overflow-tooltip />
      </el-table>
      <el-input v-model="batchForm.rationale" class="batch-rationale" type="textarea" :rows="2" maxlength="2000" show-word-limit placeholder="批次处置说明（可选）" />
      <div class="batch-actions"><el-button type="primary" :loading="batchSaving" :disabled="batchSelection.length === 0" @click="createBatch"><Layers :size="15" />生成预览（冻结版本与建议哈希）</el-button></div>
    </template>

    <div v-if="previewBatchRows.length" class="batch-section-title">该地块未结束批次（刷新后回读）</div>
    <el-table v-if="previewBatchRows.length" :data="previewBatchRows" row-key="id" size="small" class="batch-list">
      <el-table-column label="批次" min-width="150"><template #default="scope"><strong>{{ scope.row.batch_code }}</strong><small class="muted">{{ new Date(scope.row.created_at).toLocaleString() }}</small></template></el-table-column>
      <el-table-column label="冲突数" width="80"><template #default="scope">{{ scope.row.conflict_ids.length }}</template></el-table-column>
      <el-table-column label="可提交状态" width="120"><template #default="scope"><span :class="['status-pill', scope.row.submittable ? 'confirmed' : 'false_positive']">{{ scope.row.submittable ? '可提交' : '已失效' }}</span></template></el-table-column>
      <el-table-column label="失效原因" min-width="220"><template #default="scope"><span class="invalid-reason">{{ scope.row.invalid_reason || '—' }}</span></template></el-table-column>
      <el-table-column label="操作" width="230"><template #default="scope"><div class="conflict-actions"><el-button text type="primary" @click="viewBatch(scope.row)">查看</el-button><el-button text @click="rereadBatch(scope.row)"><RefreshCw :size="13" />回读</el-button><el-button text type="success" :disabled="!scope.row.submittable" @click="submitBatch(scope.row)"><Send :size="13" />提交</el-button><el-button text type="danger" @click="cancelBatch(scope.row)"><X :size="13" />取消</el-button></div></template></el-table-column>
    </el-table>

    <div v-if="finishedBatchRows.length" class="batch-section-title">已结束批次</div>
    <el-table v-if="finishedBatchRows.length" :data="finishedBatchRows" row-key="id" size="small" class="batch-list">
      <el-table-column label="批次" min-width="150"><template #default="scope"><strong>{{ scope.row.batch_code }}</strong><small class="muted">{{ new Date(scope.row.created_at).toLocaleString() }}</small></template></el-table-column>
      <el-table-column label="结果" width="100"><template #default="scope"><span :class="['status-pill', scope.row.batch_state === 'submitted' ? 'resolved' : 'closed']">{{ batchStateLabel(scope.row.batch_state) }}</span></template></el-table-column>
      <el-table-column label="冲突数 / 草稿提案" min-width="160"><template #default="scope">{{ scope.row.conflict_ids.length }} / {{ scope.row.proposal_ids.length }}</template></el-table-column>
      <el-table-column label="操作" width="90"><template #default="scope"><el-button text type="primary" @click="viewBatch(scope.row)">查看</el-button></template></el-table-column>
    </el-table>

    <div v-if="activeBatch" class="batch-preview">
      <div class="batch-preview-head">
        <div><strong>{{ activeBatch.batch_code }}</strong><small class="muted">地块 {{ parcelCode(activeBatch.parcel_id) }} · 状态 {{ batchStateLabel(activeBatch.batch_state) }}</small></div>
        <div>
          <el-button size="small" @click="rereadBatch(activeBatch)"><RefreshCw :size="13" />回读</el-button>
          <template v-if="activeBatch.batch_state === 'preview'">
            <el-button size="small" type="danger" plain @click="cancelBatch(activeBatch)"><X :size="13" />取消批次</el-button>
            <el-button size="small" type="success" :disabled="!activeBatch.submittable" @click="submitBatch(activeBatch)"><Send :size="13" />整批提交</el-button>
          </template>
        </div>
      </div>
      <el-alert v-if="activeBatch.batch_state === 'cancelled'" type="info" :closable="false" class="batch-alert" title="该批次已取消，未创建提案，也未推进冲突。" />
      <el-alert v-else-if="!activeBatch.submittable && activeBatch.batch_state === 'preview'" type="error" :closable="false" class="batch-alert" :title="`批次不可提交：${activeBatch.invalid_reason || '存在失效冲突行'}`" />
      <el-table :data="activeBatch.items" row-key="conflict_id" size="small" max-height="240">
        <el-table-column label="冲突" width="70"><template #default="scope">#{{ scope.row.conflict_id }}</template></el-table-column>
        <el-table-column label="冻结/当前状态" width="170"><template #default="scope"><span :class="{ 'line-invalid': !scope.row.submittable && !scope.row.applied && scope.row.current_state !== 'resolved' }">{{ scope.row.frozen_state }} → {{ scope.row.current_state }}</span></template></el-table-column>
        <el-table-column label="地块版本（冻结/当前）" width="170"><template #default="scope"><span :class="{ 'line-invalid': scope.row.frozen_parcel_version !== scope.row.current_parcel_version && !scope.row.applied }">v{{ scope.row.frozen_parcel_version }} / v{{ scope.row.current_parcel_version }}</span></template></el-table-column>
        <el-table-column label="建议哈希" min-width="200"><template #default="scope"><code class="hash-cell">{{ scope.row.suggestion_hash.slice(0, 18) }}…</code></template></el-table-column>
        <el-table-column label="行状态" width="220"><template #default="scope"><span v-if="scope.row.applied" class="line-ok">已生成草稿提案</span><span v-else-if="scope.row.submittable" class="line-ok">有效，可提交</span><span v-else class="line-invalid">{{ scope.row.reason || '已失效' }}</span></template></el-table-column>
      </el-table>
      <div v-if="activeBatch.proposal_ids.length" class="muted">已生成草稿提案：{{ activeBatch.proposal_ids.map((id) => `#${id}`).join('、') }}</div>
    </div>
  </el-dialog>

  <GeometryEvidenceDrawer v-model="evidenceOpen" title="冲突证据" :geometry="selected?.geometry_geojson" :explanation="selected?.explanation" />
</template>

<style scoped>
.conflict-tag { display: inline-flex; padding: 3px 8px; border: 1px solid var(--line-strong); font-size: 12px; font-weight: 800; }
.tone-overlap { color: #9c3028; background: #fbeceb; border-color: #e6afaa; }.tone-gap { color: #755310; background: #fff5db; border-color: #e0c16b; }.tone-self_intersection { color: #7b4c9e; background: #f4ecfa; }.tone-dangling_edge { color: #2b6f96; background: #e8f2f7; }
.muted { display: block; margin-top: 4px; color: var(--text-muted); font-size: 11px; }
.conflict-actions { display: flex; flex-wrap: wrap; gap: 2px; }
.status-pill.confirmed, .status-pill.resolution_proposed { color: #755310; background: #fff5db; }.status-pill.resolved { color: #17604e; background: #e8f4f0; }.status-pill.false_positive, .status-pill.closed { color: #4b5551; background: #e8ecea; }
.batch-form { margin-bottom: 6px; }
.batch-section-title { margin: 14px 0 8px; font-size: 13px; font-weight: 800; color: var(--text); }
.batch-rationale { margin-top: 10px; }
.batch-actions { margin-top: 10px; }
.batch-alert { margin: 10px 0; }
.batch-list { margin-bottom: 12px; }
.invalid-reason { color: #9c3028; font-size: 12px; }
.batch-preview { margin-top: 14px; padding: 12px; border: 1px solid var(--line-strong); background: var(--surface-strong, #f7f8f8); }
.batch-preview-head { display: flex; align-items: center; justify-content: space-between; margin-bottom: 10px; }
.hash-cell { font-size: 11px; color: var(--text-muted); }
.line-ok { color: #17604e; font-size: 12px; font-weight: 700; }
.line-invalid { color: #9c3028; font-size: 12px; font-weight: 700; }
</style>
