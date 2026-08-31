<script setup lang="ts">
type HFModel = { id: string; author?: string; downloads: number; likes: number; last_modified?: string; parameter_count?: number; tags?: string[]; private: boolean; gated: boolean }
type HFFile = { path: string; size: number; oid?: string }
type HFDependency = { kind: string; name: string; quantization?: string; total_bytes: number; files: HFFile[] }
type HFArtifact = { id: string; name: string; quantization?: string; model_bytes: number; total_bytes: number; shard_count: number; expected_shards: number; complete: boolean; files: HFFile[]; dependencies?: HFDependency[] }
type HFDetail = HFModel & { description?: string; revision: string; artifacts: HFArtifact[] }
type HFSearchPage = { items: HFModel[]; next_cursor?: string }
type QuantizationGuide = { name?: string; tier: string; quality: string; memory: string; speed: string; summary: string; tradeoff: string; warning?: string; known: boolean }
type MemoryEstimate = { weights_bytes: number; kv_cache_bytes: number; runtime_overhead_bytes: number; cpu_only_ram_bytes: number; full_offload_vram_bytes: number }
type Offload = { mode: string; gpu_layers?: number; devices?: string[]; tensor_split?: string; kv_on_gpu: boolean; reason: string }
type EstimatedGenerationSpeed = { estimated: boolean; min_tokens_per_second?: number; max_tokens_per_second?: number; label: string; reason: string }
type ArtifactAdvice = { artifact_id: string; quantization: QuantizationGuide; recommended: boolean; runnable: boolean; fit: 'gpu' | 'multi_gpu' | 'hybrid' | 'cpu' | 'no_fit' | 'unknown'; fit_label: string; reason: string; memory: MemoryEstimate; offload: Offload; estimated_generation_speed?: EstimatedGenerationSpeed; confidence: string; warnings?: string[] }
type DiscoverRecommendations = { context_length: number; context_capability: number; context_assumed: boolean; metadata: { architecture?: string; context_length?: number; block_count?: number; embedding_length?: number; head_count?: number; kv_head_count?: number }; metadata_warning?: string; hardware_warning?: string; hardware_available: boolean; hybrid_recommendations_enabled: boolean; artifacts: ArtifactAdvice[] }

const props = defineProps<{ repoId?: string }>()
const manager = useManager()
const query = useState<string>('models-discover-query', () => '')
const author = useState<string>('models-discover-author', () => '')
const sort = useState<string>('models-discover-sort', () => 'trending_score')
const results = useState<HFModel[]>('models-discover-results', () => [])
const nextCursor = useState<string>('models-discover-next-cursor', () => '')
const hasSearched = useState<boolean>('models-discover-has-searched', () => false)
const scrollPosition = useState<number>('models-discover-scroll-position', () => 0)
const selected = ref<HFDetail | null>(null)
const recommendations = ref<DiscoverRecommendations | null>(null)
const loading = ref(false)
const loadingMore = ref(false)
const detailLoading = ref(false)
const recommendationLoading = ref(false)
const error = ref('')
const recommendationError = ref('')
const downloading = ref<string[]>([])
const downloadNotice = ref('')
const loadMoreSentinel = ref<HTMLElement | null>(null)
const contextIndex = ref(0)
const contextExplicit = ref(false)
let debounceTimer: ReturnType<typeof setTimeout> | undefined
let recommendationTimer: ReturnType<typeof setTimeout> | undefined
let searchVersion = 0
let recommendationVersion = 0
let loadObserver: IntersectionObserver | undefined

const repoID = computed(() => String(props.repoId || '').trim())
const isDetail = computed(() => Boolean(repoID.value))
const pageSize = 30
const sortOptions = [
  { label: 'Trending', value: 'trending_score' },
  { label: 'Most likes', value: 'likes' },
  { label: 'Most downloads', value: 'downloads' },
  { label: 'Recently created', value: 'created_at' },
  { label: 'Recently updated', value: 'last_modified' }
]
const standardContexts = [4096, 8192, 16384, 32768, 65536, 131072, 262144, 524288, 1048576]
const contextOptions = computed(() => {
  const capability = Math.max(0, recommendations.value?.context_capability || 0)
  const current = Math.max(0, recommendations.value?.context_length || 0)
  const ceiling = capability || Math.max(131072, current)
  const values = standardContexts.filter(value => value <= ceiling)
  if (capability > 0 && !values.includes(capability)) values.push(capability)
  if (current > 0 && !values.includes(current)) values.push(current)
  values.sort((a, b) => a - b)
  return values
})
const selectedContext = computed(() => contextOptions.value[contextIndex.value] || recommendations.value?.context_length || 4096)
const adviceByID = computed(() => new Map((recommendations.value?.artifacts || []).map(item => [item.artifact_id, item])))
const orderedArtifacts = computed(() => {
  const artifacts = selected.value?.artifacts || []
  const order = recommendations.value?.artifacts || []
  if (!order.length) return artifacts
  const byID = new Map(artifacts.map(item => [item.id, item]))
  const sorted = order.map(item => byID.get(item.artifact_id)).filter((item): item is HFArtifact => Boolean(item))
  const included = new Set(sorted.map(item => item.id))
  return [...sorted, ...artifacts.filter(item => !included.has(item.id))]
})

function formatBytes(value: number) {
  if (!value) return 'Unknown size'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let amount = value
  let index = 0
  while (amount >= 1024 && index < units.length - 1) { amount /= 1024; index++ }
  return `${amount >= 10 || index === 0 ? amount.toFixed(0) : amount.toFixed(1)} ${units[index]}`
}
function formatParameters(value?: number) {
  if (!value || value <= 0) return ''
  const units = [
    { threshold: 1e12, suffix: 'T' },
    { threshold: 1e9, suffix: 'B' },
    { threshold: 1e6, suffix: 'M' },
    { threshold: 1e3, suffix: 'K' }
  ]
  for (const unit of units) {
    if (value < unit.threshold) continue
    const amount = value / unit.threshold
    const digits = amount >= 100 || Number.isInteger(amount) ? 0 : 1
    return `${amount.toFixed(digits).replace(/\.0$/, '')}${unit.suffix} params`
  }
  return `${value.toLocaleString()} params`
}
function formatContext(value: number) {
  if (value >= 1024 * 1024 && value % (1024 * 1024) === 0) return `${value / (1024 * 1024)}M`
  if (value >= 1024) return `${Math.round(value / 1024)}K`
  return String(value)
}
function formatUpdated(value?: string) {
  if (!value) return ''
  const timestamp = new Date(value).getTime()
  if (!Number.isFinite(timestamp)) return ''
  const seconds = Math.max(0, Math.floor((Date.now() - timestamp) / 1000))
  if (seconds < 60) return 'Updated just now'
  if (seconds < 3600) return `Updated ${Math.floor(seconds / 60)}m ago`
  if (seconds < 86400) return `Updated ${Math.floor(seconds / 3600)}h ago`
  if (seconds < 86400 * 30) return `Updated ${Math.floor(seconds / 86400)}d ago`
  return `Updated ${new Date(timestamp).toLocaleDateString()}`
}
function dependencyLabel(kind: string) {
  if (kind === 'mmproj') return 'Vision projector'
  if (kind === 'mtp') return 'MTP draft model'
  return kind
}
function isDownloading(artifactID: string) {
  return downloading.value.includes(artifactID)
}
function launchTo(artifact: HFArtifact) {
  if (!selected.value) return '/models/new'
  return { path: '/models/new', query: { repo: selected.value.id, artifact: artifact.id } }
}
function artifactAdvice(artifact: HFArtifact) {
  return adviceByID.value.get(artifact.id) || null
}
function fitColor(advice: ArtifactAdvice | null): 'success' | 'warning' | 'error' | 'neutral' {
  if (!advice) return 'neutral'
  if (advice.fit === 'gpu' || advice.fit === 'multi_gpu') return 'success'
  if (advice.fit === 'hybrid') return 'warning'
  if (advice.fit === 'no_fit') return 'error'
  return 'neutral'
}
function fitLabel(advice: ArtifactAdvice | null) {
  return advice?.fit_label || 'Fit unknown'
}
function fitReason(advice: ArtifactAdvice | null) {
  return advice?.reason || 'Context-aware hardware guidance is unavailable for this artifact.'
}
function speedLabel(advice: ArtifactAdvice | null) {
  return advice?.estimated_generation_speed?.label || 'Estimate unavailable'
}
function speedReason(advice: ArtifactAdvice | null) {
  return advice?.estimated_generation_speed?.reason || 'Estimated generation speed requires a runnable GPU placement and measured memory-bandwidth telemetry.'
}

function huggingFaceRepo(value: string) {
  let raw = value.trim()
  if (!raw) return ''
  if (/^(?:www\.)?huggingface\.co\//i.test(raw)) raw = `https://${raw}`
  if (!/^https?:\/\//i.test(raw)) return ''
  try {
    const url = new URL(raw)
    const host = url.hostname.toLowerCase().replace(/^www\./, '')
    if (host !== 'huggingface.co') return ''
    let parts = url.pathname.split('/').filter(Boolean)
    if (parts[0]?.toLowerCase() === 'models') parts = parts.slice(1)
    if (parts.length < 2) return ''
    if (['datasets', 'spaces'].includes(parts[0].toLowerCase())) return ''
    return `${parts[0]}/${parts[1]}`
  } catch {
    return ''
  }
}

function clearDebounce() {
  if (!debounceTimer) return
  clearTimeout(debounceTimer)
  debounceTimer = undefined
}
function clearRecommendationDebounce() {
  if (!recommendationTimer) return
  clearTimeout(recommendationTimer)
  recommendationTimer = undefined
}
function scheduleSearch() {
  clearDebounce()
  debounceTimer = setTimeout(() => {
    debounceTimer = undefined
    void search()
  }, 350)
}
function mergeResults(existing: HFModel[], incoming: HFModel[]) {
  const byID = new Map(existing.map(item => [item.id, item]))
  for (const item of incoming) byID.set(item.id, item)
  return [...byID.values()]
}

async function fetchSearchPage(reset: boolean) {
  const version = reset ? ++searchVersion : searchVersion
  if (reset) loading.value = true
  else loadingMore.value = true
  error.value = ''
  const normalizedURL = huggingFaceRepo(query.value)
  const searchQuery = normalizedURL || query.value.trim()
  const cursor = reset ? '' : nextCursor.value
  try {
    const params = new URLSearchParams({ q: searchQuery, author: author.value.trim(), sort: sort.value, limit: String(pageSize) })
    if (cursor) params.set('cursor', cursor)
    const page = await manager.request<HFSearchPage>(`/api/v1/huggingface/search?${params.toString()}`)
    if (version !== searchVersion) return false
    results.value = reset ? page.items : mergeResults(results.value, page.items)
    nextCursor.value = page.next_cursor || ''
    hasSearched.value = true
    return true
  } catch (value: any) {
    if (version === searchVersion) error.value = value?.data?.error || value?.message || 'Unable to search Hugging Face'
    return false
  } finally {
    if (version === searchVersion) {
      if (reset) loading.value = false
      else loadingMore.value = false
    }
  }
}
async function search() {
  clearDebounce()
  nextCursor.value = ''
  const succeeded = await fetchSearchPage(true)
  if (succeeded && isDetail.value) await navigateTo('/models/discover')
}
async function loadMore() {
  if (isDetail.value || !nextCursor.value || loading.value || loadingMore.value) return
  await fetchSearchPage(false)
}
function modelRoute(id: string) {
  const [owner, name] = id.split('/', 2)
  if (!owner || !name) return '/models/discover'
  return `/models/discover/${encodeURIComponent(owner)}/${encodeURIComponent(name)}`
}
async function goToModel(id: string) {
  if (import.meta.client) scrollPosition.value = window.scrollY
  error.value = ''
  downloadNotice.value = ''
  await navigateTo(modelRoute(id))
}
async function backToResults() {
  await navigateTo('/models/discover')
}
function restoreScroll() {
  if (!import.meta.client || scrollPosition.value <= 0) return
  void nextTick(() => requestAnimationFrame(() => window.scrollTo(0, scrollPosition.value)))
}

function isRecommendationResponse(value: unknown): value is DiscoverRecommendations {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false
  const candidate = value as Partial<DiscoverRecommendations>
  return typeof candidate.context_length === 'number' && Array.isArray(candidate.artifacts)
}
function syncContextIndex() {
  const target = recommendations.value?.context_length || 4096
  const exact = contextOptions.value.indexOf(target)
  if (exact >= 0) contextIndex.value = exact
}
async function loadRecommendations(contextLength?: number) {
  if (!repoID.value) return
  const version = ++recommendationVersion
  recommendationLoading.value = true
  recommendationError.value = ''
  const params = new URLSearchParams({ repo: repoID.value })
  if (contextLength && contextLength > 0) params.set('context_length', String(contextLength))
  try {
    const value = await manager.request<DiscoverRecommendations>(`/api/v1/huggingface/recommendations?${params.toString()}`)
    if (version !== recommendationVersion) return
    if (!isRecommendationResponse(value)) throw new Error('Context-aware recommendation data is unavailable')
    recommendations.value = value
    syncContextIndex()
  } catch (value: any) {
    if (version === recommendationVersion) recommendationError.value = value?.data?.error || value?.message || 'Unable to calculate hardware recommendations'
  } finally {
    if (version === recommendationVersion) recommendationLoading.value = false
  }
}
function selectContext(value: number | number[]) {
  const raw = Array.isArray(value) ? value[0] : value
  const index = Math.max(0, Math.min(contextOptions.value.length - 1, Number(raw) || 0))
  contextIndex.value = index
  contextExplicit.value = true
  clearRecommendationDebounce()
  recommendationTimer = setTimeout(() => {
    recommendationTimer = undefined
    void loadRecommendations(contextOptions.value[index])
  }, 250)
}

async function openModel(id: string) {
  detailLoading.value = true
  error.value = ''
  recommendationError.value = ''
  downloadNotice.value = ''
  recommendations.value = null
  contextExplicit.value = false
  const summary = results.value.find(item => item.id === id)
  try {
    const detail = await manager.request<HFDetail>(`/api/v1/huggingface/model?repo=${encodeURIComponent(id)}`)
    selected.value = { ...detail, parameter_count: detail.parameter_count || summary?.parameter_count }
    await loadRecommendations()
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to load repository details'
  } finally {
    detailLoading.value = false
  }
}

async function download(artifact: HFArtifact) {
  if (!selected.value || !artifact.complete || isDownloading(artifact.id)) return
  downloading.value = [...downloading.value, artifact.id]
  error.value = ''
  downloadNotice.value = ''
  try {
    await manager.request('/api/v1/downloads', { method: 'POST', body: { repo_id: selected.value.id, artifact_id: artifact.id } })
    const helpers = artifact.dependencies?.length || 0
    downloadNotice.value = helpers
      ? `${artifact.name} and ${helpers} detected helper ${helpers === 1 ? 'artifact was' : 'artifacts were'} added to Downloads.`
      : `${artifact.name} was added to Downloads.`
  } catch (value: any) {
    downloading.value = downloading.value.filter(id => id !== artifact.id)
    error.value = value?.data?.error || value?.message || 'Unable to start download'
  }
}

watch(query, (value) => {
  const repo = huggingFaceRepo(value)
  if (repo && repo !== value.trim()) { query.value = repo; return }
  scheduleSearch()
})
watch(author, scheduleSearch)
watch(sort, () => { clearDebounce(); void search() })
watch(loadMoreSentinel, (element, previous) => {
  if (!loadObserver) return
  if (previous) loadObserver.unobserve(previous)
  if (element) loadObserver.observe(element)
})
watch(contextOptions, syncContextIndex)

onMounted(() => {
  if (import.meta.client && typeof IntersectionObserver !== 'undefined') {
    loadObserver = new IntersectionObserver((entries) => {
      if (entries.some(entry => entry.isIntersecting)) void loadMore()
    }, { rootMargin: '500px 0px' })
    if (loadMoreSentinel.value) loadObserver.observe(loadMoreSentinel.value)
  }
  if (isDetail.value) void openModel(repoID.value)
  else if (!hasSearched.value || !results.value.length) scheduleSearch()
  else restoreScroll()
})
onBeforeUnmount(() => {
  clearDebounce()
  clearRecommendationDebounce()
  loadObserver?.disconnect()
})
</script>

<template>
  <div class="space-y-5" data-testid="models-discover">
    <div v-if="!isDetail" class="flex flex-wrap items-start justify-between gap-4">
      <UPageHeader
        class="min-w-0 flex-1"
        headline="MODEL REGISTRY"
        title="Discover"
        description="Search Hugging Face GGUF repositories and choose an artifact that fits this manager's hardware."
      />
      <div class="flex w-full flex-wrap justify-start gap-2 sm:w-auto sm:shrink-0 sm:justify-end" data-testid="discover-list-actions">
        <AppButton intent="secondary" to="/downloads">Downloads</AppButton>
        <AppButton intent="secondary" to="/models">Registered models</AppButton>
      </div>
    </div>

    <UForm
      v-if="!isDetail"
      :state="{}"
      class="grid gap-3 border-y border-[var(--color-divider)] py-4 lg:grid-cols-[minmax(0,1fr)_220px_220px]"
      data-testid="discover-search-row"
      @submit="search"
    >
      <UFormField label="Search" name="search">
        <UInput v-model="query" class="w-full" placeholder="Qwen, Llama, Gemma… or Hugging Face URL" icon="i-lucide-search" />
      </UFormField>
      <UFormField label="Author / organization" name="author">
        <UInput v-model="author" class="w-full" placeholder="Optional" />
      </UFormField>
      <UFormField label="Sort" name="sort">
        <USelect v-model="sort" class="w-full" :items="sortOptions" value-key="value" />
      </UFormField>
      <p class="text-xs leading-5 text-[var(--neutral-700)] lg:col-span-3">Results update automatically as you type. Press Enter to search immediately.</p>
    </UForm>

    <Frame v-if="error && !(isDetail && !selected)" class="border-[var(--accent-800)] p-4 text-sm text-[var(--accent-900)]" data-testid="discover-error">
      {{ error }}
    </Frame>
    <Frame v-if="downloadNotice" class="p-4 text-sm text-[var(--neutral-900)]" data-testid="discover-download-notice">
      {{ downloadNotice }}
    </Frame>

    <div v-if="loading && !results.length && !isDetail" class="divide-y divide-[var(--color-divider)] border-y border-[var(--color-divider)]" data-testid="discover-loading">
      <div v-for="n in 6" :key="n" class="grid min-h-20 gap-4 py-4 lg:grid-cols-[minmax(0,1fr)_auto]">
        <div class="space-y-2"><USkeleton class="h-4 w-1/3" /><USkeleton class="h-3 w-2/3" /></div>
        <USkeleton class="h-8 w-28" />
      </div>
    </div>

    <Frame v-else-if="!isDetail && !results.length" class="p-8 text-center" data-testid="discover-empty">
      <p class="font-heading text-lg font-semibold text-[var(--color-text)]">Search Hugging Face</p>
      <p class="mt-2 text-sm text-[var(--neutral-800)]">Only repositories tagged for GGUF are returned. Results load automatically and update as you search.</p>
    </Frame>

    <template v-else-if="!isDetail">
      <div class="divide-y divide-[var(--color-divider)] border-y border-[var(--color-divider)]" data-testid="discover-results">
        <div
          v-for="item in results"
          :key="item.id"
          class="grid cursor-pointer gap-4 px-1 py-4 transition-colors hover:bg-[var(--neutral-100)] lg:grid-cols-[minmax(0,1fr)_auto] lg:items-center"
          data-testid="discover-repository-row"
          @click="goToModel(item.id)"
        >
          <div class="min-w-0">
            <div class="flex flex-wrap items-center gap-2">
              <span class="truncate font-mono text-[14px] font-semibold text-[var(--color-text)]">{{ item.id }}</span>
              <StatusTag v-if="item.private" variant="pending">Private</StatusTag>
              <StatusTag v-if="item.gated" variant="pending">Gated</StatusTag>
              <StatusTag variant="neutral">GGUF</StatusTag>
            </div>
            <div class="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-[12px] text-[var(--neutral-800)]">
              <span v-if="item.author">Author <span class="font-mono text-[var(--neutral-900)]">{{ item.author }}</span></span>
              <span v-if="item.parameter_count" class="font-mono tabular-nums">{{ formatParameters(item.parameter_count) }}</span>
              <span class="font-mono tabular-nums">↓ {{ item.downloads.toLocaleString() }}</span>
              <span class="font-mono tabular-nums">♡ {{ item.likes.toLocaleString() }}</span>
              <span v-if="formatUpdated(item.last_modified)" class="font-mono tabular-nums">{{ formatUpdated(item.last_modified) }}</span>
            </div>
          </div>
          <AppButton intent="ghost" size="sm" @click.stop="goToModel(item.id)">View artifacts</AppButton>
        </div>
      </div>

      <div v-if="nextCursor || loadingMore" ref="loadMoreSentinel" class="flex min-h-14 items-center justify-center py-2 text-sm text-[var(--neutral-800)]" data-testid="discover-load-more-sentinel">
        <span v-if="loadingMore" class="flex items-center gap-2"><UIcon name="i-lucide-loader-circle" class="size-4 animate-spin" />Loading more models…</span>
      </div>
    </template>

    <div v-if="isDetail && detailLoading" class="space-y-3" data-testid="discover-detail-loading">
      <USkeleton class="h-24 w-full" />
      <USkeleton class="h-52 w-full" />
    </div>

    <Frame v-else-if="isDetail && error && !selected" class="border-[var(--accent-800)] p-5" data-testid="discover-detail-error">
      <div class="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
        <div class="min-w-0">
          <StatusTag variant="failed">Repository unavailable</StatusTag>
          <p class="mt-3 break-all font-mono text-[13px] font-semibold text-[var(--color-text)]">{{ repoID }}</p>
          <p class="mt-2 text-sm leading-6 text-[var(--neutral-800)]">{{ error }}</p>
        </div>
        <div class="flex w-full flex-wrap justify-start gap-2 sm:w-auto sm:shrink-0 sm:justify-end">
          <AppButton data-testid="discover-detail-back" intent="secondary" icon="i-lucide-arrow-left" @click="backToResults">Back to Discover</AppButton>
          <AppButton data-testid="discover-detail-retry" intent="primary" :loading="detailLoading" @click="openModel(repoID)">Retry</AppButton>
        </div>
      </div>
    </Frame>

    <template v-else-if="isDetail && selected">
      <div class="flex flex-wrap items-start justify-between gap-4">
        <UPageHeader
          class="min-w-0 flex-1"
          headline="HUGGING FACE"
          :title="selected.id"
          :description="selected.description || 'GGUF repository metadata, hardware guidance and downloadable artifacts.'"
        />
        <div class="flex w-full flex-wrap justify-start gap-2 sm:w-auto sm:shrink-0 sm:justify-end" data-testid="discover-detail-actions">
          <AppButton intent="secondary" icon="i-lucide-arrow-left" @click="backToResults">Back to Discover</AppButton>
          <AppButton intent="secondary" to="/downloads">Downloads</AppButton>
        </div>
      </div>

      <ModelsDiscoverRepositoryHeader :model="selected" :recommendations="recommendations" />

      <Frame v-if="selected.gated" class="border-[var(--accent-700)] p-4 text-sm">
        <p class="font-semibold text-[var(--color-text)]">Access may require approval</p>
        <p class="mt-1 text-[var(--neutral-800)]">A configured Hugging Face token is used only for Hugging Face requests. The manager does not accept licenses or request gated access on your behalf.</p>
      </Frame>

      <Frame class="p-5" data-testid="discover-context-section">
        <div class="flex flex-wrap items-start justify-between gap-4">
          <div>
            <p class="text-[10px] font-semibold uppercase tracking-[.1em] text-[var(--neutral-700)]">CONTEXT + RECOMMENDATION</p>
            <div class="mt-1 flex flex-wrap items-center gap-2">
              <h2 class="font-heading text-xl font-semibold text-[var(--color-text)]">Hardware guidance</h2>
              <StatusTag v-if="recommendations" :variant="recommendations.hardware_available ? 'ready' : 'neutral'">{{ recommendations.hardware_available ? 'Hardware aware' : 'Telemetry limited' }}</StatusTag>
              <StatusTag v-if="recommendationLoading" variant="pending">Recalculating</StatusTag>
            </div>
          </div>
          <div v-if="recommendations" class="text-right">
            <p class="text-[10px] uppercase tracking-[.08em] text-[var(--neutral-700)]">Selected context</p>
            <p class="mt-1 font-mono text-lg font-semibold tabular-nums text-[var(--color-text)]">{{ formatContext(selectedContext) }}</p>
          </div>
        </div>

        <div v-if="recommendations" class="mt-5" data-testid="discover-context-control">
          <div class="flex flex-wrap items-end justify-between gap-3">
            <div>
              <h3 class="text-sm font-semibold text-[var(--color-text)]">Context size</h3>
              <p class="mt-1 max-w-3xl text-sm text-[var(--neutral-800)]">Larger context uses more KV-cache memory and can change both hardware fit and estimated generation speed.</p>
            </div>
            <span v-if="recommendations.context_capability" class="font-mono text-xs tabular-nums text-[var(--neutral-800)]">Capability {{ formatContext(recommendations.context_capability) }}</span>
          </div>
          <USlider :model-value="contextIndex" :min="0" :max="Math.max(0, contextOptions.length - 1)" :step="1" class="mt-4" data-testid="discover-context-slider" @update:model-value="selectContext" />
          <div class="mt-2 flex justify-between font-mono text-xs tabular-nums text-[var(--neutral-700)]"><span>{{ formatContext(contextOptions[0] || 4096) }}</span><span>{{ formatContext(contextOptions[contextOptions.length - 1] || 4096) }}</span></div>
        </div>

        <div v-if="recommendations?.context_assumed && !contextExplicit" class="mt-4 border-l-2 border-[var(--color-accent)] pl-3 text-sm">
          <p class="font-semibold text-[var(--color-text)]">Temporary context assumption</p>
          <p class="mt-1 text-[var(--neutral-800)]">No context size is configured for this Discover choice yet. Hardware recommendations are using a temporary 4K context assumption. Choose a context size above to make the estimate explicit.</p>
        </div>
        <div v-if="recommendationError" class="mt-4 border-l-2 border-[var(--accent-800)] pl-3 text-sm">
          <p class="font-semibold text-[var(--color-text)]">Hardware guidance unavailable</p>
          <p class="mt-1 text-[var(--neutral-800)]">{{ recommendationError }}</p>
        </div>
        <div v-else-if="recommendations && !recommendations.hardware_available" class="mt-4 border-l-2 border-[var(--color-divider)] pl-3 text-sm">
          <p class="font-semibold text-[var(--color-text)]">Hardware-aware recommendation unavailable</p>
          <p class="mt-1 text-[var(--neutral-800)]">Quantization guidance is still shown, but this manager does not currently have enough hardware telemetry to claim which option will fit.</p>
        </div>
        <div v-if="recommendations?.metadata_warning" class="mt-4 border-l-2 border-[var(--color-accent)] pl-3 text-sm">
          <p class="font-semibold text-[var(--color-text)]">Limited GGUF metadata</p>
          <p class="mt-1 text-[var(--neutral-800)]">{{ recommendations.metadata_warning }}</p>
        </div>
      </Frame>

      <div class="space-y-3" data-testid="discover-artifacts">
        <div class="flex flex-wrap items-end justify-between gap-3">
          <div>
            <p class="text-[10px] font-semibold uppercase tracking-[.1em] text-[var(--neutral-700)]">GGUF ARTIFACTS</p>
            <h2 class="mt-1 font-heading text-xl font-semibold text-[var(--color-text)]">Choose a quantization</h2>
            <p class="mt-1 max-w-4xl text-sm text-[var(--neutral-800)]">Quality, memory use, estimated generation speed and runtime placement stay separate from the raw quantization label.</p>
          </div>
          <p class="text-xs text-[var(--neutral-700)]">Vision projectors and MTP draft GGUFs are attached automatically when detected.</p>
        </div>

        <Frame v-if="!selected.artifacts.length" class="p-6 text-sm text-[var(--neutral-800)]">
          No GGUF model files found
        </Frame>

        <Frame
          v-for="artifact in orderedArtifacts"
          v-else
          :key="artifact.id"
          class="p-5"
          :class="artifactAdvice(artifact)?.recommended ? 'border-[var(--color-accent)]' : ''"
          :data-testid="`artifact-${artifact.id}`"
          :data-recommended="artifactAdvice(artifact)?.recommended ? 'true' : undefined"
        >
          <div class="grid gap-5 xl:grid-cols-[minmax(0,1fr)_160px] xl:items-start">
            <div class="min-w-0">
              <div class="flex flex-wrap items-center gap-2">
                <StatusTag v-if="artifactAdvice(artifact)?.recommended" variant="ready" data-testid="recommended-badge">Recommended</StatusTag>
                <span class="text-base font-semibold text-[var(--color-text)]">{{ artifactAdvice(artifact)?.quantization.tier || 'Quantization details unavailable' }}</span>
                <StatusTag variant="neutral"><span class="font-mono">{{ artifact.quantization || 'Unknown' }}</span></StatusTag>
                <StatusTag v-if="artifact.shard_count > 1" variant="neutral">{{ artifact.shard_count }} shards</StatusTag>
                <StatusTag v-if="!artifact.complete" variant="failed">Incomplete split</StatusTag>
              </div>
              <p class="mt-2 truncate font-mono text-xs tabular-nums text-[var(--neutral-800)]">{{ artifact.name }} · {{ formatBytes(artifact.total_bytes) }}</p>

              <dl v-if="artifactAdvice(artifact)" class="mt-4 grid gap-4 text-sm sm:grid-cols-2 lg:grid-cols-5">
                <div><dt class="text-[10px] font-medium uppercase tracking-[.08em] text-[var(--neutral-700)]">Quality</dt><dd class="mt-1 font-semibold">{{ artifactAdvice(artifact)!.quantization.quality }}</dd></div>
                <div><dt class="text-[10px] font-medium uppercase tracking-[.08em] text-[var(--neutral-700)]">Memory</dt><dd class="mt-1 font-semibold">{{ artifactAdvice(artifact)!.quantization.memory }}</dd></div>
                <div>
                  <dt class="text-[10px] font-medium uppercase tracking-[.08em] text-[var(--neutral-700)]">Estimated Generation</dt>
                  <dd class="mt-1 font-semibold"><UTooltip :text="speedReason(artifactAdvice(artifact))"><span data-testid="artifact-generation-speed">{{ speedLabel(artifactAdvice(artifact)) }}</span></UTooltip></dd>
                </div>
                <div>
                  <dt class="text-[10px] font-medium uppercase tracking-[.08em] text-[var(--neutral-700)]">Hardware</dt>
                  <dd class="mt-1"><UTooltip :text="fitReason(artifactAdvice(artifact))"><StatusTag :variant="artifactAdvice(artifact)!.fit === 'gpu' || artifactAdvice(artifact)!.fit === 'multi_gpu' ? 'ready' : artifactAdvice(artifact)!.fit === 'no_fit' ? 'failed' : artifactAdvice(artifact)!.fit === 'hybrid' ? 'pending' : 'neutral'" data-testid="artifact-hardware-fit">{{ fitLabel(artifactAdvice(artifact)) }}</StatusTag></UTooltip></dd>
                </div>
                <div><dt class="text-[10px] font-medium uppercase tracking-[.08em] text-[var(--neutral-700)]">Context</dt><dd class="mt-1 font-mono font-semibold tabular-nums">{{ formatContext(recommendations?.context_length || selectedContext) }}</dd></div>
              </dl>
              <div v-else class="mt-4"><StatusTag variant="neutral" data-testid="artifact-hardware-fit">Fit unknown</StatusTag></div>

              <p v-if="artifactAdvice(artifact)" class="mt-3 text-sm text-[var(--neutral-800)]">{{ artifactAdvice(artifact)!.quantization.summary }}</p>
              <div v-if="artifactAdvice(artifact)?.warnings?.length" class="mt-3 space-y-1 text-sm text-[var(--accent-900)]">
                <p v-for="warning in artifactAdvice(artifact)!.warnings" :key="warning" class="flex gap-2"><UIcon name="i-lucide-triangle-alert" class="mt-0.5 size-4 shrink-0" /><span>{{ warning }}</span></p>
              </div>

              <div v-if="artifact.dependencies?.length" data-testid="artifact-dependencies" class="mt-4 border-l border-[var(--color-divider)] pl-4">
                <p class="mb-2 text-[10px] font-medium uppercase tracking-[.08em] text-[var(--neutral-700)]">Companion files</p>
                <div v-for="dependency in artifact.dependencies" :key="`${dependency.kind}-${dependency.name}`" class="grid gap-1 border-t border-[var(--color-divider)] py-2 text-xs first:border-t-0 sm:grid-cols-[160px_minmax(0,1fr)_auto_auto] sm:items-center">
                  <StatusTag variant="neutral">{{ dependencyLabel(dependency.kind) }}</StatusTag>
                  <span class="min-w-0 truncate font-mono text-[var(--neutral-900)]">{{ dependency.name }}</span>
                  <span v-if="dependency.quantization" class="font-mono text-[var(--neutral-800)]">{{ dependency.quantization }}</span>
                  <span class="font-mono tabular-nums text-[var(--neutral-800)]">{{ formatBytes(dependency.total_bytes) }}</span>
                </div>
              </div>

              <UCollapsible v-if="artifactAdvice(artifact)" class="mt-4">
                <AppButton intent="ghost" size="xs" trailing-icon="i-lucide-chevron-down" class="px-0">Advanced details</AppButton>
                <template #content>
                  <dl class="mt-3 grid gap-x-6 gap-y-3 border-l border-[var(--color-divider)] pl-4 text-xs sm:grid-cols-2 lg:grid-cols-3">
                    <div><dt class="text-[var(--neutral-700)]">Raw quantization</dt><dd class="mt-0.5 font-mono">{{ artifact.quantization || 'Unknown' }}</dd></div>
                    <div><dt class="text-[var(--neutral-700)]">Context used</dt><dd class="mt-0.5 font-mono tabular-nums">{{ formatContext(recommendations?.context_length || selectedContext) }}</dd></div>
                    <div><dt class="text-[var(--neutral-700)]">Confidence</dt><dd class="mt-0.5 capitalize">{{ artifactAdvice(artifact)!.confidence }}</dd></div>
                    <div><dt class="text-[var(--neutral-700)]">Placement</dt><dd class="mt-0.5">{{ artifactAdvice(artifact)!.offload.mode || 'Unknown' }}</dd></div>
                    <div><dt class="text-[var(--neutral-700)]">GPU layers</dt><dd class="mt-0.5 font-mono tabular-nums">{{ artifactAdvice(artifact)!.offload.gpu_layers || '—' }}</dd></div>
                    <div><dt class="text-[var(--neutral-700)]">KV placement</dt><dd class="mt-0.5">{{ artifactAdvice(artifact)!.offload.kv_on_gpu ? 'GPU' : artifactAdvice(artifact)!.offload.mode ? 'System RAM' : 'Unknown' }}</dd></div>
                    <div v-if="artifactAdvice(artifact)!.offload.devices?.length"><dt class="text-[var(--neutral-700)]">Devices</dt><dd class="mt-0.5 font-mono">{{ artifactAdvice(artifact)!.offload.devices!.join(', ') }}</dd></div>
                    <div v-if="artifactAdvice(artifact)!.offload.tensor_split"><dt class="text-[var(--neutral-700)]">Tensor split</dt><dd class="mt-0.5 font-mono">{{ artifactAdvice(artifact)!.offload.tensor_split }}</dd></div>
                    <div><dt class="text-[var(--neutral-700)]">Estimated weights</dt><dd class="mt-0.5 font-mono tabular-nums">{{ formatBytes(artifactAdvice(artifact)!.memory.weights_bytes) }}</dd></div>
                    <div><dt class="text-[var(--neutral-700)]">Estimated KV cache</dt><dd class="mt-0.5 font-mono tabular-nums">{{ formatBytes(artifactAdvice(artifact)!.memory.kv_cache_bytes) }}</dd></div>
                    <div><dt class="text-[var(--neutral-700)]">Estimated Generation</dt><dd class="mt-0.5">{{ speedLabel(artifactAdvice(artifact)) }}</dd></div>
                    <div class="sm:col-span-2 lg:col-span-3"><dt class="text-[var(--neutral-700)]">Generation estimate basis</dt><dd class="mt-0.5">{{ speedReason(artifactAdvice(artifact)) }}</dd></div>
                    <div class="sm:col-span-2 lg:col-span-3"><dt class="text-[var(--neutral-700)]">Technical reason</dt><dd class="mt-0.5">{{ fitReason(artifactAdvice(artifact)) }}</dd></div>
                  </dl>
                </template>
              </UCollapsible>
            </div>

            <div class="flex min-w-32 flex-col gap-2">
              <AppButton intent="primary" :to="launchTo(artifact)" :disabled="!artifact.complete" icon="i-lucide-play">Launch</AppButton>
              <AppButton intent="secondary" :disabled="!artifact.complete || isDownloading(artifact.id)" :loading="isDownloading(artifact.id)" @click="download(artifact)">Download</AppButton>
            </div>
          </div>
        </Frame>
      </div>
    </template>
  </div>
</template>
