<script setup lang="ts">
type HFModel = { id: string; author?: string; downloads: number; likes: number; last_modified?: string; tags?: string[]; private: boolean; gated: boolean }
type HFFile = { path: string; size: number; oid?: string }
type HFDependency = { kind: string; name: string; quantization?: string; total_bytes: number; files: HFFile[] }
type HFArtifact = { id: string; name: string; quantization?: string; model_bytes: number; total_bytes: number; shard_count: number; expected_shards: number; complete: boolean; files: HFFile[]; dependencies?: HFDependency[] }
type HFDetail = HFModel & { description?: string; revision: string; artifacts: HFArtifact[] }

const manager = useManager()
const query = ref('')
const author = ref('')
const sort = ref('downloads')
const results = ref<HFModel[]>([])
const selected = ref<HFDetail | null>(null)
const loading = ref(false)
const detailLoading = ref(false)
const error = ref('')
const downloading = ref('')
const downloadNotice = ref('')
const sortOptions = [
  { label: 'Most downloaded', value: 'downloads' },
  { label: 'Most liked', value: 'likes' },
  { label: 'Recently updated', value: 'lastModified' }
]

function formatBytes(value: number) {
  if (!value) return 'Unknown size'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let amount = value
  let index = 0
  while (amount >= 1024 && index < units.length - 1) { amount /= 1024; index++ }
  return `${amount >= 10 || index === 0 ? amount.toFixed(0) : amount.toFixed(1)} ${units[index]}`
}
function dependencyLabel(kind: string) {
  if (kind === 'mmproj') return 'Vision projector'
  if (kind === 'mtp') return 'MTP draft model'
  return kind
}

async function search() {
  loading.value = true; error.value = ''; selected.value = null
  try {
    const params = new URLSearchParams({ q: query.value, author: author.value, sort: sort.value, limit: '30' })
    results.value = await manager.request<HFModel[]>(`/api/v1/huggingface/search?${params.toString()}`) || []
  } catch (value: any) { error.value = value?.data?.error || value?.message || 'Unable to search Hugging Face' }
  finally { loading.value = false }
}

async function openModel(id: string) {
  detailLoading.value = true; error.value = ''; downloadNotice.value = ''
  try { selected.value = await manager.request<HFDetail>(`/api/v1/huggingface/model?repo=${encodeURIComponent(id)}`) }
  catch (value: any) { error.value = value?.data?.error || value?.message || 'Unable to load repository details' }
  finally { detailLoading.value = false }
}

async function download(artifact: HFArtifact) {
  if (!selected.value || !artifact.complete) return
  downloading.value = artifact.id; error.value = ''; downloadNotice.value = ''
  try {
    await manager.request('/api/v1/downloads', { method: 'POST', body: { repo_id: selected.value.id, artifact_id: artifact.id } })
    const helpers = artifact.dependencies?.length || 0
    downloadNotice.value = helpers
      ? `${artifact.name} and ${helpers} detected helper ${helpers === 1 ? 'artifact was' : 'artifacts were'} added to Downloads.`
      : `${artifact.name} was added to Downloads.`
  } catch (value: any) { error.value = value?.data?.error || value?.message || 'Unable to start download' }
  finally { downloading.value = '' }
}
</script>

<template>
  <div class="space-y-5">
    <UPageHeader headline="HUGGING FACE" title="Discover" description="Search GGUF repositories, inspect quantizations and download complete artifacts into the local model directory." />
    <UCard>
      <UForm :state="{}" class="grid gap-3 lg:grid-cols-[minmax(0,1fr)_220px_220px_auto]" @submit="search">
        <UFormField label="Search" name="search"><UInput v-model="query" class="w-full" placeholder="Qwen, Llama, Gemma…" icon="i-lucide-search" /></UFormField>
        <UFormField label="Author / organization" name="author"><UInput v-model="author" class="w-full" placeholder="Optional" /></UFormField>
        <UFormField label="Sort" name="sort"><USelect v-model="sort" class="w-full" :items="sortOptions" value-key="value" /></UFormField>
        <div class="flex items-end"><UButton class="w-full justify-center" type="submit" :loading="loading">Search</UButton></div>
      </UForm>
    </UCard>
    <UAlert v-if="error" color="error" variant="subtle" :description="error" />
    <UAlert v-if="downloadNotice" color="success" variant="subtle" :description="downloadNotice" />
    <div v-if="loading" class="grid gap-3 xl:grid-cols-2"><USkeleton v-for="n in 6" :key="n" class="h-36 w-full rounded-xl" /></div>
    <UEmpty v-else-if="!results.length && !selected" icon="i-lucide-search" title="Search Hugging Face" description="Only repositories tagged for GGUF are returned." />
    <div v-else-if="!selected" class="grid gap-3 xl:grid-cols-2">
      <UCard v-for="item in results" :key="item.id" class="cursor-pointer transition hover:ring-1 hover:ring-primary" @click="openModel(item.id)">
        <div class="flex items-start justify-between gap-4">
          <div class="min-w-0"><h2 class="truncate text-base font-bold text-highlighted">{{ item.id }}</h2><p class="mt-1 text-sm text-muted">{{ item.downloads.toLocaleString() }} downloads · {{ item.likes.toLocaleString() }} likes</p></div>
          <div class="flex gap-2"><UBadge v-if="item.private" color="warning" variant="subtle">Private</UBadge><UBadge v-if="item.gated" color="warning" variant="subtle">Gated</UBadge><UBadge color="primary" variant="subtle">GGUF</UBadge></div>
        </div>
        <div class="mt-4 flex flex-wrap gap-1.5"><UBadge v-for="tag in (item.tags || []).slice(0, 5)" :key="tag" color="neutral" variant="soft">{{ tag }}</UBadge></div>
      </UCard>
    </div>
    <div v-if="detailLoading" class="space-y-3"><USkeleton class="h-32 w-full rounded-xl" /><USkeleton class="h-64 w-full rounded-xl" /></div>
    <template v-else-if="selected">
      <UButton color="neutral" variant="soft" icon="i-lucide-arrow-left" @click="selected = null">Back to results</UButton>
      <UCard>
        <div class="flex flex-wrap items-start justify-between gap-4">
          <div><p class="mb-1 text-xs font-extrabold tracking-[0.18em] text-dimmed">REPOSITORY</p><h2 class="text-2xl font-bold">{{ selected.id }}</h2><p v-if="selected.description" class="mt-3 max-w-4xl whitespace-pre-line text-sm leading-6 text-muted">{{ selected.description }}</p></div>
          <div class="flex gap-2"><UBadge v-if="selected.private" color="warning">Private</UBadge><UBadge v-if="selected.gated" color="warning">Gated</UBadge></div>
        </div>
      </UCard>
      <UAlert v-if="selected.gated" color="warning" variant="subtle" title="Access may require approval" description="A configured Hugging Face token is used only for Hugging Face requests. The manager does not accept licenses or request gated access on your behalf." />
      <UCard>
        <div class="mb-4"><p class="mb-1 text-xs font-extrabold tracking-[0.18em] text-dimmed">GGUF ARTIFACTS</p><h2 class="text-xl font-bold">Available quantizations</h2><p class="mt-1 text-sm text-muted">Matching vision projectors and MTP draft GGUFs are detected automatically and included with the selected model.</p></div>
        <div v-if="!selected.artifacts.length" class="flex items-center gap-3 py-8 text-muted"><UIcon name="i-lucide-file-x" class="size-5" /><span>No GGUF model files found</span></div>
        <div v-else class="divide-y divide-default">
          <div v-for="artifact in selected.artifacts" :key="artifact.id" class="grid gap-4 py-4 lg:grid-cols-[minmax(0,1fr)_170px_140px_auto] lg:items-center">
            <div class="min-w-0">
              <div class="flex flex-wrap items-center gap-2"><span class="truncate font-semibold">{{ artifact.name }}</span><UBadge v-if="artifact.quantization" color="primary" variant="subtle">{{ artifact.quantization }}</UBadge><UBadge v-if="artifact.shard_count > 1" color="neutral" variant="soft">{{ artifact.shard_count }} shards</UBadge><UBadge v-if="!artifact.complete" color="error" variant="subtle">Incomplete split</UBadge></div>
              <p class="mt-1 truncate font-mono text-xs text-dimmed">{{ artifact.files.slice(0, artifact.shard_count).map(file => file.path).join(', ') }}</p>
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
            <UButton :disabled="!artifact.complete" :loading="downloading === artifact.id" @click="download(artifact)">Download</UButton>
          </div>
        </div>
      </UCard>
    </template>
  </div>
</template>
