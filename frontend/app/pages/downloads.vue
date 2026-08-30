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
const showCompleted = ref(false)
let fallbackTimer: ReturnType<typeof setInterval> | undefined
let reconnectTimer: ReturnType<typeof setTimeout> | undefined
let downloadSocket: WebSocket | null = null
let disposed = false

const activeStates = new Set(['QUEUED', 'RESOLVING', 'DOWNLOADING', 'VERIFYING'])
const completedCount = computed(() => jobs.value.filter(job => job.state === 'COMPLETED').length)
const visibleJobs = computed(() => showCompleted.value ? jobs.value : jobs.value.filter(job => job.state !== 'COMPLETED'))

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
  return Math.min(100, Math.max(0, Math.round(job.downloaded_bytes / job.total_bytes * 100)))
}

function eta(job: DownloadJob) {
  if (!job.speed_bps || !job.total_bytes || job.downloaded_bytes >= job.total_bytes) return ''
  const seconds = Math.ceil((job.total_bytes - job.downloaded_bytes) / job.speed_bps)
  if (seconds < 60) return `${seconds}s remaining`
  if (seconds < 3600) return `${Math.ceil(seconds / 60)}m remaining`
  return `${(seconds / 3600).toFixed(1)}h remaining`
}

function stateVariant(state: string): 'ready' | 'pending' | 'neutral' | 'failed' {
  if (state === 'COMPLETED') return 'ready'
  if (state === 'FAILED') return 'failed'
  if (state === 'CANCELLED') return 'neutral'
  return 'pending'
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
  <div class="space-y-6">
    <div class="flex flex-wrap items-start justify-between gap-5">
      <div class="min-w-0 flex-1">
        <div class="mb-1 text-[10px] font-medium uppercase tracking-[.1em] text-[var(--neutral-700)]">MODEL STORAGE</div>
        <h1 class="font-heading text-[30px] font-semibold leading-none tracking-[-.015em] text-[var(--color-text)]">Downloads</h1>
        <p class="mt-2 max-w-3xl text-[15px] leading-[1.55] text-[var(--neutral-800)]">
          Track Hugging Face GGUF transfers, resume interrupted jobs and keep partial files separate from loadable artifacts.
        </p>
      </div>
      <div class="flex flex-wrap items-center justify-end gap-2">
        <StatusTag :variant="liveUpdates ? 'ready' : 'pending'">{{ liveUpdates ? 'Live updates' : 'Reconnecting' }}</StatusTag>
        <AppButton
          data-testid="toggle-completed-downloads"
          intent="secondary"
          :disabled="completedCount === 0"
          @click="showCompleted = !showCompleted"
        >
          {{ showCompleted ? `Hide completed (${completedCount})` : `Show completed (${completedCount})` }}
        </AppButton>
        <AppButton intent="secondary" :loading="loading" @click="refresh()">Refresh</AppButton>
      </div>
    </div>

    <Frame v-if="error" class="border-[var(--accent-800)] p-4 text-sm text-[var(--accent-900)]">
      {{ error }}
    </Frame>

    <div v-if="loading && !jobs.length" class="space-y-3" data-testid="downloads-loading">
      <USkeleton v-for="n in 4" :key="n" class="h-40 w-full" />
    </div>

    <UEmpty v-else-if="!jobs.length" icon="i-lucide-download" title="No downloads yet" description="Choose a GGUF artifact from Discover to start a download.">
      <template #actions>
        <AppButton to="/models/discover" intent="primary">Open Discover</AppButton>
      </template>
    </UEmpty>

    <UEmpty v-else-if="!visibleJobs.length" icon="i-lucide-circle-check" title="No active downloads" description="Completed downloads are hidden by default.">
      <template #actions>
        <AppButton intent="secondary" @click="showCompleted = true">Show completed ({{ completedCount }})</AppButton>
      </template>
    </UEmpty>

    <div v-else class="space-y-3" data-testid="download-queue">
      <Frame v-for="job in visibleJobs" :key="job.id" class="p-5" data-testid="download-job">
        <div class="flex flex-wrap items-start justify-between gap-4">
          <div class="min-w-0 flex-1">
            <div class="flex flex-wrap items-center gap-2">
              <h2 class="min-w-0 truncate font-heading text-xl font-semibold tracking-[-.015em] text-[var(--color-text)]">{{ job.name }}</h2>
              <StatusTag :variant="stateVariant(job.state)">{{ job.state }}</StatusTag>
              <StatusTag v-if="job.quantization" variant="neutral">{{ job.quantization }}</StatusTag>
            </div>
            <p class="mt-1 truncate font-mono text-[11px] tabular-nums text-[var(--neutral-700)]">{{ job.repo_id }}</p>
          </div>
          <div class="flex flex-wrap items-center justify-end gap-2">
            <AppButton v-if="activeStates.has(job.state)" intent="destructive" size="sm" :loading="actionID === job.id" @click="cancel(job)">Cancel</AppButton>
            <AppButton v-if="job.state === 'FAILED' || job.state === 'CANCELLED'" intent="secondary" size="sm" :loading="actionID === job.id" @click="retry(job)">Retry</AppButton>
            <AppButton v-if="job.state === 'CANCELLED'" intent="ghost" size="sm" :loading="actionID === job.id" @click="remove(job)">Remove</AppButton>
          </div>
        </div>

        <div class="mt-5 space-y-2">
          <div class="h-2 w-full overflow-hidden bg-[var(--neutral-400)]" data-testid="download-progress-track">
            <div
              class="h-full bg-[var(--color-accent)] transition-[width] duration-200"
              data-testid="download-progress-fill"
              :style="{ width: `${progress(job)}%` }"
            />
          </div>
          <div class="flex flex-wrap justify-between gap-x-4 gap-y-1 font-mono text-[11px] tabular-nums text-[var(--neutral-700)]">
            <span>{{ formatBytes(job.downloaded_bytes) }} / {{ formatBytes(job.total_bytes) }} · {{ progress(job) }}%</span>
            <span v-if="job.speed_bps">{{ formatBytes(job.speed_bps) }}/s<span v-if="eta(job)"> · {{ eta(job) }}</span></span>
          </div>
        </div>

        <Frame v-if="job.error" class="mt-4 border-[var(--accent-800)] bg-[var(--neutral-200)] p-3">
          <div class="text-[10px] font-medium uppercase tracking-[.08em] text-[var(--accent-800)]">Download failed</div>
          <p class="mt-1 text-sm text-[var(--color-text)]">{{ job.error }}</p>
        </Frame>

        <UAccordion
          v-if="job.files?.length"
          class="mt-4 border-t border-[var(--color-divider)] pt-1"
          :items="[{ label: `${job.files.length} file${job.files.length === 1 ? '' : 's'}`, slot: 'files' }]"
        >
          <template #files>
            <div class="divide-y divide-[var(--color-divider)] text-sm" data-testid="download-files">
              <div v-for="file in job.files" :key="file.path" class="grid items-center gap-2 py-3 sm:grid-cols-[minmax(0,1fr)_150px_auto]">
                <code class="break-all font-mono text-[11px] tabular-nums text-[var(--color-text)]">{{ file.local_path || file.path }}</code>
                <span class="font-mono text-[11px] tabular-nums text-[var(--neutral-700)]">{{ formatBytes(file.downloaded_bytes) }} / {{ formatBytes(file.size) }}</span>
                <div class="sm:text-right"><StatusTag :variant="stateVariant(file.state)">{{ file.state }}</StatusTag></div>
              </div>
            </div>
          </template>
        </UAccordion>
      </Frame>
    </div>
  </div>
</template>
