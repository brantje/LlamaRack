<script setup lang="ts">
type HFModel = { id: string; author?: string; downloads: number; likes: number; last_modified?: string; parameter_count?: number; tags?: string[]; private: boolean; gated: boolean }
type HFFile = { path: string; size: number; oid?: string }
type HFDependency = { kind: string; name: string; quantization?: string; total_bytes: number; files: HFFile[] }
type HFArtifact = { id: string; name: string; quantization?: string; model_bytes: number; total_bytes: number; shard_count: number; expected_shards: number; complete: boolean; files: HFFile[]; dependencies?: HFDependency[] }
type HFDetail = HFModel & { description?: string; revision: string; artifacts: HFArtifact[] }
type HFSearchPage = { items: HFModel[]; next_cursor?: string }
type GPU = { id: string; name?: string; total_bytes: number; free_bytes: number }
type HardwareSnapshot = { ram_available_bytes?: number; gpus?: GPU[] }
type ArtifactFit = { label: string; detail: string; color: 'success' | 'warning' | 'neutral' }

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
const hardware = ref<HardwareSnapshot | null>(null)
const loading = ref(false)
const loadingMore = ref(false)
const detailLoading = ref(false)
const error = ref('')
const downloading = ref<string[]>([])
const downloadNotice = ref('')
const loadMoreSentinel = ref<HTMLElement | null>(null)
let debounceTimer: ReturnType<typeof setTimeout> | undefined
let searchVersion = 0
let loadObserver: IntersectionObserver | undefined

const repoID = computed(() => String(props.repoId || '').trim())
const isDetail = computed(() => Boolean(repoID.value))
const vramReserve = 512 * 1024 ** 2
const pageSize = 30
const sortOptions = [
  { label: 'Trending', value: 'trending_score' },
  { label: 'Most likes', value: 'likes' },
  { label: 'Most downloads', value: 'downloads' },
  { label: 'Recently created', value: 'created_at' },
  { label: 'Recently updated', value: 'last_modified' }
]

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
  return {
    path: '/models/new',
    query: { repo: selected.value.id, artifact: artifact.id }
  }
}
function isHardwareSnapshot(value: unknown): value is HardwareSnapshot {
  return Boolean(value && typeof value === 'object' && !Array.isArray(value))
}
function artifactModelBytes(artifact: HFArtifact) {
  if (artifact.model_bytes > 0) return artifact.model_bytes
  const shards = artifact.files.slice(0, artifact.shard_count).reduce((sum, file) => sum + Math.max(0, file.size || 0), 0)
  return shards || artifact.total_bytes || 0
}
function artifactFit(artifact: HFArtifact): ArtifactFit {
  const bytes = artifactModelBytes(artifact)
  if (!hardware.value || bytes <= 0) {
    return { label: 'Hardware fit unavailable', detail: 'Select Launch for the context-aware RAM/VRAM estimate.', color: 'neutral' }
  }
  const gpus = hardware.value.gpus || []
  if (!gpus.length) {
    return { label: 'CPU only', detail: 'No NVIDIA/ROCm GPU is currently detected.', color: 'neutral' }
  }
  const usable = gpus.map(gpu => ({ id: gpu.id, bytes: Math.max(0, gpu.free_bytes - vramReserve) }))
  const single = [...usable].sort((a, b) => b.bytes - a.bytes)[0]
  if (single && single.bytes >= bytes) {
    return {
      label: 'GPU-only weight fit',
      detail: `Hugging Face reports ${formatBytes(bytes)} of model weights; they fit in current free VRAM on ${single.id}. Context/KV is checked at Launch.`,
      color: 'success'
    }
  }
  const total = usable.reduce((sum, gpu) => sum + gpu.bytes, 0)
  if (total >= bytes) {
    return {
      label: 'GPU-only weights · multi-GPU',
      detail: `The reported ${formatBytes(bytes)} of weights fit across current GPU VRAM. Context/KV may still require RAM.`,
      color: 'success'
    }
  }
  if (total > 0) {
    return {
      label: 'GPU + CPU split likely',
      detail: `The reported ${formatBytes(bytes)} of weights exceed current usable VRAM. Launch computes the exact layer/KV split for the selected context.`,
      color: 'warning'
    }
  }
  return { label: 'CPU only', detail: 'No useful free GPU VRAM is currently available.', color: 'neutral' }
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
  void nextTick(() => {
    requestAnimationFrame(() => window.scrollTo(0, scrollPosition.value))
  })
}

async function openModel(id: string) {
  detailLoading.value = true; error.value = ''; downloadNotice.value = ''
  const summary = results.value.find(item => item.id === id)
  try {
    const [detail, snapshot] = await Promise.all([
      manager.request<HFDetail>(`/api/v1/huggingface/model?repo=${encodeURIComponent(id)}`),
      manager.request<HardwareSnapshot>('/api/v1/hardware').catch(() => null)
    ])
    selected.value = { ...detail, parameter_count: detail.parameter_count || summary?.parameter_count }
    hardware.value = isHardwareSnapshot(snapshot) ? snapshot : null
  } catch (value: any) { error.value = value?.data?.error || value?.message || 'Unable to load repository details' }
  finally { detailLoading.value = false }
}

async function download(artifact: HFArtifact) {
  if (!selected.value || !artifact.complete || isDownloading(artifact.id)) return
  downloading.value = [...downloading.value, artifact.id]; error.value = ''; downloadNotice.value = ''
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
  if (repo && repo !== value.trim()) {
    query.value = repo
    return
  }
  scheduleSearch()
})
watch(author, scheduleSearch)
watch(sort, () => {
  clearDebounce()
  void search()
})
watch(loadMoreSentinel, (element, previous) => {
  if (!loadObserver) return
  if (previous) loadObserver.unobserve(previous)
  if (element) loadObserver.observe(element)
})

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
  loadObserver?.disconnect()
})
</script>

<template>
  <div class="space-y-5">
    <UPageHeader headline="HUGGING FACE" title="Discover" description="Search GGUF repositories, inspect quantizations and download complete artifacts into the local model directory." />
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
      <UCard>
        <div class="mb-4">
          <p class="mb-1 text-xs font-extrabold tracking-[0.18em] text-dimmed">GGUF ARTIFACTS</p>
          <h2 class="text-xl font-bold">Available quantizations</h2>
          <p class="mt-1 text-sm text-muted">Hugging Face GGUF file sizes provide a pre-download weight-fit check against current VRAM. Launch performs the final context/KV-aware GPU-only vs GPU + CPU split estimate.</p>
          <p class="mt-1 text-sm text-muted">Matching vision projectors and MTP draft GGUFs are detected automatically and included with the selected model.</p>
        </div>
        <div v-if="!selected.artifacts.length" class="flex items-center gap-3 py-8 text-muted"><UIcon name="i-lucide-file-x" class="size-5" /><span>No GGUF model files found</span></div>
        <div v-else class="divide-y divide-default">
          <div v-for="artifact in selected.artifacts" :key="artifact.id" class="grid gap-4 py-4 lg:grid-cols-[minmax(0,1fr)_170px_140px_auto] lg:items-center">
            <div class="min-w-0">
              <div class="flex flex-wrap items-center gap-2">
                <span class="truncate font-semibold">{{ artifact.name }}</span>
                <UBadge v-if="artifact.quantization" color="primary" variant="subtle">{{ artifact.quantization }}</UBadge>
                <UBadge v-if="artifact.shard_count > 1" color="neutral" variant="soft">{{ artifact.shard_count }} shards</UBadge>
                <UBadge v-if="!artifact.complete" color="error" variant="subtle">Incomplete split</UBadge>
                <UBadge :color="artifactFit(artifact).color" variant="subtle" data-testid="artifact-hardware-fit">{{ artifactFit(artifact).label }}</UBadge>
              </div>
              <p class="mt-1 truncate font-mono text-xs text-dimmed">{{ artifact.files.slice(0, artifact.shard_count).map(file => file.path).join(', ') }}</p>
              <p class="mt-1 text-xs text-muted">{{ artifactFit(artifact).detail }}</p>
              <div v-if="artifact.dependencies?.length" data-testid="artifact-dependencies" class="mt-2 space-y-1">
                <div v-for="dependency in artifact.dependencies" :key="`${dependency.kind}-${dependency.name}`" class="flex flex-wrap items-center gap-2 text-xs text-muted">
                  <UBadge color="success" variant="subtle">{{ dependencyLabel(dependency.kind) }}</UBadge>
                  <span class="font-mono">{{ dependency.name }}</span>
                  <UBadge v-if="dependency.quantization" color="neutral" variant="soft">{{ dependency.quantization }}</UBadge>
                  <span>{{ formatBytes(dependency.total_bytes) }}</span>
                </div>
              </div>
            </div>
            <div class="text-sm text-muted"><div>{{ formatBytes(artifact.total_bytes) }}</div><div v-if="artifact.dependencies?.length" class="text-xs text-dimmed">Model {{ formatBytes(artifact.model_bytes) }} + helpers</div></div>
            <div class="text-sm text-muted">{{ artifact.complete ? 'Ready to download' : `${artifact.shard_count}/${artifact.expected_shards} shards` }}</div>
            <div class="flex min-w-28 flex-col gap-2">
              <UButton :to="launchTo(artifact)" :disabled="!artifact.complete" icon="i-lucide-play">Launch</UButton>
              <UButton color="neutral" variant="soft" :disabled="!artifact.complete || isDownloading(artifact.id)" :loading="isDownloading(artifact.id)" @click="download(artifact)">Download</UButton>
            </div>
          </div>
        </div>
      </UCard>
    </template>
  </div>
</template>