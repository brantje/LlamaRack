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
  <div class="space-y-5">
    <UPageHeader headline="HUGGING FACE" title="Discover" description="Search GGUF repositories, compare quantizations in plain language and choose the best fit for this manager." />

    <UCard>
      <UForm :state="{}" class="grid gap-3 lg:grid-cols-[minmax(0,1fr)_220px_220px_auto]" @submit="search">
        <UFormField label="Search" name="search"><UInput v-model="query" class="w-full" placeholder="Qwen, Llama, Gemma… or Hugging Face URL" icon="i-lucide-search" /></UFormField>
        <UFormField label="Author / organization" name="author"><UInput v-model="author" class="w-full" placeholder="Optional" /></UFormField>
        <UFormField label="Sort" name="sort"><USelect v-model="sort" class="w-full" :items="sortOptions" value-key="value" /></UFormField>
        <div class="flex items-end"><UButton class="w-full justify-center" type="submit" :loading="loading">Search</UButton></div>
      </UForm>
    </UCard>

    <UAlert v-if="error" color="error" variant="subtle" :description="error" />
    <UAlert v-if="downloadNotice" color="success" variant="subtle" :description="downloadNotice" />
    <div v-if="loading && !results.length && !isDetail" class="grid gap-3 xl:grid-cols-2"><USkeleton v-for="n in 6" :key="n" class="h-36 w-full rounded-xl" /></div>
    <UEmpty v-else-if="!isDetail && !results.length" icon="i-lucide-search" title="Search Hugging Face" description="Only repositories tagged for GGUF are returned. Results load automatically and update as you search." />

    <template v-else-if="!isDetail">
      <div class="grid gap-3 xl:grid-cols-2">
        <UCard v-for="item in results" :key="item.id" class="cursor-pointer transition hover:ring-1 hover:ring-primary" @click="goToModel(item.id)">
          <div class="flex items-start justify-between gap-4">
            <div class="min-w-0">
              <h2 class="truncate text-base font-bold text-highlighted">{{ item.id }}</h2>
              <div class="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-sm text-muted">
                <span v-if="item.parameter_count">Model size {{ formatParameters(item.parameter_count) }}</span>
                <span v-if="formatUpdated(item.last_modified)">{{ formatUpdated(item.last_modified) }}</span>
                <span>↓ {{ item.downloads.toLocaleString() }}</span>
                <span>♡ {{ item.likes.toLocaleString() }}</span>
              </div>
            </div>
            <div class="flex gap-2"><UBadge v-if="item.private" color="warning" variant="subtle">Private</UBadge><UBadge v-if="item.gated" color="warning" variant="subtle">Gated</UBadge><UBadge color="primary" variant="subtle">GGUF</UBadge></div>
          </div>
          <div class="mt-4 flex flex-wrap gap-1.5"><UBadge v-for="tag in (item.tags || []).slice(0, 5)" :key="tag" color="neutral" variant="soft">{{ tag }}</UBadge></div>
        </UCard>
      </div>
      <div v-if="nextCursor || loadingMore" ref="loadMoreSentinel" class="flex min-h-14 items-center justify-center py-2 text-sm text-muted" data-testid="discover-load-more-sentinel">
        <span v-if="loadingMore" class="flex items-center gap-2"><UIcon name="i-lucide-loader-circle" class="size-4 animate-spin" />Loading more models…</span>
      </div>
    </template>

    <div v-if="isDetail && detailLoading" class="space-y-3"><USkeleton class="h-32 w-full rounded-xl" /><USkeleton class="h-64 w-full rounded-xl" /></div>
    <template v-else-if="isDetail && selected">
      <UButton color="neutral" variant="soft" icon="i-lucide-arrow-left" @click="backToResults">Back to results</UButton>
      <UCard>
        <div class="flex flex-wrap items-start justify-between gap-4">
          <div>
            <p class="mb-1 text-xs font-extrabold tracking-[0.18em] text-dimmed">REPOSITORY</p>
            <h2 class="text-2xl font-bold">{{ selected.id }}</h2>
            <div v-if="selected.parameter_count" class="mt-1 text-sm text-muted">Hugging Face GGUF metadata: {{ formatParameters(selected.parameter_count) }}</div>
            <p v-if="selected.description" class="mt-3 max-w-4xl whitespace-pre-line text-sm leading-6 text-muted">{{ selected.description }}</p>
          </div>
          <div class="flex gap-2"><UBadge v-if="selected.private" color="warning">Private</UBadge><UBadge v-if="selected.gated" color="warning">Gated</UBadge></div>
        </div>
      </UCard>

      <UAlert v-if="selected.gated" color="warning" variant="subtle" title="Access may require approval" description="A configured Hugging Face token is used only for Hugging Face requests. The manager does not accept licenses or request gated access on your behalf." />
      <UAlert v-if="recommendationError" color="warning" variant="subtle" title="Hardware guidance unavailable" :description="recommendationError" />
      <UAlert v-else-if="recommendations && !recommendations.hardware_available" color="neutral" variant="subtle" title="Hardware-aware recommendation unavailable" description="Quantization guidance is still shown, but this manager does not currently have enough hardware telemetry to claim which option will fit." />

      <UCard>
        <div class="space-y-5">
          <div>
            <p class="mb-1 text-xs font-extrabold tracking-[0.18em] text-dimmed">GGUF ARTIFACTS</p>
            <h2 class="text-xl font-bold">Choose a quantization</h2>
            <p class="mt-1 max-w-4xl text-sm text-muted">Start with the Recommended option when available. Quality, memory use, estimated generation speed and runtime placement are shown separately so the raw quantization label does not have to carry the explanation.</p>
          </div>

          <div v-if="recommendations" class="border-y border-default py-4" data-testid="discover-context-control">
            <div class="flex flex-wrap items-start justify-between gap-3">
              <div>
                <div class="flex items-center gap-2">
                  <h3 class="font-semibold">Context size</h3>
                  <UBadge color="neutral" variant="soft">{{ formatContext(selectedContext) }}</UBadge>
                  <span v-if="recommendationLoading" class="flex items-center gap-1 text-xs text-muted"><UIcon name="i-lucide-loader-circle" class="size-3.5 animate-spin" />Recalculating</span>
                </div>
                <p class="mt-1 text-sm text-muted">Larger context uses more KV-cache memory and can change both hardware fit and estimated generation speed.</p>
              </div>
              <span v-if="recommendations.context_capability" class="text-xs text-muted">Detected capability: {{ formatContext(recommendations.context_capability) }}</span>
            </div>
            <USlider :model-value="contextIndex" :min="0" :max="Math.max(0, contextOptions.length - 1)" :step="1" class="mt-4" data-testid="discover-context-slider" @update:model-value="selectContext" />
            <div class="mt-2 flex justify-between text-xs text-dimmed"><span>{{ formatContext(contextOptions[0] || 4096) }}</span><span>{{ formatContext(contextOptions[contextOptions.length - 1] || 4096) }}</span></div>
            <UAlert v-if="recommendations.context_assumed && !contextExplicit" class="mt-4" color="warning" variant="subtle" title="Temporary context assumption" description="No context size is configured for this Discover choice yet. Hardware recommendations are using a temporary 4K context assumption. Choose a context size above to make the estimate explicit." />
            <UAlert v-if="recommendations.metadata_warning" class="mt-4" color="warning" variant="subtle" title="Limited GGUF metadata" :description="recommendations.metadata_warning" />
          </div>

          <p class="text-sm text-muted">Matching vision projectors and MTP draft GGUFs are detected automatically and included with the selected model.</p>
        </div>

        <div v-if="!selected.artifacts.length" class="flex items-center gap-3 py-8 text-muted"><UIcon name="i-lucide-file-x" class="size-5" /><span>No GGUF model files found</span></div>
        <div v-else class="mt-5 divide-y divide-default">
          <div v-for="artifact in orderedArtifacts" :key="artifact.id" class="grid gap-4 py-5 xl:grid-cols-[minmax(0,1fr)_150px] xl:items-start" :data-testid="`artifact-${artifact.id}`">
            <div class="min-w-0">
              <div class="flex flex-wrap items-center gap-2">
                <UBadge v-if="artifactAdvice(artifact)?.recommended" color="primary" variant="solid">Recommended</UBadge>
                <span class="text-base font-semibold">{{ artifactAdvice(artifact)?.quantization.tier || 'Quantization details unavailable' }}</span>
                <UBadge color="neutral" variant="soft" class="font-mono">{{ artifact.quantization || 'Unknown' }}</UBadge>
                <UBadge v-if="artifact.shard_count > 1" color="neutral" variant="soft">{{ artifact.shard_count }} shards</UBadge>
                <UBadge v-if="!artifact.complete" color="error" variant="subtle">Incomplete split</UBadge>
              </div>
              <p class="mt-1 truncate text-sm text-muted">{{ artifact.name }} · {{ formatBytes(artifact.total_bytes) }}</p>

              <dl v-if="artifactAdvice(artifact)" class="mt-4 grid gap-3 text-sm sm:grid-cols-2 lg:grid-cols-4">
                <div><dt class="text-xs font-medium uppercase tracking-wide text-dimmed">Quality</dt><dd class="mt-1 font-semibold">{{ artifactAdvice(artifact)!.quantization.quality }}</dd></div>
                <div><dt class="text-xs font-medium uppercase tracking-wide text-dimmed">Memory</dt><dd class="mt-1 font-semibold">{{ artifactAdvice(artifact)!.quantization.memory }}</dd></div>
                <div>
                  <dt class="text-xs font-medium uppercase tracking-wide text-dimmed">Estimated Generation</dt>
                  <dd class="mt-1 font-semibold">
                    <UTooltip :text="speedReason(artifactAdvice(artifact))">
                      <span data-testid="artifact-generation-speed">{{ speedLabel(artifactAdvice(artifact)) }}</span>
                    </UTooltip>
                  </dd>
                </div>
                <div>
                  <dt class="text-xs font-medium uppercase tracking-wide text-dimmed">Hardware</dt>
                  <dd class="mt-1">
                    <UTooltip :text="fitReason(artifactAdvice(artifact))">
                      <UBadge :color="fitColor(artifactAdvice(artifact))" variant="subtle" data-testid="artifact-hardware-fit">{{ fitLabel(artifactAdvice(artifact)) }}</UBadge>
                    </UTooltip>
                  </dd>
                </div>
              </dl>
              <div v-else class="mt-4"><UBadge color="neutral" variant="subtle" data-testid="artifact-hardware-fit">Fit unknown</UBadge></div>

              <p v-if="artifactAdvice(artifact)" class="mt-3 text-sm text-muted">{{ artifactAdvice(artifact)!.quantization.summary }}</p>
              <div v-if="artifactAdvice(artifact)?.warnings?.length" class="mt-3 space-y-1 text-sm text-warning">
                <p v-for="warning in artifactAdvice(artifact)!.warnings" :key="warning" class="flex gap-2"><UIcon name="i-lucide-triangle-alert" class="mt-0.5 size-4 shrink-0" /><span>{{ warning }}</span></p>
              </div>

              <div v-if="artifact.dependencies?.length" data-testid="artifact-dependencies" class="mt-3 space-y-1">
                <div v-for="dependency in artifact.dependencies" :key="`${dependency.kind}-${dependency.name}`" class="flex flex-wrap items-center gap-2 text-xs text-muted">
                  <UBadge color="success" variant="subtle">{{ dependencyLabel(dependency.kind) }}</UBadge>
                  <span class="font-mono">{{ dependency.name }}</span>
                  <UBadge v-if="dependency.quantization" color="neutral" variant="soft">{{ dependency.quantization }}</UBadge>
                  <span>{{ formatBytes(dependency.total_bytes) }}</span>
                </div>
              </div>

              <UCollapsible v-if="artifactAdvice(artifact)" class="mt-3">
                <UButton color="neutral" variant="link" size="xs" trailing-icon="i-lucide-chevron-down" class="px-0">Advanced details</UButton>
                <template #content>
                  <dl class="mt-2 grid gap-x-6 gap-y-2 border-l border-default pl-4 text-xs sm:grid-cols-2 lg:grid-cols-3">
                    <div><dt class="text-dimmed">Raw quantization</dt><dd class="mt-0.5 font-mono">{{ artifact.quantization || 'Unknown' }}</dd></div>
                    <div><dt class="text-dimmed">Context used</dt><dd class="mt-0.5">{{ formatContext(recommendations?.context_length || selectedContext) }}</dd></div>
                    <div><dt class="text-dimmed">Confidence</dt><dd class="mt-0.5 capitalize">{{ artifactAdvice(artifact)!.confidence }}</dd></div>
                    <div><dt class="text-dimmed">Placement</dt><dd class="mt-0.5">{{ artifactAdvice(artifact)!.offload.mode || 'Unknown' }}</dd></div>
                    <div><dt class="text-dimmed">GPU layers</dt><dd class="mt-0.5">{{ artifactAdvice(artifact)!.offload.gpu_layers || '—' }}</dd></div>
                    <div><dt class="text-dimmed">KV placement</dt><dd class="mt-0.5">{{ artifactAdvice(artifact)!.offload.kv_on_gpu ? 'GPU' : artifactAdvice(artifact)!.offload.mode ? 'System RAM' : 'Unknown' }}</dd></div>
                    <div v-if="artifactAdvice(artifact)!.offload.devices?.length"><dt class="text-dimmed">Devices</dt><dd class="mt-0.5 font-mono">{{ artifactAdvice(artifact)!.offload.devices!.join(', ') }}</dd></div>
                    <div v-if="artifactAdvice(artifact)!.offload.tensor_split"><dt class="text-dimmed">Tensor split</dt><dd class="mt-0.5 font-mono">{{ artifactAdvice(artifact)!.offload.tensor_split }}</dd></div>
                    <div><dt class="text-dimmed">Estimated weights</dt><dd class="mt-0.5">{{ formatBytes(artifactAdvice(artifact)!.memory.weights_bytes) }}</dd></div>
                    <div><dt class="text-dimmed">Estimated KV cache</dt><dd class="mt-0.5">{{ formatBytes(artifactAdvice(artifact)!.memory.kv_cache_bytes) }}</dd></div>
                    <div><dt class="text-dimmed">Estimated Generation</dt><dd class="mt-0.5">{{ speedLabel(artifactAdvice(artifact)) }}</dd></div>
                    <div class="sm:col-span-2 lg:col-span-3"><dt class="text-dimmed">Generation estimate basis</dt><dd class="mt-0.5">{{ speedReason(artifactAdvice(artifact)) }}</dd></div>
                    <div class="sm:col-span-2 lg:col-span-3"><dt class="text-dimmed">Technical reason</dt><dd class="mt-0.5">{{ fitReason(artifactAdvice(artifact)) }}</dd></div>
                  </dl>
                </template>
              </UCollapsible>
            </div>

            <div class="flex min-w-32 flex-col gap-2">
              <UButton :to="launchTo(artifact)" :disabled="!artifact.complete" icon="i-lucide-play">Launch</UButton>
              <UButton color="neutral" variant="soft" :disabled="!artifact.complete || isDownloading(artifact.id)" :loading="isDownloading(artifact.id)" @click="download(artifact)">Download</UButton>
            </div>
          </div>
        </div>
      </UCard>
    </template>
  </div>
</template>