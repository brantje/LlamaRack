<script setup lang="ts">
type ModelSummary = {
  id: string
  slug: string
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
type InspectionFeatures = {
  architecture?: string
  nextn_predict_layers?: number
  has_mtp?: boolean
  mtp_only?: boolean
  projector?: boolean
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
  features?: InspectionFeatures
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
const slug = computed(() => String(route.params.id || ''))
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

const pageStart = computed(() => details.value?.metadata_total ? details.value.offset + 1 : 0)
const pageEnd = computed(() => details.value ? Math.min(details.value.offset + details.value.metadata.length, details.value.metadata_total) : 0)
const canPrevious = computed(() => (details.value?.offset || 0) > 0)
const canNext = computed(() => details.value ? details.value.offset + details.value.metadata.length < details.value.metadata_total : false)
const valueCanPrevious = computed(() => (valuePage.value?.offset || 0) > 0)
const valueCanNext = computed(() => Boolean(valuePage.value?.has_more))
const valueDisplayedTotal = computed(() => Math.max(valuePage.value?.total || 0, selectedEntry.value?.array_length || 0))
const valueRangeEnd = computed(() => {
  if (!valuePage.value) return 0
  const loaded = valuePage.value.items?.length || valuePage.value.value?.length || 0
  return Math.min(valuePage.value.offset + loaded, valueDisplayedTotal.value)
})

function formatBytes(value: number) {
  if (!value) return 'Unknown'
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

function formatPositiveNumber(value?: number) {
  return value && value > 0 ? value.toLocaleString() : 'Unknown'
}

async function load() {
  if (!slug.value) return
  busy.value = true
  error.value = ''
  try {
    const params = new URLSearchParams({ offset: String(offset.value), limit: String(limit) })
    if (appliedQuery.value) params.set('q', appliedQuery.value)
    details.value = await manager.request<ModelDetails>(`/api/v1/models/${encodeURIComponent(slug.value)}/details?${params.toString()}`)
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
  if (!selectedEntry.value || !slug.value) return
  valueBusy.value = true
  valueError.value = ''
  try {
    const params = new URLSearchParams({ key: selectedEntry.value.key, offset: String(Math.max(0, nextOffset)) })
    valuePage.value = await manager.request<MetadataValuePage>(`/api/v1/models/${encodeURIComponent(slug.value)}/details/value?${params.toString()}`)
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
    <div class="flex flex-wrap items-start justify-between gap-4" data-testid="model-details-header">
      <UPageHeader
        class="min-w-0 flex-1"
        headline="MODEL REGISTRY"
        :title="details?.model.name || 'Model details'"
        description="General metadata read directly from the registered GGUF. Runtime controls remain on Instances."
      />
      <div class="flex w-full flex-wrap justify-start gap-2 sm:w-auto sm:justify-end" data-testid="model-details-actions">
        <AppButton to="/models" intent="secondary">Back to models</AppButton>
        <AppButton v-if="details?.model.slug" :to="`/models/${encodeURIComponent(details.model.slug)}/edit`" intent="primary">Edit</AppButton>
      </div>
    </div>

    <Frame v-if="error" class="p-3" data-testid="model-details-error">
      <div class="flex flex-wrap items-start gap-3">
        <StatusTag variant="failed">Unable to load GGUF metadata</StatusTag>
        <p class="min-w-0 flex-1 text-sm text-[var(--neutral-800)]">{{ error }}</p>
      </div>
    </Frame>

    <Frame
      v-for="warning in details?.warnings || []"
      :key="warning"
      class="p-4"
      data-testid="model-details-warning"
    >
      <div class="flex flex-wrap items-start gap-3">
        <StatusTag variant="pending">GGUF metadata warning</StatusTag>
        <p class="min-w-0 flex-1 text-sm text-[var(--neutral-800)]">{{ warning }}</p>
      </div>
    </Frame>

    <Frame v-if="details" class="p-5" data-testid="model-details-summary">
      <div class="mb-5">
        <p class="text-[length:var(--font-size-kicker)] font-extrabold tracking-[0.18em] text-[var(--neutral-700)]">GGUF SUMMARY</p>
        <h2 class="mt-1 text-lg font-semibold">Artifact</h2>
      </div>
      <dl class="grid gap-x-8 gap-y-5 sm:grid-cols-2 lg:grid-cols-4">
        <div><dt class="text-[length:var(--font-size-kicker)] font-semibold uppercase tracking-[0.12em] text-[var(--neutral-700)]">Path</dt><dd class="mt-1 break-all font-mono text-[length:var(--font-size-h6)]">{{ details.model.gguf_path || 'Unknown' }}</dd></div>
        <div><dt class="text-[length:var(--font-size-kicker)] font-semibold uppercase tracking-[0.12em] text-[var(--neutral-700)]">Size</dt><dd class="mt-1 break-all font-mono text-[length:var(--font-size-h6)]">{{ formatBytes(details.model.total_bytes) }}</dd></div>
        <div><dt class="text-[length:var(--font-size-kicker)] font-semibold uppercase tracking-[0.12em] text-[var(--neutral-700)]">GGUF version</dt><dd class="mt-1 break-all font-mono text-[length:var(--font-size-h6)]">{{ formatPositiveNumber(details.gguf_version) }}</dd></div>
        <div><dt class="text-[length:var(--font-size-kicker)] font-semibold uppercase tracking-[0.12em] text-[var(--neutral-700)]">Metadata keys</dt><dd class="mt-1 break-all font-mono text-[length:var(--font-size-h6)]">{{ formatPositiveNumber(details.metadata_count) }}</dd></div>
        <div><dt class="text-[length:var(--font-size-kicker)] font-semibold uppercase tracking-[0.12em] text-[var(--neutral-700)]">Architecture</dt><dd class="mt-1 break-all font-mono text-[length:var(--font-size-h6)]">{{ details.architecture || 'Unknown' }}</dd></div>
        <div><dt class="text-[length:var(--font-size-kicker)] font-semibold uppercase tracking-[0.12em] text-[var(--neutral-700)]">Quantization</dt><dd class="mt-1 break-all font-mono text-[length:var(--font-size-h6)]">{{ details.model.quantization || 'Unknown' }}</dd></div>
        <div><dt class="text-[length:var(--font-size-kicker)] font-semibold uppercase tracking-[0.12em] text-[var(--neutral-700)]">Context capability</dt><dd class="mt-1 break-all font-mono text-[length:var(--font-size-h6)]">{{ formatContext(details.model.context_length || details.detected_context_length) }}</dd></div>
        <div><dt class="text-[length:var(--font-size-kicker)] font-semibold uppercase tracking-[0.12em] text-[var(--neutral-700)]">Tensor count</dt><dd class="mt-1 break-all font-mono text-[length:var(--font-size-h6)]">{{ formatPositiveNumber(details.tensor_count) }}</dd></div>
        <div data-testid="model-details-features">
          <dt class="text-[length:var(--font-size-kicker)] font-semibold uppercase tracking-[0.12em] text-[var(--neutral-700)]">Detected features</dt>
          <dd class="mt-1 flex flex-wrap items-center gap-2 font-mono text-[length:var(--font-size-h6)]">
            <StatusTag v-if="details.features?.projector" variant="ready">Vision projector</StatusTag>
            <StatusTag v-if="details.features?.has_mtp && details.features?.mtp_only" variant="neutral">MTP helper</StatusTag>
            <StatusTag v-else-if="details.features?.has_mtp" variant="ready">Built-in MTP</StatusTag>
            <span v-if="details.features?.nextn_predict_layers" class="font-mono text-[length:var(--font-size-h6)] text-[var(--neutral-700)]">nextn_predict_layers {{ details.features.nextn_predict_layers }}</span>
            <span v-if="!details.features?.projector && !details.features?.has_mtp" class="font-mono text-[length:var(--font-size-h6)]">None</span>
          </dd>
        </div>
      </dl>
    </Frame>

    <Frame class="overflow-hidden" data-testid="gguf-metadata-card">
      <div class="flex flex-col gap-4 border-b border-[var(--color-divider)] p-5 lg:flex-row lg:items-end lg:justify-between">
        <div>
          <p class="text-[length:var(--font-size-kicker)] font-extrabold tracking-[0.18em] text-[var(--neutral-700)]">RAW GGUF METADATA</p>
          <h2 class="mt-1 text-lg font-semibold">Key / Type / Value</h2>
          <p class="mt-1 text-sm text-[var(--neutral-700)]">Unknown and future GGUF keys are shown without requiring manager-specific support.</p>
        </div>
        <div class="flex w-full flex-wrap items-center justify-end gap-2 lg:w-auto">
          <UInput v-model="query" data-testid="metadata-search" class="min-w-0 flex-1 lg:w-80" placeholder="Filter by metadata key" @keyup.enter="search" />
          <AppButton type="button" data-testid="metadata-search-button" intent="secondary" :loading="busy" @click="search">Search</AppButton>
          <AppButton v-if="appliedQuery" type="button" intent="ghost" @click="clearSearch">Clear</AppButton>
        </div>
      </div>

      <div v-if="busy && !details" class="p-6"><USkeleton class="h-40 w-full" /></div>
      <div v-else-if="details && !details.metadata.length" class="px-6 py-10 text-center">
        <p class="font-semibold">{{ appliedQuery ? 'No matching GGUF metadata' : 'No GGUF metadata returned for this artifact' }}</p>
        <p class="mt-1 text-sm text-[var(--neutral-700)]">{{ appliedQuery ? `No keys match “${appliedQuery}”. Clear the filter to see all metadata.` : 'The GGUF parser returned no metadata keys for this registered artifact.' }}</p>
      </div>
      <div v-else class="overflow-x-auto">
        <table class="w-full table-fixed text-left" data-testid="metadata-table">
          <thead class="border-b border-[var(--color-divider)] text-[length:var(--font-size-kicker)] uppercase tracking-[0.12em] text-[var(--neutral-700)]">
            <tr>
              <th class="w-[38%] px-5 py-3 font-semibold">Key</th>
              <th class="w-[18%] px-5 py-3 font-semibold">Type</th>
              <th class="px-5 py-3 font-semibold">Value</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-[var(--color-divider)]">
            <tr v-for="entry in details?.metadata || []" :key="entry.key">
              <td class="break-all px-5 py-3 align-top font-mono text-[length:var(--font-size-h6)]">{{ entry.key }}</td>
              <td class="break-all px-5 py-3 align-top font-mono text-[length:var(--font-size-h6)] text-[var(--neutral-700)]">{{ entry.type }}</td>
              <td class="px-5 py-3 align-top">
                <div class="flex min-w-0 items-start justify-between gap-3">
                  <span class="min-w-0 break-all font-mono text-[length:var(--font-size-h6)]">{{ entry.value }}</span>
                  <AppButton v-if="entry.truncated" type="button" data-testid="metadata-expand" intent="ghost" size="xs" class="shrink-0" @click="openValue(entry)">Expand</AppButton>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <div class="flex flex-wrap items-center justify-between gap-3 border-t border-[var(--color-divider)] px-5 py-3">
        <p v-if="details?.metadata_total" class="text-xs text-[var(--neutral-700)]">Showing {{ pageStart }}–{{ pageEnd }} of {{ details.metadata_total }} matching keys</p>
        <p v-else class="text-xs text-[var(--neutral-700)]">{{ appliedQuery ? 'No matching keys' : 'No metadata keys to paginate' }}</p>
        <div class="flex gap-2">
          <AppButton type="button" intent="ghost" size="sm" :disabled="!canPrevious || busy" @click="previousPage">Previous</AppButton>
          <AppButton type="button" intent="ghost" size="sm" :disabled="!canNext || busy" @click="nextPage">Next</AppButton>
        </div>
      </div>
    </Frame>

    <UModal v-model:open="valueOpen" :title="selectedEntry?.key || 'Metadata value'" :ui="{ content: 'w-[calc(100vw-2rem)] max-w-none sm:max-w-4xl' }">
      <template #body>
        <div class="space-y-4">
          <Frame v-if="valueError" class="border-[var(--accent-800)] p-3" data-testid="metadata-expanded-error">
            <p class="text-sm font-semibold text-[var(--accent-900)]">Unable to expand GGUF metadata value</p>
            <p class="mt-1 text-xs text-[var(--neutral-800)]">{{ valueError }}</p>
          </Frame>
          <div v-if="valueBusy && !valuePage"><USkeleton class="h-40 w-full" /></div>
          <template v-else-if="valuePage">
            <div class="flex flex-wrap items-center justify-between gap-2 text-xs text-[var(--neutral-700)]">
              <span>{{ valuePage.type }}</span>
              <span data-testid="metadata-expanded-count">{{ (valuePage.offset + 1).toLocaleString() }}–{{ valueRangeEnd.toLocaleString() }} of {{ valueDisplayedTotal.toLocaleString() }}{{ selectedEntry?.array_length && selectedEntry.array_length > (valuePage.total || 0) ? ' (truncated)' : '' }}</span>
            </div>
            <pre v-if="valuePage.value !== undefined" data-testid="metadata-expanded-value" class="max-h-[60vh] overflow-auto whitespace-pre-wrap break-all border border-[var(--color-divider)] p-4 font-mono text-xs">{{ valuePage.value }}</pre>
            <div v-else data-testid="metadata-expanded-items" class="max-h-[60vh] overflow-auto border border-[var(--color-divider)] p-2 font-mono text-xs">
              <div v-for="(item, itemIndex) in valuePage.items || []" :key="`${valuePage.offset + itemIndex}:${item}`" class="grid grid-cols-[5rem_minmax(0,1fr)] gap-3 border-b border-[var(--color-divider)] px-2 py-1.5 last:border-b-0">
                <span class="text-[var(--neutral-700)]">{{ valuePage.offset + itemIndex + 1 }}</span>
                <span class="break-all">{{ item }}</span>
              </div>
            </div>
            <div class="flex justify-end gap-2">
              <AppButton type="button" intent="ghost" size="sm" :disabled="!valueCanPrevious || valueBusy" @click="previousValuePage">Previous</AppButton>
              <AppButton type="button" intent="ghost" size="sm" :disabled="!valueCanNext || valueBusy" @click="nextValuePage">Next</AppButton>
            </div>
          </template>
        </div>
      </template>
    </UModal>
  </div>
</template>