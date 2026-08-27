<script setup lang="ts">
type DownloadFile = {
  path: string
  size: number
  state: string
  downloaded_bytes: number
  local_path?: string
}
type DownloadJob = {
  id: string
  provider: string
  repo_id: string
  revision: string
  artifact_id: string
  name: string
  quantization?: string
  state: string
  total_bytes: number
  downloaded_bytes: number
  speed_bps: number
  error?: string
  created_at: number
  updated_at: number
  files?: DownloadFile[]
}

const manager = useManager()
const jobs = ref<DownloadJob[]>([])
const loading = ref(false)
const actionID = ref('')
const error = ref('')
let timer: ReturnType<typeof setInterval> | undefined

const activeStates = new Set(['QUEUED', 'RESOLVING', 'DOWNLOADING', 'VERIFYING'])

function formatBytes(value: number) {
  if (!Number.isFinite(value) || value <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let amount = value
  let index = 0
  while (amount >= 1024 && index < units.length - 1) {
    amount /= 1024
    index++
  }
  return `${amount >= 10 || index === 0 ? amount.toFixed(0) : amount.toFixed(1)} ${units[index]}`
}

function progress(job: DownloadJob) {
  if (!job.total_bytes) return 0
  return Math.min(100, Math.round(job.downloaded_bytes / job.total_bytes * 100))
}

function eta(job: DownloadJob) {
  if (!job.speed_bps || !job.total_bytes || job.downloaded_bytes >= job.total_bytes) return ''
  const seconds = Math.ceil((job.total_bytes - job.downloaded_bytes) / job.speed_bps)
  if (seconds < 60) return `${seconds}s remaining`
  if (seconds < 3600) return `${Math.ceil(seconds / 60)}m remaining`
  return `${(seconds / 3600).toFixed(1)}h remaining`
}

function stateColor(state: string) {
  if (state === 'COMPLETED') return 'success'
  if (state === 'FAILED') return 'error'
  if (state === 'CANCELLED') return 'neutral'
  if (state === 'VERIFYING') return 'warning'
  return 'primary'
}

async function refresh(silent = false) {
  if (!silent) loading.value = true
  error.value = ''
  try {
    jobs.value = await manager.request<DownloadJob[]>('/api/v1/downloads') || []
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to load downloads'
  } finally {
    if (!silent) loading.value = false
  }
}

async function cancel(job: DownloadJob) {
  actionID.value = job.id
  error.value = ''
  try {
    await manager.request(`/api/v1/downloads/${encodeURIComponent(job.id)}/cancel`, { method: 'POST' })
    await refresh(true)
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to cancel download'
  } finally {
    actionID.value = ''
  }
}

async function retry(job: DownloadJob) {
  actionID.value = job.id
  error.value = ''
  try {
    await manager.request(`/api/v1/downloads/${encodeURIComponent(job.id)}/retry`, { method: 'POST' })
    await refresh(true)
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to retry download'
  } finally {
    actionID.value = ''
  }
}

onMounted(() => {
  void refresh()
  timer = setInterval(() => {
    if (jobs.value.some(job => activeStates.has(job.state))) void refresh(true)
  }, 1500)
})

onBeforeUnmount(() => {
  if (timer) clearInterval(timer)
})
</script>

<template>
  <div class="space-y-5">
    <div class="flex items-start justify-between gap-6">
      <UPageHeader class="min-w-0 flex-1" headline="MODEL STORAGE" title="Downloads" description="Track Hugging Face GGUF transfers, resume interrupted jobs and keep partial files separate from loadable artifacts." />
      <UButton color="neutral" variant="soft" :loading="loading" @click="refresh()">Refresh</UButton>
    </div>

    <UAlert v-if="error" color="error" variant="subtle" :description="error" />

    <div v-if="loading && !jobs.length" class="space-y-3">
      <USkeleton v-for="n in 4" :key="n" class="h-40 w-full rounded-xl" />
    </div>

    <UEmpty v-else-if="!jobs.length" icon="i-lucide-download" title="No downloads yet" description="Choose a GGUF artifact from Discover to start a download.">
      <template #actions><UButton to="/discover">Open Discover</UButton></template>
    </UEmpty>

    <div v-else class="space-y-3">
      <UCard v-for="job in jobs" :key="job.id">
        <div class="flex flex-wrap items-start justify-between gap-4">
          <div class="min-w-0">
            <div class="flex flex-wrap items-center gap-2">
              <h2 class="truncate text-base font-bold">{{ job.name }}</h2>
              <UBadge :color="stateColor(job.state)" variant="subtle">{{ job.state }}</UBadge>
              <UBadge v-if="job.quantization" color="neutral" variant="soft">{{ job.quantization }}</UBadge>
            </div>
            <p class="mt-1 truncate text-sm text-muted">{{ job.repo_id }}</p>
          </div>
          <div class="flex gap-2">
            <UButton v-if="activeStates.has(job.state)" color="error" variant="soft" size="sm" :loading="actionID === job.id" @click="cancel(job)">Cancel</UButton>
            <UButton v-if="job.state === 'FAILED' || job.state === 'CANCELLED'" color="primary" variant="soft" size="sm" :loading="actionID === job.id" @click="retry(job)">Retry</UButton>
          </div>
        </div>

        <div class="mt-5 space-y-2">
          <UProgress :model-value="progress(job)" />
          <div class="flex flex-wrap justify-between gap-x-4 gap-y-1 text-xs text-muted">
            <span>{{ formatBytes(job.downloaded_bytes) }} / {{ formatBytes(job.total_bytes) }} · {{ progress(job) }}%</span>
            <span v-if="job.speed_bps">{{ formatBytes(job.speed_bps) }}/s<span v-if="eta(job)"> · {{ eta(job) }}</span></span>
          </div>
        </div>

        <UAlert v-if="job.error" class="mt-4" color="error" variant="subtle" title="Download failed" :description="job.error" />

        <UAccordion v-if="job.files?.length" class="mt-3" :items="[{ label: `${job.files.length} file${job.files.length === 1 ? '' : 's'}`, slot: 'files' }]">
          <template #files>
            <div class="divide-y divide-default text-sm">
              <div v-for="file in job.files" :key="file.path" class="grid gap-1 py-2 sm:grid-cols-[minmax(0,1fr)_120px_100px]">
                <code class="truncate font-mono text-xs">{{ file.local_path || file.path }}</code>
                <span class="text-muted">{{ formatBytes(file.downloaded_bytes) }} / {{ formatBytes(file.size) }}</span>
                <span class="text-right text-muted">{{ file.state }}</span>
              </div>
            </div>
          </template>
        </UAccordion>
      </UCard>
    </div>
  </div>
</template>
