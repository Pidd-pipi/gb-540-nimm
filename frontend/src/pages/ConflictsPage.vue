<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Check, FilePlus, Layers, RefreshCw, ScanSearch, X } from 'lucide-vue-next'
import PageHeader from '@/components/common/PageHeader.vue'
import TopologyLegend from '@/components/common/TopologyLegend.vue'
import GeometryEvidenceDrawer from '@/components/common/GeometryEvidenceDrawer.vue'
import ProposalStateBadge from '@/components/common/ProposalStateBadge.vue'
import { useTopologyConflictStore } from '@/stores/topology-conflict'
import { useBoundaryProposalStore } from '@/stores/boundary-proposal'
import { useLandParcelStore } from '@/stores/land-parcel'
import { useConflictBatchStore } from '@/stores/conflict-batch'
import { useAuth } from '@/hooks/useAuth'
import { conflictTypeLabel } from '@/types/enums/conflict-type'
import type { ProposalState } from '@/types/enums/proposal-state'
import type { TopologyConflict } from '@/types/topology-conflict'

const conflicts = useTopologyConflictStore()
const proposals = useBoundaryProposalStore()
const parcels = useLandParcelStore()
const batches = useConflictBatchStore()
const auth = useAuth()
const detectOpen = ref(false)
const evidenceOpen = ref(false)
const selected = ref<TopologyConflict | null>(null)
const form = reactive({ proposal_id: 0 })
const batchOpen = ref(false)
const batchParcelId = ref<number | null>(null)
const batchSelection = ref<TopologyConflict[]>([])

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

function parcelCode(id: number | null | undefined) {
  if (!id) return '—'
  return parcels.items.find((item) => item.id === id)?.parcel_code ?? `地块 #${id}`
}

function anchorParcelId(conflict: TopologyConflict) {
  return proposals.items.find((proposal) => proposal.id === conflict.proposal_id)?.parcel_id ?? null
}

const batchParcels = computed(() => {
  const ids = new Set<number>()
  for (const conflict of conflicts.items) {
    if (conflict.conflict_state !== 'confirmed') continue
    const id = anchorParcelId(conflict)
    if (id) ids.add(id)
  }
  return [...ids].sort((a, b) => a - b)
})

const batchCandidates = computed(() =>
  conflicts.items.filter((conflict) => conflict.conflict_state === 'confirmed' && anchorParcelId(conflict) === batchParcelId.value),
)

function frozenVersions(versions: Record<string, number>) {
  return Object.entries(versions)
    .map(([id, version]) => `#${id}·v${version}`)
    .join('，')
}

async function load() {
  await Promise.all([
    parcels.fetch({ page_size: 100 }),
    proposals.fetch({ page_size: 100 }),
    conflicts.fetch({ page_size: 100 }),
  ])
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

async function openBatchDialog() {
  batchOpen.value = true
  await batches.fetchRecent()
}

async function previewBatch() {
  await batches.preview(batchSelection.value.map((conflict) => conflict.id))
  await batches.fetchRecent()
}

async function submitBatch() {
  try {
    await ElMessageBox.confirm(
      '将按预览冻结的地块版本与建议哈希整批创建草稿提案并推进冲突；任一冲突失效都会使整批失败且不做任何改动。',
      '提交批次',
      { confirmButtonText: '提交批次', cancelButtonText: '取消', type: 'warning' },
    )
  } catch (reason) {
    if (reason === 'cancel' || reason === 'close') return
    throw reason
  }
  try {
    const completed = await batches.submit()
    if (completed) ElMessage.success(`批次 #${completed.id} 已完成，创建 ${completed.items.length} 份草稿提案`)
  } catch {
    // 失败时回读批次，展示服务端记录的失效原因
    await batches.refreshCurrent()
  }
  await load()
  await batches.fetchRecent()
}

function resetBatch() {
  batches.reset()
  batchSelection.value = []
}

async function loadBatch(id: number) {
  await batches.loadBatch(id)
}

onMounted(load)
</script>

<template>
  <PageHeader title="冲突消解" eyebrow="TOPOLOGY CONFLICTS" description="查看重叠、缝隙和无效拓扑证据，所有建议都需要人工确认。">
    <el-button v-if="auth.hasRole('reviewer', 'admin')" @click="openBatchDialog"><Layers :size="15" />批次处置</el-button>
    <el-button v-if="auth.hasRole('gis_analyst', 'admin')" type="primary" @click="detectOpen = true"><ScanSearch :size="15" />运行检测</el-button>
  </PageHeader>

  <section class="content-band">
    <div class="toolbar"><el-button @click="load"><RefreshCw :size="15" />刷新</el-button><TopologyLegend /><span class="toolbar-spacer subtle-count">{{ conflicts.items.length }} 条冲突</span></div>
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

  <el-dialog v-model="batchOpen" title="冲突批次处置" width="min(860px, calc(100vw - 28px))" @closed="resetBatch">
    <div class="batch-layout">
      <section class="batch-section">
        <h4>选择同一地块下的已确认冲突</h4>
        <el-select v-model="batchParcelId" placeholder="选择地块" style="width: 100%" :disabled="!!batches.current">
          <el-option v-for="id in batchParcels" :key="id" :label="parcelCode(id)" :value="id" />
        </el-select>
        <el-table v-if="!batches.current" :data="batchCandidates" row-key="id" max-height="220" @selection-change="(rows: TopologyConflict[]) => (batchSelection = rows)">
          <el-table-column type="selection" width="42" />
          <el-table-column label="冲突" width="80"><template #default="scope"><strong>#{{ scope.row.id }}</strong></template></el-table-column>
          <el-table-column label="类型" width="120"><template #default="scope">{{ typeLabel(scope.row.conflict_type) }}</template></el-table-column>
          <el-table-column label="量级" width="110"><template #default="scope">{{ scope.row.magnitude_square_m.toFixed(2) }} m²</template></el-table-column>
          <el-table-column prop="explanation" label="说明" min-width="180" show-overflow-tooltip />
        </el-table>
        <el-alert v-if="batchParcelId && !batchCandidates.length" type="info" :closable="false" title="该地块当前没有已确认冲突。" />
        <el-button v-if="!batches.current" type="primary" :disabled="batchSelection.length < 2 || batches.loading" @click="previewBatch">生成预览（冻结版本与建议哈希）</el-button>
      </section>

      <section v-if="batches.current" class="batch-section">
        <h4>批次 #{{ batches.current.id }} <span class="status-pill" :class="batches.current.batch_state">{{ batches.current.batch_state }}</span></h4>
        <el-alert v-if="batches.current.batch_state === 'previewed' && batches.current.submittable" type="success" :closable="false" title="可提交：所有冲突仍为已确认，地块版本与建议哈希未变化。" />
        <el-alert v-else-if="batches.current.batch_state === 'previewed'" type="warning" :closable="false" title="当前不可提交" />
        <el-alert v-else-if="batches.current.batch_state === 'completed'" type="success" :closable="false" title="批次已完成：草稿提案已创建，冲突已推进。" />
        <el-alert v-else type="error" :closable="false" title="批次已失败：未改动任何冲突、提案或计数。" />
        <ul v-if="batches.current.reasons.length" class="reason-list">
          <li v-for="reason in batches.current.reasons" :key="reason">{{ reason }}</li>
        </ul>
        <el-table :data="batches.current.items" row-key="conflict_id" max-height="240">
          <el-table-column label="冲突" width="80"><template #default="scope"><strong>#{{ scope.row.conflict_id }}</strong></template></el-table-column>
          <el-table-column label="冻结地块版本" min-width="150"><template #default="scope">{{ frozenVersions(scope.row.parcel_versions) }}</template></el-table-column>
          <el-table-column label="建议哈希" width="130"><template #default="scope"><code>{{ scope.row.suggestion_hash.slice(0, 12) }}…</code></template></el-table-column>
          <el-table-column label="结果" min-width="150"><template #default="scope"><span v-if="scope.row.proposal_id">草稿提案 #{{ scope.row.proposal_id }}</span><span v-else-if="scope.row.reasons.length" class="reason-text">{{ scope.row.reasons.join('；') }}</span><span v-else>待提交</span></template></el-table-column>
        </el-table>
        <div class="batch-actions">
          <el-button v-if="batches.current.batch_state === 'previewed'" type="primary" :disabled="!batches.current.submittable || batches.loading" @click="submitBatch">提交批次</el-button>
          <el-button v-if="batches.current.batch_state === 'previewed'" :disabled="batches.loading" @click="batches.refreshCurrent()">重新校验</el-button>
          <el-button @click="resetBatch">新批次</el-button>
        </div>
      </section>

      <section v-if="batches.recent.length" class="batch-section">
        <h4>最近批次（刷新后回读一致）</h4>
        <el-table :data="batches.recent" row-key="id" max-height="200">
          <el-table-column label="批次" width="80"><template #default="scope"><strong>#{{ scope.row.id }}</strong></template></el-table-column>
          <el-table-column label="地块" width="130"><template #default="scope">{{ parcelCode(scope.row.parcel_id) }}</template></el-table-column>
          <el-table-column label="状态" width="110"><template #default="scope"><span class="status-pill" :class="scope.row.batch_state">{{ scope.row.batch_state }}</span></template></el-table-column>
          <el-table-column label="冲突数" width="80"><template #default="scope">{{ scope.row.items.length }}</template></el-table-column>
          <el-table-column label="可提交 / 失效原因" min-width="180"><template #default="scope"><span v-if="scope.row.batch_state === 'previewed'">{{ scope.row.submittable ? '可提交' : scope.row.reasons.join('；') }}</span><span v-else>{{ scope.row.reasons.join('；') || '—' }}</span></template></el-table-column>
          <el-table-column label="时间" width="160"><template #default="scope">{{ new Date(scope.row.created_at).toLocaleString() }}</template></el-table-column>
          <el-table-column label="动作" width="80"><template #default="scope"><el-button text type="primary" @click="loadBatch(scope.row.id)">载入</el-button></template></el-table-column>
        </el-table>
      </section>
    </div>
    <template #footer><el-button @click="batchOpen = false">关闭</el-button></template>
  </el-dialog>

  <GeometryEvidenceDrawer v-model="evidenceOpen" title="冲突证据" :geometry="selected?.geometry_geojson" :explanation="selected?.explanation" />
</template>

<style scoped>
.conflict-tag { display: inline-flex; padding: 3px 8px; border: 1px solid var(--line-strong); font-size: 12px; font-weight: 800; }
.tone-overlap { color: #9c3028; background: #fbeceb; border-color: #e6afaa; }.tone-gap { color: #755310; background: #fff5db; border-color: #e0c16b; }.tone-self_intersection { color: #7b4c9e; background: #f4ecfa; }.tone-dangling_edge { color: #2b6f96; background: #e8f2f7; }
.muted { display: block; margin-top: 4px; color: var(--text-muted); font-size: 11px; }
.conflict-actions { display: flex; flex-wrap: wrap; gap: 2px; }
.status-pill.confirmed, .status-pill.resolution_proposed, .status-pill.previewed { color: #755310; background: #fff5db; }.status-pill.resolved, .status-pill.completed { color: #17604e; background: #e8f4f0; }.status-pill.false_positive, .status-pill.closed, .status-pill.failed { color: #4b5551; background: #e8ecea; }
.batch-layout { display: flex; flex-direction: column; gap: 18px; }
.batch-section { display: flex; flex-direction: column; gap: 10px; }
.batch-section h4 { margin: 0; font-size: 14px; display: flex; align-items: center; gap: 8px; }
.batch-actions { display: flex; gap: 8px; }
.reason-list { margin: 0; padding-left: 18px; color: #9c3028; font-size: 12px; }
.reason-text { color: #9c3028; font-size: 12px; }
</style>
