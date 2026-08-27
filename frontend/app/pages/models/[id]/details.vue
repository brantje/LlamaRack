<script setup lang="ts">
import type { TableColumn } from '@nuxt/ui'

type ModelSummary = {
  id: string
  name: string
  gguf_path: string
  total_bytes: number
  quantization?: string
  context_length: number
}
type MetadataEntry = {
  key: string
  type: string
  value: string
  truncated?: boolean
  array_length?: number
}
type ModelDetails = {
  model: ModelSummary
  gguf_version?: number
  tensor_count?: number
  metadata_count?: number
  metadata_total: number
  metadata: MetadataEntry[]
  architecture?: string
  detected_context_length?: number
  offset: number
  limit: number
  warnings?: string[]
}
type MetadataValuePage = {
  key: string
  type: string
  value?: string
  items?: string[]
  offset: number
  limit: number
  total: number
  has_more: boolean
}

const manager = useManager()
const route = useRoute()
const id = computed(() => String(route.params.id || ''))
const details = ref<ModelDetails | null>(null)
const busy = ref(false)
const error = ref('')
const query = ref('')
const appliedQuery = ref('')
const offset = ref(0)
const limit = 100
const valueOpen = ref(false)
const valueBusy = ref(false)
const valueError = ref('')
const selectedEntry = ref<MetadataEntry | null>(null)
const valuePage = ref<MetadataValuePage | null>(null)

const columns: TableColumn<MetadataEntry>[] = [
  { accessorKey: 'key', header: 'Key' },
  { accessorKey: 'type', header: 'Type' },
  { accessorKey: 'value', header: 'Value' }
]
const pageStart = computed(() => details.value?.metadata_total ? details.value.offset + 1 : 0)
const pageEnd = computed(() => details.value ? Math.min(details.value.offset + details.value.metadata.length, details.value.metadata_total) : 0)
const canPrevious = computed(() => (details.value?.offset || 0) > 0)
const canNext = computed(() => details.value ? details.value.offset + details.value.metadata.length < details.value.metadata_total : false)
const valueCanPrevious = computed(() => (valuePage.value?.offset || 0) > 0)
const valueCanNext = computed(() => Boolean(valuePage.value?.has_more))

function formatBytes(value: number) {
  if (!value) return '—'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let amount = value
  let unit = 0
  while (amount >= 1024 && unit < units.length - 1) {
    amount /= 1024
    unit++
  }
  return `${amount >= 10 || unit === 0 ? amount.toFixed(0) : amount.toFixed(1)} ${units[unit]}`
}

function formatContext(value?: number) {
  return value && value > 0 ? value.toLocaleString() : 'Unknown'
}

async function load() {
  if (!id.value) return
  busy.value = true
  error.value = ''
  try {
    const params = new URLSearchParams({ offset: String(offset.value), limit: String(limit) })
    if (appliedQuery.value) params.set('q', appliedQuery.value)
    details.value = await manager.request<ModelDetails>(`/api/v1/models/${encodeURIComponent(id.value)}/details?${params.toString()}`)
  } catch (e: any) {
    error.value = e?.data?.error || e?.message || 'Unable to load GGUF metadata'
  } finally {
    busy.value = false
  }
}

function search() {
  appliedQuery.value = query.value.trim()
  offset.value = 0
  void load()
}

function clearSearch() {
  query.value = ''
  appliedQuery.value = ''
  offset.value = 0
  void load()
}

function previousPage() {
  offset.value = Math.max(0, offset.value - limit)
  void load()
}

function nextPage() {
  if (!details.value) return
  offset.value += limit
  void load()
}

async function loadValuePage(nextOffset: number) {
  if (!selectedEntry.value || !id.value) return
  valueBusy.value = true
  valueError.value = ''
  try {
    const params = new URLSearchParams({ key: selectedEntry.value.key, offset: String(Math.max(0, nextOffset)) })
    valuePage.value = await manager.request<MetadataValuePage>(`/api/v1/models/${encodeURIComponent(id.value)}/details/value?${params.toString()}`)
  } catch (e: any) {
    valueError.value = e?.data?.error || e?.message || 'Unable to expand GGUF metadata value'
  } finally {
    valueBusy.value = false
  }
}

function openValue(entry: MetadataEntry) {
  selectedEntry.value = entry
  valuePage.value = null
  valueError.value = ''
  valueOpen.value = true
  void loadValuePage(0)
}

function previousValuePage() {
  if (!valuePage.value) return
  void loadValuePage(Math.max(0, valuePage.value.offset - valuePage.value.limit))
}

function nextValuePage() {
  if (!valuePage.value?.has_more) return
  void loadValuePage(valuePage.value.offset + valuePage.value.limit)
}

onMounted(() => void load())
</script>

<template>
  <div class="space-y-5">
    <div class="flex items-start justify-between gap-6">
      <UPageHeader
        class="min-w-0 flex-1"
        headline="MODEL REGISTRY"
        :title="details?.model.name || 'Model details'"
        description="General metadata read directly from the registered GGUF. Runtime controls remain on Instances."
      />
      <div class="flex flex-wrap justify-end gap-2">
        <UButton to="/models" color="neutral" variant="soft">Back to models</UButton>
        <UButton v-if="details?.model.id" :to="`/models/${details.model.id}/edit`" color="neutral" variant="soft">Edit</UButton>
      </div>
    </div>

    <UAlert v-if="error" color="error" variant="subtle" :description="error" />
    <UAlert
      v-for="warning in details?.warnings || []"
      :key="warning"
      color="warning"
      variant="subtle"
      title="GGUF metadata warning"
      :description="warning"
    />

    <UCard v-if="details" data-testid="model-details-summary">
      <template #header>
        <div>
          <p class="text-xs font-extrabold tracking-[0.18em] text-dimmed">GGUF SUMMARY</p>
          <h2 class="mt-1 text-xl font-bold">Artifact</h2>
        </div>
      </template>
      <dl class="grid gap-x-8 gap-y-5 sm:grid-cols-2 lg:grid-cols-4">
        <div><dt class="text-xs font-semibold uppercase tracking-wide text-dimmed">Path</dt><dd class="mt-1 break-all font-mono text-sm">{{ details.model.gguf_path }}</dd></div>
        <div><dt class="text-xs font-semibold uppercase tracking-wide text-dimmed">Size</dt><dd class="mt-1 text-sm">{{ formatBytes(details.model.total_bytes) }}</dd></div>
        <div><dt class="text-xs font-semibold uppercase tracking-wide text-dimmed">GGUF version</dt><dd class="mt-1 text-sm">{{ details.gguf_version || 'Unknown' }}</dd></div>
        <div><dt class="text-xs font-semibold uppercase tracking-wide text-dimmed">Metadata keys</dt><dd class="mt-1 text-sm">{{ details.metadata_count ?? 0 }}</dd></div>
        <div><dt class="text-xs font-semibold uppercase tracking-wide text-dimmed">Architecture</dt><dd class="mt-1 text-sm">{{ details.architecture || 'Unknown' }}</dd></div>
        <div><dt class="text-xs font-semibold uppercase tracking-wide text-dimmed">Quantization</dt><dd class="mt-1 text-sm">{{ details.model.quantization || 'Unknown' }}</dd></div>
        <div><dt class="text-xs font-semibold uppercase tracking-wide text-dimmed">Context capability</dt><dd class="mt-1 text-sm">{{ formatContext(details.model.context_length || details.detected_context_length) }}</dd></div>
        <div><dt class="text-xs font-semibold uppercase tracking-wide text-dimmed">Tensor count</dt><dd class="mt-1 text-sm">{{ details.tensor_count ?? 'Unknown' }}</dd></div>
      </dl>
    </UCard>

    <UCard data-testid="gguf-metadata-card" :ui="{ body: 'p-0 sm:p-0' }">
      <template #header>
        <div class="flex flex-col gap-4 lg:flex-row lg:items-end lg:justify-between">
          <div>
            <p class="text-xs font-extrabold tracking-[0.18em] text-dimmed">RAW GGUF METADATA</p>
            <h2 class="mt-1 text-xl font-bold">Key / Type / Value</h2>
            <p class="mt-1 text-sm text-muted">Unknown and future GGUF keys are shown without requiring manager-specific support.</p>
          </div>
          <UFieldGroup class="w-full lg:w-auto">
            <UInput v-model="query" data-testid="metadata-search" class="min-w-0 flex-1 lg:w-80" placeholder="Filter by metadata key" @keyup.enter="search" />
            <UButton data-testid="metadata-search-button" color="neutral" variant="soft" :loading="busy" @click="search">Search</UButton>
            <UButton v-if="appliedQuery" color="neutral" variant="ghost" @click="clearSearch">Clear</UButton>
          </UFieldGroup>
        </div>
      </template>

      <div v-if="busy && !details" class="p-6"><USkeleton class="h-40 w-full" /></div>
      <UEmpty v-else-if="details && !details.metadata.length" variant="naked" title="No matching GGUF metadata" description="Try a different metadata key filter." class="py-10" />
      <UTable v-else :data="details?.metadata || []" :columns="columns" class="w-full" data-testid="metadata-table">
        <template #value-cell="{ row }">
          <div class="flex min-w-0 items-start justify-between gap-3">
            <span class="min-w-0 break-all font-mono text-xs">{{ row.original.value }}</span>
            <UButton v-if="row.original.truncated" data-testid="metadata-expand" color="neutral" variant="soft" size="xs" @click="openValue(row.original)">Expand</UButton>
          </div>
        </template>
      </UTable>

      <template #footer>
        <div class="flex flex-wrap items-center justify-between gap-3">
          <p class="text-xs text-muted">Showing {{ pageStart }}–{{ pageEnd }} of {{ details?.metadata_total || 0 }} matching keys</p>
          <div class="flex gap-2">
            <UButton color="neutral" variant="soft" size="sm" :disabled="!canPrevious || busy" @click="previousPage">Previous</UButton>
            <UButton color="neutral" variant="soft" size="sm" :disabled="!canNext || busy" @click="nextPage">Next</UButton>
          </div>
        </div>
      </template>
    </UCard>

    <UModal v-model:open="valueOpen" :title="selectedEntry?.key || 'Metadata value'" :ui="{ content: 'w-[calc(100vw-2rem)] max-w-none sm:max-w-4xl' }">
      <template #body>
        <div class="space-y-4">
          <UAlert v-if="valueError" color="error" variant="subtle" :description="valueError" />
          <div v-if="valueBusy && !valuePage"><USkeleton class="h-40 w-full" /></div>
          <template v-else-if="valuePage">
            <div class="flex flex-wrap items-center justify-between gap-2 text-xs text-muted">
              <span>{{ valuePage.type }}</span>
              <span>{{ valuePage.offset.toLocaleString() }}–{{ Math.min(valuePage.offset + (valuePage.items?.length || valuePage.value?.length || 0), valuePage.total).toLocaleString() }} of {{ valuePage.total.toLocaleString() }}</span>
            </div>
            <pre v-if="valuePage.value !== undefined" data-testid="metadata-expanded-value" class="max-h-[60vh] overflow-auto whitespace-pre-wrap break-all rounded-md bg-elevated p-4 font-mono text-xs">{{ valuePage.value }}</pre>
            <div v-else data-testid="metadata-expanded-items" class="max-h-[60vh] overflow-auto rounded-md bg-elevated p-2 font-mono text-xs">
              <div v-for="(item, itemIndex) in valuePage.items || []" :key="`${valuePage.offset + itemIndex}:${item}`" class="grid grid-cols-[5rem_minmax(0,1fr)] gap-3 border-b border-default px-2 py-1.5 last:border-b-0">
                <span class="text-dimmed">{{ valuePage.offset + itemIndex }}</span>
                <span class="break-all">{{ item }}</span>
              </div>
            </div>
            <div class="flex justify-end gap-2">
              <UButton color="neutral" variant="soft" size="sm" :disabled="!valueCanPrevious || valueBusy" @click="previousValuePage">Previous</UButton>
              <UButton color="neutral" variant="soft" size="sm" :disabled="!valueCanNext || valueBusy" @click="nextValuePage">Next</UButton>
            </div>
          </template>
        </div>
      </template>
    </UModal>
  </div>
</template>
