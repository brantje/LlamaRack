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
type DownloadEvent = {
  type: 'download_snapshot' | 'download' | 'download_deleted' | string
  downloads?: DownloadJob[]
  job?: DownloadJob
  id?: string
}

const manager = useManager()
const jobs = ref<DownloadJob[]>([])
const loading = ref(false)
const actionID = ref('')
const error = ref('')
const liveUpdates = ref(false)
let fallbackTimer: ReturnType<typeof setInterval> | undefined
let reconnectTimer: ReturnType<typeof setTimeout> | undefined
let downloadSocket: WebSocket | null = null
let disposed = false

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

function applyJob(job: DownloadJob) {
  const index = jobs.value.findIndex(item => item.id === job.id)
  if (index === -1) jobs.value = [job, ...jobs.value]
  else {
    const next = [...jobs.value]
    next[index] = job
    jobs.value = next
  }
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

function connectDownloadEvents() {
  if (!import.meta.client || disposed || downloadSocket || typeof WebSocket === 'undefined') return
  let socket: WebSocket
  try {
    socket = new WebSocket(`${manager.apiBase.value.replace(/^http/, 'ws')}/api/v1/downloads/ws`)
  } catch {
    return
  }
  downloadSocket = socket
  socket.onopen = () => {
    if (downloadSocket === socket) liveUpdates.value = true
  }
  socket.onmessage = (event) => {
    let message: DownloadEvent
    try {
      message = JSON.parse(String(event.data)) as DownloadEvent
    } catch {
      return
    }
    if (message.type === 'download_snapshot' && Array.isArray(message.downloads)) {
      jobs.value = message.downloads
    } else if (message.type === 'download' && message.job) {
      applyJob(message.job)
    } else if (message.type === 'download_deleted' && message.id) {
      jobs.value = jobs.value.filter(job => job.id !== message.id)
    }
  }
  socket.onclose = () => {
    if (downloadSocket !== socket) return
    downloadSocket = null
    liveUpdates.value = false
    if (disposed) return
    reconnectTimer = setTimeout(() => {
      reconnectTimer = undefined
      connectDownloadEvents()
    }, 1000)
  }
}

async function cancel(job: DownloadJob) {
  actionID.value = job.id
  error.value = ''
  try {
    await manager.request(`/api/v1/downloads/${encodeURIComponent(job.id)}/cancel`, { method: 'POST' })
    applyJob({ ...job, state: 'CANCELLED', speed_bps: 0 })
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
    const updated = await manager.request<DownloadJob>(`/api/v1/downloads/${encodeURIComponent(job.id)}/retry`, { method: 'POST' })
    if (updated?.id) applyJob(updated)
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to retry download'
  } finally {
    actionID.value = ''
  }
}

async function remove(job: DownloadJob) {
  actionID.value = job.id
  error.value = ''
  try {
    await manager.request(`/api/v1/downloads/${encodeURIComponent(job.id)}`, { method: 'DELETE' })
    jobs.value = jobs.value.filter(item => item.id !== job.id)
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to remove download'
  } finally {
    actionID.value = ''
  }
}

onMounted(() => {
  void refresh()
  connectDownloadEvents()
  fallbackTimer = setInterval(() => {
    if (!liveUpdates.value && jobs.value.some(job => activeStates.has(job.state))) void refresh(true)
  }, 1500)
})

onBeforeUnmount(() => {
  disposed = true
  if (fallbackTimer) clearInterval(fallbackTimer)
  if (reconnectTimer) clearTimeout(reconnectTimer)
  const socket = downloadSocket
  downloadSocket = null
  socket?.close()
})
</script>

<template>
  <div class="space-y-5">
    <div class="flex items-start justify-between gap-6">
      <UPageHeader class="min-w-0 flex-1" headline="MODEL STORAGE" title="Downloads" description="Track Hugging Face GGUF transfers, resume interrupted jobs and keep partial files separate from loadable artifacts." />
      <div class="flex items-center gap-2">
        <UBadge :color="liveUpdates ? 'success' : 'neutral'" variant="subtle">{{ liveUpdates ? 'Live updates' : 'Reconnecting' }}</UBadge>
        <UButton color="neutral" variant="soft" :loading="loading" @click="refresh()">Refresh</UButton>
      </div>
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
            <UButton v-if="job.state === 'CANCELLED'" color="neutral" variant="soft" size="sm" :loading="actionID === job.id" @click="remove(job)">Remove</UButton>
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
